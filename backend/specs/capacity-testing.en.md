# Protocol Capacity Testing Findings

[English](capacity-testing.en.md) | [简体中文](capacity-testing.md)

> Benchmark tool: `cmd/loadtest` (methodology and the **full matrix data** in [cmd/loadtest/README.en.md](../cmd/loadtest/README.en.md)).
> Environment: 16 cores / Windows / loopback / out-of-process driver plugins, scan=1000/200/100ms, RTT=0/10/20ms.
> **Numbers are gateway-side ceilings**; they vary with core count and real device round-trip latency. For field capacity planning, scale via the "frame count model" below — do not copy absolute values.

> ⚠️ **As of 2026-09 every measurement in this document and in [cmd/loadtest/README.en.md](../cmd/loadtest/README.en.md) was taken on the plugin build** (all drivers are out-of-process plugins; each read/write costs one extra gateway ↔ plugin RPC round-trip plus two rounds of point encode/decode — the readings **already include** that cost).
>
> - This document keeps the **frame count model** and the per-protocol **tuning conclusions** — those are protocol- and configuration-level findings, independent of which process the driver runs in, and they hold before and after pluginization.
> - For **all absolute throughput and drop percentages**, use the "Post-pluginization baseline" in [cmd/loadtest/README.en.md](../cmd/loadtest/README.en.md); this document no longer maintains its own copy of the full matrix. Any remaining 2026-08 numbers here are explicitly marked as historical.
> - Pre-pluginization baselines (in-process era) are **obsolete** — do not quote them.

## Frame count model (capacity estimation formula)

The essence of point capacity: **frames × per-frame round-trip latency < scan interval** (exceeding it skips rounds and drops points — the acquisition engine's in-flight anti-pileup skips polls of devices whose previous cycle hasn't finished).

| Protocol | Per frame | Frames (contiguous/regular layout) | Frames (sparse/standalone points) | Measured frames @5000 pts |
|---|---|---|---|---|
| Modbus.TCP | 125 registers | ceil(N/125) | N (when gaps > 125) | 40 |
| Mitsubishi.MC.TCP | 100 words (maxReadWords up to 960) | ceil(N/100) | N (when gaps > 8) | 50 |
| Omron.FINS.TCP | 100 words | ceil(N/100) | N (when gaps > 100) | 50 |
| Siemens.S7 | 462 bytes/PDU | ceil(N/462) (byte area, byte-addressed) | N (when gaps > 8) | 13 |
| Omron.CIP | 64 tags/frame (`maxTagsPerRequest` defaults to 64, hard cap 64; set 1 for single read) | ceil(N/64); N when single-read | N | 80 |
| Rockwell.CIP | 125 elements/frame (array tags) | ceil(N/125) per same-cardinality array, summed across cardinalities | N (standalone tags) | 40 |
| OPC.UA | maxBatch nodes/ReadRequest (default 100, up to 1000) | ceil(N/maxBatch) (direct NodeID) | Same (browse-path adds 1 Translate round-trip on first use, then caches) | 50 |

The last column is measured on the 2026-09 plugin build (`-latency 100 -scan 1000 -devices 1 -points 5000`,
derived from `cycleMs`) and matches the formulas **exactly for all seven protocols** — that experiment also
shows pluginization changed the effective per-frame latency but **not** the frame count model.

> ⚠️ **The formula is a hard boundary — leave headroom.** The criterion is `frames × RTT < scan interval`,
> but sitting *on* it breaks down: when `frames × RTT` lands near the scan interval the cycle wobbles between
> two ticks and the drop rate goes **bimodal** (measured: Modbus/Rockwell 5000 points @RTT=20ms/scan=1000ms,
> 40 frames × 20ms = 800ms flush against 1s, gave both 0% and 40% across three repeats).
> **Plan for `frames × RTT ≤ 0.7 × scan interval`** — do not plan on the equals sign.

Per-device capacity ceiling ≈ scan interval / per-frame round-trip latency × points per frame; the whole machine is jointly constrained by CPU (decode/format/GC, plus the plugin RPC's encode/decode and IPC) and the worker pool (NumCPU×2).
On this machine (16 cores) the plugin build peaks at about **1.05M records/s** (Modbus 50×5000 @200ms, at the cost of 15.7% drop) and about **300k records/s** at 0% drop — below the ~2M records/s claimed for the in-process era, a gap consistent with "one extra plugin RPC per device per cycle".

## Rockwell.CIP specifics (2026-08)

### Layout decides capacity

| Layout | Mechanism | Measured on the plugin build (2026-09) |
|---|---|---|
| **Array-regular** (`Base[0..N-1]`) | Sorted by index, split into Read Tag Elements chunks of ≤125 elements | 40 frames per 5000 points. 50 devices × 5000 points @scan=1000ms **0% drop** (300k rec/s); @200ms drops 33.7%; @100ms drops 68.5% (RTT=0). With RTT=20ms and 10 devices: still 0% at scan=1000ms, 79.2% at scan=200ms |
| **Standalone tags** (`Tag_i`) | One 0x4C serial round-trip per point (adapter mutex, no multi-service batch) | 5000 frames per 5000 points — the worst case for frame count. For the same order of magnitude see the `Omron.CIP` single-read table: at RTT=10ms even 500 points/device drops 60% |

**Standalone-tag layout capacity formula**: per device ≈ scan interval / RTT (capped around 800 points/device at 1s / 1ms RTT).
(That formula follows from frame and round-trip counts and still holds after pluginization; the new figures above are from the "Post-pluginization baseline" in [cmd/loadtest/README.en.md](../cmd/loadtest/README.en.md).)

### Production configuration advice

1. **Plan contiguous points as `Base[N..N+k]` array tags wherever possible** — in the same PLC program, move the data to be collected into a contiguous array and point capacity returns to the Modbus tier.
2. When standalone tags are unavoidable, cap points per device (≤500 points/device at 1s / 1ms RTT, leaving headroom) or lower the scan frequency.
3. Array points must share one type with fixed element size (STRING/struct members are not merged and are read per point).

### Fix record

`buildPlan` (`backend/driversrc/rockwellcip/rockwellcipdrv/range.go`) previously **abandoned merging entirely** for array groups >125 points, degrading to per-point single reads (5000 points = 5000 round-trips/cycle); it now sorts by index and greedily chunks (each chunk ≤125 elements with span ≤125), so 5000 points = 40 frames. Contiguous 50×5000 before → after: 35.2% drop → 0%.

### Fake server vs. real hardware

`testutil/fake/cip.go`'s `NewCIPAB` replies in the AB dialect:
`CC | 00 | status | extended-status-word-count (1) | type code (2) | data`. goindustrial decodes that extended-status-word-count byte strictly per the CIP spec (if missing, the first byte of the type code, 0xC3, is misread as 195 extended status words → EOF). The Omron dialect (`NewCIP`) has no such byte; the two drivers cannot share one fake server. **During live integration, if Rockwell reads fail with EOF-like errors, first check whether the response carries the extended-status-word-count byte.**

## OPC.UA specifics (2026-08)

### Frame count model: MaxBatch bulk read, capacity matches Modbus

The driver resolves points to NodeIDs, then issues ReadRequests in batches of `maxBatch` (configurable in protocol_json, default 100, cap 1000); direct NodeID (`ns=2;i=100`) resolution involves no I/O. The fake server is a gopcua in-process server + Read override handler (returns values by NodeID formula, no pre-built Node objects).

**Measured on the plugin build (2026-09)**: with direct NodeIDs (`ns=1;i=<n>`), 5000 points/device = 50 frames.

| Devices | Points/dev | Layout | RTT | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|---|---|---|
| 10 | 5000 | contiguous | 0 | 1000ms | 50k | 60k | 0.0 |
| 10 | 5000 | contiguous | 0 | 100ms | 500k | 416k | 16.8 |
| 50 | 5000 | contiguous | 0 | 1000ms | 250k | 300k | 0.0 |
| 50 | 5000 | contiguous | 0 | 200ms | 1.25M | 452k | **63.9** |
| 10 | 5000 | contiguous | 20ms | 1000ms | 50k | 18k | **63.2** |
| 10 | 5000 | contiguous | 20ms | 100ms | 500k | 28k | **94.3** |

**Drop runs systematically higher than MC/FINS despite the same 50 frames** (at RTT=20ms with 5000 points,
MC drops 40% while OPC.UA drops 63.2%). That is not the protocol being worse — it is a **fake-server**
artifact: gopcua's in-process server dispatches messages serially across all connections, so with multiple
devices plus RTT the transactions queue on the server side (see below). Real OPC UA servers normally handle
concurrent connections, so field drop should be better than this table.

**maxBatch is the only frame-count lever**: raising it (cap 1000) cuts frames linearly — a 1000-point device
goes 100 frames → 10 frames. Some servers impose per-request operation limits (BadTooManyOperations); on
failure dial it back down (the driver retries in sub-batches).

> Historical values (2026-08, in-process drivers, 8 cores) are obsolete and kept only as a trend reference:
> `maxBatch=500` once improved 50×5000@200ms from 24.5% to 1.0% drop. **Do not quote as a plugin-build promise.**

### OPC UA-specific caveats

1. **RTT sensitivity = frames × RTT**: at 50 frames/cycle (maxBatch=100) a 20ms RTT already sits at the
   1s scan interval boundary (63.2% drop at scan=1000ms on the plugin build, table above).
   In production with 100ms cycles the budget is ≈ scan interval/(2×RTT) frames.
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
   **[Unverified]** That worker-count experiment was run with in-process drivers. Under pluginization the hotspot set
   gains the plugin RPC's encode/decode and IPC; the 237 runs of the 2026-09 baseline **did not repeat it**, so this
   claim is still unconfirmed on the new architecture.
3. **Sparse address layouts magnify frame count by the point count**: production configs should use contiguous/regular addresses (all protocols).
   OPC UA is the exception: direct NodeIDs have no range-merge concept — any point combination is ceil(N/maxBatch) frames.
