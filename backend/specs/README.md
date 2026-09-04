# Go 项目通用技术规范

> 沉淀自 IoT 数据持久化系统的实践经验，形成可复用的 Go Web 项目规范体系。
> 适用于基于 **Go + Gin + GORM + SQLite + Redis** 技术栈的 Web 后端项目，
> 以及基于 **TDEngine / InfluxDB** 的时序数据存储场景。

---

## 规范文件索引

| 文件 | 说明 | 适用场景 |
|------|------|----------|
| [架构规范](architecture.md) | 分层架构定义、各层职责、层间通信规则 | 新建项目时的技术选型与架构设计 |
| [目录结构规范](directory-structure.md) | 标准目录树、分包原则、文件命名约定 | 初始化项目仓库时的目录搭建 |
| [开发技术规范](development-standards.md) | 命名规范、错误处理、日志规范、安全规范、代码风格 | 日常编码中的规范约束与 Code Review |
| [通用技术组件](technical-components.md) | 配置管理、日志系统、响应封装、数据库操作等核心组件 | 技术选型参考与组件复用 |
| [推送载荷契约](push-payload.md) | 采集数据外发 payload 的字段语义、value 按 kind 自描述解析约定 | 外部消费端对接与数据解析（[EN](push-payload.en.md)） |
| [断联报警](alarm.md) | 设备/推送通道离线与恢复报警的检测、判定与统一落库方案 | 断联报警功能设计与实现参考 |

## 使用方式

### 1. 直接引用

将 `specs/` 目录整体复制到新项目根目录，作为项目技术规范文档。

### 2. 按需裁剪

根据项目实际技术栈和规模，选择性引用各文档中的规范条目。

### 3. 作为 CLAUDE.md 的基础

参考本规范中的关键条目，生成项目的 `CLAUDE.md` 开发规范文件。

---

## 技术栈基线

| 层次 | 选择 | 说明 |
|------|------|------|
| 语言 | Go 1.25+ | 使用泛型、embed 等现代特性 |
| HTTP 框架 | Gin v1.10+ | 高性能，中间件生态丰富 |
| ORM | GORM v1.25+ | 全功能 ORM，支持关联、事务、Hook |
| 关系数据库 | SQLite | 嵌入式关系型数据库（配置存储） |
| 时序数据库 | TDEngine / InfluxDB v3 | 双引擎适配，统一抽象接口 |
| 缓存 | Redis (go-redis) | 分布式缓存与锁 |
| 依赖注入 | Google Wire | 编译期 DI，构建完整依赖图 |
| 配置 | Viper | 多环境配置管理 |
| 认证 | JWT (golang-jwt) | 无状态令牌认证 |
| 国际化 | 自定义 i18n | 基于 JSON 文件的多语言支持 |
| 文档 | Swagger (swaggo) | API 文档自动生成 |

---

*本规范遵循 [Keep it simple, stable and portable](https://go-proverbs.github.io/) 的 Go 哲学。*
