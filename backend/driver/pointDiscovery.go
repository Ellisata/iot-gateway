// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import "encoding/json"

// 本文件是「设备点位范围扫描」的**协议无关契约**：插件声明能力
// （PointDiscoverySpec），网关把一个已存在的设备的连接配置交给它，它返回该范围内
// 读得到的地址清单。
//
// 与 discovery.go 的关系（两者都是"扫描发现"，但支点完全不同）：
//
//	discovery.go   扫**网络**：网关规划目标（网段展开），插件回答"这个 IP:端口是不是我的设备"。
//	               一次 RPC 一个目标、无会话、可取消。
//	本文件          扫**设备**：目标就是一台已存在的设备，插件负责读取与降级。
//	               一次 RPC 覆盖整段、**要复用会话**、不可取消。
//
// 共同的三条道理（改一篇必须同时改另一篇，否则必然漂移）：
// ① 能力每次现算、不建缓存表（热升级只换 Meta，而 registrationSig 不含这两项）；
// ② 预算由插件声明 `MaxBudgetMs`，网关侧的 RPC 超时按它推导，不设全局配置旋钮；
// ③ 串口独占与会话复用。完整约定见 specs/point-discovery.md 与 specs/device-scan.md。

// PointDiscoverySpec 插件为某个协议声明的「点位范围扫描」能力。
//
// 在 driversdk.Meta 的 Protocol 里以指针出现：**nil 表示该协议不支持点位扫描**。
// 声明了它就必须在 driversdk.Capabilities.PointFinders 里给出对应实现，
// 否则插件启动即失败（与 Discovery / 工厂表同一条校验）。
//
// 可选字段 ⇒ 不递增 APIVersion：新网关 + 旧插件走 nil 分支，旧网关 + 新插件忽略未知字段。
type PointDiscoverySpec struct {
	// Title 设备地址页那个按钮点开后，弹窗里的标题（如「扫描 Modbus 寄存器」）。
	Title string `json:"title"`

	// Description 该协议扫描行为的说明，展示给用户（如"只能列出读得到的地址，
	// 设备不会告诉你它有哪些点位"）。**这段文案是用户理解本功能性质的主要来源**，
	// 插件作者应当写清楚自己这个协议的局限。
	Description string `json:"description,omitempty"`

	// FormJSON 扫描选项表单（form-create schema），与 Protocol.FormJSON 同构。
	// 用户在弹窗里填的内容由网关原样透传给 finder（见 driversdk.DiscoverPointsParams.Options）。
	FormJSON json.RawMessage `json:"formJson,omitempty"`

	// DefaultCommonDataType 结果里类型的**默认值**（前端下拉的初值），必须是该协议
	// TypeMap 的键（如 "Word"），由 Meta.Validate 校验。
	//
	// 注意它与 PointScanResult.AssumedCommonDataType 是两件事：本字段是"下拉框里
	// 预先选中哪一项"，那个是"本协议为了保证能读，实际按哪种类型去读的假设"。
	//
	// **刻意不让插件自动推断类型**：网关侧拿不到解码函数（DataType.Decode 带
	// json:"-"），而按值推断必然出错 —— 模拟量恒为 0/1 时会被推断成 bool，
	// 且这种错误只在时序库里表现为"字段类型变了"，比 panic 更难发现
	//（同 driversrc/abb/abbdrv/types.go 的 resolveKind 那段论证）。
	DefaultCommonDataType string `json:"defaultCommonDataType"`

	// MaxBudgetMs 插件自报的**整段扫描硬预算**（毫秒）。与 Discovery 的同名字段
	// 一样，它不是"期望值"而是"承诺"，网关侧的 RPC 超时按它推导。
	//
	// 网关还会额外把请求预算钳到一个**更小的上限**（见 pluginhost 的
	// pointScanGatewayBudgetCapMs），原因是这条链路是同步 HTTP —— 详见
	// specs/point-discovery.md §4。超预算时的正确行为是**带上已完成的部分结果
	// 正常返回**并把 Incomplete 置位，不是返回 error。
	MaxBudgetMs int `json:"maxBudgetMs"`

	// MaxPoints 单次扫描最多返回多少个点位。达到它即停止并置 Truncated。
	//
	// 上限存在的意义不是性能而是**事故半径**：一个 2000 寄存器的范围会产生一份
	// 上百 KB 的应答，而 pluginwire 写帧持插件进程的写锁，大帧会头阻塞该进程上
	// 所有协议的应答。取值由 Meta.Validate 限死在 [1, 20000]。
	MaxPoints int `json:"maxPoints"`
}

// ProtocolPointDiscovery 协议 → 点位扫描规格的绑定，供前端列举可点扫的协议。
type ProtocolPointDiscovery struct {
	// Protocol 协议名（= Meta.Protocols[].Name = iot_protocol.name）。
	Protocol string `json:"protocol"`

	// Spec 该协议声明的点位扫描规格。
	Spec PointDiscoverySpec `json:"spec"`
}

// DiscoveredPoint 扫描到的一个点位。
type DiscoveredPoint struct {
	// Name **直接可用的设备地址写法**（就是要写进 device_address.name 的那个串）。
	//
	// 它必须能被该协议自己的地址解析器还原回同一个位置 —— 这条不变量比它看起来
	// 重要得多：一个解析不出来的地址写进 device_address 之后，这个点位**永远采不到值**
	// （六个区间规划器 —— modbus / s7 / fins / mitsubishi / Omron.CIP / Rockwell.CIP
	// —— 都在规划前把这类地址拆出去，只让它报 QualityBadAddress / 报警「地址无效」），
	// 运维得去改配置才恢复。各协议应当用一条往返测试把它钉住。
	Name string `json:"name"`

	// Value 当前值，按 PointScanResult.AssumedCommonDataType 渲染。**仅供辨认**：
	// 它只是读取时刻的采样，不参与任何判定，也没有类型保证。
	Value string `json:"value,omitempty"`

	// Label 建议的中文说明。多数协议没有这个信息（设备不会告诉你"这个寄存器是温度"），
	// 因此通常为空，由用户在前端自己填。
	Label string `json:"label,omitempty"`

	// CommonDataType **本点位建议的通用类型名**（TypeMap 的键，如 "Long"），
	// 供前端初始化该行的类型下拉。
	//
	// 只有**原生枚举**类协议会填它：那里每个点位的类型是**设备自己报的**
	//（Rockwell 的 CIP 类型码），逐点不同，而结果级的 AssumedCommonDataType
	// 只有一个值，表达不了这件事 —— 把 DINT 与 REAL 的标签都按同一个类型建成
	// 点位，采集时解出来的值就是错的。
	//
	// 读一段地址的协议（Modbus/S7/FINS）**留空**：它们整段共用一个假设类型，
	// 用户逐行去改本来就是这个功能预期中的工作量。
	//
	// 可选字段 ⇒ 不递增 APIVersion：旧网关忽略未知字段，空值走
	// AssumedCommonDataType 那条既有回退链。
	CommonDataType string `json:"commonDataType,omitempty"`

	// Note 补充说明，如「当前值为 0」「超出设备支持范围」。
	Note string `json:"note,omitempty"`
}

// PointScanResult 一次点位扫描的结果。
//
// 三个计数量 + 两个标志的语义必须分清，否则用户会从"没扫到"得出错误结论
// （与 specs/device-scan.md §9 那条"这条提示不能省"是同一类要求）：
//
//	Scanned    本次尝试读取的地址数
//	Readable   有应答的地址数（**含值为 0 的**）
//	Unreadable 读不到的地址数（协议明确报"该地址不存在"那一类）
//	Truncated  达到 MaxPoints，后面的地址根本没试 —— 与"读不到"是两回事
//	Incomplete 预算耗尽，范围还没扫完 —— 与"读不到"也是两回事
//
// 不变量：len(Points) <= Readable <= Scanned，且 Readable + Unreadable == Scanned。
// Incomplete 时三者都只是**已完成部分**的统计。
type PointScanResult struct {
	Points []DiscoveredPoint `json:"points"`

	// AssumedCommonDataType 本次扫描**为了保证能读**而假设的通用类型名
	// （寄存器区通常是 Word、位区是 Boolean）。前端据此标注"当前值按 X 渲染"。
	//
	// **空 == 本次扫描没有采样值**，这是一条跨端约定（前端据此隐藏"显示 0 值"
	// 开关与零值统计，并改用"不含采样值"的那套文案）。原生枚举类协议
	// （Rockwell.CIP）就是这种情况：它列出的是设备自报的标签与各自的类型，
	// 一次请求换一个标签名，不读值，也就没有一个统一的"假设类型"可言。
	// 换句话说，这个字段空着**不是"没填"**，是在表达一件事实。
	AssumedCommonDataType string `json:"assumedCommonDataType,omitempty"`

	Scanned    int `json:"scanned"`
	Readable   int `json:"readable"`
	Unreadable int `json:"unreadable"`

	Truncated  bool `json:"truncated"`
	Incomplete bool `json:"incomplete"`

	ElapsedMs int `json:"elapsedMs,omitempty"`
}
