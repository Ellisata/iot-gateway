# IoT 网关对外开放接口文档

> 本文档面向**第三方系统开发人员**，描述通过网关开放接口（`/openApi/*`）查询设备数据的方式。
> 接口均为只读查询，不提供任何写操作。

## 版本记录

| 版本 | 日期 | 变更内容 | 破坏性变更 |
| ---- | ---- | -------- | ---------- |
| v1.0 | 2026-08-27 | 首次发布：设备分页查询、设备地址分页查询 | - |

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
http://<网关IP>:9081
```

> 端口以实际部署配置为准，默认 `9081`。

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
| protocolName | string | 协议名称（如 ModbusTcp、SiemensS7 等） |
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
     "http://192.168.1.100:9081/openApi/device/page?page=1&size=20&name=温度"
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
        "protocolName": "ModbusTcp",
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

### 4.2 分页查询设备地址（点位定义）

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

**示例**

```bash
curl -H "X-Api-Key: your-app-key" \
     "http://192.168.1.100:9081/openApi/deviceAddress/page?page=1&size=50&deviceId=a1b2c3d4e5f6"
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
        "status": 1
      }
    ]
  },
  "serverTime": "2026-08-27 10:30:05"
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
