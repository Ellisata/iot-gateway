// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-omroncip 是欧姆龙 EtherNet/IP (CIP) 协议的进程外插件实现。
//
// **本插件是该协议在网关里的唯一实现**：内置的 driver/omron/cip 已移除，
// 连它依赖的共享封装层 driver/cip（会话管理与 Common Packet Format）也已
// 一并搬迁进本插件的 ./cipcore 子包，主二进制里不再有 CIP 协议代码。
// 协议名与 scope 沿用内置时代的原值（Omron.CIP / cip），因此既有
// iot_protocol 行、device.protocol_id 与前端表单都不需要迁移 —— 现场零迁移，
// 换的只是「谁持有实现」。
//
// 本插件自带全部协议实现（见 ./omroncipdrv）：类型表、配置解析、标签寻址、
// 标签分组、0x4C 数据表读取与 0x0A 多服务批量读、TCP 客户端与驱动都在插件进程内；
// 厂商无关的 EtherNet/IP 封装层在 ./cipcore。本文件只做三件事：
//
//  1. 声明协议的名字、说明与配置表单；
//  2. 把 omroncipdrv 的类型表与类型映射原样上报（Meta.Types / Meta.TypeMap）；
//  3. 用 omroncipdrv.NewDriver 作工厂。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-omroncip ./driversrc/omroncip
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	// 目录名 omroncipdrv 与包名 cip 刻意不一致（搬迁时只改 import、
	// 不动 package 子句，保证每个文件的 diff 只剩 logger 与 import），
	// 因此这里必须显式起别名，否则标识符是 cip 而非 omroncipdrv。
	omroncipdrv "iot-gateway/driversrc/omroncip/omroncipdrv"
)

const (
	// 协议名沿用内置时代的原值 —— 它同时是 iot_protocol.name 与
	// device.protocol_id 的解析目标，改一个字就让既有设备静默采不到值。
	// 钉住它的是 main_test.go 的 TestProtocolIdentityPinned。
	protocolName = "Omron.CIP"

	// pluginScope 类型注册 scope，同样沿用原值（"cip"，见内置
	// driver/omron/cip/datatypes.go 的 protocolName 常量与 driver.scopeTypeMap）。
	pluginScope = "cip"
)

// formCIP 欧姆龙 EtherNet/IP (CIP) 的协议配置表单。
// 字段与内置 Omron.CIP 的表单一致（见 migrations/008_add_protocol_20260821.sql），
// 已去掉 form-create 设计器产生的 _fc_id/name/_fc_drag_tag/display/hidden/effect/props/info
// 等冗余字段，只保留 rule 与 options。
//
// CIP 只有 EtherNet/IP 显式报文（UCMM）一种传输，该包未实现 SerialExclusive()，
// 故表单里没有串口相关字段（是全仓最简表单：只需 host + port）。
// 其余可选项（pingTag / timeoutMs / stringLen / maxTagsPerRequest）不是表单字段，
// 直接写在 device.protocol_json 里即可，解析见 omroncipdrv/config.go。
const formCIP = `{
  "rule": [
    {"type":"input","field":"host","title":"设备IP","$required":true},
    {"type":"inputNumber","field":"port","title":"设备端口","$required":true}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// meta 插件的协议清单。等价于内置驱动在 init() 里做的事：
// 驱动注册（由工厂承担）、类型注册、类型映射、协议配置表单。
func meta() driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols: []driversdk.Protocol{
			{
				Name:        protocolName,
				Description: "Omron EtherNet/IP (CIP)",
				// SerialExclusive 显式置 false：CIP 只有 EtherNet/IP 显式报文，
				// 不独占任何串行总线（该包也没有 SerialExclusive() 实现），
				// 采集引擎因此只对单次 Read 持锁，允许同插件下多设备并发采集。
				SerialExclusive: false,
				FormJSON:        json.RawMessage(formCIP),
				// 声明局域网设备扫描。**只声明这一项，不声明 PointDiscovery** ——
				// 见下方 finders 位置的说明。
				Discovery: omroncipdrv.Discovery(),
			},
		},
		Scope:       pluginScope,
		Description: "Omron CIP",
		// 1.1.3：标签名本地就解析不出来的点位（ParseCIPAddress 只收可打印 ASCII，
		//        一个空格或中文就够）不再让整批 Read 失败 —— 它在规划前被拆出去，
		//        只让那一个点位标 QualityBadAddress（报警「地址无效」）。此前这种
		//        名字会让 Read 报错，采集侧据此把**在线**的设备判为断联并推通知。
		//
		// 1.1.2：整批（0x0A 多服务）请求被设备拒绝时不再把设备判离线 ——
		//        当场退回逐标签单读把值救回来，并记下"这台设备不吃批量"
		//        （5 分钟静默期，到期自动重试一次）。见 omroncipdrv/driver.go 的
		//        flush/readIndividually 与 frame.go 的 errBatchUnusable。
		//
		// 1.1.0：新增局域网设备扫描（EtherNet/IP ListIdentity 广播发现）。
		// 1.1.1：设备拒绝单个标签不再被当成整台设备读取失败
		//        （见 omroncipdrv/client.go 的 singleTagResult 与 driver.go 的 flush）。
		// 仅便于现场辨认，不参与握手判据（那由 APIVersion 把关）。
		Version: "1.1.3",
		Types:   omroncipdrv.Types(),
		TypeMap: omroncipdrv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolName: omroncipdrv.NewDriver,
	}
}

// probers 协议名 → 扫描探测实现。键必须与 meta() 里声明了 Discovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的 TestProbersMatchDiscovery
// 也钉住了这条不变式）。
func probers() driversdk.Probers {
	return driversdk.Probers{
		protocolName: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return omroncipdrv.Probe(req.Target, req.Options, req.Budget)
		},
	}
}

// **本插件刻意不实现 PointDiscovery（点位扫描）。**
//
// 理由是协议层面的：CIP 的点位是**符号名**（MotorSpeed、Recipe.Setpoint、
// Array[3]），不是可枚举的地址空间，因此 Modbus/S7/FINS 那种"从某个字起扫 N 个字"
// 的契约形状在这里根本落不下来。要枚举标签只能走另一条服务（CIP Symbol Object，
// 类 0x6B），那是与采集路径无关的一套对象模型。
//
// **注意别把这条理由套到 Rockwell.CIP 上** —— 它同一个类号、同一条服务，
// 却做了点位扫描（见 driversrc/rockwellcip/rockwellcipdrv/pointscan.go）。
// 差别不在协议名，在**有没有可依据的实现**：
//
//   - Logix 的 0x6B 实例属性表布局在本仓库里是一份**已验证的第三方实现**
//     （github.com/iceisfun/goindustrial 的 cip/symbol.go 与 Client.ListTags，
//     本仓库的既有依赖），所以 Rockwell 直接复用它，不手搓；
//   - 欧姆龙 NJ/NX 的变量访问走的是它自己的对象模型，上面那份依据对不上，
//     而本仓库从来没有对着一台 NJ/NX 真机核过它的实例属性表应答。
//
// 也就是说，这里守的仍然是"**不做而不是猜着做**"：写错的表现是返回一份空清单，
// 而用户会从"一个标签都没有"得出"这台设备没有点位"的结论 —— 与
// specs/point-discovery.md 通篇在防的那类错误完全同型。
//
// 真要做，正确顺序是先拿到一台 NJ/NX 真机，把它的实例属性表应答逐字节核对清楚，
// 再照 s7drv/modbusdrv/rockwellcipdrv 的 pointscan.go 写一份 ——
// 那时这里加一行 PointDiscovery + 一个 finder 即可。

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{
		Probers: probers(),
	})
}
