// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package influxdb

import (
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/delivery"
)

func newRecentTestChannel() *influxdbSink {
	w := NewWriter(Config{Database: "iot", Measurement: "points"}, tsdb.Options{})
	ch := &influxdbSink{
		log:    tsdb.NopLogger(),
		recent: delivery.NewRecentRing(4),
		writer: w,
	}
	ch.ob = delivery.NewOutbox(delivery.OutboxConfig{Tag: "influxdb", ID: "c1", Name: "influx", Conn: ch})
	return ch
}

func TestObserveWriteRecordsRecentAndRows(t *testing.T) {
	ch := newRecentTestChannel()
	ch.writer.lastStatus.Store(204)
	ch.observeWrite(tsdb.WriteObservation{
		Payload:  "points,device_id=d1 value=1 1700000000000\n",
		Rows:     3,
		OK:       true,
		Duration: 2 * time.Millisecond,
	})

	msgs := ch.RecentMessages(0)
	if len(msgs) != 1 {
		t.Fatalf("recent len = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Target != "iot/points" || m.Records != 3 || m.StatusCode != 204 || m.Error != "" {
		t.Fatalf("recent = %+v, want target iot/points rows=3 status=204", m)
	}
	if got := ch.rowsCount.Load(); got != 3 {
		t.Fatalf("rowsCount = %d, want 3", got)
	}

	ch.ob.SetLastErr("influxdb: write status 422: rejected")
	ch.writer.lastStatus.Store(422)
	ch.observeWrite(tsdb.WriteObservation{
		Payload:   "points,device_id=d2 value=1 1700000000000\n",
		Rows:      1,
		OK:        false,
		Duration:  time.Millisecond,
		FromDrain: true,
	})

	msgs = ch.RecentMessages(0)
	if len(msgs) != 2 {
		t.Fatalf("recent len = %d, want 2", len(msgs))
	}
	if msgs[0].StatusCode != 422 || msgs[0].Error == "" || !msgs[0].FromDrain {
		t.Fatalf("newest = %+v, want 422 failure from drain", msgs[0])
	}
	if got := ch.rowsCount.Load(); got != 3 {
		t.Fatalf("rowsCount = %d, failed write must not count rows", got)
	}

	vo := ch.Status()
	if vo.RowsCount != 3 {
		t.Fatalf("Snapshot.RowsCount = %d, want 3", vo.RowsCount)
	}
	if vo.Topic != "iot/points" {
		t.Fatalf("Snapshot.Topic = %q, want iot/points", vo.Topic)
	}
}
