package driver_test

import (
	"slices"
	"testing"

	"iot-gateway/driver"
	_ "iot-gateway/driver/mitsubishi" // 注册 Mitsubishi MC 类型
	_ "iot-gateway/driver/modbus"     // 注册 Modbus 类型
	_ "iot-gateway/driver/omron/cip"  // 注册 CIP 类型
	_ "iot-gateway/driver/omron/fins" // 注册 FINS 类型
	_ "iot-gateway/driver/s7"         // 注册 S7 类型
	"iot-gateway/model/po"
)

// TestScopeOf 验证 config 协议名 → TypeRegistry scope 前缀的映射（不区分大小写）。
func TestScopeOf(t *testing.T) {
	cases := []struct {
		protocol string
		want     string
	}{
		{"ModBus.TCP", "modbus"},
		{"modbus.rtu", "modbus"}, // 大小写不敏感
		{"Siemens.S7", "s7"},
		{"Omron.CIP", "cip"},
		{"Omron.FINS.HostLinkTCP", "fins"},
		{"Mitsubishi.MC.TCP", "mitsubishi"},
		{"mitsubishi.mc.serial", "mitsubishi"}, // 大小写不敏感
		{"nope", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := driver.ScopeOf(c.protocol); got != c.want {
			t.Errorf("ScopeOf(%q) = %q, want %q", c.protocol, got, c.want)
		}
	}
}

// TestLookupScopeType 验证 scope 内通用/扩展类型 → 内部名映射（不区分大小写）。
func TestLookupScopeType(t *testing.T) {
	cases := []struct {
		scope, name, want string
		ok                bool
	}{
		{"modbus", "Float", "float32", true},
		{"modbus", "float", "float32", true}, // 大小写不敏感
		{"modbus", "INT", "int32", true},     // 协议专属扩展类型
		{"s7", "Short", "int", true},
		{"s7", "BCD", "", false},   // S7 无 BCD 映射
		{"cip", "Date", "", false}, // CIP 无 Date 映射
		{"mitsubishi", "Float", "float32", true},
		{"mitsubishi", "BCD", "bcd", true},   // 三菱 D 区常用 BCD
		{"mitsubishi", "Date", "", false},    // MC 无 Date 映射
		{"nope", "Float", "", false},
	}
	for _, c := range cases {
		got, ok := driver.LookupScopeType(c.scope, c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("LookupScopeType(%q, %q) = (%q, %v), want (%q, %v)",
				c.scope, c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestLookupDataType 验证原有 API 行为不因 scope 化重构而改变（service 校验依赖）。
func TestLookupDataType(t *testing.T) {
	cases := []struct {
		protocol, name, want string
		ok                   bool
	}{
		{"ModBus.TCP", "Float", "float32", true},
		{"ModBus.RTU", "Int", "int32", true}, // 扩展类型经 config 名同样可查
		{"Siemens.S7", "Short", "int", true},
		{"Omron.CIP", "Date", "", false},
		{"Mitsubishi.MC.TCP", "Short", "int16", true},
		{"Mitsubishi.MC.Serial", "Double", "float64", true},
		{"Mitsubishi.MC.TCP", "Date", "", false}, // MC 无 Date
		{"ModBus.TCP", "Nope", "", false},
		{"nope", "Float", "", false},
	}
	for _, c := range cases {
		got, ok := driver.LookupDataType(c.protocol, c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("LookupDataType(%q, %q) = (%q, %v), want (%q, %v)",
				c.protocol, c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestAvailableTypes 验证按 scope 返回可配置类型：通用按展示顺序 + 协议专属扩展按字母序。
func TestAvailableTypes(t *testing.T) {
	cases := []struct {
		scope        string
		wantCommon   []string
		wantExtended []string
	}{
		{
			scope:        "modbus",
			wantCommon:   driver.CommonDataTypes,
			wantExtended: []string{"Int", "Real"}, // modbus 专属 PLC 风格扩展
		},
		{
			scope:        "s7",
			wantCommon:   []string{"Boolean", "Date", "String", "Byte", "Char", "Short", "Word", "DWord", "Long", "Float", "Double"},
			wantExtended: nil, // S7 无 BCD/LBCD，也无扩展
		},
		{
			scope:        "cip",
			wantCommon:   []string{"Boolean", "String", "Byte", "Char", "Short", "Word", "DWord", "Long", "Float", "Double", "BCD", "LBCD"},
			wantExtended: nil, // CIP 无 Date
		},
		{
			scope:        "fins",
			wantCommon:   driver.CommonDataTypes,
			wantExtended: nil,
		},
		{
			scope:        "mitsubishi",
			wantCommon:   []string{"Boolean", "String", "Byte", "Char", "Short", "Word", "DWord", "Long", "Float", "Double", "BCD", "LBCD"},
			wantExtended: nil, // MC 无 Date
		},
		{
			scope:        "nope",
			wantCommon:   nil,
			wantExtended: nil,
		},
	}
	for _, c := range cases {
		common, extended := driver.AvailableTypes(c.scope)
		if !slices.Equal(common, c.wantCommon) {
			t.Errorf("AvailableTypes(%q) common = %v, want %v", c.scope, common, c.wantCommon)
		}
		if !slices.Equal(extended, c.wantExtended) {
			t.Errorf("AvailableTypes(%q) extended = %v, want %v", c.scope, extended, c.wantExtended)
		}
	}
}

// TestNormalizeAddressType 验证读取路径类型回退语义（保守，只修坏的）。
func TestNormalizeAddressType(t *testing.T) {
	cases := []struct {
		name           string
		protocol       string
		dataType       string
		commonDataType string
		want           string
	}{
		{"有效内部名保留", "ModBus.TCP", "int16", "Short", "int16"},
		{"漂移回退", "ModBus.TCP", "bogus", "Float", "float32"},
		{"扩展类型漂移回退", "ModBus.TCP", "bogus", "Int", "int32"},
		{"s7 漂移回退", "Siemens.S7", "bogus", "Long", "dint"},
		{"协议未知原样", "nope", "bogus", "Float", "bogus"},
		{"两者都坏原样", "ModBus.TCP", "bogus", "", "bogus"},
	}
	for _, c := range cases {
		addr := po.DeviceAddress{DataType: c.dataType, CommonDataType: c.commonDataType}
		if got := driver.NormalizeAddressType(c.protocol, addr); got != c.want {
			t.Errorf("%s: NormalizeAddressType(%q, {DataType:%q, CommonDataType:%q}) = %q, want %q",
				c.name, c.protocol, c.dataType, c.commonDataType, got, c.want)
		}
	}
}
