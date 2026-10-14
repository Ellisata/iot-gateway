// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-rockwellcip 是 Rockwell（Allen-Bradley Logix）EtherNet/IP
// (CIP) 协议的进程外插件实现。
//
// **本插件是该协议在网关里的唯一实现**：内置的 driver/rockwell/cip 已移除，
// 主二进制里不再有 Rockwell CIP 协议代码。协议名与 scope 沿用内置时代的原值
// （Rockwell.CIP / rockwell），因此既有 iot_protocol 行、device.protocol_id
// 与前端表单都不需要迁移 —— 换的只是「谁持有实现」。
//
// 本插件自带全部协议实现（见 ./rockwellcipdrv）：类型表、类型映射、配置解析、
// 标签/数组合并的读取计划、0x4C 读取的响应解码与驱动都在插件进程内。
// 本文件只做三件事：
//
//  1. 声明协议的名字、说明与配置表单；
//  2. 把 rockwellcipdrv 的类型表与类型映射原样上报（Meta.Types / Meta.TypeMap）；
//  3. 用 rockwellcipdrv.NewDriver 作工厂。
//
// 单协议：Rockwell 只有 EtherNet/IP 显式报文（UCMM + 标签寻址）一种传输，
// 不像 Modbus/Mitsubishi 那样分 TCP/串口，故只有一个协议名与一家工厂。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-rockwellcip ./driversrc/rockwellcip
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	// 目录名 rockwellcipdrv 与包名 cip 刻意不一致（搬迁时只改 import、
	// 不动 package 子句，保证每个文件的 diff 只剩 logger 与 import），
	// 因此这里必须显式起别名，否则标识符是 cip 而非 rockwellcipdrv。
	rockwellcipdrv "iot-gateway/driversrc/rockwellcip/rockwellcipdrv"
)

const (
	// protocolName 协议名沿用内置时代的原值 —— 它同时是 iot_protocol.name 与
	// device.protocol_id 的解析目标，改一个字就让既有设备静默采不到值。
	// 钉住它的是 main_test.go 的 TestProtocolIdentityPinned。
	protocolName = "Rockwell.CIP"

	// pluginScope 类型注册 scope，同样沿用原值（"rockwell"）。
	// 内置基线的 Rockwell 条目已随内置驱动一并移除，所以这里不再撞名。
	pluginScope = "rockwell"
)

// formRockwell Rockwell CIP 的协议配置表单。
// 字段与内置 Rockwell.CIP 的表单一致（见 migrations/012_add_protocol_20260828.sql），
// 已去掉 form-create 设计器产生的 _fc_id/name/_fc_drag_tag/display/hidden/info
// 等冗余字段，保留 rule 与 options。
//
// port 的默认值提示（"EtherNet/IP 默认 44818"）原先挂在 info 字段里，随冗余字段
// 一并去掉；默认端口由驱动侧 DefaultRockwellConfig 兜底，表单只收用户显式填写的值。
const formRockwell = `{
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
				// SerialExclusive 留零值 false：本驱动是 TCP 长连接（0x4C 单次
				// 请求+响应持锁），底层不独占串行总线，collector 视为非独占驱动。
				// 该包也没有实现 driver.SerialExclusive 接口。
				Name:        protocolName,
				Description: "Rockwell CIP (Allen-Bradley Logix / EtherNet/IP)",
				FormJSON:    json.RawMessage(formRockwell),
				// Ethernet/IP 标准的 ListIdentity 广播发现，实现见 rockwellcipdrv/probe.go。
				Discovery: rockwellcipdrv.Discovery(),
				// CIP Symbol Object（类 0x6B）原生枚举，实现见 rockwellcipdrv/pointscan.go。
				// 与 Omron.CIP **刻意不同**：那边点位的形态使枚举契约落不下来，
				// 这边 0x6B 是 Logix 的标准标签表且有已验证的第三方实现可依据。
				PointDiscovery: rockwellcipdrv.PointDiscovery(),
			},
		},
		Scope:       pluginScope,
		Description: "Rockwell CIP",
		// 1.1.2：标签名本地就解析不出来的点位（ParseRockwellAddress 只收可打印
		//        ASCII，一个空格或中文就够）不再让整批 Read 失败 —— 它在规划前被
		//        拆出去、随计划缓存带出，只让那一个点位标 QualityBadAddress
		//        （报警「地址无效」）。此前这种名字会让 Read 报错，采集侧据此把
		//        **在线**的设备判为断联并推通知。
		//
		// 1.1.1：两处数组读修复 ——
		//        ① 数组区间分块读的请求路径带上起始下标（Read Tag Elements 的
		//           请求体里没有偏移字段，偏移只能写在路径里；此前第二块起会
		//           **静默读到别的元素的值**，见 rockwellcipdrv/range.go 的
		//           rangeRequestName）；
		//        ② 一块数组被设备整块拒绝时按元素下标对半降级，只摘掉真正不存在
		//           的那个元素（此前同块 125 个元素一起被标 quality=0）。
		//
		// 1.1.0：新增局域网设备扫描（EtherNet/IP ListIdentity）与点位扫描
		//        （CIP Symbol Object 类 0x6B 原生枚举）两项能力。
		// 仅便于现场辨认，不参与握手判据（那由 APIVersion 把关）。
		Version: "1.1.2",
		Types:   rockwellcipdrv.Types(),
		TypeMap: rockwellcipdrv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolName: rockwellcipdrv.NewDriver,
	}
}

// probers 协议名 → 扫描探测实现。键必须与 meta() 里声明了 Discovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的 TestProbersMatchDiscovery
// 也钉住了这条不变式）。
func probers() driversdk.Probers {
	return driversdk.Probers{
		protocolName: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return rockwellcipdrv.Probe(req.Target, req.Options, req.Budget)
		},
	}
}

// finders 协议名 → 点位扫描实现。键必须与 meta() 里声明了 PointDiscovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的
// TestPointFindersMatchPointDiscovery 也钉住了这条不变式）。
func finders() driversdk.PointFinders {
	return driversdk.PointFinders{
		protocolName: func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
			return rockwellcipdrv.ScanPoints(req.Session, req.Options, req.Deadline)
		},
	}
}

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{
		Probers:      probers(),
		PointFinders: finders(),
	})
}
