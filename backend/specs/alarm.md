# 报警设计（alarm）

> 本规范定义三类报警目标的检测、判定与落库方案：
> **设备**断联、**推送通道**断联、**采集点位**质量异常。
> 范围：**落库**（SQLite `alarm` 表 + 查询 API）+ **通知**（Webhook 群机器人）；
> 信号源**复用已有信号**（设备=采集轮询成败；推送通道=连接状态巡检；
> 点位=每轮采集到的质量与成因），不引入独立心跳 Ping。

---

## 1. 总体架构

```
collector(设备信号源)        alarm.Tracker(判定)              alarm 表 / API
  采集轮询成败 ─────────┐  per-target 状态机 + 去抖  ──▶  SQLite alarm（active/历史）
                         │        target_type=device
collector(点位信号源)    │                          + 查询接口（离线列表 / 历史分页）
  每点位的质量与成因 ────┤        target_type=point
                         │
push.Engine(通道信号源)  │
  连接状态巡检 ─────────┘        target_type=channel
                                                       ──▶ notify.Dispatcher（Webhook）
```

- **统一落库**：`alarm` 表泛化为 target（`target_id` / `target_name` / `target_type`），
  三类目标共用，构成统一"报警中心"。
- **设备信号源**：采集引擎每轮 `doPollDevice` 成功/失败分支，经
  `collector.DeviceStateSink.ReportDevicePoll` 上报（见 collector 层）。
- **通道信号源**：`alarm.ChannelMonitor` 后台协程按 10s 调 `push.Engine.GetStatus()`，
  把每通道的 `Running && Connected` 作为在线信号。三种通道类型
  （mqtt/influxdb/tdengine）都已维护 `connected` 并经 `Snapshot()` 暴露，巡检统一、零侵入。
- **点位信号源**：采集引擎把每轮**成功读到**的每个点位经
  `collector.PointStateSink.ReportPointQuality` 上报（quality 原样转发，成因已并进这个整数）。见 §7。
- **判定**：共享 `alarm.Tracker` 状态机，只在正常↔异常的边沿落库。三类目标的差异
  只有落库类型与文案（`alarm.Spec`：`DeviceSpec` / `ChannelSpec` / `PointSpec`），
  状态机本身一份。

## 2. 数据模型

迁移 `007_alarm.sql`：

```sql
CREATE TABLE IF NOT EXISTS alarm (
    id               TEXT PRIMARY KEY,
    target_id        TEXT NOT NULL,             -- device.id / push_channel.id / device_address.id
    target_name      TEXT NOT NULL,             -- 名称快照（删除后仍可追溯）
    target_type      TEXT NOT NULL DEFAULT 'device',  -- device | channel | point
    alarm_type       TEXT NOT NULL,             -- offline | quality | recover
    device_id        TEXT NOT NULL DEFAULT '',  -- 仅 point 行：所属设备（见下）
    level            TEXT NOT NULL DEFAULT 'warning',
    content          TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active',  -- active | cleared
    first_occur_time TEXT NOT NULL,
    last_occur_time  TEXT NOT NULL,
    clear_time       TEXT,
    created_at       TEXT DEFAULT (datetime('now', 'localtime')),
    updated_at       TEXT DEFAULT (datetime('now', 'localtime'))
);
CREATE INDEX IF NOT EXISTS idx_alarm_target ON alarm(target_id, target_type, status);
CREATE INDEX IF NOT EXISTS idx_alarm_device ON alarm(device_id, target_type, status);
CREATE INDEX IF NOT EXISTS idx_alarm_time   ON alarm(first_occur_time);
```

语义约定：

| 列 | 约定 |
|----|------|
| `target_type` | `device`（设备）/ `channel`（推送通道）/ `point`（采集点位） |
| `alarm_type='offline'` + `status='active'` | 该目标**当前离线**，同一目标同时最多一条 |
| `alarm_type='quality'` + `status='active'` | 该**点位质量异常**，同一目标同时最多一条；仅 `target_type='point'` |
| `alarm_type='recover'` | 恢复历史，`status` 恒为 `cleared`，三类目标共用 |
| `device_id` | **仅 `target_type='point'` 的行填写**；点位报警的 `target_id` 是点位自身，看不回设备，而"删除/停用设备时连清其点位的活跃报警"必须在设备行（乃至点位行）已被删掉之后仍能反查。设备/通道行的 `target_id` 就是自己，恒为空串 |
| `first_occur_time` / `last_occur_time` | 首次失败 / 最近一次失败时间 |
| `clear_time` | 恢复时间（仅 cleared 记录有值） |

## 3. 状态机与去抖

```
ONLINE  ──连续失败 ≥ N──▶  OFFLINE   （落 active 离线报警，仅一次）
OFFLINE ──任意一次成功──▶  ONLINE    （清 active + 写 recover，仅一次）
```

- **离线判定**：连续失败 `N` 次。设备默认 3，推送通道 2（约 20s），点位默认 3。
- **恢复判定**：任意一次成功即恢复——现场恢复是硬事实，无需去抖。
- **离线期间持续失败**：只刷新 active 行 `last_occur_time`，节流 ≥5s。
- **启动/热刷新**：状态机重置，`failCount` 从首轮重新累计，不误报。
  重启后**首个成功信号**还会顺带清掉库中残留的 active 行（`Tracker.report` 的
  `reconciled` 分支）：故障若恰好在重启窗口内结束，那次恢复边沿已经丢失，
  新状态机以 `normal=true` 起步走不到 recover 分支，不核对该行就永远停在 active，
  目标被 `AlarmService.IsDeviceOnline` 与设备列表永久判为离线。
  核对**只清行、不写 recover 行、不通知** —— 真实恢复时刻不可知（可能发生在网关
  停机期间），伪造一条"现在恢复"的事件会污染历史与值班判断（理由同 `ClearActive`）。
  设备若其实仍在故障，首个信号是失败，既有 active 行只刷新 `last_occur_time`，
  故障段的 `first_occur_time` 与时长原样保留、不被截断。
  回归见 `alarm/tracker_test.go` 的 `TestReconcile*`。
- **推送通道专属**：只对 `Running` 的通道判定；通道由停止转运行（重启/热加载）
  重置其状态机，吸收连接建立窗口；由运行转停止（运营停用）清其 active 报警。
- **文案参数化**：三类目标的差异收在 `alarm.Spec`（`TargetType` / `AlarmType` /
  `Label` / `RaiseVerb` / `RecoverVerb`）。`PointSpec` 多一项：报警文案带失败原因
  （`ReportQuality` 的 `detail`），如「点位 1#炉.温度 采集质量异常（地址无效）」——
  断联报警没有比"连不上"更细的原因，恒不带。`notify.BuildTestEvent` 的「测试发送」
  预览也读同一份 Spec，两边说法不会分叉。

## 3.1 点位质量判定的子码取舍

点位这一路**只对能定性的子码计入失败**，其余一律是"中性"——既不报成功也不报失败
（不调用状态机，状态机原地保持）。这不是保守，是因为另外三类信号在语义上都不能
归咎于点位本身（判定代码见 `alarm/point.go`）：

| quality | 处理 | 理由 |
|------|------|------|
| `QualityBadAddress` (1) | **计入失败** | 地址不存在，驱动已单独确认（对半降级到单点仍被拒）。永不自我恢复的配置错误 |
| `QualityBadParse` (3) | **计入失败** | 设备答了但本地解码失败（类型不符、字节数不足）。同样永不自我恢复 |
| `QualityBadComms` (2) | 中性 | 整段被设备以可恢复状态拒绝。按定义是瞬时的，且**成组出现**（整段被拒时该段每个点位一起拿到它）——计入失败会让一次区间拒绝同时点亮上百个点位报警 |
| `QualityBad` (0) | 中性 | 认不出来的设备状态码（不猜），以及**预算耗尽被延迟**的点位。后者每轮都在同一处被延迟，计入失败就是恒定误报 |
| `QualityUncertain` (128) | 中性 | ABB 的"不确定"（信号被勾了仿真，值可用但标注质量），不是故障 |

采集侧**不做这个取舍**：`collector.PointStateSink` 原样转发 quality，
"哪些算故障"是报警策略。采集侧若替它过滤，会静默破坏上面这套中性语义。

**"与设备离线去重"是免费得到的**：设备读失败时 `drv.Read` 直接返 error、
一条记录都不产生，点位压根不会被上报，状态机原地冻结——既不新报也不误恢复。
不需要任何额外代码。

另有两处必须一起做，否则活跃报警会永久挂在"未恢复"上（采集停了，再没有信号来恢复它们）：

- 设备停用/删除 → `AlarmService.ClearDeviceAlarm` 一条语句清**设备自身的断联报警 +
  该设备名下全部点位的质量报警**（靠 `device_id` 反查）；
- 点位删除 → `ClearPointAlarm`（`DeviceAddressService` 单条与批量删除都调）。

## 4. 代码结构

```
collector/sink.go         DeviceStateSink / PointStateSink 接口（与 RecordSink 并列）
collector/task.go         设备采集成败上报 ReportDevicePoll
                          点位质量上报 reportPointQuality → ReportPointQuality
alarm/tracker.go          Tracker：共享状态机 + 落库；Spec 描述三类目标的类型与文案
alarm/engine.go           Engine：设备信号薄封装（实现 DeviceStateSink）
alarm/point.go            PointEngine：点位信号薄封装（实现 PointStateSink）+ 成因取舍
alarm/channel.go          ChannelMonitor：通道连接状态巡检
service/alarmService.go   查询/清理（活跃报警、历史分页、设备/点位停用删除清理）
controller/alarmController.go / router/alarmRouter.go    /alarm/pageAlarm、/alarm/listActive
```

Wire 接线（都在同一次 `wire.Build`，见 wire.go 的注释）：
- `alarm.NewEngine` 绑定为 `collector.DeviceStateSink`；
- `alarm.NewPointEngine` 绑定为 `collector.PointStateSink`；
- `alarm.NewChannelMonitor(db, push.Engine)` 绑定 `alarm.StatusSource`（`push.Engine.GetStatus` 结构满足），
  实例挂到 `AppDependencies.AlarmMonitor`，由 `main.go` 随推送引擎启停。

## 5. 查询 API

| 接口 | 说明 |
|------|------|
| `GET /alarm/pageAlarm` | 历史报警分页（targetName / targetType / alarmType / status 过滤） |
| `GET /alarm/listActive` | 当前活跃**断联**报警（= 离线设备 + 离线通道列表） |

- 设备列表 `DeviceVO.online` 由 `target_type='device'` 的 active 报警反查得出。
- `listActive` **只取 `alarm_type='offline'`**，故点位质量报警不会混进"离线目标列表"，
  `DeviceVO.online` 也不受点位报警影响（它还额外按 `target_type='device'` 过滤）。
  点位质量报警在报警页上按 `targetType=point` + `status=active` 查。
- 前端首页 `alarm/device`、`alarm/channel`、`alarm/point` 三个路由复用同一个
  `views/alarm/IndexView.vue`，靠路由 props 的 `targetType` 区分。

## 6. 边界

- 设备停用（`status=0`）或删除时，清除其 active 报警 **以及其名下全部点位的质量报警**
  （历史保留）；点位单独删除时清它自己那一条。
- 协议不支持（`drv==nil`）的设备、未运行（`Running=false`）的通道不参与判定。
- 点位状态机的内存态（`Tracker.states`）**不随目标删除而回收**，与设备/通道现状一致；
  空转的条目每条仅数十字节，可接受。（删掉的目标不会再上报，只是留着占位。）
- 报警为系统生成记录，不做用户确认（ack）——仅展示与历史追溯。
