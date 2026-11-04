// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/po"
)

func testRule() *compiledRule {
	return &compiledRule{
		id:     "r1",
		name:   "r1",
		status: 1,
		groups: []compiledGroup{{conditions: []compiledCondition{{
			addressID: "a1",
			op:        driver.OpGt,
			values:    []string{"10"},
			meta:      addressMeta{deviceID: "d1", deviceName: "dev", addrName: "温度", effectiveInterval: time.Second},
		}}}},
	}
}

func newStateTestEngine(rule *compiledRule, now time.Time) *Engine {
	return &Engine{
		running:       true,
		now:           func() time.Time { return now },
		rules:         map[string]*ruleState{rule.id: {rule: rule}},
		samples:       make(map[string]sample),
		ruleByAddress: map[string][]string{"a1": {rule.id}},
		conditionIDs:  map[string]struct{}{"a1": {}},
		jobs:          make(chan job, 8),
		drops:         make(chan job, 8),
	}
}

func TestRuleWarmUpAndRisingEdge(t *testing.T) {
	now := time.Now()
	e := newStateTestEngine(testRule(), now)
	st := e.rules["r1"]

	e.samples["a1"] = sample{value: "20", kind: driver.KindFloat, quality: driver.QualityGood, at: now}
	e.evaluateRuleLocked("r1", now)
	if !st.initialized || !st.lastMet {
		t.Fatalf("预热后状态 = initialized:%v lastMet:%v", st.initialized, st.lastMet)
	}
	if len(e.jobs) != 0 {
		t.Fatal("创建时已为真的条件不应触发（预热）")
	}

	e.samples["a1"] = sample{value: "5", kind: driver.KindFloat, quality: driver.QualityGood, at: now}
	e.evaluateRuleLocked("r1", now)
	if st.lastMet {
		t.Fatal("条件不成立时应复位 lastMet")
	}

	e.samples["a1"] = sample{value: "20", kind: driver.KindFloat, quality: driver.QualityGood, at: now}
	e.evaluateRuleLocked("r1", now)
	if len(e.jobs) != 1 || !st.inFlight {
		t.Fatalf("上升沿应触发一次：jobs=%d inFlight=%v", len(e.jobs), st.inFlight)
	}
	e.evaluateRuleLocked("r1", now)
	if len(e.jobs) != 1 {
		t.Fatal("条件持续为真不应重复触发")
	}
}

func TestRuleUnknownFreezesState(t *testing.T) {
	now := time.Now()
	e := newStateTestEngine(testRule(), now)
	st := e.rules["r1"]
	st.initialized = true
	st.lastMet = true

	// 质量坏：中性，不改变 lastMet。
	e.samples["a1"] = sample{value: "0", kind: driver.KindFloat, quality: driver.QualityBadComms, at: now}
	e.evaluateRuleLocked("r1", now)
	if !st.lastMet || len(e.jobs) != 0 {
		t.Fatal("质量坏应保持中性，不触发也不复位")
	}

	// 样本陈旧：超过 3×周期。
	e.samples["a1"] = sample{value: "20", kind: driver.KindFloat, quality: driver.QualityGood, at: now.Add(-4 * time.Second)}
	e.evaluateRuleLocked("r1", now)
	if !st.lastMet || len(e.jobs) != 0 {
		t.Fatal("陈旧样本应保持中性")
	}

	// 类型认不出：中性。
	e.samples["a1"] = sample{value: "20", kind: driver.KindTime, quality: driver.QualityGood, at: now}
	e.evaluateRuleLocked("r1", now)
	if !st.lastMet || len(e.jobs) != 0 {
		t.Fatal("无法解释的类型应保持中性")
	}
}

func TestRuleCooldownSuppressesEdge(t *testing.T) {
	now := time.Now()
	rule := testRule()
	rule.cooldown = 10 * time.Second
	e := newStateTestEngine(rule, now)
	st := e.rules["r1"]
	st.initialized = true
	st.lastMet = false
	st.lastFireAt = now.Add(-time.Second)
	e.samples["a1"] = sample{value: "20", kind: driver.KindFloat, quality: driver.QualityGood, at: now}

	e.evaluateRuleLocked("r1", now)
	if len(e.jobs) != 0 {
		t.Fatal("冷却期内不应触发")
	}
	if !st.lastMet {
		t.Fatal("冷却只抑制动作，边沿状态仍应记录为真")
	}
}

func TestEffectiveIntervalUsesGroupThenScanFrequency(t *testing.T) {
	addr := po.DeviceAddress{ID: "a1", DeviceID: "d1", GroupID: "g1", ScanFrequency: 500}
	groups := map[string]po.CollectorGroup{
		"g1": {ID: "g1", DeviceID: "d1", IntervalMs: 200, Status: 1},
	}
	if got := effectiveInterval(addr, groups); got != 200*time.Millisecond {
		t.Errorf("组周期优先 = %v，期望 200ms", got)
	}

	groups["g1"] = po.CollectorGroup{ID: "g1", DeviceID: "d1", IntervalMs: 200, Status: 0}
	if got := effectiveInterval(addr, groups); got != 500*time.Millisecond {
		t.Errorf("组停用应回退 scan_frequency = %v，期望 500ms", got)
	}

	addr.GroupID = ""
	if got := effectiveInterval(addr, groups); got != 500*time.Millisecond {
		t.Errorf("未分组 = %v，期望 500ms", got)
	}
	addr.ScanFrequency = 0
	if got := effectiveInterval(addr, groups); got != time.Second {
		t.Errorf("频率<=0 应取默认 1s，实际 %v", got)
	}
}
