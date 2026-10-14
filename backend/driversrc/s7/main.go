// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-s7 是西门子 S7 协议的进程外插件实现。
//
// **本插件是该协议在网关里的唯一实现**：内置的 driver/s7 已移除，
// 主二进制里不再有 S7 协议代码。协议名与 scope 沿用内置时代的原值
// （Siemens.S7 / s7），因此既有 iot_protocol 行、device.protocol_id
// 与前端表单都不需要迁移 —— 换的只是「谁持有实现」。
//
// 本插件自带全部协议实现（见 ./s7drv）：类型表、配置解析、S7 地址解析、
// 区间规划、ISO-on-TCP 客户端与驱动都在插件进程内。本文件只做三件事：
//
//  1. 声明协议的名字、说明与配置表单，以及两项扫描能力；
//  2. 把 s7drv 的类型表与类型映射原样上报（Meta.Types / Meta.TypeMap）；
//  3. 用 s7drv.NewDriver 作工厂，并把扫描能力的实现交给 Capabilities。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-s7 ./driversrc/s7
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	// 目录名 s7drv 与包名 s7 刻意不一致（搬迁时只改 import、不动 package 子句，
	// 保证每个文件的 diff 只剩 logger 与 import），因此这里必须显式起别名，
	// 否则标识符是 s7 而非 s7drv。
	s7drv "iot-gateway/driversrc/s7/s7drv"
)

const (
	// 协议名沿用内置时代的原值 —— 它同时是 iot_protocol.name 与
	// device.protocol_id 的解析目标，改一个字就让既有设备静默采不到值。
	// 钉住它的是 main_test.go 的 TestProtocolIdentityPinned。
	protocolName = "Siemens.S7"

	// pluginScope 类型注册 scope，同样沿用原值（"s7"，见内置
	// driver/datatypes.go 的 protocolName 常量与 driver.scopeTypeMap）。
	pluginScope = "s7"
)

// formS7 Siemens S7 的协议配置表单。
// 字段与内置 Siemens.S7 的表单一致（见 migrations/008_add_protocol_20260821.sql），
// 已去掉 form-create 设计器产生的 _fc_id/name/_fc_drag_tag/display/hidden/effect/info
// 等冗余字段，只保留 rule 与 options。
//
// 本协议只有 ISO-on-TCP 一种传输（该包未实现 SerialExclusive()），
// 故表单里没有串口相关字段。
const formS7 = `{
  "rule": [
    {"type":"input","field":"host","title":"设备IP","$required":true},
    {"type":"inputNumber","field":"port","title":"设备端口","$required":true},
    {"type":"input","field":"rack","title":"机架","$required":true},
    {"type":"input","field":"slot","title":"槽号","$required":true},
    {"type":"select","field":"connectionType","title":"连接类型","$required":false,
     "options":[{"label":"PG","value":"1"},{"label":"OP","value":"2"},{"label":"PC","value":"3"}]}
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
				Description: "Siemens S7（ISO-on-TCP）",
				// SerialExclusive 显式置 false：S7 只有 ISO-on-TCP，
				// 不独占任何串行总线（该包也没有 SerialExclusive() 实现），
				// 采集引擎因此只对单次 Read 持锁，允许同插件下多设备并发采集。
				SerialExclusive: false,
				FormJSON:        json.RawMessage(formS7),
				// 两项扫描能力**都声明**。S7 只有 ISO-on-TCP 一种传输，
				// 本插件里不存在串口协议，因此 driversdk.Meta.Validate 里那条
				// 「SerialExclusive 不得声明 Discovery」的禁令在这里没有适用对象
				//（Modbus 的 RTU 恰好相反：Discovery 只能挂在 TCP 上）。
				//
				// 声明了就必须在 Capabilities 里给出实现，否则 Serve 启动即失败。
				Discovery:      s7drv.Discovery(),
				PointDiscovery: s7drv.PointDiscovery(),
			},
		},
		Scope:       pluginScope,
		Description: "Siemens S7",
		// 1.2.1：本地就解析不出来的地址不再让整批 Read 失败 —— 它在规划前被拆出去，
		// 只让那一个点位标 QualityBadAddress（报警「地址无效」）。此前这种地址会让
		// Read 报错，采集侧据此把**在线**的设备判为断联并推通知。
		//
		// 1.2.0：采集路径的区间失败改为有界对半降级 —— 区间里混进一个设备上不存在的
		// 地址（没建的 DB、越界的偏移）时，只有那个点位保持 quality=0，同区间其余点位
		// 照常取值（此前整段一起被标 0）。降级只认**能确认地址不存在**的项级错误文案，
		// gos7 的兜底桶（"CLI : function refused by CPU"）按瞬时状态原样重试 —— 点扫
		// 那条路把它当"读不到"是对的，采集这条路的代价却是把好点位摘掉 5 分钟。
		//
		// 1.1.0：新增局域网设备扫描与点位范围扫描两项能力。
		// 仅便于现场辨认，不参与握手判据（那由 APIVersion 把关）。
		Version: "1.2.1",
		Types:   s7drv.Types(),
		TypeMap: s7drv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolName: s7drv.NewDriver,
	}
}

// probers 协议名 → 扫描探测实现。键必须与 meta() 里声明了 Discovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的 TestProbersMatchDiscovery
// 也钉住了这条不变式）。
func probers() driversdk.Probers {
	return driversdk.Probers{
		protocolName: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return s7drv.ProbeTCP(req.Target, req.Options, req.Budget)
		},
	}
}

// finders 协议名 → 点位扫描实现。键必须与 meta() 里声明了 PointDiscovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的
// TestPointFindersMatchPointDiscovery 也钉住了这条不变式）。
func finders() driversdk.PointFinders {
	return driversdk.PointFinders{
		protocolName: func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
			return s7drv.ScanPoints(req.Session, req.Options, req.Deadline)
		},
	}
}

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{
		Probers:      probers(),
		PointFinders: finders(),
	})
}
