// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"sync"
	"testing"

	"gorm.io/gorm"

	"iot-gateway/push"
)

// fakeStatusSource 注入假连接状态源，模拟 push.Engine.GetStatus。
type fakeStatusSource struct {
	mu       sync.Mutex
	statuses []push.ChannelStatusVO
}

func (f *fakeStatusSource) GetStatus() []push.ChannelStatusVO {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]push.ChannelStatusVO(nil), f.statuses...)
}

func (f *fakeStatusSource) set(statuses []push.ChannelStatusVO) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append([]push.ChannelStatusVO(nil), statuses...)
}

func channelStatus(id, name string, running, connected bool) push.ChannelStatusVO {
	return push.ChannelStatusVO{ID: id, Name: name, Type: "mqtt", Running: running, Connected: connected}
}

func newChannelMonitor(db *gorm.DB, src *fakeStatusSource) *ChannelMonitor {
	m := NewChannelMonitor(db, src)
	m.tr.cfg.ConsecutiveFailures = channelFailThreshold
	return m
}

func TestChannelMonitorOfflineAfterThreshold(t *testing.T) {
	db := newTestDB(t)
	src := &fakeStatusSource{}
	m := newChannelMonitor(db, src)

	// 运行中的通道断连：连续 2 次巡检后判离线
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, false)})
	m.scan() // failCount=1
	if n := countAlarms(t, db, "ch-1", "", ""); n != 0 {
		t.Fatalf("expected no alarm after 1 failed scan, got %d", n)
	}
	m.scan() // failCount=2 -> 离线
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected 1 active offline alarm after threshold, got %d", n)
	}

	// 恢复：一次 connected=true 即清报警 + 写 recover
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, true)})
	m.scan()
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 0 {
		t.Fatalf("expected active alarm cleared on recover, got %d", n)
	}
	if n := countAlarms(t, db, "ch-1", TypeRecover, ""); n != 1 {
		t.Fatalf("expected 1 recover record, got %d", n)
	}
}

func TestChannelMonitorIgnoresStoppedChannel(t *testing.T) {
	db := newTestDB(t)
	src := &fakeStatusSource{}
	m := newChannelMonitor(db, src)

	// 未运行通道（运营停用/配置非法）：断连不报警
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", false, false)})
	m.scan()
	m.scan()
	if n := countAlarms(t, db, "ch-1", "", ""); n != 0 {
		t.Fatalf("expected no alarm for stopped channel, got %d", n)
	}
}

func TestChannelMonitorStopClearsActiveAlarm(t *testing.T) {
	db := newTestDB(t)
	src := &fakeStatusSource{}
	m := newChannelMonitor(db, src)

	// 运行中断连 -> 离线报警
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, false)})
	m.scan()
	m.scan()
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected active alarm before stop, got %d", n)
	}

	// 运营停用（running->false）：清 active 报警，历史保留
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", false, false)})
	m.scan()
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 0 {
		t.Fatalf("expected active alarm cleared on stop, got %d", n)
	}
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusCleared); n != 1 {
		t.Fatalf("expected offline history kept, got %d", n)
	}
}

func TestChannelMonitorRestartResetsState(t *testing.T) {
	db := newTestDB(t)
	src := &fakeStatusSource{}
	m := newChannelMonitor(db, src)

	// 断连两次判离线
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, false)})
	m.scan()
	m.scan()
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected offline alarm, got %d", n)
	}

	// 重启（running false->true，新实例连接建立窗口）：状态机重置，
	// 前 1 次断连不触发报警（若未重置会因 failCount 继续累积而立即报警）
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", false, false)})
	m.scan() // 停用清 active
	src.set([]push.ChannelStatusVO{channelStatus("ch-1", "mqtt1", true, false)})
	m.scan() // 重启后第 1 次：已重置，failCount=1，不报警
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 0 {
		t.Fatalf("expected no alarm right after restart (state reset), got %d", n)
	}
	m.scan() // 第 2 次：failCount=2 -> 离线
	if n := countAlarms(t, db, "ch-1", TypeOffline, StatusActive); n != 1 {
		t.Fatalf("expected offline alarm after restart threshold, got %d", n)
	}
}
