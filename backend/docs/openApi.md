# IoT 网关对外开放接口文档

> 本文档面向**第三方系统开发人员**，描述通过网关开放接口（`/openApi/*`）查询设备数据、同步物模型配置的方式。
> 除 **4.8 控制下发**与 **4.13~4.15 物模型配置写入/删除**外，接口均为只读查询；
> 4.8 会改变现场设备状态，调用前请确认已获得操作授权；4.13~4.15 只修改网关内物模型配置，不触达现场设备。

## 版本记录

| 版本 | 日期 | 变更内容 | 破坏性变更 |
| ---- | ---- | -------- | ---------- |
| v1.0 | 2026-08-27 | 首次发布：设备分页查询、设备地址分页查询 | - |
| v1.1 | 2026-09-07 | 新增：批量查询设备名称（4.2）、批量查询点位标签（4.3）；后续接口章节编号顺延 | - |
| v1.2 | 2026-10-09 | 新增：分批同步设备与点位（4.5，**游标翻页**，一次拿一批设备及其全部点位）；点位对象新增 `createdAt`/`updatedAt`（4.4 与 4.5 同时生效）；§3.3 补游标分页说明 | - |
| v1.3 | 2026-10-09 | 新增：设备可靠性统计（4.6）、点位异常 top-N（4.7）—— 报警口径的可用率与异常排行，供外部跨网关汇总 | - |
| v1.4 | 2026-10-10 | 4.6 的量纲调整：`availability` 由 0~1 小数改为**百分数 0~100**（保留两位小数），`downSeconds` / `mttrSeconds` 改为四舍五入的整秒 | ⚠️ 是（`availability` 量纲变了，与 0~1 时期对接过的调用方需改按百分数解析） |
| v1.5 | 2026-10-31 | 新增 4.8 控制下发（写 PLC 点位）。**唯一会改变现场设备状态的开放接口**，受 `control.enabled`、协议 `Writable`、点位 `W` 权限三层约束 | - |
| v1.6 | 2026-11-02 | 4.8 写入权限收紧：绑定了物模型属性的点位，属性 `access_mode` 也必须含 `W`（新增错误码 `40039`）。`RW` 点位绑定 `R` 属性时不再允许写入 | ⚠️ 是（此前能写的「RW 点位 + R 属性」现在返回 `40039`） |
| v1.7 | 2026-11-06 | 新增：报警历史分页（4.9）、当前活跃报警（4.10）、越限记录分页（4.11）—— 与 4.6/4.7 的聚合报表互补，提供明细级查询 | - |
| v1.8 | 2026-11-06 | 新增：分批同步订阅设备与点位（4.12，游标翻页，形态与 4.5 一致；数据源是订阅报文维护的设备/点位元信息索引） | - |
| v1.9 | 2026-11-09 | 新增：物模型与属性批量 upsert（4.13，按 `code` / `identifier` 自然键创建或更新，跨模型部分成功） | - |
| v1.10 | 2026-11-09 | 新增：物模型批量删除（4.14）、物模型属性批量删除（4.15），按自然键逐条删除并返回逐条结果 | - |
| v1.11 | 2026-11-11 | 新增：配置同步（4.16）—— 能力协商、五域只读、推送通道/告警 Webhook/运行开关/设备点位批量写。读接口返回脱敏期望态与 `contentHash`，供管理平台做模板部署与漂移检测 | - |

## 兼容性承诺

- 已发布字段的**名称、类型、语义不会变更或删除**；
- 只做增量扩展：新增字段、新增可选请求参数、新增错误码；
- 新增字段不影响既有调用方解析（请集成方忽略未知字段而非报错）；
- 错误码一经对外发布只增不改。

## 1. 接入准备

### 1.1 获取访问密钥

请联系网关管理员。管理员在网关管理端「开放接口密钥」页面创建密钥后将 **AppKey** 提供给接入方。

### 1.2 安全须知

- ⚠️ 密钥通过 HTTP 请求头 `X-Api-Key` **明文传输**，本接口仅限**内网/专线环境**使用，请勿经公网暴露；
- 密钥请妥善保管，勿写入代码仓库或前端代码；
- 密钥泄露时请立即通知管理员重新生成。

## 2. 认证方式

所有开放接口均需在请求头中携带密钥：

```
X-Api-Key: <管理员分配的 AppKey>
```

| 场景 | 结果 |
| ---- | ---- |
| 未携带 `X-Api-Key` 头 | 返回 HTTP **401**，`code=70003`（密钥为空） |
| 密钥无效/不存在 | 返回 HTTP **401**，`code=70004`（密钥无效） |

> 说明：出于安全考虑，「密钥不存在」与「密钥错误」统一返回 70004，不做区分，
> 这是**有意设计**，以避免攻击者探测有效密钥，请勿反复试探。

## 3. 统一约定

### 3.1 基础地址

```
http://<网关IP>:9082
```

> 端口以实际部署配置为准，默认 `9082`。

### 3.2 统一响应信封

所有业务响应的 HTTP 状态码均为 `200`，通过响应体判断结果：

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": { ... },
  "serverTime": "2026-08-27 10:30:00"
}
```

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| code | string | 业务码，`"0"` 表示成功 |
| msg | string | 描述信息（当前随网关语言配置，通常为中文） |
| isSuccess | boolean | 是否成功，**建议以此为主要判断依据** |
| data | object | 业务数据，失败时为 `null` |
| serverTime | string | 服务器时间，格式 `yyyy-MM-dd HH:mm:ss` |

> ⚠️ 重要：业务失败（如参数错误、资源不存在）HTTP 状态码同样是 `200`，
> **唯一例外是认证失败返回 `401`**。请勿仅依赖 HTTP 状态码判断业务结果。

### 3.3 分页约定

分页查询接口遵循统一的请求/响应结构：

- 请求参数：`page`（页码，从 1 开始，必填）、`size`（每页条数，1~100，必填）；
- 响应 `data` 结构：

```json
{
  "page": 1,
  "size": 20,
  "total": 135,
  "records": [ ... ]
}
```

> **例外：4.5 / 4.12 分批同步用「游标」而非 `page`/`size`。** 它们的响应是
> `{size, hasMore, nextAfter, records}`，没有 `total`。全量遍历场景下，`OFFSET` 翻页
> 在遍历途中发生增删会让未读区间整体位移、造成漏读或重读；游标按有序主键续读不受影响。
> 详见 4.5 / 4.12。

### 3.4 时间格式

所有时间字段格式为 `yyyy-MM-dd HH:mm:ss`（服务器本地时间）。
特殊地，设备的 `lastSuccessTime` 为空字符串 `""` 时表示**尚无成功采集记录**。

### 3.5 幂等与调用频率

- 除 4.8 与 4.13~4.15 外，所有开放接口均为幂等的只读查询；
- 4.8 控制下发使用 `requestId` 做幂等：相同值重复调用返回首次结果，不重复写 PLC；
- 4.13 物模型 upsert 以 `code`（模型）与 `identifier`（属性）为自然键：重复提交同一份数据不会产生重复行，
  第二次只会把已有行更新为请求中的值；
- 4.14 / 4.15 删除已不存在的模型或属性时分别返回 `41002` / `41004`，**不会**视为成功；
  外部系统若按「确保不存在」重试，可把这两个码当作已删除处理；
- 控制下发受设备级/点位级每分钟写入次数限制，超限返回 `40038`，请退避后重试；
- 建议 `size` 按需取值、全量遍历时依次翻页；请勿高频轮询，一般场景下 5~30 秒轮询一次足够。

## 4. 接口列表

### 4.1 分页查询设备

获取接入网关的设备列表及其在线状态。

```
GET /openApi/device/page
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| page | int | 是 | 页码，≥1 |
| size | int | 是 | 每页条数，1~100 |
| name | string | 否 | 设备名称，模糊匹配 |

**响应字段（records 内元素）**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | string | 设备 ID（查询点位时使用） |
| name | string | 设备名称 |
| protocolName | string | 协议名称（如 `ModBus.TCP`、`Siemens.S7` 等，与网关「协议管理」页显示的协议名一致） |
| description | string | 设备描述 |
| status | int | 配置状态：`1`=启用，`0`=停用 |
| online | boolean | 当前是否在线（由采集引擎判定，长时间未成功采集视为离线） |
| lastSuccessTime | string | 最近一次成功采集时间；`""` 表示尚无成功记录 |
| createdAt | string | 创建时间 |
| updatedAt | string | 更新时间 |

> 注意：对外接口不返回 PLC 连接配置等敏感字段（protocolJson、内部 ID 等），此为有意裁剪。

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/device/page?page=1&size=20&name=温度"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 20,
    "total": 2,
    "records": [
      {
        "id": "a1b2c3d4e5f6",
        "name": "1号PLC",
        "protocolName": "ModBus.TCP",
        "description": "车间A 1号线 PLC",
        "status": 1,
        "online": true,
        "lastSuccessTime": "2026-08-27 10:29:58",
        "createdAt": "2026-08-01 09:00:00",
        "updatedAt": "2026-08-20 14:12:33"
      }
    ]
  },
  "serverTime": "2026-08-27 10:30:00"
}
```

### 4.2 批量查询设备名称

根据设备 ID 列表批量查询设备名称。适用于外部系统已持有设备 ID、需要展示名称的场景（如报表、大屏），
一次请求即可完成 ID → 名称 的批量映射，无需逐个调用 4.1 翻页查找。

```
POST /openApi/device/names
Content-Type: application/json
```

**请求参数（JSON Body）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| ids | string[] | 是 | 设备 ID 列表，至少 1 个（如 `["id1","id2"]`） |

**响应字段（data 为数组）**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | string | 设备 ID |
| name | string | 设备名称 |

> 说明：
> - 返回顺序与传入 `ids` 顺序一致；
> - **库中不存在的 ID 不会出现在返回结果里**（不报错、不占位），调用方需自行判断缺失；
> - 单次请求建议不超过 100 个 ID，超长列表请分批请求。

**示例**

```bash
curl -X POST -H "X-Api-Key: your-app-key" -H "Content-Type: application/json" \
     -d '{"ids":["a1b2c3d4e5f6","f6e5d4c3b2a1","not-exist-id"]}' \
     "http://192.168.1.100:9082/openApi/device/names"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [
    { "id": "a1b2c3d4e5f6", "name": "1号PLC" },
    { "id": "f6e5d4c3b2a1", "name": "2号PLC" }
  ],
  "serverTime": "2026-09-07 10:30:00"
}
```

### 4.3 批量查询点位标签

根据设备 ID 数组 + 点位 ID 数组批量查询点位标签（label）。适用于外部系统已持有设备 ID 与点位 ID、
需要展示点位中文说明的场景（如报表、大屏），一次请求即可完成 点位ID → 标签 的批量映射，
无需按设备逐个调用 4.1 / 4.4 翻页查找。

```
POST /openApi/deviceAddress/labels
Content-Type: application/json
```

**请求参数（JSON Body）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| deviceIds | string[] | 是 | 设备 ID 列表，至少 1 个 |
| addressIds | string[] | 是 | 点位 ID 列表，至少 1 个 |

> `deviceIds` 作归属校验：不属于这些设备的点位视为不存在。

**响应字段（data 为数组）**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | string | 点位 ID |
| deviceId | string | 所属设备 ID |
| deviceName | string | 所属设备名称；设备已被删除时为空串 `""` |
| label | string | 点位标签（地址名称的中文说明，如"温度"、"电流"） |

> 说明：
> - 返回顺序与传入 `addressIds` 顺序一致；
> - **库中不存在的点位 ID（或不属于传入设备的点位）不会出现在返回结果里**（不报错、不占位），调用方需自行判断缺失；
> - 单次请求建议 `deviceIds`、`addressIds` 各不超过 100 个，超长列表请分批请求。

**示例**

```bash
curl -X POST -H "X-Api-Key: your-app-key" -H "Content-Type: application/json" \
     -d '{"deviceIds":["a1b2c3d4e5f6"],"addressIds":["f6e5d4c3b2a1","0e9d8c7b6a5f","not-exist-id"]}' \
     "http://192.168.1.100:9082/openApi/deviceAddress/labels"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [
    { "id": "f6e5d4c3b2a1", "deviceId": "a1b2c3d4e5f6", "deviceName": "1号PLC", "label": "炉膛温度" },
    { "id": "0e9d8c7b6a5f", "deviceId": "a1b2c3d4e5f6", "deviceName": "1号PLC", "label": "主轴电流" }
  ],
  "serverTime": "2026-09-07 10:35:00"
}
```

### 4.4 分页查询设备地址（点位定义）

按设备查询其点位（地址）定义列表。依赖 4.1 返回的 `id` 作为 `deviceId`。

```
GET /openApi/deviceAddress/page
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| page | int | 是 | 页码，≥1 |
| size | int | 是 | 每页条数，1~100 |
| deviceId | string | 是 | 所属设备 ID（4.1 返回的 id） |
| name | string | 否 | 地址名称，模糊匹配 |

**响应字段（records 内元素）**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | string | 点位 ID |
| deviceId | string | 所属设备 ID |
| name | string | 地址名称 |
| label | string | 名称的中文说明（如"温度"、"电流"） |
| commonDataType | string | 通用数据类型名（与管理端展示一致） |
| dataType | string | 协议内部数据类型名（如 `modbus.bool`、`s7.int16`），驱动解码用 |
| rwPermission | string | 读写权限：`R`=只读，`W`=只写，`RW`=读写 |
| scanFrequency | int | 采集扫描频率，单位毫秒 |
| description | string | 点位描述 |
| status | int | 配置状态：`1`=启用，`0`=停用 |
| createdAt | string | 创建时间 |
| updatedAt | string | 更新时间（改配置即变，可用于判断"这个点位是不是刚改过"） |

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/deviceAddress/page?page=1&size=50&deviceId=a1b2c3d4e5f6"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 50,
    "total": 12,
    "records": [
      {
        "id": "f6e5d4c3b2a1",
        "deviceId": "a1b2c3d4e5f6",
        "name": "D100/temperature",
        "label": "炉膛温度",
        "commonDataType": "float32",
        "dataType": "modbus.float32",
        "rwPermission": "R",
        "scanFrequency": 1000,
        "description": "1号线炉膛温度",
        "status": 1,
        "createdAt": "2026-08-01 09:10:00",
        "updatedAt": "2026-08-20 14:12:33"
      }
    ]
  },
  "serverTime": "2026-08-27 10:30:05"
}
```

### 4.5 分批同步设备与点位

一次性拉取**全部设备及其点位定义**，用于外部系统的全量数据同步。每页返回一批设备，
**点位嵌套在设备内**，所以一台设备不会因为"暂无点位"而丢失。

```
GET /openApi/device/sync
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| after | string | 否 | **游标**：上一页响应里的 `nextAfter`；**首次请求不传** |
| size | int | 是 | 每页设备条数，1~100 |

**响应 `data`**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| size | int | 本次请求的 `size`（回显） |
| hasMore | boolean | 是否还有下一页 |
| nextAfter | string | 下一页游标；`hasMore` 为 `false` 时为空串 `""` |
| records | array | 本页设备列表，每个元素见下 |

**`records` 内元素**：先是 4.1 的全部设备字段（`id`/`name`/`protocolName`/`description`/
`status`/`online`/`lastSuccessTime`/`createdAt`/`updatedAt`，语义完全一致），外加：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| addresses | array | 该设备的全部点位；**无点位时是 `[]`（不是 `null`）**。每项字段与 4.4 的点位对象一致 |
| addresses[].id | string | 点位 ID |
| addresses[].deviceId | string | 所属设备 ID |
| addresses[].name | string | 地址名称（设备地址写法，如 `%MW100`） |
| addresses[].label | string | 名称的中文说明（如"温度"、"电流"） |
| addresses[].commonDataType | string | 通用数据类型名 |
| addresses[].dataType | string | 协议内部数据类型名（如 `modbus.float32`） |
| addresses[].rwPermission | string | `R` / `W` / `RW` |
| addresses[].scanFrequency | int | 采集扫描频率，单位毫秒 |
| addresses[].description | string | 点位描述 |
| addresses[].status | int | `1`=启用，`0`=停用 |
| addresses[].createdAt | string | 创建时间 |
| addresses[].updatedAt | string | 更新时间 |

**遍历方式**

```
after = ""                      # 首次不传
loop:
    resp = GET /openApi/device/sync?size=100[&after=<after>]
    process(resp.data.records)
    if not resp.data.hasMore: break
    after = resp.data.nextAfter
```

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/device/sync?size=2"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "size": 2,
    "hasMore": true,
    "nextAfter": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
    "records": [
      {
        "id": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
        "name": "1号PLC",
        "protocolName": "ModBus.TCP",
        "description": "车间A 1号线 PLC",
        "status": 1,
        "online": true,
        "lastSuccessTime": "2026-10-09 10:28:11",
        "createdAt": "2026-08-01 09:00:00",
        "updatedAt": "2026-08-20 14:12:33",
        "addresses": [
          {
            "id": "f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3",
            "deviceId": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
            "name": "D100",
            "label": "炉膛温度",
            "commonDataType": "float32",
            "dataType": "modbus.float32",
            "rwPermission": "R",
            "scanFrequency": 1000,
            "description": "1号线炉膛温度",
            "status": 1,
            "createdAt": "2026-08-01 09:10:00",
            "updatedAt": "2026-08-20 14:12:33"
          }
        ]
      },
      {
        "id": "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5",
        "name": "2号PLC（暂无点位）",
        "protocolName": "Siemens.S7",
        "description": "",
        "status": 1,
        "online": false,
        "lastSuccessTime": "",
        "createdAt": "2026-08-02 10:00:00",
        "updatedAt": "2026-08-02 10:00:00",
        "addresses": []
      }
    ]
  },
  "serverTime": "2026-10-09 10:30:00"
}
```

> **说明：**
> - **按设备 `id` 升序返回**（与 4.1 的 `created_at` 倒序不同）。`id` 是时间有序的 UUID，
>   因此遍历途中新增的设备会排在后面被同一次遍历读到，被删除的设备自然消失 —— **不漏读、不重读**。
> - **`after` 请原样回传上一页的 `nextAfter`**，不要自行拼造。`after` 不合法不会报错，
>   但会从"大于该值"的位置续读，可能跳过设备。
> - **停用（`status=0`）的设备与点位照常返回**，靠 `status` 字段自行取舍，与 4.1 / 4.4 一致。
> - **单页体积**：`size` 上限 100，一台设备的点位数不受限制。点位特别多的网关请把 `size` 调小。
> - 遍历结束的标志是 `hasMore` 为 `false`（此时 `nextAfter` 为 `""`），**不是**某页 `records` 变空。

### 4.6 设备可靠性统计

按统计窗口汇总每台设备的断联故障情况，输出可用率、故障段数、累计停机时长与 MTTR。
数据取自**报警口径**（去抖后落库的故障段，见下方说明）。

```
GET /openApi/alarm/deviceReliability
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| from | string | 是 | 统计起始日期，格式 `YYYY-MM-DD`，**含当天** |
| to | string | 是 | 统计截止日期，格式 `YYYY-MM-DD`，**含当天**（窗口实际到 `to` 次日 00:00:00） |
| limit | int | 否 | 只返回可用率最低的前 N 名，1~500 |

> - 窗口 **≤ 90 天**，超限返回 `PARAM_ERROR`；`to` 早于 `from`、或日期格式不对同样报错。
> - `from`/`to` **刻意必填、不给默认值** —— 统计口径不藏在服务端默认值里，由调用方对齐自己的统计周期。
> - **`limit` 省略或 ≤0 = 不截断（返回全部）**。这与 4.7 的 `limit` 默认语义**刻意相反**：设备是天然有限的维度，截断后的列表容易被误读成"全网设备清单"。想按其它字段（故障段数 / MTTR）自行重排的调用方，不传 `limit` 即可拿到全量。

**响应 `data`**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| gatewayId | string | 本网关 ID（跨网关汇总时用它标注每行归属） |
| gatewayName | string | 本网关名称 |
| records | array | 设备可靠性列表，按可用率升序（最差在前）；**无故障设备时为空数组 `[]`**（不是 `null`） |

**`records` 内元素**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| deviceId | string | 设备 ID |
| deviceName | string | 设备名称（名称快照，设备已删除仍有值） |
| episodes | int | 窗口内故障段数 |
| downSeconds | number | 窗口内累计停机秒数，**四舍五入到整秒**（跨窗口边界的段按重叠部分计入；未恢复的段算到窗口终点） |
| mttrSeconds | number | 平均修复时长（秒），**四舍五入到整秒**，只统计窗口内开始且已恢复的段；**无已完成故障时为 `-1`** |
| availability | number | 可用率**百分数（0~100）**，四舍五入到两位小数（如 `99.31` 即 99.31%）；**分母是整窗口** |
| windowSeconds | number | 窗口总秒数（供调用方按自己的分母重算可用率） |
| ranking | int | 按可用率升序的排名，1 = 最差 |

> **说明：**
> - **窗口内一次故障都没有的设备不会出现在结果里**（等价于可用率 100%）。结果集只覆盖"出过故障的设备"，可直接当"最差设备排行"用，但**不是**全量设备清单。
> - 带 `limit` 时 `ranking` 仍是**全局名次**（截断发生在排序之后），所以 `limit=10` 拿到的是"最差的前 10 台"。
> - **`availability` 是取整前的原始可用率排出来的名次**：`ranking` 与结果顺序按未取整的值定序，只有输出值保留两位小数。停机只差几秒的两台设备因此不会并列，名次也不会落在设备 ID 上。
> - **`availability` 的分母是整窗口，未扣除网关自身停机时间** —— 网关停机期间不轮询、不产生报警，那段时间在所有设备上都被算作在线。需要扣除的调用方请用 `windowSeconds` 自行重算（网关观测不到自己的停机区间）。
> - **口径是"报警口径"**：只有去抖后落库的故障段才计入（连续 N 轮采集失败才报），低于去抖阈值的抖动不产生报警、不计入。因此算出的可用率会略高于"真实"可用率 —— 它与网关的报警定义保持一致；与第三方 uptime 对比时差距就出在这里。

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/deviceReliability?from=2026-10-01&to=2026-10-09&limit=10"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "gatewayId": "gw-01",
    "gatewayName": "1号网关",
    "records": [
      {
        "deviceId": "a1b2c3d4e5f6",
        "deviceName": "1号PLC",
        "episodes": 3,
        "downSeconds": 5400,
        "mttrSeconds": 1800,
        "availability": 99.31,
        "windowSeconds": 777600,
        "ranking": 1
      }
    ]
  },
  "serverTime": "2026-10-09 21:00:00"
}
```

### 4.7 点位异常 top-N

按统计窗口汇总采集异常最突出的点位。**采集质量异常与超限分开计数**，不合并：前者要找运维（地址 / 解析问题），后者可能只是工艺波动。

```
GET /openApi/alarm/pointAbnormal
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| deviceId | string | 否 | 只看某台设备下的点位；不传 = 全部设备 |
| from | string | 是 | 统计起始日期 `YYYY-MM-DD`，含当天 |
| to | string | 是 | 统计截止日期 `YYYY-MM-DD`，含当天 |
| limit | int | 否 | 返回前 N 名，1~500；**省略或 ≤0 时服务端默认 20** |

> - 窗口规则与 4.6 相同（`from`/`to` 必填、格式校验、**≤ 90 天**）。
> - `deviceId` 与 `limit` 可一起用：只传 `deviceId` 拿该设备全部上榜点位，只传 `limit` 拿全厂前 N。
> - **`limit` 省略时的默认值是服务端的**（与 4.6 的"省略 = 不截断"刻意相反）—— 点位比设备多 1~2 个数量级，不兜底会返回长尾。

**响应 `data`**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| gatewayId | string | 本网关 ID |
| gatewayName | string | 本网关名称 |
| records | array | 点位异常列表，按异常段数降序；**无异常点位时为空数组 `[]`**（不是 `null`） |

**`records` 内元素**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| targetId | string | 点位 ID（即 4.4 / 4.5 返回的点位 `id`） |
| targetName | string | 「设备名.点位名」拼串，**直接用于展示**；是名称快照，设备删除后仍可读 |
| deviceId | string | 所属设备 ID（分组 / 过滤用这个） |
| qualityEpisodes | int | 窗口内采集质量异常段数 |
| thresholdEpisodes | int | 窗口内超限段数 |
| totalEpisodes | int | 两者之和 |
| lastOccurTime | string | 窗口内最近一次异常的首次发生时间；无异常时为空串 `""` |
| ranking | int | 按 `totalEpisodes` 降序的排名，1 = 最差 |

> **说明：**
> - `targetName` 是拼串，名字里含点号时会有歧义，**不要拆**它来取设备名 —— 要设备请用 `deviceId`。
> - `ranking` **不是速率**：采集频率高的点位单位时间内判定次数更多，抖动型点位因此更容易上榜；永久性坏点不受影响（边沿触发，永远只有一段）。

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/pointAbnormal?from=2026-10-01&to=2026-10-09&limit=20"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "gatewayId": "gw-01",
    "gatewayName": "1号网关",
    "records": [
      {
        "targetId": "f6e5d4c3b2a1",
        "targetName": "1号PLC.炉膛温度",
        "deviceId": "a1b2c3d4e5f6",
        "qualityEpisodes": 12,
        "thresholdEpisodes": 5,
        "totalEpisodes": 17,
        "lastOccurTime": "2026-10-08 14:22:03",
        "ranking": 1
      }
    ]
  },
  "serverTime": "2026-10-09 21:00:00"
}
```

### 4.8 控制下发（写 PLC 点位）

> ⚠️ 这是开放接口中**唯一会改变现场设备状态**的端点。调用方必须先获得操作授权，
> 并在每次下发前向操作人员确认设备处于可安全操作的状态。

**请求**

```
POST /openApi/control/write
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{
  "deviceId": "a1b2c3d4e5f6",
  "requestId": "b6f0b0c6-4d7e-4b6a-9f3a-2f6f1c0a9d11",
  "verify": true,
  "items": [
    { "deviceAddressId": "addr-1", "value": "42" }
  ]
}
```

| 字段 | 必填 | 说明 |
| ---- | ---- | ---- |
| `deviceId` | 是 | 目标设备 id |
| `requestId` | 建议 | 幂等键。相同值重复调用返回首次结果，不重复写 PLC；MQTT/重试场景必须提供 |
| `verify` | 否 | `true` 时写后回读校验（仅对有读权限的点位生效） |
| `items[].deviceAddressId` | 是 | **点位 id，不是 PLC 地址**；必须属于该设备、启用且 `rw_permission` 含 `W`；若该点位绑定了物模型属性，属性的 `access_mode` 也必须含 `W` |
| `items[].value` | 是 | 目标值的字符串形式，按点位数据类型由插件编码（bool 可用 `true`/`false`/`1`/`0`） |

单次最多 50 个点。**批量写不是事务**：每个点位独立返回结果。

**前置条件**（任一不满足即拒绝，不会触达设备）：

1. 网关 `control.enabled=true`；
2. 协议插件声明了 `Writable`；
3. 点位 `rw_permission` 含 `W`；
4. 若点位绑定了物模型属性，属性 `access_mode` 含 `W`（属性是业务语义上界，`R` 属性即使点位 `RW` 也只读）；
5. 设备当前在采集任务中且链路在线（本接口不触发重连）；
6. 未超过设备级/点位级写频率限制。

**响应**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "requestId": "b6f0b0c6-4d7e-4b6a-9f3a-2f6f1c0a9d11",
    "deviceId": "a1b2c3d4e5f6",
    "deviceName": "1号PLC",
    "protocolName": "ModBus.TCP",
    "duplicate": false,
    "results": [
      {
        "addressId": "addr-1",
        "addressName": "40001",
        "addressLabel": "温度",
        "dataType": "int16",
        "rwPermission": "RW",
        "value": "42",
        "resultValue": "42",
        "readbackValue": "42",
        "verified": true,
        "ok": true,
        "code": 0,
        "message": ""
      }
    ]
  },
  "serverTime": "2026-10-31 10:30:00"
}
```

- `results[].code`：`0` 成功、`1` 地址无效、`2` 只读、`3` 值非法、`4` 不支持、`5` 设备拒绝、`6` 链路失败/状态未知；
- `duplicate=true` 表示该 `requestId` 已处理过，本次未重复下发；
- 链路失败时整批状态可能未知，请回读点位或现场确认后再决定是否重试。

### 4.9 报警历史分页

分页查询网关落库的报警记录（断联 / 采集质量 / 超限 / 恢复）。与 4.6 / 4.7 的聚合报表互补：
那两条回答「谁最差」，本接口回答「具体是哪几条报警」。

```
GET /openApi/alarm/pageAlarm
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| page | int | 是 | 页码，从 1 开始 |
| size | int | 是 | 每页条数，1~100 |
| targetName | string | 否 | 目标名称，模糊匹配 |
| targetType | string | 否 | `device` / `channel` / `point` / `point_threshold`，省略 = 全部 |
| alarmType | string | 否 | `offline` / `quality` / `threshold` / `recover`，省略 = 全部 |
| status | string | 否 | `active` / `cleared`，省略 = 全部 |

**响应 `data`**

分页结构（见 §3.3），`records` 内元素：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | string | 报警 ID |
| targetId | string | 报警目标 ID（设备 / 通道 / 点位） |
| targetName | string | 目标名称（发生时快照，目标删除后仍可读） |
| targetType | string | `device` / `channel` / `point` / `point_threshold` |
| alarmType | string | `offline` / `quality` / `threshold` / `recover` |
| alarmTypeName | string | 报警类型的中文名称（如「断联报警」） |
| level | string | 报警级别 |
| content | string | 报警内容 |
| status | string | `active` / `cleared` |
| statusName | string | 状态的中文名称（「未恢复」/「已恢复」） |
| firstOccurTime | string | 首次发生时间 |
| lastOccurTime | string | 最近一次发生时间 |
| clearTime | string | 恢复时间；未恢复时为空串 `""` |
| createdAt | string | 创建时间 |
| updatedAt | string | 更新时间 |

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/pageAlarm?page=1&size=20&status=active&alarmType=offline"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 20,
    "total": 1,
    "records": [
      {
        "id": "0b6f4f8e-6f2e-4a8e-9d5c-2f7b0f3c1a11",
        "targetId": "a1b2c3d4e5f6",
        "targetName": "1号PLC",
        "targetType": "device",
        "alarmType": "offline",
        "alarmTypeName": "断联报警",
        "level": "critical",
        "content": "设备连续 3 轮采集失败",
        "status": "active",
        "statusName": "未恢复",
        "firstOccurTime": "2026-11-06 08:12:30",
        "lastOccurTime": "2026-11-06 08:13:00",
        "clearTime": "",
        "createdAt": "2026-11-06 08:12:30",
        "updatedAt": "2026-11-06 08:13:00"
      }
    ]
  },
  "serverTime": "2026-11-06 10:30:00"
}
```

### 4.10 当前活跃报警

返回当前**未恢复**的断联报警（离线设备 + 离线通道）。等价于「离线目标列表」，
不分页 —— 活跃目标是需要立即处理的那一小撮，分页只会让调用方多写一层循环。

```
GET /openApi/alarm/listActive
```

**请求参数**：无。

**响应 `data`**

报警对象数组，元素字段与 4.9 的 `records` 完全一致；按 `firstOccurTime` 倒序。
无活跃报警时为空数组 `[]`（不是 `null`）。

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/listActive"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [
    {
      "id": "0b6f4f8e-6f2e-4a8e-9d5c-2f7b0f3c1a11",
      "targetId": "a1b2c3d4e5f6",
      "targetName": "1号PLC",
      "targetType": "device",
      "alarmType": "offline",
      "alarmTypeName": "断联报警",
      "level": "critical",
      "content": "设备连续 3 轮采集失败",
      "status": "active",
      "statusName": "未恢复",
      "firstOccurTime": "2026-11-06 08:12:30",
      "lastOccurTime": "2026-11-06 08:13:00",
      "clearTime": "",
      "createdAt": "2026-11-06 08:12:30",
      "updatedAt": "2026-11-06 08:13:00"
    }
  ],
  "serverTime": "2026-11-06 10:30:00"
}
```

### 4.11 越限记录分页

分页查询越限样本明细。与 4.9 的**报警**是两件事：报警是去抖后的状态机（连续 N 轮
才报），本表按**原始越限**逐条留证（同一连续越限段内节流）—— 因此 1~2 轮的尖峰
只在这里看得到。

```
GET /openApi/alarm/pageThresholdRecord
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| page | int | 是 | 页码，从 1 开始 |
| size | int | 是 | 每页条数，1~100 |
| deviceId | string | 否 | 只看某台设备；省略 = 全部 |
| name | string | 否 | 点位名称，模糊匹配 |

**响应 `data`**

分页结构（见 §3.3），`records` 内元素：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| id | string | 记录 ID |
| deviceId | string | 所属设备 ID |
| deviceName | string | 设备名称（发生时快照） |
| addressId | string | 点位 ID（即 4.4 / 4.5 返回的点位 `id`） |
| addressName | string | 点位名称 |
| value | string | 触发越限的采集值 |
| kind | string | 值类型（bool / int / float / str） |
| bound | string | 命中的条件：`range` 时为 `high` / `low`，其余为算子名本身 |
| boundName | string | 命中条件的可读名（「超上限」/「低于下限」/「大于」等） |
| thresholdOp | string | 写入时的阈值算子快照 |
| thresholdValue | string | 写入时的操作数快照（**JSON 数组字符串**，如 `["80","100"]`） |
| occurredAt | string | 越限发生时间 |

> - `thresholdOp` / `thresholdValue` 是**写入时的快照**：改配置后历史记录仍可自证当时
>   用的是哪条规则。
> - 本表只保留最近 90 天，更早的记录会被清理。

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/alarm/pageThresholdRecord?page=1&size=20&deviceId=a1b2c3d4e5f6"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "page": 1,
    "size": 20,
    "total": 1,
    "records": [
      {
        "id": "5c2b1f0e-7d8a-4f21-b3e4-9a6c8d5e2f10",
        "deviceId": "a1b2c3d4e5f6",
        "deviceName": "1号PLC",
        "addressId": "f6e5d4c3b2a1",
        "addressName": "40001",
        "value": "105",
        "kind": "int",
        "bound": "high",
        "boundName": "超上限",
        "thresholdOp": "range",
        "thresholdValue": "[\"0\",\"100\"]",
        "occurredAt": "2026-11-06 09:41:12"
      }
    ]
  },
  "serverTime": "2026-11-06 10:30:00"
}
```

### 4.12 分批同步订阅设备与点位

一次性拉取**订阅报文里出现过的全部设备及其点位元信息**，用于外部系统的全量同步。
形态与 4.5 一致（游标分批、点位嵌套在设备内），但**数据源不同**：这里读的是网关的
订阅设备/点位索引（`mqtt_subscription_device_point`），由 MQTT 订阅报文解析结果或
管理端手工维护，**不是**本地 PLC 设备表。

因此本接口没有协议名、在线状态、采集时间等采集字段。

```
GET /openApi/subscription/devicePoint/sync
```

**请求参数（Query）**

| 参数 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| after | string | 否 | **游标**：上一页响应里的 `nextAfter`；**首次请求不传** |
| size | int | 是 | 每页设备条数，1~100 |

**响应 `data`**（与 4.5 同形）

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| size | int | 本次请求的 `size`（回显） |
| hasMore | boolean | 是否还有下一页 |
| nextAfter | string | 下一页游标；`hasMore` 为 `false` 时为空串 `""` |
| records | array | 本页设备列表，每个元素见下 |

**`records` 内元素**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| deviceId | string | 订阅设备 ID（报文解析出的设备身份） |
| deviceName | string | 设备名称；订阅/手工均未配置时为空串 `""` |
| points | array | 该设备的全部点位；**恒为非空数组**（设备由点位行反推而来） |

**`points` 内元素**

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| deviceId | string | 所属设备 ID（与父级相同，便于调用方展平） |
| deviceAddressId | string | 点位 ID |
| deviceAddressLabel | string | 点位标签（如"电流"、"电压"） |
| kind | string | 数据类型类别（如 `float`）；未配置时为空串 |
| source | string | 维护方式：`auto`=订阅报文自动收录，`manual`=手工新增/编辑/导入 |
| createdAt | string | 首次收录时间 |
| updatedAt | string | 元信息最近更新时间 |

**遍历方式**

```
after = ""                      # 首次不传
loop:
    resp = GET /openApi/subscription/devicePoint/sync?size=100[&after=<after>]
    process(resp.data.records)
    if not resp.data.hasMore: break
    after = resp.data.nextAfter
```

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9082/openApi/subscription/devicePoint/sync?size=2"
```

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "size": 2,
    "hasMore": false,
    "nextAfter": "",
    "records": [
      {
        "deviceId": "meter-1",
        "deviceName": "电表1",
        "points": [
          {
            "deviceId": "meter-1",
            "deviceAddressId": "current",
            "deviceAddressLabel": "A相电流",
            "kind": "float",
            "source": "auto",
            "createdAt": "2026-10-01 09:00:00",
            "updatedAt": "2026-10-01 12:00:00"
          },
          {
            "deviceId": "meter-1",
            "deviceAddressId": "voltage",
            "deviceAddressLabel": "电压",
            "kind": "float",
            "source": "manual",
            "createdAt": "2026-10-02 09:00:00",
            "updatedAt": "2026-10-02 09:00:00"
          }
        ]
      }
    ]
  },
  "serverTime": "2026-11-06 10:30:00"
}
```

> **说明：**
> - **按 `deviceId` 升序返回**，游标即上一页最后一条 `deviceId`。`after` 请原样回传
>   上一页的 `nextAfter`，不要自行拼造。
> - **一页按设备切分**：一台设备的所有点位都在同一页里，不会把设备切到两页。
> - **不存在「零点位设备」**：设备是从点位行反推出来的，没有点位就不会有这一行；
>   与 4.5 不同，`points` 不会是空数组。
> - **`source=manual` 的行**是运维手工确认过的元信息，后续订阅报文不会再覆盖它；
>   需要以自动收录为准时，管理端可把该行交还自动维护。
> - 遍历结束的标志是 `hasMore` 为 `false`（此时 `nextAfter` 为 `""`），**不是**某页
>   `records` 变空。

### 4.13 物模型与属性批量 upsert

按自然键批量创建或更新物模型及其属性：模型按 `code` 匹配，属性在模型内按 `identifier` 匹配；
**不存在则创建，存在则更新**。本接口只修改网关内的物模型配置，不触达现场设备，
也不会删除请求中未出现的模型或属性。

**请求**

```
POST /openApi/thingModel/batchUpsert
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{
  "models": [
    {
      "code": "th_sensor",
      "name": "温湿度传感器",
      "category": "sensor",
      "version": "1.0.0",
      "description": "车间环境传感器",
      "status": 1,
      "properties": [
        {
          "identifier": "temperature",
          "name": "温度",
          "kind": "float",
          "accessMode": "R",
          "unit": "℃",
          "description": "",
          "sortOrder": 10,
          "status": 1
        },
        {
          "identifier": "humidity",
          "name": "湿度",
          "kind": "float",
          "accessMode": "R",
          "unit": "%"
        }
      ]
    }
  ]
}
```

**请求字段**

| 字段 | 必填 | 说明 |
| ---- | ---- | ---- |
| `models` | 是 | 1~100 条模型 |
| `models[].code` | 是 | 模型标识（字母开头，仅字母数字下划线短横线，≤64）。**自然键，创建后不可改** |
| `models[].name` | 是 | 模型显示名（≤100 字符，全局唯一） |
| `models[].category` | 否 | 分类 code：内置 `sensor` / `meter` / `actuator` / `robot` / `plc` / `other`，或管理平台维护的自定义 code（小写字母开头，可含小写字母、数字、`_`、`-`，≤32）；省略 = 保持原值（新建时为未分类），显式 `""` = 未分类 |
| `models[].version` | 否 | ≤32 字符；省略 = 保持原值（新建时 `1.0.0`） |
| `models[].description` | 否 | ≤500 字符；省略 = 保持原值 |
| `models[].status` | 否 | `1` 启用 / `0` 停用；省略 = 保持原值（新建时 `1`） |
| `models[].properties` | 否 | 该模型下要同步的属性，最多 200 条；省略或空数组表示**不改动**该模型的属性 |
| `models[].properties[].identifier` | 是 | 属性标识符（字母开头，仅字母数字下划线，≤64）。**模型内自然键，创建后不可改** |
| `models[].properties[].name` | 是 | 属性显示名（≤100 字符） |
| `models[].properties[].kind` | 新建必填 | `bool` / `int` / `uint` / `float` / `string` / `time`；更新时省略 = 保持原值 |
| `models[].properties[].accessMode` | 新建必填 | `R` / `W` / `RW`；更新时省略 = 保持原值 |
| `models[].properties[].unit` | 否 | ≤32 字符；省略 = 保持原值 |
| `models[].properties[].description` | 否 | ≤500 字符；省略 = 保持原值 |
| `models[].properties[].sortOrder` | 否 | 0~99999；新建省略时取该模型当前最大值 +10，更新省略 = 保持原值 |
| `models[].properties[].status` | 否 | `1` 启用 / `0` 停用；省略 = 保持原值（新建时 `1`） |

**语义说明**

- **可选字段**：省略表示“不改动”；显式传空串表示清空（`category` 空串即未分类）。
  `kind` / `accessMode` 更新时显式传空串属于非法值。
- **事务边界是模型**：同一模型的元数据与全部属性在一个事务里提交；任一属性失败会回滚该模型
  （包括元数据），其它模型不受影响 —— 因此一批请求可能部分成功。
- **不删除**：请求中未出现的属性保持原样；本接口没有删除模型或属性的能力。
- **已绑定属性**：属性一旦被点位（`device_address.property_id`）绑定，`kind` / `accessMode`
  的实际变更会被拒绝（`41018`），需先在管理端解除绑定。
- **批次内重复**：同一批次重复出现同一 `code` 时，首次出现者生效，后续条目失败；
  同一模型内重复出现同一 `identifier` 时，整个模型失败。
- **名称唯一**：模型名称全局唯一，与其它模型撞名时该模型失败（`41001`）。
- `action=updated` 表示**匹配并同步成功**，即使本次提交的值与库中完全相同。

**响应**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "total": 1,
    "success": 1,
    "failed": 0,
    "created": 1,
    "updated": 0,
    "propertiesCreated": 2,
    "propertiesUpdated": 0,
    "models": [
      {
        "code": "th_sensor",
        "name": "温湿度传感器",
        "action": "created",
        "id": "0b1c2d3e4f5a",
        "properties": [
          { "identifier": "temperature", "action": "created", "id": "a1b2c3" },
          { "identifier": "humidity", "action": "created", "id": "d4e5f6" }
        ]
      }
    ]
  },
  "serverTime": "2026-11-09 10:30:00"
}
```

| 字段 | 说明 |
| ---- | ---- |
| `total` | 请求中的模型数 |
| `success` / `failed` | 成功 / 失败的模型数 |
| `created` / `updated` | 成功的模型中，新建 / 更新的数量 |
| `propertiesCreated` / `propertiesUpdated` | 成功模型中实际新建 / 更新的属性数 |
| `models[]` | 逐条结果，顺序与请求一致 |
| `models[].code` / `name` | 请求中的标识与名称 |
| `models[].action` | `created` / `updated` / `failed` |
| `models[].id` | 模型的网关内部 ID（失败时省略） |
| `models[].errorCode` / `errorMessage` | 失败原因（仅失败时出现） |
| `models[].properties[]` | 属性逐条结果；失败模型至多给出一条失败属性明细 |

> **注意**：批量中单个模型/属性失败**不会**把顶层响应变成错误信封 —— 顶层仍是 `code=0`、
> `isSuccess=true`，调用方必须检查 `data.failed` 与 `models[].action/errorCode`。

**错误码**：逐条错误见 §5 中 `41001` / `41003` / `41007` / `41008` / `41010` / `41018` / `10001`；
请求体格式错误、`models` 为空或超过 100 条时，顶层返回 `PARAM_ERROR`。

### 4.14 物模型批量删除

按模型标识（`code`）批量删除物模型。**删除模型会连带删除其全部属性**；本接口只修改网关内配置，
不触达现场设备。

**请求**

```
POST /openApi/thingModel/batchDelete
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{ "codes": ["th_sensor", "th_old"] }
```

| 字段 | 必填 | 说明 |
| ---- | ---- | ---- |
| `codes` | 是 | 模型标识数组，1~200 条；逐条 trim 后按 `code` 精确匹配 |

**语义说明**

- **删除保护**：模型已被设备绑定（`device.model_id`），或模型下仍有属性被点位绑定
  （`device_address.property_id`）时，该模型返回 `41005` 并保持原样。
- **逐条独立**：一个模型失败不影响其它模型；顶层响应始终是成功信封。
- **不存在**：模型不存在返回 `41002`（不视为成功）；外部系统若按「确保不存在」重试，
  可把 `41002` 当作已删除处理。
- **重复 code**：同一批次重复出现同一 `code` 时，第一条删除成功后，后续条目因不存在返回 `41002`。
- 请求体格式错误、`codes` 为空或超过 200 条时，顶层返回 `PARAM_ERROR`。

**响应**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "requested": 2,
    "deleted": 1,
    "failed": 1,
    "results": [
      { "code": "th_sensor", "action": "deleted" },
      { "code": "th_old", "action": "failed", "errorCode": "41002", "errorMessage": "物模型不存在" }
    ]
  },
  "serverTime": "2026-11-09 11:00:00"
}
```

| 字段 | 说明 |
| ---- | ---- |
| `requested` / `deleted` / `failed` | 请求条数 / 实际删除数 / 失败数 |
| `results[].code` | 请求中的模型标识 |
| `results[].action` | `deleted` / `failed` |
| `results[].errorCode` / `errorMessage` | 失败原因（仅失败时出现） |

### 4.15 物模型属性批量删除

按（模型标识 `modelCode` + 属性标识符 `identifier`）批量删除物模型属性。**只删除指定的属性**，
不影响同模型的其它属性；本接口只修改网关内配置，不触达现场设备。

**请求**

```
POST /openApi/thingModelProperty/batchDelete
X-Api-Key: <AppKey>
Content-Type: application/json
```

```json
{
  "items": [
    { "modelCode": "th_sensor", "identifier": "temperature" },
    { "modelCode": "th_sensor", "identifier": "humidity" }
  ]
}
```

| 字段 | 必填 | 说明 |
| ---- | ---- | ---- |
| `items` | 是 | 1~200 条 |
| `items[].modelCode` | 是 | 所属模型的标识 `code` |
| `items[].identifier` | 是 | 属性标识符 |

**语义说明**

- **删除保护**：属性已被点位绑定（`device_address.property_id`）时返回 `41006` 并保持原样。
- **不存在**：模型不存在返回 `41002`，属性不存在返回 `41004`（均不视为成功）。
- **逐条独立**：一条失败不影响其它条目；顶层响应始终是成功信封。
- **重复条目**：同一批次重复删除同一属性时，第一条成功后，后续条目因不存在返回 `41004`。
- 请求体格式错误、`items` 为空或超过 200 条时，顶层返回 `PARAM_ERROR`。

**响应**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "requested": 2,
    "deleted": 2,
    "failed": 0,
    "results": [
      { "modelCode": "th_sensor", "identifier": "temperature", "action": "deleted" },
      { "modelCode": "th_sensor", "identifier": "humidity", "action": "deleted" }
    ]
  },
  "serverTime": "2026-11-09 11:00:00"
}
```

| 字段 | 说明 |
| ---- | ---- |
| `requested` / `deleted` / `failed` | 请求条数 / 实际删除数 / 失败数 |
| `results[].modelCode` / `identifier` | 请求中的自然键 |
| `results[].action` | `deleted` / `failed` |
| `results[].errorCode` / `errorMessage` | 失败原因（仅失败时出现） |

### 4.16 配置同步（管理平台统一配置管理）

> 本节接口供管理平台把配置模板下发到网关。**只改网关配置，不触达现场设备**。
> 读接口返回的 `payload` 就是管理平台应保存的期望态；密钥字段脱敏为 `***`。
> 写入接口逐条返回结果，顶层信封恒为成功（`code=0`），调用方必须检查 `items[]`。

#### 4.16.1 能力协商

```
GET /openApi/config/capabilities
```

返回 `cfgSyncVersion`、`openApiVersion`、`gatewayVersion`、`hashVersion`、
`hashAlgorithm` 与各配置域的 `read/upsert/delete/update/reset/maxBatch`。
管理平台必须先调用本接口：`hashVersion` 不一致时禁止做漂移判断。

#### 4.16.2 只读接口

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| GET | `/openApi/thingModel/page` | 物模型 + 属性 + `contentHash` |
| GET | `/openApi/deviceConfig/page` | 设备 + 采集组 + 点位 + `contentHash`，页大小上限 20 |
| GET | `/openApi/pushChannel/page` | 推送通道，`config` 已脱敏 |
| GET | `/openApi/pushChannel/types` | 通道类型元数据 |
| GET | `/openApi/pushChannel/forms` | 通道动态表单 |
| GET | `/openApi/alarmWebhook/page` | 告警 Webhook，`secret` 已脱敏 |
| GET | `/openApi/alarmWebhook/types` | Webhook 类型元数据 |
| GET | `/openApi/alarmWebhook/forms` | Webhook 动态表单 |
| GET | `/openApi/systemConfig/list` | 运行开关当前值 |
| GET | `/openApi/config/protocols` | 协议与点位数据类型 |

分页接口使用 `page/size`，返回 `{page,size,total,records}`；每条记录为
`{targetKey,payload,contentHash}`。

#### 4.16.3 写入接口

| 方法 | 路径 | 自然键 | 说明 |
| ---- | ---- | ------ | ---- |
| POST | `/openApi/thingModel/batchUpsert` | `code` / `identifier` | 已有接口；可选 `replaceProperties=true` 删除未出现属性 |
| POST | `/openApi/thingModel/batchDelete` | `code` | 已有接口 |
| POST | `/openApi/pushChannel/batchUpsert` | `type/name` | `config` 为对象；`"***"` 保留现有密钥 |
| POST | `/openApi/pushChannel/batchDelete` | `type/name` | 逐条删除 |
| POST | `/openApi/alarmWebhook/batchUpsert` | `name` | `secret` 为 `"***"` 时保留现有值 |
| POST | `/openApi/alarmWebhook/batchDelete` | `name` | 逐条删除 |
| POST | `/openApi/systemConfig/batchUpdate` | `key` | 批量修改运行开关 |
| POST | `/openApi/systemConfig/batchReset` | `key` | 批量恢复默认 |
| POST | `/openApi/deviceConfig/batchUpsert` | `code` 或 `name` | 设备 + 采集组 + 点位；`mode=replace` 删除未出现点位/组 |
| POST | `/openApi/deviceConfig/batchDelete` | `code` 或 `name` | 逐条删除，保留触发规则/推送通道引用保护 |

单批上限：物模型/推送通道/告警 Webhook 100，运行开关 50，设备 20。
单台设备点位上限 500、采集组上限 100。

#### 4.16.4 contentHash 与密钥保留

- 算法：`sha256-canonical-json-v1`。对象键排序、数字规范化、HTML 不转义；
  密钥字段（`password/secret/token/apiKey/...`）只计存在性，不计值。
- 密钥占位符 `"***"`：写入时表示保留网关现有值（支持 `config`/`protocolJson`
  的嵌套路径与数组下标对齐）；读接口永不返回真实密钥。
- 因此「导入 → 再次下发」即使拿不到密钥，也不会把网关密钥覆盖成 `***`。
- 已知限制：只修改密钥值不会产生漂移（hash 不计值）。

#### 4.16.5 应用顺序

管理平台按 `运行时开关 → 推送通道 → 告警 Webhook → 物模型 → 设备/点位`
的顺序下发。设备/点位依赖物模型与协议，顺序反了会因子键不存在而失败。

#### 4.16.6 错误码

`42001-42009` 为配置同步专用错误码：不支持的配置类型、协议不存在、
物模型不存在/停用、模型变更缺点位、协议变更缺全部点位、采集组不存在、
hash 版本不支持、批量超限、设备配置不合法。逐条结果中的 `errorCode`
以网关实际返回为准。

## 5. 错误码总表

开放接口可能返回的错误码如下（完整列表见本节，其他内部错误码不会出现在开放接口中）：

| code | HTTP | 含义 | 处理建议 |
| ---- | ---- | ---- | -------- |
| `0` | 200 | 成功 | - |
| `70003` | 401 | 开放接口密钥为空 | 请求头未携带 `X-Api-Key`，检查请求构造 |
| `70004` | 401 | 开放接口密钥无效 | 密钥错误或已被删除，联系管理员确认 |
| `PARAM_ERROR` | 200 | 参数校验失败 | 按 msg 提示修正请求参数（msg 形如 `参数校验失败: size max`） |
| `40031` | 200 | 控制下发功能未启用 | 请联系管理员确认 `control.enabled` |
| `40032` | 200 | 该协议不支持控制下发 | 该协议插件未声明 `Writable`，只能读 |
| `40033` | 200 | 该点位没有写权限 | 点位 `rw_permission` 需为 `W` 或 `RW` |
| `40034` | 200 | 点位不存在或不属于该设备 | 核对 `deviceAddressId` 与 `deviceId` |
| `40035` | 200 | 设备不在采集或链路离线 | 等设备恢复在线后重试 |
| `40036` | 200 | 控制下发参数不合法 | 按 msg 修正，如点位重复、超过 50 个、时间格式错误 |
| `40037` | 200 | 控制下发失败（链路错误，状态未知） | 先回读/现场确认，再决定是否换 `requestId` 重试 |
| `40038` | 200 | 控制下发过于频繁 | 按设备级/点位级限流退避后重试 |
| `40039` | 200 | 该点位绑定的物模型属性为只读 | 点位绑定的属性 `access_mode` 需为 `W` 或 `RW`；改属性模式或解绑后重试 |
| `10001` | 200 | 物模型/属性条目参数校验失败（逐条） | 按 `models[].errorMessage` 修正名称、枚举值、长度或数量上限 |
| `41001` | 200 | 物模型名称已存在，或批次内 `code` 重复（逐条） | 模型名称全局唯一；换一个名称，或修正批次内重复的 `code` |
| `41003` | 200 | 批次内属性标识符重复（逐条） | 同一批次内同一模型的 `identifier` 只能出现一次 |
| `41002` | 200 | 物模型不存在（逐条） | 模型可能已被删除；按「已删除」处理或核对 `code` |
| `41004` | 200 | 物模型属性不存在（逐条） | 属性可能已被删除；核对 `modelCode` 与 `identifier` |
| `41005` | 200 | 物模型已被设备绑定或其属性已被点位绑定，不能删除（逐条） | 先解绑设备/点位，或保留该模型 |
| `41006` | 200 | 属性已被点位绑定，不能删除（逐条） | 先解除点位绑定再删除 |
| `41007` | 200 | 物模型标识不合法（逐条） | 字母开头，仅字母数字下划线短横线，≤64 |
| `41008` | 200 | 属性标识符不合法（逐条） | 字母开头，仅字母数字下划线，≤64 |
| `41010` | 200 | 物模型分类格式不合法（逐条） | 空串，或小写字母开头、仅含小写字母/数字/`_`/`-`、≤32 位的 code（内置 `sensor/meter/actuator/robot/plc/other` 同样合法） |
| `41018` | 200 | 属性已被点位绑定，不能修改数据类型或读写模式（逐条） | 先在管理端解除点位绑定，再修改 `kind`/`accessMode` |
| `-1` | 200 | 系统错误 | 稍后重试，持续出现请联系管理员并查看网关日志 |

## 6. FAQ

**Q: 请求返回 401 但我的密钥是正确的？**
排查顺序：① 确认请求头名称为 `X-Api-Key`（HTTP 头名称大小写不敏感，写 `x-api-key` 也可以，但该头必须存在）；② 确认密钥值与下发的一致——**密钥值本身严格区分大小写**，注意复制时不要混入多余空格；③ 确认密钥未被管理员删除或重建。若仍失败，联系管理员。

**Q: 需要实时读取某个点位的当前值怎么办？**
当前开放接口仅提供设备与点位定义（配置层面）的查询；**实时数据**请通过网关的推送通道（如 MQTT）订阅获取，或联系管理员扩展开放接口能力。

**Q: 修改了点位配置多久生效？**
管理端修改设备/点位配置后，网关会在约 10 秒内热加载生效，随后查询结果即为最新配置。
