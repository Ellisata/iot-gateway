# 进程外协议驱动插件

> **仓库边界（2026-10）**：驱动 SDK 已独立为 [`iot-gateway-sdk`](https://github.com/Ellisata/iot-gateway-sdk)
> （Apache-2.0），官方驱动独立为 [`iot-gateway-drivers`](https://github.com/Ellisata/iot-gateway-drivers)
> （AGPL-3.0）。本仓库的 `backend/sdk/` 与 `backend/driversrc/` 是这两个仓库的 submodule。
> 本文中的 `backend/driversrc/...` 路径指向 submodule；第三方新驱动请直接依赖 SDK，
> 不要 import 网关核心。许可证边界见 [LICENSING.md](../LICENSING.md)。

给网关**新增或升级一个 PLC 协议驱动**，不再需要修改网关源码、重新编译、停机部署：
把驱动编译成一个独立可执行文件，丢进 `plugins/` 目录，下一次目录扫描（默认 10s）即自动加载；
**替换**已在运行的插件二进制同样在下次扫描即生效，且不会牵动其它协议
（见 [5.5 热升级](#55-热升级替换已加载的插件)）。

- [1. 快速开始](#1-快速开始)
- [2. 编写驱动](#2-编写驱动) —— 含 [2.4 多协议](#24-一个插件提供多个协议) 与
  [2.5 把既有协议搬成插件](#25-把既有协议搬成插件唯一路径自带实现)（唯一路径：自带实现，含行为等价黄金基线）
- [3. 必须遵守的约定](#3-必须遵守的约定) —— 含 [3.1 Scope](#31-scope-沿用原值但不得与另一个插件撞名)（结论已反转）
- [4. 配置项](#4-配置项)
- [5. 生命周期与自愈](#5-生命周期与自愈) —— 含 [5.5 热升级](#55-热升级替换已加载的插件)
- [6. 安全](#6-安全)
- [7. 排错](#7-排错)

---

## 1. 快速开始

### 1.1 写一个插件

完整的真实范例见 [`backend/driversrc/modbus/`](../backend/driversrc/modbus/)
（自包含的 Modbus 实现，见 §2.5）。

最小骨架：

```go
package main

import (
    "github.com/Ellisata/iot-gateway-sdk/driver"
    "github.com/Ellisata/iot-gateway-sdk/driversdk"
    "iot-gateway/model/po"
)

type myDriver struct{ /* ... */ }

func (d *myDriver) Connect(protocolJSON string) error                          { /* ... */ }
func (d *myDriver) Ping(protocolJSON string) error                             { /* ... */ }
func (d *myDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) { /* ... */ }
func (d *myDriver) IsConnected() bool                                          { /* ... */ }
func (d *myDriver) Close() error                                               { /* ... */ }

func main() {
    driversdk.Main(
        // 协议名 → 工厂。一个插件可提供多个协议，见 §2.4。
        driversdk.Factories{
            "Acme.PLC.TCP": func() driver.Driver { return &myDriver{} },
        },
        driversdk.Meta{
            APIVersion: driversdk.APIVersion,
            Scope:      "acme",   // scope 前缀，不得与其它插件重名，见 §3.1
            Protocols: []driversdk.Protocol{
                {
                    Name:     "Acme.PLC.TCP", // = iot_protocol.name，全局唯一
                    FormJSON: []byte(`{"rule":[...],"options":{...}}`),
                },
            },
            Types: []*driver.DataType{
                {Name: "int16", Kind: driver.KindInt, Size: 2,
                 Decode: decodeInt16, Encode: encodeInt16, Format: formatValue},
            },
            TypeMap: map[string]string{"Short": "int16", "Boolean": "bool"},
        },
    )
}
```

`driver.Driver` 的五个方法签名与网关里其它任何驱动**完全一致** —— 你可以照抄随发行版的
任何一个协议插件，例如
[`driversrc/modbus/modbusdrv/driverTcp.go`](../backend/driversrc/modbus/modbusdrv/driverTcp.go)
（`driver/` 下已经没有协议实现可抄了，见 §2.5）。

> **已有现成协议想直接搬成插件？** 见 §2.5 —— 注意这一版里「复用内置包」那条路
> **已经不存在**：主二进制里不再有任何协议实现，内置基线表也已清空，
> 唯一路径是把实现整体搬进插件包。随发行版的 Modbus 插件
>（[`backend/driversrc/modbus/`](../backend/driversrc/modbus/)）就是这条路的样子：
> 它自带一份完整的 Modbus 实现（`modbusdrv/` 子包），可当作真实协议插件的范例来读。

### 1.2 编译

```bash
cd backend/driversrc
go build -o ../plugins/iot-driver-acme ./acme
```

文件名必须以 `iot-driver-` 开头（可配，见 §4），否则不会被扫描到。

**输出目录要对准二进制所在的那一层。** `plugin.dir` 是相对 `os.Executable()`
解析的（见 §4），不认 cwd —— 本地开发时主二进制由 `go build -o iot-gateway .`
产在 `backend/`，插件就得放 `backend/` 下那一层且目录名要等于 `plugin.dir` 的值
（默认 `plugins`；用 `build.bat` / `build.sh` 批量编则是带平台后缀的
`backend/plugins-windows/` 等，跑之前得改名或改配置）；发行包里二进制在包根，
插件就在包根的 `plugins/`。

仓库内自带的插件（放在 `backend/driversrc/<名字>/`，**拍平的一层目录，每个目录里是
`main.go` 加一个实现子包**）会被 `build.sh` / `release.sh` / `backend/Dockerfile`
按 `driversrc/*/` 一层 glob 自动交叉编译到上述位置并打进发行包。

> **插件是发行包的必需组成，不是可选项。** 全部协议驱动都由插件提供，
> 主二进制里没有任何协议实现，所以 `plugins/` 为空 = 网关不支持任何协议。
> `release.sh` 与 `Dockerfile` 因此都在「一个插件都没编出来」时**直接失败**，
> 而不是产出一个能启动、能登录、却一台设备也采不到的发行包 ——
> 那种包要到现场接设备时才会暴露问题。Windows 本地开发路径
>（`build.bat` / `backend/package.bat`）同样带插件编译且同样以失败收场。

### 1.3 启用

`default.yaml`：

```yaml
plugin:
  enabled: true
  dir: plugins        # 相对「可执行文件所在目录」解析，不是 cwd
```

重启网关。日志里应出现：

```
pluginhost: 已加载插件 iot-driver-acme（协议 Acme.PLC.TCP, Acme.PLC.Serial，scope acme，版本 1.0.0，sha256 ...）
pluginhost: 已为插件协议 Acme.PLC.TCP 创建 iot_protocol 行
pluginhost: 已为插件协议 Acme.PLC.Serial 创建 iot_protocol 行
```

此时前端的协议下拉里就能选到这些协议，按各自的表单填好配置即可建设备与点位。

### 1.4 之后新增协议 / 升级插件

**都不需要重启。**

- **新增**：把新的 `iot-driver-xxx` 放进 `plugins/`，等一次扫描（默认 10s），
  协议会自动注册、协议行会自动建立、采集配置会自动热刷新。
- **升级**：用新编译的二进制替换掉已在运行的那个（文件名不变），同样等一次扫描。
  网关会拉起新版本、把驱动切过去，**且不重建采集任务** —— 其它协议
  完全不受影响。详见 [5.5](#55-热升级替换已加载的插件)。

> ⚠️ **网关与插件必须同一次构建产出。** 线协议版本（`driversdk.APIVersion`，当前 `4`）
> 与帧魔数（`pluginwire.FrameMagic`，当前 `IGP2`）都会随线格式变化进位。
> 两者**各管一层**：载荷字段变化只进 `APIVersion`（如 v2→v3 新增 `ReadResult.QualityCause`，
> v3→v4 又把它删掉、成因并进 `ReadResult.Quality` 这个整数；两次帧魔数都不变），
> 信封变化才进帧魔数。
> 升级网关时**必须重新编译全部插件**，否则握手阶段就会以
> `APIVersion 不匹配` 拒绝加载 —— 这是刻意的：旧插件若被放进来，
> 读到的会是错位的数据而不是一句报错。

---

## 2. 编写驱动

### 2.1 `Meta` 字段

**插件级**（两者对所有协议共用）：

| 字段 | 说明 |
|---|---|
| `APIVersion` | 填 `driversdk.APIVersion`。版本不匹配的插件会被拒绝加载，防止用旧套件构建的插件在网关升级后静默错配。 |
| `Scope` | 类型注册前缀。**沿用原值，或自取一个不与其它插件重名的新名**，见 [§3.1](#31-scope-沿用原值但不得与另一个插件撞名)（该节的结论已反转，别再照旧版写成「必须全新」）。一个插件的全部协议共用它。 |
| `Description` | 插件说明，作为各协议的兜底描述。 |
| `Version` | 插件自身版本号，仅用于日志与诊断。 |
| `Types` | 本插件注册的内部类型，见 §2.2。 |
| `TypeMap` | 通用/扩展类型名 → 内部裸名。**每个 value 必须出现在 `Types` 中**，否则插件会被拒绝加载。 |
| `Protocols` | 本插件提供的协议列表，见下。 |

**协议级**（`driversdk.Protocol`，每个协议一份）：

| 字段 | 说明 |
|---|---|
| `Name` | 驱动注册名，也是 `iot_protocol.name`。**全局唯一 —— 现在所有协议都来自插件，所以实际约束是不得与另一个插件（含大小写变体）重名**。同时也必须出现在 `driversdk.Factories` 的键里。 |
| `SerialExclusive` | 该协议的底层连接是否独占串行总线（如 RS-485）。`true` 时采集引擎整轮持设备锁，保证总线上帧不交错；`false` 时只在单次 `Read` 期间持锁。见 [`driver.SerialExclusive`](../backend/sdk/driver/driver.go)。**按协议独立**：Modbus 的 RTU 是 `true`，TCP 是 `false`。 |
| `Description` | 该协议的说明，写入 `iot_protocol.description`；留空回退到 `Meta.Description`。 |
| `FormJSON` | 该协议的配置表单（form-create schema），原样写入 `iot_protocol.form_json`。形状参考随发行版插件的 `main.go`，例如 [`opcua/main.go`](../backend/driversrc/opcua/main.go) 的 `formOcUa`、[`abb/main.go`](../backend/driversrc/abb/main.go) 的 `formRWS`（含 `props` 密码框、`select` 三态、`value` 默认值的写法）。表单里的 `field` 就是 `Connect` 收到的 `protocolJSON` 的键 —— 两边错开一个字母的症状是「用户填了参数但它不生效」，没有任何报错，所以值得用一条测试把两个集合对上（见 [`dlt645/main_test.go`](../backend/driversrc/dlt645/main_test.go) 的 `TestFormsMatchConfigStruct`，以及 [`modbus/main_test.go`](../backend/driversrc/modbus/main_test.go) 的 `TestSerialParamsAreConfigurable`）。**按协议而非按插件** —— TCP 填 host/port，RTU 填串口号。 |
| `Discovery` | 该协议的**局域网扫描发现**能力，见 [§2.6](#26-声明扫描能力discovery)。`nil` = 不支持扫描，该协议不会出现在扫描页的协议下拉里。**可选字段**，所以加了它不会让旧插件失效；但**串口协议（`SerialExclusive`）不得声明它** —— 探测的目标模型是 host/port，表达不了 `comPort`。 |

### 2.2 数据类型

`Types` 里的每一项就是一个 `driver.DataType`，`Decode`/`Encode`/`Format` 照常写 ——
**这三个函数不会离开插件进程**（它们带 `json:"-"`），网关只取 `Name`/`Kind`/`Size`。

这一点成立的前提是**网关从不解码**：驱动返回的 `ReadResult.Value` 已经是格式化好的字符串
（见 [`collector.toRecords`](../backend/collector/task.go)）。所以你必须在插件内完成解码，
把最终字符串放进 `Value`，并填好 `Kind`。

`TypeMap` 的 key 用 [`driver.CommonDataTypes`](../backend/sdk/driver/dataTypeMap.go) 里的规范名
（`Boolean`/`Short`/`Float`/…）时，会出现在配置界面类型下拉的「通用」组并按全局展示顺序排列；
其它名字（如 PLC 风格的 `Int`/`Real`）落在「扩展」组，按字母序。

> 插件**不能新增全局通用类型** —— 那需要改动 `driver.CommonDataTypes` 源码。
> 这是刻意边界：按协议新增类型随意，改全局展示顺序是网关级的事。

### 2.3 日志

用 `driversdk.Logf`（或任何 stderr 输出），网关会按行转到自己的日志：

```go
driversdk.Logf("握手响应 %d 字节", n)
```

**绝不要往 stdout 写东西。** stdout 是帧通道。`Serve` 会把 `os.Stdout` 重定向到 stderr
作为兜底，但如果绕过它直接写原始终端，帧流会错位，网关会判定失步并杀掉进程。

### 2.4 一个插件提供多个协议

一个插件可以提供多个协议，它们**共用同一个 `Scope` 与类型表**，
但各自有独立的 `SerialExclusive`、`Description` 与 `FormJSON`：

```go
driversdk.Main(
    driversdk.Factories{
        "Acme.PLC.TCP":    func() driver.Driver { return &acmeTCP{} },
        "Acme.PLC.Serial": func() driver.Driver { return &acmeSerial{} },
    },
    driversdk.Meta{
        Scope: "acme",
        Protocols: []driversdk.Protocol{
            {Name: "Acme.PLC.TCP",    FormJSON: formTCP},
            {Name: "Acme.PLC.Serial", SerialExclusive: true, FormJSON: formSerial},
        },
        Types:   []*driver.DataType{ /* 两者共用 */ },
        TypeMap: map[string]string{ /* 两者共用 */ },
    },
)
```

这与随发行版各插件的形状完全对应（Modbus 的 TCP 与 RTU 就是一个 scope、两个协议；
Fins 是 `fins` 一个 scope、四个协议）。
`driversdk.Factories` 的键必须与 `Meta.Protocols[].Name` 逐一对应，
**必须在 `Main`/`Serve` 之前配齐**：缺一个会在插件启动时就报错，
而不是等那个协议的设备来采集才失败。

什么时候该拆成两个插件？如果一个协议要独立升级、独立启停，就拆开。
共用类型表与实现的两个协议放一个插件里更省事。

### 2.5 把既有协议搬成插件（唯一路径：自带实现）

**结论已反转，请重读。** 旧版这一节写了 A / B 两条路。现在只剩 B。

#### 为什么 A（复用内置包）已经不可用

旧版的 A 是「空导入内置驱动包 + 用 `driver.Create` 当工厂 +
从 `driver.AvailableTypes` 派生类型表」，它依赖三块地基，而现在三块都没了：

```go
// 旧版 A 的写法，现在已经编译/运行不过去
import _ "iot-gateway/driver/modbus"     // ← 旧的内置包已移除；驱动请用 SDK

d, err := driver.Create("ModBus.TCP")     // ← 返回 unsupported protocol
common, _ := driver.AvailableTypes("modbus")     // ← 返回空
internal, _ := driver.LookupScopeType("modbus", "Short") // ← 返回空
```

逐条对应：

- **内置驱动包已被整体移除。** 协议契约、插件套件与线协议现在都在独立 SDK 仓库
  （本仓库 `backend/sdk/` submodule，Apache-2.0）：`sdk/driver/`（`Driver` 接口 + 注册表 +
  类型表）、`sdk/pluginwire/`（帧编解码 / 方法名 / `RemoteError`）、`sdk/driversdk/`
  （`Main` / `Serve` / `Meta` / RPC 参数）。核心仓库不再保留这些包，宿主直接 import SDK；
  数据库模型与 SDK `DeviceAddress` 的转换在 `backend/driveradapter/`。
  官方驱动在 `backend/driversrc/`（`iot-gateway-drivers` submodule，AGPL-3.0）。
  7 个内置协议（Modbus / Mitsubishi MC / Siemens S7 / OPC UA / Rockwell CIP /
  Omron CIP / Omron FINS）已全部搬成插件；
  此后新增的协议（如 `abb/` 的 `ABB.RWS`）从一开始就是插件，没有「内置版」那一段历史。
- **`driver.Create` 的注册表里只剩插件注册的协议。** 主二进制启动后、插件加载前，
  `driver.Registered()` 返回空 —— 旧版 A 那种「从进程内注册表复用一份现成实现」
  的路径不存在了。
- **内置基线表已清空。** `driver.AvailableTypes("modbus")` / `LookupScopeType` 之所以
  还能查到东西，唯一原因是 `modbus` 这个 scope 现在由**插件自己**在加载时注册进
  运行时扩展层（`driver.RegisterScope`）。而在插件内部查一个**自己即将注册**的 scope，
  是循环依赖：查的时候还没注册，自然什么都查不到。

所以「类型表从哪来」这个问题的答案现在只有一个：**插件包自己带一张**。

#### 搬的时候注意

随发行版的插件全部走这条路，任选一个当范例：`driversrc/modbus/` 自带一份完整实现
（[`modbusdrv/`](../backend/driversrc/modbus/modbusdrv/) 子包 —— 类型表、配置解析、
PLC 地址解析、区间规划、TCP/RTU 客户端、驱动），不导入任何网关内的协议代码。
**若你要写的是一个全新协议**（没有前身），看 [`driversrc/abb/`](../backend/driversrc/abb/)
更直接：它没有「对齐内置行为」这层约束，能一眼看出一个协议插件**至少**要有哪些文件 ——
`config.go`（配置解析）、`address.go`（地址语法）、`types.go`（类型表与 Kind 解析）、
`driver.go`（五个方法）、`metadata.go`（上报），外加 `main.go` 的 `Meta` 与配置表单。

**若协议是从别的项目移植过来的**（本仓库没有它的内置前身），看
[`driversrc/dlt645/`](../backend/driversrc/dlt645/)：实现在 iot-gateway 那边以内置驱动的
形式存在过，逐文件搬过来即可 —— 搬迁作业量最小，但要额外守住一条：**协议名与 scope 沿用
来源项目的原值**（`DLT645.Serial` / `DLT645.TCP` / `dlt645`），两侧的 `device.protocol_id`、
点位地址与类型名才能直接互相迁移。代价是拿不到黄金基线（黄金是「本仓库的内置实现在被移除
**之前**」冻下来的，来源不在本仓库里），验收方式与全新协议相同（§2.5 末尾）。

- **类型真源放在插件包内**（一张 `map[string]*driver.DataType`），
  `Meta.Types` / `Meta.TypeMap` 从它派生。别再查内置 scope —— 那里什么都查不到，
  而查不到的症状是「类型映射静默失效」（前端类型下拉少项，日志里一个字都没有）。
- **协议名与 scope 沿用原值，一个字都不要改。** 这是第二处结论反转（旧版要求
  「必须换个新的」），理由与边界见 [§3.1](#31-scope-沿用原值但不得与另一个插件撞名)。
  沿用原值的收益是**零迁移**：`iot_protocol` 行、`device.protocol_id`、前端表单全都不用动，
  换的只是「谁持有实现」。搬迁来的 7 个插件全部沿用原值（`ModBus.TCP`/`modbus`、
  `Siemens.S7`/`s7`、`Omron.FINS.*`/`fins`、`Mitsubishi.MC.*`/`mitsubishi`、
  `Omron.CIP`/`cip`、`Rockwell.CIP`/`rockwell`、`OPC.UA`/`opcua`），就是靠它。
  协议名与 scope 在插件里都写成 `const` 并用一条 `TestProtocolIdentityPinned` 钉住
  （见各插件的 `main_test.go`），防止手滑改字 —— 全新协议（`ABB.RWS`/`abb`）
  没有原值可沿用，但同样要钉：**名字一旦发布就冻结**，它同时是 `iot_protocol.name`
  与 `device.protocol_id` 的解析目标。
- **日志只能走 `driversdk.Logf`**（stderr）。网关把它按行收进自己的日志、
  当前整条流统一记在 **INFO** 级别（行里没有级别信息，网关无法按行分流），
  所以轮询热路径上的调试日志要在插件侧自己加开关
  （`modbusdrv.Debug` 就是这么来的），否则每轮采集都会刷屏。
- **只实现 `driver.Driver` 的五个方法**。旧内置实现里往往还有一些接口之外的代码
  （Modbus 的写寄存器、`EncodeWriteValue`）—— 插件侧没有对应接口，不必搬。
- **`RangeSig` / `RangeCacheMaxEntries` 仍然从 `github.com/Ellisata/iot-gateway-sdk/driver` 取。**
  区间/规划缓存键是插件与网关**共用的契约**，所以它留在了 `driver/rangeSig.go` 里
  （`RangeSig` 是唯一没被搬走的协议相关代码）。Modbus / S7 / FINS / Mitsubishi
  四个插件照常 `driver.RangeSig(addrs)` 算缓存键，缓存条目上限也用它
  （OPC UA / 两个 CIP 插件没有区间规划这一步，不用它）。别把它和「复用内置协议实现」混为一谈
  —— 前者是必须的、仍然存在的依赖，后者已经不存在。
- **目录名与包名可以不一致，这是刻意的。** 每个插件是 `driversrc/<名字>/main.go`
  加一个实现子包；搬迁时只改 import、不动 `package` 子句，让每个文件的 diff 只剩
  logger 与 import，所以会出现 `driversrc/mitsubishi/mitsubishidrv/` 里是
  `package mitsubishi`、`driversrc/rockwellcip/rockwellcipdrv/` 里是 `package cip`
  这种错位。`main.go` 里必须显式起别名
  （`mitsubishidrv "github.com/Ellisata/iot-gateway-drivers/mitsubishi/mitsubishidrv"`），
  否则标识符是 `mitsubishi` 而不是 `mitsubishidrv`。
  子包目录名仍需唯一 —— 不同插件里的同名包在同一 module 下靠完整路径区分，没问题，
  但插件**目录**名（决定产物名 `iot-driver-<目录名>`）必须唯一且不含层级。
- **别改协议语义**。同一份现场配置在搬迁前后必须采出相同的值，
  所以类型映射、默认值、地址解析优先级都要逐项对齐。

#### 行为等价靠「黄金基线」守住，不靠自觉

这是这条路唯一真正的代价：**仓库里不再有第二份实现可以对照**，
「插件版采出的值对不对」失去了参照物。旧版 §2.5 里那句「用内置版与插件版分别采集再比对」
已经不可能执行 —— 内置版已经不存在了。

等价性因此从「跨实现」改为「跨时间」：内置实现被移除**之前**，用假 PLC 把它采出的结果
冻成黄金文件；之后插件必须复现同一份黄金。

- **黄金基线**在 [`backend/pluginhost/testdata/parity/`](../backend/pluginhost/testdata/parity/)：
  - `addresses.json` —— 全部用例**共享的输入**（点位清单与协议配置），
    生成侧与消费侧读同一份，避免两边各写一份 fixture 悄悄漂移；
  - `<case>.golden.json` —— 逐点位的 `Value` / `DataType` / `Kind` / `Quality`
    （`modbus-tcp`、`s7`、`opcua`、`mc-tcp`、`fins-tcp`、`omron-cip`、
    `omron-cip-batch`、`rockwell-cip`）；
  - `typemap/<scope>.json` —— 该 scope 的类型表（`common` / `extended`）与
    **机器生成的协议名清单**。协议名清单必须机器生成而不能手抄：抄错一个字符，
    用它的设备会被判为「协议不支持」而静默跳过，而插件自带的测试会跟着一起抄错。
- **比对入口**是 [`pluginhost/pluginparity_test.go`](../backend/pluginhost/pluginparity_test.go)
  的一族 `Test<协议>PluginParity`（如 `TestS7PluginParity`、`TestMitsubishiPluginParity`），
  它们编译并拉起**真实的插件二进制**，跑同一份 `addresses.json`，逐项比对黄金；
  共享助手（编译插件、加载、比对）在同目录的 `modbusplugin_test.go` 里
  （`buildPlugin` / `startPluginManager` / `runParityThroughPlugin`），
  每个协议只声明「插件目录 + scope + 用例名」。
- **黄金文件的读写格式**在 [`backend/testutil/parity`](../backend/testutil/parity/parity.go)。
  它刻意做成**普通包而不是 `_test.go`**：插件是 `package main`，无法 import
  `pluginhost` 的测试代码，而黄金文件可能需要在插件侧读（例如把某个插件的用例
  下沉到它自己的包里跑）。**当前仓库里读它的只有 `pluginhost` 的测试** ——
  比对统一从网关侧发起，插件自己的 `main_test.go` 只做 `Meta.Validate()` 与
  协议名/工厂一致性这类自检。
- **网关侧的测试**（`collector` / `service`）需要「某个协议可用」这个前提，
  但它们既不该拉起插件进程，也不该依赖某个真实协议的内容 —— 那是插件自带测试与
  parity 比对负责的事。它们用
  [`backend/testutil/drivertest`](../backend/testutil/drivertest/drivertest.go)
  的 `EnsureSyntheticProtocol()` 自备前提（合成协议 `Test.Synthetic.TCP` +
  合成 scope `testsynthetic` + 桩驱动，全部幂等）。
- **黄金基线的已知局限**：`testutil/fake` 的假 PLC 一律回定长全零数据，所以除 OPC UA 外
  各协议的 Value 恒为 `"0"`/空串。黄金比对能抓住「帧没对上、索引错位、Quality 掉了、
  DataType/Kind 变了」，抓不住「字节序或解码算式改了」—— 后者由随实现一起搬进插件包的
  单测（`parser_test` / `frame_test` / `driver_test` 等）守着，它们与实现同目录、同时被搬，
  覆盖不会丢。

**从别的项目移植过来的协议**（`dlt645/`：来源是 iot-gateway 的内置实现）走的是
上面两条之间的第三种情形 —— 有实现可搬、但**本仓库里没有它的黄金基线**（黄金是「本仓库的
内置实现在被移除**之前**」冻下来的，来源不在本仓库里）。所以它既享受不到 parity 比对，
也不适用「从零设计」的自由：协议名、scope 与类型映射必须沿用来源项目的原值，否则两侧的
`device.protocol_id`、点位地址与类型名就对不上了。验收方式与全新协议相同：
插件能否被正确加载/注销 + 能否跑通完整采集链路，见
[`pluginhost/dlt645plugin_test.go`](../backend/pluginhost/dlt645plugin_test.go)。
行为正确性由**随实现一起搬过来的单测**（`codec_test` / `decode_test` / `address_test` /
`config_test` / `driver_test`）守着 —— 它们与实现在同一目录、同时被搬，覆盖不会丢。

选哪条路？现在没有第二条。想让协议独立演进、随发行包分发、热升级而不用重编网关，
就把实现搬进插件包 —— 这是唯一的选择。

---

### 2.6 声明扫描能力（Discovery）

网关能主动扫本机网段找设备，但**扫什么、怎么扫由协议决定**，因此这是一项插件能力。
完整契约见 [specs/device-scan.md](../backend/specs/device-scan.md)，这里只讲怎么接。

三件事：

**① 在该协议的 `Meta.Protocols[]` 里填 `Discovery`**

```go
{Name: protocolTCP, Description: "Modbus TCP", FormJSON: json.RawMessage(formTCP),
 Discovery: modbusdrv.Discovery()},   // 返回 *driver.DiscoverySpec
```

| 字段 | 说明 |
|---|---|
| `Title` | 扫描页协议下拉里的显示名。必填（空串会被启动期校验拒绝）。 |
| `Description` | 扫描行为说明，展示在前端（如"默认只探站号 1"）。 |
| `FormJSON` | **扫描选项表单**（form-create schema，与 `Protocol.FormJSON` 同构）。用户在扫描页填的内容会原样透给 prober。 |
| `DefaultPort` | 默认探测端口，前端据此回填。 |
| `TargetMode` | `unicast`（逐主机）/ `broadcast`（探测器绑本机地址往子网广播地址发，**一个目标可产出多台设备**）/ `both`。留空按 `unicast`。 |
| `MaxBudgetMs` | **单目标硬预算**，见下。取值必须在 `[500, 60000]`。 |

**② 在 `driversdk.Main` 的第三个参数（`Capabilities`）的 `Probers` 字段里给出实现**

```go
func main() {
    driversdk.Main(factories(), meta(), driversdk.Capabilities{
        Probers: probers(),
    })
}

func probers() driversdk.Probers {
    return driversdk.Probers{
        protocolTCP: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
            return modbusdrv.ProbeTCP(req.Target, req.Options, req.Budget)
        },
    }
}
```

不需要任何扫描能力的插件传零值 `driversdk.Capabilities{}` 即可
（**不是 `nil`** —— 第三个参数是结构体）。**声明了 `Discovery` 却没有对应
prober，插件进程会启动失败** —— 与"每个协议都必须有工厂"是同一条校验，
理由也一样：漏配要在启动时报错，而不是等运维点开扫描页看到"扫不到设备"。

> 为什么是结构体而不是再裸加一个参数：第三种能力（原生枚举）已经可以预见，
> 而 `Main` / `Serve` / `ServeConn` 是本套件对外的三个入口 —— 每加一种能力就得
> 在三个入口重复一遍、并在仓库内所有调用点各改一次。

**③ `ProbeFunc` 要守住五条**

1. **返回 `(nil, nil)` 表示"这不是我的设备"** —— "没扫到"不是错误。
   目标不可达、端口上跑着别的服务、设备不支持某个功能码，都是扫描的正常结果。
   把这类情况当 error 会让一次 /24 扫描产出两百多条"错误"噪声。
2. `error` 只用于**探测本身失败**（选项解析不出来、协议内部错误）——
   它会被前端逐条列出来给用户看。
3. **必须在 `req.Budget` 内返回**。超预算会撞网关侧的 probe RPC 超时，
   并在卡死计数上累加，连续几次会把**整个插件进程连同它下面所有协议、所有设备**
   一起重启。预算不够时的正确做法是**带上已完成的部分结果正常返回**
   （如站号只扫了一半）。
   `req.Budget` 已经取过 `min(本协议声明的 MaxBudgetMs, 网关请求值)`，
   直接用即可；那两条路径的说明见 [specs/device-scan.md](../backend/specs/device-scan.md) §4。
4. **不得分配会话、不得持有跨调用资源**。探测没有 session，调用方永远不会为它调
   `Close` —— 与 [`Ping`](#35-ping-不分配会话但应当复用已有连接) 是同一条约束。
5. 不要 panic。panic 会被 SDK 兜住并降级成一次 RPC 错误 + 一条带栈日志，
   但那次探测就白做了 —— 边界（空切片、越界）自己判。

**结果里的 `ProtocolJSON` 必须能被你自己的配置解析器吃下**：键名与配置结构的
json tag 逐一对上，否则「扫描 → 一键建设备」建出来的设备连不上，而两边都不报错。
建议在测试里直接调一次自己的解析函数把这条钉住
（[`modbusdrv/probe_test.go`](../backend/driversrc/modbus/modbusdrv/probe_test.go) 的
`TestProbeTCPFindsDeviceWithDeviceID` 就是这么做的）。

**预算要按"一次探测包含几次 socket 等待"反推着花。** 若你的协议一次握手要顺序做
若干次带超时的网络操作（S7 就是：TCP 拨号 → ISO-on-TCP 连接 → PDU 协商 → 读设备标识），
那么"每个目标最多花 `connectTimeout`"这种写法在最坏情况下会花掉
`阶段数 × connectTimeout`，用户把超时值填大一点就能让探测突破 `req.Budget` ——
而那正是 §③-3 里那条会重启整个插件进程的路径。正确做法是把每个候选/阶段的时间上限
从**剩余预算**里反推出来（[`s7drv/probe.go`](../backend/driversrc/s7/s7drv/probe.go) 的
`ProbeTCP` 就是按 `剩余预算 / 阶段数` 逐候选收缩的），并用一条断言耗时的测试钉住它
（同文件的 `TestProbeTCPRespectsBudget`）。

五个可参考的完整实现，覆盖五种协议形态：

- [`modbusdrv/probe.go`](../backend/driversrc/modbus/modbusdrv/probe.go) —— 裸发 MBAP 帧读
  FC43/14 设备标识，再用一次 FC3 兜底判定"这是 Modbus 设备"。**适合"一次请求就能判定"
  的协议**，判据是应答的自洽性（协议号 / 事务号回显 / 长度），自己校验。
- [`s7drv/probe.go`](../backend/driversrc/s7/s7drv/probe.go) —— 复用驱动自己的连接握手，
  **适合"必须先建立会话才知道对面是不是我的设备"的协议**。它还示范了两件事：
  ① 协议特有的连接参数（S7 的机架/槽位）做成**候选列表**逐组尝试，并把连上的那一组
  写回 `ProtocolJSON`（否则一键建出来的设备连不上）；② 连不上的原因分两类处理 ——
  TCP 层失败直接放弃该目标（可达性与机架/槽位无关，换候选只是白花超时），
  协议层失败才换下一组。
- [`finsdrv/probe.go`](../backend/driversrc/fins/finsdrv/probe.go) —— **复用驱动自己的
  传输层**（`newTransport` + `client.Read`），完全不另写裸帧。协议本身**没有设备标识**
  时照这份看；它还示范了"结束码错误也算设备"与"候选列表可选（留空走采集同款推导）"
  两处取舍。
- [`omroncipdrv/probe.go`](../backend/driversrc/omroncip/omroncipdrv/probe.go) 与
  [`rockwellcipdrv/probe.go`](../backend/driversrc/rockwellcip/rockwellcipdrv/probe.go) ——
  **广播型发现**：声明 `TargetMode=both`，一条数据报拿回整个网段。适合 EtherNet/IP
  这类有标准发现命令（ListIdentity）的协议；两份**逐段同形，差别只有厂商过滤**
  （47 / 1），但帧的编解码**各持一份**（`driversrc/omroncip/cipcore/` 与
  `driversrc/rockwellcip/cipcore/`）。
- [`mitsubishidrv/probe.go`](../backend/driversrc/mitsubishi/mitsubishidrv/probe.go) ——
  **上面 FINS 那份的最简一档**：MC 3E 帧的网络号 / PC 号 / I-O 号 / 站号在驱动里是
  写死的常量，既不是 `protocol_json` 的字段、也不随目标变化，所以**没有候选参数**，
  一次探测就是一次拨号加一次读取。协议里没有"决定连上看不看得到"的旋钮时照这份看
  —— 上面那条"多阶段握手要按剩余预算反推"也顺带不需要了。
- [`opcuadrv/probe.go`](../backend/driversrc/opcua/opcuadrv/probe.go) ——
  **调协议自带的发现服务，不建会话**：OPC UA 的 `GetEndpoints` 规范允许在"只建立了
  SecureChannel、尚未 `CreateSession`"时调用，所以一次探测是"拨号 → OpenSecureChannel
  → GetEndpoints → 关闭"。两个要点：① **它完全不校验认证**，用户名密码错的服务器
  照样会命中，这一点必须写进 `Description`（用户不会从界面上别的地方看出来）；
  ② 它示范了一条别处没有的**三分法** —— 不是我的设备 / 是但拒绝了我们的安全参数
  （照样列出 + 在 `Extra` 里告警）/ 是。后者的判据是"只有协议对端发得出来的错误"
  （`*uacp.Error` 与服务级 `ua.StatusCode`），而要特别当心那些**本地合成**的
  超时类错误：它既可能是"对端不应答"也可能是"对端静默拒绝"，两者无从区分
  （`opcuadrv/probe_test.go` 的 `TestClassifyProbeErr` 把这条钉住了）。
  协议自带发现服务、或需要把"设备存在但连不上"与"没有设备"分开的，照这份看。

  **这里有一条反直觉的约定，值得单独记下**：协议帧格式是厂商无关的，共用一份
  编解码明显更省事 —— 但**本仓库刻意不跨插件目录 import**。插件是要能独立改名、
  独立删除、独立热升级、独立拆去别的仓库的产物，共享一份源码就把这条打掉了：
  对方目录一改名，这边报一句 `package not found`，而报错的位置离真正的原因
  隔着两个插件。**代价（两份可能漂移）由测试兜底**：两份解析器的用例都由
  **同一套独立实现**的假应答驱动（[`testutil/fake/cipidentity.go`](../backend/testutil/fake/cipidentity.go)，
  照规范另写一遍），哪一边写错哪一边自己会红，不会被"另一份是对的"掩盖。
  **新写 EtherNet/IP 插件时照这条办**：复制你需要的那几个纯函数，别去 import
  别人目录里的包。

### 2.7 声明点位扫描能力（PointDiscovery）

与 §2.6 是**两件相反的事**，别混：

| | Discovery（§2.6） | PointDiscovery（本节） |
|---|---|---|
| 目标 | 网段里的一个 `IP:端口` | **一台已存在的设备**（网关传 `deviceId` 对应的连接配置） |
| 会话 | 无 | **复用那台设备的采集会话** |
| 一次 RPC | 一个目标 | 一整段地址范围 |
| 串口协议 | **禁止**声明，校验会拒 | **可以且应当**声明 |
| 产出 | 一批 `DiscoveredDevice` | 一批 `DiscoveredPoint` + 统计 |

完整契约见 [specs/point-discovery.md](../backend/specs/point-discovery.md)，这里只讲怎么接。

**① 在该协议的 `Meta.Protocols[]` 里填 `PointDiscovery`**

```go
{Name: protocolTCP, Description: "Modbus TCP",
 PointDiscovery: modbusdrv.PointDiscovery()},   // 返回 *driver.PointDiscoverySpec
```

| 字段 | 说明 |
|---|---|
| `Title` | 弹窗标题。必填。 |
| `Description` | **用户理解本功能性质的主要来源**。务必写清"列出的是读得到的地址，不是设备的点位表"，以及你这个协议特有的局限。这段文案会常驻显示在弹窗顶部。 |
| `FormJSON` | 扫描参数表单（form-create schema），原样透给 finder。 |
| `DefaultCommonDataType` | 结果里类型的默认值，必须是本协议 **`TypeMap` 的 key**（`Word`、`Boolean` 这类**通用名**）。写成内部裸名（`word`）会被启动期校验拒绝。 |
| `MaxBudgetMs` | 整段扫描硬预算，`[500, 30000]`。**网关还会再钳到 7000ms**（见 §4）。 |
| `MaxPoints` | 单次最多返回多少点位，`[1, 20000]`。达到即停并置 `truncated`。 |

**② 在 `Capabilities.PointFinders` 里给出实现**

```go
func main() {
    driversdk.Main(factories(), meta(), driversdk.Capabilities{
        Probers:      probers(),      // §2.6，可选
        PointFinders: finders(),
    })
}

func finders() driversdk.PointFinders {
    return driversdk.PointFinders{
        protocolTCP: func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
            return modbusdrv.ScanPoints(req.Session, req.Options, req.Deadline)
        },
    }
}
```

**③ `SessionAccess` 的五条契约（这是本节最容易写错的地方）**

finder **不**直接拿 `driver.Driver`，而是拿 `req.Session`：

```go
type SessionAccess interface {
    Do(fn func(drv driver.Driver) error) error
}
```

1. **别用 `drv.Read` 去扫。** 驱动的 `Read` 是为采集设计的：它读设备的 cfg 做
   区间规划（Modbus 的 `groupAddrsByFC` / `CalcReadRanges` 都以"配置模式的
   `startAddress`/`functionCode`"优先）。配置模式下你会**静默拿到一份错位的值**，
   而照它建出来的点位会**永远取到别处的数据**（在时序库里表现为一条完全合理的
   曲线，看不出任何异常）。所以：拿到 `drv` 之后取**它自己的客户端与配置**，
   直接发请求；并且**先拒绝你协议里"地址含义由配置决定"的形态**。
2. **块与块之间必须重新调 `Do`**。一次 `Do` 覆盖整段扫描 = 把采集卡死几十秒。
   正确形态是主循环每块一次 `Do`，`fn` 内部只做那一段。
3. **每次都重新取驱动。** `Do` 可能换一个实例（热刷新会重建驱动、重连会换
   client），缓存下来的指针读的是一个已经 `Close` 的句柄。
4. **`fn` 里不得调用 `drv.Close` / `drv.Connect`**。复用路径上那是正在采集的
   实例，关掉它等于把设备打成离线。
5. **`fn` 必须尽快返回**，持锁期间采集侧读不到这台设备。

**④ `FindPointsFunc` 要守住五条**

1. **必须在 `req.Budget` 内返回**（`req.Deadline` 是同一件事的绝对形式，
   主循环每块之前判一次）。超预算的正确做法是**带上已完成的部分结果正常返回**
   并把 `PointScanResult.Incomplete` 置位，不是返回 error。
2. **空结果不是错误**。"这个范围里一个地址都读不到"是正常答案 ——
   设备可能只实现了其中一小段。
3. `error` 只用于"这次扫描根本没法进行"。中止时**把已经拿到的部分结果一起返回**。
4. **不得 `Close`/`Connect` `req.Session` 里的驱动**（见上）。
5. 不要 panic。panic 会被 SDK 兜住并降级成一次 RPC 错误（且**会话锁会跟着放掉**），
   但那次扫描白做了。

**⑤ 生成的地址必须能被你自己的解析器还原**

`DiscoveredPoint.Name` 是**直接写进 `device_address.name` 的串**。一个解析不出来的
地址写进去之后，这个点位**永远没有值**（只报一条点位质量报警「地址无效」），
运维得去改配置才恢复 —— 不会波及同设备的其它点位，但也没人会替你发现它写错了。
请用一条往返测试钉住，并且**仍然在前端把地址列设为只读**：点扫产出的是候选清单，
不该让用户顺手改成一份你自己的解析器认不出的写法。

> 这条后果**曾经严重得多**，是设计这条规则时的原始理由：那时 `CalcXRanges` 全量
> 解析、遇第一个非法地址就返回 error，`Read` 整个失败，采集侧据此把这个**配置错误**
> 当成**设备故障**判离线并报**断联**，还跳过该批之后的所有批次。现在 Modbus / S7 /
> FINS / Mitsubishi MC / Omron.CIP / Rockwell.CIP 六个区间（标签）规划器都在规划前
> 把解析不出的地址拆出去（各包 `badaddr_test.go`），但**别据此以为地址写法可以随便**：
> 写坏就是写坏，只是代价从"整台设备"变成了"这一个点位"。

Modbus 的取舍（为什么必须用 IEC `%MW` 而不是传统 `40001` 风格）见
[specs/point-discovery.md](../backend/specs/point-discovery.md) §8。

六个可参考的完整实现：

- [`modbusdrv/pointscan.go`](../backend/driversrc/modbus/modbusdrv/pointscan.go) ——
  **按功能区扫寄存器/位**，示范了 §③-1 里那条守卫（先拒绝配置模式）与
  "整段 → 对半 → 单点"的降级。降级是必需的：设备对**含空洞的区间**会整段拒绝，
  只有拆到单点才能把"哪几个地址真的不存在"摘出来。
- [`s7drv/pointscan.go`](../backend/driversrc/s7/s7drv/pointscan.go) ——
  **按区域（DB/M/I/Q）+ 字节偏移 + 粒度（位/字节/字/双字）扫**，示范了三件 Modbus
  那边没有的事：① 你的协议**可能没有**"地址含义由配置决定"的形态（S7 就没有，
  `StringLen`/`MaxGap` 只影响采集侧的区间规划）—— 那就**不要**照抄那条守卫，
  它是为 Modbus 的具体形态写的；② 错误分类只能靠**字符串匹配**（gos7 把 PLC 的
  项级错误码翻译成一句人话，没有可 `errors.As` 的类型），此时应当只把**明确表示
  "地址/对象不存在"的那几句**判成可降级，其余一律中止 —— 并写清每一句的出处；
  ③ 除了 `deadline`，再加一条**请求数上限**：对一段"整段都读不到"的地址，
  对半降级会退化成一棵满二叉树，光靠 deadline 止损会让耗时随网络延迟漂移。
- [`rockwellcipdrv/pointscan.go`](../backend/driversrc/rockwellcip/rockwellcipdrv/pointscan.go) ——
  **第三种形态：原生枚举**。前两份都是"扫一段地址、问哪些读得到"，这一份是
  "让设备把它的标签表交出来"（CIP Symbol Object）。**如果你的协议是让设备自报点位
  （OPC UA 的 Browse、ABB 的 RWS 信号表），照这份看**；若点位来自**协议自己的一张
  清单**（DLT645 的数据标识字典），看本节最后那份 —— 那份还带采样值，取舍不同。它示范了
  四件前两份遇不到的事：① 布局**不猜** —— 复用既有的第三方实现，而不是照
  手册写一遍请求/应答；② 枚举是"整只对象"的操作，超预算时**拿不到部分结果**，
  此时**报错**比返回空清单 + `incomplete` 好（后者会让人从"一个都没有"得出
  "设备没有点位"）；③ 第三方库可能不设读超时，必须自己用 goroutine + select 兜住
  预算，并让收尾的 `Close` 把阻塞解开；④ 结果不读采样值时，**让
  `AssumedCommonDataType` 留空**（前端据它隐藏「显示 0 值」开关），
  并把每个点位的类型放进 `DiscoveredPoint.CommonDataType`。
- [`mitsubishidrv/pointscan.go`](../backend/driversrc/mitsubishi/mitsubishidrv/pointscan.go) ——
  **同一段地址空间里既有"连续"也有"有空洞"的软元件**，这是前几份都没遇到的形态。
  它示范了三件事：① **单请求点数上限随「软元件类别 × 粒度」变四档**，
  当成一个常数会静默错两档（请求数变 16 倍，或每帧字数翻倍）；②
  **"读到第一个读不到就停"只对单片连续的软元件成立** —— 数字 I/O（`X`/`Y`）与
  链接区（`B`/`W`）按槽位与模块分配，中段可以有空洞，照抄那条捷径会让清单
  **从空洞处静默断掉**，所以它有一张显式的「是否连续」表；③ 结束码**只有白名单
  可选**，且要按**语义**收而不是照一台设备的码表收 —— 同一个"地址范围外"在 QnA
  家族是 `0xC056`、在 iQ 家族是 `0x4031`，漏掉一个的表现是把"这里没有点位"报成
  "扫描失败"（真机踩过）；白名单与黑名单各写一遍测试。若你的协议里软元件的地址
  空间性质是**混的**，照这份看。
- [`opcuadrv/pointscan.go`](../backend/driversrc/opcua/opcuadrv/pointscan.go) ——
  **第四种形态：按层推进的 Browse 枚举**。与上一份同属"原生枚举"，但**三处不能照抄**：
  ① 它是**能拿到部分结果**的（按层推进，停在哪儿就有多少），所以超预算时
  **带 `Incomplete` 正常返回**，而不是像 Rockwell 那样报错；② 它**有分块**，
  因此要守 §6 的锁纪律（一块一次 `Session.Do`）；③ 它的瓶颈是**请求数**，
  于是 Browse 与属性读取都必须批量 —— 退化成"一节点一请求"不会报任何错，
  只是扫描会在预算耗尽时停在半程，症状是"清单莫名其妙断了一半"。
  同目录另有两份值得看的：`pointtype.go`（节点的类型是**另一个节点的 NodeID**，
  要沿逆 `HasSubtype` 上溯，**一层一个请求**覆盖该层全部待解析类型）与
  `pointaddr.go`（渲染出的地址必须能**解析回来逐段比对**才允许产出 —— 这里的
  危险不是"读不到"，而是**解析到另一个节点**：那条曲线看起来完全合理）。
  地址空间是一棵树、且能问出每个节点的类型/名字的协议，照这两份看。
- [`dlt645drv/pointscan.go`](../backend/driversrc/dlt645/dlt645drv/pointscan.go) ——
  **第五种形态：字典枚举，且带采样值**。点位来自**协议自带的数据标识字典**
  （不是设备自报的），而"读一遍"既是判定这块表有没有该数据标识的手段、又顺带取到了值。
  因此它示范了上一份**没有**的两件事：① **结果级 `AssumedCommonDataType` 与逐点的
  `CommonDataType` 同时非空** —— 逐点类型是因为每个数据标识的输出形态由它自己决定
  （表号是 BCD 数字串、状态字是二进制位域、电量是 2 位小数的工程值），结果级那个则
  只是"本次结果带采样值"的载体（前端据它点亮「显示 0 值」开关）。**上一份的"留空"
  不是规矩，别照抄**；② **成帧打包要先核对应答数据域的总长**：一帧里打包了多个
  数据标识时，回显校验只保证"这一项的标识对"，不保证"这一项的数据落在正确的位置"
  —— 设备少回一项的数据，后面每一项的窗口都会整体前移，解出的是**看似合理实为别处**
  的值，而它会被算成"读到了"。这条只有"一帧多项、且各项长度不同"的协议才有。
  另有一处**有意偏离**：它把"设备静默超时"与"设备掉线"分开（先复探一次与 Ping 同一个
  数据标识），因为老表对不支持的数据标识往往干脆不答 —— 照抄前请先确认你的协议
  有没有同样的歧义（并如实说明那条假设需要真机确认）。

**同一个插件可以两种能力都声明**（S7、Rockwell.CIP、OPC.UA、DLT645 都是），也可以只声明一种
（Modbus 的 RTU 只声明 `PointDiscovery`，因为 `Discovery` 对串口是禁用的）。
两条独立校验、两张独立的能力表，互不牵连。
**也可以两种都不声明**（`Omron.CIP` 就只有一个 `Discovery`）—— 见
[specs/point-discovery.md](../backend/specs/point-discovery.md) §3 的"两种合法的少声明"。

## 3. 必须遵守的约定

### 3.1 `Scope` 沿用原值，但不得与另一个插件撞名

> **这条结论已经反转，读者请重读。**
> 旧版本节写的是「`Scope` 必须是全新前缀」，理由是两条：内置驱动包各自用 `sync.Once`
> 缓存了「裸名 → `*DataType`」映射，首次读取后新注册的类型对它们不可见，扩展内置 scope
> 会静默失效；以及 `driver.RegisterScope` 会直接拒绝与内置 scope 撞名。
> **这两条前提现在都不成立了** —— 内置驱动包已整体移除，不存在任何一个
> 「缓存了类型映射的内置驱动包」；`RegisterScope` 里那段「与内置 scope 撞名」的判定
> 也因为基线表被清空而永远不会触发。照旧结论把 scope 改成全新前缀，反而会
> 平白丢掉「零迁移」这个收益（见 §2.5）。

现状（依据 [`driver/dataTypeMap.go`](../backend/sdk/driver/dataTypeMap.go)）：

- `scopeTypeMap` 与 `scopeProtocols` 两张**内置基线表已被清空** ——
  **变量保留、内容为空**。留着空表而不是删掉整段机制，是为了保住一条护栏：
  将来若有人再把某个协议编回主二进制，会先在 `RegisterScope` 这里被挡住，
  而不是让插件与内置实现悄悄共用同一个类型命名空间互相覆盖。
- 读取规则仍是「先查基线、命中即终结」（`scopeTypes` / `lookupScopeProtocol`）。
  基线为空时它等价于直查扩展层，但保留这条规则，将来真有基线条目时插件影响不到它。
- 「插件未加载时 `ScopeOf` 对所有协议都返回空串」这条仍然成立
  （`AvailableTypes` 因此返回空、`NormalizeAddressType` 原样透传）。
  首次扫描在采集引擎之前完成，所以正常启动下这不是问题 ——
  但它的另一面是：**没有插件就等于没有协议**。

所以现在的规则是两条：

1. **沿用原 scope 是允许的，而且对既有协议是必需的。** 7 个内置协议插件
   （`modbus` / `s7` / `fins` / `mitsubishi` / `cip` / `rockwell` / `opcua`）
   全部沿用原值，这正是「`iot_protocol` 行、`device.protocol_id`、前端表单零迁移」
   得以成立的前提。新写一个此前不存在的协议时，自取一个不与其它插件重名的前缀即可。
2. **不得与另一个已注册的插件 scope 相同 —— 含大小写变体。**
   这一条仍然被拒绝，而且它是现实中唯一会踩到的路径
   （`RegisterScope` 里 `strings.EqualFold(existing, scope)` 那段）。
   为什么必须不区分大小写：scope 前缀最终经 `TypeRegistry.ForProtocol` **小写化**后
   拼成 `{scope}.{type}`，所以 `"ModBus"` 与 `"modbus"` 落在同一个类型命名空间里，
   按原样比对会把它们放过去。
   放过去之后的后果是**双向污染**：
   - `TypeRegistry.Register` 是覆盖写，后到的插件会把先到的同名类型顶掉；
   - `extScopes[scope]` / `extScopeProtocols[scope]` 也互相覆盖，
     `AvailableTypes` 给前端类型下拉的清单会变成两者混合甚至缺项；
   - `driver.UnregisterScope` 是**按 scope 整条摘除**的 —— 两个插件共用一个 scope 时，
     谁先消失就把另一个的 scope 一并注销，剩下那个插件的类型映射当场全部失效，
     表现为「设备还在、协议也在，但点位解析不到类型」。
   这几条的共同点是**两个插件的加载日志上明明都写着成功**，作者看不出是自己的问题；
   严重时症状就是「某台设备的点位永远采不到值」。
   协议名同理，也不得与另一个插件的协议名重复（同样含大小写变体）。

> 一句话记忆：scope 该不该换新，判据**不再是「它是不是内置的」**，
> 而是「**它有没有被另一个插件占着**」。

### 3.2 `Read` 的返回值必须与 `addrs` 按索引一一对应

网关的 `toRecords` 依赖索引对齐（与所有驱动的契约一致）。网关侧会逐项校验，违反即报错：

- **数量不符** → 整批采集失败并记 `collector: read device ... failed`，**不会**产生任何记录；
- **ID 张冠李戴**（某个请求的点位没被回、或出现了没请求过的 ID、或同一 ID 回了两次）→ 同样整批报错。

**顺序错位本身不报错**：`toRecords` 在索引对不上时回退为按 `DeviceAddressID` 查名称（§3.3），
所以回错顺序不会把值记到别的点位上 —— 但每记录一次多点位查询，且绕过索引快路径，
海量点位下会明显变慢。仍然应当返回与 `addrs` 同序的结果。

### 3.3 `addrs` 里只有三个字段是有效的

RPC 层为降低传输量做了投影，插件收到的 `po.DeviceAddress` 只有
**`ID` / `Name` / `DataType`** 被填充，其余字段为零值。

地址的具体信息（寄存器号、区域等）一律靠解析 `Name` 得到 —— 这与各插件驱动的做法相同
（例如 FINS 的 `D100`、S7 的 `DB1.DBW0` 都是解析 `Name`）。

`Label`（中文说明）等展示字段由网关侧保留，不参与采集。

### 3.4 `IsConnected()` 的返回值至关重要

这是本设计里唯一会导致**永久静默丢数据**的地方：采集引擎**只在 `IsConnected()` 返回
`false` 时触发重连**，`Read` 报错本身不触发重连。因此：

- 链路断了、驱动已经不可用了，`IsConnected()` 就必须返回 `false`；
- 不要让它永远返回 `true`，否则一旦掉线就再也连不上。

网关侧的代理驱动会在三种情况下把状态置为未连接：驱动级错误应答携带的 `connected`、
RPC 层失败（超时/进程死）、以及插件进程重启（世代号变化）。你只需要如实返回驱动的真实状态。

### 3.5 `Ping` 不分配会话，但应当复用已有连接

`driver.PingDevice` 调用 `Ping` 后**不会调用 `Close`**（「测试连接」按钮走的就是这条路）。
所以 `Ping` 必须自己完成全部清理，不要在 `Ping` 里分配需要显式释放的资源。
SDK 的 `ping` 处理已经对**临时实例**做了 `defer Close()`，但你写在 `Ping` 里的临时 socket 要自己关。

两条不变量必须成立：

1. **不留下需要调用方显式释放的资源**（因为没人会替你 Close）；
2. **不让实例变得不可用** —— 不得 `Close`，不得把 `IsConnected()` 永久打成 `false`。
   （部分驱动的串口客户端在读写失败时会把连接标记为断开，下一轮轮询会自动重连，
   属可接受；"永久"指的是再也连不回来。）

#### 串口驱动应当实现 `driver.ConnectionMatcher`

临时实例继承不到任何已建立的连接，而**串口是独占资源**：采集任务已经打开了 COM1 时，
对它再开一次句柄必然失败（Windows 报 `Access is denied`），把"设备其实可达"误报成错误。

所以 SDK 的 `ping` 处理会先找同协议的活跃会话来复用，判据是驱动自己实现的
`ConnectionMatcher`：

```go
// MatchConnection 报告本实例此刻持有的连接能否直接服务这次测试。
// 纯查询：不得打开/关闭连接、不得修改驱动内部状态。保守返回 false 永远安全。
func (d *yourDriver) MatchConnection(protocolJSON string) bool
```

实现要点：

- **只让串口传输返回 `true`**。TCP 复用没有收益，反而会让这次 ping 白排在一个可能长达
  数十秒的批量读取后面（半死的 TCP 连接 `IsConnected()` 仍可能为 `true`，还会误报失败）。
  > **已知代价（真机实测）**：TCP 不复用就意味着一台正在采集的设备必须能接受**第二条**
  > 连接。设备侧有并发连接数上限时（三菱内置以太网的「开放式设置」常见只开一条，实测
  > `10.62.100.22:10000` 就是如此），第二条连接会在 TCP 层被收下、在协议层永不应答，
  > 表现为"采集正常、测试连接超时"；连续重试之后连 TCP 握手都不再被接受。网关侧会为
  > 这个现象补一句提示（见 §3.7），但**提示代替不了能力** —— 要让这类设备通过测试，
  > 得在设备侧放开连接数。串口那半边没有这个问题：它的"第二个句柄打不开"恰恰靠复用才解。
- **比较必须覆盖所有会改变链路语义的参数**：端口、波特率、数据位、停止位、校验位，
  以及从站号/表号这类已经固化进连接的身份。少比一个就是**假成功** —— 用户把波特率从
  9600 改成 19200 再点「测试连接」，复用了旧连接并报成功，而那个结论与被测配置无关。
- 端口名用 `strings.EqualFold` 比较：Windows 的串口名大小写不敏感。
- 把判据抽成一个私有谓词（如 `matchConfig`），**`Ping` 内部与 `MatchConnection` 共用它** ——
  两处各写一份，迟早会分叉，而分叉的症状正是上面那个假成功。

一个现成的模板见 `driversrc/modbus/modbusdrv` 的 `matchConfig` / `MatchConnection`。

> **已知残留风险**：复用意味着一台被采集的设备的串口会被第二个请求用到。SDK 用会话锁
> 保证帧不会在字节级交错，但**迟到的 ping 应答**理论上可能被下一次同功能码同长度的读取
> 当成自己的应答。触发条件苛刻且会自愈（CRC 失败 → 重连）。`dlt645` 用
> `MarkDirty`+`Drain` 覆盖了该场景，其它串口驱动没有。

### 3.6 串口协议必须使用 `comPort` 字段名

所有串口类协议（`SerialExclusive` 为 `true` 的那些）都必须在 `protocol_json` 里
用 **`comPort`** 这个键放串口名，值是可读的端口名（`COM3`、`/dev/ttyUSB0`）。

为什么这是网关侧的契约而不是各插件内部的事：串口是独占资源，「测试连接」失败时网关
要能回答"这个口当前被哪台设备的采集任务占着"，而网关口拿到的只有 `protocol_json` ——
它不认识任何具体协议，只能靠这个约定好的键取值（`driver.ExtractSerialPort`）。

违反约定的症状很隐蔽：占用提示**永远不出现**，且没有任何日志或报错 ——
`ExtractSerialPort` 取不到值只会返回空串。因此它由 `driver.SerialPortField`
与各插件的表单契约测试（见 §2.1 的 `FormJSON` 一行）共同钉住。

---

### 3.7 以太网协议应当使用 `host` / `port` 字段名

走 TCP 的协议请沿用 **`host`** / **`port`** 这两个键（当前全部以太网插件都已如此），
理由与 §3.6 的 `comPort` 同源：「测试连接」与点位扫描失败时，网关要能回答"这个地址
当前是不是正被某台设备的采集任务连着"，而它不认识任何具体协议，只能靠约定好的键取值
（`driver.ExtractEndpoint`，返回 `host:port`；`port` 是数字还是字符串都认）。

与 §3.6 的**两处不同**，别照抄那一条：

- **违反约定的后果轻一档**：串口是独占资源，取不到端口名等于丢掉了唯一线索；TCP 目标
  不是独占资源，取不到它只是少一句提示（同样是静默的，没有日志）。因此它是**沿用**
  而非新增硬性契约，也没有表单契约测试钉它。
- **分档依据是"链路是否通着"而不是"配了就算"**：同一台 PLC 上挂多条连接是正常的，
  所以措辞分两档 —— `Connected` 为 `true` 说"该设备正在采集"，否则说"当前未连接"。
  **但两档都要提示，不得按 `Connected` 过滤**（初版过滤过，被真机打回）：设备侧那个
  连接槽被一条**已死**的连接占住后（对端断开它不会立刻察觉），网关侧恰恰没有任何
  "正在采集"的设备，而未连接的这台才是那个地址唯一的历史持有者。

提示长这样（真机 `10.62.100.22:10000` 的场景）：

> 设备连接测试失败：mc: ping failed: mc tcp: read header failed: … i/o timeout
> （10.62.100.22:10000 已配置给设备「10.62.100.22」，该设备正在采集；若被测设备只允许
> 一条连接，第二条通常会被拒绝或不应答（表现都是超时）——可先停用该设备再测试，或在
> 设备侧放开连接数）

措辞只陈述事实（这个地址配给了谁、那台设备当前是否连着）加一句可被证伪的推测，
**不断言因果** —— 与 §3.6 的占用提示同一条纪律：真实原因也可能是地址填错、网络不通，
那时这句话依然读得通。

---

### 3.8 探测（probe）与 `Ping` 是一类调用

扫描探测没有会话，约束与 [`Ping`](#35-ping-不分配会话但应当复用已有连接) 同源：

- **不分配会话、不持有跨调用资源** —— 调用方永远不会为它调 `Close`；
- **不改变实例状态** —— 探测失败不得把任何既有连接打成断连；
- **超时由自己兜底**：`ProbeFunc` 拿到的 `req.Budget` 是网关能容忍的上限，
  必须在它之内返回（见 [§2.6](#26-声明扫描能力discovery) 第 3 条）。

与 `Ping` 的区别只有一处：`Ping` 面向**一台已配置的设备**，可以复用正在采集的
连接（串口独占场景必需）；探测面向**一个可能什么都没有的 IP**，因此一律临时拨号、
用完即关，不与采集链路发生任何关系。

**点位扫描（`discoverPoints`，见 §2.7）与 probe 有三处不同**，别把它当成同类：

| | probe | discoverPoints |
|---|---|---|
| 会话 | 一律临时、用完即关 | 优先复用设备自己的采集会话 |
| 复用不到时 | —— | **串口协议绝不回落**到临时实例（会抢占串口句柄把采集打断） |
| 预算 | `Min(声明, 网关请求值)` | 同上，但网关还会再钳到 7000ms（同步 HTTP，见 §4） |
| 取消 | 可取消 | **不可取消** |

## 4. 配置项

`default.yaml` 的 `plugin` 段：

| 键 | 默认 | 说明 |
|---|---|---|
| `enabled` | `true` | 总开关。**出厂即为 `true`** —— 全部协议驱动都由插件提供、主二进制里没有任何协议实现，关掉它等于网关不支持任何协议（设备全部被静默跳过，症状只有一行 WARN）。代价是加载插件等于以网关权限执行该目录下的程序，所以部署方必须保证该目录只对管理员可写（见 §6）。 |
| `dir` | `plugins` | 插件目录。**空或相对路径时相对可执行文件所在目录解析，不是 cwd**（Windows 服务由 `sc create` 注册，SCM 给的工作目录是 `System32`）。不自动创建。 |
| `glob` | `iot-driver-*` | 文件名匹配。 |
| `scanIntervalMs` | `10000` | 目录扫描间隔。 |
| `handshakeTimeoutMs` | `10000` | `describe` 握手超时。 |
| `connectTimeoutMs` | `30000` | `connect` / `ping` 超时。 |
| `readTimeoutMs` | `60000` | `read` 超时。**必须大于 `protocolJSON` 里配置的协议自身超时**，否则网关会放弃插件仍在执行的调用，重试会在慢速串行总线上堆积。 |
| `restartMinDelayMs` / `restartMaxDelayMs` | `1000` / `30000` | 崩溃重启的退避区间。 |
| `maxRestarts` | `10` | 连续失败上限，超过后停止监管。 |
| `disableProtocolRowProvisioning` | `false` | 关闭「依据插件 `describe` 自动维护 `iot_protocol` 行」。 |
| `allowInsecureDir` | `false` | 允许插件目录为其他用户可写（不推荐），见 §6。 |
| `disableHotUpgrade` | `false` | 关闭「替换插件二进制即生效」，回到「只警告、重启网关后生效」，见 [§5.5](#55-热升级替换已加载的插件)。 |

> 布尔字段一律取反命名（`disableXxx`），是为了让配置缺省时的零值等于「启用」——
> 若取名 `enableXxx`，yaml 里没写这一项会被静默解释成 false。

> 配置项**不支持环境变量** —— `InitConfig` 未开启 viper 的 `AutomaticEnv`。
> 容器部署需要覆盖时请挂载一份 `default.yaml`。

### `deviceScan` 段（局域网设备扫描发现 / 设备点位范围扫描）

与 `plugin` 平级，控制**网关主动扫本机网段找设备**这一功能
（与 `discovery` 段方向相反：那个是网关广播"我在哪"）。完整语义见
[specs/device-scan.md](../backend/specs/device-scan.md)。

> **设备地址页的「自动发现点位」复用本段的 `enabled`，不开新开关。**
> 两者是同一个页面族上的两个动作，两个开关会让人只关掉一个，
> 然后在另一个入口重新遭遇他以为已经关掉的功能。
> 点扫的预算**不是**配置项：网关侧把插件声明的 `MaxBudgetMs` 钳到 7000ms
> 再推导 RPC 超时，为的是不超过 `main.go` 里 `srv.Shutdown` 的 10s 宽限期
> （超了的表现是"网关停不掉"）。详见
> [specs/point-discovery.md](../backend/specs/point-discovery.md) §4。

| 键 | 默认 | 说明 |
|---|---|---|
| `enabled` | `true` | 总开关。**出厂即为 `true`**，与 `plugin.enabled` / `discovery.enabled` 同一取舍。需要关掉的理由主要是合规：扫描会主动向整个网段发起 TCP 连接（广播类协议还会发 UDP 广播），某些生产网络对此有明确禁止条款。关闭后 `/deviceScan/*` 全部返回"功能未启用"。 |
| `maxConcurrentProbes` | `8` | 同时在飞的目标探测数（上限 32）。乘上单目标耗时就是扫一个网段的墙钟时间，同时也是插件进程一侧的并发压力。 |
| `maxTargets` | `1024` | 单次扫描的目标数上限（上限 65536）。防手滑用：一个 /16 展开是 65534 个目标。**截断不是静默的** —— 任务快照里带 `truncated` / `requestedTargets` / `plannedTargets`，前端会明确提示。 |
| `jobTTLMinutes` | `10` | 终态任务与结果的保留时长。任务**不落库**，网关重启即丢。 |

> `plugin` 段里**没有**探测超时项。探测超时是**每协议**的属性，
> 由插件在 `DiscoverySpec.MaxBudgetMs` 里声明，网关侧的 RPC 超时按它推导
> （+3s 余量）。理由见 [specs/device-scan.md](../backend/specs/device-scan.md) §4。

> **`default.yaml` 本身是从进程的工作目录（cwd）读的**，不是相对可执行文件 ——
> 与 `plugin.dir` 相反。以服务方式运行时安装脚本会把 cwd 切到安装目录
> （`service_windows.go` 的 `os.Chdir`，systemd 单元的 `WorkingDirectory`），两者一致；
> 但**前台手动启动时请先 `cd` 到安装目录**，否则你编辑的那份 `default.yaml` 会被整个忽略
> （回落到嵌进二进制的默认值，即 `plugin.enabled=true` 与 `plugin.dir=plugins`），
> 日志里也不会有任何提示 —— 而此时若 cwd 与安装目录并非同一个地方，
> `plugins/` 会被解析到另一个位置，表现就是「插件莫名其妙全没了」。

---

## 5. 生命周期与自愈

### 5.1 扫描时的增量策略

| 扫描结果 | 动作 |
|---|---|
| 新文件 | 拉起 → 握手 → 注册 → 自动建 `iot_protocol` 行 → 触发采集配置热刷新 |
| 指纹与内容都未变 | 无操作 |
| 指纹变了但内容没变 | 记一条日志，**不重启驱动**（`touch`、备份工具、重复拷贝都会走到这里） |
| **二进制被替换** | **热升级**，见 [5.5](#55-热升级替换已加载的插件)；`plugin.disableHotUpgrade=true` 时退回「只警告，重启网关才生效」 |
| 文件消失 | 注销协议与类型映射、停掉进程；**`iot_protocol` 行保留** |

「删除不删行」是因为 `iot_protocol` 行是运维数据（表单可能已被现场手工调整），
且既有设备按 `protocol_id` 引用它；删行会让采集引擎每轮为每个设备刷「协议找不到」的警告。
需要清理时请人工删除该行。

**但这条策略带来一个插件化之后才存在的失效模式，值得单独说清楚：删掉插件文件 ≠ 协议从界面上消失。**
`unregister` 只摘运行时能力（`driver.Unregister` 注销协议 + `driver.UnregisterScope`
注销 scope 与类型映射，见 [`pluginhost/adapter_driver.go`](../backend/pluginhost/adapter_driver.go)），
**刻意不动 `iot_protocol` 行**，于是：

- 前端协议下拉里这一项**还在**（下拉读的是表，不是运行时注册表），看界面完全正常；
- 使用它的设备在采集引擎建任务时被逐个跳过，日志是
  `collector: device <设备名> protocol "<协议名>" unsupported, skip: driver: unsupported protocol ...`
  （见 [`collector/task.go`](../backend/collector/task.go)），
  外加一行（无任何受支持设备时）`collector: no devices with supported protocol driver, ...`；
- 插件侧则记 `pluginhost: 插件 <路径> 已移除，注销协议 <协议名>；使用这些协议的设备将被跳过，
  iot_protocol 行保留以便恢复或人工清理`。

也就是说，**「协议还在下拉里」不再是「协议可用」的证据**。排查「设备采不到值」时，
要同时看 `pluginhost` 的加载日志与 `collector` 的 skip 日志，而不是只看前端 ——
前端会让你觉得一切都好。

> 反向的坑不存在：把文件放回 `plugins/` 只会让协议重新可用，
> `iot_protocol` 行与各设备的 `protocol_id` 都还在，**不需要任何修数据**。
> 这正是「删文件不删行」换来的一点好处。

**零协议时的告警。** 协议全部来自插件，所以「一个协议都没注册上」是
**能启动但什么都不做**的状态 —— 网关照常监听、前端照常能建设备，
只是每台设备都在采集时被静默跳过，比启动失败更难发现。`Manager.Start()` 因此在几处
显式喊话（见 [`pluginhost/manager.go`](../backend/pluginhost/manager.go) 的 `Start`）：

| 情形 | 日志（级别 / 措辞） |
|---|---|
| `enabled=false`，但目录里有匹配 `glob` 的文件 | WARN `pluginhost: 外部协议插件未启用（plugin.enabled=false），但目录 <dir> 里有 <n> 个匹配 <glob> 的文件 —— 网关将不支持任何协议，所有设备都会被跳过。启用后重启即可。` |
| `enabled=false`，目录里也没有插件 | WARN `pluginhost: 外部协议插件未启用（plugin.enabled=false）且目录 <dir> 里没有插件，网关将不支持任何协议（协议驱动全部由插件提供，主程序已不含内置实现）` |
| `enabled=true`，但一个协议都没注册上 | WARN `pluginhost: 目录 <dir> 里没有成功加载任何协议驱动，网关将不支持任何设备采集。请确认 plugins/ 下存在 iot-driver-* 可执行文件（发行包中随附），且其平台与网关一致。` |
| `enabled=true`，且有协议注册成功 | INFO `pluginhost: 已加载 <n> 个协议：<协议名列表>`（另有每个插件的 `已加载插件 ...（协议 ..., scope ..., 版本 ..., sha256 ...）`） |

最后一行那条 INFO 是排查「到底加载了几个协议、分别是哪些」最快的入口 ——
它列的是 `driver.Registered()` 的全部协议名（含各插件提供的每一个协议），
不用去数文件、也不用猜哪个文件加载失败了。

变更检测用 `(大小, 修改时间)` 做「是否可能变了」的信号，再用 SHA-256 确认
「是否真的变了」—— 每 10s 对每个插件算一次内容哈希是纯浪费，而只看指纹又会让
一次 `touch` 白白重启该插件下的所有设备。

### 5.2 进程崩溃自愈

插件进程崩溃后：

1. `IsConnected()` 立刻变 `false`（进程下线）；
2. 采集引擎既有的 1s→60s 重连退避接管，下一轮尝试 `Connect`；
3. 监管同时按 1s→2s→4s→…→30s 的退避重新拉起进程；
4. 进程就绪后 `Connect` 成功，恢复采集。

进程连续失败超过 `maxRestarts` 次后停止监管（恢复需重启网关，或移除并重新放入插件文件）。

> 退避 1s→2s→4s→…→30s 的归零条件是「进程连续存活超过 60s」，不是「起来过」。
> 反复"起来就崩"的插件因此会一路退避到 30s（1+2+4+8+16+30×5 ≈ 181s，约三分钟）
> 才会被判放弃 —— 这正是 `maxRestarts` 该有的作用。

**崩溃重启不是一次热升级。** 监管每次拉起进程前都会复核磁盘上的内容哈希（加载 / 升级时记下的
那个），对不上就不拉起、只记一条：

```
driver plugin iot-driver-x: 磁盘上的插件二进制已被替换（期望 sha256 …, 实际 …）；
已停止按崩溃重启拉起，等待目录扫描热升级（plugin.disableHotUpgrade=true 时需重启网关）
```

随后把"文件变了"交回目录扫描 —— 热升级会做完整的注册与记账。理由是：照旧拉起的进程会用
**它自己的** `Meta`（协议名、类型表）服务，而网关的运行时注册表还是上一版，相关协议会全部
报 `unknown protocol`；`plugin.disableHotUpgrade=true` 时更会静默换上未经记账的新版本。

因此**正在崩溃循环时替换二进制**的观感是：先看到这条告警、插件短暂下线，随后热升级把它正常
拉起来（默认 ≤10s 一次扫描）。这是刻意的，不需要人工干预。

### 5.3 卡死检测

「进程还活着但不再响应 RPC」是比崩溃更隐蔽的失败模式：`IsConnected()` 会永远停在 `true`
且 `Read` 永远超时，形成无恢复的活锁。监管每 5s 巡检一次，连续多次 RPC 超时且长期无成功调用
即判定卡死，杀掉并重启进程。

**计数只认真正的超时，不认主动取消。** 这对探测功能是必需的：取消一个局域网扫描
会同时掐掉在飞的全部探测，若无条件计数，一次取消就能顶满阈值；而"一台还没建设备的新网关"
（运维第一件事就是扫描）上，该插件进程除探测外没有任何成功调用 —— 看门狗随即杀掉进程，
该插件下所有协议、所有设备一起断。

因此写 `ProbeFunc` 时**不要试着忽略预算**：真超时仍会计入，
而一次把插件进程带走的代价远大于"这一台设备没扫到"。

### 5.4 防孤儿进程

四层保障，按优先级：

1. **Windows：Job Object 连坐终止** —— 插件进程被放进一个带
   `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 的 Job，持有它的句柄就是网关进程本身。
   网关**无论以何种方式终止**（含 `TerminateProcess` 强杀），内核都会关闭该句柄
   并连带终止 Job 内的全部插件进程。这是唯一不依赖插件配合的一层。
2. **插件读到 stdin EOF 即退出** —— 父进程消失时管道关闭，插件据此退出。
   覆盖 Unix（无第 1 层），也是 Windows 上 Job 创建失败时的退路。
3. **正常停机**时先发 `shutdown` 请求，宽限 2s 后强杀；
4. `cmd.Wait()` 回收进程。

**第 2 层成立的前提是插件真的在读 stdin。** 用 `driversdk.Serve`/`Main` 注册的插件
（包括 `driversrc/` 下的全部现成插件）天然满足。若插件在**调用 `Serve` 之前**
就卡死（例如卡在自己的初始化里），它不会注意到父进程消失 ——
Windows 上由第 1 层兜住，Unix 上没有等效的安全机制（见下），会留下孤儿。

> 因此仍建议：把耗时自检放在 `Serve` **之后**（放进 `Connect`，或 `Serve` 之前只做快速校验），
> 这样无论哪个平台 stdin 都一直有人在读。

> Unix 侧为何不用 `Pdeathsig`：它绑定的是「创建子进程的那个**线程**」而非进程，
> 而 Go 的 goroutine 会在 OS 线程间迁移 —— 线程一旦被运行时回收，内核就会给插件
> 发 `SIGKILL`，**即使网关还活得好好的**。误杀比泄漏严重，故不采用。

> **Windows 注意**：`Process.Kill` 不杀进程树，**插件不得自己派生常驻子进程**，否则会泄漏。

### 5.5 热升级（替换已加载的插件）

用新编译的二进制**覆盖**掉已在运行的那个（文件名不变），等一次扫描（默认 10s），
新版本即上线 —— 不用重启网关，也**不用重建采集任务**。

```
pluginhost: 已热升级插件 iot-driver-modbus.exe（第 1 次，版本 1.0.0 → 1.0.1，
              sha256 3f9a1c0d2b44 → 7e01bb52c9a8，世代号 2）
```

升级过程分两个阶段，**新版本起来之前完全不碰正在运行的旧实例**：

1. 拉起新进程 → `describe` 握手。失败就到此为止，旧版本毫发无损。
2. 比对注册面（见下）→ 退役旧进程 → 把驱动切到新进程。

**为什么其它协议不受影响。** 采集引擎只在 `IsConnected()` 返回 false 时才重连；
换代时新进程的世代号严格大于旧进程，于是所有既有驱动实例立刻报告未连接，
采集引擎用既有的 1s→60s 退避自行重连到新进程。整个过程不调用
`collector.Refresh()`，因此**不会**停掉其它驱动、也不会重开 RTU 串口
（RTU 现在也由 Modbus 插件提供，`SerialExclusive` 的锁粒度同样是建任务时固化的）。

**注册面比对与是否重建采集任务的对应关系：**

| 新版本改了什么 | 动作 |
|---|---|
| 只改了实现 / 版本号 / 表单 / 说明 | 换进程即可，**不触发任何刷新** |
| 数据类型、`TypeMap` | 重注册类型表（查询时现算，立刻生效），**不刷新** |
| `Scope` | 重注册 scope，**不刷新** |
| 协议名集合 | 重注册驱动 + **重建采集任务**（决定哪些设备能建出驱动） |
| `SerialExclusive` | 重注册驱动 + **重建采集任务**（锁粒度建任务时就固化了） |
| `Discovery`（扫描能力） | 换进程即可，**不触发任何刷新** —— 它不参与建任务 |

后两行会走一次 `collector.Refresh()`（防抖 2s），那一次会停掉全部驱动 ——
但这是必要的代价：不重建的话，本该整轮独占串行总线的协议会以「只锁单次 Read」
运行，RS-485 上的帧会交错。

> **`Discovery` 不进「注册面」是刻意的，但有一条后果要记住**：一份**只改了
> `Discovery`** 的新二进制升级后不会触发重注册，因此网关侧**不能把扫描能力缓存
> 成一张表** —— 那会停在旧值上，症状是"插件明明改了扫描配置、前端却一直显示旧的"，
> 而两边日志都写着成功。当前实现是每次现算（遍历已加载插件读它们的 `Meta`），
> 所以一致性自动成立。回归测试：
> [`pluginhost/discovery_test.go`](../backend/pluginhost/discovery_test.go) 的
> `TestDiscoverySpecsRefreshAfterHotUpgrade`。

**失败与重试**

新二进制起不来（握手超时、`Meta` 非法、协议名/scope 与其它插件撞名等）时，**旧版本继续运行**，
`Manager.Status()` 里对应条目的 `lastUpgradeError` 会记下原因。注意此时 `up` 仍为
`true`（旧版本还在服务），只看 `up` 无法判断磁盘上的新版本有没有生效。
（`Status()` 目前只接在 `AppDependencies.DriverManager` 上，尚无 HTTP 接口暴露，
因此实际主要靠日志里的 `热升级 ... 失败` 一行。）

同一个坏文件不会被反复重试。抑制表 `Manager.failed` 是**按「文件指纹 → 失败」记在内存里**的，
所以逃逸口有三个：把改好的文件重新拷一份进去（内容变了，SHA-256 与指纹自然变）、
`touch` 一下文件（指纹变、内容未变时 `upgrade` 会用同内容短路识别为无操作，
但失败记录已经被清掉）、或者**重启网关**（内存表随进程消失）。
三条路等价，选最顺手的即可。

> 注意这套抑制只在**同一版本**内生效：指纹是 `(大小, 修改时间)`，
> 所以「重新拷贝一份一模一样的坏文件」也可能因为 mtime 变化而被重试一次 ——
> 重试一次的代价是拉起一个进程 + 等满一个握手超时（默认 10s）。

**注意**

- **备份文件不能留在插件目录里且匹配 `iot-driver-*`。** 它会作为第二个插件被加载，
  并因 scope 与协议名都被第一个占着而被拒。因为注册顺序是「先 scope、后驱动」
  （见 `Manager.register`：scope 必须最先注册，否则非法插件会先把类型表覆盖掉），
  实际报出来的是**第一条**：
  `pluginhost: 加载 ... 失败: 注册类型 scope: driver: scope "modbus" is already registered (as "modbus")`
  —— 而不是旧版本文档里写的 `driver: protocol already registered`
  （那条只在 scope 相同而协议名集合不同的畸形情形下才会出现）。
  这一版不会被反复重试，但网关**每次重启**都会再撞一次（失败指纹只存在内存里）。
  备份请放到子目录或插件目录之外。
- **已有 `iot_protocol` 行的表单不会被升级覆盖**。供给策略是「只插入、只补空、绝不覆盖
  非空值」（见 §5.1 与 `provision.go`）：`form_json` 为空或 `{}` 的占位行会被插件表单填上，
  运维手工调过的非空表单则原样保留。要用插件的新表单，走接口
  `ProtocolService.ResetProtocolForm`（「恢复默认表单」，见 §7 排错）—— 
  **不要靠删行**：删掉的是一行运维数据，且下次供给会把它当成全新协议重新插入。
- **替换手法因平台而异**（下面两条均为实测结论，非推断）：

  | 操作 | Windows 11 | Linux |
  |---|---|---|
  | 原地覆盖 | ❌ `used by another process` | ❌ `Text file busy`（ETXTBSY） |
  | `cp` 覆盖 | ❌ 同上 | ❌ `cp: cannot create regular file` |
  | 拿新文件 `rename` 覆盖上去 | ❌ `Access is denied` | ✅ **成功** |
  | 路径是否出现空窗 | 走两步则**会** | **从不** |

  **Linux：一步原子替换。** 把新二进制先放到插件目录里一个**不匹配 `glob`**
  的名字下（同目录才在同一文件系统上，`rename` 才是原子的），再 `mv` 覆盖：

  ```bash
  go build -o plugins/new-modbus ./driversrc/modbus
  mv plugins/new-modbus plugins/iot-driver-modbus      # rename(2)，原子
  ```

  旧进程照常运行，只是它的 `/proc/<pid>/exe` 会变成 `...(deleted)` ——
  目录项被重指向，inode 因仍被进程引用而存活。路径全程存在，**不会**被扫描
  误判成「插件被移除」。

  ⚠️ 临时名不能以 `iot-driver-` 开头（`iot-driver-modbus.new` 也匹配
  `iot-driver-*`），否则它会被当作第二个插件加载并因 scope / 协议重名被拒。

  **Windows：必须两步。** 先把新二进制准备在插件目录之外 → 把在跑的
  `iot-driver-x.exe` 改名挪到插件目录之外（子目录也可以，`glob` 不递归）→
  再把新二进制拷到原路径。

  ⚠️ **两步之间原路径是空的。** 若 10s 一次的扫描恰好落在这一瞬间，网关会
  先判定「插件已移除」（注销协议、停进程、触发一次采集刷新），下一轮再当作
  新插件加载 —— 功能上会恢复，但走的是 remove + add 而不是热升级。窗口通常
  只有几十毫秒，概率很低；介意的话把两条命令写成一行快速执行。
- 一次升级在最坏情况下会占用扫描协程约 `handshakeTimeoutMs + 2s + 2×2s`；
  常见情况远低于 1s。扫描间隔 10s，不会因此丢轮次。
- 关掉这个能力：`plugin.disableHotUpgrade: true`（见 §4），行为退回
  「检测到替换只记一条警告，重启网关后生效」。

---

## 6. 安全

**`plugin.enabled: true` 等价于允许网关以自身的 OS 权限执行 `plugins/` 下的任意可执行文件。**
插件是与网关同权限的**可信代码**，不是沙箱内的东西。

因此（注意第 1 条是**结论反转**，旧版本文档写的是「保持默认关闭」）：

- **出厂就是启用（`plugin.enabled: true`），且这不该被当成「可选优化」。**
  全部协议驱动都由插件提供、主二进制里没有任何协议实现，关掉它等于网关不支持任何协议
  —— 设备会被逐个静默跳过（见 §5.1 的零协议告警）。所以这里的取舍不是
  「要不要加载插件」，而是「**要不要让产品能工作**」；能做的只有把配套的安全边界补齐
  （目录权限、审计哈希、环境变量隔离，见下），而不是把开关关掉。
  旧的「默认关闭」结论建立在「插件只是内置驱动之外的一种可选扩展」这个前提上，
  那个前提已经不成立。
- **插件目录只对管理员可写。** 既然默认为启用，这条从「建议」升级为**部署前提**。
  Unix 下网关会拒绝世界可写的目录（除 `allowInsecureDir: true`）；
  Windows 的 stat 权限位无意义，网关只记警告、不做 ACL 校验，需由部署方自行保证。
- 候选文件经符号链接解析后的父目录必须等于插件目录，防止在目录里放指向 `/bin/sh`
  或 `cmd.exe` 的链接。
- 子进程环境变量是从零构造的（不是 `os.Environ()`），**不包含 `IOT_GATEWAY_JWT_SECRET`**
  等密钥类变量。
- 每个插件的 SHA-256 在加载时记入日志，并可通过 `Manager.Status()` 查询，作为审计线索。

---

## 7. 排错

**插件没被加载**

- 确认 `plugin.enabled` 是 `true`（出厂值，见 §4），且日志里有
  `pluginhost: 外部协议插件已启用，目录 ...` —— 注意核对日志里打印的**目录绝对路径**，
  相对路径是相对可执行文件解析的。若看到的是「外部协议插件未启用」那两条 WARN，
  问题就在开关或目录（见 §5.1 的告警表）。
- 确认文件名匹配 `glob`（默认 `iot-driver-*`），且平台与网关一致
  （Linux 包里的插件不能给 Windows 网关用，反之亦然）。
- 看有没有 `pluginhost: 加载 ... 失败: ...`。常见原因：**协议名或 scope 与另一个插件
  重名（含大小写变体）**、`TypeMap` 指向了未在 `Types` 中声明的类型、`APIVersion` 不匹配。
  注意「协议名与内置协议重名」「`Scope` 撞内置」这两条**旧原因已经不可能发生**
  —— 内置协议实现与内置基线表都已清空（见 §3.1）。
- 加载全部成功后应有一条 `pluginhost: 已加载 <n> 个协议：...`，先看它。
  若一个协议都没注册上，会看到 `pluginhost: 目录 ... 里没有成功加载任何协议驱动 ...`。
- **前端协议下拉里有这一项，不代表协议可用** —— 插件文件消失时 `iot_protocol` 行会刻意保留，
  设备却是被跳过的。见 §5.1「删掉插件文件 ≠ 协议从界面上消失」。

**扫描页里一个协议都没有 / 点了扫描什么都没扫到**

- **协议下拉是空的**：说明没有任何已加载的插件声明了扫描能力。
  去插件列表确认带扫描能力的插件（如 `iot-driver-modbus`）已加载；
  插件没声明 `Discovery` 的协议**不会**出现在这里，这是设计如此，不是 bug。
- **`deviceScan.enabled=false`**：页面顶部会有一条明确的告警，`/deviceScan/*` 全部
  返回"功能未启用"。
- **扫完发现 0 台**：先去 `pluginhost` 的日志确认探测确实被执行了。常见原因有三：
  ① 端口填错了（默认是插件声明的 `DefaultPort`，现场常被改成别的）；
  ② 目标其实是 Modbus 之外的东西 —— 那时探测返回的是"没有设备"而不是错误；
  ③ **站号**：默认只探 1 号站，现场设备若不是 1 号站就要在扫描参数里显式写。
- **提示"只扫了前 N 个地址"**：所选网段的目标数超过了 `deviceScan.maxTargets`
  （默认 1024，一个 /16 展开是 65534 个）。缩小网段范围或调大该值。
- **任务突然提示"已过期"**：扫描任务**只在内存里**保留 `jobTTLMinutes`，
  网关重启即丢。重新扫描即可。

**日志里出现「握手失败」或 `frame desync`**

`握手失败` 是网关侧的措辞（`process.go` 的 `握手失败: %w`）；帧流损坏那条在插件侧是英文的
`driversdk: frame desync (plugin wrote to stdout?)`，经 `IsConnected()`/`Read` 的 RPC 错误
冒到网关日志里。两者通常都是插件往 stdout 写了非帧数据 —— 检查是否有绕过 `driversdk.Logf`
的 stdout 输出（`fmt.Println`、第三方库的默认输出、`log` 包未重定向等）。

**替换了二进制但没生效**

- 看日志里有没有 `已热升级插件 ...`。没有的话：
  - 搜 `热升级 ... 失败`，后面跟的就是原因；同一个坏文件不会被反复重试，
    逃逸口是重新拷贝一份、`touch` 一下、或重启网关（详见 §5.5「失败与重试」）。
  - 确认不是只改了 mtime（`touch`）—— 内容没变时网关**刻意不重启驱动**，
    日志里是「文件属性已变化但内容未变」。
  - 确认 `plugin.disableHotUpgrade` 不是 `true`。
- 升级成功了但**前端表单还是旧的**：这是供给策略使然（从不覆盖已有行），
  不是故障。见 §5.5「注意」。
  **动作**：在协议列表里对该协议点「恢复默认表单」才会采纳插件当前声明的表单。
  行动信号是启动日志里那条
  `pluginhost: 协议 X 的 iot_protocol 行与插件自带的表单不一致，保留库内值...` ——
  插件更新了表单（比如给 `ModBus.RTU` 补上了波特率/校验位），但库里还是旧的那份，
  界面上不会出现新字段。

**「测试连接」失败**

- **`Access is denied` / 端口被占用**：串口是独占资源，同一时刻只能被一个连接打开。
  错误后面现在会跟一句
  `（串口打开失败：请核对端口名是否正确，或该端口已被其它程序占用）` ——
  先照着它查，再分两种情况：
  - **那句提示后面还跟着「`COM1` 当前由设备「xxx」的采集任务占用…」**：占用者就是本网关
    自己的采集任务。正常情况下 SDK 会先复用那个会话（见 §3.5），出现这条说明复用没生效：
    - 该协议**跨了插件进程**（ModBus RTU 占着 COM1，却在测 DLT645.Serial）——
      插件之间互相看不见，只能失败。停用那台设备再测即可。
    - 该驱动**没有实现 `ConnectionMatcher`**（§3.5），或实现里比较的参数与 `Ping`
      内部不一致。第三方插件请核对这两点。
  - **只有那句提示、没有点名任何设备**：端口被**网关之外**的程序占着（串口调试助手、
    Modbus Slave 之类的仿真软件），或者端口名写错了。用
    `[System.IO.Ports.SerialPort]::GetPortNames()`（Windows）/ `ls /dev/tty*`（Linux）
    核对端口名，并关掉占用它的程序。
    注意网关侧**故意不猜**这种情况 —— 它只能看到自己的设备，没有依据就点名等于误导。
    另：串口工具的"共享模式"设置（有些软件会以共享方式打开）会让端口**看起来能用**，
    但两边的收发会互相串台，症状是随机的 `exception`/CRC 错误而不是明确的拒绝。
- **`Access is denied`，但占用者是一台正在做长批量读取的设备**：那次 ping 没能在
  3 秒预算内拿到目标会话的锁（对方正在读取），于是跳过了复用、回落到临时实例，
  而临时实例撞上了仍被持有的端口。提示里的设备名就是它。稍后重试，或先停用该设备。
  这个预算本身是**止损**：不设的话这次调用会一直挂到网关侧的 `connectTimeoutMs`
  （默认 30s），而连续超时可能触发卡死检测把整个插件进程重启（该插件下所有协议、
  所有设备一起断）。超时后**刻意回落临时实例而不是直接报错** —— 拿不到锁时无从
  判断那个忙会话是不是目标端口，一律报"忙"会让「测 COM2 时因为 COM1 在读取而失败」
  这种假失败出现。
- **成功得太可疑**：改了波特率/从站号/表号之后点「测试连接」仍然报成功，
  多半是驱动的 `MatchConnection` 漏比了那个参数，复用了另一条链路（§3.5）。

**「自动发现点位」相关**

- **「地址写法看不懂」**：Modbus 生成的是 IEC 61131-3 形式（`%MW100` = 保持寄存器
  偏移 100、`%IW100` = 输入寄存器、`%MX100` = 线圈、`%IX100` = 离散输入），
  **全是 0-based**，与表单里的「起始地址」同基准。
  **不要**把它改成传统的 `40001` 风格：传统写法在满量程、差一、功能区三处都会坏，
  而一个解析不出来的地址写进点位后**这个点位永远没有值**（只报「地址无效」，
  改配置才恢复）。
  完整论证见 [specs/point-discovery.md](../backend/specs/point-discovery.md) §8。
  地址列在前端是**只读**的，这一点是有意的。
- **报「设备正忙」**：采集正在读取那台设备，而扫描需要它的会话（串口独占时那是唯一
  可行的路径）。SDK 最多等 5 秒拿锁，等不到就报这条 —— **不会**回落到临时实例，
  因为那会打开同一个串口的第二个句柄、把采集打断。先停用该设备再扫。
- **报「未找到可复用的串口连接」**：该串口协议有会话，但没有一条是本次要连的那个
  端口（端口配给了别的设备、或设备掉线但旧句柄仍占着端口）。同样**刻意不回退**：
  一次长扫描持有第二个句柄几十秒，采集侧下次重连就会 `Access is denied` ——
  现象是「点一次扫描把设备打成离线」。
- **结果比预期少 / 提示「预算耗尽未扫完」**：网关侧把插件的预算钳到 7000ms
  （为了不超过 `main.go` 的 10s 关机宽限期），RTU 大范围扫描会被截断。
  缩小范围分几次扫即可。这与「结果已达上限」是**两个不同的提示**，别混。
- **报「配置模式」**：该设备的 `protocol_json` 里带 `startAddress > 0`。
  那种设备上地址的含义由配置决定，扫描会给出**错位的值**，因此一律拒绝。
  改用自动模式（清掉 `startAddress`）后再扫。

**设备采集不到数据**

- **先排除「协议整体不可用」这一类。** 插件没加载、插件文件被删、或 `plugin.enabled`
  被关掉时，症状都是：前端协议下拉正常、设备列表正常、健康检查正常，
  只有每台相关设备在采集时被静默跳过。搜索：
  - `collector: device <名字> protocol "<协议名>" unsupported, skip: ...`（每台设备一行）；
  - `collector: no devices with supported protocol driver, ...`（一个都不剩时）；
  - `pluginhost:` 打头的加载/移除/未启用日志（见 §5.1 的告警表）。
- 排除之后再看驱动自身：确认 `Read` 的返回值与 `addrs` 对得上（数量或 ID 不符会让整批
  采集报错，见 §3.2）；确认 `IsConnected()` 的语义正确（§3.4）；
  确认点位类型映射到了插件确实注册过的内部类型（映射表里写了、注册表里没有时，
  类型会从下拉里**静默消失**，见 §2.2）。
- **「某台设备的点位永远采不到值，而两个插件的加载日志都写着成功」** ——
  这是两个插件共用同一个 scope（含大小写变体）的典型症状：类型映射表被后来者覆盖、
  前端类型下拉缺项、甚至一个插件消失时把另一个的 scope 也注销掉。
  判据、机制与检查方法见 §3.1 第 2 条。
  先确认 `pluginhost: 已加载 <n> 个协议：...` 里列出的协议数对不对，
  再确认这些协议各自的 `driver.ScopeOf(协议名)` 是否两两不同。

**测试连接报错**

`Ping` 走的是独立路径，与 `Connect`/`Read` 无关。确认 `Ping` 自己完成了资源清理（§3.5）。
