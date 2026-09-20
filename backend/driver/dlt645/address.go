// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"strconv"
	"strings"
)

// 数据域偏移：DL/T 645 数据域每个字节收发各 +/- 0x33。
// 该校验和无关的偏移同时作用于数据标识与数据内容，C/L/地址域/CS/16H 不参与。
const dataOffset = 0x33

// ParseAddress 解析点位地址字符串为数据标识规格。
//
// 语法：DI[:字节数[:小数位[:标志]]]
//
//	DI      数据标识十六进制，2007 版 8 位（如 02010100），1997 版 4 位（如 9010）。
//	        可带 0x 前缀，不区分大小写。书写形式为大端（DI3DI2DI1DI0），
//	        线上按低字节在前发送，由 DIWireBytes 处理反转与 0x33 偏移。
//	字节数  1~8，省略或留空表示沿用内置字典。
//	小数位  0~16，省略或留空表示沿用内置字典。
//	标志    可组合的字符：s=有符号、u=无符号、b=二进制、d=BCD。
//	        标志**叠加**在字典规格之上而非替换，因此 ":s" 不会把二进制 DI 重置回 BCD。
//
// 示例：
//
//	02010100         内置字典 → 2 字节 1 位小数（A相电压）
//	00000000         内置字典 → 4 字节 2 位小数（组合有功总电能）
//	02020100:3:3     显式覆盖字节数与小数位
//	12345678:2:0:s   2 字节有符号 BCD（厂商私有数据标识）
//	04000501:2:0:b   2 字节二进制位域
//
// 未收录于字典且未显式给出字节数时返回错误——此时无法推断数据长度，
// 只能由使用者按手册指定，避免"猜到"一个长度而读到错误的数据。
func ParseAddress(name, version string) (DISpec, error) {
	s := strings.TrimSpace(name)
	if s == "" {
		return DISpec{}, fmt.Errorf("dlt645: 地址为空")
	}

	parts := strings.Split(s, ":")
	if len(parts) > 4 {
		return DISpec{}, fmt.Errorf("dlt645: 地址 %q 格式错误，应为 DI[:字节数[:小数位[:标志]]]", name)
	}

	diBytes := 4
	diDigits := 8
	if version == Version1997 {
		diBytes = 2
		diDigits = 4
	}

	// ---- 数据标识 ----
	hexPart := strings.TrimSpace(parts[0])
	hexPart = strings.TrimPrefix(strings.TrimPrefix(hexPart, "0x"), "0X")
	if len(hexPart) != diDigits {
		example := "02010100"
		if diBytes == 2 {
			example = "9010"
		}
		return DISpec{}, fmt.Errorf(
			"dlt645: 数据标识 %q 长度错误，%d 字节版应为 %d 位十六进制（如 %s）",
			parts[0], diBytes, diDigits, example)
	}
	diVal, err := strconv.ParseUint(hexPart, 16, 32)
	if err != nil {
		return DISpec{}, fmt.Errorf("dlt645: 数据标识 %q 不是合法十六进制: %w", parts[0], err)
	}
	di := uint32(diVal)

	// ---- 字典默认规格 ----
	info, found := LookupDI(version, di)
	spec := DISpec{
		DI:       di,
		Version:  version,
		Name:     info.Name,
		Unit:     info.Unit,
		Bytes:    info.Bytes,
		Decimals: info.Decimals,
		Signed:   info.Signed,
		Binary:   info.Binary,
		Layout:   info.Layout,
	}

	// ---- 字节数 ----
	lenGiven := false
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		v, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || v < 1 || v > 8 {
			return DISpec{}, fmt.Errorf("dlt645: 地址 %q 的字节数 %q 非法，应为 1~8",
				name, parts[1])
		}
		spec.Bytes = v
		lenGiven = true
	}

	// 字典未收录且未显式给出字节数 —— 无法推断长度，明确报错而非猜一个。
	if !found && !lenGiven {
		return DISpec{}, fmt.Errorf(
			"dlt645: 数据标识 %s 不在内置字典中，无法推断数据长度，"+
				"请按电表手册用 %s:字节数:小数位[:标志] 显式指定格式",
			hexPart, hexPart)
	}

	// ---- 小数位 ----
	if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
		v, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil || v < 0 || v > 16 {
			return DISpec{}, fmt.Errorf("dlt645: 地址 %q 的小数位 %q 非法，应为 0~16",
				name, parts[2])
		}
		spec.Decimals = v
	}

	// ---- 标志 ----
	if len(parts) == 4 {
		for _, f := range strings.ToLower(strings.TrimSpace(parts[3])) {
			switch f {
			case 's':
				spec.Signed = true
			case 'u':
				spec.Signed = false
			case 'b':
				spec.Binary = true
			case 'd':
				spec.Binary = false
			default:
				return DISpec{}, fmt.Errorf(
					"dlt645: 地址 %q 的标志 %q 无法识别，可用 s(有符号)/u(无符号)/b(二进制)/d(BCD)",
					name, string(f))
			}
		}
	}

	// ---- 规格自洽校验 ----
	if spec.Bytes < 1 || spec.Bytes > 8 {
		return DISpec{}, fmt.Errorf("dlt645: 地址 %q 字节数 %d 超出 1~8", name, spec.Bytes)
	}
	if spec.Decimals > spec.Bytes*2 {
		return DISpec{}, fmt.Errorf(
			"dlt645: 地址 %q 小数位 %d 超过 %d 字节 BCD 可表达的 %d 位",
			name, spec.Decimals, spec.Bytes, spec.Bytes*2)
	}
	if spec.Layout != layoutNone && spec.Bytes != layoutBytes(spec.Layout) {
		return DISpec{}, fmt.Errorf(
			"dlt645: 数据标识 %s 为时间类（布局 %s，固定 %d 字节），不接受自定义字节数 %d",
			hexPart, spec.Layout, layoutBytes(spec.Layout), spec.Bytes)
	}

	return spec, nil
}

// formatDI 返回数据标识的规范书写形式，用于日志与错误信息
// （2007 为 8 位、1997 为 4 位十六进制）。
func formatDI(di uint32, version string) string {
	if version == Version1997 {
		return fmt.Sprintf("%04X", di)
	}
	return fmt.Sprintf("%08X", di)
}

// DIWireBytes 返回数据标识在报文中的字节序列：书写形式反转（低字节在前）。
//
// 例：2007 的 02010100 → 00 01 01 02；1997 的 9010 → 10 90。
//
// 不含数据域 +0x33 偏移——该偏移由 buildFrame 对数据域统一施加，
// 在此重复施加会导致双重偏移（线上变成 66 67 67 68 这样的垃圾值）。
func (s *DISpec) DIWireBytes(diBytes int) []byte {
	out := make([]byte, diBytes)
	for i := 0; i < diBytes; i++ {
		out[i] = byte(s.DI >> (8 * uint(i)))
	}
	return out
}

// MeterAddressBytes 将 12 位十进制表号打包为 6 字节 BCD 地址域。
//
// 地址域按**低字节在前**发送：书写 "000000000003" 的末两位是最低位，
// 应落在第一个字节，即 03 00 00 00 00 00。
func MeterAddressBytes(addr string) ([6]byte, error) {
	var out [6]byte
	normalized, ok := normalizeMeterAddress(addr)
	if !ok {
		return out, fmt.Errorf("dlt645: 表号 %q 非法，应为 1~12 位十进制数字", addr)
	}
	// normalized 恒为 12 位；取最后两位（最低位）填入第一个字节。
	for i := 0; i < 6; i++ {
		hi := normalized[len(normalized)-2*(i+1)]
		lo := normalized[len(normalized)-2*(i+1)+1]
		out[i] = (hi-'0')<<4 | (lo - '0')
	}
	return out, nil
}

// unoffsetData 还原数据域偏移（接收方向：每字节 -0x33）。
// 入参会被原地修改，调用方需传入已拷贝的切片。
func unoffsetData(b []byte) []byte {
	for i := range b {
		b[i] -= dataOffset
	}
	return b
}

// offsetData 施加数据域偏移（发送方向：每字节 +0x33）。
func offsetData(b []byte) []byte {
	for i := range b {
		b[i] += dataOffset
	}
	return b
}
