// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package discovery

import (
	"testing"
	"time"

	"iot-gateway/model/vo"
	"iot-gateway/runtimecfg"
)

// fakePolicy 只实现 Reader；测试直接改 enabled 模拟页面上的切换。
type fakePolicy struct {
	enabled bool
}

func (f *fakePolicy) Bool(key runtimecfg.Key) bool {
	if key == runtimecfg.KeyDiscoveryEnabled {
		return f.enabled
	}
	return false
}

// RegisterApplier 本用例直接调 ApplyEnabled，不需要经由 applier。
func (f *fakePolicy) RegisterApplier(runtimecfg.Key, runtimecfg.Applier) func() {
	return func() {}
}

func TestApplyEnabledTogglesSocket(t *testing.T) {
	src := &fakeIdentity{gw: &vo.GatewayVO{ID: "gw-1", Name: "车间网关-01"}}
	rig := newTestRig(t, src)
	policy := &fakePolicy{enabled: false}
	rig.ann.policy = policy
	t.Cleanup(rig.ann.Stop)

	// 启动时开关为关：Start 应静默无操作，不绑定套接字。
	if err := rig.ann.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if rig.listenConn != nil {
		t.Fatal("关闭状态下 Start 不应绑定套接字")
	}

	// 打开：Manager 会先发布新快照再调 applier，因此这里先改 policy。
	policy.enabled = true
	if err := rig.ann.ApplyEnabled(true); err != nil {
		t.Fatalf("ApplyEnabled(true): %v", err)
	}
	if rig.listenConn == nil {
		t.Fatal("开启后应绑定套接字")
	}
	if _, ok := rig.recvNotify(t, 2*time.Second); !ok {
		t.Fatal("开启后未收到广播")
	}

	// 关闭：套接字应被释放，之后的广播不再到达。
	policy.enabled = false
	if err := rig.ann.ApplyEnabled(false); err != nil {
		t.Fatalf("ApplyEnabled(false): %v", err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok := rig.recvNotify(t, 50*time.Millisecond); ok {
			t.Fatal("关闭后仍收到广播")
		}
	}

	// 再次打开应能重新绑定（Stop 是可重入的）。
	policy.enabled = true
	if err := rig.ann.ApplyEnabled(true); err != nil {
		t.Fatalf("再次 ApplyEnabled(true): %v", err)
	}
	if _, ok := rig.recvNotify(t, 2*time.Second); !ok {
		t.Fatal("重新开启后未收到广播")
	}
}
