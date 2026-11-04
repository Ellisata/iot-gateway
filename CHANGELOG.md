# 更新日志

本文件记录 iot-gateway 网关的所有重要变更，遵循[语义化版本](https://semver.org/)。每个版本先列中文条目，再列英文条目。

All notable changes to the iot-gateway gateway are documented here, following
[Semantic Versioning](https://semver.org/). Chinese entries come first under
each release, followed by English entries.

## [未发布]

### 变更

- **`group-json` 载荷重构（破坏性）**：`values`/`errors` 字典合并为 `points` 数组，坏点保留并携带 `quality`（`< 128` 判坏）；帧头新增 `deviceCode`/`protocol`/`thingModelCode`/`groupCode`，`timestamp` 更名为 `collectedAt`、`intervalMs` 更名为 `groupIntervalMs`。字段契约见 `specs/push-payload.md` §1.1；映射编辑器同步补充批次字段 `intervalMs`。

### Changed

- **`group-json` payload redesign (breaking)**: the `values`/`errors` dictionaries are replaced by a single `points` array that keeps bad-quality points (`quality < 128`); the frame header now carries `deviceCode` / `protocol` / `thingModelCode` / `groupCode`, `timestamp` is renamed to `collectedAt`, and `intervalMs` to `groupIntervalMs`. The field contract lives in `specs/push-payload.md` §1.1; the mapping editor also exposes the `intervalMs` batch field.

## [0.7.0] - 2026-11-04

### 新增

- **OPC UA 通道监控**：服务端通道记录最近写入地址空间的批次，`publishCount` / `rowsCount` 改为更新批次 / 更新点位，`extra` 展示点位覆盖度、订阅与监控项数及重建次数；`Connected` 表示监听已就绪。
- **事件触发采集规则**：新增规则管理与触发历史（迁移 `033_trigger_rule.sql`）。条件为 DNF（组间 OR、组内 AND），可配冷却与读取超时；采集样本出现上升沿后异步读取动作设备指定点位并定向推送到一个通道，结果按 `success` / `offline` / `timeout` / `read_failed` / `push_failed` / `dropped` 落库，保留规则、条件与结果快照（7 天或 2 万行）。控制台新增「事件触发规则」「触发历史」页，接口整组镜像到 `/agentApi`，新增错误码 `40051`–`40062`。
- **触发规则读取超时**：设备锁等待改为 context 感知。设备正被慢采集或重连占用时，规则读取在超时（默认 5s，最大 60s）后放弃等待并记录新状态 `timeout`；已开始但超时后才返回的读取不再推送迟到结果。控制下发同样按调用方 ctx 放弃等锁。
- **触发执行按设备隔离**：规则引擎把读点/推送交给动作设备各自的串行执行者，全局分发协程不再直接执行 I/O。单台慢设备只会占住自己的执行队列，其他设备的规则照常执行；同一设备仍严格串行，单设备队列满时记 `dropped` 并释放规则的单飞标记。
- **触发规则编码**：`trigger_rule` 新增全局唯一的业务编码 `code`（迁移 `034_trigger_rule_code.sql`），创建必填、创建后不可修改，格式与设备/采集组编码一致；存量规则按 ID 回填。事件载荷的 `trigger` 对象新增 `ruleCode`，触发历史新增 `rule_code` 快照；列表支持按编码检索。
- **事件触发载荷元数据**：事件触发规则定向推送的动作记录新增可选 `trigger` 对象（`source` / `ruleId` / `ruleCode` / `ruleName` / `triggerId` / `firedAt`），`firedAt` 为条件上升沿时刻。仅事件推送携带，正常采集与外部订阅载荷逐字节不变；断网缓存补发保留该元数据。native-json / group-json 直接输出，mapping-json / script 可按需取用。
- **触发规则实时监控**：规则引擎在内存保留最近 100 次已结束的触发（成功/离线/超时/读失败/推送失败/丢弃），新增只读 `GET /triggerRule/recentTriggers`（可按规则过滤，镜像到 `/agentApi`）；控制台「事件触发规则」页新增实时监控抽屉，展示触发时间、规则、状态、目标通道、动作设备、耗时与错误，展开可看本次实际读到的点位值，打开期间每 3 秒刷新。仅内存、重启即清空；持久化历史与条件快照仍走触发历史页。

### Added

- **OPC UA channel monitoring**: the server channel keeps its recent
  address-space batches; `publishCount` / `rowsCount` now mean update batches /
  updated points, `extra` exposes point coverage, subscription and
  monitored-item counts plus rebuild count, and `Connected` means the listener
  is up.
- **Event-triggered collection rules**: adds rule management and trigger history
  (migration `033_trigger_rule.sql`). Conditions are DNF (OR across groups, AND
  within a group) with optional cooldown and read timeout; on a rising edge in
  collected samples the engine asynchronously reads the action device's points
  and pushes them to one channel, recording `success` / `offline` / `timeout` /
  `read_failed` / `push_failed` / `dropped` with rule, condition and result
  snapshots (7 days or 20,000 rows). The console gains Event Trigger Rules and
  Trigger History pages; routes are mirrored to `/agentApi` and error codes
  `40051`–`40062` are added.
- **Trigger-rule read timeout**: device-lock acquisition is now
  context-aware. When a slow poll or reconnect owns the device, a rule read
  gives up waiting at its timeout (default 5s, max 60s) and records the new
  `timeout` status; a read that was already in flight and finishes late is no
  longer pushed. Control writes also give up waiting on the caller's ctx.
- **Per-device trigger execution**: the rule engine now hands reads and pushes
  to a serial executor per action device; global dispatch goroutines no longer
  perform I/O. A slow device only occupies its own queue, while rules for other
  devices keep running. The same device remains strictly serial; a full
  per-device queue records `dropped` and clears the rule's single-flight flag.
- **Trigger rule code**: `trigger_rule` gains a globally unique business code
  `code` (migration `034_trigger_rule_code.sql`), required on creation,
  immutable afterwards, using the same format as device and collector-group
  codes; existing rows are backfilled from their IDs. Event payload `trigger`
  objects gain `ruleCode`, trigger history stores a `rule_code` snapshot, and
  the rule list can be searched by code.
- **Trigger metadata in event payloads**: action records pushed by a trigger
  rule carry an optional `trigger` object (`source` / `ruleId` / `ruleCode` /
  `ruleName` / `triggerId` / `firedAt`, where `firedAt` is the rising-edge
  time). Only event pushes include it, so normal collection and
  external-subscription payloads stay byte-for-byte unchanged; offline spool
  replay preserves the metadata. native-json and group-json expose it directly,
  while mapping-json and script can opt in by referencing `trigger.*`.
- **Live trigger-rule monitoring**: the rule engine keeps the last 100 completed
  triggers in memory (success / offline / timeout / read failed / push failed / dropped)
  behind the read-only `GET /triggerRule/recentTriggers` (optional rule filter,
  mirrored to `/agentApi`). The Event Trigger Rules page gains a live monitor
  drawer showing fired time, rule, status, target channel, action device,
  duration and error; expanding a row shows the point values actually read,
  refreshing every 3 seconds while open. Memory-only — cleared on restart;
  persisted history and condition snapshots remain on the Trigger History page.

## [0.6.0] - 2026-11-03

### 新增

- **采集组与分组推送**：设备下可建采集组（迁移 `028_collector_group.sql`），组持有采集周期；点位通过 `device_address.group_id` 绑定到组，绑定后按组周期采集，未绑定点位继续按 `scan_frequency` 采集（升级零行为变化）。组停用后绑定点位停止采集与推送，仍被点位绑定的组禁止删除；删除设备级联删除其采集组。推送侧按 `(设备, 组)` 分帧，新增 `group-json` 载荷格式（`timestamp/device/group` + `values/errors` 字典）；通道可用 `config_json.groupIds` 只订阅指定组（空 = 全量，未分组记录在非空订阅下不推送）。断网缓存新增组列（迁移 `029_push_outbox_group.sql`），补发按原组重建主题。控制台新增「采集组」工作区页签、点位批量分配采集组与通道组订阅选择器。
- **无变化周期重发**：`push.changeOnly` 新增配套运行期开关 `push.keepaliveIntervalSec`（0 / 60 / 300 / 600 秒，出厂 0 = 关闭）。点位值持续不变时，按配置周期再投递一次，供下游做心跳/存活判断。判定在本轮采集记录上进行，设备离线或采集失败不会合成旧值；实际间隔不短于采集周期。仅在「变化才推送」开启时生效；打开后时序库会周期性写入相同值的重复行。运行开关页新增下拉选择，修改后下一批采集即生效。
- **物模型与属性 Excel 导入**：新增物模型、物模型属性的模板下载与批量导入接口（`GET /thingModel/importThingModelTemplate` 等），页面提供「下载模板 / 导入」按钮。模板中的分类、数据类型、读写模式列改为「中文名（code）」下拉，导入接受 code、中文名或组合写法（code 忽略大小写）。物模型按标识与名称去重，属性在模型内按标识符去重；逐行校验、部分导入并返回失败明细。设备地址导入模板支持传 `deviceId`，按该设备协议生成数据类型下拉。
- **设备与采集组编码**：设备与采集组新增面向主题与外部引用的稳定编码 `code`（迁移 `030_device_collector_group_code.sql`）：字母开头、仅字母数字下划线短横线、最长 64，创建必填、创建后不可修改、`legacy` 保留；Excel 导入与扫描建库按名称自动生成并避让，存量行按 ID 回填。编码随记录、载荷与断网缓存落盘（迁移 `031_push_outbox_topic_code.sql`），补发可重建原主题；控制台表单与检索均支持编码。
- **MQTT 主题改用编码模板**：占位符改为 `${deviceCode}` / `${thingModelCode}` / `${groupCode}`，缺失时分别回落 `_unencoded` / `_unmodeled` / `legacy`，组改名不影响订阅；通道编辑页主题字段改为模板下拉 + 手动输入（迁移 `032_mqtt_topic_template_select.sql`）。
- **推送报文实时监控**：每通道在内存保留最近 100 条推送报文，新增只读 `GET /push/recentMessages`（镜像到 `/agentApi`）；控制台通道列表新增监控抽屉，展示连接状态、积压、最近报文的主题/设备/组、2 KiB 载荷预览与错误，打开期间每 3 秒刷新；时序库通道新增累计写入行数 `rowsCount`。

### Added

- **Collector groups and grouped push**: devices can define collector groups
  (migration `028_collector_group.sql`) that own a polling interval. Points bind
  to a group through `device_address.group_id`; bound points are polled at the
  group interval, while unbound points keep the existing `scan_frequency`
  behaviour (zero change on upgrade). Disabling a group stops collection and
  push for bound points; a group with bound points cannot be deleted, and
  deleting a device cascades to its groups. Push batches are framed per
  `(device, group)` and a new `group-json` payload emits
  `timestamp/device/group` plus `values/errors` dictionaries. Message channels
  can subscribe to specific groups via `config_json.groupIds` (empty = receive
  everything; ungrouped records are excluded when the list is non-empty). The
  offline spool stores group columns (migration
  `029_push_outbox_group.sql`) so replay rebuilds the original topic. The
  console adds a Collector Groups workspace tab, bulk point assignment, and a
  per-channel group subscription selector.
- **Periodic republish of unchanged points**: adds the runtime switch
  `push.keepaliveIntervalSec` (0 / 60 / 300 / 600 seconds, default 0 = off)
  alongside `push.changeOnly`. When a point stays unchanged, it is delivered
  once more after the configured interval so downstream systems receive a
  heartbeat. The check runs against the current collection round, so an offline
  device or failed read never produces a synthesized old value, and the real
  interval is never shorter than the collection period. Effective only while
  change-only push is on; enabling it also writes periodic duplicate rows to
  time-series databases. The Runtime Switches page renders it as a dropdown and
  changes apply from the next collection batch.
- **Excel import for thing models and properties**: adds template download and
  bulk import endpoints for thing models and their properties
  (`GET /thingModel/importThingModelTemplate` etc.) plus "Download template" /
  "Import" actions in the console. Category, data-type and access-mode columns
  in the templates become "label (code)" dropdowns; imports accept a code, a
  display name, or the combined form (code is case-insensitive). Thing models
  are deduped by code and name, properties by identifier within a model; rows
  are validated individually, valid rows are imported and failures are
  reported. The device-address import template now accepts `deviceId` to scope
  data-type dropdowns to that device's protocol.
- **Device and collector-group codes**: devices and collector groups gain a
  stable `code` for topics and external references (migration
  `030_device_collector_group_code.sql`): starts with a letter, letters/digits/
  underscore/hyphen only, up to 64 chars, required on create, immutable
  afterwards, `legacy` reserved; Excel import and scan-created devices derive
  one from the name with collision avoidance, and existing rows are backfilled
  from the ID. The code is stored with records, payloads and the offline spool
  (migration `031_push_outbox_topic_code.sql`) so replay rebuilds the original
  topic; console forms and search support it.
- **MQTT topic templates use codes**: placeholders become `${deviceCode}` /
  `${thingModelCode}` / `${groupCode}`, falling back to `_unencoded` /
  `_unmodeled` / `legacy` respectively, so renaming a group no longer changes
  downstream subscriptions; the channel editor turns the topic field into a
  template picker plus free text (migration
  `032_mqtt_topic_template_select.sql`).
- **Live push-message monitoring**: each channel keeps its last 100 outbound
  messages in memory behind the read-only `GET /push/recentMessages` (mirrored
  to `/agentApi`); the channel list gains a monitor drawer showing connection
  state, backlog, each message's topic/device/group, a 2 KiB payload preview and
  errors, refreshing every 3 seconds while open. Time-series channels also
  report cumulative written rows as `rowsCount`.

## [0.5.0] - 2026-11-02

### 新增

- **控制下发（写 PLC 点位）**：新增驱动 Writer 与插件远程写路径，受 `control.enabled` 运行开关、协议 `Writable`、点位 `rw_permission` 含 W 三层约束，只复用在线链路、不触发重连。入口为 `POST /openApi/control/write`、`/agentApi/control/write` 与 MQTT 订阅的 `controlTopic`。写入审计落新表 `control_log`（迁移 `025_control_log.sql`），控制台新增审计页，支持设备级（默认 60 点/分钟）与点位级（默认 20 次/分钟）限流；新增插件配置 `plugin.writeTimeoutMs`。
- **物模型管理与设备/点位绑定**：新增物模型属性 CRUD（迁移 `027_thing_model.sql`），设备可绑定物模型、点位可绑定属性；采集记录携带 `thingModelCode` / `propertyCode`，在 InfluxDB v3、TDengine v3 与 JSON 载荷中作为标签输出并纳入变化检测。属性 `access_mode` 生效：只写属性不再采集，只读属性拒绝控制写入（`40039`）；实时点位改为最新在上排序。
- **大网段分批续扫**：`deviceScan.maxTargets` 改为单批上限，超出时先扫一批并在快照返回 `remainingTargets` / `scannedTotal`；新增 `POST /deviceScan/continue` 沿用同一任务、已发现设备与去重集合继续扫描，中断的批次会重扫。扫描页新增「继续扫描（剩余 N）」与总数显示。
- **MQTT 订阅最近报文**：每条订阅在内存保留最近 100 条原始报文，通过 `GET /mqttSubscription/recentMessages` 返回；订阅页新增轮询抽屉，展示主题、2 KiB 载荷预览与 QoS / retained / 超大标记，未解析或丢弃的报文同样可见。

### 变更

- SDK 升级到 v0.3.0（testkit v0.2.0），`drivers.version` 同步为 v0.3.0。
- 补充驱动插件文档（[docs/driver-plugins.md](docs/driver-plugins.md)），README 增加在线演示环境链接。

### 修复

- **编辑通道 / Webhook 时动态表单不回显**：规则加载完成后再渲染表单，并通过 `v-model` 预填保存值，避免加载阶段的空表单吞掉一次性回填。
- **载荷预览不可见**：原生 JSON 预览渲染后自动滚动到可视区。
- **大盘趋势 tooltip 区间**：单点桶与多点桶统一采集窗口格式，区间与柱体、断点对齐。

### Added

- **Control writes (PLC point write)**: adds the driver Writer and pluginhost remote
  write path, gated by the `control.enabled` runtime switch, protocol `Writable`
  and point `rw_permission` containing W; writes reuse the live link and never
  trigger a reconnect. Entry points are `POST /openApi/control/write`,
  `/agentApi/control/write` and MQTT subscription `controlTopic`. Writes are
  audited in the new `control_log` table (migration `025_control_log.sql`) with a
  console audit page and per-device (default 60 points/min) and per-point
  (default 20/min) rate limits; new plugin option `plugin.writeTimeoutMs`.
- **Thing model management and device/point binding**: thing model properties get
  CRUD (migration `027_thing_model.sql`); devices bind to a model and points to
  properties. Collected records carry `thingModelCode` / `propertyCode` as tags in
  InfluxDB v3, TDengine v3 and JSON payloads and include them in change detection.
  Property `access_mode` is enforced: write-only-bound points are no longer
  collected and writes to read-only properties return `40039`; the realtime point
  list now orders newest first.
- **Resumable large-subnet scans**: `deviceScan.maxTargets` now caps a single
  batch; oversize scans report `remainingTargets` / `scannedTotal` in the
  snapshot, and `POST /deviceScan/continue` resumes the same job, found devices
  and dedup set, re-scanning a batch cancelled mid-flight. The scan page adds a
  "Continue scan (N left)" action and an overall count.
- **Recent MQTT messages**: each subscription keeps its last 100 raw messages in
  memory behind `GET /mqttSubscription/recentMessages`; the subscription page
  gains a polling drawer showing topic, a 2 KiB payload preview and
  QoS/retained/oversized markers, including unparsed or dropped messages.

### Changed

- SDK bumped to v0.3.0 (testkit v0.2.0) and `drivers.version` to v0.3.0.
- Expanded the driver plugin documentation
  ([docs/driver-plugins.md](docs/driver-plugins.md)); READMEs link the live demo.

### Fixed

- **Dynamic forms did not echo values when editing channels / webhooks**: the form
  now renders after the rule loads and prefills saved values through `v-model`,
  so the loading-phase empty form no longer consumes the one-shot prefill.
- **Payload preview appeared to do nothing**: the native JSON preview scrolls
  into view once rendered.
- **Dashboard trend tooltip ranges**: single-point buckets use the same
  collection-window format as multi-point ones, aligned with bars and gap edges.

## [0.4.0] - 2026-10-29

### 新增

- **驱动动态表单与扫描元数据多语言**：驱动的 15 个协议配置表单、7 个局域网扫描表单、7 个点位扫描表单改为 `{{$t.key}}` + `options.language` 数据驱动多语言；扫描能力的标题/描述新增 `titleI18n` / `descriptionI18n` 透传（SDK v0.2.0）。前端在设备对象、设备扫描、点位扫描弹窗按当前语言渲染，运行期不读任何前端词典。迁移 `024_seed_protocol_form_i18n.sql` 用「协议名 + v0.1.0 表单原文」guard 自动升级存量 `iot_protocol.form_json`，运维改过的行保持原样。

### 变更

- `drivers.version` / `backend/go.mod` 的 SDK 依赖升级到 v0.2.0。发布前需先发布 SDK 与 drivers v0.2.0 tag，并在有网络的环境执行 `go mod tidy` 补全 `go.sum`。

### Added

- **Multilingual driver forms and scan metadata**: the drivers' 15 protocol config
  forms, 7 LAN discovery forms and 7 point-discovery forms now carry
  `options.language` (`{{$t.key}}` display text); scan specs pass optional
  `titleI18n` / `descriptionI18n` through (SDK v0.2.0). The device-object,
  device-scan and point-discovery dialogs render in the current locale with no
  runtime dictionary in the frontend. Migration `024_seed_protocol_form_i18n.sql`
  upgrades existing `iot_protocol.form_json` rows guarded by protocol name plus
  the exact v0.1.0 form text; operator-customized rows are left untouched.

### Changed

- Bumped `drivers.version` and the backend SDK dependency to v0.2.0. The SDK and
  drivers v0.2.0 tags must be published first; run `go mod tidy` with network
  access to refresh `go.sum`.

## [0.3.0] - 2026-10-27

### 新增

- **`/agentApi` 运行开关**：新增运行期开关 `agentApi.enabled`（出厂 `false`）。`/agentApi/*` 整组路由照常注册，分组中间件按请求现读开关，关闭时整组返回 403 —— 包括清单与 `/agentApi/systemConfig/update`，因此 dsh 无法自行打开这个免鉴权调用面；在「运行开关」页开启（需二次确认）后恢复可达。采集与上报完全不受影响。
- **版本兼容性说明**：新增 [docs/version-compatibility.md](docs/version-compatibility.md)，明确网关与插件之间唯一的兼容判据是 `driversdk.APIVersion`，并给出官方发行配对矩阵、插件兼容区间与 `APIVersion` 进位规则。
- **发行包 `MANIFEST.json`**：`release.sh` / `release.bat` 现在在包根生成机器可读的清单，包含网关版本、目标平台、SDK 版本、`APIVersion`、`FrameMagic`、`drivers.version` 与每个插件的 sha256；现场可直接据此核对是否是官方配对组合。
- **CI 工作流**：新增 GitHub Actions —— PR 上跑 `go test -short` 与插件编译，tag 推送时构建发行包并创建 GitHub / Gitee Release；`dry_run` 可空跑验证整条流水线而不发布。

### 变更

- **SDK 与官方驱动拆分为独立仓库**：驱动 SDK 迁至 [`iot-gateway-sdk`](https://github.com/Ellisata/iot-gateway-sdk)（Apache-2.0），官方驱动迁至 [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers)（AGPL-3.0），新增 `LICENSING.md` 说明三仓授权模型与进程边界。网关改为以 Go module 依赖 SDK，移除 `backend/sdk` / `backend/driversrc` 子模块；发行构建按 `drivers.version` 从模块代理拉取驱动源码编译插件，`drivers.txt` 列出随包插件。`APIVersion` 仍为 4，现有插件二进制继续兼容。
- **发行脚本**：新增 `release.bat` 与 `release.sh` 对齐；`.bat` 脚本统一为纯 ASCII + CRLF 并校验末行，插件编译数量与 `drivers.txt` 不一致时构建直接失败。

### 修复

- **网关身份版本与发行 tag 不一致**：`default.yaml` 里写死的 `gateway.version`（此前为 `1.0.2`）与 `v0.2.0` 发行 tag 对不上，导致 `iot_gateway.firmware_version` 与局域网发现广播的 `firmwareVersion` 都不是实际运行的版本。现在构建脚本统一以 `-X main.version=<tag>` 注入，解析优先级为 `IOT_GATEWAY_VERSION > 构建注入版本（非 dev）> gateway.version > dev`；`gateway.version` 只在未注入版本的本地构建里生效。
- **日志文件名校验**：Linux 上 `filepath.Base` 不把 `\` 当分隔符，`..\default.yaml` 这类路径能绕过检查，并被报成「文件不存在」而不是「非法」。现在显式拒绝两种分隔符，与插件上传的文件名校验一致。
- **报警去抖窗口起点**：`lastTouch` 此前只在节流函数内部赋值，报警边沿刚写过 `last_occur_time` 之后，紧随其后的第一次失败总是绕过节流再写一次库。现在在报警边沿置位 `lastTouch`，5 秒窗口从边沿开始计算。

### Added

- **`/agentApi` runtime switch**: new runtime switch `agentApi.enabled` (ships
  `false`). The `/agentApi/*` routes stay registered and a group middleware reads
  the switch per request, returning 403 group-wide while it is off — including
  the catalog and `/agentApi/systemConfig/update`, so dsh cannot reopen the
  unauthenticated surface by itself; enabling it in the Runtime Switches page
  (behind a confirmation dialog) restores access. Collection and reporting are
  unaffected.
- **Version compatibility doc**: adds
  [docs/version-compatibility.md](docs/version-compatibility.md) documenting the
  single compatibility gate (`driversdk.APIVersion`), the official release
  pairing matrix, the plugin compatibility range and the `APIVersion` bump rules.
- **Release `MANIFEST.json`**: `release.sh` / `release.bat` now write a
  machine-readable manifest at the package root with the gateway version, target
  platform, SDK version, `APIVersion`, `FrameMagic`, `drivers.version` and each
  plugin's sha256, so field checks no longer rely on file timestamps.
- **CI workflow**: new GitHub Actions — `go test -short` plus a plugin compile
  job on PRs, and release bundles published to GitHub / Gitee Releases on tag
  pushes; a `dry_run` mode exercises the whole pipeline without publishing.

### Changed

- **SDK and official drivers split into separate repositories**: the driver SDK
  moves to [`iot-gateway-sdk`](https://github.com/Ellisata/iot-gateway-sdk)
  (Apache-2.0) and the official drivers to
  [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers)
  (AGPL-3.0), with a new `LICENSING.md` documenting the three-repository
  licensing model and process boundary. The gateway now depends on the SDK as a
  Go module and drops the `backend/sdk` / `backend/driversrc` submodules; release
  builds fetch driver sources pinned by `drivers.version` from the module proxy
  and compile the plugins listed in `drivers.txt`. `APIVersion` remains 4, so
  existing plugin binaries stay compatible.
- **Release scripts**: new `release.bat` mirrors `release.sh`; `.bat` scripts are
  pure ASCII with CRLF and final-line checks, and the build fails when the
  compiled plugin count disagrees with `drivers.txt`.

### Fixed

- **Gateway identity version no longer drifts from the release tag**: the
  hardcoded `gateway.version` in `default.yaml` (`1.0.2`) disagreed with the
  `v0.2.0` tag, so `iot_gateway.firmware_version` and the discovery broadcast
  carried the wrong version. Build scripts now inject `-X main.version=<tag>`;
  resolution order is `IOT_GATEWAY_VERSION > injected build version (non-dev) >
  gateway.version > dev`, and `gateway.version` only applies to local builds
  without injection.
- **Log file name validation**: on Linux `filepath.Base` does not treat `\` as a
  separator, so a path such as `..\default.yaml` slipped past the check and was
  reported as "not found" instead of "illegal". Both separators are now rejected
  explicitly, matching the plugin upload check.
- **Alarm debounce window start**: `lastTouch` was only set inside the throttle
  helper, so the first failure after the alarm edge always bypassed the throttle
  and rewrote `last_occur_time` right after the edge had written it. `lastTouch`
  is now set at the alarm edge, so the 5 s window starts there.

## [0.2.0] - 2026-10-24

### 新增

- **OPC UA Server 推送通道**：新增 `opcua` 类型，网关作为 OPC UA 服务端按设备/点位生成地址空间（`deviceIds` 限定范围、`nodeIdStyle` 选择 ID 风格），支持 Read / 订阅与变化通知、`None` / `Basic256Sha256` 安全策略，端点自动公布本机地址。迁移 `022_add_opcua_push_channel_form.sql`。
- **HTTP/Webhook 推送通道**：新增 `http` 类型，支持 POST/PUT/PATCH、自定义请求头、Bearer / Basic / HMAC-SHA256 鉴权、超时与并发配置；2xx 成功，408/429/5xx 与网络错误重试，其余 4xx/3xx 丢弃；支持断网缓存补发。迁移 `021_add_http_push_channel_form.sql`。
- **出站映射（mapping-json）**：`mqtt` / `http` 通道可在 `config_json` 选择 `format` 与 `mapping`，把采集记录映射为自定义 JSON；复用 `path` / `const` / `template` / `map` 与 `transform`，新增 `type` / `emit` / `envelope` / `batchFields`。native-json 载荷保持逐字节不变。
- **通道多实例**：`push_channel` 新增 `type` 列，同类型可建多条通道；旧配置与接口保持兼容。迁移 `020_rebuild_push_channel_with_type.sql`。
- **载荷预览与测试发送**：新增 `listTypes` / `preview` / `testSend` 接口，控制台支持格式选择、映射编辑、预览与测试发送；`testConnectivity` 返回具体失败原因。
- **出站映射可视化编辑器**：通道弹窗内嵌字段表格，覆盖输出形态、取值方式、类型、默认值、变换与批次字段；保留高级 JSON 双向切换，新增只读接口 `POST /pushChannel/previewMapping`。
- **脚本编码（script）**：消息类通道新增 `format: "script"`，用 goja 执行 `encode(batch, ctx)` 生成任意文本载荷；保存时预编译校验，每次编码使用独立 runtime。
- **映射原语下沉**：新增 stdlib-only `mappingdsl` 包，入站解析与出站映射共用路径、模板、变换与标量转换；`parser/mapping` 行为不变。
- 文档：[推送通道与出站映射](backend/docs/push-channels.md)；[推送载荷契约](backend/specs/push-payload.md) 增加格式选择说明。

### 变更

- **SQLite WAL 定期检查点**：自动 checkpoint 阈值显式设为 512 页（约 2MB）并经 DSN 下发；新增每 10 分钟一次的维护协程，先 `PASSIVE` 回写已合并帧，无读者时再 `TRUNCATE` 把 `-wal` 收缩到 0。外部进程钉住 WAL 时打 WARN 并给出已合并/总帧数，读者退出后自动恢复截断。
- **SQLite 写入吞吐**：WAL 连接改为 `synchronous=NORMAL`，断网缓存写入把排队批次合并为一次多行 INSERT，实测约 1.86ms/批降至约 0.09ms/批。代价是掉电最多丢失上次 checkpoint 之后的提交（约 2MB），库不会损坏；合并写失败时退回逐条写。

### Added

- **OPC UA Server push channel**: new `opcua` type exposing collected data as an
  OPC UA server with a device/point address space (`deviceIds` scope,
  `nodeIdStyle`), Read/subscribe with change notifications, `None` /
  `Basic256Sha256` security and automatic endpoint advertisement. Migration
  `022_add_opcua_push_channel_form.sql`.
- **HTTP/Webhook push channel**: new `http` type with POST/PUT/PATCH, custom
  headers, Bearer / Basic / HMAC-SHA256 auth, timeout/worker settings and offline
  spool replay; 2xx succeeds, 408/429/5xx and network errors are retried, other
  4xx/3xx are dropped. Migration `021_add_http_push_channel_form.sql`.
- **Outbound mapping (`mapping-json`)**: `mqtt` / `http` channels select `format`
  and `mapping` in `config_json` to map records into a custom JSON payload,
  reusing `path` / `const` / `template` / `map` and `transform` with new `type` /
  `emit` / `envelope` / `batchFields`. Native-json output stays byte-for-byte
  unchanged.
- **Multiple channel instances**: `push_channel` gains a `type` column so one
  type can be configured multiple times; legacy configs and clients keep
  working. Migration `020_rebuild_push_channel_with_type.sql`.
- **Payload preview and test send**: new `listTypes` / `preview` / `testSend`
  endpoints; the console supports format selection, mapping editing, preview and
  test send, and `testConnectivity` reports the concrete failure reason.
- **Visual outbound mapping editor**: a field table in the channel dialog covers
  output shape, sources, types, defaults, transforms and batch fields, with a
  two-way advanced JSON mode and a read-only `POST /pushChannel/previewMapping`.
- **Script encoding (`script`)**: message-based channels gain `format: "script"`
  running a goja `encode(batch, ctx)` to produce arbitrary text payloads; scripts
  are compiled on save and every encode gets a fresh runtime.
- **Shared mapping primitives**: new stdlib-only `mappingdsl` package shared by
  inbound parsing and outbound mapping (paths, templates, transforms, scalar
  coercion); `parser/mapping` behavior is unchanged.
- Docs: [Push channels & outbound mapping](backend/docs/push-channels.md);
  [push payload contract](backend/specs/push-payload.md) documents format
  selection.

### Changed

- **Periodic SQLite WAL checkpoints**: the autocheckpoint threshold is now
  explicit (512 pages ≈ 2MB) and applied through the DSN; a 10-minute
  maintenance goroutine `PASSIVE`-checkpoints merged frames and `TRUNCATE`s
  `-wal` back to zero once no readers remain. An external process pinning the WAL
  logs a WARN with merged/total frames, and truncation resumes after it exits.
- **Faster SQLite writes**: the WAL connection uses `synchronous=NORMAL`, and the
  offline-spool writer coalesces queued batches into one multi-row INSERT,
  cutting ≈1.86ms per batch to ≈0.09ms. Power loss can lose at most the commits
  since the last checkpoint (≈2MB) without corrupting the database; failed merged
  writes fall back to one row at a time.

## [0.1.4] - 2026-10-23

### 新增

- **可插拔 MQTT 订阅解析器**：订阅可选用内置 DSL 或 goja JS 脚本解析，支持逗号分隔多主题、`topicPattern` / `fanout` / `transform` 算子与跳过 / 丢弃计数；仅改解析配置时原地热更新，不重连 MQTT。迁移 `019_mqtt_subscription_parser.sql`，控制台提供解析器编辑器。契约见 [parser-plugins.md](backend/specs/parser-plugins.md)。
- **订阅设备与点位手工维护**：新增来源列（自动 / 手工）与新增、编辑、Excel 导入 / 模板下载；手工行不会被自动收录覆盖，支持批量删除（≤200）并恢复为自动。
- **日志文件下载**：`GET /log/downloadFile` 以附件流式下载原始日志（支持 Range），镜像到 `/agentApi`；日志列表与查看器均可下载。

### 变更

- **大盘趋势修正**：采样时间戳对齐整分钟，查询按墙钟裁剪窗口（长时间休眠不再把 6 小时窗口拉成更长的历史），累计时长取自环形缓冲的最旧样本。

### Added

- **Pluggable MQTT subscription parsers**: a subscription can parse with the
  built-in DSL or a goja JS script; adds comma-separated topics, `topicPattern` /
  `fanout` / `transform` operators and skipped / dropped counters. Parser-only
  edits hot-reload in place without reconnecting MQTT. Migration
  `019_mqtt_subscription_parser.sql`; the console gains a parser editor.
  Contract: [parser-plugins.md](backend/specs/parser-plugins.md).
- **Manual maintenance of subscription device points**: adds a source column
  (auto / manual), create/edit, Excel import and template download; manual rows
  survive automatic upserts, and can be deleted in batches (≤200) and restored
  to auto.
- **Log file downloads**: `GET /log/downloadFile` streams the raw log as an
  attachment (Range-capable) and is mirrored in `/agentApi`; available from both
  the log list and the viewer.

### Changed

- **Dashboard trend fixes**: sample timestamps snap to the minute grid, windows
  are trimmed by wall clock (a long suspend can no longer stretch a 6-hour window
  into more history), and accumulated duration is reported from the oldest
  ring-buffer sample.

## [0.1.3] - 2026-10-22

### 新增

- **运行大盘**：新增每分钟一次的内存采样器，记录在线率、采集吞吐、坏点及推送队列/积压，保留最近 24 小时，提供 `GET /dashboard/trend?hours=1..24`（只读，镜像到 `/agentApi`）。趋势不落库，重启后从空开始积累。大盘首页重做：在线率环形、KPI 指标条、采集趋势图、最近事件与数据出口面板；趋势图按分钟网格合并成约 96 个桶，并用最小/最大值包络保留短暂掉线。

### 变更

- **管理端界面重构**：统一各管理页的页头、表格、空状态与弹窗；侧栏改为深色导轨，支持拖动调宽并记住宽度；设备列表与扫描页改版；图标迁移到 Tabler；补充 favicon 并重录 README 演示。

### Added

- **Live dashboard**: an in-memory sampler records online rate, throughput, bad
  points and push queue/spool depth once a minute, keeping 24 hours, exposed as
  `GET /dashboard/trend?hours=1..24` (read-only, mirrored to `/agentApi`). Trends
  are not persisted and start empty after a restart. The dashboard is rebuilt
  with an online-rate ring, a KPI strip, a telemetry trend, recent events and a
  data-outlet panel; the chart buckets samples into ~96 minute-aligned bars and
  keeps brief dropouts visible with a min/max envelope.

### Changed

- **Unified admin UI**: page headers, tables, empty states and dialogs are
  standardized; the sidebar becomes a dark rail that can be resized and remembers
  its width; the device list and scan pages are redesigned; icons move to Tabler;
  favicon assets are added and the README demos re-recorded.

## [0.1.2] - 2026-10-21

### 新增

- **运行期配置开关**：`default.yaml` 仍是默认值的唯一真源，新增「系统设置 → 运行开关」页面可在此之上叠加覆盖值：修改写入 SQLite 新表 `system_config`（迁移 `018_system_config.sql`），优先于默认值、重启后仍然保留；表里没有某一行就表示「使用默认值」，「恢复默认」删除覆盖行，而不是把默认值抄进表里。首批纳入 4 个开关：`discovery.enabled`（立即生效，热启停 UDP 广播与探询应答）、`deviceScan.enabled`（立即生效，只拦截新扫描，运行中的任务照常结束或由用户取消）、`push.changeOnly`（立即生效，见下一条）、`plugin.enabled`（重启后生效 —— 插件管理器的 `Stop` 是终态的，页面会标出「已保存，重启后生效」）。更新顺序固定为「校验 → 落库 → 发布内存快照 → 应用」；applier 失败时恢复旧快照并补偿写回旧值，接口返回「配置已回滚」，而不是假装成功；应用期间**不持有 SQLite 事务**，避免 `discovery.Stop()` 等待协程退出时占住单写连接、拖住采集与 outbox 落盘。新增 `GET /systemConfig/list`、`POST /systemConfig/update`、`POST /systemConfig/reset`，按既有约定整组镜像到免鉴权的 `/agentApi`（update / reset 属写操作，dsh 调用前应取得用户确认）；新增错误码 `80002`–`80006` 与中英文案。日志、端口、JWT 等启动期配置有意不在动态管理范围内。`runtimecfg/registry_test.go` 会反射遍历 `configFile.Config` 的布尔叶子：新增布尔配置若既未登记、也未在排除表里写明理由，测试即失败，迫使「要不要让它可动态修改」被有意识地决定一次。契约见 [runtime-config.md](backend/specs/runtime-config.md)。
- **变化才推送（可开关）**：新增配置项 `push.changeOnly`（出厂 `true`）。点位的**值与质量码（`quality`）**自上次真正投递后都没变化时，本轮不再投递给任何推送通道（MQTT / TDengine / InfluxDB），以降低带宽占用与时序库写入量。网关启动、新增或改配推送通道后的首轮总是全量投递，保证稳定不变的点位在新通道上也有初值；质量码变化（如 192 → 2）本身也算变化。只作用于本地点位采集，MQTT 订阅映射产生的记录不参与去重。依赖周期心跳 / 等间隔采样的场景将该项置为 `false`；该项可在「运行开关」页动态切换，从下一批采集起生效。README（中 / 英）与推送载荷契约（[push-payload.md](backend/specs/push-payload.md) / [.en](backend/specs/push-payload.en.md)）同步补充说明。

### Added

- **Runtime configuration switches**: `default.yaml` remains the single source of
  defaults; the new "System Settings → Runtime Switches" page layers overrides on
  top of it. Changes are written to the new `system_config` SQLite table
  (migration `018_system_config.sql`), take precedence over the defaults and
  survive restarts; no row for a key means "use the default", and "Restore
  default" deletes the override row rather than copying the default into the
  table. Four switches ship in the first batch: `discovery.enabled` (immediate —
  starts/stops the UDP broadcast and probe responder), `deviceScan.enabled`
  (immediate — blocks new scans only; a running job still finishes or is
  cancelled by the user), `push.changeOnly` (immediate — see the next entry) and
  `plugin.enabled` (restart required — the plugin manager's `Stop` is terminal,
  and the page marks it "saved, applies after restart"). The update order is
  fixed: validate → persist → publish the in-memory snapshot → apply. If the
  applier fails, the manager restores the previous snapshot, compensates the row
  back to its old value and returns "rolled back" rather than a false success.
  **No SQLite transaction is held while applying**, so `discovery.Stop()` waiting
  for goroutines to exit cannot occupy the single write connection and stall
  collection and outbox flushes. Adds `GET /systemConfig/list`,
  `POST /systemConfig/update` and `POST /systemConfig/reset`, mirrored as a group
  into the unauthenticated `/agentApi` surface like the rest of the admin routes
  (update / reset are writes — dsh must get user confirmation first), plus error
  codes `80002`–`80006` and matching Chinese/English messages. Startup-time
  settings such as logging, the server port and JWT are deliberately excluded.
  `runtimecfg/registry_test.go` reflects over every boolean leaf of
  `configFile.Config`: a new boolean setting that is neither registered nor
  explicitly excluded with a reason fails the test, forcing the "should this be
  runtime-switchable?" decision to be made on purpose. Contract:
  [runtime-config.md](backend/specs/runtime-config.md).
- **Change-only push (switchable)**: new option `push.changeOnly` (ships as
  `true`). When a point's **value and quality code** have not changed since its
  last actual delivery, it is no longer sent to any push channel (MQTT /
  TDengine / InfluxDB) in that round, reducing bandwidth and time-series writes.
  The first round after gateway startup, or after a push channel is added or
  reconfigured, is always a full snapshot so stable points get an initial value
  on a new channel; a quality transition (e.g. 192 → 2) counts as a change.
  Applies only to locally collected points — records produced by MQTT
  subscription mappings are never deduplicated. Scenarios that rely on periodic
  heartbeats or evenly spaced samples can set it to `false`; it is switchable on
  the Runtime Switches page and takes effect from the next collection batch. The
  README (Chinese and English) and the push payload contract
  ([push-payload.en.md](backend/specs/push-payload.en.md)) document the
  behaviour.

## [0.1.1] - 2026-10-20

### 新增

- **MQTT 订阅入时序库**：新增「订阅管理 → MQTT 订阅」。每条订阅 = 一个独立的 MQTT 客户端（Broker / 端口 / Client ID / 用户名密码 / Topic 通配符 `+`、`#` / QoS 0–2）+ 一套「JSON 报文 → 采集记录」映射规则；转换后的记录经推送引擎**只投递到时序数据库类通道**（`tdengine-v3` / `influxdb-v3`），**刻意不投 MQTT 等普通通道** —— 订阅主题与发布主题相同时会形成自激。映射规则支持 `root`（整篇 / 对象 / 顶层数组自动逐条拆条）、`path` / `const` / `template` / `map` 四种取值方式、隐式 AND 过滤条件（`eq`/`neq`/`gt`/`gte`/`lt`/`lte`/`contains`/`in`/`notin`/`exists`/`notexists`）、`collectedAt`（epoch 秒/毫秒/微秒/纳秒与常见日期串，归一为毫秒精度本地时间；未配置时回落到批次落地时间），以及 `kind` / `quality` 的值映射（`quality` 必须配置整数默认值，避免把语义相反的外部质量码原样带入）。目标字段是采集记录白名单，未知字段保存即拒；数字以 `json.Number` 解码，大整数不丢精度。保存前可「测试连接」并用样例报文「预览」转换结果（均不落库）；订阅配置每 10 秒校验和热加载，增删改启停无需重启网关，运行状态展示连接状态、报文数 / 记录数 / 错误数与最近错误。转换成功的记录在投递时序库的同时，将设备 / 点位元信息按自然键 `(device_id, device_address_id)` 异步收录进新表 `mqtt_subscription_device_point`（不存采样值），对应控制台「订阅设备与点位」页。新增迁移 `016_mqtt_subscription.sql`、`017_mqtt_subscription_device_point.sql`；README（中 / 英）同步补充功能与配置说明。

### Added

- **MQTT subscription ingestion into time-series databases**: adds "Subscription
  Management → MQTT Subscriptions". Each subscription is an independent MQTT
  client (broker / port / client ID / username & password / topic with `+` and
  `#` wildcards / QoS 0–2) plus a "JSON message → collection record" mapping
  rule. Converted records are delivered through the push engine **only to
  time-series channels** (`tdengine-v3` / `influxdb-v3`) and **deliberately never
  to plain channels such as MQTT** — publishing back to MQTT would self-excite
  when the subscribed topic is also a publish topic. Rules support `root` (whole
  message / object / automatic per-element splitting for top-level arrays), four
  value sources — `path` / `const` / `template` / `map` — implicit-AND filters
  (`eq`/`neq`/`gt`/`gte`/`lt`/`lte`/`contains`/`in`/`notin`/`exists`/`notexists`),
  `collectedAt` (epoch seconds/milliseconds/microseconds/nanoseconds and common
  date formats, normalized to local time at millisecond precision, falling back
  to batch arrival time when absent), and `kind` / `quality` value maps
  (`quality` requires an integer default so an external code with opposite
  semantics is never passed through). Target fields are a whitelist of
  collection-record fields and unknown names are rejected at save time; numbers
  are decoded as `json.Number`, so large integers keep full precision. Before
  saving you can test connectivity and preview conversion results against a
  sample message (neither is persisted); subscription configs are
  checksum-compared and hot-reloaded every 10 seconds, so
  create/update/delete/enable/disable needs no restart, and runtime status shows
  connection state, message/record/error counts and the last error. While
  delivering to the time-series database, successfully converted records also
  asynchronously index device/point metadata by the natural key
  `(device_id, device_address_id)` into the new `mqtt_subscription_device_point`
  table (no sampled values), surfaced by the console's "Subscription Devices &
  Points" page. New migrations `016_mqtt_subscription.sql` and
  `017_mqtt_subscription_device_point.sql`; the README (Chinese and English)
  documents the feature and configuration.
