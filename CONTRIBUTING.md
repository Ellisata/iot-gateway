# 贡献指南 / Contributing to iot-gateway

感谢关注 iot-gateway！欢迎以任何形式贡献：报告 Bug、补充文档、实现新协议驱动或推送通道、分享使用案例。
Thanks for your interest in contributing! Bug reports, docs, new protocol drivers, push channels and use cases are all welcome.

- [English](#english) · [简体中文](#简体中文)

---

<a id="简体中文"></a>

## 简体中文

### 提 Issue

- Bug 报告请附：版本、部署方式、涉及协议、复现步骤、**脱敏后**的日志片段。
- 安全漏洞请勿公开 Issue，参见 [SECURITY.md](SECURITY.md)。

### 开发环境

```bash
# 依赖：Go ≥ 1.26、Node.js ≥ 22
cd backend  && go run .          # 后端 :9082
cd frontend && npm install && npm run dev   # 前端 :4000（Vite 代理到 9082）
```

> **开发模式下的插件目录**：协议驱动是进程外插件，`plugin.dir`（默认 `plugins`）**相对可执行文件所在目录**解析；
> 而 `go run .` 的可执行文件在 Go 临时构建目录里，因此 `go run .` **找不到 `plugins/`**（症状是能启动、能登录，
> 但设备全被静默跳过）。要在开发模式下调协议驱动，请先 `build.bat windows`（插件落在 `backend/plugins-windows/`），
> 再把 `plugin.dir` 配成指向那个目录，或把它改名成 `plugins/` 后直接跑 `backend/iot-gateway.exe`。

### 开发流程

1. Fork 仓库，从 `main` 拉出功能分支（如 `feat/omron-cip`）。
2. 开发前阅读 [backend/CLAUDE.md](backend/CLAUDE.md) 与 [backend/specs/](backend/specs/) 中的架构与规范约定（分层、命名、错误处理）。
3. 提交前自查：

```bash
cd backend
go test ./...        # 测试通过
go vet ./...         # 静态检查
go fmt ./...         # 格式化
```

4. 提交信息格式：`feat: 支持 Kafka 推送通道` / `fix: 修复 S7 断线重连竞态`（类型：`feat` / `fix` / `docs` / `refactor` / `test` / `chore`）。
5. 发起 Pull Request，按模板填写；首次贡献请阅读 [CLA.md](CLA.md)（提交贡献即视为同意）。**仅修改 SDK 或官方驱动时，请到对应的独立仓库提交，按那边的贡献流程执行。**

### 独立的 SDK 与官方驱动仓库

- **SDK**：`iot-gateway-sdk`（本仓库通过 `backend/go.mod` 按版本依赖），Apache-2.0 + DCO，**不适用本仓库的 CLA**。见 [LICENSING.md](LICENSING.md)。
- **官方驱动**：`iot-gateway-drivers`（本仓库通过 `drivers.version` 固定版本），AGPL-3.0 + CLA，保持商业/OEM 双授权。
- 核心仓库只维护宿主、引擎、UI 与插件生命周期；协议实现和 SDK 分别在上述两个仓库维护。

### 典型贡献场景

**新增协议驱动**（如基恩士、施耐德）：协议驱动一律做成**进程外插件**（不改网关、不停机），作者指南见 [docs/driver-plugins.md](docs/driver-plugins.md)。
1. 在 `iot-gateway-drivers` 仓库新建 `<名字>/main.go`，实现 `driver.Driver` 接口（Connect / Ping / Read / IsConnected / Close），并在本仓库的 `drivers.txt` 里加一行。
2. 调用 `driversdk.Main(driversdk.Factories{...}, driversdk.Meta{...}, driversdk.Capabilities{...})` 注册协议名、类型 scope、类型表与各协议的配置表单（不需要扫描能力时第三个参数传零值 `driversdk.Capabilities{}`）。**协议名与类型 scope 必须与已注册的错开**（撞名会被拒绝注册）。
3. 产物名为 `iot-driver-<名字>`，放进可执行文件同级的 `plugins/` 目录即被加载；`build.sh` / `release.sh` / `backend/Dockerfile` 会自动交叉编译并打包（编不出插件的构建会直接失败）。
4. 在 `cmd/loadtest/` 中支持该协议，跑一遍容量压测并在 README 附数据；注意插件版每次读写多一次进程间 RPC 往返，**基线不可与早期进程内驱动的数据直接比较**。

> 主二进制**不再内置任何协议实现**（契约与基建在独立 SDK 仓库 `iot-gateway-sdk`），所以新协议只能以插件形式接入。

**新增推送通道**（如 Kafka、PostgreSQL）：
1. 新建 `backend/push/<type>/`，实现 `push.Channel` 接口。
2. `init()` 中 `push.Register(...)` 注册，并在 `backend/main.go` 空导入该子包。

**规范**：单元测试与功能代码同 PR 提交；数据库结构变更须新增 `NNN_name.sql` 迁移脚本；前端文案走 i18n 双语。

---

<a id="english"></a>

## English

### Filing Issues

- For bugs, include: version, deployment mode, protocol involved, repro steps and **sanitized** logs.
- Never open a public issue for security vulnerabilities — see [SECURITY.md](SECURITY.md).

### Development Setup

```bash
# Requirements: Go >= 1.26, Node.js >= 22
cd backend  && go run .          # backend :9082
cd frontend && npm install && npm run dev   # frontend :4000 (Vite proxies to 9082)
```

> **Plugin directory in dev mode**: protocol drivers are out-of-process plugins, and `plugin.dir`
> (default `plugins`) resolves **relative to the executable's directory** — but `go run .` places the
> executable in a Go temp build directory, so `go run .` **will not find `plugins/`** (the symptom is a
> gateway that starts and logs in fine, yet silently skips every device). To exercise protocol drivers in
> development, run `build.bat windows` (the plugins land in `backend/plugins-windows/`), then point
> `plugin.dir` at that directory — or rename it to `plugins/` and run `backend/iot-gateway.exe` directly.

### Workflow

1. Fork the repo and branch off `main` (e.g. `feat/omron-cip`).
2. Read [backend/CLAUDE.md](backend/CLAUDE.md) and the specs in [backend/specs/](backend/specs/) for architecture and coding conventions (layering, naming, error handling).
3. Before submitting:

```bash
cd backend
go test ./...        # tests pass
go vet ./...         # static checks
go fmt ./...         # formatting
```

4. Commit message style: `feat: add Kafka push channel` / `fix: race in S7 reconnect` (types: `feat` / `fix` / `docs` / `refactor` / `test` / `chore`).
5. Open a Pull Request using the template; first-time contributors please read [CLA.md](CLA.md) (submitting a contribution counts as agreeing to it). **For changes to the SDK or official drivers, contribute in the corresponding standalone repository instead.**

### Standalone SDK and driver repositories

- **SDK**: `iot-gateway-sdk` (pinned here via `backend/go.mod`), **Apache-2.0 + DCO**; the CLA does not apply. See [LICENSING.md](LICENSING.md).
- **Official drivers**: `iot-gateway-drivers` (pinned here via `drivers.version`), **AGPL-3.0 + CLA**; commercial/OEM dual licensing applies.
- This core repository maintains the host, engines, UI and plugin lifecycle; protocol implementations and the SDK live in their own repositories.

### Common Contribution Scenarios

**New protocol driver** (e.g. Keyence, Schneider): protocol drivers are always **out-of-process plugins** (no gateway rebuild, no downtime) — author guide in [docs/driver-plugins.md](docs/driver-plugins.md).
1. Create `<name>/main.go` in the `iot-gateway-drivers` repository implementing the `driver.Driver` interface (Connect / Ping / Read / IsConnected / Close), and add one line to `drivers.txt` here.
2. Call `driversdk.Main(driversdk.Factories{...}, driversdk.Meta{...}, driversdk.Capabilities{...})` to register the protocol names, type scopes, type tables and per-protocol config forms (pass the zero value `driversdk.Capabilities{}` when you need no scanning capabilities). **Protocol names and type scopes must not collide with existing ones** (colliding registrations are rejected).
3. The build output is `iot-driver-<name>`, loaded once placed in the `plugins/` directory next to the executable; `build.sh` / `release.sh` / `backend/Dockerfile` cross-compile and package it automatically (a build that produces no plugin fails outright).
4. Support the protocol in `cmd/loadtest/`, run a capacity benchmark and attach the numbers to the README. The tool runs **serially** by design (parallel sweeps contend for CPU and ruin the CPU-bound readings), supports `-latency` on every protocol to simulate RTT, and the README's "Post-pluginization baseline" (2026-09) is the reference matrix to extend — add a `WithLatency` fake server alongside the protocol so the RTT grid stays complete. Do **not** compare against pre-pluginization numbers: those baselines predate the one extra gateway ↔ plugin RPC round-trip per read/write and are obsolete.

> The gateway binary **contains no protocol implementation anymore** (the contract and infrastructure live in the standalone `iot-gateway-sdk` repository), so new protocols can only be added as plugins.

**New push channel** (e.g. Kafka, PostgreSQL):
1. Create `backend/push/<type>/` implementing the `push.Channel` interface.
2. Call `push.Register(...)` in `init()` and add a blank import of the package in `backend/main.go`.

**Conventions**: unit tests ship in the same PR; schema changes need a new `NNN_name.sql` migration; UI strings go through bilingual i18n.
