# iot-gateway · 工业 IoT 数据网关

[English](README.en.md) | 简体中文

**iot-gateway** 是一个开箱即用的工业物联网数据采集网关：定时轮询 PLC / 工业设备，将采集数据推送至 MQTT、TDEngine、InfluxDB 等下游通道，并提供内嵌的 Web 管理控制台。整体以 **单二进制** 形式部署到网关设备或工控机，无需外部数据库。

<!-- 管理控制台概览：动图是唯一能在 Gitee/GitHub 上直接动起来的形式 ——
     <video> 标签会被 Markdown 清洗器丢弃。
     动图是视频的精华剪辑，依次走过登录、大盘、协议、设备对象、动态表单、
     通道、日志、开放接口文档。
     点击进外链播放：仓库自己的 raw 链接带 Content-Disposition: attachment，
     只会触发下载；image.edwining.com 不带该头且支持 Range，浏览器直接播。 -->
[![管理控制台概览](docs/images/demo.zh.gif)](https://image.edwining.com/public/redpandacompress_%E6%BC%94%E7%A4%BA%E8%A7%86%E9%A2%91.mp4)

*点击动图观看完整演示视频（90 秒）*


## 功能特性

- **多协议采集**：Modbus、西门子 S7、三菱 MC、欧姆龙 FINS/CIP、罗克韦尔 CIP、OPC UA、ABB 机器人 RWS、DL/T 645 电能表等 15 种协议/传输驱动，支持寄存器批量读取、字节序/字序配置。
- **动态表单驱动**：协议参数以 JSON Schema 动态表单存储于数据库，新增协议无需改前端代码。
- **进程外协议插件**：**上述 15 种协议驱动全部是插件**——主二进制不含任何协议实现，每个驱动是一个 `iot-driver-<名字>` 独立可执行文件，放进可执行文件同级的 `plugins/` 目录**即自动加载，无需停机重编网关**；插件进程崩溃自动退避重启、卡死自动检测，替换二进制即可热升级，见[驱动插件指南](docs/driver-plugins.md)。
- **采集引擎**：Worker 并发池调度轮询任务；后台 watcher 检测配置变更，**零中断热加载**。
- **推送引擎**：设备级分组批量分发；通道配置热加载；断网时数据写入 **本地 outbox 持久缓存**，恢复后自动补发，不丢数据。
- **MQTT 订阅入时序库**：订阅外部 MQTT broker 上的 JSON 报文，按映射规则转换为采集记录后写入已启用的 TDengine / InfluxDB 通道；支持通配符 Topic、英文逗号分隔的多 Topic、QoS、鉴权、样例报文预览与连通性测试，解析器可选内置映射 DSL 或 goja JS 脚本，配置增删改自动热加载。
- **断联报警**：设备采集失败与推送通道断连自动检测（去抖 + 状态机），离线/恢复边沿落库，统一报警中心查询。
- **Web 管理控制台**：首页大盘、设备对象、地址标签、协议管理、通道管理、报警管理、日志查看、密钥管理，中英双语界面。
- **开放接口**：`/openApi/*` 只读查询接口，`X-Api-Key` 密钥鉴权，供 MES / ERP 等外部系统对接。
- **单二进制部署**：前端经 `go:embed` 内嵌，SQLite 纯 Go 实现（免 CGO），主程序 + `plugins/` 目录即完整系统；主二进制**不再链接任何协议库**（协议库随插件分发），依赖面更窄；支持注册为 Windows 服务 / Linux systemd 服务，开机自启、崩溃自动拉起。

## 系统架构

```
                 ┌────────────────────── 单二进制 iot-gateway ──────────────────────┐
                 │                                                                  │
  PLC / 设备 ◀──▶│  collector 采集引擎 ──▶ driver 契约层 ──RPC──▶ plugins/ 插件进程   │
                 │   (Worker池/热加载)      (注册表/类型表)   (协议实现全在插件内)    │
                 │        │                                                         │
                 │        ▼ RecordSink                                              │
                 │  push 推送引擎 ────┬──▶ MQTT Broker                              │
                 │   (通道热加载)      ├──▶ TDEngine v3                              │
                 │        │          └──▶ InfluxDB v3                               │
                 │        │ 断网                                                     │
                 │        ▼                                                         │
                 │  SQLite outbox 本地缓存(补发)                                     │
                 │                                                                  │
                 │  alarm 断联报警 · Web 控制台(/admin) · Open API(/openApi)          │
                 └──────────────────────────────────────────────────────────────────┘
                                     ▲
                        ┌────────────┴─────────────┐
                        │  plugins/iot-driver-*     │   ← 独立进程，每协议一个插件
                        │  (Modbus/S7/MC/FINS/CIP/  │     每次读写一次 RPC 往返
                        │   OPC UA/ABB RWS ...)     │
                        └───────────────────────────┘
```

> 协议驱动跑在**独立进程**里（`plugins/` 下的 `iot-driver-*`），网关经 RPC 调用，每次读写多一次进程间往返；协议实现不进入主二进制。

## 支持协议

共 15 个协议名，**全部由 `plugins/` 下的进程外插件提供**（协议名与 scope 沿用历史值，**既有设备配置、`iot_protocol` 行与前端表单无需迁移**）：

| 协议 | 传输方式 | 典型设备 | 提供插件 |
|---|---|---|---|
| `ModBus.RTU` / `ModBus.TCP` | 串口 / 以太网 | 各类支持 Modbus 的 PLC、仪表、变频器 | `iot-driver-modbus` |
| `Siemens.S7` | 以太网 | 西门子 S7 系列 PLC | `iot-driver-s7` |
| `Mitsubishi.MC.TCP` / `Mitsubishi.MC.Serial` | 以太网 / 串口 | 三菱 MC 协议 PLC | `iot-driver-mitsubishi` |
| `Omron.FINS.UDP` / `.TCP` / `.Serial` / `.HostLinkTCP` | UDP / TCP / 串口 | 欧姆龙 FINS 协议 PLC | `iot-driver-fins` |
| `Omron.CIP` | EtherNet/IP | 欧姆龙 CIP 设备 | `iot-driver-omroncip` |
| `Rockwell.CIP` | EtherNet/IP | 罗克韦尔（AB）ControlLogix / CompactLogix | `iot-driver-rockwellcip` |
| `OPC.UA` | 以太网 | 支持 OPC UA 的设备与网关 | `iot-driver-opcua` |
| `ABB.RWS` | 以太网 (HTTP/HTTPS) | ABB 机器人控制器（IRC5 / OmniCore） | `iot-driver-abb` |
| `DLT645.Serial` / `DLT645.TCP` | 串口 (RS-485/RS-232) / 以太网 | DL/T 645 多功能电能表 | `iot-driver-dlt645` |

> `ABB.RWS` 通过 ABB 的 **Robot Web Services** REST 接口采集，可读 IO 信号、RAPID 变量、控制器面板状态、关节/笛卡尔位姿与系统信息；自动识别 RWS 1.0（IRC5/RW6，Digest 认证）与 RWS 2.0（OmniCore/RW7，Basic 认证）。地址写法见插件源码的地址语法说明，例如 `signal:Local/DRV_1/DI1`、`rapid:T_ROB1/Mod1/nTest`、`panel:ctrlstate`、`motion:ROB_1/joint/rax_1`、`system:rwversion`。

> `DLT645.Serial` / `DLT645.TCP` 采集 DL/T 645-2007 与 1997 两个方言的多功能电能表（电压/电流/功率/功率因数/电能/电表状态字），支持 RS-485 直连与串口服务器/DTU 透传两种链路。点位地址即**数据标识**：内置字典收录 A相电压 `02010100`、组合有功总电能 `00000000` 等常用项，厂商私有标识按 `DI:字节数:小数位[:标志]` 显式声明（如 `12345678:2:0:s`）。数据的字节数、小数位与符号由数据标识本身决定（不由类型下拉决定），下拉类型只决定输出形态。TCP 的 `host` 填串口服务器地址、`port` 填其透传端口（非 8899 时按现场填写）。

### 插件目录与加载方式

- 每个插件编译为 `iot-driver-<目录名>`（Windows 加 `.exe`），放在**可执行文件同级的 `plugins/`** 目录。
- 网关按 `plugin.dir`（默认 `plugins`，空或相对路径时**相对可执行文件所在目录**解析，而非当前工作目录）与 `plugin.glob`（默认 `iot-driver-*`）扫描目录，**启动即加载，运行期新放入的插件也会被自动加载**。
- `plugin.enabled` **出厂即为 `true`**：协议全部来自插件，关掉它网关就**不支持任何协议**（设备被静默跳过，只有一行 WARN）。
- 插件源码在 `backend/driversrc/<目录名>/`；`build.sh` / `release.sh` / `backend/Dockerfile` 会自动交叉编译并打进发行包（**一个插件都没编出来时构建直接失败**，不会产出没有协议的包）。
- 加载插件等于**以网关自身的 OS 权限执行该目录下的程序**——插件是可信代码而非沙箱内容，部署时须保证该目录仅管理员可写，详见 [SECURITY.md](SECURITY.md)。

> 各协议可承载的设备/点位容量与调优建议，见[采集容量压测报告](backend/cmd/loadtest/README.md)。该报告自 2026-09 起全部采自**插件版**（每次读写多一次网关 ↔ 插件 RPC 往返，读数已含该开销），覆盖 7 个协议在扫描频率 1000/200/100ms × RTT 0/10/20ms 下的完整矩阵，另含 DL/T 645 专节（`ABB.RWS` 未纳入本轮压测）；帧数模型与调优结论见[容量测试结论](backend/specs/capacity-testing.md)。

## 支持推送通道

| 通道 | 说明 |
|---|---|
| `mqtt` | Eclipse Paho MQTT 客户端，支持 QoS、TLS、遗嘱等 |
| `tdengine-v3` | TDEngine 3.x，官方 driver-go WebSocket 驱动（免 CGO，经 taosAdapter 连接） |
| `influxdb-v3` | InfluxDB 3.x，自研 Line Protocol over HTTP 直连（不引入官方重依赖客户端） |

> 推送通道与协议驱动均为**注册表 + 接口**的插件式设计，新增协议/通道无需改动引擎代码。

## MQTT 订阅入时序库

除了「采集 → 推送」，网关还可以**反向接入**：订阅外部 MQTT broker 上主题的 JSON 报文，按映射规则转换为与本地采集一致的记录，写入已启用的 **TDengine / InfluxDB** 通道。设备或第三方平台已经通过 MQTT 上报数据时，可用这种方式把数据统一汇入同一套时序库。

```
外部设备 / 平台 ──MQTT──▶ Broker ──订阅──▶ 订阅引擎（JSON → 采集记录）──▶ 推送引擎 ──▶ TDengine / InfluxDB
```

- **只写时序库通道**：记录只投递给 `tdengine-v3` / `influxdb-v3`，**不投 MQTT 等普通通道**——订阅主题与发布主题相同时会形成自激。转换结果复用推送通道的攒批、断网缓存（outbox）与补发。
- **每条订阅独立连接**：Broker / 端口 / Client ID / 用户名密码 / Topic（支持 `+`、`#` 通配符，多个 Topic 用英文逗号分隔并共用同一套映射与 QoS）/ QoS `0`~`2` 分别配置；保存前可「测试连接」，并用样例报文「预览」转换结果（均不落库）。
- **解析器**：每个订阅可选「内置映射」或「脚本」。内置映射即下一条的声明式 DSL；脚本在 goja 中运行 `function parse(msg, ctx)`，用于 DSL 表达不了的报文（逐报文 1–500ms 超时，超时后重建 VM；不提供文件、网络与 `require`），控制台内置解析器编辑器。仅修改解析器时**原地热替换**，不重连 MQTT。契约见[解析器规范](backend/specs/parser-plugins.md)，脚本写法见[脚本指南](backend/docs/parser-scripts.md)。
- **映射规则**：`root` 指定取值根（默认 `$`，整篇报文为数组时自动逐条拆条，也可显式写 `$.data[*]`）；目标字段是采集记录的白名单（设备 ID / 设备名称 / 点位 ID / 点位说明 / 采集值 / 类型类别 / 质量码等，未知字段保存即拒）；每个字段支持 `path`（JSON 路径）、`const`（固定值）、`template`（模板拼接）、`map`（值映射，用于外部类型与质量码翻译）四种取值方式，并可附加 `transform` 算子链（`scale` / `round` / `trim` / `default` / `hexDecode` / `base64Decode` / `bitfield` / `regex`）；`topicPattern` 可按主题路径段取名（配合 `source: "topic"`），`fanout` 可把一条报文扇出成多条测点记录；`filter` 为隐式 AND 的过滤条件（`eq` / `neq` / `gt` / `gte` / `lt` / `lte` / `contains` / `in` / `notin` / `exists` / `notexists`）。
- **采集时间**：报文自带时间时可映射到 `collectedAt`（epoch 秒 / 毫秒 / 微秒 / 纳秒与常见日期串，归一为毫秒精度本地时间）；未配置或无法识别时按收到报文的时间落库。
- **设备 / 点位元信息**：时序库只保存 ID 与数值，订阅报文里出现过的设备 / 点位会按自然键 `(deviceId, deviceAddressId)` 异步收录到「订阅设备与点位」页（不含采样值），便于按名称查询与后续统计分析。该页支持手工维护：新增 / 编辑 / Excel 导入（逐行返回错误）/ 模板下载；来源列区分「自动」与「手工」，手工行不会被后续自动收录覆盖，可批量删除（≤200，仅手工行）并「恢复为自动」。
- 订阅配置每 10 秒比对校验和热加载，新增 / 修改 / 删除 / 启停**无需重启网关**；运行状态页展示每个订阅的连接状态、报文数 / 记录数 / 跳过数 / 丢弃数 / 错误数与最近错误。

## 快速开始

### 环境要求

- Go ≥ 1.26、Node.js ≥ 22（仅开发/构建时需要；生产运行只需产物二进制）

### 1. 开发模式（前后端分离，热更新）

```bash
# 终端 1：后端（端口 9082）
cd backend && go run .

# 终端 2：前端（端口 4000）
cd frontend && npm install && npm run dev
```

或使用 VS Code：终端 → 运行任务 → `dev`，一键并行启动前后端。
访问 `http://localhost:4000`，请求经 Vite 代理转发至后端 9082。

### 2. 生产构建（单二进制）

```bash
build.bat            # Windows 上构建四个平台产物（Linux amd64/arm64/arm32 + Windows）
build.bat windows    # 仅构建 Windows
```

产物为 `backend/iot-gateway(.exe)`（内嵌前端）+ 与它同层的插件目录（每个平台一个：`backend/plugins-windows/`、`backend/plugins-linux/`、`backend/plugins-linux-arm64/`、`backend/plugins-linux-arm/`）。**两者必须一起拷贝到目标设备**，且部署时插件目录要叫 `plugins/`（网关默认的 `plugin.dir`）并与可执行文件同级：

```bash
./iot-gateway        # 管理控制台: http://<主机IP>:9082/admin
```

> ⚠️ 主二进制**不含任何协议实现**：`plugins/` 缺失或为空时网关能启动、能登录，但**一台设备也采不到**。打包发行版请用 `release.sh`，它会把插件一并交叉编译并打进压缩包；`build.sh` 同样会构建插件。插件是怎么编出来的、各入口产物长什么样，见[插件打包](#插件打包)。

### 3. 服务化部署（开机自启）

| 平台 | 安装 | 卸载 |
|---|---|---|
| Windows | 管理员运行 `install.bat` | 管理员运行 `uninstall.bat [--purge]` |
| Linux | `sudo ./install.sh` | `sudo ./uninstall.sh [--purge]` |

- Windows 经 `sc` 注册为服务（内置原生服务支持，无需 NSSM），崩溃自动重启。
- 默认安装目录：Windows `%ProgramFiles%\iot-gateway`、Linux `/opt/iot-gateway`（环境变量 `IOT_GATEWAY_HOME` / `INSTALL_DIR` 可覆盖）。
- 服务以安装目录为工作目录，`data/`（SQLite）与 `log/`（日志）写入该目录；`--purge` 卸载时连数据一并删除，默认保留。

### 4. Docker Compose 部署（容器化）

无需本地安装 Go / Node.js，在项目根目录执行即可（多阶段构建：前端构建 → Go 编译 → 运行镜像）：

```bash
docker compose up -d --build    # 构建镜像并后台启动
docker compose logs -f          # 跟踪日志
docker compose ps               # 查看状态（含健康检查）
docker compose down             # 停止并移除容器
```

- 管理控制台：`http://<主机IP>:9082/admin`（宿主机端口 `9082`）。
- `./data`（SQLite）与 `./log`（日志）挂载至容器内 `/app/data`、`/app/log`，容器重建后数据不丢。
- **`plugins/` 刻意不挂载**：镜像内已构建好全部协议插件，而 `./plugins:/app/plugins` 这类绑定挂载会把镜像内的插件整个遮蔽（宿主目录为空即无任何协议）。需运行期换插件时见[升级须知](#升级须知破坏性变更非滚动升级)。
- 内置健康检查（`/health`），配合 `restart: unless-stopped` 异常自动拉起。

### 插件打包

插件**不需要逐个手动编译**——所有构建入口都按 `backend/driversrc/*/` 一层目录交叉编译，**目录名即产物名**：

- 扫描 `backend/driversrc/` 下的**一层**子目录（含 `main.go` 的才编），每个编成 `iot-driver-<目录名>`（Windows 加 `.exe` 后缀）；
- 用与主二进制**同一份** `GOOS` / `GOARCH` / `GOARM` 环境编译——插件与宿主架构不一致时网关照样启动，但该插件会被静默跳过，表现为「设备建好了却采不到值」；
- **一个插件都没编出来时构建直接失败**，不会产出没有协议的包。

| 打包入口 | 运行环境 | 产物 |
|---|---|---|
| `build.bat` / `build.sh` | Windows / Linux·macOS | `backend/iot-gateway(.exe)` + `backend/plugins-<平台>/`（与主二进制同层） |
| `backend/package.bat` | Windows | 同上，一次产出 4 个平台目录（`plugins-linux/`、`plugins-linux-arm64/`、`plugins-linux-arm/`、`plugins-windows/`） |
| `release.sh` | Linux·macOS / Git Bash | `release/` 下的 `*.zip` / `*.tar.gz`，压缩包内就是 `iot-gateway(.exe)` + `plugins/` 两层 |
| `backend/Dockerfile` | 容器（多阶段构建） | 镜像内 `/app/plugins/`，出厂即带全部插件 |

**只重编单个插件**（改了某个协议、或本地验证），直接 `go build` 即可：

```bash
cd backend
go build -o ./plugins/iot-driver-modbus ./driversrc/modbus    # Windows 上加 .exe 后缀
```

**新增协议**只需在 `backend/driversrc/` 下新建一个目录（内含 `package main` 的 `main.go`），**无需改动任何构建脚本**——下一次打包就会被 glob 到。唯一约束是**目录名 = 产物名，必须唯一且不嵌套**（所以是 `omroncip` / `rockwellcip`，而不是 `omron/cip`）。

> 本地构建产出的目录带平台后缀（`plugins-windows/` 等），是为了避免四个平台的同名产物互相覆盖——混在一个目录里会覆盖成错的架构，而且**覆盖后不报任何错**。部署时把对应平台的目录改名为 `plugins/` 并放到可执行文件同级即可，`release.sh` 已经替你做好了这一步。

### 默认账号

首次启动自动初始化账号：`admin`，初始密码 `hand@123`。

> ⚠️ **安全提醒**：默认密码仅用于首次登录，生产部署后请**立即修改**所有默认账号密码；管理控制台（`/admin`）不建议直接暴露公网。其他加固建议（JWT 密钥配置、网络隔离等）见 [SECURITY.md](SECURITY.md)。

## 升级须知（破坏性变更，非滚动升级）

协议驱动全部搬为进程外插件后，**主二进制与 `plugins/` 目录是严格的版本配对关系**：

- **两种混搭都会失效**：旧网关二进制 + 新插件、新网关二进制 + 旧插件，都会让对应协议不可用（插件与网关之间是一套进程间契约，版本不匹配的插件不会被正常加载）。
- **必须成对替换**：升级时把**主二进制与 `plugins/` 目录作为一个整体**替换，不要只换其中一个。
- **回滚很干净**：本次改造**未改动数据库**（`migrations/` 未新增或修改，`iot_protocol` 等表结构不变），把旧二进制与旧 `plugins/` **成对换回**即可完全回到原状。
- **协议名没变**：原有的 12 个协议名与类型 scope 沿用原值，**既有设备配置、`iot_protocol` 行、前端表单都不需要迁移**（`ABB.RWS` 与 `DLT645.Serial` / `DLT645.TCP` 是新增的协议，只影响新建设备）。

**Docker 部署注意**：`docker-compose.yml` 里原先的 `./plugins:/app/plugins:ro` 绑定挂载会**遮蔽镜像内已构建好的插件**（宿主目录为空 = 网关没有任何协议），现已注释掉。若确需在运行期挂载插件（灰度 / 热升级），**必须先把发行包 `plugins/` 的内容拷进宿主目录**再挂载，并保持只读。

## 管理控制台

| 页面 | 说明 |
|---|---|
| 首页大盘 | 设备在线状态、采集与推送概览 |
| 设备对象 / 地址标签 | 按网关分组管理设备，维护采集点位与数据类型 |
| 协议管理 | 协议参数动态表单配置（支持自定义表单 JSON） |
| 通道管理 | MQTT / TDEngine / InfluxDB 推送通道配置与状态监控 |
| 订阅管理 | 订阅外部 MQTT 报文并写入时序库：内置映射 / JS 脚本解析器、映射规则、样例预览、连通性测试与运行状态；「订阅设备与点位」页自动收录并支持手工增改、Excel 导入、删除与恢复 |
| 报警管理 | 设备断联报警、通道断联报警，历史记录查询 |
| 日志查看 | 在线浏览与下载网关运行日志 |
| 密钥管理 | 生成 / 启停 Open API 访问密钥（admin） |

## 开放接口

外部系统凭密钥调用（密钥在控制台「密钥管理」生成），请求头携带 `X-Api-Key`：

```
GET /openApi/device/page          # 设备分页查询
GET /openApi/deviceAddress/page   # 设备地址分页查询（按设备 ID）
```

## 推送数据格式

推送到 MQTT / TDEngine / InfluxDB 的每条点位数据的字段语义（`value`、`kind`、`quality` 等）与自描述解析约定，见[推送载荷契约](backend/specs/push-payload.md)。下游消费端（MES / 时序库分析）应据此解析，不依赖网关内部实现。

## 配置说明

配置文件为 `backend/default.yaml`，可用 `APP_ENV=dev|prod` 切换 `dev.yaml` / `prod.yaml` 覆盖，主要配置项：

```yaml
server:
  port: 9082          # HTTP 服务端口
jwt:
  secret: ""          # JWT 签名密钥：留空则每次启动随机生成（重启后登录态失效）；
                      # 生产环境建议固定，优先读环境变量 IOT_GATEWAY_JWT_SECRET
  expireHour: 24      # 登录令牌有效期
log:
  level: INFO         # 日志级别
  maxSizeMB: 10       # 日志滚动大小
  maxDays: 30         # 日志保留天数
sqlite:
  path: data/iot-gateway.db   # SQLite 数据文件路径
plugin:
  enabled: true       # 进程外协议插件总开关：**出厂即为 true** —— 全部协议都来自插件，
                      # 关掉它网关不支持任何协议。加载插件 = 以网关自身的 OS 权限执行
                      # plugins/ 下的程序（可信代码，非沙箱），务必保证目录仅管理员可写
  dir: plugins        # 插件目录：空或相对路径时相对「可执行文件所在目录」解析，而非工作目录
push:
  changeOnly: true    # 变化才推送：点位值或质量码（quality）与上次投递相同就不再发往
                      # MQTT / 时序库，降低带宽与写入量。网关启动、新增或改配推送通道
                      # 后的首轮总是全量投递（否则稳定不变的点位在新通道上没有初值）。
                      # 依赖周期心跳数据的场景置 false。可在「运行开关」页动态切换，
                      # 从下一批采集起生效。
```

## 目录结构

```
├── backend/            # Go 后端
│   ├── collector/      # 采集引擎（轮询调度 / 配置热加载）
│   ├── driver/         # 协议驱动契约与基建（注册表 / 类型表 / 数据类型映射，不含协议实现）
│   ├── pluginwire/     # 进程外插件传输层（帧编解码 / 方法名 / RemoteError，与插件种类无关）
│   ├── pluginhost/     # 进程外插件宿主（目录扫描 / 进程监管 / RPC 代理 / 热升级；驱动侧适配在 adapter_driver.go）
│   ├── driversdk/      # 进程外驱动插件开发套件（Serve / Meta / RPC 参数）
│   ├── driversrc/      # 协议插件源码（9 个子目录，各编译成 iot-driver-<目录名>）
│   ├── plugins/        # 插件运行时目录（构建产物，发行包自带）
│   ├── push/           # 推送引擎（mqtt/tdengine-v3/influxdb-v3 + outbox）
│   ├── parser/         # 订阅报文解析器（内置映射 DSL / goja 脚本，输出采集记录）
│   ├── subscribe/      # MQTT 订阅引擎（连接 / 队列 / 热加载，解析与投递编排）
│   ├── controller/ service/ model/  # HTTP 分层（Controller → Service → Database）
│   ├── database/sqlite/migrations/  # SQLite 迁移脚本
│   ├── router/ middleware/          # 路由与中间件（JWT / Open API 密钥 / CORS）
│   ├── web/            # 前端构建产物（go:embed 嵌入）
│   └── specs/          # 项目技术规范文档
├── frontend/           # Vue3 管理前端（Element Plus + Pinia + vue-i18n）
├── docker-compose.yml  # Docker Compose 部署编排（配合 backend/Dockerfile 多阶段构建）
├── build.bat / .sh     # 一键打包（前端构建 → 嵌入 → Go 交叉编译）
├── install.bat / .sh   # 服务化安装（Windows / Linux）
└── uninstall.bat / .sh # 服务化卸载
```

## 技术栈

| 层次 | 选型 |
|---|---|
| 后端 | Go 1.26 + Gin + GORM + Google Wire + Viper + JWT |
| 存储 | SQLite（纯 Go 实现，免 CGO） |
| 协议库 | **随插件分发**：goburrow/modbus · robinson/gos7 · gopcua/opcua 等仅被 `backend/driversrc/` 下的插件链接，主二进制不链接任何协议库 |
| 前端 | Vue 3 + Vite + Element Plus + Pinia + Tailwind CSS 4 + vue-i18n |
| 部署 | go:embed 单二进制 · Windows 服务 / Linux systemd |

## 文档导航

| 文档 | 内容 |
|---|---|
| [驱动插件指南](docs/driver-plugins.md) | 进程外协议插件的开发、生命周期、安全与排错（新增协议必读） |
| [技术规范](backend/specs/README.md) | 分层架构、目录结构、开发规范等 |
| [推送载荷契约](backend/specs/push-payload.md) | 外发数据的字段语义与解析约定（[EN](backend/specs/push-payload.en.md)） |
| [订阅解析器规范](backend/specs/parser-plugins.md) | 内置映射 DSL 与 goja 脚本的契约、限制与热加载语义 |
| [订阅解析脚本指南](backend/docs/parser-scripts.md) | 脚本解析器的写法、`msg` / `ctx` API 与示例 |
| [采集容量压测报告](backend/cmd/loadtest/README.md) | 各协议可承载的设备/点位容量与调优建议（[EN](backend/cmd/loadtest/README.en.md)） |
| [安全策略](SECURITY.md) | 漏洞报告与部署加固建议 |
| [贡献指南](CONTRIBUTING.md) | 提 Issue / PR 与开发流程 |
| [更新日志](CHANGELOG.md) | 版本变更记录 |

> 完整文档索引（含各文档的中英对照情况）见 [docs/](docs/README.md)。

## 参与贡献

欢迎报告问题、补充文档、贡献新协议驱动或推送通道，见 [CONTRIBUTING.md](CONTRIBUTING.md)；安全漏洞请勿公开 Issue，参见 [SECURITY.md](SECURITY.md)。

## 社区交流

- **Issue / Discussion**：Bug 与功能建议请提 [Issue](https://gitee.com/wang4856304/iot-gateway/issues)（GitHub 仓库为镜像，仅接受 PR）。
- **微信交流群**：联系 `15289288565@163.com` 拉群。

<!-- TODO: <img src="docs/images/wechat-group.png" width="240" alt="微信交流群" /> -->

## 许可证与 FAQ

本项目以 [GNU AGPL-3.0](LICENSE) 协议开源。以下 FAQ 帮助你快速判断自己的使用场景（仅为友好说明，不构成法律意见）：

**Q1：我在公司内部部署使用（采集自家产线数据），需要开源自己的代码吗？**

不需要。AGPL 的源码开放义务只在**分发**软件或**将其作为网络服务对外提供**时触发。内部使用未修改版本，不向外部用户提供服务，无需开放任何源码。

**Q2：我通过 Open API / MQTT 对接自己的系统（MES、ERP、大屏等），我的业务代码需要以 AGPL 开源吗？**

不需要。你的系统经网络接口与本网关通信，属于独立程序间的数据交互，你的业务代码不是本项目的衍生作品，不受 AGPL 约束。

**Q3：什么情况下我必须开放源码？**

修改了本项目源码，且满足以下任一条件：① 将修改后的版本**分发**给他人（含打包出售、随硬件交付）；② 将修改后的版本**通过网络向用户提供服务**（含部署给第三方使用）。此时须按 AGPL-3.0 向相应对象提供完整的对应源码。

**Q4：我可以为本项目开发闭源的协议驱动 / 推送通道吗？**

- 编译进本网关二进制的驱动 / 通道属于衍生作品，须随项目以 AGPL-3.0 开源。
- 以**独立进程**经网络与本网关交互（如独立采集程序、独立推送代理）则不受约束。
- 欢迎将自研驱动 / 通道以 AGPL-3.0 贡献回社区，见 [CONTRIBUTING.md](CONTRIBUTING.md)——项目采用注册表 + 接口的插件式设计，接入成本很低。

**Q5：网关采集、推送的数据受 AGPL 约束吗？**

不受。数据归使用者所有，AGPL 只约束软件本身及其衍生作品，不会"传染"到采集、传输、存储的数据。

**Q6：我是硬件 / 网关设备厂商，想把本项目嵌入产品销售，可以吗？**

可以，两条路径任选：① 按 AGPL-3.0 履行义务，向购买者提供完整对应源码；② 联系我们购买**商业授权**，免除 AGPL 义务，支持闭源 OEM 嵌入。

**Q7：如何获得闭源商用 / OEM / 双授权？**

联系 `<15289288565@163.com>`，说明使用场景（OEM 嵌入、SaaS、二次开发交付等），我们提供商业许可。

---

- 外部贡献默认按 [CLA.md](CLA.md) 授权给项目维护方，并随项目以 AGPL-3.0 许可；商业版仓库与本仓库代码相互独立。
- 开源咨询与商业授权请联系：`<15289288565@163.com>`
