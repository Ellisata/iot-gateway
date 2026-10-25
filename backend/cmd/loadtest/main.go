// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// loadtest 端到端采集容量压测工具。
//
// 进程内跑完整采集链路（采集引擎 + worker 池 + 进程外协议驱动插件 + 假 PLC 服务器），
// 扫描（设备数 × 单设备点位）矩阵，输出每组的掉点率 / 吞吐 / 内存 / 协程数，
// 用于测量每种 PLC 协议在网关侧可支撑的设备和点位容量上限。
//
// 协议驱动全部来自进程外插件（内置实现已随插件化移除，见 docs/driver-plugins.md），
// 因此运行前必须先编译插件：见 README 的「运行前先准备插件」。
//
// 用法：
//
//	go build -o ./plugins/iot-driver-modbus ./driversrc/modbus
//	go run ./cmd/loadtest -protocol modbus -devices 10,50,100 -points 500,2000,5000 -plugins-dir ./plugins
//
// 支持协议：modbus / mc / fins / s7 / cip / rockwell / opcua / dlt645（各协议假服务器见 testutil/fake）。
// 常用选项：
//
//	-plugins-dir ./plugins  插件目录（相对路径按「可执行文件旁 → 当前工作目录」依次解析）
//	-sparse    稀疏地址布局（地址间隙超过合并窗口，每个点位一个请求帧，探测帧数最坏情况）
//	-scan 500  把采集频率从默认 1000ms 收紧到 500ms
//	-latency 20 每事务注入 20ms 延迟模拟真实 RTT（全部 8 个协议都支持）
//	-dlt645-dis 1   DL/T 645 单请求只打包 1 个数据标识（出厂默认 12 = 协议上限，即一帧 12 点）
//	-serial-baud 2400  模拟串口链路：按帧字节数折算线路时间并叠加到 -latency（无需串口硬件）
//	-run 10    延长测量窗口到 10s（减小首尾边界误差）
//	-cpuprofile cpu.pprof  -memprofile mem.pprof  在最大组合下输出 pprof 画像
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

	"github.com/Ellisata/iot-gateway-drivers/testutil/fake"
	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/configFile"
	"iot-gateway/logger"
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
	cipBatch := flag.Int("cip-batch", 0, "CIP 0x0A multi-service read: tags per request (1 = single read, N>1 = batch; 0 = omit field, driver default 64)")
	opcuaBatch := flag.Int("opcua-batch", 0, "OPC UA maxBatch: nodes per ReadRequest (0 = driver default 100)")
	dlt645DIs := flag.Int("dlt645-dis", 0, "DL/T 645 maxDIsPerRead: data identifiers packed per request (1..12; 0 = omit field, driver default 12 = protocol ceiling; 1 = one frame per point)")
	dlt645InterFrame := flag.Int("dlt645-interframe", -1, "DL/T 645 interFrameDelayMs: post-send wait before reading the response, ms (-1 = omit field, driver default 0 for TCP / 30 for serial; 0 = disable)")
	latency := flag.Int("latency", 0, "artificial per-transaction latency in ms (simulates real RTT; supported by all protocols)")
	serialBaud := flag.Int("serial-baud", 0, "model a serial line at this baud rate: derive the per-frame line time from the protocol frame size (8E1, 11 bits/byte) and ADD it to -latency; use -latency for meter processing time (0 = off; errors out for protocols with no known frame layout)")
	workers := flag.Int("workers", 0, "worker pool concurrency (0 = NumCPU*2)")
	pluginsDir := flag.String("plugins-dir", "plugins",
		"plugin directory; a relative path resolves against the executable's dir first, then the invocation working directory (NOT cwd only); must contain iot-driver-* built from driversrc/")
	cpuprofile := flag.String("cpuprofile", "", "write cpu profile to file for the largest case")
	memprofile := flag.String("memprofile", "", "write heap profile to file for the largest case")
	flag.Parse()

	// 记下调用方所在目录，再切 cwd。相对 -plugins-dir 要靠它解析：下面的 Chdir
	// 之后 cwd 已经是临时目录，那时再看 cwd 只会解析到临时目录里去。
	invocationDir, _ := os.Getwd()

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

	// 加载协议驱动插件。**必须早于任何一次采集引擎启动**：插件的首次扫描是
	// 同步的（见 pluginhost.Manager.Start），注册表就位后采集引擎建任务时才能
	// 为对应协议的设备建出驱动；否则设备会被静默跳过、跑出全 0 的假结论。
	// （时序与 backend/main.go 的 deps.DriverManager.Start() 完全一致。）
	resolvedDir, err := resolvePluginsDir(*pluginsDir, invocationDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n  需要的插件产物：%s（源码 %s）\n"+
			"  编译：cd backend && go build -o ./plugins/%s %s\n"+
			"  （或在仓库根执行 ./build.sh，一次性编出全部插件）\n",
			err, pa.pluginBinary(), pa.pluginSrc, pa.pluginBinary(), pa.pluginSrc)
		os.Exit(1)
	}
	mgr, stopPlugins, err := startPluginManager(resolvedDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动插件管理器失败: %v\n", err)
		os.Exit(1)
	}
	// 晚于全部 collector.Engine.Stop()（每个用例的引擎都在 runCase 内停妥），
	// 届时无 RPC 在途，回收插件进程才安全。
	defer stopPlugins()
	// 协议不在注册表里就直接失败，不跑出一堆「协议不支持」的假结果。
	// 这里显式 stopPlugins 再退：os.Exit 不执行 defer，否则白留一份临时库
	//（插件子进程另有兜底：Windows 是 Job Object、Unix 是 stdin EOF）。
	if perr := requireProtocol(pa, resolvedDir); perr != nil {
		fmt.Fprintf(os.Stderr, "%v\n", perr)
		stopPlugins()
		os.Exit(1)
	}

	srvCount := *servers
	if srvCount < 1 {
		srvCount = 1
	}

	// opts 必须先于服务器池构造：-serial-baud 的帧字节数折算要读 opts
	//（打包数等选项会改变帧大小，进而改变线路时间）。
	opts := Options{
		Protocol:           pa.name,
		Devices:            devList,
		Points:             ptList,
		ScanMs:             *scan,
		RunSec:             time.Duration(*run * float64(time.Second)),
		Warmup:             time.Duration(*warmup * float64(time.Second)),
		Sparse:             *sparse,
		CIPBatch:           *cipBatch,
		OpcUaBatch:         *opcuaBatch,
		DLT645DIs:          *dlt645DIs,
		DLT645InterFrameMs: *dlt645InterFrame,
		Workers:            *workers,
	}

	// 单帧等效时延 = 人工注入的 RTT（-latency，用于表处理时间） + 串口线路时间
	// （-serial-baud，由协议帧字节数折算）。
	//
	// -serial-baud 只负责「线路时间」这一项，不碰 -dlt645-interframe：换向预留是
	// 配置项、线路时间是物理量，两者相乘进同一个轮询周期，但来源不同，混在一起
	// 会让「测出来的容量对应哪个配置」说不清。
	latencyDur := time.Duration(*latency) * time.Millisecond
	lineTime := time.Duration(0)
	frameBytes := 0
	if *serialBaud > 0 {
		if pa.serialFrameBytes == nil {
			fmt.Fprintf(os.Stderr, "-serial-baud is not supported for protocol %q: "+
				"its frame layout is not modelled here, so the line time cannot be derived "+
				"(running anyway would silently measure loopback instead of a serial link)\n", *protocol)
			os.Exit(2)
		}
		frameBytes = pa.serialFrameBytes(opts)
		if frameBytes <= 0 {
			fmt.Fprintf(os.Stderr, "-serial-baud: derived frame size is %d bytes, want > 0\n", frameBytes)
			os.Exit(2)
		}
		lineTime = lineTimeAt(*serialBaud, frameBytes)
		latencyDur += lineTime
	}
	if *latency < 0 || *serialBaud < 0 || (latencyDur > 0 && pa.newServerWithLatency == nil) {
		fmt.Fprintf(os.Stderr, "-latency is not supported for protocol %q\n", *protocol)
		os.Exit(2)
	}

	// 启动假 PLC 服务器池。latencyDur>0 时换用带延迟构造模拟真实 RTT。
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

	layout := "contiguous"
	if *sparse {
		layout = "sparse"
	}
	if *cipBatch > 0 {
		layout = fmt.Sprintf("%s+cip-batch-%d", layout, *cipBatch)
	}
	if *opcuaBatch > 0 && *opcuaBatch != 100 {
		layout = fmt.Sprintf("%s+maxBatch-%d", layout, *opcuaBatch)
	}
	if *dlt645DIs > 0 {
		layout = fmt.Sprintf("%s+dis-%d", layout, *dlt645DIs)
	}
	if *dlt645InterFrame >= 0 {
		layout = fmt.Sprintf("%s+interframe-%dms", layout, *dlt645InterFrame)
	}
	if *workers > 0 {
		layout = fmt.Sprintf("%s+workers-%d", layout, *workers)
	}
	if *serialBaud > 0 {
		layout = fmt.Sprintf("%s+serial-%dbps", layout, *serialBaud)
	}
	if latencyDur > 0 {
		layout = fmt.Sprintf("%s+latency-%dms", layout, latencyDur.Milliseconds())
	}
	// 插件装载结果：本次压测结论的有效性以它为前提（驱动跑在独立进程里）。
	fmt.Printf("plugins dir=%s loaded=%d registered=%s\n",
		resolvedDir, len(mgr.Status()), strings.Join(driver.Registered(), ", "))
	fmt.Printf("protocol=%s scan=%dms layout=%s devices=%v points=%v servers=%d workers=%d queue=1024\n",
		pa.name, *scan, layout, devList, ptList, srvCount, runtime.NumCPU()*2)
	// 串口建模的完整折算过程 —— 一个数字（等效时延）会直接决定测出来的容量，
	// 必须让跑的人能自己复核，而不是只看到一个 latency。
	if *serialBaud > 0 {
		fmt.Printf("serial modeled: %dbps 8E1 (%d bits/byte) × %d bytes/frame = %dms line time"+
			" (+ -latency %dms = %dms per frame)\n",
			*serialBaud, uartBitsPerByte, frameBytes,
			lineTime.Milliseconds(), *latency, latencyDur.Milliseconds())
	}

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
