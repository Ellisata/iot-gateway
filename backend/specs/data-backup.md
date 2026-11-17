# 数据备份与快速恢复

> 状态：**设计稿，未实现**（本文只定义契约与实现方案，代码落地见 §12 的分期计划）
>
> 适用版本：iot-gateway-premium v1.4+
>
> 关联代码：[database/sqlite](../database/sqlite/)、[configFile](../configFile/)、
> [pluginhost](../pluginhost/)、[service/openApiConfigService.go](../service/openApiConfigService.go)

---

## 0. 结论摘要

- **备份单位是“整库一致性快照 + 外部配置 + 插件清单”的 `.igbak` 包**，不是直接复制 `iot-gateway.db`。
- **在线备份用 `VACUUM INTO` 走独立数据库连接**，不阻塞采集/推送，也不会复制出撕裂的 WAL 副本。
- **默认做 `config` 范围备份**（配置、用户、密钥、网关身份），`full` 范围（含报警历史/断网缓存）按周或手动执行。
- **本机备份只解决“误删/升级回滚”**；整机故障必须依赖异机副本 + 预装同版本备机。恢复一律**停机执行**，不做热恢复。
- 备份包含 MQTT 口令、开放接口密钥、用户口令哈希等敏感数据：目录 `0700`、文件 `0600`，上传异机前必须加密。

目标指标：

| 指标 | 建议值 | 说明 |
|---|---|---|
| RPO（配置） | ≤ 1 分钟 | 配置变更后 60s 防抖触发 |
| RPO（历史/运行数据） | ≤ 6 小时 | 定时 `full` 备份 |
| RTO（同机回滚） | ≤ 5 分钟 | 停服 + `restore` + 启动 |
| RTO（整机切换） | ≤ 10 分钟 | 备机已预装二进制/插件，拉取最新包恢复 |
| 副本 | 本机 ≥ 1 + 异机 ≥ 1 | 异机不能与网关同盘 |

---

## 1. 目标与非目标

### 1.1 目标

1. **配置零丢失**：设备、点位、物模型、推送通道、采集组、订阅、触发规则、用户、密钥、运行开关、网关身份可完整恢复。
2. **同机可回滚**：升级失败、配置误删、SQLite 文件损坏时，几分钟内回到上一份可用快照。
3. **整机可切换**：硬盘/主板/系统故障后，在备机上用同一份备份接管，管理平台尽量无感。
4. **备份可校验**：每个包有 manifest、sha256、schema 版本、完整性检查结果；恢复前先验证再替换。
5. **不干扰主业务**：在线备份不停止采集/推送，不长时间占用唯一写连接。

### 1.2 非目标

- **不做自动 failover / 双活**。SQLite 单写、两台网关会重复轮询 PLC 并重复发布 MQTT，active-active 不可取。
- **不支持热恢复**。采集引擎、推送引擎、订阅管理器持有数据库连接和配置缓存，替换底层文件必须重启。
- **不支持降级恢复**。备份包的 schema 比当前二进制新时直接拒绝，避免“新库跑旧代码”。
- **默认不备份插件二进制和日志**。备机预装同版本发行包即可；插件以清单（版本 + sha256）核对。
- **不把运行中的 SQLite 放到 NAS/SMB 上**。网络文件系统的锁语义不可靠；NAS 只能作为备份落点。

---

## 2. 故障场景与恢复路径

| 场景 | 数据来源 | 恢复方式 | 目标 RTO |
|---|---|---|---|
| 配置误删 / 升级回滚 | 本机最近 `config` 快照 | 停服 → CLI `restore` → 启动 | 3~5 分钟 |
| SQLite 文件损坏 | 本机 `full` 或上一份 `config` | 同上，先保留损坏库为 `.pre-restore-*` | 5 分钟 |
| 整机硬件故障 | 异机最新备份 | 备机拉取 → `restore` → 启动 → 接管 IP/域名 | 10 分钟 |
| 站点迁移 / 克隆一套配置 | `config` 快照 | 克隆模式恢复，重新生成网关 ID | 10 分钟 |
| 平台保存着完整期望态 | 开放接口配置同步 | 空网关启动 → 平台下发配置 | 视平台而定 |

> **开放平台配置同步与备份是主备关系，不是二选一**：前者能重建设备/点位/物模型/推送通道，但恢复不了用户、开放接口密钥、网关身份、报警历史与断网缓存，且要求平台配置没有漂移。

---

## 3. 备份范围

### 3.1 SQLite 表分类

**配置类（`config` 范围必须包含）**

| 表 | 内容 | 关键性 |
|---|---|---|
| `sys_user` | 用户与口令哈希 | 恢复后可直接登录 |
| `iot_gateway` | 网关 ID / 名称 / 固件版本 | 决定平台是否认成同一台网关 |
| `iot_protocol` | 协议行与表单值 | 设备协议参数依赖它 |
| `device` | 设备与协议 JSON | 必须 |
| `device_address` | 点位、阈值、转换 | 必须 |
| `collector_group` | 采集组 | 必须 |
| `thing_model` / `thing_model_property` | 物模型与属性 | 必须 |
| `push_channel` | 推送通道（含明文连接凭据） | 必须 |
| `push_channel_form` | 通道动态表单定义 | 必须 |
| `alarm_webhook` / `alarm_webhook_form` | 报警通知配置 | 必须 |
| `mqtt_subscription` / `mqtt_subscription_device_point` | 外部订阅与点位绑定 | 必须 |
| `trigger_rule` | 触发规则 | 必须 |
| `system_config` | 页面改过的运行开关 | 必须 |
| `open_api_secret` | 开放接口密钥原文 | 必须 |
| `schema_migrations` | 已应用迁移版本 | 恢复时兼容性判断 |

**运行/历史类（`full` 范围才包含）**

| 表 | 内容 | 说明 |
|---|---|---|
| `push_outbox` | 断网缓存待补发批次 | 可能是整库最大的表 |
| `alarm` | 报警与恢复历史 | 平台报表依赖 |
| `control_log` | 控制下发审计 | 合规审计依赖 |
| `trigger_rule_record` | 触发动作历史 | 可丢，但 `full` 保留 |
| `point_threshold_record` | 越限记录 | 可丢，但 `full` 保留 |

### 3.2 外部文件

| 内容 | 说明 |
|---|---|
| 磁盘上的 `default.yaml` | 部署时覆盖的端口、JWT secret、插件目录等 |
| 磁盘上的 `${APP_ENV}.yaml` | `APP_ENV=prod` / `dev` 时生效 |
| `drivers.version` | 官方驱动版本真源 |
| 插件清单 | 每个 `iot-driver-*` 的文件名、describe 版本、sha256 |

嵌入二进制的 `default.yaml`、前端 `web/dist`、i18n、migrations **不需要备份**，它们随二进制走。

### 3.3 明确不备份

- `log/` 运行日志；
- 设备扫描任务与结果（内存态，明确不落库）；
- `push.changeOnly` 的“上次已投递值”内存缓存；
- JWT 登录态（无状态令牌；只要 JWT secret 不变，旧 token 在过期前仍有效）。

### 3.4 环境变量

以下配置可能来自环境变量而非文件，**不会进入备份包**，换机时必须由运维重新注入：

| 环境变量 | 影响 |
|---|---|
| `IOT_GATEWAY_JWT_SECRET` | 不注入则旧登录 token 全部失效（不影响功能） |
| `IOT_GATEWAY_ADMIN_PASSWORD` | 仅首次启动生效；恢复后用户表已有 admin，不再读取 |
| `IOT_GATEWAY_NAME` / `IOT_GATEWAY_VERSION` | 覆盖网关名称/版本 |
| `APP_ENV` | 决定加载哪份环境 yaml |

manifest 只记录“哪些环境变量被设置过”的**名称**，不记录值。

---

## 4. 备份包格式

### 4.1 文件布局

```
20261117-031700_ab12cd34_config.igbak
├── manifest.json
├── db/
│   └── iot-gateway.db          # VACUUM INTO 或白名单拷贝出来的一致性快照
├── config/
│   ├── default.yaml            # 仅当磁盘存在
│   └── prod.yaml               # 仅当 APP_ENV=prod 且文件存在
└── plugins/
    └── manifest.json           # 插件清单，不含二进制
```

命名建议：

```
iot-gateway_<gatewayId 前 8 位>_<yyyyMMdd-HHmmss>_<scope>.igbak
```

### 4.2 `manifest.json`

```json
{
  "formatVersion": 1,
  "scope": "config",
  "createdAt": "2026-11-17T03:17:00+08:00",
  "createdBy": "scheduler | manual | cli | openapi",
  "gateway": {
    "id": "019f...",
    "name": "车间网关-01",
    "version": "v1.4.0"
  },
  "schemaVersion": "036_device_address_transform.sql",
  "env": ["IOT_GATEWAY_JWT_SECRET"],
  "db": {
    "path": "db/iot-gateway.db",
    "sha256": "...",
    "size": 1048576
  },
  "files": [
    { "path": "config/default.yaml", "sha256": "...", "size": 8192 }
  ],
  "plugins": [
    { "file": "iot-driver-modbus", "version": "v1.2.0", "sha256": "..." }
  ],
  "encrypted": false,
  "encryption": null
}
```

约束：

- `schemaVersion` 取 `schema_migrations` 中按文件名排序的最大值，不是数据库 `user_version`。
- `gateway.id` 来自 `iot_gateway` 行；恢复时接管/克隆模式据此决策。
- `plugins[].sha256` 直接复用 [pluginhost/manager.go](../pluginhost/manager.go) 已加载插件的 `SHA256` 与 describe 版本。
- manifest 本身在加密包中随密文一起保护；加密包外层另有明文头（见 §8.3）。

---

## 5. 在线一致性快照

### 5.1 为什么不能直接复制 `iot-gateway.db`

当前库是 WAL 模式（[database.go](../database/sqlite/database.go)）：

- 已提交但尚未 checkpoint 的数据只在 `-wal` 文件里，只复制主库会丢数据；
- 复制过程中 SQLite 可能正在 checkpoint 改写主库，得到**撕裂副本**；
- `-shm` 是内存索引，复制它没有意义；
- 运行中直接 `copy` 三件套也没有跨文件的一致性保证。

### 5.2 `full` 快照：`VACUUM INTO`

```sql
VACUUM INTO '/abs/path/data/backups/tmp/iot-gateway-<ts>.db';
```

实现要点：

1. **使用独立数据库连接**，不能占用 GORM 主连接的 `MaxOpenConns=1`。
   - 新建 `*sql.DB`（或第二个 `*gorm.DB`），沿用 `dsnWithPragmas` 的 DSN；
   - `SetMaxOpenConns(1)`，用完即关；
   - 设置 `busy_timeout` 与单次快照超时（建议 60s）。
2. `VACUUM INTO` 是读事务，WAL 模式下不阻塞采集/推送写入；但会短暂钉住 WAL。
3. 输出文件必须**预先不存在**；先写临时目录，失败即删。
4. 路径含单引号时做 SQL 转义：`strings.ReplaceAll(path, "'", "''")`，再拼进单引号字面量。
5. 快照完成后对快照文件执行 `PRAGMA integrity_check`（或 `quick_check`），必须返回 `ok`。
6. 计算 sha256，写入 manifest。

**不要在运行中的主连接上执行 `VACUUM` / `VACUUM INTO`**：主连接只有一条，长时间占用会让配置写入和 outbox 落盘排队。

### 5.3 `config` 快照：白名单拷贝

`config` 范围不应把可能很大的 `push_outbox` 也读一遍。建议实现：

1. 在临时目录新建空库，用现有的迁移器建出完整 schema
   （把 `runMigrations` 封装成可复用的 `Migrate(db)`）。
2. 打开这个空库作为主连接，`ATTACH DATABASE <源库> AS src`。
3. 在**一个事务**内按 §3.1 的配置表清单执行：

   ```sql
   INSERT INTO main.<table> SELECT * FROM src.<table>;
   ```

   事务的第一个读操作确定快照点，后续所有表看到同一份数据。
4. `DETACH`，关闭源连接；对结果库执行 `integrity_check` 与 `VACUUM` 收缩。
5. 后续 zip/manifest 流程与 `full` 相同。

> 为什么不用“先 `VACUUM INTO` 整库，再 `DELETE` 运行表”：在离线期 `push_outbox` 可能达到数百 MB，配置级备份会被运行数据拖慢。白名单拷贝是配置级备份的正解；P0 可先只做 `full`，P1 再补 `config`。

### 5.4 原子落盘

```
写入 data/backups/tmp/<name>.part  →  fsync  →  rename 为 <name>.igbak
```

- 任何一步失败都只留下 `.part`，由启动清理或下次备份前清理；
- 不要在备份目录里直接生成最终文件再慢慢写，避免列表/上传读到半个包；
- 压缩用标准库 `archive/zip`，流式写入临时文件，避免包大小占内存。

### 5.5 与 WAL 维护器的协作

[database/sqlite/walCheckpoint.go](../database/sqlite/walCheckpoint.go) 的 `runOnce` 会把“被外部读者钉住 WAL”打成 WARN，注释里已经点名 `a backup tool?`。备份期间这是**预期现象**，不应每轮都告警：

- `BackupService` 在快照期间置一个 `maintenance` 原子标记；
- `WALCheckpointer` 读取该标记，维护中跳过 `Checkpointed < Log` 的 WARN（仍可 Debug 记录）；
- 快照结束后清除标记。若快照崩溃，由超时或进程重启自然清除。

### 5.6 资源与边界

| 项 | 建议值 |
|---|---|
| 单次快照超时 | 60s |
| 临时目录 | `<backup.dir>/tmp/` |
| 磁盘空间检查 | 低于 `max(2 × 数据库大小, 64MB)` 时拒绝备份并告警 |
| 快照期间最大 WAL 增长 | 记录到日志；超过阈值（如 64MB）打 WARN |

---

## 6. 触发、保留与异机保存

### 6.1 触发方式

| 方式 | 频率 | 范围 | 说明 |
|---|---|---|---|
| 启动后延迟 | 启动后 5 分钟 | `config` | 给迁移与首轮配置稳定留时间 |
| 定时 | 默认每 6 小时 | `full` | 每天至少一次；可配置 |
| 配置变更 | 变更后 60s 防抖 | `config` | 见 6.2 |
| 手动 | 页面 / CLI / OpenAPI | 可选 | 运维操作 |
| 恢复前 | 恢复动作开始前 | `full` | 强制生成 pre-restore 副本（本地文件，不是包） |

配置变更检测建议：

- 在 GORM 的 Create / Update / Delete 回调里，对 §3.1 的**配置表白名单**打脏标记；
- 不直接用 `PRAGMA data_version` 判断：报警、outbox 写入也会变化，会把备份频率打成每轮采集一次；
- 大批量导入（Excel、平台下发）会在一个事务里产生很多写，防抖窗口合并成一次备份。

### 6.2 保留策略

| 规则 | 默认值 |
|---|---|
| 保留最近 N 份 | 14 |
| 备份目录总大小上限 | 512MB |
| `full` 至少保留 | 最近 4 份 |
| 删除顺序 | 先删最旧的 `config`，再删最旧的 `full`；`.pre-restore-*` 不计入配额但保留最近 2 份 |

保留策略只能删本服务生成的、文件名符合命名规则的包；**不得删除用户手工放入的文件**，避免误删运维资产。

### 6.3 异机保存

本机 `data/backups/` 只解决同机回滚。整机故障必须依赖异机副本，按优先级：

1. **HTTP 上传到管理平台**（推荐）：复用开放接口鉴权，网关主动 `POST`，平台落对象存储。
2. **共享目录 / NAS**：把包原子写到挂载点（先写临时文件再 rename）。NAS 只作为备份落点，**不要**把运行库放上去。
3. **SFTP / WebDAV**：按现场要求再加，避免一上来引入重型 SDK。

统一抽象：

```go
type RemoteTarget interface {
    Put(ctx context.Context, meta BundleMeta, r io.Reader) error
    Latest(ctx context.Context) (BundleMeta, error) // 供备机拉取；可由平台实现
}
```

上传要求：

- 默认只上传 `config`（小、频繁），`full` 按天/周上传；
- 上传失败不影响本地备份成功，但必须记录 `lastUploadedAt` 与连续失败次数；
- 页面对“异地副本超过 N 小时未更新”标红；
- 上传前若配置要求加密，则先用 §8.3 的格式加密。

### 6.4 备机拓扑

```
现场网关 ──本机快照(6h / 变更后 60s)──► data/backups/
        └──HTTP / 共享目录上传(15min)──► 平台存储 / NAS
                                                    │
备机（同版本二进制 + 插件，常停） ──拉取最新包──► restore ──► 启动 ──► 接管 IP/域名
```

约束：

- 备机**不能与现场网关同时运行**：两台会重复轮询 PLC、重复发布 MQTT、在 discovery 中广播同一 ID。
- 备机必须预装**同版本二进制 + 同版本 `plugins/`**；恢复前用 manifest 对账，不一致要显式确认。
- 网络接管要提前演练：静态 IP 重绑、VIP、DNS 或平台侧改地址。
- 现场网关故障后，老机器必须先关机/断网/擦除数据再复用，否则同 ID 双活。

---

## 7. 恢复

### 7.1 三种入口

| 入口 | 适用场景 | 行为 |
|---|---|---|
| CLI `iot-gateway restore --bundle <path>` | 本机 / 备机现场 | 校验 → 停服检查 → 保留旧库 → 替换 → 退出，随后由运维启动服务 |
| 启动前待恢复 | 页面/API 上传后重启 | 上传暂存到 `<data-dir>/restore-pending/`，下次启动在 `InitDB` 之前应用 |
| 页面 / API | 管理端操作 | 只负责“上传 + 校验 + 暂存 + 提示重启”，**不做热恢复** |

CLI 建议子命令（在 `main()` 最前面解析，先于 Windows 服务检测）：

```
iot-gateway backup create [--scope config|full]
iot-gateway backup list
iot-gateway backup verify --bundle <path>
iot-gateway restore --bundle <path> [--identity takeover|clone] [--db <path>]
iot-gateway restore --pending
```

### 7.2 恢复前校验（缺一不可）

1. 包格式 / manifest 可解析；
2. sha256 与 manifest 一致；
3. 临时库 `PRAGMA integrity_check` 返回 `ok`；
4. **schema 兼容**：
   - 包内 `schemaVersion` 比当前二进制内嵌迁移列表更新 → 拒绝（不支持降级）；
   - 包更旧 → 允许，启动时由 [migrator.go](../database/sqlite/migrator.go) 自动补齐；
5. 软件版本不同 → 警告（要求操作者确认）；
6. 插件清单与备机 `plugins/` 不一致 → 警告并列出差异文件；
7. 目标库未被运行中的网关占用（见 7.3）；
8. 磁盘剩余空间 ≥ 2 倍快照大小。

### 7.3 服务锁与替换流程

SQLite 在 Windows 上被打开时 rename 会失败，但在 Linux 上 rename 仍可成功，运行中的进程会继续使用旧 inode，产生“看似恢复成功、实际没生效”的假象。因此必须有**显式进程锁**：

- 启动时在 `<data-dir>/.gateway.lock` 获取 OS 级排他锁（Windows `LockFileEx`，Unix `flock`），进程生命周期内持有；
- `restore` 尝试非阻塞获取同一把锁，拿不到直接拒绝：“网关正在运行，请先停止服务”。

替换顺序：

1. 再次校验包；
2. 把 `iot-gateway.db`、`-wal`、`-shm` 三件套一起改名为
   `iot-gateway.db.pre-restore-<yyyyMMddHHmmss>`（同后缀）；
3. 把快照库放到 `iot-gateway.db`；
4. 确保目录中没有残留的 `-wal` / `-shm`；
5. 保留至少最近 2 份 `.pre-restore-*`；
6. 启动网关，迁移器补齐 schema。

> **只替换主库文件是不够的**：旧 `-wal` 可能被应用到新库上。三件套必须一起处理。

### 7.4 启动前待恢复流程

```
<data-dir>/restore-pending/
├── bundle.igbak
└── restore.json      # { state, scope, identity, checksum, uploadedBy, createdAt }
```

启动顺序（在 `main.runGateway` 中）：

1. 加载 `default.yaml` 与 `APP_ENV` 配置；
2. 初始化日志；
3. **执行待恢复**（此时尚未 `InitializeApp`，数据库未打开）；
4. Wire 构建依赖，`InitDB` 正常打开已替换的库；
5. 迁移器补齐 schema。

失败处理：

- 校验失败：把整个 pending 目录改名为 `restore-pending.failed-<ts>`，记 ERROR，**继续用旧库启动**（避免现场变砖）；
- 替换过程中崩溃：启动时检测到 `restore.json` 的 `state=prepared`，优先从 `.pre-restore-*` 回滚；回滚也失败则拒绝启动并提示人工处理；
- 成功后删除 pending 目录并记审计日志。

### 7.5 网关身份：接管 vs 克隆

`iot_gateway.id` 决定管理平台是否把它当成“原来那台网关”。

| 模式 | 行为 | 使用场景 |
|---|---|---|
| `takeover`（默认） | 保留原 `id` / `name` / `firmware_version` | 故障接管，平台报表与告警归属延续 |
| `clone` | 重新生成 `id`，`name` 加后缀（如 `-copy`） | 复制一套配置到另一个站点 |

注意：

- `iot_gateway.name` 有唯一索引；克隆到同一平台时名称后缀必须唯一；
- 接管模式下，老机器必须下线；网关侧可在 discovery 中检测“同 ID 邻居”并打 ERROR，但**不要硬阻断**（无法可靠区分平台缓存与真实双活）；
- 克隆模式应同时轮换 `open_api_secret` 中不适合共用的密钥，或在恢复后手工处理。

### 7.6 `push_outbox` 的重复风险

`full` 恢复会带回老机器未补发的缓存。若老机器在崩溃前“已发出但未来得及删除 outbox 行”，备机接管后会再发一次：

- 默认 `config` 备份不包含 `push_outbox`，规避该问题；
- 使用 `full` 恢复时，要求下游（MQTT 消费端 / 时序库）具备幂等键或去重能力；
- 该取舍必须写进恢复操作手册，不能默认“恢复就完全等价”。

### 7.7 恢复后自检清单

1. `/health` 正常；
2. 登录成功（用户表已恢复）；
3. 网关名称/ID 符合预期（`/gateway/get`）；
4. 插件管理器已加载预期数量的协议（`/plugin/status`）；
5. 设备数与点位数与备份前一致；
6. 推送通道状态为已连接，断网缓存深度正常；
7. 管理平台已看到网关（discovery 广播的新 IP 或手工更新地址）；
8. 抽查 1~2 台设备实时值与 PLC 一致。

---

## 8. 安全

### 8.1 敏感内容

备份包是整个网关的“钥匙串”，至少包含：

- `push_channel.config_json` 中的 MQTT / Kafka / TDengine 口令；
- `open_api_secret` 的密钥原文；
- `sys_user` 的口令哈希；
- 磁盘 `default.yaml` 中的 JWT secret；
- 设备协议参数（可能含 PLC 访问凭据）。

### 8.2 文件权限

- 备份目录 `0700`，备份文件 `0600`；
- `umask` 不能依赖进程环境，落盘后显式 `chmod`；
- Windows 下至少限制为服务运行账户可读写；
- 备份目录不得放在 Web 静态目录、日志目录或任何会被 `go:embed` / 打包脚本扫到的路径。

### 8.3 可选加密格式

上传异机或跨网络传输时**必须加密**。建议标准库实现，不引入新依赖：

```
[magic "IGBAK" 5B][formatVersion 1B][salt 16B][iterations 4B][nonce 12B][ciphertext...]
```

- KDF：PBKDF2-HMAC-SHA256（Go 1.26 标准库 `crypto/pbkdf2`），迭代次数记录在头里，建议 ≥ 210000；
- 加密：AES-256-GCM，AAD 为上面的明文头；
- 口令只由操作者持有，**不得**与备份包同目录保存，也不得写入 `default.yaml`；
- 解密失败 / GCM 认证失败一律视为包损坏，不得尝试部分恢复。

### 8.4 权限与审计

- 页面备份/恢复接口仅管理员 JWT 可用，恢复操作要求二次口令确认；
- `/openApi` 默认只开放“创建备份 / 查询状态 / 下载备份”，**不开放恢复**；恢复只允许页面（维护开关打开时）或本地 CLI；
- `/agentApi` 按项目现有约定免鉴权，与“上传插件 = 远程代码执行”同一暴露面；**不得**在 `/agentApi` 暴露恢复接口；
- 备份创建、删除、下载、恢复、恢复失败都写审计日志（操作者、来源、包名、校验和、结果）。

---

## 9. 与现有机制的关系

| 现有机制 | 关系 |
|---|---|
| 开放接口配置同步（[openApiConfigService.go](../service/openApiConfigService.go)） | 可重建配置期望态，但不含用户/密钥/历史；作为换机重建的快速路径，与备份互补 |
| WAL 维护器（[walCheckpoint.go](../database/sqlite/walCheckpoint.go)） | 备份期间会钉住 WAL，需要 maintenance 标记抑制误告警 |
| 迁移器（[migrator.go](../database/sqlite/migrator.go)） | 恢复旧包后由它在启动时补齐 schema；`schema_migrations` 是兼容性判断依据 |
| 插件管理器（[pluginhost/manager.go](../pluginhost/manager.go)） | 提供插件版本与 sha256，用于 manifest 与备机对账 |
| 日志下载（[logfileController.go](../controller/logfileController.go)） | 备份下载复用 `FileAttachment`，天然支持 Range |
| 运行开关（[runtimecfg](../../runtimecfg/)） | `system_config` 表随配置备份；恢复后开关状态与备份时一致 |

---

## 10. 配置项

在 [default.yaml](../default.yaml) 增加（启动期配置，暂不纳入 runtimecfg 热开关）：

```yaml
backup:
  enabled: true
  dir: data/backups              # 相对工作目录，与 sqlite.path 同一解析规则
  defaultScope: config           # config | full
  startupDelayMinutes: 5
  intervalHours: 6               # full 备份周期
  onChange: true
  changeDebounceSeconds: 60
  keep: 14
  maxTotalMB: 512
  snapshotTimeoutSeconds: 60
  remote:
    enabled: false
    type: ""                     # http | dir
    dir: ""
    url: ""
    intervalMinutes: 15
    encrypt: true
```

说明：

- 相对路径与 `sqlite.path`、`log.outputDir` 一样按工作目录解析；Windows 服务会在启动时切到可执行文件目录（[service_windows.go](../service_windows.go)）。
- `backup.enabled=false` 时不启动调度与页面入口，但 CLI 仍可手动备份/恢复。
- 远端口令、加密口令**不放在这里**：由页面输入或环境变量一次性提供，不落盘。

---

## 11. API 与前端

### 11.1 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/backup/list` | 包列表：时间、范围、大小、sha256、远端上传状态 |
| POST | `/backup/create` | body `{scope}`，同步生成或返回任务 ID |
| GET | `/backup/download?name=` | 文件流（`FileAttachment`） |
| POST | `/backup/upload` | multipart 上传备份包，只暂存并校验 |
| POST | `/backup/stageRestore` | body `{name, identity}`，写入 `restore-pending/`，返回“需重启” |
| DELETE | `/backup/delete?name=` | 删除本服务生成的包 |
| GET | `/backup/status` | 最近成功时间、下次计划、异地副本新鲜度、错误信息 |
| GET/PUT | `/backup/config` | 读取/修改调度与远端配置（可选，P2） |

与项目现有约定一致：HTTP 200 + 统一响应信封；文件下载接口除外。

### 11.2 前端页面

建议放在“系统设置”下新增 **备份与恢复**：

- 顶部状态卡：最近本机备份、最近异地备份、下次计划、目录占用；
- 列表：时间 / 范围 / 大小 / 校验结果 / 上传状态 / 操作（下载、删除、用于恢复）；
- 操作区：立即备份、上传包、恢复（强提示：需重启、会覆盖当前配置）；
- 恢复确认文案必须写明：**网关 ID 采用接管还是克隆**、**是否会带走断网缓存**、**插件版本是否一致**。

---

## 12. 代码落点与分期

### 12.1 建议包结构

```
backend/backup/
├── bundle.go        # zip、manifest、sha256、命名与列表
├── snapshot.go      # full: VACUUM INTO；config: 白名单拷贝
├── verify.go        # integrity_check、schema 兼容性、插件对账
├── restore.go       # 服务锁、三件套替换、pre-restore、pending 应用
├── scheduler.go     # 定时、变更防抖、保留策略
├── remote.go        # RemoteTarget 抽象 + dir/http 实现
├── crypto.go        # AES-256-GCM + PBKDF2（可选）
└── backup_test.go
```

装配点：

- `main.go`：CLI 子命令解析 → 配置/日志初始化 → `backup.ApplyPending()` → `InitializeApp()`；
- `wire.go`：`NewBackupService`、`NewBackupScheduler` 注入；
- `router/router.go`：挂 `/backup` 路由；
- `WALCheckpointer`：增加 maintenance 标记读取；
- `database/sqlite`：导出 `Migrate(db)` 供 config 快照建空库；
- `pluginhost`：导出已加载插件清单（文件名/版本/sha256）。

### 12.2 分期

| 阶段 | 内容 | 验收 |
|---|---|---|
| **P0** | `.igbak` full 包；`VACUUM INTO`；integrity_check；本机轮转；CLI `backup/restore`；服务锁；pre-restore 三件套 | 运行中备份不丢已提交数据；停服恢复后设备/点位/用户完整 |
| **P1** | `config` 范围白名单快照；变更防抖；页面/API 列表、创建、下载、上传、暂存恢复；审计 | 改一台设备后 60s 内出现新包；客户端上传后重启生效 |
| **P2** | 异机上传（HTTP / 共享目录）；AES-GCM 加密；插件清单对账；备机操作手册；同 ID 检测 | 模拟整机断电后，备机 10 分钟内接管 |
| **P3（可选）** | WAL 增量投递（Litestream 式），把 RPO 压到秒级 | 仅当业务明确不能容忍分钟级丢失时再做 |

---

## 13. 验收与测试

### 13.1 单元/集成测试

1. **一致性**：持续写入（配置 + outbox）时做 `full` 快照，快照库 `integrity_check=ok`，包含快照开始前已提交的配置；
2. **不阻塞**：快照期间采集写入延迟无明显抖动（对比基线）；
3. **配置范围**：`config` 包不含 `push_outbox` / `alarm` / `control_log` 等运行表；
4. **原子性**：模拟 zip 写入失败、磁盘满、进程崩溃，`backups/` 中不出现可被误认的半包；
5. **恢复替换**：旧库带非空 `-wal` / `-shm`，恢复后不得把旧 WAL 应用到新库；
6. **服务锁**：网关运行中执行 `restore` 必须拒绝；
7. **schema 兼容**：伪造更高 `schemaVersion` 的包必须拒绝；旧包恢复后迁移成功；
8. **加密**：错误口令 / 篡改密文必须失败且不产生部分恢复；
9. **pending**：上传后重启自动恢复；校验失败时旧库照常启动并留下 failed 目录；
10. **身份模式**：takeover 保留 ID/名称；clone 生成新 ID、名称带后缀；
11. **插件对账**：清单不一致时返回差异并拒绝自动继续（除非显式确认）；
12. **保留策略**：超过 N 份 / 总大小上限时删除最旧，不删手工文件。

### 13.2 演练

- **同机回滚演练**：改坏配置 → 停服 → 恢复上一份 `config` → 启动 → 自检；
- **整机切换演练**：现场机断电 → 备机拉最新包 → `restore --identity takeover` → 启动 → 接管 IP → 平台确认；
- **带病恢复演练**：备份包被人为截断/改一字节 → 必须拒绝且不影响当前运行库。

---

## 14. 风险与明确不做的做法

| 做法 | 结论 | 原因 |
|---|---|---|
| 运行中直接 `copy iot-gateway.db` | **禁止** | 丢 WAL 帧 / 撕裂副本 |
| 复制 `db + -wal + -shm` 三件套 | **禁止** | 无跨文件一致性保证 |
| 热替换运行中的数据库文件 | **禁止** | 引擎持有连接与缓存；Linux 下还会出现“假成功” |
| 把运行库放到 NAS/SMB | **禁止** | 网络文件系统锁语义不可靠 |
| 备份只留本机 | **不满足灾备** | 整机故障时一起丢失 |
| 不加密就上传公网/邮件 | **禁止** | 包含明文口令与密钥 |
| 自动 failover / 双活 | **不做** | SQLite 单写；两台会重复轮询与发布 |
| 用新版二进制恢复更高 schema 的包 | **拒绝** | 旧代码跑新库，行为不可预期 |

---

## 附录 A：参考代码

- SQLite 初始化与 WAL pragma：[database/sqlite/database.go](../database/sqlite/database.go)
- WAL 定期维护与“外部读者钉住”告警：[database/sqlite/walCheckpoint.go](../database/sqlite/walCheckpoint.go)
- 迁移器：[database/sqlite/migrator.go](../database/sqlite/migrator.go)
- 配置加载与路径解析：[configFile/configFile.go](../configFile/configFile.go)
- Windows 服务工作目录切换：[service_windows.go](../service_windows.go)
- 插件版本与 sha256：[pluginhost/manager.go](../pluginhost/manager.go)
- 开放接口配置同步：[service/openApiConfigService.go](../service/openApiConfigService.go)
- 文件下载模式：[controller/logfileController.go](../controller/logfileController.go)

## 附录 B：待确认事项

1. 异机落点优先做“HTTP 上传管理平台”还是“共享目录”？（影响 P2 的 `RemoteTarget` 首个实现）
2. 备机是否已有固定硬件与预装流程？如果没有，P2 需同时提供备机制备脚本/手册。
3. `config` 备份是否需要包含 `sys_user` 口令哈希？（包含则恢复后可直接登录；不包含则恢复后需重置管理员口令）
4. 是否需要英文版本文档（`data-backup.en.md`）？
