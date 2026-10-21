// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"testing"

	"iot-gateway/configFile"
	"iot-gateway/runtimecfg"
)

type fakePolicy struct {
	enabled bool
}

func (f *fakePolicy) Bool(key runtimecfg.Key) bool {
	if key == runtimecfg.KeyPluginEnabled {
		return f.enabled
	}
	return false
}

// TestPluginEnabledIsFrozenAtConstruction 钉住 plugin.enabled 的「仅重启生效」语义：
// Manager 构造时读一次运行期值，之后即使 Manager 的生效值变了，运行时也不变。
func TestPluginEnabledIsFrozenAtConstruction(t *testing.T) {
	cfg := &configFile.Config{Plugin: configFile.PluginConfig{Enabled: true}}
	policy := &fakePolicy{enabled: false}

	m := NewManagerWithPolicy(nil, cfg, nil, policy)
	if m.Enabled() {
		t.Fatal("构造时运行期值为 false，Manager.Enabled 应为 false")
	}

	policy.enabled = true
	if m.Enabled() {
		t.Fatal("运行期修改后 Manager 不应跟着变 —— 该开关只在重启后生效")
	}
}
