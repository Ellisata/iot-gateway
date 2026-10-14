// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// pointReport 一次点位质量上报的原始参数快照。
type pointReport struct {
	deviceID   string
	deviceName string
	pointID    string
	pointName  string
	quality    int
}

// recordingPointSink 记录收到的每一次上报，不做任何判定。
type recordingPointSink struct {
	got []pointReport
}

func (r *recordingPointSink) ReportPointQuality(deviceID, deviceName, pointID, pointName string, quality int) {
	r.got = append(r.got, pointReport{deviceID, deviceName, pointID, pointName, quality})
}

func testDevice() *po.Device { return &po.Device{ID: "dev-1", Name: "1#炉"} }

// TestReportPointQualityPrefersLabel 点位名优先用中文说明，没有才退回协议地址 ——
// 报警文案是给人看的。
func TestReportPointQualityPrefersLabel(t *testing.T) {
	sink := &recordingPointSink{}
	task := &gatewayTask{pointSink: sink}
	dev := testDevice()

	task.reportPointQuality(dev, []CollectedRecord{
		{DeviceAddressID: "pt-1", DeviceAddressName: "%MW100", DeviceAddressLabel: "温度",
			Quality: driver.QualityBadAddress},
		{DeviceAddressID: "pt-2", DeviceAddressName: "%MW101",
			Quality: driver.QualityGood},
	})

	if len(sink.got) != 2 {
		t.Fatalf("期望 2 次上报，实际 %d", len(sink.got))
	}
	if got := sink.got[0]; got.pointName != "温度" || got.pointID != "pt-1" ||
		got.quality != driver.QualityBadAddress {
		t.Fatalf("带 label 的点位名应为中文说明，实际 %+v", got)
	}
	if got := sink.got[1]; got.pointName != "%MW101" || got.quality != driver.QualityGood {
		t.Fatalf("无 label 时应退回协议地址，实际 %+v", got)
	}
	if sink.got[0].deviceID != "dev-1" || sink.got[0].deviceName != "1#炉" {
		t.Fatalf("应带上所属设备，实际 %+v", sink.got[0])
	}
}

// TestReportPointQualityForwardsEveryRecord 采集侧只报事实，不做报警策略。
//
// 尤其要守住两点：
//   - **不得在采集侧过滤掉"看起来正常"的点位** —— 恢复信号就是 QualityGood，
//     滤掉它状态机就永远无法恢复，报警会永久挂在未恢复上；
//   - **不得把归因不明的点位就地丢掉** —— QualityBadComms / 未归类 是不是"故障"是
//     alarm 的策略（见 alarm.PointEngine），采集侧替它决定会让那套中性语义悄无声息地失效。
func TestReportPointQualityForwardsEveryRecord(t *testing.T) {
	sink := &recordingPointSink{}
	task := &gatewayTask{pointSink: sink}

	recs := []CollectedRecord{
		{DeviceAddressID: "pt-ok", DeviceAddressName: "A", Quality: driver.QualityGood},
		{DeviceAddressID: "pt-comms", DeviceAddressName: "B", Quality: driver.QualityBadComms},
		{DeviceAddressID: "pt-unknown", DeviceAddressName: "C", Quality: driver.QualityBad},
		{DeviceAddressID: "pt-sim", DeviceAddressName: "D", Quality: driver.QualityUncertain},
	}
	task.reportPointQuality(testDevice(), recs)

	if len(sink.got) != len(recs) {
		t.Fatalf("每条记录都应原样上报，期望 %d 次，实际 %d", len(recs), len(sink.got))
	}
	for i, got := range sink.got {
		if got.pointID != recs[i].DeviceAddressID || got.quality != recs[i].Quality {
			t.Fatalf("第 %d 条上报失真：%+v", i, got)
		}
	}
}

// TestReportPointQualityNilSinkIsNoop pointSink 未装配（裁剪部署/压测）时不得 panic。
func TestReportPointQualityNilSinkIsNoop(t *testing.T) {
	task := &gatewayTask{}
	task.reportPointQuality(testDevice(), []CollectedRecord{{DeviceAddressID: "pt-1", Quality: driver.QualityGood}})
}

// ==================== 点位阈值上报（reportPointValues） ====================

// pointValueReport 一次点位阈值上报的原始参数快照。
type pointValueReport struct {
	deviceID   string
	deviceName string
	pointID    string
	pointName  string
	value      string
	kind       string
	quality    int
	threshold  PointThreshold
}

// recordingPointValueSink 记录收到的每一次阈值上报，不做任何判定。
type recordingPointValueSink struct {
	got []pointValueReport
}

func (r *recordingPointValueSink) ReportPointValue(deviceID, deviceName, pointID, pointName, value, kind string, quality int, threshold PointThreshold) {
	r.got = append(r.got, pointValueReport{deviceID, deviceName, pointID, pointName, value, kind, quality, threshold})
}

// TestReportPointValuesSkipsInertConfig 未启用 / 无算子 / 无操作数的点位不上报。
//
// 这是采集侧唯一的一处过滤，且它不是报警策略："没配条件的点位没有任何状态需要维护"。
// 需要钉住的是**判定权仍在实现方**：采集侧不得因为"值看起来没命中"而滤掉它 ——
// 那样实现方永远收不到正常值，状态机就无法恢复（与 reportPointQuality 同一个坑）。
func TestReportPointValuesSkipsInertConfig(t *testing.T) {
	sink := &recordingPointValueSink{}
	task := &gatewayTask{pointValueSink: sink}
	dev := testDevice()

	addrs := []po.DeviceAddress{
		{ID: "pt-off", ThresholdEnabled: 0, ThresholdOp: driver.OpGt, ThresholdValue: `["40"]`},
		{ID: "pt-noop", ThresholdEnabled: 1, ThresholdValue: `["40"]`},
		{ID: "pt-novalue", ThresholdEnabled: 1, ThresholdOp: driver.OpGt},
		{ID: "pt-on", ThresholdEnabled: 1, ThresholdOp: driver.OpGt, ThresholdValue: `["40"]`},
	}
	recs := []CollectedRecord{
		{DeviceAddressID: "pt-off", Value: "999", Kind: driver.KindFloat, Quality: driver.QualityGood},
		{DeviceAddressID: "pt-noop", Value: "999", Kind: driver.KindFloat, Quality: driver.QualityGood},
		{DeviceAddressID: "pt-novalue", Value: "999", Kind: driver.KindFloat, Quality: driver.QualityGood},
		{DeviceAddressID: "pt-on", Value: "999", Kind: driver.KindFloat, Quality: driver.QualityGood},
	}
	task.reportPointValues(dev, addrs, recs)

	if len(sink.got) != 1 {
		t.Fatalf("只有配齐了条件的点位应上报，期望 1 次，实际 %d", len(sink.got))
	}
	if sink.got[0].pointID != "pt-on" {
		t.Fatalf("应为 pt-on，实际 %s", sink.got[0].pointID)
	}
}

// TestReportPointValuesForwardsFactsVerbatim 值/类型/质量/条件原样透传，点位名优先用中文说明。
func TestReportPointValuesForwardsFactsVerbatim(t *testing.T) {
	sink := &recordingPointValueSink{}
	task := &gatewayTask{pointValueSink: sink}

	addrs := []po.DeviceAddress{
		{ID: "pt-1", ThresholdEnabled: 1, ThresholdOp: driver.OpRange, ThresholdValue: `["10","40"]`},
		{ID: "pt-2", ThresholdEnabled: 1, ThresholdOp: driver.OpEq, ThresholdValue: `["1"]`},
	}
	recs := []CollectedRecord{
		{DeviceAddressID: "pt-1", DeviceAddressName: "%MW100", DeviceAddressLabel: "温度",
			Value: "45.2", Kind: driver.KindFloat, Quality: driver.QualityGood},
		{DeviceAddressID: "pt-2", DeviceAddressName: "%MW101",
			Value: "1", Kind: driver.KindBool, Quality: driver.QualityUncertain},
	}
	task.reportPointValues(testDevice(), addrs, recs)

	if len(sink.got) != 2 {
		t.Fatalf("期望 2 次上报，实际 %d", len(sink.got))
	}
	got := sink.got[0]
	if got.pointName != "温度" || got.value != "45.2" || got.kind != driver.KindFloat || got.quality != driver.QualityGood {
		t.Fatalf("带 label 时点位名应为中文说明、其余原样，实际 %+v", got)
	}
	// 操作数从 JSON 解析回切片后原样透传
	if got.threshold.Op != driver.OpRange || len(got.threshold.Values) != 2 ||
		got.threshold.Values[0] != "10" || got.threshold.Values[1] != "40" {
		t.Fatalf("条件应原样透传，实际 %+v", got.threshold)
	}
	if got.deviceID != "dev-1" || got.deviceName != "1#炉" {
		t.Fatalf("应带上所属设备，实际 %+v", got)
	}
	if second := sink.got[1]; second.pointName != "%MW101" || second.threshold.Op != driver.OpEq ||
		len(second.threshold.Values) != 1 || second.threshold.Values[0] != "1" {
		t.Fatalf("无 label 时应退回协议地址、条件正常解析，实际 %+v", second)
	}
}

// TestReportPointValuesMalformedJSONIsInert 库里的 threshold_value 不是合法 JSON 数组时，
// 该点位**不上报**（等价于未配置）而不是 panic 或猜一个条件 —— 坏配置不该让采集出错。
func TestReportPointValuesMalformedJSONIsInert(t *testing.T) {
	sink := &recordingPointValueSink{}
	task := &gatewayTask{pointValueSink: sink}

	addrs := []po.DeviceAddress{
		{ID: "pt-bad", ThresholdEnabled: 1, ThresholdOp: driver.OpGt, ThresholdValue: "40"}, // 裸值不是数组
	}
	recs := []CollectedRecord{
		{DeviceAddressID: "pt-bad", Value: "999", Kind: driver.KindFloat, Quality: driver.QualityGood},
	}
	task.reportPointValues(testDevice(), addrs, recs)

	if len(sink.got) != 0 {
		t.Fatalf("条件解析失败时不得上报，实际 %d 次", len(sink.got))
	}
}

// TestReportPointValuesIndexMisalignmentSkipped 索引未对齐（异常驱动）时放弃该条上报，不猜归属 ——
// 猜错归属会把 A 的条件判到 B 的值上，比漏报更糟。
func TestReportPointValuesIndexMisalignmentSkipped(t *testing.T) {
	sink := &recordingPointValueSink{}
	task := &gatewayTask{pointValueSink: sink}

	addrs := []po.DeviceAddress{{ID: "pt-1", ThresholdEnabled: 1, ThresholdOp: driver.OpGt, ThresholdValue: `["40"]`}}
	recs := []CollectedRecord{
		{DeviceAddressID: "pt-OTHER", Value: "99", Kind: driver.KindFloat, Quality: driver.QualityGood},
		{DeviceAddressID: "pt-2", Value: "99", Kind: driver.KindFloat, Quality: driver.QualityGood}, // 命中，无对应 addr
	}
	task.reportPointValues(testDevice(), addrs, recs)

	if len(sink.got) != 0 {
		t.Fatalf("索引未对齐时不得上报，实际 %d 次", len(sink.got))
	}
}

// TestReportPointValuesNilSinkIsNoop pointValueSink 未装配时不得 panic。
func TestReportPointValuesNilSinkIsNoop(t *testing.T) {
	task := &gatewayTask{}
	task.reportPointValues(testDevice(),
		[]po.DeviceAddress{{ID: "pt-1", ThresholdEnabled: 1, ThresholdOp: driver.OpGt, ThresholdValue: `["40"]`}},
		[]CollectedRecord{{DeviceAddressID: "pt-1", Value: "99", Kind: driver.KindFloat, Quality: driver.QualityGood}})
}
