// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mappingjson

import (
	"encoding/json"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

func sampleBatch() push.PushBatch {
	return push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{
			{
				DeviceID:           "d1",
				DeviceName:         "Device 1",
				DeviceAddressID:    "a1",
				DeviceAddressName:  "temp",
				DeviceAddressLabel: "温度",
				Value:              "23.5",
				DataType:           "float32",
				Kind:               "float",
				Protocol:           "ModBus.TCP",
				Quality:            192,
			},
			{
				DeviceID:        "d1",
				DeviceName:      "Device 1",
				DeviceAddressID: "a2",
				Value:           "1",
				DataType:        "bool",
				Kind:            "bool",
				Protocol:        "ModBus.TCP",
				Quality:         2,
				CollectedAt:     "2026-10-19 17:05:30.123",
			},
		},
	}
}

func mustEncoder(t *testing.T, raw string) *Encoder {
	t.Helper()
	enc, err := New(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return enc
}

func encodeOne(t *testing.T, enc *Encoder, b push.PushBatch) string {
	t.Helper()
	msgs, err := enc.Encode(b)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("Encode returned no messages")
	}
	return string(msgs[0].Payload)
}

func TestBatchArrayAndTypedValues(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"fields": {
			"deviceId": {"source": "path", "value": "deviceId"},
			"value":    {"source": "path", "value": "value"},
			"quality":  {"source": "path", "value": "quality"},
			"ts":       {"source": "path", "value": "collectedAt"}
		}
	}`)

	got := encodeOne(t, enc, sampleBatch())
	want := `[{"deviceId":"d1","quality":192,"ts":"2026-10-24 10:00:00.000","value":23.5},{"deviceId":"d1","quality":2,"ts":"2026-10-19 17:05:30.123","value":true}]`
	if got != want {
		t.Errorf("payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestEnvelopeAndBatchFields(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"emit": "batch",
		"envelope": "data",
		"batchFields": {
			"site":  {"source": "const", "value": "plant-a"},
			"count": {"source": "path", "value": "count", "type": "number"}
		},
		"fields": {"id": {"source": "path", "value": "deviceAddressId"}}
	}`)

	got := encodeOne(t, enc, sampleBatch())
	want := `{"count":2,"data":[{"id":"a1"},{"id":"a2"}],"site":"plant-a"}`
	if got != want {
		t.Errorf("payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestRecordEmit(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"emit": "record",
		"fields": {"id": {"source": "path", "value": "deviceAddressId"}}
	}`)

	msgs, err := enc.Encode(sampleBatch())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	if string(msgs[0].Payload) != `{"id":"a1"}` || string(msgs[1].Payload) != `{"id":"a2"}` {
		t.Errorf("unexpected payloads: %s | %s", msgs[0].Payload, msgs[1].Payload)
	}
}

func TestNestedKeysAndTransforms(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"fields": {
			"device.id":   {"source": "path", "value": "deviceId"},
			"device.name": {"source": "template", "value": "{deviceName}/{deviceAddressLabel}"},
			"value":       {"source": "path", "value": "value", "type": "number", "transform": [{"op": "scale", "factor": 0.1}, {"op": "round", "digits": 2}]}
		}
	}`)

	got := encodeOne(t, enc, sampleBatch())
	want := `[{"device":{"id":"d1","name":"Device 1/温度"},"value":2.35},{"device":{"id":"d1","name":"Device 1/"},"value":0.10}]`
	if got != want {
		t.Errorf("payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestLargeIntegerPrecision(t *testing.T) {
	b := push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{{
			DeviceID: "d1", DeviceAddressID: "a1", Value: "9007199254740993", Kind: "uint",
		}},
	}
	enc := mustEncoder(t, `{"version":1,"fields":{"v":{"source":"path","value":"value","type":"number"}}}`)
	got := encodeOne(t, enc, b)
	if got != `[{"v":9007199254740993}]` {
		t.Errorf("large integer lost precision: %s", got)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"empty":            `{}`,
		"bad version":      `{"version":2,"fields":{"v":{"source":"path","value":"value"}}}`,
		"bad emit":         `{"emit":"x","fields":{"v":{"source":"path","value":"value"}}}`,
		"bad type":         `{"fields":{"v":{"source":"path","value":"value","type":"date"}}}`,
		"topic source":     `{"fields":{"v":{"source":"topic","value":"x"}}}`,
		"missing path":     `{"fields":{"v":{"source":"path"}}}`,
		"prefix clash":     `{"fields":{"a":{"source":"const","value":"1"},"a.b":{"source":"const","value":"2"}}}`,
		"record envelope":  `{"emit":"record","envelope":"data","fields":{"v":{"source":"path","value":"value"}}}`,
		"records reserved": `{"batchFields":{"records":{"source":"const","value":"x"}},"fields":{"v":{"source":"path","value":"value"}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(json.RawMessage(raw)); err == nil {
				t.Fatalf("New(%s) succeeded, want error", raw)
			}
		})
	}
}

func TestBatchFieldsOnlyDefaultsEnvelope(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"batchFields": {"site": {"source": "const", "value": "plant-a"}},
		"fields": {"id": {"source": "path", "value": "deviceAddressId"}}
	}`)
	got := encodeOne(t, enc, sampleBatch())
	want := `{"records":[{"id":"a1"},{"id":"a2"}],"site":"plant-a"}`
	if got != want {
		t.Errorf("payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}
