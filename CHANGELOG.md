# 更新日志

本文件记录 iot-gateway 网关的所有重要变更，遵循[语义化版本](https://semver.org/)。每个版本先列中文条目，再列英文条目。

All notable changes to the iot-gateway gateway are documented here, following
[Semantic Versioning](https://semver.org/). Chinese entries come first under
each release, followed by English entries.

## [0.8.3] - 2026-11-20

### 新增

- **本机数据备份与恢复（P0）**：新增 `.igbak` 备份包（manifest + `VACUUM INTO` 一致性快照 + 外部 `default.yaml` / `<APP_ENV>.yaml` / 插件清单），在线备份不阻塞采集与推送；后台默认每 6 小时生成一次，保留 14 份 / 512MB，超限删最旧。恢复必须停服执行，校验 sha256、`integrity_check`、schema 兼容性与数据目录排他锁后，把 `db` / `-wal` / `-shm` 三件套一起替换，旧库保留为 `*.pre-restore-<时间戳>`；支持 `takeover`（保留网关 ID）与 `clone`（重新生成 ID）两种身份模式。CLI：`iot-gateway backup create|list|verify`、`iot-gateway restore --bundle ... [--identity clone] [--restore-config] [--yes]`。设计见 `backend/specs/data-backup.md`。
- **备份配置段**：`default.yaml` 新增 `backup.*`（`enabled` / `dir` / `defaultScope` / `startupDelayMinutes` / `intervalHours` / `keep` / `maxTotalMB` / `snapshotTimeoutSeconds`）。备份目录与文件权限收紧为 `0700` / `0600`；包内含 MQTT 口令、开放接口密钥与用户口令哈希，上传异机前仍需另行加密（P2）。
- **CLI 子命令入口**：`backup` / `restore` 在 `main()` 中先于 Windows 服务检测解析；磁盘 `default.yaml` 损坏时 CLI 回退到内置默认路径，仍可按 `--db` / `--dir` 完成紧急恢复。
- **本机备份与恢复（P1）**：新增 `config` 范围白名单快照（用户、设备、点位、物模型、推送通道、订阅、触发规则、运行开关、开放接口密钥、网关身份），不含 `push_outbox` / `alarm` / `control_log` 等运行与历史表。配置白名单表写入后 60 秒防抖生成 config 包，启动 5 分钟后先备一份基线；定时任务仍每 6 小时生成 full。`config` 恢复采用**表级覆盖**而不是整库替换：停服后在一个事务里删除并复制白名单表，保留当前报警历史、控制审计与断网缓存，事务前另存完整库副本。管理端新增 `/backup/*`（列表、状态、创建、下载、删除、暂存、上传、取消待恢复），只走管理员 JWT，刻意不挂 `/openApi` 与 `/agentApi`。上传/暂存只做校验，重启时在 `InitDB` 之前应用；校验失败时目录改名为 `restore-pending.failed-<ts>`，旧库照常启动。备份、下载、删除、暂存与恢复均写入 `data/backups/audit.log`。新增页面「系统设置 → 备份与恢复」，CLI 支持 `backup create --scope config` 与 `restore --pending`。
- **物模型绑定管理**：创建/编辑物模型可直接绑定设备（`deviceIds`），创建/编辑属性可直接绑定设备点位（`addressIds`），均为三态同步（省略 = 不改、空数组 = 全部解绑、非空 = 以列表为准），单次上限 200。已有点位绑定属性的设备拒绝改绑/解绑模型（`41013`）；已绑定其他属性的点位拒绝抢绑（`41019`）；同一设备对同一属性只能绑一个点位（`41021`）。新增错误码 `41019`–`41021`。
- **物模型绑定查询接口**：`/thingModel` 新增 `listThingModelDeviceOptions`（设备候选，keyword 远程搜索、`locked` 标记、超 200 条 `truncated`）、`listThingModelDevices`（模型已绑设备）、`listThingModelPointOptions`（属性表单点位候选，`kind` 由协议类型推导）、`listThingModelPropertyPoints`（属性已绑点位），并镜像到 `/agentApi`。物模型列表新增「设备数」抽屉，属性列表新增 `pointCount` 与「点位数」抽屉；表单支持多选绑定与锁定/类型不符的原因提示。
- **协议插件卸载**：协议页新增「卸载插件」入口，粒度为**插件文件**而非单行（一个插件可供给多个协议，确认弹窗会列出全部）。执行顺序为停插件进程 → 删除 `plugins/` 下的二进制并清理热升级残留的 `.old` 文件 → 删除其供给的 `iot_protocol` 行（含表单）。卸载前按 `device.protocol_id` 反查设备（含停用设备），仍有引用时拒绝并列出设备名与协议名（`90009`）；只接受插件目录内的文件名，路径注入被拒。Windows 上必须先退役进程再删文件（运行中的 exe 不能直接删除）；若文件删不掉，下一轮扫描会重新加载插件，状态回到卸载前而不是留下半死状态。管理端新增 `GET /plugin/list`（插件 → 协议名映射）与 `POST /plugin/uninstallPlugin`，镜像到 `/agentApi`，不开放到 `/openApi`；新增错误码 `90008`–`90010`。插件文件**意外消失**时仍沿用既有策略保留协议行，只有显式卸载才删行。
- **FANUC 机器人 CIP 驱动**：新增 `FANUC.CIP` 协议（`iot-driver-fanuc`），经 EtherNet/IP（CIP 显式消息）读写数值寄存器 R[]——整型 0x6B / 实型 0x6C，连接前用 ListIdentity 校验厂商号 591。同一 class 下间隙 ≤16 的连续点位自动合并为 `Get_Attribute_All` 块读（默认 100 个/请求、上限 124，设 1 关闭）；设备拒绝或数据错位时退回单读并暂停块读 5 分钟。位置寄存器、DI/DO 与字符串寄存器暂不支持。协议总数 15 → 16，`drivers.version` 升至 v0.4.1。
- **压测工具覆盖 FANUC**：`backend/cmd/loadtest` 新增 `-protocol fanuc` 与 `-fanuc-block`，假设备在 TCP 44818 上同时应答 UDP ListIdentity；建库批次由 2000 行降为 1000 行，避开 SQLite 绑定变量上限（10 设备 × 1000 点位矩阵此前报 `too many SQL variables`）。

### 变更

- **同一数据目录只允许一个网关实例**：启动时在数据目录获取 OS 级排他锁（Unix `flock` / Windows `LockFileEx`），锁被占用时拒绝启动。这同时让 `restore` 能可靠判断“服务是否已停止”。
- **WAL 维护器不再把进程内备份快照误报为外部读者**：备份期间跳过 “pinned by an external reader” 的 WARN（降级为 Debug）。CLI 跨进程备份仍可能看到该 WARN，属预期。
- **点位属性绑定唯一性**：设备点位创建/修改（含 `/openApi` 配置同步 upsert）新增「同一设备同一属性只能绑一个点位」校验，先返回 `41021` 而不是 SQLite 唯一约束原文。属性改类型/读写模式前可同时清空绑定，一次保存完成。

### 修复

- **实时值长文本撑破单元格**：实时监控与设备点位列表里的长十六进制 / 字符串值改为单行省略号截断，悬停显示完整值，质量标签不再被挤出单元格。

### Added

- **Local backup and restore (P0)**: introduces the `.igbak` bundle (manifest + consistent `VACUUM INTO` snapshot + external `default.yaml` / `<APP_ENV>.yaml` / plugin manifest). Online backups do not block collection or pushing; the scheduler runs every 6 hours by default and keeps 14 bundles / 512 MB, deleting the oldest when over the limit. Restore requires the gateway to be stopped: it verifies sha256, `integrity_check`, schema compatibility and the data-directory lock, then replaces the `db` / `-wal` / `-shm` trio together and keeps the old database as `*.pre-restore-<timestamp>`. Both `takeover` (keep the gateway ID) and `clone` (regenerate the ID) identity modes are supported. CLI: `iot-gateway backup create|list|verify`, `iot-gateway restore --bundle ... [--identity clone] [--restore-config] [--yes]`. Design: `backend/specs/data-backup.md`.
- **Backup configuration**: `default.yaml` gains `backup.*` (`enabled` / `dir` / `defaultScope` / `startupDelayMinutes` / `intervalHours` / `keep` / `maxTotalMB` / `snapshotTimeoutSeconds`). The backup directory and files are tightened to `0700` / `0600`; bundles contain MQTT credentials, OpenAPI keys and password hashes, so off-machine copies still require separate encryption (P2).
- **CLI subcommands**: `backup` / `restore` are parsed in `main()` before Windows service detection. If the on-disk `default.yaml` is broken, the CLI falls back to the built-in defaults and can still perform an emergency restore via `--db` / `--dir`.
- **Local backup and restore (P1)**: adds a `config` scope whitelist snapshot (users, devices, points, thing models, push channels, subscriptions, trigger rules, runtime switches, OpenAPI secrets, gateway identity) that excludes runtime and history tables such as `push_outbox` / `alarm` / `control_log`. Writes to config tables trigger a config bundle after a 60-second debounce, with a baseline bundle 5 minutes after startup; scheduled backups still produce a full snapshot every 6 hours. Restoring a `config` bundle performs **table-level replacement** instead of replacing the database file: after the service is stopped, whitelisted tables are deleted and copied in one transaction, preserving alarm history, control audit and offline buffering, with a full pre-restore copy saved first. Adds admin-only `/backup/*` endpoints (list, status, create, download, delete, stage, upload, cancel pending), deliberately not mounted under `/openApi` or `/agentApi`. Upload/stage only validates; the restore is applied on the next startup before `InitDB`. If validation fails the directory is renamed `restore-pending.failed-<ts>` and the old database starts normally. Backup, download, delete, stage and restore are recorded in `data/backups/audit.log`. New UI page under System Settings → Backup & Restore; CLI supports `backup create --scope config` and `restore --pending`.
- **Thing-model binding management**: creating/updating a thing model can bind devices (`deviceIds`), and creating/updating a property can bind device points (`addressIds`); both use tri-state sync (omitted = unchanged, empty = unbind all, non-empty = authoritative), capped at 200 per request. Devices with points bound to properties cannot be re-bound/unbound from a model (`41013`); points bound to another property are never stolen (`41019`); one device may bind at most one point to a property (`41021`). New error codes `41019`–`41021`.
- **Thing-model binding query APIs**: `/thingModel` gains `listThingModelDeviceOptions` (device candidates with keyword remote search, `locked` flag and `truncated` past 200), `listThingModelDevices` (devices bound to a model), `listThingModelPointOptions` (property-form point candidates with `kind` derived from the protocol type) and `listThingModelPropertyPoints` (points bound to a property), all mirrored to `/agentApi`. The model list gains a "device count" drawer and the property list gains `pointCount` plus a "point count" drawer; forms support multi-select binding with reasons for locked/incompatible entries.
- **Protocol plugin uninstall**: the protocol page gains an "Uninstall Plugin" action whose unit is the **plugin file**, not a single row (one plugin may supply several protocols, and the confirmation dialog lists them all). It stops the plugin process, deletes the binary under `plugins/` together with any hot-upgrade `.old` leftover, then deletes the `iot_protocol` rows (and their forms) supplied by that plugin. Before unloading it looks up devices by `device.protocol_id` (disabled devices included) and refuses while any remain, listing device and protocol names (`90009`); only file names inside the plugin directory are accepted, so path injection is rejected. On Windows the process must be retired before the file can be removed (a running exe cannot be deleted); if the file cannot be deleted, the next scan reloads the plugin and the state returns to pre-uninstall instead of a half-dead one. Admin endpoints: `GET /plugin/list` (plugin → protocol mapping) and `POST /plugin/uninstallPlugin`, mirrored to `/agentApi` and deliberately not exposed under `/openApi`; new error codes `90008`–`90010`. When a plugin file **disappears unexpectedly**, protocol rows are still kept as before — only an explicit uninstall deletes them.
- **FANUC robot CIP driver**: adds the `FANUC.CIP` protocol (`iot-driver-fanuc`), reading and writing numeric registers R[] over EtherNet/IP (CIP explicit messaging) — integer object 0x6B, real object 0x6C — with vendor ID 591 verified via ListIdentity before connecting. Contiguous points of the same class (gap ≤16) are merged into `Get_Attribute_All` block reads (100 registers per request by default, max 124; set 1 to disable); on rejection or shifted data the driver falls back to single reads and pauses block reads for 5 minutes. Position registers, DI/DO and string registers are not supported yet. Protocol count 15 → 16 and `drivers.version` bumped to v0.4.1.
- **FANUC coverage in the load-test tool**: `backend/cmd/loadtest` gains `-protocol fanuc` and `-fanuc-block`; its fake device answers ListIdentity on the same port over UDP alongside TCP 44818. Seeding now batches 1000 rows instead of 2000 to stay under SQLite's bound-variable limit (the 10 × 1000 device/point matrix previously failed with `too many SQL variables`).

### Changed

- **One gateway instance per data directory**: startup takes an OS-level exclusive lock on the data directory (Unix `flock` / Windows `LockFileEx`) and refuses to start when it is held. This also lets `restore` reliably decide whether the service is stopped.
- **WAL maintainer no longer reports in-process backup snapshots as external readers**: the "pinned by an external reader" warning is suppressed (logged at Debug) while a snapshot is running. A cross-process CLI backup may still produce it, which is expected.
- **One point per device per property**: creating/updating a device point (including the `/openApi` config-sync upsert) now enforces a single point per device/property pair, returning `41021` instead of the raw SQLite unique-constraint message. Changing a property's type or access mode can clear its bindings in the same save.

### Fixed

- **Long live values no longer break the cell**: long hex/string values in realtime monitoring and the device-point list are clipped to one line with an ellipsis and shown in full on hover, so the quality pill is no longer pushed out of the cell.

## [0.8.2] - 2026-11-17

### 新增

- **点位级工程量变换**：设备点位新增「工程量变换」（`device_address.transform_json`，迁移 `036_device_address_transform.sql`），在采集侧对原始值执行一次 `scale / round / trim / default` 流水线。实时值、点位阈值报警、触发规则、断网缓存与全部推送通道（MQTT / Kafka / HTTP / OPC UA / TDengine / InfluxDB）使用同一个工程值，时序库不再需要单独配置换算。`scale`（或保留小数位的 `round`）会把数值点位的 `kind` 提升为 `float`，OPC UA 整数节点相应提升为 Double；变换失败保留原值并限频记录日志。出站 `mapping-json` 的 transform 仍作为展示层保留，迁移到点位级后应删除通道侧重复的 `scale`。
- **点位配置列表展示绑定与变换**：设备点位配置列表新增「工程量变换」列，展示 `缩放 ×0.1 → 四舍五入 2 位` 形式的可读摘要（悬停看完整流水线），未配置时显示「原始值」；「物模型属性」列不再在窄屏下隐藏 —— 绑定关系与换算规则都属于排查采集值的必要信息。
- **开放接口配置同步**：`/openApi` 新增配置同步接口（文档 4.16）：能力协商 `GET /config/capabilities`、物模型 / 设备点位 / 推送通道 / 告警 Webhook / 运行开关的脱敏只读（附 `contentHash`），以及推送通道、告警 Webhook、运行开关、设备点位的批量写入。只改网关配置、不触达设备，逐条部分成功；`"***"` 表示保留现有密钥，hash 采用 `sha256-canonical-json-v1`（密钥只计存在性）。物模型 upsert 新增 `replaceProperties` 删除未出现的属性；新增错误码 `42001`–`42009`。
- **开放接口推送通道连通性测试**：`/openApi` 新增 `POST /pushChannel/testConnectivity`（文档 4.16.7），用调用方传入的类型与未保存的通道配置真实连接一次下游（MQTT / HTTP / 时序库等）后立即断开，供管理平台保存推送通道前校验。`configJson` 与配置同步的 `config` 同结构，但密钥必须是真实值（`"***"` 无法鉴权）；失败时原样返回网关的具体原因（口令错误、地址不可达、端口拒绝）。只探测，不落库、不改网关配置。
- **开放接口采集组列表**：`/openApi` 新增 `GET /collectorGroup/list`（文档 4.16.2），按可选 `deviceId` 返回网关各设备的采集组，供管理平台推送通道「采集组订阅」下拉框使用 —— `config.groupIds` 是网关本地主键，平台只能从网关取这份列表。采集组 VO 新增设备编码 `deviceCode`，便于平台按「设备编码 + 组编码」与主数据对齐。
- **开放接口出站映射预览**：`/openApi` 新增 `POST /pushChannel/previewMapping`（文档 4.16.8），用网关内固定样例批次试编码 `native-json` / `group-json` / `mapping-json` / `script`，供管理平台可视化映射编辑器与无需映射的格式即时预览；`native-json` / `group-json` 不需要 `mapping`，只按 `format` 选编码器。只编码，不发送、不落库，映射或编码错误放在 `data.error` 而不当作接口失败。
- **开放接口推送通道运行状态**：`/openApi` 新增 `GET /pushChannel/status`（文档 4.22），返回推送引擎内存快照（运行/连接状态、队列与补发深度、发布/写出/丢弃计数、最近成功与错误），供管理平台按网关聚合监控。只读、不触达下游；计数重启清零，仅含已启用且加载成功的通道。
- **物模型分类支持平台自定义 code**：开放接口的分类校验由内置枚举改为格式校验（小写字母开头、≤32），词表由管理平台维护；本地管理端仍只能新建内置分类，但会保留并展示已下发的自定义分类。
- **Docker 基础镜像可覆盖**：`Dockerfile` 新增 `NODE_IMAGE` / `GOLANG_IMAGE` / `ALPINE_IMAGE` 构建参数，`docker-compose.yml` 透传同名环境变量，便于切换国内镜像源或私有 registry，默认仍为官方标签。
- **发现广播携带平台信息**：`notify` / `reply` 新增 `os` / `arch` 字段，取自 Go 的 `runtime.GOOS` / `runtime.GOARCH`（如 `linux/amd64`、`linux/arm64`、`windows/amd64`），供管理平台发现网关后下发与它匹配的插件二进制。字段带 `omitempty`，旧版接收方照常解析。
- **开放接口插件二进制下发**：`/openApi` 新增 `GET /plugin/info`（平台 / 命名规则 / 上传限制）、`GET /plugin/status`（已加载插件的 sha256 快照）与 `POST /plugin/importPlugin`（multipart 上传，可选 `X-Content-SHA256` 完整性校验），供管理平台按 `os/arch` 选包下发；上传仍受 `plugin.enabled`、单文件 / 单批 / 请求体上限约束，新增错误码 `90007`（文档 4.17）。
- **开放接口设备扫描**：`/openApi` 新增设备扫描接口（文档 4.18）：能力查询、发起 / 查询 / 续扫 / 取消、点位扫描能力，以及两种点位扫描——已下发设备按 `deviceCode` 取网关侧配置，未下发设备按 `protocolName` + `protocolJson` 用平台主数据配置直接扫描；`discoverPointsFromScan` 按 `jobId` + `index` 扫描结果中的设备，连接配置由服务端从任务快照取，不采信客户端。只提供扫描能力，结果不直接落库，设备与点位仍走 4.16.3 `deviceConfig/batchUpsert` 下发；受 `deviceScan.enabled` 控制，新增错误码 `40021`–`40027`。
- **开放接口设备实时数据快照**：`/openApi` 新增 `GET /realtime/snapshot`（文档 4.19），按 `deviceId` 或设备编码 `code` 定位设备（二者同时给出以 `deviceId` 为准），返回在线 / 采集中 / 采集引擎状态、最近成功时间与逐点最新值（质量码、`stale` 过期标记），供管理平台做设备实时监控。值直接取自采集引擎内存快照，不读时序库、不触达现场设备；未采集到的点位仍占位返回（`collected=false`）以保证分页之间行序稳定，`stale=true` 表示距上次更新超过该点位生效周期（绑定采集组时取组周期）的 3 倍。管理端按 UUID 查询、开放接口按自然键查询，两条路最终复用同一个 `GetDeviceSnapshot`，实时值语义不漂移。
- **开放接口控制能力查询**：`/openApi` 新增 `GET /control/capability`（文档 4.20），按 `deviceId` 或设备编码 `code` 返回 `control.enabled`、协议可写性 `writable` 与不可写原因 `reason`（`disabled` / `device_disabled` / `protocol_missing` / `protocol_not_writable`），供管理平台在展示写值入口前判断「现在能不能写、为什么不能写」。只读、不触达现场设备；真正写值仍走 4.8 `/control/write`。
- **开放接口设备连接测试**：`/openApi` 新增 `POST /device/testConnection`（文档 4.21），用调用方传入的协议名与未保存的连接配置真实连接一次现场设备（与管理端 `POST /device/testDeviceConnection` 同一条驱动路径），供管理平台「新建设备对象」在保存前校验连接配置。只探测：不写网关配置、不改设备状态；失败返回 `40003` 并原样带回驱动错误（地址不可达、串口被占用、超时等），调用方应原样展示。
- **OPC UA 验证工具**：新增 `backend/cmd/opcuaprobe`，用标准 OPC UA 客户端完成 connect / browse / read / subscribe，独立验证网关 OPC UA Server 通道有没有把点位供出去，不依赖 UaExpert，便于在服务器、CI 或录屏环境里重复执行。
- **实时监控写值区分原始值与工程值**：设备实时快照透传点位 `transformJson`；控制下发弹窗的输入框始终按原始值填写，并按工程量变换只读预览对应的工程值（下发链路不做逆变换，避免把工程值当成原始值写入设备）；同一弹窗新增设备信息卡与「复制地址」。前端换算算子与后端 `mappingdsl` 对齐（`scale` 默认 ×1+0、`round` 定长小数），换算失败时不显示预览。

### 变更

- **控制台交互优化**：点位与触发规则编辑弹窗改为分栏卡片并补充内联帮助，点位弹窗加宽到 `min(900px, 94vw)`；设备扫描页重做统计卡、协议说明侧栏与结果区布局，扫描中的结果不再随轮询跳动。
- **构建脚本目标校验**：`build.bat` 新增 `linux` / `native` 目标，未知目标直接报错退出；此前未知目标会静默回落到全量构建，`build.bat linux` 实际编译了四个平台。

### 修复

- **开机后的无谓采集重建**：插件管理器首次扫描（`Manager.Start`）不再安排采集配置刷新。该次扫描必然早于采集引擎启动，加载到的驱动会自然进入首个采集任务；此前它仍会排一次 2s 防抖刷新，定时器到期时引擎已运行，导致每次开机后约 2 秒白白重建一轮全部采集任务（`go test -race` 下还会表现为 `TestCollectionContinuesAcrossPluginHotUpgrade` 偶发失败）。运行期的插件增删与热升级行为不变。

### Added

- **Per-point engineering transform**: device points gain an "engineering transform" (`device_address.transform_json`, migration `036_device_address_transform.sql`) that runs a `scale / round / trim / default` pipeline once at collection. Live values, point threshold alarms, trigger rules, the offline spool and every push channel (MQTT / Kafka / HTTP / OPC UA / TDengine / InfluxDB) see the same engineering value, so TSDB channels no longer need their own conversion. `scale` (or `round` with decimal digits) promotes numeric points' `kind` to `float`, and integer OPC UA nodes become Double accordingly; on failure the raw value is kept and the error is logged with rate limiting. The outbound `mapping-json` transform remains a presentation layer — remove any duplicate `scale` from channel mappings after moving it to the point.
- **Point list shows bindings and transforms**: the device point configuration list gains an "Engineering Transform" column with a readable summary (e.g. `Scale ×0.1 → Round 2 dp`, full pipeline on hover) and "Raw" when unset; the "Thing Model Property" column is no longer hidden on narrow screens, since both the binding and the conversion rule are needed to interpret a collected value.
- **OpenAPI configuration sync**: adds a configuration-sync API group under `/openApi` (docs 4.16): capability negotiation via `GET /config/capabilities`, masked reads for thing models / devices & points / push channels / alarm webhooks / runtime switches (each with `contentHash`), and batch writes for push channels, alarm webhooks, runtime switches and device points. Writes only change gateway configuration — never field devices — and report per-item results; `"***"` keeps the existing secret, and hashes use `sha256-canonical-json-v1` (secrets count by presence only). Thing-model upsert gains `replaceProperties` to drop absent properties; new error codes `42001`–`42009`.
- **OpenAPI push channel connectivity test**: `/openApi` gains `POST /pushChannel/testConnectivity` (docs 4.16.7), connecting once to the downstream (MQTT / HTTP / time-series database, …) with the caller's channel type and unsaved config, then disconnecting immediately, so the management platform can validate a push channel before saving it. `configJson` has the same shape as the `config` used by configuration sync, but secrets must hold real values (`"***"` cannot authenticate); failures return the gateway's concrete reason (bad credentials, unreachable host, refused port). Probe only — nothing is persisted and gateway configuration is unchanged.
- **OpenAPI collector group list**: `/openApi` gains `GET /collectorGroup/list` (docs 4.16.2), returning the gateway's collector groups with an optional `deviceId`, for the management platform's push channel "collector group subscription" selector — `config.groupIds` are gateway-local keys, so the platform can only get this list from the gateway. The collector-group VO gains the device code `deviceCode`, so the platform can align groups with master data by "device code + group code".
- **OpenAPI outbound mapping preview**: `/openApi` gains `POST /pushChannel/previewMapping` (docs 4.16.8), encoding `native-json` / `group-json` / `mapping-json` / `script` against the gateway's fixed sample batch, for the management platform's visual mapping editor and mapping-less formats to preview instantly; `native-json` / `group-json` need no `mapping` and pick the encoder from `format` alone. Encoding only — nothing is sent downstream or persisted, and mapping/encoding errors are returned in `data.error` rather than as API failures.
- **OpenAPI push channel runtime status**: `/openApi` gains `GET /pushChannel/status` (docs 4.22), returning the push engine's in-memory snapshot (running/connected state, queue and spool depth, publish/row/dropped counters, last success and error) for the management platform to aggregate per gateway. Read-only with no downstream access; counters reset on restart and only enabled, successfully loaded channels appear.
- **Platform-defined thing-model categories**: OpenAPI category validation now checks the format (lowercase letter start, ≤32) instead of a built-in whitelist, so the management platform owns the vocabulary; the local console still creates only built-in categories but keeps and displays custom codes delivered through the API.
- **Overridable Docker base images**: `Dockerfile` gains `NODE_IMAGE` / `GOLANG_IMAGE` / `ALPINE_IMAGE` build args wired through `docker-compose.yml`, easing the switch to mainland mirrors or a private registry; defaults stay on the official tags.
- **Discovery broadcasts carry platform info**: `notify` / `reply` gain `os` / `arch` fields taken from Go's `runtime.GOOS` / `runtime.GOARCH` (e.g. `linux/amd64`, `linux/arm64`, `windows/amd64`), so the management platform can deliver the matching plugin binaries. Both fields use `omitempty`, so older receivers parse the rest unchanged.
- **OpenAPI plugin binary delivery**: `/openApi` gains `GET /plugin/info` (platform / naming rules / upload limits), `GET /plugin/status` (sha256 snapshot of loaded plugins) and `POST /plugin/importPlugin` (multipart upload with optional `X-Content-SHA256` integrity check), so the management platform can select and deliver a package by `os/arch`. Uploads remain bounded by `plugin.enabled` and the per-file / per-batch / request-body limits; new error code `90007` (docs 4.17).
- **OpenAPI device scan**: `/openApi` gains device-scan endpoints (docs 4.18): capabilities, start / status / continue / cancel, point-scan capability, and two point-scan modes — by `deviceCode` using the gateway's own device config, or by `protocolName` + `protocolJson` using the platform's master data for not-yet-delivered devices; `discoverPointsFromScan` scans one device in a job result by `jobId` + `index`, with the connection config taken from the job snapshot rather than the client. Scan results are not persisted here — devices and points still go through 4.16.3 `deviceConfig/batchUpsert` — and everything is gated by `deviceScan.enabled`; new error codes `40021`–`40027`.
- **OpenAPI realtime snapshot**: `/openApi` gains `GET /realtime/snapshot` (docs 4.19), locating a device by `deviceId` or device code `code` (`deviceId` wins when both are given) and returning online / collecting / collector-engine status, last success time and each point's latest value (quality code, `stale` flag) for the management platform's realtime device monitoring. Values come straight from the collector engine's in-memory snapshot — no time-series reads, no field-device access; points not yet collected still occupy a row (`collected=false`) so row order stays stable across pages, and `stale=true` means no update for 3× the point's effective period (the group period when bound to a collector group). The admin side queries by UUID and the OpenAPI by natural key, with both ending at the same `GetDeviceSnapshot`, so realtime semantics never drift.
- **OpenAPI control capability query**: `/openApi` gains `GET /control/capability` (docs 4.20), returning by `deviceId` or device code `code` the `control.enabled` switch, protocol writability `writable` and a `reason` when not writable (`disabled` / `device_disabled` / `protocol_missing` / `protocol_not_writable`), so the management platform can decide whether to show a write entry. Read-only and never touching field devices; actual writes still go through 4.8 `/control/write`.
- **OpenAPI device connection test**: `/openApi` gains `POST /device/testConnection` (docs 4.21), opening a real connection once to a field device with the caller's protocol name and unsaved connection config (the same driver path as the admin `POST /device/testDeviceConnection`), for the management platform to validate connection settings before saving a new device object. Probe only — no gateway config or device state changes; failures return `40003` with the raw driver error (unreachable address, serial port busy, timeout, …) that callers should surface verbatim.
- **OPC UA verification tool**: adds `backend/cmd/opcuaprobe`, a standard OPC UA client with connect / browse / read / subscribe modes that independently checks whether the gateway's OPC UA Server channel exposes points, without UaExpert, so it can be re-run on a server, in CI or on a recording host.
- **Raw vs. engineering values in realtime writes**: the realtime device snapshot now passes each point's `transformJson` through; the control-write dialog keeps the input in the raw value and shows a read-only preview of the corresponding engineering value (the write path has no inverse transform, so an engineering value is never sent as if it were raw). The same dialog gains a device identity card and "copy address". The frontend pipeline mirrors the backend `mappingdsl` operators (`scale` defaults to ×1+0, `round` to fixed decimals) and shows no preview when conversion fails.

### Changed

- **Console interaction polish**: point and trigger-rule edit dialogs are split into sectioned cards with inline help, and the point dialog widens to `min(900px, 94vw)`; the device scan page gains a stat card, a protocol-help aside and a stable results area, so in-flight results no longer jump between polls.
- **Build script target validation**: `build.bat` gains `linux` / `native` targets and rejects unknown targets; previously an unknown target silently fell back to the full build, so `build.bat linux` compiled all four platforms.

### Fixed

- **Pointless collection rebuild after boot**: the plugin manager's initial scan (`Manager.Start`) no longer schedules a collector refresh. That scan always runs before the collector starts, so the drivers it loads are picked up by the first collection task; previously it still queued a 2s-debounced refresh, which by then saw a running engine and rebuilt the entire collection task about 2 seconds after every boot (and made `TestCollectionContinuesAcrossPluginHotUpgrade` flaky under `go test -race`). Runtime plugin add/remove and hot-upgrade behavior is unchanged.

## [0.8.1] - 2026-11-11

### 新增

- **Kafka 推送通道**：新增 `kafka` 类型，基于纯 Go 的 franz-go 实现，支持多个 seed broker、SASL/PLAIN、TLS、`acks`（all/leader/none，非 all 自动关闭幂等写入）、自动建 topic 开关与断网缓存/补发。复用消息类通道的全部载荷格式（native-json / group-json / mapping-json / script）、采集组订阅、载荷预览与测试发送；record key 固定为设备编码（缺失回落设备 ID），同一设备落在同一分区，`workers` 默认 1 保证按序发送。新增迁移 `035_add_kafka_push_channel_form.sql`。
- **开放接口报警明细**：新增报警历史分页 `GET /openApi/alarm/pageAlarm`、当前活跃离线报警 `GET /openApi/alarm/listActive`（不分页）与越限记录分页 `GET /openApi/alarm/pageThresholdRecord`，与既有的可靠性 / 异常聚合报表互补，回答「具体是哪几条」（开放接口文档 v1.7，4.9–4.11）。
- **开放接口订阅设备与点位同步**：新增 `GET /openApi/subscription/devicePoint/sync`，按 `deviceId` 游标分批返回订阅报文维护的设备 / 点位元信息（`mqtt_subscription_device_point`），形态与 4.5 设备同步一致，供外部系统全量遍历（开放接口文档 v1.8，4.12）。
- **开放接口物模型批量 upsert**：新增 `POST /openApi/thingModel/batchUpsert`，外部系统可按 `code` / `identifier` 自然键批量创建或更新物模型与其属性（没有则创建，有则更新）。每个模型一个事务、跨模型部分成功，逐条返回 created/updated/failed 与错误码；请求中未出现的属性不删除，已被点位绑定的属性改变 `kind`/`accessMode` 仍被拒绝（`41018`）（开放接口文档 v1.9，4.13）。
- **开放接口物模型批量删除**：新增 `POST /openApi/thingModel/batchDelete`（按 `code` 批量删模型，连带其属性）与 `POST /openApi/thingModelProperty/batchDelete`（按 `modelCode` + `identifier` 批量删属性），逐条返回 deleted/failed 与错误码，跨条部分成功；沿用单条删除的绑定保护（模型被设备绑定或其属性被点位绑定返回 `41005`，属性被点位绑定返回 `41006`），已不存在时分别返回 `41002` / `41004`（开放接口文档 v1.10，4.14–4.15）。
- **按协议生成完整设备导入模板**：`GET /device/importTemplate` 支持 `protocolId`。指定协议时，模板在基础列（名称/协议/描述/编码/物模型/状态）之外追加该协议 `form_json` 的全部连接参数列，标题按当前语言解析、必填项带标记、下拉项带 Excel 数据验证；连接参数会按字段类型写入 `protocol_json`（input→字符串、inputNumber→数字、select→选项原始类型）。含参数列的模板要求整表同一协议，混协议或模板与协议表单不一致时整表拒绝。示例行移到第二张工作表，直接导入未修改的模板不会产生示例设备。按协议模板导入时还会根据 `host/port/unitId/dstNode` 生成扫描身份签名（`scan_identity`），让导入设备参与扫描判重；通用模板与串口协议仍留空。手工新建与修改设备连接配置时也会按配置生成/**重算**扫描身份（修改配置此前是直接清空），只有认不出 host 时才写空串。
- **设备导入的「编码」改为必填**：模板必须包含编码列，逐行留空、格式非法或重复只让该行失败，不再按名称自动生成。旧的三列模板（无编码列）不再支持，四列及以上（含编码）的旧模板仍可导入。
- **设备导入支持物模型与启停状态**：模板新增「物模型」（名称或标识，必须存在且启用）与「状态」（启用/禁用，默认启用）两列。
- **导入模板表头标注优化**：必填列统一在列名末尾加 ` *`，表头按必填（深蓝底 + 红色下边框）/选填（浅蓝底）分色，示例表底部附一行图例。导入仍按列名与 `[fieldKey]` 识别，不受配色影响。
- **设备列表展示在线状态**：设备对象列表的「状态」列改为「在线状态」，按「禁用 / 未接入 / 在线 / 离线」四态展示，并带悬停说明。后端 `DeviceVO` 新增 `collected` 字段，区分「启用但尚未进入采集调度」与真正的在线/离线，口径与首页设备总览一致。
- **扫描结果导出为设备导入模板**：`POST /deviceScan/exportTemplate` 把勾选的扫描结果按该协议的设备导入模板导出（.xlsx）。文件与 `GET /device/importTemplate?protocolId=` 同构，表头下方直接填好名称、编码、描述与 `protocolJson` 里的连接参数；在 Excel 里改完编码/名称后，走设备导入即可落库。扫描页「添加为设备」弹窗新增「导出 Excel」按钮。

### 修复

- **被推送通道订阅的采集组拒绝删除**：删除采集组前检查各推送通道的 `config_json.groupIds`，仍被任一通道（含停用通道）订阅时返回新错误码 `40050`，避免删除后通道配置里留下永远匹配不到任何批次的组 ID。设备删除（单条与批量）会级联删掉名下采集组，因此名下任一组被订阅时同样整批拒绝并返回 `40065`；批量删除不做部分成功。先从通道配置中移除该组即可恢复删除。
- **设备/点位被触发规则引用时拒绝删除**：删除设备（单条与批量）前检查事件触发规则的动作设备、动作点位与条件点位引用；删除点位（单条与批量）前检查动作点位与条件点位引用。有引用时分别返回新错误码 `40063` / `40064` 并整批不删，避免删除后规则持续报 `condition address ... not found`、且再也无法触发。先修改或删除相关规则即可恢复删除。

### Added

- **Kafka push channel**: adds the `kafka` type backed by the pure-Go franz-go client, supporting multiple seed brokers, SASL/PLAIN, TLS, `acks` (all/leader/none, with idempotent writes disabled for non-all), an auto-create-topic switch and the shared offline spool/replay. It reuses every message format (native-json / group-json / mapping-json / script), group subscriptions, payload preview and test send; the record key is the device code (falling back to the device ID) so one device stays on one partition, and `workers` defaults to 1 for ordered sends. Adds migration `035_add_kafka_push_channel_form.sql`.
- **OpenAPI alarm details**: adds alarm-history paging `GET /openApi/alarm/pageAlarm`, currently-active offline alarms `GET /openApi/alarm/listActive` (unpaginated) and threshold-record paging `GET /openApi/alarm/pageThresholdRecord`, complementing the existing reliability / abnormality aggregates with "which alarms exactly" detail queries (OpenAPI docs v1.7, 4.9–4.11).
- **OpenAPI subscription device/point sync**: adds `GET /openApi/subscription/devicePoint/sync`, returning device/point metadata maintained from subscription messages (`mqtt_subscription_device_point`) in device-level cursor batches, mirroring 4.5 for full external traversal (OpenAPI docs v1.8, 4.12).
- **OpenAPI thing-model batch upsert**: adds `POST /openApi/thingModel/batchUpsert`, letting external systems create or update thing models and their properties in batches by the `code` / `identifier` natural keys (create if absent, update if present). Each model is one transaction with partial success across models; every entry reports created/updated/failed plus an error code. Properties absent from the request are never deleted, and changing `kind`/`accessMode` on a property bound to a point is still rejected (`41018`) (OpenAPI docs v1.9, 4.13).
- **OpenAPI thing-model batch delete**: adds `POST /openApi/thingModel/batchDelete` (delete models by `code`, including their properties) and `POST /openApi/thingModelProperty/batchDelete` (delete properties by `modelCode` + `identifier`). Each entry reports deleted/failed plus an error code, with partial success across entries. The single-delete binding guards are reused (a model bound to a device or whose properties are bound to points returns `41005`; a bound property returns `41006`), and missing entries return `41002` / `41004` (OpenAPI docs v1.10, 4.14–4.15).
- **Protocol-aware complete device import template**: `GET /device/importTemplate` accepts `protocolId`. For a selected protocol the template adds every connection-parameter column from that protocol's `form_json` to the base columns (name / protocol / description / code / thing model / status), localizes titles, marks required fields and adds Excel data validation for options; imported values are written into `protocol_json` with their proper types (input → string, inputNumber → number, select → the option's original type). Templates with parameter columns require a single protocol for the whole file; mixed protocols or a stale template are rejected wholesale. The sample row moves to a second worksheet so importing an untouched template creates no sample device. Protocol-template imports also derive the scan identity (`scan_identity`) from `host/port/unitId/dstNode`, so imported devices participate in scan duplicate detection; universal templates and serial protocols stay empty. Manual creation and connection-config edits now generate/**recompute** the scan identity from the config (edits previously cleared it), writing an empty value only when no host can be derived.
- **Device import code column is now required**: the template must contain a code column; empty, invalid or duplicate codes fail only that row, and codes are no longer generated from names. The legacy three-column template (without a code column) is no longer supported; legacy four-or-more-column templates that include a code column still import.
- **Thing model and enabled status in device import**: two new columns — "thing model" (name or code, must exist and be enabled) and "status" (enabled/disabled, default enabled).
- **Clearer required/optional header annotations**: required columns now end with ` *`, and header cells are color-coded (required: deep blue with a red bottom border; optional: light blue). The example sheet ends with a legend. Import still matches columns by header name and `[fieldKey]`, so the styling does not affect parsing.
- **Online status in the device list**: the device object list's status column now shows a four-state "online status" — disabled / not collected / online / offline — with a hover explanation. `DeviceVO` gains a `collected` field so "enabled but not in the collection schedule" is distinguishable from genuinely online/offline, using the same semantics as the dashboard overview.
- **Export scan results as a device import template**: `POST /deviceScan/exportTemplate` exports the selected scan results using the protocol's device import template (.xlsx). The file matches `GET /device/importTemplate?protocolId=` exactly, with names, codes, descriptions and connection parameters from `protocolJson` prefilled below the header; edit the codes/names in Excel and import them from the device object page. The scan add dialog gains an "Export to Excel" button.

### Fixed

- **Collector groups subscribed by push channels can no longer be deleted**: deleting a collector group now checks every push channel's `config_json.groupIds`; if any channel (including disabled ones) still subscribes to it, deletion is rejected with the new error code `40050`, so channel configs no longer retain group IDs that can never match a batch. Since deleting a device cascades to its collector groups, single and batch device deletion are rejected wholesale with `40065` when any group of the device is subscribed; batch deletion never partially succeeds. Remove the group from the channel configuration first.
- **Devices and points referenced by trigger rules can no longer be deleted**: single and batch device deletion check event-trigger rules for references through the action device, action points and condition points; single and batch point deletion check action and condition points. A referenced device or point returns the new error code `40063` / `40064` and the whole batch is left untouched, so deleting them no longer leaves rules logging `condition address ... not found` and unable to fire. Modify or delete the referencing rule first to proceed.

## [0.8.0] - 2026-11-05

### 新增

- **扫描建库支持自定义设备编码**：扫描结果新增建议编码 `suggestedCode`（按地址派生，批内唯一），「添加为设备」弹窗新增编码列并预填，可修改；创建接口 `items[].code` 可选，留空仍按名称自动生成，显式给出时逐行校验格式与库内/批内唯一，不合法或重复只让该行失败（**不自动避让** —— 编码创建后不可改，悄悄改成 `-2` 比报错更糟）。
- **设备扫描两阶段：TCP 可达性预筛**：插件在 `DiscoverySpec` 里新增可选 `Transport`（`tcp` / `udp` / 留空）。对声明 `tcp` 的协议，网关先用高并发、短超时（默认 256 / 500ms）拨一遍扫描端口，连不上的地址直接判死（计入 `scanned`、不产生错误），只把端口开着的主机交给插件 `probe`。广播目标永远绕过，`udp`/未声明协议行为不变。典型 /24 的协议探测次数从 254 次降到十几台，/16 从小时级降到分钟级。新增配置 `deviceScan.prefilterEnabled` / `prefilterConcurrency` / `prefilterTimeoutMs`（均需重启）。需要 SDK v0.4.0 与 drivers v0.4.0 配套。

### 变更

- **设备扫描默认并发 8 → 32，上限 32 → 128**：扫描耗时 ≈ ceil(目标数 / 并发) × 单目标耗时，一个网段里不可达地址占绝大多数、每个都要等满插件侧的拨号超时，因此并发是最直观的加速旋钮。默认值下 /24（254 个目标）按 1.5s 拨号超时由约 48 秒降至约 12 秒。**升级后若保留了旧的 `default.yaml`，需手动把 `deviceScan.maxConcurrentProbes` 调到 32 或以上**；本项改动需重启网关。
- **扫描剩余批次自动续扫**：单批上限（`deviceScan.maxTargets`，默认 1024）仍作内部安全阀，但扫描页在任务正常结束（`done` 且 `truncated=true`）时自动调用 `POST /deviceScan/continue`，直到 `remainingTargets=0`，无需逐批点按钮；取消、失败或自动续扫失败时停止续扫，工具条上的「继续扫描」供手动重试。前端轮询上限改为按本批估算耗时每批重新计时，跨批总时长不再受 30 分钟限制。

### Added

- **Custom device codes when creating devices from scan results**: scan results now carry a suggested `suggestedCode` (derived from the address and unique within the batch), and the "Add as devices" dialog gains a prefilled, editable code column. `items[].code` is optional on the create API: when omitted the code is still generated from the name; when provided it is validated per row for format and uniqueness against both the database and the batch, and invalid or duplicate rows fail individually (**no silent `-2` renaming** — codes are immutable after creation, so quietly changing one is worse than an error).
- **Two-stage device scan: TCP reachability prefilter**: `DiscoverySpec` gains an optional `Transport` (`tcp` / `udp` / unset). For protocols declaring `tcp`, the gateway first dials the scan port at high concurrency with a short timeout (default 256 / 500ms); unreachable addresses are marked done (counted in `scanned`, no error entry) and only hosts with an open port reach the plugin's `probe`. Broadcast targets always bypass it, and `udp`/unset protocols behave as before. A typical /24 drops from 254 protocol probes to a dozen, and a /16 from hours to minutes. New settings `deviceScan.prefilterEnabled` / `prefilterConcurrency` / `prefilterTimeoutMs` (all require a restart). Requires SDK v0.4.0 and drivers v0.4.0.

### Changed

- **Device-scan concurrency default raised from 8 to 32, cap from 32 to 128**: scan time ≈ ceil(targets / concurrency) × per-target time. Most addresses in a subnet are unreachable and each waits out the plugin's dial timeout, so concurrency is the most direct speed lever. At the new default a /24 (254 targets) drops from about 48s to about 12s with a 1.5s dial timeout. **If an existing deployment keeps its old `default.yaml`, raise `deviceScan.maxConcurrentProbes` to 32 or more manually**; this setting requires a restart.
- **Automatic continuation of remaining scan batches**: the per-batch cap (`deviceScan.maxTargets`, default 1024) stays as an internal safety valve, but the scan page now calls `POST /deviceScan/continue` automatically after a batch completes normally (`done` with `truncated=true`) until `remainingTargets=0`, so users no longer click through batches. Cancelling, a failure, or a failed auto-continue stops the chain and leaves the toolbar's Continue scan button for manual retry. The frontend poll timeout is now re-armed per batch from that batch's estimate, so the total time across batches is no longer capped at 30 minutes.

## [0.7.2] - 2026-11-05

### 新增

- **MQTT 订阅复制**：新增 `POST /mqttSubscription/copyMqttSubscription`，按 ID 复制为一条**默认停用**的副本（避免同一 topic 重复入库），配置列原样取自库中原行、不经前端表单往返；名称自动加 `-copy` / `-copy2` 后缀并查重，拼缀后仍受 100 字符上限约束。控制台列表新增复制按钮，接口镜像到 `/agentApi`。

### 变更

- **订阅 Topic 长度上限 500 → 2000**：创建、编辑与连通性测试同步放宽，表单输入框显示字数；控制下行 Topic 仍保持 500。

### 修复

- **pluginhost：已取消的 RPC 立即失败**：`client.call` 在写帧前先检查 `ctx.Err()`，已取消/超时的调用不再发送请求，修复取消扫描时探测偶发"成功"的问题。

### Added

- **MQTT subscription duplication**: `POST /mqttSubscription/copyMqttSubscription` copies a subscription by ID into a **disabled-by-default** copy (so the same topic is not ingested twice); config columns come straight from the stored row with no form round-trip. The name gets a `-copy` / `-copy2` suffix with collision avoidance and is truncated to stay within the 100-character limit. The console list gains a copy action and the route is mirrored to `/agentApi`.

### Changed

- **Subscription topic limit raised from 500 to 2000 characters**: create, update and connectivity-test validation are relaxed together and the form shows a character counter; the control downlink topic stays capped at 500.

### Fixed

- **pluginhost: cancelled RPCs fail fast**: `client.call` now checks `ctx.Err()` before writing a frame, so cancelled/timed-out calls are never sent — fixes probes intermittently "succeeding" after a scan was cancelled.

## [0.7.1] - 2026-11-05

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
