# iot-gateway - 工业 IoT 网关 - 项目规范

## 项目简介

工业物联网数据网关:通过 Modbus(RTU/TCP)与 Siemens S7 协议定时采集 PLC/设备数据,推送至 MQTT 等下游通道;内嵌 Vue 管理前端(`web/dist`,构建产物),以单二进制方式部署到网关设备。

**核心数据流**:SQLite 配置 → `collector` 采集引擎轮询设备 → `driver` 协议驱动读写 → `push` 推送引擎分发到各通道(MQTT)。

## 技术栈
- **语言**: Go 1.26
- **HTTP 框架**: Gin v1.12
- **ORM / 存储**: GORM + SQLite(纯 Go 实现 `glebarez/sqlite`,免 CGO;配置存储,路径见 `default.yaml`)
- **协议驱动**: `goburrow/modbus`(Modbus RTU/TCP)、`robinson/gos7`(Siemens S7)、`gopcua/opcua`(OPC UA)
- **推送**: `eclipse/paho.mqtt.golang`(MQTT)
- **依赖注入**: Google Wire(编译期 DI,`wire.go` / `wire_gen.go`)
- **其他**: Viper(配置)、golang-jwt(认证)、go-redis(缓存)、自定义 i18n(JSON)

## 项目结构
```
├── main.go              # 入口: 配置→日志→i18n→Wire 组装→启动采集/推送引擎→Gin
├── wire.go / wire_gen.go # Google Wire 依赖图(AppDependencies + ProvideRouteOptions)
├── configFile/          # 配置加载(default.yaml / dev.yaml / prod.yaml)
├── router/              # 路由层(RouteOption 模式,各领域 xxxRouter.go)
├── middleware/          # 中间件: 认证 / 请求日志 / 区域 / CORS / 客户端密钥
├── controller/          # HTTP 处理层(参数校验 + 响应封装)
├── service/             # 业务逻辑层
├── model/               # 数据模型
│   ├── dto/             # 请求参数
│   ├── po/              # 数据库映射(GORM)
│   └── vo/              # 响应对象
├── database/sqlite/     # 数据库层 + migrations/*.sql(按文件名顺序执行)
├── collector/           # 采集引擎(核心): 轮询任务 + 配置热加载 watcher
├── driver/              # 协议驱动注册表 + 数据类型注册表(TypeRegistry)
│   ├── modbus/          # Modbus RTU / TCP 驱动
│   ├── s7/              # 西门子 S7 驱动
│   └── opcua/           # OPC UA 驱动（gopcua）
├── push/                # 推送引擎: 接收采集数据,分发到各推送通道
│   └── mqtt/            # MQTT 推送通道
├── web/                 # 前端构建产物(仅 dist/,go:embed 嵌入,无前端源码)
├── workerPool/          # 并发 Worker 池(采集任务调度)
├── enums/ i18n/ logger/ response/ appError/ utils/ constants/
├── specs/               # 通用技术规范文档(架构 / 目录 / 开发规范)
├── data/                # SQLite 运行时数据文件(gitignore)
└── log/                 # 运行日志(gitignore)
```

## 核心架构

### 分层与依赖注入
- Controller → Service → Database,上层依赖下层,下层不依赖上层。
- 依赖通过 **Google Wire** 编译期组装,根节点为 `InitializeApp`([wire.go](wire.go)),返回 `AppDependencies{Engine, CollectorEngine, PushEngine}`。`wire_gen.go` 为生成文件,改 `wire.go` 后执行 `go generate`。

### 采集引擎([collector/engine.go](collector/engine.go))
- 从 SQLite 加载活跃设备与地址(`status=1`),经 WorkerPool 并发调度 `driver.Driver` 轮询。
- 后台 watcher 每 10s 计算配置 checksum,设备/地址变更自动**热加载**(`Refresh` 零中断换任务)。
- 采集结果通过 `RecordSink` 接口交给下游(绑定为 `push.Engine`)。

### 推送引擎([push/engine.go](push/engine.go))
- 实现 `collector.RecordSink`,按设备分组后非阻塞分发给所有活跃通道。
- 同样带 watcher 热加载(`push_channel` 表),配置未变的通道保留运行实例。

### 协议驱动注册表([driver/driver.go](driver/driver.go))
- 各协议子包在 `init()` 中调用 `driver.Register("协议名", NewFunc)` 注册,`collector` 用 `driver.Create(protocol)` 创建实例。
- 数据类型通过全局 `TypeRegistry`([driver/typeRegistry.go](driver/typeRegistry.go))注册,**协议作用域自动加前缀**(如 `modbus.bool`、`s7.int16`),查询不区分大小写。

### 前端
- `web/` 只含构建产物 `web/dist/`,经 `go:embed` 嵌入二进制,由 [web/embed.go](web/embed.go) 的 `ServeStatic` 在 `/admin/*` 路径分发(SPA 回退 index.html)。
- **仓库内无前端源码**;修改前端需在外部前端工程构建后替换 `web/dist/`。

### 路由
- API 路由按领域注册,无统一前缀(如 `/device/xxx`、`/user/xxx`),见各 `xxxRouter.go`。
- SPA 走 `/admin/*`;`/health` 为健康检查。

## 扩展点(新增模块的标准步骤)

### 新增协议驱动
1. 新建 `driver/<protocol>/`,实现 `driver.Driver` 接口(Connect/Ping/Read/IsConnected/Close)。
2. 在 `init()` 中 `driver.Register("<ProtocolName>", ...)` 注册;协议专用数据类型用 `driver.GetTypeRegistry().ForProtocol("xxx")` 注册。

### 新增推送通道
1. 新建 `push/<type>/`,实现 `push.Channel` 接口(ChannelID/ConfigSig/Start/Stop/Enqueue/Snapshot)。
2. 在 `init()` 中 `push.Register("name", factory)` 注册。
3. 在 [main.go](main.go) 顶部**空导入**该子包(与 `_ "iot-gateway/push/mqtt"` 并列)。

### 新增业务领域模块
1. 建 `model/po|dto|vo/xxx.go` → `service/xxxService.go` → `controller/xxxController.go` → `router/xxxRouter.go`。
2. 在 `service/wire_set.go`、`controller/wire_set.go` 追加构造函数。
3. 在 [wire.go](wire.go) 的 `ProvideRouteOptions` 追加 `WithXxxRoutes`,并执行 `go generate` 重生成 `wire_gen.go`。

## 开发命令
```bash
go run main.go           # 开发运行(默认端口 9081,见 default.yaml)
go build -o iot-gateway .        # 构建
go test ./...            # 测试
go fmt ./...             # 格式化
go generate             # 重新生成 wire_gen.go
```

## 命名规范
- 包名: 小写单数 (`user`, `configFile`)
- 文件: 驼峰 (`userController.go`, `deviceDto.go`)
- 数据库: snake_case (`users`, `created_at`),迁移文件 `NNN_name.sql` 按序编号
- JSON: camelCase (`accessToken`, `serverTime`)
- 单元测试: `{entity}_test.go`

## 规范文档
通用技术规范沉淀在 `specs/` 目录(架构、目录结构、开发标准、技术组件),代码风格与错误处理遵循其中的约定;`response` 统一响应、`appError` 错误处理、`enums/resultEnum.go` 业务错误码。
