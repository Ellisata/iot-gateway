# iot-gateway · Industrial IoT Data Gateway

English | [简体中文](README.md)

[![CI](https://github.com/Ellisata/iot-gateway/actions/workflows/ci.yml/badge.svg)](https://github.com/Ellisata/iot-gateway/actions/workflows/ci.yml)
![License](https://img.shields.io/badge/license-AGPL--3.0-blue)
[![Release](https://img.shields.io/github/v/tag/Ellisata/iot-gateway?label=release&sort=semver&color=green)](https://github.com/Ellisata/iot-gateway/releases)
![Go](https://img.shields.io/badge/go-1.26-00ADD8)

**One executable + a `plugins/` directory is all it takes to poll PLCs on an industrial PC and push data to MQTT / TDengine / InfluxDB — no database, no runtime to install.**

Polls PLCs and industrial devices on a schedule, ships with an embedded web admin console, and deploys as a Windows service, a Linux systemd unit or a Docker container.

<!-- Admin console tour: an animated GIF -- the only form that actually moves
     on Gitee/GitHub. The video tag is sanitized away, and the raw URL sends
     Content-Disposition: attachment, which downloads instead of playing.
     The GIF is a highlight reel of the demo -- login, dashboard, protocols,
     device objects, dynamic form, channels, logs, open API docs.
     Clicking plays the video from image.edwining.com: the repo's own raw URL
     sends Content-Disposition: attachment, which only downloads, whereas
     this host omits the header and supports Range, so the browser plays it. -->
![Admin console](docs/images/demo.en.gif)

## Why iot-gateway

- **Single binary, zero external dependencies** — the frontend is embedded via `go:embed` and SQLite is pure Go (CGO-free); the binary plus `plugins/` is the whole system.
- **Process-isolated protocol drivers** — all 15 protocols run as separate `plugins/` processes: crashes restart with backoff, wedged processes are recycled, and replacing a binary hot-upgrades the driver without touching the gateway.
- **Built for the field** — a persistent local outbox with automatic replay, change-only push, zero-downtime config reload, device/point scanning, and reverse MQTT ingestion into time-series databases.

## Try it in 5 minutes

1. **Download** the release for your platform from [Gitee Releases](https://gitee.com/wang4856304/iot-gateway/releases) or [GitHub Releases](https://github.com/Ellisata/iot-gateway/releases) (Windows amd64, Linux amd64 / arm64).
2. **Extract** and keep the `iot-gateway` binary and the `plugins/` directory **side by side**.
3. **Run**:

   ```bash
   ./iot-gateway        # on Windows, double-click iot-gateway.exe
   ```

4. **Open the console** at `http://<host-ip>:9082/admin` — default account `admin / hand@123`; **change it immediately after the first login**.
5. **Add your first device** under Device Objects and save; data appears on the dashboard. Add an MQTT / TDengine / InfluxDB channel under Channel Management to push it downstream.

> ⚠️ With `plugins/` missing or empty the gateway still starts and lets you log in, but **it cannot collect a single point**. Release bundles ship with the plugins — do not copy the binary alone.
>
> No PLC at hand? Use the fake PLCs in [`backend/cmd/loadtest`](backend/cmd/loadtest/README.en.md) to exercise the full acquisition chain.

## Scope

| Covered | Not yet covered (issues welcome) |
|---|---|
| Small/medium production lines, single-node installs, Windows industrial PCs | IEC 60870-5-104 / IEC 61850 / BACnet / CAN |
| Siemens, Mitsubishi, Omron, Rockwell, OPC UA | Kafka / RabbitMQ / MySQL / PostgreSQL downstreams |
| DL/T 645 meters, ABB robots | Clustering / HA, rule engine, central fleet management |

**License**: the gateway core and official drivers are AGPL-3.0; the driver SDK is published separately under Apache-2.0. Internal deployment and integration with your own systems via Open API / MQTT require no source disclosure; OEM or redistribution needs a commercial license — see [License & FAQ](#license--faq) and [LICENSING.md](LICENSING.md).

## Features

- **Multi-protocol acquisition**: 15 protocol/transport drivers covering Modbus, Siemens S7, Mitsubishi MC, Omron FINS/CIP, Rockwell CIP, OPC UA, ABB robot RWS and DL/T 645 electricity meters — with batch register reads and configurable byte/word order.
- **Dynamic form driven**: protocol parameters are stored in the database as dynamic form schemas (JSON), so new protocols require no frontend changes.
- **Out-of-process protocol plugins**: **all 15 protocol drivers are plugins** — the gateway binary contains no protocol implementation at all. Each driver is an `iot-driver-<name>` standalone executable dropped into the `plugins/` directory next to the binary to be **loaded automatically — no gateway rebuild, no downtime**; crashed plugin processes restart with backoff, wedged ones are detected and recycled, and replacing a binary hot-upgrades the plugin. See the [driver plugin guide](docs/driver-plugins.md) (中文).
- **Acquisition engine**: worker-pool scheduled polling tasks; a background watcher detects configuration changes and applies **zero-downtime hot reload**.
- **Push engine**: device-grouped batch dispatch; channel configs hot-reload; on network outage data lands in a **persistent local outbox** and is automatically re-sent once the connection recovers — no data loss.
- **MQTT subscription into time-series databases**: subscribes to JSON messages on an external MQTT broker, maps them to collection records and writes them to enabled TDengine / InfluxDB channels — with wildcard and comma-separated multi-topics, QoS, authentication, sample-message preview, connectivity testing, a choice of built-in mapping DSL or a goja JS parser, and hot reload of subscription config.
- **Disconnection alarms**: automatic detection of device poll failures and channel disconnections (debounce + state machine), recorded on offline/recovery edges, queryable in a unified alarm center.
- **Web admin console**: dashboard, device objects, address tags, protocol management, channel management, alarms, log viewer and API key management — bilingual (EN/中文) UI.
- **Open API**: read-only `/openApi/*` endpoints authenticated by an `X-Api-Key` header, for MES / ERP and other external systems.
- **Single-binary deployment**: frontend embedded via `go:embed`, SQLite implemented in pure Go (CGO-free). The binary plus a `plugins/` directory is the whole system; the binary **links no protocol library at all** (protocol libraries ship with the plugins), so its dependency surface is much smaller. Can be registered as a Windows service or Linux systemd service with auto-start and crash restart.

## Architecture

```
              ┌─────────────────── single iot-gateway binary ───────────────────┐
              │                                                                 │
  PLC / device ◀──▶ collector engine ──▶ driver contract ───RPC──▶ plugins/    │
              │   (worker pool /      (registry / type            (separate   │
              │    hot reload)         tables)                    processes)  │
              │      │                                                          │
              │      ▼ RecordSink                                               │
              │  push engine ──────┬──▶ MQTT broker                             │
              │   (channel hot     ├──▶ TDEngine v3                             │
              │    reload)         └──▶ InfluxDB v3                             │
              │      │ offline                                                  │
              │      ▼                                                          │
              │  SQLite outbox local buffer (replay on recovery)                │
              │                                                                 │
              │  disconnection alarms · web console (/admin) · Open API         │
              └─────────────────────────────────────────────────────────────────┘
                                  ▲
                     ┌────────────┴──────────────┐
                     │  plugins/iot-driver-*     │  ← separate processes,
                     │  (Modbus/S7/MC/FINS/CIP/  │    one plugin per protocol;
                     │   OPC UA/ABB RWS ...)     │    one RPC round-trip per read
                     └───────────────────────────┘
```

> Protocol drivers run in **separate processes** (the `iot-driver-*` binaries under `plugins/`) and are invoked over RPC, adding one inter-process round-trip per read/write. No protocol implementation lives inside the binary.

## Supported Protocols

15 protocol names in total, **all provided by out-of-process plugins under `plugins/`** (protocol names and type scopes are unchanged from previous releases, so **existing device configs, `iot_protocol` rows and frontend forms need no migration**):

| Protocol | Transport | Typical devices | Providing plugin |
|---|---|---|---|
| `ModBus.RTU` / `ModBus.TCP` | Serial / Ethernet | Modbus-capable PLCs, meters, VFDs | `iot-driver-modbus` |
| `Siemens.S7` | Ethernet | Siemens S7 series PLCs | `iot-driver-s7` |
| `Mitsubishi.MC.TCP` / `Mitsubishi.MC.Serial` | Ethernet / Serial | Mitsubishi MC-protocol PLCs | `iot-driver-mitsubishi` |
| `Omron.FINS.UDP` / `.TCP` / `.Serial` / `.HostLinkTCP` | UDP / TCP / Serial | Omron FINS-protocol PLCs | `iot-driver-fins` |
| `Omron.CIP` | EtherNet/IP | Omron CIP devices | `iot-driver-omroncip` |
| `Rockwell.CIP` | EtherNet/IP | Rockwell (Allen-Bradley) ControlLogix / CompactLogix | `iot-driver-rockwellcip` |
| `OPC.UA` | Ethernet | OPC UA capable devices and gateways | `iot-driver-opcua` |
| `ABB.RWS` | Ethernet (HTTP/HTTPS) | ABB robot controllers (IRC5 / OmniCore) | `iot-driver-abb` |
| `DLT645.Serial` / `DLT645.TCP` | Serial (RS-485/RS-232) / Ethernet | DL/T 645 multifunction electricity meters | `iot-driver-dlt645` |

> `ABB.RWS` polls ABB's **Robot Web Services** REST API for IO signals, RAPID variables, controller panel state, joint/cartesian pose and system information. It auto-detects RWS 1.0 (IRC5/RW6, Digest auth) and RWS 2.0 (OmniCore/RW7, Basic auth). Address syntax lives in the plugin source, e.g. `signal:Local/DRV_1/DI1`, `rapid:T_ROB1/Mod1/nTest`, `panel:ctrlstate`, `motion:ROB_1/joint/rax_1`, `system:rwversion`.

> `DLT645.Serial` / `DLT645.TCP` collect DL/T 645-2007 and 1997 multifunction electricity meters (voltage, current, power, power factor, energy, meter status words) over RS-485 or through a serial server / DTU. A point address *is* the **data identifier**: the built-in dictionary covers common items such as A-phase voltage `02010100` and total combined active energy `00000000`, while vendor-private identifiers are declared explicitly as `DI:bytes:decimals[:flags]` (e.g. `12345678:2:0:s`). Byte length, decimal places and sign come from the data identifier itself rather than from the type dropdown — the dropdown only selects the output form. For TCP, `host` is the serial server's address and `port` its transparent port.

### Plugin directory and loading

- Each plugin builds to `iot-driver-<dir-name>` (plus `.exe` on Windows) and lives in the `plugins/` directory **next to the executable**.
- The gateway scans it using `plugin.dir` (default `plugins`; an empty or relative path resolves **relative to the executable's directory**, not the working directory) and `plugin.glob` (default `iot-driver-*`). Plugins are **loaded at startup, and ones added while running are picked up automatically**.
- `plugin.enabled` **ships as `true`**: all protocols come from plugins, so turning it off means the gateway supports **no protocol at all** (devices are skipped silently with a single WARN line).
- Plugin sources live in the standalone [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers) repository: `drivers.version` pins the driver tag and `drivers.txt` lists the drivers to ship; `build.sh` / `release.sh` / `backend/Dockerfile` fetch their sources with `go get` and cross-compile them into the release archive (**if not a single plugin is installed, the build fails** — it never produces a protocol-less package).
- Loading a plugin means **executing the programs in that directory with the gateway's own OS privileges** — plugins are trusted code, not sandboxed. Ensure the directory is writable only by administrators; see [SECURITY.md](SECURITY.md).

> For measured per-protocol capacity (devices × points) and tuning advice, see the [capacity load-test report](backend/cmd/loadtest/README.en.md). Since 2026-09 every measurement there is taken on the **plugin build** (each read/write costs one gateway ↔ plugin RPC round-trip, and the readings already include it), covering all 7 protocols across scan 1000/200/100ms × RTT 0/10/20ms, plus a dedicated DL/T 645 section (`ABB.RWS` was not part of this benchmark); the frame-count model and tuning conclusions are in the [capacity testing findings](backend/specs/capacity-testing.en.md).

## Supported Push Channels

| Channel | Notes |
|---|---|
| `mqtt` | Eclipse Paho MQTT client — QoS, TLS, last-will, etc. |
| `http` | HTTP/Webhook — POST/PUT/PATCH, Bearer / Basic / HMAC-SHA256 auth, custom headers, offline spool and replay |
| `tdengine-v3` | TDEngine 3.x via the official driver-go WebSocket driver (CGO-free, connects through taosAdapter) |
| `influxdb-v3` | InfluxDB 3.x via a self-built Line-Protocol-over-HTTP client (no heavy official SDK) |

> Channels are now **type + instance name**: multiple instances of the same type can coexist (e.g. two HTTP channels for MES and ERP).
>
> Message-based channels (`mqtt` / `http`) support two payload formats: `native-json` (default, contract unchanged) and `mapping-json` (customer-defined outbound mapping reusing the subscription vocabulary `path` / `const` / `template` / `map` / `transform`). The console provides payload preview and test send; see [Push channels & outbound mapping](backend/docs/push-channels.md) (中文).


## MQTT Subscription into Time-Series Databases

Besides "collect → push", the gateway can also **ingest in the opposite direction**: subscribe to JSON messages on an external MQTT broker, map them into records identical to locally collected ones, and write them to enabled **TDengine / InfluxDB** channels. When devices or third-party platforms already publish data over MQTT, this funnels that data into the same time-series database.

```
External device / platform ──MQTT──▶ Broker ──subscribe──▶ subscription engine (JSON → record) ──▶ push engine ──▶ TDengine / InfluxDB
```

- **Time-series channels only**: records are delivered only to `tdengine-v3` / `influxdb-v3`, **never to plain channels such as MQTT** — delivering back to MQTT would self-excite when the subscribed topic is also a publish topic. Converted records reuse the push channels' batching, offline outbox cache and replay.
- **Each subscription has its own connection**: broker / port / client ID / username & password / topic (`+` and `#` wildcards supported; multiple topics separated by commas share the mapping and QoS) / QoS `0`–`2` are configured per subscription. Before saving you can **test the connection** and **preview** the mapping against a sample message (neither is persisted).
- **Parsers**: each subscription uses either the built-in mapping DSL or a JS script. The script runs `function parse(msg, ctx)` under goja for payloads the DSL cannot express (per-message 1–500 ms timeout with VM rebuild; no file, network or `require` access) and is edited in the console's parser editor. Saving parser-only changes **swaps the parser in place without reconnecting MQTT**. Contract: [parser-plugins.md](backend/specs/parser-plugins.md) (中文), authoring guide: [parser-scripts.md](backend/docs/parser-scripts.md) (中文).
- **Mapping rules**: `root` selects the value root (default `$`; a top-level array is split into one record per element automatically, or write `$.data[*]` explicitly); target fields are a whitelist of collection-record fields (device ID / device name / point ID / point description / value / kind / quality, etc. — unknown fields are rejected on save); each field supports `path` (JSON path), `const` (fixed value), `template` (string interpolation) and `map` (value translation for external type and quality codes), plus an optional `transform` operator chain (`scale` / `round` / `trim` / `default` / `hexDecode` / `base64Decode` / `bitfield` / `regex`); `topicPattern` extracts named segments from the topic (with `source: "topic"`), `fanout` expands one message into multiple point records, and `filter` holds implicit-AND conditions (`eq` / `neq` / `gt` / `gte` / `lt` / `lte` / `contains` / `in` / `notin` / `exists` / `notexists`).
- **Collection time**: a timestamp carried by the message can be mapped to `collectedAt` (epoch seconds / milliseconds / microseconds / nanoseconds and common date formats, normalized to local time at millisecond precision). When absent or unrecognized, the batch arrival time is used.
- **Device / point metadata**: the time-series database stores only IDs and values, so devices and points seen in subscribed messages are asynchronously indexed by the natural key `(deviceId, deviceAddressId)` on the "Subscription Devices & Points" page (no sampled values), for name-based lookup and later analytics. The page also supports manual maintenance: create / edit / Excel import (with per-row errors) / template download; a source column distinguishes "auto" from "manual", manual rows are never overwritten by later automatic indexing, and they can be batch-deleted (≤200, manual rows only) or restored to "auto".
- Subscription configs are checksum-compared and hot-reloaded every 10 seconds, so create / update / delete / enable / disable needs **no gateway restart**. The runtime status view shows each subscription's connection state, message / record / skipped / dropped / error counts and last error.

## Quick Start

### Prerequisites

- Go ≥ 1.26 and Node.js ≥ 22 (only needed for development/building; running in production needs only the binary)

### 1. Development mode (split frontend/backend, hot reload)

```bash
# Terminal 1: backend (port 9082)
cd backend && go run .

# Terminal 2: frontend (port 4000)
cd frontend && npm install && npm run dev
```

Or from VS Code: Terminal → Run Task → `dev` to start both in parallel.
Open `http://localhost:4000` — requests are proxied by Vite to the backend on 9082.

### 2. Production build (single binary)

```bash
./build.sh            # Linux/macOS: builds Linux + Windows artifacts (default)
./build.sh windows    # Windows only    ./build.sh linux    # Linux only    ./build.sh native    # current platform

build.bat             # Same on Windows (build.bat windows for Windows-only)
```

The artifacts are `backend/iot-gateway(.exe)` (frontend embedded) plus a same-layer plugin directory named per platform (`backend/plugins-windows/`, `backend/plugins-linux/`, `backend/plugins-linux-arm64/`, `backend/plugins-linux-arm/`). **Both must be copied to the target device together**, with the plugin directory renamed to `plugins/` (the gateway's default `plugin.dir`) next to the executable:

```bash
./iot-gateway        # Admin console: http://<host-ip>:9082/admin
```

> ⚠️ The binary contains **no protocol implementation**: if `plugins/` is missing or empty the gateway starts and the console works, but it **cannot collect a single device**. To package a release use `release.sh`, which cross-compiles the plugins into the archive; `build.sh` builds them as well. For how the plugins are compiled and what each entry point produces, see [Plugin packaging](#plugin-packaging).

### 3. Service deployment (auto-start on boot)

| Platform | Install | Uninstall |
|---|---|---|
| Windows | Run `install.bat` as administrator | Run `uninstall.bat [--purge]` as administrator |
| Linux | `sudo ./install.sh` | `sudo ./uninstall.sh [--purge]` |

- On Windows the service is registered via `sc` (native Windows service support is built in — no NSSM or other third-party tools), with automatic restart on crash.
- Default install dirs: Windows `%ProgramFiles%\iot-gateway`, Linux `/opt/iot-gateway` (override with `IOT_GATEWAY_HOME` / `INSTALL_DIR`).
- The service runs with the install directory as working directory; `data/` (SQLite) and `log/` (logs) are written there. `--purge` also deletes data on uninstall; by default data is preserved.

### 4. Docker Compose deployment (containerized)

No local Go / Node.js installation required. Run from the project root (multi-stage build: frontend build → Go compile → runtime image):

```bash
docker compose up -d --build    # Build the image and start in the background
docker compose logs -f          # Follow logs
docker compose ps               # Check status (incl. health check)
docker compose down             # Stop and remove the container
```

- Admin console: `http://<host-ip>:9082/admin` (host port `9082`).
- `./data` (SQLite) and `./log` (logs) are mounted into the container at `/app/data` and `/app/log` — data survives container rebuilds.
- **`plugins/` is deliberately not mounted**: the image already contains every protocol plugin, and a bind mount like `./plugins:/app/plugins` would mask them entirely (an empty host directory means no protocols at all). For runtime plugin swaps see [Upgrade notes](#upgrade-notes-breaking-change-not-a-rolling-upgrade).
- A built-in health check (`/health`) plus `restart: unless-stopped` restarts the container automatically on failure.

### Plugin packaging

Plugins are **never compiled one by one by hand** — every build entry point reads `drivers.txt` (the driver directory names to ship) and `drivers.version` (the driver repository tag) at the repo root, fetches the published driver sources with `go get`, and **the directory name is the artifact name**:

- Each line of `drivers.txt` is one driver directory, installed as `iot-driver-<directory name>` (with an `.exe` suffix on Windows).
- They are compiled with the **same** `GOOS` / `GOARCH` / `GOARM` environment as the main binary. A plugin whose architecture does not match the host will not stop the gateway from starting, but that plugin is silently skipped — the symptom is "the device is configured but no value is ever collected".
- **If not a single plugin is installed, the build fails outright** — a release without any protocol is never produced.

| Entry point | Runs on | Output |
|---|---|---|
| `build.bat` / `build.sh` | Windows / Linux·macOS | `backend/iot-gateway(.exe)` + `backend/plugins-<platform>/` (same layer as the binary) |
| `backend/package.bat` | Windows | The same, for all four platforms at once (`plugins-linux/`, `plugins-linux-arm64/`, `plugins-linux-arm/`, `plugins-windows/`) |
| `release.sh` / `release.bat` | Linux·macOS / Git Bash, Windows cmd | `*.zip` / `*.tar.gz` under `release/`, containing exactly two layers: `iot-gateway(.exe)` + `plugins/` |
| `backend/Dockerfile` | Container (multi-stage build) | `/app/plugins/` inside the image, shipped with every plugin already built in |

**To rebuild a single plugin** (you changed one protocol, or you want to verify locally), `go build` it inside a driver repository checkout:

```bash
cd ../iot-gateway-drivers        # git clone https://github.com/Ellisata/iot-gateway-drivers
go build -o ../iot-gateway/backend/plugins/iot-driver-modbus ./modbus   # .exe on Windows
```

To reproduce exactly what a release ships, `go get` the driver at `drivers.version` in a throwaway module and `go build -o` it (that is exactly what `build_plugins` in `build.sh` does).

**To add a protocol**, create one top-level directory in the [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers) repository and add one line to `drivers.txt` here — **no build logic changes**; the next build installs it. The only constraint: **directory name = artifact name, and it must be unique and non-nested** (hence `omroncip` / `rockwellcip` rather than `omron/cip`).

> The `-<platform>` suffix on locally built directories (`plugins-windows/`, …) exists to keep the four platforms' identically named artifacts from overwriting one another — mixing them in a single directory overwrites files with the wrong architecture, and **the overwrite fails silently**. At deploy time rename the directory for your platform to `plugins/` and put it next to the executable; `release.sh` already does this for you.

### Default accounts

Two accounts are seeded on first start: `admin` and `hand`, both with the initial password `hand@123`.

> ⚠️ **Security notice**: the default password is for first login only — **change all default account passwords immediately** in production. Avoid exposing the admin console (`/admin`) directly to the public internet. See [SECURITY.md](SECURITY.md) for further hardening (JWT secret configuration, network isolation, etc.).

## Upgrade notes (breaking change, not a rolling upgrade)

With all protocol drivers moved out of process, the **binary and the `plugins/` directory are built, tested and released as a pair**:

- **Official support is pair-for-pair replacement**: on upgrade, swap the **binary and the `plugins/` directory together** — never just one of them.
- **There is exactly one technical gate**: a plugin's `driversdk.APIVersion` must equal the one the gateway was built against; a mismatch is rejected at handshake time, never silently misparsed. **Within the same `APIVersion`** the two halves can technically be mixed, but that is not an officially paired combination; **across `APIVersion`** the mix cannot work.
- **Rollback is clean**: this plugin extraction **touches no database state** (`migrations/` is unchanged, so `iot_protocol` and the other tables keep their structure). Swap the old binary and old `plugins/` back as a pair and you are exactly where you started.
- **Protocol names are unchanged**: the 12 pre-existing protocol names and type scopes keep their values, so **existing device configs, `iot_protocol` rows and frontend forms need no migration** (`ABB.RWS` and `DLT645.Serial`/`DLT645.TCP` are newly added and only affect new devices).

The full version model, official release pairing matrix and upgrade rules are in [Version compatibility](docs/version-compatibility.md).

**Docker deployment note**: the previous `./plugins:/app/plugins:ro` bind mount in `docker-compose.yml` would **mask the plugins built into the image** (an empty host directory means no protocols at all), so it is now commented out. If you really need to mount plugins at runtime (canary / hot upgrade), **copy the release archive's `plugins/` contents into the host directory first**, then mount it read-only.

## Admin Console

| Page | Description |
|---|---|
| Dashboard | Device online status, acquisition and push overview |
| Device Objects / Address Tags | Manage devices grouped by gateway; maintain collection points and data types |
| Protocol Management | Dynamic-form protocol parameter configuration (custom form JSON supported) |
| Channel Management | MQTT / TDEngine / InfluxDB channel configuration and status monitoring |
| Subscription Management | Subscribe to external MQTT messages and write them into time-series databases: built-in DSL / JS-script parsers, mapping rules, sample preview, connectivity test and runtime status; the "Subscription Devices & Points" page indexes device / point metadata automatically and supports manual create/edit, Excel import, deletion and restore |
| Alarm Management | Device and channel disconnection alarms with history query |
| Log Viewer | Browse and download gateway runtime logs online |
| API Keys | Create / enable / disable Open API keys (admin) |

## Open API

External systems call these endpoints with a key created under "API Keys" in the console, carried in the `X-Api-Key` header:

```
GET /openApi/device/page          # Paged device query
GET /openApi/deviceAddress/page   # Paged device address query (by device ID)
```

## Push Payload Format

The field semantics of each pushed point record (`value`, `kind`, `quality`, etc.) and the self-describing parsing conventions for MQTT / TDEngine / InfluxDB are documented in the [push payload contract](backend/specs/push-payload.en.md). Downstream consumers (MES / TSDB analytics) should parse according to it rather than gateway internals.

## Configuration

Configuration lives in `backend/default.yaml`; `APP_ENV=dev|prod` switches to `dev.yaml` / `prod.yaml` overrides. Key options:

```yaml
server:
  port: 9082          # HTTP service port
jwt:
  secret: ""          # JWT signing secret: if empty, a random ephemeral secret is
                      # generated per start (sessions don't survive restarts). In
                      # production set a fixed one, preferably via the
                      # IOT_GATEWAY_JWT_SECRET environment variable.
  expireHour: 24      # Login token validity
log:
  level: INFO         # Log level
  maxSizeMB: 10       # Log rotation size
  maxDays: 30         # Log retention days
sqlite:
  path: data/iot-gateway.db   # SQLite data file path
plugin:
  enabled: true       # Master switch for out-of-process plugins: **ships as true** — every
                      # protocol comes from a plugin, so disabling it means no protocols at
                      # all. Loading plugins = running programs in plugins/ with the gateway's
                      # own OS privileges (trusted code, not sandboxed) — keep the directory
                      # writable only by administrators.
  dir: plugins        # Plugin directory; an empty or relative path resolves relative to the
                      # executable's directory, not the working directory
push:
  changeOnly: true    # Push only on change: skip points whose value and quality code are
                      # unchanged since the last delivery, cutting MQTT traffic and TSDB
                      # writes. The first round after startup, or after a push channel is
                      # added/reconfigured, is always a full snapshot (otherwise stable
                      # points would never get an initial value on the new channel).
                      # Set false when downstream expects periodic heartbeats. Switchable on
                      # the Runtime Switches page; applies from the next collection batch.
```

## Project Layout

```
├── backend/            # Go backend
│   ├── collector/      # Acquisition engine (poll scheduling / config hot reload)
│   ├── driveradapter/  # Host-side po.DeviceAddress ↔ SDK driver.DeviceAddress conversion
│   ├── pluginhost/     # Out-of-process plugin host (dir scan / process supervision / RPC proxy / hot upgrade;
│   │                   #   contract & wire layer come from the SDK module)
│   ├── testutil/       # Test infrastructure (parity goldens / drivertest synthetic protocol)
│   ├── plugins/        # Plugin runtime directory (build output; shipped in the release archive)
│   ├── mappingdsl/     # Mapping primitives shared by inbound parsing and outbound mapping (paths / templates / transforms)
│   ├── push/           # Push engine (mqtt/http/tdengine-v3/influxdb-v3 + outbox + codec/messagechannel)
│   ├── parser/         # Subscription payload parsers (built-in mapping DSL / goja scripts, emit collection records)
│   ├── subscribe/      # MQTT subscription engine (connection / queue / hot reload, parse & delivery orchestration)
│   ├── controller/ service/ model/  # HTTP layers (Controller → Service → Database)
│   ├── database/sqlite/migrations/  # SQLite migration scripts
│   ├── router/ middleware/          # Routing and middleware (JWT / Open API key / CORS)
│   ├── web/            # Frontend build artifacts (embedded via go:embed)
│   ├── docs/           # User-facing docs (incl. push-channels.md)
│   └── specs/          # Engineering specification documents
├── frontend/           # Vue3 admin frontend (Element Plus + Pinia + vue-i18n)
├── docker-compose.yml  # Docker Compose deployment orchestration (multi-stage build with backend/Dockerfile)
├── build.bat / .sh     # One-click build (frontend build → embed → Go cross compile)
├── install.bat / .sh   # Service installation (Windows / Linux)
└── uninstall.bat / .sh # Service uninstallation
```

## Tech Stack

| Layer | Choice |
|---|---|
| Backend | Go 1.26 + Gin + GORM + Google Wire + Viper + JWT |
| Storage | SQLite (pure Go implementation, CGO-free) |
| Protocol libs | **Shipped with the plugins**: goburrow/modbus · robinson/gos7 · gopcua/opcua etc. are linked only by the official driver plugins (standalone `iot-gateway-drivers` repository); the main binary links no protocol library |
| Frontend | Vue 3 + Vite + Element Plus + Pinia + Tailwind CSS 4 + vue-i18n |
| Deployment | go:embed single binary · Windows service / Linux systemd |

### Driver SDK (Apache-2.0)

Third parties can build protocol plugins — including proprietary ones — with the standalone [`iot-gateway-sdk`](https://github.com/Ellisata/iot-gateway-sdk) repository. The SDK is Apache-2.0, has zero third-party dependencies and does not import the gateway core; a driver runs as a separate process and talks to the gateway over the wire protocol, so a closed-source driver is not an AGPL derivative work. The nine official drivers live in the [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers) repository and stay AGPL-3.0. This repository depends on them by version (`backend/go.mod` for the SDK and testkit, `drivers.version` for the drivers) without vendoring their source; see [LICENSING.md](LICENSING.md) for the boundary.

## Contributing

Bug reports, documentation, new protocol drivers and push channels are all welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Please do not open public issues for security vulnerabilities; see [SECURITY.md](SECURITY.md) instead.

## Documentation

| Document | Contents |
|---|---|
| [Driver plugin guide](docs/driver-plugins.md) (中文) | Developing, lifecycle, security and troubleshooting for out-of-process protocol plugins — required reading before adding a protocol |
| [Driver SDK](https://github.com/Ellisata/iot-gateway-sdk) | Apache-2.0 SDK for third-party and proprietary drivers; see [LICENSING.md](LICENSING.md) |
| [Engineering specs](backend/specs/README.md) (中文) | Layering, directory layout, coding standards, etc. |
| [Push payload contract](backend/specs/push-payload.en.md) | Field semantics and parsing conventions for pushed data |
| [Subscription parser spec](backend/specs/parser-plugins.md) (中文) | Contract, limits and hot-reload semantics for the built-in mapping DSL and goja scripts |
| [Subscription script guide](backend/docs/parser-scripts.md) (中文) | Writing script parsers: `msg` / `ctx` API and examples |
| [Capacity load-test report](backend/cmd/loadtest/README.en.md) | Per-protocol device/point capacity and tuning advice |
| [Security policy](SECURITY.md) | Vulnerability reporting and deployment hardening |
| [Contributing](CONTRIBUTING.md) | Filing issues / PRs and the development workflow |
| [Changelog](CHANGELOG.md) | Release history |

> For the full index, including which documents have an English counterpart, see [docs/](docs/README.md).

## Community

- **Issues / Discussions**: file bugs and feature suggestions on [Gitee Issues](https://gitee.com/wang4856304/iot-gateway/issues) — the Gitee repo is the primary one and is where issues live; the [GitHub mirror](https://github.com/Ellisata/iot-gateway) only accepts pull requests.
- **WeChat group**: email `15289288565@163.com` to be added.
- **Email**: open-source inquiries and commercial licensing at `15289288565@163.com`.

## License & FAQ

This project is open-sourced under the [GNU AGPL-3.0](LICENSE). The FAQ below helps you quickly assess your use case (a friendly explanation only, not legal advice):

**Q1: I deploy it inside my own company (collecting data from our own production lines). Do I have to open-source my code?**

No. AGPL's source-disclosure obligations are triggered only when you **convey** the software or **offer it as a network service to others**. Internal use of an unmodified version, without serving external users, requires no source disclosure at all.

**Q2: I integrate my own systems (MES, ERP, dashboards) via the Open API / MQTT. Do those have to be AGPL-licensed?**

No. Your systems talk to this gateway over network interfaces — that's data exchange between independent programs. Your business code is not a derivative work of this project and is not bound by AGPL.

**Q3: When must I release source code?**

If you modify this project and then either ① **convey** the modified version to others (including bundling it for sale or shipping it with hardware), or ② **offer the modified version as a network service to users** (including deploying it for third parties). In either case you must provide the complete corresponding source code under AGPL-3.0 to the relevant recipients.

**Q4: Can I develop a closed-source protocol driver / push channel for this project?**

- A driver or channel compiled into the gateway binary is a derivative work and must be released under AGPL-3.0.
- A **separate process** interacting with the gateway over the network (a standalone collector, a standalone push agent, etc.) is not bound by it.
- Contributions of self-built drivers / channels under AGPL-3.0 are very welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). The project's registry + interface plugin design keeps integration cheap.

**Q5: Does AGPL cover the data the gateway collects and pushes?**

No. The data belongs to its owner. AGPL governs the software and its derivative works only — it never "infects" the data collected, transported or stored.

**Q6: I'm a hardware / gateway vendor and want to embed this project in a product I sell. Is that allowed?**

Yes, via either path: ① comply with AGPL-3.0 by providing the complete corresponding source code to buyers; or ② contact us to purchase a **commercial license** exempting you from AGPL obligations, supporting closed-source OEM embedding.

**Q7: How do I get a closed-source / OEM / dual license?**

Contact `<15289288565@163.com>` with your use case (OEM embedding, SaaS, derivative delivery, etc.) and we'll provide a commercial license.

---

- External contributions are licensed to the maintainer under [CLA.md](CLA.md) and released with the project under AGPL-3.0; the commercial-edition repository stays separate from this one.
- For open-source inquiries and commercial licensing, contact: `<15289288565@163.com>`
