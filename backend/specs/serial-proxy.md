# 网关串口代理（跨插件共用物理口）

> 适用版本：SDK ≥ v0.5.0、官方驱动 ≥ v0.5.0、网关 ≥ v0.8.4（未发行）。
> 相关：`driversdk/serialbus`（进程内共享）、`driversdk/serialproxy`（插件侧）、
> `pluginhost/serialhost.go`（网关侧）、`collector/serialbus.go`（启用策略）。

## 1. 为什么需要它

一条 RS-485 上挂多台设备是常态，而这些设备可能分属**不同协议插件**
（例如 DLT645.Serial 与 ModBus.RTU）。插件是独立进程，同一个物理串口各开一个 fd
会同时踩三个坑：

1. 同一个 tty 出现两个读端，内核把每个字节只交给其中一个 fd —— A 的应答被 B 读走，
   A 超时或收到残缺帧（日志里的「已收 7/9 字节」）；
2. 非阻塞 fd + select 让两个读端同时被唤醒，竞争失败方拿到 `EAGAIN`
   （`resource temporarily unavailable`），被判为链路断开；
3. Linux 的 termios 是 tty 级状态，任一方 `Close` 恢复打开前的备份，会改写另一个
   句柄所在端口的工作模式，双方交替"连接已关闭 / 已连接"。

进程内的 `driversdk/serialbus` 修不了这一类（fd 属于不同进程）。终局是**一个物理
端口只有一个 fd**，由网关主进程持有，插件经 RPC 收发字节。

## 2. 拓扑与所有权

```
                    ┌────────────── 网关主进程 ──────────────┐
  iot-driver-dlt645 │  collector: deviceGate（按端口串行）    │
        │    serial.* RPC（JSON，反向请求）                 │
        └──────────►  pluginhost.client ⇄ serialHost         │
  iot-driver-modbus │        │                              │
        └──────────►         └─ 1 个 fd + 1 把跨进程总线锁 ──►／dev/ttyUSB0
                    └────────────────────────────────────────┘
```

- 物理句柄、参数冲突判定、总线锁、超时全部在网关侧；插件只看到一条字节流。
- 每个插件进程的句柄以 `*client`（插件进程连接）为属主。进程退出、崩溃、被强杀时
  `client.fail` 调用 `serialHost.closeOwner`，释放句柄与锁 —— 不允许出现"锁被一个
  不存在的会话占死"。
- 同端口、参数不同的请求明确报错（同一条总线上的设备必须参数一致），不会悄悄再开
  一个 fd。

## 3. 线协议

反向请求（插件 → 网关）复用 `pluginwire` 信封；**请求带 Method，应答不带**，
因此两个方向的 ID 空间互不干扰。

| 方法 | 参数 | 应答 | 语义 |
|---|---|---|---|
| `serial.open` | `{port, baudRate, dataBits, stopBits, parity}` | `{id}` | 按「归一端口 + 参数」共享 fd；返回本会话的句柄 id |
| `serial.lock` | `{id, timeoutMs}` | — | 取得跨进程总线锁；超时报错 |
| `serial.unlock` | `{id}` | — | 释放；非持锁者报错 |
| `serial.read` | `{id, max, timeoutMs}` | `{data, timedOut}` | 最多等 `timeoutMs`，**一有字节就返回**；无字节且超时 → `timedOut` |
| `serial.write` | `{id, data}` | `{n}` | 必须持锁 |
| `serial.close` | `{id}` | — | 引用计数递减；最后一个引用关闭 fd（并释放仍持有的锁） |

字节流用 JSON 的 base64（`[]byte`）；串口帧很小，代理链路的瓶颈在物理层。

### 读语义与超时

- goburrow/serial 的读超时在打开时固定，因此网关以 **50ms 切片**反复读，直到调用方
  给的截止时间。这样既能"有字节立即返回"（驱动按 `ReadFull`/截止时间循环的语义不变），
  又不会为一次 5s 的读开一个无法中断的阻塞调用。
- 插件侧 `serialproxy.Conn.Read` 把 `timedOut` 翻译成 `serialproxy.ErrTimeout`；
  驱动必须把它与 `serial.ErrTimeout` 同等看待（否则一台设备离线会被误判成链路故障，
  进而判死共享句柄、把整条总线拖进重连）。四个官方驱动已处理。

## 4. 启用条件（collector）

建采集任务时（`collector.configureSerialBusSharing`）：

1. 按 `driver.CanonicalSerialPort(driver.ExtractSerialPort(...))` 给串口独占
   （`SerialExclusive`）设备分组；
2. 一组 < 2 台设备：不处理；
3. 只有一种协议：INFO（进程内 `serialbus` 已共享句柄），**不启用代理**；
4. 多种协议且**全部**驱动都实现 `driver.SerialProxyCapable`（返回 true）与
   `driver.SerialProxySetter`：对每台设备 `SetSerialProxy(true)`，INFO；
5. 混入任何一个不支持代理的插件：保持直连，WARN（分口或 DTU）。

只让一半设备走代理更糟：网关持有一个 fd、旧插件再开一个，问题原样复现且更难排查。

## 5. 插件侧

- `driversdk.Protocol.SerialProxy: true` 声明能力；启动时校验对应工厂返回的驱动
  实现了 `driver.SerialTransportProvider`，否则插件启动即失败。
- `ConnectParams.SerialProxy` 置位时，SDK 调用 `SetSerialTransport(opener)` 注入
  `serialproxy.Open`。驱动把 opener 用在串口传输上（本地直连时 opener 为 nil）。
- `Ping` / `DiscoverPoints` 的请求也带 `serialProxy` 字段，插件的临时实例看到它会
  走代理。**网关目前只在采集会话（`Connect`）上置位** —— Ping/点扫的临时实例没有
  设备上下文，无法判断该端口是否已被代理；它们优先复用采集会话，复用不到时的行为
  见 §6。
- `serialbus.Handle` 会把资源上的 `Lock/Unlock` 串进自己的总线锁（顺序：进程内锁 →
  跨进程锁），驱动代码不需要区分本地与代理。

## 6. 已知边界

- **临时实例的代理标志**：`Ping` / 点位扫描在插件侧优先复用采集会话；命中时天然是
  代理连接。只有在"没有可复用会话"时才建临时实例，而网关侧的临时驱动（由 service /
  pointdiscovery 直接创建）没有设备上下文，不会置位 `SerialProxy`。此时若端口正被
  代理占用（采集任务在跑，例如设备被停用后重试），测试连接/点扫会如实报端口被占用，
  而不会去抢第二个 fd。功能上安全（不会损坏采集），体验上是一条已知限制；要消除它，
  需要把"该端口是否由网关代理"从采集任务传到 service 层，属于后续工作。
- **跨主机**：代理只在网关本进程与其插件进程之间；DTU / 串口服务器不在范围内。
- **性能**：每笔事务多 1 次 `serial.lock` + 每次读 1 次 `serial.read` 的 RPC。
  2400–115200 bps 的物理链路是瓶颈，本地管道开销可忽略；长帧读取靠 50ms 切片循环，
  不会按 RPC 次数放大延迟。
- **不支持的组合**：没有声明 `SerialProxy` 的第三方插件仍按旧方式直连；网关用 WARN
  说明该端口上的冲突不会被消除。

## 7. 测试锚点

- SDK：`driversdk/serialproxy`（读写/超时/粘性错误/锁转发）、`driversdk/serve_serial_test.go`
  （反向 RPC 编解码、连接断开时立即返回）。
- 网关：`pluginhost/serialhost_test.go`（共享 fd、参数冲突、跨属主锁、读超时、
  属主清理、所有权）与 `TestSerialProxyEndToEnd`（网关 client ⇄ 插件 Serve ⇄ 驱动
  opener ⇄ serial.* RPC ⇄ serialHost ⇄ 物理端口替身 全链路）。
- 驱动：`dlt645` 的注入 opener 测试、`modbus` 的代理 Transporter 测试（CRC 应答解码、
  从站号、锁转发）、FINS/MC 的同端口共享测试。
- 采集：`collector/serialbus_test.go` 的 `TestConfigureSerialBusSharingEnablesProxy`
  （全部支持才启用 / 混合不启用 / 同协议不启用）。
