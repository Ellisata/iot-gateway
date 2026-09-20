// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"

	"iot-gateway/logger"
)

// 时间类数据标识的字节布局标识（DIInfo.Layout 取值）。
// DL/T 645 的日期时间为 BCD 编码且字节序固定，各字节含义不同，
// 不能按普通数值解码，需按布局逐字节取 BCD 位。
const (
	layoutNone = ""
	// layoutDate 4 字节，线上顺序：星期、日、月、年。
	// 规范的书写形式是 YYMMDDWW，而数据项按低字节在前传输，故报文里最先到的是星期。
	layoutDate = "date"
	layoutTime = "time" // 3 字节，线上顺序：秒、分、时
	layoutAddr = "addr" // 6 字节：表号 / 通信地址，BCD 数字串
)

// layoutBytes 返回布局占用的字节数。
func layoutBytes(layout string) int {
	switch layout {
	case layoutDate:
		return 4
	case layoutTime:
		return 3
	case layoutAddr:
		return 6
	default:
		return 0
	}
}

// DIInfo 数据标识的规格信息。
//
// 关键点：DL/T 645 的**字节数、小数位、符号、编码方式由数据标识 DI 本身决定**，
// 不由配置界面的数据类型下拉决定。字典为常见 DI 提供默认规格，
// 未收录的厂商私有 DI 由地址后缀显式指定（见 address.go）。
type DIInfo struct {
	Name     string // 数据项名称
	Unit     string // 单位
	Bytes    int    // 数据字节数
	Decimals int    // 小数位数（工程值 = 原始整数 / 10^Decimals）
	Signed   bool   // 有符号：BCD 时符号位为最高字节 bit7；二进制时为补码
	Binary   bool   // 二进制编码（默认 BCD）
	Layout   string // 时间类布局（layoutXxx），非空时 Bytes 必须与布局一致
	// Verified 是否为多来源交叉核实过的条目。
	// false 表示「厂商手册间存在分歧、待真机核实」——数值可能偏，但不会报错，
	// 现场比对不上时用地址后缀显式覆盖即可。
	Verified bool
}

// ==================== 2007 版数据标识字典 ====================
//
// 数据标识为 4 字节，书写形式为 DI3DI2DI1DI0（大端书写，线上按低字节在前发送）。

var diDict2007 = map[uint32]DIInfo{
	// ---- 电能量（4 字节，2 位小数）----
	0x00000000: {Name: "组合有功总电能", Unit: "kWh", Bytes: 4, Decimals: 2, Verified: true},
	0x00010000: {Name: "正向有功总电能", Unit: "kWh", Bytes: 4, Decimals: 2, Verified: true},
	0x00020000: {Name: "反向有功总电能", Unit: "kWh", Bytes: 4, Decimals: 2, Verified: true},
	0x00030000: {Name: "组合无功1总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},
	0x00040000: {Name: "组合无功2总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},
	0x00050000: {Name: "第一象限无功总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},
	0x00060000: {Name: "第二象限无功总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},
	0x00070000: {Name: "第三象限无功总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},
	0x00080000: {Name: "第四象限无功总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},

	// ---- 电压（2 字节，1 位小数，无符号）----
	0x02010100: {Name: "A相电压", Unit: "V", Bytes: 2, Decimals: 1, Verified: true},
	0x02010200: {Name: "B相电压", Unit: "V", Bytes: 2, Decimals: 1},
	0x02010300: {Name: "C相电压", Unit: "V", Bytes: 2, Decimals: 1},

	// ---- 电流（3 字节，3 位小数，有符号）----
	0x02020100: {Name: "A相电流", Unit: "A", Bytes: 3, Decimals: 3, Signed: true, Verified: true},
	0x02020200: {Name: "B相电流", Unit: "A", Bytes: 3, Decimals: 3, Signed: true},
	0x02020300: {Name: "C相电流", Unit: "A", Bytes: 3, Decimals: 3, Signed: true},

	// ---- 功率（3 字节，4 位小数，有符号）----
	0x02030000: {Name: "瞬时总有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0x02030100: {Name: "瞬时A相有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0x02030200: {Name: "瞬时B相有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0x02030300: {Name: "瞬时C相有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0x02040000: {Name: "瞬时总无功功率", Unit: "kvar", Bytes: 3, Decimals: 4, Signed: true},
	0x02040100: {Name: "瞬时A相无功功率", Unit: "kvar", Bytes: 3, Decimals: 4, Signed: true},
	0x02040200: {Name: "瞬时B相无功功率", Unit: "kvar", Bytes: 3, Decimals: 4, Signed: true},
	0x02040300: {Name: "瞬时C相无功功率", Unit: "kvar", Bytes: 3, Decimals: 4, Signed: true},
	0x02050000: {Name: "瞬时总视在功率", Unit: "kVA", Bytes: 3, Decimals: 4, Signed: true},
	0x02050100: {Name: "瞬时A相视在功率", Unit: "kVA", Bytes: 3, Decimals: 4, Signed: true},
	0x02050200: {Name: "瞬时B相视在功率", Unit: "kVA", Bytes: 3, Decimals: 4, Signed: true},
	0x02050300: {Name: "瞬时C相视在功率", Unit: "kVA", Bytes: 3, Decimals: 4, Signed: true},

	// ---- 功率因数与频率 ----
	0x02060000: {Name: "总功率因数", Bytes: 2, Decimals: 3, Signed: true},
	0x02060100: {Name: "A相功率因数", Bytes: 2, Decimals: 3, Signed: true},
	0x02060200: {Name: "B相功率因数", Bytes: 2, Decimals: 3, Signed: true},
	0x02060300: {Name: "C相功率因数", Bytes: 2, Decimals: 3, Signed: true},
	0x02800001: {Name: "电网频率", Unit: "Hz", Bytes: 2, Decimals: 2, Verified: true},
	0x02800002: {Name: "表内温度", Unit: "℃", Bytes: 2, Decimals: 1, Signed: true},

	// ---- 日期时间与身份 ----
	0x04000101: {Name: "日期及星期", Bytes: 4, Layout: layoutDate},
	0x04000102: {Name: "时间", Bytes: 3, Layout: layoutTime},
	0x04000401: {Name: "通信地址", Bytes: 6, Layout: layoutAddr},
	0x04000402: {Name: "表号", Bytes: 6, Layout: layoutAddr},

	// ---- 运行状态（二进制位域，非 BCD）----
	0x04000501: {Name: "电表运行状态字1", Bytes: 2, Binary: true},
	0x04000502: {Name: "电表运行状态字2", Bytes: 2, Binary: true},
	0x04000503: {Name: "电表运行状态字3", Bytes: 2, Binary: true},
	0x04000504: {Name: "电表运行状态字4", Bytes: 2, Binary: true},
	0x04000505: {Name: "电表运行状态字5", Bytes: 2, Binary: true},
	0x04000506: {Name: "电表运行状态字6", Bytes: 2, Binary: true},
	0x04000507: {Name: "电表运行状态字7", Bytes: 2, Binary: true},
}

// ==================== 1997 版数据标识字典 ====================
//
// 数据标识为 2 字节，书写形式为 DI1DI0（大端书写，线上按低字节在前发送）。
//
// 注意：1997 版各厂商手册对同一数据项的数据标识与格式存在分歧，
// 本表仅收录与 2007 版可对应、结构上可推断的条目，且除少数交叉核实者外
// 均标记为待真机核实。现场务必用报文比对确认后再依赖。

var diDict1997 = map[uint32]DIInfo{
	// ---- 电能量 ----
	0x9010: {Name: "正向有功总电能", Unit: "kWh", Bytes: 4, Decimals: 2, Verified: true},
	0x9020: {Name: "反向有功总电能", Unit: "kWh", Bytes: 4, Decimals: 2},
	0x9030: {Name: "正向无功总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},
	0x9040: {Name: "反向无功总电能", Unit: "kvarh", Bytes: 4, Decimals: 2},

	// ---- 电压 ----
	0xB611: {Name: "A相电压", Unit: "V", Bytes: 2, Decimals: 1, Verified: true},
	0xB612: {Name: "B相电压", Unit: "V", Bytes: 2, Decimals: 1},
	0xB613: {Name: "C相电压", Unit: "V", Bytes: 2, Decimals: 1},

	// ---- 电流（与 2007 的 0202xxxx 对应）----
	0xB621: {Name: "A相电流", Unit: "A", Bytes: 3, Decimals: 3, Signed: true},
	0xB622: {Name: "B相电流", Unit: "A", Bytes: 3, Decimals: 3, Signed: true},
	0xB623: {Name: "C相电流", Unit: "A", Bytes: 3, Decimals: 3, Signed: true},

	// ---- 功率 ----
	0xB630: {Name: "瞬时总有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0xB631: {Name: "瞬时A相有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0xB632: {Name: "瞬时B相有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0xB633: {Name: "瞬时C相有功功率", Unit: "kW", Bytes: 3, Decimals: 4, Signed: true},
	0xB640: {Name: "瞬时总无功功率", Unit: "kvar", Bytes: 3, Decimals: 4, Signed: true},
	0xB650: {Name: "瞬时总视在功率", Unit: "kVA", Bytes: 3, Decimals: 4, Signed: true},
	0xB660: {Name: "总功率因数", Bytes: 2, Decimals: 3, Signed: true},

	// ---- 身份 ----
	0xC032: {Name: "表号", Bytes: 6, Layout: layoutAddr},
}

// LookupDI 按版本与数据标识查字典。version 非 "1997" 时一律按 2007 处理。
func LookupDI(version string, di uint32) (DIInfo, bool) {
	if version == Version1997 {
		info, ok := diDict1997[di]
		return info, ok
	}
	info, ok := diDict2007[di]
	return info, ok
}

// verifyDictionary 启动自检数据字典的结构合法性，把表内笔误变成一行日志而非脏数据。
//
// 检查项：
//   - 数据标识宽度是否符合版本约束（1997 必须落在 2 字节内）；
//   - 字节数是否在 1~8 的合理范围内；
//   - 小数位数是否超过该字节数能表达的 BCD 位数；
//   - 时间类布局声明的字节数是否与布局实际占用一致；
//   - 统计待真机核实条目数（点名提示，避免现场误信）。
func verifyDictionary() {
	for _, d := range []struct {
		version string
		dict    map[uint32]DIInfo
	}{
		{Version2007, diDict2007},
		{Version1997, diDict1997},
	} {
		unverified := make([]string, 0, 8)
		for di, info := range d.dict {
			if d.version == Version1997 && di > 0xFFFF {
				logger.Warn("dlt645: 1997 字典数据标识 %04X 超出 2 字节范围（%s）", di, info.Name)
			}
			if info.Bytes < 1 || info.Bytes > 8 {
				logger.Warn("dlt645: 字典条目 DI=%X（%s）字节数 %d 非法，应为 1~8",
					di, info.Name, info.Bytes)
			}
			if info.Decimals < 0 || info.Decimals > info.Bytes*2 {
				logger.Warn("dlt645: 字典条目 DI=%X（%s）小数位 %d 超过 %d 字节 BCD 可表达范围",
					di, info.Name, info.Decimals, info.Bytes)
			}
			if info.Layout != layoutNone {
				if want := layoutBytes(info.Layout); want != info.Bytes {
					logger.Warn("dlt645: 字典条目 DI=%X（%s）布局 %q 需 %d 字节，声明为 %d 字节",
						di, info.Name, info.Layout, want, info.Bytes)
				}
			}
			if !info.Verified {
				unverified = append(unverified, fmt.Sprintf("%s(%s)", info.Name, formatDI(di, d.version)))
			}
		}
		if len(unverified) > 0 {
			logger.Info("dlt645: %s 版字典共 %d 条，其中待真机核实 %d 条：%v",
				d.version, len(d.dict), len(unverified), unverified)
		} else {
			logger.Info("dlt645: %s 版字典共 %d 条，均已核实", d.version, len(d.dict))
		}
	}
}
