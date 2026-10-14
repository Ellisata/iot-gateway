# 局域网自动发现设计（discovery）

> 本规范定义网关如何把自己的身份**发布到局域网**，使其他管理平台无需预配置、
> 无需凭据即可自动发现并接入。
>
> **本文档是对外契约**：管理平台侧照第 3 节实现一个 UDP 接收循环即可对接。
> 报文字段的真源在 [../discovery/message.go](../discovery/message.go)，改动契约时两者必须同步。

范围：**只做单向发布与探询应答**。网关不维护"局域网里有哪些网关"的名单 ——
那是管理平台的职责。

---

## 1. 总体架构

```
                    ┌──────────── 子网广播地址:9083 ────────────┐
                    │                                          │
   discovery.Announcer                             管理平台 (UDP 接收循环)
     ┌──────────────┴──────────────┐
     │ announceLoop                │  write ─ notify ─▶  被动发现（每 5s 一条）
     │   每轮重读 iot_gateway 身份  │
     │ readLoop                    │  ◀ probe ─ read ─   主动探询（秒级）
     │   校验 → 限流 → reply        │  ── reply ─▶        单播应答
     └─────────────────────────────┘
                    单个 UDP 套接字，绑定 0.0.0.0:9083
```

- **一套接字双向**：同一个绑定在 `0.0.0.0` 上的套接字既发广播也收探询。
  拆成两个套接字后，回包的源端口会变成随机值，探询方就没法把请求与应答对起来；
  而且只有绑定在 `0.0.0.0` 上的套接字才能在各张网卡上都收到探询。
- **逐网卡定向广播**：对每张可用 IPv4 网卡算出子网广播地址（`ip | ^mask`），
  各发一条。不发 `255.255.255.255` —— 受限广播只会在 FIB 选中的那一张网卡出去
  （通常是默认路由），并不比定向广播多覆盖任何东西，反而会给 VPN / docker0 添噪声，
  且常被广播风暴抑制丢弃。
- **无认证**：收发两侧都不校验来源。这是发现阶段本就没有共享秘密的必然结果，
  不是疏漏。代价是这条通道上**任何能收到本网段广播的主机都能读到报文内容**。
- **刻意公开一个凭据**：报文里的 `apiKey` 是启动播种的默认开放接口密钥
  （`open_api_secret` 里名为 `default` 的那一条）。这是本功能唯一让出的秘密，
  目的很具体：管理平台发现网关后**无需人工录入密钥**就能直接调它的开放接口。
  取舍是清楚的 —— 发现阶段本就没有共享秘密可用来先认证再下发密钥，
  要免录入就只能让它随广播一起明文出去。
  **但这条通道的额度只有这一个凭据**：`apiKey` 之外不得再出现任何秘密
  （token、口令、设备凭据），加字段前先问一句"它泄露给同网段主机会怎样"。
  不放心的部署把 `discovery.enabled` 关掉即可 —— 关掉后网关对此完全静默，
  代价是管理平台只能手工录入 `IP:端口` 与密钥。
- **不引入第三方库**：纯标准库实现，无 mDNS / zeroconf 依赖。

## 2. 配置

```yaml
discovery:
  enabled: true        # 默认开启；关闭后网关对局域网完全静默
  port: 9083           # 广播与监听共用端口
  intervalMs: 5000     # 广播间隔
```

- 仅配置文件控制，**不落库、不动前端**：网关身份本来就在 `iot_gateway` 单行表里，
  发现只是把那一行读出来发出去，没有新的可编辑状态。
- 各项都在**每次广播时实时生效**：改端口/间隔重启即可，改名称则**无需重启**
  （下一轮广播即带新值）。
- `enabled=false` 时 `Start()` 静默无操作：不绑端口、不起协程、不打日志。

## 3. 报文格式

UDP 端口 **9083**，载荷为 UTF-8 JSON。

```json
{
  "protocol": "iot-gateway-discovery",
  "version": 1,
  "type": "notify",
  "gatewayId": "a1b2c3d4e5f6...",
  "name": "车间网关-01",
  "ip": "192.168.1.50",
  "port": 9082,
  "firmwareVersion": "1.0.0",
  "description": "1 号车间",
  "apiKey": "9f2c...64 位 hex...",
  "timestamp": 1757999999000
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `protocol` | string | 恒为 `"iot-gateway-discovery"`。**接收方必须先校验它**，用于过滤该端口上的非本协议流量 |
| `version` | int | 协议版本，当前 `1`。不认识的版本一律**静默丢弃**，不要"尽力解析" |
| `type` | string | `notify` / `probe` / `reply`，见下 |
| `gatewayId` | string | 网关唯一标识（`iot_gateway.id`）。平台据此把同一台网关的 notify 与 reply 归并成一条记录 |
| `name` | string | 网关名称 |
| `ip` | string | 本端地址。`notify` 中=**本次广播所用网卡的地址**；`reply` 中=路由选出的、探询方能回连的地址。**接收方应以 UDP 源地址为准**，本字段仅作参考 |
| `port` | int | 网关 HTTP 服务端口（**当前运行值**） |
| `firmwareVersion` | string | 网关软件版本 |
| `description` | string | 网关描述 |
| `apiKey` | string | **默认开放接口密钥**（`open_api_secret` 里名为 `default` 那条的内容，64 位 hex），供平台免人工录入调用开放接口。缺失表示该网关没有默认密钥（老库未升级或被改过库），平台需回退到人工录入。见第 1 节的取舍说明 |
| `timestamp` | int64 | 发出时刻的 Unix 毫秒。**仅供参考，不应信任**：不参与校验也不参与排序（跨主机时钟不可比） |

除 `protocol` / `version` / `type` 外全部字段可选。**新增字段必须带 `omitempty`**，
让旧版接收方仍能解析出它认识的部分。

### 报文类型

| `type` | 方向 | 说明 |
|--------|------|------|
| `notify` | 网关 → 子网广播地址 | 周期发布"我是谁"。启动时**立即额外发一次** |
| `probe` | 管理平台 → 广播 | 探询"谁在？"，只需带 `protocol`/`version`/`type` |
| `reply` | 网关 → 单播回探询源 | 应答 `probe`，字段与 `notify` 相同 |

网关收到 `notify` 一律忽略（那是别的网关发的），只处理 `probe`。
未知 `type` 一律忽略 —— 9083 不是保留端口，陌生流量属正常现象。

## 4. 交互时序

```
网关启动 ──▶ 立即广播 notify ──▶ 之后每 intervalMs 一次
                                    （平台被动发现，最坏等待 = intervalMs）

管理平台启动 ──▶ 广播 probe ──▶ 网关单播 reply ──▶ 平台得到完整身份
                （若被限流或身份缺失而未得应答，等下一个 notify 周期兜底）
```

- **启动即宣告**：网关重启后平台在一次报文内就能发现，不必等满一个周期。
- **探询应答是"锦上添花"**：单靠周期广播平台也能发现全部网关，只是慢一点；
  `probe` 让平台能在启动瞬间（或用户点"刷新"时）拿到完整名单。因此
  **限流丢弃一次探询不影响正确性**，平台侧不需要重试逻辑。

## 5. 代码结构

```
discovery/message.go   报文契约（Protocol/Version/Type 常量、Message、encode/decode）
discovery/netif.go     本机 IPv4 网卡枚举与子网广播地址计算（纯函数）
discovery/announcer.go Announcer：Start/Stop、announceLoop、readLoop、探询限流
```

Wire 接线（[../wire.go](../wire.go)）：

- `discovery.NewAnnouncer(cfg, src, keys)` 挂到 `AppDependencies.Discovery`；
- `wire.Bind(new(discovery.IdentitySource), new(*service.GatewayService))` ——
  身份来源复用已有的 `GatewayService.GetGateway`，没有新增查询路径；
- `wire.Bind(new(discovery.ApiKeySource), new(*service.OpenApiSecretService))` ——
  默认密钥来源是 `OpenApiSecretService.DefaultOpenApiKey`（同一次 `wire.Build`
  里已有的 provider）。两者是**并列的两个接口**而不是一个：身份来自 `iot_gateway`
  单行表、密钥来自 `open_api_secret`，合并会让只想测广播格式的用例被迫实现一个
  用不到的密钥源；
- `main.go` 在采集引擎之后 `Start()`（失败只记日志），并把它作为**最先**停止的一项。

关键实现约定：

| 约定 | 理由 |
|------|------|
| 网络字面量写死 `"udp4"` | Windows 的 `setDefaultSockopts` 对 AF_INET6 **不设** `SO_BROADCAST`，双栈主机上裸 `"udp"` 可能落到 AF_INET6 |
| 不设 `SO_REUSEADDR` | 同主机第二个实例就该 `EADDRINUSE`。共用接收端口会让探询应答落到不确定的那个实例上 |
| 每轮重读身份 + 重新枚举网卡 | 改名/插拔网线在一个周期内自愈；单行库查询是微秒级，缓存只会买来失效问题 |
| 读循环对非 `net.ErrClosed` 错误 `continue` | Windows 上此前回包的 ICMP port unreachable 会以 `WSAECONNRESET` 在**下一次** `recvfrom` 暴露；当成终止条件会让读循环静默死掉 |
| `Stop()` 先摘字段再 `close` | 支持重复调用（否则二次 close panic）；故子协程拿的是 Start 传入的句柄，不读字段 |
| 先 `listen` 成功再置 `stopCh` | 与 `alarm.ChannelMonitor` 唯一的偏离：`Start()` 会失败（端口占用），而 `main.go` 只记日志、随后仍会 `defer Stop()` |

## 6. 边界

- **身份行缺失**（`iot_gateway` 为空，即启动播种失败）：跳过本轮广播，下一轮再试；
  只在状态翻转时打一条 WARN，不每轮刷屏。探询同样不应答。
  读循环保持存活，身份行一出现即恢复。
- **默认密钥缺失**（`open_api_secret` 里没有 `default` 行）：**照常广播**，
  只是省略 `apiKey` 字段；同样不告警（只记 DEBUG）。与上一条刻意不同的处置 ——
  身份是"我是谁"，缺了这条广播就没有意义；密钥是附带的便利，少一个字段不该
  让整条通道失效，更不该每 5 秒刷一条 WARN 去淹掉真正的告警。
- **探询限流**：同源地址每秒最多换来一次应答（防反射放大）。限流表有 TTL 与硬上限
  （源地址可伪造，无上限即一条可被远程触发的慢速内存泄漏）。
  **未应答的探询不占用限流额度**，否则身份行缺失期间会出现"探询时灵时不灵"。
- **无可用网卡**（只有 loopback）：广播目标为空，记一条 WARN 后照常运行 ——
  监听侧仍然工作，平台主动探询照样能收到应答。**这不是启动失败。**
- **掩码跳过**：`/31` `/32` `/0` 一律跳过。它们不是"无用"而是**错误** ——
  `/32` 算出的广播地址是自己（单播给自己），`/31`（RFC 3021 点对点）算出的是
  **对端**的单播地址，`/0` 算出 255.255.255.255。同时跳过：loopback、网卡 down、
  点对点、纯 IPv6、IPv4-mapped 之外的非 IPv4、169.254/16（APIPA）、0.0.0.0。
  同网段多地址按广播地址去重。

### 部署注意

- **Windows 防火墙**：出站广播默认放行，但入站 9083 默认被拦截，且以 Windows 服务
  （SCM）方式运行时**不会**弹交互式放行提示 —— 症状是"平台能看到网关通知，
  但主动探询收不到应答"，且日志里什么都没有。手工放行：
  ```
  netsh advfirewall firewall add rule name="IoT Gateway Discovery" ^
    dir=in action=allow protocol=UDP localport=9083
  ```
- **Docker 默认 bridge 网络**：容器在独立 netns 中，广播出不了 `docker0`，
  且它公布的 `172.17.0.x` 从局域网不可达。需要 `network_mode: host`（Linux）
  或 macvlan 才能被局域网发现。
