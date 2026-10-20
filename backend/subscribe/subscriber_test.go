// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"sync"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/model/po"
)

// fakeMessage 最小 mqtt.Message 实现，用于在没有 broker 的情况下驱动 onMessage。
type fakeMessage struct {
	topic   string
	payload []byte
}

func (m fakeMessage) Duplicate() bool   { return false }
func (m fakeMessage) Qos() byte         { return 1 }
func (m fakeMessage) Retained() bool    { return false }
func (m fakeMessage) Topic() string     { return m.topic }
func (m fakeMessage) MessageID() uint16 { return 1 }
func (m fakeMessage) Payload() []byte   { return m.payload }
func (m fakeMessage) Ack()              {}

type fakeSink struct {
	mu      sync.Mutex
	records []collector.CollectedRecord
}

func (s *fakeSink) PushRecordsToTimeSeries(records []collector.CollectedRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
}

func (s *fakeSink) snapshot() []collector.CollectedRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]collector.CollectedRecord(nil), s.records...)
}

// fakeInfoSink 记录传给设备/点位元信息落库器的记录。
type fakeInfoSink struct {
	mu      sync.Mutex
	records []collector.CollectedRecord
}

func (s *fakeInfoSink) Record(records []collector.CollectedRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
}

func (s *fakeInfoSink) snapshot() []collector.CollectedRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]collector.CollectedRecord(nil), s.records...)
}

func newTestSubscriber(t *testing.T, mappingJSON string) (*subscriber, *fakeSink, *fakeInfoSink) {
	t.Helper()
	cfg, err := parseSubscription(&po.MqttSubscription{
		ID:          "sub-1",
		Name:        "test",
		Broker:      "127.0.0.1",
		Port:        "1883",
		Topic:       "factory/+/telemetry",
		QoS:         1,
		MappingJSON: mappingJSON,
	})
	if err != nil {
		t.Fatalf("parseSubscription: %v", err)
	}
	sink := &fakeSink{}
	info := &fakeInfoSink{}
	return newSubscriber(cfg, sink, info), sink, info
}

func TestSubscriberConvertsAndDelivers(t *testing.T) {
	sub, sink, info := newTestSubscriber(t, `{
		"root": "$[*]",
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"},
			"kind":            {"source": "path", "value": "kind"}
		}
	}`)

	sub.onMessage(nil, fakeMessage{
		topic:   "factory/line1/telemetry",
		payload: []byte(`[{"deviceId":"d1","tagId":"a1","value":1,"kind":"int"},{"deviceId":"d1","tagId":"a2","value":"x","kind":"string"}]`),
	})

	got := sink.snapshot()
	if len(got) != 2 {
		t.Fatalf("delivered %d records, want 2", len(got))
	}
	if got[0].Value != "1" || got[1].Value != "x" {
		t.Errorf("delivered values = %q,%q", got[0].Value, got[1].Value)
	}
	if meta := info.snapshot(); len(meta) != 2 {
		t.Errorf("device point sink received %d records, want 2", len(meta))
	}
	if sub.messages.Load() != 1 || sub.records.Load() != 2 || sub.errors.Load() != 0 {
		t.Errorf("counters messages/records/errors = %d/%d/%d, want 1/2/0",
			sub.messages.Load(), sub.records.Load(), sub.errors.Load())
	}
	if sub.lastMessageTime() == "" {
		t.Error("lastMessageTime should be set after a successful conversion")
	}
}

func TestSubscriberCountsConversionErrors(t *testing.T) {
	sub, sink, info := newTestSubscriber(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	sub.onMessage(nil, fakeMessage{topic: "t", payload: []byte(`{not json`)})

	if len(sink.snapshot()) != 0 {
		t.Error("invalid payload must not be delivered")
	}
	if len(info.snapshot()) != 0 {
		t.Error("invalid payload must not update device point info")
	}
	if sub.errors.Load() != 1 {
		t.Errorf("errors = %d, want 1", sub.errors.Load())
	}
	if sub.lastError() == "" {
		t.Error("lastError should be set")
	}
}

func TestBrokerURL(t *testing.T) {
	cases := []struct {
		broker, port, want string
	}{
		{"127.0.0.1", "1883", "tcp://127.0.0.1:1883"},
		{"127.0.0.1", "", "tcp://127.0.0.1:1883"},
		{"ssl://broker.example.com:8883", "", "ssl://broker.example.com:8883"},
	}
	for _, tc := range cases {
		cfg := &subscription{broker: tc.broker, port: tc.port}
		if got := cfg.brokerURL(); got != tc.want {
			t.Errorf("brokerURL(%q,%q) = %q, want %q", tc.broker, tc.port, got, tc.want)
		}
	}
}
