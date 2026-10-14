// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

// 点位报警条件的比较符。落库在 device_address.threshold_op，
// 操作数是 JSON 数组字符串（device_address.threshold_value）。
//
// 为什么比较符要和 Kind 绑在一起、且放在 driver 包：一张表要同时被三处读 ——
// 配置保存时的校验（service）、采集时的判定（alarm）、以及前端下拉
// （经 /protocol/getDataTypes 的 kindByType）。三处各写一份必然漂移，
// 而 Kind 的真源本来就在本包。
const (
	// OpRange 数值区间：操作数 [min, max]，任一为空表示该侧不检查，
	// 越限 = v < min || v > max。**它不是"一个比较"** —— 是数值区间的原语，
	// 刻意保留成一个算子，否则"上下限都要"这个最常用的形态就得配两条条件。
	OpRange = "range"
	OpGt    = "gt" // v >  x
	OpGe    = "ge" // v >= x
	OpLt    = "lt" // v <  x
	OpLe    = "le" // v <= x
	OpEq    = "eq" // v == x
	OpNe    = "ne" // v != x
	// OpIn 文本集合成员：操作数是集合，命中任一即满足。
	OpIn = "in"
)

// 各 Kind 可用的比较符集合。
//
// 时间类型**刻意不支持**：对时间值做区间比较几乎总是想表达"这个点位多久没更新了"，
// 而那比的是当前时间、不是配置里的边界，是另一个功能。显式不支持好过给一个
// 语义可疑的开关 —— 见 driversrc 里 date 类型被格式化成 RFC3339 字符串的事实。
var operatorsByKind = map[string][]string{
	KindInt:    {OpRange, OpGt, OpGe, OpLt, OpLe, OpEq, OpNe},
	KindUInt:   {OpRange, OpGt, OpGe, OpLt, OpLe, OpEq, OpNe},
	KindFloat:  {OpRange, OpGt, OpGe, OpLt, OpLe, OpEq, OpNe},
	KindBool:   {OpEq, OpNe},
	KindString: {OpEq, OpNe, OpIn},
	// KindTime 不在表里：任何算子都不可用。
}

// OperatorsForKind 返回某数据类型类别可用的比较符（副本，调用方可安全修改）。
// 不支持的类别（含 KindTime 与未知/空串）返回 nil。
//
// 返回副本而不是内部切片：调用方主要是 HTTP 响应与校验循环，
// 一个共享切片被意外改写会让"某个协议突然少了个比较符"，且极难查。
func OperatorsForKind(kind string) []string {
	ops, ok := operatorsByKind[kind]
	if !ok {
		return nil
	}
	out := make([]string, len(ops))
	copy(out, ops)
	return out
}

// OperatorAllowed 判断某 Kind 是否支持某比较符。
func OperatorAllowed(kind, op string) bool {
	for _, o := range operatorsByKind[kind] {
		if o == op {
			return true
		}
	}
	return false
}

// IsNumericKind 判断是否为可比数值类别（区间/大小比较只对它们有意义）。
func IsNumericKind(kind string) bool {
	switch kind {
	case KindInt, KindUInt, KindFloat:
		return true
	}
	return false
}
