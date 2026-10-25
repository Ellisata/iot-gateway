# iot-gateway-premium 能力清单报告（面向「DSH 插件对接网关」）

> 分析对象：`D:\code\汉得\iot-gateway-premium`（工业 IoT 数据采集网关，AGPL-3.0）
> 分析方式：只读静态分析 + 对**正在运行的实例**（`http://127.0.0.1:9082`）做只读实测
> 分析日期：2026-09-30
> 所有结论均附源码相对路径与行号，可逐条核对。

---

## 0. 最重要的三条结论（先读）

### 0.1 网关已内置面向 DSH 的**无鉴权**调用面 `/agentApi/*` —— 不要实现 RSA 登录

> **2026-10-25 更新**：`/agentApi` 现已**出厂关闭**，由运行期开关
> `agentApi.enabled` 控制（管理端「运行开关」页开启，立即生效）。未开启时整组
> 返回 403（业务码 `10005`），**包括清单端点**；开启必须由管理员登录后操作，
> dsh 自己无法打开（`/agentApi/systemConfig/update` 同样被拦）。本文其余部分
> 描述的免鉴权行为，均以该开关已开启为前提。

`backend/router/agentApiRouter.go:12-104` 把**全部领域路由整体镜像**到 `/agentApi` 前缀下：

```go
// agentApiRouter.go:13
const AgentApiPrefix = "/agentApi"

// agentApiRouter.go:62-103 —— 复用各领域的 XxxRoutes，而非复制路由表
g := r.Group(AgentApiPrefix)
g.GET(surfacePath, SurfaceHandler)
UserRoutes(g, uc)          // 用户
DeviceRoutes(g, dc)        // 设备与点位
DeviceAddressRoutes(g, ac)
CollectionRoutes(g, cc)    // 采集与实时值
RealtimeRoutes(g, rc)
ProtocolRoutes(g, pc)      // 协议与推送
PushChannelRoutes(g, pc2)
PushChannelFormRoutes(g, pcf)
PushRoutes(g, pus)
AlarmRoutes(g, ac2)        // 报警
AlarmWebhookRoutes(g, awc)
AlarmWebhookFormRoutes(g, awfc)
LogFileRoutes(g, lc)       // 运维
OpenApiSecretRoutes(g, oasc)
GatewayRoutes(g, gc)
PluginRoutes(g, plc)
DeviceScanRoutes(g, dsc)   // 局域网设备扫描 + 点位扫描
```

放行位置在 `backend/middleware/authMiddleware.go`：

```go
// authMiddleware.go:40
const agentApiPrefix = "/agentApi"

// authMiddleware.go:52-55 —— /agentApi/* 免鉴权放行
// /agentApi/* 免鉴权放行，理由与风险见 agentApiPrefix 的注释
if path == agentApiPrefix || strings.HasPrefix(path, agentApiPrefix+"/") {
    return true
}
```

`authMiddleware.go:24-36` 的注释把性质差异写得很清楚（**引用原文**）：

> - `/admin/*` 免认证是因为它只是静态资源；
> - `/openApi/*` 免 JWT，但**仍要 X-Api-Key**；
> - `/agentApi/*` 是**真的没有任何凭据校验** —— 任何能访问到网关端口的主机都能调用，包括删除设备、上传并加载插件（可执行文件）、修改推送配置等写操作。
>
> 这是刻意的设计选择（前提：隔离内网，或 dsh 与网关同机），不是遗漏。

**实测确认**（无需任何 header）：

```
GET http://127.0.0.1:9082/agentApi/gateway/get
→ {"code":"0","msg":"success","isSuccess":true,
   "data":{"id":"94ea29ca23a94cffb279474acab664ef","name":"23VJV25B27302557",
           "port":9082,"firmwareVersion":"1.0.0", ...}}
```

对照：同一份数据走根路径会被拦：

```
GET http://127.0.0.1:9082/device/pageDevice   （无 JWT）
→ HTTP 401  {"code":"20006","msg":"令牌为空","isSuccess":false,...}
```

**结论**：DSH 插件应**一律走 `/agentApi/*`**，完全绕开用户登录、JWT、RSA-OAEP 加密。这不是"偷懒"——`backend/docs/apiCatalog.md:35-37` 与 `:647` 明确写了这是推荐路径，且 `/agentApi` **没有能力缺口**（82 个领域接口与根路径一一对应）。

### 0.2 接入的第一步：拉机器可读的接口分类清单

**`GET /agentApi/__surface`** —— 无需凭据，返回**正在运行的那个网关**的真实路由清单。

实现：`backend/router/agentApiSurface.go:69-70,353-355`
路径常量 `surfacePath = "/__surface"`；处理函数 `SurfaceHandler` 直接返回 `AgentApiSurface()`。

**实测返回 83 条**（82 个领域接口 + 清单自身）。每条形如：

```json
{
  "method": "GET",
  "path": "/agentApi/device/deleteDevice/:id",
  "access": "write",
  "confirm": true,
  "note": "**用 GET 实现**，按方法判只读会误删。连带删掉该设备全部点位"
}
```

**实测分类统计**（我从 live 端点实际统计得出）：

| access | 条数 | 含义 | DSH 该怎么做 |
|---|---|---|---|
| `read` | 38 | 不改任何状态、无对外副作用、代价可忽略 | 直接调 |
| `probe` | 6 | 不改状态，但**有真实代价或对外副作用** | **先要人确认** |
| `write` | 39 | 改变网关状态（持久化数据 / 启停运行行为） | **先要人确认** |

`confirm` 由 `access != read` **统一推导**，不在表里重复维护（`agentApiSurface.go:311-312,325`）。

**为什么不能按 HTTP 方法判断只读**（`agentApiSurface.go:18-26` 给出的四组反例，引用原文）：

| 反例 | 方法 | 实际 |
|---|---|---|
| `GET /device/deleteDevice/:id` | GET | **删数据** |
| `POST /openApi/device/names` | POST | 纯查询 |
| `POST /device/testDeviceConnection` | POST | 不改状态，但真实连一次 PLC |
| `POST /alarmWebhook/testSend` | POST | 不改状态，但**真的往外发一条消息** |

这 6 条 `probe` 的完整清单（`agentApiSurface.go:233-235,276-283,291-296,118-121,195-198`，实测核对一致）：

| 接口 | 代价 |
|---|---|
| `POST /agentApi/alarmWebhook/testSend` | 真的对外发消息，群里有人看见 |
| `POST /agentApi/deviceScan/start` | 扫本机网段，重且慢，同时只允许一个任务 |
| `POST /agentApi/deviceScan/discoverPoints` | **同步阻塞**最长约 7s，且占用该设备采集会话锁 |
| `POST /agentApi/deviceScan/cancel` | 取消在跑的扫描 |
| `POST /agentApi/device/testDeviceConnection` | 真实连接一次 PLC |
| `POST /agentApi/pushChannel/testConnectivity` | 真实连接下游（MQTT 等） |

> ⚠️ **网关只提供分类，不强制确认**（`agentApiSurface.go` 无任何强制逻辑；
> `apiCatalog.md:97-99` 原文：「这是**给 dsh 遵守的契约，不是服务端的安全控制**」）。
> 是否真的先问人，**完全取决于 DSH 侧实现**。

### 0.3 没有 WebSocket / SSE，实时数据只能轮询

对 `backend/` 全量 grep `websocket|gorilla/ws|text/event-stream|EventSource|SSE|Upgrade\(` —— **零命中**。
对 `frontend/src` grep `new WebSocket|EventSource|socket.io` —— **零命中**（仅 3 处 MQTT 通道名字符串）。

唯一的实时通道是**轮询**：

```go
// backend/router/realtimeRouter.go:13-18
func RealtimeRoutes(r gin.IRouter, rc *controller.RealtimeController) {
	rt := r.Group("/realtime")
	rt.GET("/snapshot", rc.Snapshot)
}
```

数据源是**内存最新值**（`backend/realtime/store.go`），**不落库、无历史**：

- `realtime/store.go:6-11`（包注释）：网关本身不落地采集数据，采集结果经 `collector.RecordSink` 直接交给推送引擎；本包补上内存快照这一层。**只存最新值、不存历史**——历史趋势应由已推送的时序库承担。
- `realtime/store.go:40-43`：`Store{mu sync.RWMutex; data map[string]map[string]Point}` → `deviceID -> addressID -> 最新值`。
- `realtime/store.go:80-87`：`Lookup` 只返回调用方点名的点位，避免为一页 50 条复制上万条记录。

**DSH 侧含义**：想要"变化推送"必须在插件里自己实现定时轮询 + 差分。建议 1–2s 间隔（点位默认扫描频率即 1000ms，见 `deviceAddress.scanFrequency`）。

---

## 1. 产品概览

**一句话**：iot-gateway 是一个单二进制的工业物联网数据采集网关——用 15 种协议（全部为进程外插件）轮询 PLC / 工业设备与仪器仪表，把采集值推送到 MQTT / TDEngine / InfluxDB，并提供内嵌 Web 管理控制台、断联报警、局域网设备扫描与点位自动发现；本文分析的 premium 版本额外内置了一条**专供 AI Agent（dsh）调用的无鉴权全量 HTTP 调用面** `/agentApi/*`。

| 项 | 值 | 出处 |
|---|---|---|
| 产品名 | iot-gateway（工业 IoT 数据网关） | `README.md:1` |
| 配置版本号 | `1.0.0` | `backend/default.yaml:41` |
| **运行实例上报版本** | `1.0.0` | live `GET /agentApi/gateway/get` |
| Docker 镜像 tag | `iot-gateway:1.0` | `docker-compose.yml:11` |
| CHANGELOG | `## [Unreleased]` 为唯一顶层节，**无已发布版本号小节** | `CHANGELOG.md:7`（grep `^## \[[0-9]` 零命中） |
| 许可证 | GNU AGPL-3.0 | `LICENSE`、`README.md:265` |
| 语言/框架 | Go 1.26 + Gin + GORM + Google Wire + Viper + JWT | `README.md:246`、`backend/CLAUDE.md` |
| 存储 | SQLite（纯 Go `glebarez/sqlite`，免 CGO） | `README.md:247` |
| 前端 | Vue 3 + Vite + Element Plus + Pinia + Tailwind 4 + vue-i18n | `README.md:249` |
| 部署 | `go:embed` 单二进制；Windows 服务 / Linux systemd / Docker | `README.md:250` |

> 注：`backend/configFile/configFile.go:102-105` 说明 `gateway.version` **只在首次启动写入库**，升级二进制不会刷新库内已有值。因此 live 上报的 `1.0.0` 是**首启时**的版本，未必等于当前二进制版本。库内版本与配置不一致时启动日志会打一条 WARN（`default.yaml:38-40`）。

### 1.1 支持协议（全部由 `plugins/` 下的进程外插件提供）

`README.md:51-69`。**共 15 个协议名**，主二进制内**不含任何协议实现**（`README.md:15`）。

| 协议 | 传输 | 提供插件 | 支持设备扫描 | 支持点位扫描 |
|---|---|---|---|---|
| `ModBus.RTU` / `ModBus.TCP` | 串口 / 以太网 | `iot-driver-modbus` | ✅ | ✅ |
| `Siemens.S7` | 以太网 | `iot-driver-s7` | ✅ | ✅ |
| `Mitsubishi.MC.TCP` / `.Serial` | 以太网 / 串口 | `iot-driver-mitsubishi` | ✅ | ✅ |
| `Omron.FINS.UDP` / `.TCP` / `.Serial` / `.HostLinkTCP` | UDP/TCP/串口 | `iot-driver-fins` | 部分 | ❌（未真机验证） |
| `Omron.CIP` | EtherNet/IP | `iot-driver-omroncip` | ✅ | ❌（刻意不声明） |
| `Rockwell.CIP` | EtherNet/IP | `iot-driver-rockwellcip` | ✅ | ✅（原生枚举） |
| `OPC.UA` | 以太网 | `iot-driver-opcua` | ✅ | ✅（Browse 枚举） |
| `ABB.RWS` | HTTP/HTTPS | `iot-driver-abb` | — | — |
| `DLT645.Serial` / `.TCP` | 串口 / 以太网 | `iot-driver-dlt645` | ❌（刻意不声明） | ✅（数据标识字典，逐点带类型） |

**实测 `plugins/` 目录确有 9 个可执行文件**：`iot-driver-{abb,dlt645,fins,mitsubishi,modbus,omroncip,opcua,rockwellcip,s7}.exe`。
**实测 live `GET /agentApi/protocol/pageProtocol` 返回 `total: 15`** —— 与协议名数量完全吻合。

### 1.2 支持推送通道

`README.md:83-87`、`frontend/src/components/ChannelSelector.vue:52`（`['mqtt','tdengine-v3','influxdb-v3']`）。

| 通道 | 说明 |
|---|---|
| `mqtt` | Eclipse Paho MQTT，支持 QoS / TLS / 遗嘱 / 断网缓存 |
| `tdengine-v3` | TDEngine 3.x，官方 driver-go WebSocket（经 taosAdapter） |
| `influxdb-v3` | InfluxDB 3.x，自研 Line Protocol over HTTP |

> 推送通道是**进程内**插件（注册表 + 接口），与协议驱动的进程外插件形态不同。
> 出处：`backend/CLAUDE.md`「新增推送通道」节；`backend/main.go:22-24` 三个空导入。

---

## 2. 启动与端口

### 2.1 端口（**注意 README 已过期**）

| 用途 | 端口 | 出处 |
|---|---|---|
| **HTTP API + 管理控制台 + /agentApi** | **9082** | `backend/default.yaml:1-2`、`docker-compose.yml:14` |
| UDP 局域网自动发现（网关广播"我在哪"） | 9083 | `backend/default.yaml:65` |
| 开发模式前端 Vite | 4000 | `README.md:104,108` |

> ⚠️ **README.md 里的 9081 是过期值**：`README.md:100,120,147` 三处写 9081。
> 正确值 9082 —— 依据 ①`backend/default.yaml:2` 实配 `port: 9082`；
> ②`docker-compose.yml:14` 映射 `"9082:9082"`；③`apiCatalog.md:153-155` 明确记录
> 「出厂默认是 9082 —— 它在 2026-09-16 的提交 `33ab028` 里从 9081 改过来」；
> ④**实测**：`127.0.0.1:9082/health` 返回 `{"status":"ok"}`，且 live `gateway/get` 回报 `"port":9082`。

监听地址由 `backend/main.go:155-159` 决定：`addr := fmt.Sprintf(":%d", cfg.Server.Port)`，`http.Server{Addr, Handler: deps.Engine}` —— 绑定**所有网卡**（`:9082`），非仅 localhost。**DSH 插件默认应连 `http://127.0.0.1:9082`**，并允许配置覆盖。

### 2.2 启动方式

| 方式 | 命令 | 出处 |
|---|---|---|
| 开发（后端，热更新） | `cd backend && go run .` | `README.md:101` |
| 开发（前端） | `cd frontend && npm install && npm run dev`（:4000，代理到后端） | `README.md:104,108` |
| 生产（单二进制） | `./iot-gateway` → 控制台 `http://<IP>:9082/admin` | `README.md:120`（端口按实配 9082） |
| 一键打包 | `build.bat` / `build.sh`（默认 4 平台 + 各平台独立 `plugins/`） | `backend/CLAUDE.md` |
| Windows 服务 | 管理员 `install.bat` / `uninstall.bat [--purge]` | `README.md:129-130` |
| Linux 服务 | `sudo ./install.sh` / `sudo ./uninstall.sh [--purge]` | `README.md:130` |
| Docker Compose | `docker compose up -d --build` | `README.md:141` |

**当前工作区状态（实测）**：网关**已经在跑**。`backend/` 下同时存在 `iot-gateway.exe` 与 `plugins/*.exe`，且 `backend/data/iot-gateway.db`（598KB）与 `-wal`（4.1MB，最后写入 2026-09-30 14:12）均为活跃状态。**DSH 插件不需要自己启动网关**，直接连即可。

### 2.3 默认账号 / 密码

| 项 | 值 | 出处 |
|---|---|---|
| 用户名 | `admin` | `backend/database/sqlite/seed.go:21`（`defaultAdminUsername = "admin"`） |
| 默认口令 | `hand@123` | `backend/default.yaml:17`（`defaultAdminPassword: "hand@123"`） |
| 口令优先级 | 环境变量 `IOT_GATEWAY_ADMIN_PASSWORD` > 配置项 > **随机生成并只在日志打印一次** | `seed.go:84-94` |
| 播种时机 | **仅首次启动（`sys_user` 表为空）**；表非空即返回，不覆盖 | `seed.go:35-42` |

> ⚠️ **纠正 `README.md:154` 的说法**：README 写「首次启动自动初始化两个账号：`admin`、`hand`」，但
> `backend/database/sqlite/seed.go:20-73` **只播种 `admin` 一个账号**，全文件没有 `hand` 这个字面量。
> **实测 live `GET /agentApi/users` 返回 `total: 1`，唯一记录 `name: "admin"`** —— 证实 README 有误（疑为历史遗留）。
>
> 另注 `default.yaml:12-17` 明确：`defaultAdminPassword` **仅在首次启动（用户表为空）时生效**，账号建好后本项不再被读取；改密码要走登录后的修改密码接口。

**对 DSH 的实际影响**：因为走 `/agentApi` 无鉴权，**默认账号密码对 DSH 插件是可选项**——仅当插件需要回落到 JWT 通道（如未来新接口未被镜像）或需要在 Web UI 里人工核对时才用得上。

### 2.4 配置文件位置与格式

| 优先级 | 位置 | 说明 |
|---|---|---|
| 1（最低） | **嵌入二进制的 `default.yaml`** | 经 `go:embed` 注入（`config_embed.go`），单二进制部署无需外部配置 |
| 2 | **运行目录下的 `default.yaml`** | 存在则逐键合并，覆盖嵌入值 |
| 3（最高，文件维度） | `APP_ENV=dev\|prod` 指定的 `dev.yaml` / `prod.yaml` | 逐键合并覆盖 |
| 4（最高，单项维度） | 环境变量 | `IOT_GATEWAY_JWT_SECRET` / `IOT_GATEWAY_ADMIN_PASSWORD` / `IOT_GATEWAY_NAME` / `IOT_GATEWAY_VERSION` |

出处：`backend/configFile/configFile.go:242-299`（`InitConfig` 的四段加载 + 逐键合并）、环境变量常量在 `:17-28`。

**格式 YAML**。顶层键（`configFile.go:37-51`）：`server` / `jwt` / `auth` / `log` / `sqlite` / `gateway` / `discovery` / `deviceScan` / `plugin`。

关键项（`backend/default.yaml`）：

```yaml
server:
  port: 9082                     # :2   HTTP 端口
jwt:
  secret: "019fabb7..."          # :8   留空则每次启动随机生成（重启后登录态全失效）
  expireHour: 24                 # :9
  issuer: iot-gateway            # :10
auth:
  defaultAdminPassword: "hand@123"  # :17  仅首启生效
log:
  level: INFO                    # :20
sqlite:
  path: data/iot-gateway.db      # :26
gateway:
  name: ""                       # :37  留空 → IOT_GATEWAY_NAME > 本项 > 主机名 > "IoT Gateway"
  version: "1.0.0"               # :41  权威版本，每次启动同步进库；页面只读
discovery:
  enabled: true                  # :51
  port: 9083                     # :65  UDP
plugin:
  enabled: true                  # :122 **出厂即 true**，关掉=不支持任何协议
  dir: plugins                   # :126 相对 os.Executable() 解析，非 cwd
```

> `configFile.go:150-152` 的结构体注释写着 `Enabled 总开关。默认 false`，但 `default.yaml:122` 实配 `enabled: true`。
> **以 `default.yaml` 为准**（`README.md:75` 也确认"出厂即为 true"）。注释与实配不一致，属代码陈旧注释。

---

## 3. 认证流程

网关有**三条互不相通**的通道（`apiCatalog.md:25-31`）：

| 通道 | 覆盖范围 | 认证方式 | DSH 友好度 |
|---|---|---|---|
| **`/agentApi/*`** | **全部 82 个领域接口（含全部写操作）** | **无任何鉴权** | ★★★ **DSH 应走这条** |
| `/openApi/*` | 仅 4 个只读接口 | 请求头 `X-Api-Key` | ★★★ 可用 |
| 根路径管理接口 | 只读 + 全部编辑能力 | `Authorization: Bearer <JWT>` | ★ 需 RSA-OAEP 登录 |

### 3.1 通道 A：`/agentApi/*` —— **无认证（DSH 首选）**

**无需任何 header、无需登录、无需 token。** 请求示例：

```http
GET /agentApi/device/pageDevice?page=1&size=20 HTTP/1.1
Host: 127.0.0.1:9082

（无任何认证头）
```

实测响应（HTTP 200）：

```json
{
  "code": "0", "msg": "success", "isSuccess": true,
  "data": { "page": 1, "size": 20, "total": 8, "records": [ ... ] },
  "serverTime": "2026-09-30 14:22:22"
}
```

### 3.2 通道 B：`/openApi/*` —— `X-Api-Key`（只读，4 个接口）

中间件实现在 `backend/middleware/openApiAuthMiddleware.go`：

```go
// openApiAuthMiddleware.go:17
const OpenApiKeyHeader = "X-Api-Key"

// openApiAuthMiddleware.go:24-43
key := c.GetHeader(OpenApiKeyHeader)
if key == "" { → HTTP 401, code=70003 "开放接口密钥为空" }
exists, err := secretSvc.ExistsOpenApiSecretKey(ctx, key)
if err != nil || !exists { → HTTP 401, code=70004 "开放接口密钥无效" }
```

挂载点在 `backend/router/openApiRouter.go:18`（`r.Group("/openApi", middleware.OpenApiAuthMiddleware(oass))`）。

```http
GET /openApi/device/page?page=1&size=20 HTTP/1.1
Host: 127.0.0.1:9082
X-Api-Key: <管理员在控制台「密钥管理」生成的 AppKey>
```

**实测无 key 时**：

```
GET http://127.0.0.1:9082/openApi/device/page?page=1&size=1
→ HTTP 401  {"code":"70003","msg":"开放接口密钥为空","isSuccess":false,"data":null,...}
```

**密钥从哪来**（两条路，都不需要 JWT 之外的登录——`/agentApi` 已覆盖）：
1. `POST /agentApi/openApiSecret/create`，body `{"name":"dsh"}`（`openApiSecretDto.go:9-11`）。
   **成功返回 `data: null`，不返回新建的 key**。
2. 然后 `GET /agentApi/openApiSecret/list` 取回 **明文 AppKey**（`agentApiSurface.go:249-252` 的 Note：*"响应含**明文 AppKey** —— 拿到它就能调 /openApi 的只读接口"*）。

> ⚠️ `list` 是 `AccessRead`，但会把**明文密钥**交出去。DSH 侧应把它当作敏感操作用对待。

### 3.3 通道 C：根路径 —— JWT（**DSH 不建议走**）

**登录的 `username` 与 `password` 都不是明文**，而是 **RSA-OAEP / SHA-256** 加密后 Base64 的字符串（`apiCatalog.md:230-243`）。

服务端解密证据：

```go
// backend/service/userService.go:116-124
func (s *UserService) Login(ctx context.Context, req *dto.LoginDTO) (*vo.LoginVO, error) {
	username, err := utils.RSADecrypt(req.Username, utils.PrivateKey)
	password, err := utils.RSADecrypt(req.Password, utils.PrivateKey)
```

加密参数（**必须完全一致**，`apiCatalog.md:236-243`）：

| 项 | 值 |
|---|---|
| 算法 | `RSA-OAEP` |
| OAEP 摘要 / MGF1 | `SHA-256` |
| OAEP label | 空（Go 侧 `nil`） |
| 公钥格式 | Base64 编码的 PEM |
| 输出编码 | Base64 |
| 密钥长度 | 2048 位（单次可加密明文上限 **190 字节**） |

**公钥从哪来：网关没有下发公钥的接口，是硬编码常量**（`apiCatalog.md:245-250`）：

- 后端：`backend/utils/rsaUtil.go:17` 的 `utils.PublicKey`
- 前端：`frontend/src/utils/rsa.js` 的 `PUBLIC_KEY_B64`
- **两者是同一串值**；DSH 需把该常量复制一份到自己这侧

> **陷阱**：`utils.PublicKey`（`rsaUtil.go:17`）的 PEM 头是 `-----BEGIN RSA PUBLIC KEY-----`（PKCS#1），
> 而 `utils.GenerateRSAKey`（`rsaUtil.go:36-44`）生成的是 `-----BEGIN PUBLIC KEY-----`（X.509 SPKI）。
> `stringToPublicKey`（`rsaUtil.go:88`）用 `x509.ParsePKCS1PublicKey` 解析，**只认 PKCS#1**。
> 前端 `rsa.js:121-150` 因此专门做了 PKCS#1→SPKI 的 DER 转换（`rsa.js:84-94`）+ WebCrypto 导入。
> DSH 侧若用 Node `crypto.publicEncrypt` 需注意：**直接喂这串 PEM 给 Node 可能因头标识不一致而失败**，
> 稳妥做法是用 `node-forge`（与前端 `rsa.js:174-183` 同一路径）或自行做 PKCS#1→SPKI 转换。

**登录请求 / 响应**：

```http
POST /user/login HTTP/1.1
Host: 127.0.0.1:9082
Content-Type: application/json

{
  "username": "<RSA-OAEP(SHA-256) 密文的 Base64>",
  "password": "<RSA-OAEP(SHA-256) 密文的 Base64>"
}
```

```json
{ "code":"0","isSuccess":true,
  "data": { "accessToken":"<JWT>", "expiresIn": 86400, "expiresAt":"2026-10-01T14:22:22+08:00" } }
```

后续请求：

```http
GET /device/pageDevice?page=1&size=20 HTTP/1.1
Authorization: Bearer <accessToken>
```

**关键点**：
- 用户名与密码**必须两次独立加密**，不可拼接后一起加密（`apiCatalog.md:258`）—— RSA-OAEP 单次明文上限 190 字节，拼接会失败。
- 令牌有效期 `jwt.expireHour`（默认 24h，`default.yaml:9`）。
- JWT 用 **HS256** 签名（`backend/utils/jwtutil.go:44`），密钥来自 `jwt.secret`，**留空则每次启动随机生成**（`configFile.go:306-322`），重启后全部登录态失效。
- 改密码 `PUT /user/password` 的三个字段（`oldPassword`/`newPassword`/`confirmPassword`）**同样全部 RSA 加密**，且**用户名从 JWT 声明取，请求体里不带**（`userController.go:116-118`，`userService.go:160-172`）。

**实测无 JWT 时**：`GET /device/pageDevice` → `HTTP 401 {"code":"20006","msg":"令牌为空"}`。

### 3.4 认证方式选型建议（给 DSH 插件）

```
1. 默认：一律走 /agentApi/*，零认证。覆盖全部 82 个领域接口，无能力缺口。
2. 仅当需要「对外只读契约」语义时，才用 /openApi/* + X-Api-Key。
3. 不要实现 JWT 通道 —— RSA 加密登录对 Agent 是纯粹的负担
   （apiCatalog.md:647 原文：「不要让 dsh 走 JWT 通道」）。
4. 部署前提：确认网关处于隔离内网、或 dsh 与网关同机（apiCatalog.md:652）。
   若网关暴露到不可信网络，须先给 /agentApi 加回鉴权再上线。
```

---

## 4. REST API 端点清单

### 4.0 通用约定

| 约定 | 内容 | 出处 |
|---|---|---|
| **基础地址** | `http://<网关IP>:9082`（DSH 默认 `127.0.0.1:9082`） | `default.yaml:2`、实测 |
| **HTTP 状态码** | **除 401 与文件下载外，恒为 200**；必须读 body 的 `code`/`isSuccess` 判成败 | `response.go:26-44` |
| **统一信封** | `{code(string), msg, isSuccess(bool), data, serverTime}` | `backend/response/response.go:9-15` |
| **成功 code** | 字符串 `"0"`（**不是数字 0**） | `response.go:28` |
| **失败 code** | 字符串业务码，`data=null` | `response.go:37-44` |
| **分页结构** | `PageVo{page,size,total,records}` | `response.go:18-23` |
| **分页参数** | `page`(≥1, 必填) + `size`(1–100, 必填)；**几乎全部分页接口都必填** | `deviceDto.go:89-93` |
| **时间格式** | `YYYY-MM-DD HH:mm:ss`（本地时区）；JWT `expiresAt` 例外用 RFC3339 | `apiCatalog.md:192-195` |
| **i18n** | 可选请求头 `area`: `zh`(默认)/`en`/`zh-hk`，**只影响错误文案** | `middleware/areaMiddleware.go`、`apiCatalog.md:197-200` |

**统一信封实测样例**：

```json
{
  "code": "0", "msg": "success", "isSuccess": true,
  "data": {
    "running": true, "lastPollTime": "2026-09-30 14:22:21",
    "lastSuccessTime": "2026-09-30 14:22:21",
    "errorCount": 1, "deviceCount": 8, "addressCount": 8
  },
  "serverTime": "2026-09-30 14:22:22"
}
```

### 4.1 元信息 / 清单

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/__surface` | **接口分类清单（read/probe/write + confirm + note）** | — | `router/agentApiSurface.go:69-70,353-355`；`router/agentApiRouter.go:67` |
| GET | `/health` | 健康检查，**裸 JSON 非信封** | — | `router/router.go:34-36` |

`/health` 实测：`{"status":"ok"}`（注意**没有** `code`/`isSuccess` 包裹）。

### 4.2 网关身份 / 系统状态

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/gateway/get` | 网关自身身份（id/name/port/firmwareVersion/description） | — | `router/gatewayRouter.go:19`；`controller/gatewayController.go:?` |
| POST | `/agentApi/gateway/update` | 改网关名称与描述（🔒 write） | `name`*(1–100), `description`(≤500) | `gatewayRouter.go:20`；`model/dto/gatewayDto.go` |
| GET | `/agentApi/collection/status` | 采集引擎状态 | — | `router/collectionRouter.go:19` |
| POST | `/agentApi/collection/start` | 启动采集（🔒 write） | — | `collectionRouter.go:16` |
| POST | `/agentApi/collection/stop` | 停止采集（🔒 write） | — | `collectionRouter.go:17` |
| POST | `/agentApi/collection/refresh` | 热刷新采集配置（🔒 write，通常无需手动调） | — | `collectionRouter.go:18` |
| GET | `/agentApi/push/status` | 推送通道运行状态数组 | — | `router/pushRouter.go:16` |
| POST | `/agentApi/push/refresh` | 热刷新推送配置（🔒 write） | — | `pushRouter.go:17` |
| GET | `/agentApi/plugin/info` | 插件目录 / 命名规则 / 总开关 | — | `router/pluginRouter.go:16` |
| POST | `/agentApi/plugin/importPlugin` | ⚠️ 上传并加载**可执行文件**（🔒 write，**等价 RCE**） | multipart 字段名 **`files`**（可多个），总上限 512MiB | `pluginRouter.go:17`；`controller/pluginController.go:22-37,61-79` |
| GET | `/agentApi/log/listFiles` | 日志文件分页（**page/size 可省略**，服务端兜底 1/20） | `page`,`size` | `router/logfileRouter.go:17`；`model/dto/logfileDto.go` |
| GET | `/agentApi/log/readFile` | 读日志末尾 N 行 | `fileName`*, `tail`(默认 200，上限 5000) | `logfileRouter.go:18` |

**实测响应**：

```json
// GET /agentApi/gateway/get
{"code":"0","msg":"success","isSuccess":true,
 "data":{"id":"94ea29ca23a94cffb279474acab664ef","name":"23VJV25B27302557","port":9082,
         "firmwareVersion":"1.0.0","description":"","status":1,
         "createdAt":"2026-09-18 14:50:27","updatedAt":"2026-09-18 14:50:27"},
 "serverTime":"2026-09-30 14:22:22"}

// GET /agentApi/collection/status
{"code":"0",...,"data":{"running":true,"lastPollTime":"2026-09-30 14:22:21",
 "lastSuccessTime":"2026-09-30 14:22:21","errorCount":1,"deviceCount":8,"addressCount":8}}

// GET /agentApi/push/status   （data 是**数组**，不是 PageVo）
{"code":"0",...,"data":[{"id":"01a0c363b1487c73b02e1726c481d193","name":"mqtt","type":"mqtt",
 "running":true,"connected":true,"broker":"tcp://172.22.5.23:1888","topic":"wer",
 "queueDepth":0,"spoolDepth":0,"publishCount":1779,"droppedCount":0,
 "lastPublishTime":"2026-09-30 14:22:35","lastSuccessTime":"2026-09-30 14:22:35","error":""}]}

// GET /agentApi/plugin/info
{"code":"0",...,"data":{"os":"windows","arch":"amd64",
 "dir":"D:\\code\\汉得\\iot-gateway-premium\\backend\\plugins",
 "glob":"iot-driver-*","fileExt":".exe","enabled":true}}
```

`ChannelStatusVO` 结构出处：`backend/push/status.go:16`（`Topic` 字段）等；`push/outbox.go:323` 注释「通道负责先补齐自身类型相关的字段（Type/Broker/Topic）」。

### 4.3 设备管理

路由：`backend/router/deviceRouter.go:13-26`（`device := r.Group("/device")`）

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/device/pageDevice` | 设备分页 | `page`*, `size`*, `name`(模糊) | `deviceRouter.go:22`；`dto/deviceDto.go:89-93` |
| GET | `/agentApi/device/getDeviceById/:id` | 按 ID 查设备 | 路径 `id` | `deviceRouter.go:20` |
| GET | `/agentApi/device/overview` | 设备在线统计 | — | `deviceRouter.go:23` |
| POST | `/agentApi/device/createDevice` | 创建设备（🔒 write，**data 为 null 不回 id**） | `name`*(1–100), `protocolId`*, `protocolJson`*, `description` | `deviceRouter.go:16`；`deviceDto.go:11-16` |
| POST | `/agentApi/device/updateDevice` | 更新设备（🔒 write） | `id`,`name`,`protocolId`,`protocolJson`,`description`,`status` | `deviceRouter.go:17`；`deviceDto.go:19-26` |
| 🗑 GET | `/agentApi/device/deleteDevice/:id` | **删除设备**（🔒 write，**用 GET 实现**，连带删全部点位） | 路径 `id` | `deviceRouter.go:18` |
| 🗑 POST | `/agentApi/device/batchDeleteDevice` | **批量删除设备**（🔒 write，连带删点位） | `ids`*[]（1–200） | `deviceRouter.go:19`；`deviceDto.go:43-45` |
| ⚠️ POST | `/agentApi/device/testDeviceConnection` | 真实连一次 PLC（🔒 probe，不可达时等满超时） | `protocolName`*, `protocolJson`* | `deviceRouter.go:21`；`deviceDto.go:30-33` |
| POST | `/agentApi/device/importDevice` | Excel 批量导入（🔒 write） | multipart 字段名 **`file`**（`.xlsx`） | `deviceRouter.go:24` |
| GET | `/agentApi/device/importTemplate` | 下载导入模板（**返回 .xlsx 文件流，非 JSON 信封**） | — | `deviceRouter.go:25` |

**实测 `pageDevice` 单条记录**（注意 `protocolJson` 是**字符串化的 JSON**，不是嵌套对象）：

```json
{
  "id": "01a0f032868f75e1959e1895b308900f",
  "iotGatewayId": "",
  "name": "10.62.100.12",
  "protocolId": "01a0b39eae5478499644eb96aa037f35",
  "protocolName": "OPC.UA",
  "protocolJson": "{\"authMode\":\"Anonymous\",\"host\":\"10.62.100.12\",\"port\":4840,\"securityMode\":\"None\",\"securityPolicy\":\"None\"}",
  "description": "", "status": 1, "online": true,
  "lastSuccessTime": "2026-09-30 14:22:30",
  "createdAt": "2026-09-30 10:43:58", "updatedAt": "2026-09-30 10:43:58"
}
```

- **`protocolJson` 是字符串**（`po/device.go:17` 声明为 `string`，`dto/deviceDto.go:14` 请求侧是 `json.RawMessage`）——**写入时传 JSON 对象，读出时拿到字符串**，DSH 侧需 `JSON.parse` 二次解析。
- `online` 语义 = 「无 active 断联报警」（`apiCatalog.md:369`），**不是实时 ping**。
- `lastSuccessTime` 是**内存态**，空串表示尚无成功记录（重启后短暂为空，属正常，`apiCatalog.md:369-370`）。

**实测 `overview`**：

```json
{"code":"0",...,"data":{"total":8,"enabled":8,"disabled":0,"collected":3,
 "online":2,"offline":1,"unCollected":5,"onlineRate":0.67}}
```

`onlineRate = online / collected`，保留 2 位小数（`apiCatalog.md:385`）。

**批量删除返回** `BatchDeleteDeviceResultVO{requested, deleted, addresses}`；`addresses` 是**随设备一并删掉的点位行数**；`deleted` 可能小于 `requested`（属正常，`apiCatalog.md:460-463`）。

### 4.4 设备点位（device_address / tag）

路由：`backend/router/deviceAddressRouter.go:13-24`（`addr := r.Group("/deviceAddress")`）

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/deviceAddress/pageDeviceAddress` | 点位分页 | `page`*, `size`*, **`deviceId`***, `name`(模糊) | `deviceAddressRouter.go:21`；`dto/deviceDto.go:96-101` |
| GET | `/agentApi/deviceAddress/getDeviceAddressById/:id` | 按 ID 查点位 | 路径 `id` | `deviceAddressRouter.go:20` |
| POST | `/agentApi/deviceAddress/createDeviceAddress` | 创建点位（🔒 write） | `deviceId`*, `name`*, `label`*, `commonDataType`, `rwPermission`*(R/W/RW), `scanFrequency`, `description` | `deviceAddressRouter.go:16`；`deviceDto.go:52-61` |
| POST | `/agentApi/deviceAddress/updateDeviceAddress` | 更新点位（🔒 write） | `id`*, `label`*, 其余可选, `status` | `deviceAddressRouter.go:17`；`deviceDto.go:64-74` |
| 🗑 GET | `/agentApi/deviceAddress/deleteDeviceAddress/:id` | **删除点位**（🔒 write，**用 GET 实现**，静默生效） | 路径 `id` | `deviceAddressRouter.go:18` |
| 🗑 POST | `/agentApi/deviceAddress/batchDeleteDeviceAddress` | **批量删除点位**（🔒 write） | `deviceId`*(范围约束), `ids`*[](1–200) | `deviceAddressRouter.go:19`；`deviceDto.go:81-84` |
| POST | `/agentApi/deviceAddress/importDeviceAddress` | Excel 批量导入（🔒 write） | multipart：**`file`**(.xlsx) + 普通字段 **`deviceId`** | `deviceAddressRouter.go:22` |
| GET | `/agentApi/deviceAddress/importDeviceAddressTemplate` | 下载模板（**xlsx 文件流**） | — | `deviceAddressRouter.go:23` |

**实测 `pageDeviceAddress` 单条记录**：

```json
{
  "id": "01a0f03616bf7728ab4571abb0c81107",
  "deviceId": "01a0f032868f75e1959e1895b308900f",
  "name": "i=2255",
  "label": "Server/NamespaceArray",
  "commonDataType": "String",
  "dataType": "string",
  "rwPermission": "R",
  "scanFrequency": 1000,
  "description": "", "status": 1,
  "createdAt": "2026-09-30 10:47:51", "updatedAt": "2026-09-30 10:47:51"
}
```

> ⚠️⚠️ **最重要的一条业务约束**：`name` 是**设备地址写法**（如 `%MW100`、`DB1.DBW100`、`D100`、`i=2255`），
> 由协议决定。**服务端不校验写法**（驱动在插件里，主二进制不含协议解析器）——
> 校验只发生在驱动的读取路径上，写坏的后果是**这个点位永远没有值**（只报一条点位
> 质量报警「地址无效」，要改配置才恢复），不影响同设备的其它点位。
> 出处：`router/agentApiSurface.go` 的 createDeviceAddress / deviceScan.createAddresses
> 两条 Note、`apiCatalog.md` 的设备地址节、`backend/CLAUDE.md`「点位范围扫描」节。
> **DSH 绝不应自行发明点位名**，必须从 `pageDeviceAddress` 或点扫结果里取（`apiCatalog.md:618-619`）。

- `rwPermission` 只允许 `R` / `W` / `RW`（`deviceDto.go:58` 的 `binding:"required,oneof=R W RW"`）。
- `label` 是**地址的中文说明**（如「温度」「电流」），创建时必填。
- `dataType` 是**兼容旧请求**字段；新调用方应传 `commonDataType`（通用类型名如 `Word`），服务端结合协议映射出内部 `data_type`（`deviceDto.go:50-51,56-57`；`service/deviceAddressService.go:320` `resolveDataType`）。
- `scanFrequency` 默认 1000（毫秒）（`po/deviceAddress.go:21`）。

### 4.5 实时值

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/realtime/snapshot` | 设备实时数据快照（**内存最新值，非点位定义**） | `page`*, `size`*, `deviceId`*, `name`(点位名/说明模糊) | `router/realtimeRouter.go:16`；`dto/realtimeDto.go:10-14` |

**实测响应**（**注意不是 `PageVo`，是带上下文的专用 VO**）：

```json
{
  "code":"0","msg":"success","isSuccess":true,
  "data":{
    "deviceId":"01a0f032868f75e1959e1895b308900f",
    "deviceName":"10.62.100.12",
    "protocolName":"OPC.UA",
    "online":true,"collecting":true,"collectorRunning":true,
    "lastSuccessTime":"2026-09-30 14:22:35","serverTime":"2026-09-30 14:22:35",
    "page":1,"size":2,"total":2,
    "records":[
      {"addressId":"01a0f03363f573b7919d1bf962651d3f","name":"i=2994",
       "label":"Server/Auditing","value":"1","dataType":"bool","commonDataType":"Boolean",
       "rwPermission":"R","scanFrequency":1000,"quality":192,
       "collected":true,"stale":false,"updatedAt":"2026-09-30 14:22:35"},
      {"addressId":"01a0f03616bf7728ab4571abb0c81107","name":"i=2255",
       "label":"Server/NamespaceArray",
       "value":"http://opcfoundation.org/UA/,urn:SIMATIC.S7-1500.OPC-UA.Application:PLC_1,...",
       "dataType":"string","commonDataType":"String","rwPermission":"R",
       "scanFrequency":1000,"quality":192,"collected":true,"stale":false,
       "updatedAt":"2026-09-30 14:22:35"}
    ]
  },
  "serverTime":"2026-09-30 14:22:35"
}
```

**三个状态位组合可解释「为什么没数据」**（`apiCatalog.md:379-382`）：

| 字段 | 含义 |
|---|---|
| `collectorRunning=false` | 采集引擎未启动 |
| `collecting=false` | 协议不可用或无活跃点位 |
| `online=false` | 设备已判离线 |

未采集到的点位**同样占位返回**（`collected=false`），使行数稳定。

> **实时值不在 `deviceAddress` 里**：`realtime/snapshot` 取的是内存最新值（秒级轮询用）；
> `deviceAddress/pageDeviceAddress` 取的是**点位定义**。两者别混（`apiCatalog.md:622-623`）。

### 4.6 协议管理

路由：`backend/router/protocolRouter.go:13-28`（`protocol := r.Group("/protocol")`）

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/protocol/pageProtocol` | 协议分页（**实测 total=15**） | `page`*, `size`*, `name` | `protocolRouter.go:27` |
| GET | `/agentApi/protocol/getProtocolById/:id` | 按 ID 查协议 | 路径 `id` | `protocolRouter.go:21` |
| GET | `/agentApi/protocol/getProtocolByName` | 按名查协议 | query `name` | `protocolRouter.go:22` |
| GET | `/agentApi/protocol/getDataTypes` | 协议可用数据类型清单 | query `protocolName` **或** `protocolId` | `protocolRouter.go:23` |
| POST | `/agentApi/protocol/updateProtocol` | 更新协议（🔒 write，**name 仅用于校验未被改动**，改名返回 `50003`） | `id`,`name`,`description`,`formJson`,`sort` | `protocolRouter.go:20` |
| POST | `/agentApi/protocol/updateProtocolFormByName` | 按名更新表单 JSON（🔒 write） | `name`*, `formJson`* | `protocolRouter.go:24` |
| POST | `/agentApi/protocol/updateProtocolFormById` | 按 ID 更新表单 JSON（🔒 write） | `id`*, `formJson`* | `protocolRouter.go:25` |
| POST | `/agentApi/protocol/resetProtocolForm/:id` | 恢复为插件声明的默认表单（🔒 write，**丢弃运维自定义**） | 路径 `id` | `protocolRouter.go:26` |

> **没有 `createProtocol` / `deleteProtocol`** —— 协议行**全部由插件供给**
> （`pluginhost/provision.go`，只插入/只补空，绝不覆盖运维改过的非空表单）。
> 手工建的行没有驱动与之对应；删掉的行会在插件下次加载时被重新插回。
> 出处：`protocolRouter.go:16-19` 注释原文。

`form_json` 是**动态表单 JSON Schema**（前端 `form-create` 渲染），实测结构形如：

```json
{"rule":[
  {"type":"select","field":"protocolVersion","title":"协议版本","$required":true,
   "value":"2007","info":"2007 版数据标识为 4 字节，1997 版为 2 字节；选错表现为设备无应答（超时）",
   "options":[{"label":"DL/T 645-2007","value":"2007"},{"label":"DL/T 645-1997","value":"1997"}]},
  ...
]}
```

**DSH 用法**：`getProtocolByName` 拿 `formJson` → 向用户展示/填参 → 拼成 `protocolJson` 传给 `createDevice`。
**不同协议的 `protocolJson` 结构完全不同**，服务端以 `json.RawMessage` 原样透传、不做固定结构解析（`dto/deviceDto.go:29-32` 注释）。

实测某设备 `protocolJson` 示例（OPC.UA）：
`{"authMode":"Anonymous","host":"10.62.100.12","port":4840,"securityMode":"None","securityPolicy":"None"}`

### 4.7 推送通道

路由：`backend/router/pushChannelRouter.go:13-22`、`pushChannelFormRouter.go:13-19`

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/pushChannel/pagePushChannel` | 通道分页（**响应带 configJson，含 MQTT 口令**） | `page`*, `size`*, `name` | `pushChannelRouter.go:20` |
| GET | `/agentApi/pushChannel/getPushChannelById/:id` | 按 ID 查通道（同样带 configJson） | 路径 `id` | `pushChannelRouter.go:18` |
| POST | `/agentApi/pushChannel/createPushChannel` | 创建通道（🔒 write） | `name`*(1–100), `description`, **`configJson`*** | `pushChannelRouter.go:16` |
| POST | `/agentApi/pushChannel/updatePushChannel` | 更新通道（🔒 write） | `id`*, `name`,`description`,`configJson`,`status` | `pushChannelRouter.go:17` |
| 🗑 GET | `/agentApi/pushChannel/deletePushChannel/:id` | **删除通道**（🔒 write，**用 GET 实现**） | 路径 `id` | `pushChannelRouter.go:19` |
| ⚠️ POST | `/agentApi/pushChannel/testConnectivity` | 真实连下游（🔒 probe，不保存配置） | `name`*, `configJson`* | `pushChannelRouter.go:21` |
| GET | `/agentApi/pushChannelForm/getPushChannelFormByName` | 按名查通道表单（🔒 read） | query `name` | `pushChannelFormRouter.go:18` |
| POST | `/agentApi/pushChannelForm/createPushChannelForm` | 创建通道表单（🔒 write） | `name`*, `formJson`* | `pushChannelFormRouter.go:16` |
| POST | `/agentApi/pushChannelForm/updatePushChannelForm` | 更新通道表单（🔒 write） | `id`*, `name`,`formJson` | `pushChannelFormRouter.go:17` |

**`configJson` 的 MQTT 结构**（真源 `backend/push/mqtt/mqttConfig.go:20-40`）：

```go
type mqttConfig struct {
	Broker   string  `json:"broker"`       // 必填
	Port     portStr `json:"port"`         // 兼容 "1888" 与 1888 两种写法
	ClientID string  `json:"clientId"`
	QoS      byte    `json:"qos"`          // 必填校验 0..2
	Topic    string  `json:"topic"`        // 必填；逗号分隔=多主题；`+` 会被替换为 deviceId
	Username string  `json:"username"`
	Password string  `json:"password"`
	PublishWorkers  int  `json:"publishWorkers"`  // 缺省 4，上限 64
	SpoolDisabled   bool `json:"spoolDisabled"`   // 缺省 false（即默认启用断网缓存）
	SpoolMaxBatches int  `json:"spoolMaxBatches"` // 缺省 100000
}
```

校验规则（`mqttConfig.go:87-107`）：`broker` 必填、`port` 须为数字串、`qos` ≤ 2、`topic` 必填。

连接地址构造（`mqttConfig.go:111-120`）：`broker` 已含 `://` 则直用，否则 `tcp://{broker}:{port}`，**port 缺省 1883**。

**实测 `pagePushChannel`**：

```json
{"code":"0",...,"data":{"page":1,"size":10,"total":1,"records":[
  {"id":"01a0c363b1487c73b02e1726c481d193","name":"mqtt","description":"",
   "configJson":{"broker":"172.22.5.23","port":"1888","topic":"wer"},
   "status":1,"createdAt":"2026-09-21 17:54:45","updatedAt":"2026-09-21 17:54:45"}]}}
```

> 注意 `configJson` 在 `pagePushChannel` 里被**反序列化成嵌套对象**（不是像 `protocolJson` 那样的字符串）——两者行为不一致，DSH 侧解析要分别处理。

### 4.8 报警与报警通知

路由：`backend/router/alarmRouter.go:13-18`、`alarmWebhookRouter.go:13-36`

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/alarm/pageAlarm` | 报警历史分页 | `page`*, `size`*, `targetName`(模糊), `targetType`(device\|channel\|point), `alarmType`(offline\|quality\|recover), `status`(active\|cleared) | `alarmRouter.go:16`；`dto/alarmDto.go:9-16` |
| GET | `/agentApi/alarm/listActive` | 当前活跃**断联**报警（= 离线设备 + 离线通道；不含点位质量报警） | — | `alarmRouter.go:17` |
| GET | `/agentApi/alarmWebhook/pageAlarmWebhook` | 报警通知分页 | `page`*, `size`*, `name`, `type`, `status` | `alarmWebhookRouter.go:20` |
| GET | `/agentApi/alarmWebhook/getAlarmWebhookById/:id` | 按 ID 查（**secret 永不回显**，恒 `""`，只有 `hasSecret`） | 路径 `id` | `alarmWebhookRouter.go:18` |
| GET | `/agentApi/alarmWebhook/listTypes` | **支持的 webhook 类型与各自 msgTypes（创建前应先调）** | — | `alarmWebhookRouter.go:24` |
| GET | `/agentApi/alarmWebhook/status` | 通知器运行状况 | — | `alarmWebhookRouter.go:22` |
| POST | `/agentApi/alarmWebhook/createAlarmWebhook` | 创建（🔒 write） | `name`*, `type`*, `description`, `url`*, `secret`, `msgType`, `atAll`, `atList` | `alarmWebhookRouter.go:16` |
| POST | `/agentApi/alarmWebhook/updateAlarmWebhook` | 更新（🔒 write，**零值不覆盖**） | `id`*, 其余全可选 | `alarmWebhookRouter.go:17` |
| 🗑 GET | `/agentApi/alarmWebhook/deleteAlarmWebhook/:id` | **删除**（🔒 write，**用 GET 实现**） | 路径 `id` | `alarmWebhookRouter.go:19` |
| POST | `/agentApi/alarmWebhook/refresh` | 热刷新通知配置（🔒 write） | — | `alarmWebhookRouter.go:23` |
| ⚠️ POST | `/agentApi/alarmWebhook/testSend` | **真的往外发一条报警消息**（🔒 probe） | `id`,`type`,`url`,`secret`,`msgType`,`atAll`,`atList`,`alarmType`,`targetType`,`targetName` | `alarmWebhookRouter.go:21` |
| GET | `/agentApi/alarmWebhookForm/getAlarmWebhookFormByName` | 按名查通知表单 | query `name` | `alarmWebhookRouter.go:34` |
| POST | `/agentApi/alarmWebhookForm/createAlarmWebhookForm` | 创建通知表单（🔒 write） | `name`*, `formJson`* | `alarmWebhookRouter.go:32` |
| POST | `/agentApi/alarmWebhookForm/updateAlarmWebhookForm` | 更新通知表单（🔒 write） | `id`*, `name`,`formJson` | `alarmWebhookRouter.go:33` |

**实测 `listActive`**：

```json
{"code":"0",...,"data":[
  {"id":"01a0ec7b8f4c78a0a2dde66d2c35a720",
   "targetId":"01a0eaf2f3b57d9fa434426bbe7560a2","targetName":"10.62.100.24",
   "targetType":"device","alarmType":"offline","alarmTypeName":"断联报警",
   "level":"warning","content":"设备 10.62.100.24 断联","status":"active",
   "statusName":"未恢复","firstOccurTime":"2026-09-29 17:25:15",
   "lastOccurTime":"2026-09-29 17:28:42","clearTime":"",
   "createdAt":"2026-09-29 17:25:15","updatedAt":"2026-09-29 17:25:15"}]}
```

**`Alarm` 数据模型**（`backend/model/po/alarm.go:15-32`）：`targetId` / `targetName`（名称快照，删除后仍可追溯）/ `targetType`(device|channel|point) / `alarmType`(offline|quality|recover) / `deviceId`（**仅 `target_type='point'` 的行填写**，点位报警所属设备）/ `level` / `content` / `status`(active|cleared) / `firstOccurTime` / `lastOccurTime` / `clearTime`。
语义：同一目标同时最多一条 `status='active'` 的记录；恢复时置 `cleared` 并写一条 `recover` 历史。
三类目标：`device`/`channel` 为**断联**报警（`alarmType=offline`）；`point` 为**采集点位质量**报警（`alarmType=quality`），只对能归咎于点位自身的子码报警（`quality` 为 `1`=地址级 / `3`=解析级，见下），设备停机/整段被拒/预算延迟等一律不报。详见 `backend/specs/alarm.md` §3.1。

**`updateAlarmWebhook` 的「零值不覆盖」陷阱**（`apiCatalog.md:530-534`，核实 `dto/alarmWebhookDto.go:4040` 长度文件）：

- `url`/`name`/`type` 等字符串：传空串 = **不改**（不是清空）
- `secret`/`atAll`/`atList`/`status` 是**指针类型**：传 `null`/省略 = 不修改；传 `""`/`false`/`0` = **清空**。要清空加签密钥必须显式传 `"secret": ""`

**支持的 webhook 类型**：`dingtalk` / `wecom` / `feishu` / `custom`（`apiCatalog.md:422`）；`msgType` 取值 `text`/`markdown`/`card`，**各平台支持子集不同，以 `listTypes` 返回为准**（`apiCatalog.md:419-420`）。`type` 合法性由 `notify.ValidateConfig` 判定，传错返回 `95003`。

**`TypeMeta` 示例**（`apiCatalog.md:413-417`）：

```json
{ "type":"dingtalk","label":"钉钉群机器人","msgTypes":["markdown","text"],
  "defaultMsgType":"markdown","supportsSign":true,"supportsAt":true,
  "urlPlaceholder":"https://oapi.dingtalk.com/robot/send?access_token=xxx" }
```

### 4.9 局域网设备扫描 + 点位扫描

路由：`backend/router/deviceScanRouter.go:21-35`（`scan := r.Group("/deviceScan")`）

> **两个方向相反的功能**，`deviceScanRouter.go:12-20` 注释明确分开：
> `/deviceScan` 是"扫网段"（目标由网关规划、无会话、可取消）；
> `/deviceScan/*Point*` 是"扫设备"（目标由 `deviceId` 定、**复用那台设备的采集会话**、一次 RPC 覆盖整段、**不可取消**）。
> 它们**共用同一个总开关** `deviceScan.enabled` 与错误码组。

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/deviceScan/capabilities` | **扫描页初始化：协议列表 + 网段列表 + 开关** | — | `deviceScanRouter.go:24` |
| ⚠️ POST | `/agentApi/deviceScan/start` | 发起扫描（🔒 probe，**同一时刻只允许一个任务**，否则 `40022`） | `protocol`*, `options`, `subnets`, `port` | `deviceScanRouter.go:25`；`dto/deviceScanDto.go:11-28` |
| GET | `/agentApi/deviceScan/status` | 任务状态（前端轮询用） | `jobId`*, `withDevices`(默认 true) | `deviceScanRouter.go:26`；`deviceScanDto.go:31-46` |
| ⚠️ POST | `/agentApi/deviceScan/cancel` | 取消在跑扫描（🔒 probe） | `jobId`* | `deviceScanRouter.go:27` |
| POST | `/agentApi/deviceScan/createDevices` | 把扫描结果批量建成设备（🔒 write，**两段式确认**） | `jobId`*, `items`*[], `confirmDuplicate` | `deviceScanRouter.go:28`；`deviceScanDto.go:54-70` |
| GET | `/agentApi/deviceScan/pointCapability` | 协议点扫能力（含表单与预算） | `protocolName`* | `deviceScanRouter.go:31`；`dto/devicePointScanDto.go:15-21` |
| ⚠️ POST | `/agentApi/deviceScan/discoverPoints` | **同步阻塞，最长约 7s**，且**占住该设备采集会话锁**（🔒 probe） | `deviceId`*, `options` | `deviceScanRouter.go:32`；`devicePointScanDto.go:24-34` |
| POST | `/agentApi/deviceScan/createAddresses` | 把点扫结果批量建成点位（🔒 write） | `deviceId`*, `items`*[] | `deviceScanRouter.go:33`；`devicePointScanDto.go:37-43` |

**实测 `capabilities`**（结构：`{enabled, protocols[], subnets[]}`）：

```json
{"code":"0",...,"data":{"enabled":true,"protocols":[
  {"name":"Mitsubishi.MC.TCP","protocolId":"01a0b39ea6ac7991b044fa0197311fd2",
   "title":"Mitsubishi MC 扫描",
   "description":"逐主机连接 5007/TCP，发一条 MC 批量读命令（3E 帧，读 D0 一个字），收到子头与结束码自洽的应答即认定是三菱 PLC。判据是「读到一次软元件数据」而不是「端口开着」……",
   "defaultPort":5007,"targetMode":"unicast","scanFormJson":{"rule":[...]}},
  {"name":"ModBus.TCP",...,"defaultPort":502,"targetMode":"unicast",...}
  ], ...}}
```

> ⚠️ `dto/deviceScanDto.go:12-13,21-23` 的注释写着「必须来自 `GET /deviceScan/protocols`」、
> 「只能从 `GET /deviceScan/subnets` 里选」—— **这两个接口并不存在**。实际合并进了
> `capabilities` 响应的 `data.protocols[]` 与 `data.subnets[]`。
> 出处：`apiCatalog.md:625-637`「代码注释里的一处失效引用（别照着调）」。**以本报告与
> `deviceScanRouter.go` 的实际注册为准。**

**实测 `pointCapability?protocolName=OPC.UA`**：

```json
{"code":"0",...,"data":{"enabled":true,"supported":true,"protocol":"OPC.UA",
  "spec":{"title":"枚举 OPC UA 节点","description":"……",
          "pointFormJson":{"rule":[{"type":"input","field":"startNode",...},
                                   {"type":"inputNumber","field":"maxDepth","value":8,...}]},
          "defaultCommonDataType":"Long","maxBudgetMs":20000,"effectiveBudgetMs":7000,
          "maxPoints":2000}}}
```

- **`effectiveBudgetMs: 7000`** —— 网关侧硬钳 7000ms（与 `main.go:173` 的 10s 关机宽限期耦合）。
  **DSH 侧 HTTP 超时必须 > 8 秒**，否则会在网关还没返回时先行超时（`apiCatalog.md:350-352,620-621`）。
- 各协议支持情况不同（`pointCapability.supported`）。已知 `Omron.CIP` **不声明**点扫能力（会返回 `40027`）。

**`createDevices` 的两段式确认**（`dto/deviceScanDto.go:62-69`）：

- `items[].index` 指向**任务快照里 `devices` 数组的下标**
- `protocolJson` 由**服务端从自己持有的快照里取**，不采信客户端
- 地址与库内既有设备重复时**默认不建**，只回 `code: "addressExists"` 的明细；用户确认后带 `confirmDuplicate: true` 重提

**`createAddresses` 与前者相反：采信客户端全部字段**（`devicePointScanDto.go:47-51` 注释）——点位的名称/类型本就是用户在界面上逐行编辑的产物；唯一的服务端权威是 `deviceId`（它决定协议，从而决定类型名怎么映射）。
`items[]` 字段：`name`*（设备地址写法）、`label`（留空兜底为 `name`）、`commonDataType`*、`rwPermission`、`scanFrequency`（≤0 按 1000）、`description`。

**`PointScanResultVO` 计数语义必须分清**（`apiCatalog.md:393-400`）：

| 字段 | 含义 |
|---|---|
| `scanned` | 尝试读取的地址数 |
| `readable` | 有应答数（**含值为 0 的**） |
| `unreadable` | 读不到数 |
| 不变量 | `len(points) <= readable <= scanned`，且 `readable + unreadable == scanned` |
| `truncated` | 达到 `maxPoints`，后面的地址根本没试 |
| `incomplete` | 预算耗尽、范围没扫完（**与"读不到"是两回事**） |
| `warning` | 非空表示**中途断了**，`points` 是断之前已读到的部分（≠失败；失败时无 `points`） |
| `assumedCommonDataType` | 为空 == 本次扫描没有采样值（如 Rockwell.CIP 只列标签不读值） |

> **点位扫描产出的是「该范围内读得到的地址清单」，是候选清单而非设备的点位表** ——
> 设备不会告诉你它有哪些点位。插件刻意**不做零值过滤**（过滤在调用方侧做）。
> 出处：`apiCatalog.md:402-403`、`backend/CLAUDE.md`。

### 4.10 用户

路由：`backend/router/userRouter.go:14-25`

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| POST | `/agentApi/user/login` | 登录换 JWT（**账号密码需 RSA 加密**；对 DSH **不需要**） | `username`*, `password`*（均密文） | `userRouter.go:16`；`dto/userDto.go:27-30` |
| GET | `/agentApi/profile` | 当前登录用户信息 | — | `userRouter.go:23` |
| GET | `/agentApi/users` | 用户分页（**实测 total=1，仅 admin**） | `page`*, `size`* | `userRouter.go:22` |
| GET | `/agentApi/users/:id` | 按 ID 查用户 | 路径 `id` | `userRouter.go:20` |
| POST | `/agentApi/createUser` | 创建用户（🔒 write） | `username`*(2–50), `password`*(≥6) | `userRouter.go:19`；`userDto.go:7-10` |
| PUT | `/agentApi/users/:id` | 更新用户资料（🔒 write） | `name`(2–50), `email`, `avatar` | `userRouter.go:21`；`userDto.go:13-17` |
| PUT | `/agentApi/user/password` | 改**当前令牌所属**用户密码（🔒 write，3 字段全 RSA 加密） | `oldPassword`*, `newPassword`*, `confirmPassword`* | `userRouter.go:24`；`userDto.go:20-24` |

**实测 `GET /agentApi/users`**：

```json
{"code":"0",...,"data":{"page":1,"size":20,"total":1,"records":[
  {"id":"55cdc937bc574a15b30fa6122da8f09f","name":"admin","email":"",
   "avatar":"","status":1,"createdAt":"2026-09-18 14:50:27"}]}}
```

> `po.User` 的 JSON tag 把 `Username` 映射为 `"name"`（`model/po/user.go:9`），`Password` 是 `json:"-"`（永不出参，`:11`）。

### 4.11 开放接口密钥管理

路由：`backend/router/openApiSecretRouter.go:13-22`

| 方法 | 路径 | 说明 | 关键参数 | 出处 |
|---|---|---|---|---|
| GET | `/agentApi/openApiSecret/list` | 密钥列表（**⚠️ 响应含明文 AppKey**） | — | `openApiSecretRouter.go:20` |
| GET | `/agentApi/openApiSecret/doc` | **`data` 是渲染好的 HTML 字符串，不是 JSON 对象** | — | `openApiSecretRouter.go:17`；`controller/openApiSecretController.go:69-72` |
| POST | `/agentApi/openApiSecret/create` | 创建密钥（🔒 write，**data 为 null，不回 key**） | `name`*(1–100) | `openApiSecretRouter.go:18`；`dto/openApiSecretDto.go:9-11` |
| POST | `/agentApi/openApiSecret/updateName` | 编辑密钥名称（🔒 write） | `id`*, `name`* | `openApiSecretRouter.go:19`；`openApiSecretDto.go:14-17` |
| 🗑 GET | `/agentApi/openApiSecret/delete/:id` | **删除密钥**（🔒 write，**用 GET 实现**，外部系统立即失访） | 路径 `id` | `openApiSecretRouter.go:21` |

### 4.12 对外只读接口 `/openApi/*`（X-Api-Key，**不走 /agentApi**）

`backend/router/openApiRouter.go:17-25`。**刻意不在 `/agentApi` 下挂载**（`agentApiRouter.go:100-102` 注释：它自带 X-Api-Key 中间件，挂进去会得到既多一层前缀、又仍要密钥的路径，对 dsh 没有意义）。

| 方法 | 路径 | 说明 | 关键参数 |
|---|---|---|---|
| GET | `/openApi/device/page` | 设备分页（脱敏 VO） | `page`*, `size`*, `name` |
| POST | `/openApi/device/names` | 按 ID 批量查设备名（**POST 但纯查询**） | body `ids`*[] |
| POST | `/openApi/deviceAddress/labels` | 按设备+点位 ID 批量查标签（**POST 但纯查询**） | body `deviceIds`*[], `addressIds`*[] |
| GET | `/openApi/deviceAddress/page` | 点位分页（按设备 ID） | `page`*, `size`*, `deviceId`*, `name` |

出处：`openApiRouter.go:21-24`；`apiCatalog.md:284-291`。

### 4.13 非标准响应接口（DSH 响应解析器必须特判）

| 接口 | 返回 | 出处 |
|---|---|---|
| `GET /agentApi/device/importTemplate` | **`.xlsx` 文件流**（`Content-Disposition: attachment`），非 JSON 信封 | `agentApiSurface.go:97-100` Note |
| `GET /agentApi/deviceAddress/importDeviceAddressTemplate` | **`.xlsx` 文件流** | `agentApiSurface.go:126-129` Note |
| `GET /agentApi/openApiSecret/doc` | `data` 是 **HTML 字符串**（仍在标准信封内） | `agentApiSurface.go:253` Note |
| `GET /health` | **裸 JSON**，非信封 | `router/router.go:35` |

### 4.14 错误码总表

出处：`backend/enums/resultEnum.go:48-143`；整理见 `apiCatalog.md:575-603`。

| code | 含义 | 备注 |
|---|---|---|
| `0` | 成功 | `resultEnum.go:48` |
| `-1` | 系统错误 | `:49` |
| `10001` | 参数校验错误 | 必填缺失/超长/枚举越界都归这里 `:50` |
| `10002`/`10003`/`10004` | 未授权 / 禁止访问 / 资源不存在 | `:51-53` |
| `20001`–`20003` | 用户已存在 / 用户不存在 / 密码错误 | `:56-58` |
| `20004`–`20006` | 令牌过期 / 无效 / 为空 | **伴随 HTTP 401** `:59-61` |
| `30001`/`30002` | 开始采集失败 / 停止采集失败 | `:69-70` |
| `40001`–`40003` | 设备名已存在 / 设备不存在 / 设备连接测试失败 | `:64-66` |
| `40011`–`40013` | 点位名已存在 / 点位不存在 / 协议不支持该数据类型 | `:84-86` |
| `40021` | 局域网扫描功能未启用 | 开关 `deviceScan.enabled` `:93` |
| `40022` | 已有扫描任务在运行 | 同一时刻只允许一个 `:94` |
| `40023` | 扫描任务不存在或已过期 | **任务只在内存，重启即丢** `:95` |
| `40024` | 该协议不支持局域网扫描 | `:96` |
| `40025` | 没有可扫描的目标 | `:97` |
| `40026` | 扫描参数不合法 | `:98` |
| `40027` | **该协议不支持点位扫描** | 与 `40024` 是**两件事**，别混 `:109` |
| `50001`–`50005` | 协议名已存在 / 不存在 / 不可修改 / 插件不在 / 插件未声明默认表单 | `:73-81` |
| `60001`–`60003` | 推送通道名已存在 / 不存在 / 连通性测试失败 | `:112-114` |
| `60011`/`60012` | 通道表单名已存在 / 不存在 | `:117-118` |
| `70001`–`70004` | 密钥名已存在 / 不存在 / 密钥为空 / 密钥无效 | **后两个伴随 HTTP 401** `:121-124` |
| `80001` | 网关身份不存在 | `:127` |
| `90001`–`90006` | 插件未启用 / 未收到文件 / 文件数超限 / 同批重名 / 目录不可用 / 安装失败 | `:130-135` |
| `95001`–`95006` | 报警通知名已存在 / 不存在 / 配置不合法 / 测试发送失败 / 表单名已存在 / 表单不存在 | `:138-143` |

> **业务成败看 `code` 不看 HTTP 状态码** —— HTTP 200 也可能是失败（`apiCatalog.md:617`）。

---

## 5. 实时通道

### 5.1 结论：**没有 WebSocket、没有 SSE、没有 MQTT 订阅接口**

| 检查项 | 结果 |
|---|---|
| `backend/` grep `websocket\|gorilla/ws\|text/event-stream\|EventSource\|SSE\|Upgrade\(` | **零命中** |
| `frontend/src` grep `new WebSocket\|EventSource\|socket.io` | **零命中** |
| `go.mod` 是否有 websocket 依赖 | 无（`README.md:248` 技术栈也未列） |

### 5.2 唯一的实时通道：HTTP 轮询 `GET /agentApi/realtime/snapshot`

- **地址**：`http://<ip>:9082/agentApi/realtime/snapshot?page=1&size=50&deviceId=<id>`
- **无 topic 概念**（不是消息总线），是**请求-响应**分页查询
- **数据源**：内存最新值 `realtime.Store`，**不落库、无历史**
- **不需要认证**
- **消息格式**：见 §4.5（`RealtimeSnapshotVO`，含 `collectorRunning`/`collecting`/`online` 三个状态位 + `records[]`）

**DSH 侧实现建议**：

```
轮询间隔：1000–2000ms（点位默认 scanFrequency=1000ms，po/deviceAddress.go:21）
去重依据：records[].addressId + records[].updatedAt / value
离线判定：online=false，或走 GET /agentApi/alarm/listActive（更权威，来自报警状态机）
```

> 内存快照的生命周期与采集引擎**解耦**：`Store` 常驻，不随 `Engine.Refresh` 重建，
> 因此配置热加载不会清空已采集的值，前端页面不会闪回「未采集」（`realtime/store.go:37-39`）。

### 5.3 另一条真实时数据通路：MQTT 推送（**但网关是发布方，不是订阅方**）

网关**推**数据到下游 MQTT broker。如果 DSH 插件想拿流式数据，**更合理的做法是直接订阅那个 broker**，而不是轮询网关——但要注意：

- **MQTT 的 topic 与连接配置是每个推送通道各自的**，存在 `push_channel.config_json` 里
- **实测当前部署的通道**：`broker=tcp://172.22.5.23:1888`，`topic="wer"`（`configJson` 只有 `broker`/`port`/`topic` 三个键，即**匿名连接**）
- 取值方式：`GET /agentApi/pushChannel/pagePushChannel`（`configJson` 以嵌套对象返回）

**Topic 模板机制**（`backend/push/mqtt/mqttConfig.go:122-137`）：

```go
// buildTopics 根据配置主题模板与设备 ID 生成实际发布主题
// MQTT 发布禁止含通配符（+/#）的主题，因此把配置中的 + 替换为设备 ID
// （如 /IIoT/+/+/read → /IIoT/{deviceId}/{deviceId}/read），接收端订阅
// /IIoT/+/+/read 仍可收到全量。逗号分隔视作多个主题分别发布。
func buildTopics(template, deviceID string) []string {
	for _, t := range strings.Split(template, ",") {
		t = strings.TrimSpace(t)
		if t == "" { continue }
		topics = append(topics, strings.ReplaceAll(t, "+", deviceID))
	}
}
```

> ⚠️ 所以**配置里写的 topic 含 `+` 占位，实际发布的 topic 是替换后的具体值**。
> 实测配置 `topic="wer"` 不含 `+`，故实际发布主题就是 `wer`。
> 订阅方若照配置里的模板订阅（含 `+`）可以收到全量。

**MQTT 载荷格式**（真源 `backend/specs/push-payload.md`，对外契约）：

按**设备分组**推送，payload 为 JSON **数组**，数组内每条 = 一个点位：

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

字段语义（`push-payload.md:33-42`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `deviceId` / `deviceName` | string | 设备标识与名称 |
| `deviceAddressId` / `deviceAddressName` | string | 点位标识与地址名（如 `411115`） |
| `value` | string | **已解码**的采集值字符串（协议、字节序、字序已在网关侧完成） |
| `protocol` | string | 协议名称（`iot_protocol.name`），类型命名空间 |
| `dataType` | string | 协议**内部类型名**（`word`/`int16`），与 `protocol` 组合定位精确 PLC 类型 |
| `kind` | string | 数据类型类别（6 类之一），**`value` 解析的唯一事实来源** |
| `quality` | int | **192=正常，0=异常**（区间读取失败时置 0，value 为空串） |
| `collectedAt` | string | `2006-01-02 15:04:05.000`（毫秒精度），**本地时区、无时区标记** |

**`value` 解析：按 `kind` 分派，不要按 `dataType` 枚举**（`push-payload.md:44-59`）：

| kind | 含义 | 解析方式 |
|---|---|---|
| `bool` | 布尔 | 字符串 `"0"`/`"1"` |
| `int` | 有符号整数 | 十进制整数，可含负号 |
| `uint` | 无符号整数 | 十进制整数，无负号 |
| `float` | 浮点 | 十进制小数，**最短往返表示**（无指数） |
| `string` | 文本 | 原样字符串（已去除尾部 `\x00`） |
| `time` | 日期/时间 | 按协议为 ISO-8601 或日期/时刻字符串 |

浮点由 `strconv.FormatFloat(v,'f',-1,bits)` 生成：**固定记法（永不出现 `1.23e+08`）+ 最短往返**（`push-payload.md:73-78`）。
时间类型按协议不同（`push-payload.md:82-91`）：Modbus/FINS `date` 为 UTC RFC3339；S7 `date`/`tod`/`dt` 为无时区的可读格式。

**已知契约缺口**（`push-payload.md:94-99`）：`collectedAt` 是**本地时区、无时区标记**的字符串，跨时区部署多网关时下游排序可能错位；工程单位量纲（scale/offset）**不在采集范围内，payload 不含**。

### 5.4 断网缓存（数据不丢的保证）

每个推送通道有独立 outbox（有界内存队列 + SQLite 落盘缓存）：

- 断连且启用缓存 → 直接落盘（`push_outbox` 表，按 `channel_id` 隔离）
- 重连后按入队序补发，发布成功即删
- 出处：`backend/push/outbox.go`、`push/drain.go`、`push/writer.go`；`model/po/pushOutbox.go:6-19`
- MQTT 默认启用（`mqttConfig.SpoolDisabled` 缺省 false），上限 `defaultSpoolMaxBatches = 100000`（`mqttChannel.go:39-41`）
- 该状态可在 `GET /agentApi/push/status` 观察到：`queueDepth` / `spoolDepth` / `droppedCount`

### 5.5 UDP 局域网自动发现（**非** DSH 关心的通道，但要知道它存在）

网关周期性把自己的身份（id/名称/IP/端口/固件版本/描述）以 **UDP 广播**发到各网段，并单播应答探询，使管理平台**无需预配置、无需凭据**即可发现网关。

- 端口 **9083**（`default.yaml:65`），报文契约见 `backend/specs/discovery.md`
- **无认证**（`configFile.go:113-115`：「收发两侧都不校验来源，同一局域网内任何主机都能看见、也能伪造」）
- 因此**这条通道上永远不得加入敏感字段**（当前只有身份信息）
- ⚠️ 别与 `devicescan/` 混：`discovery/` 是网关广播"我在哪"，`devicescan/` 是网关主动问"网段里谁在"（`backend/CLAUDE.md`）

**对 DSH 的意义**：如果 DSH 插件需要**自动发现网关而不硬编码 IP**，可以监听 UDP 9083 收网关广播。但报文契约需照 `specs/discovery.md` 实现，收益通常不值得（本地部署直接 `127.0.0.1:9082` 即可）。

---

## 6. 核心数据模型

### 6.1 Device（设备）

`backend/model/po/device.go:13-35`；表名 `device`（`:38-40`）；ID 为 **UUID v7**（`:43-47` 的 `BeforeCreate`）

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | string | `id` | 主键，UUID v7 |
| `Name` | string | `name` | **唯一索引**；设备名 |
| `ProtocolID` | string | `protocolId` | 指向 `iot_protocol.id` |
| `ProtocolJSON` | string | `protocolJson` | **PLC 连接配置，JSON 字符串**（敏感） |
| `Description` | string | `description` | |
| `Status` | int | `status` | 默认 1（启用） |
| `CreatedAt` / `UpdatedAt` | string | `createdAt`/`updatedAt` | `YYYY-MM-DD HH:mm:ss` |
| `ScanIdentity` | string | `json:"-"` | **不参与 JSON 序列化**：扫描来源身份签名（`协议\|主机\|端口\|站号`），只由"扫描结果一键建设备"写入；手工新建与 Excel 导入一律留空（`:23-34` 详注） |

**API 层 VO 额外字段**（不在 PO 上，由 service 组装）：`protocolName`、`online`、`lastSuccessTime`、`iotGatewayId`。
实测样例见 §4.3。

### 6.2 DeviceAddress / 点位 / tag

`backend/model/po/deviceAddress.go:13-26`；表名 `device_address`（`:29-31`）；ID 也是 UUID v7（`:34-38`）

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | string | `id` | 主键，UUID v7 |
| `DeviceID` | string | `deviceId` | 所属设备 |
| `Name` | string | `name` | **唯一索引**；**设备地址写法**（如 `%MW100`、`i=2255`） |
| `Label` | string | `label` | 地址 name 的中文说明（如 温度、电流） |
| `CommonDataType` | string | `commonDataType` | **通用数据类型名**（配置界面展示，如 `Word`、`Boolean`） |
| `DataType` | string | `dataType` | **协议内部数据类型名**（驱动解码用，如 `word`、`bool`） |
| `RwPermission` | string | `rwPermission` | `R` / `W` / `RW` |
| `ScanFrequency` | int | `scanFrequency` | 默认 **1000**（毫秒） |
| `Description` | string | `description` | |
| `Status` | int | `status` | 默认 1 |
| `CreatedAt` / `UpdatedAt` | string | `createdAt`/`updatedAt` | |

> **`CommonDataType` vs `DataType` 的分工是核心概念**：调用方传 `commonDataType`（通用名），
> 服务端结合协议经 `resolveDataType`（`service/deviceAddressService.go:320`）映射出 `data_type`（内部名）。
> `dataType` 字段保留仅为**兼容旧请求**（`dto/deviceDto.go:56-57`）。
> 推送载荷里的 `dataType` 是**内部名**、`protocol` 是命名空间，二者组合可精确还原 PLC 类型（`push-payload.md:63-69`）。

### 6.3 IotProtocol（协议）

`backend/model/po/iotProtocol.go:18-26`；表名 `iot_protocol`（`:29-31`）

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | string | `id` | 主键，UUID v7 |
| `Name` | string | `name` | **唯一索引**；协议名（`ModBus.TCP`、`Siemens.S7`…）。**与插件驱动的唯一纽带，改名会让引用它的设备静默停采** |
| `Description` | string | `description` | |
| `FormJSON` | string | `formJson` | **动态表单 JSON Schema**（`{rule:[...]}`），前端 `form-create` 渲染 |
| `Sort` | int | `sort` | 默认 1 |
| `CreatedAt` / `UpdatedAt` | string | | |

> ⚠️ `iotProtocol.go:14-17` 注释说明：表里**还留着 `status` 列**（迁移 004 建的），
> 但 **Go 侧刻意不再映射它** —— 协议行全部由插件供给、供给时恒为启用，
> 「禁用协议」这个能力**从未生效过**。DSH 侧不要试图用 status 控制协议启停。

**协议来源**：全部由 `pluginhost/provision.go` 依插件 `describe` 结果**自动维护**（只插入、只补空，**绝不覆盖**运维改过的非空表单）。
**没有 create/delete 协议接口**（`protocolRouter.go:16-19`）。

### 6.4 PushChannel（推送通道）

`backend/model/po/pushChannel.go:13-21`；表名 `push_channel`（`:24-26`）

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | string | `id` | 主键，UUID v7 |
| `Name` | string | `name` | **唯一索引**；通道类型：`mqtt` / `tdengine-v3` / `influxdb-v3` |
| `Description` | string | `description` | |
| `ConfigJSON` | string | `configJson` | 通道配置（含口令等凭据） |
| `Status` | int | `status` | 默认 1 |
| `CreatedAt` / `UpdatedAt` | string | | |

`mqtt` 的 `configJson` 字段结构见 §4.7（真源 `push/mqtt/mqttConfig.go:20-40`）。

### 6.5 IotGateway（网关身份）

`backend/model/po/iotGateway.go:7-16`；表名 `iot_gateway`（`:19-21`）—— **单行表**

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | string | `id` | **网关唯一标识**（改了等于换机，故 `gateway/update` 不开放） |
| `Name` | string | `name` | **唯一索引**；网关名称 |
| `Port` | int | `port` | **只在首启写入**；广播发布的是**当前运行端口** `cfg.Server.Port`，不是这一列 |
| `FirmwareVersion` | string | `firmwareVersion` | 只读，以配置（`gateway.version` / `IOT_GATEWAY_VERSION`）为准，每次启动比对并同步 |
| `Description` | string | `description` | |
| `Status` | int | `status` | 默认 1 |
| `CreatedAt` / `UpdatedAt` | string | | |

实测：`{"id":"94ea29ca23a94cffb279474acab664ef","name":"23VJV25B27302557","port":9082,"firmwareVersion":"1.0.0",...}`
（`name` 是**主机名**，因 `default.yaml:37` 的 `gateway.name` 留空 → 回落到 `os.Hostname()`）

> `gateway/update` **刻意只开放 `name` 与 `description`**：`id` 是网关唯一标识、
> `firmware_version` 以配置为准（启动时自动同步，页面上改不了）、`status` 本轮不开放
> （`apiCatalog.md:549-551`）。

### 6.6 Alarm（报警）

`backend/model/po/alarm.go:15-29`；表名 `alarm`（`:32-34`）

见 §4.8 的语义说明与实测样例。

### 6.7 User（用户）

`backend/model/po/user.go:7-17`；表名 **`sys_user`**（`:20-22`，注意不是 `user`）

| 字段 | 类型 | JSON | 说明 |
|---|---|---|---|
| `ID` | string | `id` | |
| `Username` | string | **`name`** | ⚠️ JSON 名是 `name` 不是 `username`（`:9`） |
| `Email` | string | `email` | **唯一索引** |
| `Password` | string | **`-`** | bcrypt 哈希，**永不出参**（`:11`） |
| `Phone` / `Avatar` | string | `phone`/`avatar` | |
| `Status` | int | `status` | 默认 1 |
| `CreatedAt` / `UpdatedAt` | string | | |

### 6.8 OpenApiSecret（开放接口密钥）

`backend/model/po/openApiSecret.go:13-19`；表名 `open_api_secret`（`:22-24`）

`ID` / `Name`（唯一索引）/ **`Key`（密钥明文，JSON 名 `key`）** / `CreatedAt` / `UpdatedAt`

> ⚠️ `Key` 是**明文存储**，且 `GET /agentApi/openApiSecret/list` 会**原样回显明文**。

### 6.9 PushOutbox（断网缓存）

`backend/model/po/pushOutbox.go:8-15`；表名 `push_outbox`（`:18-20`）

`ID`(自增) / `ChannelID`(索引) / `DeviceID` / `CollectedAt` / `RecordsJSON` / `CreatedAt`

### 6.10 数据模型关系图

```
iot_gateway (单行，网关自身身份)
     │
     │  （无外键，表间不建约束）
     ▼
device ──protocol_id──▶ iot_protocol ──(由插件 provision，无 create/delete API)
  │                          ▲
  │ 1:N                      │ form_json 驱动前端动态表单
  ▼                          │
device_address          pluginhost ← plugins/iot-driver-*.exe（9 个进程外插件）
  (name = 地址写法)                    │
  │                                   │ Meta 声明协议/类型/表单/扫描能力
  │ 采集值（不落库，仅内存）           ▼
  └──▶ realtime.Store ──▶ GET /realtime/snapshot
  │
  └──▶ push.Engine ──▶ push_channel (mqtt / tdengine-v3 / influxdb-v3)
                            │                  │
                            │ 断连            │ 发布
                            ▼                  ▼
                       push_outbox        MQTT Broker / TDengine / InfluxDB
                       (SQLite 补发)

alarm ◀── 采集失败/通道断连（device.id 或 push_channel.id）
alarm_webhook ◀── 报警外发（dingtalk/wecom/feishu/custom）
open_api_secret ──▶ /openApi/* 的 X-Api-Key
sys_user ──▶ /user/login 的 JWT（DSH 不需要）
```

---

## 7. 建议的插件工具集

按**优先级**排序。P0 = 立刻做（无风险、高价值）；P1 = 核心写能力（需 human-in-the-loop）；P2 = 高级/诊断；P3 = 可选/低优先。

### 7.1 工具设计的三条硬约束（先读）

```
① 所有工具都打 /agentApi 前缀，零认证。不要实现 JWT / RSA。
② confirm=true 的工具（probe + write，共 45 条）默认必须走人工确认。
   分类不要硬编码 —— 启动时拉 GET /agentApi/__surface，按 access 字段动态构建策略。
   网关加接口时清单会自动更新，硬编码的会静默过期（apiCatalog.md:641-643）。
③ 删除接口是 GET。绝不按 HTTP 方法推断只读。
   高危清单：device/deleteDevice、deviceAddress/deleteDeviceAddress、
             pushChannel/deletePushChannel、alarmWebhook/deleteAlarmWebhook、
             openApiSecret/delete（agentApiSurface.go:106,136,191,225,256）
```

### 7.2 P0 —— 只读基础工具（access=read，可直接调，零风险）

| # | 工具名建议 | 端点 | 为什么 P0 |
|---|---|---|---|
| 1 | `gateway_surface` | `GET /agentApi/__surface` | **必做**。启动时调一次，构建工具策略（哪些直接调/哪些要确认）。这是网关给 DSH 的契约入口 |
| 2 | `gateway_info` | `GET /agentApi/gateway/get` | 网关身份 + 版本，用于连接自检与展示 |
| 3 | `gateway_health` | `GET /health` | 连通性探针（**注意裸 JSON，非信封**） |
| 4 | `collection_status` | `GET /agentApi/collection/status` | 采集引擎运行态（running / deviceCount / addressCount / errorCount）——"有没有在采" |
| 5 | `device_list` | `GET /agentApi/device/pageDevice` | 最常用的设备清单。分页 + 名称模糊 |
| 6 | `device_get` | `GET /agentApi/device/getDeviceById/:id` | 单设备详情（含 protocolJson 连接配置） |
| 7 | `device_overview` | `GET /agentApi/device/overview` | 一次拿到在线率/总数/离线数 —— 大盘类问题的最优单次调用 |
| 8 | `point_list` | `GET /agentApi/deviceAddress/pageDeviceAddress` | 点位清单（`deviceId` 必填）。**DSH 取点位名的唯一安全来源** |
| 9 | `point_get` | `GET /agentApi/deviceAddress/getDeviceAddressById/:id` | 单点位详情 |
| 10 | `realtime_snapshot` | `GET /agentApi/realtime/snapshot` | **实时值**（内存最新值）。是"现在温度多少"的唯一答案 |
| 11 | `protocol_list` | `GET /agentApi/protocol/pageProtocol` | 可用协议（实测 15 个）+ 各自的动态表单 |
| 12 | `protocol_get` | `GET /agentApi/protocol/getProtocolByName` | 取单协议（含 `formJson`）——**创建设备前的必调步骤** |
| 13 | `protocol_datatypes` | `GET /agentApi/protocol/getDataTypes` | 某协议可用数据类型清单 ——**创建点位前的必调步骤** |
| 14 | `push_status` | `GET /agentApi/push/status` | 推送通道运行态（connected / queueDepth / spoolDepth / publishCount / droppedCount）——"数据有没有出去" |
| 15 | `push_channel_list` | `GET /agentApi/pushChannel/pagePushChannel` | 推送通道清单 |
| 16 | `alarm_list_active` | `GET /agentApi/alarm/listActive` | **当前活跃离线报警 = 离线设备列表**（比 `device_list` 的 online 字段更权威） |
| 17 | `alarm_history` | `GET /agentApi/alarm/pageAlarm` | 报警历史，支持 4 维过滤 |
| 18 | `log_list` | `GET /agentApi/log/listFiles` | 日志文件列表（page/size 可省略） |
| 19 | `log_read` | `GET /agentApi/log/readFile` | 读日志末尾 N 行 ——**排障的主力工具** |
| 20 | `plugin_info` | `GET /agentApi/plugin/info` | 插件环境（目录/命名规则/开关）——"为什么某协议没有"的第一诊断点 |
| 21 | `gateway_discovery_capabilities` | `GET /agentApi/deviceScan/capabilities` | 扫描能力（协议 + 网段 + 开关）。也是取**本机网段列表**的唯一途径 |
| 22 | `point_scan_capability` | `GET /agentApi/deviceScan/pointCapability` | 某协议是否支持点扫 + 预算 + 表单（决定要不要给用户"自动发现点位"按钮） |

> **P0 的组合威力**：`device_overview` + `collection_status` + `push_status` + `alarm_list_active`
> 四次调用就能回答"网关整体健康吗"；`device_list` + `realtime_snapshot` 两次调用回答"这台设备现在什么值"。
> 这两组是最常见的用户意图，应优先打磨。

### 7.3 P1 —— 写能力（access=write，**必须 human-in-the-loop**）

| # | 工具名建议 | 端点 | 确认理由 |
|---|---|---|---|
| 23 | `point_create` | `POST /agentApi/deviceAddress/createDeviceAddress` | ⚠️ **`name` 写坏会让这个点位永远没有值**（服务端不校验写法，只报「地址无效」，需改配置恢复）。工具描述必须写死这条；且**只允许从 `point_list` 或点扫结果里取值**，不许模型自造 |
| 24 | `point_update` | `POST /agentApi/deviceAddress/updateDeviceAddress` | 同上（`label` 必填） |
| 25 | `point_import` | `POST /agentApi/deviceAddress/importDeviceAddress` | multipart `file`(.xlsx) + `deviceId` |
| 26 | `point_import_template` | `GET /agentApi/deviceAddress/importDeviceAddressTemplate` | **返回 xlsx 文件流**，响应解析器必须特判 |
| 27 | `device_create` | `POST /agentApi/device/createDevice` | ⚠️ **成功 `data: null`，不回 id** —— 建完必须按名字回查 `device_list` 才能拿 id（`apiCatalog.md:615-616`） |
| 28 | `device_update` | `POST /agentApi/device/updateDevice` | |
| 29 | `collection_start` / `collection_stop` | `POST /agentApi/collection/{start,stop}` | **影响产线数据流**，最需要确认的一类 |
| 30 | `collection_refresh` | `POST /agentApi/collection/refresh` | 热刷新采集配置（通常无需手动调 —— 改完设备/点位 10s 内自动生效，`agentApiSurface.go:156-159`） |
| 31 | `push_channel_create` | `POST /agentApi/pushChannel/createPushChannel` | |
| 32 | `push_channel_update` | `POST /agentApi/pushChannel/updatePushChannel` | |
| 33 | `push_refresh` | `POST /agentApi/push/refresh` | 热刷新推送配置（通常无需手动调） |
| 34 | `gateway_update` | `POST /agentApi/gateway/update` | 只开放 `name`/`description`；改完**无需重启** |
| 35 | `alarm_webhook_list_types` | `GET /agentApi/alarmWebhook/listTypes` | （实为 read）**创建通知前应先调此接口取合法 `type` 值** |
| 36 | `alarm_webhook_create` | `POST /agentApi/alarmWebhook/createAlarmWebhook` | |
| 37 | `alarm_webhook_update` | `POST /agentApi/alarmWebhook/updateAlarmWebhook` | ⚠️ **零值不覆盖陷阱**：`secret`/`atAll`/`atList`/`status` 是指针，传 `null`=不改、传 `""`/`false`=**清空**（`apiCatalog.md:530-534`）。工具 schema 必须能区分"未提供"与"清空" |
| 38 | `alarm_webhook_form_get` | `GET /agentApi/alarmWebhookForm/getAlarmWebhookFormByName` | |

### 7.4 P1.5 —— 扫描类（probe / write 混合，扫描是网关的高价值能力）

| # | 工具名建议 | 端点 | 说明 |
|---|---|---|---|
| 39 | `device_scan_start` | `POST /agentApi/deviceScan/start` | 🔒 **probe**。扫本机网段，重且慢；**同一时刻只允许一个任务**（否则 `40022`）。`subnets` 只能取 `capabilities` 里的值 |
| 40 | `device_scan_status` | `GET /agentApi/deviceScan/status` | 轮询用。`withDevices=false` 拿轻量快照（`devices`/`errors` 为 `null`）；据 `found` 变化再补完整请求（`deviceScanDto.go:34-45`） |
| 41 | `device_scan_cancel` | `POST /agentApi/deviceScan/cancel` | 🔒 probe |
| 42 | `device_scan_create_devices` | `POST /agentApi/deviceScan/createDevices` | 🔒 write。**两段式确认**：重复地址默认不建，只回 `code=addressExists`，确认后带 `confirmDuplicate:true` 重提 |
| 43 | `point_scan_discover` | `POST /agentApi/deviceScan/discoverPoints` | 🔒 **probe，最需要小心的一条**：**同步阻塞最长 7s**、**占住该设备采集会话锁**（会干扰正在跑的采集）、**不可取消**。**DSH 的 HTTP 超时必须 > 8s**（`apiCatalog.md:350-352,620-621`） |
| 44 | `point_scan_create_addresses` | `POST /agentApi/deviceScan/createAddresses` | 🔒 write。`name` 是地址写法，写坏拖垮整台设备 |

> **扫描任务只在内存**：不落库，重启即丢，过 `deviceScan.jobTTLMinutes`（默认 10 分钟）后惰性清理。
> 未导入的结果会丢失 —— 前端会提示任务已过期（`default.yaml:104-108`、`enums:40023`）。

### 7.5 P2 —— 危险 / 诊断工具（**默认不暴露，或必须强确认**）

| # | 工具名建议 | 端点 | 风险 |
|---|---|---|---|
| 45 | `device_test_connection` | `POST /agentApi/device/testDeviceConnection` | 🔒 probe。真实连一次 PLC，**不可达时等满超时** |
| 46 | `push_channel_test` | `POST /agentApi/pushChannel/testConnectivity` | 🔒 probe。真实连下游，不保存配置 |
| 47 | `alarm_webhook_test_send` | `POST /agentApi/alarmWebhook/testSend` | 🔒 probe。**真的往钉钉/企微/飞书发一条消息，群里会有人看见**。工具描述必须明确写这条 |
| 48 | `device_delete` | 🗑 `GET /agentApi/device/deleteDevice/:id` | 🔒 **write + GET 方法**。**静默停掉产线采集**，连带删全部点位，无二次确认、无回收站 |
| 49 | `device_batch_delete` | 🗑 `POST /agentApi/device/batchDeleteDevice` | 🔒 write。上限 200，连带删点位 |
| 50 | `point_delete` | 🗑 `GET /agentApi/deviceAddress/deleteDeviceAddress/:id` | 🔒 write + GET。**静默生效**（下一轮热加载就少一个点） |
| 51 | `point_batch_delete` | 🗑 `POST /agentApi/deviceAddress/batchDeleteDeviceAddress` | 🔒 write。`deviceId` 是**删除范围前置约束** |
| 52 | `push_channel_delete` | 🗑 `GET /agentApi/pushChannel/deletePushChannel/:id` | 🔒 write + GET。**数据不再推往下游** |
| 53 | `alarm_webhook_delete` | 🗑 `GET /agentApi/alarmWebhook/deleteAlarmWebhook/:id` | 🔒 write + GET |
| 54 | `plugin_import` | `POST /agentApi/plugin/importPlugin` | 🔒 write。⚠️⚠️ **上传并加载可执行文件 = 等价远程代码执行**。`agentApiSurface.go:263` 原文：「本分组里风险最高的一条」。multipart 字段名 `files`，总上限 512MiB |
| 55 | `protocol_update_form` | `POST /agentApi/protocol/updateProtocolForm{ByName,ById}` | 🔒 write。改动态表单 |
| 56 | `protocol_reset_form` | `POST /agentApi/protocol/resetProtocolForm/:id` | 🔒 write。**丢弃运维的自定义**表单 |

### 7.6 P3 —— 不建议封装成工具

| 端点 | 理由 |
|---|---|
| `POST /agentApi/user/login` | DSH 走 `/agentApi` 不需要它（`agentApiSurface.go:78-82` 原文：「dsh 走 /agentApi 不需要它（本分组无鉴权），保留是因为内部路由被整组镜像过来了」） |
| `PUT /agentApi/user/password` | 需 RSA 加密 3 个字段，且改的是令牌所属用户 —— DSH 无令牌，无法正确定位 |
| `POST /agentApi/createUser` / `PUT /agentApi/users/:id` | 账号管理，非网关业务能力 |
| `GET /agentApi/openApiSecret/*`（list/doc/create/updateName/delete） | 只有当 DSH 需要调 `/openApi` 才有意义；而 `/agentApi` 已覆盖只读需求。**`list` 会交出明文 AppKey** |
| `GET /agentApi/device/importTemplate` | 与 `deviceAddress` 的模板重复，且是文件流 |
| `GET /agentApi/profile` | 无令牌时语义不成立 |
| `POST /agentApi/protocol/updateProtocol` | 极少用；`name` 改名会返回 `50003` |

### 7.7 推荐的最小实现顺序

```
第 1 步（半天）：gateway_surface + gateway_health + gateway_info
                → 确认连通、拿到分类表、建立 access→confirm 的策略引擎
第 2 步（1 天）：device_list + device_overview + collection_status + push_status
                + point_list + realtime_snapshot
                → 覆盖"看网关状态"和"看设备实时值"两大高频意图
第 3 步（1 天）：alarm_list_active + alarm_history + log_list + log_read + plugin_info
                → 覆盖"为什么没数据/掉线了"的排障链路
第 4 步（1 天）：protocol_list + protocol_get + protocol_datatypes
                → 为写能力做参数准备（表单 + 类型清单）
第 5 步（2 天）：point_create/update + device_create/update + collection_start/stop
                + push_channel_create/update + alarm_webhook_*
                → 核心写能力，全部挂 confirm
第 6 步（1 天）：device_scan_* + point_scan_*
                → 扫描能力（注意 8s 超时与唯一任务约束）
第 7 步（谨慎）：删除类 + plugin_import
                → 建议默认关闭，或用双重确认 + 显式开关启用
```

---

## 8. 若要写一个外部客户端（Node / TypeScript）：最小可行调用序列

### 8.1 好消息：**不需要认证**

因为 `/agentApi/*` 完全免鉴权，最小可行序列**没有登录步骤**。这消除了 Node 侧最大的实现障碍（RSA-OAEP + PKCS#1 公钥转换，见 §3.3 的陷阱说明）。

### 8.2 最小可行序列（4 步）

```ts
// 1) 连通性 + 版本自检
GET http://127.0.0.1:9082/health
→ {"status":"ok"}                     // 裸 JSON，不是信封

// 2) 拿接口分类契约（决定哪些工具要人工确认）
GET http://127.0.0.1:9082/agentApi/__surface
→ {"code":"0","isSuccess":true,"data":[
     {"method":"GET","path":"/agentApi/device/pageDevice","access":"read","confirm":false},
     {"method":"GET","path":"/agentApi/device/deleteDevice/:id","access":"write","confirm":true,
      "note":"**用 GET 实现**，按方法判只读会误删。连带删掉该设备全部点位"},
     ... 共 83 条
   ]}

// 3) 看整体状态（一次调用拿到设备统计）
GET http://127.0.0.1:9082/agentApi/device/overview
→ {"code":"0",...,"data":{"total":8,"enabled":8,"disabled":0,"collected":3,
   "online":2,"offline":1,"unCollected":5,"onlineRate":0.67}}

// 4) 列设备 → 取实时值（两步）
GET http://127.0.0.1:9082/agentApi/device/pageDevice?page=1&size=20
→ data.records[0].id = "<deviceId>"

GET http://127.0.0.1:9082/agentApi/realtime/snapshot?page=1&size=50&deviceId=<deviceId>
→ data.records[] = [{addressId,name,label,value,dataType,commonDataType,quality,
                     collected,stale,updatedAt}, ...]
```

### 8.3 可运行的 TypeScript 骨架

```ts
// iot-gateway-client.ts —— 零依赖（Node 18+ 内置 fetch）
const BASE = process.env.IOT_GATEWAY_URL ?? 'http://127.0.0.1:9082';

/** 统一信封。注意 code 是**字符串**。 */
type Envelope<T> = {
  code: string;        // "0" 成功；"-1" 系统错误
  msg: string;
  isSuccess: boolean;
  data: T;
  serverTime: string;  // "YYYY-MM-DD HH:mm:ss"
};
type PageVo<T> = { page: number; size: number; total: number; records: T[] };

class GatewayError extends Error {
  constructor(readonly code: string, msg: string) { super(`[${code}] ${msg}`); }
}

/**
 * 唯一低层出口。
 * 关键约定：**业务成败看 body.code，不看 HTTP 状态码**（除 401 外恒为 200）。
 */
async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      // 只对 JSON 请求加 Content-Type；GET 不加也无妨
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
    // ⚠️ discoverPoints 是同步阻塞最长 7s，其调用点需单独传 10s+ 的 signal
    signal: init?.signal ?? AbortSignal.timeout(10_000),
  });

  // 401 是**唯二**的非 200 情况（另一个是 401 for X-Api-Key）
  if (res.status === 401) {
    const body = (await res.json().catch(() => null)) as Envelope<null> | null;
    throw new GatewayError(body?.code ?? '401', body?.msg ?? 'unauthorized');
  }

  const body = (await res.json()) as Envelope<T>;
  if (!body.isSuccess || body.code !== '0') {
    throw new GatewayError(body.code, body.msg);
  }
  return body.data;
}

/** 极少数不返回 JSON 信封的接口，必须特判：xlsx 文件流。 */
async function callRaw(path: string): Promise<ArrayBuffer> {
  const res = await fetch(`${BASE}${path}`);
  if (!res.ok) throw new GatewayError(String(res.status), res.statusText);
  return res.arrayBuffer();
}

// ---------- 必要的类型（字段来源见 §6 与 §4.5） ----------

export type DeviceVO = {
  id: string;
  iotGatewayId: string;
  name: string;
  protocolId: string;
  protocolName: string;
  /** ⚠️ 是 **JSON 字符串**，不是对象 —— 需二次 JSON.parse */
  protocolJson: string;
  description: string;
  status: number;
  online: boolean;          // = 无 active 断联报警，不是实时 ping
  lastSuccessTime: string;  // 内存态，空串 = 尚无成功记录（重启后短暂为空，正常）
  createdAt: string; updatedAt: string;
};

export type DeviceAddressVO = {
  id: string; deviceId: string;
  /** ⚠️ **设备地址写法**（如 %MW100 / i=2255）。写坏会让这个点位永远没有值 */
  name: string;
  label: string;
  commonDataType: string;   // 通用类型名（创建点位时应传这个）
  dataType: string;         // 协议内部类型名（驱动解码用）
  rwPermission: 'R' | 'W' | 'RW';
  scanFrequency: number;    // 毫秒，默认 1000
  description: string; status: number;
  createdAt: string; updatedAt: string;
};

export type RealtimeSnapshotVO = {
  deviceId: string; deviceName: string; protocolName: string;
  online: boolean;
  collecting: boolean;
  collectorRunning: boolean;
  lastSuccessTime: string; serverTime: string;
  page: number; size: number; total: number;
  records: {
    addressId: string; name: string; label: string;
    value: string;                 // 已解码字符串；异常时为空串
    dataType: string; commonDataType: string;
    rwPermission: string; scanFrequency: number;
    quality: number;               // 采集质量码：192=正常 128=不确定(值可用) 0=坏未归类 1=坏·地址 2=坏·通信 3=坏·解析（判坏写 <128）
    collected: boolean;            // false = 尚未采集到（占位返回，保证行数稳定）
    stale: boolean;
    updatedAt: string;
  }[];
};

export type CollectionStatusVO = {
  running: boolean;
  lastPollTime: string; lastSuccessTime: string;
  errorCount: number; deviceCount: number; addressCount: number;
};

export type DeviceOverviewVO = {
  total: number; enabled: number; disabled: number;
  collected: number; online: number; offline: number;
  unCollected: number; onlineRate: number;
};

export type ChannelStatusVO = {
  id: string; name: string; type: string;
  running: boolean; connected: boolean;
  broker: string; topic: string;
  queueDepth: number; spoolDepth: number;
  publishCount: number; droppedCount: number;
  lastPublishTime: string; lastSuccessTime: string;
  error: string;
};

export type AlarmVO = {
  id: string; targetId: string; targetName: string;
  targetType: 'device' | 'channel' | 'point';
  alarmType: 'offline' | 'quality' | 'recover'; alarmTypeName: string;
  deviceId: string; // 仅 point 行非空
  level: string; content: string;
  status: 'active' | 'cleared'; statusName: string;
  firstOccurTime: string; lastOccurTime: string; clearTime: string;
  createdAt: string; updatedAt: string;
};

// ---------- P0 只读工具 ----------

export const health          = () => callRaw('/health');   // 裸 JSON，非信封
export const surface         = () => call<SurfaceRoute[]>('/agentApi/__surface');
export const gatewayInfo     = () => call<Record<string, unknown>>('/agentApi/gateway/get');
export const collectionStatus= () => call<CollectionStatusVO>('/agentApi/collection/status');
export const deviceOverview  = () => call<DeviceOverviewVO>('/agentApi/device/overview');
export const pushStatus      = () => call<ChannelStatusVO[]>('/agentApi/push/status');
export const activeAlarms    = () => call<AlarmVO[]>('/agentApi/alarm/listActive');
export const pluginInfo      = () => call<Record<string, unknown>>('/agentApi/plugin/info');

export const listDevices = (page = 1, size = 20, name?: string) =>
  call<PageVo<DeviceVO>>(
    `/agentApi/device/pageDevice?page=${page}&size=${size}` +
    (name ? `&name=${encodeURIComponent(name)}` : ''));
// ⚠️ page/size 是**必填**（binding:"required"），缺省返回 10001

export const listPoints = (deviceId: string, page = 1, size = 50, name?: string) =>
  call<PageVo<DeviceAddressVO>>(
    `/agentApi/deviceAddress/pageDeviceAddress?page=${page}&size=${size}` +
    `&deviceId=${encodeURIComponent(deviceId)}` +
    (name ? `&name=${encodeURIComponent(name)}` : ''));

export const snapshot = (deviceId: string, page = 1, size = 50) =>
  call<RealtimeSnapshotVO>(
    `/agentApi/realtime/snapshot?page=${page}&size=${size}` +
    `&deviceId=${encodeURIComponent(deviceId)}`);

export const listLogs = (page = 1, size = 20) =>
  call<PageVo<{ fileName: string; size: number; modifyTime: string }>>(
    `/agentApi/log/listFiles?page=${page}&size=${size}`);

export const readLog = (fileName: string, tail = 200) =>
  call<unknown>(`/agentApi/log/readFile?fileName=${encodeURIComponent(fileName)}&tail=${tail}`);

// ---------- 写操作：调用前必须已获得用户确认 ----------

export const createAddress = (body: {
  deviceId: string;
  name: string;            // ⚠️ 设备地址写法，必须来自 listPoints / 点扫结果，禁止自造
  label: string;
  commonDataType?: string;
  rwPermission: 'R' | 'W' | 'RW';
  scanFrequency?: number;
  description?: string;
}) => call<null>('/agentApi/deviceAddress/createDeviceAddress', {
  method: 'POST', body: JSON.stringify(body),
});

export const startCollection = () =>
  call<null>('/agentApi/collection/start', { method: 'POST' });
export const stopCollection  = () =>
  call<null>('/agentApi/collection/stop',  { method: 'POST' });
export const refreshCollection = () =>
  call<null>('/agentApi/collection/refresh', { method: 'POST' });

/** 点扫：同步阻塞最长 7s，且占住该设备的采集会话锁 —— 超时必须放宽。 */
export const discoverPoints = (deviceId: string, options?: unknown) =>
  call<unknown>('/agentApi/deviceScan/discoverPoints', {
    method: 'POST',
    body: JSON.stringify({ deviceId, options }),
    signal: AbortSignal.timeout(15_000),   // 网关硬钳 7000ms，留足余量
  });

// ---------- 危险操作：建议默认不导出，或加显式开关 ----------

/** ⚠️ 删除设备：**用 GET 实现**，静默生效，连带删全部点位，无回收站。 */
export const deleteDevice = (id: string) =>
  call<null>(`/agentApi/device/deleteDevice/${encodeURIComponent(id)}`);
```

### 8.4 轮询实时值的最小循环

```ts
/** 内存最新值轮询。网关无 WS/SSE，只能这样做。 */
export async function* watchRealtime(deviceId: string, intervalMs = 1500) {
  let last = new Map<string, string>();   // addressId -> "value@updatedAt"

  for (;;) {
    const snap = await snapshot(deviceId, 1, 100);

    // 三个状态位组合可解释「为什么没数据」
    if (!snap.collectorRunning) { yield { kind: 'engine-stopped' as const }; }
    else if (!snap.collecting)  { yield { kind: 'not-collecting' as const }; }
    else if (!snap.online)      { yield { kind: 'device-offline' as const }; }

    for (const r of snap.records) {
      if (!r.collected) continue;              // 占位行，跳过
      const sig = `${r.value}@${r.updatedAt}`;
      if (last.get(r.addressId) !== sig) {
        last.set(r.addressId, sig);
        yield { kind: 'value' as const, point: r };
      }
    }
    await new Promise((r) => setTimeout(r, intervalMs));
  }
}
```

### 8.5 Node 侧必须注意的 8 个坑（按踩到概率排序）

| # | 坑 | 依据 |
|---|---|---|
| 1 | **判断成败看 `body.code === "0"`，不看 HTTP 状态码** —— HTTP 200 也可能是失败 | `response.go:26-44` |
| 2 | **删除接口是 GET**。任何"GET 可以预取/重试"的通用逻辑都会误删生产数据 | `agentApiSurface.go:106,136,191,225,256` |
| 3 | **`device.protocolJson` 是字符串**，`pushChannel.configJson` 是**对象** —— 两者行为不一致，解析要分别处理 | 实测（§4.3 vs §4.7） |
| 4 | **分页 `page`/`size` 几乎全是必填**，缺省报 `10001`。唯一例外 `log/listFiles` | `deviceDto.go:89-93`；`apiCatalog.md:188-190` |
| 5 | **创建接口不回 ID**（`data: null`）。要 ID 必须回查 `pageDevice` 按名字找 | `apiCatalog.md:615-616` |
| 6 | **`discoverPoints` 是同步阻塞的**，网关侧硬钳 7000ms。HTTP 超时必须 > 8s | `apiCatalog.md:350-352,620-621`；实测 `effectiveBudgetMs:7000` |
| 7 | **两个接口返回文件流不是 JSON 信封**（`device/importTemplate`、`deviceAddress/importDeviceAddressTemplate`），JSON 解析器会抛错 | `apiCatalog.md:207-210` |
| 8 | **点位 `name` 是设备地址写法，写坏拖垮整台设备**。只能从 `listPoints` 或点扫结果取，禁止自造 | `apiCatalog.md:479-481,618-619` |

### 8.6 什么情况下才需要实现 RSA 登录（并附做法）

**只有当**你发现某个需要的接口没有出现在 `/agentApi/__surface` 里（即未被镜像到 `/agentApi`），
才需要回落到根路径 + JWT。`agentApiRouter.go:34-36` 说明这是**显式白名单**：新增领域路由不会自动出现，必须显式追加。

若确实需要：

```ts
// 1) 硬编码这个常量（网关没有下发公钥的接口）
//    来源：backend/utils/rsaUtil.go:17 的 utils.PublicKey
//          与 frontend/src/utils/rsa.js 的 PUBLIC_KEY_B64 是同一串值
const PUBLIC_KEY_B64 = 'LS0tLS1CRUdJTiBSU0EgUFVCTElDIEtFWS0tLS0t...';

// 2) ⚠️ 陷阱：这串 PEM 的头是 "BEGIN RSA PUBLIC KEY"（PKCS#1），
//    而 Node 的 crypto.publicEncrypt 期望 SPKI（"BEGIN PUBLIC KEY"）。
//    最省事的做法是走 node-forge（与前端 rsa.js:174-183 同一路径）。
import forge from 'node-forge';

function rsaEncrypt(plaintext: string, b64Pem = PUBLIC_KEY_B64): string {
  const pem = Buffer.from(b64Pem, 'base64').toString('utf8');
  const publicKey = forge.pki.publicKeyFromPem(pem);
  const encrypted = publicKey.encrypt(forge.util.encodeUtf8(plaintext), 'RSA-OAEP', {
    md: forge.md.sha256.create(),          // OAEP 摘要 + MGF1 均 SHA-256
  });
  return forge.util.encode64(encrypted);
}

export async function login(username: string, password: string) {
  // ⚠️ 用户名与密码必须**两次独立加密**，不可拼接后一起加密
  //    （RSA-OAEP 2048 位单次明文上限 190 字节）
  const body = { username: rsaEncrypt(username), password: rsaEncrypt(password) };
  const res = await fetch(`${BASE}/user/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const env = (await res.json()) as Envelope<{
    accessToken: string; expiresIn: number; expiresAt: string;
  }>;
  if (!env.isSuccess) throw new GatewayError(env.code, env.msg);
  return env.data.accessToken;   // 之后带 Authorization: Bearer <token>
}
```

---

## 9. 不确定 / 未找到的部分

以下各项经过检索但**未能确认**，或存在**源码与文档不一致**，DSH 实现时需自行验证或规避。

### 9.1 文档与源码不一致（已确认的矛盾，**以源码/live 为准**）

| # | 矛盾点 | README/文档说法 | 源码/live 事实 | 结论 |
|---|---|---|---|---|
| 1 | **端口** | `README.md:100,120,147` 三处写 **9081** | `default.yaml:2` = **9082**；`docker-compose.yml:14` = 9082；live `gateway/get` 回报 9082；`apiCatalog.md:154` 记录 2026-09-16 已从 9081 改过来 | **9082 正确**。README 过期 |
| 2 | **默认账号数** | `README.md:154` 说初始化两个账号 `admin`、`hand` | `database/sqlite/seed.go:20-73` **只播种 `admin`**，全文件无 `hand` 字面量；live `/agentApi/users` **total=1** | **只有 `admin`**。README 有误 |
| 3 | **`plugin.enabled` 默认值** | `configFile.go:151` 结构体注释写「默认 false」 | `default.yaml:122` 实配 `enabled: true`；live `plugin/info` 回报 `"enabled":true`；`README.md:75` 说"出厂即为 true" | **true 正确**。代码注释陈旧 |
| 4 | **设备扫描的协议/网段来源接口** | `dto/deviceScanDto.go:12-13,21-23` 注释指 `GET /deviceScan/protocols` 与 `/subnets` | 这两个接口**不存在**；实际合并进 `capabilities` 的 `data.protocols[]` / `data.subnets[]`（`deviceScanRouter.go:24`） | 用 **`capabilities`**。注释是早期残留（`apiCatalog.md:625-637` 已记录） |
| 5 | **版本号是否等于当前二进制** | `default.yaml:41` 写 `version: "1.0.0"` | `configFile.go:102-105` + `default.yaml:38-40`：该值**以配置为准**，每次启动比对并同步进库（`seedGateway.go` 的 `syncGatewayVersion`） | live 的 `1.0.0` 等于 `default.yaml` 的值，未必等于当前二进制（二进制版本号无处可查） |
| 6 | **CHANGELOG 无已发布版本号** | `CHANGELOG.md:3` 声称遵循 SemVer | grep `^## \[[0-9]` **零命中**；唯一顶层节是 `## [Unreleased]`（`:7`） | **无法从 CHANGELOG 得到版本号** |

### 9.2 未找到 / 无法确认

| # | 项 | 检索情况 | 影响与应对 |
|---|---|---|---|
| 1 | **WebSocket / SSE 实时推送** | `backend/` grep `websocket\|text/event-stream\|EventSource\|Upgrade\(` **零命中**；`frontend/src` 同 | **确认不存在**。DSH 必须轮询（§5.2） |
| 2 | **历史采集数据查询接口** | 无任何端点；`realtime/store.go:6-11` 明说"只存最新值、不存历史" | 网关**不存历史**。历史趋势须从下游时序库（TDengine/InfluxDB）查，网关侧无接口 |
| 3 | **点位写值 / 反向下发接口** | 无任何 endpoint；`rwPermission` 有 `W`/`RW` 值但**未找到写入路径** | `rwPermission` 目前只影响驱动读取规划，**网关无"写点位"HTTP 接口**。若 DDS 需要写 PLC，当前**做不到** |
| 4 | **网关侧的"DSH 插件"现有实现** | `D:\code\汉得` 下 grep `agentApi\|iot-gateway-premium\|9082` 于 `*.ts` **零命中** | **尚未开始**。本报告可作为起点；网关侧的 `/agentApi` 已为 DSH 预留好 |
| 5 | **`subnets` 字段的精确结构** | `capabilities` 实测响应被截断（1200 字符），只看到 `protocols[]` 部分 | 需实调 `GET /agentApi/deviceScan/capabilities` 取完整响应确认 `subnets[]` 的字段名 |
| 6 | **`alarmWebhook` 的完整 DTO 字段绑定规则** | `dto/alarmWebhookDto.go` 4040 字节未逐行读；仅依 `apiCatalog.md:522-537` 与 `agentApiSurface.go:220-224` | 指针字段（`secret`/`atAll`/`atList`/`status`）的"null vs 空值"语义需在实现 `alarm_webhook_update` 工具时**实读该文件**核对 |
| 7 | **`tdengine-v3` / `influxdb-v3` 通道的 `configJson` 结构** | 仅读了 `mqttConfig.go`；两个 TSDB 通道的 config 结构未读 | 若 DSH 需要创建 TSDB 通道，需读 `push/tdengine/v3/*Config*.go` 与 `push/influxdb/v3/*Config*.go` |
| 8 | **`deviceScan/capabilities` 的 `subnets` 是否含子网掩码/CIDR 格式** | 未确认 | `deviceScanDto.go:21-24` 说 `subnets` 是 CIDR 格式且"只能从返回值里选"，但返回结构未实测完整 |
| 9 | **`/agentApi` 在未来版本是否会加回鉴权** | `authMiddleware.go:36` 注释：「**要收回这个口子，删掉本常量与下面那一行 HasPrefix 即可**」；`apiCatalog.md:117-121` 同 | **存在被收回的可能**。DSH 插件应把"无鉴权"当作**可配置假设**而非硬编码前提；建议同时支持可选的 `X-Api-Key` 或 JWT 回落 |
| 10 | **`gateway/update` 是否真能改 `port`** | `apiCatalog.md:549-551` 说刻意只开放 `name` 与 `description` | 需读 `dto/gatewayDto.go` + `service/gatewayService.go` 确认；当前依文档 |
| 11 | **各协议的 `protocolJson` 字段清单** | 只有 OPC.UA 的实测样例 | 新增设备前**必须**调 `getProtocolByName` 取该协议的 `formJson` 再拼参数，**不能从 OPc.UA 的样例推断其他协议** |
| 12 | **点位扫描的 `options` 各协议形状** | 只实测了 OPC.UA 的 `pointFormJson` | 同上：必须调 `pointCapability?protocolName=...` 取表单 |

### 9.3 需要运行时验证（本报告未实测的写操作）

以下写接口**本报告刻意未实测**（遵守"只做只读分析"的约束），实现 DSH 工具时需在**测试环境**验证：

- 所有 `write` 类端点（39 条）—— 尤其 `createDevice` 的 `protocolJson` 参数格式
- 所有 `probe` 类端点（6 条）—— 尤其 `discoverPoints` 的实际耗时与锁竞争表现
- `POST /agentApi/deviceScan/createDevices` 的 **两段式确认**流程（重复地址 → `addressExists` → `confirmDuplicate:true`）
- `PUT /agentApi/user/password` 的三字段 RSA 加密（若决定支持该工具）

### 9.4 已确认但值得反复提醒的"静默失败"模式

这些不会报错，只会让行为**悄悄不对**：

| 模式 | 症状 | 依据 |
|---|---|---|
| 插件目录缺失或为空 | 网关**能启动、能登录**，但**一台设备也采不到**（只有一行 WARN） | `README.md:123`、`default.yaml:113-116` |
| 协议行被删 | 插件下次加载会**重新插回** | `protocolRouter.go:16-19` |
| 插件文件消失 | 协议消失，但 `iot_protocol` 行**刻意保留**（前端下拉还在，设备却被跳过） | `backend/CLAUDE.md` |
| 一个非法点位地址 | **整台设备**采集全部失败（不是这一个点位失败） | `agentApiSurface.go:132-134`、`apiCatalog.md:479-481` |
| 删除点位 | **静默生效**，下一轮热加载就少一个点，无二次确认、无回收站 | `apiCatalog.md:436-437` |
| 扫描网段被截断 | `truncated=true` 但不报错；用户会从"没扫到"得出"网段里没有设备"的错误结论 | `default.yaml:96-102`、`apiCatalog.md:391` |
| 点位扫描预算耗尽 | `incomplete=true` 但**正常返回**部分结果，不报错 | `apiCatalog.md:397-398` |
| `device.online=false` 被当成 ping 失败 | 实际是"有 active 断联报警"，来自报警状态机（去抖 + 状态机），非即时探测 | `apiCatalog.md:369` |

---

## 10. 附：本报告的证据清单

### 10.1 主要源码出处

| 主题 | 文件 | 关键行 |
|---|---|---|
| DSH 专用无鉴权路由挂载 | `backend/router/agentApiRouter.go` | 12-13, 15-41（风险说明）, 62-103 |
| 免鉴权放行 | `backend/middleware/authMiddleware.go` | 19-22（publicPaths）, 24-40（agentApiPrefix 注释）, 45-57（isPublicPath） |
| 接口分类表 + 清单端点 | `backend/router/agentApiSurface.go` | 15-30（为何不能按方法判）, 32-46（AccessLevel）, 50-67（SurfaceEntry）, 69-70（surfacePath）, 76-307（83 条分类表）, 311-336（AgentApiSurface 排序）, 349-355（SurfaceHandler） |
| 全局路由 + 中间件顺序 | `backend/router/router.go` | 22-48（SetupRouter）, 50-202（各 With*Routes） |
| 统一响应信封 | `backend/response/response.go` | 9-15（Response）, 18-23（PageVo）, 26-34（Success）, 37-44（Fail） |
| JWT | `backend/utils/jwtutil.go` | 19-22（UserClaims）, 25-50（GenerateToken, HS256 @44）, 53-72（ValidateToken） |
| RSA / 公钥常量 | `backend/utils/rsaUtil.go` | 16（PrivateKey）, 17（PublicKey）, 36-44（SPKI 生成）, 88（PKCS1 解析）, 96-112（RSAEncrypt/OAEP-SHA256）, 115-135（RSADecrypt） |
| 登录服务 | `backend/service/userService.go` | 116-157（Login，117/121 解密） |
| X-Api-Key 中间件 | `backend/middleware/openApiAuthMiddleware.go` | 17（header 常量）, 22-47 |
| 配置加载（4 段合并） | `backend/configFile/configFile.go` | 17-28（环境变量常量）, 37-51（Config）, 242-299（InitConfig）, 301-322（resolveJwtSecret） |
| 默认配置 | `backend/default.yaml` | 2（port 9082）, 8（jwt.secret）, 17（默认口令）, 41（version）, 65（discovery port 9083）, 122（plugin.enabled true） |
| 账号播种 | `backend/database/sqlite/seed.go` | 21（"admin"）, 35-74（seedDefaultAdmin）, 84-94（口令优先级） |
| 启动流程与监听 | `backend/main.go` | 29-36（main）, 57-180（runGateway）, 99-108（DriverManager 必须先于 Collector）, 154-159（http.Server） |
| 实时值内存存储 | `backend/realtime/store.go` | 6-11（只存最新值）, 21-30（Point）, 40-43（Store）, 53-78（PushRecords）, 80-107（Lookup） |
| 实时值路由 | `backend/router/realtimeRouter.go` | 13-18 |
| 各领域路由 | `backend/router/{user,device,deviceAddress,collection,protocol,pushChannel,pushChannelForm,push,alarm,alarmWebhook,logfile,openApiSecret,openApi,gateway,plugin,deviceScan}Router.go` | 见 §4 各表 |
| MQTT 配置与 topic | `backend/push/mqtt/mqttConfig.go` | 20-40（结构）, 87-107（校验）, 111-120（brokerURL）, 122-137（buildTopics） |
| MQTT 通道常量 | `backend/push/mqtt/mqttChannel.go` | 28-50（超时/worker 上限/clientId 长度）, 43-49（clientID 后缀） |
| 推送载荷契约 | `backend/specs/push-payload.md` | 14-28（载荷样例）, 33-42（字段语义）, 44-59（kind 分派）, 63-69（protocol+dataType）, 73-78（浮点）, 82-91（时间）, 94-99（契约缺口） |
| 数据模型 | `backend/model/po/{device,deviceAddress,iotProtocol,pushChannel,iotGateway,alarm,user,openApiSecret,pushOutbox}.go` | 见 §6 各表 |
| 请求 DTO | `backend/model/dto/{user,device,realtime,deviceScan,devicePointScan,alarm,openApiSecret,gateway,logfile}.go` | 见 §4 各表 |
| 错误码 | `backend/enums/resultEnum.go` | 48-143 |
| 插件安装控制器 | `backend/controller/pluginController.go` | 22-37（字段名/上限常量）, 61-79（ImportPlugin）, 86-117（readPluginUploads） |
| 设备扫描路由 | `backend/router/deviceScanRouter.go` | 12-20（方向相反的功能说明）, 21-35 |
| **网关自带的 dsh 文档** | `backend/docs/apiCatalog.md` | 全 653 行（v1.2），尤其 13-20（版本记录）, 23-126（§0 通道与风险）, 147-213（通用约定）, 217-268（认证）, 272-425（只读清单）, 428-571（编辑清单）, 575-603（错误码）, 607-653（注意事项与推荐路径） |
| 安全说明 | `README.md`（SECURITY 引用）, `SECURITY.md` | — |

### 10.2 实测证据（对 live 实例 `127.0.0.1:9082` 的只读调用）

| 调用 | 结果摘要 |
|---|---|
| `GET /health` | `{"status":"ok"}` |
| `GET /agentApi/__surface` | 83 条路由（read 38 / probe 6 / write 39） |
| `GET /agentApi/gateway/get` | id `94ea29ca…`, name `23VJV25B27302557`, port **9082**, fw `1.0.0` |
| `GET /agentApi/collection/status` | running=true, deviceCount=8, addressCount=8, errorCount=1 |
| `GET /agentApi/device/overview` | total=8, online=2, offline=1, collected=3, unCollected=5, onlineRate=0.67 |
| `GET /agentApi/device/pageDevice` | 8 台设备；首条为 OPC.UA @ 10.62.100.12:4840，`protocolJson` 确认是**字符串** |
| `GET /agentApi/deviceAddress/pageDeviceAddress?deviceId=…` | 点位 `name` 为 OPC UA NodeID 写法（`i=2255`），`commonDataType`/`dataType` 双字段确认 |
| `GET /agentApi/realtime/snapshot?deviceId=…` | 确认是 **RealtimeSnapshotVO**（非 PageVo），含 `collectorRunning/collecting/online` + `records[].quality/collected/stale` |
| `GET /agentApi/protocol/pageProtocol` | **total=15**，与 15 个协议名吻合；`formJson` 是 `{rule:[...]}` 动态表单 |
| `GET /agentApi/push/status` | 1 个 mqtt 通道，connected=true，broker `tcp://172.22.5.23:1888`，topic `wer`，publishCount=1779 |
| `GET /agentApi/pushChannel/pagePushChannel` | `configJson` **以嵌套对象**返回（与 protocolJson 不一致） |
| `GET /agentApi/alarm/listActive` | 2 条活跃 offline 报警（10.62.100.24、10.62.100.16） |
| `GET /agentApi/plugin/info` | `os=windows, arch=amd64, dir=…\backend\plugins, glob=iot-driver-*, fileExt=.exe, enabled=true` |
| `GET /agentApi/deviceScan/capabilities` | `enabled=true`，`protocols[]` 含 `Mitsubishi.MC.TCP`(5007)、`ModBus.TCP`(502) 等，各带 `scanFormJson` |
| `GET /agentApi/deviceScan/pointCapability?protocolName=OPC.UA` | `supported=true`, `maxBudgetMs=20000`, **`effectiveBudgetMs=7000`**, `maxPoints=2000` |
| `GET /agentApi/users` | **total=1**，唯一记录 `admin`（证实无 `hand` 账号） |
| `GET /agentApi/log/listFiles` | 19 个日志文件 |
| `GET /openApi/device/page`（无 key） | **HTTP 401** `{"code":"70003","msg":"开放接口密钥为空"}` |
| `GET /device/pageDevice`（无 JWT） | **HTTP 401** `{"code":"20006","msg":"令牌为空"}` |

### 10.3 检索过的"不存在"项（负面证据）

| 检索 | 范围 | 结果 |
|---|---|---|
| `websocket\|gorilla/ws\|text/event-stream\|EventSource\|SSE\|Upgrade\(` | `backend/**/*.go` | **零命中** → 无 WS/SSE |
| `new WebSocket\|EventSource\|socket.io` | `frontend/src/**` | **零命中** |
| `^## \[[0-9]` | `CHANGELOG.md` | **零命中** → 无已发布版本号小节 |
| `hand`（作为账号名） | `backend/database/sqlite/seed.go` | **不存在** → 只播种 admin |
| `/deviceScan/protocols`、`/deviceScan/subnets` | `backend/router/**` | **未注册** → 注释中的失效引用 |
| `agentApi\|iot-gateway-premium\|9082` | `D:\code\汉得\**/*.ts` | **零命中** → DSH 侧尚无实现 |
| `rsa\|oaep\|encrypt\|publicKey` | `frontend/src/**` | 命中 `utils/rsa.js`（`node-forge` + WebCrypto 双路径） |

---

## 11. 结语：给 DSH 插件开发者的一句话

网关作者已经**为 DSH 铺好了路**——`/agentApi/*` 的 82 个领域接口、零鉴权、外加一个会自动与运行中网关保持一致的 `/agentApi/__surface` 分类清单（read/probe/write）。

所以对接工作的**重心不在认证与协议**，而在三件事：

1. **信任 `__surface` 而不是硬编码**——启动时拉一次，用 `access` 字段驱动确认策略，网关加接口时自动跟上。
2. **尊重 `confirm=true` 的 45 条接口**——尤其 `plugin/importPlugin`（等价 RCE）、`alarmWebhook/testSend`（真发消息）、以及那 5 个**用 GET 实现的删除接口**。
3. **把两条"静默失败"写进工具描述**——点位 `name` 写坏会拖垮**整台设备**；删除**静默生效、无回收站**。无鉴权通道下没有凭据这一层兜底，误调的门槛只剩下工具描述本身。

---

# 附录 A：实测证据补充（对 live 实例 `127.0.0.1:9082`）

> **标注约定**：每条结论都标了来源。
> - 🟢 **实测** —— 对正在运行的网关真实发起 HTTP 请求得到的**原始响应**（全部为只读调用；唯一的写风险动作 `discoverPoints` 也已确认是只读探测，不落库、不改配置）。
> - 🔵 **源码推断** —— 仅读源码得出，未在运行时验证。
>
> 本附录一次性补齐 8 项。**全部 8 项均已实测**，源码推断仅作为解释补充。

---

## A.1 `GET /agentApi/__surface` 实际返回样例

🟢 **实测**。前缀 `/agentApi`，完整 URL：`http://127.0.0.1:9082/agentApi/__surface`，无任何请求头。
实测 **HTTP 200**，`data` 是长度 **83** 的数组，外层是标准信封。

### A.1.1 完整信封骨架（实测）

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": [ /* 83 条 SurfaceRoute，见下 */ ],
  "serverTime": "2026-09-30 14:25:52"
}
```

### A.1.2 样例 1 —— 带**路径占位符 `:id`**（🔴 用 GET 实现删除）

🟢 **实测**（原文照抄，`note` 里的 `**` 是网关自己输出的 Markdown 粗体标记）：

```json
{
  "method": "GET",
  "path": "/agentApi/device/deleteDevice/:id",
  "access": "write",
  "confirm": true,
  "note": "**用 GET 实现**，按方法判只读会误删。连带删掉该设备全部点位"
}
```

> 与 `router/agentApiSurface.go:106-109` 的源码定义**逐字一致**，确认清单是源码表的直出。

### A.1.3 样例 2 —— 带**敏感数据 note**（read 但要注意）

🟢 **实测**：

```json
{
  "method": "GET",
  "path": "/agentApi/alarmWebhook/getAlarmWebhookById/:id",
  "access": "read",
  "confirm": false,
  "note": "secret 永不回显（恒为空串），只有 hasSecret；但 url 含凭据"
}
```

### A.1.4 样例 3 —— `read` 但**响应含明文密钥**

🟢 **实测**（本条是我认为 DSH 侧最需要单独处理的 `read` 条目）：

```json
{
  "method": "GET",
  "path": "/agentApi/openApiSecret/list",
  "access": "read",
  "confirm": false,
  "note": "响应含**明文 AppKey** —— 拿到它就能调 /openApi 的只读接口"
}
```

### A.1.5 样例 4 —— 清单**自身**也登记在内（不变量无例外）

🟢 **实测**：

```json
{
  "method": "GET",
  "path": "/agentApi/__surface",
  "access": "read",
  "confirm": false,
  "note": "本清单。登记在这里是为了让「每个 /agentApi 路由都有分类」这条不变量无例外地成立"
}
```

### A.1.6 样例 5 —— `probe` 类，note 明确写了同步阻塞与超时要求

🟢 **实测**（`note` 较长，此处保留首段）：

```json
{
  "method": "POST",
  "path": "/agentApi/deviceScan/discoverPoints",
  "access": "probe",
  "confirm": true,
  "note": "**同步阻塞**，最长约 7s（网关侧硬钳）。且复用该设备的采集会话，期间会占住会话锁 —— 6 条 probe 里唯一会干扰正在跑的采集的一条。dsh 侧 HTTP 超时必须 > 8s"
}
```

### A.1.7 字段语义（🔵 源码 + 🟢 实测一致）

| 字段 | 类型 | 说明 | 出处 |
|---|---|---|---|
| `method` | string | HTTP 方法。**不可用于判断只读/可写** | `agentApiSurface.go:60` |
| `path` | string | **含 `/agentApi` 前缀**、含 `:id` 占位符 | `agentApiSurface.go:323`（`AgentApiPrefix + rel`） |
| `access` | string | `read` / `probe` / `write` | `agentApiSurface.go:35-46` |
| `confirm` | bool | **恒等于 `access != "read"`**，由代码统一推导 | `agentApiSurface.go:325` |
| `note` | string | 可选（`omitempty`）；只写"光看字段名看不出来"的事 | `agentApiSurface.go:66`、注释 `:53-55` |

🟢 **实测统计**：read **38** / probe **6** / write **39** = 83。与 `apiCatalog.md:68-70` 记载的 38/6/39 完全吻合。

> 🔵 **源码补充**：清单**按 path 再按 method 排序**（`agentApiSurface.go:329-334`），
> 所以 DSH 侧不应依赖"分组聚集"的顺序假设。
> 另：`note` 中带 `**` 粗体标记，是**给模型读的 Markdown**，不是给机器解析的结构化数据（`agentApiSurface.go:53-55` 注释原文：「写给 dsh 背后的模型看，不是给机器解析的」）。

---

## A.2 路径参数占位符清单

🟢 **实测**（从 live `__surface` 的 `path` 字段正则提取 `:([A-Za-z]+)` 并去重）。

### A.2.1 结论

| 项 | 值 |
|---|---|
| 带路径占位符的路由数 | **13** 条（占 83 条的 15.7%） |
| 占位符名字种类 | **只有 1 种：`id`** |
| 各名字出现次数 | `id` × 13 |
| **没有**的占位符 | `jobId`、`deviceId`、`addressId`、`fileName` 等**都不是**路径参数 |

> ⚠️ **重要**：DSH 侧做路径模板替换时，**只需处理 `:id` 一个占位符**。
> 其余看起来像 ID 的参数（`jobId` / `deviceId` / `protocolName` / `fileName` / `name` / `tail` / `withDevices`）
> **全部走 query string 或 JSON body**，不在路径里。

### A.2.2 全部 13 条（🟢 实测，含 access）

| # | 方法 | 路径 | 占位符 | access | confirm |
|---|---|---|---|---|---|
| 1 | GET | `/agentApi/device/getDeviceById/:id` | `:id` | read | false |
| 2 | GET | `/agentApi/device/deleteDevice/:id` | `:id` | **write** | true |
| 3 | GET | `/agentApi/deviceAddress/getDeviceAddressById/:id` | `:id` | read | false |
| 4 | GET | `/agentApi/deviceAddress/deleteDeviceAddress/:id` | `:id` | **write** | true |
| 5 | GET | `/agentApi/pushChannel/getPushChannelById/:id` | `:id` | read | false |
| 6 | GET | `/agentApi/pushChannel/deletePushChannel/:id` | `:id` | **write** | true |
| 7 | GET | `/agentApi/alarmWebhook/getAlarmWebhookById/:id` | `:id` | read | false |
| 8 | GET | `/agentApi/alarmWebhook/deleteAlarmWebhook/:id` | `:id` | **write** | true |
| 9 | GET | `/agentApi/protocol/getProtocolById/:id` | `:id` | read | false |
| 10 | GET | `/agentApi/openApiSecret/delete/:id` | `:id` | **write** | true |
| 11 | POST | `/agentApi/protocol/resetProtocolForm/:id` | `:id` | **write** | true |
| 12 | GET | `/agentApi/users/:id` | `:id` | read | false |
| 13 | PUT | `/agentApi/users/:id` | `:id` | **write** | true |

### A.2.3 三条从这张表读出来的规律（🟢 实测数据 + 🔵 源码解释）

1. **13 条里 9 条是 GET，且 5 条是删除**。这与 §0.2 的"删除用 GET"是同一个陷阱的另一面：
   带占位符的路由**恰好是 DELETE 语义最集中的地方**。DSH 侧实现"路径参数替换"这个通用工具时，
   **不能顺带把它套上 GET→只读的默认策略**。
   🔵 源码：`router/deviceRouter.go:18`、`deviceAddressRouter.go:18`、`pushChannelRouter.go:19`、
   `alarmWebhookRouter.go:19`、`openApiSecretRouter.go:21`（5 处 `GET .../delete.../:id` 注册）。

2. **7 条是 `getXxxById` ↔ `deleteXxx/:id` 成对出现**（device、deviceAddress、pushChannel、alarmWebhook）。
   这个对称性很适合做工具命名：`device_get` / `device_delete`。
   `protocol` 与 `users` 不对称（`protocol` 有 `getProtocolById` 但删除换成 `resetProtocolForm`；
   `users` 是 `GET` 查 + `PUT` 改）。

3. **`protocol/resetProtocolForm/:id` 是唯一带占位符的 POST/PUT 之外的写操作**，
   且它的占位符是**协议 ID**（不是表单 ID）——🔵 `router/protocolRouter.go:26` 注册为 `resetProtocolForm/:id`，
   语义是"把该协议的表单恢复为插件当前声明的那一份"（`agentApiSurface.go:178-181`）。

### A.2.4 推荐的路由匹配实现（TypeScript）

```ts
/** 把 /agentApi/device/getDeviceById/:id 编译成正则 + 参数名列表。只可能出现 :id。 */
export function compileRoute(template: string) {
  const names: string[] = [];
  const source = template.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')      // 转义字面量
    .replace(/:([A-Za-z_][A-Za-z0-9_]*)/g, (_, n) => {                // 再还原 :param
      names.push(n);
      return '([^/]+)';
    });
  return { re: new RegExp(`^${source}$`), names };
}

/** 注意：:id 的值必须 encodeURIComponent，因为网关用 UUID（安全），但不要假设。 */
export function buildPath(template: string, params: Record<string, string>) {
  return template.replace(/:([A-Za-z_][A-Za-z0-9_]*)/g,
    (_, n) => encodeURIComponent(params[n] ?? ''));
}
```

---

## A.3 必填 query / body 参数（逐接口 + 实测确认）

🟢 **实测**（对分页接口与路径/query 参数做了真实调用验证）；表格主干来自 🔵 `apiCatalog.md` §3.0/§4 + 🔵 各 DTO 源码。

### A.3.1 ⚠️ 首先纠正一个通用约定的错误

🔵 `apiCatalog.md:173,583` 说参数校验失败的 `code` 是 **`10001`**。
🟢 **实测：不是。真实值是字符串 `"PARAM_ERROR"`。**

```json
{
  "code": "PARAM_ERROR",
  "msg": "参数校验失败: Page required",
  "isSuccess": false,
  "data": null,
  "serverTime": "2026-09-30 14:25:52"
}
```

🔵 **源码证据**：`backend/appError/globalError.go:39-46` 把 validator 错误**硬编码**成 `"PARAM_ERROR"`：

```go
// 参数校验错误
var validErrs validator.ValidationErrors
if errors.As(err, &validErrs) {
    field := validErrs[0].Field()
    tag := validErrs[0].Tag()
    msg := "参数校验失败: " + field + " " + tag
    return response.Fail("PARAM_ERROR", msg)   // ← 不是 enums.ParamValidEnum 的 "10001"
}
```

🔵 `enums.ParamValidEnum`（`enums/resultEnum.go:50`，`"10001"`）**仍然存在，但只在服务层显式构造 `AppError` 时使用**
（如 `excelutil/excelutil.go:108`、`service/deviceAddressService.go:67`、`service/protocolService.go:140`、
`controller/uploadHelper.go:21`）—— **通用的 binding 校验路径不走它**。

🟢 **交叉验证**：网关自己渲染的 `/agentApi/openApiSecret/doc` 文档里，
错误码表**已经写的是 `PARAM_ERROR`**：

```html
<td><code>PARAM_ERROR</code></td><td>200</td>
<td>参数校验失败</td>
<td>按 msg 提示修正请求参数（msg 形如 <code>参数校验失败: size max</code>）</td>
```

> **DSH 结论**：判定"参数错误"必须匹配 `code === "PARAM_ERROR"`（字符串），
> **不能**匹配 `"10001"`。也不要只判断数值范围。`msg` 后缀（`required`/`max`/`min`）含字段名与原因，可直接展示给用户。
> ⚠️ 但 `10001` **确实仍可能出现**（服务层显式抛出的那几处，如 Excel 导入、缺 `deviceId`、协议名为空）。
> 所以稳妥做法：**两者都当参数错误处理**。

### A.3.2 分页参数是否真的必填？🟢 **实测：绝大多数是，唯一例外 `log/listFiles`**

| 实测调用 | HTTP | `code` | `msg` |
|---|---|---|---|
| `GET /agentApi/device/pageDevice`（完全无参） | **200** | `PARAM_ERROR` | `参数校验失败: Page required` |
| `GET /agentApi/device/pageDevice?page=1`（仅 page） | **200** | `PARAM_ERROR` | `参数校验失败: Size required` |
| `GET /agentApi/device/pageDevice?size=5`（仅 size） | **200** | `PARAM_ERROR` | `参数校验失败: Page required` |
| `GET /agentApi/device/pageDevice?page=1&size=200`（size 超上限） | **200** | `PARAM_ERROR` | `参数校验失败: Size max` |
| `GET /agentApi/deviceAddress/pageDeviceAddress?page=1&size=5`（缺 deviceId） | **200** | `PARAM_ERROR` | `参数校验失败: DeviceID required` |
| `GET /agentApi/alarm/pageAlarm`（完全无参） | **200** | `PARAM_ERROR` | `参数校验失败: Page required` |
| **`GET /agentApi/log/listFiles`（完全无参）** | **200** | **`"0"` ✅** | `success`（服务端兜底 `page=1,size=20`，实测 `total:19`） |

> 🟢 **结论**：
> - `page` 与 `size` **都是必填**（缺一即报错），且 `size` 上限 **100**。
> - ⚠️ **注意错误顺序**：两个都缺时先报 `Page required`；只给 `page` 时报 `Size required`。
>   这说明校验顺序是 **Page → Size**。
> - ✅ **唯一例外是 `GET /agentApi/log/listFiles`**，两个都可省略（🔵 `agentApiSurface.go:242` 的 note 也写明「page/size 可省略（服务端兜底 1/20）」）。
> - 🔵 `dto/userDto.go:33-36` 的 `PageDTO` 对 `users` 是 `binding:"min=1"`（**没有 `required`**），
>   但 `dto/deviceDto.go:90-91` 的 `PageDeviceDTO` 是 `binding:"required,min=1"` —— **不一致**，
>   所以 DSH 侧**一律显式传 `page` 与 `size`** 最省心。

### A.3.3 必填 **query** 参数（非分页）

🟢 **实测** + 🔵 源码。全部为必填，缺失即 `PARAM_ERROR`：

| 方法 | 路径 | 必填 query | 🟢 实测缺失时的报错 |
|---|---|---|---|
| GET | `/agentApi/deviceAddress/pageDeviceAddress` | `deviceId` | `参数校验失败: DeviceID required` |
| GET | `/agentApi/realtime/snapshot` | `page`, `size`, `deviceId` | `参数校验失败: DeviceID required`（page/size 也必填） |
| GET | `/agentApi/deviceScan/status` | `jobId` | `参数校验失败: JobID required` |
| GET | `/agentApi/deviceScan/pointCapability` | `protocolName` | 🔵 `dto/devicePointScanDto.go:20` |
| GET | `/agentApi/log/readFile` | `fileName` | `参数校验失败: FileName required` |
| GET | `/agentApi/pushChannelForm/getPushChannelFormByName` | `name` | 🔵 `controller/pushChannelFormController.go` |
| GET | `/agentApi/alarmWebhookForm/getAlarmWebhookFormByName` | `name` | 🔵 同上形态 |
| GET | `/agentApi/log/listFiles` | **无**（page/size 可省） | 🟢 实测成功 |

### A.3.4 必填 **body** 参数（关键写操作）

🟢 **实测**（用 `{}` 空 body 触发校验）+ 🔵 源码 DTO。

🟢 实测样例：

```
POST /agentApi/device/testDeviceConnection   body: {}
→ HTTP 200  {"code":"PARAM_ERROR","msg":"参数校验失败: ProtocolName required",...}
```

| 方法 | 路径 | 必填 body 字段（名字取自 Go `binding` tag） | 出处 |
|---|---|---|---|
| POST | `/agentApi/device/createDevice` | `Name`, `ProtocolID`, `ProtocolJSON` | `dto/deviceDto.go:12-14` |
| POST | `/agentApi/device/updateDevice` | **全部可选**（`ID` 等无 `required`） | `dto/deviceDto.go:20-25` |
| POST | `/agentApi/device/batchDeleteDevice` | `IDs`（1–200） | `dto/deviceDto.go:44` |
| POST | `/agentApi/device/testDeviceConnection` | `ProtocolName`, `ProtocolJSON` | `dto/deviceDto.go:31-32` |
| POST | `/agentApi/deviceAddress/createDeviceAddress` | `DeviceID`, `Name`, `Label`, `RwPermission`(oneof R/W/RW) | `dto/deviceDto.go:53-58` |
| POST | `/agentApi/deviceAddress/updateDeviceAddress` | `ID`, `Label` | `dto/deviceDto.go:65,67` |
| POST | `/agentApi/deviceAddress/batchDeleteDeviceAddress` | `DeviceID`, `IDs`(1–200) | `dto/deviceDto.go:82-83` |
| POST | `/agentApi/deviceScan/start` | `Protocol` | `dto/deviceScanDto.go:13` |
| POST | `/agentApi/deviceScan/cancel` | `JobID` | `dto/deviceScanDto.go:50` |
| POST | `/agentApi/deviceScan/createDevices` | `JobID`, `Items`(min 1) | `dto/deviceScanDto.go:57,60` |
| POST | `/agentApi/deviceScan/discoverPoints` | `DeviceID` | `dto/devicePointScanDto.go:28` |
| POST | `/agentApi/deviceScan/createAddresses` | `DeviceID`, `Items`(min 1) | `dto/devicePointScanDto.go:39,42` |
| POST | `/agentApi/pushChannel/createPushChannel` | `Name`, `ConfigJSON` | 🔵 `dto/pushChannelDto.go` |
| POST | `/agentApi/pushChannel/testConnectivity` | `Name`, `ConfigJSON` | 🔵 同上 |
| POST | `/agentApi/alarmWebhook/createAlarmWebhook` | `Name`, `Type`, `URL` | 🔵 `dto/alarmWebhookDto.go` |
| POST | `/agentApi/openApiSecret/create` | `Name`(1–100) | `dto/openApiSecretDto.go:10` |
| POST | `/agentApi/createUser` | `Username`(2–50), `Password`(min 6) | `dto/userDto.go:8-9` |
| POST | `/agentApi/user/login` | `Username`, `Password`(min 6) | `dto/userDto.go:28-29` |

> ⚠️ **`msg` 里用的是 Go 字段名（PascalCase），不是 JSON 名**。
> 🟢 实测：JSON 里传 `protocolName` 缺了，`msg` 报的是 **`ProtocolName required`**（首字母大写）。
> 所以 DSH 侧**不能**用 `msg` 里的字段名去反查 JSON schema —— 直接展示即可。

> 🔵 **`updateDevice` / `updateDeviceAddress` / `updateAlarmWebhook` 是「部分更新」语义**：
> 字段多数可选、零值不覆盖。`updateAlarmWebhook` 的 `secret`/`atAll`/`atList`/`status` 是**指针**，
> `null`=不改、`""`/`false`=**清空**（`apiCatalog.md:530-534`）。工具 schema 必须能区分"未提供"与"清空"。

### A.3.5 一个"必填但没报参数错误"的特例

🟢 **实测**：

```
GET /agentApi/protocol/getProtocolByName          （无 name）
→ HTTP 200  {"code":"50002","msg":"协议不存在","isSuccess":false,"data":null,...}
```

🔵 **解释**：`dto/protocolDto.go` 的 `name` 未标 `required`，空值直接进服务层按"查不到"处理，
返回业务码 **`50002`（协议不存在）而非 `PARAM_ERROR`**。
**DSH 侧对"按名查询"类接口不能假设空参会得到参数错误。**

### A.3.6 一个"未报错但返回空"的特例

🟢 **实测**：

```
GET /agentApi/protocol/getDataTypes?protocolName=NoSuch.Protocol
→ HTTP 200  {"code":"0","msg":"success","isSuccess":true,
             "data":{"protocol":"NoSuch.Protocol",
                     "commonTypes":null,"extendedTypes":null,"allTypes":null}}
```

🔵 **解释**：`service/protocolService.go:140` 只在 `protocolName` **与 `protocolId` 同时为空**时才报 `10001`
（"协议名或协议ID不能为空"）。传了一个**不存在**的名字走的是"查不到 → 返回空清单且算成功"这条路。
**DSH 侧必须自行判断 `commonTypes == null` 表示"该协议不存在/无类型"，不能只看 `isSuccess`。**

---

## A.4 响应信封实测（成功 / 失败 / 401）

🟢 **全部实测**。数据结构 🔵 见 `backend/response/response.go:9-15`。

### A.4.1 信封字段类型（🟢 实测确认）

| 字段 | JSON 类型 | 说明 |
|---|---|---|
| `code` | **字符串** | ✅ 实测确认是 `"0"`（带引号），**不是数字 `0`**。失败时是 `"40002"` / `"PARAM_ERROR"` / `"-1"` 等字符串 |
| `msg` | 字符串 | 成功恒为 `"success"`；失败为中文描述（或 i18n 后文案） |
| `isSuccess` | **布尔** | ✅ 实测：成功 `true`、失败 `false` |
| `data` | 任意 / `null` | 失败时**恒为 `null`**（🟢 实测全部失败样例都是 `"data":null`） |
| `serverTime` | 字符串 | `"YYYY-MM-DD HH:mm:ss"`（🟢 实测确认是**空格分隔**、非 ISO8601、**无时区标记**） |

🔵 源码（`response.go:9-15`）：

```go
type Response[T any] struct {
	Code       string `json:"code"`
	Message    string `json:"msg"`
	IsSuccess  bool   `json:"isSuccess"`
	Data       T      `json:"data"`
	ServerTime string `json:"serverTime"`
}
```

🟢 **注意**：Go 字段名是 `Message`，但 **JSON tag 是 `msg`** —— DSH 侧要读 `msg` 不是 `message`。

### A.4.2 ✅ 成功样例（🟢 实测）

```http
GET /agentApi/collection/status HTTP/1.1
Host: 127.0.0.1:9082
```

**HTTP 200**

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "running": true,
    "lastPollTime": "2026-09-30 14:22:21",
    "lastSuccessTime": "2026-09-30 14:22:21",
    "errorCount": 1,
    "deviceCount": 8,
    "addressCount": 8
  },
  "serverTime": "2026-09-30 14:22:22"
}
```

### A.4.3 ✅ 失败样例 1 —— **不存在的设备 ID**（§4 要求的那条）

🟢 **实测**：

```http
GET /agentApi/device/getDeviceById/0000000000000000000000000000ffff HTTP/1.1
Host: 127.0.0.1:9082
```

**HTTP 200**（⚠️ **注意是 200，不是 404**）

```json
{
  "code": "40002",
  "msg": "设备不存在",
  "isSuccess": false,
  "data": null,
  "serverTime": "2026-09-30 14:25:59"
}
```

🔵 错误码出处：`backend/enums/resultEnum.go:65`
（`DeviceNotExistsEnum = NewBusinessEnum("40002", "设备不存在", "err.device_not_found")`）。

### A.4.4 ✅ 失败样例 2 —— 不存在的点位 ID

🟢 **实测**：

```http
GET /agentApi/deviceAddress/getDeviceAddressById/0000000000000000000000000000ffff
```

**HTTP 200**

```json
{"code":"40012","msg":"设备地址不存在","isSuccess":false,"data":null,"serverTime":"2026-09-30 14:25:59"}
```

🔵 `enums/resultEnum.go:85`（`40012 设备地址不存在`）。

### A.4.5 ✅ 失败样例 3 —— 参数校验失败（**`PARAM_ERROR`，非 `10001`**）

🟢 **实测**：

```http
GET /agentApi/device/pageDevice?page=1&size=200
```

**HTTP 200**

```json
{"code":"PARAM_ERROR","msg":"参数校验失败: Size max","isSuccess":false,"data":null,"serverTime":"2026-09-30 14:25:52"}
```

### A.4.6 ✅ 失败样例 4 —— 请求体 JSON 语法错误

🟢 **实测**：

```http
POST /agentApi/device/testDeviceConnection
Content-Type: application/json

{bad json
```

**HTTP 200**

```json
{"code":"PARAM_ERROR","msg":"请求体 JSON 格式错误","isSuccess":false,"data":null,"serverTime":"2026-09-30 14:26:24"}
```

🔵 `globalError.go:49-52`。

### A.4.7 ✅ 401 样例 1 —— 根路径缺 JWT

🟢 **实测**（**HTTP 401 —— 这是"业务失败也是 200"的唯一例外之一**）：

```http
GET /device/pageDevice?page=1&size=1        （无 Authorization 头）
```

**HTTP 401**

```json
{"code":"20006","msg":"令牌为空","isSuccess":false,"data":null,"serverTime":"2026-09-30 14:26:24"}
```

🔵 `middleware/authMiddleware.go:72`（`enums.TokenEmptyErr`，`resultEnum.go:61`，`"20006"`）。
🔵 令牌无效时是 `20005 无效的令牌`（`authMiddleware.go:88`）。

### A.4.8 ✅ 401 样例 2 —— `/openApi` 无效 X-Api-Key

🟢 **实测**：

```http
GET /openApi/device/page?page=1&size=1
X-Api-Key: bogus-key
```

**HTTP 401**

```json
{"code":"70004","msg":"开放接口密钥无效","isSuccess":false,"data":null,"serverTime":"2026-09-30 14:26:24"}
```

🟢 无 key 时（实测）：`{"code":"70003","msg":"开放接口密钥为空",...}`
🔵 `middleware/openApiAuthMiddleware.go:26-43`（`enums.OpenApiSecretKeyEmptyEnum` / `OpenApiSecretInvalidEnum`，`resultEnum.go:123-124`）。
🔵 设计意图（`openApiAuthMiddleware.go:36` 注释 + 网关文档原文）：
**"密钥不存在"与"密钥错误"统一返回 70004，不做区分**，避免攻击者探测有效密钥。

### A.4.9 HTTP 状态码总结（🟢 实测）

| 场景 | HTTP 码 | `code` |
|---|---|---|
| 任何业务成功 | **200** | `"0"` |
| 资源不存在（设备/点位/协议） | **200** | `"40002"` / `"40012"` / `"50002"` |
| 参数校验失败 | **200** | `"PARAM_ERROR"` |
| 请求体 JSON 语法/类型错误 | **200** | `"PARAM_ERROR"` |
| 业务规则错误（服务层显式抛） | **200** | `"10001"` / `"40001"` … |
| 未知系统错误 | **200** | `"-1"` |
| **缺/无效 JWT** | **401** | `"20006"` / `"20005"` |
| **缺/无效 X-Api-Key** | **401** | `"70003"` / `"70004"` |

> 🟢 **实测确认：只有 401 是例外，其余全部 200。**
> DSH 侧的 HTTP 客户端**必须只在 `status === 401` 时走"认证失败"分支，其余一律解析 JSON 看 `code`/`isSuccess`**。

### A.4.10 可运行的信封判定（TypeScript，🟢 依据实测）

```ts
/** 成功的唯一判据。code 是字符串，必须与 "0" 比较。 */
export const isOk = (env: { code: unknown; isSuccess: unknown }): boolean =>
  env.isSuccess === true && env.code === '0';

/** 参数类错误（binding 校验 + 服务层显式抛，两者码不同，都要认）。 */
export const isParamError = (env: { code: unknown }): boolean =>
  env.code === 'PARAM_ERROR' || env.code === '10001';

/** 业务码 → 可直接展示的中文（msg 已经是给人看的）。 */
export const describe = (env: { code: unknown; msg: unknown }): string =>
  `${String(env.code)}: ${String(env.msg)}`;
```

---

## A.5 两个 `.xlsx` 下载接口实测

🟢 **实测**。两个接口都**不返回 JSON 信封**，直接返回二进制文件流。

### A.5.1 `GET /agentApi/device/importTemplate`

🟢 **实测**：

```http
GET /agentApi/device/importTemplate HTTP/1.1
Host: 127.0.0.1:9082
```

**HTTP 200**，响应头：

```
Content-Type        : application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
Content-Disposition : attachment; filename="device-import-template.xlsx"
Content-Length      : 6316
```

**Body 前 8 字节（hex）**：

```
50 4B 03 04 14 00 08 00
```

**前 16 字节（ASCII 可读部分）**：

```
PK               ← ZIP 魔数 "PK\x03\x04"，确认是真 .xlsx（OPC 包 = ZIP）
```

✅ **确认是真二进制流，不是 JSON 信封**：body 以 ZIP 魔数 `50 4B 03 04`（`PK\x03\x04`）开头，
**没有任何 `{` `"code"` 等 JSON 特征**。实测 body 长度 6316 字节，与 `Content-Length` **完全一致**。

### A.5.2 `GET /agentApi/deviceAddress/importDeviceAddressTemplate`

🟢 **实测**：

```http
GET /agentApi/deviceAddress/importDeviceAddressTemplate HTTP/1.1
Host: 127.0.0.1:9082
```

**HTTP 200**，响应头：

```
Content-Type        : application/vnd.openxmlformats-officedocument.spreadsheetml.sheet
Content-Disposition : attachment; filename="device-address-import-template.xlsx"
Content-Length      : 6408
```

**Body 前 8 字节（hex）**：

```
50 4B 03 04 14 00 08 00
```

✅ 同样是 ZIP 魔数开头的真二进制流，body 长度 6408 与 `Content-Length` 一致。

### A.5.3 DSH 侧处理要点

| 项 | 值 |
|---|---|
| MIME 类型 | `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` |
| 文件名来源 | **`Content-Disposition` 的 `filename=` 参数**（不要自己猜） |
| 魔数 | `50 4B 03 04`（`PK\x03\x04`，ZIP） |
| 信封 | **无** —— 没有 `code`/`msg`/`isSuccess`/`data` |
| 失败时的行为 | 🔵 **未实测**（无法在不制造错误的前提下触发）。🔵 依据 `documents/` 与 `uploadHelper.go` 的代码结构，**失败仍走 JSON 信封**（`c.JSON(200, ...)`），因此 DSH 必须**先看 `Content-Type` 再决定解析方式** |

### A.5.4 推荐实现（TypeScript）

```ts
/**
 * 下载类接口：正文是文件流，不是 JSON 信封。
 * 判定顺序必须是「先看 Content-Type」，否则会在正常下载时 JSON.parse 抛错。
 */
export async function downloadXlsx(path: string): Promise<{
  bytes: Uint8Array; filename: string;
}> {
  const res = await fetch(`${BASE}${path}`, { signal: AbortSignal.timeout(30_000) });
  const ctype = res.headers.get('content-type') ?? '';

  // 失败时网关返回 JSON 信封（HTTP 200）——必须先于二进制分支判断
  if (ctype.includes('application/json')) {
    const env = (await res.json()) as Envelope<null>;
    throw new GatewayError(String(env.code), String(env.msg));
  }

  if (!ctype.includes('spreadsheetml.sheet')) {
    throw new Error(`意外的 Content-Type: ${ctype}`);
  }

  const buf = new Uint8Array(await res.arrayBuffer());

  // 校验 ZIP 魔数，防止拿到半截/错误内容还当成功
  if (!(buf[0] === 0x50 && buf[1] === 0x4b && buf[2] === 0x03 && buf[3] === 0x04)) {
    throw new Error('响应不是合法的 .xlsx（缺少 ZIP 魔数 PK\\x03\\x04）');
  }

  const cd = res.headers.get('content-disposition') ?? '';
  const filename = /filename="?([^";]+)"?/.exec(cd)?.[1] ?? 'template.xlsx';
  return { bytes: buf, filename };
}
```

---

## A.6 `realtime/snapshot` 必填参数与返回结构

🟢 **实测**（结构与字段名逐字取自真实响应）；🔵 补充来自 `backend/dto/realtimeDto.go` 与 `model/vo/realtimeVo.go`。

### A.6.1 必填参数（🟢 实测）

| 参数 | 位置 | 必填 | 类型 | 约束 | 实测缺失时报错 |
|---|---|---|---|---|---|
| `deviceId` | query | ✅ **是** | string | — | 🟢 `PARAM_ERROR` / `参数校验失败: DeviceID required` |
| `page` | query | ✅ **是** | int | `min=1` | 🔵 缺失报 `Page required` |
| `size` | query | ✅ **是** | int | `min=1,max=100` | 🔵 缺失报 `Size required` |
| `name` | query | ❌ 否 | string | 模糊匹配**点位名或说明** | — |

🟢 **实测请求**：

```http
GET /agentApi/realtime/snapshot?page=1&size=2&deviceId=01a0f032868f75e1959e1895b308900f
```

🔵 **源码**（`backend/model/dto/realtimeDto.go:10-15`）：

```go
type RealtimeSnapshotDTO struct {
	Page     int    `form:"page" binding:"required,min=1"`
	Size     int    `form:"size" binding:"required,min=1,max=100"`
	DeviceID string `form:"deviceId" binding:"required"`
	Name     string `form:"name"` // 点位名或说明（模糊查询）
}
```

### A.6.2 ⚠️ 返回结构不是 `PageVo`（🟢 实测确认）

`data` 是一个**带上下文的专用 VO**，`page`/`size`/`total`/`records` 与设备上下文**平铺在同一层**：

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "deviceId": "01a0f032868f75e1959e1895b308900f",
    "deviceName": "10.62.100.12",
    "protocolName": "OPC.UA",
    "online": true,
    "collecting": true,
    "collectorRunning": true,
    "lastSuccessTime": "2026-09-30 14:22:35",
    "serverTime": "2026-09-30 14:22:35",
    "page": 1,
    "size": 2,
    "total": 2,
    "records": [
      {
        "addressId": "01a0f03363f573b7919d1bf962651d3f",
        "name": "i=2994",
        "label": "Server/Auditing",
        "value": "1",
        "dataType": "bool",
        "commonDataType": "Boolean",
        "rwPermission": "R",
        "scanFrequency": 1000,
        "quality": 192,
        "collected": true,
        "stale": false,
        "updatedAt": "2026-09-30 14:22:35"
      },
      {
        "addressId": "01a0f03616bf7728ab4571abb0c81107",
        "name": "i=2255",
        "label": "Server/NamespaceArray",
        "value": "http://opcfoundation.org/UA/,urn:SIMATIC.S7-1500.OPC-UA.Application:PLC_1,http://opcfoundation.org/UA/DI/,http://www.siemens.com/simatic-s7-opcua",
        "dataType": "string",
        "commonDataType": "String",
        "rwPermission": "R",
        "scanFrequency": 1000,
        "quality": 192,
        "collected": true,
        "stale": false,
        "updatedAt": "2026-09-30 14:22:35"
      }
    ]
  },
  "serverTime": "2026-09-30 14:22:35"
}
```

### A.6.3 `records[]` 每行字段清单（🟢 实测，逐字段列出）

| 字段 | 类型 | 实测值示例 | 语义 |
|---|---|---|---|
| `addressId` | string | `"01a0f03363f573b7919d1bf962651d3f"` | 点位 ID（= `device_address.id`）。**注意不叫 `id`** —— 与 `pageDeviceAddress` 的 `id` 不同名 |
| `name` | string | `"i=2994"` | 点位名 = **设备地址写法**（与 `device_address.name` 同源） |
| `label` | string | `"Server/Auditing"` | 点位中文说明（与 `device_address.label` 同源） |
| `value` | string | `"1"` / `"http://..."` | **已解码的采集值字符串**。异常时为空串 `""` |
| `dataType` | string | `"bool"` / `"string"` | **协议内部**数据类型名 |
| `commonDataType` | string | `"Boolean"` / `"String"` | **通用**数据类型名（配置界面展示用） |
| `rwPermission` | string | `"R"` | `R` / `W` / `RW` |
| `scanFrequency` | int | `1000` | 扫描频率（毫秒） |
| `quality` | int | `192` | **采集质量码**（`driver.QualityXxx`，类别在高 2 位、成因在低 6 位）：`192` = 正常／`128` = 不确定但值可用（ABB 仿真信号）／`0` = 坏但未归类（认不出来的设备状态码、预算耗尽被延迟的点位）／`1` = 坏·地址级（地址坏/不存在，**永不自我恢复**）／`2` = 坏·通信级（设备在带内以可恢复状态拒绝，下轮原样重试）／`3` = 坏·解析级（设备答了但本地解码失败）。**判"坏"要写 `quality < 128`，不能写 `quality == 0`**。区间读取失败时置坏码且 `value` 为空串 |
| `collected` | bool | `true` | **是否已采集到**。`false` = 尚无值（**仍占位返回，保证行数稳定**） |
| `stale` | bool | `false` | 值是否陈旧 |
| `updatedAt` | string | `"2026-09-30 14:22:35"` | 该值的采集时刻 |

### A.6.4 `data` 顶层字段清单（🟢 实测）

| 字段 | 类型 | 语义 |
|---|---|---|
| `deviceId` | string | 设备 ID（回显请求参数） |
| `deviceName` | string | 设备名 |
| `protocolName` | string | 协议名（如 `OPC.UA`） |
| `online` | bool | 设备是否在线（**= 无 active 断联报警**，非即时 ping） |
| `collecting` | bool | 是否正在采集（`false` = 协议不可用或无活跃点位） |
| `collectorRunning` | bool | 采集引擎是否已启动 |
| `lastSuccessTime` | string | 最近一次成功采集时间（内存态） |
| `serverTime` | string | 服务端当前时间 |
| `page` / `size` / `total` | int | 分页元信息（**与 `records` 平铺，不在嵌套 `PageVo` 里**） |
| `records` | array | 点位行（见上表） |

### A.6.5 三个状态位的排障用法（🟢 实测确认字段存在 + 🔵 语义）

| 组合 | 诊断结论 |
|---|---|
| `collectorRunning=false` | 采集引擎**未启动** → 查 `GET /agentApi/collection/status`，必要时 `collection/start` |
| `collectorRunning=true, collecting=false` | 引擎在跑但此设备**没有活跃点位 / 协议不可用** → 查 `plugin/info` 与 `pageDeviceAddress` |
| `online=false` | 设备**已判离线** → 查 `GET /agentApi/alarm/listActive` |
| 全部正常但 `records[].collected=false` | 点位刚建、尚未轮到采集 → 等待或 `collection/refresh` |

---

## A.7 `deviceScan/discoverPoints` 实测耗时与返回结构

🟢 **实测，两次调用**（对在线设备 `10.62.100.100`，Modbus.TCP，`id=01a0eaf2f3b67675a08acccb59ea4cc4`）。

> ✅ **安全性说明**：该接口被 `__surface` 标为 `probe`（不改状态）。
> 代码依据上它只做"复用设备会话读一段地址"（`driver/pointDiscovery.go` 契约），
> **不落库、不改配置**，结果只返回给调用方 —— 所以它属于安全的只读探测。
> 我刻意选了**小范围**（`count=16` 和 `count=3`）以避免长时间占住采集会话锁。

### A.7.1 计时结果（🟢 实测）

| 次序 | 请求 | `options` | 客户端往返耗时 | 响应内 `elapsedMs` |
|---|---|---|---|---|
| 1 | 首次调用 | `{functionCode:"3", startAddress:0, count:16}` | **490 ms** | *(该次未取)* |
| 2 | 紧接着再调 | `{functionCode:"3", startAddress:0, count:3}` | **55 ms** | **12** |

🟢 **观察**：
- 首次 490ms（含建会话/首次往返），第二次 **55ms**（会话已复用）。
- 两次都**远低于**网关的 `effectiveBudgetMs = 7000`，说明**正常情况不会触及 7 秒硬钳**。
- 响应内的 `elapsedMs`（12）是**网关侧测的纯探测耗时**，与客户端往返（55ms）之差约 43ms 是 HTTP + JSON 开销。

> ⚠️ **不要据此认为"这个接口很快"**。本次是小范围（3–16 点）、设备在线、Modbus TCP。
> 🔵 依据 `specs/point-discovery.md` 与 `default.yaml` 注释，**大范围扫描 / 串口低速链路会接近甚至触到 7000ms 上限**，
> 所以 **DSH 侧 HTTP 超时仍必须 > 8 秒**（`agentApiSurface.go:291-296` 的 note 也明确要求）。

### A.7.2 返回结构（🟢 实测，完整响应原文）

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "protocol": "ModBus.TCP",
    "points": [
      { "name": "%MW0", "note": "当前值为 0" },
      { "name": "%MW1", "note": "当前值为 0" },
      { "name": "%MW2", "note": "当前值为 0" }
    ],
    "assumedCommonDataType": "Word",
    "scanned": 3,
    "readable": 3,
    "unreadable": 0,
    "truncated": false,
    "incomplete": false,
    "elapsedMs": 12
  },
  "serverTime": "2026-09-30 14:26:19"
}
```

### A.7.3 `data` 顶层字段清单（🟢 实测，逐字段）

| 字段 | 类型 | 实测值 | 语义 |
|---|---|---|---|
| `protocol` | string | `"ModBus.TCP"` | 协议名 |
| `points` | array | 见下 | 发现的点位清单（**候选清单，非设备点位表**） |
| `assumedCommonDataType` | string | `"Word"` | **结果级**默认通用类型。**为空串 = 本次扫描没有采样值**（如 Rockwell.CIP 只列标签） |
| `scanned` | int | `3` | 尝试读取的地址数 |
| `readable` | int | `3` | 有应答数（**含值为 0 的**） |
| `unreadable` | int | `0` | 读不到数 |
| `truncated` | bool | `false` | 达到 `maxPoints`（默认 2000），后面的地址**根本没试** |
| `incomplete` | bool | `false` | 预算耗尽、范围**没扫完**（与"读不到"是两回事） |
| `elapsedMs` | int | `12` | 🟢 **实测新增发现**：网关侧探测耗时（毫秒）。**§4.9 的字段表里没有这一项** |

> 🟢 **不变量实测成立**：`len(points)=3 <= readable=3 <= scanned=3`，且 `readable + unreadable = 3 + 0 = 3 = scanned`。✅

### A.7.4 `points[]` 每行字段（🟢 实测）

| 字段 | 类型 | 实测值 | 语义 |
|---|---|---|---|
| `name` | string | `"%MW0"` | **设备地址写法**（Modbus 用 IEC `%MW` 风格，0-based） |
| `note` | string | `"当前值为 0"` | 人类可读补充说明（🔵 对该协议是采样值提示；OPC UA 等只列不读的协议里会放类型说明） |

🟢 **重要观察**：本次响应里 `points[]` **只有 `name` 与 `note` 两个字段**，
🔵 而 `specs/point-discovery.md` 里提到的 `DiscoveredPoint.CommonDataType`（逐点类型）
**在 Modbus 这条路径上没有出现** —— 因为 Modbus 走的是"结果级 `assumedCommonDataType`"那一支。
🔵 前端的三级回退是 `逐点类型 → 结果级 → 规格默认`，所以 DSH 侧也必须实现同样的回退，
**不能假设 `points[]` 每行都带类型**。

### A.7.5 `data` 里**没有** `deviceId`（🟢 实测纠正）

🟢 实测确认：返回的 `data` 顶层**只有** `protocol, points, assumedCommonDataType, scanned, readable, unreadable, truncated, incomplete, elapsedMs`
—— **没有 `deviceId`、没有 `deviceName`（我最初按 VO 命名习惯预期的字段名不存在）**。
DSH 侧要关联回设备必须**自己记住请求里传的 `deviceId`**。

### A.7.6 `createAddresses` 的请求形状（🔵 源码，未实测）

🔵 因为 `createAddresses` 是 **write**（会真的建点位行），本报告**刻意未实测**。
🔵 依据 `dto/devicePointScanDto.go:37-73`，把上表的 `points[]` 转成请求时需补齐：

```json
{
  "deviceId": "<请求时的 deviceId>",
  "items": [
    {
      "name": "%MW0",              // 必填，原样来自 points[].name
      "label": "……",               // 可空，服务端兜底为 name
      "commonDataType": "Word",    // 必填；Modbus 用结果级 assumedCommonDataType
      "rwPermission": "R",
      "scanFrequency": 1000,
      "description": ""
    }
  ]
}
```

> ⚠️ 与 `createDevices` **相反**：这里**采信客户端全部字段**（`devicePointScanDto.go:47-51` 注释），
> 唯一的服务端权威是 `deviceId`。所以 **DSH 侧必须原样透传 `points[].name`，绝不可改写** ——
> 写坏一个地址会让**整台设备**采集全部失败。

---

## A.8 网关正在运行的版本号

🟢 **实测，两次独立调用**（间隔约 4 分钟，结果一致）。

### A.8.1 `GET /agentApi/gateway/get`（🟢 实测，原始响应）

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "id": "94ea29ca23a94cffb279474acab664ef",
    "name": "23VJV25B27302557",
    "port": 9082,
    "firmwareVersion": "1.0.0",
    "description": "",
    "status": 1,
    "createdAt": "2026-09-18 14:50:27",
    "updatedAt": "2026-09-18 14:50:27"
  },
  "serverTime": "2026-09-30 14:26:24"
}
```

| 字段 | 值 | DSH 含义 |
|---|---|---|
| `firmwareVersion` | **`"1.0.0"`** | 网关上报的版本。🔵 **注意**：`default.yaml:38-40` 与 `configFile.go:102-105` 说明它**以配置为准**，每次启动比对并同步进库（`seedGateway.go` 的 `syncGatewayVersion`），所以它等于 `default.yaml` 的值、未必等于当前二进制 |
| `port` | **`9082`** | 🔵 **只在首启写入**，不是当前运行端口（`default.yaml` 注释 + `backend/CLAUDE.md`）。当前运行端口恰好也是 9082，但**不应依赖它做断言** |
| `id` | `94ea29ca23a94cffb279474acab664ef` | 网关唯一标识（32 位 hex，**不是** UUID v7 的 36 位带连字符格式；🔵 疑为 `utils.GenerateUUID()` 去连字符） |
| `name` | `23VJV25B27302557` | 看起来是**主机名**（🔵 `default.yaml:37` 的 `gateway.name` 留空 → 回落到 `os.Hostname()`，与 `configFile.go:98` 的注释一致） |
| `status` | `1` | 🔵 `gateway/update` 刻意不开放此字段 |
| `createdAt` / `updatedAt` | 均为 `2026-09-18 14:50:27` | **从未被修改过**（两值相同 → 该行自首启后未被 `gateway/update` 动过） |

### A.8.2 `GET /agentApi/plugin/info`（🟢 实测，原始响应）

```json
{
  "code": "0",
  "msg": "success",
  "isSuccess": true,
  "data": {
    "os": "windows",
    "arch": "amd64",
    "dir": "D:\\code\\汉得\\iot-gateway-premium\\backend\\plugins",
    "glob": "iot-driver-*",
    "fileExt": ".exe",
    "enabled": true
  },
  "serverTime": "2026-09-30 14:26:24"
}
```

| 字段 | 值 | DSH 含义 |
|---|---|---|
| `os` / `arch` | `windows` / `amd64` | 运行平台。DSH 侧要上传插件时应据此判断目标二进制架构 |
| `dir` | `D:\code\汉得\iot-gateway-premium\backend\plugins` | **插件绝对路径** —— 揭示网关进程的工作目录/可执行文件位置 |
| `glob` | `iot-driver-*` | 🔵 对应 `default.yaml:129` |
| `fileExt` | `.exe` | 平台相关的插件后缀（🔵 Linux 上应为空/无后缀） |
| `enabled` | `true` | 🔵 **出厂即为 true**（`default.yaml:122`）。`false` 时网关**不支持任何协议** |

### A.8.3 版本相关结论汇总

| 项 | 值 | 来源 | 可信度 |
|---|---|---|---|
| 运行实例上报版本 | **`1.0.0`** | 🟢 `gateway/get` | 高（网关自报） |
| 配置中的版本 | `1.0.0` | 🔵 `default.yaml:41` | 高（源码） |
| Docker 镜像 tag | `iot-gateway:1.0` | 🔵 `docker-compose.yml:11` | 高（源码） |
| **当前二进制真实版本** | **未知** | — | ⚠️ **无法确定**：库内版本每次启动都从 `default.yaml` 同步，反映的是配置值而非二进制；`CHANGELOG.md` 又**没有已发布版本号小节**（🟢 grep `^## \[[0-9]` 零命中） |
| 运行端口 | **9082** | 🟢 `gateway/get` + 🟢 `docker-compose` + 🟢 实际连通 | **高（三源一致）** |
| 插件系统状态 | **启用**（`enabled: true`） | 🟢 `plugin/info` | 高 |
| 已装插件数 | **9 个** `iot-driver-*.exe` | 🔵 目录列举 + 🔵 `plugins/` 实际内容 | 高 |
| 已注册协议数 | **15** | 🟢 `protocol/pageProtocol` 的 `total:15` | **高（网关自报）** |

> 🟢 **实测交叉印证**：`plugin/info.enabled = true` 且 `pageProtocol.total = 15`，
> **与"15 个协议全部由插件提供"完全一致** —— 若 `enabled=false`，协议表应为空或设备全被跳过。
> 这条可用于 DSH 的**启动自检**：`plugin/info.enabled === true` 且 `pageProtocol.total > 0` 才算健康。

---

## A.9 本附录新增/纠正的结论汇总

### A.9.1 对正式报告 §4 的**纠正**

| # | 正式报告写的 | 🟢 实测纠正 |
|---|---|---|
| 1 | §4.14 错误码表把 `10001` 列为"参数校验错误" | ⚠️ **通用的 binding 校验错误返回的是字符串 `"PARAM_ERROR"`，不是 `"10001"`**。`10001` 只在服务层显式抛出的少数路径出现。**两者都要认** |
| 2 | §4.9 的 `PointScanResultVO` 字段表未列 `elapsedMs` | 🟢 **实测 `data` 顶层确有 `elapsedMs`**（网关侧探测耗时，毫秒） |
| 3 | §4.9 暗示 `points[]` 带 `CommonDataType` | 🟢 **Modbus 路径下 `points[]` 只有 `name` 与 `note`**，逐点类型字段不存在；必须实现"逐点 → 结果级 → 规格默认"三级回退 |
| 4 | §4.9 未说明返回是否含 `deviceId` | 🟢 **实测 `data` 里没有 `deviceId`**，DSH 必须自己记住请求时的 `deviceId` |

### A.9.2 新增的关键事实

| # | 事实 | 影响 |
|---|---|---|
| 5 | **路径占位符只有 `:id` 一种，共 13 条** | 路由匹配器只需处理一个占位符名，实现大幅简化 |
| 6 | 13 条占位符路由中 **5 条是 GET 删除**、9 条是 GET | 占位符路由是"GET 陷阱"最密集的区域 |
| 7 | **`page`/`size` 在绝大多数分页接口都必填**，唯一例外 `log/listFiles` | DSH 必须无条件带上这两个参数 |
| 8 | 校验错误的 `msg` 用 **Go 字段名（PascalCase）**，不是 JSON 名 | 不能拿 `msg` 反查 schema，只能展示 |
| 9 | **两个 xlsx 接口返回真 ZIP 二进制流**（魔数 `50 4B 03 04`），MIME 与文件名均由响应头给出 | 必须先判 `Content-Type` 再决定解析方式 |
| 10 | `discoverPoints` 小范围实测 **490ms（首次）/ 55ms（复用会话）**，远低于 7000ms 钳制 | 正常场景很快，但超时仍必须 > 8s |
| 11 | `getProtocolByName` 缺 `name` 返回 **`50002` 而非 `PARAM_ERROR`** | "按名查询"类接口不能假设空参报参数错 |
| 12 | `getDataTypes` 传**不存在**的协议名返回 **`isSuccess:true` + 全 `null`** | 必须自行判 `commonTypes == null`，不能只看 `isSuccess` |
| 13 | `openApiSecret/doc` 的 `data` 是**完整 HTML 文档字符串**（含错误码表，实测体积极大） | 不要意外把它塞进模型上下文；它是 `read` 但响应可达数十 KB |
| 14 | `gateway/get` 的 `createdAt == updatedAt` | 可用于判断"网关身份行是否被改过" |
| 15 | **只有 401 不是 HTTP 200**（🟢 全部 6 类失败样例验证） | 客户端逻辑：`status===401` 走认证分支，其余一律解析 JSON |

### A.9.3 DSH 启动自检推荐序列（🟢 全部可实测）

```
1. GET /health
   → 期望 {"status":"ok"}（裸 JSON，无信封）

2. GET /agentApi/__surface
   → 期望 code="0"，data.length === 83
   → 用它构建 access → confirm 策略表（不要硬编码条数，仅作连通性断言）

3. GET /agentApi/gateway/get
   → 记录 id / name / firmwareVersion / port，用于展示与日志

4. GET /agentApi/plugin/info
   → 断言 enabled === true（否则网关不支持任何协议）

5. GET /agentApi/protocol/pageProtocol?page=1&size=100
   → 断言 total > 0（实测 15）

6. GET /agentApi/collection/status
   → 记录 running（false 时提示用户启动采集）

7. GET /agentApi/push/status
   → 记录各通道 connected / droppedCount

8. GET /agentApi/alarm/listActive
   → 当前离线设备清单
```

全部 8 步**无需任何凭据**，实测均返回 `code: "0"`。这 8 次调用可作为 DSH 插件的"连接体检"。
