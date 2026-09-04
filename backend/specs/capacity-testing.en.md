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

## Scaling bottleneck notes

1. **Config watcher full-table checksum**: `collector.Engine.loadConfigChecksum` runs every 10s,
   loading the whole device_address table + hashing. The main scaling bottleneck at massive point counts (1.7s per pass at 1M addresses).
2. **High-throughput drops are a CPU ceiling**: the worker pool never filled (queue 1024 suffices) and adding workers
   made things worse (context switching); hotspots are driver decode/format + record construction + GC.
3. **Sparse address layouts magnify frame count by the point count**: production configs should use contiguous/regular addresses (all protocols).
   OPC UA is the exception: direct NodeIDs have no range-merge concept — any point combination is ceil(N/maxBatch) frames.
