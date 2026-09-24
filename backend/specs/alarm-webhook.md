# 报警 Webhook 通知（notify）

> 报警除落库外，**同时**推送到钉钉 / 企业微信 / 飞书群机器人，以及任意自定义 HTTP 端点。
> 通知是**异步旁路**：绝不阻塞采集线程与报警状态机，投递失败只记日志与计数，不落盘补发。
>
> 报警的检测与判定见 [断联报警设计](alarm.md)；本文只讲**外发通知**。

---

## 1. 总体架构

```
alarm.Tracker ──Notify(Event)──▶ ┌ ingress 队列（非阻塞，满则丢+计数）
  （持锁，只做一次 select）        │
                                  └─ fanout 单协程 ─┬─▶ webhook A 队列 ─▶ worker ─▶ HTTP（退避重试）
                                                    ├─▶ webhook B 队列 ─▶ worker ─▶ HTTP
                                                    └─▶ ...
```

- **解耦方式**：`alarm` 定义 `Event` 与 `NotifySink` 接口，`notify.Dispatcher` 实现它，
  wire 里用 `wire.Bind(new(alarm.NotifySink), new(*notify.Dispatcher))` 接线。
  **`alarm` 不 import `notify`**（与 `collector.DeviceStateSink` ← `alarm.Engine` 同一手法）。
- **为什么不做成 push 通道**：push 通道的语义是「采集数据」（`Enqueue(PushBatch)`），
  报警事件塞不进去；且前端 `web/dist` 里通道类型是**硬编码**的
  （`ChannelSelector-*.js` 的 `["mqtt","tdengine-v3","influxdb-v3"]`），
  新增类型在界面上选不出来。故 webhook 自带独立的表与 API 面。
- **为什么不复用 `push.Outbox`**：outbox/spool 与 `PushBatch`、`push_outbox` 表
  在 6 处硬绑定，无法泛化到报警事件。

## 2. NotifySink 契约（重要）

`Notify` **会在 `Tracker.mu` 持有期间被调用**，因此实现必须遵守：

1. **立即返回，绝不阻塞**：生产实现只做一次 `select`/`default` 入队，
   队列满则丢弃并计数（`ingressDropped`）。绝不做 IO、绝不反压。
2. **绝不回调 Tracker**：否则与 `t.mu` 形成锁环。

违反契约的后果仅是采集协程延迟升高——状态迁移与报警落库在调用 `Notify` **之前**已完成。
`alarm/notifyHook_test.go` 用「灌满队列 + 反复制造边沿」的回归测试守住这条契约。

> 注意：`Dispatcher.Notify` 未启动时直接丢弃并计数，因此**必须早于报警巡检与采集启动**，
> 否则启动窗口内的报警会丢（`main.go` 中的启动顺序即为此）。

## 3. 数据模型

迁移 `016_alarm_webhook.sql`：

```sql
CREATE TABLE IF NOT EXISTS alarm_webhook (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,          -- 展示名
    type        TEXT    NOT NULL,                 -- dingtalk | wecom | feishu | custom
    description TEXT,
    url         TEXT    NOT NULL,                 -- 完整 webhook 地址（本身即凭证）
    secret      TEXT,                             -- 加签密钥；custom 时作为 Bearer Token
    msg_type    TEXT    NOT NULL DEFAULT 'markdown',
    at_all      INTEGER NOT NULL DEFAULT 0,
    at_list     TEXT,                             -- JSON 数组：钉钉=手机号，飞书=open_id
    status      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT DEFAULT (datetime('now','localtime')),
    updated_at  TEXT DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_alarm_webhook_status ON alarm_webhook(status);

CREATE TABLE IF NOT EXISTS alarm_webhook_form (   -- 与 push_channel_form 同构
    id        TEXT PRIMARY KEY,
    name      TEXT NOT NULL UNIQUE,               -- 名称即类型名
    form_json TEXT NOT NULL,
    created_at TEXT, updated_at TEXT
);
```

`alarm_webhook_form` **刻意不复用 `push_channel_form`**：后者按 push 通道类型名做键、
由前端通道选择器读取，塞进 webhook 类型会让 push 通道选择器枚举出它们。
迁移里已预置 4 条表单行（dingtalk / wecom / feishu / custom），前端按 name 拉取后动态渲染。

## 4. 触发范围

**断联与恢复都推**，**设备与推送通道都推**（触发点见 [alarm.md](alarm.md) 第 4 节）。

消息内容由 `notify.Message` 统一渲染，各平台再套自己的报文外壳：

```
【断联报警】设备 dev1
目标类型 / 目标名称 / 报警级别 / 报警内容 / 首次发生
```

恢复通知把最后一行换成「恢复时间」。`报警内容` 与结构化行刻意重复——它就是报警页面上的
原文，便于群消息与库内记录对账。

> 恢复记录的 `first_occur_time` 即恢复时刻（与 `clear_time` 同值），
> 因此恢复通知只展示一行时间，不重复展示同值的「首次发生」。
> 断联时长（离线起点 → 恢复）**暂未提供**，需要额外查一次 active 行，属后续可选项。

## 5. 各平台报文与限制

| 类型 | 加签 | 时间戳单位 | 签名位置 | 报文格式 | @ 支持 |
|------|------|-----------|---------|---------|--------|
| 钉钉 | HMAC-SHA256(key=secret, data=`ts\n+secret`) | 毫秒 | **查询串** `timestamp`/`sign` | markdown（默认）/ text | ✅ 手机号 |
| 企业微信 | 不支持 | — | — | markdown（默认）/ text | ❌ |
| 飞书 | HMAC-SHA256(key=`ts\n+secret`, data=**空**) | 秒 | **body 顶层** `timestamp`/`sign` | text（默认）/ card | ✅ open_id |
| 自定义 | — | — | — | 固定 JSON 信封 | ❌ |

易错点，务必按上表实现：

- **钉钉与飞书的加签算法不同**且时间戳单位不同。飞书的 HMAC **key 是整个
  `timestamp\nsecret`、待签数据为空串**；钉钉则是 `key=secret`、`data=timestamp\nsecret`。
  飞书的 `sign`/`timestamp` 拼在 URL 上**验签不通过**，必须在 body 里。
  两者都有测试用**独立重算**的值做断言，防止实现与测试共用同一份错误逻辑。
- **钉钉的 URL 必须用 `url.Values.Encode()` 拼**：base64 签名里的 `+` 要转成 `%2B`，
  字符串拼接会把 `+` 原样带上去导致验签失败。
- **钉钉 @ 是「双写」**：正文里要有字面量 `@手机号`，`at.atMobiles` 也要带上，缺一不可。
- **企微正文有硬性字节上限**：markdown 4096、text 2048（UTF-8）。
  超限平台会报错，故发送前截断——截断在 markdown 装饰**之后**做、落回 rune 边界、
  预留截断标记长度（`truncateUTF8Bytes`）。
- **三家平台出错时都返回 HTTP 200 + body 里的业务错误码**，只看状态码会把失败当成功。
  必须解包：`errcode`/`errmsg`（钉钉、企微）、`code`/`msg`（飞书 v2）、
  `StatusCode`/`StatusMessage`（飞书旧版）。限流码（钉钉 130101 / 企微 45009 / 飞书 11232）
  归为**可重试**，其余非 0 一律**永久失败**；2xx 但 body 不可解析则按成功处理并记 Warn——
  宁可漏报一次，也不要误重试造成重复推送。
- **钉钉关键字安全模式**：机器人若配置了自定义关键词，消息必须包含该关键词，
  否则返回 `errcode 310000`（永久失败，不重试）。`【断联报警】` 是天然的关键词候选。
- **企微不支持 @**：保存配置时**直接拒绝** `at_all`/`at_list`，而不是静默忽略——
  静默会让值班人员以为已经 @ 到人。

自定义端点的固定信封（字段稳定，作为对外契约）：

```json
{
  "source": "iot-gateway", "event": "alarm",
  "alarmId": "...", "alarmType": "offline", "status": "active",
  "targetType": "device", "targetId": "...", "targetName": "dev1",
  "level": "warning", "content": "设备 dev1 断联",
  "firstOccurTime": "2026-09-24 10:00:00", "lastOccurTime": "...",
  "clearTime": "", "notifyTime": "2026-09-24 10:00:01"
}
```

`secret` 非空时额外发 `Authorization: Bearer <secret>`。自定义端点无统一报文约定，
**2xx 即视为成功**（不解析 body）。

## 6. 投递可靠性

**有界内存队列 + 指数退避重试，不落盘**（报警时效性强，几小时后的补发没有意义）。

| 参数 | 默认值 | 说明 |
|------|--------|------|
| 入口队列 `IngressSize` | 1024 | 满则丢弃并计入 `ingressDropped` |
| 单通道队列 `QueueSize` | 256 | 满则**丢最旧**（让最新报警优先，与 `push.Outbox` 同策略） |
| `MaxAttempts` | 4 | 含首次 |
| `InitialBackoff` → `MaxBackoff` | 1s → ×2 → 30s | |
| `HTTPTimeout` | 5s | |
| `StopDrain` | 5s | 停机排空预算 |
| `WatchInterval` | 10s | 配置校验和巡检 |

- 单条事件最坏耗时 ≈ `1+2+4s` 退避 + `4×5s` 超时 ≈ 27s，之后**丢弃**并计入 `failedCount`
  + ERROR 日志；不落盘、不重投。
- **永久失败不重试**（4xx/配置错/加签错/关键字不匹配）。
- **每次尝试都重新 `Format`**：加签含时间戳，必须在发送时刻生成，不能在构造时固化。
- **停机排空**：每个 worker 对剩余事件各做一次尝试（不重试），到 deadline 放弃并计数。
- **热加载**：配置校验和每 10s 比对（增删改启停都会变），未变更的行**保留原实例**
  （队列与计数不丢）。CRUD 后还会即时触发一次刷新。
  fanout 协程上**不做任何 DB IO**（SQLite 是单连接），配置读取交给巡视协程。

## 7. API

| 接口 | 说明 |
|------|------|
| `POST /alarmWebhook/createAlarmWebhook` | 新建（校验类型/URL/报文格式/@ 组合） |
| `POST /alarmWebhook/updateAlarmWebhook` | 更新（字段可选；用**合并后**的完整配置做校验） |
| `GET  /alarmWebhook/getAlarmWebhookById/:id` | 详情 |
| `GET  /alarmWebhook/deleteAlarmWebhook/:id` | 删除 |
| `GET  /alarmWebhook/pageAlarmWebhook` | 分页（name/type/status 过滤） |
| `POST /alarmWebhook/testSend` | 测试发送（**同步单次，不重试**，回平台原始错误） |
| `GET  /alarmWebhook/status` | 运行状况：入口积压 + 各 webhook 的队列/成功/失败/丢弃/最近错误 |
| `POST /alarmWebhook/refresh` | 手动热刷新 |
| `GET  /alarmWebhook/listTypes` | 支持的类型及元信息（报文格式、是否支持加签/@） |
| `GET  /alarmWebhookForm/getAlarmWebhookFormByName` | 按类型名取表单（另有 create/update） |

约定与仓库其余模块一致：GET 删除、动词前缀路径、HTTP 恒 200 + 业务码。

**`testSend` 的 `id` 语义**：非空则以库中配置为基准、请求字段覆盖之（支持「改完先测再存」）；
为空则完全用请求字段（支持「还没保存先测」）。密钥不回显，故请求未带密钥时沿用库中的。

**校验失败保留具体原因**：`80003` 的 msg 形如
`报警Webhook配置非法: webhook type "wecom" does not support @mention`。
这里刻意用 `NewAppError`（不走 i18n）而非 `NewAppErrorCtx`——后者会用通用文案
替换掉具体原因，而具体原因正是操作者定位问题所需。

## 8. 安全与已知取舍

- **`secret` 永不回显**：VO 恒返回 `""` + `hasSecret: true/false`（更新用 `*string`
  区分「不改」与「清空」）。这是对 `open_api_secret.key` 既有做法的一处**有意偏离**，
  安全上更优：密钥只在服务端用。
- **`url` 本身即凭证**（钉钉 `access_token`、企微 `key`、飞书 hook uuid），
  照 `push_channel.config_json` 保存 MQTT 口令的既有先例**原样返回**以便编辑。
- ⚠️ 请求日志中间件会记录 2KB 请求体，因此**创建/编辑 webhook 时会把 secret 与 url
  写进 `log/app-*.log`**。这与 MQTT 密码的现状一致，属已知限制；
  如需收紧，应对 `/alarmWebhook*` 跳过 body 记录（会影响全局中间件，故未在本次改动）。
  SQLite 库文件与日志文件均含凭证，按敏感文件管理。
- **URL 校验用 `http_url`**（而非 `url`），拒掉 `javascript:` / `file:` 等非 HTTP scheme。
  SSRF 不在防护范围：接口仅管理员可用，目标是操作者自己的群机器人。
- **热加载竞态**：配置变更的窗口内事件可能重复或丢失（无去重），报警场景可接受。
- **未做限速/合并**：机房级联断网时几十台设备可能同时触发，会撞平台
  （钉钉/企微约 20 条/分钟、飞书约 100 条/分钟）。退避重试能吸收一部分，
  但 per-webhook 限速或窗口合并是后续可选项。
- **通道恢复的一个既有语义**：通道由停止转运行时 `Tracker.Reset` + `Report(connected=true)`
  不会写恢复行，因此**已有 active 通道报警不会产生恢复通知**。通知跟随「行是否写入」，
  行为自洽，但值得知情。

## 9. 代码结构

```
notify/dispatcher.go        Dispatcher：生命周期 / 配置加载 / checksum 热加载 / 入口队列 / 扇出
notify/worker.go            单 webhook 运行时：有界队列 + worker + 指数退避重试 + 计数
notify/sender.go            专用 http.Client、HTTP 状态分类、平台响应体解码
notify/formatter.go         Formatter 接口 + 工厂表 + ValidateConfig + Message 渲染 + BuildTestEvent
notify/sign.go              加签纯函数（钉钉 / 飞书两套算法）
notify/{dingtalk,wecom,feishu,custom}Formatter.go
notify/status.go            运行健康快照 VO
alarm/event.go              Event / NotifySink / notifyLocked
service/alarmWebhookService.go             CRUD + 校验闸门 + testSend + status
service/alarmWebhookFormService.go         表单 CRUD
controller/ + router/                       /alarmWebhook*、/alarmWebhookForm*
database/sqlite/migrations/016_alarm_webhook.sql
```

**为什么类型注册用包内 map 而非 `push/<type>/` 子包**：push 通道是重运行时
（连接、outbox、补发、300~500 行）；webhook 格式化器只是 40~80 行纯 JSON 构造，
无连接、无后台协程、无 token 刷新。四个子包会带来 4 个 `init()` + 4 个空导入，
还把类型集合变成编译期不可枚举——而 service 层恰恰需要枚举它来校验 `type`。

## 10. 测试

- `alarm/notifyHook_test.go`：边沿才通知、`touch`/`ClearActive` 不通知、nil sink 安全，
  以及**契约回归**（灌满的非阻塞 sink + 反复边沿，必须在 10s 内跑完——
  谁把 sink 改成阻塞就会红）。
- `notify/*_test.go`：四平台报文与加签（测试内**独立重算** HMAC）、企微截断
  （字节数 + `utf8.ValidString` + 标记）、响应体解码分类、连接失败/5xx/4xx 分类、
  重试成功/永久失败不重试/重试耗尽三条路径的**精确请求次数**、
  队列打满时 `Notify` 不阻塞、热加载保留未变更实例（**指针同一性**）、
  停机排空、Stop 后可重启。
- `database/sqlite/migrator_test.go`：跑**完整迁移链**（`;` 误入字符串会在此炸），
  校验预置表单 JSON 合法、字段齐全、url 必填。
