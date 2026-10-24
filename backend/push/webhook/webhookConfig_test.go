// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package webhook

import (
	"strings"
	"testing"

	"iot-gateway/model/po"
	"iot-gateway/push"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(`{"url":"http://example.com/ingest"}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Method != "POST" {
		t.Errorf("Method = %q, want POST", cfg.Method)
	}
	if cfg.ContentType != defaultContentType {
		t.Errorf("ContentType = %q", cfg.ContentType)
	}
	if cfg.TimeoutSeconds != defaultTimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d", cfg.TimeoutSeconds)
	}
	if cfg.Workers != defaultWorkers {
		t.Errorf("Workers = %d", cfg.Workers)
	}
	if !cfg.spoolEnabled() {
		t.Error("spool should default to enabled")
	}
}

func TestParseConfigValidation(t *testing.T) {
	cases := map[string]string{
		"bad url":      `{"url":"ftp://example.com"}`,
		"no host":      `{"url":"http://"}`,
		"bad method":   `{"url":"http://example.com","method":"GET"}`,
		"bad auth":     `{"url":"http://example.com","authType":"oauth"}`,
		"bearer empty": `{"url":"http://example.com","authType":"bearer"}`,
		"basic empty":  `{"url":"http://example.com","authType":"basic"}`,
		"hmac empty":   `{"url":"http://example.com","authType":"hmac-sha256"}`,
		"bad headers":  `{"url":"http://example.com","headersJson":"[]"}`,
		"bad timeout":  `{"url":"http://example.com","timeoutSeconds":999}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfig(raw); err == nil {
				t.Fatalf("parseConfig(%s) succeeded, want error", raw)
			}
		})
	}
}

func TestParseHeaders(t *testing.T) {
	h, err := parseHeaders(`{"X-Env":"prod","X-Trace":"abc"}`)
	if err != nil {
		t.Fatalf("parseHeaders: %v", err)
	}
	if h["X-Env"] != "prod" || h["X-Trace"] != "abc" {
		t.Fatalf("headers = %#v", h)
	}
	if _, err := parseHeaders(`{"X-Test":"a\nb"}`); err == nil || !strings.Contains(err.Error(), "非法") {
		t.Fatalf("CRLF header should be rejected, got %v", err)
	}
}

func TestHMACDefaults(t *testing.T) {
	cfg, err := parseConfig(`{"url":"http://example.com","authType":"hmac-sha256","hmacSecret":"s"}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.HMACHeader != defaultHMACHeader || cfg.HMACPrefix != defaultHMACPrefix {
		t.Fatalf("HMAC defaults = %q / %q", cfg.HMACHeader, cfg.HMACPrefix)
	}
}

func TestHTTPTypeRegistration(t *testing.T) {
	meta, ok := push.RegisteredTypeMeta("http")
	if !ok {
		t.Fatal("http type is not registered")
	}
	if !meta.MessageBased || !meta.SupportsPreview || !meta.SupportsTestSend {
		t.Fatalf("http meta = %+v", meta)
	}
	if _, err := newHTTPChannel(nil, &po.PushChannel{Name: "h", Type: "http", ConfigJSON: `{}`}); err == nil {
		t.Fatal("newHTTPChannel with empty config should fail")
	}
}
