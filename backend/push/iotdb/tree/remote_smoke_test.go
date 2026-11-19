// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tree

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/apache/iotdb-client-go/client"
	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

func TestRemoteSmokeRoundTripsTreeRows(t *testing.T) {
	port := os.Getenv("IOTDB_TEST_PORT")
	if port == "" {
		t.Skip("set IOTDB_TEST_PORT to an IoTDB RPC endpoint to run the remote smoke test")
	}
	host := os.Getenv("IOTDB_TEST_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	username := os.Getenv("IOTDB_TEST_USER")
	if username == "" {
		username = "root"
	}
	password := os.Getenv("IOTDB_TEST_PASSWORD")
	if password == "" {
		password = "root"
	}
	database := fmt.Sprintf("root.dbeco_iotdb_%d", time.Now().UnixNano())
	configJSON := fmt.Sprintf(`{"host":%q,"port":%q,"username":%q,"password":%q,"database":%q,"spoolDisabled":true}`, host, port, username, password, database)
	channel, err := newIoTDBChannel(nil, &po.PushChannel{ID: "smoke", Name: "IoTDB smoke", Type: "iotdb-tree", ConfigJSON: configJSON})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		channel.poolMu.Lock()
		if channel.pool != nil {
			channel.pool.Close()
			channel.pool = nil
		}
		channel.poolMu.Unlock()
		session := client.NewSession(channel.sessionConfig())
		if err := session.Open(false, int(connectTimeout.Milliseconds())); err != nil {
			t.Errorf("open cleanup session: %v", err)
			return
		}
		defer session.Close()
		if err := session.ExecuteNonQueryStatement("DELETE DATABASE " + channel.cfg.safeDatabase); err != nil {
			t.Errorf("delete isolated IoTDB database: %v", err)
		}
	}()

	channel.tryConnect()
	if !channel.Connected() {
		t.Fatalf("IoTDB did not connect: %s", channel.ob.LastErr())
	}
	collectedAt := time.Now().Add(-time.Second).Format("2006-01-02 15:04:05.000")
	builder := newInsertBuilder(channel.cfg.safeDatabase)
	builder.Append(push.PushBatch{
		CollectedAt: collectedAt,
		Records: []collector.CollectedRecord{{
			DeviceID:        "smoke-device",
			DeviceAddressID: "temperature",
			Value:           "21.75",
			Kind:            "float",
			Protocol:        "remote-smoke",
			Quality:         192,
			ThingModelCode:  "smoke-model",
			PropertyCode:    "temperature",
		}},
	})
	if !channel.write(builder.Payload()) {
		t.Fatalf("IoTDB write failed: %s", channel.ob.LastErr())
	}
	// Reopening a channel clears its local schema cache. Replay must also
	// succeed when every series already exists on the server.
	channel.schemas = make(map[string]struct{})
	if err := channel.TestConnectivity(); err != nil || !channel.write(builder.Payload()) {
		t.Fatalf("IoTDB reinitialization and replay failed: %v %s", err, channel.ob.LastErr())
	}

	querySession := client.NewSession(channel.sessionConfig())
	if err := querySession.Open(false, int(connectTimeout.Milliseconds())); err != nil {
		t.Fatal(err)
	}
	defer querySession.Close()
	device := seriesPath(channel.cfg.safeDatabase, "smoke-device", "temperature")
	data, err := querySession.ExecuteQueryStatement("SELECT value_float AS reading FROM "+device+" LIMIT 2", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	next, err := data.Next()
	if err != nil || !next {
		t.Fatalf("IoTDB query returned no row: %v", err)
	}
	value, err := data.GetDouble("reading")
	if err != nil || value != 21.75 {
		t.Fatalf("IoTDB value = %v (%v), want 21.75", value, err)
	}
}
