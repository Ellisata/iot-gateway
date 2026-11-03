// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"testing"
	"time"

	"iot-gateway/push"
)

func newRecentTestChannel(db string) *tdengineChannel {
	ch := &tdengineChannel{
		recent: push.NewRecentRing(4),
		cfg:    &tdengineConfig{Database: db},
	}
	ch.ob = push.NewOutbox(push.OutboxConfig{Tag: "tdengine", ID: "c1", Name: "td", Conn: ch})
	return ch
}

func TestObserveWriteRecordsRecentAndRows(t *testing.T) {
	ch := newRecentTestChannel("iot")
	ch.observeWrite(push.WriteObservation{
		Payload:  "INSERT INTO iot.t USING iot.st TAGS(1) VALUES (1)",
		Rows:     2,
		OK:       true,
		Duration: 3 * time.Millisecond,
	})

	msgs := ch.RecentMessages(0)
	if len(msgs) != 1 {
		t.Fatalf("recent len = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Target != "iot" || m.Records != 2 || m.Error != "" || m.FromDrain {
		t.Fatalf("recent = %+v, want target iot rows=2 success", m)
	}
	if m.Payload == "" || m.Size == 0 || m.DurationMs != 3 {
		t.Fatalf("recent payload/duration = %+v", m)
	}
	if got := ch.rowsCount.Load(); got != 2 {
		t.Fatalf("rowsCount = %d, want 2", got)
	}

	ch.ob.SetLastErr("tdengine: exec failed: boom")
	ch.observeWrite(push.WriteObservation{
		Payload:   "INSERT INTO iot.t VALUES (2)",
		Rows:      5,
		OK:        false,
		Duration:  time.Millisecond,
		FromDrain: true,
	})

	msgs = ch.RecentMessages(0)
	if len(msgs) != 2 {
		t.Fatalf("recent len = %d, want 2", len(msgs))
	}
	if msgs[0].Error == "" || !msgs[0].FromDrain || msgs[0].Records != 5 {
		t.Fatalf("newest = %+v, want failed drain write with error", msgs[0])
	}
	if got := ch.rowsCount.Load(); got != 2 {
		t.Fatalf("rowsCount = %d, failed write must not count rows", got)
	}

	vo := ch.Snapshot()
	if vo.RowsCount != 2 {
		t.Fatalf("Snapshot.RowsCount = %d, want 2", vo.RowsCount)
	}
	if vo.Topic != "iot" {
		t.Fatalf("Snapshot.Topic = %q, want iot", vo.Topic)
	}
}
