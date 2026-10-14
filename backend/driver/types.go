// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import "encoding/binary"

// DataType 数据类型定义。
//
// 每种协议在 init() 中通过 TypeRegistry 注册自己的编解码器。
// Decode/Encode 接收 binary.ByteOrder 处理字节序，协议特有逻辑（如 Modbus 字序交换）
// 在协议层的 ParseReadResult/EncodeWriteValue 中提前处理。
//
// 序列化契约（进程外驱动插件依赖）：
// 三个函数字段带 `json:"-"`，因此 DataType 的 JSON 形式恰好是 {name, kind, size}。
// 插件通过 describe 把自己的类型表传给网关时，编解码函数**不出插件进程**；
// 网关侧反序列化得到的是 Decode/Encode/Format 为 nil 的 DataType。
// 这成立的前提是：**网关侧代码永不调用这三个函数**。
// 网关只用它们做类型存在性判定（LookupScopeType/AvailableTypes/NormalizeAddressType）
// 与 Kind 分类，而采集值本身由驱动格式化好放在 ReadResult.Value 里（见 collector.toRecords）。
// 若将来网关侧需要真正解码，必须改为让插件在 RPC 层完成，而不是在这里去掉 json:"-"。
type DataType struct {
	// Name 内部标识名，全小写，如 "bool", "int16", "float32"
	Name string `json:"name"`

	// Kind 数据类型类别（KindBool/KindInt/KindUInt/KindFloat/KindString/KindTime）。
	// 推送通道按 Kind 决定时序库存储表示，而非枚举类型名（见 push 通道 formatFieldValue/
	// parseValueByType）。各协议注册时声明一次，为分类的唯一事实来源。
	Kind string `json:"kind"`

	// Size 数据占用字节数。
	// 0 表示动态长度（如 string），由协议层自行确定。
	Size int `json:"size"`

	// Decode 将原始字节解码为 Go 值。
	// raw 已被协议层预处理过（字序交换等），只做字节序转换。
	Decode func(raw []byte, bo binary.ByteOrder) (any, error) `json:"-"`

	// Encode 将值编码为原始字节。
	// val 通常是 float64（来自 JSON 反序列化），也支持原生类型。
	Encode func(val any, bo binary.ByteOrder) ([]byte, error) `json:"-"`

	// Format 将解码后的值格式化为字符串（用于存储和展示）。
	Format func(val any) string `json:"-"`
}

// PLC 数据类型类别（DataType.Kind 的取值）。
// 所有协议的类型最终坍缩到这 6 类；推送通道按类别分派，新增协议类型只需注册时声明 Kind。
const (
	KindBool   = "bool"   // 布尔
	KindInt    = "int"    // 有符号整数
	KindUInt   = "uint"   // 无符号整数
	KindFloat  = "float"  // 浮点
	KindString = "string" // 文本（string/char）
	KindTime   = "time"   // 日期/时间值（date/time/tod/s5time/dt；当前通道按字符串存储）
)

// 采集质量码（driver.ReadResult.Quality 的取值）。
//
// 布局沿袭既有的 192 / 128 / 0：**高 2 位是类别，低 6 位是子码**，因此任何子码
// 都必须 < 64，免得撞上类别位。
//
//	0b11_000000 = 192  QualityGood       正常
//	0b10_000000 = 128  QualityUncertain  不确定，但值可用（目前只有 ABB 的仿真信号）
//	0b00_000000 = 0    QualityBad        坏，未归类
//
// 子码这一维存在的唯一理由，是把两种**性质相反**的失败分开：地址级失败永不自我
// 恢复，通信级失败下一轮就重试。只看 0 时两者无从分辨，下游（点位级报警、可解释性
// 报表）不知道该报"测点坏了"还是"这轮通信抖了一下"。
//
//	QualityBadAddress  地址级失败：这个地址本身坏/不存在，设备是好的，属**永不自我
//	                   恢复**的配置错误 —— 这是它区别于 Comms 的依据。两个来源性质
//	                   相同：① **本地就没法把它放进计划**（解析器认不出这个地址、它
//	                   声明的类型/长度放不进单次请求、或配置模式下与 startAddress 对
//	                   不上 —— 三种都一个字节没上过线；驱动据此把它摘出区间计划，只让
//	                   这一个点位报坏，而不是让整批 Read 失败）、② 设备单独确认过它
//	                   （对半降级到单点仍被拒，或设备明确回"地址非法"）。
//	QualityBadComms    通信级失败：整段被设备以可恢复状态拒绝（从站忙、节点忙、
//	                   网关错…）。未确认任何点位，留在计划里下一轮原样重试。
//	                   **不含链路超时**：传输层超时/EOF 会让整个 Read 返回 error，
//	                   根本不产生 ReadResult；本值专指"设备在带内拒绝了"。
//	QualityBadParse    解析级失败：设备应答了，但本地对这一点位/这段响应的解码失败
//	                   （类型不符、字节数不足、响应形状异常）。地址与链路都没问题。
//	QualityBad         未分类（默认）：认不出来的设备状态码/错误码（不猜）、无法定位
//	                   的错误（如 DL/T 645 的"其他错误"，表号不对与数据标识没实现
//	                   都报它）、分类不适用之处，以及预算耗尽被**延迟**的点位
//	                   （那是暂缓，不是设备判定）。
const (
	QualityGood      = 192
	QualityUncertain = 128
	QualityBad       = 0

	QualityBadAddress = 1
	QualityBadComms   = 2
	QualityBadParse   = 3
)

// QualityUsable 报告该质量码下的值是否可用 —— 正常，或"不确定但值可用"。
//
// 判定"这一轮读到了可用的值没有"必须用它，**不能写 `q != 0`**：坏点位不再恒为 0
// （见上面的子码），`q != 0` 会把 1/2/3 全当成可用值放行。
func QualityUsable(q int) bool { return q == QualityGood || q == QualityUncertain }
