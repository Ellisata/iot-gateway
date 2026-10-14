// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"testing"
	"time"
)

// 读取失败日志的两条判据，以及"成功即清除"这条配套关系。
//
// 为什么是"首次或原因变化"而不是时间窗口：间歇性故障（失败几轮、成功一轮）最该被
// 看见，时间窗口会吃掉它中间那几次；而永久故障（每轮都失败）用状态变化就能压住。
// 下面最后一段正是在钉这条 —— 若有人把实现换成时间窗口，它就会红。
func TestReadErrorLogDedup(t *testing.T) {
	task := &gatewayTask{}

	if !task.shouldLogReadError("dev-1", "boom") {
		t.Fatal("首次失败应记一行")
	}
	if task.shouldLogReadError("dev-1", "boom") {
		t.Fatal("同一原因连续失败不该重复记（永久故障就是靠这条压住的）")
	}
	if !task.shouldLogReadError("dev-1", "另一个原因") {
		t.Fatal("换了原因应立刻记一行，不必等窗口")
	}
	if !task.shouldLogReadError("dev-2", "另一个原因") {
		t.Fatal("另一台设备的同一句话不该被吃掉")
	}

	// 一轮成功 → 清记录（与采集成功分支同一处更新）→ 下一次失败要重新记。
	// 缺了这段，间歇性故障会从第二次起彻底静音。
	task.mu.Lock()
	task.clearReadErrLocked("dev-1")
	task.mu.Unlock()
	if !task.shouldLogReadError("dev-1", "另一个原因") {
		t.Fatal("好了又坏应重新记一行")
	}
}

// 重连日志的三条：首次记、窗口内不记、窗口外记且带出累计次数。
// 次数那条最要紧：一台设备"每分钟连一次"与"每轮都连"是两件事，
// 少了计数，后者会被读成前者。
func TestReconnectLogThrottle(t *testing.T) {
	task := &gatewayTask{}
	t0 := time.Now()

	if _, due := task.shouldLogReconnect("dev-1", t0); !due {
		t.Fatal("首次重连应记一行")
	}
	if _, due := task.shouldLogReconnect("dev-1", t0.Add(time.Second)); due {
		t.Fatal("窗口内不该重复记")
	}
	n, due := task.shouldLogReconnect("dev-1", t0.Add(reconnectLogInterval))
	if !due {
		t.Fatal("窗口过后应重新记一行（一直抖不能只报一次）")
	}
	if n != 2 {
		t.Fatalf("累计次数 = %d，期望 2（被压掉的那次 + 本次）", n)
	}

	// 另一台设备各自计时，互不影响
	if _, due := task.shouldLogReconnect("dev-2", t0.Add(time.Second)); !due {
		t.Fatal("另一台设备应各自计时")
	}
}
