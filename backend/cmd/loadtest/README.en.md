# loadtest — end-to-end acquisition capacity benchmark

[English](README.en.md) | [简体中文](README.md)

Runs the full acquisition pipeline in-process (acquisition engine + worker pool + protocol drivers + **fake PLC servers**), sweeping a (device count × points-per-device) matrix to quantify the **device capacity** and **point capacity** each protocol can sustain on the gateway side.

Core idea: **point capacity is not decided by the number of points, but by frames × round-trip latency.** The fake servers only return valid responses per request and do no protocol-limit checks, isolating the benchmark target to the gateway itself.

## Usage

```bash
go run ./cmd/loadtest -protocol modbus -devices 10,50,100 -points 500,2000,5000
```

| Flag | Default | Description |
|---|---|---|
| `-protocol` | `modbus` | `modbus` / `mc` / `fins` / `s7` / `cip` / `rockwell` / `opcua` / `dlt645` |
| `-devices` | `10,50,100` | Device count matrix (comma-separated) |
| `-points` | `500,2000,5000` | Points-per-device matrix |
| `-scan` | `1000` | Scan frequency (ms); tighten to 200/100 to probe saturation |
| `-run` / `-warmup` | `5` / `2` | Measurement window / warm-up seconds |
| `-servers` | `4` | Fake PLC server pool size (devices round-robin across it) |
| `-sparse` | `false` | Sparse address layout (gaps > merge window, **1 frame/point**, worst-case frame count) |
| `-latency` | `0` | Fixed per-transaction latency in ms (simulates real RTT; CIP family / `opcua` / `dlt645` only) |
| `-opcua-batch` | `0` | OPC UA maxBatch (nodes per ReadRequest; 0 = driver default 100) |
| `-dlt645-batch` | `0` | DL/T 645 maxDIsPerRead (data identifiers per request; spec limit 12; `0` = driver default **12**, `1` = one point per round-trip) |
| `-dlt645-interframe` | `-1` | DL/T 645 inter-frame delay in ms (`-1` = driver default: `0` for TCP, `30` for serial) |
| `-cpuprofile` / `-memprofile` | - | pprof profiles of the largest combination |

> `-sparse` is meaningless for `dlt645` and `opcua`: neither has a range-merge concept, so the frame
> count depends only on points / per-request batch size, not on whether addresses are adjacent.

## Metrics

- **drop%** (core verdict): `1 - actual records / expected records`. Expected = devices × points × (window / scan interval). Non-zero means device poll cycles exceeded the scan interval and got skipped (in-flight anti-pileup) or the worker pool dropped rounds.
- **actual/s**: records/sec actually received by the sink (counting only; replaces the real push engine to isolate push interference).
- **cycleMs**: poll cycle derived from diffs of dev-0 successful reads (meaningful only when `scan>=1000ms`; below 1s it is washed out by second-level timestamp precision).
- **heapMB / goroutines / errors**: peaks within the measurement window; errors come from engine error counters.
- **frames/s**: transaction frames answered per second by the fake servers (reported by `dlt645` only; `-` elsewhere).
  For request/response, per-point-addressed protocols the frame count is the true independent variable — deriving a
  frame rate from "records/sec" goes through the engine's batching and scheduling, and a mistake anywhere in that
  chain yields a wrong conclusion. Note that at small point counts this value is capped by the scan interval
  (frames per cycle ÷ scan interval), not by frame cost.

"Supported" verdict: drop%=0 **and** errors=0 **and** good%=100.

> **Counting window**: both record and frame snapshots are taken **before** `engine.Stop()`. `Stop()` drains
> in-flight polls (`wg.Wait()` in `task.go`), and unfinished device cycles keep producing records during the
> drain; counting after that spreads those records over a window of only `-run` seconds — overstating
> throughput more the heavier the drop (i.e. the longer the cycle), which corrupts exactly the regime this
> tool exists to detect (fixed 2026-09).

## How it works

```
testutil/fake/           fake PLC servers (real TCP listeners, frame-by-frame parsing with valid responses)
  server.go              shared TCP skeleton: one goroutine per connection, Close shuts all down
  modbus.go              MBAP + FC1/2/3/4/5/6/15/16
  mc3e.go                MC 3E (SLMP) batch reads (word / bit units)
  fins.go                FINS/TCP (connection handshake + memory-area read 0x0101)
  s7.go                  S7 (ISO CR/CC + PDU negotiation + read vars, byte-aligned with gos7)
  cip.go                 EtherNet/IP (Register Session + SendRRData + 0x4C reads;
                         Omron dialect NewCIP / AB dialect NewCIPAB / RTT-enabled *WithLatency)
  dlt645.go              DL/T 645 (read-data command 0x11; echoes identifiers + fixed-length data;
                         Dlt645ReadFrames provides the frame counter)
  opcua.go               OPC UA (reuses the gopcua in-process server package + Read handler overrides;
                         address space ns=1;i=≥1001 fully formulaic; RTT-enabled NewOpcUaWithLatency)
  integration_test.go    real-driver-to-fake-server interop tests (guards against frame-format drift)
cmd/loadtest/
  harness.go             data seeder (real migrations) + counting sink + measurement sampler + protocol adapters
  main.go                flag parsing + fake server pool + matrix sweep
```

- Devices and points are written to a temp SQLite (via `database/sqlite.InitDB` with the real migrations — same schema as production).
- Acquisition engine parameters match `wire.go`: worker pool `NumCPU*2` + 1024 queue.
- Logging is muted to WARN into a temp dir, and `configFile` uses loadtest's own embedded config so a DEBUG-level `default.yaml` in the working directory can't pollute measurements.

## First-round results per protocol

Environment: 8 cores / Windows / loopback. **Numbers are gateway-side ceilings; they vary with core count and real device round-trip latency.**

### Modbus.TCP (contiguous layout, default mergeWindow=125, frame = 125 registers)

scan=1000ms: 10/50/100 devices × ≤5000 points all **0% drop** (up to 100 devices × 5000 points ≈ 500k records/s).

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 5000 | 1000ms | 250k | 299k | 0.0 |
| 100 | 5000 | 1000ms | 500k | 600k | 0.0 |
| 200 | 2000 | 200ms | 2M | 2.08M | 0.0 |
| 200 | 5000 | 200ms | 5M | 4.48M | **10.4** |
| 100 | 5000 | 100ms | 5M | 4.1M | **17.9** |

**Boundary**: zero-drop throughput on this machine is about **2M records/s**; at 5M/s drop runs 10–18%.
Per-device safe ceiling at 100ms scan is ≤2000 points; 5000 points makes the poll cycle exceed 100ms and rounds get skipped.

### Modbus.TCP (sparse layout, 1 frame/point, ≤520 points/dev limited by register address space)

100 devices × 500 points = 50k frames/s, scan=1000ms, still **0% drop** — frame count itself is not the bottleneck; decoding and scheduling are.

### MC 3E (default maxReadWords=100, frame = 100 words)

At scan=1000ms the whole matrix is **0% drop** like Modbus (up to 100 × 5000 ≈ 500k records/s).
MC defaults to 100 words/frame, ~1.25× Modbus frame count; raising `maxReadWords` (cap 960) cuts frames significantly.

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 5000 | 1000ms | 250k | 250k | 0.0 |
| 100 | 2000 | 1000ms | 200k | 240k | 0.0 |
| 100 | 5000 | 1000ms | 500k | 500k | 0.0 |

### FINS.TCP (default maxReadWords=100, frame = 100 words)

scan=1000ms: whole matrix **0% drop** (up to 100×5000 ≈ 500k records/s), same tier as MC/Modbus.

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 100 | 2000 | 1000ms | 200k | 240k | 0.0 |
| 100 | 5000 | 1000ms | 500k | 500k | 0.0 |

### Siemens.S7 (byte area, gos7 chunks at PDU 480, ≤462 bytes)

scan=1000ms: whole matrix **0% drop** (up to 100×5000 ≈ 500k records/s). S7 has the fewest frames
(5000 bytes ≈ 11 PDU requests) — the most frame-efficient tier.

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 100 | 2000 | 1000ms | 200k | 200k | 0.0 |
| 100 | 5000 | 1000ms | 500k | 599k | 0.0 |

### Omron.CIP (one round-trip per point by default; optional 0x0A multi-service batch read)

**Single-read (maxTagsPerRequest=0/1)**: one SendRRData round-trip per point, frames = points, capacity is locked in.

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 100 | 2000 | 1000ms | 200k | 216k | 0.0 |
| 50 | 5000 | 1000ms | 250k | 214k | **14.4** |
| 100 | 5000 | 1000ms | 500k | 151k | **69.8** (cycle climbs to 3s) |

**Batch read (`maxTagsPerRequest=100`, one 0x0A packet reads 100 tags)**:
round-trips drop from N to ceil(N/100), returning capacity to the common tier (~2M records/s).

| Devices | Points/dev | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 100 | 5000 | 1000ms | 500k | 599k | 0.0 (69.8% single-read) |
| 50 | 5000 | 100ms | 2.5M | 2.55M | 0.0 |
| 100 | 5000 | 200ms | 2.5M | 2.59M | 0.0 |
| 100 | 5000 | 100ms | 5M | 2.68M | **46.4** (common worker-pool ceiling) |

[Hardware verification pending] The 0x0A multi-service packet is a new CIP-driver feature, enabled via `maxTagsPerRequest` in `device.protocol_json` (default 0 = single read, zero regression). The wire layout follows HslCommunication's OmronCipNet concise format (`0x0A | service count | concatenated 0x4C requests`); **it must be validated on a target NJ/NX PLC before production use**. If it mismatches real hardware, only the `buildMultipleDataTableRead` / `parseMultipleDataTableRead` pair in `driver/omron/cip/frame.go` needs adjusting.

### Rockwell.CIP (Logix tag addressing; array tags merge ≤125 elements/frame, standalone tags serialize one round-trip each)

The driver groups reads by tag name: same-cardinality `Arr[N]` points are sorted by index and split into Read Tag Elements chunks of ≤125 elements (`maxTagElements`, PDU 508-byte cap); standalone `Tag_x` tags cost one 0x4C round-trip each, serialized (client-adapter mutex, no 0x0A multi-service).

**Contiguous layout (Arr[i], best case for array merging, scan=1000ms)**: whole matrix **0% drop**;
50 devices × 5000 points (250k points, ≈40 frames/device/cycle) hit 293k records/s — same tier as the Modbus baseline.

| Devices | Points/dev | Layout | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 5000 | contiguous | 250k | 293k | 0.0 |
| 10 | 5000 | contiguous + RTT 1ms | 50k | 60k | 0.0 (40 frames × 1.4ms ≈ 56ms/cycle, ample headroom) |
| 50 | 5000 | contiguous + RTT 1ms | 250k | 300k | 0.0 |

**Sparse layout (Tag_i standalone tags, 1 frame/point, worst-case serial round-trips, scan=1000ms)**:
with zero RTT, 50×5000 drops 35% (5000 serial round-trips per cycle ≈ 2s > 1s scan → skipped rounds);
with RTT 1ms, a single 1000-point device starts dropping (cycle ≈ 1.2s).

| Devices | Points/dev | Layout | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 2000 | sparse | 100k | 120k | 0.0 (edge) |
| 50 | 5000 | sparse | 250k | 162k | **35.2** (cycle 2s) |
| 1 | 1000 | sparse + RTT 1ms | 1k | 0.6k | **40.0** (cycle 2s) |
| 10 | 2000 | sparse + RTT 1ms | 20k | 8k | **60.0** |

**Boundary conclusions**:
- Array-regular layout (recommended): point capacity matches Modbus (250k points @1s, zero drop on this machine);
  frames = sum of ceil(same-cardinality array points / 125);
- Standalone-tag layout: per-device capacity ≈ scan interval / one round-trip latency (capped around 800 points/device at 1s / 1ms RTT);
  multi-device capacity is constrained by worker pool and CPU together. In production, plan contiguous points as `Base[N..N+k]` array tags wherever possible;
- Fix note: array groups >125 points previously abandoned merging entirely and degraded to per-point reads (5000 points = 5000 round-trips);
  now sorted by index and greedily chunked (5000 points = 40 frames). Before the fix, contiguous 50×5000 dropped 35.2%.

### OPC.UA (maxBatch bulk Read, default 100 nodes/frame; direct NodeID addressing)

The driver resolves addresses to NodeIDs (no local I/O) and issues ReadRequests in `maxBatch` batches;
frames = ceil(N/maxBatch), independent of point layout (numeric NodeIDs have no range-merge concept).
The fake server reuses the gopcua in-process server package (same approach as the driver/opcua integration tests);
the Read override handler returns values by NodeID formula (detailed findings in [specs/capacity-testing.en.md](../../specs/capacity-testing.en.md)).

**Contiguous layout (`ns=1;i=<n>`, scan=1000ms)**: whole matrix **0% drop** (up to 100×5000
= 500k points, actual 560k records/s) — same tier as the Modbus baseline.

| Devices | Points/dev | Layout | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|
| 50 | 5000 | contiguous | 250k | 281k | 0.0 |
| 100 | 5000 | contiguous | 500k | 562k | 0.0 |
| 50 | 5000 | contiguous + RTT 1ms | 250k | 269k | 0.0 |
| 10 | 5000 | contiguous + RTT 5ms | 50k | 56k | 0.0 (edge; 100 frames × 10ms ≈ 1s) |
| 50 | 5000 | contiguous + RTT 5ms | 250k | 97k | **61.1** (frames × RTT exceeds scan interval) |
| 50 | 5000 | 500ms | 500k | 531k | 0.0 |
| 100 | 2000 | 200ms | 1M | 968k | 3.2 (edge) |
| 50 | 5000 | 200ms | 1.25M | 944k | **24.5** |
| 50 | 5000 | 200ms + maxBatch 500 | 1.25M | 1237k | 1.0 |
| 100 | 5000 | 200ms + maxBatch 500 | 2.5M | 1217k | **51.3** (CPU ceiling) |

**Boundary conclusions**:
- Capacity formula `ceil(N/maxBatch) × 2×RTT < scan interval`; zero drop at 50×5000 with RTT 1ms,
  but at RTT 5ms the boundary arrives at just 100 frames/cycle — notably more RTT-sensitive than the Modbus family (125 points/frame).
- Raising `maxBatch` (≤1000) is this protocol's only frame-count lever: 50×5000@200ms went from 24.5% to 1.0%.
- Browse-path addresses (`Device/Tag` form) need a Translate round-trip on first use and invalidate on reconnect — not covered by this benchmark;
  direct NodeID addressing is recommended in production.

### DL/T 645 (per-identifier addressing, no range merging; batching is the only frame-count lever, spec limit 12)

**Gateway-side compute is not a bottleneck at all here**: one complete exchange costs 280ns (see the micro-benchmarks
in `driver/dlt645`), while a single frame at 2400bps takes 165ms — six orders of magnitude apart. Capacity is set by
the **baud rate** and the **inter-frame delay**.

What makes the inter-frame delay special: it is **charged per frame** and is the hard per-device throughput ceiling on
serial (1000/delay frames per second), independent of link speed and device concurrency (measured at 1/2/5/10
concurrent devices: a constant 32 frames/s per device). **30ms is only meaningful on serial** — on a TCP transparent
link the serial server / DTU handles turnaround itself, so the driver now defaults it to 0 there.

**Point capacity gain from batching equals the batch size exactly** (single device, scan=1000ms, 30ms delay):

| maxDIsPerRead | Points | Frames | Frames/s | Cycle | drop% |
|---|---|---|---|---|---|
| 1 | 20 | 20 | 20 | 1s | 0.0 |
| 1 | 50 | 50 | 25 | 2s | **50.0** |
| 1 | 100 | 100 | 25 | 4s | **75.0** |
| 12 (default) | 240 | 20 | 20 | 1s | 0.0 |
| 12 | 600 | 50 | 25 | 2s | **50.0** |
| 12 | 1200 | 100 | 25 | 4s | **75.0** |

At the same frame count, batching carries 12× the points. With `maxDIsPerRead=1` frames = points, which is the root
cause of "one meter yields only a handful of points" in the field; the driver default is now 12, and the `1` rows are
kept for contrast (`-dlt645-batch 1` reproduces them).

Note also that the inter-frame delay now defaults to **0 on TCP** (30ms is only meaningful on serial); pass
`-dlt645-interframe` explicitly to reproduce the old behaviour.

**Real-link simulation (`-latency`, 2400bps / 8E1)**: 165ms round-trip for 1 identifier, 825ms for 12.

| maxDIsPerRead | Round-trip | Points | Points/s | drop% |
|---|---|---|---|---|
| 1 | 165ms | 10 | 5 | **50.0** |
| 12 | 825ms | 12 | 12 | 0.0 |
| 12 | 825ms | 24 | 12 | **50.0** |

**At 2400bps a single meter tops out at 12 points/s** (1s scan), even with batching maxed out.

**Gateway-side ceiling (`-dlt645-interframe 0`, maxDIs=12, ~1.2ms per frame)**:

| Devices | Points/device | Frames/s | Records/s | drop% |
|---|---|---|---|---|
| 10 | 2000 | 1670 | 20000 | 0.0 |
| 50 | 5000 | 17270 | 205000 | **18.0** |
| 100 | 5000 | 24637 | 298000 | **40.4** |

Tops out around **25k frames/s / 300k records/s** (CPU ceiling). Worth noting: with the inter-frame delay disabled a
frame costs 1.2ms, of which about 1ms is the fixed read deadline in `tcpClient.Drain()` — **an optimization
opportunity**, since at high throughput (delay already disabled) it accounts for 80% of per-frame cost. Draining only
when the buffer actually holds residue (probing with `SetReadDeadline(time.Now())`) would remove it.

Full findings, micro-benchmarks and uncovered real-hardware paths: [specs/capacity-testing.en.md](../../specs/capacity-testing.en.md).

### Optimization directions
1. **Frame count decides point capacity**: Modbus 125 registers/frame, MC 960 words/frame, FINS 100 words/frame,
   S7 462 bytes/PDU, CIP 1 point/round-trip by default. At equal point counts frame counts differ by 1–2 orders of magnitude and capacity scales linearly with frames.
   With CIP `maxTagsPerRequest` batching, frames drop to batch-points/round-trip, back to the common tier (measured in this repo: 69.8% drop → 0%).
   Sparse layouts (gaps > merge window) magnify frame count by the point count — production configs should use contiguous/regular addresses wherever possible.
2. **The config watcher's full-table checksum is the scaling bottleneck**: `collector.Engine.loadConfigChecksum` runs every 10s
   `SELECT * FROM device_address WHERE status=1` (full-table load + hash). At 1M addresses a single pass takes 1.7s and several GB of RAM,
   directly conflicting with high-point scenarios. For massive point counts, switch to incremental/db-change awareness or lengthen the check interval.
3. **High-throughput drops are a CPU ceiling, not the worker pool**. Verified in benchmarking: the `worker pool full` log never fired at ≤200 devices
   (queue 1024 ≫ max backlog); raising workers from 32 to 64 actually worsened drop from 16.1% to 19.4% (CPU already saturated; more concurrency just adds context switching). Profiles show hotspots in driver decode/format + record construction + GC pressure. `collector.toRecords` was optimized
   (two map lookups per record → index-aligned direct access; measured toRecords CPU 13.7% → 1.7%);
   further candidates: driver-side formatting (skip re-formatting unchanged values) and per-round `RangeSig` cache-key recomputation.
