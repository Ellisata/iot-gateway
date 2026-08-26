package mitsubishi

import "testing"

// TestParseMCAddress 验证地址解析（设备码/数字/八进制/.bit 后缀）。
func TestParseMCAddress(t *testing.T) {
	cases := []struct {
		name string
		dev  string // 期望设备名
		num  uint32
		bit  int
		ok   bool
	}{
		{"D100", "D", 100, -1, true},
		{"d100", "D", 100, -1, true}, // 大小写不敏感
		{"M10", "M", 10, -1, true},
		{"L5", "L", 5, -1, true},
		{"X30", "X", 24, -1, true}, // 八进制 30 = 十进制 24
		{"Y7", "Y", 7, -1, true},
		{"ZR100", "ZR", 100, -1, true},
		{"Z10", "Z", 10, -1, true}, // 非 ZR
		{"SB5", "SB", 5, -1, true},
		{"S5", "S", 5, -1, true},
		{"SD10", "SD", 10, -1, true},
		{"SW10", "SW", 10, -1, true},
		{"TS5", "TS", 5, -1, true},
		{"TC5", "TC", 5, -1, true},
		{"CC3", "CC", 3, -1, true},
		{"CN3", "CN", 3, -1, true},
		{"D100.5", "D", 100, 5, true}, // 字设备位访问
		{"D100.15", "D", 100, 15, true},
		{"W100", "W", 100, -1, true},
		{"X0", "X", 0, -1, true},

		// 非法地址
		{"", "", 0, -1, false},
		{"D", "", 0, -1, false},          // 无数字
		{"X8", "", 0, -1, false},         // 八进制含 8
		{"X19", "", 0, -1, false},        // 八进制含 9
		{"M10.3", "", 0, -1, false},      // 位设备禁止 .bit 后缀
		{"D100.16", "", 0, -1, false},    // 位超 15
		{"D100.", "", 0, -1, false},      // 位为空
		{"XYZ", "", 0, -1, false},        // X 后非数字
		{"W123456789", "", 0, -1, false}, // 超出 3 字节地址上限
		{"D-5", "", 0, -1, false},        // 负数
	}
	for _, c := range cases {
		addr, ok := ParseMCAddress(c.name)
		if ok != c.ok {
			t.Errorf("ParseMCAddress(%q) ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if addr.Device.name != c.dev || addr.Number != c.num || addr.Bit != c.bit {
			t.Errorf("ParseMCAddress(%q) = {%s %d bit=%d}, want {%s %d bit=%d}",
				c.name, addr.Device.name, addr.Number, addr.Bit, c.dev, c.num, c.bit)
		}
	}
}

// TestParseMCOctal 验证 X/Y 八进制解析的正确数值。
func TestParseMCOctal(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"0", 0, true},
		{"7", 7, true},
		{"10", 8, true},
		{"17", 15, true},
		{"30", 24, true},
		{"100", 64, true},
		{"8", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, err := parseOctal(c.in)
		if (err == nil) != c.ok {
			t.Errorf("parseOctal(%q) ok = %v, want %v", c.in, err == nil, c.ok)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("parseOctal(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
