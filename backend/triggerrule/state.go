// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/po"
)

// sample 一个条件点位最近一次被采集到的值。
type sample struct {
	value   string
	kind    string
	quality int
	at      time.Time
}

// addressMeta 条件/动作点位的运行期元数据。
type addressMeta struct {
	deviceID   string
	deviceName string
	addrName   string
	// effectiveInterval 该点位实际生效的采集周期：绑定组用组周期，否则用 scan_frequency。
	// 用于判断样本是否已经陈旧（3×周期）。
	effectiveInterval time.Duration
}

// compiledCondition 编译后的单条条件。
type compiledCondition struct {
	addressID string
	op        string
	values    []string
	meta      addressMeta
}

// compiledGroup 编译后的条件组（组内 AND）。
type compiledGroup struct {
	conditions []compiledCondition
}

// compiledRule 一份只读的规则快照。热加载时整份替换，运行中的 job 持有旧指针。
type compiledRule struct {
	id              string
	code            string
	name            string
	status          int
	actionDeviceID  string
	actionAddresses []po.DeviceAddress
	targetChannelID string
	groups          []compiledGroup
	cooldown        time.Duration
	timeout         time.Duration
	// sig 规则自身 + 引用元数据的签名：签名相同的规则在热加载时保留边沿/冷却状态。
	sig uint64
}

// ruleState 单条规则的可变运行状态。
type ruleState struct {
	rule        *compiledRule
	initialized bool
	lastMet     bool
	lastFireAt  time.Time
	inFlight    bool
	pending     bool
}

// triState 三值判定：条件为真 / 为假 / 说不清（样本缺失、陈旧、质量不可用、值认不出）。
type triState int

const (
	triFalse triState = iota
	triUnknown
	triTrue
)

// staleFactor 样本陈旧的容差倍数：3 个采集周期内视为新鲜。
const staleFactor = 3

const defaultSampleInterval = time.Second

// evaluate 用当前样本判定规则（组间 OR、组内 AND）。
//
// 组内语义：任一条为假 → 假；否则任一条 Unknown → Unknown；否则真。
// 规则语义：任一组为真 → 真；否则任一组 Unknown → Unknown；否则假。
//
// Unknown 是刻意的第三态：设备离线/读失败/值类型认不出时不能当作"条件不成立"，
// 否则下一次正常采样会被误判成一次新的上升沿。
func evaluate(rule *compiledRule, samples map[string]sample, now time.Time) triState {
	if len(rule.groups) == 0 {
		return triUnknown
	}
	anyUnknown := false
	for _, g := range rule.groups {
		switch evaluateGroup(g, samples, now) {
		case triTrue:
			return triTrue
		case triUnknown:
			anyUnknown = true
		}
	}
	if anyUnknown {
		return triUnknown
	}
	return triFalse
}

func evaluateGroup(g compiledGroup, samples map[string]sample, now time.Time) triState {
	if len(g.conditions) == 0 {
		return triUnknown
	}
	anyUnknown := false
	for _, c := range g.conditions {
		s, ok := samples[c.addressID]
		if !ok {
			anyUnknown = true
			continue
		}
		if now.Sub(s.at) > staleFactor*c.meta.effectiveInterval {
			anyUnknown = true
			continue
		}
		if !driver.QualityUsable(s.quality) {
			anyUnknown = true
			continue
		}
		met, determinate := Compare(s.value, s.kind, c.op, c.values)
		if !determinate {
			anyUnknown = true
			continue
		}
		if !met {
			return triFalse
		}
	}
	if anyUnknown {
		return triUnknown
	}
	return triTrue
}

func effectiveInterval(addr po.DeviceAddress, groups map[string]po.CollectorGroup) time.Duration {
	if addr.GroupID != "" {
		if g, ok := groups[addr.GroupID]; ok && g.Status == 1 && g.DeviceID == addr.DeviceID && g.IntervalMs > 0 {
			return time.Duration(g.IntervalMs) * time.Millisecond
		}
	}
	if addr.ScanFrequency > 0 {
		return time.Duration(addr.ScanFrequency) * time.Millisecond
	}
	return defaultSampleInterval
}
