// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"sync"

	"iot-gateway/logger"
)

// problemLog 按问题主体抑制重复告警：同一问题只在**状态变化**时才打 WARN。
//
// 电表拒读某个数据标识是现场常态（表就是没实现那一项），而采集扫描周期可以低到
// 1 秒——不做抑制时同一行 WARN 一分钟就能刷出上百条，把真正有用的日志淹没
// （实测 2026-09-20 现场日志：10 分钟 124 条完全相同的告警）。
//
// 语义：
//   - 问题描述变化（含首次出现）→ WARN，返回 true；
//   - 问题描述不变 → 降为 DEBUG，返回 false；
//   - 恢复正常 → 一次 INFO（由 resolve 负责）。
//
// 比较的是**整条问题描述**，因此错误码从 01 变成 02、从「读取被拒绝」变成
// 「应答含后续帧」都会重新打印——只有一模一样的重复才被压掉。
//
// 键由调用方指定：单点位问题用数据标识的书写形式，成组问题用组的标识串。
// 不同键之间互不影响。
type problemLog struct {
	mu   sync.Mutex
	last map[string]string // 键 → 上一轮上报的问题描述
}

// report 上报键 key 主体本轮的问题描述，返回本次是否判定为问题变化。
//
// 问题变化时打 WARN；重复时打 DEBUG（保留在调试日志里，便于确认「一直在报」
// 而不是「只报过一次」）。
func (p *problemLog) report(key, problem string) bool {
	p.mu.Lock()
	if p.last == nil {
		p.last = make(map[string]string)
	}
	prev, existed := p.last[key]
	p.last[key] = problem
	p.mu.Unlock()

	if existed && prev == problem {
		logger.Debug("dlt645: %s（异常持续，重复告警已抑制）", problem)
		return false
	}
	logger.Warn("dlt645: %s", problem)
	return true
}

// resolve 上报键 key 主体本轮恢复正常，返回此前是否处于异常。
//
// 只有真正发生过异常才打 INFO：正常情况下每轮都调用它，不能变成新的刷屏源。
func (p *problemLog) resolve(key, subject string) bool {
	p.mu.Lock()
	prev, existed := p.last[key]
	delete(p.last, key)
	p.mu.Unlock()

	if !existed {
		return false
	}
	logger.Info("dlt645: %s 已恢复正常（此前: %s）", subject, prev)
	return true
}

// abnormal 返回键 key 主体当前是否处于异常状态。
func (p *problemLog) abnormal(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, existed := p.last[key]
	return existed
}

// clear 清空全部状态。重连时调用：表号或电表型号都可能已经换了，
// 旧表的问题状态不该影响新表的告警。
func (p *problemLog) clear() {
	p.mu.Lock()
	p.last = nil
	p.mu.Unlock()
}
