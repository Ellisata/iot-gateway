# 推送通道与出站映射

本文档描述消息类推送通道（`mqtt` / `http` / `kafka`）的**载荷格式**与连接配置，
以及状态型通道 **OPC UA Server** 的地址空间约定。
`tdengine-v3` / `influxdb-v3` 的写入语义不变，见各自的表结构说明。

## 1. 通道 = 类型 + 实例名

`push_channel` 现在有两列：

- `type`：通道类型（`mqtt` / `http` / `kafka` / `tdengine-v3` / `influxdb-v3`），决定用哪个实现；
- `name`：实例名，同一类型下唯一。

因此同一类型可以建多条。例如两条 HTTP 通道分别接 MES 与 ERP，或两条 MQTT
通道接不同 broker。旧数据库升级时，原有行会被迁移为 `type = name`，配置与
旧接口行为保持不变。

## 2. 载荷格式

消息类通道在 `config_json` 里支持两个字段：

```json
{
  "format": "native-json",
  "mapping": { }
}
```

| format | 说明 |
|---|---|
| `native-json`（默认，可省略） | 网关默认契约，字段语义见 [push-payload.md](../specs/push-payload.md)。**切换本功能不会改变它的任何字节。** |
| `group-json` | 按 (设备, 采集组) 输出的自描述帧（见 §2.1），字段语义见 [push-payload.md §1.1](../specs/push-payload.md)。不接受 `mapping`。 |
| `mapping-json` | 按下面的映射规则生成客户自定义 JSON。必须配置 `mapping`。 |
| `script` | 用 goja 脚本生成任意文本载荷，适合声明式映射表达不了的结构。必须配置 `mapping` 里的脚本。 |

非消息类通道（TDengine / InfluxDB）不接受 `format` / `mapping`。

### 2.1 采集组与 group-json

点位可以绑定到设备下的**采集组**（`collector_group`）。绑定后：

- 采集周期由组的 `interval_ms` 决定，点位自身的 `scan_frequency` 不生效；
- 组停用时，绑定到该组的点位不采集、不推送；
- 推送按 `(设备, 组)` 分帧，一个批次只含同一组的点位；
- 未分组点位保持旧行为（按 `scan_frequency` 采集，批次 `groupId` 为空）。

`group-json` 的帧结构：

```json
{
  "collectedAt": "2026-11-02 20:00:00.123",
  "deviceId": "dev-1",
  "deviceCode": "dev001",
  "deviceName": "一号机",
  "protocol": "ModBus.TCP",
  "thingModelCode": "th_sensor",
  "groupId": "g-1",
  "groupCode": "fast",
  "groupName": "fast",
  "groupIntervalMs": 500,
  "points": [
    {
      "deviceAddressId": "a1",
      "deviceAddressName": "temperature",
      "value": "23.5",
      "dataType": "float",
      "kind": "number",
      "quality": 192,
      "propertyCode": ""
    },
    {
      "deviceAddressId": "a2",
      "deviceAddressName": "pressure",
      "value": "",
      "dataType": "int16",
      "kind": "number",
      "quality": 1,
      "propertyCode": ""
    }
  ]
}
```

`points` 是本组**全部**点位（正常与坏质量点都在其中），顺序与批次记录一致，
下游按 `quality` 区分：`>= 128` 为正常/不确定，`< 128` 为坏。坏点的 `value`
通常为空串，但**不能用空串判坏**。点位字段名与 `native-json` 的逐点契约一致
（`deviceAddressId` / `deviceAddressName` / `value` / `dataType` / `kind` /
`quality` / `propertyCode`）；`deviceId` / `deviceCode` / `deviceName` /
`protocol` / `thingModelCode` 只出现在帧头（`protocol` 是 `dataType` 的命名空间），
`collectedAt` 与 `trigger` 是帧级、同帧点位共享，均不随点位重复。
未分组批次的 `groupId`/`groupCode`/`groupName` 为空串、`groupIntervalMs` 为 0。

**通道订阅采集组**：`config_json.groupIds` 是组 ID 数组。

- 省略/空数组：接收全部记录（含未分组）；
- 非空：只接收这些组，未分组记录被排除。

被任一通道订阅的组**不能删除**（错误码 `40050`）：先在该通道的 `groupIds` 里移除这个组。
停用的通道同样算引用 —— 重新启用时配置不会丢。删除设备会级联删组，所以名下任一组
被订阅时设备删除（单条与批量）同样被拒绝（`40065`），批量删除不做部分成功。

MQTT 主题模板只支持以下编码占位符：

| 占位符 | 值 | 缺失时 |
|---|---|---|
| `${deviceCode}` | 设备编码 `device.code` | `_unencoded` |
| `${thingModelCode}` | 设备绑定的物模型编码 `thing_model.code` | `_unmodeled` |
| `${groupCode}` | 采集组编码 `collector_group.code` | `legacy`（未分组） |

控制台的主题字段是一个“模板下拉 + 手动输入”的选择框：下拉提供下面的典型模板，
也可以直接键入任意主题（多个主题用英文逗号分隔）。

`+`、`${deviceId}`、`${group}`、`${groupId}` 不再替换，原样保留。**主题层级必须由模板固定，缺失维度用占位段补齐，不要少一层。** 典型模板：

```text
# 需要按物模型订阅/授权
iot/${thingModelCode}/${deviceCode}/${groupCode}

# 不需要按物模型订阅/授权
iot/${deviceCode}/${groupCode}
```

未绑定物模型的设备发布到 `iot/_unmodeled/...`；未分组点位发布到 `.../legacy`；
未编码的旧设备（正常不会出现）发布到 `.../_unencoded/...`。
这样无论设备是否绑定模型、点位是否分组，主题深度都不变，下游只需一套通配符规则：

```text
iot/+/+/+        # 全量
iot/modelA/+/+   # 某个物模型
iot/+/dev001/+   # 某台设备
iot/+/+/fast     # 某个组（跨设备）
```

> 组编码、设备编码创建后不可修改；物模型编码创建后也不可变。
> 主题里不要使用组名或 ID，组名只是展示字段。

时序库通道把**设备的物模型编码与点位的属性编码**也作为 tag 写入：

| 通道 | tag |
|---|---|
| `influxdb-v3` | `device_id`、`device_address_id`、`thing_model_code`、`property_code`（后两个未绑定时**省略**） |
| `tdengine-v3` | 同上四个 tag（超级表 `TAGS`，未绑定时写空串） |

TDengine 的超级表用 `CREATE STABLE IF NOT EXISTS` 建表：**已部署实例不会自动加 tag**，
升级后需手动执行 `ALTER STABLE ... ADD TAG thing_model_code NCHAR(64)` 与
`ALTER STABLE ... ADD TAG property_code NCHAR(64)`，否则写入会因 tag 数不符被判为毒批次
并静默丢数据（启动日志会报出缺哪个 tag）。已有子表的新 tag 值为 NULL，新写入不会回填，
需要时按子表 `ALTER TABLE ... SET TAG ...`。

### 2.2 事件触发的载荷元数据

事件触发规则定向推送动作点位时，每条记录额外携带 `trigger` 对象：

```json
{
  "source": "trigger",
  "ruleId": "019...",
  "ruleCode": "batch_done",
  "ruleName": "批次完成取数",
  "triggerId": "019...",
  "firedAt": "2026-11-04 10:00:00.000"
}
```

- `ruleCode` 是规则创建时填写的业务编码，创建后不可修改，下游应优先用它；
  `ruleId` 仅是网关内部主键。
- `firedAt` 是**条件上升沿时刻**，不是读取完成时刻；可与触发历史对齐。
- 只有事件触发定向推送的批次才有该字段。**正常采集、外部订阅的载荷逐字节不变。**
- 元数据随记录写入断网缓存（`push_outbox.records_json`），补发不丢失。

各格式的暴露方式：

| 格式 | 位置 |
|---|---|
| `native-json` | 每个 point 末尾的 `trigger` 字段（正常点位无此字段） |
| `group-json` | 帧顶层的 `trigger` 字段（正常帧无此字段） |
| `mapping-json` | 输入文档新增 `trigger` 对象，用 `{"source":"path","value":"trigger.ruleId"}` 取用；正常记录的 `trigger.*` 为空串 |
| `script` | `batch.trigger.*` 与 `batch.records[i].trigger.*`；正常批次不新增键 |

时序库通道（TDengine / InfluxDB）与 OPC UA Server 通道不使用该对象，不会改变表结构或地址空间。

## 3. 出站映射（mapping-json）

### 3.1 完整示例

```json
{
  "version": 1,
  "emit": "batch",
  "envelope": "data",
  "batchFields": {
    "site":  { "source": "const", "value": "plant-a" },
    "count": { "source": "path", "value": "count", "type": "number" }
  },
  "fields": {
    "device.id":         { "source": "path", "value": "deviceId" },
    "device.name":       { "source": "path", "value": "deviceName" },
    "point.id":          { "source": "path", "value": "deviceAddressId" },
    "point.label":       { "source": "path", "value": "deviceAddressLabel" },
    "measurement.value": {
      "source": "path",
      "value": "value",
      "type": "number",
      "transform": [ { "op": "scale", "factor": 0.1 } ]
    },
    "quality": { "source": "path", "value": "quality", "type": "number" },
    "ts":      { "source": "path", "value": "collectedAt" }
  }
}
```

输出（一批两条记录）：

```json
{
  "count": 2,
  "data": [
    {
      "device": { "id": "d1", "name": "空调-1" },
      "point": { "id": "a1", "label": "温度" },
      "measurement": { "value": 23.5 },
      "quality": 192,
      "ts": "2026-10-24 10:00:00.000"
    }
  ],
  "site": "plant-a"
}
```

### 3.2 输入字段

每条记录的输入文档固定为以下键（与 `collectedAt` 的回落规则）：

| 键 | 说明 |
|---|---|
| `deviceId` / `deviceCode` / `deviceName` | 设备 ID、编码、名称（编码未设置时为空串） |
| `deviceAddressId` / `deviceAddressName` / `deviceAddressLabel` | 点位 ID、地址名、说明 |
| `value` | 已解码的采集值（字符串） |
| `dataType` / `kind` / `protocol` | 协议内部类型、类型类别、协议名 |
| `quality` | 质量码（整数） |
| `collectedAt` | 采集时间；记录为空时回落为批次时间 |
| `thingModelCode` | 设备绑定的物模型编码（`thing_model.code`）；未绑定为空串 |
| `propertyCode` | 点位绑定的物模型属性标识符（`thing_model_property.identifier`）；未绑定为空串 |
| `groupId` / `groupCode` / `groupName` | 采集组 ID、编码、名称；未分组为空串 |

`batchFields` 额外可读：`deviceId`、`deviceCode`、`groupId`、`groupCode`、`groupName`、`intervalMs`（组周期，未分组为 0）、`collectedAt`、`count`（本批记录数）。

### 3.3 取值方式

与订阅侧的内置映射共用同一套词汇：

| source | 说明 |
|---|---|
| `path`（默认） | 按 JSON 路径取值，如 `value`、`deviceId` |
| `const` | 固定值 |
| `template` | 字符串模板，如 `{deviceName}/{deviceAddressLabel}`；`{{` / `}}` 表示字面量花括号 |
| `map` | 先按路径取值，再查 `mapping` 表；键比较忽略大小写与数值差异 |

可选字段：

- `default`：路径不存在 / 映射未命中时的回落值。
- `transform`：算子链，支持 `scale` / `round` / `trim` / `default` / `hexDecode` / `base64Decode` / `bitfield` / `regex`。
- `type`：`auto`（默认）/ `string` / `number` / `boolean`。

`source=topic` 仅用于入站订阅，出站映射会拒绝。

### 3.4 输出类型

- `auto`：`value` 字段按记录的 `kind` 输出原生 JSON 类型（bool / number / string）；
  `quality` 输出数字；其他字段保持字符串。
- `string`：一律字符串。
- `number`：输出 JSON 数字，使用 `json.Number` 保留大整数精度；拒绝空值、
  `NaN`、`Inf`、十六进制等非 JSON 数字。
- `boolean`：接受 `true/false`、`1/0`、数字 1/0。

### 3.5 emit / envelope / batchFields

| 配置 | 输出 |
|---|---|
| `emit` 省略或 `batch`，无 `envelope` / `batchFields` | JSON 数组，每个元素是一条记录 |
| `emit: batch` + `envelope: "data"` | `{"data":[...], ...batchFields}` |
| `emit: batch` + 只有 `batchFields` | 数组放在默认键 `records` 下 |
| `emit: record` | 每条记录一条消息（HTTP 即一次请求）；不能配置 `envelope` / `batchFields` |

输出键用点号表示嵌套，例如 `device.id` 生成 `{"device":{"id":...}}`。
P0 只支持对象嵌套，不支持数组、条件与循环。

### 3.6 至少一次投递

HTTP 的 `emit=record` 会一个批次发多次请求。若其中一条失败并触发补发，已经
成功的记录会在重放时**再次发送**。下游应按业务主键做幂等，或改用
`emit=batch`（一条请求一条批次）。

### 3.7 脚本格式（script）

映射规则表达不了的结构（条件分支、自定义嵌套、非 JSON 文本等）可以用脚本编码：

```json
{
  "format": "script",
  "mapping": {
    "script": "function encode(batch, ctx) { return batch.records.map(function (r) { return JSON.stringify({ device: batch.deviceId, point: r.deviceAddressId, value: Number(r.value) }) }) }",
    "timeoutMs": 50,
    "maxMessages": 200
  }
}
```

脚本必须定义 `function encode(batch, ctx)`：

| 输入 | 说明 |
|---|---|
| `batch.deviceId` | 批次设备 ID |
| `batch.deviceCode` | 批次设备编码（未编码为空串） |
| `batch.groupId` / `batch.groupCode` / `batch.groupName` | 批次采集组 ID、编码、名称（未分组为空串） |
| `batch.intervalMs` | 批次采集组周期（毫秒，未分组为 0） |
| `batch.collectedAt` | 批次采集时间（可能为空） |
| `batch.records[]` | 记录数组，字段与 CollectedRecord 一致：`deviceId`、`deviceCode`、`deviceName`、`deviceAddressId`、`deviceAddressName`、`deviceAddressLabel`、`value`、`dataType`、`kind`、`protocol`、`quality`、`collectedAt`、`thingModelCode`、`propertyCode`、`groupId`、`groupCode`、`groupName` |
| `ctx.log(...)` | 写网关日志（每实例最多 100 行） |

- `records[].collectedAt` 与 `batch.collectedAt` 遵循同一条回落规则：本地点位采集的记录级时间为空，此时取 `batch.collectedAt`（毫秒精度本地时间）。外部订阅记录带 `collectedAt` 时优先用记录自己的。
- `value` 始终是**字符串**，不做 float64 中转，大整数不丢精度；需要算术时用 `Number(...)`。
- 返回值：`string` 发送一条消息；`string[]` 发送多条消息；`null` / `undefined` 表示本批不发送。
- 每次编码都新建 goja runtime，**不保留跨批次状态**。断网缓存会重放同一批次，脚本应对同一输入产出相同结果，不要依赖 `Date.now()` / `Math.random()`。
- 保存时会编译脚本并执行一遍顶层代码；语法错误、顶层异常、缺少 `encode` 函数会直接拒绝。
- 限制：脚本 ≤256 KiB；`timeoutMs` 1–500（默认 50）；`maxMessages` 1–1000（默认 200）；单条载荷 ≤1 MiB，单批总产出 ≤16 MiB。
- 运行期脚本异常按编码失败处理：HTTP 视为毒批次丢弃，MQTT 在启用断网缓存时回写缓存等待重试。建议脚本逐条容错（跳过坏记录），不要让一条坏数据抛错拖垮整批。
- 脚本在网关进程内执行，仅防误用，不是针对不可信代码的硬隔离。

## 4. HTTP / Webhook 通道

### 4.1 配置

```json
{
  "url": "https://mes.example.com/api/ingest",
  "method": "POST",
  "contentType": "application/json; charset=utf-8",
  "headersJson": "{\"X-Environment\":\"prod\"}",
  "authType": "hmac-sha256",
  "hmacSecret": "shared-secret",
  "hmacHeader": "X-Signature",
  "hmacPrefix": "sha256=",
  "timeoutSeconds": 10,
  "workers": 4,
  "spoolDisabled": false,
  "spoolMaxBatches": 100000,
  "format": "mapping-json",
  "mapping": { }
}
```

- `url`：仅支持 `http` / `https`。
- `method`：`POST` / `PUT` / `PATCH`，默认 `POST`。
- `headersJson`：字符串形式的 JSON 对象；自定义头先写入，`Content-Type` 与鉴权头优先。
- `authType`：
  - `none`
  - `bearer`：`Authorization: Bearer <token>`
  - `basic`：HTTP Basic
  - `hmac-sha256`：`hex(HMAC-SHA256(secret, body))`，默认头 `X-Signature`，默认前缀 `sha256=`。
    请求体是原始字节，便于下游复算。
- `timeoutSeconds`：1–120，默认 10。
- `workers`：并发发送数，1–64，默认 4。
- `spoolDisabled` / `spoolMaxBatches`：断网缓存开关与批次上限，默认开启 / 100000。
- `insecureSkipVerify`：仅内网自签名证书时开启。

### 4.2 错误分类与补发

| 下游响应 | 处理 |
|---|---|
| 2xx | 成功 |
| 408 / 429 / 5xx | 可重试：标记断连，批次写入本地 outbox，恢复后补发 |
| 其他 4xx / 3xx | 毒批次：标记错误并丢弃，不阻塞后续数据 |
| 网络 / DNS / TLS / 超时 | 可重试 |

3xx 不跟随重定向（`Location` 通常是配置错误，不应悄悄改投别处）。

### 4.3 连通性测试、映射预览 vs 测试发送

- **测试连接**：只对 URL 的 host:port 做 TCP 连接（https 再做 TLS 握手），
  不发 HTTP 请求。适合确认「网络是否通」。
- **测试发送**：用第一台启用设备/点位构造样例记录，按当前格式与映射真的发送一次。
  路径、鉴权与载荷是否被下游接受，以这个结果为准。
- **映射预览**：`POST /pushChannel/previewMapping`，请求体只有 `mapping`。
  它不构造通道、不读连接配置，直接用样例记录试编码出站映射；适合在 URL / Broker
  还没填完时先把映射调好。响应形状与 `/preview` 相同，映射/编码错误同样放在
  `data.error`（HTTP 200）。

控制台的「预览载荷」只编码、不发送；「测试发送」会二次确认后发出真实请求。
可视化映射编辑器在切换字段、取值方式或类型时会自动调用映射预览接口。

### 4.4 安全提示

HTTP 通道允许管理员把采集数据发往任意 http/https 地址，配置里包含 Bearer /
Basic / HMAC 凭据。`/agentApi/pushChannel/*` 是**免鉴权**的（见
[SECURITY.md](../../SECURITY.md)），因此网关必须部署在隔离工业内网，
或只允许可信主机访问网关端口。

## 5. 从订阅映射迁到出站映射

入站与出站共用 `path` / `const` / `template` / `map` 与 `transform` 算子。
区别只在方向：

- 入站：外部 JSON → `CollectedRecord`（目标字段是白名单）；
- 出站：`CollectedRecord` → 客户 JSON（目标键由你定义，支持嵌套）。

因此订阅里已经验证过的 `scale` / `regex` / `bitfield` 等算子可以直接复制到
出站 `fields` 里使用。

## 6. OPC UA Server 通道（type=opcua）

这不是消息类通道：它不编码载荷、不走断网补发，而是把采集数据暴露成 OPC UA
地址空间，由客户端 Read / 订阅。通道复用推送引擎的配置与热加载。

### 6.1 地址空间

```
Objects (ns=0;i=85)
└── iot-gateway                ns=1;s=root
    └── <设备名>                ns=1;s=dev:<deviceID>
        └── <点位名>            ns=1;s=pt:<deviceID>:<addressID>
```

- 只暴露 `status=1` 的设备与点位；`deviceIds` 非空时只暴露这些设备。
- `nodeIdStyle=id`（默认）用数据库 ID，改名/重启后 NodeID 不变；`name` 用设备/点位名，
  可读但改名即失效。
- 点位 DisplayName 取 `device_address.label`，为空时退回 `name`。
- 数据类型由 `common_data_type` 决定（Boolean / Byte / Short / Word / Long / DWord /
  Float / Double / Date / String / BCD）。采集值按该类型解析；解析失败保留上一个值
  并置 `BadDataEncodingInvalid`。
- 质量码映射：Good → Good，Uncertain → Uncertain，地址坏 → `BadConfigurationError`，
  通信坏 → `BadCommunicationError`，解析坏 → `BadDataEncodingInvalid`，
  其余 → `BadInternalError`。从未采集过的点位为 `BadWaitingForInitialData`。
- 时间戳：批次的 `collectedAt` → SourceTimestamp，服务端当前时间 → ServerTimestamp。
- 点位是**只读**的（AccessLevel = CurrentRead）。客户端写入返回
  `BadUserAccessDenied`：本通道只负责把采集值暴露出去，不回写 PLC。
- 点位变量带完整的类型元数据：`DataType` 按 Part 3 §5.6.2 写成 **NodeId** 值类型
  （gopcua 默认写 ExpandedNodeId，UaExpert 会解析失败并当成 `i=0`），标量点位
  `ValueRank = -1`。缺这两项时 UaExpert 拖入点位会在日志里报
  `DataTypeId ... has bad statuscode` / `NodeClass of i=0 ... BadNodeIdUnknown` /
  `ValueRank ... BadAttributeIdInvalid`。
- 每个点位附带 `EngineeringUnits`（dimensionless）与 `ValuePrecision` 两个标准属性，
  并实现了 `TranslateBrowsePathsToNodeIDs`。UaExpert 把点位拖入 Data Access View 时
  不会再报 `BadServiceUnsupported` / `Failed to get EU Information` / `Failed to get Value Precision`。

### 6.2 配置

| 字段 | 默认 | 说明 |
|---|---|---|
| `listenHost` | `0.0.0.0` | 监听地址；客户端不接受 0.0.0.0 时填本机局域网 IP |
| `listenPort` | `4840` | 监听端口 |
| `namespaceUri` | `urn:iot-gateway:devices` | 自定义命名空间 URI |
| `rootName` | `iot-gateway` | 根 Object 的显示名 |
| `securityPolicy` | `None` | `None` 或 `Basic256Sha256` |
| `securityMode` | `None` | `None` / `Sign` / `SignAndEncrypt` |
| `certFile` / `keyFile` | 空 | 安全策略非 `None` 时必填（PEM/DER 证书 + RSA 私钥） |
| `nodeIdStyle` | `id` | `id` / `name` |
| `deviceIds` | 空 | 留空 = 全部启用设备；JSON 数组或逗号分隔字符串 |
| `advertiseHosts` | 空 | 额外公布的地址（DNS 别名、NAT 公网 IP 等），只登记端点描述、不绑定监听；数组或逗号分隔 |

### 6.3 更新与重建

- 每个采集批次由独立 goroutine 应用；`Enqueue` 只做非阻塞入队，绝不阻塞采集协程。
- 节点值放在原子指针里，通过 `ValueFunc` 读取；只在值/状态变化时调用
  `ChangeNotification`（新建 MonitoredItem 时 gopcua 会主动补一次初值）。
- 端点表会登记 `0.0.0.0`（监听地址）、`127.0.0.1`、`localhost`、主机名以及本机所有
  IPv4 地址。gopcua 的 `GetEndpoints` 与 `CreateSession` 都按 EndpointURL
  **精确匹配**：客户端必须使用这些地址之一（通常是网关的局域网 IP），否则
  `CreateSessionResponse.ServerEndpoints` 为空，UaExpert 会在 ActivateSession 报
  `BadConfigurationError`。客户端使用 DNS 别名或 NAT 地址时，把该地址填进
  `advertiseHosts`（只公布、不绑定）；本机主机的别名也可以直接把 `listenHost` 填成它。
- `GetEndpoints` 被覆盖：优先返回与请求 URL 匹配的端点；一个都没匹配上时返回全部端点
  并把 URL 回写成客户端请求的地址。
- 每 10s 比对设备/点位结构签名；变化时重建整棵地址空间（gopcua 不支持运行期删除节点）。
  重建会短暂断开客户端；同一点位、同一类型会沿用旧值，避免出现一整片空值。
  设备/点位的**取值**变化不触发重建。

### 6.4 限制

- gopcua 的 server 不校验用户名/密码，目前只支持匿名访问。
- `ModifySubscription`、`Republish`、`TransferSubscriptions` 等订阅服务未实现。
- 服务启动时 gopcua 会向标准库 log 打一行 `Started listening on ...`。
- `deviceIds` 使用数据库设备 ID（不是设备名）。

## 7. Kafka 通道（type=kafka）

消息类通道：复用 §2 的全部载荷格式与出站映射，以及断网缓存 / 补发 / 监控。
底层是纯 Go 的 `franz-go`（`kgo`），每个通道写一个**静态 topic**（v1 不做按设备
动态 topic 模板）。建议 Kafka 0.11+（RecordBatch v2）；SASL/PLAIN 需要 broker
启用对应的 SASL 机制。

### 7.1 配置

```json
{
  "brokers": "10.0.0.1:9092,10.0.0.2:9092",
  "topic": "iot-data",
  "clientId": "iot-gateway",
  "acks": "all",
  "allowAutoTopicCreation": false,
  "authType": "plain",
  "saslUsername": "gateway",
  "saslPassword": "secret",
  "tlsEnabled": true,
  "insecureSkipVerify": false,
  "timeoutSeconds": 10,
  "workers": 1,
  "spoolDisabled": false,
  "spoolMaxBatches": 100000,
  "format": "native-json"
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `brokers` | 必填 | 逗号分隔的 seed broker；省略端口时补 `9092`，IPv6 用 `[::1]:9092` 或直接写 `::1` |
| `topic` | 必填 | 静态 topic，仅允许字母、数字、点、下划线与短横线，最长 249 |
| `clientId` | `iot-gateway` | Kafka client.id |
| `acks` | `all` | `all` / `leader` / `none`。**`leader`/`none` 会关闭幂等写入**（franz-go 的幂等写入要求 acks=all） |
| `allowAutoTopicCreation` | `false` | 允许 broker 自动建 topic。关闭且 topic 不存在时批次按毒批次丢弃（见 §7.3） |
| `authType` | `none` | `none` / `plain`；`plain` 必须填 `saslUsername`，密码可空 |
| `tlsEnabled` | `false` | 启用 TLS（最低 TLS 1.2） |
| `insecureSkipVerify` | `false` | 仅自签名证书的内网环境使用，要求 `tlsEnabled=true` |
| `timeoutSeconds` | `10` | 1–120；同时用于单次发送超时与 Produce 请求超时 |
| `workers` | `1` | 1–64。默认 1 保证同一设备的批次按入队顺序发送，调大可能乱序 |
| `spoolDisabled` / `spoolMaxBatches` | `false` / `100000` | 断网缓存开关与批次上限，与 HTTP/MQTT 同语义 |
| `format` / `mapping` | `native-json` | 见 §2，`kafka` 支持全部四种格式 |

### 7.2 分区与顺序

- 一条编码后的消息 = 一条 Kafka record。`native-json` / `group-json` 每批一条，
  `mapping-json` 的 `emit=record` 每批多条。
- record key 固定为**设备编码**（`device.code`），缺失时回落设备 ID。franz-go 的
  默认按键分区器把同一个 key 放到同一分区，因此同一设备的数据在 Kafka 侧可保序。
- `workers` 默认 1：同设备的多个批次也按入队顺序写入。调大后跨批次的顺序不再保证
  （分区内仍然有序）。
- 下游按设备消费时应使用 record key（或载荷里的 `deviceCode`）作为业务主键。

### 7.3 错误分类与补发

| 情况 | 处理 |
|---|---|
| 网络 / 超时 / leader 选举 / 分区迁移 | 可重试：标记断连，批次写入本地 outbox，恢复后补发 |
| `UNKNOWN_TOPIC_OR_PARTITION` 且自动创建关闭 | 毒批次：删除并计入 dropped，避免队头永久阻塞、缓存无限堆积 |
| 鉴权失败 / 非法 topic / 消息过大等不可重试错误 | 毒批次：删除并计入 dropped |
| `acks=none` | 写完即成功，**broker 的拒绝不可见**；生产环境推荐 `all` 或 `leader` |

与 HTTP/MQTT 一样是**至少一次**投递：补发可能产生重复，下游应按业务主键幂等。

### 7.4 连通性测试 vs 测试发送

- **测试连接**只对 seed broker 发一次 broker-only Metadata 请求（franz-go `Ping`），
  验证集群可达与 SASL/TLS 握手，**不校验 topic 是否存在、不触发自动创建**。
- **测试发送**用样例记录真的 produce 一条，topic 是否存在、鉴权与载荷是否被接受，
  以这个结果为准。
- **预览载荷**只编码、不发送，不需要 broker。
