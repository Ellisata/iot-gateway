package mitsubishi

import (
	"testing"
	"time"
)

// TestParseMCConfigDefaults 空配置/空 JSON 返回默认值。
func TestParseMCConfigDefaults(t *testing.T) {
	cfg, err := ParseMCConfig("")
	if err != nil {
		t.Fatalf("ParseMCConfig(\"\") = %v", err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != defaultPort {
		t.Errorf("default host/port = %s:%d, want 127.0.0.1:%d", cfg.Host, cfg.Port, defaultPort)
	}
	if cfg.TimeoutMS != defaultTimeoutMS || cfg.Timeout != time.Duration(defaultTimeoutMS)*time.Millisecond {
		t.Errorf("default timeout = %d/%v", cfg.TimeoutMS, cfg.Timeout)
	}
	if cfg.StringLen != defaultStringLen || cfg.MaxGap != defaultMergeGap || cfg.MaxReadWords != defaultMaxReadWords {
		t.Errorf("default tuning = %+v", cfg)
	}

	cfg, err = ParseMCConfig("{}")
	if err != nil {
		t.Fatalf("ParseMCConfig(\"{}\") = %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("{} default port = %d, want %d", cfg.Port, defaultPort)
	}
}

// TestParseMCConfigFull 完整 JSON 解析。
func TestParseMCConfigFull(t *testing.T) {
	cfg, err := ParseMCConfig(`{
		"host": "10.0.0.5",
		"port": 2000,
		"timeoutMs": "3000",
		"stringLen": "8",
		"maxGap": "16",
		"maxReadWords": "200"
	}`)
	if err != nil {
		t.Fatalf("ParseMCConfig = %v", err)
	}
	if cfg.Host != "10.0.0.5" || cfg.Port != 2000 {
		t.Errorf("host/port = %s:%d", cfg.Host, cfg.Port)
	}
	if cfg.TimeoutMS != 3000 || cfg.Timeout != 3*time.Second {
		t.Errorf("timeout = %d/%v", cfg.TimeoutMS, cfg.Timeout)
	}
	if cfg.StringLen != 8 || cfg.MaxGap != 16 || cfg.MaxReadWords != 200 {
		t.Errorf("tuning = %+v", cfg)
	}
}

// TestParseMCConfigSerial 串口配置解析。
func TestParseMCConfigSerial(t *testing.T) {
	cfg, err := ParseMCConfig(`{
		"comPort": "COM3",
		"baudRate": "19200",
		"dataBits": "7",
		"stopBits": "2",
		"parity": "E",
		"timeout": "1000"
	}`)
	if err != nil {
		t.Fatalf("ParseMCConfig = %v", err)
	}
	if cfg.ComPort != "COM3" || cfg.BaudRate != 19200 || cfg.DataBits != 7 ||
		cfg.StopBits != 2 || cfg.Parity != "E" {
		t.Errorf("serial config = %+v", cfg)
	}
	if cfg.TimeoutMS != 1000 { // 兼容旧字段名 timeout
		t.Errorf("timeout(legacy) = %d, want 1000", cfg.TimeoutMS)
	}
}

// TestParseMCConfigFaultTolerant 非法数值容错回退默认值。
func TestParseMCConfigFaultTolerant(t *testing.T) {
	cfg, err := ParseMCConfig(`{
		"host": "10.0.0.5",
		"port": -1,
		"timeoutMs": "abc",
		"stringLen": "-5",
		"maxGap": "-1",
		"maxReadWords": "999999"
	}`)
	if err != nil {
		t.Fatalf("ParseMCConfig = %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("invalid port should fallback, got %d", cfg.Port)
	}
	if cfg.TimeoutMS != defaultTimeoutMS {
		t.Errorf("invalid timeout should fallback, got %d", cfg.TimeoutMS)
	}
	if cfg.StringLen != defaultStringLen {
		t.Errorf("invalid stringLen should fallback, got %d", cfg.StringLen)
	}
	if cfg.MaxGap != defaultMergeGap {
		t.Errorf("invalid maxGap should fallback to default %d, got %d", defaultMergeGap, cfg.MaxGap)
	}
	if cfg.MaxReadWords != maxReadWordsLimit {
		t.Errorf("maxReadWords 999999 should clamp to %d, got %d", maxReadWordsLimit, cfg.MaxReadWords)
	}
}

// TestParseMCConfigInvalidJSON 非法 JSON 报错。
func TestParseMCConfigInvalidJSON(t *testing.T) {
	if _, err := ParseMCConfig(`{not json`); err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}
