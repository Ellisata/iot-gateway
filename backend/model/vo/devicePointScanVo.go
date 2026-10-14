// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

import "encoding/json"

// ==================== 点位范围扫描 VO ====================
//
// 与 deviceScanVo.go 同一条取舍：不把插件契约类型直接暴露到 HTTP 响应里。
// driver.ProtocolPointDiscovery / driver.PointScanResult 是插件与网关之间的契约，
// 它随插件版本演进；前端只该看到网关这一层的稳定形状。

// PointSpecVO 一个协议声明的点位扫描规格。
type PointSpecVO struct {
	// Title 弹窗标题（插件声明，如「扫描 Modbus 地址范围」）。
	Title string `json:"title"`
	// Description 该协议扫描行为的说明。**这段话是用户理解本功能性质的主要来源**
	//（"列出读得到的地址"而不是"设备的点位表"），前端必须原样显示。
	Description string `json:"description,omitempty"`
	// PointFormJSON 扫描选项表单（form-create schema），前端用 <form-create> 渲染。
	PointFormJSON json.RawMessage `json:"pointFormJson,omitempty"`
	// DefaultCommonDataType 结果里类型的默认值，必须是该协议 TypeMap 的**键**
	//（通用类型名，如 "Word"）。前端据此初始化类型下拉。
	DefaultCommonDataType string `json:"defaultCommonDataType"`
	// MaxBudgetMs 插件自报的整段扫描预算（毫秒）。**这是插件的声明值，不是实际
	// 生效的值** —— 网关几乎总会把它钳得更小（同步 HTTP 链路要保证网关能停得下来）。
	// 前端要显示"大约要等多久"时必须用 EffectiveBudgetMs。
	MaxBudgetMs int `json:"maxBudgetMs"`
	// EffectiveBudgetMs 本次扫描**实际生效**的整段预算（毫秒），即
	// min(MaxBudgetMs, 网关上限)。与 MaxBudgetMs 可以差好几倍（Modbus 声明
	// 20000、实生效 7000），显示错了用户会去怀疑设备与链路，而根因在网关。
	EffectiveBudgetMs int `json:"effectiveBudgetMs"`
	// MaxPoints 单次扫描最多返回多少个点位，达到即截断（结果里的 truncated）。
	MaxPoints int `json:"maxPoints"`
}

// PointCapabilityVO 某台设备的协议是否支持点位扫描，以及它的规格。
//
// Enabled 与 Supported 分开：前者是网关的总开关（deviceScan.enabled，点扫复用
// 它，不开新开关），后者是协议能力。两者都为真时按钮才可用 —— 前端要能把
// "功能关了"与"这个协议不支持"区分开，否则用户只能看到一个没有理由的灰按钮。
type PointCapabilityVO struct {
	Enabled   bool         `json:"enabled"`
	Supported bool         `json:"supported"`
	Protocol  string       `json:"protocol"`
	Spec      *PointSpecVO `json:"spec,omitempty"`
}

// PointScanPointVO 扫描到的一个点位。
//
// 字段名与 device_address 的列对齐（name/label），因为前端要把这三项直接
// 编辑成待提交的行；value/note 是只读的辨认信息。
type PointScanPointVO struct {
	// Name **设备地址写法**（如 %MW100），直接可用作 device_address.name。
	// 前端必须把它渲染成**只读**列 —— 写坏一个地址的后果是这个点位永远没有值
	// （只报「地址无效」，要改配置才恢复），不该让用户顺手在这里改它。
	Name string `json:"name"`

	// Value 当前值，按 AssumedCommonDataType 渲染。**值为 0 的点位这里为空串**
	// 并带 Note，前端的"显示 0 值"开关据此过滤（插件侧刻意不做过滤：
	// 在所有寄存器都是 0 的设备上过滤会返回空列表，用户会以为"这里没有点位"）。
	Value string `json:"value,omitempty"`

	// Label 插件建议的标签。多数协议没有这个信息（设备不会告诉你"这个寄存器是
	// 温度"），通常为空，由用户自己填。
	Label string `json:"label,omitempty"`

	// CommonDataType 插件为**这一个点位**建议的通用类型名（TypeMap 的键，如 "Long"），
	// 前端用它初始化该行的类型下拉。
	//
	// 只有原生枚举协议（Rockwell.CIP）会填：那里每个点位的类型由设备自报，逐点不同，
	// 结果级的 AssumedCommonDataType 表达不了。为空时前端照旧回退到
	// assumedCommonDataType → spec.defaultCommonDataType 那条链，因此老协议不受影响。
	CommonDataType string `json:"commonDataType,omitempty"`

	// Note 补充说明，如「当前值为 0」。
	Note string `json:"note,omitempty"`
}

// PointScanResultVO 一次点位扫描的结果。
//
// 三个计数量 + 两个标志的语义必须分清，前端也不能混着显示：
//
//	scanned    本次尝试读取的地址数
//	readable   有应答的地址数（**含值为 0 的**）
//	unreadable 读不到的地址数
//	truncated  达到 maxPoints，后面的地址根本没试
//	incomplete 预算耗尽，范围还没扫完 —— 与"读不到"是两回事
//
// 不变量：len(points) <= readable <= scanned，readable + unreadable == scanned。
// incomplete 时三者都只是**已完成部分**的统计。
type PointScanResultVO struct {
	// Protocol 本次扫描用的协议名，供前端回显与排查。
	Protocol string `json:"protocol"`

	Points []PointScanPointVO `json:"points"`

	// AssumedCommonDataType 本次扫描为了保证能读而假设的通用类型名
	//（寄存器区是 Word、位区是 Boolean）。是类型下拉的**兜底初值**（每一行优先用
	// Points[].CommonDataType）。
	//
	// **空 == 本次扫描没有采样值**（原生枚举协议，如 Rockwell.CIP 只列标签与类型、
	// 不读值），见 driver.PointScanResult 上的同一段约定。前端据此隐藏「显示 0 值」
	// 开关与零值统计 —— 那一列全是空的，开关留着只会让人以为数据丢了。
	AssumedCommonDataType string `json:"assumedCommonDataType,omitempty"`

	Scanned    int `json:"scanned"`
	Readable   int `json:"readable"`
	Unreadable int `json:"unreadable"`

	Truncated  bool `json:"truncated"`
	Incomplete bool `json:"incomplete"`

	// Warning 非空表示这次扫描**中途断了**，而 Points 是断之前已经读到的部分。
	//
	// 与"扫描失败"（业务错误码 + 没有 Points）是两回事：那时插件一个地址都没读到，
	// 这里则是"跑了一半、拿回一半"。前端必须把这段话显示出来并照常允许建点位 ——
	// 用户拿到的仍是可用的东西，但他得知道这份清单是残缺的。
	//
	// 文案由服务端拼（根因是动态的，拼的是插件的原文，见 pointScanInterruptMessage），
	// 与 pointScanError 同一条取舍。
	Warning string `json:"warning,omitempty"`

	ElapsedMs int `json:"elapsedMs"`
}
