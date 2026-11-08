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

// TestDeviceAndGroupCodes 记录与批次两级都能读到设备编码和采集组编码。
func TestDeviceAndGroupCodes(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"batchFields": {
			"batchCode": {"source": "path", "value": "deviceCode"},
			"groupCode": {"source": "path", "value": "groupCode"}
		},
		"fields": {
			"device": {"source": "path", "value": "deviceCode"},
			"group":  {"source": "path", "value": "groupCode"}
		}
	}`)

	b := sampleBatch()
	b.DeviceCode = "plc-a"
	b.GroupCode = "fast"
	for i := range b.Records {
		b.Records[i].DeviceCode = "plc-a"
		b.Records[i].GroupCode = "fast"
	}

	got := encodeOne(t, enc, b)
	want := `{"batchCode":"plc-a","groupCode":"fast","records":[{"device":"plc-a","group":"fast"},{"device":"plc-a","group":"fast"}]}`
	if got != want {
		t.Errorf("payload:\n got: %s\nwant: %s", got, want)
	}
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

// TestEmptyValueBecomesNull 坏质量码的点位驱动只回空串占位，映射层必须输出 null
// 而不是让整批失败：否则 MQTT 批次编码失败、反复补发也永远发不出去。
func TestEmptyValueBecomesNull(t *testing.T) {
	b := push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{{
			DeviceID: "d1", DeviceAddressID: "a1",
			Value: "", DataType: "uint16", Kind: "uint", Quality: 0,
		}},
	}

	cases := map[string]string{
		"auto by kind":    `{"version":1,"fields":{"value":{"source":"path","value":"value"}}}`,
		"explicit number": `{"version":1,"fields":{"value":{"source":"path","value":"value","type":"number"}}}`,
		"explicit bool":   `{"version":1,"fields":{"value":{"source":"path","value":"value","type":"boolean"}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got := encodeOne(t, mustEncoder(t, raw), b)
			if got != `[{"value":null}]` {
				t.Errorf("payload = %s, want [{\"value\":null}]", got)
			}
		})
	}

	// 非空但不是数字仍然报错：那是真的数据类型/映射配置问题，不能静默变成 null。
	bad := b
	bad.Records[0].Value = "abc"
	enc := mustEncoder(t, `{"version":1,"fields":{"value":{"source":"path","value":"value","type":"number"}}}`)
	if _, err := enc.Encode(bad); err == nil {
		t.Fatal("Encode with non-numeric value should fail")
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

// TestThingModelCodesInDoc 物模型编码 / 属性编码是出站映射可读的记录字段；
// 未绑定时值为空串（路径存在、不报错），便于客户按需输出。
func TestThingModelCodesInDoc(t *testing.T) {
	enc := mustEncoder(t, `{"version":1,"fields":{
		"model": {"source":"path","value":"thingModelCode"},
		"prop":  {"source":"path","value":"propertyCode"}
	}}`)
	b := sampleBatch()
	b.Records[0].ThingModelCode = "th_sensor"
	b.Records[0].PropertyCode = "temperature"

	got := encodeOne(t, enc, b)
	want := `[{"model":"th_sensor","prop":"temperature"},{"model":"","prop":""}]`
	if got != want {
		t.Errorf("payload mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestTriggerMetadataReachableByPath(t *testing.T) {
	enc := mustEncoder(t, `{
		"version": 1,
		"batchFields": {
			"rule": {"source": "path", "value": "trigger.ruleId"},
			"tid":  {"source": "path", "value": "trigger.triggerId"}
		},
		"fields": {
			"point": {"source": "path", "value": "deviceAddressId"},
			"rule":  {"source": "path", "value": "trigger.ruleId"}
		}
	}`)
	b := sampleBatch()
	b.Records[0].Trigger = &collector.RecordTrigger{
		Source: "trigger", RuleID: "r1", RuleName: "批次完成",
		TriggerID: "t1", FiredAt: "2026-11-04 10:00:00.000",
	}
	out := encodeOne(t, enc, b)

	var root map[string]any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if root["rule"] != "r1" || root["tid"] != "t1" {
		t.Fatalf("batchFields trigger = %v/%v", root["rule"], root["tid"])
	}
	records, ok := root["records"].([]any)
	if !ok || len(records) != 2 {
		t.Fatalf("records = %v", root["records"])
	}
	first, _ := records[0].(map[string]any)
	if first["rule"] != "r1" {
		t.Fatalf("record trigger = %v", first["rule"])
	}
}
