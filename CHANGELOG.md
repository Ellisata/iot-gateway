# Changelog

本项目遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

All notable changes to this project are documented in this file (Keep a Changelog format, SemVer versioning).

## [Unreleased]

### Added

- 新增欧姆龙 FINS 本地串口协议 `Omron.FINS.Serial`：驱动与 Host Link over serial 传输层早已实现并注册，但 `iot_protocol` 里一直缺这一条，界面上建不出这种设备。至此 FINS 的四种传输（UDP / TCP / 本地串口 / HostLinkTCP）在设备页都可配置。
  Added the Omron FINS local-serial protocol `Omron.FINS.Serial`: the driver and its Host Link over serial transport already existed and were registered, but the `iot_protocol` row was missing, so the protocol could not be configured from the UI. All four FINS transports (UDP / TCP / local serial / HostLinkTCP) are now selectable.
- 「测试连接」不再与运行中的采集任务抢串口：新增独占串口驱动登记表（`DLT645.Serial` / `ModBus.RTU` / `Mitsubishi.MC.Serial` / `Omron.FINS.Serial`），采集引擎在任务启停时登记与注销自己的驱动实例，`driver.PingDevice` 据此复用引擎已持有的那条串口连接，而不是再开一个句柄。串口打开失败时错误里会点名端口被哪台设备的采集任务占用，替换掉 Windows 那句毫无指向性的 `Access is denied`，并透传到「测试连接」的响应消息里。
  "Test connection" no longer fights a running collection task for the serial port: a registry of exclusive-serial drivers (`DLT645.Serial` / `ModBus.RTU` / `Mitsubishi.MC.Serial` / `Omron.FINS.Serial`) lets the collector register/unregister its driver instances, and `driver.PingDevice` reuses the connection the engine already holds instead of opening a second handle. When a serial open does fail, the error now names the device whose collection task owns the port — replacing Windows' opaque `Access is denied` — and surfaces it in the test-connection response.

### Changed

- DL/T 645 驱动传输层性能优化：TCP 每秒帧数提升约 70 倍（单帧 1.5ms → 21µs，5000 点一轮）。发送前清缓冲改为先探测接收缓冲、空则零成本返回，不再为每帧空等 1ms；TCP 默认帧间延时由 30ms 改为 0（透传链路的换向由串口服务器/DTU 处理，该等待对 TCP 无意义，串口仍为 30ms）；串口侧改为仅在上一轮未干净收尾时才真正清缓冲，省下 20ms 空读。另：`maxDIsPerRead` 出厂默认由 1 改为 12（规范上限），帧数由「一帧一点」降为 `⌈点数/12⌉`。
  DL/T 645 transport performance: ~70× more frames per second on TCP (1.5ms → 21µs per frame, 5000-point round). The pre-send drain now probes the receive buffer and returns immediately when empty instead of idling 1ms per frame; the TCP inter-frame delay now defaults to 0 (turnaround on a transparent link is the serial server's/DTU's job — serial keeps 30ms); serial only drains when the previous round did not end cleanly, saving a 20ms idle read. Also: `maxDIsPerRead` now defaults to 12 (the spec limit) rather than 1, cutting frames from one-per-point to `⌈points/12⌉`.

### Fixed

- `ModBus.RTU` 与 `Mitsubishi.MC.Serial` 的「设备端口」由固定选项的下拉框改为文本框：原先只有 COM1 与 /dev/ttyUSB0 两个选项，而前端并未给 select 开 `filterable`/`allow-create`（已核对 `web/dist` 产物），因此端口号为 COM7、`/dev/ttyUSB1` 等其它值的设备**根本无法配置**。端口的可选值集合由现场实际接了什么决定，网关侧枚举不出来，现与 `DLT645.Serial` / `Omron.FINS.Serial` 的既有做法一致。
  The "device port" field of `ModBus.RTU` and `Mitsubishi.MC.Serial` is now a text box instead of a fixed-option dropdown: it offered only COM1 and /dev/ttyUSB0, and the frontend does not enable `filterable`/`allow-create` on selects (verified against the `web/dist` bundle), so a device on COM7 or `/dev/ttyUSB1` simply could not be configured. The set of valid port names depends on what is physically attached to the gateway and cannot be enumerated by the gateway itself — this now matches what `DLT645.Serial` / `Omron.FINS.Serial` already did.
- ModBus.RTU 的串口链路参数与单帧超时此前**根本无法配置**：表单只有端口与站号，解析器（`modbusRTUConfigRaw`）也从未读取 `baudRate`/`dataBits`/`stopBits`/`parity`/`timeoutMs`，五项永远停在默认的 9600/8/1/N/5000，从站只要不是这个组合就必然通信不上，而界面上无从改起。现已补齐表单字段并如实解析（非法值回退默认，不会让整条配置解析失败）；这四项链路参数同时参与「测试连接」的连接复用判定，此前该判定拿到的也一直是默认值。存量设备的 `protocol_json` 里本就没有这几个字段，行为不变。
  ModBus.RTU's serial link parameters and frame timeout could not be configured at all: the form exposed only the port and slave id, and the parser (`modbusRTUConfigRaw`) never read `baudRate`/`dataBits`/`stopBits`/`parity`/`timeoutMs`, so the five always stayed at the 9600/8/1/N/5000 defaults — any slave not using that combination could not communicate, with no way to change it from the UI. The form fields and the parsing are now in place (invalid values fall back to defaults rather than failing the whole config), and the four link parameters also feed the connection-reuse check used by "test connection", which until now always saw the defaults. Existing devices' `protocol_json` never contained these fields, so their behaviour is unchanged.
- 压力测试工具 `-dlt645-batch 1` 不再被静默忽略：此前仅当值 >1 才注入配置，为 1 时落回驱动默认，无法复现「一个数据标识一次往返」的测量口径。
  The load-test tool no longer silently ignores `-dlt645-batch 1`: the value was only injected when >1, so 1 fell back to the driver default and could not reproduce the one-identifier-per-round-trip measurement.
- 移除 Mitsubishi.MC.TCP / MC.Serial 与 Omron.FINS.UDP / TCP / HostLinkTCP 表单里的 `transport` 字段：传输层由协议注册名固定，驱动在 `Connect`/`Ping` 里用 `cfg.Transport = d.transport` 硬覆盖，界面上让用户选 UDP/TCP/HOSTLINK 只会误导（选了不生效）。存量设备 `protocol_json` 里残留的该字段无害，驱动照旧忽略。顺带修掉 MC.Serial 该字段 label/value 写反（`{"label":"SERIAL","value":"串口"}`）导致的脏数据。
  Removed the `transport` field from the Mitsubishi.MC.TCP / MC.Serial and Omron.FINS.UDP / TCP / HostLinkTCP forms: the transport is fixed by the registered protocol name — the driver overwrites it with `cfg.Transport = d.transport` in `Connect`/`Ping` — so letting the user pick UDP/TCP/HOSTLINK was misleading (the choice had no effect). Leftover values in existing devices' `protocol_json` are harmless and still ignored. This also retires the reversed label/value pair (`{"label":"SERIAL","value":"串口"}`) that wrote `"transport":"串口"` into saved configs.
- 三菱 MC 串口与欧姆龙 FINS 串口在 Windows 上无法从一次读超时中恢复：`Connect` 先打开新串口再关旧的，而读失败只标记断连、不释放句柄，于是第二次打开必然 `Access is denied`，此后每次重连都失败且旧句柄一直挂着。改为照 DLT645 / ModBus.RTU 的既有正确顺序：先释放旧句柄，再打开新的。
  Mitsubishi MC serial and Omron FINS serial could not recover from a single read timeout on Windows: `Connect` opened the new port before closing the old one, and a failed read only flagged the link as broken without releasing the handle — so the second open failed with `Access is denied` and every reconnect failed thereafter while the stale handle stayed open. The order now matches the existing correct DLT645 / ModBus.RTU behaviour: release the old handle first, then open the new one.
- ModBus.RTU 的连接复用判定此前只比较串口号，波特率、数据位、从站号不同也会复用：测试从站号不同的设备时，请求会打到总线上的相邻从站并被正常应答，测试连接返回成功而目标从站根本没被问到（假绿）。改为逐项比较全部链路参数。
  The ModBus.RTU connection-reuse check compared only the port name, so a differing baud rate, data bits or slave id still reused the connection: testing a device with a different slave id sent the request to a neighbouring slave on the bus, which answered normally — the test reported success while the target slave was never asked. All link parameters are now compared.
- 欧姆龙 FINS 的连接复用判定不比较串口链路参数（波特率/数据位/停止位/校验位），9600/N 的连接能"验证"19200/E 的配置；Mitsubishi MC 与 FINS 的复用分支在锁外读配置，与重连构成数据竞争。均已修正。
  Omron FINS' reuse check ignored serial link parameters, so a 9600/N connection could "verify" a 19200/E configuration; Mitsubishi MC and FINS read their config outside the lock in the reuse path, racing with reconnect. Both fixed.
- 串口连接的 `IsConnected` 改为原子读：它现在会被采集协程之外调用方（「测试连接」）读到，而写它的是采集协程里的读失败路径。
  A serial connection's `IsConnected` now uses an atomic read: it is read by callers outside the polling goroutine ("test connection") while the polling goroutine writes it on a failed read.

## [0.2.1] - 2026-09-20

### Changed

- JWT 签名密钥不再使用内置固定值：未配置时每次启动随机生成临时密钥；生产环境建议通过 `IOT_GATEWAY_JWT_SECRET` 环境变量或 `jwt.secret` 配置固定密钥。
  The JWT signing secret no longer ships with a built-in fixed value: an ephemeral random secret is generated per start when unset; set `IOT_GATEWAY_JWT_SECRET` or `jwt.secret` in production to keep sessions across restarts.

### Added

- 新增 DL/T 645 电能表协议驱动（`DLT645.Serial` / `DLT645.TCP`）：经 RS-485 串口或串口服务器（DTU）采集多功能电能表的电压、电流、功率、电能等数据；支持 DL/T 645-2007 / 1997 两版数据标识、逐标识寻址与批量打包读（`maxDIsPerRead`）、内置数据标识字典与校验和校验，并参与断联报警。
  Added the DL/T 645 electricity-meter driver (`DLT645.Serial` / `DLT645.TCP`): reads voltage, current, power and energy from multifunction meters over RS-485 or a serial server (DTU), supporting both the DL/T 645-2007 and 1997 identifier sets, per-identifier addressing with batch packing (`maxDIsPerRead`), a built-in data-identifier dictionary and checksum verification, and disconnection alarms.
- 开放接口文档按管理端用户语言展示：新增英文版文档，`/openApiSecret/doc` 接口根据用户语言（`area` 头）返回中文或英文版本。
  Open API documentation now follows the admin user's language: an English version was added, and `/openApiSecret/doc` returns the Chinese or English version based on the user's language (`area` header).

[Unreleased]: https://gitee.com/wang4856304/iot-gateway/compare/v0.2.1...HEAD
[0.2.1]: https://gitee.com/wang4856304/iot-gateway/compare/v0.1.0...v0.2.1
