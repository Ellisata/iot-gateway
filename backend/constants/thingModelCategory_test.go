// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package constants

import "testing"

// TestParseThingModelCategory 钉住导入模板允许的三种写法：
// code（忽略大小写）、中文展示名、以及「展示名（code）」组合。
func TestParseThingModelCategory(t *testing.T) {
	tests := []struct {
		raw    string
		want   string
		wantOK bool
	}{
		{"", "", true},
		{"  ", "", true},
		{"sensor", "sensor", true},
		{"SENSOR", "sensor", true},
		{" 仪表 ", "meter", true},
		{"传感器（sensor）", "sensor", true},
		{"传感器(sensor)", "sensor", true},
		{"传感器（SENSOR）", "sensor", true},
		{"PLC / 控制器（plc）", "plc", true},
		{"机器人", "robot", true},
		{"sensorx", "", false},
		{"未知分类", "", false},
		{"仪表（nope）", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseThingModelCategory(tt.raw)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("ParseThingModelCategory(%q) = (%q, %v)，期望 (%q, %v)",
				tt.raw, got, ok, tt.want, tt.wantOK)
		}
	}
}

// TestIsValidOpenApiThingModelCategory 开放接口的分类只校验格式，不校验内置枚举：
// 平台可以下发自定义 code，但空串以外的取值必须是小写字母开头的稳定标识。
func TestIsValidOpenApiThingModelCategory(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"", true},
		{"  ", true},
		{"sensor", true},
		{"energy", true},
		{"energy_storage", true},
		{"energy-storage", true},
		{"a", true},
		{"a1234567890123456789012345678901", true},   // 32 位
		{"a12345678901234567890123456789012", false}, // 33 位
		{"Energy", false},
		{"energy storage", false},
		{"1energy", false},
		{"能源", false},
	}
	for _, tt := range tests {
		if got := IsValidOpenApiThingModelCategory(tt.code); got != tt.want {
			t.Errorf("IsValidOpenApiThingModelCategory(%q) = %v，期望 %v", tt.code, got, tt.want)
		}
	}
}

// TestThingModelCategoryTemplateOptions 模板下拉项必须是「中文名（code）」。
func TestThingModelCategoryTemplateOptions(t *testing.T) {
	options := ThingModelCategoryTemplateOptions()
	if len(options) != len(ThingModelCategoryCodes) {
		t.Fatalf("下拉项数 = %d，期望 %d", len(options), len(ThingModelCategoryCodes))
	}
	for i, code := range ThingModelCategoryCodes {
		want := ThingModelCategoryDisplayNames[code] + "（" + code + "）"
		if options[i] != want {
			t.Errorf("第 %d 项 = %q，期望 %q", i, options[i], want)
		}
	}
}
