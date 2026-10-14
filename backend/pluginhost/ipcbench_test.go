// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// 进程外插件相对进程内驱动的**通信开销**微基准。
//
// 对照的两侧跑的是同一份驱动实现（fixtureDriver，功能等价于"只做纯计算"的
// 驱动），同一份输入（[]po.DeviceAddress）。所以：
//
//	PluginRPC - InProcess ≈ 插件边界的净开销
//	                      = WireAddr 投影 + 两侧载荷编解码 + stdio 管道往返
//	                        + 跨进程调度 + 结果 id 多重集校验
//
// read 的载荷走二进制（见 driversdk/binary.go），其余方法走 JSON。
// 编解码本身的 JSON/二进制对比在 driversdk/binary_bench_test.go。
//
// ⚠️ 夹具驱动本身几乎不做事，因此这里量到的是**相对开销的最坏情况**。
// 真实协议的 Read 里有一次到 PLC 的往返（毫秒级），插件开销在其中占比要小得多；
// 换算方法见 bench 输出旁的注释与 specs/capacity-testing.md 的帧数模型。
//
// 运行：go test ./pluginhost -run '^$' -bench 'BenchmarkPluginOverhead' -benchtime 200x

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/model/po"
	"iot-gateway/pluginwire"
)

// benchPointCounts 覆盖「单点小设备」到「5000 点大设备」之间常见的批次规模。
var benchPointCounts = []int{1, 10, 100, 1000}

// benchAddrs 造 n 个点位，字段长度贴近真实配置（UUID v7 主键 + 中文无关的短名）。
func benchAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := range addrs {
		addrs[i] = po.DeviceAddress{
			ID:             fmt.Sprintf("0192f3a1-0000-7000-8000-%012d", i),
			Name:           fmt.Sprintf("point_%d", i),
			Label:          "温度",
			CommonDataType: "Short",
			DataType:       "int16",
			ScanFrequency:  1000,
			Status:         1,
		}
	}
	return addrs
}

// benchReply 造 n 条读结果，形态贴近真实驱动（数值字符串 + 类型名）。
func benchReply(addrs []po.DeviceAddress) driversdk.ReadReply {
	res := make([]driver.ReadResult, len(addrs))
	for i, a := range addrs {
		res[i] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           fmt.Sprintf("%d.5", 1000+i),
			DataType:        a.DataType,
			Kind:            driver.KindInt,
			Quality:         driver.QualityGood,
		}
	}
	return driversdk.ReadReply{Results: res, Connected: true}
}

// benchInstallFixture 与 installFixture 同构，只是接受 testing.TB。
func benchInstallFixture(tb testing.TB) string {
	tb.Helper()
	self, err := os.Executable()
	if err != nil {
		tb.Fatalf("定位测试二进制: %v", err)
	}
	self, err = filepath.Abs(self)
	if err != nil {
		tb.Fatalf("解析测试二进制绝对路径: %v", err)
	}
	name := "iot-driver-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(tb.TempDir(), name)
	if err := copyFile(self, path); err != nil {
		tb.Fatalf("复制夹具二进制: %v", err)
	}
	return path
}

// benchStartPlugin 拉起一个真实的夹具插件进程并完成 describe 握手。
func benchStartPlugin(tb testing.TB) *pluginProcess {
	tb.Helper()
	path := benchInstallFixture(tb)
	opts := processOptions{
		Env:         append(pluginEnv(), envFixture+"=1"),
		Watchdog:    100 * time.Millisecond,
		RestartMin:  10 * time.Millisecond,
		RestartMax:  50 * time.Millisecond,
		MaxRestarts: 20,
		StableAfter: time.Hour,
		KillGrace:   time.Second,
		Timeouts:    DefaultTimeouts(),
		Infof:       func(string, ...any) {},
		Warnf:       func(string, ...any) {},
	}
	p := newPluginProcess(path, opts)
	p.start()
	tb.Cleanup(p.stop)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !p.isUp() {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.isUp() {
		tb.Fatal("夹具进程未在 10s 内就绪")
	}
	return p
}

// BenchmarkPluginOverheadRead 是问「性能损耗有多少」时要看的那张表：
// 同一份读逻辑，进程内 vs 经插件 RPC。
func BenchmarkPluginOverheadRead(b *testing.B) {
	// 进程内基线：同一个 fixtureDriver 结构体，直接调用。
	local := &fixtureDriver{connected: true}

	p := benchStartPlugin(b)
	rd := newRemoteDriver(p, fixtureProtocol, false, DefaultTimeouts())
	if err := rd.Connect(`{}`); err != nil {
		b.Fatalf("Connect: %v", err)
	}

	for _, n := range benchPointCounts {
		addrs := benchAddrs(n)

		b.Run(fmt.Sprintf("InProcess/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := local.Read(addrs); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(fmt.Sprintf("PluginRPC/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := rd.Read(addrs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPluginOverheadFixedCost 量「净往返地板」：0 点位的 read。
// 它不含任何点位序列化，是每次 RPC 无论如何都要付的钱。
func BenchmarkPluginOverheadFixedCost(b *testing.B) {
	p := benchStartPlugin(b)
	rd := newRemoteDriver(p, fixtureProtocol, false, DefaultTimeouts())
	if err := rd.Connect(`{}`); err != nil {
		b.Fatalf("Connect: %v", err)
	}

	rd.mu.Lock()
	sess := rd.session
	rd.mu.Unlock()

	cl, err := p.acquire(context.Background())
	if err != nil {
		b.Fatalf("acquire: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var reply driversdk.ReadReply
		if err := cl.call(context.Background(), pluginwire.MethodRead,
			driversdk.ReadParams{Session: sess}, &reply); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPluginOverheadSerialize 把网关侧为过线付出的编解码单列出来：
//
//   - WireAddr 投影（remoteDriver.Read 里那段循环，只挑驱动真读的 3 个字段）
//   - 请求帧 marshal（call 里 marshal 一次 params，WriteFrame 里再 marshal 一次 Frame）
//   - 应答帧 unmarshal（ReadFrame 解 Frame，call 里再解 Result）
//
// 插件侧付的是镜像的一份，故实际总编解码成本约为本结果 ×2。
func BenchmarkPluginOverheadSerialize(b *testing.B) {
	for _, n := range benchPointCounts {
		addrs := benchAddrs(n)
		reply := benchReply(addrs)

		b.Run(fmt.Sprintf("GatewaySide/points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// ---- 请求方向 ----
				wa := make([]driversdk.WireAddr, len(addrs))
				for j, a := range addrs {
					wa[j] = driversdk.WireAddr{ID: a.ID, Name: a.Name, DataType: a.DataType}
				}
				params, err := json.Marshal(driversdk.ReadParams{Session: 1, Addrs: wa})
				if err != nil {
					b.Fatal(err)
				}
				payload, err := json.Marshal(pluginwire.Frame{ID: 1, Method: pluginwire.MethodRead, Params: params})
				if err != nil {
					b.Fatal(err)
				}

				// ---- 应答方向 ----
				var f pluginwire.Frame
				if err := json.Unmarshal(payload, &f); err != nil {
					b.Fatal(err)
				}
				raw, err := json.Marshal(reply)
				if err != nil {
					b.Fatal(err)
				}
				f = pluginwire.Frame{ID: 1, Result: raw}
				var out driversdk.ReadReply
				if err := json.Unmarshal(f.Result, &out); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPluginOverheadValidate 单列 remoteDriver.Read 里那段结果校验
// （按 id 多重集比对）：它是插件边界**新增**的纯网关侧 CPU，进程内驱动没有这一步，
// 每批点位要建一张 1000 键的 map。
func BenchmarkPluginOverheadValidate(b *testing.B) {
	for _, n := range benchPointCounts {
		addrs := benchAddrs(n)
		reply := benchReply(addrs)

		b.Run(fmt.Sprintf("points=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				want := make(map[string]int, len(addrs))
				for _, a := range addrs {
					want[a.ID]++
				}
				for j := range reply.Results {
					id := reply.Results[j].DeviceAddressID
					want[id]--
				}
			}
		})
	}
}

// BenchmarkPluginOverheadIsConnected 量采集热路径上每轮每设备都要调的
// IsConnected()。进程外实现刻意缓存、不走 RPC，这条基准验证该设计
// 确实把它压回了与进程内同量级。
func BenchmarkPluginOverheadIsConnected(b *testing.B) {
	local := &fixtureDriver{connected: true}

	p := benchStartPlugin(b)
	rd := newRemoteDriver(p, fixtureProtocol, false, DefaultTimeouts())
	if err := rd.Connect(`{}`); err != nil {
		b.Fatalf("Connect: %v", err)
	}

	b.Run("InProcess", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if !local.IsConnected() {
				b.Fatal("应已连接")
			}
		}
	})

	b.Run("PluginCached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if !rd.IsConnected() {
				b.Fatal("应已连接")
			}
		}
	})
}
