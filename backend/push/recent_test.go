// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRecentRingNewestFirstAndLimit(t *testing.T) {
	r := NewRecentRing(5)
	for i := 1; i <= 3; i++ {
		r.Add(RecentPushMessage{Target: "t"}, []byte("x"))
	}

	all := r.Snapshot(0)
	if len(all) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(all))
	}
	if all[0].Seq != 3 || all[1].Seq != 2 || all[2].Seq != 1 {
		t.Fatalf("snapshot order = %d,%d,%d, want 3,2,1", all[0].Seq, all[1].Seq, all[2].Seq)
	}

	limited := r.Snapshot(2)
	if len(limited) != 2 || limited[0].Seq != 3 || limited[1].Seq != 2 {
		t.Fatalf("limited snapshot = %+v, want newest two", limited)
	}
}

func TestRecentRingOverwritesOldest(t *testing.T) {
	r := NewRecentRing(2)
	for i := 1; i <= 3; i++ {
		r.Add(RecentPushMessage{Target: "t"}, []byte("x"))
	}

	got := r.Snapshot(0)
	if len(got) != 2 {
		t.Fatalf("snapshot len = %d, want 2 (capacity)", len(got))
	}
	if got[0].Seq != 3 || got[1].Seq != 2 {
		t.Fatalf("seqs = %d,%d, want 3,2 (oldest overwritten)", got[0].Seq, got[1].Seq)
	}
}

func TestPreviewPayloadTruncatesOnUTF8Boundary(t *testing.T) {
	// 2047 个 ASCII 后接一个 3 字节汉字：截断点正好落在汉字中间，
	// 预览必须回退到完整字符边界，且不能多字节字符截半。
	payload := []byte(strings.Repeat("a", 2047) + "中")
	text, size, truncated := PreviewPayload(payload)

	if size != len(payload) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if !utf8.ValidString(text) {
		t.Fatalf("preview is not valid UTF-8: %q", text)
	}
	if len(text) != 2047 {
		t.Fatalf("preview len = %d, want 2047", len(text))
	}
}

func TestPreviewPayloadKeepsSmallPayload(t *testing.T) {
	text, size, truncated := PreviewPayload([]byte("hello"))
	if text != "hello" || size != 5 || truncated {
		t.Fatalf("preview = (%q, %d, %v), want (hello, 5, false)", text, size, truncated)
	}
}
