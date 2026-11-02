// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"fmt"
	"sync"
	"time"
)

// 控制下发限流：固定一分钟窗口，按**设备**与**点位**两个维度同时限制。
//
// 为什么不用令牌桶：控制下发是低频动作，固定窗口的语义对运维更好解释
// （"这台设备一分钟最多写 60 个点"），也更容易在前端提示里说清楚。
// 代价是窗口边界可能产生一次 2 倍突发，对 PLC 控制场景可以接受。
//
// 为什么检查与扣减必须在同一把锁内：否则两个并发请求会各自看到"还剩 1 个配额"
// 然后都放行，合计超额。批量请求按实际点位数扣减，任一超限时整批拒绝 ——
// 不能出现"前几个点已写下去、后几个点被限流"的半批状态。
const (
	controlRateWindow    = time.Minute
	controlRateRetention = 2 * time.Minute
	// 限流表是配置驱动的（设备 × 点位），正常规模远小于此值。
	// 这个上限只防异常输入把 map 撑爆；超过时先做一次过期清理。
	controlRateMaxEntries = 8192
)

type controlRateBucket struct {
	start time.Time
	count int
}

type controlRateLimiter struct {
	mu        sync.Mutex
	perDevice int // <=0 表示不限制
	perPoint  int // <=0 表示不限制
	windows   map[string]controlRateBucket
	lastPrune time.Time
}

func newControlRateLimiter(perDevice, perPoint int) *controlRateLimiter {
	return &controlRateLimiter{
		perDevice: perDevice,
		perPoint:  perPoint,
		windows:   make(map[string]controlRateBucket),
	}
}

// allow 原子地检查并扣减配额。返回 false 时 reason 是可读的中文原因。
func (l *controlRateLimiter) allow(deviceID string, addressIDs []string, now time.Time) (bool, string) {
	if l == nil || (l.perDevice <= 0 && l.perPoint <= 0) {
		return true, ""
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)

	if l.perDevice > 0 {
		key := "d:" + deviceID
		if l.countLocked(key, now)+len(addressIDs) > l.perDevice {
			return false, fmt.Sprintf("设备 %s 的控制下发频率超过限制（%d 点位/分钟）", deviceID, l.perDevice)
		}
	}
	if l.perPoint > 0 {
		for _, id := range addressIDs {
			key := pointRateKey(deviceID, id)
			if l.countLocked(key, now)+1 > l.perPoint {
				return false, fmt.Sprintf("点位 %s 的控制下发频率超过限制（%d 次/分钟）", id, l.perPoint)
			}
		}
	}

	// 全部通过后才扣减：检查阶段不修改状态，避免"前几个点扣了、后面被拒"。
	if l.perDevice > 0 {
		l.consumeLocked("d:"+deviceID, now, len(addressIDs))
	}
	if l.perPoint > 0 {
		for _, id := range addressIDs {
			l.consumeLocked(pointRateKey(deviceID, id), now, 1)
		}
	}
	return true, ""
}

func pointRateKey(deviceID, addressID string) string {
	// \x00 不会出现在 UUID 里，用作分隔符不会与设备/点位 id 撞键。
	return "p:" + deviceID + "\x00" + addressID
}

func (l *controlRateLimiter) countLocked(key string, now time.Time) int {
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= controlRateWindow {
		return 0
	}
	return w.count
}

func (l *controlRateLimiter) consumeLocked(key string, now time.Time, n int) {
	w, ok := l.windows[key]
	if !ok || now.Sub(w.start) >= controlRateWindow {
		w = controlRateBucket{start: now}
	}
	w.count += n
	l.windows[key] = w
}

// pruneLocked 惰性清理过期窗口。每过一分钟或表过大时执行一次，
// 不额外起 goroutine（与 discovery 的 probeWindow 同一取舍）。
func (l *controlRateLimiter) pruneLocked(now time.Time) {
	if len(l.windows) <= controlRateMaxEntries && now.Sub(l.lastPrune) < controlRateWindow {
		return
	}
	for key, w := range l.windows {
		if now.Sub(w.start) >= controlRateRetention {
			delete(l.windows, key)
		}
	}
	l.lastPrune = now
}
