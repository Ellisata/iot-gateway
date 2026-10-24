// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package codec

import (
	"encoding/json"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

func TestEmptyFormatDefaultsToNative(t *testing.T) {
	enc, err := New("", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b := push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{{
			DeviceID: "d1", DeviceAddressID: "a1", Value: "1",
		}},
	}
	msgs, err := enc.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
}

func TestFormatValidation(t *testing.T) {
	cases := []struct {
		name    string
		format  string
		mapping string
		wantErr bool
	}{
		{"native", "native-json", "", false},
		{"native with mapping", "native-json", `{}`, true},
		{"mapping", "mapping-json", `{"fields":{"v":{"source":"path","value":"value"}}}`, false},
		{"mapping missing", "mapping-json", "", true},
		{"script", "script", `{"script":"function encode() { return 'x' }"}`, false},
		{"script missing", "script", "", true},
		{"script syntax error", "script", `{"script":"function encode( {"}`, true},
		{"unknown", "xml", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			if tc.mapping != "" {
				raw = json.RawMessage(tc.mapping)
			}
			_, err := New(tc.format, raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("New(%q) error = %v, wantErr %v", tc.format, err, tc.wantErr)
			}
		})
	}
}
