# IoT 网关 API 能力清单（供 dsh 调用）

> 本文档面向 **dsh（AI Agent / 工具调用方）**，把网关当前全部 HTTP 接口按
> **只读**与**编辑**两类整理清楚，供 Agent 选取工具、拼装参数、判断副作用。
>
> 与同目录 [openApi.md](openApi.md) 的分工：那份是给**第三方系统开发者**看的对外契约
> （只覆盖 `/openApi/*` 六个只读接口，带兼容性承诺）；本文是**全量台账**，
> 覆盖网关所有接口，含内部管理接口。两者受众不同，不要互相替代。
>
> 本文件**不参与 `go:embed`**（[docs.go](docs.go) 只嵌入 openApi.md / openApi.en.md），
> 编辑它不影响二进制产物。

## 版本记录

| 版本 | 日期 | 变更内容 |
| ---- | ---- | -------- |
| v1.0 | 2026-09-30 | 首次盘点：按只读 / 编辑两类整理网关全部接口 |
| v1.1 | 2026-09-30 | 新增 dsh 专用无鉴权通道 `/agentApi/*`（82 条，见 §0）；三条认证通道的决策规则随之改写 |
| v1.2 | 2026-09-30 | 新增 `GET /agentApi/__surface` 机器可读分类清单（read/probe/write，见 §0.1），dsh 靠它判断是否需人确认 |
| v1.3 | 2026-10-06 | `__surface` 每条新增 `params` 参数表（由反射从控制器绑定的 DTO 导出，184 条参数），dsh 不再需要按字段名猜参数；本文 §0.1 与 §4.1 的参数列以 `__surface` 为准 |
| v1.4 | 2026-10-09 | 开放接口新增两条可靠性查询（`/openApi/alarm/deviceReliability`、`/openApi/alarm/pointAbnormal`），§3.0 开放接口由 4 条增至 6 条 |

---

## 0. 先读这一段：Agent 该走哪条认证通道

网关有**三条互不相通**的调用通道，选错通道是最常见的失败原因：

| 通道 | 覆盖范围 | 认证方式 | Agent 友好度 |
| ---- | -------- | -------- | ------------ |
| **dsh 专用** `/agentApi/*` | **全部 82 个领域接口**（含全部写操作） | **无任何鉴权** | ★★★ 直接可用 |
| **开放接口** `/openApi/*` | 仅 6 个只读接口 | 请求头 `X-Api-Key` | ★★★ 直接可用 |
| **管理接口**（根路径） | 只读 + 全部编辑能力 | `Authorization: Bearer <JWT>` | ★ 需先完成 RSA-OAEP 登录 |

**决策规则：**

- **dsh 一律走 `/agentApi/*`** —— 它是为 dsh 专门准备的整组镜像，覆盖内部接口的
  **全部**能力（82 条，与根路径一一对应），**不需要任何凭据**。
- 只在 `/agentApi` 尚未覆盖某个新接口时，才需要回落到 JWT 通道。

### 0.1 先调清单端点，拿到每个接口的分类与参数

**dsh 接入的第一步是调这个接口**，不要靠 HTTP 方法猜只读/可写：

```
GET /agentApi/__surface        # 无需任何凭据
```

它返回 **83 条**接口的分类（82 个领域接口 + 清单自身）**与逐条参数表**：

```json
{
  "code": "0", "isSuccess": true,
  "data": [
    {
      "method": "GET",
      "path": "/agentApi/device/deleteDevice/:id",
      "access": "write",
      "confirm": true,
      "params": [],
      "note": "**用 GET 实现**，按方法判只读会误删。连带删掉该设备全部点位"
    },
    {
      "method": "POST",
      "path": "/agentApi/deviceAddress/createDeviceAddress",
      "access": "write",
      "confirm": true,
      "params": [
        {
          "name": "name", "in": "body", "type": "string",
          "required": true, "minLength": 1, "maxLength": 100,
          "desc": "**设备地址写法**（如 %MW100、DB1.DBW100、D100）…"
        },
        {
          "name": "rwPermission", "in": "body", "type": "string",
          "required": true, "enum": ["R", "W", "RW"]
        }
      ]
    }
  ]
}
```

**`params` 是本端点最有价值的部分 —— 拼请求前先读它，不要按字段名猜。**

它由后端用反射从**控制器真正绑定的那个 DTO** 导出（[agentApiParams.go](../router/agentApiParams.go)），
所以与网关的实际校验规则同源，不会与代码漂移。字段含义：

| 字段 | 含义 |
| ---- | ---- |
| `in` | `path` 替换 URL 里的 `:name`；`query` 拼在 URL 上；`body` 放进 JSON 请求体；`form` 走 multipart |
| `type` | `string` / `int` / `number` / `bool` / `object` / `file` / `[]object` 等 |
| `required` | 必填。`false` 不表示"可以传 null"——是否允许 null 见 `desc` |
| `enum` | 取值白名单，来自 `binding:"oneof=…"`。**不在表里就是非法值** |
| `minimum`/`maximum` | 数字的取值范围 |
| `minLength`/`maxLength` | 字符串的长度范围 |
| `minItems`/`maxItems` | 数组的元素个数范围 |
| `format` | `email` / `uri` 等格式要求 |
| `desc` | 人类可读说明。**只写光看字段名看不出来的事**，例如"空串 = 清空"这类陷阱 |
| `fields` | 嵌套对象的子字段（如 `items[]`）。取 `desc` 时用点号路径理解层次 |

几条最容易踩的，都写在对应参数的 `desc` 里，这里再点一遍：

- **`page`/`size` 在 `in: "query"`**，不是 body（删点位分页、设备分页等都一样）；
- **`rwPermission` 只接受 `R` / `W` / `RW`**（`enum` 里有），写 `read` 会被拒；
- **`updateAlarmWebhook` 的 `secret`/`atAll`/`atList`/`status` 是指针语义**：
  省略或传 `null` = 不改，传 `""`/`false`/`0` = **清空**；
- **上传类接口（导入设备/点位/插件）是 `in: "form"` 的 multipart，没有 JSON body**，
  字段名各不相同（`file` / `files`），以 `params` 里的名字为准；
- **`deviceScan/createDevices` 的 `items[].index` 是任务快照里 `devices` 数组的下标**，
  不是设备 id —— 见该字段的 `desc`。

`in: "path"` 的参数由路由串自动导出，永远与路径里的 `:占位符` 一一对应。

`access` 三档，**`confirm` 由它推导**（除 `read` 外都为 `true`）：

`access` 三档，**`confirm` 由它推导**（除 `read` 外都为 `true`）：

| access | 条数 | 含义 | dsh 该怎么做 |
| ------ | ---- | ---- | ------------ |
| `read` | 38 | 不改网关任何状态，无对外副作用，代价可忽略 | 直接调 |
| `probe` | 6 | 不改状态，但**有真实代价或对外副作用** | **先要人确认** |
| `write` | 39 | **改变网关状态**（持久化数据，或启停/热刷新运行行为） | **先要人确认** |

**为什么不能按 HTTP 方法推**——本项目里方法完全不可靠：

| 反例 | 方法 | 实际 |
| ---- | ---- | ---- |
| `GET /agentApi/device/deleteDevice/:id` | GET | **删数据** |
| `POST /agentApi/device/testDeviceConnection` | POST | 不改状态，但真实连一次 PLC |
| `POST /agentApi/alarmWebhook/testSend` | POST | 不改状态，但**真的往钉钉/飞书发一条消息** |

`probe` 这 6 条是全部清单里最容易被误判的一组，完整列出：

| 接口 | 代价 |
| ---- | ---- |
| `POST /agentApi/alarmWebhook/testSend` | 真的对外发消息，群里会有人看见 |
| `POST /agentApi/deviceScan/start` | 扫本机网段，重且慢，同时只允许一个任务 |
| `POST /agentApi/deviceScan/discoverPoints` | 同步阻塞最长约 7s，且占用该设备的采集会话锁 |
| `POST /agentApi/deviceScan/cancel` | 取消在跑的扫描 |
| `POST /agentApi/device/testDeviceConnection` | 真实连接一次 PLC |
| `POST /agentApi/pushChannel/testConnectivity` | 真实连接下游（MQTT 等） |

> **清单是端点而不是静态文件**，是为了让 dsh 拿到的分类永远与**正在跑的那个网关**
> 一致：路由变了清单跟着变，没有「文件忘了同步」这种失败模式。
> 分类表在 [router/agentApiSurface.go](../router/agentApiSurface.go)，
> 完整性由 `TestAgentApiSurfaceIsComplete` 守住（新增路由没登记、或登记了不存在的
> 路由，都会让测试失败）。

> ⚠️ **网关只提供分类，不强制确认。** 它无法验证 dsh 是否真的问过人
> —— 所以这是**给 dsh 遵守的契约，不是服务端的安全控制**。
> 是否真的先问人，取决于 dsh 侧的实现。

### ⚠️ `/agentApi/*` 无鉴权 —— 这是刻意的设计选择

任何能访问到网关端口的主机都可以调用 `/agentApi` 下的**全部**接口，其中包含：

| 接口 | 后果 |
| ---- | ---- |
| `POST /agentApi/plugin/importPlugin` | 上传并加载可执行文件 —— **等价于远程代码执行** |
| `GET /agentApi/device/deleteDevice/:id` | 删除设备 —— **静默停掉产线采集** |
| `POST /agentApi/device/batchDeleteDevice` | 批量删除设备，连带删掉其全部点位 |
| `GET /agentApi/device/pageDevice` | 返回的 `DeviceVO` **带 `protocolJson`**（PLC 连接配置） |
| `GET /agentApi/pushChannel/*` | `configJson` 含 MQTT 口令等凭据 |
| `POST /agentApi/user/createUser`、`PUT /agentApi/user/password` | 账号管理 |

**这个分组只在网关处于隔离工业内网、或 dsh 与网关同机时才成立。**
若网关会暴露到不可信网络，必须先给它加回鉴权。

**收回这个口子的方式**（无需改动任何路由注册）：
删掉 [middleware/authMiddleware.go](../middleware/authMiddleware.go) 里的
`agentApiPrefix` 常量与 `isPublicPath` 中对应的那一行 `HasPrefix` 判断即可
—— `/agentApi` 下 82 条路由会立刻全部要求 JWT。
设计取舍见 [router/agentApiRouter.go](../router/agentApiRouter.go) 文件头注释。

**两个分组都是显式白名单**（`/openApi` 与 `/agentApi` 各自如此）：
新增领域路由不会自动出现在 `/agentApi` 下，必须在 `AgentApiRoutes` 里显式追加。
`backend/agentApiRoutes_test.go` 的 `TestAgentApiMirrorsInternalRoutes` 会守住这件事
——漏挂会让测试直接失败并点出缺了哪几条。

---

## 1. 通用调用约定

### 1.0 无需认证的接口

白名单见 [middleware/authMiddleware.go](../middleware/authMiddleware.go) 的
`isPublicPath`。注意三者**免认证的性质并不相同**：

| 方法 | 路径 | 用途 | 免认证的性质 |
| ---- | ---- | ---- | ---- |
| GET | `/health` | 健康检查，返回 `{"status":"ok"}`（**裸 JSON，非统一信封**） | 真的无凭据 |
| POST | `/user/login` | 登录换取 JWT（**账号密码需 RSA 加密**，见 §2.2） | 真的无凭据（登录本身不能要求已登录） |
| GET | `/admin/*` | 内嵌 Vue 管理前端静态资源（SPA 回退 index.html） | 真的无凭据（只是静态文件） |
| **ALL** | **`/agentApi/*`** | **dsh 调用面，82 条接口** | **真的无凭据 —— 风险最高，见 §0** |
| ALL | `/openApi/*` | 对外只读接口 | **免的只是 JWT，仍要 `X-Api-Key`** |

除上表外，其余接口都需要 JWT。

### 1.1 基础地址

```
http://<网关IP>:9082
```

> 端口以实际部署配置为准（配置项 `server.port`，见 [default.yaml](../default.yaml)）。
> 出厂默认是 `9082` —— 它在 2026-09-16 的提交 `33ab028` 里从 `9081` 改过来，
> [openApi.md](openApi.md) / [openApi.en.md](openApi.en.md) 已同步更正。

### 1.2 统一响应信封

所有业务接口（**除 §1.6 的文件下载接口外**）HTTP 状态码恒为 `200`，
**必须靠响应体里的 `code` / `isSuccess` 判断成败**：

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": { },
  "serverTime": "2026-09-30 10:30:00"
}
```

- `code` 是**字符串**（`"0"` 表示成功，`"-1"` 表示系统错误），不是数字。
- 失败时 `isSuccess=false`、`data=null`、`code` 为 §5 中的业务错误码。

**唯二的例外**（HTTP 状态码非 200）：

- 未携带 / 令牌无效 → HTTP **401**，`code=20006` / `20005`；
- 未携带 / 无效 `X-Api-Key` → HTTP **401**，`code=70003` / `70004`。

### 1.3 分页约定

分页响应统一是 `PageVo` 结构：

```json
{ "page": 1, "size": 20, "total": 137, "records": [ ] }
```

请求参数统一是 `page`（从 1 开始，`min=1`）与 `size`（`min=1,max=100`），
**两者在几乎所有分页接口中都是必填**（缺省会报参数校验错误 `10001`）。
例外：`GET /log/listFiles` 的 `page`/`size` 可省略（服务端兜底 1/20）。

**另有 `CursorVo` 游标分页**（keyset，与 `PageVo` 并列）：`{size, hasMore, nextAfter, records}`，
**没有 `total`**，用 `after` 游标而非 `page` 翻页。目前只有 `GET /openApi/device/sync` 用
（[openApi.md](openApi.md) 4.5）—— 全量遍历时 `OFFSET` 会在增删下漏读/重读，游标不会。

### 1.4 时间格式

所有时间字段为字符串 `YYYY-MM-DD HH:mm:ss`（本地时区）。
登录令牌的 `expiresAt` 例外，用 RFC3339。

### 1.5 语言（i18n）

可选请求头 `area`，取值 `zh`（默认） / `en` / `zh-hk`，只影响**错误消息文案**，
不影响数据结构。

### 1.6 非标准响应的接口（Agent 需特别处理）

以下两个接口**不返回 JSON 信封**，直接返回文件流，
Agent 的响应解析器必须对它们特判（否则会因解析 JSON 失败而报错）：

| 接口 | 返回 |
| ---- | ---- |
| `GET /device/importTemplate` | `.xlsx` 文件流（`Content-Disposition: attachment`） |
| `GET /deviceAddress/importDeviceAddressTemplate` | `.xlsx` 文件流 |

另有一个**响应体是 HTML 而非 JSON 对象**的接口（仍包在标准信封的 `data` 里）：
`GET /openApiSecret/doc`，`data` 为服务端渲染好的 HTML 字符串。

---

## 2. 认证

### 2.1 开放接口：`X-Api-Key`

```
X-Api-Key: <管理员分配的 AppKey>
```

仅适用于 `/openApi/*`。AppKey 由管理员在管理端「开放接口密钥」页面创建
（对应本文 §3.1 的 `/openApiSecret/*` 接口）。

### 2.2 管理接口：JWT（**登录需 RSA 加密**）

这是 Agent 接入的最大门槛：`POST /user/login` 的 `username` 与 `password`
**都不是明文**，而是 **RSA-OAEP / SHA-256** 加密后再 Base64 编码的字符串。
直接发明文会解密失败。

**加密参数（必须完全一致）：**

| 项 | 值 |
| -- | -- |
| 算法 | `RSA-OAEP` |
| OAEP 摘要 / MGF1 | `SHA-256` |
| OAEP label | 空（Go 侧 `nil`） |
| 公钥格式 | Base64 编码的 PEM |
| 输出编码 | Base64 |
| 密钥长度 | 2048 位（单次可加密明文上限 190 字节） |

**公钥从哪来**：网关**没有下发公钥的接口**，公钥是硬编码常量：

- 后端：[../utils/rsaUtil.go](../utils/rsaUtil.go) 的 `utils.PublicKey`
- 前端：[../../frontend/src/utils/rsa.js](../../frontend/src/utils/rsa.js) 的 `PUBLIC_KEY_B64`

两者是同一串值。dsh 需要把该常量复制一份到自己这侧。

```text
LS0tLS1CRUdJTiBSU0EgUFVCTElDIEtFWS0tLS0tCk1JSUJDZ0tDQVFFQXpWWnVvYkdZWWpmNVNtelRJY1Nnay9NT3d6emovQU94NkhHSk5OczNLYmlLVGFtZTMwaEUKb25BR0ZHT1A3MmllNzFGZ0tXREM3QzRRV3poOTNNNlE4V2Q2Z2pWUzJub05ueER6T3BKOVIwRnJWY3BlcDdoTQowVDFlajBxN0NtNWJaL3N4a0FERUVtVTVGcDQxOUczWUpTcm44NVVlUDdOQUpUVzJzUytNa2xmQlVqWVN1WGhkCjNhUVE0UWJndXZqb1U3WUtMa1VxMC9nV0l1STF6emV5WlRPQU4ya2xyNHVjS1FpQ2VUS0MvTlV0UmpRVmdGZ0MKZ0MwdHQ3ZlNUOHFUV0thR0NCTHhGYVJRRWNtaGZVRFpHT3hNSUVqcmc5d2N5aytTK2JIdE10V2VtS0N4MXo5LwozbzVSVitMQVdUTEpUTHRJTVlhNjU5cGpGYWRQMEJKdWlRSURBUUFCCi0tLS0tRU5EIFJTQSBQVUJMSUMgS0VZLS0tLS0K
```

**登录流程：**

1. 用上面公钥分别加密用户名与密码（**两次独立加密，不可拼接后一起加密**）；
2. `POST /user/login`，Body：`{"username":"<密文>","password":"<密文>"}`；
3. 取 `data.accessToken`，后续请求带 `Authorization: Bearer <accessToken>`；
4. 令牌有效期见 `data.expiresIn`（秒，配置项 `jwt.expireHour`，默认 24 小时）。

**初始账号**：首次启动（用户表为空）时播种 `admin`，口令取配置项
`auth.defaultAdminPassword`（默认 `hand@123`）或环境变量 `IOT_GATEWAY_ADMIN_PASSWORD`，
两者皆空则随机生成并**仅在启动日志里打印一次**。详见 [../database/sqlite/seed.go](../database/sqlite/seed.go)。

**修改密码**：`PUT /user/password` 三个字段（`oldPassword`/`newPassword`/`confirmPassword`）
同样全部 RSA 加密；用户名从 JWT 声明取，请求体里不带用户名。

---

## 3. 只读 API 清单

### 3.0 只读接口索引

下表所有接口均**不修改任何持久化状态**，可安全重复调用。
但标 ⚠️ 的接口**有真实代价**（占用网络、设备会话或对外发消息），
Agent 不应在无用户意图时自行调用。

#### 开放接口（`X-Api-Key`）

| 方法 | 路径 | 用途 | 主要参数 | 响应 |
| ---- | ---- | ---- | -------- | ---- |
| GET | `/openApi/device/page` | 设备分页 | `page`*, `size`*, `name`(模糊) | `PageVo<OpenApiDeviceVO>` |
| POST | `/openApi/device/names` | 按 ID 批量查设备名 | Body: `ids`*[] | `[]OpenApiDeviceNameVO` |
| POST | `/openApi/deviceAddress/labels` | 按设备+点位 ID 批量查标签 | Body: `deviceIds`*[], `addressIds`*[] | `[]OpenApiAddressLabelVO` |
| GET | `/openApi/deviceAddress/page` | 点位分页 | `page`*, `size`*, `deviceId`*, `name`(模糊) | `PageVo<OpenApiDeviceAddressVO>` |
| GET | `/openApi/device/sync` | **分批同步**设备+其全部点位（**游标翻页**，非 `page`/`size`） | `after`(游标，首次不传), `size`*(1~100) | `CursorVo<OpenApiSyncDeviceVO>` |
| GET | `/openApi/alarm/deviceReliability` | 设备可靠性统计（可用率/故障段数/停机时长/MTTR） | `from`*, `to`*(YYYY-MM-DD，窗口≤90天), `limit`(省略=不截断) | `DeviceReliabilityReportVO` |
| GET | `/openApi/alarm/pointAbnormal` | 点位异常 top-N（质量异常与超限分开计数） | `deviceId`(可选), `from`*, `to`*, `limit`(省略=服务端默认20) | `PointAbnormalReportVO` |

> 注意 `POST /openApi/device/names` 与 `POST /openApi/deviceAddress/labels` 虽是 POST，
> 但**是纯查询、无副作用** —— 用 POST 是因为 ID 数组放在 Body 里（列表可能很长）。
> Agent 选取工具时应按「只读」对待。

#### 管理接口（JWT）

| 方法 | 路径 | 用途 | 主要参数 | 响应 |
| ---- | ---- | ---- | -------- | ---- |
| GET | `/profile` | 当前登录用户信息 | — | `UserVO` |
| GET | `/users` | 用户分页 | `page`, `size` | `PageVo<UserVO>` |
| GET | `/users/:id` | 按 ID 查用户 | 路径 `id` | `UserVO` |
| GET | `/device/pageDevice` | 设备分页 | `page`*, `size`*, `name`(模糊) | `PageVo<DeviceVO>` |
| GET | `/device/getDeviceById/:id` | 按 ID 查设备 | 路径 `id` | `DeviceVO` |
| GET | `/device/overview` | 设备在线统计 | — | `DeviceOverviewVO` |
| GET | `/device/importTemplate` | 下载设备导入模板 | — | **xlsx 文件流** |
| GET | `/deviceAddress/pageDeviceAddress` | 点位分页 | `page`*, `size`*, `deviceId`*, `name` | `PageVo<DeviceAddressVO>` |
| GET | `/deviceAddress/getDeviceAddressById/:id` | 按 ID 查点位 | 路径 `id` | `DeviceAddressVO` |
| GET | `/deviceAddress/importDeviceAddressTemplate` | 下载点位导入模板 | — | **xlsx 文件流** |
| GET | `/collection/status` | 采集引擎状态 | — | `CollectionStatusVO` |
| GET | `/realtime/snapshot` | 设备实时数据快照 | `page`*, `size`*, `deviceId`*, `name` | `RealtimeSnapshotVO` |
| GET | `/protocol/pageProtocol` | 协议分页 | `page`*, `size`*, `name` | `PageVo<ProtocolVO>` |
| GET | `/protocol/getProtocolById/:id` | 按 ID 查协议 | 路径 `id` | `ProtocolVO` |
| GET | `/protocol/getProtocolByName` | 按名查协议 | Query `name` | `ProtocolVO` |
| GET | `/protocol/getDataTypes` | 协议可用数据类型（`kindByType` 给出每个类型的类别，供点位报警条件按类别切换比较符） | Query `protocolName` 或 `protocolId` | `DataTypeOptionVO` |
| GET | `/pushChannel/pagePushChannel` | 推送通道分页 | `page`*, `size`*, `name` | `PageVo<PushChannelVO>` |
| GET | `/pushChannel/getPushChannelById/:id` | 按 ID 查通道 | 路径 `id` | `PushChannelVO` |
| GET | `/pushChannelForm/getPushChannelFormByName` | 按名查通道表单 | `name`* | `PushChannelFormVO` |
| GET | `/push/status` | 推送通道运行状态 | — | `[]ChannelStatusVO` |
| GET | `/alarm/pageAlarm` | 报警历史分页（`targetType`=device\|channel\|point\|point_threshold，`alarmType`=offline\|quality\|threshold\|recover） | `page`*, `size`*, `targetName`, `targetType`, `alarmType`, `status` | `PageVo<AlarmVO>` |
| GET | `/alarm/listActive` | 当前活跃**断联**报警（离线设备 + 离线通道；不含点位质量报警） | — | `[]AlarmVO` |
| GET | `/alarm/pageThresholdRecord` | 点位条件命中样本明细（按**原始命中**逐条留证，与去抖后的报警不同） | `page`*, `size`*, `deviceId`, `name` | `PageVo<PointThresholdRecordVO>` |
| GET | `/alarmWebhook/pageAlarmWebhook` | 报警通知分页 | `page`*, `size`*, `name`, `type`, `status` | `PageVo<AlarmWebhookVO>` |
| GET | `/alarmWebhook/getAlarmWebhookById/:id` | 按 ID 查通知 | 路径 `id` | `AlarmWebhookVO` |
| GET | `/alarmWebhook/status` | 通知器运行状况 | — | `DispatcherStatus` |
| GET | `/alarmWebhook/listTypes` | 支持的 Webhook 类型 | — | `[]TypeMeta` |
| GET | `/alarmWebhookForm/getAlarmWebhookFormByName` | 按名查通知表单 | `name`* | `AlarmWebhookFormVO` |
| GET | `/log/listFiles` | 日志文件列表 | `page`, `size`（**可省略**） | `PageVo<LogFileVO>` |
| GET | `/log/readFile` | 读日志末尾 N 行 | `fileName`*, `tail`(默认 200，上限 5000) | `LogReadVO` |
| GET | `/gateway/get` | 网关自身身份 | — | `GatewayVO` |
| GET | `/plugin/info` | 插件环境信息 | — | `PluginInfoVO` |
| GET | `/deviceScan/capabilities` | 扫描页初始化（协议+网段+开关） | — | `ScanCapabilitiesVO` |
| GET | `/deviceScan/status` | 扫描任务状态 | `jobId`*, `withDevices` | `ScanJobVO` |
| GET | `/deviceScan/pointCapability` | 协议点扫能力 | `protocolName`* | `PointCapabilityVO` |
| GET | `/openApiSecret/list` | 开放接口密钥列表 | — | `[]OpenApiSecretVO` |
| GET | `/openApiSecret/doc` | 开放接口文档 | — | HTML 字符串 |

参数带 `*` 为必填。`name` 类参数均为模糊匹配。

#### ⚠️ 有代价的只读接口（探询类）

这些接口不写库，但会**真实占用网络或设备会话**，且 `testSend` 会**真的对外发出消息**。
Agent 调用前应先获得用户明确意图。

| 方法 | 路径 | 代价 | 参数 | 响应 |
| ---- | ---- | ---- | ---- | ---- |
| POST | `/device/testDeviceConnection` | 真实连接一次 PLC（超时受限于设备） | `protocolName`*, `protocolJson`* | `data: null` |
| POST | `/pushChannel/testConnectivity` | 真实连接下游（MQTT 等），不保存配置 | `name`*, `configJson`* | `data: null` |
| POST | `/deviceScan/start` | **全网段扫描**，重且慢，同一时刻只允许一个任务 | `protocol`*, `options`, `subnets`, `port` | `ScanJobVO` |
| POST | `/deviceScan/cancel` | 取消在跑的扫描 | `jobId`* | `data: null` |
| POST | `/deviceScan/discoverPoints` | **同步 HTTP**，复用设备采集会话，最长 7 秒 | `deviceId`*, `options` | `PointScanResultVO` |
| POST | `/alarmWebhook/testSend` | ⚠️ **真的发送一条报警消息到钉钉/企微/飞书** | 见 §4 说明 | `data: null` |

> `POST /deviceScan/discoverPoints` 是**同步阻塞**调用：网关侧有 7000ms 硬钳制
> （与 `main.go` 的 10s 关机宽限期耦合）。Agent 侧 HTTP 超时应设为 **> 8 秒**，
> 否则会在网关还没返回时先行超时。

### 3.1 只读接口的关键响应字段

**`DeviceVO`（设备）** — 注意 `protocolJson` 是 PLC 连接配置，**敏感**：

```json
{
  "id": "…", "iotGatewayId": "…", "name": "1号灌装机",
  "protocolId": "…", "protocolName": "ModBus.TCP",
  "protocolJson": "{…连接配置…}",
  "description": "", "status": 1,
  "online": true, "lastSuccessTime": "2026-09-30 10:28:11",
  "createdAt": "…", "updatedAt": "…"
}
```

`online` 为「无 active 断联报警」（仅按 `target_type='device'` 反查，不受点位质量报警影响）；`lastSuccessTime` 是**内存态**，空串表示尚无成功记录
（重启后会短暂为空，属正常）。

**`CollectionStatusVO`（采集状态）**：

```json
{ "running": true, "lastPollTime": "…", "lastSuccessTime": "…",
  "errorCount": 0, "deviceCount": 12, "addressCount": 340 }
```

**`RealtimeSnapshotVO`（实时快照）** — 三个状态位组合可解释「为什么没数据」：
`collectorRunning=false` → 采集未启动；`collecting=false` → 协议不可用或无活跃点位；
`online=false` → 设备已判离线。`records[]` 中未采集到的点位同样占位返回（`collected=false`），
使行数稳定。

**`DeviceOverviewVO`（设备统计）**：`total/enabled/disabled/collected/online/offline/unCollected/onlineRate`。
`onlineRate = online / collected`，保留 2 位小数。

**`ScanJobVO`（扫描任务）** — `devices` 与 `errors` **可能为 `null`**：
轮询时传 `withDevices=false` 拿轻量快照（不含清单），此时两个字段为 `null`；
**非 null 时一定是数组**（`errors` 空清单发成 `[]`，刻意不加 `omitempty`）。
调用方需保留自己已拿到的那份清单，据 `found` 判断是否漏掉了新设备。
`truncated=true` 表示网段展开被 `maxTargets` 截断，**只扫了一部分**。

**`PointScanResultVO`（点位扫描）** — 计数语义必须分清：

- `scanned` 尝试读取的地址数，`readable` 有应答数（**含值为 0 的**），`unreadable` 读不到数；
- 不变量：`len(points) <= readable <= scanned`，`readable + unreadable == scanned`；
- `truncated` 达到 `maxPoints`，后面的地址根本没试；`incomplete` 预算耗尽、范围没扫完
  （与「读不到」是两回事）；
- `warning` 非空表示**中途断了**，`points` 是断之前已读到的部分（与「失败」不同，失败时无 `points`）；
- `assumedCommonDataType` 为空 == 本次扫描没有采样值（如 Rockwell.CIP 只列标签不读值）；
  非空则**表示本次结果带采样值**（前端据它显示「显示 0 值」开关）。
  它**不排斥**逐点的 `commonDataType`：DLT645 的数据标识字典让每个点位各有各的类型
  （表号 String / 状态字 Word / 电量 Float），因此两个字段同时非空 ——
  逐点优先，结果级只是兜底与那枚开关的判据。

> **点位扫描产出的是「该范围内读得到的地址清单」，是候选清单而非设备的点位表** ——
> 设备不会告诉你它有哪些点位。插件刻意**不做零值过滤**（过滤在前端/调用方侧做）。

**`PushChannelVO` 中的 `configJson`** 含 MQTT 口令等凭据，网关原样回显以便编辑。
`AlarmWebhookVO.secret` 则**永不回显**（恒为空串），只用 `hasSecret` 表示是否已配置。

**`DispatcherStatus`（报警通知器状态）**：
`{running, ingressDepth, ingressDropped, webhooks[]}`。

**`TypeMeta`（`listTypes` 的每一项）** —— **创建报警通知前应先调此接口取合法 `type` 值**：

```json
{ "type": "dingtalk", "label": "钉钉群机器人", "msgTypes": ["markdown", "text"],
  "defaultMsgType": "markdown", "supportsSign": true, "supportsAt": true,
  "urlPlaceholder": "https://oapi.dingtalk.com/robot/send?access_token=xxx" }
```

可用的 `msgType` 取值为 `text` / `markdown` / `card`；**各平台支持子集不同**
（钉钉支持 markdown + text，默认 markdown），以 `listTypes` 返回的 `msgTypes` 为准。

当前支持的类型为 `dingtalk` / `wecom` / `feishu` / `custom`。
`createAlarmWebhook` 的 `type` 取值合法性由服务端的 notify 注册表判定
（`notify.ValidateConfig`，支持集只维护一处），传错返回 `95003`。

---

## 4. 编辑 API 清单

### 4.1 编辑接口索引（全部需要 JWT）

> **高危提示 ①**：本项目的删除接口**用 GET 实现**（`/device/deleteDevice/:id` 等）。
> Agent 的分类器**绝不能因为方法是 GET 就把它当只读** —— 一次预取、一次重试、
> 一次对话里的误触都会**真删数据**。下表已用 🗑 显式标出。

> **高危提示 ②**：删除是**静默生效**的 —— 采集任务下一轮热加载就少了这些点位，
> 没有二次确认、没有回收站。

#### 用户

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/createUser` | 创建用户 | `username`*(2–50), `password`*(≥6) |
| PUT | `/users/:id` | 更新用户资料 | 路径 `id`；Body `name`, `email`, `avatar` |
| PUT | `/user/password` | 修改**当前用户**密码 | `oldPassword`*, `newPassword`*, `confirmPassword`*（**均 RSA 加密**） |

#### 设备

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/device/createDevice` | 创建设备 | `name`*(1–100), `protocolId`*, `protocolJson`*, `description` |
| POST | `/device/updateDevice` | 更新设备 | `id`, `name`, `protocolId`, `protocolJson`, `description`, `status` |
| 🗑 GET | `/device/deleteDevice/:id` | **删除设备** | 路径 `id` |
| 🗑 POST | `/device/batchDeleteDevice` | **批量删除设备** | `ids`*[]（1–200） |
| POST | `/device/importDevice` | Excel 批量导入设备 | multipart，字段名 **`file`**（`.xlsx`） |

> `POST /device/createDevice` 与 `/device/updateDevice` 成功时 `data` 为 **`null`**
> —— 响应里**不返回新建设备的 ID**。需要 ID 时须再调 `GET /device/pageDevice` 按名称查。

> `POST /device/batchDeleteDevice` 返回 `BatchDeleteDeviceResultVO`：
> `{requested, deleted, addresses}`。`addresses` 是**随设备一并删掉的点位行数**
> （表间无外键，不显式删会留下查不到的孤立行）。`deleted` 可能小于 `requested`
> （勾选后又被人删掉的地址不在删除集里），这属**正常**，不是错误。

#### 设备点位（device_address）

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/deviceAddress/createDeviceAddress` | 创建点位 | `deviceId`*, `name`*, `label`*, `commonDataType`, `dataType`, `rwPermission`*, `scanFrequency`, `description` |
| POST | `/deviceAddress/updateDeviceAddress` | 更新点位 | `id`*, `name`, `label`*, `commonDataType`, `dataType`, `rwPermission`, `scanFrequency`, `description`, `status` |
| 🗑 GET | `/deviceAddress/deleteDeviceAddress/:id` | **删除点位** | 路径 `id` |
| 🗑 POST | `/deviceAddress/batchDeleteDeviceAddress` | **批量删除点位** | `deviceId`*, `ids`*[]（1–200） |
| POST | `/deviceAddress/importDeviceAddress` | Excel 批量导入点位 | multipart：字段名 **`file`**（`.xlsx`）+ 普通字段 **`deviceId`** |

- `rwPermission` 取值只允许 `R` / `W` / `RW`（三选一，否则报 `10001`）。
- `label` 是**地址的中文说明**（如「温度」「电流」），创建时必填。
- `dataType` 是**兼容旧请求**的字段；新调用方应传 `commonDataType`（通用类型名，
  如 `Word`），服务端结合协议映射得出内部 `data_type`。
- 点位名 `name` 是**设备地址写法**（如 `%MW100`、`DB1.DBW100`、`D100`），
  写法由协议决定。**这一层不校验写法**：服务端只校验非空、长度与唯一性，
  真正的解析发生在驱动的读取路径上。写错的后果是**这个点位永远没有值**，
  只报一条点位质量报警「地址无效」，需要改配置才恢复 —— 不会影响同设备的其它点位。

> `POST /deviceAddress/batchDeleteDeviceAddress` 的 `deviceId` 是**删除范围的前置约束**
> ——这些 id 必须属于这台设备才会被删。这是为了防止用当前页看到的 id 去删另一台设备的点位。

#### 采集控制

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/collection/start` | 启动采集 | — |
| POST | `/collection/stop` | 停止采集 | — |
| POST | `/collection/refresh` | 热刷新采集配置（改完设备/点位立即生效，无需等 10s 巡检） | — |

#### 协议

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/protocol/updateProtocol` | 更新协议（描述 / 表单 / 排序） | `id`, `name`, `description`, `formJson`, `sort` |
| POST | `/protocol/updateProtocolFormByName` | 按协议名更新表单 JSON | `name`*, `formJson`* |
| POST | `/protocol/updateProtocolFormById` | 按协议 ID 更新表单 JSON | `id`*, `formJson`* |
| POST | `/protocol/resetProtocolForm/:id` | 恢复插件声明的默认表单 | 路径 `id` |

> **没有「创建协议」接口**：协议只能由插件供给，手工建的行没有驱动与之对应。
> `updateProtocol` 里的 `name` 仅用于**校验它没被改动**，服务端不会写回 ——
> 协议名是与插件驱动的唯一纽带，改名会让引用它的设备静默停采，故返回 `50003`。

#### 推送通道

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/pushChannel/createPushChannel` | 创建通道 | `name`*(1–100), `description`, `configJson`* |
| POST | `/pushChannel/updatePushChannel` | 更新通道 | `id`*, `name`, `description`, `configJson`, `status` |
| 🗑 GET | `/pushChannel/deletePushChannel/:id` | **删除通道** | 路径 `id` |
| POST | `/pushChannelForm/createPushChannelForm` | 创建通道表单 | `name`*, `formJson`* |
| POST | `/pushChannelForm/updatePushChannelForm` | 更新通道表单 | `id`*, `name`, `formJson` |
| POST | `/push/refresh` | 热刷新推送配置 | — |

#### 报警

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/alarmWebhook/createAlarmWebhook` | 创建报警通知 | `name`*, `type`*, `description`, `url`*, `secret`, `msgType`, `atAll`, `atList` |
| POST | `/alarmWebhook/updateAlarmWebhook` | 更新报警通知 | `id`*，其余字段**全部可选、零值不覆盖** |
| 🗑 GET | `/alarmWebhook/deleteAlarmWebhook/:id` | **删除报警通知** | 路径 `id` |
| POST | `/alarmWebhook/refresh` | 热刷新报警通知配置 | — |
| ⚠️ POST | `/alarmWebhook/testSend` | **发送测试报警（真的发消息）** | `id`, `type`, `url`, `secret`, `msgType`, `atAll`, `atList`, `alarmType`(recover 时才需传，其余按 `targetType` 取报警类型), `targetType`(device\|channel\|point), `targetName` |
| POST | `/alarmWebhookForm/createAlarmWebhookForm` | 创建通知表单 | `name`*, `formJson`* |
| POST | `/alarmWebhookForm/updateAlarmWebhookForm` | 更新通知表单 | `id`*, `name`, `formJson` |

**`updateAlarmWebhook` 的「零值不覆盖」细节（易踩坑）**：

- `url`/`name`/`type` 等字符串：传空串 = 不改（不是清空）；
- `secret`/`atAll`/`atList`/`status` 是**指针类型** —— 传 `null`/省略 = 不修改，
  传 `""`/`false`/`0` = **清空**。要清空加签密钥必须显式传 `"secret": ""`。

**`testSend` 的配置来源**：`id` 非空时以库中配置为基准、请求字段覆盖之
（便于「改完先测再存」）；`id` 为空则完全使用请求字段（便于「还没保存先测」）。

#### 网关身份 / 插件 / 密钥

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/gateway/update` | 编辑网关名称与描述 | `name`*(1–100), `description`(≤500) |
| POST | `/plugin/importPlugin` | 上传协议插件（热升级） | multipart，字段名 **`files`**（可多个，同名部件重复出现），总上限 512MiB |
| POST | `/openApiSecret/create` | 创建开放接口密钥 | `name`*(1–100) |
| POST | `/openApiSecret/updateName` | 编辑密钥名称 | `id`*, `name`* |
| 🗑 GET | `/openApiSecret/delete/:id` | **删除密钥** | 路径 `id` |

> **名为 `default` 的密钥不可删除也不可改名**：它由启动播种创建，并随局域网广播发布，
> 供管理平台免人工录入直接调用开放接口。对它的 `delete` / `updateName` 返回业务错误码
> `70005`；列表里它带 `isDefault: true`。

> `gateway/update` **刻意只开放 name 与 description**：`id` 是网关唯一标识（改了等于换机）、
> `firmware_version` 以配置为准（启动时自动同步、页面上改不了）、`status` 本轮不开放。
> 网关名称改完**无需重启** —— 局域网广播每轮都重读该行。

#### 设备扫描 / 点位扫描（写操作部分）

| 方法 | 路径 | 用途 | 参数 |
| ---- | ---- | ---- | ---- |
| POST | `/deviceScan/createDevices` | 把扫描结果批量建成设备 | `jobId`*, `items`*[], `confirmDuplicate` |
| POST | `/deviceScan/createAddresses` | 把点扫结果批量建成点位 | `deviceId`*, `items`*[] |

**`createDevices` 的两段式确认**：`items[].index` 指向**任务快照里 `devices` 数组的下标**；
`protocolJson` 由服务端从自己持有的快照里取，**不采信客户端**。
若某行的地址与库内既有设备重复，默认**不创建**，只作为带 `code: "addressExists"` 的明细返回；
用户确认后才带 `confirmDuplicate: true` 重提。
（同一地址上多台设备既合法又常见：Modbus TCP 串口服务器后可挂多个从站、
多通道串口服务器的不同端口、同一台 PLC 同时开 S7 与 Modbus。）

**`createAddresses`**：`items[]` 字段为 `name`*（设备地址写法）、`label`（留空兜底为 `name`）、
`commonDataType`*、`rwPermission`、`scanFrequency`（≤0 按 1000 处理）、`description`。
这里**采信客户端的全部字段**（与设备扫描相反）—— 点位的名称/类型本就是用户在界面上
逐行编辑的产物，服务端只做校验与去重；唯一的服务端权威是 `deviceId`（它决定协议，
从而决定类型名怎么映射）。

---

## 5. 错误码总表

响应体 `code` 字段取值（[enums/resultEnum.go](../enums/resultEnum.go)）：

| code | 含义 | 备注 |
| ---- | ---- | ---- |
| `0` | 成功 | |
| `-1` | 系统错误 | |
| `10001` | 参数校验错误 | 必填缺失、超长、枚举越界都归这里 |
| `10002` / `10003` / `10004` | 未授权 / 禁止访问 / 资源不存在 | |
| `20001`–`20003` | 用户已存在 / 用户不存在 / 密码错误 | |
| `20004`–`20006` | 令牌过期 / 令牌无效 / 令牌为空 | 伴随 HTTP 401 |
| `30001` / `30002` | 开始采集失败 / 停止采集失败 | |
| `40001`–`40003` | 设备名已存在 / 设备不存在 / 设备连接测试失败 | |
| `40011`–`40013` | 点位名已存在 / 点位不存在 / 协议不支持该数据类型 | |
| `40021` | 局域网扫描功能未启用 | 开关 `deviceScan.enabled` |
| `40022` | 已有扫描任务在运行 | 同一时刻只允许一个 |
| `40023` | 扫描任务不存在或已过期 | 任务只在内存，重启即丢 |
| `40024` | 该协议不支持局域网扫描 | |
| `40025` | 没有可扫描的目标 | |
| `40026` | 扫描参数不合法 | |
| `40027` | 该协议不支持点位扫描 | 与 40024 是**两件事**，别混 |
| `50001`–`50005` | 协议名已存在 / 协议不存在 / 协议名不可修改 / 插件不在 / 插件未声明默认表单 | |
| `60001`–`60003` | 推送通道名已存在 / 不存在 / 连通性测试失败 | |
| `60011` / `60012` | 通道表单名已存在 / 不存在 | |
| `70001`–`70004` | 密钥名已存在 / 不存在 / 密钥为空 / 密钥无效 | 后两个伴随 HTTP 401 |
| `80001` | 网关身份不存在 | |
| `90001`–`90006` | 插件未启用 / 未收到文件 / 文件数超限 / 同批重名 / 目录不可用 / 安装失败 | |
| `95001`–`95006` | 报警通知名已存在 / 不存在 / 配置不合法 / 测试发送失败 / 表单名已存在 / 表单不存在 | |

---

## 6. Agent 接入注意事项

### 6.1 必读的坑

1. **删除是 GET**（见 §4.1 高危提示 ①）。把「HTTP 方法是 GET」等同于「无副作用」
   会直接删掉生产数据。判定只读请看本文的分类，不要看方法。
2. **走 JWT 通道时登录密码必须 RSA 加密**（§2.2），且**公钥无接口下发**，需硬编码复制。
   （dsh 走 `/agentApi` 则完全绕开这一条，见 §0。）
3. **创建接口不返回 ID**：`createDevice` / `updateDevice` / `createDeviceAddress` 等
   成功时 `data: null`。要拿 ID 必须回查分页接口。
4. **业务成败看 `code` 不看 HTTP 状态码**（§1.2）。HTTP 200 也可能是失败。
5. **点位名是设备地址写法，写错会拖垮整台设备**（§4.1）。Agent 不应自行发明点位名，
   应从 `GET /deviceAddress/pageDeviceAddress` 或点扫结果里取。
6. **`POST /deviceScan/discoverPoints` 是同步阻塞的**，网关侧硬钳 7000ms，
   Agent 的 HTTP 超时必须 > 8 秒（§3.0）。
7. **实时值不在 `deviceAddress` 里**：`GET /realtime/snapshot` 取的是**内存中的最新值**，
   按秒级轮询用；`GET /deviceAddress/pageDeviceAddress` 取的是**点位定义**。两者别混。

### 6.2 代码注释里的一处失效引用（别照着调）

[../model/dto/deviceScanDto.go](../model/dto/deviceScanDto.go) 的注释写着
「协议名必须来自 `GET /deviceScan/protocols` 的返回值」、「网段只能从
`GET /deviceScan/subnets` 的返回值里选」——**这两个接口并不存在**。

实际形态是合并进了同一个响应：

- 协议列表 → `GET /deviceScan/capabilities` 响应的 `data.protocols[]`
- 网段列表 → `GET /deviceScan/capabilities` 响应的 `data.subnets[]`

注释是早期版本的残留，未随接口合并更新。**以本文与
[router/deviceScanRouter.go](../router/deviceScanRouter.go) 的实际注册为准。**

### 6.3 dsh 接入的推荐路径

0. **启动时先调 `GET /agentApi/__surface`**（§0.1），用返回的 `access`/`confirm`
   构建自己的工具策略——哪些可以直接调、哪些必须先问人。**不要把这个分类硬编码进
   提示词**：网关加接口时清单会自动更新，硬编码的会静默过期。
1. **一律走 `/agentApi/*`**：把本文 §3 / §4 里的**每一个**路径前面加上 `/agentApi`
   就是 dsh 的调用地址，无需任何凭据、无需登录流程。
   例：`GET /device/pageDevice` → `GET /agentApi/device/pageDevice`。
2. **不要让 dsh 走 JWT 通道**：RSA 加密登录对 Agent 是纯粹的负担，
   而 `/agentApi` 已覆盖全部 82 条接口，没有能力缺口。
3. **调用编辑接口前**：把 §4.1 的两条高危提示写进 Agent 的工具描述里
   —— 尤其是「删除接口是 GET」这条，它是最容易造成不可逆损失的一个坑。
   无鉴权通道下这个坑的后果更重：**没有凭据这一层兜底，误调的门槛只剩工具描述**。
4. **部署前提**：确认网关处于隔离内网、或 dsh 与网关同机（见 §0 的风险说明）。
   若不满足，先给 `/agentApi` 加回鉴权再上线。
