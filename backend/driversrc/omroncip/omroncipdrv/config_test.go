// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import "testing"

func TestParseCIPConfigDefaults(t *testing.T) {
	cfg, err := ParseCIPConfig("")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.TimeoutMS != defaultTimeoutMS {
		t.Errorf("TimeoutMS = %d, want %d", cfg.TimeoutMS, defaultTimeoutMS)
	}
	if cfg.Timeout.Milliseconds() != defaultTimeoutMS {
		t.Errorf("Timeout = %v, want %dms", cfg.Timeout, defaultTimeoutMS)
	}
	if cfg.StringLen != defaultStringLen {
		t.Errorf("StringLen = %d, want %d", cfg.StringLen, defaultStringLen)
	}
}

func TestParseCIPConfigValid(t *testing.T) {
	json := `{"host":"192.168.1.10","port":44819,"pingTag":"PingTag","timeoutMs":"3000","stringLen":"128"}`
	cfg, err := ParseCIPConfig(json)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.Host != "192.168.1.10" {
		t.Errorf("Host = %q, want 192.168.1.10", cfg.Host)
	}
	if cfg.Port != 44819 {
		t.Errorf("Port = %d, want 44819", cfg.Port)
	}
	if cfg.PingTag != "PingTag" {
		t.Errorf("PingTag = %q, want PingTag", cfg.PingTag)
	}
	if cfg.TimeoutMS != 3000 || cfg.Timeout.Milliseconds() != 3000 {
		t.Errorf("TimeoutMS = %d, Timeout = %v, want 3000", cfg.TimeoutMS, cfg.Timeout)
	}
	if cfg.StringLen != 128 {
		t.Errorf("StringLen = %d, want 128", cfg.StringLen)
	}
}

func TestParseCIPConfigLegacyTimeout(t *testing.T) {
	json := `{"timeout":"2500"}`
	cfg, err := ParseCIPConfig(json)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.TimeoutMS != 2500 {
		t.Errorf("TimeoutMS = %d, want 2500 (legacy timeout field)", cfg.TimeoutMS)
	}
}

func TestParseCIPConfigTolerance(t *testing.T) {
	// 非法数值回退默认，非法 JSON 报错
	cfg, err := ParseCIPConfig(`{"port":-1,"timeoutMs":"0","stringLen":"abc"}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Port = %d, want default %d", cfg.Port, defaultPort)
	}
	if cfg.TimeoutMS != defaultTimeoutMS {
		t.Errorf("TimeoutMS = %d, want default %d", cfg.TimeoutMS, defaultTimeoutMS)
	}
	if cfg.StringLen != defaultStringLen {
		t.Errorf("StringLen = %d, want default %d", cfg.StringLen, defaultStringLen)
	}

	if _, err := ParseCIPConfig(`{invalid`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSameConfig(t *testing.T) {
	a := &CIPConfig{Host: "1.2.3.4", Port: 44818, PingTag: "T", StringLen: 80}
	b := &CIPConfig{Host: "1.2.3.4", Port: 44818, PingTag: "T", StringLen: 80}
	if !sameConfig(a, b) {
		t.Error("identical configs should match")
	}
	b2 := &CIPConfig{Host: "1.2.3.5", Port: 44818, PingTag: "T", StringLen: 80}
	if sameConfig(a, b2) {
		t.Error("different host should not match")
	}
	if sameConfig(nil, a) || sameConfig(a, nil) {
		t.Error("nil config should not match")
	}
}
