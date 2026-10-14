// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// 各协议插件的 parity 验收：用**插件驱动**复现内置实现在移除前冻结下来的
// 黄金基线（见 parity_test.go 的说明）。
//
// 每个协议一条。共享助手（编译插件、加载、比对）在 modbusplugin_test.go，
// 这里只声明「插件目录 + scope + 用例名」。
//
// 一条用例没通过，意味着「同一份现场配置在插件版下采出的值与内置版不同」——
// 这正是搬迁最该防住的事，也是照搬实现这条路的唯一代价。

import "testing"

func TestMitsubishiPluginParity(t *testing.T) {
	runParityThroughPlugin(t, "./driversrc/mitsubishi", "mitsubishi", []string{"mc-tcp"})
}

func TestS7PluginParity(t *testing.T) {
	runParityThroughPlugin(t, "./driversrc/s7", "s7", []string{"s7"})
}

func TestOpcUaPluginParity(t *testing.T) {
	runParityThroughPlugin(t, "./driversrc/opcua", "opcua", []string{"opcua"})
}

func TestRockwellPluginParity(t *testing.T) {
	runParityThroughPlugin(t, "./driversrc/rockwellcip", "rockwell", []string{"rockwell-cip"})
}

func TestOmronCipPluginParity(t *testing.T) {
	// 两条用例：单读与 0x0A 批量读（maxTagsPerRequest=4）是插件里两条不同的
	// 报文组装路径，内置换算搬迁时最容易只搬对一条。
	runParityThroughPlugin(t, "./driversrc/omroncip", "cip", []string{"omron-cip", "omron-cip-batch"})
}

func TestFinsPluginParity(t *testing.T) {
	runParityThroughPlugin(t, "./driversrc/fins", "fins", []string{"fins-tcp"})
}
