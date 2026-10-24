// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package scriptpayload

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

func sampleBatch() push.PushBatch {
	return push.PushBatch{
		DeviceID:    "dev-1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{
			{
				DeviceID:        "dev-1",
				DeviceAddressID: "a1",
				Value:           "9007199254740993",
				Kind:            "int",
				Quality:         192,
			},
			{
				DeviceID:        "dev-1",
				DeviceAddressID: "a2",
				Value:           "12.5",
				Kind:            "float",
				Quality:         192,
			},
		},
	}
}

func newTestEncoder(t *testing.T, cfg map[string]any) *Encoder {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	enc, err := New(raw)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return enc
}

func TestEncodeString(t *testing.T) {
	enc := newTestEncoder(t, map[string]any{
		"script": `function encode(batch) {
			return JSON.stringify({device: batch.deviceId, values: batch.records.map(function (r) { return r.value })})
		}`,
	})
	msgs, err := enc.Encode(sampleBatch())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	want := `{"device":"dev-1","values":["9007199254740993","12.5"]}`
	if string(msgs[0].Payload) != want {
		t.Errorf("payload = %s, want %s", msgs[0].Payload, want)
	}
}

func TestEncodeArrayReturnsMultipleMessages(t *testing.T) {
	enc := newTestEncoder(t, map[string]any{
		"script": `function encode(batch) {
			var out = []
			for (var i = 0; i < batch.records.length; i++) {
				out.push(batch.deviceId + '/' + batch.records[i].deviceAddressId + '=' + batch.records[i].value)
			}
			return out
		}`,
	})
	msgs, err := enc.Encode(sampleBatch())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if got := string(msgs[0].Payload); got != "dev-1/a1=9007199254740993" {
		t.Errorf("first payload = %q", got)
	}
	if got := string(msgs[1].Payload); got != "dev-1/a2=12.5" {
		t.Errorf("second payload = %q", got)
	}
}

func TestEncodeEmptyOutput(t *testing.T) {
	for name, body := range map[string]string{
		"null":      `function encode() { return null }`,
		"undefined": `function encode() { return undefined }`,
		"empty":     `function encode() { return [] }`,
	} {
		t.Run(name, func(t *testing.T) {
			enc := newTestEncoder(t, map[string]any{"script": body})
			msgs, err := enc.Encode(sampleBatch())
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if len(msgs) != 0 {
				t.Fatalf("messages = %d, want 0", len(msgs))
			}
		})
	}
}

func TestEncodePreservesLargeInteger(t *testing.T) {
	enc := newTestEncoder(t, map[string]any{
		"script": `function encode(batch) { return batch.records[0].value }`,
	})
	msgs, err := enc.Encode(sampleBatch())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got := string(msgs[0].Payload); got != "9007199254740993" {
		t.Errorf("payload = %q, want original integer string", got)
	}
}

func TestNewRejectsInvalidScript(t *testing.T) {
	cases := map[string]map[string]any{
		"syntax error":   {"script": `function encode( {`},
		"missing encode": {"script": `function parse() {}`},
		"not a function": {"script": `var encode = 1`},
		"empty script":   {"script": "  "},
		"timeout range":  {"script": `function encode() {}`, "timeoutMs": 501},
		"messages range": {"script": `function encode() {}`, "maxMessages": 1001},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(cfg)
			if _, err := New(raw); err == nil {
				t.Fatal("New should fail")
			}
		})
	}

	if _, err := New([]byte(`null`)); err == nil {
		t.Fatal("New should reject empty mapping")
	}
	if _, err := New([]byte(`{}`)); err == nil {
		t.Fatal("New should reject missing script")
	}
}

func TestEncodeRejectsInvalidReturn(t *testing.T) {
	cases := map[string]string{
		"object": `function encode() { return {} }`,
		"number": `function encode() { return 1 }`,
		"mixed":  `function encode() { return ['ok', 1] }`,
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			enc := newTestEncoder(t, map[string]any{"script": script})
			if _, err := enc.Encode(sampleBatch()); err == nil {
				t.Fatal("Encode should fail")
			}
		})
	}
}

func TestEncodeRejectsTooManyMessages(t *testing.T) {
	enc := newTestEncoder(t, map[string]any{
		"script":      `function encode() { return new Array(201).fill('x') }`,
		"maxMessages": 200,
	})
	_, err := enc.Encode(sampleBatch())
	if err == nil || !strings.Contains(err.Error(), "超过上限") {
		t.Fatalf("err = %v, want message count limit error", err)
	}
}

func TestEncodeTimeout(t *testing.T) {
	enc := newTestEncoder(t, map[string]any{
		"script":    `function encode() { while (true) {} }`,
		"timeoutMs": 10,
	})
	_, err := enc.Encode(sampleBatch())
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("err = %v, want timeout error", err)
	}
}

func TestEncodeConcurrent(t *testing.T) {
	enc := newTestEncoder(t, map[string]any{
		"script": `function encode(batch) {
			var total = 0
			for (var i = 0; i < batch.records.length; i++) {
				total += Number(batch.records[i].value)
			}
			return String(total)
		}`,
	})
	batch := sampleBatch()

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				msgs, err := enc.Encode(batch)
				if err != nil {
					errCh <- err
					return
				}
				if len(msgs) != 1 {
					errCh <- errors.New("unexpected message count")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent Encode: %v", err)
	}
}
