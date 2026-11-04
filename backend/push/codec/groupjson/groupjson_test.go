// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package groupjson

import (
	"encoding/json"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

func TestEncodeFrameKeepsAllPoints(t *testing.T) {
	batch := push.PushBatch{
		DeviceID:        "dev-1",
		DeviceCode:      "dev-code-1",
		ThingModelCode:  "th_sensor",
		GroupID:         "g-1",
		GroupCode:       "fast",
		GroupName:       "fast",
		GroupIntervalMs: 500,
		CollectedAt:     "2026-11-02 20:00:00.123",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceName: "一号机", Protocol: "ModBus.TCP",
				DeviceAddressID: "a1", DeviceAddressName: "temperature",
				Value: "23.5", DataType: "float", Kind: "number", Quality: 192, PropertyCode: "temp"},
			{DeviceID: "dev-1", DeviceName: "一号机", Protocol: "ModBus.TCP",
				DeviceAddressID: "a2", DeviceAddressName: "pressure",
				Value: "", DataType: "int16", Kind: "number", Quality: 1},
		},
	}

	msgs, err := New().Encode(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}

	var frame Frame
	if err := json.Unmarshal(msgs[0].Payload, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if frame.CollectedAt != batch.CollectedAt || frame.DeviceID != "dev-1" || frame.GroupID != "g-1" ||
		frame.GroupCode != "fast" || frame.GroupName != "fast" || frame.GroupIntervalMs != 500 || frame.DeviceName != "一号机" ||
		frame.DeviceCode != "dev-code-1" || frame.ThingModelCode != "th_sensor" || frame.Protocol != "ModBus.TCP" {
		t.Errorf("frame header = %+v", frame)
	}

	// 坏质量点不剔除：与正常点同在一个数组，顺序与批次一致，下游按 quality 区分。
	if len(frame.Points) != 2 {
		t.Fatalf("points = %d, want 2: %+v", len(frame.Points), frame.Points)
	}
	good := frame.Points[0]
	if good.DeviceAddressName != "temperature" || good.Value != "23.5" || good.Quality != 192 ||
		good.DataType != "float" || good.Kind != "number" || good.PropertyCode != "temp" {
		t.Errorf("points[0] = %+v", good)
	}
	bad := frame.Points[1]
	if bad.DeviceAddressID != "a2" || bad.Value != "" || bad.Quality != 1 {
		t.Errorf("points[1] = %+v", bad)
	}
}

// 点位字段名必须与 native-json 的 PushPoint 一致；设备级字段不得随点位重复。
func TestEncodePointFieldNamesMatchNativeJSON(t *testing.T) {
	batch := push.PushBatch{
		DeviceID:    "dev-1",
		CollectedAt: "2026-11-02 20:00:00.123",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceAddressID: "a1", DeviceAddressName: "speed",
				Value: "1450", DataType: "int32", Kind: "number", Quality: 192, PropertyCode: "speed"},
		},
	}
	msgs, err := New().Encode(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(msgs[0].Payload, &doc); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	wantFrame := []string{"collectedAt", "deviceId", "deviceCode", "deviceName", "protocol",
		"thingModelCode", "groupId", "groupCode", "groupName", "groupIntervalMs", "points"}
	if len(doc) != len(wantFrame) {
		t.Fatalf("frame keys = %v, want exactly %v", doc, wantFrame)
	}
	for _, k := range wantFrame {
		if _, ok := doc[k]; !ok {
			t.Errorf("frame missing key %q: %v", k, doc)
		}
	}
	var points []map[string]json.RawMessage
	if err := json.Unmarshal(doc["points"], &points); err != nil {
		t.Fatalf("unmarshal points: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	want := []string{"deviceAddressId", "deviceAddressName", "value", "dataType", "kind", "quality", "propertyCode"}
	if len(points[0]) != len(want) {
		t.Fatalf("point keys = %v, want exactly %v", points[0], want)
	}
	for _, k := range want {
		if _, ok := points[0][k]; !ok {
			t.Errorf("point missing key %q: %v", k, points[0])
		}
	}
}

// 断网补发重建的批次可能只有记录带设备编码：帧头须与 spool.ToPushBatch 同口径回退。
func TestEncodeHeaderFallsBackToRecordDeviceFields(t *testing.T) {
	batch := push.PushBatch{
		DeviceID: "dev-1",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceCode: "dev-code-1", ThingModelCode: "th_sensor",
				GroupCode: "fast", Protocol: "S7", DeviceAddressID: "a1", Value: "1", Quality: 192},
		},
	}
	msgs, err := New().Encode(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var frame Frame
	if err := json.Unmarshal(msgs[0].Payload, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if frame.DeviceCode != "dev-code-1" || frame.ThingModelCode != "th_sensor" ||
		frame.Protocol != "S7" || frame.GroupCode != "fast" {
		t.Errorf("frame header = %+v, want fields from record", frame)
	}
}

func TestEncodeIncludesTriggerMetadata(t *testing.T) {
	trigger := &collector.RecordTrigger{
		Source: "trigger", RuleID: "r1", RuleCode: "batch-done", RuleName: "批次完成",
		TriggerID: "t1", FiredAt: "2026-11-04 10:00:00.000",
	}
	batch := push.PushBatch{
		DeviceID:    "dev-1",
		CollectedAt: "2026-11-04 10:00:01.000",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceAddressID: "a1", DeviceAddressName: "temperature",
				Value: "23.5", Quality: 192, Trigger: trigger},
			{DeviceID: "dev-1", DeviceAddressID: "a2", DeviceAddressName: "speed",
				Value: "1450", Quality: 192, Trigger: trigger},
		},
	}
	msgs, err := New().Encode(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var frame Frame
	if err := json.Unmarshal(msgs[0].Payload, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if frame.Trigger == nil || frame.Trigger.RuleID != "r1" || frame.Trigger.RuleCode != "batch-done" ||
		frame.Trigger.TriggerID != "t1" || frame.Trigger.FiredAt != "2026-11-04 10:00:00.000" {
		t.Fatalf("trigger = %+v", frame.Trigger)
	}
}

func TestEncodeEmptyBatchProducesNothing(t *testing.T) {
	msgs, err := New().Encode(push.PushBatch{DeviceID: "dev-1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %d, want 0", len(msgs))
	}
}

func TestEncodeUngroupedFrame(t *testing.T) {
	batch := push.PushBatch{
		DeviceID:    "dev-1",
		CollectedAt: "2026-11-02 20:00:00.123",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceAddressID: "a1", DeviceAddressName: "speed", Value: "1450", Quality: 192},
		},
	}
	msgs, err := New().Encode(batch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var frame Frame
	if err := json.Unmarshal(msgs[0].Payload, &frame); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if frame.GroupID != "" || frame.GroupCode != "" || frame.GroupName != "" || frame.GroupIntervalMs != 0 {
		t.Errorf("ungrouped header = %+v, want empty group", frame)
	}
	if len(frame.Points) != 1 || frame.Points[0].DeviceAddressName != "speed" {
		t.Errorf("points = %+v, want one ungrouped point", frame.Points)
	}
}
