# iot-gateway - 工业 IoT 网关 - 项目规范

## 项目简介

工业物联网数据网关:通过 Modbus(RTU/TCP)、Siemens S7、OPC UA、Omron FINS/CIP、Mitsubishi MC、Rockwell CIP、ABB 机器人 RWS、DL/T 645 电能表等协议定时采集 PLC/设备数据,推送至 MQTT 等下游通道;内嵌 Vue 管理前端(`web/dist`,构建产物)。主二进制 + 同目录 `plugins/` 下的协议插件共同部署。

**核心数据流**:SQLite 配置 → `collector` 采集引擎轮询设备 → `driver` 协议驱动读写 → `push` 推送引擎分发到各通道(MQTT)。

## 技术栈
- **语言**: Go 1.26
- **HTTP 框架**: Gin v1.12
- **ORM / 存储**: GORM + SQLite(纯 Go 实现 `glebarez/sqlite`,免 CGO;配置存储,路径见 `default.yaml`)
- **协议驱动**: 全部为进程外插件(见 `driversrc/`)。`goburrow/modbus`(Modbus RTU/TCP)、`robinson/gos7`(Siemens S7)、`gopcua/opcua`(OPC UA)等第三方库只被插件二进制链接,**主二进制里不含任何驱动实现**。唯一例外是推送侧的 OPC UA Server 通道(`push/opcuaserver/`)——它是服务端而非驱动,在进程内链接 gopcua 的 server 包
- **推送**: `eclipse/paho.mqtt.golang`(MQTT)、`gopcua/opcua` server(OPC UA Server 通道)
- **订阅解析**: `dop251/goja`(纯 Go、无 CGO)执行自定义解析脚本;契约见 [specs/parser-plugins.md](specs/parser-plugins.md)
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
├── driver/              # 协议无关的契约与注册表（**不含任何协议实现**）
│   ├── driver.go        # Driver 接口 + 协议注册表(Register/RegisterExternal/Unregister/Create/PingDevice)
│   ├── types.go         # DataType(编解码函数带 json:"-",不出插件进程)
│   ├── typeRegistry.go  # 类型注册表(TypeRegistry,按 scope 前缀命名空间)
│   ├── dataTypeMap.go   # 通用类型清单 + scope 映射(内置基线表已清空,见文件内注释)
│   ├── rangeSig.go      # RangeSig:区间缓存键,插件与网关共用的契约(插件也在用)
│   ├── discovery.go     # 扫描发现契约:DiscoverySpec/ScanTarget/DiscoveredDevice
│   └── pointDiscovery.go # 点位扫描契约:PointDiscoverySpec/PointScanResult/DiscoveredPoint(**与 discovery.go 是两件相反的事**,见下)
├── pluginwire/          # 进程外插件的**传输层**(只依赖标准库):帧编解码/方法名/RemoteError
├── pluginhost/          # 进程外插件宿主(**与插件种类无关**):目录扫描/进程监管/RPC/热升级/权限校验
│   ├── adapter_driver.go #   驱动侧适配:describe → driver 的三张运行时注册表(见该文件头的边界说明)
│   ├── provision.go     #   驱动侧适配:iot_protocol 行供给(只插入/只补空)
│   └── remote.go        #   驱动侧适配:把 RPC 调用包装成 driver.Driver 的 remoteDriver
├── driversdk/           # 进程外驱动插件开发套件(Main/Serve/Meta/RPC 参数;依赖 pluginwire)
├── driversrc/           # 协议插件的源码(**协议的唯一实现处**,编译成 iot-driver-* 独立可执行文件)
│   ├── modbus/          # ModBus.TCP / ModBus.RTU;实现在 modbusdrv/
│   ├── mitsubishi/      # Mitsubishi.MC.TCP / .Serial;实现在 mitsubishidrv/（包名 mitsubishi）
│   ├── s7/              # Siemens.S7;实现在 s7drv/（包名 s7）
│   ├── opcua/           # OPC.UA;实现在 opcuadrv/（包名 opcua）
│   ├── rockwellcip/     # Rockwell.CIP;实现在 rockwellcipdrv/（包名 cip）;cipcore/ 是 omroncip 那份的裁剪副本,刻意不共享（见「命名规范」）
│   ├── omroncip/        # Omron.CIP;实现在 omroncipdrv/（包名 cip）;EtherNet/IP 封装层在 cipcore/
│   ├── fins/            # Omron.FINS.UDP/TCP/Serial/HostLinkTCP;实现在 finsdrv/（包名 fins）
│   ├── abb/             # ABB.RWS（机器人 Robot Web Services）;实现在 abbdrv/（包名 abb）;纯 HTTP，无第三方依赖
│   └── dlt645/          # DLT645.Serial / DLT645.TCP（DL/T 645 电能表）;实现在 dlt645drv/（包名 dlt645）;移植自 iot-gateway 内置驱动
├── testutil/            # 测试基建(非 _test 文件,插件也能 import)
│   ├── parity/          # 行为等价黄金基线的读写格式(pluginhost/testdata/parity/)
│   ├── drivertest/      # 网关侧测试的合成协议(EnsureSyntheticProtocol)与桩驱动
│   └── fake/            # 各协议的假 PLC(parity 比对与集成测试用)
├── mappingdsl/          # 入站解析与出站映射共用的映射原语(仅标准库):JSON 路径/模板/变换/标量转换
├── parser/              # 外部报文解析契约与实现(**与报文来源解耦**,见 specs/parser-plugins.md)
│   ├── parser.go        # Parser/Factory 契约、注册表、CollectedRecord 白名单与限制
│   ├── path.go          # JSON 路径取值(委托 mappingdsl,保留旧 API)
│   ├── mapping/         # builtin 声明式 DSL(topicPattern / fanout / transform)
│   └── script/          # goja 脚本解析(沙箱、逐报文超时、超时后重建 VM)
├── push/                # 推送引擎: 接收采集数据,分发到各推送通道
│   ├── encoder.go       # **载荷编码契约**:Encoder / FormatMeta(native-json / mapping-json)
│   ├── optional.go      # 可选接口 Previewer / TestSender,不改 push.Channel
│   ├── outbox.go        # **通道共用的队列 + 断网缓存骨架**(Outbox):入队路由/落盘/补发唤醒/状态/生命周期/重连
│   ├── drain.go         # **补发策略**:DrainLoop(毒批次/窄化)+ GroupedDrainer(聚合回放算法)+ Aggregator 契约
│   ├── writer.go        # **攒批下发写协程**(AggregateWriter):消费队列 → 聚合 → 按行数/时间窗刷出
│   ├── codec/           # 载荷编码器实现:nativejson(默认契约)/ mappingjson(出站映射)
│   ├── messagechannel/  # 消息类通道通用骨架:Outbox + Encoder + Transport
│   ├── webhook/         # HTTP/Webhook 通道(注册类型 "http")
│   ├── opcuaserver/     # OPC UA Server 通道(注册类型 "opcua"):状态型,地址空间 + 最新值 + 订阅通知
│   └── mqtt/ tdengine/v3/ influxdb/v3/   # 各通道只实现「负载构造 + 下发 + 连接维护」
├── discovery/           # 局域网自动发现: 以 UDP 广播发布网关身份,供管理平台自动发现
│   ├── message.go       # **对外契约**:报文常量/Message/encode/decode(见 specs/discovery.md)
│   ├── netif.go         # 本机 IPv4 网卡枚举 + 子网广播地址计算(纯函数);also LocalSubnets
│   └── announcer.go     # Announcer:Start/Stop、周期广播、探询应答与限流
├── devicescan/          # 局域网设备扫描发现(**与 discovery 方向相反**):主动扫本机网段找 PLC
│   ├── targets.go       # 网段展开成逐主机目标 + maxTargets 截断(纯函数)
│   ├── job.go           # job/Snapshot:进度、结果、终态(见 specs/device-scan.md)
│   └── engine.go        # Engine:Submit/Status/Cancel/Stop、并发探测、惰性过期
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
- 依赖通过 **Google Wire** 编译期组装,根节点为 `InitializeApp`([wire.go](wire.go)),返回 `AppDependencies{Engine, CollectorEngine, PushEngine, AlarmMonitor, DriverManager, Subscription}`。`wire_gen.go` 为生成文件,改 `wire.go` 后执行 `go generate`。
- 启动顺序有硬约束([main.go](main.go)):**`DriverManager.Start()` 必须早于 `CollectorEngine.Start()`** —— 插件驱动要先完成注册,采集引擎建任务时才能为使用插件协议的设备创建驱动,否则这些设备首轮会被判为「协议不支持」而跳过。停止顺序相反(插件管理器最后停)。

### 协议插件([pluginhost/](pluginhost/), [driversrc/](driversrc/))
- 全部协议是进程外插件:独立可执行文件 `iot-driver-*`,由 `pluginhost.Manager` 扫描 `plugin.dir`(相对 `os.Executable()` 解析)、校验插件的 `driversdk.Meta.APIVersion` 并握手、把驱动工厂与类型/scope 注册进 `driver` 的运行时注册表,并在 `iot_protocol` 表里自动维护协议行(只插入、只补空,绝不覆盖运维改过的非空表单)。
- **宿主分成通用核心与适配层**:扫描/进程监管/帧 RPC/热升级/目录权限校验([process.go](pluginhost/process.go) · [upgrade.go](pluginhost/upgrade.go) · [client.go](pluginhost/client.go))不认识任何插件种类,只依赖 [pluginwire/](pluginwire/);认识「驱动」的只有 [adapter_driver.go](pluginhost/adapter_driver.go) · [provision.go](pluginhost/provision.go) · [remote.go](pluginhost/remote.go)。将来推送通道进程外化时,加 `adapter_push.go` 而不复制通用核心。
- `plugin.enabled` **出厂为 `true`**([default.yaml](default.yaml)):协议驱动全部来自插件,关掉它等于网关不支持任何协议。
- 替换已加载插件的二进制即**热升级**(见 [pluginhost/upgrade.go](pluginhost/upgrade.go));插件文件消失 = 协议消失,但**刻意保留 `iot_protocol` 行**(前端下拉还在,设备却被跳过)。

### 采集引擎([collector/engine.go](collector/engine.go))
- 从 SQLite 加载活跃设备与地址(`status=1`),经 WorkerPool 并发调度 `driver.Driver` 轮询。
- 后台 watcher 每 10s 计算配置 checksum,设备/地址变更自动**热加载**(`Refresh` 零中断换任务)。
- 采集结果通过 `RecordSink` 接口交给下游(绑定为 `push.Engine`)。

### 推送引擎([push/engine.go](push/engine.go))
- 实现 `collector.RecordSink`,按设备分组后非阻塞分发给所有活跃通道。
- 同样带 watcher 热加载(`push_channel` 表),配置未变的通道保留运行实例。
- **通道 = 类型 + 实例名**:`push_channel.type` 决定工厂,`name` 是实例名,同一类型可建多条。
  旧库迁移为 `type = name`;`BeforeCreate` 在 Type 为空时回退 Name。
- 通道注册统一走 `push.RegisterType(ChannelTypeMeta, factory)`;旧的 `Register` 等价于
  `MessageBased:false` 的类型。`SupportedChannelTypeMetas` 供 `GET /pushChannel/listTypes`。

### 载荷编码与出站映射([push/encoder.go](push/encoder.go), [push/codec/](push/codec/))
- `Encoder` 只负责 `PushBatch → []EncodedMessage`;`native-json` 的字节契约由
  `codec/nativejson` 唯一实现(golden 测试钉住),MQTT 只把序列化入口换成 `encoder`。
- `mapping-json` 把 `CollectedRecord` 映射为客户 JSON,复用 [mappingdsl/](mappingdsl)
  的 `path`/`const`/`template`/`map`/`transform`,并支持 `type`、`emit`、`envelope`、
  `batchFields`。细节见 [docs/push-channels.md](docs/push-channels.md)。
- `script` 格式([push/codec/scriptpayload](push/codec/scriptpayload))用 goja 执行
  `function encode(batch, ctx)`,返回 string/string[] 作为一条或多条载荷。与订阅脚本
  不同:每次 `Encode` 新建 runtime(通道多 worker 并发、断网补发会重放同一批次),
  不提供跨批状态;脚本在保存时预编译并校验。
- `push.NewTemporaryChannel` 用与运行期相同的工厂构建未启动实例,供 `testConnectivity`、
  `preview`、`testSend` 复用;这三个能力走可选接口,不扩充 `push.Channel`。
- `messagechannel.Channel` 是消息类通道通用骨架(Outbox + 编码 + Transport + 断网补发);
  HTTP/Webhook(注册名 `http`)基于它实现。MQTT 保留自己的 paho 循环。

### 推送通道的共享骨架([push/outbox.go](push/outbox.go), [push/drain.go](push/drain.go), [push/writer.go](push/writer.go))
每个通道 = **`push.Outbox`(队列/断网缓存/状态)+ 通用循环(攒批下发 / 补发 / 重连)+
通道自己的负载构造与传输**。mqtt / tdengine / influxdb 的这段逻辑此前各复制了一份,现只有一处实现:

- **入队路由**(`Outbox.Enqueue`,绝不阻塞采集线程):断连且启用缓存 → 直接落盘;否则内存队列快路径;
  队列满 → 启用缓存则溢写,未启用则丢弃最旧。
- **断网缓存**(`spool`,即 `push_outbox` 表,按 `channel_id` 隔离):落盘协程串行写 SQLite,
  写成功即 ping 补发唤醒(`Notify`/`WaitDrain`,事件驱动,空闲时对 SQLite 零查询,兜底 timer 仅防漏 ping)。
- **攒批下发**(`AggregateWriter.Run`):消费队列、把多批聚合成一个负载,行数达 `MaxRows` 或
  时间到 `Interval` 双触发刷出;写入失败且启用缓存时**整窗**转投缓存,未启用则整窗计入 dropped。
  负载构造由通道的 `push.Aggregator` 提供(tdengine 的 `insertBuilder` / influxdb 的 `lineBuilder`,
  见两侧 channel 文件末尾的适配方法)。**契约:新构造的空聚合器必须接受任意批次** ——
  调用方在 `Append` 返回 false 时「刷出当前负载 → 用同一批次重试」,空负载也拒绝会死循环。
- **补发**(`Outbox.DrainLoop` + `DrainFunc`):按入队序回放,成功即删。`DrainPolicy` 按通道错误语义选择
  —— 写入被下游明确拒绝(SQL 报错 / HTTP 4xx)才判**毒批次**(删除并记 dropped,避免队头永久阻塞),
  纯超时类失败(mqtt)不判毒;`Narrow` 置位后逐批试写,把坏点从聚合组里隔离出来,避免连坐误删。
  聚合类通道复用 `GroupedDrainer`(分组/切分/删除算法只有一份),逐批发布的 mqtt 自备 `DrainFunc`。
- **重连**(`Outbox.ReconnectLoop`):首连立即尝试,之后仅在断开时按间隔重试;
  真正的建连由通道的 `tryConnect` 负责,成功时须置位连通状态并 `Notify` 唤醒补发。
- **生命周期有硬顺序**:`Start` → 通道跑自己的 worker 组 → `Close`(关 quit)→ 等 worker 组退出 →
  `Wait`(等落盘协程排空)。**worker 停服前还会把 outbox 中写失败的批次转投缓存**,提前收尾会丢数据。

### MQTT 订阅([subscribe/](subscribe/))
- 管理外部 broker 上的多条订阅:每行 `mqtt_subscription` = 一个独立 paho 客户端 +
  一个解析器实例。收包后解析成 `collector.CollectedRecord`,再经
  `push.Engine.PushRecordsToTimeSeries` **只投递到时序数据库类通道**(TDengine / InfluxDB)。
- `topic` 列支持**英文逗号分隔多个过滤器**(与推送通道 `buildTopics` 同口径:逐段 trim、
  丢弃空段、去重保序),一次 `SubscribeMultiple` 全部订阅,共用该行的 QoS 与解析器;
  单个 topic 的原配置行为不变。通配符 `+` / `#` 照常可用,`topicPattern` 匹配的是
  实际到达的 `msg.Topic()`,与配置里写了几段无关。`connSig` 用拆分后的过滤器拼签名,
  只改空白/重复项不会触发重连。
- **不投 MQTT 等非时序通道是刻意的**:订阅主题与发布主题相同时会形成自激。判据是可选接口
  `push.TimeSeriesChannel`(新增通道若写时序库需实现 `IsTimeSeries() bool`)。
- **解析与连接解耦**([parser/](parser/),完整规范见 [specs/parser-plugins.md](specs/parser-plugins.md)):
  外部报文结构不可预知,解析器按 `parser_type` 注册(`builtin` / `script` / 将来的
  `plugin:<name>`)。输出固定为 CollectedRecord 白名单,**一条记录一个 value**;
  插件不得自定义 measurement/tags/fields。实现写作指南见
  [docs/parser-scripts.md](docs/parser-scripts.md)。
- **配置真源按类型分列**:`builtin` 读 `mapping_json`(历史列,零迁移);
  其它类型读 `parser_config`。同一类型绝不能有两份配置。`sample_topic` 只供预览
  `topicPattern`,运行期不参与解析(迁移 `019_mqtt_subscription_parser.sql`)。
- **builtin DSL**(实现在 [parser/mapping/](parser/mapping/)):`root`(`$` 默认;整篇报文是数组时
  自动逐条拆条,`$[*]` / `$.data[*]` 仍可显式指定)、`fields`(`path` / `const` /
  `template` / `map` / `topic`)、`filter`(隐式 AND);另有
  `topicPattern`(如 `factory/{deviceId}/telemetry`,身份可直接来自主题)、
  `fanout`(一条报文多个测点 → 每项一条记录)、`transform`(scale/round/trim/default/
  hexDecode/base64Decode/bitfield/regex 白名单算子)。目标字段是 CollectedRecord 的 11 个字段白名单,
  未知字段在保存时直接拒绝。身份字段的目标名是 `deviceId` / `deviceAddressId`
  (早期配置里的 `deviceID` / `deviceAddressID` 在解析时自动归一,老订阅仍可加载;
  CollectedRecord 的 JSON tag 保持原样,不影响断网缓存里已有批次)。数字统一 `UseNumber` 解码,大整数不丢精度。
- **script 层**([parser/script/](parser/script/)):脚本必须定义 `function parse(msg, ctx)`;
  只注入 `msg`/`ctx`,不提供文件、网络、`require`、Go 对象。逐报文超时
  (1–500ms,默认 50),超时后**重建 VM 并清空 `ctx.state`**;未捕获异常只失败该条报文。
  `ctx.get` 返回字符串(大整数不丢精度),`ctx.emit` 走同一个字段白名单与记录上限。
  goja 非并发安全:每个订阅一个 VM,只在解析 worker 中调用。
- **收包管线**:paho 回调只做大小检查与入队(有界 8192,满时丢最旧并计 `dropped`,
  绝不阻塞回调);独立 worker 串行消费,批 ≤256,解析后投递。
  `SetOrderMatters(true)` 保证脚本的跨报文状态按到达顺序。`Stop()` 顺序:
  断开 → 关队列 → 等 worker 排空 → 关闭解析器 → 停止点位落库器。
- **签名拆分**:`connSig`(broker/port/clientId/账号/topic/qos)变化才重建 paho;
  `parserSig`(类型 + 配置原文)变化只**原地替换解析器**,不断开 MQTT。
  未变化的订阅不构造解析器(脚本编译不便宜)。状态计数含 `messages/records/errors/skipped/dropped`。
- `collectedAt` 是可选目标字段:报文自带时间时映射进来(epoch 秒/毫秒/微秒/纳秒、
  `YYYY-MM-DD HH:mm:ss[.SSS]`、`YYYY/MM/DD`、RFC3339 均可识别),归一成毫秒精度本地时间;
  未配置或无法识别时留空,时序通道逐条回落到批次落地时间。本地点位采集不使用该字段。
  `map` 用于外部类型/质量码翻译(`kind`、`quality`):按路径取原始值后查映射表,
  键比较忽略大小写并对纯数字做数值相等;未命中走 `default`。`quality` 的 `map`
  必须配置整数默认值,避免把外部质量码原样当成网关质量码。
- **设备/点位元信息落 SQLite**([subscribe/devicePointStore.go](subscribe/devicePointStore.go)):
  转换成功的记录在投递时序库的同时,把 `deviceId / deviceName / deviceAddressId /
  deviceAddressLabel / kind` 异步 upsert 进
  `mqtt_subscription_device_point`,自然键 `(device_id, device_address_id)`(与时序库身份口径一致)。
  时序库只存 ID 与数值,名称/说明在查询侧无处可查,这张表供后续统计分析按名称关联;它**不存采样值**。
  收包协程只做内存查重 + 非阻塞入队(队列满则丢弃、下一条报文重试),写库由独立协程攒批完成
  ——SQLite 只有一条写连接,不能在 paho 收包协程里直接写。首次出现与元信息变化时立即更新;
  空值不覆盖已有非空值(兼容未配置名称字段的历史订阅)。
  管理器 `Start/Stop` 与落库器同生命周期,停机前排空队列。
  对外查询走 `GET /mqttSubscription/pageMqttSubscriptionDevicePoint`(分页 + 设备/点位 ID、名称模糊过滤
  与来源 `source` 过滤 —— `manual` / `auto` 分开检索,空 = 全部;
  `/agentApi` 镜像归为 read),前端页面在「订阅管理 → 订阅设备与点位」。
  该页还支持**手工新增 / 编辑 / 删除 / Excel 导入**(`source=manual`):手工行是运维的最终确认,
  自动上报只更新 `source=auto` 的行,upsert 语义见 `subscribe/devicePointUpsert.go`;
  导入按自然键 upsert(已存在则覆盖元信息),自然键创建后不可修改。
  编辑会把该行 `source` 置为 `manual`(即**接管维护** —— 不这么做,下次元信息变化
  就会被报文覆盖,编辑等于没保存);因此页面把这列称为「维护方式」(自动同步 / 手工维护),
  而不是「来源」。手工行可随时经 `POST /mqttSubscription/restoreMqttSubscriptionDevicePointAuto`
  交还自动收录(只改 `source`,元信息保留;已是 `auto` 的行幂等成功)。
  删除**只对手工行开放**(单条 `GET /mqttSubscription/deleteMqttSubscriptionDevicePoint`,
  自然键走查询参数 `deviceId` / `deviceAddressId`;批量
  `POST /mqttSubscription/batchDeleteMqttSubscriptionDevicePoint`,请求体
  `{items:[{deviceId,deviceAddressId}]}`,上限 200):单条删除对自动行返回 60026 拒绝,
  行不存在返回 60025;批量删除**不因个别条目删不掉而失败**(自动行、已被他人删除的
  行都跳过),返回的实际 `deleted` 可能小于 `requested`。
  恢复 / 删除 / 批量删除成功后,服务层会**作废这些点位的元信息指纹**
  (`Manager.InvalidateDevicePoints` → `devicePointStore.Forget`),因此**下一条报文
  到达时**就会重新入队:恢复即按报文值覆盖元信息,删除即重新收录为 `auto`。
  这一步不能省 —— 落库器在内存里按点位缓存指纹,指纹不变时 `Record` 会一直跳过,
  表现为"点了恢复,值却没变 / 删掉的自动记录再也不回来",只能等元信息变化或网关重启。
  作废只清内存 map(持 store 的锁),不动落库协程状态;批量删除在事务提交后统一作废。
- 订阅配置每 10s 比对校验和热加载;服务层增删改后也会立即 `Refresh()`。
  连接未变时只换解析器;只有连接参数变化才断开重连。

### 协议驱动注册表([driver/driver.go](driver/driver.go))
- **`driver/` 下没有任何协议实现**,只有协议无关的契约与注册表。全部协议由进程外插件在运行期经 `pluginhost` 调用 `driver.RegisterExternal("协议名", NewFunc)` 注册(内置协议已全部移除),`collector` 用 `driver.Create(protocol)` 创建实例。
- 数据类型通过全局 `TypeRegistry`([driver/typeRegistry.go](driver/typeRegistry.go))注册,**协议作用域自动加前缀**(如 `modbus.bool`、`s7.int16`),查询不区分大小写。插件的 scope 与类型映射在加载时经 `driver.RegisterScope` 写入运行时扩展层;`driver/dataTypeMap.go` 里的 `scopeTypeMap`/`scopeProtocols` 两张内置基线表**已清空(变量保留、内容为空)**,用于挡住将来若有人把协议编回主二进制的情况。
- 协议名与 scope 沿用内置时代的原值(`ModBus.TCP`/`modbus` 等),因此 `iot_protocol` 行、`device.protocol_id` 与前端表单**零迁移**;约束只剩「不得与另一个插件的 scope/协议名撞名(含大小写变体)」。

### 局域网自动发现([discovery/announcer.go](discovery/announcer.go))
- 网关周期性把 `iot_gateway` 单行表里的身份(id/名称/IP/端口/版本/描述)以 UDP 广播发到
  各网段的广播地址,并单播应答管理平台发来的探询,使其**无需预配置、无需凭据**即可发现本网关。
- **一个套接字双向**(绑定 `0.0.0.0:9083`):拆成收发两个套接字后回包源端口会变成随机值,
  探询方就没法把请求与应答对起来。逐网卡发定向广播(`ip | ^mask`),**不发 255.255.255.255**。
- **报文是对外契约**,真源在 [discovery/message.go](discovery/message.go),完整约定见
  [specs/discovery.md](specs/discovery.md)。管理平台侧照该文档实现接收端。
- **无认证,故这条通道上的内容对同网段主机是公开的**。报文里带一个 `apiKey` ——
  启动播种的默认开放接口密钥(`open_api_secret` 里名为 `default` 那条),让管理平台
  发现网关后**无需人工录入密钥**直接调开放接口。这是唯一的例外,额度也只有这一个:
  `apiKey` 之外不得再加任何秘密(token/口令/设备凭据)。担心泄露就关
  `discovery.enabled`(关掉后完全静默,代价是平台只能手工录入 IP+密钥)。
  默认密钥缺失时照常广播、只是省略该字段。
  纯标准库,无 mDNS/zeroconf 依赖;`discovery.enabled` 出厂 true。
- 身份**每轮广播都重读**库(单行表、微秒级),因此系统设置页改名无需重启即生效。
  发布的是**当前运行端口**(`cfg.Server.Port`),不是 `iot_gateway.port` 那一列(只在首启写入)。
- 生命周期照 `alarm.ChannelMonitor`,但**先 `listen` 成功再置 `stopCh`** ——
  `Start()` 会失败(端口占用),而 `main.go` 只记日志、随后仍会 `defer Stop()`。

### 局域网设备扫描发现([devicescan/](devicescan/))
- **与上面的「局域网自动发现」方向相反**,别混:`discovery/` 是网关广播"我在哪"
  (供管理平台发现),`devicescan/` 是网关主动问"网段里谁在"(扫出 PLC 并一键建成设备)。
  包名刻意分开,共用 discovery 词根会让日志与文档里出现两个含义相反的名字。
- **职责边界是本功能的支点**:网关规划目标(枚举本机网段、展开主机地址)、控制并发、
  限速、统计进度、取消;插件只回答"你给我的这个 IP:端口,是不是我的设备"。
  广播型协议(OPC UA? 不,EtherNet/IP 的 ListIdentity、FINS/UDP 的节点查询)
  由网关把各网段的**子网广播地址**也加进目标列表即可覆盖。
- **能力与实现都由插件给**:`Meta.Protocols[].Discovery` 声明(可选字段,故不递增
  APIVersion),`driversdk.Probers` 给实现。声明了却没有 prober ⇒ 插件启动即失败。
  串口协议不得声明(目标模型是 host/port),`Meta.Validate` 会拒。
- 线协议是 `probe` 方法,**无会话、一次一个目标**。完整契约见
  [specs/device-scan.md](specs/device-scan.md) —— 插件作者照它实现。
- **任务只在内存里**:不落库,重启即丢,过 `deviceScan.jobTTLMinutes` 后惰性清理
  (没有 reaper 协程,过期判定放在 Submit/Status 里做)。同一时刻只允许一个任务。
- **`Engine` 没有 `Start()`**:没人发起扫描前不持有任何资源,只有 `Stop()`
  (取消在跑的任务、等在飞探测收尾,上限 2s)。main.go 里 `defer DeviceScan.Stop()`
  **必须注册在 `DriverManager.Stop()` 之后** —— 在飞探测直接发给插件进程,
  要在插件退役之前收尾。
- **三处不能踩的坑**(都写在代码注释里,动这块之前先读):
  ① `pluginhost/client.go` 的 `ctx.Done` 只把 `DeadlineExceeded` 计入卡死计数 ——
     取消扫描会掐掉全部在飞探测,无条件计数会让"取消一次"顶满阈值并**杀掉整个插件进程**
     (该插件下所有协议、所有设备一起断)。回归见 `pluginhost/client_test.go` 的
     `TestCancelDoesNotCountAsTimeout`。
  ② probe RPC 超时**由插件自报的 `MaxBudgetMs` 推导**(+3s 余量),不设全局配置旋钮 ——
     全局旋钮配小了就是"RPC 先超时、插件还在跑、用户看到扫不到设备"。
  ③ **不建扫描能力缓存表**:热升级只换 `lp.meta`,而 `registrationSig` 不含 Discovery,
     缓存会陈旧。每次遍历 `m.plugins` 现算。
- **网段过滤规则只有一份**:`discovery.eachUsableIPv4` 同时供广播目标与扫描目标使用。
  两处规则分叉只会表现为"某个网段扫不到设备",不报任何错。
- 只允许扫**本机网段**(不接受任意 CIDR,否则网关就成了通用端口扫描器);
  展开时只排除网络地址与广播地址,**本机地址照扫**(与网关同机的设备是真实存在的,
  静默跳过会让用户从"没扫到"得出"那里没有设备")。

### 设备点位范围扫描(设备地址页的「自动发现点位」)

- **与上面的设备扫描是相反的两件事**,别混:设备扫描问"网段里谁在"(目标由网关规划、
  无会话、可取消);点位扫描问"**这台已存在的设备**上有哪些点位"(目标由 `deviceId` 定、
  **复用那台设备的采集会话**、一次 RPC 覆盖整段、**不可取消**)。
  代码分布:契约 [driver/pointDiscovery.go](driver/pointDiscovery.go)、
  宿主 [pluginhost/pointdiscovery.go](pluginhost/pointdiscovery.go)、
  插件套件 `driversdk/serve.go` 的 `handleDiscoverPoints` / `selectSession`、
  Modbus 实现 `driversrc/modbus/modbusdrv/pointscan.go`(参考实现共六份,按形态挑:
  扫地址范围的看 Modbus/S7、按层枚举的看 OPC UA、**按协议自带的清单枚举的看
  `driversrc/dlt645/dlt645drv/pointscan.go`**)、
  服务 `service/devicePointScanService.go`(挂在与设备扫描同一个 `DeviceScanService` 上)。
- **产出的是「该范围内读得到的地址清单」,是候选清单而不是点位表** ——
  设备不会告诉你它有哪些点位。因此插件**不做零值过滤**(过滤放前端),
  界面与文案必须一直说清这件事。完整契约见 [specs/point-discovery.md](specs/point-discovery.md)。
- **五处不能踩的坑**:
  ① **finder 不得走 `drv.Read`**。驱动自己的 `Read` 读设备的 cfg 做区间规划,
     配置模式(`startAddress > 0`)下会**静默给出错位的值**,而照它建出来的点位会
     永远取到**别处**的数据(在时序库里表现为一条完全合理的曲线)。正确形态是取
     驱动自己的 client 直接读。
     (先拒绝配置模式那条守卫**只对 Modbus 那种形态成立**,见下面那条例外 —— 别照抄。)
  ② **块间必须释放会话锁**。一次 `Do` 覆盖整段扫描 = 把采集卡死几十秒
     (与 `pingSessionBudget` 同一条理由),所以主循环每块一次 `req.Session.Do`。
     回归见 `driversdk/pointscan_test.go` 的 `TestDiscoverPointsReleasesLockBetweenChunks`。
  ③ **串口协议复用不到会话时绝不回落临时实例**。回落会抢占串口句柄,采集侧下次重连
     报 Access is denied 并退避 —— "点一次扫描把设备打成离线"。回退表见 specs 的 §5。
  ④ **网关侧的 7000ms 钳制与 `main.go` 的 10s 关机宽限期互相耦合**:
     这条链路是**同步 HTTP**,`srv.Shutdown` 会等在飞 handler,超了就是"网关停不掉"。
     **改一个必须同时改另一个**,回归见 `pluginhost/pointdiscovery_test.go` 的
     `TestBudgetWithinGatewayCap`。
  ⑤ 能力**现算,不建缓存表**(与设备扫描同一个坑:`registrationSig` 不含 `PointDiscovery`)。
- **地址写法每个协议选自己那一种,但都必须能被该协议自己的解析器往返解析**。
  共同的不变量只有这样一条。违反它的代价**已经收敛到那一个点位**:进不了区间规划的
  点位在计划期就被摘出计划(`partitionBadAddrs` / `partitionBadTags`,`Read` 侧共用的
  判据见各包 `range.go`),`Read` 不再报错,该点位单独标 `QualityBadAddress` 并由
  点位质量报警报出「地址无效」,其余点位照常采集、设备保持在线 —— 六个区间规划器
  (modbus/s7/fins/mc/Omron.CIP/Rockwell.CIP)都是这个形态,
  各包 `badaddr_test.go` 是它的回归。
  **别退回老形态**:那之前 `CalcXRanges` 全量解析、遇第一个非法地址就返回 error,
  采集侧只看到一个 `err`,于是把这个**配置错误**当成设备故障 —— `incError` +
  `reportState(false)`,连续三轮落一条**断联**报警并推通知,而设备全程在线
  (`IsConnected()` 为真,重连逻辑根本不启动),`forEachBatch` 还会把坏地址之后的
  批次一起跳过。运维从这里得出的结论(查网络、重启 PLC)与真实原因完全无关。
  所以结果里的地址列在前端**仍然只读** —— 点扫产出的是候选清单,不该让用户顺手改成
  一份自家解析器认不出的写法。
  各协议的选择:Modbus 用 IEC `%MW/%IW/%MX/%IX`(0-based,传统 `40001` 风格在满量程、
  差一、功能区三处都会坏)、S7 用 `DB1.DBW100`/`M0.1`、FINS 用 `D100`/`CIO0.00`、
  MC 用 `D100`/`D100.5`/`M10`(X/Y 是八进制)、DLT645 用裸数据标识(`02010100`/`9010`,
  字典未收录的才写 `DI:字节数:小数位[:标志]`)。完整论证见 specs 的 §8。
- **现在没有"仍然批次致命"的点位级条件了**。FINS/MC 的「单点位跨度超过
  `maxReadWords`」与 Modbus 配置模式的 `addr < startAddress` 曾**刻意**留在批次致命
  那一侧,理由是"改成逐点得在 `CalcXRanges` 之外重算一遍跨度与钳制 —— 同一个问题
  两条路径两种答案"。现在这条路走通了,而且不是靠复制判据:把**判据本身**提成共享函数
  (`finsSpanWords` / `finsMaxReadWords`、`mcParseAddr` + `mcSpanTooLarge` +
  `mcMaxReadWords` + `mcStringLen`、`modbusExcluded`),`CalcXRanges` 与
  `partitionBadAddrs` 调的是**同一份** —— "漂移"这个反对理由因此消失,`CalcXRanges`
  自己那条错误退化成给直接调用方(契约测试)的兜底。
  **MC 的位模式点位不参与字跨度检查**(`mcSpanTooLarge` 里 `!addr.BitMode` 那一半,
  与 `mcParseAddr` 把检查放在非位模式分支里同源) —— 照抄字模式那半会把 `M10` 这类
  点位误判成超限。
  **Modbus 那半个来源(`addr < startAddress`)**今天**配不出来**:`ParseModbusTcpConfig`
  不读 `protocol_json` 的 `startAddress`(`modbusConfigRaw` 里没有这个字段,表单也只有
  `host`/`port`/`unitId`/`byteOrder`/`wordOrder`),所以 `cfg.StartAddress` 恒为 0 ——
  它和 [specs/point-discovery.md](specs/point-discovery.md) §8 那条「配置模式被拒」的
  守卫防的是同一件事(将来有人把 `startAddress` 加回表单),现在由
  `TestPlanFor_BelowStartAddressGoesToBad` 直接调 `planFor` 覆盖。**别照着它去现场
  复现** —— 现场能触发的那一半只有地址写法。
  仍**没有**校验的是点位保存那一层(前端只校验长度,服务层只校验非空/长度/唯一),
  这是刻意的:驱动全在插件进程里,主二进制不含任何协议解析器,后端要校验就得走插件 RPC。
- **第五种形态:字典枚举且带采样值**(`DLT645.Serial` / `.TCP`,**两个协议都声明**)——
  点位来自**协议自带的数据标识字典**(`dlt645drv/dictionary.go`,2007 版 44 条 / 1997 版
  18 条),不是设备自报的;而"读一遍"既是判定这块表有没有该数据标识的手段、又顺带取到了值。
  两处与既有实现不同,别照抄:**① 结果级 `AssumedCommonDataType` 与逐点 `CommonDataType`
  同时非空** —— 逐点类型因为每个数据标识的输出形态由它自己决定(表号是 BCD 数字串、
  状态字是二进制位域、电量是 2 位小数),结果级那个只是"本次带采样值"的载体(前端据它
  点亮「显示 0 值」开关,"留空"那条规矩只对不读值的协议成立);**② 成帧打包要先核对应答
  数据域的总长** —— 一帧打包多个数据标识时,回显校验只保证"这一项的标识对"、不保证
  "数据落在正确的位置",设备少回一项会让后面每一项的窗口整体前移,解出**看似合理实为
  别处**的值。它还把"设备静默超时"与"掉线"分开(先复探一次与 Ping 同一个数据标识):
  645 的「静默」有歧义,老表对不支持的数据标识常常干脆不答——**这条假设需要真机确认**。
  另外它**不做**「读到第一个读不到就停」(字典不是连续地址空间)、**不做**请求数上限
  (降级树有界),也不声明 `Discovery`。
- **几条协议特有的形态例外,别当成通用规则照抄**(详见 [specs/point-discovery.md](specs/point-discovery.md)
  §3 末 / §6 / §9):① **「读到第一个读不到就停」只对地址空间单片连续的协议成立** ——
  MC 的 `X`/`Y`(按槽位)与 `B`/`W`(按模块)中段可以有空洞,照抄会让清单从空洞处
  **静默断掉**;② **「先拒绝配置模式」那条守卫防的是 Modbus 特有的形态**
  (地址含义由 `protocol_json` 的 `startAddress` 决定)—— 你的协议没有这种形态就
  **不要**加,它只会凭空多出一条拒绝条件;③ **「结果级 `AssumedCommonDataType`
  留空」只对不读采样值的协议成立**(Rockwell/OPC UA)—— 逐点类型 + 结果级类型
  同时非空是合法的(645 就是这样),照抄"留空"会让前端隐藏「显示 0 值」开关;
  ④ **「数据域总长校验」只有一帧打包多个数据标识、且各项长度不同的协议才需要**。
  同理,**串口能不能声明点扫要按"那条路有没有可依据的实现"判**:FINS 不声明
  (未真机验证),MC 声明(复用路径已实现且采集一直在用),645 两个协议都声明。
- **不开新的总开关**:点扫复用 `deviceScan.enabled`。错误码只新增
  `40027 该协议不支持点位扫描`,其余复用设备扫描那一组。
- 两篇 spec **必须成对修改**([specs/device-scan.md](specs/device-scan.md) ↔
  [specs/point-discovery.md](specs/point-discovery.md)):它们共同依赖三条道理
  (不建能力缓存表 / RPC 超时按插件声明推导 / 串口独占与会话复用),只改一篇必然漂移。

### 运行期配置开关([runtimecfg/](runtimecfg/))
- `default.yaml` 是**默认值**的唯一真源;[runtimecfg/](runtimecfg/) 在它之上叠加
  SQLite `system_config` 表里的覆盖值(页面 / `/systemConfig/*` 写入),优先级更高、
  重启后保留。「恢复默认」= 删除覆盖行,不是把默认值写进表。
- 首批登记 4 个开关:`discovery.enabled`、`deviceScan.enabled`、`push.changeOnly`
  (立即生效,组件从 `runtimecfg.Reader` 现读),`plugin.enabled`(重启生效,插件管理器
  在构造时冻结 —— `Stop` 是终态的)。
- 更新顺序固定为**校验 → 落库 → 发布快照 → apply**;applier 失败时恢复旧快照并
  补偿写回旧行。**不要在 apply 期间持有 SQLite 事务**:单写连接会被 `discovery.Stop()`
  这类等待协程退出的操作占住,拖垮采集与 outbox 落盘。
- 新增开关的四步清单见 [specs/runtime-config.md](specs/runtime-config.md);注册表完整性
  由 `runtimecfg/registry_test.go` 反射 `configFile.Config` 的布尔叶子守住。
- `configFile.ReloadConfig` 仍无调用方,**不要**用它做第二套覆盖机制。手工改
  `default.yaml` 不会被监听,动态修改只走页面 / API。

### 前端
- `web/` 只含构建产物 `web/dist/`,经 `go:embed` 嵌入二进制,由 [web/embed.go](web/embed.go) 的 `ServeStatic` 在 `/admin/*` 路径分发(SPA 回退 index.html)。
- 前端源码在本仓库根部的 `frontend/`(Vue 3 + Vite + Element Plus + form-create)。
  **`frontend/dist` 被 gitignore,真正提交的是嵌入产物 `backend/web/dist/`** —— 改完前端要
  `npm run build` 后把 `frontend/dist` 同步进 `backend/web/dist`(只增删变化的文件,
  别整目录清空:那是 `go:embed` 的目标,清空会让二进制里没有前端)。
- 新增页面要同时改四处:`frontend/src/views/.../IndexView.vue`、
  `frontend/src/router/index.js`(路由 + `meta.titleKey`)、
  `frontend/src/components/SideMenu.vue`(菜单项)、
  `frontend/src/i18n/locales/{zh-CN,en}.js`(**两侧必须同步**,没有测试会替你检查)。

### 路由
- API 路由按领域注册,无统一前缀(如 `/device/xxx`、`/user/xxx`),见各 `xxxRouter.go`。
- SPA 走 `/admin/*`;`/health` 为健康检查。

## 扩展点(新增模块的标准步骤)

### 新增协议驱动

**唯一路径是进程外插件。** 内置驱动这条路已经不存在 —— 原有的 7 个内置协议驱动已全部搬成
`driversrc/` 下的插件(此后新增的协议如 `abb/` 同理,直接就是插件),主二进制里不再有任何协议实现,
`driver/` 只剩协议无关的契约与注册表,
`driver/dataTypeMap.go` 的 `scopeTypeMap` / `scopeProtocols` 内置基线表也已清空。
(旧版本文档在这里写过一条「A. 内置驱动」的步骤清单,已删除;它依赖的
`driver/<protocol>/` 包、`init()` 注册、`collector/task.go` 空导入、播种迁移这套机制
都不再适用。真要走回内置,先得把基线表加回去,而那正是被刻意留空挡住的事。)

驱动做成独立可执行文件,丢进 `plugins/` 目录即自动加载。适合新 PLC 协议这类
「现场才确定、不希望为它重编网关」的场景。作者指南见 [../docs/driver-plugins.md](../docs/driver-plugins.md)。

1. 新建 `driversrc/<name>/main.go`(`package main`)。
2. 实现 `driver.Driver` 接口(Connect/Ping/Read/IsConnected/Close),调用
   `driversdk.Main(driversdk.Factories{...}, driversdk.Meta{...}, driversdk.Capabilities{...})` 注册。
   第三个参数是**能力实现**,两个字段各自可选,不需要就给零值
   (`driversdk.Capabilities{}`,**不是 `nil`** —— 它是结构体):

   ```go
   driversdk.Main(factories(), meta(), driversdk.Capabilities{
       Probers:      probers(),      // 局域网扫描发现(Meta 里的 Discovery)
       PointFinders: finders(),      // 点位范围扫描(Meta 里的 PointDiscovery)
   })
   ```

   一旦某个协议在 `Meta` 里声明了 `Discovery` / `PointDiscovery`,对应的能力表里就必须有
   那一项,否则**插件启动即失败**(与"每个协议都必须有工厂"同一条校验)。
   写 prober 的五条契约见 [../docs/driver-plugins.md](../docs/driver-plugins.md) §2.6,
   写 finder 的见同文 §2.7。
   **串口协议可以声明 `PointDiscovery` 但不能声明 `Discovery`** —— 这条不对称是刻意的,
   理由见 [specs/point-discovery.md](specs/point-discovery.md) §3。
3. `Meta` 一次性声明协议列表(一个插件可提供多个协议)、类型 scope、类型表与各协议的配置表单;
   类型表是**插件自带**的真源(通常放一个子包,如 `driversrc/modbus/modbusdrv/`),
   只依赖 `iot-gateway/driver` 契约,不再从网关注册表派生。
   **目录布局是拍平的一层**:目录名决定产物名 `iot-driver-<目录名>`,故必须唯一且不含层级
   (`omroncip` / `rockwellcip` 就是这么来的);子包目录名可与包名不一致。
4. **协议名与 scope 沿用原值即可**(有既有协议时),不再要求「全新前缀」;唯一约束是
   不得与另一个插件的 scope / 协议名撞名(含大小写变体),否则两个插件的类型会互相覆盖。
   **全新协议**(如 `abb/` 的 `ABB.RWS` / `abb`)没有原值可沿用,自取即可 —— 但一旦发布就冻结:
   协议名同时是 `iot_protocol.name` 与 `device.protocol_id` 的解析目标,改一个字就让既有设备
   静默采不到值。各插件用一条 `TestProtocolIdentityPinned` 把字面量钉住。
5. 构建产物名为 `iot-driver-<name>`,放在可执行文件同级的 `plugins/` 目录;
   `build.sh` / `release.sh` / `Dockerfile` / `build.bat` / `backend/package.bat` 按
   `driversrc/*/` 一层 glob 自动交叉编译并打包,`release.sh` 与 `Dockerfile`
   **在没编出任何插件时会失败**(插件是发行包的必需组成,不是可选项)。
   **替换已加载插件的二进制即可热升级**(默认开启,`plugin.disableHotUpgrade=true` 关闭):
   网关拉起新进程、经稳定间接层 `pluginLink` 把驱动切过去,靠世代号让既有驱动实例
   自动重连,**不触发全局 Refresh**;只有协议名集合或 SerialExclusive 变化才重建采集任务。
6. 若协议是把既有实现搬过来的,行为等价性靠**黄金基线**守:
   [pluginhost/testdata/parity/](pluginhost/testdata/parity/)(`addresses.json` 共享输入 +
   `<case>.golden.json` 逐点位结果 + `typemap/<scope>.json` 类型表与机器生成的协议名清单,
   由内置实现在移除前生成),比对入口是
   [pluginhost/pluginparity_test.go](pluginhost/pluginparity_test.go) 的 `Test*PluginParity`;
   读写格式见 [testutil/parity](testutil/parity/parity.go)。
   网关侧测试需要「某协议可用」时用 [testutil/drivertest](testutil/drivertest/drivertest.go)
   的 `EnsureSyntheticProtocol()` 自备合成协议,不要拉起真实插件进程。
   **全新协议(如 `abb/`)没有黄金可对**(仓库里从未有过它的第二份实现),验收改看两件事:
   插件能否被正确加载/注销,以及能否跑通完整采集链路 —— 模板见
   [pluginhost/abbplugin_test.go](pluginhost/abbplugin_test.go),它用 `startPluginManager` +
   `waitForDriverRegistered` + `collector.NewEngine` 断言到采集记录一级,不读任何黄金文件。
   **从别的项目移植过来的协议同理**(如 `dlt645/`,协议实现逐文件搬自 iot-gateway 的内置
   `driver/dlt645`):黄金基线是「本仓库的内置实现在被移除**之前**」冻下来的,移植来源不在
   本仓库里,就没有可对照的第二份实现,验收同样走上面这条 —— 见
   [pluginhost/dlt645plugin_test.go](pluginhost/dlt645plugin_test.go)。协议名与 scope 仍应
   沿用来源项目的原值,使两侧的 `device.protocol_id`、点位地址与类型名可以直接互相迁移。

网关侧实现见 [pluginhost/](pluginhost/)(进程监管、RPC 代理、运行时注册表注入、热升级)。

### 新增 / 扩展报文解析器

**默认先扩展 builtin DSL,不要一上来写脚本或插件。** 契约、分层与限制见
[specs/parser-plugins.md](specs/parser-plugins.md)。

- **builtin**:在 [parser/mapping/](parser/mapping/) 增加取值方式 / 算子 / 夹具;
  同步 `Rule.Validate`、前端映射编辑器与测试。新增算子必须进白名单并在保存时校验参数。
- **script**:脚本存于 `mqtt_subscription.parser_config`,无需重编网关;改 API 时同时更新
  [docs/parser-scripts.md](docs/parser-scripts.md) 与沙箱测试(超时、异常、精度、产出上限)。
- **进程外插件(未实现,仅在二进制/厂商 SDK/崩溃隔离时启用)**:套件 `parsersdk/` +
  源码 `parsersrc/` + 产物 `iot-parser-<name>` + 适配器 `pluginhost/adapter_parser.go`;
  线协议复用 [pluginwire/](pluginwire/),必须按批 `parse` RPC,禁止每报文一次调用;
  **不要复制** `process.go` / `upgrade.go`。宿主当前只扫描 `iot-driver-*`,接入时按前缀分流。
- 任何解析器都必须输出 CollectedRecord 白名单(**一条记录一个 value**),走
  `parser.RecordFromMap` 或等价校验;不得自定义 measurement/tags/fields。

### 新增推送通道
通道是**进程内**的插件(注册表 + 接口),不像协议驱动那样有独立的进程外运行时 —— 断网缓存
(`push_outbox`)是网关级的持久化保证,把通道搬到独立进程会让它失去单写者前提。
新通道只需写「怎么把负载发出去」,队列与缓存复用 `push.Outbox`:

> **若将来要把通道也做成进程外插件**(目录组织已为此预留:套件 `pushsdk/` + 源码
> `pushsrc/` + 适配器 [pluginhost/adapter_driver.go](pluginhost/adapter_driver.go) 旁边的
> `adapter_push.go` + 产物 `iot-push-*`),先解决两处协议缺口,否则目录定了也走不通:
> ① 现有线协议是**纯请求/应答**,而 `Outbox` 靠 `Connectivity.Connected()` 决定入队路由,
> 该状态由通道的连接回调产生,**需要新增一条反向通知帧**(插件→网关);
> ② 需要错误分类字段区分「下游明确拒绝(可判毒批次)」与「纯超时(不判毒)」,
> 否则 [push/drain.go](push/drain.go) 的毒批次语义跨进程即丢失。
> 另外要决定 [push/writer.go](push/writer.go) 的 `Aggregator` 留网关侧(每批一次 RPC)
> 还是随插件走(网关侧攒批/补发骨架作废)。

**消息类通道(发 JSON 到某个下游)优先走通用骨架**,不要复制 Outbox 循环:

1. 新建 `push/<type>/`,实现 `messagechannel.Transport`(`Probe` + `Send`),
   `newXxxChannel` 里用 `codec.New(cfg.Format, cfg.Mapping)` 构建编码器,再调用
   `messagechannel.New(...)` 返回通道(参照 [push/webhook/](push/webhook))。
   `Transport.Send` 必须把结果分类为 `FailureNone` / `FailureRetry` / `FailurePoison`。
2. `init()` 中 `push.RegisterType(push.ChannelTypeMeta{...}, factory)`;消息类类型要置
   `MessageBased:true` 并给出 `Formats`/`DefaultFormat`,需要预览/测试发送时再置
   `SupportsPreview` / `SupportsTestSend`。旧的 `push.Register` 等价于 `MessageBased:false`。
3. 新增 `push_channel_form` 行(迁移 `INSERT OR IGNORE`)供动态表单渲染;`format`/`mapping`
   不放进动态表单,由通道页外层控件合并。
4. 在 [main.go](main.go) 顶部**空导入**该子包(与 `_ "iot-gateway/push/mqtt"` 并列)。
5. 控制台类型下拉来自 `GET /pushChannel/listTypes`,不要在前端硬编码类型列表。

**聚合写入类通道(时序库/关系库)**仍各自实现 `push.Channel`:

1. 负载构造器实现 `push.Aggregator`(Append/Rows/Empty/Payload),
   `run()` 里起 N 个 `push.AggregateWriter{New, Write, MaxRows, Interval}.Run(ob)`,
   并用 `push.GroupedDrainer{...}.Drain` 作为 `DrainFunc` 交给 `ob.DrainLoop`。
2. 构造时用 `push.NewOutbox(...)`,`Start()` 里 `ob.Start()` 后 `go run()`;
   `Stop()` 里按 `ob.Close()` → 等 worker 组退出 → `ob.Wait()` 收尾。
3. `run()` 里挂 `ob.ReconnectLoop(interval, ch.tryConnect)`;`tryConnect` 建连成功时
   置位连通状态并 `ob.Notify()` 唤醒补发。
4. 同样用 `push.RegisterType` 注册(或 `Register` 表示非消息类),并在 main 空导入。

### 新增业务领域模块
1. 建 `model/po|dto|vo/xxx.go` → `service/xxxService.go` → `controller/xxxController.go` → `router/xxxRouter.go`。
2. 在 `service/wire_set.go`、`controller/wire_set.go` 追加构造函数。
3. 在 [wire.go](wire.go) 的 `ProvideRouteOptions` 追加 `WithXxxRoutes`,并执行 `go generate` 重生成 `wire_gen.go`。

## 开发命令
```bash
go run main.go           # 开发运行(默认端口 9082,见 default.yaml)
go build -o iot-gateway .        # 构建主二进制(不含任何协议实现)
go test ./...            # 测试
go fmt ./...             # 格式化
go generate             # 重新生成 wire_gen.go

# 协议插件:必须编到主二进制同级目录(plugin.dir 相对 os.Executable() 解析,不是 cwd)。
# **不编插件 = 网关不支持任何协议**(设备会被逐个静默跳过,前端看不出异常)。
go build -o ./plugins/iot-driver-modbus ./driversrc/modbus
```

一次性构建全部主二进制 + 全部插件请用仓库根目录的脚本:Linux/macOS 用 `../build.sh`
(或 `../release.sh` 出发行包),Windows 用 `..\build.bat`(内部调 `package.bat`)。
它们按 `driversrc/*/` 一层 glob 编译,默认产出 **四个平台**:
`iot-gateway`(linux/amd64)、`iot-gateway-linux-arm64`、`iot-gateway-linux-arm`
(linux/arm 32 位,固定 **GOARM=7**)、`iot-gateway.exe`(windows/amd64)。

主二进制与它的插件**必须在同一层**:网关按 `plugin.dir`(默认 `plugins`)相对
`os.Executable()` 解析。因此脚本给每个平台各配一个插件目录——`plugins-linux/`、
`plugins-linux-arm64/`、`plugins-linux-arm/`、`plugins-windows/`,目录名一律带平台后缀。
**各平台产物文件名相同,写进同一个目录会互相覆盖,且覆盖后不报任何错**——只会得到一个
插件架构不对、设备全被静默跳过的网关。插件还必须与主二进制**同 GOARM**,否则同样跑不通。
带后缀也有代价:目录名不再等于默认的 `plugin.dir`,**本地直跑 exe 之前要把那个目录改名成
`plugins/`,或把 `plugin.dir` 指过去**,否则一台设备也扫不到。发行包不受影响,包内统一是
包根 `plugins/`(`release.sh` 是独立打包流程,无此问题)。

arm32 的 `GOARM=7` 是编译期写死的(ARMv7-A + VFPv3 硬浮点)。**目标板若是 ARMv6/ARMv5**
(树莓派 1 / Zero 一代等),这个产物在板子上直接起不来 —— 得改成 `GOARM=5` 软浮点重编。

插件改动后无需重编主二进制:**替换 `plugins/` 下的二进制即热升级**(默认开启)。
本地改了插件源码要生效,重新 `go build -o ./plugins/iot-driver-<name> ./driversrc/<name>` 即可。

## 命名规范
- 包名: 小写单数 (`user`, `configFile`)
- 文件: 驼峰 (`userController.go`, `deviceDto.go`)
- 数据库: snake_case (`users`, `created_at`),迁移文件 `NNN_name.sql` 按序编号
- JSON: camelCase (`accessToken`, `serverTime`)
- 单元测试: `{entity}_test.go`
- 插件相关目录按**角色**命名,并列于 `backend/` 根下,互不嵌套:
  `pluginhost/`(宿主,一种) · `pluginwire/`(传输层,共用) ·
  `driversdk/` + `driversrc/`(驱动插件的套件与源码) · `plugins/`(运行时)。
  - **宿主与传输层只有一份**:`pluginhost/` 不认识任何插件种类(只依赖 `pluginwire`),
    各插件种类的差异全部收在同包的 `adapter_*.go` 里。将来接入推送插件时加
    `adapter_push.go` 即可,**不要**复制 `process.go`/`upgrade.go` 另起一个宿主 ——
    进程监管与热升级是最难写对、最容易悄悄漂移的部分。
  - 传输层独立成 `pluginwire/`(只依赖标准库):帧编解码与插件种类无关,驱动与将来的
    推送插件共用同一套帧,网关侧也复用它,两侧不会各自漂移出一份"差不多"的协议。
  - `driver/` 与 `push/` 是**契约**(各自领域 + 引擎),不属于这组角色目录,不要并进
    `driversdk/`(采集引擎与宿主都直接依赖契约,合并会让「采集引擎依赖插件 SDK」
    成为字面事实)。
  - 新增插件种类时的命名:套件 `xxxsdk/` + 源码 `xxxsrc/`,产物 `iot-xxx-<目录名>`,
    适配器 `pluginhost/adapter_xxx.go`。
  - **插件之间不得互相 import**:两个插件共用一段源码,就把它们的构建绑在了一起 ——
    对方目录一改名/删除/拆去别的仓库,这边报一句 `package not found`,而报错位置离
    真正的原因隔着两个插件。插件是要能独立改名、独立删除、独立热升级、独立拆仓库的
    产物,这条优先于"少写一份代码"。**需要就复制那几个纯函数**(如 EtherNet/IP 的
    ListIdentity 编解码:omroncip 与 rockwellcip 各持一份 `cipcore/`),代价(两份可能
    漂移)由"两边都用同一套独立实现的假应答驱动测试"兜底,而不是靠人工同步。
    注意这与 `pluginhost/`+`pluginwire/`+`driversdk/` 的"只有一份"**不矛盾**:
    那几个是**宿主与套件**(对全部插件一视同仁),不是某个插件的实现。
  - **设备扫描与点位扫描共用一套命名**(`devicescan/`、`pluginhost/discovery.go` ↔
    `pluginhost/pointdiscovery.go`、`driver/discovery.go` ↔ `driver/pointDiscovery.go`、
    `service/deviceScanService.go` ↔ `service/devicePointScanService.go`),
    它们挂在**同一个页面族与同一个总开关**上,错误码也大量复用 ——
    拆成两套名字会让人以为它们是两个独立的功能,进而在两个入口对同一种失败给出不同答复。
    但**语义上必须分清**:`discovery` 扫网段,`pointDiscovery` 扫一台已存在的设备。
  - `discovery/`(我告诉局域网我在哪)与 `devicescan/`(我去问网段里谁在)是**方向相反**
    的两件事,各自是独立包,不要合并;也不要把它俩的任何一个并进 `pluginhost/`
    —— 前者是网关自身的能力,后者是任务编排,宿主只提供 `ProbeTargets` 这一层。

## 规范文档
通用技术规范沉淀在 `specs/` 目录(架构、目录结构、开发标准、技术组件),代码风格与错误处理遵循其中的约定;`response` 统一响应、`appError` 错误处理、`enums/resultEnum.go` 业务错误码。
