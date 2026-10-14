// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Command iot-driver-fins 是欧姆龙 FINS 协议（4 种传输形态）的进程外插件实现。
//
// **本插件是该协议在网关里的唯一实现**：内置的 driver/omron/fins 已移除，
// 主二进制里不再有 FINS 协议代码。协议名与 scope 沿用内置时代的原值
// （Omron.FINS.UDP / Omron.FINS.TCP / Omron.FINS.Serial / Omron.FINS.HostLinkTCP,
// scope = fins），因此既有 iot_protocol 行、device.protocol_id 与前端表单都不需要
// 迁移 —— 换的只是「谁持有实现」。
//
// 本插件自带全部协议实现（见 ./finsdrv）：类型表、配置解析、FINS 地址解析、
// 区间规划、UDP/TCP/串口/HostLinkTCP 客户端与驱动都在插件进程内。本文件只做三件事：
//
//  1. 声明四个协议的名字、说明、串行独占性与配置表单；
//  2. 把 finsdrv 的类型表与类型映射原样上报（Meta.Types / Meta.TypeMap）；
//  3. 用 finsdrv.NewUDP / NewTCP / NewSerial / NewHostLinkTCP 作工厂。
//
// 四个协议共享同一 FINS 应用层（内存区读取命令、地址解析、区间计算、数据解码），
// 仅底层传输与封帧不同，因此共用一个 scope 与一套类型表。
//
// 构建：
//
//	cd backend && go build -o ./plugins/iot-driver-fins ./driversrc/fins
//
// 注意：Serve 之前不做任何耗时自检。父进程消失时插件靠 stdin EOF 退出，
// 若在调用 Serve 前就卡在自检里，Unix 侧没有兜底机制，会留下孤儿进程。
package main

import (
	"encoding/json"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	// 目录名 finsdrv 与包名 fins 刻意不一致（搬迁时只改 import、不动 package 子句，
	// 保证每个文件的 diff 只剩 logger 与 import），因此这里必须显式起别名，
	// 否则标识符是 fins 而非 finsdrv。
	finsdrv "iot-gateway/driversrc/fins/finsdrv"
)

const (
	// 协议名沿用内置时代的原值 —— 它们同时是 iot_protocol.name 与
	// device.protocol_id 的解析目标，改一个字就让既有设备静默采不到值。
	// 钉住它们的是 main_test.go 的 TestProtocolIdentityPinned。
	protocolUDP         = "Omron.FINS.UDP"
	protocolTCP         = "Omron.FINS.TCP"
	protocolSerial      = "Omron.FINS.Serial"
	protocolHostLinkTCP = "Omron.FINS.HostLinkTCP"

	// pluginScope 类型注册 scope，同样沿用原值（"fins"）。
	pluginScope = "fins"
)

// 表单来源说明：UDP / TCP / HostLinkTCP 三份表单取自 iot-gateway 的播种脚本
// backend/database/sqlite/migrations/015_add_protocol_20260921.sql（Serial 一行由该脚本
// 新增；UDP/TCP/HostLinkTCP 的早先版本见 008_add_protocol_20260821.sql），
// 已去掉 form-create 设计器产生的 _fc_id/name/_fc_drag_tag/display/hidden/effect/props
// 等冗余字段，字段名与顺序、rule 与 options 均与参考值一致。
//
// 表单里没有 transport：驱动的传输层由协议注册名固定（finsdrv/driver.go 的 Connect/Ping
// 会用注册名覆盖 protocol_json 里的 transport），该字段**完全不被读取**，列出来只会让用户
// 以为选了就能换传输层。参考脚本在 015 里也已经把它删掉了。
//
// 四个以太网/串口协议去掉 transport 后，UDP / TCP / HostLinkTCP 的字段集合会变得相同，
// 因此靠**各自协议自己的 info 文案**区分（端口默认值、有无握手、连的是谁），
// 而不是靠一个没人读的下拉框撑住「表单按协议而非按插件」这条不变式 ——
// 后者正是 main_test.go 的 TestFormsAreDistinct 在守的东西。info 里写的每一条
// 都能在 finsdrv 里找到依据，不是凑差异的装饰。

// formUDP Omron.FINS.UDP 的协议配置表单。
//
// info 是这个协议独有的：UDP 无连接（直接发数据报，不做连接建立），
// 节点号留空时由 finsdrv 的 resolveDstNode 取 host 的末段补齐。
const formUDP = `{
  "rule": [
    {"type":"input","field":"host","title":"设备ip","$required":true},
    {"type":"inputNumber","field":"port","title":"设备端口","$required":true,
     "info":"默认 9600；UDP 无连接，网关直接发数据报，不建连接"},
    {"type":"input","field":"dstNode","title":"设备节点号","$required":false,
     "info":"PLC 的 FINS 节点号（DA1）；留空时自动取「设备ip」末段"},
    {"type":"input","field":"srcNode","title":"网关节点号","$required":false,
     "info":"网关的 FINS 节点号（SA1）；留空时为 0"},
    {"type":"select","field":"byteOrder","title":"字节序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]},
    {"type":"select","field":"wordOrder","title":"字序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// formTCP Omron.FINS.TCP 的协议配置表单。
//
// 与 UDP 的区别写在 info 里：FINS/TCP 建连时要先做一次连接握手（UDP 不做），
// 这是这两个协议在配置界面之外唯一的差别。
const formTCP = `{
  "rule": [
    {"type":"input","field":"host","title":"设备ip","$required":true},
    {"type":"inputNumber","field":"port","title":"设备端口","$required":true,
     "info":"默认 9600；建连时会先与 PLC 做一次 FINS/TCP 连接握手"},
    {"type":"input","field":"dstNode","title":"设备节点号","$required":false,
     "info":"PLC 的 FINS 节点号（DA1）；留空时自动取「设备ip」末段"},
    {"type":"input","field":"srcNode","title":"网关节点号","$required":false,
     "info":"网关的 FINS 节点号（SA1）；留空时为 0"},
    {"type":"select","field":"byteOrder","title":"字节序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]},
    {"type":"select","field":"wordOrder","title":"字序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// formHostLinkTCP Omron.FINS.HostLinkTCP 的协议配置表单。
// 与 UDP/TCP 同为以太网连接（这里连的是串口服务器的 TCP 端口），
// 差别只在驱动的封帧层（Host Link 帧而非 FINS over TCP 帧），配置项一致。
//
// info 里点明「填的是串口服务器的地址」：这个协议最容易配错的地方就是
// 把 PLC 的 IP 填进来 —— PLC 走的是串口，网络上根本没有它的地址。
const formHostLinkTCP = `{
  "rule": [
    {"type":"input","field":"host","title":"设备ip","$required":true,
     "info":"串口服务器的 IP，不是 PLC 的 IP（PLC 走 RS-232/485，网络上没有它的地址）"},
    {"type":"inputNumber","field":"port","title":"设备端口","$required":true,
     "info":"默认 9600；串口服务器上转发 PLC 串口的那一个 TCP 端口"},
    {"type":"input","field":"dstNode","title":"设备节点号","$required":false,
     "info":"未使用（DA1 仅 FINS/UDP、FINS/TCP 生效）；驱动对串口服务器只发 Host Link 帧"},
    {"type":"input","field":"srcNode","title":"网关节点号","$required":false,
     "info":"未使用（SA1 仅 FINS/UDP、FINS/TCP 生效）"},
    {"type":"select","field":"byteOrder","title":"字节序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]},
    {"type":"select","field":"wordOrder","title":"字序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// formSerial Omron.FINS.Serial 的协议配置表单。
//
// 内置时代这个协议没有播种过表单（008 里没有它的行，驱动虽已注册但界面上建不出这种设备），
// iot-gateway 在 015_add_protocol_20260921.sql 里补上了它。本份与那一行逐字段一致，
// 每一项都能在 finsdrv/config.go 的 ParseFINSConfigRaw 里找到对应的解析键：
//
//	comPort / baudRate / dataBits / stopBits / parity —— 串口参数（clientSerial.go 打开串口用）
//	unitNo  —— Host Link 单元号（帧里的 @<单元号>，须与 PLC 的 Host Link 单元号一致）
//	fcsMode —— Host Link FCS 取窗（部分设备/串口服务器透传的 FCS 算法不同，现场联调项）
//	timeoutMs —— 单帧超时（现场波特率低或 PLC 应答慢时需要调大）
//	byteOrder / wordOrder —— 字节序与字序
//
// 未列出的键（mergeWindow/maxReadWords/stringLen/dstUnit/srcUnit）由
// DefaultFINSConfig() 兜底，不需要现场填。
//
// byteOrder/wordOrder 的选项保留「默认」空值一项，与 UDP/TCP/HostLinkTCP 三份一致
// （参考脚本给 Serial 写的是只含大端/小端两项，与同插件另外三份不同）：
// 驱动对空值本就是回退大端，留一个选得中的「默认」比让既有配置在界面上显示空白好。
//
// 注意 unitNo/dstUnit/srcUnit 以及 timeoutMs 在 finsConfigRaw 里都是**字符串**
// （strconv.Atoi 解析），因此一律用 type:"input" 而非 "inputNumber" —— 后者会发出
// JSON 数字，反序列化直接报错，整份 protocol_json 都解析不出来。
const formSerial = `{
  "rule": [
    {"type":"input","field":"comPort","title":"设备端口","$required":true,"value":"COM1",
     "info":"如 COM3、COM12（Windows）或 /dev/ttyUSB0、/dev/ttyS0（Linux）；须与网关设备上实际枚举出的端口名完全一致"},
    {"type":"select","field":"baudRate","title":"波特率","$required":false,
     "options":[{"label":"9600","value":"9600"},{"label":"19200","value":"19200"},{"label":"38400","value":"38400"},{"label":"57600","value":"57600"},{"label":"115200","value":"115200"}]},
    {"type":"select","field":"dataBits","title":"数据位","$required":false,
     "options":[{"label":"7","value":"7"},{"label":"8","value":"8"}]},
    {"type":"select","field":"stopBits","title":"停止位","$required":false,
     "options":[{"label":"1","value":"1"},{"label":"2","value":"2"}]},
    {"type":"select","field":"parity","title":"校验位","$required":false,
     "options":[{"label":"无","value":"N"},{"label":"偶","value":"E"},{"label":"奇","value":"O"}]},
    {"type":"input","field":"unitNo","title":"Host Link 单元号","$required":false},
    {"type":"select","field":"fcsMode","title":"FCS 取窗","$required":false,
     "options":[{"label":"NOSID（默认，含@排除SID）","value":"NOSID"},{"label":"FULL（含@，手册标准）","value":"FULL"},{"label":"BODY（仅hex体）","value":"BODY"}]},
    {"type":"input","field":"timeoutMs","title":"单帧超时(ms)","$required":false,
     "info":"默认 5000；波特率低或 PLC 应答慢时调大。过小会把应答帧截断，表现为偶发 FCS 校验错而非明确超时"},
    {"type":"select","field":"byteOrder","title":"字节序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]},
    {"type":"select","field":"wordOrder","title":"字序","$required":false,
     "options":[{"label":"默认","value":""},{"label":"高字在前","value":"BIG_ENDIAN"},{"label":"低字在前","value":"LITTLE_ENDIAN"}]}
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
				Name:        protocolUDP,
				Description: "Omron FINS/UDP（端口 9600）",
				FormJSON:    json.RawMessage(formUDP),
				// 两项扫描能力都声明。串口独占的那条禁令（SerialExclusive 不得
				// 声明 Discovery）在这里没有适用对象 —— 见下面 Serial 那一项的注释。
				Discovery:      finsdrv.DiscoveryUDP(),
				PointDiscovery: finsdrv.PointDiscovery(),
			},
			{
				Name:        protocolTCP,
				Description: "Omron FINS/TCP（含连接握手）",
				FormJSON:    json.RawMessage(formTCP),
				// 与 UDP 的差别只在传输层，因此两份能力声明共用同一批构造。
				Discovery:      finsdrv.DiscoveryTCP(),
				PointDiscovery: finsdrv.PointDiscovery(),
			},
			{
				Name: protocolSerial,
				// 串口独占串行总线：采集引擎据此整轮持设备锁，
				// 保证 RS-485 上的帧不被交错。其余三个为 false。
				SerialExclusive: true,
				Description:     "Omron Host Link 串口（FINS over serial）",
				FormJSON:        json.RawMessage(formSerial),
				// **两项扫描能力都不声明**，而且理由是**两条不同的**规矩：
				//
				//   - Discovery（扫网段）被 driversdk.Meta.Validate 明文禁止挂在
				//     串口协议上：目标模型是 host/port，表达不了 comPort；串口上
				//     "总线上有哪些站号"是另一个功能。声明了插件启动即失败。
				//   - PointDiscovery（扫一台已存在的设备的点位）**契约上允许**
				//     串口协议声明（见 specs/point-discovery.md §3），本插件不声明
				//     是因为它的会话复用路径（finsDriver.matchConfig 只为串口而设）
				//     与 Host Link 的帧格式都还没有真机验证过 —— 能力一旦声明出去，
				//     前端就会出现一个按钮，而按下去的结果没人验证过。
				//     验证过之后再补，只需在这里加一行。
			},
			{
				Name: protocolHostLinkTCP,
				// 名字里带 Host Link，但网关视角是一条 TCP 长连接，客户端内部
				// 已对单次请求+响应持互斥锁，与 FINS/TCP 同级 —— 不独占。
				// 判据与内置实现一致：transport == TransportSerial 才独占。
				SerialExclusive: false,
				Description:     "Omron Host Link over TCP（经串口服务器采集远端串口）",
				FormJSON:        json.RawMessage(formHostLinkTCP),
				// Discovery 不声明：这个协议连的是**串口服务器**，扫一个网段
				// 只会扫出一堆串口服务器，一键建出来的设备大概率指向一台
				// 后面并没有 PLC 的服务器。PointDiscovery 同理（没验证过）。
			},
		},
		Scope:       pluginScope,
		Description: "Omron FINS",
		// 1.2.2：本地就解析不出来的地址不再让整批 Read 失败 —— 它在规划前被拆出去，
		// 只让那一个点位标 QualityBadAddress（报警「地址无效」）。此前这种地址会让
		// Read 报错，采集侧据此把**在线**的设备判为断联并推通知。
		//
		// 1.2.1：采集路径的结束码错误改为有界对半降级 —— 区间里混进一个设备上
		// 不存在的地址时，只有那个点位保持 quality=0，同区间其余点位照常取值
		//（此前整段一起被标 0）。降级结果进区间缓存（带 5 分钟 TTL），
		// 坏地址不再每轮重付一遍整段失败。
		//
		// 1.2.0：FINS/UDP 与 FINS/TCP 新增局域网设备扫描与点位范围扫描两项能力。
		// 仅便于现场辨认，不参与握手判据（那由 APIVersion 把关）。
		//
		// 1.1.0：四份表单删掉不被读取的 transport 字段、各协议改为用自己的 info
		// 文案区分；Serial 补上此前配不到的 timeoutMs（对齐 iot-gateway 的
		// 015_add_protocol_20260921.sql）。
		Version: "1.2.2",
		Types:   finsdrv.Types(),
		TypeMap: finsdrv.CommonTypeMap(),
	}
}

// factories 协议名 → 工厂。键必须与 meta().Protocols[].Name 逐一对应，
// 否则 Serve 会在启动时报错（main_test.go 也钉住了这条不变式）。
func factories() driversdk.Factories {
	return driversdk.Factories{
		protocolUDP:         finsdrv.NewUDP,
		protocolTCP:         finsdrv.NewTCP,
		protocolSerial:      finsdrv.NewSerial,
		protocolHostLinkTCP: finsdrv.NewHostLinkTCP,
	}
}

// probers 协议名 → 扫描探测实现。键必须与 meta() 里声明了 Discovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的 TestProbersMatchDiscovery
// 也钉住了这条不变式）。
//
// **只有 UDP 与 TCP**：串口按契约不得声明 Discovery，
// HostLinkTCP 连的是串口服务器而不是 PLC（理由见 meta() 里那两项的注释）。
func probers() driversdk.Probers {
	return driversdk.Probers{
		protocolUDP: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return finsdrv.ProbeUDP(req.Target, req.Options, req.Budget)
		},
		protocolTCP: func(req driversdk.ProbeRequest) ([]driver.DiscoveredDevice, error) {
			return finsdrv.ProbeTCP(req.Target, req.Options, req.Budget)
		},
	}
}

// finders 协议名 → 点位扫描实现。键必须与 meta() 里声明了 PointDiscovery 的协议
// 逐一对应，否则 Serve 会在启动时报错（main_test.go 的
// TestPointFindersMatchPointDiscovery 也钉住了这条不变式）。
//
// 两个协议共用同一份实现：这个功能走的地址解析、区域码与解码与传输层无关。
func finders() driversdk.PointFinders {
	scan := func(req driversdk.FindPointsRequest) (*driver.PointScanResult, error) {
		return finsdrv.ScanPoints(req.Session, req.Options, req.Deadline)
	}
	return driversdk.PointFinders{
		protocolUDP: scan,
		protocolTCP: scan,
	}
}

func main() {
	driversdk.Main(factories(), meta(), driversdk.Capabilities{
		Probers:      probers(),
		PointFinders: finders(),
	})
}
