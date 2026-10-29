// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/parser"
)

// fakeMessage 最小 mqtt.Message 实现，用于在没有 broker 的情况下驱动 onMessage。
type fakeMessage struct {
	topic    string
	payload  []byte
	retained bool
}

func (m fakeMessage) Duplicate() bool   { return false }
func (m fakeMessage) Qos() byte         { return 1 }
func (m fakeMessage) Retained() bool    { return m.retained }
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
	sub := newSubscriber(cfg, sink, info)
	t.Cleanup(sub.stop)
	return sub, sink, info
}

// feed 投递一条报文并等待 worker 处理完（测试里用确定性等待替代 sleep）。
func feed(t *testing.T, sub *subscriber, msg fakeMessage) {
	t.Helper()
	sub.onMessage(nil, msg)
	if !sub.waitIdle(2 * time.Second) {
		t.Fatal("解析 worker 未在 2s 内排空队列")
	}
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

	feed(t, sub, fakeMessage{
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

	feed(t, sub, fakeMessage{topic: "t", payload: []byte(`{not json`)})

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

func TestSubscriberSwapParserWithoutReconnect(t *testing.T) {
	sub, sink, _ := newTestSubscriber(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	feed(t, sub, fakeMessage{topic: "t", payload: []byte(`{"deviceId":"d1","tagId":"a1","value":"v1"}`)})
	if got := sink.snapshot(); len(got) != 1 || got[0].Value != "v1" {
		t.Fatalf("before swap records = %+v", got)
	}

	next, err := parseSubscription(&po.MqttSubscription{
		ID:          "sub-1",
		Name:        "test",
		Broker:      "127.0.0.1",
		Port:        "1883",
		Topic:       "factory/+/telemetry",
		QoS:         1,
		MappingJSON: `{"fields":{"deviceId":{"source":"path","value":"deviceId"},"deviceAddressId":{"source":"path","value":"tagId"},"value":{"source":"template","value":"v2-{value}"}}}`,
	})
	if err != nil {
		t.Fatalf("parseSubscription(next): %v", err)
	}
	if next.parserSig == sub.ParserSig() {
		t.Fatal("解析配置变化后签名应不同")
	}
	if err := sub.swapParser(next.prs, next.parserSig); err != nil {
		t.Fatalf("swapParser: %v", err)
	}

	feed(t, sub, fakeMessage{topic: "t", payload: []byte(`{"deviceId":"d1","tagId":"a1","value":"x"}`)})
	got := sink.snapshot()
	if len(got) != 2 || got[1].Value != "v2-x" {
		t.Fatalf("after swap records = %+v", got)
	}
}

func TestEnqueueDropsOldestWhenFull(t *testing.T) {
	sub := &subscriber{queue: make(chan parser.Message, 1)}
	sub.enqueue(parser.Message{Topic: "old"})
	sub.enqueue(parser.Message{Topic: "new"})

	if got := (<-sub.queue).Topic; got != "new" {
		t.Errorf("remaining message = %q, want newest", got)
	}
	if sub.dropped.Load() != 1 {
		t.Errorf("dropped = %d, want 1", sub.dropped.Load())
	}
	if sub.pending.Load() != 1 {
		t.Errorf("pending = %d, want 1 (dropped message must be subtracted)", sub.pending.Load())
	}
}

func TestSubscriberRecordsRecentMessages(t *testing.T) {
	sub, _, _ := newTestSubscriber(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	feed(t, sub, fakeMessage{topic: "factory/a", payload: []byte(`{"x":1}`)})
	feed(t, sub, fakeMessage{topic: "factory/b", payload: []byte(`{"y":2}`), retained: true})

	msgs := sub.recentMessages()
	if len(msgs) != 2 {
		t.Fatalf("recent messages = %d, want 2", len(msgs))
	}
	// 新→旧
	if msgs[0].Topic != "factory/b" || msgs[1].Topic != "factory/a" {
		t.Errorf("recent order = %q,%q, want factory/b,factory/a", msgs[0].Topic, msgs[1].Topic)
	}
	if msgs[0].Seq <= msgs[1].Seq {
		t.Errorf("seq should increase: %d,%d", msgs[1].Seq, msgs[0].Seq)
	}
	if !msgs[0].Retained || msgs[1].Retained {
		t.Errorf("retained flags = %v,%v, want true,false", msgs[0].Retained, msgs[1].Retained)
	}
	if msgs[0].QoS != 1 {
		t.Errorf("qos = %d, want 1", msgs[0].QoS)
	}
	if msgs[0].Payload != `{"y":2}` || msgs[0].Size != len(`{"y":2}`) {
		t.Errorf("payload = %q size=%d", msgs[0].Payload, msgs[0].Size)
	}
	if msgs[0].Truncated || msgs[0].Oversized {
		t.Errorf("truncated/oversized = %v/%v, want false/false", msgs[0].Truncated, msgs[0].Oversized)
	}
}

func TestSubscriberRecordsOversizedMessage(t *testing.T) {
	sub, _, _ := newTestSubscriber(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	big := bytes.Repeat([]byte("a"), parser.DefaultMaxPayloadBytes+1)
	feed(t, sub, fakeMessage{topic: "factory/big", payload: big})

	msgs := sub.recentMessages()
	if len(msgs) != 1 {
		t.Fatalf("recent messages = %d, want 1", len(msgs))
	}
	if !msgs[0].Oversized || !msgs[0].Truncated {
		t.Errorf("oversized/truncated = %v/%v, want true/true", msgs[0].Oversized, msgs[0].Truncated)
	}
	if msgs[0].Size != len(big) {
		t.Errorf("size = %d, want %d", msgs[0].Size, len(big))
	}
	if len(msgs[0].Payload) != recentPayloadPreviewBytes {
		t.Errorf("preview bytes = %d, want %d", len(msgs[0].Payload), recentPayloadPreviewBytes)
	}
	if sub.errors.Load() != 1 {
		t.Errorf("errors = %d, want 1 (oversized still counted as before)", sub.errors.Load())
	}
}

func TestSubscriberRecentMessagesSurviveParserSwap(t *testing.T) {
	sub, _, _ := newTestSubscriber(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)
	feed(t, sub, fakeMessage{topic: "t", payload: []byte(`{"deviceId":"d1","tagId":"a1","value":"v1"}`)})

	before := sub.recentMessages()
	next, err := parseSubscription(&po.MqttSubscription{
		ID:          "sub-1",
		Name:        "test",
		Broker:      "127.0.0.1",
		Port:        "1883",
		Topic:       "factory/+/telemetry",
		QoS:         1,
		MappingJSON: `{"fields":{"deviceId":{"source":"path","value":"deviceId"},"deviceAddressId":{"source":"path","value":"tagId"},"value":{"source":"template","value":"v2-{value}"}}}`,
	})
	if err != nil {
		t.Fatalf("parseSubscription(next): %v", err)
	}
	if err := sub.swapParser(next.prs, next.parserSig); err != nil {
		t.Fatalf("swapParser: %v", err)
	}

	after := sub.recentMessages()
	if len(after) != len(before) || after[0].Seq != before[0].Seq {
		t.Fatalf("recent messages changed after parser swap: before=%+v after=%+v", before, after)
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
