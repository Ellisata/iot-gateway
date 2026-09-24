// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"testing"
)

func TestCustomEnvelope(t *testing.T) {
	cfg := cfgOf(TypeCustom, "https://example.com/alarm")
	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := req.Headers["Authorization"]; ok {
		t.Fatal("no Authorization header expected when secret is empty")
	}

	var payload map[string]any
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"source":         "iot-gateway",
		"event":          "alarm",
		"alarmId":        "a-1",
		"alarmType":      "offline",
		"status":         "active",
		"targetType":     "device",
		"targetId":       "dev-1",
		"targetName":     "dev1",
		"level":          "warning",
		"content":        "设备 dev1 断联",
		"firstOccurTime": "2026-09-24 10:00:00",
	}
	for k, v := range want {
		if got, _ := payload[k].(string); got != v {
			t.Fatalf("envelope[%s] = %q, want %q", k, got, v)
		}
	}
	if _, ok := payload["notifyTime"].(string); !ok {
		t.Fatal("envelope should carry notifyTime")
	}
}

func TestCustomBearerToken(t *testing.T) {
	cfg := cfgOf(TypeCustom, "https://example.com/alarm")
	cfg.Secret = "tok-123"

	f, err := NewFormatter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := f.Format(testOfflineEvent())
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Headers["Authorization"]; got != "Bearer tok-123" {
		t.Fatalf("unexpected Authorization header: %q", got)
	}
}

func TestCustomRecoverEnvelope(t *testing.T) {
	cfg := cfgOf(TypeCustom, "https://example.com/alarm")
	f, _ := NewFormatter(cfg)
	req, err := f.Format(testRecoverEvent())
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["alarmType"] != "recover" || payload["status"] != "cleared" {
		t.Fatalf("unexpected recover envelope: %v", payload)
	}
	if payload["clearTime"] != "2026-09-24 10:05:00" {
		t.Fatalf("recover envelope should carry clearTime, got %v", payload["clearTime"])
	}
	if payload["targetType"] != "channel" {
		t.Fatalf("expected channel target, got %v", payload["targetType"])
	}
}
