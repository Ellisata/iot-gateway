// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// newThresholdEngine 建一个连续命中 3 轮才报警的引擎（与生产默认一致），
// 记录节流也沿用生产默认（1 分钟）：同一用例内连续上报必然落在同一节流窗内，
// 因此"同段内只写一条"的用例直接用这个即可；要精确控制段边界的用例见 newThrottledEngine。
func newThresholdEngine(t *testing.T) (*ThresholdEngine, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	return NewThresholdEngine(db, nil), db
}

// newThrottledEngine 建一个记录节流可控的引擎，便于断言"同段内只写一条"。
func newThrottledEngine(t *testing.T, minInterval time.Duration) (*ThresholdEngine, *gorm.DB) {
	t.Helper()
	db := newTestDB(t)
	e := NewThresholdEngine(db, nil)
	e.minWriteInterval = minInterval
	return e, db
}

// thr 构造一个启用的报警条件。
func thr(op string, values ...string) collector.PointThreshold {
	return collector.PointThreshold{Enabled: true, Op: op, Values: values}
}

// report 以指定 Kind 上报一个值（默认点位 dev-1 / pt-1）。
func report(e *ThresholdEngine, value, kind string, t collector.PointThreshold) {
	e.ReportPointValue("dev-1", "1#炉", "pt-1", "温度", value, kind, driver.QualityGood, t)
}

// reportFloat 数值类型的简写。
func reportFloat(e *ThresholdEngine, value string, t collector.PointThreshold) {
	report(e, value, driver.KindFloat, t)
}

// thresholdAlarm 取某点位的唯一一条条件报警行。
func thresholdAlarm(t *testing.T, db *gorm.DB, pointID, alarmType string) po.Alarm {
	t.Helper()
	var a po.Alarm
	if err := db.Where("target_id = ? AND target_type = ? AND alarm_type = ?",
		pointID, TypePointThreshold, alarmType).First(&a).Error; err != nil {
		t.Fatalf("load %s threshold alarm for %s: %v", alarmType, pointID, err)
	}
	return a
}

func countThresholdRecords(t *testing.T, db *gorm.DB, addressID string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&po.PointThresholdRecord{}).Where("address_id = ?", addressID).Count(&n).Error; err != nil {
		t.Fatalf("count threshold records: %v", err)
	}
	return n
}

// ==================== 数值：区间 ====================

// TestThresholdRangeBreach 区间双向越限都能命中，文案区分是哪一侧 ——
// 值班人员据此判断是过载还是失温。
func TestThresholdRangeBreach(t *testing.T) {
	t.Run("超上限", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			reportFloat(e, "45.2", thr(driver.OpRange, "10", "40"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "告警") || !strings.Contains(a.Content, "45.2 > 上限 40") {
			t.Fatalf("文案应含「告警」与「45.2 > 上限 40」，实际 %q", a.Content)
		}
		if a.TargetType != TypePointThreshold || a.DeviceID != "dev-1" {
			t.Fatalf("target_type/device_id 不对：%+v", a)
		}
	})
	t.Run("低于下限", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			reportFloat(e, "3.5", thr(driver.OpRange, "10", "40"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "3.5 < 下限 10") {
			t.Fatalf("文案应含「3.5 < 下限 10」，实际 %q", a.Content)
		}
	})
	t.Run("区间内不报", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 10; i++ {
			reportFloat(e, "25", thr(driver.OpRange, "10", "40"))
		}
		if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
			t.Fatalf("区间内不得报警，实际 %d 条", n)
		}
	})
	t.Run("单侧留空只查一侧", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		// 上限空 → 只查下限
		for i := 0; i < 10; i++ {
			reportFloat(e, "999", thr(driver.OpRange, "10", ""))
		}
		if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
			t.Fatalf("上限留空时高位不该报，实际 %d 条", n)
		}
	})
}

// TestThresholdNumericComparisons 单比较算子逐个验：命中方向与文案都对。
func TestThresholdNumericComparisons(t *testing.T) {
	cases := []struct {
		op        string
		operand   string
		breachVal string
		okVal     string
		wantText  string
	}{
		{driver.OpGt, "40", "45.2", "39", "45.2 > 40"},
		{driver.OpGe, "40", "40", "39.9", "40 >= 40"},
		{driver.OpLt, "10", "9.5", "10", "9.5 < 10"},
		{driver.OpLe, "10", "10", "10.1", "10 <= 10"},
		{driver.OpEq, "40", "40", "41", "40 = 40"},
		{driver.OpNe, "40", "41", "40", "41 != 40"},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			e, db := newThresholdEngine(t)
			for i := 0; i < 3; i++ {
				reportFloat(e, c.breachVal, thr(c.op, c.operand))
			}
			a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
			if !strings.Contains(a.Content, c.wantText) {
				t.Fatalf("文案应含 %q，实际 %q", c.wantText, a.Content)
			}
		})
		t.Run(c.op+"_不命中", func(t *testing.T) {
			e, db := newThresholdEngine(t)
			for i := 0; i < 10; i++ {
				reportFloat(e, c.okVal, thr(c.op, c.operand))
			}
			if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
				t.Fatalf("%s 在不该命中时报警了，实际 %d 条", c.op, n)
			}
		})
	}
}

// ==================== 布尔：状态 ====================

// TestThresholdBoolEq 布尔位「为 1 报警 / 为 0 报警」都能配。
//
// 布尔值的 "0"/"1" 是各插件 formatBool 的**实现约定、不是契约**，
// 所以这里同时钉住 true/false 也被识别。
func TestThresholdBoolEq(t *testing.T) {
	t.Run("为1报警_值1命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "1", driver.KindBool, thr(driver.OpEq, "1"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "值 = 1") {
			t.Fatalf("文案应含「值 = 1」，实际 %q", a.Content)
		}
	})
	t.Run("为1报警_值0不报", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 10; i++ {
			report(e, "0", driver.KindBool, thr(driver.OpEq, "1"))
		}
		if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
			t.Fatalf("值为 0 时不该报警，实际 %d 条", n)
		}
	})
	t.Run("为0报警_值0命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "0", driver.KindBool, thr(driver.OpEq, "0"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "值 = 0") {
			t.Fatalf("文案应含「值 = 0」，实际 %q", a.Content)
		}
	})
	t.Run("布尔ne", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "0", driver.KindBool, thr(driver.OpNe, "1"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "值 != 1") {
			t.Fatalf("文案应含「值 != 1」，实际 %q", a.Content)
		}
	})
}

// TestThresholdBoolAcceptsTrueFalse 插件未必都用 "0"/"1" —— formatBool 是各驱动的
// 实现约定而非跨插件契约，所以 true/false 也必须认。认不出的值则保持中性（见下个用例）。
func TestThresholdBoolAcceptsTrueFalse(t *testing.T) {
	cases := []struct{ value, operand string }{
		{"true", "true"},
		{"TRUE", "1"},
		{"False", "false"},
		{"false", "0"},
	}
	for _, c := range cases {
		t.Run(c.value+"_"+c.operand, func(t *testing.T) {
			e, db := newThresholdEngine(t)
			for i := 0; i < 3; i++ {
				report(e, c.value, driver.KindBool, thr(driver.OpEq, c.operand))
			}
			if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
				t.Fatalf("%s == %s 应命中，实际 %d 条", c.value, c.operand, n)
			}
		})
	}
}

// TestThresholdBoolUnrecognizedValueIsNeutral 认不出的布尔值是**中性**：
// 既不报命中、也不"顺手恢复"已有报警。绝不猜 —— 猜错等于给运维一个假的状态。
func TestThresholdBoolUnrecognizedValueIsNeutral(t *testing.T) {
	e, db := newThresholdEngine(t)
	// 先制造一条活跃报警
	for i := 0; i < 3; i++ {
		report(e, "1", driver.KindBool, thr(driver.OpEq, "1"))
	}
	if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
		t.Fatalf("前置条件失败：期望 1 条 active，实际 %d", n)
	}

	for i := 0; i < 5; i++ {
		report(e, "on", driver.KindBool, thr(driver.OpEq, "1"))
		report(e, "", driver.KindBool, thr(driver.OpEq, "1"))
	}
	if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
		t.Fatalf("中性信号不应改动状态机，期望仍有 1 条 active，实际 %d", n)
	}
	if n := countAlarms(t, db, "pt-1", TypeRecover, ""); n != 0 {
		t.Fatalf("中性信号不应写恢复记录，实际 %d 条", n)
	}
}

// ==================== 文本：等于 / 不等于 / 属于集合 ====================

func TestThresholdStringConditions(t *testing.T) {
	t.Run("等于命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "FAULT", driver.KindString, thr(driver.OpEq, "FAULT"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "值 = FAULT") {
			t.Fatalf("文案应含「值 = FAULT」，实际 %q", a.Content)
		}
	})
	t.Run("等于不命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 10; i++ {
			report(e, "RUNNING", driver.KindString, thr(driver.OpEq, "FAULT"))
		}
		if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
			t.Fatalf("不该报警，实际 %d 条", n)
		}
	})
	t.Run("不等于命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "RUNNING", driver.KindString, thr(driver.OpNe, "FAULT"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "值 != FAULT") {
			t.Fatalf("文案应含「值 != FAULT」，实际 %q", a.Content)
		}
	})
	t.Run("属于集合命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "OVERLOAD", driver.KindString, thr(driver.OpIn, "FAULT", "OVERLOAD"))
		}
		a := thresholdAlarm(t, db, "pt-1", TypeThreshold)
		if !strings.Contains(a.Content, "值 属于 {FAULT, OVERLOAD}") {
			t.Fatalf("文案应含集合，实际 %q", a.Content)
		}
	})
	t.Run("属于集合不命中", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 10; i++ {
			report(e, "RUNNING", driver.KindString, thr(driver.OpIn, "FAULT", "OVERLOAD"))
		}
		if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
			t.Fatalf("集合外的值不该报警，实际 %d 条", n)
		}
	})
}

// TestThresholdStringComparisonIsTrimmedAndCaseSensitive 与文本语义有关的两个约定：
// 首尾空白不算差异（设备常带填充），但**大小写敏感**（故障码大小写有别，
// 把 FAULT 与 fault 当同一个会漏掉真实故障）。
func TestThresholdStringComparisonIsTrimmedAndCaseSensitive(t *testing.T) {
	t.Run("首尾空白被忽略", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 3; i++ {
			report(e, "  FAULT  ", driver.KindString, thr(driver.OpEq, "FAULT"))
		}
		if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
			t.Fatalf("trim 后应命中，实际 %d 条", n)
		}
	})
	t.Run("大小写敏感", func(t *testing.T) {
		e, db := newThresholdEngine(t)
		for i := 0; i < 10; i++ {
			report(e, "fault", driver.KindString, thr(driver.OpEq, "FAULT"))
		}
		if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
			t.Fatalf("大小写不同不该命中，实际 %d 条", n)
		}
	})
}

// ==================== 中性：类型/值/操作数认不出来 ====================

// TestThresholdIgnoresUnsupportedKind 时间类型与未知类型不参与判定。
//
// 时间值比较几乎总是想表达"多久没更新了"（比的是 now，不是配置边界），
// 是另一个功能；给它一个语义可疑的开关不如不给。未知 Kind 同理：不猜。
func TestThresholdIgnoresUnsupportedKind(t *testing.T) {
	kinds := []string{driver.KindTime, "", "unknown"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			e, db := newThresholdEngine(t)
			for i := 0; i < 10; i++ {
				report(e, "2026-10-07T09:30:00Z", kind, thr(driver.OpEq, "2026-10-07T09:30:00Z"))
			}
			if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
				t.Fatalf("类型 %q 不应参与判定，实际 %d 条报警", kind, n)
			}
		})
	}
}

// TestThresholdNeutralOnBadQualityAndUnparseable 质量不可用 / 值解析不出来 / 操作数
// 与类型不匹配都是**中性**：既不报命中、也不"顺手恢复"已有报警（同 alarm.PointEngine）。
//
// **四种坏码逐条钉住**，而不是只试探性地写一个 quality=0：质量码并入成因之后，
// 坏点位不再恒为 0 —— 守「读失败就不判定」的那道闸必须写成 driver.QualityUsable，
// 写成 `quality == 0` 会让地址级/通信级/解析级的失败带着一个占位值直奔
// evaluateCondition。这里刻意给每一种坏码都配一个**像样的值**（"45.2"），
// 因为真实驱动对坏点位给的正是空串，而空串会被 evaluateCondition 挡下 ——
// 那道二次防线会让写错的守卫看起来是对的。
func TestThresholdNeutralOnBadQualityAndUnparseable(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		kind     string
		op       string
		operands []string
		quality  int
	}{
		{"读失败（坏：未归类）", "45.2", driver.KindFloat, driver.OpGt, []string{"40"}, driver.QualityBad},
		{"读失败（坏：地址级）", "45.2", driver.KindFloat, driver.OpGt, []string{"40"}, driver.QualityBadAddress},
		{"读失败（坏：通信级）", "45.2", driver.KindFloat, driver.OpGt, []string{"40"}, driver.QualityBadComms},
		{"读失败（坏：解析级）", "45.2", driver.KindFloat, driver.OpGt, []string{"40"}, driver.QualityBadParse},
		{"数值为空", "", driver.KindFloat, driver.OpGt, []string{"40"}, driver.QualityGood},
		{"数值非数字", "abc", driver.KindFloat, driver.OpGt, []string{"40"}, driver.QualityGood},
		{"操作数非数字", "45.2", driver.KindFloat, driver.OpGt, []string{"abc"}, driver.QualityGood},
		{"算子与类型不匹配", "45.2", driver.KindFloat, driver.OpIn, []string{"40"}, driver.QualityGood},
		{"文本用数值算子", "FAULT", driver.KindString, driver.OpGt, []string{"40"}, driver.QualityGood},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, db := newThresholdEngine(t)
			// 先制造一条活跃报警（用合法的数值条件）
			for i := 0; i < 3; i++ {
				reportFloat(e, "45.2", thr(driver.OpGt, "40"))
			}
			if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
				t.Fatalf("前置条件失败：期望 1 条 active，实际 %d", n)
			}

			for i := 0; i < 5; i++ {
				e.ReportPointValue("dev-1", "1#炉", "pt-1", "温度", c.value, c.kind, c.quality,
					collector.PointThreshold{Enabled: true, Op: c.op, Values: c.operands})
			}
			if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
				t.Fatalf("中性信号不应改动状态机，期望仍有 1 条 active，实际 %d", n)
			}
			if n := countAlarms(t, db, "pt-1", TypeRecover, ""); n != 0 {
				t.Fatalf("中性信号不应写恢复记录，实际 %d 条", n)
			}
		})
	}
}

// TestThresholdInertConfigIsNeutral 配置为空（未启用 / 无算子 / 无操作数）不产生任何信号。
func TestThresholdInertConfigIsNeutral(t *testing.T) {
	configs := map[string]collector.PointThreshold{
		"未启用":  {Enabled: false, Op: driver.OpGt, Values: []string{"40"}},
		"无算子":  {Enabled: true, Values: []string{"40"}},
		"无操作数": {Enabled: true, Op: driver.OpGt},
	}
	for name, th := range configs {
		t.Run(name, func(t *testing.T) {
			e, db := newThresholdEngine(t)
			for i := 0; i < 10; i++ {
				reportFloat(e, "45.2", th)
			}
			if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
				t.Fatalf("空配置不得产生任何信号，实际 %d 条报警", n)
			}
		})
	}
}

// TestThresholdTreatsUncertainQualityAsUsable 质量 128（ABB 的"不确定"）值仍可用，
// 照常参与判定 —— 与 alarm.PointEngine 对 128 的处理一致。
func TestThresholdTreatsUncertainQualityAsUsable(t *testing.T) {
	e, db := newThresholdEngine(t)
	for i := 0; i < 3; i++ {
		e.ReportPointValue("dev-1", "1#炉", "pt-1", "温度", "45.2", driver.KindFloat, driver.QualityUncertain, thr(driver.OpGt, "40"))
	}
	if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
		t.Fatalf("128 的值可用，命中应报警，实际 %d 条", n)
	}
}

// ==================== 状态机语义（沿用原有契约） ====================

// TestThresholdDebounceAndRecover 连续命中才报、一次不命中即恢复、持续命中不重复报。
func TestThresholdDebounceAndRecover(t *testing.T) {
	e, db := newThresholdEngine(t)
	cond := thr(driver.OpGt, "40")

	for i := 0; i < 2; i++ {
		reportFloat(e, "45.2", cond)
	}
	if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
		t.Fatalf("连续命中未达阈值前不应报警，实际 %d 条", n)
	}

	reportFloat(e, "45.2", cond)
	if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 1 {
		t.Fatalf("第 3 轮应报警，实际 %d 条", n)
	}

	// 持续命中不重复报警
	for i := 0; i < 5; i++ {
		reportFloat(e, "45.2", cond)
	}
	if n := countAlarms(t, db, "pt-1", "", ""); n != 1 {
		t.Fatalf("同一目标同时最多一条 active，实际 %d 条", n)
	}

	// 一轮不命中即恢复
	reportFloat(e, "20", cond)
	if n := countAlarms(t, db, "pt-1", TypeThreshold, StatusActive); n != 0 {
		t.Fatalf("期望 active 已清，实际 %d", n)
	}
	rec := thresholdAlarm(t, db, "pt-1", TypeRecover)
	if rec.TargetType != TypePointThreshold || !strings.Contains(rec.Content, "恢复") {
		t.Fatalf("恢复记录不对：%+v", rec)
	}
}

// TestThresholdIsolatesPoints 同批点位互不连坐：只报命中的那个。
func TestThresholdIsolatesPoints(t *testing.T) {
	e, db := newThresholdEngine(t)
	cond := thr(driver.OpGt, "40")

	for i := 0; i < 3; i++ {
		e.ReportPointValue("dev-1", "1#炉", "pt-bad", "温度", "45.2", driver.KindFloat, driver.QualityGood, cond)
		e.ReportPointValue("dev-1", "1#炉", "pt-ok", "压力", "20", driver.KindFloat, driver.QualityGood, cond)
	}

	if n := countAlarms(t, db, "pt-bad", "", ""); n != 1 {
		t.Fatalf("命中点位期望 1 条报警，实际 %d", n)
	}
	if n := countAlarms(t, db, "pt-ok", "", ""); n != 0 {
		t.Fatalf("未命中点位不得报警，实际 %d", n)
	}
}

// TestThresholdIsolatesTrailingZero 点位名带后缀不会误并（状态机键是 addressID）。
func TestThresholdIsolatesTrailingZero(t *testing.T) {
	e, db := newThresholdEngine(t)
	for i := 0; i < 3; i++ {
		e.ReportPointValue("dev-1", "1#炉", "pt-1", "温度", "45.2", driver.KindFloat, driver.QualityGood, thr(driver.OpGt, "40"))
	}
	if n := countAlarms(t, db, "pt-10", "", ""); n != 0 {
		t.Fatalf("pt-10 不该受 pt-1 影响，实际 %d 条", n)
	}
}

// ==================== 越限记录 ====================

// TestThresholdPersistsBreachSample 命中即落一条记录，条件是**快照**（改配置后仍可自证）。
func TestThresholdPersistsBreachSample(t *testing.T) {
	e, db := newThresholdEngine(t)

	reportFloat(e, "45.2", thr(driver.OpRange, "10", "40"))

	var rec po.PointThresholdRecord
	if err := db.Where("address_id = ?", "pt-1").First(&rec).Error; err != nil {
		t.Fatalf("命中应落一条记录：%v", err)
	}
	if rec.Value != "45.2" || rec.Kind != driver.KindFloat || rec.Bound != boundHigh {
		t.Fatalf("记录字段失真：%+v", rec)
	}
	if rec.ThresholdOp != driver.OpRange || rec.ThresholdValue != `["10","40"]` {
		t.Fatalf("条件快照不对：op=%q value=%q", rec.ThresholdOp, rec.ThresholdValue)
	}
	if rec.DeviceID != "dev-1" || rec.DeviceName != "1#炉" || rec.AddressName != "温度" {
		t.Fatalf("记录应带设备与点位名：%+v", rec)
	}
	if rec.OccurredAt == "" {
		t.Fatalf("应有发生时间：%+v", rec)
	}
}

// TestThresholdRecordSnapshotsTextCondition 文本条件的快照同样落库。
func TestThresholdRecordSnapshotsTextCondition(t *testing.T) {
	e, db := newThresholdEngine(t)

	report(e, "FAULT", driver.KindString, thr(driver.OpIn, "FAULT", "OVERLOAD"))

	var rec po.PointThresholdRecord
	if err := db.Where("address_id = ?", "pt-1").First(&rec).Error; err != nil {
		t.Fatalf("load record: %v", err)
	}
	if rec.ThresholdOp != driver.OpIn || rec.ThresholdValue != `["FAULT","OVERLOAD"]` {
		t.Fatalf("文本条件快照不对：op=%q value=%q", rec.ThresholdOp, rec.ThresholdValue)
	}
	if rec.Bound != driver.OpIn {
		t.Fatalf("bound 应为算子名本身，实际 %q", rec.Bound)
	}
}

// TestThresholdRecordThrottledWithinEpisode 同一连续命中段内，首条立即写、其后按间隔节流。
func TestThresholdRecordThrottledWithinEpisode(t *testing.T) {
	e, db := newThrottledEngine(t, time.Hour) // 段内不可能再写第二条

	for i := 0; i < 5; i++ {
		reportFloat(e, "45.2", thr(driver.OpGt, "40"))
	}

	if n := countThresholdRecords(t, db, "pt-1"); n != 1 {
		t.Fatalf("同段内应只写首条，实际 %d 条", n)
	}
}

// TestThresholdRecordGateResetsAfterNormal 段结束后，下一段的首次命中**立即**再写一条。
//
// 守的是"短促越限也留证"：若实现改成"节流窗内一律跳过"，一个
// 命中→正常→再命中的现场就只剩第一条记录，第二次在历史里完全消失。
func TestThresholdRecordGateResetsAfterNormal(t *testing.T) {
	e, db := newThrottledEngine(t, time.Hour)
	cond := thr(driver.OpGt, "40")

	reportFloat(e, "45.2", cond) // 第 1 段：写
	reportFloat(e, "20", cond)   // 回到正常：段结束
	reportFloat(e, "45.2", cond) // 第 2 段：应再写

	if n := countThresholdRecords(t, db, "pt-1"); n != 2 {
		t.Fatalf("两段各应写一条，实际 %d 条", n)
	}
}

// TestThresholdRecordCleanupByAge 超过保留时长的记录在惰性清理时被删掉。
func TestThresholdRecordCleanupByAge(t *testing.T) {
	e, db := newThresholdEngine(t)

	old := time.Now().Add(-thresholdRecordRetention - time.Hour).Format("2006-01-02 15:04:05")
	if err := db.Create(&po.PointThresholdRecord{
		ID: "old-1", DeviceID: "dev-1", DeviceName: "1#炉", AddressID: "pt-1",
		AddressName: "温度", Value: "45.2", Bound: boundHigh, OccurredAt: old,
	}).Error; err != nil {
		t.Fatalf("seed old record: %v", err)
	}

	// 触发一条新命中 → 顺带清理（lastCleanup 为零值，必然执行）
	reportFloat(e, "45.2", thr(driver.OpGt, "40"))

	var n int64
	if err := db.Model(&po.PointThresholdRecord{}).Where("id = ?", "old-1").Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("超龄记录应被清理，实际仍在")
	}
}

// TestThresholdRecordCleanupByRowCap 行数超过上限时删最旧的超额行。
func TestThresholdRecordCleanupByRowCap(t *testing.T) {
	e, db := newThresholdEngine(t)

	// 造出比上限多一行的记录。occurred_at 都在保留期内（不会被按时长的清理删掉），
	// 逐个递增保证"最旧"的判定有确定顺序。批量插入：2 万次单条 Create 在
	// MaxOpenConns=1 下要几十秒，而这里只想验行数上限。
	base := time.Now().Add(-time.Hour)
	seeds := make([]po.PointThresholdRecord, 0, thresholdRecordMaxRows+1)
	for i := 0; i <= thresholdRecordMaxRows; i++ {
		seeds = append(seeds, po.PointThresholdRecord{
			ID: "seed-" + strconv.Itoa(i), DeviceID: "dev-1", DeviceName: "1#炉",
			AddressID: "pt-1", AddressName: "温度", Value: "45.2", Bound: boundHigh,
			OccurredAt: base.Add(time.Duration(i) * time.Second).Format("2006-01-02 15:04:05"),
		})
	}
	if err := db.CreateInBatches(seeds, 500).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}

	reportFloat(e, "45.2", thr(driver.OpGt, "40")) // 触发清理

	var total int64
	if err := db.Model(&po.PointThresholdRecord{}).Count(&total).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if total > thresholdRecordMaxRows+1 { // 清理后加上本次写入的那条
		t.Fatalf("应被裁到上限附近，实际 %d 条", total)
	}
}

// TestThresholdBadQualityNeverRaises 坏码点位**绝不参与阈值判定**，哪怕带着一个
// 看起来会越限的值。
//
// 这条专门钉住 quality.go 里那道闸本身，而不是它的副产品：真实驱动对坏点位给的
// 是空串，而空串会被 evaluateCondition 当"解析不出来"挡下（ok=false → 中性）——
// 那道二次防线会让写错的守卫看起来完全正常。所以这里刻意喂一个合法的越限值
// （"45.2" > 40），让唯一的拦截点只剩那道守卫。
//
// 四种坏码逐条试：质量码并入成因之后坏点位不再恒为 0，把守卫写成 `quality == 0`
// 会让地址级/通信级/解析级的失败一路走到判定里，坏点位于是凭一个占位值报警。
func TestThresholdBadQualityNeverRaises(t *testing.T) {
	cases := []struct {
		name    string
		quality int
	}{
		{"未归类", driver.QualityBad},
		{"地址级", driver.QualityBadAddress},
		{"通信级", driver.QualityBadComms},
		{"解析级", driver.QualityBadParse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, db := newThresholdEngine(t)
			// 5 轮远超声明的阈值 3：若被计入判定，第 3 轮就该报出来
			for i := 0; i < 5; i++ {
				e.ReportPointValue("dev-1", "1#炉", "pt-1", "温度", "45.2", driver.KindFloat,
					c.quality, thr(driver.OpGt, "40"))
			}
			if n := countAlarms(t, db, "pt-1", "", ""); n != 0 {
				t.Fatalf("坏码点位不得触发阈值报警，实际 %d 条", n)
			}
		})
	}
}
