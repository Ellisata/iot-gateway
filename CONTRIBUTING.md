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
cd backend  && go run .          # 后端 :9081
cd frontend && npm install && npm run dev   # 前端 :4000（Vite 代理到 9081）
```

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
5. 发起 Pull Request，按模板填写；首次贡献请阅读 [CLA.md](CLA.md)（提交贡献即视为同意）。

### 典型贡献场景

**新增协议驱动**（如基恩士、施耐德）：
1. 新建 `backend/driver/<protocol>/`，实现 `driver.Driver` 接口（Connect / Ping / Read / IsConnected / Close）。
2. `init()` 中 `driver.Register(...)`；协议专用数据类型经 `driver.GetTypeRegistry().ForProtocol(...)` 注册。
3. 在 `cmd/loadtest/` 中支持该协议，跑一遍容量压测并在 README 附数据。

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
cd backend  && go run .          # backend :9081
cd frontend && npm install && npm run dev   # frontend :4000 (Vite proxies to 9081)
```

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
5. Open a Pull Request using the template; first-time contributors please read [CLA.md](CLA.md) (submitting a contribution counts as agreeing to it).

### Common Contribution Scenarios

**New protocol driver** (e.g. Keyence, Schneider):
1. Create `backend/driver/<protocol>/` implementing the `driver.Driver` interface (Connect / Ping / Read / IsConnected / Close).
2. Call `driver.Register(...)` in `init()`; register protocol-scoped data types via `driver.GetTypeRegistry().ForProtocol(...)`.
3. Support the protocol in `cmd/loadtest/`, run a capacity benchmark and attach the numbers to the README.

**New push channel** (e.g. Kafka, PostgreSQL):
1. Create `backend/push/<type>/` implementing the `push.Channel` interface.
2. Call `push.Register(...)` in `init()` and add a blank import of the package in `backend/main.go`.

**Conventions**: unit tests ship in the same PR; schema changes need a new `NNN_name.sql` migration; UI strings go through bilingual i18n.
