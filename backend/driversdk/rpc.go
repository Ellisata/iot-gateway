// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk

import "iot-gateway/driver"

// 本文件是驱动插件方法（connect/read/isConnected/close/ping）的参数与结果
// 结构。信封与帧编解码在 iot-gateway/pluginwire，插件作者无需关心；
// 网关侧实现（iot-gateway/pluginhost）直接复用这些定义，两侧共用同一份
// 定义以保证协议不会各自漂移。

// ConnectParams connect 请求参数。
//
// 不带 session：会话 id 由插件进程分配并在应答中返回。这样网关侧的
// remoteDriver 永远只持有"插件最后告诉它的 id"，进程重启后不可能用陈旧 id
// 误打到新进程的其它会话上。
type ConnectParams struct {
	// Protocol 目标协议名（Meta.Protocols 中的一项）。
	// 一个插件可能提供多个协议，子进程据此选择对应的工厂。
	Protocol string `json:"protocol"`

	ProtocolJSON string `json:"protocolJson"`
}

// ConnectResult connect 应答。
type ConnectResult struct {
	// Session 本次连接在插件进程内的会话 id（0 保留为"未分配"）。
	Session uint64 `json:"session"`

	// Connected Connect 返回 nil 后驱动的实际连接状态。
	Connected bool `json:"connected"`
}

// WireAddr 是 po.DeviceAddress 在 RPC 层的投影：只含驱动 Read 真正读取的字段。
//
// Label/Description/CommonDataType/ScanFrequency 等由网关侧保留
// （采集引擎的 toRecords 直接取 addrs[i]），不过线——2000 点位/批时
// 这些字段占了大半 JSON 体积。
//
// 新增字段是向后兼容的协议变更，由 Meta.APIVersion 把关。
type WireAddr struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DataType string `json:"dataType"`
}

// ReadParams read 请求参数。
type ReadParams struct {
	Session uint64     `json:"session"`
	Addrs   []WireAddr `json:"addrs"`
}

// ReadReply read 应答。
//
// Connected 在**成功与驱动报错两种情况下都必须填**：链路掉线时驱动返回
// error 且 IsConnected()==false，网关据此在下一轮轮询触发重连
// （采集引擎只在 IsConnected()==false 时重连，Read 报错本身不触发）。
type ReadReply struct {
	Results   []driver.ReadResult `json:"results"`
	Connected bool                `json:"connected"`
}

// SessionParams 只需 session 的方法（isConnected / close）的参数。
type SessionParams struct {
	Session uint64 `json:"session"`
}

// IsConnectedReply isConnected 应答。
type IsConnectedReply struct {
	Connected bool `json:"connected"`
}

// PingParams ping 请求参数。
//
// ping **不带 session**：driver.PingDevice 从不调用 Close，若 ping 分配了会话
// 资源则每次「测试连接」泄漏一个。但这不等于必须用临时实例 —— 插件侧会先看看
// 有没有同协议的活跃会话可以复用（串口独占，见 serve.go 的 pingViaSession），
// 复用不分配任何新资源。
type PingParams struct {
	// Protocol 目标协议名（Meta.Protocols 中的一项）。
	Protocol string `json:"protocol"`

	ProtocolJSON string `json:"protocolJson"`
}

// CloseReply 关闭会话的应答，含关闭后的连接状态。
type CloseReply struct {
	Connected bool `json:"connected"`
}

// ProbeParams probe（局域网扫描发现）请求参数。
//
// 与 ping 一样**不带 session**：探测不分配会话、不持有跨调用资源，
// 调用方（devicescan 引擎）也永远不会为它调 Close —— 理由与 PingParams 完全一致，
// 若 probe 分配了会话资源则每次扫描都会泄漏一个。
//
// **一次 RPC 只带一个目标**（不是一批）：契约最简单，插件不必自己写并发与
// 批量预算分摊，吞吐由网关侧的 worker 池控制。代价是 /24 网段会产生 254 次
// RPC —— 对本地管道而言可忽略，而"每目标一次超时"的卡死计数风险由
// pluginhost/client.go 里「取消不计入超时」那条规则消除。
//
// 本类型**刻意只走 JSON**（不实现 pluginwire.BinaryEncoder）：probe 是冷路径，
// 一次扫描最多几百次调用，二进制编码省不下任何可感知的东西，而"意外实现
// BinaryEncoder 导致静默换编码"是 pluginwire 注释里警示过的失败模式。
// driversdk/rpc_test.go 的 TestProbeParamsStayJSON 钉住这一点。
type ProbeParams struct {
	// Protocol 目标协议名（Meta.Protocols 中的一项）。
	Protocol string `json:"protocol"`

	// Options 扫描选项，形状由该协议 DiscoverySpec.FormJSON 定义，原样透给 prober。
	//
	// **用 string 而不是 json.RawMessage**：空的 RawMessage 会让网关侧
	// EncodeBody 的 json.Marshal 校验失败，报出来是一句指向"编码失败"的错，
	// 与真正的根因（用户没填扫描选项）相隔十万八千里。ConnectParams.ProtocolJSON
	// 已是同一个先例。
	Options string `json:"options,omitempty"`

	// Target 本次要探测的单个目标。
	Target driver.ScanTarget `json:"target"`

	// BudgetMs 网关希望本次探测占用的时间上限（毫秒）。插件实际采用的预算取
	// **自己声明的 MaxBudgetMs 与该值的较小者** —— 插件是必须不超时的那一方，
	// 不能把这条不变量交给对端给的值。
	BudgetMs int `json:"budgetMs,omitempty"`
}

// ProbeTargetError 单个目标的探测失败。
//
// **"没扫到"不是错误**，不进这里：目标上跑着别的服务、或者压根没有设备，
// 都是扫描的正常结果（prober 返回空列表 + nil error）。这里只装探测本身
// 失败了的情形（选项解析失败、协议内部错误），以便前端能把原因显示给用户。
type ProbeTargetError struct {
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

// ProbeReply probe 应答。
//
// 一个目标可能产出多台设备：广播探测下多台设备同时应答，单播探测下
// 同一 IP 的多个站号也可能各算一台。
type ProbeReply struct {
	Devices []driver.DiscoveredDevice `json:"devices"`
	Errors  []ProbeTargetError        `json:"errors,omitempty"`
}

// DiscoverPointsParams 点位范围扫描的请求参数。
//
// 与 probe 的区别是**要复用会话**：probe 问的是"这个 IP 上有没有设备"（本机无涉），
// 而点位扫描问的是"这台**已存在的设备**上有哪些点位" —— 它必须读那台设备，
// 而在独占串口的场景下，唯一能读的就是正在采集的那个会话
// （见 driversdk 的 FindPointsRequest.Session 与 serve.go 的回退规则）。
//
// 与 PingParams 一样不带 session 字段：复用哪条连接由插件侧决定，网关侧不持有会话。
//
// 本类型**刻意只走 JSON**（不实现 pluginwire.BinaryEncoder），理由同 ProbeParams。
type DiscoverPointsParams struct {
	// Protocol 目标协议名（Meta.Protocols 中的一项）。
	Protocol string `json:"protocol"`

	// ProtocolJSON 该设备的连接配置。**由网关从 device 行读取后透传**，
	// 不接受客户端的值 —— 它决定网关去连哪台设备。
	ProtocolJSON string `json:"protocolJson"`

	// Options 扫描选项，形状由该协议 PointDiscoverySpec.FormJSON 定义，原样透给 finder。
	// 用 string 而非 json.RawMessage 的理由同 ProbeParams.Options。
	Options string `json:"options,omitempty"`

	// BudgetMs 网关希望本次扫描占用的时间上限（毫秒）。插件实际采用的预算取
	// **自己声明的 MaxBudgetMs 与该值的较小者**。
	//
	// 网关还会把这个值再钳到一个更小的上限（pluginhost 的
	// pointScanGatewayBudgetCapMs），因为这条链路是同步 HTTP ——
	// 详见 specs/point-discovery.md §4。
	BudgetMs int `json:"budgetMs,omitempty"`
}

// DiscoverPointsReply 点位扫描的应答。直接复用契约类型，不另包一层 ——
// 与 discover/probe 的取舍一致（那边也只有 Devices/Errors，包装只会多一层解引用）。
type DiscoverPointsReply = driver.PointScanResult
