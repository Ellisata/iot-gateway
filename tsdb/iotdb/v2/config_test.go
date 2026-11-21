// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package iotdb

import (
	"strings"
	"testing"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig(`{"host":"127.0.0.1"}`)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if cfg.Username != "root" {
		t.Errorf("username default = %q, want root", cfg.Username)
	}
	if cfg.Password != "root" {
		t.Errorf("password default = %q, want root", cfg.Password)
	}
	if cfg.port() != "6667" {
		t.Errorf("port default = %q, want 6667", cfg.port())
	}
	if cfg.safeDB != defaultDatabase {
		t.Errorf("safeDB default = %q, want %q", cfg.safeDB, defaultDatabase)
	}
	if !cfg.SpoolEnabled() {
		t.Error("spool should be enabled by default")
	}
	if cfg.batchWorkers() != defaultBatchWorkers {
		t.Errorf("batchWorkers = %d, want %d", cfg.batchWorkers(), defaultBatchWorkers)
	}
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("batchRows = %d, want %d", cfg.batchRows(), defaultBatchRows)
	}
}

func TestParseConfigFull(t *testing.T) {
	cfg, err := ParseConfig(`{"host":"iotdb.internal","port":6668,"username":"u","password":"p@ss",` +
		`"database":"factory.line1","rpcCompression":true,"batchWorkers":8,"batchRows":99999,` +
		`"batchIntervalMs":50,"spoolDisabled":true,"spoolMaxBatches":50,` +
		`"tlsCAFile":"ca.pem","tlsCertFile":"cert.pem","tlsKeyFile":"key.pem"}`)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if cfg.port() != "6668" {
		t.Errorf("port = %q, want 6668", cfg.port())
	}
	if cfg.Username != "u" || cfg.Password != "p@ss" {
		t.Errorf("credentials = %q/%q", cfg.Username, cfg.Password)
	}
	if cfg.safeDB != "root.factory.line1" {
		t.Errorf("safeDB = %q, want root.factory.line1", cfg.safeDB)
	}
	if !cfg.RPCCompression {
		t.Error("rpcCompression should be true")
	}
	if cfg.batchWorkers() != 8 {
		t.Errorf("batchWorkers = %d, want 8", cfg.batchWorkers())
	}
	if cfg.batchRows() != maxBatchRows {
		t.Errorf("batchRows = %d, want clamp %d", cfg.batchRows(), maxBatchRows)
	}
	if cfg.SpoolEnabled() {
		t.Error("spool should be disabled")
	}
	if cfg.SpoolBatchCap() != 50 {
		t.Errorf("spool cap = %d, want 50", cfg.SpoolBatchCap())
	}
	if cfg.tlsConfig() == nil {
		t.Error("tls config should be non-nil when cert/key are set")
	}
}

func TestParseConfigNodesForms(t *testing.T) {
	for name, raw := range map[string]string{
		"array":  `{"nodes":["a:6667","b:6667"]}`,
		"string": `{"nodes":"a:6667, b:6667"}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfig(raw)
			if err != nil {
				t.Fatalf("ParseConfig failed: %v", err)
			}
			if len(cfg.Nodes) != 2 || cfg.Nodes[0] != "a:6667" || cfg.Nodes[1] != "b:6667" {
				t.Fatalf("nodes = %v, want [a:6667 b:6667]", cfg.Nodes)
			}
			if got := cfg.endpoint(); got != "a:6667,b:6667" {
				t.Errorf("endpoint = %q", got)
			}
		})
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := map[string]string{
		"missing host":      `{}`,
		"invalid port":      `{"host":"h","port":"abc"}`,
		"invalid node":      `{"nodes":["noport"]}`,
		"cert without key":  `{"host":"h","tlsCertFile":"c.pem"}`,
		"key without cert":  `{"host":"h","tlsKeyFile":"k.pem"}`,
		"database too long": `{"host":"h","database":"` + strings.Repeat("a", 70) + `"}`,
		"invalid json":      `{`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(raw); err == nil {
				t.Fatalf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestNormalizeDatabasePath(t *testing.T) {
	cases := map[string]string{
		"iot":            "root.iot",
		"root.iot":       "root.iot",
		"ROOT.Factory-1": "root.Factory_1",
		"root.a.b.c":     "root.a.b.c",
		".root.iot.":     "root.iot",
		"...":            "root",
		"1factory":       "root._1factory",
	}
	for in, want := range cases {
		got, err := normalizeDatabasePath(in)
		if err != nil {
			t.Errorf("normalizeDatabasePath(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeDatabasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBatchClamps(t *testing.T) {
	cfg, err := ParseConfig(`{"host":"h","batchWorkers":1000,"batchRows":-1,"batchIntervalMs":-5}`)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if cfg.batchWorkers() != maxBatchWorkers {
		t.Errorf("batchWorkers = %d, want %d", cfg.batchWorkers(), maxBatchWorkers)
	}
	if cfg.batchRows() != defaultBatchRows {
		t.Errorf("batchRows = %d, want %d", cfg.batchRows(), defaultBatchRows)
	}
	if cfg.batchInterval() != defaultBatchInterval {
		t.Errorf("batchInterval = %v, want %v", cfg.batchInterval(), defaultBatchInterval)
	}
}

func TestCreateDatabaseSQL(t *testing.T) {
	cfg, err := ParseConfig(`{"host":"h","database":"iot"}`)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if got, want := cfg.createDatabaseSQL(), "CREATE DATABASE root.iot"; got != want {
		t.Fatalf("createDatabaseSQL = %q, want %q", got, want)
	}
}
