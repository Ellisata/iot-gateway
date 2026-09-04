// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import "testing"

// TestParseS7ConfigConnectionType 验证 connectionType 的解析与容错回退。
func TestParseS7ConfigConnectionType(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{"empty json", "", ConnectionTypePG},
		{"missing field", `{"host":"1.2.3.4"}`, ConnectionTypePG},
		{"PG", `{"connectionType":"1"}`, ConnectionTypePG},
		{"OP", `{"connectionType":"2"}`, ConnectionTypeOP},
		{"PC basic", `{"connectionType":"3"}`, ConnectionTypeBasic},
		{"invalid zero", `{"connectionType":"0"}`, ConnectionTypePG},
		{"invalid high", `{"connectionType":"9"}`, ConnectionTypePG},
		{"invalid text", `{"connectionType":"op"}`, ConnectionTypePG},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseS7Config(c.json)
			if err != nil {
				t.Fatalf("ParseS7Config(%q) error: %v", c.json, err)
			}
			if cfg.ConnectionType != c.want {
				t.Errorf("ParseS7Config(%q).ConnectionType = %d, want %d",
					c.json, cfg.ConnectionType, c.want)
			}
		})
	}
}

// TestParseS7ConfigMaxGap 验证 maxGap 的解析、默认值与钳制。
func TestParseS7ConfigMaxGap(t *testing.T) {
	cases := []struct {
		name string
		json string
		want int
	}{
		{"empty json", "", 8},
		{"missing field", `{"host":"1.2.3.4"}`, 8},
		{"zero disables", `{"maxGap":"0"}`, 0},
		{"small", `{"maxGap":"4"}`, 4},
		{"clamped to max", `{"maxGap":"99999"}`, maxConfigGap},
		{"negative invalid", `{"maxGap":"-1"}`, 8},
		{"invalid text", `{"maxGap":"abc"}`, 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseS7Config(c.json)
			if err != nil {
				t.Fatalf("ParseS7Config(%q) error: %v", c.json, err)
			}
			if cfg.MaxGap != c.want {
				t.Errorf("ParseS7Config(%q).MaxGap = %d, want %d",
					c.json, cfg.MaxGap, c.want)
			}
		})
	}
}
