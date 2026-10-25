// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/model/po"
)

// newPointEngine 建一个连续失败 3 次判异常的引擎（与生产默认一致）。
func newPointEngine(t *testing.T) (*PointEngine, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	return NewPointEngine(db, nil), db
}

// pointAlarm 取某点位的唯一一条报警行。
func pointAlarm(t *testing.T, db *gorm.DB, pointID, alarmType string) po.Alarm {
	t.Helper()
	var a po.Alarm
	if err := db.Where("target_id = ? AND target_type = ? AND alarm_type = ?",
		pointID, TypePoint, alarmType).First(&a).Error; err != nil {
		t.Fatalf("load %s alarm for %s: %v", alarmType, pointID, err)
	}
	return a
}

// TestPointAlarmRaisesOnAddressQuality 地址级失败连续 3 轮 → 一条 active 质量报警，
// 且点位、设备、文案、质量码都对得上。
func TestPointAlarmRaisesOnAddressQuality(t *testing.T) {
	e, db := newPointEngine(t)

	for i := 0; i < 2; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadAddress)
	}
	if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
		t.Fatalf("阈值前不应报警，实际 %d 条", n)
	}

	e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadAddress)

	a := pointAlarm(t, db, "pt-1", TypeQuality)
	if a.Status != StatusActive {
		t.Fatalf("期望 active，实际 %s", a.Status)
	}
	if a.TargetType != TypePoint {
		t.Fatalf("期望 target_type=point，实际 %s", a.TargetType)
	}
	if a.TargetName != "1#炉.温度" {
		t.Fatalf("期望点位名 1#炉.温度，实际 %s", a.TargetName)
	}
	// device_id 是「删除/停用设备时清掉其点位报警」的唯一反查依据，必须落上
	if a.DeviceID != "dev-1" {
		t.Fatalf("期望 device_id=dev-1，实际 %q", a.DeviceID)
	}
	if !strings.Contains(a.Content, "地址无效") {
		t.Fatalf("文案应带「地址无效」，实际 %q", a.Content)
	}
	if a.Level != defaultLevel {
		t.Fatalf("期望级别 %s，实际 %s", defaultLevel, a.Level)
	}
}

// TestPointAlarmQualityText 解析级失败用「解析失败」，与地址级区分开 ——
// 前者要改点位的数据类型，后者要改地址，值班人员据此决定找谁。
func TestPointAlarmQualityText(t *testing.T) {
	e, db := newPointEngine(t)

	for i := 0; i < 3; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadParse)
	}

	a := pointAlarm(t, db, "pt-1", TypeQuality)
	if !strings.Contains(a.Content, "解析失败") {
		t.Fatalf("文案应带「解析失败」，实际 %q", a.Content)
	}
	if strings.Contains(a.Content, "地址无效") {
		t.Fatalf("不应出现另一个子码的文案，实际 %q", a.Content)
	}
}

// TestPointAlarmIgnoresNonAttributableQualities 是本特性的核心守卫。
//
// 只有「设备是好的、这个点位本身有问题」的质量码才算失败。comms（整段被拒，
// 瞬时且成组）、QualityBad（认不出的码、预算耗尽的延迟点位）、Uncertain
// （ABB 的"不确定"）都不构成证据 —— 反复上报也不得产生任何报警。
//
// **这是一条容易写错的边界**：最直觉的实现是 quality != QualityGood 就报，而预算
// 耗尽的延迟点位恒为 QualityBad，且每轮都在同一处被延迟 —— 那么写就会让每个
// 坏点恒挂一条假报警。这里逐类逐轮断言，把这个错误实现钉死。
func TestPointAlarmIgnoresNonAttributableQualities(t *testing.T) {
	cases := []struct {
		name    string
		quality int
	}{
		{"comms 整段被拒（瞬时，成组出现）", driver.QualityBadComms},
		{"未归类（预算耗尽的延迟点位、认不出的码）", driver.QualityBad},
		{"ABB 的不确定（值可用）", driver.QualityUncertain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, db := newPointEngine(t)
			// 10 轮远超声明的阈值 3：若被计入失败，第 3 轮就该报出来
			for i := 0; i < 10; i++ {
				e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", c.quality)
			}
			if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
				t.Fatalf("非点位自身问题的质量码不得报警，实际 %d 条", n)
			}
		})
	}
}

// TestPointAlarmNeutralDoesNotRecover 中性信号不得"顺手恢复"已有报警。
//
// 不调用状态机（而非调用 ok=true）才是正确的中性语义：一个已确认地址不存在的
// 点位，这一轮只是因为整段被拒而拿不到值，没有任何证据说明那个地址回来了。
// 若实现改成"非 address/parse 就报成功"，这里会多出一条 recover 记录。
func TestPointAlarmNeutralDoesNotRecover(t *testing.T) {
	e, db := newPointEngine(t)

	for i := 0; i < 3; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadAddress)
	}
	if n := countAlarms(t, db, "pt-1", TypeQuality, StatusActive); n != 1 {
		t.Fatalf("期望 1 条 active，实际 %d", n)
	}

	// 之后连续多轮整段被拒（comms）与未归类：报警必须原样挂着
	for i := 0; i < 5; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadComms)
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBad)
	}
	if n := countAlarms(t, db, "pt-1", TypeQuality, StatusActive); n != 1 {
		t.Fatalf("中性信号不应改动状态机，期望仍有 1 条 active，实际 %d", n)
	}
	if n := countAlarms(t, db, "pt-1", TypeRecover, ""); n != 0 {
		t.Fatalf("中性信号不应写恢复记录，实际 %d 条", n)
	}
}

// TestPointAlarmRecoversOnGoodValue 读到有效值即恢复：清 active、写一条恢复历史，
// 文案用「恢复采集」而不是断联那套说法。
func TestPointAlarmRecoversOnGoodValue(t *testing.T) {
	e, db := newPointEngine(t)

	for i := 0; i < 3; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadAddress)
	}
	e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityGood)

	if n := countAlarms(t, db, "pt-1", TypeQuality, StatusActive); n != 0 {
		t.Fatalf("期望 active 已清，实际 %d", n)
	}
	if n := countAlarms(t, db, "pt-1", TypeQuality, StatusCleared); n != 1 {
		t.Fatalf("期望 1 条已清的报警，实际 %d", n)
	}
	rec := pointAlarm(t, db, "pt-1", TypeRecover)
	if rec.Status != StatusCleared {
		t.Fatalf("恢复记录 status 恒为 cleared，实际 %s", rec.Status)
	}
	if !strings.Contains(rec.Content, "恢复采集") {
		t.Fatalf("恢复文案应为「恢复采集」，实际 %q", rec.Content)
	}
}

// TestPointAlarmIsolatesPoints 同批点位互不连坐：只报坏的那个。
func TestPointAlarmIsolatesPoints(t *testing.T) {
	e, db := newPointEngine(t)

	for i := 0; i < 3; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-bad", "温度", driver.QualityBadAddress)
		e.ReportPointQuality("dev-1", "1#炉", "pt-ok", "压力", driver.QualityGood)
	}

	if n := countAlarms(t, db, "pt-bad", "", ""); n != 1 {
		t.Fatalf("坏点位期望 1 条报警，实际 %d", n)
	}
	if n := countAlarms(t, db, "pt-ok", "", ""); n != 0 {
		t.Fatalf("好点位不得报警，实际 %d", n)
	}
}

// TestPointAlarmNoDuplicateWhileActive 已判异常后持续失败不重复报警（去抖 + 边沿）。
func TestPointAlarmNoDuplicateWhileActive(t *testing.T) {
	e, db := newPointEngine(t)

	for i := 0; i < 3; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadAddress)
	}
	for i := 0; i < 5; i++ {
		e.ReportPointQuality("dev-1", "1#炉", "pt-1", "温度", driver.QualityBadAddress)
	}

	if n := countAlarms(t, db, "pt-1", "", ""); n != 1 {
		t.Fatalf("同一目标同时最多一条 active，实际 %d 条", n)
	}
}
