// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"strings"
	"testing"
)

func TestParseCIPAddressValid(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"MotorSpeed", "MotorSpeed"},
		{"Recipe.Setpoint", "Recipe.Setpoint"},
		{"Array[3]", "Array[3]"},
		{"Array[3].Member", "Array[3].Member"},
		{"  Tag  ", "Tag"}, // trim 空白
		{"A-B_C.1", "A-B_C.1"},
	}
	for _, c := range cases {
		got, ok := ParseCIPAddress(c.in)
		if !ok {
			t.Errorf("ParseCIPAddress(%q) rejected, want ok", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCIPAddress(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseCIPAddressInvalid(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"My Tag",                  // 内嵌空格
		"Tag\tName",               // 制表符
		"Tag\nName",               // 换行
		"标签",                      // 非 ASCII
		strings.Repeat("A", 1025), // 超长
	}
	for _, c := range cases {
		if got, ok := ParseCIPAddress(c); ok {
			t.Errorf("ParseCIPAddress(%q) = %q, want rejected", c, got)
		}
	}
}
