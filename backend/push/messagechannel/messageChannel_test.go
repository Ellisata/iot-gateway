// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package messagechannel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

type fakeEncoder struct {
	err      error
	messages int
}

func (e fakeEncoder) Encode(push.PushBatch) ([]push.EncodedMessage, error) {
	if e.err != nil {
		return nil, e.err
	}
	n := e.messages
	if n == 0 {
		n = 1
	}
	out := make([]push.EncodedMessage, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, push.EncodedMessage{Payload: []byte("payload")})
	}
	return out, nil
}

type fakeTransport struct {
	mu       sync.Mutex
	outcomes []Outcome
	probeErr error
	sends    int
	payloads []string
}

func (f *fakeTransport) Probe(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probeErr
}

func (f *fakeTransport) Send(_ context.Context, payload []byte) Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends++
	f.payloads = append(f.payloads, string(payload))
	if len(f.outcomes) == 0 {
		return Outcome{Kind: FailureNone, StatusCode: 200}
	}
	out := f.outcomes[0]
	if len(f.outcomes) > 1 {
		f.outcomes = f.outcomes[1:]
	}
	return out
}

func (f *fakeTransport) setOutcomes(out ...Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes = out
}

func (f *fakeTransport) setProbeErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeErr = err
}

func newTestChannel(t *testing.T, enc push.Encoder, tr Transport) *Channel {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushOutbox{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	c := New(Config{
		Tag:               "test",
		ID:                "c1",
		Name:              "test",
		Type:              "test",
		ConfigSig:         "c1",
		DB:                db,
		Encoder:           enc,
		Transport:         tr,
		Workers:           2,
		Timeout:           time.Second,
		SpoolEnabled:      true,
		SpoolCap:          100,
		ReconnectInterval: 20 * time.Millisecond,
	})
	c.Start()
	t.Cleanup(c.Stop)
	waitFor(t, "connected", func() bool { return c.Connected() })
	return c
}

func sampleBatch() push.PushBatch {
	return push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{
			{DeviceID: "d1", DeviceAddressID: "a1", Value: "1"},
		},
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestMessageChannelSuccess(t *testing.T) {
	tr := &fakeTransport{}
	c := newTestChannel(t, fakeEncoder{}, tr)

	c.Enqueue(sampleBatch())
	waitFor(t, "publish count", func() bool { return c.Snapshot().PublishCount >= 1 })

	if got := c.Snapshot().Type; got != "test" {
		t.Errorf("Snapshot.Type = %q, want test", got)
	}
	if got := len(tr.payloads); got != 1 {
		t.Errorf("transport sends = %d, want 1", got)
	}
}

func TestMessageChannelRetrySpoolsAndDrains(t *testing.T) {
	tr := &fakeTransport{}
	tr.setOutcomes(Outcome{Kind: FailureRetry, Err: errors.New("timeout")})
	c := newTestChannel(t, fakeEncoder{}, tr)

	c.Enqueue(sampleBatch())
	waitFor(t, "spool", func() bool { return spoolCount(c) == 1 })
	waitFor(t, "disconnected", func() bool { return !c.Connected() })

	tr.setOutcomes(Outcome{Kind: FailureNone, StatusCode: 200})
	c.tryConnect()
	waitFor(t, "reconnected", func() bool { return c.Connected() })
	waitFor(t, "drained", func() bool { return spoolCount(c) == 0 })
}

func TestMessageChannelPoisonDrops(t *testing.T) {
	tr := &fakeTransport{}
	tr.setOutcomes(Outcome{Kind: FailurePoison, StatusCode: 400, Err: errors.New("bad request")})
	c := newTestChannel(t, fakeEncoder{}, tr)

	c.Enqueue(sampleBatch())
	waitFor(t, "dropped", func() bool { return c.Snapshot().DroppedCount >= 1 })
	if got := spoolCount(c); got != 0 {
		t.Errorf("spool count = %d, want 0 after poison", got)
	}
	if !c.Connected() {
		t.Error("poison (4xx) must not mark the channel disconnected")
	}
}

func TestMessageChannelEncoderErrorIsPoison(t *testing.T) {
	tr := &fakeTransport{}
	c := newTestChannel(t, fakeEncoder{err: errors.New("bad mapping")}, tr)

	c.Enqueue(sampleBatch())
	waitFor(t, "dropped", func() bool { return c.Snapshot().DroppedCount >= 1 })
	if got := spoolCount(c); got != 0 {
		t.Errorf("spool count = %d, want 0 after encode error", got)
	}
}

func TestMessageChannelMultiMessagePartialRetry(t *testing.T) {
	tr := &fakeTransport{}
	tr.setOutcomes(
		Outcome{Kind: FailureNone, StatusCode: 200},
		Outcome{Kind: FailureRetry, Err: errors.New("timeout")},
	)
	c := newTestChannel(t, fakeEncoder{messages: 2}, tr)

	c.Enqueue(sampleBatch())
	waitFor(t, "spool", func() bool { return spoolCount(c) == 1 })
	if got := c.Snapshot().PublishCount; got != 1 {
		t.Errorf("publish count = %d, want 1 (first message succeeded)", got)
	}
}

func TestMessageChannelPreviewAndTestSend(t *testing.T) {
	tr := &fakeTransport{}
	c := newTestChannel(t, fakeEncoder{}, tr)

	preview := c.Preview(sampleBatch())
	if len(preview.Payloads) != 1 || preview.Payloads[0] != "payload" {
		t.Fatalf("preview = %+v", preview)
	}

	res := c.TestSend(sampleBatch())
	if !res.Success || res.Messages != 1 || res.StatusCode != 200 {
		t.Fatalf("TestSend = %+v", res)
	}

	tr.setOutcomes(Outcome{Kind: FailurePoison, StatusCode: 422, Err: errors.New("bad")})
	res = c.TestSend(sampleBatch())
	if res.Success || res.StatusCode != 422 || res.Error == "" {
		t.Fatalf("TestSend poison = %+v", res)
	}
}

func TestMessageChannelRecordsRecentMessages(t *testing.T) {
	tr := &fakeTransport{}
	tr.setOutcomes(
		Outcome{Kind: FailureNone, StatusCode: 200},
		Outcome{Kind: FailurePoison, StatusCode: 422, Err: errors.New("bad")},
	)
	c := New(Config{
		Tag:       "test",
		ID:        "c1",
		Name:      "test",
		Type:      "test",
		Target:    "https://example.test/hook",
		Encoder:   fakeEncoder{},
		Transport: tr,
		Timeout:   time.Second,
	})
	c.ctx = context.Background()

	if got := c.sendBatch(sampleBatch(), false); got != FailureNone {
		t.Fatalf("first sendBatch = %v, want FailureNone", got)
	}
	if got := c.sendBatch(sampleBatch(), true); got != FailurePoison {
		t.Fatalf("second sendBatch = %v, want FailurePoison", got)
	}

	msgs := c.RecentMessages(0)
	if len(msgs) != 2 {
		t.Fatalf("recent len = %d, want 2", len(msgs))
	}
	// 新→旧：失败的那次在前。
	if msgs[0].StatusCode != 422 || msgs[0].Error == "" || !msgs[0].FromDrain {
		t.Fatalf("newest = %+v, want poison from drain", msgs[0])
	}
	if msgs[1].StatusCode != 200 || msgs[1].Error != "" {
		t.Fatalf("oldest = %+v, want success", msgs[1])
	}
	if msgs[1].Target != "https://example.test/hook" || msgs[1].Payload != "payload" || msgs[1].Size != 7 {
		t.Fatalf("oldest payload/target = %+v", msgs[1])
	}
}

func spoolCount(c *Channel) int64 {
	n, _ := c.ob.Spool().Count()
	return n
}

// fakeKeyedTransport 记录消息键，验证 KeyedTransport 分支。
type fakeKeyedTransport struct {
	fakeTransport
	mu   sync.Mutex
	keys []string
}

func (f *fakeKeyedTransport) SendKeyed(_ context.Context, key string, _ []byte) Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, key)
	return Outcome{Kind: FailureNone, StatusCode: 200}
}

func (f *fakeKeyedTransport) recordedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...)
}

func TestMessageChannelUsesKeyedTransport(t *testing.T) {
	tr := &fakeKeyedTransport{}
	c := newTestChannel(t, fakeEncoder{}, tr)

	b := sampleBatch()
	b.DeviceCode = "dev-001"
	c.Enqueue(b)
	waitFor(t, "first publish", func() bool { return c.Snapshot().PublishCount >= 1 })

	b.DeviceCode = "" // 回落到 DeviceID
	c.Enqueue(b)
	waitFor(t, "second publish", func() bool { return c.Snapshot().PublishCount >= 2 })

	keys := tr.recordedKeys()
	if len(keys) != 2 {
		t.Fatalf("keys = %v, want 2 entries", keys)
	}
	if keys[0] != "dev-001" {
		t.Errorf("first key = %q, want dev-001", keys[0])
	}
	if keys[1] != "d1" {
		t.Errorf("second key = %q, want fallback device ID d1", keys[1])
	}
}

// fakeClosingTransport 记录 Close 调用次数。
type fakeClosingTransport struct {
	fakeTransport
	closeMu sync.Mutex
	closes  int
}

func (f *fakeClosingTransport) Close() error {
	f.closeMu.Lock()
	defer f.closeMu.Unlock()
	f.closes++
	return nil
}

func TestMessageChannelReleaseClosesTransport(t *testing.T) {
	tr := &fakeClosingTransport{}
	c := New(Config{
		Tag:       "test",
		ID:        "c1",
		Name:      "test",
		Type:      "test",
		Encoder:   fakeEncoder{},
		Transport: tr,
	})

	c.Release()
	c.Release()
	c.Stop() // 未启动：Stop 也应释放资源
	tr.closeMu.Lock()
	defer tr.closeMu.Unlock()
	if tr.closes != 3 {
		t.Fatalf("close calls = %d, want 3", tr.closes)
	}
}
