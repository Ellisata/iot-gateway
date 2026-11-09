// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package kafka

import (
	"strings"
	"testing"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(`{"brokers":"10.0.0.1","topic":"iot-data"}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got, want := strings.Join(cfg.brokerAddrs, ","), "10.0.0.1:9092"; got != want {
		t.Errorf("brokers = %q, want %q", got, want)
	}
	if cfg.ClientID != defaultClientID {
		t.Errorf("clientId = %q, want %q", cfg.ClientID, defaultClientID)
	}
	if cfg.Acks != acksAll {
		t.Errorf("acks = %q, want %q", cfg.Acks, acksAll)
	}
	if cfg.AuthType != authNone {
		t.Errorf("authType = %q, want %q", cfg.AuthType, authNone)
	}
	if cfg.TimeoutSeconds != defaultTimeoutSeconds {
		t.Errorf("timeoutSeconds = %d, want %d", cfg.TimeoutSeconds, defaultTimeoutSeconds)
	}
	if cfg.Workers != defaultWorkers {
		t.Errorf("workers = %d, want %d", cfg.Workers, defaultWorkers)
	}
	if cfg.SpoolMaxBatches != defaultSpoolMaxBatches {
		t.Errorf("spoolMaxBatches = %d, want %d", cfg.SpoolMaxBatches, defaultSpoolMaxBatches)
	}
}

func TestParseConfigBrokers(t *testing.T) {
	cfg, err := parseConfig(`{"brokers":" 10.0.0.1:19092, 10.0.0.2 ,10.0.0.1:19092,[::1],::2 ","topic":"iot-data"}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	want := []string{
		"10.0.0.1:19092",
		"10.0.0.2:9092",
		"[::1]:9092",
		"[::2]:9092",
	}
	if len(cfg.brokerAddrs) != len(want) {
		t.Fatalf("brokers = %v, want %v", cfg.brokerAddrs, want)
	}
	for i := range want {
		if cfg.brokerAddrs[i] != want[i] {
			t.Errorf("brokers[%d] = %q, want %q", i, cfg.brokerAddrs[i], want[i])
		}
	}
}

func TestParseConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing brokers", `{"topic":"iot-data"}`},
		{"missing topic", `{"brokers":"10.0.0.1"}`},
		{"invalid topic", `{"brokers":"10.0.0.1","topic":"bad/topic"}`},
		{"dot topic", `{"brokers":"10.0.0.1","topic":"."}`},
		{"invalid acks", `{"brokers":"10.0.0.1","topic":"t","acks":"bogus"}`},
		{"plain without username", `{"brokers":"10.0.0.1","topic":"t","authType":"plain"}`},
		{"unknown auth", `{"brokers":"10.0.0.1","topic":"t","authType":"scram"}`},
		{"insecure without tls", `{"brokers":"10.0.0.1","topic":"t","insecureSkipVerify":true}`},
		{"timeout too large", `{"brokers":"10.0.0.1","topic":"t","timeoutSeconds":121}`},
		{"invalid broker port", `{"brokers":"10.0.0.1:abc","topic":"t"}`},
		{"broker with scheme", `{"brokers":"tcp://10.0.0.1:9092","topic":"t"}`},
		{"broker without host", `{"brokers":":9092","topic":"t"}`},
		{"broken json", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseConfig(tc.raw); err == nil {
				t.Fatalf("parseConfig(%s) should fail", tc.raw)
			}
		})
	}
}

func TestParseConfigNormalizes(t *testing.T) {
	cfg, err := parseConfig(`{
		"brokers":"10.0.0.1",
		"topic":" iot-data ",
		"acks":"ALL",
		"authType":"PLAIN",
		"saslUsername":"gateway",
		"tlsEnabled":true,
		"insecureSkipVerify":true,
		"workers":-3,
		"spoolMaxBatches":-1,
		"groupIds":[" g1 ","","g1","g2"]
	}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Topic != "iot-data" {
		t.Errorf("topic = %q", cfg.Topic)
	}
	if cfg.Acks != acksAll {
		t.Errorf("acks = %q, want all", cfg.Acks)
	}
	if cfg.AuthType != authPlain {
		t.Errorf("authType = %q, want plain", cfg.AuthType)
	}
	if cfg.Workers != 1 {
		t.Errorf("workers = %d, want 1", cfg.Workers)
	}
	if cfg.SpoolMaxBatches != defaultSpoolMaxBatches {
		t.Errorf("spoolMaxBatches = %d", cfg.SpoolMaxBatches)
	}
	if got, want := strings.Join(cfg.GroupIDs, ","), "g1,g2"; got != want {
		t.Errorf("groupIds = %q, want %q", got, want)
	}
}

func TestParseConfigClampsWorkers(t *testing.T) {
	cfg, err := parseConfig(`{"brokers":"10.0.0.1","topic":"t","workers":999}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.Workers != maxWorkers {
		t.Errorf("workers = %d, want %d", cfg.Workers, maxWorkers)
	}
}
