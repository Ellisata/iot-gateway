# IoT 网关对外开放接口文档

> 本文档面向**第三方系统开发人员**，描述通过网关开放接口（`/openApi/*`）查询设备数据的方式。
> 接口均为只读查询，不提供任何写操作。

## 版本记录

| 版本 | 日期 | 变更内容 | 破坏性变更 |
| ---- | ---- | -------- | ---------- |
| v1.0 | 2026-08-27 | 首次发布：设备分页查询、设备地址分页查询 | - |
| v1.1 | 2026-09-07 | 新增：批量查询设备名称（4.2）、批量查询点位标签（4.3）；后续接口章节编号顺延 | - |
| v1.2 | 2026-10-09 | 新增：分批同步设备与点位（4.5，**游标翻页**，一次拿一批设备及其全部点位）；点位对象新增 `createdAt`/`updatedAt`（4.4 与 4.5 同时生效）；§3.3 补游标分页说明 | - |
| v1.3 | 2026-10-09 | 新增：设备可靠性统计（4.6）、点位异常 top-N（4.7）—— 报警口径的可用率与异常排行，供外部跨网关汇总 | - |
| v1.4 | 2026-10-10 | 4.6 的量纲调整：`availability` 由 0~1 小数改为**百分数 0~100**（保留两位小数），`downSeconds` / `mttrSeconds` 改为四舍五入的整秒 | ⚠️ 是（`availability` 量纲变了，与 0~1 时期对接过的调用方需改按百分数解析） |

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

> **例外：4.5 分批同步用「游标」而非 `page`/`size`。** 它的响应是
> `{size, hasMore, nextAfter, records}`，没有 `total`。全量遍历场景下，`OFFSET` 翻页
> 在遍历途中发生增删会让未读区间整体位移、造成漏读或重读；游标按有序主键续读不受影响。
> 详见 4.5。

### 3.4 时间格式

所有时间字段格式为 `yyyy-MM-dd HH:mm:ss`（服务器本地时间）。
特殊地，设备的 `lastSuccessTime` 为空字符串 `""` 时表示**尚无成功采集记录**。

### 3.5 幂等与调用频率

- 所有开放接口均为幂等的只读查询；
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

## 5. 错误码总表

开放接口可能返回的错误码如下（完整列表见本节，其他内部错误码不会出现在开放接口中）：

| code | HTTP | 含义 | 处理建议 |
| ---- | ---- | ---- | -------- |
| `0` | 200 | 成功 | - |
| `70003` | 401 | 开放接口密钥为空 | 请求头未携带 `X-Api-Key`，检查请求构造 |
| `70004` | 401 | 开放接口密钥无效 | 密钥错误或已被删除，联系管理员确认 |
| `PARAM_ERROR` | 200 | 参数校验失败 | 按 msg 提示修正请求参数（msg 形如 `参数校验失败: size max`） |
| `-1` | 200 | 系统错误 | 稍后重试，持续出现请联系管理员并查看网关日志 |

## 6. FAQ

**Q: 请求返回 401 但我的密钥是正确的？**
排查顺序：① 确认请求头名称为 `X-Api-Key`（HTTP 头名称大小写不敏感，写 `x-api-key` 也可以，但该头必须存在）；② 确认密钥值与下发的一致——**密钥值本身严格区分大小写**，注意复制时不要混入多余空格；③ 确认密钥未被管理员删除或重建。若仍失败，联系管理员。

**Q: 需要实时读取某个点位的当前值怎么办？**
当前开放接口仅提供设备与点位定义（配置层面）的查询；**实时数据**请通过网关的推送通道（如 MQTT）订阅获取，或联系管理员扩展开放接口能力。

**Q: 修改了点位配置多久生效？**
管理端修改设备/点位配置后，网关会在约 10 秒内热加载生效，随后查询结果即为最新配置。
