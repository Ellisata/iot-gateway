// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import "testing"

func TestMeterAddressBytes(t *testing.T) {
	got, err := MeterAddressBytes("000000000003")
	if err != nil {
		t.Fatal(err)
	}
	// 地址域按低字节在前发送：书写形式的末两位是最低位，落在第一个字节
	want := [6]byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00}
	if got != want {
		t.Errorf("MeterAddressBytes = % X, want % X", got, want)
	}

	// 缩略写法应等价于补零后的写法
	got2, err := MeterAddressBytes("3")
	if err != nil || got2 != want {
		t.Errorf("MeterAddressBytes(\"3\") = % X, %v; want % X", got2, err, want)
	}

	// 现场常见的分隔写法应被容忍
	if got3, err := MeterAddressBytes("00-00-00-00-00-03"); err != nil || got3 != want {
		t.Errorf("MeterAddressBytes(带分隔符) = % X, %v; want % X", got3, err, want)
	}

	for _, bad := range []string{"", "12x4", "1234567890123"} {
		if _, err := MeterAddressBytes(bad); err == nil {
			t.Errorf("MeterAddressBytes(%q) 应报错", bad)
		}
	}
}

// DIWireBytes 返回**未施加 0x33 偏移**的数据标识字节（低字节在前）。
// 偏移由 buildFrame 对数据域统一施加，在此重复施加会导致双重偏移。
func TestDIWireBytes(t *testing.T) {
	cases := []struct {
		name    string
		di      uint32
		diBytes int
		want    []byte
	}{
		{"2007 A相电压", 0x02010100, 4, []byte{0x00, 0x01, 0x01, 0x02}},
		{"2007 正向有功总电能", 0x00010000, 4, []byte{0x00, 0x00, 0x01, 0x00}},
		{"1997 正向有功总电能", 0x9010, 2, []byte{0x10, 0x90}},
		{"1997 A相电压", 0xB611, 2, []byte{0x11, 0xB6}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := DISpec{DI: c.di}
			if got := s.DIWireBytes(c.diBytes); string(got) != string(c.want) {
				t.Errorf("DI %08X wire = % X, want % X", c.di, got, c.want)
			}
		})
	}
}

func TestParseAddress(t *testing.T) {
	cases := []struct {
		name, version string
		wantBytes     int
		wantDec       int
		wantSigned    bool
		wantBinary    bool
		wantErr       bool
	}{
		{"02010100", Version2007, 2, 1, false, false, false},      // 字典
		{"02020100", Version2007, 3, 3, true, false, false},       // 字典：有符号
		{"02010100:4:2", Version2007, 4, 2, false, false, false},  // 显式覆盖
		{"02010100::3", Version2007, 2, 3, false, false, false},   // 空段保留字典值
		{"12345678:3:3:s", Version2007, 3, 3, true, false, false}, // 私有 DI + 标志
		{"12345678:2:0:b", Version2007, 2, 0, false, true, false}, // 二进制位域
		{"12345678:2:0:bs", Version2007, 2, 0, true, true, false}, // 标志可组合
		{"1A2B3C4D", Version2007, 0, 0, false, false, true},       // 不在字典且未给长度
		{"0x02010100", Version2007, 2, 1, false, false, false},    // 容忍 0x 前缀
		{"9010", Version1997, 4, 2, false, false, false},
		{"B611", Version1997, 2, 1, false, false, false},
		{"02010100", Version1997, 0, 0, false, false, true},       // 1997 应为 4 位
		{"0201010", Version2007, 0, 0, false, false, true},        // 位数不对
		{"02010100:9:1", Version2007, 0, 0, false, false, true},   // 字节数超范围
		{"02010100:2:9", Version2007, 0, 0, false, false, true},   // 小数位超 BCD 位数
		{"02010100:2:1:x", Version2007, 0, 0, false, false, true}, // 未知标志
		{"", Version2007, 0, 0, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name+"/"+c.version, func(t *testing.T) {
			s, err := ParseAddress(c.name, c.version)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %+v", s)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if s.Bytes != c.wantBytes || s.Decimals != c.wantDec ||
				s.Signed != c.wantSigned || s.Binary != c.wantBinary {
				t.Errorf("Bytes=%d Decimals=%d Signed=%v Binary=%v, want %d/%d/%v/%v",
					s.Bytes, s.Decimals, s.Signed, s.Binary,
					c.wantBytes, c.wantDec, c.wantSigned, c.wantBinary)
			}
		})
	}
}

// 时间类数据标识固定 4/3/6 字节，不接受自定义字节数（否则布局解码会越界）。
func TestParseAddressRejectsLayoutOverride(t *testing.T) {
	if _, err := ParseAddress("04000102:4:0", Version2007); err == nil {
		t.Error("时间类数据标识应拒绝自定义字节数")
	}
}
