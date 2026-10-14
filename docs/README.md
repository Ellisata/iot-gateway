# 文档索引 · Documentation Index

[English](#english) | 简体中文

本目录（`docs/`）放**面向使用与二次开发**的说明文档；工程内部规范、压测数据等随代码放在 `backend/` 下。下面是全仓库文档的导航。

## 按角色阅读

| 你是 | 建议顺序 |
|---|---|
| **第一次部署** | [README](../README.md) → [快速开始](../README.md#快速开始) → [安全策略](../SECURITY.md)（部署加固） |
| **对接下游系统（MES / ERP / 时序库）** | [推送载荷契约](../backend/specs/push-payload.md) → [开放接口](../README.md#开放接口) |
| **开发一个新协议驱动** | [驱动插件指南](driver-plugins.md) → [点位扫描契约](../backend/specs/device-scan.md) → [贡献指南](../CONTRIBUTING.md) |
| **评估容量 / 选型** | [采集容量压测报告](../backend/cmd/loadtest/README.md) → [容量测试结论](../backend/specs/capacity-testing.md) |
| **要改本项目源码** | [backend/CLAUDE.md](../backend/CLAUDE.md) → [技术规范](../backend/specs/README.md) → [贡献指南](../CONTRIBUTING.md) |

## 本目录

| 文件 | 说明 | 语言 |
|---|---|---|
| [driver-plugins.md](driver-plugins.md) | 进程外协议驱动插件：编写、多协议、生命周期与自愈、热升级、安全、排错 | 中文 |
| [dsh-plugin-capability-report.md](dsh-plugin-capability-report.md) | 面向 DSH 插件对接的网关能力清单（含 `/agentApi/*` 只读/写接口分类，逐条附源码位置） | 中文 |
| [images/](images/) | README 使用的界面动图与截图（中英各一套：`demo.zh.gif` / `demo.en.gif`，以及 `dashboard.zh.png` / `dashboard.en.png`） | — |

## 仓库其他位置的文档

| 位置 | 说明 | 语言 |
|---|---|---|
| [README.md](../README.md) / [README.en.md](../README.en.md) | 项目介绍、功能、协议与通道、部署、配置、FAQ | 中文 / English |
| [CONTRIBUTING.md](../CONTRIBUTING.md) | 提 Issue / PR、开发环境、典型贡献场景 | 中文 / English |
| [SECURITY.md](../SECURITY.md) | 漏洞报告渠道与部署加固建议（含插件信任边界） | 中文 / English |
| [CHANGELOG.md](../CHANGELOG.md) | 版本变更记录（Keep a Changelog 格式） | 中文 / English |
| [CLA.md](../CLA.md) / [LICENSE](../LICENSE) | 贡献者许可协议 / AGPL-3.0 许可证 | 中文 / English |
| [backend/specs/](../backend/specs/) | 分层架构、目录结构、开发规范、断联报警、局域网发现、设备/点位扫描、推送载荷契约 | 中文（部分有 EN） |
| [backend/cmd/loadtest/](../backend/cmd/loadtest/) | 各协议容量压测的方法、矩阵与读数 | 中文 / English |
| [backend/CLAUDE.md](../backend/CLAUDE.md) | 后端开发约定（改代码前必读） | 中文 |

> `backend/specs/` 下有英文对照的文档会在正文顶部互相链接：`push-payload.md` ↔ [`push-payload.en.md`](../backend/specs/push-payload.en.md)，`capacity-testing.md` ↔ [`capacity-testing.en.md`](../backend/specs/capacity-testing.en.md)。其余规范目前仅中文。

## 未随仓库分发

`docs/articles/`（推广文章、封面素材、估算表等）已被 [.gitignore](../.gitignore) 排除，**不随仓库发布**，仅在本地工作副本中存在——它们是面向外部渠道的内容稿，不属于开源文档的一部分。

---

<a id="english"></a>

## English

`docs/` holds the **usage and secondary-development** documents; internal engineering specs and benchmarks live under `backend/` next to the code.

| Document | Contents | Language |
|---|---|---|
| [Driver plugin guide](driver-plugins.md) | Out-of-process protocol plugins: authoring, multi-protocol, lifecycle & self-healing, hot upgrade, security, troubleshooting | Chinese |
| [DSH capability report](dsh-plugin-capability-report.md) | Gateway capability inventory for DSH integration (incl. the `/agentApi/*` read/probe/write surface, with source locations) | Chinese |
| [images/](images/) | Console demo GIFs and screenshots used by the README (one set per locale: `demo.{zh,en}.gif`, `dashboard.{zh,en}.png`) | — |
| [README](../README.en.md) | Overview, features, protocols & channels, deployment, configuration, FAQ | English |
| [CONTRIBUTING](../CONTRIBUTING.md) | Filing issues/PRs, dev setup, common contribution scenarios | English |
| [SECURITY](../SECURITY.md) | Vulnerability reporting and deployment hardening (incl. the plugin trust boundary) | English |
| [Push payload contract](../backend/specs/push-payload.en.md) | Field semantics and parsing conventions for pushed data | English |
| [Capacity load-test report](../backend/cmd/loadtest/README.en.md) | Benchmark method, matrix and readings | English |
| [Engineering specs](../backend/specs/) | Architecture, directory layout, coding standards, alarms, discovery, point scan | Chinese (partly EN) |

> Documents under `backend/specs/` that have an English counterpart cross-link each other at the top. Chinese-only documents are marked `(中文)` where they are referenced from the English README.
