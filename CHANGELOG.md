# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

All notable changes to this project are documented in this file (Keep a Changelog format, SemVer versioning).

## [0.2.0] - 2026-10-24

### Added

- **OPC UA Server 推送通道**：新增 `opcua` 类型，网关作为 OPC UA 服务端按设备/点位生成地址空间（`deviceIds` 限定范围、`nodeIdStyle` 选择 ID 风格），支持 Read / 订阅与变化通知、`None` / `Basic256Sha256` 安全策略，端点自动公布本机地址。迁移 `022_add_opcua_push_channel_form.sql`。
  **OPC UA Server push channel**: new `opcua` type exposing collected data as an OPC UA server with a device/point address space (`deviceIds` scope, `nodeIdStyle`), Read/subscribe with change notifications, `None` / `Basic256Sha256` security and automatic endpoint advertisement. Migration `022_add_opcua_push_channel_form.sql`.
- **HTTP/Webhook 推送通道**：新增 `http` 类型，支持 POST/PUT/PATCH、自定义请求头、Bearer / Basic / HMAC-SHA256 鉴权、超时与并发配置；2xx 成功，408/429/5xx 与网络错误重试，其余 4xx/3xx 丢弃；支持断网缓存补发。迁移 `021_add_http_push_channel_form.sql`。
  **HTTP/Webhook push channel**: new `http` type with POST/PUT/PATCH, custom headers, Bearer / Basic / HMAC-SHA256 auth, timeout/worker settings and offline spool replay; 2xx succeeds, 408/429/5xx and network errors are retried, other 4xx/3xx are dropped. Migration `021_add_http_push_channel_form.sql`.
- **出站映射（mapping-json）**：`mqtt` / `http` 通道可在 `config_json` 选择 `format` 与 `mapping`，把采集记录映射为自定义 JSON；复用 `path` / `const` / `template` / `map` 与 `transform`，新增 `type` / `emit` / `envelope` / `batchFields`。native-json 载荷保持逐字节不变。
  **Outbound mapping (`mapping-json`)**: `mqtt` / `http` channels select `format` and `mapping` in `config_json` to map records into a custom JSON payload, reusing `path` / `const` / `template` / `map` and `transform` with new `type` / `emit` / `envelope` / `batchFields`. Native-json output stays byte-for-byte unchanged.
- **通道多实例**：`push_channel` 新增 `type` 列，同类型可建多条通道；旧配置与接口保持兼容。迁移 `020_rebuild_push_channel_with_type.sql`。
  **Multiple channel instances**: `push_channel` gains a `type` column so one type can be configured multiple times; legacy configs and clients keep working. Migration `020_rebuild_push_channel_with_type.sql`.
- **载荷预览与测试发送**：新增 `listTypes` / `preview` / `testSend` 接口，控制台支持格式选择、映射编辑、预览与测试发送；`testConnectivity` 返回具体失败原因。
  **Payload preview and test send**: new `listTypes` / `preview` / `testSend` endpoints; the console supports format selection, mapping editing, preview and test send, and `testConnectivity` reports the concrete failure reason.
- **出站映射可视化编辑器**：通道弹窗内嵌字段表格，覆盖输出形态、取值方式、类型、默认值、变换与批次字段；保留高级 JSON 双向切换，新增只读接口 `POST /pushChannel/previewMapping`。
  **Visual outbound mapping editor**: a field table in the channel dialog covers output shape, sources, types, defaults, transforms and batch fields, with a two-way advanced JSON mode and a read-only `POST /pushChannel/previewMapping`.
- **脚本编码（script）**：消息类通道新增 `format: "script"`，用 goja 执行 `encode(batch, ctx)` 生成任意文本载荷；保存时预编译校验，每次编码使用独立 runtime。
  **Script encoding (`script`)**: message-based channels gain `format: "script"` running a goja `encode(batch, ctx)` to produce arbitrary text payloads; scripts are compiled on save and every encode gets a fresh runtime.
- **映射原语下沉**：新增 stdlib-only `mappingdsl` 包，入站解析与出站映射共用路径、模板、变换与标量转换；`parser/mapping` 行为不变。
  **Shared mapping primitives**: new stdlib-only `mappingdsl` package shared by inbound parsing and outbound mapping (paths, templates, transforms, scalar coercion); `parser/mapping` behavior is unchanged.
- 文档：[推送通道与出站映射](backend/docs/push-channels.md)；[推送载荷契约](backend/specs/push-payload.md) 增加格式选择说明。
  Docs: [Push channels & outbound mapping](backend/docs/push-channels.md); [push payload contract](backend/specs/push-payload.md) documents format selection.

### Changed

- **SQLite WAL 定期检查点**：自动 checkpoint 阈值显式设为 512 页（约 2MB）并经 DSN 下发；新增每 10 分钟一次的维护协程，先 `PASSIVE` 回写已合并帧，无读者时再 `TRUNCATE` 把 `-wal` 收缩到 0。外部进程钉住 WAL 时打 WARN 并给出已合并/总帧数，读者退出后自动恢复截断。
  **Periodic SQLite WAL checkpoints**: the autocheckpoint threshold is now explicit (512 pages ≈ 2MB) and applied through the DSN; a 10-minute maintenance goroutine `PASSIVE`-checkpoints merged frames and `TRUNCATE`s `-wal` back to zero once no readers remain. An external process pinning the WAL logs a WARN with merged/total frames, and truncation resumes after it exits.
- **SQLite 写入吞吐**：WAL 连接改为 `synchronous=NORMAL`，断网缓存写入把排队批次合并为一次多行 INSERT，实测约 1.86ms/批降至约 0.09ms/批。代价是掉电最多丢失上次 checkpoint 之后的提交（约 2MB），库不会损坏；合并写失败时退回逐条写。
  **Faster SQLite writes**: the WAL connection uses `synchronous=NORMAL`, and the offline-spool writer coalesces queued batches into one multi-row INSERT, cutting ≈1.86ms per batch to ≈0.09ms. Power loss can lose at most the commits since the last checkpoint (≈2MB) without corrupting the database; failed merged writes fall back to one row at a time.

## [0.1.4] - 2026-10-23

### Added

- **可插拔 MQTT 订阅解析器**：订阅可选用内置 DSL 或 goja JS 脚本解析，支持逗号分隔多主题、`topicPattern` / `fanout` / `transform` 算子与跳过 / 丢弃计数；仅改解析配置时原地热更新，不重连 MQTT。迁移 `019_mqtt_subscription_parser.sql`，控制台提供解析器编辑器。契约见 [parser-plugins.md](backend/specs/parser-plugins.md)。
  **Pluggable MQTT subscription parsers**: a subscription can parse with the built-in DSL or a goja JS script; adds comma-separated topics, `topicPattern` / `fanout` / `transform` operators and skipped / dropped counters. Parser-only edits hot-reload in place without reconnecting MQTT. Migration `019_mqtt_subscription_parser.sql`; the console gains a parser editor. Contract: [parser-plugins.md](backend/specs/parser-plugins.md).
- **订阅设备与点位手工维护**：新增来源列（自动 / 手工）与新增、编辑、Excel 导入 / 模板下载；手工行不会被自动收录覆盖，支持批量删除（≤200）并恢复为自动。
  **Manual maintenance of subscription device points**: adds a source column (auto / manual), create/edit, Excel import and template download; manual rows survive automatic upserts, and can be deleted in batches (≤200) and restored to auto.
- **日志文件下载**：`GET /log/downloadFile` 以附件流式下载原始日志（支持 Range），镜像到 `/agentApi`；日志列表与查看器均可下载。
  **Log file downloads**: `GET /log/downloadFile` streams the raw log as an attachment (Range-capable) and is mirrored in `/agentApi`; available from both the log list and the viewer.

### Changed

- **大盘趋势修正**：采样时间戳对齐整分钟，查询按墙钟裁剪窗口（长时间休眠不再把 6 小时窗口拉成更长的历史），累计时长取自环形缓冲的最旧样本。
  **Dashboard trend fixes**: sample timestamps snap to the minute grid, windows are trimmed by wall clock (a long suspend can no longer stretch a 6-hour window into more history), and accumulated duration is reported from the oldest ring-buffer sample.

## [0.1.3] - 2026-10-22

### Added

- **运行大盘**：新增每分钟一次的内存采样器，记录在线率、采集吞吐、坏点及推送队列/积压，保留最近 24 小时，提供 `GET /dashboard/trend?hours=1..24`（只读，镜像到 `/agentApi`）。趋势不落库，重启后从空开始积累。大盘首页重做：在线率环形、KPI 指标条、采集趋势图、最近事件与数据出口面板；趋势图按分钟网格合并成约 96 个桶，并用最小/最大值包络保留短暂掉线。
  **Live dashboard**: an in-memory sampler records online rate, throughput, bad points and push queue/spool depth once a minute, keeping 24 hours, exposed as `GET /dashboard/trend?hours=1..24` (read-only, mirrored to `/agentApi`). Trends are not persisted and start empty after a restart. The dashboard is rebuilt with an online-rate ring, a KPI strip, a telemetry trend, recent events and a data-outlet panel; the chart buckets samples into ~96 minute-aligned bars and keeps brief dropouts visible with a min/max envelope.

### Changed

- **管理端界面重构**：统一各管理页的页头、表格、空状态与弹窗；侧栏改为深色导轨，支持拖动调宽并记住宽度；设备列表与扫描页改版；图标迁移到 Tabler；补充 favicon 并重录 README 演示。
  **Unified admin UI**: page headers, tables, empty states and dialogs are standardized; the sidebar becomes a dark rail that can be resized and remembers its width; the device list and scan pages are redesigned; icons move to Tabler; favicon assets are added and the README demos re-recorded.

## [0.1.2] - 2026-10-21

### Added

- **运行期配置开关**：`default.yaml` 仍是默认值的唯一真源，新增「系统设置 → 运行开关」页面可在此之上叠加覆盖值：修改写入 SQLite 新表 `system_config`（迁移 `018_system_config.sql`），优先于默认值、重启后仍然保留；表里没有某一行就表示「使用默认值」，「恢复默认」删除覆盖行，而不是把默认值抄进表里。首批纳入 4 个开关：`discovery.enabled`（立即生效，热启停 UDP 广播与探询应答）、`deviceScan.enabled`（立即生效，只拦截新扫描，运行中的任务照常结束或由用户取消）、`push.changeOnly`（立即生效，见下一条）、`plugin.enabled`（重启后生效 —— 插件管理器的 `Stop` 是终态的，页面会标出「已保存，重启后生效」）。更新顺序固定为「校验 → 落库 → 发布内存快照 → 应用」；applier 失败时恢复旧快照并补偿写回旧值，接口返回「配置已回滚」，而不是假装成功；应用期间**不持有 SQLite 事务**，避免 `discovery.Stop()` 等待协程退出时占住单写连接、拖住采集与 outbox 落盘。新增 `GET /systemConfig/list`、`POST /systemConfig/update`、`POST /systemConfig/reset`，按既有约定整组镜像到免鉴权的 `/agentApi`（update / reset 属写操作，dsh 调用前应取得用户确认）；新增错误码 `80002`–`80006` 与中英文案。日志、端口、JWT 等启动期配置有意不在动态管理范围内。`runtimecfg/registry_test.go` 会反射遍历 `configFile.Config` 的布尔叶子：新增布尔配置若既未登记、也未在排除表里写明理由，测试即失败，迫使「要不要让它可动态修改」被有意识地决定一次。契约见 [runtime-config.md](backend/specs/runtime-config.md)。
  **Runtime configuration switches**: `default.yaml` remains the single source of defaults; the new "System Settings → Runtime Switches" page layers overrides on top of it. Changes are written to the new `system_config` SQLite table (migration `018_system_config.sql`), take precedence over the defaults and survive restarts; no row for a key means "use the default", and "Restore default" deletes the override row rather than copying the default into the table. Four switches ship in the first batch: `discovery.enabled` (immediate — starts/stops the UDP broadcast and probe responder), `deviceScan.enabled` (immediate — blocks new scans only; a running job still finishes or is cancelled by the user), `push.changeOnly` (immediate — see the next entry) and `plugin.enabled` (restart required — the plugin manager's `Stop` is terminal, and the page marks it "saved, applies after restart"). The update order is fixed: validate → persist → publish the in-memory snapshot → apply. If the applier fails, the manager restores the previous snapshot, compensates the row back to its old value and returns "rolled back" rather than a false success. **No SQLite transaction is held while applying**, so `discovery.Stop()` waiting for goroutines to exit cannot occupy the single write connection and stall collection and outbox flushes. Adds `GET /systemConfig/list`, `POST /systemConfig/update` and `POST /systemConfig/reset`, mirrored as a group into the unauthenticated `/agentApi` surface like the rest of the admin routes (update / reset are writes — dsh must get user confirmation first), plus error codes `80002`–`80006` and matching Chinese/English messages. Startup-time settings such as logging, the server port and JWT are deliberately excluded. `runtimecfg/registry_test.go` reflects over every boolean leaf of `configFile.Config`: a new boolean setting that is neither registered nor explicitly excluded with a reason fails the test, forcing the "should this be runtime-switchable?" decision to be made on purpose. Contract: [runtime-config.md](backend/specs/runtime-config.md).
- **变化才推送（可开关）**：新增配置项 `push.changeOnly`（出厂 `true`）。点位的**值与质量码（`quality`）**自上次真正投递后都没变化时，本轮不再投递给任何推送通道（MQTT / TDengine / InfluxDB），以降低带宽占用与时序库写入量。网关启动、新增或改配推送通道后的首轮总是全量投递，保证稳定不变的点位在新通道上也有初值；质量码变化（如 192 → 2）本身也算变化。只作用于本地点位采集，MQTT 订阅映射产生的记录不参与去重。依赖周期心跳 / 等间隔采样的场景将该项置为 `false`；该项可在「运行开关」页动态切换，从下一批采集起生效。README（中 / 英）与推送载荷契约（[push-payload.md](backend/specs/push-payload.md) / [.en](backend/specs/push-payload.en.md)）同步补充说明。
  **Change-only push (switchable)**: new option `push.changeOnly` (ships as `true`). When a point's **value and quality code** have not changed since its last actual delivery, it is no longer sent to any push channel (MQTT / TDengine / InfluxDB) in that round, reducing bandwidth and time-series writes. The first round after gateway startup, or after a push channel is added or reconfigured, is always a full snapshot so stable points get an initial value on a new channel; a quality transition (e.g. 192 → 2) counts as a change. Applies only to locally collected points — records produced by MQTT subscription mappings are never deduplicated. Scenarios that rely on periodic heartbeats or evenly spaced samples can set it to `false`; it is switchable on the Runtime Switches page and takes effect from the next collection batch. The README (Chinese and English) and the push payload contract ([push-payload.en.md](backend/specs/push-payload.en.md)) document the behaviour.

## [0.1.1] - 2026-10-20

### Added

- **MQTT 订阅入时序库**：新增「订阅管理 → MQTT 订阅」。每条订阅 = 一个独立的 MQTT 客户端（Broker / 端口 / Client ID / 用户名密码 / Topic 通配符 `+`、`#` / QoS 0–2）+ 一套「JSON 报文 → 采集记录」映射规则；转换后的记录经推送引擎**只投递到时序数据库类通道**（`tdengine-v3` / `influxdb-v3`），**刻意不投 MQTT 等普通通道** —— 订阅主题与发布主题相同时会形成自激。映射规则支持 `root`（整篇 / 对象 / 顶层数组自动逐条拆条）、`path` / `const` / `template` / `map` 四种取值方式、隐式 AND 过滤条件（`eq`/`neq`/`gt`/`gte`/`lt`/`lte`/`contains`/`in`/`notin`/`exists`/`notexists`）、`collectedAt`（epoch 秒/毫秒/微秒/纳秒与常见日期串，归一为毫秒精度本地时间；未配置时回落到批次落地时间），以及 `kind` / `quality` 的值映射（`quality` 必须配置整数默认值，避免把语义相反的外部质量码原样带入）。目标字段是采集记录白名单，未知字段保存即拒；数字以 `json.Number` 解码，大整数不丢精度。保存前可「测试连接」并用样例报文「预览」转换结果（均不落库）；订阅配置每 10 秒校验和热加载，增删改启停无需重启网关，运行状态展示连接状态、报文数 / 记录数 / 错误数与最近错误。转换成功的记录在投递时序库的同时，将设备 / 点位元信息按自然键 `(device_id, device_address_id)` 异步收录进新表 `mqtt_subscription_device_point`（不存采样值），对应控制台「订阅设备与点位」页。新增迁移 `016_mqtt_subscription.sql`、`017_mqtt_subscription_device_point.sql`；README（中 / 英）同步补充功能与配置说明。
  **MQTT subscription ingestion into time-series databases**: adds "Subscription Management → MQTT Subscriptions". Each subscription is an independent MQTT client (broker / port / client ID / username & password / topic with `+` and `#` wildcards / QoS 0–2) plus a "JSON message → collection record" mapping rule. Converted records are delivered through the push engine **only to time-series channels** (`tdengine-v3` / `influxdb-v3`) and **deliberately never to plain channels such as MQTT** — publishing back to MQTT would self-excite when the subscribed topic is also a publish topic. Rules support `root` (whole message / object / automatic per-element splitting for top-level arrays), four value sources — `path` / `const` / `template` / `map` — implicit-AND filters (`eq`/`neq`/`gt`/`gte`/`lt`/`lte`/`contains`/`in`/`notin`/`exists`/`notexists`), `collectedAt` (epoch seconds/milliseconds/microseconds/nanoseconds and common date formats, normalized to local time at millisecond precision, falling back to batch arrival time when absent), and `kind` / `quality` value maps (`quality` requires an integer default so an external code with opposite semantics is never passed through). Target fields are a whitelist of collection-record fields and unknown names are rejected at save time; numbers are decoded as `json.Number`, so large integers keep full precision. Before saving you can test connectivity and preview conversion results against a sample message (neither is persisted); subscription configs are checksum-compared and hot-reloaded every 10 seconds, so create/update/delete/enable/disable needs no restart, and runtime status shows connection state, message/record/error counts and the last error. While delivering to the time-series database, successfully converted records also asynchronously index device/point metadata by the natural key `(device_id, device_address_id)` into the new `mqtt_subscription_device_point` table (no sampled values), surfaced by the console's "Subscription Devices & Points" page. New migrations `016_mqtt_subscription.sql` and `017_mqtt_subscription_device_point.sql`; the README (Chinese and English) documents the feature and configuration.
