# 推送载荷契约（push payload）

[简体中文](push-payload.md) | [English](push-payload.en.md)

> 本规范定义采集数据推送到外部通道（当前为 MQTT）时，每条点位 JSON 的字段语义与
> `value` 值的**自描述解析约定**。外部消费端应据此解析，不依赖网关内部实现。

---

## 1. 载荷结构

MQTT 通道按**设备分组**推送，payload 为 JSON 数组，数组内每条 = 一个点位：

```json
[
  {
    "deviceId": "019fabd15a9176dda844147d98d38f9b",
    "deviceName": "空调-modbus-tcp",
    "deviceAddressId": "019fb1137da076a4ac97af5e51f2e22c",
    "deviceAddressName": "411115",
    "value": "0",
    "protocol": "ModBus.TCP",
    "dataType": "word",
    "kind": "uint",
    "quality": 192,
    "collectedAt": "2026-08-19 16:39:47.864"
  }
]
```

## 2. 字段语义

| 字段 | 类型 | 说明 |
|------|------|------|
| `deviceId` / `deviceName` | string | 设备标识与名称 |
| `deviceAddressId` / `deviceAddressName` | string | 点位标识与地址名（如 `411115`） |
| `value` | string | **已解码**的采集值字符串（协议、字节序、字序已在网关侧完成） |
| `protocol` | string | 协议名称（`iot_protocol.name`，如 `ModBus.TCP`），命名空间标识，见 §4 |
| `dataType` | string | 协议的**内部数据类型名**（如 `word`、`int16`），与 `protocol` 组合确定精确 PLC 类型，见 §4 |
| `kind` | string | 数据类型类别（6 类之一），`value` 解析的**唯一事实来源** |
| `quality` | int | 192=正常，0=异常（区间读取失败时置 0，value 为空串） |
| `collectedAt` | string | 采集时间，格式 `2006-01-02 15:04:05.000`（毫秒精度），**本地时区** |

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

- `protocol` 为协议名称（`iot_protocol.name`，如 `ModBus.TCP`、`Siemens.Net.S7`），
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
