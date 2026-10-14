// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(`{"host":"127.0.0.1","token":"t"}`)
	if err != nil {
		t.Fatalf("parseConfig failed: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("host = %q, want %q", cfg.Host, "127.0.0.1")
	}
	if cfg.port() != strconv.Itoa(defaultPort) {
		t.Errorf("port default = %q, want %q", cfg.port(), strconv.Itoa(defaultPort))
	}
	if cfg.Database != defaultDatabase {
		t.Errorf("database default = %q, want %q", cfg.Database, defaultDatabase)
	}
	if cfg.Measurement != defaultMeasurement {
		t.Errorf("measurement default = %q, want %q", cfg.Measurement, defaultMeasurement)
	}
	if cfg.batchWorkers() != defaultBatchWorkers {
		t.Errorf("batchWorkers default = %d, want %d", cfg.batchWorkers(), defaultBatchWorkers)
	}
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("batchRows default = %d, want %d", cfg.batchRows(), defaultBatchRows)
	}
	if cfg.batchInterval() != defaultBatchInterval {
		t.Errorf("batchInterval default = %v, want %v", cfg.batchInterval(), defaultBatchInterval)
	}
	if !cfg.spoolEnabled() {
		t.Error("spool should be enabled by default")
	}
	if cfg.spoolBatchCap() != defaultSpoolMaxBatches {
		t.Errorf("spoolBatchCap default = %d, want %d", cfg.spoolBatchCap(), defaultSpoolMaxBatches)
	}
}

func TestParseConfigFull(t *testing.T) {
	cfg, err := parseConfig(`{"host":"10.0.0.1","port":"8443","token":"tok","database":"iot-db","measurement":"meter","batchWorkers":8,"batchRows":3000,"batchIntervalMs":500,"useSSL":true,"insecureSkipVerify":true,"spoolDisabled":true,"spoolMaxBatches":50}`)
	if err != nil {
		t.Fatalf("parseConfig failed: %v", err)
	}
	if cfg.Host != "10.0.0.1" || cfg.port() != "8443" {
		t.Errorf("host/port = %q/%q", cfg.Host, cfg.port())
	}
	if cfg.Token != "tok" {
		t.Errorf("token = %q", cfg.Token)
	}
	if cfg.Database != "iot-db" || cfg.Measurement != "meter" {
		t.Errorf("database/measurement = %q/%q", cfg.Database, cfg.Measurement)
	}
	if cfg.batchWorkers() != 8 {
		t.Errorf("batchWorkers = %d, want 8", cfg.batchWorkers())
	}
	if cfg.batchRows() != 3000 {
		t.Errorf("batchRows = %d, want 3000", cfg.batchRows())
	}
	if cfg.batchInterval() != 500*time.Millisecond {
		t.Errorf("batchInterval = %v, want 500ms", cfg.batchInterval())
	}
	if !cfg.UseSSL {
		t.Error("useSSL should be true")
	}
	if !cfg.InsecureSkipVerify {
		t.Error("insecureSkipVerify should be true")
	}
	if cfg.spoolEnabled() {
		t.Error("spool should be disabled")
	}
	if cfg.spoolBatchCap() != 50 {
		t.Errorf("spoolBatchCap = %d, want 50", cfg.spoolBatchCap())
	}
}

func TestParseConfigPort(t *testing.T) {
	// 数字写法端口
	cfg, err := parseConfig(`{"host":"127.0.0.1","port":8181,"token":"t"}`)
	if err != nil {
		t.Fatalf("parseConfig failed: %v", err)
	}
	if cfg.port() != "8181" {
		t.Errorf("numeric port = %q, want 8181", cfg.port())
	}
	// 字符串写法端口
	cfg, _ = parseConfig(`{"host":"10.0.0.1","port":"8443","token":"t"}`)
	if cfg.port() != "8443" {
		t.Errorf("string port = %q, want 8443", cfg.port())
	}
}

func TestParseConfigBaseURL(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"10.0.0.1","port":"8443","token":"t","useSSL":true}`)
	if cfg.baseURL() != "https://10.0.0.1:8443" {
		t.Errorf("baseURL = %q, want https://10.0.0.1:8443", cfg.baseURL())
	}
	// useSSL 缺省 → http
	cfg, _ = parseConfig(`{"host":"10.0.0.1","token":"t"}`)
	if cfg.baseURL() != "http://10.0.0.1:"+strconv.Itoa(defaultPort) {
		t.Errorf("baseURL = %q, want http://10.0.0.1:%d", cfg.baseURL(), defaultPort)
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing token", `{"host":"127.0.0.1","database":"db"}`},
		{"missing host", `{"token":"t"}`},
		{"bad port", `{"host":"127.0.0.1","port":"abc","token":"t"}`},
		{"bad json", `{not json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseConfig(c.json); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestBatchWorkersClamp(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"127.0.0.1","token":"t"}`)
	if cfg.batchWorkers() != defaultBatchWorkers {
		t.Errorf("empty batchWorkers = %d, want %d", cfg.batchWorkers(), defaultBatchWorkers)
	}
	cfg.BatchWorkers = 0
	if cfg.batchWorkers() != defaultBatchWorkers {
		t.Errorf("zero batchWorkers = %d, want default", cfg.batchWorkers())
	}
	cfg.BatchWorkers = 999
	if cfg.batchWorkers() != maxBatchWorkers {
		t.Errorf("oversized batchWorkers = %d, want %d", cfg.batchWorkers(), maxBatchWorkers)
	}
}

func TestBatchRowsClamp(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"127.0.0.1","token":"t"}`)
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("empty batchRows = %d, want %d", cfg.batchRows(), defaultBatchRows)
	}
	cfg.BatchRows = 0
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("zero batchRows = %d, want default", cfg.batchRows())
	}
	cfg.BatchRows = 999999
	if cfg.batchRows() != maxBatchRows {
		t.Errorf("oversized batchRows = %d, want %d", cfg.batchRows(), maxBatchRows)
	}
}

func TestBatchIntervalFallback(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"127.0.0.1","token":"t"}`)
	cfg.BatchIntervalMs = -1
	if cfg.batchInterval() != defaultBatchInterval {
		t.Errorf("non-positive batchIntervalMs should fall back, got %v", cfg.batchInterval())
	}
}

func TestEndpoints(t *testing.T) {
	cfg, _ := parseConfig(`{"host":"10.0.0.1","port":"8181","token":"t","database":"iot-db","measurement":"m"}`)
	if cfg.healthURL() != "http://10.0.0.1:8181/health" {
		t.Errorf("healthURL = %q", cfg.healthURL())
	}
	if cfg.configureDatabaseURL() != "http://10.0.0.1:8181/api/v3/configure/database" {
		t.Errorf("configureDatabaseURL = %q", cfg.configureDatabaseURL())
	}
	want := "http://10.0.0.1:8181/api/v3/write_lp?db=iot-db&precision=millisecond"
	if cfg.writeURL() != want {
		t.Errorf("writeURL = %q, want %q", cfg.writeURL(), want)
	}
	// db 含特殊字符时防御性 QueryEscape
	cfg.Database = "my db"
	if !strings.Contains(cfg.writeURL(), "db=my+db") {
		t.Errorf("writeURL should QueryEscape database, got %q", cfg.writeURL())
	}
}
