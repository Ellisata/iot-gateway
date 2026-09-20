# Protocol Capacity Testing Findings

[English](capacity-testing.en.md) | [简体中文](capacity-testing.md)

> Benchmark tool: `cmd/loadtest` (methodology and full matrix data in [cmd/loadtest/README.en.md](../cmd/loadtest/README.en.md)).
> Environment: 8 cores / Windows / loopback / in-process fake PLC servers, scan=1000ms.
> **Numbers are gateway-side ceilings**; they vary with core count and real device round-trip latency. For field capacity planning, scale via the "frame count model" below — do not copy absolute values.

## Frame count model (capacity estimation formula)

The essence of point capacity: **frames × per-frame round-trip latency < scan interval** (exceeding it skips rounds and drops points — the acquisition engine's in-flight anti-pileup skips polls of devices whose previous cycle hasn't finished).

| Protocol | Per frame | Frames (contiguous/regular layout) | Frames (sparse/standalone points) |
|---|---|---|---|
| Modbus.TCP | 125 registers | ceil(N/125) | N (when gaps > 125) |
| Mitsubishi.MC.TCP | 100 words (maxReadWords up to 960) | ceil(N/100) | N (when gaps > 8) |
| Omron.FINS.TCP | 100 words | ceil(N/100) | N (when gaps > 100) |
| Siemens.S7 | 462 bytes/PDU | ceil(N/462) (byte area, byte-addressed) | N (when gaps > 8) |
| Omron.CIP | 1 tag/round-trip (0x0A batch via maxTagsPerRequest) | N → ceil(N/batch) | N |
| Rockwell.CIP | 125 elements/frame (array tags) | ceil(N/125) per same-cardinality array, summed across cardinalities | N (standalone tags) |
| OPC.UA | maxBatch nodes/ReadRequest (default 100, up to 1000) | ceil(N/maxBatch) (direct NodeID) | Same (browse-path adds 1 Translate round-trip on first use, then caches) |
| DL/T 645 | 12 data identifiers/frame (`maxDIsPerRead`, spec limit; default 1) | **No range-merge concept, layout-independent**: ceil(N/maxDIsPerRead) | Same |

Per-device capacity ceiling ≈ scan interval / per-frame round-trip latency × points per frame; the whole machine is jointly constrained by CPU (decode/format/GC) and the worker pool (NumCPU×2). The measured common throughput ceiling on this machine is about **2M records/s**.

## Rockwell.CIP specifics (2026-08)

### Layout decides capacity

| Layout | Mechanism | Measured (scan=1000ms) |
|---|---|---|
| **Array-regular** (`Base[0..N-1]`) | Sorted by index, split into Read Tag Elements chunks of ≤125 elements | 50 devices × 5000 points = 250k points **0% drop** (293k rec/s), same tier as Modbus; still 0% with RTT 1ms |
| **Standalone tags** (`Tag_i`) | One 0x4C serial round-trip per point (adapter mutex, no multi-service batch) | 50 × 5000 drops 35.2% (cycle 2s); with RTT 1ms even 1 device × 1000 points drops 40% |

**Standalone-tag layout capacity formula**: per device ≈ scan interval / RTT (capped around 800 points/device at 1s / 1ms RTT).

### Production configuration advice

1. **Plan contiguous points as `Base[N..N+k]` array tags wherever possible** — in the same PLC program, move the data to be collected into a contiguous array and point capacity returns to the Modbus tier.
2. When standalone tags are unavoidable, cap points per device (≤500 points/device at 1s / 1ms RTT, leaving headroom) or lower the scan frequency.
3. Array points must share one type with fixed element size (STRING/struct members are not merged and are read per point).

### Fix record

`buildPlan` (`driver/rockwell/cip/range.go`) previously **abandoned merging entirely** for array groups >125 points, degrading to per-point single reads (5000 points = 5000 round-trips/cycle); it now sorts by index and greedily chunks (each chunk ≤125 elements with span ≤125), so 5000 points = 40 frames. Contiguous 50×5000 before → after: 35.2% drop → 0%.

### Fake server vs. real hardware

`testutil/fake/cip.go`'s `NewCIPAB` replies in the AB dialect:
`CC | 00 | status | extended-status-word-count (1) | type code (2) | data`. goindustrial decodes that extended-status-word-count byte strictly per the CIP spec (if missing, the first byte of the type code, 0xC3, is misread as 195 extended status words → EOF). The Omron dialect (`NewCIP`) has no such byte; the two drivers cannot share one fake server. **During live integration, if Rockwell reads fail with EOF-like errors, first check whether the response carries the extended-status-word-count byte.**

## OPC.UA specifics (2026-08)

### Frame count model: MaxBatch bulk read, capacity matches Modbus

The driver resolves points to NodeIDs, then issues ReadRequests in batches of `maxBatch` (configurable in protocol_json, default 100, cap 1000); direct NodeID (`ns=2;i=100`) resolution involves no I/O. The fake server is a gopcua in-process server + Read override handler (returns values by NodeID formula, no pre-built Node objects).

**Contiguous layout (`ns=1;i=<n>`, scan=1000ms)**: whole matrix **0% drop**; 100 devices × 5000 points
= 500k points (5000 frames/device/cycle → 100 frames/device) hit 560k records/s — same tier as the Modbus baseline.

| Devices | Points/dev | Layout | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 5000 | contiguous | 250k | 281k | 0.0 |
| 100 | 5000 | contiguous | 500k | 562k | 0.0 |
| 50 | 5000 | contiguous + RTT 1ms | 250k | 269k | 0.0 (100 frames × 2.4ms ≈ 240ms/cycle, ample headroom) |
| 10 | 5000 | contiguous + RTT 5ms | 50k | 56k | 0.0 (100 frames × 10ms ≈ 1s, edge) |
| 50 | 5000 | contiguous + RTT 5ms | 250k | 97k | **61.1** (cycle 3.5s, see below) |

**Tightened scan frequency (CPU ceiling, not a protocol ceiling)**:

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 5000 | 500ms | 500k | 531k | 0.0 |
| 50 | 5000 | 200ms | 1.25M | 944k | **24.5** |
| 100 | 2000 | 200ms | 1M | 968k | 3.2 (edge) |
| 100 | 5000 | 200ms | 2.5M | 884k | **64.6** |

**maxBatch tuning**: at `maxBatch=500`, 50×5000@200ms improved from 24.5% to 1.0% drop
(frames 10→50 per cycle); 100 devices on the same matrix still dropped 51.3% (CPU saturated — frame count no longer the bottleneck).

### OPC UA-specific caveats

1. **RTT sensitivity = frames × RTT**: at 100 frames/cycle a 5ms RTT approaches the 1s scan interval
   (the main cause of 61.1% drop at 50 devices is polls queueing serially on the single-connection server).
   At RTT 1ms, 50×5000 still drops 0%; in production with 100ms cycles the budget is ≈ scan interval/(2×RTT) frames.
2. **First-use cost of browse-path points**: `Device/Tag_x` addresses require a server
   TranslateBrowsePathsToNodeIds (one round-trip per address); results are cached per connection and
   re-resolved on reconnect. The benchmark did not cover this path (the fake server lacks the Translate service);
   with many browse-path points in the field, first cycles will stretch significantly — prefer direct NodeID addressing.
3. **gopcua client sends/receives serially by requestID**: concurrent Reads on the same device do not parallelize;
   per-device cycle ≈ frames × RTT; different devices share no mutex and can be polled in parallel.
4. **Fake server dispatches on one goroutine**: the gopcua server's monitorConnections processes messages one by one;
   with multiple devices + RTT, transactions queue serially server-side — part of the RTT-heavy matrix drops comes from
   fake-server throughput (real PLC servers usually handle connections concurrently, so field results will beat the benchmark),
   but the "frames × RTT < scan interval" capacity formula still holds.

### Production configuration advice

1. Address with direct NodeIDs (`ns=<n>;<i|s|g|b>=...`) to avoid browse-path first-resolution overhead.
2. Raise `maxBatch` as far as the PLC tolerates (default 100, cap 1000): frame count falls linearly,
   a 1000-point device goes 100 frames → 10 frames. Note some servers impose per-request operation limits
   (BadTooManyOperations); on failure dial it back down (the driver retries in sub-batches).
3. Per-device cycle estimate: `ceil(points/maxBatch) × 2×RTT`, which must stay below the scan interval.

## DL/T 645 specifics (2026-09)

**This is the one protocol where gateway-side compute is not a bottleneck at all** — the limits are the
2400bps physical link and the `interFrameDelayMs` setting, both 5–6 orders of magnitude above gateway overhead.

### Per-frame cost model

```
per-device cycle = ceil(points / maxDIsPerRead) × (inter-frame delay + round-trip)
round-trip       = (request bytes + response bytes) × 11 / baud + meter processing time
```

- **Inter-frame delay** (`interFrameDelayMs`, default **30ms**): RS-485 transceiver turnaround + meter
  processing. **It is charged per frame and is the hard per-device throughput ceiling: 1000/delay frames
  per second, independent of link speed and of how many devices run concurrently** (measured at 1/2/5/10
  concurrent devices: a constant 32 frames/s per device). The 30ms default gives 33 frames/s.
- **No range merging**: 645 read commands address one data identifier at a time; adjacent identifiers are
  never coalesced. **Batched reads (`maxDIsPerRead`) are therefore the only frame-count lever**, capped at 12 by the spec.
- Frame size grows with the batch: `maxDIsPerRead=1` is 16B request + 20B response; `=12` is 72B + 108B
  (each identifier carries 8 bytes of data field).

### Measured: point capacity gain from batching = batch size (exactly linear)

Single device, scan=1000ms, default 30ms inter-frame delay (loopback, to remove link latency from the picture):

| maxDIsPerRead | Points | Frames | Frames/s | Cycle | drop% |
|---|---|---|---|---|---|
| 1 (default) | 20 | 20 | 20 | 1s | 0.0 |
| 1 | 50 | 50 | 25 | 2s | **50.0** |
| 1 | 100 | 100 | 25 | 4s | **75.0** |
| 12 | 240 | 20 | 20 | 1s | 0.0 |
| 12 | 600 | 50 | 25 | 2s | **50.0** |
| 12 | 1200 | 100 | 25 | 4s | **75.0** |

**At the same frame count, batching carries 12× the points**: 240 batched points and 20 unbundled points
show identical cycle, frame rate and drop. The default `maxDIsPerRead=1` means frames = points, which is
the root cause of "one meter yields only a handful of points" in the field.

### Measured: real-link simulation (2400bps / 8E1)

`-latency` injects the round-trip from the formula above (165ms for 1 identifier, 825ms for 12):

| maxDIsPerRead | Round-trip | Points | Points/s | drop% |
|---|---|---|---|---|
| 1 | 165ms | 5 | 5 | 0.0 |
| 1 | 165ms | 10 | 5 | **50.0** |
| 12 | 825ms | 12 | 12 | 0.0 |
| 12 | 825ms | 24 | 12 | **50.0** |

**At 2400bps a single meter tops out at 12 points/s** (1s scan), even with batching maxed out. At 9600bps
it scales proportionally to roughly 48 points/s.

### Measured: gateway-side ceiling (inter-frame delay disabled)

`-dlt645-interframe 0`, maxDIsPerRead=12, ~1.2ms per frame:

| Devices | Points/device | Frames/s | Records/s | drop% |
|---|---|---|---|---|
| 10 | 2000 | 1670 | 20000 | 0.0 |
| 10 | 5000 | 4179 | 50000 | 0.0 |
| 50 | 2000 | 8347 | 100000 | 0.0 |
| 50 | 5000 | 17270 | 205000 | **18.0** |
| 100 | 2000 | 16699 | 200000 | 0.0 |
| 100 | 5000 | 24637 | 298000 | **40.4** |

The gateway side tops out around **25k frames/s / 300k records/s** (CPU ceiling, on par with the other protocols).

### Micro-benchmarks (driver/dlt645, 16 threads)

| Stage | Cost | Allocations |
|---|---|---|
| Request framing | 45ns (1 id) / 149ns (12 ids) | 2 / 3 allocs |
| Response parse (incl. checksum) | 79ns | 3 allocs |
| Read response (whole frame / 1-byte chunks) | 239ns / 508ns | 7 / 8 allocs |
| Decode BCD / binary / date | 19ns / 4.3ns / 173ns | 1 / 0 / 3 allocs |
| Plan cache hit | 11.7ns | 0 allocs |
| Full `Read`, 1000 points | 807µs (maxDIs=1) / 419µs (maxDIs=12) | 17845 / 7101 allocs |

**A complete exchange (drain → encode → send → parse → dispatch) is 280ns, while one frame at 2400bps
takes 165ms — six orders of magnitude apart.** There is no gateway-side optimization pressure on 645.

### Production configuration advice

1. **Raise `maxDIsPerRead` to 8–12** (spec limit 12). It is the only point-capacity lever, and the gain
   equals the batch size exactly. The trade-off: when a batch is rejected the driver degrades to reading
   identifier by identifier (`plan.go`), so abnormal responses cost extra frames.
2. **Lower `interFrameDelayMs` to match the actual meter.** The 30ms default is conservative; fast meters
   tolerate 10–20ms, improving per-device throughput linearly. At 5600/9600bps or above — or on Ethernet
   (DTU transparent mode) — 30ms becomes the bottleneck before the link does. After lowering it, watch for
   bit errors: insufficient transceiver turnaround drops the first byte.
3. **Size per-device point count from "baud rate + scan interval", not from gateway capability**:
   `points ≤ baud/11 / (request + response bytes) × scan interval × batch`. At 2400bps / 1s / batch 12 that is 12 points.
4. **Serial (`DLT645.Serial`) and Ethernet (`DLT645.TCP`) share the same application layer**, so frame
   counts and batching gains are identical. This benchmark covers TCP only — the extra serial constraint is
   RS-485 bus exclusivity (`SerialExclusive`): while one meter is polled, every other meter on the same bus
   waits, so divide capacity by the number of meters sharing one 485 bus.

### Fake meter vs real hardware

The fake meter in `testutil/fake/dlt645.go` implements read-data commands only (2007=0x11 / 1997=0x01),
echoing each requested data identifier with fixed-length data. The data length is a **convention**, not
derived from the request (645 read commands carry only identifiers; lengths come from the meter manual),
so the load-test seeder declares 4 bytes explicitly via `%08X:4:2`. The following real-hardware paths are
**not covered** — do not infer them from benchmark numbers:

- **Half-duplex echo**: some DTUs / serial servers echo the request frame back verbatim, which the driver
  must parse and skip (`isRequestCtrl` in `codec.go`). The fake meter does not echo, so an echoing link
  doubles the byte volume and raises per-frame time.
- **Follow-up frames (0xB1/0xB2)**: the driver does not reassemble them yet; receiving one marks the whole
  group as abnormal. The fake meter never sends them.
- **Abnormal responses**: the fake meter answers error code 02 for rejected identifiers. The external
  simulator on port 8899 **replies with error code 01 to every read request, with a byte-swapped address
  field** — it cannot be used for capacity measurement.

### Data identifier dictionary verification status

Of the built-in dictionary (44 entries for 2007, 18 for 1997) **only 6 / 2** — total energy, phase-A
voltage/current, grid frequency and a few more — have been cross-verified against multiple sources. The
byte counts and decimal places of the remaining entries come from the spec text alone and have **not been
compared against a real meter one by one**. At startup the driver logs these "pending verification" entries
in one INFO line; check that log first when a reading does not match the meter.

Do not patch the code to adapt — override through the **address suffix** instead:

```
02010100          # from the dictionary: 2 bytes, 1 decimal (phase-A voltage)
02020100:3:3      # explicitly 3 bytes, 3 decimals
12345678:2:0:s    # vendor-private identifier: 2-byte signed BCD
```

An identifier that is neither in the dictionary nor given an explicit byte count **fails loudly** rather
than guessing a length — a guessed length silently yields wrong values.

## Scaling bottleneck notes

1. **Config watcher full-table checksum**: `collector.Engine.loadConfigChecksum` runs every 10s,
   loading the whole device_address table + hashing. The main scaling bottleneck at massive point counts (1.7s per pass at 1M addresses).
2. **High-throughput drops are a CPU ceiling**: the worker pool never filled (queue 1024 suffices) and adding workers
   made things worse (context switching); hotspots are driver decode/format + record construction + GC.
3. **Sparse address layouts magnify frame count by the point count**: production configs should use contiguous/regular addresses (all protocols).
   OPC UA is the exception: direct NodeIDs have no range-merge concept — any point combination is ceil(N/maxBatch) frames.
