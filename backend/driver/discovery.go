// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import "encoding/json"

// 本文件是「局域网设备扫描发现」的**协议无关契约**：插件在 describe 时声明能力
// （DiscoverySpec），网关侧据此枚举目标并发起探测，探测结果以 DiscoveredDevice 回传。
//
// 与 driver.go 的驱动注册表同属一层：这里只有形状，没有任何协议实现，也没有
// 任何"谁去扫"的策略 —— 枚举网段、并发、限速、取消是宿主（devicescan 包）的事，
// 插件只回答一个问题：「你给我的这个目标，是不是我的设备」。
//
// 注意与 iot-gateway/discovery 的区别：那个包做的是**反方向**的事 ——
// 网关用 UDP 广播告诉局域网"我在哪"，供管理平台发现自己。本文件是"我去问
// 局域网里谁在"。两者语义相反，命名上刻意分开（包名 devicescan / discovery），
// 避免日志与文档里出现两个含义不同的 discovery。

// TargetMode 探测目标的类型。决定网关为该协议生成什么样的目标列表。
type TargetMode string

const (
	// TargetModeUnicast 逐主机探测：目标列表是网段内展开出的主机地址。
	// 多数 TCP 类协议（Modbus TCP、S7、MC）走这条路。
	TargetModeUnicast TargetMode = "unicast"

	// TargetModeBroadcast 探测子网广播地址：目标是各网段的广播地址，
	// **一个目标可以产出多台设备**（广播发出后多台设备同时应答）。
	// EtherNet/IP 的 ListIdentity、FINS/UDP 的节点地址查询走这条路。
	//
	// 广播目标的 Host 必须填**子网广播地址**（ip | ^mask），不是 255.255.255.255 ——
	// 受限广播只会在 FIB 选中的那一张网卡出去，并不比定向广播多覆盖任何东西。
	// 这条取舍与 discovery/announcer.go 逐网卡定向广播的理由完全一致。
	TargetModeBroadcast TargetMode = "broadcast"

	// TargetModeBoth 两种都要：既扫主机地址，也扫广播地址。
	TargetModeBoth TargetMode = "both"
)

// DiscoverySpec 插件为某个协议声明的「局域网扫描发现」能力。
//
// 在 driversdk.Meta 的 Protocol 里以指针出现：**nil 表示该协议不支持扫描**。
// 声明了它就必须在 driversdk.Probers 里给出对应实现，否则插件启动即失败。
//
// 这是**可选字段**，因此它的引入不递增 API 版本：新网关 + 旧插件走 nil 分支
// （该协议不出现在扫描页的协议下拉里），旧网关 + 新插件忽略未知字段。递增版本号
// 会让全部旧插件二进制在网关升级后失效，是一次没必要的破坏性升级。
type DiscoverySpec struct {
	// Title 扫描页协议下拉里的显示名，如「Modbus TCP 扫描」。
	Title string `json:"title"`

	// Description 该协议扫描行为的说明（会提示到前端，如"默认只探站号 1"）。
	Description string `json:"description,omitempty"`

	// FormJSON 扫描选项表单（form-create schema），与 Protocol.FormJSON 同构。
	// 用户在扫描页填的这些选项由网关原样透传给 prober（见 driversdk.ProbeParams.Options）。
	FormJSON json.RawMessage `json:"formJson,omitempty"`

	// DefaultPort 默认探测端口，供前端回填与网关构造目标。
	DefaultPort int `json:"defaultPort"`

	// TargetMode 目标类型，见 TargetMode。留空按 unicast 处理。
	TargetMode TargetMode `json:"targetMode,omitempty"`

	// MaxBudgetMs 插件自报的**单目标硬预算**（毫秒）。
	//
	// 这不是"期望值"而是"承诺"：prober 必须在它之内返回。网关侧的 probe RPC
	// 超时 = 本值 + 一个固定余量（见 pluginhost 的 probeRPCGrace），因此插件
	// 只要守住自己的声明就不会被 RPC 层超时打断。
	//
	// 之所以由插件而非配置项决定：预算是**每协议**的属性（UDP 广播探测天然比
	// TCP 拨号慢），而配置项是全局的一个旋钮。全局旋钮配小了，症状是 RPC 先
	// 超时、插件其实还在跑、网关拿到空结果、用户看到"扫不到设备"，日志里只有
	// 一条 timeout —— 又一条"配错了就静默不工作"的构造性 bug。
	// 取值由 driversdk.Meta.Validate 限死在 [500, 60000]，超时值因此自然有界。
	MaxBudgetMs int `json:"maxBudgetMs"`
}

// ProtocolDiscovery 协议 → 扫描规格的绑定。网关据此告诉前端"哪些协议可以扫"。
type ProtocolDiscovery struct {
	// Protocol 协议名（= Meta.Protocols[].Name = iot_protocol.name）。
	Protocol string `json:"protocol"`

	// Spec 该协议声明的扫描规格。
	Spec DiscoverySpec `json:"spec"`
}

// ScanTarget 一个待探测的目标。
type ScanTarget struct {
	// Host 目标地址。单播目标是主机地址，广播目标是**子网广播地址**。
	Host string `json:"host"`

	// Port 目标端口（取自插件的 DefaultPort 或用户在扫描表单里的覆盖值）。
	Port int `json:"port"`

	// LocalIP 本机在该目标所在网段上的地址，广播探测必须绑它作源地址。
	//
	// 多网卡机器（办公网 + OT 网 + docker0 + VPN 是工业现场的常态）上，不绑
	// 源地址的 UDP 广播只会从默认路由那一张网卡出去 —— 于是"扫 OT 网段"实际
	// 发到了办公网上，一台设备也发现不了。discovery/announcer.go 早已为同一个
	// 坑写过一遍注释（"逐网卡定向广播"），这里是同一条取舍。
	LocalIP string `json:"localIp,omitempty"`

	// Subnet 该目标所属网段（CIDR），仅用于展示与去重，不参与探测。
	Subnet string `json:"subnet,omitempty"`

	// Broadcast 本目标是否为广播地址，由网关填充。
	Broadcast bool `json:"broadcast,omitempty"`
}

// Attribute 设备的一个附加属性（型号、固件、序列号等协议特有信息）。
// 用切片而不是 map 是为了**保序**：前端照插件给的顺序展示。
type Attribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DiscoveredDevice 插件探测到的一台设备。
//
// 一台物理 PLC 可能被同一目标探出多条（多站号），网关侧按行展示、逐行建 device。
type DiscoveredDevice struct {
	// Host/Port 设备地址。通常等于目标，但广播探测下应答方可能与目标不同。
	Host string `json:"host"`
	Port int    `json:"port"`

	// UnitID 从站号/站地址。不适用该概念的协议留 0。
	UnitID int `json:"unitId,omitempty"`

	// Name 展示名（通常是 Vendor + " " + Model），供前端列表直接显示。
	Name string `json:"name,omitempty"`

	// Vendor/Model/Version 设备自述的厂商、型号、固件版本。
	// 多数 PLC 不支持读设备标识，因此这三项常常为空 —— 为空不影响"发现了设备"。
	Vendor  string `json:"vendor,omitempty"`
	Model   string `json:"model,omitempty"`
	Version string `json:"version,omitempty"`

	// ProtocolJSON 可直接用于建 device 行的连接配置（键名必须与插件自己的
	// 配置解析结构逐一对应）。**用 string 而不是 json.RawMessage**：
	// 应答里一个坏的 RawMessage 会让插件侧 EncodeBody 失败，整批结果退化成
	// 一条 RemoteError —— 与"逐条失败不中断整批"的契约直接冲突。校验推迟到
	// 服务层逐条 json.Valid。
	ProtocolJSON string `json:"protocolJson,omitempty"`

	// Extra 协议特有的附加信息（同 Attribute 的说明）。
	Extra []Attribute `json:"extra,omitempty"`

	// ElapsedMs 插件自报的本次探测耗时，**仅供展示**。
	// 跨进程无从校验，真正的耗时由网关侧计时。
	ElapsedMs int `json:"elapsedMs,omitempty"`
}
