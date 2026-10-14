// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// DataTypeOptionVO 协议可配置数据类型选项（配置界面类型下拉）。
// CommonTypes 为该协议支持的通用类型（CommonDataTypes 顺序）；
// ExtendedTypes 为该协议专属的扩展类型（如 Modbus 的 Int / Real），
// 不在通用列表中、仅该协议可见。
// AllTypes 为 CommonTypes 与 ExtendedTypes 的合并集合，便于集中展示。
// KindByType 为「通用类型名 → 数据类型类别」（driver.KindXxx）。
//
// 为什么需要它：前端的报警条件编辑器必须知道当前选中的数据类型的**类别**
// （数值/布尔/文本）才能给出正确的比较符下拉 —— 只给类型名字的话，
// "Boolean" 与 "Float" 在前端长得一样。可用比较符的矩阵在 driver.OperatorsForKind。
type DataTypeOptionVO struct {
	Protocol      string            `json:"protocol"`
	CommonTypes   []string          `json:"commonTypes"`
	ExtendedTypes []string          `json:"extendedTypes"`
	AllTypes      []string          `json:"allTypes"`
	KindByType    map[string]string `json:"kindByType"`
}
