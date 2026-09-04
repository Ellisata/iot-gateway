# iot-gateway · 工业 IoT 数据网关

[English](README.en.md) | 简体中文

**iot-gateway** 是一个开箱即用的工业物联网数据采集网关：定时轮询 PLC / 工业设备，将采集数据推送至 MQTT、TDEngine、InfluxDB 等下游通道，并提供内嵌的 Web 管理控制台。整体以 **单二进制** 形式部署到网关设备或工控机，无需外部数据库。

<!-- 管理控制台截图（首页大盘 / 设备管理）：docs/images/ 下放中文界面截图 -->
<img src="docs/images/dashboard.zh.png" width="900" alt="管理控制台" />


## 功能特性

- **多协议采集**：内置 Modbus、西门子 S7、三菱 MC、欧姆龙 FINS/CIP、罗克韦尔 CIP、OPC UA 等 12 种协议/传输驱动，支持寄存器批量读取、字节序/字序配置。
- **动态表单驱动**：协议参数以 JSON Schema 动态表单存储于数据库，新增协议无需改前端代码。
- **采集引擎**：Worker 并发池调度轮询任务；后台 watcher 检测配置变更，**零中断热加载**。
- **推送引擎**：设备级分组批量分发；通道配置热加载；断网时数据写入 **本地 outbox 持久缓存**，恢复后自动补发，不丢数据。
- **断联报警**：设备采集失败与推送通道断连自动检测（去抖 + 状态机），离线/恢复边沿落库，统一报警中心查询。
- **Web 管理控制台**：首页大盘、设备对象、地址标签、协议管理、通道管理、报警管理、日志查看、密钥管理，中英双语界面。
- **开放接口**：`/openApi/*` 只读查询接口，`X-Api-Key` 密钥鉴权，供 MES / ERP 等外部系统对接。
- **单二进制部署**：前端经 `go:embed` 内嵌，SQLite 纯 Go 实现（免 CGO），一个可执行文件即完整系统；支持注册为 Windows 服务 / Linux systemd 服务，开机自启、崩溃自动拉起。

## 系统架构

```
                 ┌────────────────────── 单二进制 iot-gateway ──────────────────────┐
                 │                                                                  │
  PLC / 设备 ───▶│  collector 采集引擎 ──▶ driver 协议驱动层                          │
                 │   (Worker池/热加载)      (Modbus/S7/MC/FINS/CIP/OPC UA ...)       │
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
```

## 支持协议

| 协议 | 传输方式 | 典型设备 |
|---|---|---|
| ModBus.RTU / ModBus.TCP | 串口 / 以太网 | 各类支持 Modbus 的 PLC、仪表、变频器 |
| Siemens.S7 | 以太网 | 西门子 S7 系列 PLC |
| Mitsubishi.MC.TCP / MC.Serial | 以太网 / 串口 | 三菱 MC 协议 PLC |
| Omron.FINS.UDP / FINS.TCP / FINS.Serial / FINS.HostLinkTCP | UDP / TCP / 串口 | 欧姆龙 FINS 协议 PLC |
| Omron.CIP | EtherNet/IP | 欧姆龙 CIP 设备 |
| Rockwell.CIP | EtherNet/IP | 罗克韦尔（AB）ControlLogix / CompactLogix |
| OPC.UA | 以太网 | 支持 OPC UA 的设备与网关 |

> 各协议可承载的设备/点位容量与调优建议，见[采集容量压测报告](backend/cmd/loadtest/README.md)。

## 支持推送通道

| 通道 | 说明 |
|---|---|
| `mqtt` | Eclipse Paho MQTT 客户端，支持 QoS、TLS、遗嘱等 |
| `tdengine-v3` | TDEngine 3.x，官方 driver-go WebSocket 驱动（免 CGO，经 taosAdapter 连接） |
| `influxdb-v3` | InfluxDB 3.x，自研 Line Protocol over HTTP 直连（不引入官方重依赖客户端） |

> 推送通道与协议驱动均为**注册表 + 接口**的插件式设计，新增协议/通道无需改动引擎代码。

## 快速开始

### 环境要求

- Go ≥ 1.26、Node.js ≥ 22（仅开发/构建时需要；生产运行只需产物二进制）

### 1. 开发模式（前后端分离，热更新）

```bash
# 终端 1：后端（端口 9081）
cd backend && go run .

# 终端 2：前端（端口 4000）
cd frontend && npm install && npm run dev
```

或使用 VS Code：终端 → 运行任务 → `dev`，一键并行启动前后端。
访问 `http://localhost:4000`，请求经 Vite 代理转发至后端 9081。

### 2. 生产构建（单二进制）

```bash
build.bat            # Windows 上构建 Linux + Windows 双平台产物
build.bat windows    # 仅构建 Windows
```

产物为 `backend/iot-gateway(.exe)`（内嵌前端），拷贝到目标设备直接运行：

```bash
./iot-gateway        # 管理控制台: http://<主机IP>:9081/admin
```

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

- 管理控制台：`http://<主机IP>:9081/admin`（宿主机端口 `9081`）。
- `./data`（SQLite）与 `./log`（日志）挂载至容器内 `/app/data`、`/app/log`，容器重建后数据不丢。
- 内置健康检查（`/health`），配合 `restart: unless-stopped` 异常自动拉起。

### 默认账号

首次启动自动初始化两个账号：`admin`、`hand`，初始密码 `hand@123`。

> ⚠️ **安全提醒**：默认密码仅用于首次登录，生产部署后请**立即修改**所有默认账号密码；管理控制台（`/admin`）不建议直接暴露公网。其他加固建议（JWT 密钥配置、网络隔离等）见 [SECURITY.md](SECURITY.md)。

## 管理控制台

| 页面 | 说明 |
|---|---|
| 首页大盘 | 设备在线状态、采集与推送概览 |
| 设备对象 / 地址标签 | 按网关分组管理设备，维护采集点位与数据类型 |
| 协议管理 | 协议参数动态表单配置（支持自定义表单 JSON） |
| 通道管理 | MQTT / TDEngine / InfluxDB 推送通道配置与状态监控 |
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
  port: 9081          # HTTP 服务端口
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
```

## 目录结构

```
├── backend/            # Go 后端
│   ├── collector/      # 采集引擎（轮询调度 / 配置热加载）
│   ├── driver/         # 协议驱动注册表（modbus/s7/mitsubishi/omron/rockwell/opcua）
│   ├── push/           # 推送引擎（mqtt/tdengine-v3/influxdb-v3 + outbox）
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
| 协议库 | goburrow/modbus · robinson/gos7 · gopcua/opcua（其余协议自研实现） |
| 前端 | Vue 3 + Vite + Element Plus + Pinia + Tailwind CSS 4 + vue-i18n |
| 部署 | go:embed 单二进制 · Windows 服务 / Linux systemd |

## 参与贡献

欢迎报告问题、补充文档、贡献新协议驱动或推送通道，见 [CONTRIBUTING.md](CONTRIBUTING.md)；安全漏洞请勿公开 Issue，参见 [SECURITY.md](SECURITY.md)。

## 社区交流

- **Issue / Discussion**：Bug 与功能建议请提 [Issue](https://gitee.com/wang4856304/iot-hand-gateway/issues)（GitHub 仓库为镜像，仅接受 PR）。
- **微信交流群**：扫描下方二维码或联系 `15289288565@163.com` 拉群。

<!-- TODO: <img src="docs/images/wechat-group.png" width="240" alt="微信交流群" /> -->

## 许可证

本项目以 [GNU AGPL-3.0](LICENSE) 协议开源。

- 修改本项目源码并通过网络向用户提供服务（含部署给第三方使用）的，须按 AGPL-3.0 开放相应源码。
- 如需**闭源商用、OEM 嵌入、或不希望公开源码的商业部署**，可联系我们购买商业授权（免除 AGPL 义务）。
- 外部贡献默认按 [CLA.md](CLA.md) 授权给项目维护方，并随项目以 AGPL-3.0 许可；商业版仓库与本仓库代码相互独立。

开源咨询与商业授权请联系：`<15289288565@163.com>`
