// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package groupjson

import (
	"encoding/json"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

func TestEncodeFrameSplitsByQuality(t *testing.T) {
	batch := push.PushBatch{
		DeviceID:        "dev-1",
		GroupID:         "g-1",
		GroupName:       "fast",
		GroupIntervalMs: 500,
		CollectedAt:     "2026-11-02 20:00:00.123",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceName: "一号机", DeviceAddressID: "a1", DeviceAddressName: "temperature", Value: "23.5", Quality: 192},
			{DeviceID: "dev-1", DeviceName: "一号机", DeviceAddressID: "a2", DeviceAddressName: "pressure", Value: "", Quality: 1},
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
	if frame.Timestamp != batch.CollectedAt || frame.DeviceID != "dev-1" || frame.GroupID != "g-1" ||
		frame.GroupName != "fast" || frame.IntervalMs != 500 || frame.DeviceName != "一号机" {
		t.Errorf("frame header = %+v", frame)
	}
	if got := frame.Values["temperature"]; got != "23.5" {
		t.Errorf("values[temperature] = %q, want 23.5", got)
	}
	if _, ok := frame.Values["pressure"]; ok {
		t.Errorf("bad-quality point leaked into values: %v", frame.Values)
	}
	if got := frame.Errors["pressure"]; got != 1 {
		t.Errorf("errors[pressure] = %d, want 1", got)
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
	if frame.GroupID != "" || frame.GroupName != "" || frame.IntervalMs != 0 {
		t.Errorf("ungrouped header = %+v, want empty group", frame)
	}
	// 空字典必须序列化为 {} 而不是 null，下游不必做空值判空。
	if frame.Values == nil || frame.Errors == nil {
		t.Errorf("values/errors = %v/%v, want empty maps", frame.Values, frame.Errors)
	}
}
