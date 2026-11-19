// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tree

import (
	"strings"
	"testing"
	"time"

	"iot-gateway/push"
)

func TestParseConfig(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		wantDB   string
		wantPort string
		wantErr  string
	}{
		{name: "defaults", json: `{"host":"iotdb.local"}`, wantDB: "root.dbeco", wantPort: "6667"},
		{name: "root path and numeric port", json: `{"host":"iotdb.local","port":6668,"database":"root.site.area"}`, wantDB: "root.site.area", wantPort: "6668"},
		{name: "unsafe node sanitized", json: `{"host":"iotdb.local","database":"site; DROP DATABASE root.x"}`, wantDB: "root.site__DROP_DATABASE_root.x", wantPort: "6667"},
		{name: "reject root database", json: `{"host":"iotdb.local","database":"root"}`, wantErr: "below root"},
		{name: "reject empty host", json: `{"host":" "}`, wantErr: "host is required"},
		{name: "reject invalid port", json: `{"host":"iotdb.local","port":70000}`, wantErr: "port"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := parseConfig(test.json)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseConfig error = %v, want text %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.safeDatabase != test.wantDB || cfg.port() != test.wantPort {
				t.Fatalf("database/port = %q/%q, want %q/%q", cfg.safeDatabase, cfg.port(), test.wantDB, test.wantPort)
			}
			if cfg.batchWorkers() != defaultBatchWorkers || cfg.batchRows() != defaultBatchRows || cfg.batchInterval() != defaultBatchInterval {
				t.Fatalf("unexpected defaults: workers=%d rows=%d interval=%s", cfg.batchWorkers(), cfg.batchRows(), cfg.batchInterval())
			}
		})
	}
}

func TestBatchLimits(t *testing.T) {
	cfg := &iotdbConfig{BatchWorkers: 100, BatchRows: 10000, BatchIntervalMs: 15, SpoolMaxBatches: 9}
	if cfg.batchWorkers() != maxBatchWorkers || cfg.batchRows() != maxBatchRows {
		t.Fatalf("limits = workers %d rows %d", cfg.batchWorkers(), cfg.batchRows())
	}
	if cfg.batchInterval() != 15*time.Millisecond || cfg.spoolBatchCap() != 9 || cfg.spoolEnabled() != true {
		t.Fatalf("unexpected config helpers: interval=%s cap=%d spool=%v", cfg.batchInterval(), cfg.spoolBatchCap(), cfg.spoolEnabled())
	}
}

func TestChannelTypeRegistration(t *testing.T) {
	meta, ok := push.RegisteredTypeMeta("iotdb-tree")
	if !ok || meta.Label != "Apache IoTDB Tree" || meta.MessageBased {
		t.Fatalf("IoTDB type metadata = %+v, registered=%v", meta, ok)
	}
	channel, err := push.NewTemporaryChannel(nil, "iotdb-tree", "test", `{"host":"iotdb.local","spoolDisabled":true}`)
	if err != nil {
		t.Fatalf("build IoTDB channel from registry: %v", err)
	}
	if _, ok := channel.(push.TimeSeriesChannel); !ok {
		t.Fatalf("registered IoTDB channel %T does not implement TimeSeriesChannel", channel)
	}
}
