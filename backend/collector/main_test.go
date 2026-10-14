// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"os"
	"testing"

	"iot-gateway/testutil/drivertest"
)

// TestMain 为整个 collector 测试包备好「至少有一个可用协议」这一前提。
//
// 本包的测试都要建 iot_protocol 行再让 newGatewayTask 建出任务，于是
// driver.Create 必须成功、ScopeOf 必须解析得出来。内置驱动已全部插件化，
// 主二进制里不再有协议实现，而为一个测试包去拉起若干插件进程过重 ——
// 用 drivertest 的合成协议即可：这里要验的是任务编排与类型解析，
// 不是协议语义（后者由各插件的自带测试与 pluginhost 的 parity 黄金比对守着）。
func TestMain(m *testing.M) {
	drivertest.EnsureSyntheticProtocol()
	os.Exit(m.Run())
}
