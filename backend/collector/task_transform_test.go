// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/po"
)

const scalePointJSON = `[{"op":"scale","factor":0.1}]`

func mustCompileTransform(t *testing.T, raw string) compiledTransform {
	t.Helper()
	ct, err := compileAddressTransform(raw)
	if err != nil {
		t.Fatalf("compile transform: %v", err)
	}
	return ct
}

func TestToRecordsAppliesTransformWhenAligned(t *testing.T) {
	ct := mustCompileTransform(t, scalePointJSON)
	task := &gatewayTask{addrTransforms: map[string]compiledTransform{"addr-1": ct}}
	dev := &po.Device{ID: "dev-1", Name: "1#炉"}
	addrs := []po.DeviceAddress{{
		ID: "addr-1", DeviceID: "dev-1", Name: "40001", Label: "温度",
		TransformJSON: scalePointJSON,
	}}
	results := []driver.ReadResult{{
		DeviceAddressID: "addr-1", Value: "253", DataType: "int16",
		Kind: driver.KindInt, Quality: driver.QualityGood,
	}}

	recs := task.toRecords(dev, addrs, results)
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	if recs[0].Value != "25.3" {
		t.Fatalf("value = %q, want 25.3", recs[0].Value)
	}
	if recs[0].Kind != driver.KindFloat {
		t.Fatalf("kind = %q, want %q", recs[0].Kind, driver.KindFloat)
	}
	if recs[0].DataType != "int16" {
		t.Fatalf("dataType 不应被变换改写，实际 %q", recs[0].DataType)
	}
}

func TestToRecordsAppliesTransformWhenMisaligned(t *testing.T) {
	ct := mustCompileTransform(t, scalePointJSON)
	task := &gatewayTask{
		addrTransforms: map[string]compiledTransform{"addr-1": ct},
		addrNameMap:    map[string]map[string]string{"dev-1": {"addr-1": "40001"}},
		addrLabelMap:   map[string]map[string]string{"dev-1": {"addr-1": "温度"}},
	}
	dev := &po.Device{ID: "dev-1", Name: "1#炉"}
	// addrs 与 results 顺序错位：走回退查询分支，变换仍必须按记录 ID 命中。
	addrs := []po.DeviceAddress{{ID: "other", DeviceID: "dev-1", Name: "x"}}
	results := []driver.ReadResult{{
		DeviceAddressID: "addr-1", Value: "253", DataType: "int16",
		Kind: driver.KindInt, Quality: driver.QualityGood,
	}}

	recs := task.toRecords(dev, addrs, results)
	if recs[0].Value != "25.3" || recs[0].Kind != driver.KindFloat {
		t.Fatalf("回退分支未应用变换：value=%q kind=%q", recs[0].Value, recs[0].Kind)
	}
}

func TestToRecordsLeavesUnconfiguredPointRaw(t *testing.T) {
	task := &gatewayTask{}
	dev := &po.Device{ID: "dev-1", Name: "1#炉"}
	addrs := []po.DeviceAddress{{ID: "addr-1", DeviceID: "dev-1", Name: "40001"}}
	results := []driver.ReadResult{{
		DeviceAddressID: "addr-1", Value: "253", DataType: "int16",
		Kind: driver.KindInt, Quality: driver.QualityGood,
	}}

	recs := task.toRecords(dev, addrs, results)
	if recs[0].Value != "253" || recs[0].Kind != driver.KindInt {
		t.Fatalf("未配置变换的点位应保持原值：value=%q kind=%q", recs[0].Value, recs[0].Kind)
	}
}

type capturedValueSink struct {
	value string
	kind  string
}

func (c *capturedValueSink) ReportPointValue(_ string, _ string, _ string, _ string,
	value, kind string, _ int, _ PointThreshold) {
	c.value, c.kind = value, kind
}

// 阈值报警在 toRecords 之后消费记录，因此必须看到工程值 —— 否则运维按工程值
// 配的阈值会和原始寄存器值比较，报警永不触发或误触发。
func TestReportPointValuesUsesTransformedValue(t *testing.T) {
	ct := mustCompileTransform(t, scalePointJSON)
	task := &gatewayTask{
		addrTransforms: map[string]compiledTransform{"addr-1": ct},
		pointValueSink: &capturedValueSink{},
	}
	dev := &po.Device{ID: "dev-1", Name: "1#炉"}
	addrs := []po.DeviceAddress{{
		ID: "addr-1", DeviceID: "dev-1", Name: "40001", Label: "温度",
		TransformJSON:    scalePointJSON,
		ThresholdEnabled: 1, ThresholdOp: driver.OpGt, ThresholdValue: `["10"]`,
	}}
	results := []driver.ReadResult{{
		DeviceAddressID: "addr-1", Value: "253", DataType: "int16",
		Kind: driver.KindInt, Quality: driver.QualityGood,
	}}

	recs := task.toRecords(dev, addrs, results)
	task.reportPointValues(dev, addrs, recs)

	sink := task.pointValueSink.(*capturedValueSink)
	if sink.value != "25.3" || sink.kind != driver.KindFloat {
		t.Fatalf("阈值报警应收到工程值：value=%q kind=%q", sink.value, sink.kind)
	}
}
