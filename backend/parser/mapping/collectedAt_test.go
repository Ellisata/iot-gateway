// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"fmt"
	"testing"
	"time"
)

func TestNormalizeCollectedAt(t *testing.T) {
	local := time.Date(2026, 10, 19, 17, 5, 30, 123000000, time.Local)
	ms := local.UnixMilli()

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"local millis", "2026-10-19 17:05:30.123", "2026-10-19 17:05:30.123"},
		{"local seconds", "2026-10-19 17:05:30", "2026-10-19 17:05:30.000"},
		{"slash", "2026/10/19 17:05:30", "2026-10-19 17:05:30.000"},
		{"rfc3339", local.Format(time.RFC3339Nano), "2026-10-19 17:05:30.123"},
		{"epoch millis", fmt.Sprint(ms), "2026-10-19 17:05:30.123"},
		{"epoch seconds", fmt.Sprint(ms / 1000), "2026-10-19 17:05:30.000"},
		{"empty", nil, ""},
		{"unknown", "not-a-time", "not-a-time"},
	}
	for _, tc := range cases {
		if got := normalizeCollectedAt(tc.in); got != tc.want {
			t.Errorf("%s: normalizeCollectedAt(%v) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestConvertCollectedAt(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"},
			"collectedAt":     {"source": "path", "value": "ts"}
		}
	}`)

	const epochMS = int64(1729328730123)
	want := time.UnixMilli(epochMS).Format(collectedAtLayout)
	res, err := rule.Convert([]byte(fmt.Sprintf(`[
		{"deviceId":"d1","tagId":"a1","value":1,"ts":%d},
		{"deviceId":"d1","tagId":"a2","value":2}
	]`, epochMS)))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.Records))
	}
	if res.Records[0].CollectedAt != want {
		t.Errorf("collectedAt = %q, want %q", res.Records[0].CollectedAt, want)
	}
	if res.Records[1].CollectedAt != "" {
		t.Errorf("missing ts should leave collectedAt empty, got %q", res.Records[1].CollectedAt)
	}
}
