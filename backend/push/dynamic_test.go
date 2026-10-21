// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"testing"

	"iot-gateway/collector"
	"iot-gateway/runtimecfg"
)

// fakeRuntime 记录 applier，并允许测试模拟一次「页面切换」。
type fakeRuntime struct {
	changeOnly bool
	appliers   []runtimecfg.Applier
}

func (f *fakeRuntime) Bool(key runtimecfg.Key) bool {
	if key == runtimecfg.KeyPushChangeOnly {
		return f.changeOnly
	}
	return false
}

func (f *fakeRuntime) RegisterApplier(_ runtimecfg.Key, fn runtimecfg.Applier) func() {
	f.appliers = append(f.appliers, fn)
	return func() {}
}

// set 模拟 Manager 的「发布快照 → 调 applier」顺序。
func (f *fakeRuntime) set(v bool) {
	old := f.changeOnly
	f.changeOnly = v
	for _, fn := range f.appliers {
		_ = fn(old, v)
	}
}

func TestChangeOnlyFollowsRuntimePolicy(t *testing.T) {
	policy := &fakeRuntime{changeOnly: true}
	e := NewEngineWithPolicy(nil, nil, policy)
	ch := &stubChannel{id: "c1"}
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}

	e.PushRecords(recs) // 首轮：投递
	e.PushRecords(recs) // 未变化：跳过
	if got := len(ch.drain()); got != 1 {
		t.Fatalf("变化才推送开启时收到 %d 批，want 1", got)
	}

	// 关掉开关：同样的数据下一轮应重新投递。
	policy.set(false)
	e.PushRecords(recs)
	if got := len(ch.drain()); got != 1 {
		t.Fatalf("关闭变化才推送后收到 %d 批，want 1", got)
	}

	// 再打开：tracker 应被重置，下一轮（值未变）也要全量投递一次。
	policy.set(true)
	e.PushRecords(recs)
	if got := len(ch.drain()); got != 1 {
		t.Fatalf("重新开启后首批收到 %d 批，want 1（tracker 未重置？）", got)
	}
}
