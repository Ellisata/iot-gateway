# 推送载荷契约（push payload）

[简体中文](push-payload.md) | [English](push-payload.en.md)

> 本规范定义采集数据推送到外部消息通道（MQTT / HTTP-Webhook）时，载荷的字段语义与
> `value` 值的**自描述解析约定**。`native-json`（§1 / §2）与 `group-json`（§1.1）
> 都适用；外部消费端应据此解析，不依赖网关内部实现。

## 0. 载荷格式

推送通道现在支持四种载荷格式，由通道配置的 `format` 决定：

- `native-json`（默认，可省略）：即 §1 / §2 描述的契约。引入格式选择**不会改变**
  它的字段、顺序或取值。
- `group-json`：按 (设备, 采集组) 输出的自描述帧，设备与组身份在帧头、点位在
  `points` 数组（§1.1）。点位字段名与 `native-json` 一致，`value` 的解析规则
  同样适用于 §3 / §4。
- `mapping-json`：按用户配置的出站映射生成客户自定义 JSON。见
  [push-channels.md](../docs/push-channels.md)。
- `script`：用 goja 脚本生成任意文本载荷（string 一条 / string[] 多条）。见
  [push-channels.md](../docs/push-channels.md)。

选择 `mapping-json` 或 `script` 的通道不适用本文的字段表；下游按该通道自己的
映射规则或脚本约定解析。

---

## 1. native-json 载荷结构

消息类通道按**采集批次**推送（批次以 (设备, 采集组) 为单位），`native-json` 的 payload
为 JSON 数组，数组内每条 = 一个点位：

```json
[
  {
    "deviceId": "019fabd15a9176dda844147d98d38f9b",
    "deviceCode": "ac-1-code",
    "deviceName": "空调-modbus-tcp",
    "deviceAddressId": "019fb1137da076a4ac97af5e51f2e22c",
    "deviceAddressName": "411115",
    "value": "0",
    "protocol": "ModBus.TCP",
    "dataType": "word",
    "kind": "uint",
    "quality": 192,
    "collectedAt": "2026-08-19 16:39:47.864",
    "thingModelCode": "th_ac",
    "propertyCode": "temperature"
  }
]
```

> **投递时机（变化才推送）**：`push.changeOnly` 出厂为 true。同一设备同一点位的
> `value`、`quality` 与物模型/属性编码自上次**真正投递**后都没变时，本轮不再出现 —— 下游不能把
> "收到一条记录"当作设备在线心跳（在线状态请看设备断联报警）。网关启动、
> 推送通道新增或改配后的首轮总是全量，且质量码变化（如 192 → 2）本身也算变化。
> 需要等间隔采样时把该开关置为 false（可在「运行开关」页动态切换，从下一批采集起生效）。

### 1.1 group-json 帧结构

`group-json` 把设备级与组级信息提到帧头，`points` 放本组全部点位；一条消息 =
一个 (设备, 采集组) 的一次采集快照：

```json
{
  "collectedAt": "2026-11-02 20:00:00.123",
  "deviceId": "019fabd15a9176dda844147d98d38f9b",
  "deviceCode": "ac-1-code",
  "deviceName": "空调-modbus-tcp",
  "protocol": "ModBus.TCP",
  "thingModelCode": "th_ac",
  "groupId": "019fb110000000000000000000000001",
  "groupCode": "fast",
  "groupName": "fast",
  "groupIntervalMs": 500,
  "points": [
    {
      "deviceAddressId": "019fb1137da076a4ac97af5e51f2e22c",
      "deviceAddressName": "411115",
      "value": "0",
      "dataType": "word",
      "kind": "uint",
      "quality": 192,
      "propertyCode": "temperature"
    },
    {
      "deviceAddressId": "019fb1137da076a4ac97af5e51f2e23d",
      "deviceAddressName": "411116",
      "value": "",
      "dataType": "word",
      "kind": "uint",
      "quality": 2,
      "propertyCode": ""
    }
  ]
}
```

帧头字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `collectedAt` | string | 同 §2 的 `collectedAt`；**帧级**，同一帧内的点位共享 |
| `deviceId` / `deviceCode` / `deviceName` | string | 同 §2；每帧只出现一次，不随点位重复 |
| `protocol` | string | 同 §2；同一批次只含一台设备，因此是帧级字段；`dataType` 仍是逐点字段 |
| `thingModelCode` | string | 同 §2；设备级，只出现一次 |
| `groupId` | string | 采集组内部主键；未分组为空串 |
| `groupCode` | string | 采集组编码（`collector_group.code`），创建后不可修改，**下游优先用它关联业务**；未分组为空串 |
| `groupName` | string | 采集组名称，仅展示用；未分组为空串 |
| `groupIntervalMs` | int | 组采集周期（毫秒）；未分组为 0 |
| `points` | array | 本组全部点位，见下 |
| `trigger` | object | 同 §2.1；**帧级**（同一批次内的记录共享同一个 trigger），仅事件触发推送时存在，正常帧没有这个键 |

`points[]` 元素与 §2 完全同名同义（`deviceAddressId` / `deviceAddressName` /
`value` / `dataType` / `kind` / `quality` / `propertyCode`），不再重复；`value`
的解析规则见 §3，`protocol` 仍是 `dataType` 的命名空间（§4）。

**坏点不剔除**：`points` 包含本组全部点位，质量码 `< 128` 的点保留在数组里
（`value` 通常为空串）。**判坏用 `quality < 128`，不能用 `value` 是否为空判断**
——空串也可能是合法的文本值。

投递时机与 §1 的 change-only 规则一致：未变化的点位不会出现在 `points` 里，整帧
点位都未变化时不产出消息。因此一帧可能只含部分点位，下游不能假设每次都是全量。
除 `trigger` 外字段始终存在，缺失时为空串或 0；读取按字段名，不要依赖位置。
主题模板与 `groupIds` 订阅过滤见 [push-channels.md §2.1](../docs/push-channels.md)。

## 2. 字段语义

| 字段 | 类型 | 说明 |
|------|------|------|
| `deviceId` / `deviceName` | string | 设备标识与名称 |
| `deviceCode` | string | 设备编码（`device.code`），未编码的旧数据为空串。创建后不可修改，适合做跨环境的稳定关联；读取时按字段名取值，不要依赖字段位置 |
| `deviceAddressId` / `deviceAddressName` | string | 点位标识与地址名（如 `411115`） |
| `value` | string | **已解码**的采集值字符串（协议、字节序、字序已在网关侧完成） |
| `protocol` | string | 协议名称（`iot_protocol.name`，如 `ModBus.TCP`），命名空间标识，见 §4 |
| `dataType` | string | 协议的**内部数据类型名**（如 `word`、`int16`），与 `protocol` 组合确定精确 PLC 类型，见 §4 |
| `kind` | string | 数据类型类别（6 类之一），`value` 解析的**唯一事实来源** |
| `quality` | int | 采集质量码（`driver.QualityXxx`，类别在高 2 位、成因在低 6 位）：192=正常、128=不确定但值可用、0=坏但未归类、1=坏·地址、2=坏·通信、3=坏·解析。**判"坏"要写 `quality < 128`，不能写 `quality == 0`**（区间读取失败时置坏码，value 为空串） |
| `collectedAt` | string | 采集时间，格式 `2006-01-02 15:04:05.000`（毫秒精度），**本地时区** |
| `thingModelCode` | string | 设备绑定的物模型编码（`thing_model.code`）；未绑定为空串。创建后不可修改，可作稳定维度 |
| `propertyCode` | string | 点位绑定的物模型属性标识符（`thing_model_property.identifier`）；未绑定为空串。绑定时已校验属性属于设备的物模型 |
| `trigger` | object | **仅事件触发定向推送时存在**，正常采集/外部订阅**没有这个键**。结构见 §2.1 |

`deviceCode` 位于 `deviceId` 之后；`thingModelCode` / `propertyCode` 是**追加字段**（位于
JSON 末尾）。三者都始终存在，缺失时为空串；既有字段的名称、相对顺序与取值不变。
外部 MQTT 订阅产生的记录没有本地绑定，
这三个字段同样为空串。
时序库通道（TDengine / InfluxDB）把两者作为 tag 写入（`thing_model_code` / `property_code`）。

### 2.1 trigger：事件触发元数据

事件触发规则定向推送动作点位时，每条 point 末尾追加：

```json
"trigger": {
  "source": "trigger",
  "ruleId": "019...",
  "ruleCode": "batch_done",
  "ruleName": "批次完成取数",
  "triggerId": "019...",
  "firedAt": "2026-11-04 10:00:00.000"
}
```

`ruleCode` 是规则的业务编码（创建后不可修改），下游优先用它关联业务；`ruleId`
仅是网关内部主键。`firedAt` 是**条件上升沿时刻**，不是读取完成时刻。该对象随记录写入断网缓存
（`push_outbox.records_json`），补发不丢失。正常采集与外部订阅的记录 `trigger` 为 nil，
因 `omitempty` 而**完全不出现在 JSON 里**——既有报文逐字节不变。

## 3. value 解析：按 `kind` 分派

`kind` 是 `driver.KindXxx` 的取值，恒为以下 6 类之一。外部按此分派解析，**不要**按
`dataType` 枚举解析（dataType 虽已归一化为内部名，但同一类别可对应多种内部类型，如
`uint8`/`uint16`/`word` 同为 `uint`，解析方式相同）。

| kind | 含义 | 解析方式 |
|------|------|----------|
| `bool` | 布尔 | 字符串 `"0"`/`"1"` |
| `int` | 有符号整数 | 十进制整数（`strconv.ParseInt`），可含负号 |
| `uint` | 无符号整数 | 十进制整数（`strconv.ParseUint`），无负号 |
| `float` | 浮点 | 十进制小数，**最短往返表示**（见 §5） |
| `string` | 文本 | 原样字符串（已去除尾部 `\x00`） |
| `time` | 日期/时间 | 按协议为 ISO-8601 或日期/时刻字符串（见 §6） |

未知/空 `kind` 时按数值推断降级：整数 → 浮点 → 文本（与 TSDB 通道一致）。

## 4. `protocol` + `dataType` 说明

- `protocol` 为协议名称（`iot_protocol.name`，如 `ModBus.TCP`、`Siemens.S7`），
  是类型的**命名空间**；`dataType` 为该协议作用域下的**内部数据类型名**
  （`word`、`int16`、`uint8`…），来自地址配置的 `data_type` 字段，已归一化
  （`Word`/`WORD`/`word` 均落为 `word`）。
- 二者组合可精确定位 PLC 类型：同一内部名在不同协议下语义不同
  （如 `byte` → Modbus `uint8`、S7 `byte`），`protocol` 消除跨协议同名歧义。
- **外部消费端按 `kind` 解析 `value`**（见 §3）；`protocol`+`dataType` 用于精确类型还原与聚合分析。

## 5. 浮点表示（无损往返）

`float`/`uint`/`int` 的字符串均由 `strconv.FormatFloat(v, 'f', -1, bits)` 生成：

- **固定记法**：永不出现指数（无 `1.23e+08`）；
- **最短往返**：字符串可无损还原解码后的浮点/整数值（float32 用 32、float64 用 64 位）。
- 示例：`25.5000 → "25.5"`，float32 π → `"3.1415927"`。
- 因此浮点精度**在字符串层面无损失**，外部按数值解析即可恢复原始精度；不应假设固定小数位。

## 6. 时间类型（`kind=time`）表示

| 协议 | 类型 | value 格式 | 示例 |
|------|------|-----------|------|
| Modbus / FINS | `date` | RFC3339，UTC | `2026-08-19T08:39:47Z` |
| S7 | `date` | `YYYY-MM-DD` | `2026-08-19` |
| S7 | `tod` | `HH:MM:SS` | `16:39:47` |
| S7 | `dt` | `YYYY-MM-DD HH:MM:SS` | `2026-08-19 16:39:47` |

- Modbus/FINS `date` 原始语义为 **Unix 秒**（驱动解码约定），归一化为 UTC RFC3339 输出，
  消费端无需再约定单位；需要 epoch 时由 ISO 转换。
- S7 的 date/tod/dt 已按可读格式输出，**不含时区**（S7 解码为本地时间），消费端按文本使用。

## 7. 已知契约缺口（不按本规范解决）

- `collectedAt` 为**本地时区、无时区标记**的字符串。跨时区部署多网关时，下游按时间排序/
  比较可能错位；TSDB 通道在网关侧按 `time.Local` 解析为 epoch 存储，不受影响。
  若需消除歧义，后续将 `collectedAt` 归一化为带时区的 RFC3339（需同步修改
  TDengine/InfluxDB 的时间戳解析）。
- 工程单位量纲（scale/offset）不在采集范围内，payload 不含，需要时由点位配置扩展。
