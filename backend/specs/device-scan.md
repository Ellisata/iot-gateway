# 局域网设备扫描发现（device-scan）

> 本规范定义网关如何**主动扫描本机网段、发现 PLC 等设备，并一键建成 `device` 行**。
>
> **本文档是契约**：`probe` 方法的字段、预算模型与取消语义是网关与协议插件之间
> 的约定。字段真源在 [../driver/discovery.go](../driver/discovery.go)、
> [../sdk/driversdk/rpc.go](../sdk/driversdk/rpc.go) 与 [../pluginhost/discovery.go](../pluginhost/discovery.go)，
> 改动契约时两者必须同步。

范围：**只做"网关去问网段里谁在"**，不做"我告诉局域网我在哪" ——
后者是 [discovery.md](discovery.md)。两个方向语义相反，命名上刻意分开
（包名 `devicescan/` 与 `discovery/`），别混。

范围也**不包括**"这台设备上有哪些点位" —— 那是
[point-discovery.md](point-discovery.md)，与本文档是**并列的两件事**：
那边扫的是**一台已存在的设备**、要复用采集会话、一次 RPC 覆盖整段、不可取消；
这边扫的是**网络**、无会话、一次 RPC 一个目标、可取消。

**两篇必须成对修改**，否则必然漂移 —— 它们共同依赖三条道理（不建能力缓存表 /
预算是每协议属性且 RPC 超时按插件声明推导 / 串口独占与会话复用），
详见 [point-discovery.md](point-discovery.md) §12。

---

## 1. 总体架构

```
前端扫描页 ──POST /deviceScan/start──▶ DeviceScanService ──▶ devicescan.Engine
   ▲                                                              │ 规划目标（本机网段展开）
   │ GET /deviceScan/status?jobId=（轮询）                         │ 并发 worker（上限可配）
   └──────────────────────────────────────────────────────────────┤
                                                                  ▼
                                            pluginhost.Manager.ProbeTargets(ctx, protocol, options, target)
                                                                  │  RPC「probe」，**无会话**
                                                                  ▼
                                                        插件进程内的 prober
                                                  （Modbus TCP：裸 MBAP → FC43/14 读设备标识
                                                    → FC3 兜底判定"是 Modbus" → 站号扫描）
```

职责边界只有一条，但它是整个设计的支点：

- **网关**规划目标（枚举本机网段、展开主机地址）、控制并发、限速、统计进度、取消；
- **插件**只回答一个问题：「你给我的这个 IP:端口，是不是我的设备」。

这样并发/限速/取消这些与协议无关的事只写一遍，而插件不必各自实现一套网段枚举
与 worker 池。广播型协议（EtherNet/IP 的 ListIdentity、FINS/UDP 的节点查询）
由网关把各网段的**子网广播地址**也加进目标列表即可覆盖，不需要插件自己扫网段。

**为什么任务状态机在网关侧**：目标由网关规划，所以进度、结果、取消全都留在网关
内存里；对插件的 `probe` 是一次短调用，不引入长 RPC。

---

## 2. 能力声明（插件侧）

协议是否支持扫描，由插件在 `describe` 时通过 `Meta.Protocols[].Discovery` 声明。
**nil 表示不支持**，该协议不出现在扫描页的协议下拉里。

| 字段 | 语义 |
|------|------|
| `Title` | 扫描页下拉里的显示名 |
| `Description` | 扫描行为说明（提示到前端） |
| `FormJSON` | 扫描选项表单（form-create schema），用户填的内容原样透给 prober |
| `DefaultPort` | 默认探测端口，前端据此回填端口输入框 |
| `TargetMode` | `unicast` / `broadcast` / `both`，决定网关生成什么目标（留空按 unicast） |
| `MaxBudgetMs` | **单目标硬预算**，见第 4 节 |

启动期校验（`driversdk.Meta.Validate`）会拒绝：Title 为空、端口越界、
TargetMode 非法、`MaxBudgetMs` 不在 `[500, 60000]`、FormJSON 不是合法 JSON、
以及**串口协议声明扫描能力**（目标模型是 host/port，表达不了 comPort；
串口上"总线上有哪些站号"是另一个功能）。

声明了 `Discovery` 的协议**必须有对应的 prober**（`driversdk.Probers`），
否则插件进程启动即失败 —— 与"每个协议都必须有工厂"是同一条校验，理由也一样：
漏配要在启动时报错，而不是等运维点开扫描页看到"扫不到设备"。

字段可选 ⇒ **不递增 `APIVersion`**：新网关 + 旧插件走 nil 分支（该协议不能被扫描），
旧网关 + 新插件忽略未知字段。递增版本号会让全部旧插件二进制在网关升级后失效，
是一次没必要的破坏性升级。

---

## 3. probe 线协议

```
method: "probe"

ProbeParams {
  protocol  string        // Meta.Protocols[].Name
  options   string        // 扫描选项原文；未填时为空串（不是空 JSON）
  target    ScanTarget    // 单个目标
  budgetMs  int           // 网关请求的预算；插件取 min(自己声明的, 本值)
}

ProbeReply {
  devices []DiscoveredDevice
  errors  []{host, reason}   // **"没扫到"不进这里**
}
```

四条约定：

1. **无会话**。与 `ping` 一样不带 `session`：探测不分配会话资源、不持有跨调用
   资源，调用方也永远不为它调 `Close`。否则每次扫描都会在插件进程里泄漏一个会话。

2. **一次 RPC 一个目标**。契约最简单，插件不必自己写并发与批量预算分摊；
   吞吐由网关侧的 worker 池控制。代价是 /24 网段会产生 254 次 RPC ——
   对本地管道可忽略，而"每次超时都会累积卡死计数"的风险由第 5 节解决。

3. **`options` 与结果的 `protocolJson` 都是 `string`，不是 `json.RawMessage`**。
   空的 `RawMessage` 会让编码失败并报一句"编码失败"，与真正的根因
   （用户没填选项）相隔十万八千里；而应答里一个坏的 `RawMessage` 会让整批结果
   退化成一条 `RemoteError`，与"逐条失败不中断整批"的契约直接冲突。
   校验推迟到服务层逐条 `json.Valid`。

4. **"没扫到"不是错误**。目标不可达、端口上跑着别的服务、设备不支持读设备标识，
   都是扫描的正常结果，prober 返回空列表 + `nil`。把这类情况当 error 会让一次
   /24 扫描产出两百多条"错误"噪声。`errors` 只装**探测本身失败**
   （选项解析不出来、协议内部错误）。

---

## 4. 预算与超时

**`MaxBudgetMs` 由插件声明，不是配置项。** 理由：预算是每协议的属性
（UDP 广播探测天然比 TCP 拨号慢），而配置项是全局的一个旋钮。全局旋钮配小了，
症状是 RPC 先超时、插件其实还在跑、网关拿到空结果、用户看到"扫不到设备"，
而日志里只有一条 timeout —— 典型的"配错了就静默不工作"。

网关侧的 probe RPC 超时因此由它推导：

```
RPC 超时 = spec.MaxBudgetMs + probeRPCGrace（3s）
```

`MaxBudgetMs` 的取值区间由 `Meta.Validate` 限死在 `[500, 60000]`，
所以 RPC 超时自然有界，`plugin` 段因此**没有** `probeTimeoutMs` 这一项。

插件侧再取一次 `min(自己声明的, 网关请求值)`：插件是必须不超时的那一方，
不能把这条不变量交给对端给的值。

**预算耗尽时的正确行为是带上已完成的部分结果正常返回**（站号只扫了一半就返回
已扫到的那几台），不是返回 error —— 超预算返回 error 会让网关侧 RPC 超时，
进而累积卡死计数。

### 多阶段握手：预算要按"几次 socket 等待"反推着花

`MaxBudgetMs` 是**单目标**的硬预算，而有的协议一次探测要顺序做**多次**带超时的
网络操作。S7 就是五步：TCP 拨号 → ISO-on-TCP 连接 → S7 PDU 协商 → 读 CPU 标识的
SZL 首帧 → SZL 续帧，每一步都会用 `handler.Timeout` 重设一次 socket deadline。

于是"每个候选最多花 `connectTimeout`"这种写法在最坏情况下会花掉
`候选数 × 阶段数 × connectTimeout` —— 而扫描选项表单是**用户填的**，把超时填大
一点就能让探测突破 `MaxBudgetMs`，撞上网关侧 RPC 超时并累积卡死计数
（见第 5 节）。正确做法是把每阶段/每候选的上限从**剩余预算**里反推：

```go
// s7drv/probe.go 的 ProbeTCP：每个候选最多花剩余预算的 1/5
wait := opt.connectWait
if share := remaining / s7HandshakeStages; share < wait {
    wait = share
}
```

这条不变量由 `s7drv/probe_test.go` 的 `TestProbeTCPRespectsBudget` 钉住
（用一个"收下连接就不说话"的监听器驱动，断言耗时落在预算内）。
**只在自己协议的握手确实是多阶段时才需要这么做** —— 一次请求就能判定的协议
（Modbus）把单个超时钳进预算就够了。

### 协议特有的连接参数：做成候选列表，并把命中的那组写回 `ProtocolJSON`

有的协议除了 host/port 之外还有"决定连上看不看得到设备"的参数。S7 的**机架/槽位**
是最典型的一个：S7-1200/1500 常见 `0/1`，S7-300/400 常见 `0/2`，其余组合只在带扩展
机架或通信处理器时出现；而它本质是 PLC 上**连接资源的地址**，填错的表现是
"102 端口开着却连不上"，错误信息里看不出任何线索。

处置方式（`s7drv/probe.go` 的 `ProbeTCP`）：

1. 表单项是一张**候选列表**（"0/1,0/2"），逐个尝试，而不是一个单值 ——
   单值会让用户在遇到冷门组合时完全扫不到，而且无从判断是网络问题还是参数问题。
2. **TCP 层失败就直接放弃该目标**（`errors.As(err, &net.OpError) && opErr.Op == "dial"`）：
   可达性与机架/槽位无关，而一个 /24 里绝大多数地址都走这条分支，
   不跳出会让整次扫描的耗时乘以候选数。**协议层失败才换下一组候选**。
3. **连上的那一组必须写回 `ProtocolJSON`**，否则「扫描 → 一键建设备」建出来的
   设备连不上，而两边都不报错（见第 8 节）。
4. 候选数要有上限（S7 是 4）：一份手滑的选项不该把单目标探测拖过预算。

没有这类参数的协议（Modbus）自然不需要它。

### FINS 的节点号：候选列表**可选**，因为默认值与采集逐字相同

FINS 也有一个"决定连上看不看得到设备"的参数：帧头里的 **DA1 目标节点号**。
但它的处置与 S7 的机架/槽位**不一样**，值得单独说明：

- 驱动在采集时用 `resolveDstNode(host)` **按设备 IP 末段推导**它，扫不到设备的
  最常见原因不是推导值错，而是根本没人在那个地址上；
- 因此扫描表单里的「节点号候选」是**可选项，留空即按 IP 末段推导** —— 与采集
  走同一条推导路径，于是"探测能通的设备，用同样的配置采集必定也能通"；
- **只有用户显式填了候选时**，命中的那个才写回 `ProtocolJSON`。这是 S7 那条
  "命中的那组必须写回"的**边界**：写回的语义是"记录一个探测才回答得出来的事实"，
  而推导值不是事实 —— 把它写进配置只是把一个能跟着 IP 变化自动更新的默认值冻死，
  将来给设备改个 IP 就静默失联。

同理，SA1 源节点号**一律不写回**：它由 `resolveSrcNode` 按"本机到目标的源 IP
末段"现算（真机核实 SA1=0 会被设备拒绝），填死一个值会让多网卡网关上探测与采集
的 SA1 分叉。

实现见 [../driversrc/fins/finsdrv/probe.go](../driversrc/fins/finsdrv/probe.go)
的 `scanOptions.candidates` 与 `buildFINSDevice`。

---

## 5. 与卡死检测的关系（必读）

`pluginhost` 的看门狗按「连续 3 次 RPC 超时 + 30s 无成功调用」判定"插件还活着
但卡死"，命中即 `Kill` 进程 —— 该插件下**所有协议、所有设备一起断**。
扫描与这套机制的交互有两条，都是硬约束：

1. **主动取消不计入超时计数，真超时计入。**

   `client.call` 的 `ctx.Done` 分支原先无条件 `noteTimeout()`。而取消一个扫描
   会同时掐掉在飞的全部 probe，一次取消就能顶满 streak；又因为判定还要求
   "很久没有成功调用"，在**一台还没建设备的新网关**上（运维第一件事就是扫描）
   该插件进程除 probe 外没有任何成功调用 —— 看门狗随即杀进程。

   现在只把 `context.DeadlineExceeded` 计入。这对既有调用方零行为变更：
   `Connect`/`Read`/`Ping`/`Close` 的超时全部由
   `context.WithTimeout(context.Background(), …)` 派生，`ctx.Err()` 只可能是
   `DeadlineExceeded`。只有扫描任务的 ctx 才可能被取消。

   回归测试：`pluginhost/client_test.go` 的 `TestCancelDoesNotCountAsTimeout`。

2. **单目标 RPC 的粒度不能更粗，也不能更细。**

   更粗（一次 RPC 带一批目标）会让一批的失败一起计入；更细（把取消也计入）
   就是上面那条。当前粒度加上"取消不计入"，使得**正常取消绝不会推进 streak**，
   而一个真正无视自己预算的 prober 仍会被正常发现。

---

## 6. 取消语义

- 网关侧：`Cancel` 立即把任务置为 `canceled` 并 `cancel()` 任务的 ctx。
  `client.call` 在 `ctx.Done` 上立刻返回，迟到的应答被 `dispatch` 静默丢弃。
- **已扫到的结果保留** —— 用户点了取消不代表那些结果没用。
- **被取消的目标不计入进度、也不计入错误明细**：那个目标并没有被扫过，
  把它算进 `scanned` 会让进度条假装扫完了，把它的错误报给用户更是误导
  （用户已经知道自己点了取消）。
- **取消 ≠ 插件立刻停止探测**：插件侧那次探测会跑到自己的预算耗尽才返回，
  那个结果已无人接收。这是无会话设计的直接后果，不是缺陷。
- 终态只能被设置一次：对已结束的任务再取消是幂等的成功，且不会把 `done` 改成
  `canceled`（否则用户点取消后看到"完成"会以为取消没生效）。

---

## 7. 目标模型

- 网段来源是**本机网卡枚举**，与 `discovery` 的广播目标共用同一套过滤规则
  （`discovery.eachUsableIPv4`：排除 loopback / 点对点 / APIPA / 掩码不在 [1,30]）。
  抽成公共函数是必需的：两处规则一旦分叉，只会表现为"某个网段扫不到设备"，
  不报任何错。
- **只允许扫描本机网段**。接口不接受任意 CIDR —— 那会把网关变成一台可以扫任何
  地址的通用端口扫描器。要扫别的网段，正确做法是把网关部署到那台网络上。
- 展开时只排除网络地址与广播地址。**本机地址照扫**：与网关同机的设备是真实存在
  的（网关与 PLC 仿真器同机调试、边缘一体机上另跑着服务），而静默跳过某一个地址
  恰恰会让用户从"没扫到"得出"那里没有设备"的错误结论，代价却只是多一次探测。
- `ScanTarget.LocalIP` 是本机在该网段上的地址，**多网卡机器上广播探测必须绑它作
  源地址**，否则广播只会走默认路由那一张网卡，一台设备也发现不了。

### TargetMode 决定展开成什么目标（2026-09 补齐）

目标形状**由插件声明的 `TargetMode` 决定**，<u>不是</u>一律逐主机展开：

| `TargetMode` | 产出的目标 |
|---|---|
| `unicast`（或留空） | 网段内逐主机地址，一个目标一台候选设备 |
| `broadcast` | 每个网段一个**子网广播地址**（`ip | ^mask`），一个目标可产出多台设备 |
| `both` | 两者都要，**广播目标排在最前** |

广播地址不是 `255.255.255.255`：受限广播只会在 FIB 选中的那张网卡出去，
并不比定向广播多覆盖任何东西。理由与 `discovery` 完全一致。掩码不在 `[1,30]` 的
网段（`/31`、`/32`）没有广播地址的概念，直接跳过。

**广播目标排在最前是有意的**：它一个就覆盖整个网段，排在两百多个逐主机目标之后，
一个大网段触顶时就可能永远轮不到它 —— 而它恰恰是最廉价、最可能先出结果的那一个。
分批续扫时它只出现在**第一批**（虚拟列表的最前面），后续批次从它之后接着取。

**曾经有个缺口**：`TargetMode` 写在契约里、由 `driversdk.Meta.Validate` 校验，
但引擎侧的展开逻辑从来没有读它 —— 于是"声明了广播的插件"拿到的仍是逐主机目标，
而两边都不报错，症状是 EtherNet/IP 那类协议扫得又慢又（在某些设备上）扫不到。
回归：`devicescan/targets_test.go` 的 `TestExpandTargetsBroadcast` 与
`engine_test.go` 的 `TestBroadcastSpecPlansBroadcastTarget`。

### 单批上限与分批续扫（`maxTargets` 的语义）

`deviceScan.maxTargets` 限制的是**一批**的目标数，不是整个扫描。超过时：

- 第一批只扫虚拟目标列表的前 `maxTargets` 个，快照带 `truncated=true`、
  `remainingTargets` = 尚未扫描的数量；
- 用户点「继续扫描」（`POST /deviceScan/continue`）后，引擎用**同一个 job**
  取 `[offset, offset+maxTargets)` 这一段继续扫：`jobId` 不变，已发现的设备、
  失败明细与去重表全部保留，因此扫完多批之后仍能一次性勾选建库
  （`createDevices` 的 `devices[index]` 下标全程有效）。

虚拟列表的次序固定（广播在前、之后逐主机，按网段列表顺序），offset 数的是
**目标序号**（广播也计入）。同一组网段 + 同一模式下的第 N 个目标永远相同 ——
这是分批不重不漏的前提，`expandTargetsPage` 的回归
（`TestExpandTargetsPageCoversWholeListOnce`）钉住它。

几条刻意的取舍：

- **取消后续扫从当前批的起点重扫**，而不是从下一批开始：取消时并发探测完成到
  哪一步无法确定（与目标顺序无关），重扫整批比静默漏掉一段安全；重复发现的设备
  由 `acceptDevice` 去重，代价只是多几次探测。
- **续扫沿用第一批的规划**（网段、端口、选项）：中途重新枚举网卡或换参数会让
  批次交界处错位。客户端只传 `jobId`。
- **上一批在飞探测未收尾时续扫返回"已有任务在运行"**：内部会先等旧的 worker
  退出（上限 2s），否则旧 worker 会往新批次的进度与结果里写。
- 单批上限的默认值保持 1024：它的初衷（防手滑把一次扫描拖成按小时计）没有变，
  变的是**用户可以用续扫把整个网段扫完**。

前端进度条与 `scanned` / `total` 是**本批**读数；`scannedTotal` 配
`requestedTargets` 给累计读数。把两个口径混成一个，续扫后已扫数会"归零"或
虚增。

### 结果去重：`host:port:unitId`

`TargetMode=both` 下同一台设备会被广播与它自己的单播目标**各报一次**，
因此引擎按 `host + port + unitId` 去重（`job.acceptDevice`），先到者胜。

键里**必须带 `unitId`**：同一 IP:端口 上的不同站号是协议定义的真实区分
（Modbus 的多站号探测、一台 PLC 的多个 FINS 节点），合并它们会把本该建成多台
设备的发现压成一台。同一 IP 上的不同端口同理（串口服务器后面挂着多台从站）。

不去重的症状是用户看到两份一模一样的设备行，而他无从判断这是"两台设备"还是
"重复"—— 只能靠人工比对列表。回归：`engine_test.go` 的
`TestDedupDropsSameDeviceFromTwoTargets` 与 `TestDedupKeepsDistinctUnits`。

---

## 8. 结果落地（扫描结果 → device 行）

`POST /deviceScan/createDevices` 把勾选的结果批量建成 `device` 行，
逐条失败不中断整批，返回契约复用 `vo.ExcelImportResultVO`
（与 Excel 批量导入、插件批量上传共用同一套前端组件）。

- **`protocolJson` 由服务端从任务快照里取，不采信客户端传入的值**：它决定网关
  去连哪个地址，是跨信任边界的数据，而快照本来就在本进程内。
- 即便取了也要过一道 `json.Valid` + 非空：插件给的东西不能假定合法 ——
  一份坏 JSON 写进 `device` 行之后，症状是"设备采不到值"，完全指不到根因。
- 逐行校验复用 `service.validateImportRow`，与 Excel 导入**逐字同规则**，
  否则两条路径会对同一份数据给出不同判定。
- **不自动 ping**：批量建 N 台设备各发一次探测，又是一次卡死计数风险。
  改为创建后引导用户手动点"测试连接"。

---

## 9. 边界与已知局限

| 现象 | 原因 / 处置 |
|------|------------|
| 直连的 Modbus TCP PLC 扫多个站号会扫出多台 | 原生 TCP PLC 多数**忽略** MBAP 里的单元标识，对任何站号都应答。默认只探站号 1，要扫范围必须显式写 |
| 设备标识（厂商/型号/版本）为空 | 多数 PLC 不支持 FC43/14 读设备标识。此时的判据是"一次寄存器读取有没有自洽的 Modbus 应答"，结果里会标注识别方式 |
| 提示"还有 N 个未扫描" | 目标数超过**单批**上限 `deviceScan.maxTargets`（默认 1024）。**这条提示不能省** —— 否则用户会从"没扫到设备"得出"这个网段没有设备"的错误结论。点「继续扫描」用同一个任务接着扫下一批，结果会累计 |
| 任务突然查不到 | 任务**不落库**，只在内存里保留到 `jobTTLMinutes` 之后；网关重启即丢。前端据此提示"请重新扫描"并停止轮询 |
| 同一时刻只能有一个扫描任务 | 设计如此：同时扫两个网段只会把并发压力翻倍，而它们本来也要排在同一个 worker 池上。第二个请求返回"已有扫描任务在运行" |
| 端口上开着别的服务被误报成设备 | 探测用 MBAP 自洽性判定（协议号 / 事务号回显 / 长度），HTTP 等其它协议会被挡掉。但一个恰好说 Modbus 的服务仍会被报出来 —— 那是准确的 |
| 确认某 IP 上有 S7 PLC 却扫不到 | 机架/槽位没命中：默认候选只有 `0/1`（1200/1500）与 `0/2`（300/400）。在扫描表单的「机架/槽位」里加上实际那组再扫 |
| S7 设备扫出来型号为空 | 设备不支持 SZL 0x001C（老固件常见），或回记录的长度不足以解析。**这不影响"发现了设备"** —— 结果里会标注识别方式 |
| 102 端口有服务却没被报成 S7 设备 | **刻意不上报**：HMI 触摸屏、Comfort Panel、各类 S7 网关都监听 102，但只有 PLC 会完成 S7 PDU 协商。把"有服务应答"当成设备会引入一批假的"PLC" |
| 确认某 IP 上有欧姆龙 PLC 却扫不到（FINS） | 该 PLC 的 FINS 节点号不是 IP 末段。驱动与探测都按 IP 末段推导 DA1，此时要在扫描表单的「节点号候选」里填上实际的节点号再扫 —— 填了以后命中的那个会写进新建设备的配置 |
| FINS 设备回了结束码错误却被报成设备 | **刻意如此**：结束码非 0 意味着设备**应答了**，只是拒绝了这次读取（区域不存在、单元号不对）。把它当成"没扫到"，会让一台真实存在的 PLC 因为一句不认识的结束码而消失 |
| FINS 设备扫出来型号为空 | 设备不支持控制器数据读取（0x0501），或回的载荷短于型号字段。**这不影响"发现了设备"** |
| 确认某 IP 上有三菱 PLC 却扫不到（MC） | 端口默认 5007，但现场确有把它改到别的端口的情况，扫描表单的「端口」要填实际值。另一类原因是**该 CPU 不响应以太网**：串口模块（C24）或 FX 系列走的是 4C 帧，网络上根本没有它的地址，只能扫它前面那台串口服务器 —— 而串口服务器不会回出 MC 形状的应答 |
| MC 设备扫出来型号/厂商为空 | **必然为空，这是设计如此**：MC 没有任何设备标识读取手段，本插件的驱动只实现了批量读（0x0401），仓库里也没有第二条可依据的 SLMP 命令实现。填 "Mitsubishi" 是拿协议名冒充设备自述，而 SLMP 并非只有三菱在用 |
| MC 设备回了结束码错误却被报成设备 | **刻意如此**：结束码非 0 意味着设备**应答了**，只是拒绝了这次读取（PLC 忙、单元号不对…）。把它当成"没扫到"，会让一台真实存在的 PLC 因为一句不认识的结束码而消失。结果里会标注识别方式是"回了结束码错误" |
| MC 串口设备无法被扫描 | 串口协议**不得**声明设备扫描（`Meta.Validate` 明文禁止：目标模型是 host/port，表达不了 comPort）。这是契约限制，不是漏配 —— 串口的「总线上有哪些站号」是另一个功能 |
| 广播扫到了设备、逐主机却没扫到（EtherNet/IP） | 正常：单播的 `ListIdentity` 要设备愿意逐个应答，部分设备只答广播。两条路的结果由引擎去重合并 |
| 扫出来的 EtherNet/IP 设备比实际少 | 本插件的扫描**只上报欧姆龙（厂商号 47）** 的设备 —— 别的厂商的 EtherNet/IP 设备用 `Omron.CIP` 采不了，报出来只会让人建出永远采不到值的设备。它们在日志里有计数 |
| 同一台 EtherNet/IP PLC 在欧姆龙与罗克韦尔两个协议下只出现一次 | 正常：两个插件各按自己的厂商号过滤（47 / 1），而每个插件只答自己那一半。一台设备只会命中其中一个 |
| 一台罗克韦尔 PLC 扫不出来（`Rockwell.CIP`） | 与欧姆龙那格对称：本插件的扫描只上报**厂商号 1（Allen-Bradley）** 的设备。它在日志里有计数 |
| 禁广播的受管网络上扫不到 EtherNet/IP 设备 | 该协议声明的是 `TargetMode=both`，逐主机那一轮会兜底。若两轮都扫不到，是网络层面的封锁（ACL / 端口过滤），不是参数问题 |
| 确认某 IP 上有 OPC UA 服务器却扫不到 | 端口默认 4840，但现场确有改到别的端口的情况，扫描表单的「端口」要填实际值。另一类原因是该服务器**既拒绝无安全策略（None）的 SecureChannel、又不按协议回一个错误**（直接断链）：那时我们既读不到端点表、也拿不到任何能证明"它是 OPC UA"的状态码，只能按"不是我们的设备"处理 |
| OPC UA 服务器扫出来型号/厂商为空 | **必然为空，这是设计如此**：`GetEndpoints` 是发现服务、不需要会话，服务器不报厂商/型号/固件（那要走 `ServerStatus`，代价是建一次会话）。`Name` 取的是服务器自述的 `ApplicationName` |
| OPC UA 设备扫出来却连不上 | 结果里那条「提示」会说明原因，最常见的是**只提供加密端点**（无 None 安全策略）：网关当前未实现客户端证书，选 Sign/SignAndEncrypt 必然失败。其次是不接受匿名登录（提示会要求填用户名密码）。**探测本身不校验认证** —— 用户名密码错的服务器照样会被列出来，那要到采集时才暴露 |
| OPC UA 服务器被列出来了，却从没读过它的任何数据 | **刻意如此**：判据是"读回一份自洽的端点表"，不是"建会话成功"。与 FINS/MC 的"结束码错误也算设备"同一条取舍 —— **拒绝应答也证明对面是 OPC UA** |

| DL/T 645 的两个协议都扫不到设备 | **刻意不做，不是漏配**。`DLT645.Serial` 按契约不得声明 `Discovery`（目标模型是 host/port，表达不了 comPort）；`DLT645.TCP` 虽然可以，但它连的是**串口服务器 / DTU**，与 `Omron.FINS.HostLinkTCP` 同形（FINS 也刻意不声明）。DL/T 645-2007 确有广播「读通信地址」(控制码 13H → 应答 93H，应答帧地址域回填真实表号) 能枚举**一条 RS-485 总线上的表**，但那回答的是"这条总线上挂着哪些表"，不是"网段里有哪些主机"——是另一个功能（对应"串口上总线上有哪些站号"那一格），且 1997 版的广播语义无依据、无真机可验证。按"不做而不是猜着做"：留 nil，由 `driversrc/dlt645/main_test.go` 的 `TestNoDeviceScan` 钉住。**它的点位扫描是两个协议都声明的**，见 [point-discovery.md](point-discovery.md) §3 第五种形态 |

**三分法的判据**（OPC UA 特有。写在这里是因为它是本功能里唯一一处
"不是 OPC UA / 是 OPC UA 但拒绝了我们的安全参数 / 是 OPC UA"的三分分支）：

拨号与 SecureChannel 的失败里，`*uacp.Error` 与**服务级 `ua.StatusCode`**
只有 OPC UA 对端发得出来，因此都算命中；其中安全策略与证书那一族状态码
（`BadSecurityPolicyRejected` / `BadSecurityModeRejected` / `BadCertificate*` 等）
另走"照样列出 + `Extra` 告警"那一支；而 `StatusBadTimeout` 是 gopcua **本地合成**的
（等不到应答时由计时器发出），既可能是"对面不应答"也可能是"对面静默拒绝了"，
**两者无从区分**，一律按"不是我们的设备"处理 —— 误报一台连不上的幽灵设备
比漏报更费用户的时间。回归：`opcuadrv/probe_test.go` 的 `TestClassifyProbeErr`。

**合规提醒**：扫描会主动向整个网段发起 TCP 连接（广播类协议还会发 UDP 广播），
某些生产网络对此有明确的禁止条款。`deviceScan.enabled` 是为此准备的开关。

---

## 10. 代码结构

| 文件 | 职责 |
|------|------|
| [../driver/discovery.go](../driver/discovery.go) | **契约**：`DiscoverySpec` / `ScanTarget` / `DiscoveredDevice` / `TargetMode` |
| [../sdk/driversdk/rpc.go](../sdk/driversdk/rpc.go) | `ProbeParams` / `ProbeReply`（过线载荷） |
| [../sdk/driversdk/serve.go](../sdk/driversdk/serve.go) | `Probers` / `ProbeFunc` / `ProbeRequest`、启动期校验、`handleProbe`、预算钳制 |
| [../pluginhost/discovery.go](../pluginhost/discovery.go) | 驱动侧适配：`DiscoverySpecs`（现算，不缓存）/ `ProbeTargets`（RPC 超时推导） |
| [../devicescan/](../devicescan/) | 任务引擎：目标规划（`targets.go`）、任务与快照（`job.go`）、并发/取消/过期（`engine.go`） |
| [../discovery/netif.go](../discovery/netif.go) | `eachUsableIPv4`（两处共用的网卡过滤）+ `LocalSubnets` |
| [../service/deviceScanService.go](../service/deviceScanService.go) | 错误码映射、VO 转换、批量建设备 |
| [../controller/deviceScanController.go](../controller/deviceScanController.go) | `/deviceScan/*` |

**插件侧的参考实现**（四种形态，按自己协议的样子挑一个看）：

| 文件 | 适用形态 |
|---|---|
| [../driversrc/modbus/modbusdrv/probe.go](../driversrc/modbus/modbusdrv/probe.go) | **一次请求就能判定**：自己发协议帧，判据是应答的自洽性。不需要建立会话 |
| [../driversrc/s7/s7drv/probe.go](../driversrc/s7/s7drv/probe.go) | **必须先建立会话才知道对面是不是我的设备**：复用驱动的连接握手；含候选参数列表与"TCP 层失败即放弃"的取舍 |
| [../driversrc/fins/finsdrv/probe.go](../driversrc/fins/finsdrv/probe.go) | **复用驱动自己的传输层**（`newTransport` + `client.Read`），完全不另写裸帧；含"结束码错误也算设备"与"候选列表可选"两处取舍。协议本身没有设备标识的，看这一份 |
| [../driversrc/mitsubishi/mitsubishidrv/probe.go](../driversrc/mitsubishi/mitsubishidrv/probe.go) | **同上，且没有候选参数**：MC 3E 帧的网络号/PC 号/I-O 号/站号在本驱动里是写死的常量，既不是 `protocol_json` 的字段、也不随目标变化，所以一次探测就是一次拨号加一次读取。声明最简的一档（单协议、单目标模式、表单只有一栏超时） |
| [../driversrc/omroncip/omroncipdrv/probe.go](../driversrc/omroncip/omroncipdrv/probe.go) | **广播型发现**：一条数据报拿回整个网段（`TargetModeBoth`），一个目标产出多台设备；含安静窗口、设备数上限与厂商过滤。帧的解析在 [cipcore/listidentity.go](../driversrc/omroncip/cipcore/listidentity.go)（罗克韦尔插件有一份自己的副本，见下一行） |
| [../driversrc/rockwellcip/rockwellcipdrv/probe.go](../driversrc/rockwellcip/rockwellcipdrv/probe.go) | **同上，逐段同形**——同一个 EtherNet/IP 命令、同一套帧，差别只有厂商过滤（1 = Allen-Bradley）。它的帧编解码在**本插件自己的** [cipcore](../driversrc/rockwellcip/cipcore/)（欧姆龙那份的裁剪副本），**两个插件不共享源码**：共用会让双方构建互相牵连，而插件要能独立改名/删除/拆仓库。代价（两份可能漂移）由"两边都用同一套独立实现的假应答驱动解析器测试"兜底 |
| [../driversrc/opcua/opcuadrv/probe.go](../driversrc/opcua/opcuadrv/probe.go) | **调协议自带的发现服务，不建会话**：`GetEndpoints` 是 OPC UA 规范允许在"只建立了 SecureChannel、尚未 `CreateSession`"时调用的服务，所以一次探测是"拨号 → OpenSecureChannel → GetEndpoints → 关闭"，**完全不校验认证**（这一点必须写进 `Description`）。含一条别处没有的**三分法**：不是 OPC UA / 是 OPC UA 但拒绝了我们的安全参数（照样列出 + 告警）/ 是 OPC UA。协议自带发现服务、或需要把"设备存在但连不上"与"没有设备"分开的，看这一份 |

**不建扫描能力缓存表**：热升级只替换 `lp.meta`，而 `upgrade.go` 的
`registrationSig` 只含 scope / protocols / types / typeMap，**不含 Discovery** ——
一个只改了 Discovery 的新二进制升级后不会触发重注册。若把能力另存一张表，
它会停在旧值上，症状是"插件明明改了扫描配置、前端却一直显示旧的"，
而两边日志都写着成功。遍历 `m.plugins` 读 `lp.meta` 则一致性自动成立。
回归测试：`pluginhost/discovery_test.go` 的 `TestDiscoverySpecsRefreshAfterHotUpgrade`。

---

## 11. HTTP 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/deviceScan/capabilities` | 可扫描的协议（含协议行 id 与扫描表单）、本机网段、开关、目标上限。三合一，因为进页面时都要拿到 |
| POST | `/deviceScan/start` | 发起扫描，**登记任务即返回**（`jobId`）。一个 /24 是十几秒到几十秒的量级，同步等会把停机拖到超时。超过单批上限时只扫第一批，快照 `truncated=true` |
| POST | `/deviceScan/continue` | 续扫**同一任务**的下一批（只传 `jobId`）。任务 id 不变、已发现的结果保留；已是最后一批时幂等返回当前快照 |
| GET | `/deviceScan/status?jobId=&withDevices=` | 轮询进度与结果。`withDevices=false` 只回进度与计数（见下） |
| POST | `/deviceScan/cancel` | 取消；已结束的任务取消是幂等的成功 |
| POST | `/deviceScan/createDevices` | 勾选的结果批量建成设备 |

错误码见 `enums/resultEnum.go` 的 `40021`~`40026`（设备域）：
功能未启用 / 已有任务在运行 / 任务不存在或已过期 / 该协议不支持扫描 /
没有可扫描的目标 / 扫描参数不合法。

**轮询注意**：`status` 在前端是 `silent` 请求（轮询失败不该每 800ms 弹一次提示），
拦截器因此返回的是**响应信封**而不是解包后的 `data`，调用方要自己判 `code`。

**轻量轮询**（`withDevices=false`）：结果清单只增不减，一个扫到几百台的任务每
800ms 回传整份清单，而绝大多数拍子并没有新设备；带清单的快照还要在 `Engine.mu`
下浅拷贝整个切片，而那把锁正是探测 worker 追加结果用的。因此轮询默认只取
计数与进度，前端拿 `found` 比对自己已经持有的那份，发现数变了再补一次
`withDevices=true` 的请求。

这条契约靠**`devices` 字段的 null 语义**成立，两侧都不能破坏：

- 轻量快照里 `devices`/`errors` 是 **`null`**，完整快照里**一定是数组**（空也是
  `[]`）。客户端据"是不是数组"决定保留还是清空自己持有的那份清单；
- 计数（`found` / `scanned` / `scannedTotal`）与完整快照**同源**
  （都是 `len(j.devices)` / 本批计数 / 累计计数），两份计数一旦各算一套，
  补请求的时机就会漏 —— 症状是"扫到的东西一直不出现"。

回归测试：`devicescan` 的 `TestProgressOmitsResults`、`service` 的
`TestScanProgressOmitsDevices`。

---

## 12. 配置

```yaml
deviceScan:
  enabled: true             # 出厂 true；关掉后 /deviceScan/* 全部返回"功能未启用"
  maxConcurrentProbes: 8    # 同时在飞的目标探测数（上限 32）
  maxTargets: 1024          # 单批扫描的目标数上限（上限 65536），超出即截断并如实上报；剩余地址可续扫
  jobTTLMinutes: 10         # 终态任务的保留时长；任务不落库，重启即丢
```

各项都在**每次扫描时实时生效**。探测超时**不在本段** —— 它是每协议的属性，
由插件声明（见第 4 节）。

---

## 13. 给插件作者的最小清单

1. 在 `Meta.Protocols[]` 的那一项上填 `Discovery`（`Title` / `DefaultPort` /
   `MaxBudgetMs` / 扫描表单）。
2. 在 `driversdk.Main` 的第三个参数（`driversdk.Capabilities`）的 `Probers`
   字段里给出 `Probers{协议名: 探测函数}`：

   ```go
   driversdk.Main(factories(), meta(), driversdk.Capabilities{
       Probers: probers(),
   })
   ```
3. 探测函数要守住五条：`(nil, nil)` 不是错误 / error 只用于探测本身失败 /
   必须在 `req.Budget` 内返回 / 不分配会话 / 不 panic。
4. 探测结果里的 `ProtocolJSON` 要能被**你自己的配置解析器**吃下 ——
   键名与配置结构的 json tag 逐一对上，否则建出来的设备连不上。
   建议加一条测试钉住（`driversrc/modbus/modbusdrv/probe_test.go` 的
   `TestProbeTCPFindsDeviceWithDeviceID` 里直接调 `ParseModbusTcpConfig` 就是这个用意）。

完整示例见 [../../docs/driver-plugins.md](../../docs/driver-plugins.md)。
