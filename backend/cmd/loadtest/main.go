// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// loadtest 端到端采集容量压测工具。
//
// 进程内跑完整采集链路（采集引擎 + worker 池 + 协议驱动 + 假 PLC 服务器），
// 扫描（设备数 × 单设备点位）矩阵，输出每组的掉点率 / 吞吐 / 内存 / 协程数，
// 用于测量每种 PLC 协议在网关侧可支撑的设备和点位容量上限。
//
// 用法：
//
//	go run ./cmd/loadtest -protocol modbus -devices 10,50,100 -points 500,2000,5000
//
// 支持协议：modbus / mc / fins / s7 / cip / rockwell / opcua / dlt645
// （各协议假服务器见 testutil/fake）。
// 常用选项：
//
//	-sparse    稀疏地址布局（地址间隙超过合并窗口，每个点位一个请求帧，探测帧数最坏情况）
//	-scan 500  把采集频率从默认 1000ms 收紧到 500ms
//	-run 10    延长测量窗口到 10s（减小首尾边界误差）
//	-latency   每事务注入固定延迟（模拟真实线路往返，CIP 系 / OPC UA / DL/T 645）
//	-cpuprofile cpu.pprof  -memprofile mem.pprof  在最大组合下输出 pprof 画像
//
// 注：-sparse 对 DL/T 645 与 OPC UA 无意义——两者都没有区间合并概念，
// 帧数只由「点数 / 单请求打包数」决定，与地址是否相邻无关。
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/logger"
	"iot-gateway/testutil/fake"
)

// loadtestDefaultYAML loadtest 自包含配置：日志静音到 WARN 并落到临时目录，
// 避免采集热路径的 DEBUG 日志 I/O 干扰吞吐测量。
const loadtestDefaultYAML = `
log:
  level: WARN
  outputDir: iotgw-loadtest-logs
`

func main() {
	protocol := flag.String("protocol", "modbus", "protocol: modbus | mc | fins | s7 | cip | rockwell | opcua | dlt645")
	devices := flag.String("devices", "10,50,100", "comma-separated device counts")
	points := flag.String("points", "500,2000,5000", "comma-separated points per device")
	scan := flag.Int("scan", 1000, "scan frequency in ms")
	run := flag.Float64("run", 5, "measure window seconds")
	warmup := flag.Float64("warmup", 2, "warmup seconds")
	servers := flag.Int("servers", 4, "fake PLC server count")
	sparse := flag.Bool("sparse", false, "sparse address layout (breaks range merging)")
	cipBatch := flag.Int("cip-batch", 0, "CIP 0x0A multi-service read: tags per request (>1 enables batching)")
	opcuaBatch := flag.Int("opcua-batch", 0, "OPC UA maxBatch: nodes per ReadRequest (0 = driver default 100)")
	dlt645Batch := flag.Int("dlt645-batch", 0, "DL/T 645 maxDIsPerRead: data identifiers per request (0 = driver default 12, 1 = one round trip per point, max 12)")
	dlt645Inter := flag.Int("dlt645-interframe", -1, "DL/T 645 inter-frame delay ms (-1 = driver default: 0 for TCP, 30 for serial)")
	latency := flag.Int("latency", 0, "artificial per-transaction latency in ms (CIP / OPC UA / DL/T 645 fake servers only)")
	workers := flag.Int("workers", 0, "worker pool concurrency (0 = NumCPU*2)")
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to file for the largest case")
	memprofile := flag.String("memprofile", "", "write heap profile to file for the largest case")
	flag.Parse()

	// 自包含运行：把工作目录切到临时目录，避免被运行目录下 backend/default.yaml
	// 的 DEBUG 日志级别覆盖；loadtest 用自身嵌入的 WARN 配置初始化 config 与 logger。
	// （getLogger 的 sync.Once 会用同一 config 再初始化一次，级别一致，不会回退到 DEBUG。）
	if err := os.Chdir(os.TempDir()); err != nil {
		fmt.Fprintf(os.Stderr, "chdir to temp dir: %v\n", err)
		os.Exit(2)
	}
	configFile.SetDefaultConfig([]byte(loadtestDefaultYAML))
	configFile.InitConfig()
	logger.InitLogger(configFile.MustGetConfig())

	pa, ok := adapters[strings.ToLower(*protocol)]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown protocol %q (supported: %s)\n", *protocol, knownProtocols())
		os.Exit(2)
	}

	devList := parseIntList(*devices)
	ptList := parseIntList(*points)
	if len(devList) == 0 || len(ptList) == 0 {
		fmt.Fprintln(os.Stderr, "devices/points must be non-empty comma-separated integers")
		os.Exit(2)
	}
	if *scan <= 0 {
		fmt.Fprintln(os.Stderr, "scan must be > 0")
		os.Exit(2)
	}

	// 启动假 PLC 服务器池。-latency>0 时 CIP 系与 OPC UA 假服务器换用
	// 带延迟构造，模拟真实 RTT；其它协议不支持该选项，配置了直接报错退出。
	srvCount := *servers
	if srvCount < 1 {
		srvCount = 1
	}
	latencyDur := time.Duration(*latency) * time.Millisecond
	if *latency < 0 || (*latency > 0 && pa.newServerWithLatency == nil) {
		fmt.Fprintf(os.Stderr, "-latency is not supported for protocol %q\n", *protocol)
		os.Exit(2)
	}
	srvs := make([]*fake.Server, 0, srvCount)
	for i := 0; i < srvCount; i++ {
		var s *fake.Server
		var err error
		if latencyDur > 0 {
			s, err = pa.newServerWithLatency(latencyDur)
		} else {
			s, err = pa.newServer()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "start fake server: %v\n", err)
			os.Exit(2)
		}
		srvs = append(srvs, s)
	}
	defer func() {
		for _, s := range srvs {
			s.Close()
		}
	}()

	opts := Options{
		Protocol:         pa.name,
		Devices:          devList,
		Points:           ptList,
		ScanMs:           *scan,
		RunSec:           time.Duration(*run * float64(time.Second)),
		Warmup:           time.Duration(*warmup * float64(time.Second)),
		Sparse:           *sparse,
		CIPBatch:         *cipBatch,
		OpcUaBatch:       *opcuaBatch,
		DLT645Batch:      *dlt645Batch,
		DLT645InterFrame: *dlt645Inter,
		Workers:          *workers,
	}

	layout := "contiguous"
	if *sparse {
		layout = "sparse"
	}
	if *cipBatch > 1 {
		layout = fmt.Sprintf("%s+cip-batch-%d", layout, *cipBatch)
	}
	if *opcuaBatch > 0 && *opcuaBatch != 100 {
		layout = fmt.Sprintf("%s+maxBatch-%d", layout, *opcuaBatch)
	}
	if *dlt645Batch >= 1 {
		layout = fmt.Sprintf("%s+maxDIs-%d", layout, *dlt645Batch)
	}
	if *dlt645Inter >= 0 {
		layout = fmt.Sprintf("%s+interFrame-%dms", layout, *dlt645Inter)
	}
	if *workers > 0 {
		layout = fmt.Sprintf("%s+workers-%d", layout, *workers)
	}
	if latencyDur > 0 {
		layout = fmt.Sprintf("%s+latency-%dms", layout, *latency)
	}
	fmt.Printf("protocol=%s scan=%dms layout=%s devices=%v points=%v servers=%d workers=%d queue=1024\n",
		pa.name, *scan, layout, devList, ptList, srvCount, runtime.NumCPU()*2)

	// 可选的 CPU/堆画像：覆盖矩阵中最大组合的整个运行
	var cpuStop func()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create cpuprofile: %v\n", err)
			os.Exit(2)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "start cpuprofile: %v\n", err)
			os.Exit(2)
		}
		cpuStop = func() {
			pprof.StopCPUProfile()
			f.Close()
			fmt.Printf("cpu profile written to %s\n", *cpuprofile)
		}
	}
	defer func() {
		if cpuStop != nil {
			cpuStop()
		}
	}()

	if err := sweep(pa, opts, srvs); err != nil {
		fmt.Fprintf(os.Stderr, "sweep failed: %v\n", err)
		os.Exit(1)
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create memprofile: %v\n", err)
			os.Exit(2)
		}
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "write memprofile: %v\n", err)
			os.Exit(2)
		}
		f.Close()
		fmt.Printf("heap profile written to %s\n", *memprofile)
	}
}

func knownProtocols() string {
	var names []string
	for k := range adapters {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

func parseIntList(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil || v <= 0 {
			continue
		}
		out = append(out, v)
	}
	return out
}
