# 断联报警设计（alarm）

> 本规范定义**设备**与**推送通道**断联（离线/恢复）报警的检测、判定与落库方案。
> 范围：检测、判定与**落库**（SQLite `alarm` 表 + 查询 API）；
> 信号源**复用已有信号**（设备=采集轮询成败；推送通道=连接状态巡检），不引入独立心跳 Ping。
>
> 报警的**外发通知**（钉钉 / 企业微信 / 飞书 / 自定义 Webhook）见
> [报警 Webhook 通知](alarm-webhook.md)。本包只负责在状态边沿投递一个事件，
> 投递与重试由 `notify` 包负责，两者通过 `alarm.NotifySink` 解耦。

---

## 1. 总体架构

```
collector(设备信号源)        alarm.Tracker(判定)              alarm 表 / API
  采集轮询成败 ─────────┐  per-target 状态机 + 去抖  ──▶  SQLite alarm（active/历史）
                         │        target_type=device
push.Engine(通道信号源)  │                          + 查询接口（离线列表 / 历史分页）
  连接状态巡检 ─────────┘
```

- **统一落库**：`alarm` 表泛化为 target（`target_id` / `target_name` / `target_type`），
  设备与推送通道共用，构成统一"报警中心"。
- **设备信号源**：采集引擎每轮 `doPollDevice` 成功/失败分支，经
  `collector.DeviceStateSink.ReportDevicePoll` 上报（见 collector 层）。
- **通道信号源**：`alarm.ChannelMonitor` 后台协程按 10s 调 `push.Engine.GetStatus()`，
  把每通道的 `Running && Connected` 作为在线信号。三种通道类型
  （mqtt/influxdb/tdengine）都已维护 `connected` 并经 `Snapshot()` 暴露，巡检统一、零侵入。
- **判定**：共享 `alarm.Tracker` 状态机，只在 ONLINE↔OFFLINE 边沿落库。

## 2. 数据模型

迁移 `007_alarm.sql`：

```sql
CREATE TABLE IF NOT EXISTS alarm (
    id               TEXT PRIMARY KEY,
    target_id        TEXT NOT NULL,             -- device.id 或 push_channel.id
    target_name      TEXT NOT NULL,             -- 名称快照（删除后仍可追溯）
    target_type      TEXT NOT NULL DEFAULT 'device',  -- device | channel
    alarm_type       TEXT NOT NULL,             -- offline | recover
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
CREATE INDEX IF NOT EXISTS idx_alarm_time   ON alarm(first_occur_time);
```

语义约定：

| 列 | 约定 |
|----|------|
| `target_type` | `device`（设备）/ `channel`（推送通道） |
| `alarm_type='offline'` + `status='active'` | 该目标**当前离线**，同一目标同时最多一条 |
| `alarm_type='recover'` | 恢复历史，`status` 恒为 `cleared` |
| `first_occur_time` / `last_occur_time` | 首次失败 / 最近一次失败时间 |
| `clear_time` | 恢复时间（仅 cleared 记录有值） |

## 3. 状态机与去抖

```
ONLINE  ──连续失败 ≥ N──▶  OFFLINE   （落 active 离线报警，仅一次）
OFFLINE ──任意一次成功──▶  ONLINE    （清 active + 写 recover，仅一次）
```

- **离线判定**：连续失败 `N` 次。设备默认 3，推送通道 2（约 20s）。
- **恢复判定**：任意一次成功即恢复——现场恢复是硬事实，无需去抖。
- **离线期间持续失败**：只刷新 active 行 `last_occur_time`，节流 ≥5s。
- **启动/热刷新**：状态机重置，`failCount` 从首轮重新累计，不误报。
- **推送通道专属**：只对 `Running` 的通道判定；通道由停止转运行（重启/热加载）
  重置其状态机，吸收连接建立窗口；由运行转停止（运营停用）清其 active 报警。

## 4. 代码结构

```
collector/sink.go         DeviceStateSink 接口（与 RecordSink 并列）
collector/task.go         设备采集成败上报 ReportDevicePoll
alarm/tracker.go          Tracker：共享状态机 + 落库（按 targetType 参数化）
alarm/engine.go           Engine：设备信号薄封装（实现 DeviceStateSink）
alarm/channel.go          ChannelMonitor：通道连接状态巡检
alarm/event.go            Event / NotifySink：报警外发通知出口（sink 为 nil 时只落库）
service/alarmService.go   查询/清理（活跃报警、历史分页、目标停用清理）
controller/alarmController.go / router/alarmRouter.go    /alarm/pageAlarm、/alarm/listActive
```

**通知出口的触发点**（`alarm/event.go` 的 `notifyLocked`）：只在**状态边沿**、
且**落库成功之后**投递，以维持「通知 ⇔ 报警行已写入」这一不变式：

| 时机 | 是否通知 | 原因 |
|------|----------|------|
| `raiseOfflineLocked` 新建 active 离线行 | ✅ | 状态边沿 |
| `recoverLocked` 写 recover 行 | ✅ | 状态边沿 |
| `touchLastOccurLocked` 刷新时间戳 | ❌ | 非边沿，按节流周期发会刷屏 |
| `ClearActive` 清除（目标停用/删除） | ❌ | 行政取消，不是「恢复通信」 |
| `db.Create` 失败 | ❌ | 没写进库，无可通知的事实 |

Wire 接线：
- `alarm.NewEngine(db, sink)` 绑定为 `collector.DeviceStateSink`；
- `alarm.NewChannelMonitor(db, push.Engine, sink)` 绑定 `alarm.StatusSource`
  （`push.Engine.GetStatus` 结构满足），实例挂到 `AppDependencies.AlarmMonitor`；
- `notify.NewDispatcher(db)` 经 `wire.Bind` 绑定为 `alarm.NotifySink`，
  同时挂到 `AppDependencies.AlarmNotifier`。**`alarm` 不 import `notify`**，
  接口由消费方（`alarm`）定义（与 `collector.DeviceStateSink` 同一手法）。
- 启停顺序：通知器在推送引擎之后、报警巡检与采集之前启动；`defer` 最先注册故**最后停止**，
  让停机前产生的报警仍有机会在途排空。

## 5. 查询 API

| 接口 | 说明 |
|------|------|
| `GET /alarm/pageAlarm` | 历史报警分页（targetName / targetType / alarmType / status 过滤） |
| `GET /alarm/listActive` | 当前活跃离线报警（= 离线设备 + 离线通道列表） |

设备列表 `DeviceVO.online` 由 `target_type='device'` 的 active 报警反查得出。

## 6. 边界

- 设备/通道停用（`status=0`）或删除时，清除其 active 报警（历史保留）。
- 协议不支持（`drv==nil`）的设备、未运行（`Running=false`）的通道不参与判定。
- 报警为系统生成记录，不做用户确认（ack）——仅展示与历史追溯。
