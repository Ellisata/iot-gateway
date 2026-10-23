# MQTT 订阅报文解析器规范

> 状态：v1（builtin + script）。进程外解析插件（`plugin:<name>`）已预留契约，尚未实现。
> 代码入口：[parser/](../parser/) · 订阅管线：[subscribe/](../subscribe/)

---

## 1. 目标与边界

外部 MQTT 报文结构不可预知，声明式 DSL 无法覆盖全部形态。解析器层把
「报文 → `collector.CollectedRecord`」从订阅连接中独立出来，提供可替换的实现，
同时对下游保持唯一契约。

**不可让步的边界：**

- 输出固定为 `collector.CollectedRecord` 白名单，**一条记录一个 value**。
  不允许自定义 measurement / tags / fields —— 时序通道、断网缓存、设备点位索引
  全部依赖这个结构。
- 解析器只回答「怎么解析」，不负责连接、topic 订阅、投递与落库。
- 解析器实例与订阅一一对应，由单个 worker 顺序调用；允许持有跨报文状态，
  **不要求并发安全**。
- 身份字段（`deviceId` / `deviceAddressId`）与 `value` 必填；其余字段可选。

---

## 2. 契约（[parser/parser.go](../parser/parser.go)）

```go
type Message struct {
    Topic      string
    Payload    []byte
    ReceivedAt time.Time
    QoS        byte
    Retained   bool
}

type Result struct {
    Records []collector.CollectedRecord
    Errors  []MessageError // 单条报文失败不整批丢弃
    Skipped int            // 报文内被跳过的记录数
}

type Parser interface {
    Parse(ctx context.Context, batch []Message) (Result, error)
    Close() error
}

type Factory interface {
    Validate(cfg json.RawMessage) error // 保存前
    New(cfg json.RawMessage) (Parser, error)
}
```

注册与构造：`parser.Register(type, factory)` / `parser.Resolve(type)` /
`parser.Validate(cfg)` / `parser.New(cfg)`。类型为空按 `builtin`。

**批量语义**：`Parse` 接收一批报文，批内保持入参顺序。当前 builtin/script
逐个处理；将来的进程外插件必须按批 RPC，禁止每报文一次 RPC。

**失败语义**：`Parse` 返回 `error` 只表示整批失败（如插件进程不可用、上下文取消）；
单条报文失败写进 `Result.Errors`，其余报文继续。返回的 `Records` 顺序即落库顺序。

**限制**（[parser/parser.go](../parser/parser.go)）：

| 限制 | 默认 | 说明 |
|---|---|---|
| `DefaultMaxPayloadBytes` | 1 MiB | 超过的报文在入队/解析前丢弃并计数 |
| `DefaultMaxBatchMessages` | 256 | worker 单批上限 |
| `DefaultMaxRecordsPerMessage` | 200 | 单报文产出上限 |
| `HardMaxRecordsPerMessage` | 1000 | 配置可调上限的硬顶 |
| `MaxValueBytes` | 4096 | 单个 value 长度上限 |
| `DefaultTimeout` | 30s（管线） | 单批兜底超时；脚本另有逐报文超时 |

---

## 3. 配置与存储

`mqtt_subscription` 新增列（迁移 `019_mqtt_subscription_parser.sql`）：

| 列 | 说明 |
|---|---|
| `parser_type` | `builtin`（默认）/ `script` / 将来的 `plugin:<name>` |
| `parser_config` | 非 builtin 的解析配置（JSON） |
| `sample_topic` | 样例主题，仅供预览 `topicPattern`，运行期不参与解析 |

**同一类型只有一处配置真源：**

- `parser_type = 'builtin'` → 读 `mapping_json`（历史列，零迁移）
- 其它类型 → 读 `parser_config`

不要把 builtin 配置复制到 `parser_config`；两份配置会漂移成
「界面看到的是一套、运行的是另一套」。

API 字段：`parserType` / `parserConfig` / `mappingJson` / `sampleTopic`。
`builtin` 仍用 `mappingJson`；`script` 用 `parserConfig`。

---

## 4. builtin：声明式映射

实现在 [parser/mapping/](../parser/mapping/)。完整 DSL 见包注释，本节只列
解析器扩展：

### 4.1 主题取值

```json
{
  "topicPattern": "factory/{deviceId}/telemetry/{tag}",
  "fields": {
    "deviceId":        {"source": "topic", "value": "deviceId"},
    "deviceAddressId": {"source": "topic", "value": "tag"},
    "value":           {"source": "path",  "value": "v"}
  }
}
```

- `{name}` 匹配一个 topic 段；整串必须完整匹配（不支持通配符）。
- `source: "topic"` 从命名段取值；模板 `{deviceId}` 先查报文，取不到再查命名段。
- 主题不匹配/为空是报文级错误，该报文不产出记录。

### 4.2 多测点扇出

一条报文多个指标时，每个扇出项在基础字段之上覆盖并产出一条记录：

```json
{
  "fields": {
    "deviceId": {"source": "path", "value": "id"},
    "kind":     {"source": "const", "value": "float"}
  },
  "fanout": [
    {"deviceAddressId": {"source": "const", "value": "temp"},
     "value":           {"source": "path",  "value": "data.temp"}},
    {"deviceAddressId": {"source": "const", "value": "hum"},
     "value":           {"source": "path",  "value": "data.hum"}}
  ]
}
```

- 覆盖层路径仍相对同一篇报文；未配置 `fanout` 时保持单记录行为。
- 每个扇出项的合并结果都必须满足必填字段；单项失败只跳过该项。
- 同一批内的 `deviceAddressId` 必须互不相同（时序通道按 `(deviceId, addressId)` 去重）。

### 4.3 值变换

`ValueSource.transform` 是按顺序执行的白名单算子：

| op | 参数 | 说明 |
|---|---|---|
| `scale` | `factor`（默认 1）、`offset`（默认 0） | 数值缩放 |
| `round` | `digits`（0–12） | 保留小数位 |
| `trim` | — | 去首尾空白 |
| `default` | `value` | 空值回落 |
| `hexDecode` | — | 十六进制文本 → 字节串 |
| `base64Decode` | — | Base64 → 字节串 |
| `bitfield` | `offset`（0–63）、`length`（1–64） | 取整数位域 |
| `regex` | `pattern`、`group`（默认 0） | 正则提取 |

未知算子、参数越界、正则非法在保存时拒绝。变换失败只跳过该记录。

---

## 5. script：goja 脚本

实现在 [parser/script/](../parser/script/)。作者指南见
[docs/parser-scripts.md](../docs/parser-scripts.md)。

配置：

```json
{
  "script": "function parse(msg, ctx) { ... }",
  "timeoutMs": 50,
  "maxRecordsPerMessage": 200
}
```

**运行约束：**

- 每个订阅一个 `goja.Runtime`，仅在该订阅的解析 worker 中调用；**非并发安全**。
- 每条报文独立超时（1–500ms，默认 50ms）；超时后重建 VM 并清空 `ctx.state`。
- 未捕获异常只失败该条报文，批内后续报文继续。
- 只注入 `msg` / `ctx` 两个普通对象；不提供 `require`、文件、网络、Go 对象。
- 产出字段走 `parser.RecordFromMap` 白名单；未知字段直接抛错。
- `ctx.get(path)` 返回字符串，`json.Number` 不经过 `float64`，大整数不丢精度。

**脚本不是安全边界。** 它挡住逻辑错误与失控循环，挡不住恶意内存分配；
若将来需要不可信代码的硬隔离，走进程外插件（第 7 节）或 WASM。

---

## 6. 管线与热加载（[subscribe/](../subscribe/)）

```
paho onMessage → 有界队列（8192） → 解析 worker（批 ≤256）
    → Parser.Parse → push.Engine.PushRecordsToTimeSeries
                   → devicePointStore.Record
```

- **回调只入队**，不做解析与 I/O。队列满时丢最旧、计 `dropped`，绝不阻塞收包。
- `topic` 列可用英文逗号分隔多个过滤器（逐段 trim、丢空段、去重保序），
  建连后一次 `SubscribeMultiple` 全部订阅，共用该行的 QoS 与解析器；
  `topicPattern` 匹配的是实际到达的 `msg.Topic()`。
- `SetOrderMatters(true)`：脚本可持有跨报文状态，必须保证到达顺序。
- `Stop()`：断开 → 关队列 → 等 worker 排空 → 关闭解析器 → 停止点位落库器。

**签名拆分：**

- `connSig`：broker/port/clientId/账号/规范化后的 topic 过滤器列表/qos。变化才重建 paho 客户端。
- `parserSig`：解析类型 + 配置原文。变化只**原地替换解析器**，不断开 MQTT。
- 未变化的订阅不构造解析器（脚本编译不便宜），直接沿用运行实例。

**状态计数**：`messages`（收到）、`records`（投递）、`errors`（失败报文）、
`skipped`（报文内跳过）、`dropped`（队列丢弃）。

---

## 7. 进程外解析插件（预留，未实现）

只有在出现二进制/厂商 SDK/必须崩溃隔离的需求时才实现。路径复用现有插件体系：

- 套件 `parsersdk/`、源码 `parsersrc/`、产物 `iot-parser-<name>`；
- 宿主适配器 `pluginhost/adapter_parser.go`，**不复制** `process.go` / `upgrade.go`；
- 线协议复用 [pluginwire/](../pluginwire/)，新增 `parse(batch)` 方法；
- `Meta` 声明 `APIVersion`、解析器名、配置 JSON Schema、输出契约版本；
- 必须按批 RPC；单批结果带逐条错误；
- 插件崩溃/重启不得断开订阅：队列保留，标记错误，恢复后继续；
- 宿主当前只扫描 `iot-driver-*`，接入时需按前缀分流到不同适配器。

契约与类型名（`plugin:<name>`）先占位，避免届时改配置格式。

---

## 8. 验收与回归

- 契约：未知类型、空配置、字段白名单、批内顺序。
- builtin：topic 模板命中/不命中、扇出部分失败、每个 transform、未知算子拒绝。
- script：基本产出、跨报文状态、异常继续、**死循环超时后恢复**、大整数精度、
  未知字段拒绝、产出上限、VM 重建。
- 管线：队列丢最旧、超时/错误计数、parser 热替换不重连、停机排空。
- 服务层：script CRUD、状态更新保留配置、预览与运行期同一路径。
- 迁移：历史行默认 `builtin`，script 行独立读写 `parser_config`。
