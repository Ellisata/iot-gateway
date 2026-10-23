# MQTT 订阅解析脚本指南

> 适用：订阅的解析方式选择「脚本」（`parser_type = script`，goja 运行）。
> 契约与限制见 [specs/parser-plugins.md](../specs/parser-plugins.md)。

内置映射能表达常规 JSON 时优先用内置映射；脚本用于声明式表达不了的结构：
二进制外的特殊编码、跨报文状态、条件产出、复杂派生值等。

---

## 1. 最小脚本

脚本必须定义 `function parse(msg, ctx)`：

```js
function parse(msg, ctx) {
  var d = ctx.json()
  ctx.emit({
    deviceId: d.deviceId,
    deviceName: d.deviceName,
    deviceAddressId: d.tagId,
    deviceAddressLabel: d.tagLabel,
    value: d.value,
    kind: d.kind,
    quality: 192
  })
}
```

每条报文调用一次；一次调用可以 `emit` 多条记录。

---

## 2. 输入

| 属性 | 类型 | 说明 |
|---|---|---|
| `msg.topic` | string | 实际收到的主题 |
| `msg.payload` | string | 报文原文（二进制会按字节转字符串） |
| `msg.receivedAt` | number | 到达时间，Unix 毫秒 |
| `msg.receivedAtText` | string | `YYYY-MM-DD HH:MM:SS.SSS` |
| `msg.qos` | number | 0 / 1 / 2 |
| `msg.retained` | boolean | 是否为保留消息 |

### ctx API

| 调用 | 说明 |
|---|---|
| `ctx.json()` | 解析 payload，返回对象/数组；数字为 int64（放不下时字符串） |
| `ctx.get(path)` | 按路径取标量，**始终返回字符串或 null**；大整数不丢精度 |
| `ctx.emit(record)` | 产出一条记录；字段走 CollectedRecord 白名单 |
| `ctx.state` | 跨报文对象；热加载、重连、超时后清空 |
| `ctx.log(...)` | 写订阅日志（每实例最多 100 行，之后抑制） |

路径写法与内置映射一致：`a.b.c`、`a[0].b`、`$.a.b`、`$`。

---

## 3. 记录字段

只允许以下字段，未知字段会抛错（防止拼错后「配置看着没问题、数据一条没有」）：

`deviceId`、`deviceName`、`deviceAddressId`、`deviceAddressName`、
`deviceAddressLabel`、`value`、`collectedAt`、`dataType`、`kind`、
`protocol`、`quality`。

- `deviceId` / `deviceAddressId` / `value` 必填且非空。
- `kind` 留空时按 `value` 的 Go 类型推断（bool/int/float/string）。
- `quality` 缺省为 192（正常）；质量码可参考内置映射的受控枚举。
- `value` 最多 4096 字节；单报文产出默认最多 200 条（可配置，硬顶 1000）。

---

## 4. 常见写法

### 4.1 身份来自主题

```js
function parse(msg, ctx) {
  var parts = msg.topic.split('/')      // factory/<deviceId>/telemetry/<tag>
  ctx.emit({
    deviceId: parts[1],
    deviceAddressId: parts[3],
    deviceName: 'line-' + parts[1],
    deviceAddressLabel: '值',
    value: ctx.get('v'),
    kind: 'float'
  })
}
```

### 4.2 一条报文多个测点

```js
function parse(msg, ctx) {
  var d = ctx.json()
  var metrics = [
    { id: 'temp', label: '温度', value: d.data.temp },
    { id: 'hum',  label: '湿度', value: d.data.hum }
  ]
  for (var i = 0; i < metrics.length; i++) {
    var m = metrics[i]
    if (m.value === undefined || m.value === null) continue
    ctx.emit({
      deviceId: d.deviceId,
      deviceAddressId: m.id,
      deviceAddressLabel: m.label,
      value: m.value,
      kind: 'float',
      quality: 192
    })
  }
}
```

### 4.3 跨报文状态（增量/累计）

```js
function parse(msg, ctx) {
  var d = ctx.json()
  var prev = ctx.state.last || 0
  var delta = d.total - prev
  ctx.state.last = d.total
  if (delta < 0) delta = d.total       // 计数器回零
  ctx.emit({
    deviceId: d.deviceId,
    deviceAddressId: 'delta',
    value: delta,
    kind: 'int',
    quality: 192
  })
}
```

状态只在同一解析器实例内有效；修改脚本、删除/重建订阅、超时都会重置。

### 4.4 时间字段

```js
ctx.emit({
  ...,
  collectedAt: d.ts         // 支持 epoch 秒/毫秒/微秒/纳秒、YYYY-MM-DD HH:mm:ss[.SSS]、RFC3339
})
```

未提供时由时序通道使用批次落地时间。

---

## 5. 数字精度

- `ctx.get(path)` 永远返回字符串，适合设备 ID、序列号、纳秒时间戳。
- `ctx.json()` 中整数能放进 int64 就是精确的 int64；**超出 int64 的数字会变成字符串**，
  避免经 `float64` 丢精度。不要对这类值做算术。
- 需要做算术时用 `Number(...)`，并确认远端数值在安全范围内。

---

## 6. 错误与超时

- 抛异常：该条报文失败，计入 `errors`，批内后续报文继续。
- 超过 `timeoutMs`（默认 50、最大 500）：该条失败，VM 重建，`ctx.state` 清空。
- 脚本语法错误/缺少 `parse` 函数：保存或热加载时直接拒绝，不会进入运行期。
- `ctx.emit` 未知字段、缺必填字段、超过记录上限：该条报文失败。
- 预览与运行期使用同一套解析器；预览的 error 就是运行期会看到的错误。

---

## 7. 限制与安全

| 项 | 限制 |
|---|---|
| 脚本大小 | 256 KiB |
| 单报文超时 | 1–500ms（默认 50） |
| 单报文记录数 | 1–1000（默认 200） |
| 报文大小 | 1 MiB |
| 单 value | 4096 字节 |
| 可用能力 | 仅 `msg` / `ctx`；无文件、网络、`require`、Go 对象 |

脚本运行在网关进程内，**不是**针对不可信代码的硬隔离。需要崩溃隔离或
厂商 SDK 时使用进程外解析插件（见 spec 第 7 节）。

---

## 8. 调试建议

1. 在订阅编辑页填入样例报文与样例主题，点「运行预览」；
2. 用 `ctx.log()` 输出中间值（注意每实例 100 行上限）；
3. 先在预览里把异常/缺字段跑通，再保存（保存会编译脚本并拒绝语法错误）；
4. 不要在脚本里做重循环或大对象分配 —— 单条超时后这条报文就丢了；
5. 需要多测点时优先用内置映射的 `fanout`，比脚本更易维护。
