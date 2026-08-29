package opcua

import (
	"strings"
	"testing"
	"time"
)

// TestParseOpcUaConfigDefaults 验证默认值与缺省字段容错。
func TestParseOpcUaConfigDefaults(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		check   func(*testing.T, *OpcUaConfig)
		wantErr bool
	}{
		{name: "empty json", json: "", check: func(t *testing.T, c *OpcUaConfig) {
			if c.Host != "127.0.0.1" || c.Port != 4840 {
				t.Errorf("default host/port = %s:%d, want 127.0.0.1:4840", c.Host, c.Port)
			}
			if c.SecurityPolicy != "None" || c.SecurityMode != "None" || c.AuthMode != "Anonymous" {
				t.Errorf("default security = %s/%s auth=%s, want None/None/Anonymous",
					c.SecurityPolicy, c.SecurityMode, c.AuthMode)
			}
			if c.TimeoutMS != 5000 || c.Timeout != 5*time.Second {
				t.Errorf("default timeout = %d/%v, want 5000/5s", c.TimeoutMS, c.Timeout)
			}
			if c.MaxBatch != 100 {
				t.Errorf("default maxBatch = %d, want 100", c.MaxBatch)
			}
		}},
		{name: "empty object", json: `{}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.Host != "127.0.0.1" {
				t.Errorf("host = %s, want 127.0.0.1", c.Host)
			}
		}},
		{name: "missing optional", json: `{"host":"10.0.0.1"}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.Endpoint != "opc.tcp://10.0.0.1:4840" {
				t.Errorf("endpoint = %q, want opc.tcp://10.0.0.1:4840", c.Endpoint)
			}
		}},
		{name: "zero port falls back", json: `{"host":"10.0.0.1","port":0}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.Port != 4840 {
				t.Errorf("port = %d, want 4840", c.Port)
			}
		}},
		{name: "non-numeric port errors", json: `{"port":"abc"}`, wantErr: true},
		{name: "port range clamp", json: `{"host":"10.0.0.1","port":70000}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.Port != 4840 {
				t.Errorf("port = %d, want 4840 (out of range)", c.Port)
			}
		}},
		{name: "timeout parsed", json: `{"timeoutMs":8000}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.Timeout != 8*time.Second {
				t.Errorf("timeout = %v, want 8s", c.Timeout)
			}
		}},
		{name: "maxBatch parsed", json: `{"maxBatch":250}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.MaxBatch != 250 {
				t.Errorf("maxBatch = %d, want 250", c.MaxBatch)
			}
		}},
		{name: "maxBatch clamped", json: `{"maxBatch":9999}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.MaxBatch != 1000 {
				t.Errorf("maxBatch = %d, want 1000 (clamped)", c.MaxBatch)
			}
		}},
		{name: "maxBatch zero falls back", json: `{"maxBatch":0}`, check: func(t *testing.T, c *OpcUaConfig) {
			if c.MaxBatch != 100 {
				t.Errorf("maxBatch = %d, want 100", c.MaxBatch)
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseOpcUaConfig(c.json)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParseOpcUaConfig(%q) want error, got nil", c.json)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOpcUaConfig(%q) error: %v", c.json, err)
			}
			c.check(t, cfg)
		})
	}
}

// TestParseOpcUaConfigEndpoint 验证 endpoint 合成与校验。
func TestParseOpcUaConfigEndpoint(t *testing.T) {
	t.Run("explicit endpoint wins", func(t *testing.T) {
		cfg, err := ParseOpcUaConfig(`{"endpoint":"opc.tcp://192.168.1.5:53530"}`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Endpoint != "opc.tcp://192.168.1.5:53530" {
			t.Errorf("endpoint = %q", cfg.Endpoint)
		}
	})

	t.Run("built from host port", func(t *testing.T) {
		cfg, err := ParseOpcUaConfig(`{"host":"192.168.1.5","port":53530}`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Endpoint != "opc.tcp://192.168.1.5:53530" {
			t.Errorf("endpoint = %q", cfg.Endpoint)
		}
	})

	t.Run("bad scheme", func(t *testing.T) {
		_, err := ParseOpcUaConfig(`{"endpoint":"http://192.168.1.5:4840"}`)
		if err == nil || !strings.Contains(err.Error(), "opc.tcp://") {
			t.Fatalf("want scheme error, got %v", err)
		}
	})

	t.Run("default host when only port given", func(t *testing.T) {
		cfg, err := ParseOpcUaConfig(`{"port":4840}`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Endpoint != "opc.tcp://127.0.0.1:4840" {
			t.Errorf("endpoint = %q, want default-host opc.tcp://127.0.0.1:4840", cfg.Endpoint)
		}
	})
}

// TestParseOpcUaConfigSecurityAuth 验证安全策略/模式与认证方式的取值校验。
func TestParseOpcUaConfigSecurityAuth(t *testing.T) {
	t.Run("bad policy", func(t *testing.T) {
		_, err := ParseOpcUaConfig(`{"securityPolicy":"Aes256"}`)
		if err == nil || !strings.Contains(err.Error(), "securityPolicy") {
			t.Fatalf("want policy error, got %v", err)
		}
	})

	t.Run("bad mode", func(t *testing.T) {
		_, err := ParseOpcUaConfig(`{"securityMode":"Encrypt"}`)
		if err == nil || !strings.Contains(err.Error(), "securityMode") {
			t.Fatalf("want mode error, got %v", err)
		}
	})

	t.Run("username auth without credentials", func(t *testing.T) {
		_, err := ParseOpcUaConfig(`{"authMode":"Username"}`)
		if err == nil || !strings.Contains(err.Error(), "username and password") {
			t.Fatalf("want credential error, got %v", err)
		}
	})

	t.Run("username auth with credentials", func(t *testing.T) {
		cfg, err := ParseOpcUaConfig(`{"authMode":"Username","username":"opc","password":"secret"}`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AuthMode != "Username" || cfg.Username != "opc" || cfg.Password != "secret" {
			t.Errorf("auth = %s/%s/%s, want Username/opc/secret", cfg.AuthMode, cfg.Username, cfg.Password)
		}
	})

	t.Run("anonymous normalized", func(t *testing.T) {
		cfg, err := ParseOpcUaConfig(`{"authMode":"anonymous"}`)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AuthMode != "Anonymous" {
			t.Errorf("authMode = %s, want Anonymous", cfg.AuthMode)
		}
	})

	t.Run("bad auth mode", func(t *testing.T) {
		_, err := ParseOpcUaConfig(`{"authMode":"Token"}`)
		if err == nil || !strings.Contains(err.Error(), "authMode") {
			t.Fatalf("want authMode error, got %v", err)
		}
	})
}
