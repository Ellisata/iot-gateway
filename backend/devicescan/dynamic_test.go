// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package devicescan

import (
	"errors"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/configFile"
	"iot-gateway/runtimecfg"
)

// fakePolicy 只实现 Reader；测试直接改 enabled 模拟页面上的切换。
type fakePolicy struct {
	enabled bool
}

func (f *fakePolicy) Bool(key runtimecfg.Key) bool {
	if key == runtimecfg.KeyDeviceScanEnabled {
		return f.enabled
	}
	return false
}

func TestEnabledFollowsRuntimePolicy(t *testing.T) {
	cfg := &configFile.Config{DeviceScan: configFile.DeviceScanConfig{Enabled: true}}
	policy := &fakePolicy{enabled: false}

	spec := testSpec(1000)
	e := NewEngineWithPolicy(cfg, &fakeSpecs{specs: []driver.ProtocolDiscovery{spec}}, &fakeProber{}, policy)
	t.Cleanup(e.Stop)
	e.subnets = testSubnets

	// 默认配置是 true，但运行期覆盖为 false：应以覆盖值为准。
	if e.Enabled() {
		t.Fatal("运行期开关为 false 时 Enabled 应为 false")
	}
	if _, err := e.Submit(SubmitRequest{Protocol: testProtocol}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("关闭后 Submit 应返回 ErrDisabled，实际 %v", err)
	}

	policy.enabled = true
	if !e.Enabled() {
		t.Fatal("运行期开关切回 true 后 Enabled 应立即为 true")
	}
	if _, err := e.Submit(SubmitRequest{Protocol: testProtocol}); err != nil {
		t.Fatalf("重新开启后 Submit 不应再被开关拦下: %v", err)
	}
}
