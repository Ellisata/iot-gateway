// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultRockwellConfig()
	if cfg.Port != 44818 {
		t.Errorf("Port = %d, want 44818", cfg.Port)
	}
	if cfg.TimeoutMS != 5000 {
		t.Errorf("TimeoutMS = %d, want 5000", cfg.TimeoutMS)
	}
	if cfg.StringLen != defaultStringLen {
		t.Errorf("StringLen = %d, want %d", cfg.StringLen, defaultStringLen)
	}
	if cfg.Timeout != 5000*1e6 {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestParseConfigEmpty(t *testing.T) {
	cfg, err := ParseRockwellConfig("")
	if err != nil {
		t.Fatalf("ParseRockwellConfig(\"\") error: %v", err)
	}
	if cfg.Port != 44818 || cfg.TimeoutMS != 5000 {
		t.Errorf("empty config should use defaults, got %+v", cfg)
	}
}

func TestParseConfigFull(t *testing.T) {
	cfg, err := ParseRockwellConfig(`{"host":"192.168.1.10","port":44818,"pingTag":"Heartbeat","timeoutMs":"3000","stringLen":"40"}`)
	if err != nil {
		t.Fatalf("ParseRockwellConfig error: %v", err)
	}
	if cfg.Host != "192.168.1.10" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.Port != 44818 {
		t.Errorf("Port = %d", cfg.Port)
	}
	if cfg.PingTag != "Heartbeat" {
		t.Errorf("PingTag = %q", cfg.PingTag)
	}
	if cfg.TimeoutMS != 3000 {
		t.Errorf("TimeoutMS = %d, want 3000", cfg.TimeoutMS)
	}
	if cfg.StringLen != 40 {
		t.Errorf("StringLen = %d, want 40", cfg.StringLen)
	}
}

func TestParseConfigCompatTimeoutField(t *testing.T) {
	// 兼容旧字段名 timeout
	cfg, err := ParseRockwellConfig(`{"host":"h","timeout":"2000"}`)
	if err != nil {
		t.Fatalf("ParseRockwellConfig error: %v", err)
	}
	if cfg.TimeoutMS != 2000 {
		t.Errorf("TimeoutMS = %d, want 2000 (compat field)", cfg.TimeoutMS)
	}
}

func TestParseConfigTolerance(t *testing.T) {
	// 非法数值容错回退默认值
	cfg, err := ParseRockwellConfig(`{"host":"h","timeoutMs":"abc","port":-1,"stringLen":"xyz"}`)
	if err != nil {
		t.Fatalf("ParseRockwellConfig error: %v", err)
	}
	if cfg.TimeoutMS != 5000 {
		t.Errorf("TimeoutMS = %d, want default 5000", cfg.TimeoutMS)
	}
	if cfg.Port != 44818 {
		t.Errorf("Port = %d, want default 44818", cfg.Port)
	}
	if cfg.StringLen != defaultStringLen {
		t.Errorf("StringLen = %d, want default %d", cfg.StringLen, defaultStringLen)
	}
}

func TestParseConfigInvalidJSON(t *testing.T) {
	if _, err := ParseRockwellConfig(`{invalid`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSameConfig(t *testing.T) {
	a, _ := ParseRockwellConfig(`{"host":"h","port":44818,"pingTag":"P"}`)
	b, _ := ParseRockwellConfig(`{"host":"h","port":44818,"pingTag":"P"}`)
	if !sameConfig(a, b) {
		t.Error("same configs should match")
	}
	c, _ := ParseRockwellConfig(`{"host":"h2","port":44818,"pingTag":"P"}`)
	if sameConfig(a, c) {
		t.Error("different host should not match")
	}
	if sameConfig(nil, a) || sameConfig(a, nil) {
		t.Error("nil config should not match")
	}
}

func TestParseAddress(t *testing.T) {
	// 合法：Logix 标签语法（结构体/数组/程序作用域/位访问）
	valid := []string{
		"MyTag",
		"MyStruct.Field",
		"Array[5]",
		"Matrix[2,3]",
		"Program:MainProgram.MyTag",
		"MyDINT.5",
		" Local:2:I.Data[0] ",
	}
	for _, name := range valid {
		got, ok := ParseRockwellAddress(name)
		if !ok {
			t.Errorf("ParseRockwellAddress(%q) should be valid", name)
			continue
		}
		if got != "Local:2:I.Data[0]" && got != name {
			t.Errorf("ParseRockwellAddress(%q) = %q (expect trimmed or same)", name, got)
		}
	}
	// 非法：空、超长、含空白/控制字符/非 ASCII
	invalid := []string{
		"",
		"   ",
		string(make([]byte, 1025)),
		"My Tag",    // 空格
		"标签",        // 非 ASCII
		"My\x01Tag", // 控制字符
	}
	for _, name := range invalid {
		if _, ok := ParseRockwellAddress(name); ok {
			t.Errorf("ParseRockwellAddress(%q) should be invalid", name)
		}
	}
}
