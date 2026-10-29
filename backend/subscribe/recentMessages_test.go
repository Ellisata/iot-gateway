// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestRecentMessageRingSnapshotNewestFirst(t *testing.T) {
	r := newRecentMessageRing(3)
	r.add(RecentMessage{Topic: "a"})
	r.add(RecentMessage{Topic: "b"})
	r.add(RecentMessage{Topic: "c"})

	got := r.snapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(got))
	}
	want := []string{"c", "b", "a"}
	for i, topic := range want {
		if got[i].Topic != topic {
			t.Errorf("snapshot[%d].Topic = %q, want %q", i, got[i].Topic, topic)
		}
	}
	if got[0].Seq != 3 || got[2].Seq != 1 {
		t.Errorf("seq = %d..%d, want 3..1", got[0].Seq, got[2].Seq)
	}
}

func TestRecentMessageRingEvictsOldest(t *testing.T) {
	r := newRecentMessageRing(3)
	topics := []string{"t0", "t1", "t2", "t3", "t4"}
	for _, topic := range topics {
		r.add(RecentMessage{Topic: topic})
	}

	got := r.snapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(got))
	}
	want := []string{"t4", "t3", "t2"}
	for i, topic := range want {
		if got[i].Topic != topic {
			t.Errorf("snapshot[%d].Topic = %q, want %q", i, got[i].Topic, topic)
		}
	}
}

func TestRecentMessageRingEmpty(t *testing.T) {
	r := newRecentMessageRing(4)
	got := r.snapshot()
	if got == nil {
		t.Fatal("empty snapshot should be non-nil")
	}
	if len(got) != 0 {
		t.Fatalf("empty snapshot len = %d", len(got))
	}
}

func TestRecentMessageRingConcurrentAddAndSnapshot(t *testing.T) {
	r := newRecentMessageRing(8)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			r.add(RecentMessage{Topic: "t"})
		}
	}()
	for i := 0; i < 1000; i++ {
		if got := r.snapshot(); len(got) > 8 {
			t.Errorf("snapshot len = %d, want <= 8", len(got))
			break
		}
	}
	wg.Wait()
}

func TestPreviewPayloadShortUnchanged(t *testing.T) {
	text, truncated := previewPayload([]byte("hello"))
	if text != "hello" || truncated {
		t.Fatalf("preview = %q truncated=%v, want hello/false", text, truncated)
	}
}

func TestPreviewPayloadTruncatesAtUTF8Boundary(t *testing.T) {
	payload := []byte(strings.Repeat("汉", 2000)) // 6000 字节，截断点落在字符中间
	text, truncated := previewPayload(payload)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if len(text) > recentPayloadPreviewBytes {
		t.Errorf("preview bytes = %d, want <= %d", len(text), recentPayloadPreviewBytes)
	}
	if !utf8.ValidString(text) {
		t.Error("preview must stay valid UTF-8")
	}
	if strings.HasSuffix(text, "�") {
		t.Error("preview must not end with a half character")
	}
	if len(text)%3 != 0 {
		t.Errorf("preview bytes = %d, want a multiple of 3", len(text))
	}
}

func TestPreviewPayloadBinaryIsJSONEncodable(t *testing.T) {
	payload := []byte{0xff, 0xfe, 0x00, 0x41}
	text, truncated := previewPayload(payload)
	if truncated {
		t.Error("short binary payload should not be truncated")
	}
	if _, err := json.Marshal(struct {
		Payload string `json:"payload"`
	}{text}); err != nil {
		t.Fatalf("json.Marshal(binary preview): %v", err)
	}
}
