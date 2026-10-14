# loadtest — end-to-end acquisition capacity benchmark

[English](README.en.md) | [简体中文](README.md)

Runs the full acquisition pipeline in-process (acquisition engine + worker pool + **out-of-process protocol driver plugins** + fake PLC servers), sweeping a (device count × points-per-device) matrix to quantify the **device capacity** and **point capacity** each protocol can sustain on the gateway side.

**All** protocol drivers now come from the out-of-process plugins built from `backend/driversrc/*` (the in-process implementations were removed when drivers were pluginized — see [docs/driver-plugins.md](../../../docs/driver-plugins.md)). **Plugins must therefore exist before loadtest can run.**

Core idea: **point capacity is not decided by the number of points, but by frames × round-trip latency.** The fake servers only return valid responses per request and do no protocol-limit checks, isolating the benchmark target to the gateway itself.

## Usage

### Build the plugins first (required)

With an empty plugin directory loadtest **fails fast** instead of printing a table of "protocol unsupported, all-zero records". Build the protocol you want to benchmark:

```bash
cd backend

# Option 1: build just the one protocol. The artifact MUST be named iot-driver-<driversrc dir name>
go build -o ./plugins/iot-driver-modbus ./driversrc/modbus

# Option 2: build every plugin at once (run from the repo root; same build as the release package)
./build.sh
```

> **On Windows the artifact must carry the `.exe` suffix** (`-o ./plugins/iot-driver-modbus.exe`).
> Without an extension `os/exec` cannot find the executable: the log shows a few "start failed"
> warnings, then a handshake timeout, and the run ends as "protocol not registered".

Then add `-plugins-dir` as needed (default `plugins`):

```bash
go run ./cmd/loadtest -protocol modbus -devices 10,50,100 -points 500,2000,5000 -plugins-dir ./plugins
```

**How a relative `-plugins-dir` is resolved:**

1. Relative to the **directory of the executable** — the same semantics as the gateway's
   `plugin.dir` (via `pluginhost.resolvePluginDir`); hit by `go build -o ./loadtest ./cmd/loadtest`
   when the binary sits next to `plugins/`;
2. Relative to the **working directory loadtest was invoked from** — `go run` places the binary
   in a temp dir, so this fallback is what lets `-plugins-dir ./plugins` work from `backend/`.

If neither exists, loadtest exits with an error listing every absolute path it tried.

| Flag | Default | Description |
|---|---|---|
| `-protocol` | `modbus` | `modbus` / `mc` / `fins` / `s7` / `cip` / `rockwell` / `opcua` / `dlt645` |
| `-plugins-dir` | `plugins` | Plugin directory; a relative path resolves against **the executable's directory first, then the invocation working directory** (not cwd only) — see above |
| `-devices` | `10,50,100` | Device count matrix (comma-separated) |
| `-points` | `500,2000,5000` | Points-per-device matrix |
| `-scan` | `1000` | Scan frequency (ms); tighten to 200/100 to probe saturation |
| `-run` / `-warmup` | `5` / `2` | Measurement window / warm-up seconds |
| `-servers` | `4` | Fake PLC server pool size (devices round-robin across it) |
| `-sparse` | `false` | Sparse address layout (gaps > merge window, **1 frame/point**, worst-case frame count) |
| `-latency` | `0` | Fixed per-transaction latency in ms (simulates real RTT; **supported by all 8 protocols**) |
| `-serial-baud` | `0` | **Model a serial link**: derive the line time from this baud rate and the protocol frame size, and **add** it to `-latency` (see "Load-testing a serial link" below). `0` = off; only protocols with a known frame layout are supported — others **fail loudly** |
| `-cip-batch` | `0` | Tags per CIP 0x0A multi-service read: `1` = one tag per round-trip, `>1` = batch; `0` = omit the field and use the driver default (**note: the driver default is 64**) |
| `-opcua-batch` | `0` | OPC UA maxBatch (nodes per ReadRequest; 0 = driver default 100) |
| `-dlt645-dis` | `0` | DL/T 645 `maxDIsPerRead` (data identifiers packed per request, 1..12; 0 = omit the field, driver default **12 = protocol ceiling**; pass `1` to reproduce the old one-frame-per-point configuration) |
| `-dlt645-interframe` | `-1` | DL/T 645 `interFrameDelayMs` (wait after sending before reading the response; **`-1` = driver default: TCP `0` / serial `30ms`**, `0` = disabled) |
| `-cpuprofile` / `-memprofile` | - | pprof profiles of the largest combination |

## Metrics

- **drop%** (core verdict): `1 - actual records / expected records`. Expected = devices × points × (window / scan interval). Non-zero means device poll cycles exceeded the scan interval and got skipped (in-flight anti-pileup) or the worker pool dropped rounds.
- **actual/s**: records/sec actually received by the sink (counting only; replaces the real push engine to isolate push interference).
- **cycleMs**: poll cycle derived from diffs of dev-0 successful reads (meaningful only when `scan>=1000ms`; below 1s it is washed out by second-level timestamp precision).
- **heapMB / goroutines / errors**: peaks within the measurement window; errors come from engine error counters.

"Supported" verdict: drop%=0 **and** errors=0 **and** good%=100.

## How it works

```
testutil/fake/           fake PLC servers (real TCP listeners, frame-by-frame parsing with valid responses)
  server.go              shared TCP skeleton: one goroutine per connection, Close shuts all down
  modbus.go              MBAP + FC1/2/3/4/5/6/15/16 (NewModbusTCP / *WithLatency)
  mc3e.go                MC 3E (SLMP) batch reads (word / bit units, *WithLatency)
  fins.go                FINS/TCP (connection handshake + memory-area read 0x0101, *WithLatency)
  s7.go                  S7 (ISO CR/CC + PDU negotiation + read vars, byte-aligned with gos7, *WithLatency)
  cip.go                 EtherNet/IP (Register Session + SendRRData + 0x4C reads;
                         Omron dialect NewCIP / AB dialect NewCIPAB / RTT-enabled *WithLatency)
  dlt645.go              DL/T 645-2007 meter (read command echoes the data identifier plus fixed-length
                         data derived from it; NewDLT645WithLatency adds RTT)
  opcua.go               OPC UA (reuses the gopcua in-process server package + Read handler overrides;
                         address space ns=1;i=≥1001 fully formulaic; RTT-enabled NewOpcUaWithLatency)
cmd/loadtest/
  harness.go             data seeder (real migrations) + counting sink + measurement sampler + protocol adapters
  pluginLoader.go        plugin dir resolution + pluginhost.Manager lifecycle + protocol registration check
  main.go                flag parsing + fake server pool + matrix sweep
```

- Devices and points are written to a temp SQLite (via `database/sqlite.InitDB` with the real migrations — same schema as production).
- Acquisition engine parameters match `wire.go`: worker pool `NumCPU*2` + 1024 queue.
- Logging is muted to WARN into a temp dir, and `configFile` uses loadtest's own embedded config so a DEBUG-level `default.yaml` in the working directory can't pollute measurements.
- Plugins are loaded through the gateway's own `pluginhost.Manager` — **the exact same path as production**.
  `Start()`'s first scan is synchronous and runs before any acquisition engine starts (same ordering as
  `deps.DriverManager.Start()` in `main.go`), so protocols are registered by the time the engine builds its tasks.
  The directory scan interval is stretched to 1 hour during a run, so a "driver set changed → refresh acquisition
  config" cycle cannot rebuild tasks inside a measurement window.

## Post-pluginization baseline (measured 2026-09)

This is **the first valid baseline for the plugin build**, superseding every earlier reading taken
**while drivers still ran inside the gateway process** (that data died with pluginization — see
[specs/capacity-testing.en.md](../../specs/capacity-testing.en.md)). All drivers now run as
out-of-process plugins, so every device poll pays one extra RPC (request encode + IPC + response
decode); **the numbers below already include that cost**.

Environment: **16 cores** / Windows / loopback / 4 fake PLC servers / worker pool 32 + queue 1024.
**Figures are gateway-side ceilings** — they vary with core count and real device round-trip latency.
Re-measure on the target machine.

Matrix: per protocol `-devices 10`, `-points 500/2000/5000`, `-scan 1000/200/100ms`,
`-latency 0/10/20ms`; plus an aggregate tier `-devices 50 -points 5000 @ RTT=0`.
**All 237 runs reported errors=0 and good%=100.**

### Frame-count model (every number below follows from it)

**Per-device cycle ≈ frames × RTT**, where the frame count is set by each protocol's merge window
and address layout — independent of which process the driver lives in.

Measured with `-latency 100 -scan 1000 -devices 1 -points 5000` (derived from `cycleMs`); the
observed frame counts match the formulas **exactly, for all seven protocols**:

| Protocol | Default merge window (tunable) | Frames for 5000 pts/dev | Measured cycle @RTT=100ms |
|---|---|---|---|
| ModBus.TCP | `mergeWindow` 125 registers | 40 | 5000ms |
| Mitsubishi.MC.TCP | `maxReadWords` 100 words (tunable to 960) | 50 | 6000ms |
| Omron.FINS.TCP | `maxReadWords` 100 words | 50 | 6000ms |
| Siemens.S7 | gos7 chunks at negotiated PDU 480 → 462 bytes/frame | 13 | 2100ms |
| Omron.CIP | `maxTagsPerRequest` **64** (hard cap 64) | 80 | 9000ms |
| Rockwell.CIP | `maxTagElements` 125 elements (array tags only) | 40 | 5000ms |
| OPC.UA | `maxBatch` 100 nodes (tunable to 1000) | 50 | 6000ms |
| DLT645.TCP | `maxDIsPerRead` **12** (protocol ceiling) + `interFrameDelayMs` **0** (TCP) | 417 | see the DLT645.TCP section |

Measured cycles run slightly above "frames × 100ms" (MC: 50 frames → 6000ms, not 5000ms). The
difference is tick alignment in the collector engine: when the cycle is not a whole multiple of the
scan interval, polling defers to the next tick.

> ⚠️ **Cells sitting on the boundary are bimodal (important)**: when "frames × RTT" lands near the
> scan interval, the cycle wobbles between two ticks and the drop rate **flips between two levels**.
> Reproduced by repetition: Modbus and Rockwell at 5000 points @RTT=20ms/scan=1000ms
> (40 frames × 20ms = 800ms, flush against the 1s interval) gave `16.8 / 0.0 / 0.0` and
> `0.0 / 40.0 / 0.0` over three runs each. The tables below record **one sample**; treat such cells
> as "**on the boundary, may drop**" rather than a stable promise. To judge whether a combination is
> safe, ask whether **frames × RTT is comfortably below the scan interval** (leave 20%–30% headroom) —
> not whether one table cell reads 0.0.

### Overview: 5000 points/device, 10 devices

**RTT=0ms (plugin RPC only, no network latency)**

| Protocol | scan=1000ms | scan=200ms | scan=100ms |
|---|---|---|---|
| ModBus.TCP | 0.0 | 0.0 | 0.0 |
| Mitsubishi.MC.TCP | 0.0 | 6.8 | 48.7 |
| Omron.FINS.TCP | 0.0 | 0.0 | 48.7 |
| Siemens.S7 | 0.0 | 0.0 | 0.0 |
| Omron.CIP | 0.0 | 0.0 | 0.0 |
| Rockwell.CIP | 0.0 | 0.0 | 0.0 |
| OPC.UA | 0.0 | 0.0 | 16.8 |

**RTT=10ms**

| Protocol | scan=1000ms | scan=200ms | scan=100ms |
|---|---|---|---|
| ModBus.TCP | 0.0 | 65.6 | 80.0 |
| Mitsubishi.MC.TCP | 0.0 | 73.6 | 83.6 |
| Omron.FINS.TCP | 0.0 | 73.6 | 84.7 |
| Siemens.S7 | 0.0 | 48.0 | 65.6 |
| Omron.CIP | 0.0 | 76.0 | 88.0 |
| Rockwell.CIP | 0.0 | 64.0 | 82.0 |
| OPC.UA | 40.0 | 83.4 | 91.0 |

**RTT=20ms**

| Protocol | scan=1000ms | scan=200ms | scan=100ms |
|---|---|---|---|
| ModBus.TCP | 0.0 | 79.7 | 89.0 |
| Mitsubishi.MC.TCP | 40.0 | 81.6 | 90.8 |
| Omron.FINS.TCP | 40.0 | 81.6 | 90.8 |
| Siemens.S7 | 0.0 | 56.0 | 74.4 |
| Omron.CIP | 40.0 | 88.0 | 92.0 |
| Rockwell.CIP | 0.0 | 79.2 | 89.6 |
| OPC.UA | 63.2 | 88.6 | 94.3 |

(Cells are drop%, lower is better; 0.0 = the combination is supported. **Note**: cells on the
boundary (frames × RTT ≈ scan interval) are bimodal between 0 and a higher level — see "Cells sitting
on the boundary are bimodal" above.)

**Three conclusions:**

1. **Once RTT reaches 10–20ms, frame count is the only bottleneck — CPU is not remotely the limit.**
   At 10 devices × 5000 points the absolute throughput is only 50–60k records/s and the gateway CPU
   sits mostly idle, yet drop already runs 64%–94%: the **per-device cycle = frames × RTT** has
   exceeded the scan interval, so polls are skipped by the in-flight guard. Fewer points per device
   or a slower scan interval are the only fixes.
2. **scan=1000ms is the dividing line**: at RTT=20ms only protocols with ≤40 frames (Modbus 40,
   S7 13, Rockwell 40) still hold 0% drop; MC/FINS at 50 frames drop 40%, CIP at 80 frames drops
   40%, and OPC.UA drops 63.2% because the fake server adds serial dispatch queueing on top.
3. **Fewer frames means more headroom when tightening the scan**: S7 (13 frames) only drops 74.4%
   at scan=100ms, while MC/FINS (50 frames) already drop 90.8% there. This is "frame count decides
   capacity" stated as a measurement.

### Per-protocol detail

(drop% per RTT, plus the real plugin-build throughput at RTT=0.)

#### ModBus.TCP

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 0.0 | 0.0 | 26k |
| 500 | 100ms | 0.0 | 0.0 | 13.0 | 51k |
| 2000 | 1000ms | 0.0 | 0.0 | 0.0 | 24k |
| 2000 | 200ms | 0.0 | 0.0 | 48.0 | 104k |
| 2000 | 100ms | 0.0 | 48.0 | 73.6 | 204k |
| 5000 | 1000ms | 0.0 | 0.0 | 0.0 | 60k |
| 5000 | 200ms | 0.0 | 65.6 | 79.7 | 260k |
| 5000 | 100ms | 0.0 | 80.0 | 89.0 | 510k |

At RTT=0 even 10 devices × 5000 points @100ms holds **0% drop** (510k records/s) — the lightest
plugin overhead of the set. At a 100ms scan the safe per-device ceiling (RTT=0) is 2000 points.

#### Mitsubishi.MC.TCP

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 5k |
| 500 | 200ms | 0.0 | 0.0 | 0.0 | 25k |
| 500 | 100ms | 0.0 | 0.0 | 48.0 | 51k |
| 2000 | 1000ms | 0.0 | 0.0 | 0.0 | 24k |
| 2000 | 200ms | 0.0 | 48.0 | 64.0 | 104k |
| 2000 | 100ms | 0.0 | 64.6 | 78.0 | 204k |
| 5000 | 1000ms | 0.0 | 0.0 | 40.0 | 60k |
| 5000 | 200ms | 6.8 | 73.6 | 81.6 | 233k |
| 5000 | 100ms | 48.7 | 83.6 | 90.8 | 257k |

1.25× Modbus's frame count (100 words vs 125 registers), and it saturates first even at RTT=0:
5000 points @200ms drops 6.8%, @100ms drops 48.7%. Raising `maxReadWords` (cap 960) takes 5000
points from 50 frames to 6 — the only frame-count lever this protocol has.

#### Omron.FINS.TCP

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 0.0 | 0.0 | 25k |
| 500 | 100ms | 0.0 | 0.0 | 48.0 | 51k |
| 2000 | 1000ms | 0.0 | 0.0 | 0.0 | 20k |
| 2000 | 200ms | 0.0 | 48.0 | 64.0 | 104k |
| 2000 | 100ms | 0.0 | 64.8 | 78.0 | 204k |
| 5000 | 1000ms | 0.0 | 0.0 | 40.0 | 60k |
| 5000 | 200ms | 0.0 | 73.6 | 81.6 | 260k |
| 5000 | 100ms | 48.7 | 84.7 | 90.8 | 256k |

Same 100 words/frame as MC; the two curves coincide within measurement noise.

#### Siemens.S7

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 0.0 | 0.0 | 25k |
| 500 | 100ms | 0.0 | 0.0 | 0.0 | 50k |
| 2000 | 1000ms | 0.0 | 0.0 | 0.0 | 24k |
| 2000 | 200ms | 0.0 | 0.0 | 0.0 | 104k |
| 2000 | 100ms | 0.0 | 0.0 | 48.0 | 204k |
| 5000 | 1000ms | 0.0 | 0.0 | 0.0 | 50k |
| 5000 | 200ms | 0.0 | 48.0 | 56.0 | 260k |
| 5000 | 100ms | 0.0 | 65.6 | 74.4 | 510k |

The most frame-efficient tier (byte area, 462 bytes/frame — 5000 points is just 13 frames), so it is
also **the most RTT-tolerant**: at RTT=20ms it still holds 0% drop at scan=1000ms, and at scan=200ms
it is the only protocol that still sustains 2000 points/device (0% vs 48%–78% for the rest).

#### Omron.CIP (driver default `maxTagsPerRequest=64`, i.e. batch read ON)

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 0.0 | 0.0 | 26k |
| 500 | 100ms | 0.0 | 0.0 | 48.0 | 51k |
| 2000 | 1000ms | 0.0 | 0.0 | 0.0 | 20k |
| 2000 | 200ms | 0.0 | 48.0 | 72.0 | 100k |
| 2000 | 100ms | 0.0 | 74.0 | 83.0 | 204k |
| 5000 | 1000ms | 0.0 | 0.0 | 40.0 | 50k |
| 5000 | 200ms | 0.0 | 76.0 | 88.0 | 260k |
| 5000 | 100ms | 0.0 | 88.0 | 92.0 | 510k |

> **The driver default contradicts its own docs (left for you to decide).** `DefaultCIPConfig` in
> `omroncipdrv/config.go` defaults `MaxTagsPerRequest` to **64 (batch reads ON)**, while the comment
> in that file and this README previously said "default 0 = one tag per round-trip, zero regression".
> This run measures **the code's actual behaviour** (batch 64): 5000 points produced 80 frames,
> which is exactly what a batch of 64 predicts. Driver behaviour was **not** changed — only the
> incorrect comment in `config.go` was corrected.

**Single-read comparison (`-cip-batch 1`, one frame per tag)** — what batching is worth:

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 |
|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 60.0 | 80.0 |
| 500 | 200ms | 0.0 | 92.0 | 96.0 |
| 500 | 100ms | 0.0 | 96.0 | 98.0 |
| 2000 | 1000ms | 0.0 | 80.0 | 80.0 |
| 2000 | 200ms | 0.0 | 96.0 | 96.0 |
| 2000 | 100ms | 64.4 | 98.0 | 98.0 |
| 5000 | 1000ms | 0.0 | 80.0 | 80.0 |
| 5000 | 200ms | 65.6 | 96.0 | 96.0 |
| 5000 | 100ms | 83.6 | 98.0 | 100.0 |

In single-read mode frames = points (5000 points → 5000 frames), and anything above zero RTT
collapses immediately — from RTT=10ms even 500 points drops 60%. **Batch reads are a prerequisite
for CIP, not an optimization.**

#### Rockwell.CIP (array tags `Arr[i]`, merged into ≤125-element frames)

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 0.0 | 0.0 | 26k |
| 500 | 100ms | 0.0 | 0.0 | 0.0 | 51k |
| 2000 | 1000ms | 0.0 | 0.0 | 0.0 | 24k |
| 2000 | 200ms | 0.0 | 0.0 | 48.0 | 104k |
| 2000 | 100ms | 0.0 | 48.8 | 74.0 | 200k |
| 5000 | 1000ms | 0.0 | 0.0 | 0.0 | 60k |
| 5000 | 200ms | 0.0 | 64.0 | 79.2 | 260k |
| 5000 | 100ms | 0.0 | 82.0 | 89.6 | 510k |

With a regular array layout it is on par with Modbus (40 frames). **This table covers the array
layout only**; the standalone-tag layout (`Tag_i`) costs one frame per point — the worst case, on the
order of the CIP single-read table above. Production configs should plan contiguous points as
`Base[N..N+k]` array tags.

#### OPC.UA (`maxBatch` 100 nodes per ReadRequest)

| Points/dev | Scan | RTT 0 | RTT 10 | RTT 20 | Actual rec/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 0.0 | 38.4 | 26k |
| 500 | 100ms | 0.0 | 46.6 | 60.2 | 50k |
| 2000 | 1000ms | 0.0 | 0.0 | 16.0 | 20k |
| 2000 | 200ms | 0.0 | 68.8 | 78.4 | 104k |
| 2000 | 100ms | 0.0 | 82.4 | 89.2 | 200k |
| 5000 | 1000ms | 0.0 | 40.0 | 63.2 | 60k |
| 5000 | 200ms | 0.0 | 83.4 | 88.6 | 260k |
| 5000 | 100ms | 16.8 | 91.0 | 94.3 | 416k |

Same 50 frames as MC/FINS, but **consistently worse drop** (at RTT=20ms/5000 points MC drops 40%
while OPC.UA drops 63.2%). The cause is the fake server: gopcua's in-process server dispatches
messages serially across all connections, so with multiple devices plus RTT the transactions **queue
on the server side**. Real OPC UA servers normally handle concurrent connections, so field drop
should be better than this table; the capacity formula (frames × RTT < scan interval) still holds.

#### DLT645.TCP (DL/T 645 electricity meters)

DL/T 645 differs from every other protocol here in one fundamental way: **the frame count is driven by
`maxDIsPerRead`, not by address layout or a merge window** (frames = ⌈points / maxDIsPerRead⌉; 5000 points
/ 12 = 417 frames). Its factory default is **12 (the protocol ceiling)**, and on TCP `interFrameDelayMs`
defaults to **0** (the 30ms wait is an RS-485 transceiver-turnaround allowance; on a pass-through link the
serial server / DTU handles that itself — serial keeps 30, see the notes below).

**Driver defaults** (`maxDIsPerRead=12`, TCP `interFrameDelayMs=0`):

| points/dev | scan | RTT 0 | RTT 10 | RTT 20 | records/s (RTT=0) |
|---|---|---|---|---|---|
| 500 | 1000ms | 0.0 | 0.0 | 0.0 | 6k |
| 500 | 200ms | 0.0 | 64.0 | 76.0 | 25k |
| 500 | 100ms | 0.0 | 78.0 | 88.0 | 51k |
| 2000 | 1000ms | 0.0 | 40.0 | 60.0 | 24k |
| 2000 | 200ms | 0.0 | 88.0 | 92.0 | 104k |
| 2000 | 100ms | 0.0 | 94.0 | 96.0 | 204k |
| 5000 | 1000ms | 0.0 | 68.0 | 80.0 | 50k |
| 5000 | 200ms | 13.8 | 93.6 | 96.0 | 215k |
| 5000 | 100ms | 0.0 | 96.8 | 98.0 | 505k |

**RTT tolerance is the worst of any protocol here in its default configuration**: the frame count is driven
solely by `maxDIsPerRead`, and 12 is already the protocol ceiling, so 5000 points is 417 frames — **8.3×**
MC / FINS's 50, and RTT tolerance is correspondingly 8× worse (at 5000 points / 1000ms, MC / FINS drop 0%
at RTT=10ms where 645 drops 68%; at RTT=20ms it is 40% against 80%). The capacity formula
(frames × RTT < scan interval) holds here too — it is just that 645's frame count pushes that line very
low: 500 points / 42 frames needs 840ms at RTT=20ms, so the 200ms tier necessarily drops (76.0%
measured); 5000 points / 417 frames needs 4.2s even at RTT=10ms, hence the 68% drop at the 1000ms tier.

> ⚠️ **The 5000-point / 200ms cell is not monotonic** (13.8% measured, while the tighter 100ms tier shows
> 0.0%). The driver is not the limit: `BenchmarkReadTCP` (`driversrc/dlt645/dlt645drv`, 5000 points / 12 =
> 417 frames) measures 21µs per round trip at 47,482 frames/s, so 417 frames take 8.7ms — far below any
> scan interval. This cell looks like a tick-alignment sampling artifact in the collector at that
> frequency; **do not infer driver capacity from it**. Use the frames × RTT formula or that benchmark
> instead.

**Notes for serial (`DLT645.Serial`)**:

- `maxDIsPerRead=12` is also the serial default.
- **`interFrameDelayMs` stays at the default 30ms** — it is the transceiver-turnaround allowance, and the
  fake meter here is TCP-based and **cannot model RS-485 turnaround**, so the zero-delay the table above
  was measured with does not transfer to a direct serial port. To lower it on site, step down while
  checking that frames × measured per-frame period stays below the scan interval, and watch for dropped
  frames.
- Serial's Drain only performs the read when the previous round did not end cleanly: an empty serial read
  blocks for `serialReadTimeout` (20ms), and that 20ms times the frame count is pure overhead too.

##### Serial capacity (no serial hardware needed — measured with `-serial-baud`)

**Measured per-frame round trip** (1 device, `maxDIsPerRead=12`, 4 data bytes per identifier, 8E1; using
`scan=100ms` makes **the round duration the only constraint**, so `per frame = round ÷ frames`, with a
quantization error of 0.1s/15s ≈ 0.7%):

| baud | points | frames | round | **measured per frame** | model per frame | delta |
|---|---|---|---|---|---|---|
| 2400 | 200 | 17 | 14.00s | **823.5ms** | 820ms | +0.4% |
| 9600 | 800 | 67 | 16.33s | **243.8ms** | 242.5ms | +0.5% |
| 115200 | 3000 | 250 | 17.00s | **68.0ms** | 66.0ms | +3.0% |

The model is line time + meter processing time. Line time = frame bytes ÷ baud, where a DL/T 645-2007
read frame is `12 + 4N` (request) + `12 + N×(4 + data bytes per identifier)` (response) = 168 bytes at
N=12; meter processing is taken as 50ms (see "Measurement inputs" below). All three are within 3%, the
excess being local queueing overhead (1–2ms per frame).

**Point ceiling per meter** (leaving 30% headroom on `frames × frame period ≤ scan interval × 0.7`):

| baud | scan=10s | scan=30s | scan=60s |
|---|---|---|---|
| 2400 | **~96 points** | ~300 points | ~610 points |
| 9600 | **~336 points** | ~1030 points | ~2060 points |
| 115200 | **~1220 points** | ~3700 points | ~7400 points |

The boundary was cross-checked with 10 devices × scan=10s: 100 / 300 / 1500 points all showed 0% drop,
and doubling them (200 / 800 / 3000) dropped across the board, consistent with the table. The specific
drop% values from that pair of runs are not worth quoting — the readings go bimodal when the round
duration is on the order of the measurement window (see the boundary warning near the top).

**Diminishing returns from raising the baud rate** (breaking one frame down):

| baud | line time | meter processing (assumed 50ms) | line share |
|---|---|---|---|
| 2400 | 770ms | 50ms | **94%** |
| 9600 | 192ms | 50ms | 79% |
| 115200 | 16ms | 50ms | **24%** |

The crossover is around **38400bps** (168 bytes × 11 bits ÷ 50ms ≈ 37kbps) — above it the line time is
already below the meter's processing time, so more baud buys almost nothing. 2400 → 9600 yields 3.4×,
while 9600 → 115200 yields only 3.6× (it should be 12×). **If a meter runs at 2400bps, going to
9600/19200 is the big win; confirm how fast the meter itself answers before going to 115200.**

**Measurement inputs (required before quoting the numbers above)**:

- **The 50ms meter processing time is an assumption, not a measurement** — a synthetic fake cannot
  produce it. It is the one quantity that needs calibrating against a real meter, and the higher the baud
  rate, the more it dominates the result (76% of each frame at 115200). To calibrate: take a real meter
  and see how much the per-frame measurement exceeds the pure line time — that difference is it.
- Each identifier is assumed to carry **4 data bytes** (the `%08X:4:2` address convention). Real meters
  use 2 / 3 / 4 bytes depending on the DI; one byte less is 12 bytes less per frame — 55ms per frame at
  2400bps.
- **8E1** (11 bits/byte); 8N1 would be about 9% faster.
- Packing 12 identifiers is still 2.6× faster than reading one at a time (64ms vs 165ms per point), so
  batching is worth even more on serial than on TCP.
- **So for serial, only the frame-count model of the TCP table above carries over; its absolute numbers do
  not.** If a site really has a thousand-point meter on 2400bps, no driver parameter will save it — raise
  the baud rate or split the meter.

**Reproducing**:

```bash
# 1 device + scan=100ms: the round becomes the only constraint, so per frame = round ÷ frames
go run ./cmd/loadtest -protocol dlt645 -devices 1 -points 200 -scan 100 \
  -warmup 20 -run 60 -serial-baud 2400 -latency 50
```

`-serial-baud` derives the line time from the frame size and **adds** it to `-latency` (which is left for
meter processing time), printing the derivation for review:

```
serial modeled: 2400bps 8E1 (11 bits/byte) × 168 bytes/frame = 770ms line time (+ -latency 50ms = 820ms per frame)
```

The frame size **varies with the packing count**, so `-dlt645-dis` changes the line time too; without it
the driver's default of 12 is used. Scale `-run` / `-warmup` to the round duration — at low baud rates a
single round takes tens of seconds, so the default 2s/5s will not even complete one.

> ⚠️ **Do not use a virtual port pair** (com0com / socat) to load-test serial: those move bytes at memory
> speed and **do not model baud-rate timing**, so the per-frame time comes out near zero and the capacity
> you get is wildly optimistic — worse than not testing at all.

> Note also that the `cycleMs` column cannot be used for fine-grained 645 analysis: it is derived from
> `DeviceLastSuccessTime`, which is a **second-resolution** string, so periods below 1s are flattened to
> multiples of a second. The serial rounds measured above are all above 14s, so they are unaffected; the
> TCP table's drop% and records/s columns are direct counts and are likewise unaffected.

### Aggregate tier: 50 devices × 5000 points (250k points), RTT=0

Measures the **CPU/throughput ceiling** (no network latency):

| Protocol | Scan | Expected rec/s | Actual rec/s | drop% |
|---|---|---|---|---|
| ModBus.TCP | 1000ms | 250k | 300k | 0.0 |
| ModBus.TCP | 200ms | 1.25M | 1.05M | 15.7 |
| ModBus.TCP | 100ms | 2.50M | 987k | 60.5 |
| Mitsubishi.MC.TCP | 1000ms | 250k | 250k | 0.0 |
| Mitsubishi.MC.TCP | 200ms | 1.25M | 799k | 36.1 |
| Mitsubishi.MC.TCP | 100ms | 2.50M | 746k | 70.1 |
| Omron.FINS.TCP | 1000ms | 250k | 300k | 0.0 |
| Omron.FINS.TCP | 200ms | 1.25M | 727k | 41.8 |
| Omron.FINS.TCP | 100ms | 2.50M | 751k | 70.0 |
| Siemens.S7 | 1000ms | 250k | 300k | 0.0 |
| Siemens.S7 | 200ms | 1.25M | 829k | 33.7 |
| Siemens.S7 | 100ms | 2.50M | 788k | 68.5 |
| Omron.CIP | 1000ms | 250k | 250k | 0.0 |
| Omron.CIP | 200ms | 1.25M | 620k | 50.4 |
| Omron.CIP | 100ms | 2.50M | 671k | 73.2 |
| Rockwell.CIP | 1000ms | 250k | 250k | 0.0 |
| Rockwell.CIP | 200ms | 1.25M | 829k | 33.7 |
| Rockwell.CIP | 100ms | 2.50M | 789k | 68.5 |
| OPC.UA | 1000ms | 250k | 300k | 0.0 |
| OPC.UA | 200ms | 1.25M | 452k | 63.9 |
| OPC.UA | 100ms | 2.50M | 455k | 81.8 |
| DLT645.TCP (driver defaults) | 1000ms | 250k | 300k | 0.0 |

**Boundary**: on this machine the plugin build peaks at roughly **1.05M records/s** (Modbus,
50×5000 @200ms, at the cost of 15.7% drop); the ceiling at 0% drop is about **300k records/s**
(50×5000 @1000ms). The historical in-process baseline claimed ~2M records/s at 0% drop — **different
matrices, so do not compare cell by cell**, but the plugin build's peak is roughly half of it, and
the gap is the expected order of magnitude for "one extra plugin RPC plus two rounds of point
encode/decode per device per cycle". Plan field capacity from this table, not from the old numbers.

### Optimization directions
1. **Frame count decides point capacity**: Modbus 125 registers/frame, MC 100 words/frame (tunable to 960), FINS 100 words/frame,
   S7 462 bytes/PDU, CIP 64 tags/frame (batched, tunable), Rockwell 125 elements/frame (arrays), OPC UA 100 nodes/frame.
   At equal point counts frame counts differ by 1–2 orders of magnitude and capacity scales linearly with frames.
   The payoff of CIP batching is now quantified: 5000 points go from 5000 frames to 80, and drop at RTT=10ms goes from
   80% to 0% (see the single-read table above) — **batching is a prerequisite for CIP, not an optimization**.
   Sparse layouts (gaps > merge window) magnify frame count by the point count — production configs should use contiguous/regular addresses wherever possible.
   **DL/T 645 is the most extreme case of the same rule**: frame count is driven solely by `maxDIsPerRead`, which defaults to 12 (the
   protocol ceiling), so 5000 points/device @1s is 417 frames and 50 devices × 5000 points (250k points) measured 300k records/s at 0.0%
   drop. See the DLT645.TCP section.
2. **The config watcher's full-table checksum is the scaling bottleneck**: `collector.Engine.loadConfigChecksum` runs every 10s
   `SELECT * FROM device_address WHERE status=1` (full-table load + hash). At 1M addresses a single pass takes 1.7s and several GB of RAM,
   directly conflicting with high-point scenarios. For massive point counts, switch to incremental/db-change awareness or lengthen the check interval.
3. **High-throughput drops are a CPU ceiling, not the worker pool**. Verified in benchmarking: the `worker pool full` log never fired at ≤200 devices
   (queue 1024 ≫ max backlog); raising workers from 32 to 64 actually worsened drop from 16.1% to 19.4% (CPU already saturated; more concurrency just adds context switching). Profiles show hotspots in driver decode/format + record construction + GC pressure. `collector.toRecords` was optimized
   (two map lookups per record → index-aligned direct access; measured toRecords CPU 13.7% → 1.7%);
   further candidates: driver-side formatting (skip re-formatting unchanged values) and per-round `RangeSig` cache-key recomputation.
   **[Unverified]** That worker-count experiment was run with **in-process drivers**. Under pluginization the hotspot set gains the
   plugin RPC's request/response encode/decode and IPC; the 237 runs in this baseline **did not repeat it**, so "more workers makes it worse"
   is still unconfirmed on the new architecture and should be treated as qualitative for now.
