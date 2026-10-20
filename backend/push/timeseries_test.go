// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"testing"

	"iot-gateway/collector"
)

// tsStub 实现 TimeSeriesChannel 的测试通道。
type tsStub struct {
	stubChannel
	timeSeries bool
}

func (s *tsStub) IsTimeSeries() bool { return s.timeSeries }

// TestPushRecordsToTimeSeriesOnlyTargetsTimeSeriesChannels 验证订阅数据只投时序库通道，
// 不经过 MQTT 等非时序通道回环发布。
func TestPushRecordsToTimeSeriesOnlyTargetsTimeSeriesChannels(t *testing.T) {
	plain := &stubChannel{id: "mqtt"}
	ts := &tsStub{stubChannel: stubChannel{id: "td"}, timeSeries: true}
	notTS := &tsStub{stubChannel: stubChannel{id: "other"}, timeSeries: false}

	e := NewEngine(nil)
	e.mu.Lock()
	e.channels["mqtt"] = plain
	e.channels["td"] = ts
	e.channels["other"] = notTS
	e.mu.Unlock()

	records := []collector.CollectedRecord{
		{DeviceID: "d1", DeviceAddressID: "a1", Value: "1", Quality: 192},
	}
	e.PushRecordsToTimeSeries(records)

	if got := len(plain.drain()); got != 0 {
		t.Errorf("plain channel received %d batches, want 0", got)
	}
	if got := len(notTS.drain()); got != 0 {
		t.Errorf("non-time-series channel received %d batches, want 0", got)
	}
	if got := len(ts.drain()); got != 1 {
		t.Fatalf("time-series channel received %d batches, want 1", got)
	}

	// 常规采集推送仍然扇出到全部通道
	e.PushRecords(records)
	if got := len(plain.drain()); got != 1 {
		t.Errorf("PushRecords fan-out: plain got %d, want 1", got)
	}
}
