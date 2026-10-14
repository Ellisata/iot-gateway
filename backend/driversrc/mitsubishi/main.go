// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-mitsubishi 是三菱 MC 协议的进程外插件实现。
//
// **本插件是该协议在网关里的唯一实现**：内置的 driver/mitsubishi 已移除，
// 主二进制里不再有 MC 协议代码。协议名与 scope 沿用内置时代的原值
// （Mitsubishi.MC.TCP / Mitsubishi.MC.Serial / mitsubishi），因此既有
// iot_protocol 行、device.protocol_id 与前端表单都不需要迁移 ——
// 换的只是「谁持有实现」。
//
// 本插件自带全部协议实现（见 ./mitsubishidrv）：类型表、配置解析、MC 地址解析、
// 区间规划、TCP(3E)/串口(4C) 客户端与驱动都在插件进程内。本文件只做三件事：
//
//  1. 声明两个协议的名字、说明、串行独占性与配置表单；
//  2. 把 mitsubishidrv 的类型表与类型映射原样上报（Meta.Types / Meta.TypeMap）；
//  3. 用 mitsubishidrv.NewTCP / NewSerial 作工厂。
//
// 两个协议共享同一 MC 应用层（设备码、地址解析、区间计算、数据解码），
// 仅底层传输与封帧不同，因此共用一个 scope 与一套类型表。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-mitsubishi ./driversrc/mitsubishi
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	// 目录名 mitsubishidrv 与包名 mitsubishi 刻意不一致（搬迁时只改 import、
	// 不动 package 子句，保证每个文件的 diff 只剩 logger 与 import），
	// 因此这里必须显式起别名，否则标识符是 mitsubishi 而非 mitsubishidrv。
	mitsubishidrv "iot-gateway/driversrc/mitsubishi/mitsubishidrv"
)

const (
	// 协议名沿用内置时代的原值 —— 它们同时是 iot_protocol.name 与
	// device.protocol_id 的解析目标，改一个字就让既有设备静默采不到值。
	// 钉住它们的是 main_test.go 的 TestProtocolIdentityPinned
	// 与 pluginhost/testdata/parity/typemap/mitsubishi.json 里的机器生成清单。
	protocolTCP    = "Mitsubishi.MC.TCP"
	protocolSerial = "Mitsubishi.MC.Serial"

	// pluginScope 类型注册 scope，同样沿用原值（"mitsubishi"）。
	pluginScope = "mitsubishi"
)

// formTCP Mitsubishi MC 以太网的协议配置表单。
// 字段与 iot-gateway 的 Mitsubishi.MC.TCP 表单一致（见
// backend/database/sqlite/migrations/015_add_protocol_20260921.sql），
// 已去掉 form-create 设计器产生的 _fc_id/name/_fc_drag_tag/display/hidden/effect/info 等冗余字段。
//
// 表单里没有 transport：传输层由协议注册名固定（3E 帧二进制），驱动在 Connect/Ping 里
// 用 cfg.Transport = d.transport 硬覆盖 protocol_json 的值，列出来只会让用户以为选了就能换。
const formTCP = `{
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

// formSerial Mitsubishi MC 串口的协议配置表单，与 iot-gateway 的
// Mitsubishi.MC.Serial 表单一致（见 015_add_protocol_20260921.sql）。
//
// 表单里没有 transport，同 formTCP：4C 帧由协议注册名固定，驱动硬覆盖该值。
// （原先照搬的 transport 选项 label="SERIAL"、value="串口" 还是反的 —— 一个不被读取
// 的字段本就该删掉，而不是修它的字面量。）
//
// comPort 是自由文本框而非下拉（原为只有 COM1 与 /dev/ttyUSB0 两项的 select）：
// 现场端口号不可穷举，COM7、/dev/ttyUSB1 这类根本配不了，而前端并未给 select 开
// filterable/allow-create。不带默认值 —— newMCSerialClient 对空端口直接报
// "comPort is required"，预填一个驱动并不认同的默认值反而是另一种界面在骗人。
const formSerial = `{
  "rule": [
    {"type":"input","field":"comPort","title":"设备端口","$required":true,
     "info":"如 COM3、COM12（Windows）或 /dev/ttyUSB0、/dev/ttyS0（Linux）；须与网关设备上实际枚举出的端口名完全一致"},
    {"type":"select","field":"baudRate","title":"波特率","$required":false,
     "options":[{"label":"9600","value":"9600"},{"label":"19200","value":"19200"},{"label":"38400","value":"38400"},{"label":"57600","value":"57600"},{"label":"115200","value":"115200"}]},
    {"type":"select","field":"dataBits","title":"数据位","$required":false,
     "options":[{"label":"7","value":"7"},{"label":"8","value":"8"}]},
    {"type":"select","field":"stopBits","title":"停止位","$required":false,
     "options":[{"label":"1","value":"1"},{"label":"2","value":"2"}]},
    {"type":"select","field":"parity","title":"校验位","$required":false,
     "options":[{"label":"无","value":"N"},{"label":"偶","value":"E"},{"label":"奇","value":"O"}]}
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
				Name:        protocolTCP,
				Description: "Mitsubishi MC 以太网（3E 帧）",
				FormJSON:    json.RawMessage(formTCP),
				// 两项扫描能力都声明：以太网是唯一能声明 Discovery 的形态
				//（串口被契约禁止，见下面 Serial 那一项）。
				Discovery:      mitsubishidrv.DiscoveryTCP(),
				PointDiscovery: mitsubishidrv.PointDiscovery(),
			},
			{
				Name: protocolSerial,
				// 串口独占串行总线：采集引擎据此整轮持设备锁，
				// 保证总线上帧不被交错。TCP 为 false。
				SerialExclusive: true,
				Description:     "Mitsubishi MC 串口（4C 帧）",
				FormJSON:        json.RawMessage(formSerial),
				// **只声明 PointDiscovery**，两项能力的取舍是两条不同的规矩：
				//
				//   - Discovery（扫网段）被 driversdk.Meta.Validate **明文禁止**
				//     挂在串口协议上：目标模型是 host/port，表达不了 comPort；
				//     串口上"总线上有哪些站号"是另一个功能。声明了插件启动即失败。
				//   - PointDiscovery（扫一台已存在设备的点位）**契约上允许**
				//     （见 specs/point-discovery.md §3），而且本插件的串口会话语义
				//     是齐的：mcDriver 实现了 driver.ConnectionMatcher（matchConfig
				//     只为串口而设），所以"命中就复用、不命中就报错不回退"那条
				//     回退表走得通 —— 「测试连接」走的正是同一条路。
				//
				// 与 FINS 插件的差别值得说明（那边对串口刻意不声明）：FINS 的
				// Host Link 帧格式与串口复用路径都还没有真机验证过，而 MC 的串口
				// 传输（4C Format5）是**采集路径上一直在用**的同一份实现，
				// 点扫复用的就是它。
				PointDiscovery: mitsubishidrv.PointDiscovery(),
			},
		},
		Scope:       pluginScope,
		Description: "Mitsubishi MC",
		// 1.2.2：采集路径的结束码错误改为有界对半降级 —— 区间里混进一个设备上
		// 没有的地址时，只有那个点位保持 quality=0，同区间其余点位照常取值
		//（此前整段一起被标 0）。降级按区间自己的单位切（位模式按位、字模式按字、
		// 位设备的字访问按 16 对齐后的位地址），结果进区间缓存（带 5 分钟 TTL）。
		//
		// 1.2.2：本地就解析不出来的地址不再让整批 Read 失败 —— 它在规划前被拆出去，
		// 只让那一个点位标 QualityBadAddress（报警「地址无效」）。此前这种地址会让
		// Read 报错，采集侧据此把**在线**的设备判为断联并推通知。
		//
		// 1.2.1：点位扫描把 0x4031（iQ 家族的「指定软元件编号超出范围」）纳入
		// 地址问题白名单；结束码错误的应答体改为读干净再返回（真机 10.62.100.22
		// 上扫 ZR 曾中止成「mc: end code 0x4031」，且错误应答带的 9 字节残留
		// 会让这条连接上的后续请求全部错位）。
		//
		// 1.2.0：MC.TCP 与 MC.Serial 新增点位范围扫描，MC.TCP 另外新增局域网
		// 设备扫描。仅便于现场辨认，不参与握手判据（那由 APIVersion 把关）。
		//
		// 1.1.0：两份表单删掉不被读取的 transport 字段（对齐 iot-gateway 的
		// 015_add_protocol_20260921.sql）。
		Version: "1.2.2",
		Types:   mitsubishidrv.Types(),
		TypeMap: mitsubishidrv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolTCP:    mitsubishidrv.NewTCP,
		protocolSerial: mitsubishidrv.NewSerial,
	}
}

// probers 协议名 → 扫描探测实现。键必须与 meta() 里声明了 Discovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的 TestProbersMatchDiscovery
// 也钉住了这条不变式）。
//
// **只有 TCP**：串口按契约不得声明 Discovery（理由见 meta() 里那一项的注释）。
// MC 3E 帧的 UDP 变体本插件也不提供 —— 驱动的传输层只有 TCP 与串口两种
// （见 newTransport），声明一个采不了的传输形态只会让人建出连不上的设备。
func probers() driversdk.Probers {
	return driversdk.Probers{
		protocolTCP: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return mitsubishidrv.ProbeTCP(req.Target, req.Options, req.Budget)
		},
	}
}

// finders 协议名 → 点位扫描实现。键必须与 meta() 里声明了 PointDiscovery 的协议
// 逐一对应，否则 Serve 会在启动时失败（main_test.go 的
// TestPointFindersMatchPointDiscovery 也钉住了这条不变式）。
//
// 两个协议共用同一份实现：这个功能走的软元件解析、地址生成与解码与传输层无关。
func finders() driversdk.PointFinders {
	scan := func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
		return mitsubishidrv.ScanPoints(req.Session, req.Options, req.Deadline)
	}
	return driversdk.PointFinders{
		protocolTCP:    scan,
		protocolSerial: scan,
	}
}

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{
		Probers:      probers(),
		PointFinders: finders(),
	})
}
