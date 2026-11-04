// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
)

func TestParseConditions(t *testing.T) {
	ok := `{"groups":[{"conditions":[{"addressId":"a1","op":"gt","values":["10"]}]}]}`
	c, err := ParseConditions([]byte(ok))
	if err != nil {
		t.Fatalf("ParseConditions: %v", err)
	}
	if len(c.Groups) != 1 || len(c.Groups[0].Conditions) != 1 || c.Groups[0].Conditions[0].AddressID != "a1" {
		t.Fatalf("解析结果 = %+v", c)
	}

	for name, raw := range map[string]string{
		"空":       ``,
		"非法 JSON": `{`,
		"无组":      `{"groups":[]}`,
		"空组":      `{"groups":[{"conditions":[]}]}`,
		"缺点位":     `{"groups":[{"conditions":[{"op":"gt","values":["1"]}]}]}`,
		"缺算子":     `{"groups":[{"conditions":[{"addressId":"a1","values":["1"]}]}]}`,
	} {
		if _, err := ParseConditions([]byte(raw)); err == nil {
			t.Errorf("%s: 期望解析失败", name)
		}
	}
}

func TestCompareNumeric(t *testing.T) {
	tests := []struct {
		value string
		op    string
		vals  []string
		want  bool
		ok    bool
	}{
		{"11", driver.OpGt, []string{"10"}, true, true},
		{"10", driver.OpGt, []string{"10"}, false, true},
		{"10", driver.OpGe, []string{"10"}, true, true},
		{"9", driver.OpLt, []string{"10"}, true, true},
		{"9", driver.OpLe, []string{"9"}, true, true},
		{"9", driver.OpEq, []string{"9"}, true, true},
		{"9", driver.OpNe, []string{"9"}, false, true},
		{"5", driver.OpRange, []string{"1", "10"}, false, true},
		{"11", driver.OpRange, []string{"1", "10"}, true, true},
		{"0", driver.OpRange, []string{"1", "10"}, true, true},
		{"0", driver.OpRange, []string{"", "10"}, false, true},
		{"abc", driver.OpGt, []string{"10"}, false, false},
		{"10", driver.OpGt, []string{"abc"}, false, false},
		{"10", driver.OpRange, []string{"1"}, false, false},
	}
	for _, tt := range tests {
		got, ok := Compare(tt.value, driver.KindFloat, tt.op, tt.vals)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Compare(%q,%s,%v) = %v/%v，期望 %v/%v", tt.value, tt.op, tt.vals, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCompareBoolAndString(t *testing.T) {
	if got, ok := Compare("1", driver.KindBool, driver.OpEq, []string{"true"}); !got || !ok {
		t.Errorf("bool 1==true = %v/%v", got, ok)
	}
	if _, ok := Compare("yes", driver.KindBool, driver.OpEq, []string{"1"}); ok {
		t.Error("无法识别的布尔值应返回 determinate=false")
	}
	if got, ok := Compare("RUN", driver.KindString, driver.OpIn, []string{"STOP", "RUN"}); !got || !ok {
		t.Errorf("string in = %v/%v", got, ok)
	}
	if got, ok := Compare("run", driver.KindString, driver.OpEq, []string{"RUN"}); got || !ok {
		t.Errorf("string eq 大小写敏感 = %v/%v", got, ok)
	}
	if _, ok := Compare("x", driver.KindTime, driver.OpEq, []string{"x"}); ok {
		t.Error("时间类型应返回 determinate=false")
	}
}
