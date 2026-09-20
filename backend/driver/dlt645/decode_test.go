// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"math"
	"testing"
)

func TestDecodeValues(t *testing.T) {
	cases := []struct {
		name     string
		spec     DISpec
		raw      []byte
		internal string
		want     string
	}{
		// 2 字节 1 位小数：2205 → 220.5
		{"A相电压 220.5V", DISpec{Bytes: 2, Decimals: 1}, []byte{0x05, 0x22}, "float", "220.5"},
		// 4 字节 2 位小数：128 → 1.28
		{"电能 2位小数", DISpec{Bytes: 4, Decimals: 2}, []byte{0x28, 0x01, 0x00, 0x00}, "float", "1.28"},
		{"功率 4位小数", DISpec{Bytes: 3, Decimals: 4}, []byte{0x00, 0x00, 0x00}, "float", "0.0000"},
		// 电量级数值必须无损：float32 会丢最后一位
		{"电能 8位有效数字", DISpec{Bytes: 4, Decimals: 2}, []byte{0x99, 0x99, 0x99, 0x99}, "float", "999999.99"},

		// 有符号位在**最高字节**（低字节在前的最后一个字节）的 bit7。
		// 1.000 的原始整数为 1000，编码为 6 位 BCD 数字 "001000"；
		// 按低字节在前拆成 [00][10][00]，负值再给最高字节置 bit7。
		{"电流 有符号正值", DISpec{Bytes: 3, Decimals: 3, Signed: true}, []byte{0x00, 0x10, 0x00}, "float", "1.000"},
		{"电流 有符号负值", DISpec{Bytes: 3, Decimals: 3, Signed: true}, []byte{0x00, 0x10, 0x80}, "float", "-1.000"},
		// 3 字节有符号 BCD 的幅值上限：最高字节让出 bit7 后最高两位只能是 79。
		// 这与厂商手册中「读到 799999 表示越界」的现象一致。
		{"有符号 3 字节上限", DISpec{Bytes: 3, Decimals: 3, Signed: true}, []byte{0x99, 0x99, 0x79}, "float", "799.999"},
		{"有符号 3 字节上限 负值", DISpec{Bytes: 3, Decimals: 3, Signed: true}, []byte{0x99, 0x99, 0xF9}, "float", "-799.999"},

		// 下拉类型只决定呈现：非 float 类型不应用 DI 的小数位
		{"原始整数 不缩放", DISpec{Bytes: 2, Decimals: 1}, []byte{0x05, 0x22}, "uint", "2205"},
		{"BCD 类型 不缩放", DISpec{Bytes: 2, Decimals: 1}, []byte{0x05, 0x22}, "bcd", "2205"},
		{"int 类型 不缩放", DISpec{Bytes: 2, Decimals: 1, Signed: true}, []byte{0x05, 0x22}, "int", "2205"},

		// 二进制编码（电表运行状态字等位域）
		{"二进制 无符号", DISpec{Bytes: 2, Binary: true}, []byte{0x34, 0x12}, "uint", "4660"},
		{"二进制 补码负值", DISpec{Bytes: 2, Binary: true, Signed: true}, []byte{0xFF, 0xFF}, "int", "-1"},

		// 文本
		{"6 字节表号", DISpec{Bytes: 6}, []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00}, "string", "000000000003"},
		{"非零布尔", DISpec{Bytes: 2}, []byte{0x01, 0x00}, "bool", "1"},
		{"零布尔", DISpec{Bytes: 2}, []byte{0x00, 0x00}, "bool", "0"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := c.spec
			v, err := DecodeValue(c.raw, &spec, c.internal)
			if err != nil {
				t.Fatalf("解码失败: %v", err)
			}
			if got := FormatValue(v, c.internal); got != c.want {
				t.Errorf("= %q, want %q", got, c.want)
			}
		})
	}
}

func TestDecodeTimeLayout(t *testing.T) {
	spec := DISpec{Bytes: 3, Layout: layoutTime}
	// 13:45:07 → 低字节在前：秒、分、时
	got, err := DecodeValue([]byte{0x07, 0x45, 0x13}, &spec, "datetime")
	if err != nil {
		t.Fatal(err)
	}
	if s := FormatValue(got, "datetime"); s != "13:45:07" {
		t.Errorf("时间 = %q, want \"13:45:07\"", s)
	}
}

func TestDecodeDateLayout(t *testing.T) {
	spec := DISpec{Bytes: 4, Layout: layoutDate}
	// 星期、日、月、年（低字节在前）→ 2026-09-20
	got, err := DecodeValue([]byte{0x01, 0x20, 0x09, 0x26}, &spec, "datetime")
	if err != nil {
		t.Fatal(err)
	}
	if s := FormatValue(got, "datetime"); s != "2026-09-20" {
		t.Errorf("日期 = %q, want \"2026-09-20\"", s)
	}
}

// 非法值必须报错、置 Quality=0，而不是输出一个看似合理实则错误的值。
func TestDecodeRejectsInvalidData(t *testing.T) {
	cases := []struct {
		name     string
		spec     DISpec
		raw      []byte
		internal string
	}{
		{"BCD nibble 超 9", DISpec{Bytes: 2, Decimals: 1}, []byte{0xAF, 0x22}, "float"},
		{"BCD nibble 超 9（高字节）", DISpec{Bytes: 2, Decimals: 1}, []byte{0x05, 0xF2}, "float"},
		{"数据字节不足", DISpec{Bytes: 4, Decimals: 2}, []byte{0x01, 0x02}, "float"},
		{"时间字段超范围", DISpec{Bytes: 3, Layout: layoutTime}, []byte{0x00, 0x00, 0x99}, "datetime"},
		{"日期字段超范围", DISpec{Bytes: 4, Layout: layoutDate}, []byte{0x00, 0x00, 0x99, 0x26}, "datetime"},
		// 星期字节异常时，绝不能靠「换个排列再试一次」凑出一个年月日互换的假日期。
		// 该报文按规范应读作 2026-03-05（星期字节被干扰成 0x10），曾静默输出 2026-05-16。
		{"日期星期字节异常", DISpec{Bytes: 4, Layout: layoutDate}, []byte{0x10, 0x05, 0x03, 0x26}, "datetime"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := c.spec
			if _, err := DecodeValue(c.raw, &spec, c.internal); err == nil {
				t.Error("应报错，实际未报错")
			}
		})
	}
}

// 时间类数据标识配非 Date 类型时按普通数值解码（用户显式选择，不报错）。
func TestDateDIWithNumericType(t *testing.T) {
	spec := DISpec{Bytes: 4, Layout: layoutDate}
	if _, err := DecodeValue([]byte{0x01, 0x20, 0x09, 0x26}, &spec, "float"); err != nil {
		t.Errorf("取原始数值不应报错: %v", err)
	}
}

// 定标格式化全程整数运算，必须无浮点舍入。
func TestFormatScaledExact(t *testing.T) {
	cases := []struct {
		raw      int64
		decimals int
		want     string
	}{
		{1, 3, "0.001"},
		{1000, 3, "1.000"},
		{-1000, 3, "-1.000"},
		{0, 2, "0.00"},
		{99999999, 2, "999999.99"},
		{-99999999, 2, "-999999.99"},
		{123, 0, "123"},
		// int64 下界取幅值会溢出，必须仍能无损输出
		{math.MinInt64, 0, "-9223372036854775808"},
		{math.MinInt64, 2, "-92233720368547758.08"},
	}
	for _, c := range cases {
		if got := formatScaled(c.raw, c.decimals); got != c.want {
			t.Errorf("formatScaled(%d, %d) = %q, want %q", c.raw, c.decimals, got, c.want)
		}
	}
}
