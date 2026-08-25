package service

import (
	"testing"
)

// TestBuildDeviceOverview 覆盖 buildDeviceOverview 纯函数的统计口径：
// 总数（启用+禁用）/启用/禁用/已接入采集/在线/离线/未接入采集/在线率。
func TestBuildDeviceOverview(t *testing.T) {
	tests := []struct {
		name          string
		enabled       []string
		disabled      []string
		offline       []string
		collected     []string
		wantTotal     int
		wantEnabled   int
		wantDisabled  int
		wantCollected int
		wantOnline    int
		wantOffline   int
		wantUnCollect int
		wantRate      float64
	}{
		{
			name:          "空集",
			enabled:       nil,
			disabled:      nil,
			offline:       nil,
			collected:     nil,
			wantTotal:     0,
			wantEnabled:   0,
			wantDisabled:  0,
			wantCollected: 0,
			wantOnline:    0,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      0,
		},
		{
			name:          "全部在线无禁用",
			enabled:       []string{"a", "b"},
			disabled:      nil,
			offline:       nil,
			collected:     []string{"a", "b"},
			wantTotal:     2,
			wantEnabled:   2,
			wantDisabled:  0,
			wantCollected: 2,
			wantOnline:    2,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      1,
		},
		{
			name:          "混合含未接入采集和禁用",
			enabled:       []string{"a", "b", "c", "d"},
			disabled:      []string{"e"},
			offline:       []string{"b"},
			collected:     []string{"a", "b", "c"},
			wantTotal:     5,
			wantEnabled:   4,
			wantDisabled:  1,
			wantCollected: 3,
			wantOnline:    2,
			wantOffline:   1,
			wantUnCollect: 1,
			wantRate:      float64(2) / 3,
		},
		{
			name:          "一台接入采集在线一台禁用",
			enabled:       []string{"a", "b"},
			disabled:      []string{"c"},
			offline:       nil,
			collected:     []string{"a"},
			wantTotal:     3,
			wantEnabled:   2,
			wantDisabled:  1,
			wantCollected: 1,
			wantOnline:    1,
			wantOffline:   0,
			wantUnCollect: 1,
			wantRate:      1,
		},
		{
			name:          "全部禁用不参与在线率",
			enabled:       nil,
			disabled:      []string{"a", "b"},
			offline:       nil,
			collected:     nil,
			wantTotal:     2,
			wantEnabled:   0,
			wantDisabled:  2,
			wantCollected: 0,
			wantOnline:    0,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      0,
		},
		{
			name:          "未接入采集的设备不拉低在线率",
			enabled:       []string{"a", "b", "c"},
			disabled:      nil,
			offline:       []string{"a"},
			collected:     []string{"a"},
			wantTotal:     3,
			wantEnabled:   3,
			wantDisabled:  0,
			wantCollected: 1,
			wantOnline:    0,
			wantOffline:   1,
			wantUnCollect: 2,
			wantRate:      0,
		},
		{
			name:          "collected 含未启用设备被防御性剔除",
			enabled:       []string{"a"},
			disabled:      []string{"b"},
			offline:       nil,
			collected:     []string{"a", "x"},
			wantTotal:     2,
			wantEnabled:   1,
			wantDisabled:  1,
			wantCollected: 1,
			wantOnline:    1,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      1,
		},
		{
			name:          "offline 指向未启用/未采集设备不计入",
			enabled:       []string{"a", "b"},
			disabled:      []string{"x"},
			offline:       []string{"x"},
			collected:     []string{"a", "b"},
			wantTotal:     3,
			wantEnabled:   2,
			wantDisabled:  1,
			wantCollected: 2,
			wantOnline:    2,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      1,
		},
		{
			name:          "collected 为 0 时在线率为 0",
			enabled:       []string{"a"},
			disabled:      nil,
			offline:       nil,
			collected:     nil,
			wantTotal:     1,
			wantEnabled:   1,
			wantDisabled:  0,
			wantCollected: 0,
			wantOnline:    0,
			wantOffline:   0,
			wantUnCollect: 1,
			wantRate:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enabledSet := toSet(tt.enabled)
			disabledSet := toSet(tt.disabled)
			offlineSet := toSet(tt.offline)
			collectedSet := toSet(tt.collected)

			got := buildDeviceOverview(enabledSet, disabledSet, offlineSet, collectedSet)

			if got.Total != tt.wantTotal ||
				got.Enabled != tt.wantEnabled ||
				got.Disabled != tt.wantDisabled ||
				got.Collected != tt.wantCollected ||
				got.Online != tt.wantOnline ||
				got.Offline != tt.wantOffline ||
				got.UnCollected != tt.wantUnCollect {
				t.Fatalf("mismatch: got %+v", got)
			}
			// 在线率保留 2 位小数，与实现一致
			if got.OnlineRate != float64(int(tt.wantRate*100+0.5))/100 {
				t.Fatalf("onlineRate mismatch: got %v, want %v", got.OnlineRate, tt.wantRate)
			}
		})
	}
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
