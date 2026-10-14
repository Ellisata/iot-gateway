// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-opcua 是 OPC UA 协议的进程外插件实现。
//
// **本插件是该协议在网关里的唯一实现**：内置的 driver/opcua 已移除，
// 主二进制里不再有 OPC UA 协议代码。协议名与 scope 沿用内置时代的原值
// （OPC.UA / opcua），因此既有 iot_protocol 行、device.protocol_id 与前端
// 表单都不需要迁移 —— 换的只是「谁持有实现」。
//
// 本插件自带全部协议实现（见 ./opcuadrv）：类型表、配置解析、NodeID/浏览路径
// 地址解析、gopcua 客户端与驱动都在插件进程内。本文件只做三件事：
//
//  1. 声明协议的名字、串行独占性与配置表单；
//  2. 把 opcuadrv 的类型表与类型映射原样上报（Meta.Types / Meta.TypeMap）；
//  3. 用 opcuadrv.NewDriver 作工厂。
//
// 注意 OPC UA 的类型只有 Name + Kind：服务器在 Read 响应里返回原生值，
// 不需要字节级 Decode/Encode/Format，因此没有"类型带解码函数"这条不变式
// （对应地，main_test.go 钉的是 Kind 不被丢掉）。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-opcua ./driversrc/opcua
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	// 目录名 opcuadrv 与包名 opcua 刻意不一致（搬迁时只改 import、
	// 不动 package 子句，保证每个文件的 diff 只剩 logger 与 import），
	// 因此这里必须显式起别名，否则标识符是 opcua 而非 opcuadrv。
	opcuadrv "iot-gateway/driversrc/opcua/opcuadrv"
)

const (
	// 协议名沿用内置时代的原值 —— 它同时是 iot_protocol.name 与
	// device.protocol_id 的解析目标，改一个字就让既有设备静默采不到值。
	// 钉住它的是 main_test.go 的 TestProtocolIdentityPinned
	// 与 pluginhost/testdata/parity/typemap/opcua.json 里的机器生成清单。
	protocolName = "OPC.UA"

	// pluginScope 类型注册 scope，同样沿用原值（"opcua"）。
	pluginScope = "opcua"
)

// formOpcUa OPC UA 的协议配置表单。
// 字段与内置 OPC.UA 的表单一致（见 migrations/013_add_protocol_20260831.sql），
// 已去掉 form-create 设计器产生的 _fc_id/name/_fc_drag_tag/display/hidden/
// effect/info 等冗余字段。
//
// 例外：password 一项保留了 props（{"type":"password"}）。它不是设计器噪声 ——
// form-create 会把 props 透传给底层组件，去掉它密码框就退化成明文输入框，
// 是实打实的回退。host/port/endpoint 的关系由驱动决定：endpoint 留空时按
// host + port 自动生成（见 opcuadrv.ParseOpcUaConfig）。
const formOpcUa = `{
  "rule": [
    {"type":"input","field":"endpoint","title":"端点地址","$required":false},
    {"type":"input","field":"host","title":"设备IP","$required":true},
    {"type":"inputNumber","field":"port","title":"设备端口","$required":true},
    {"type":"select","field":"securityPolicy","title":"安全策略","$required":false,
     "options":[{"label":"无","value":"None"},{"label":"Basic128Rsa15","value":"Basic128Rsa15"},{"label":"Basic256","value":"Basic256"},{"label":"Basic256Sha256","value":"Basic256Sha256"}]},
    {"type":"select","field":"securityMode","title":"安全模式","$required":false,
     "options":[{"label":"无","value":"None"},{"label":"签名","value":"Sign"},{"label":"签名并加密","value":"SignAndEncrypt"}]},
    {"type":"select","field":"authMode","title":"认证方式","$required":false,
     "options":[{"label":"匿名","value":"Anonymous"},{"label":"用户名/密码","value":"Username"}]},
    {"type":"input","field":"username","title":"用户名","$required":false},
    {"type":"input","field":"password","title":"密码","$required":false,"props":{"type":"password"}},
    {"type":"inputNumber","field":"timeoutMs","title":"请求超时(ms)","$required":false},
    {"type":"inputNumber","field":"maxBatch","title":"批量读取节点数","$required":false}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// meta 插件的协议清单。等价于内置驱动在 init() 里做的事：
// 驱动注册（由工厂承担）、类型注册、类型映射、协议配置表单。
//
// 单协议：OPC.UA 不分 TCP/串口 —— 传输由 endpoint 的 URL scheme 决定
// （opc.tcp://），底层连接是长连接 TCP，不实现 SerialExclusive
// （该包没有 SerialExclusive() 方法），故这里为 false。
func meta() driversdk.Meta {
	return driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Protocols: []driversdk.Protocol{
			{
				Name:            protocolName,
				SerialExclusive: false,
				Description:     "OPC UA",
				FormJSON:        json.RawMessage(formOpcUa),
				// 两项扫描能力都声明。OPC UA 只有一个协议、只走 opc.tcp 长连接，
				// 所以没有 MC 那种"串口只能声明一半"的不对称（那边 Discovery
				// 被 Meta.Validate 明文禁止挂在串口协议上）。
				//
				// 这两项都是**可选字段**（nil 表示不支持），因此它们的引入
				// 不递增 APIVersion：新网关 + 旧插件走 nil 分支（该协议不出现在
				// 扫描页的下拉里），旧网关 + 新插件忽略未知字段。
				Discovery:      opcuadrv.Discovery(),
				PointDiscovery: opcuadrv.PointDiscovery(),
			},
		},
		Scope:       pluginScope,
		Description: "OPC UA",
		// 1.1.0：新增局域网设备扫描（GetEndpoints 判据）与点位扫描（Browse 原生
		// 枚举）。仅便于现场辨认，不参与握手判据（那由 APIVersion 把关）。
		Version: "1.1.0",
		Types:   opcuadrv.Types(),
		TypeMap: opcuadrv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolName: opcuadrv.NewDriver,
	}
}

// probers 协议名 → 扫描探测实现。键必须与 meta() 里声明了 Discovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的 TestProbersMatchDiscovery
// 也钉住了这条不变式）。
func probers() driversdk.Probers {
	return driversdk.Probers{
		protocolName: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return opcuadrv.Probe(req.Target, req.Options, req.Budget)
		},
	}
}

// finders 协议名 → 点位扫描实现。键必须与 meta() 里声明了 PointDiscovery 的协议
// 逐一对应，否则 Serve 会在启动时失败（main_test.go 的
// TestPointFindersMatchPointDiscovery 也钉住了这条不变式）。
func finders() driversdk.PointFinders {
	return driversdk.PointFinders{
		protocolName: func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
			return opcuadrv.ScanPoints(req.Session, req.Options, req.Deadline)
		},
	}
}

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{
		Probers:      probers(),
		PointFinders: finders(),
	})
}
