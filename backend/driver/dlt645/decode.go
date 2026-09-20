// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"strconv"
	"strings"
)

// scaledValue 定标后的数值：保留原始整数与小数位。
//
// 刻意不转 float64 再做格式化：电量/电费级数值（如 999999.99）在 float32 下会丢
// 最后一位，即便 float64 也存在格式化时的舍入歧义。直接对整数插入小数点可以做到
// 无损且输出稳定。
type scaledValue struct {
	raw      int64
	decimals int
}

// DecodeValue 按数据标识规格与内部类型解码原始数据字节，是读取路径的唯一解码入口。
//
// raw 为已 -0x33 还原的数据域内容；internal 为 dataTypeMap 映射后的内部类型裸名。
//
// 分工（这是本驱动的核心设计）：
//   - 长度、小数位、符号、编码方式全部来自 spec（即数据标识 DI），与下拉类型无关；
//   - internal 只决定输出形态与 Kind：float 应用小数位输出工程值，
//     int/uint/bcd/lbcd 输出原始整数，string 输出文本，datetime 输出时间，bool 输出非零判定。
func DecodeValue(raw []byte, spec *DISpec, internal string) (any, error) {
	if spec == nil {
		return nil, fmt.Errorf("dlt645: 缺少数据标识规格")
	}
	if len(raw) < spec.Bytes {
		return nil, fmt.Errorf("dlt645: 数据字节不足，需要 %d 字节，实际 %d 字节",
			spec.Bytes, len(raw))
	}
	body := raw[:spec.Bytes]

	switch internal {
	case "bool":
		v, err := numericValue(body, spec)
		if err != nil {
			return nil, err
		}
		return v != 0, nil

	case "float":
		v, err := numericValue(body, spec)
		if err != nil {
			return nil, err
		}
		return scaledValue{raw: v, decimals: spec.Decimals}, nil

	case "int", "uint", "bcd", "lbcd", "":
		// 空类型名（未注册类型）按无符号原始整数处理，交由上层报 Quality=0
		return numericValue(body, spec)

	case "string":
		if spec.Binary {
			return strings.TrimRight(string(body), "\x00"), nil
		}
		return bcdString(body)

	case "datetime":
		switch spec.Layout {
		case layoutTime:
			return decodeTimeLayout(body)
		case layoutDate:
			return decodeDateLayout(body)
		default:
			return nil, fmt.Errorf("dlt645: 数据标识 %s 不是时间类，无法按 Date 类型解码",
				formatDI(spec.DI, spec.Version))
		}

	default:
		return nil, fmt.Errorf("dlt645: 不支持的数据类型 %q", internal)
	}
}

// FormatValue 将解码结果格式化为入库字符串。
//
// 定标值走 formatScaled 精确格式化；其余类型直接转字符串。
func FormatValue(v any, internal string) string {
	if internal == "bool" {
		return formatBool(v)
	}
	return formatAny(v)
}

// formatAny 通用格式化：定标值精确输出定点小数，其余按 Go 默认表示。
// 同时用作类型注册表里各 DataType.Format 的缺省实现。
func formatAny(v any) string {
	switch val := v.(type) {
	case scaledValue:
		return formatScaled(val.raw, val.decimals)
	case string:
		return val
	case bool:
		if val {
			return "1"
		}
		return "0"
	case int64:
		return strconv.FormatInt(val, 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatBool 布尔格式化（645 位域统一用 1/0 表示）。
func formatBool(v any) string {
	if b, ok := v.(bool); ok && b {
		return "1"
	}
	return "0"
}

// numericValue 按规格解析出整数原始值（尚未应用小数位）。
func numericValue(raw []byte, spec *DISpec) (int64, error) {
	if spec.Binary {
		return binaryValue(raw, spec.Signed)
	}
	return bcdValue(raw, spec.Signed)
}

// bcdValue 将压缩 BCD 字节解析为整数。
//
// 字节序为**低字节在前**（DL/T 645 规定数据项先传低字节），
// 即 raw[len-1] 是最高位字节。有符号时符号位取最高位字节的 bit7
// （S=1 表示负值）；这同时解释了「3 字节有符号 BCD 的最大幅值为 799999」——
// 最高字节的 bit7 让出后，最高位十进制数字最大只能是 7。
//
// 任一 nibble 大于 9 即判定为脏数据并返回错误：ECD 字段出现非法 nibble
// 通常意味着解码方式选错了（例如把二进制位域当成了 BCD），
// 此时报 Quality=0 远好于输出一个看似合理实则错误的值。
func bcdValue(raw []byte, signed bool) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("dlt645: BCD 数据为空")
	}
	if len(raw) > 8 {
		return 0, fmt.Errorf("dlt645: BCD 数据 %d 字节超出 8 字节上限", len(raw))
	}

	negative := false
	var v int64
	for i := len(raw) - 1; i >= 0; i-- {
		b := raw[i]
		if signed && i == len(raw)-1 {
			if b&0x80 != 0 {
				negative = true
				b &^= 0x80
			}
		}
		hi, lo := b>>4, b&0x0F
		if hi > 9 || lo > 9 {
			return 0, fmt.Errorf("dlt645: BCD 位非法（字节 %d = %02X，nibble 超出 0~9）", i, raw[i])
		}
		v = v*100 + int64(hi)*10 + int64(lo)
	}
	if negative {
		v = -v
	}
	return v, nil
}

// binaryValue 将二进制字节解析为整数（低字节在前）。
// 有符号按补码解释；8 字节且最高位为 1 时超出 int64 表示范围，返回错误。
func binaryValue(raw []byte, signed bool) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("dlt645: 二进制数据为空")
	}
	if len(raw) > 8 {
		return 0, fmt.Errorf("dlt645: 二进制数据 %d 字节超出 8 字节上限", len(raw))
	}

	var v uint64
	for i := len(raw) - 1; i >= 0; i-- {
		v = v<<8 | uint64(raw[i])
	}

	if !signed {
		if len(raw) == 8 && v > 1<<63-1 {
			return 0, fmt.Errorf("dlt645: 8 字节无符号值超出 int64 表示范围")
		}
		return int64(v), nil
	}

	bits := uint(len(raw) * 8)
	if bits < 64 && v&(1<<(bits-1)) != 0 {
		return int64(v | ^uint64(0)<<bits), nil
	}
	return int64(v), nil
}

// bcdString 将压缩 BCD 字节渲染为数字串（高位在前，与书写形式一致）。
// 表号 / 通信地址使用此形式，保留前导零。
func bcdString(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("dlt645: BCD 数据为空")
	}
	var sb strings.Builder
	sb.Grow(len(raw) * 2)
	for i := len(raw) - 1; i >= 0; i-- {
		hi, lo := raw[i]>>4, raw[i]&0x0F
		if hi > 9 || lo > 9 {
			return "", fmt.Errorf("dlt645: BCD 位非法（字节 %d = %02X）", i, raw[i])
		}
		sb.WriteByte('0' + hi)
		sb.WriteByte('0' + lo)
	}
	return sb.String(), nil
}

// formatScaled 将定标整数格式化为定点小数字符串，全程整数运算、无浮点参与。
func formatScaled(raw int64, decimals int) string {
	if decimals <= 0 {
		return strconv.FormatInt(raw, 10)
	}
	// 幅值用无符号算术取：8 字节有符号二进制取到 int64 下界时 -raw 会溢出，
	// 静默输出一个符号与数值都不对的结果。-(raw+1)+1 与 -raw 等价且全程不溢出。
	negative := raw < 0
	mag := uint64(raw)
	if negative {
		mag = uint64(-(raw + 1)) + 1
	}
	s := strconv.FormatUint(mag, 10)
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	out := s[:len(s)-decimals] + "." + s[len(s)-decimals:]
	if negative {
		out = "-" + out
	}
	return out
}

// ==================== 时间类布局解码 ====================
//
// 布局字节序均按「低字节在前」的线上顺序，即数组下标 0 是规范里最先传输的字段。

// bcdByteInt 把单字节 BCD 解析为 0~99 的整数。
func bcdByteInt(b byte) (int, bool) {
	hi, lo := b>>4, b&0x0F
	if hi > 9 || lo > 9 {
		return 0, false
	}
	return int(hi)*10 + int(lo), true
}

// decodeTimeLayout 解码 3 字节时间：秒、分、时。
func decodeTimeLayout(raw []byte) (string, error) {
	if len(raw) < 3 {
		return "", fmt.Errorf("dlt645: 时间布局需 3 字节，实际 %d 字节", len(raw))
	}
	sec, ok1 := bcdByteInt(raw[0])
	min, ok2 := bcdByteInt(raw[1])
	hour, ok3 := bcdByteInt(raw[2])
	if !ok1 || !ok2 || !ok3 {
		return "", fmt.Errorf("dlt645: 时间字段 BCD 位非法: % X", raw[:3])
	}
	if sec > 59 || min > 59 || hour > 23 {
		return "", fmt.Errorf("dlt645: 时间字段超范围（时=%d 分=%d 秒=%d）", hour, min, sec)
	}
	return fmt.Sprintf("%02d:%02d:%02d", hour, min, sec), nil
}

// decodeDateLayout 解码 4 字节日期及星期。
//
// 线上顺序为「星期、日、月、年」：规范的书写形式是 YYMMDDWW，而数据项按低字节在前
// 传输，故报文里最先到达的是星期，最后是年。
//
// 这里只按这一种（即规范规定的）排列解码，不做「换个排列再试一次」的自愈：
// 备选排列既不是任何厂商的规范做法，又会在星期字节异常时凑出一个年月日互换的
// 假日期（例：周一 2026-03-05 的报文若星期字节被干扰成 0x10，会被读成 2026-05-05）。
// 布局与预期不符时报错、置 Quality=0，远好于静默输出一个错误日期。
func decodeDateLayout(raw []byte) (string, error) {
	if len(raw) < 4 {
		return "", fmt.Errorf("dlt645: 日期布局需 4 字节，实际 %d 字节", len(raw))
	}

	year, month, day, week, ok := parseDateParts(raw[3], raw[2], raw[1], raw[0])
	if !ok {
		return "", fmt.Errorf(
			"dlt645: 日期字段不符合「星期、日、月、年」布局（星期 %02X 日 %02X 月 %02X 年 %02X）: % X",
			raw[0], raw[1], raw[2], raw[3], raw[:4])
	}
	_ = week
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), nil
}

// parseDateParts 校验并组装日期各字段，任一字段非法即返回 false。
func parseDateParts(rawYear, rawMonth, rawDay, rawWeek byte) (year, month, day, week int, ok bool) {
	y, ok1 := bcdByteInt(rawYear)
	m, ok2 := bcdByteInt(rawMonth)
	d, ok3 := bcdByteInt(rawDay)
	w, ok4 := bcdByteInt(rawWeek)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, 0, 0, 0, false
	}
	if m < 1 || m > 12 || d < 1 || d > 31 || w > 7 {
		return 0, 0, 0, 0, false
	}
	// 645 只传两位年份，统一按 2000 年后解释（协议实际使用年限远晚于 2000）
	return 2000 + y, m, d, w, true
}
