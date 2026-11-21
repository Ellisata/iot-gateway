// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package iotdb

import (
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/delivery"
)

func newRecentTestSink() *iotdbSink {
	w := NewWriter(Config{Host: "127.0.0.1", Port: portStr("6667"), safeDB: "root.iot"}, tsdb.Options{})
	ch := &iotdbSink{
		channelType: "iotdb-v2",
		log:         tsdb.NopLogger(),
		recent:      delivery.NewRecentRing(4),
		writer:      w,
	}
	ch.ob = delivery.NewOutbox(delivery.OutboxConfig{Tag: "iotdb", ID: "c1", Name: "iotdb", Conn: ch})
	return ch
}

func TestObserveWriteRecordsRecentAndRows(t *testing.T) {
	ch := newRecentTestSink()
	ch.observeWrite(tsdb.WriteObservation{
		Payload:  `[{"d":"root.iot.d_a_1","t":1700000000000,"m":["value_float","quality"],"ty":["DOUBLE","INT32"],"v":[1.5,0]}]`,
		Rows:     3,
		OK:       true,
		Duration: 2 * time.Millisecond,
	})

	msgs := ch.RecentMessages(0)
	if len(msgs) != 1 {
		t.Fatalf("recent len = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Target != "root.iot" || m.Records != 3 || m.Error != "" {
		t.Fatalf("recent = %+v, want target root.iot rows=3 no error", m)
	}
	if got := ch.rowsCount.Load(); got != 3 {
		t.Fatalf("rowsCount = %d, want 3", got)
	}

	ch.ob.SetLastErr("iotdb: insert failed: rejected")
	ch.observeWrite(tsdb.WriteObservation{
		Payload:   `[{"d":"root.iot.d_b_2","t":1700000000000,"m":["quality"],"ty":["INT32"],"v":[192]}]`,
		Rows:      1,
		OK:        false,
		Duration:  time.Millisecond,
		FromDrain: true,
	})

	msgs = ch.RecentMessages(0)
	if len(msgs) != 2 {
		t.Fatalf("recent len = %d, want 2", len(msgs))
	}
	if msgs[0].Error == "" || !msgs[0].FromDrain {
		t.Fatalf("newest = %+v, want failed drain entry", msgs[0])
	}
	if got := ch.rowsCount.Load(); got != 3 {
		t.Fatalf("rowsCount = %d, failed write must not count rows", got)
	}

	vo := ch.Status()
	if vo.Type != "iotdb-v2" {
		t.Fatalf("Status.Type = %q, want iotdb-v2", vo.Type)
	}
	if vo.Broker != "127.0.0.1:6667" || vo.Topic != "root.iot" {
		t.Fatalf("Status = %+v, want broker 127.0.0.1:6667 topic root.iot", vo)
	}
	if vo.RowsCount != 3 {
		t.Fatalf("Status.RowsCount = %d, want 3", vo.RowsCount)
	}
}

func TestNewSinkChannelType(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	sink := New(*cfg, tsdb.Options{ID: "c1", Name: "iotdb"})
	if got := sink.Status().Type; got != "iotdb-v2" {
		t.Fatalf("sink status type = %q, want iotdb-v2", got)
	}
}
