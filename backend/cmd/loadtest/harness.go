// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/testkit/fake"
	"iot-gateway/collector"
	"iot-gateway/configFile"
	dbsqlite "iot-gateway/database/sqlite"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// protocolAdapter 描述一种协议在压测中的表现方式：
// 假服务器工厂 + 设备连接配置生成 + 点位地址名生成 + 数据类型。
// 采集引擎通过 iot_protocol.name（驱动注册名）匹配驱动，name 必须与 driver.Register 完全一致。
type protocolAdapter struct {
	name       string // iot_protocol.name（驱动注册名）
	driverName string // 驱动名（= iot-gateway-drivers 仓库的顶层目录名）；产物名与报错提示都靠它
	newServer  func() (*fake.Server, error)
	// newServerWithLatency 带 RTT 模拟的假服务器工厂。全部 8 个协议都支持 -latency；
	// nil 时 main 会直接报错退出（保留该分支是为了让新加协议忘了实现时立刻暴露）。
	newServerWithLatency func(time.Duration) (*fake.Server, error)
	deviceJSON           func(port int, opts Options) string // 生成指向某假服务器端口的 protocol_json
	addrName             func(i int) string                  // 连续布局：第 i 个点位的地址名
	sparseName           func(i int) string                  // 稀疏布局：第 i 个点位的地址名（间隙 > 合并窗口）
	sparseStride         int                                 // 稀疏步长（地址间隙，用于地址空间上限估算）
	maxSparse            int                                 // 稀疏布局点数上限（地址空间限制，0=不限制）
	dataType             string                              // 点位数据类型（驱动内部类型名）

	// serialFrameBytes 一次「请求 + 应答」在线路上占用的总字节数（含帧定界与校验），
	// 供 -serial-baud 折算等效线路时间。只有帧格式确定的协议才实现它。
	//
	// nil 或返回 0 表示本协议不支持该折算 —— 此时带 -serial-baud 运行会**直接报错
	// 退出**，而不是静默按 0 处理：一个「配了却不生效」的开关会让人以为测的是串口容量，
	// 实际测的还是 loopback，比没有这个开关糟得多。
	//
	// 只对**本框架真的会用的那条协议变体**负责：例如 dlt645 只覆盖 DLT645.TCP 的
	// 2007 方言（harness 的 deviceJSON 从不设 protocolVersion）。
	serialFrameBytes func(opts Options) int
}

// uartBitsPerByte UART 每字节占用的位数为 1 起始 + 8 数据 + 1 校验 + 1 停止。
//
// 固定按 8E1 折算：这是 DL/T 645 电表的出厂默认（parity=E / 8 / 1），也是本框架
// 目前唯一实现了 serialFrameBytes 的协议。其它帧格式（如 Modbus RTU 常见的 8N1
// = 10 位）会差 10%，需要时再把这个位数变成参数。
const uartBitsPerByte = 11

// lineTimeAt 返回在 baud 波特率下传输 n 字节所需的线路时间（单向）。
//
// 纯算术，没有测量不确定性 —— 串口容量的自变量正是它，不是网关侧的处理能力。
func lineTimeAt(baud, n int) time.Duration {
	if baud <= 0 || n <= 0 {
		return 0
	}
	return time.Duration(int64(n) * uartBitsPerByte * int64(time.Second) / int64(baud))
}

// pluginBinary 本协议对应的插件产物名。
//
// 派生而非另存一份：产物名恒为 `iot-driver-<驱动目录名>`（见 build.sh 与
// docs/driver-plugins.md §1.2），目录名与 adapters 的 key 并不总是一致
// （key `mc` → 目录 `mitsubishi`、key `cip` → 目录 `omroncip`），写死两份迟早会对不上。
// Windows 下必须带 .exe：build.sh 与 pluginhost 的测试助手都按平台补后缀。
func (pa protocolAdapter) pluginBinary() string {
	name := "iot-driver-" + filepath.Base(pa.driverName)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

var adapters = map[string]protocolAdapter{
	"modbus": {
		name:                 "ModBus.TCP",
		driverName:           "modbus",
		newServer:            fake.NewModbusTCP,
		newServerWithLatency: fake.NewModbusTCPWithLatency,
		deviceJSON:           func(port int, opts Options) string { return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port) },
		// 传统 Modicon 保持寄存器：4%05d → FC3, 0-based addr = i（连续可合并）
		addrName: func(i int) string { return fmt.Sprintf("4%05d", i+1) },
		// 稀疏：步长 126 > 合并窗口 125，每个点位自成单点区间（1 帧/点）
		sparseName: func(i int) string { return fmt.Sprintf("4%05d", i*126+1) },
		maxSparse:  520, // 寄存器地址空间 65535 / 126
		dataType:   "int16",
	},
	"mc": {
		name:                 "Mitsubishi.MC.TCP",
		driverName:           "mitsubishi",
		newServer:            fake.NewMC3E,
		newServerWithLatency: fake.NewMC3EWithLatency,
		deviceJSON:           func(port int, opts Options) string { return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port) },
		// 字设备 D：连续（默认 maxReadWords=100，逐 100 字一帧）
		addrName: func(i int) string { return fmt.Sprintf("D%d", i) },
		// 稀疏：步长 10 > 默认 maxGap=8，每个点位自成单点区间（1 帧/点）
		sparseName: func(i int) string { return fmt.Sprintf("D%d", i*10) },
		maxSparse:  0, // D 设备地址空间 0xFFFFFF，现实点数远达不到上限
		dataType:   "int16",
	},
	"fins": {
		name:                 "Omron.FINS.TCP",
		driverName:           "fins",
		newServer:            fake.NewFINSTCP,
		newServerWithLatency: fake.NewFINSTCPWithLatency,
		deviceJSON: func(port int, opts Options) string {
			return fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"transport":"TCP"}`, port)
		},
		// 字设备 D（DM 区）：连续（默认 maxReadWords=100，逐 100 字一帧）
		addrName: func(i int) string { return fmt.Sprintf("D%d", i) },
		// 稀疏：步长 101 > 合并窗口 100，每个点位自成单点区间
		sparseName: func(i int) string { return fmt.Sprintf("D%d", i*101) },
		maxSparse:  0, // D 字地址 0xFFFF，现实点数远达不到上限
		dataType:   "int16",
	},
	"s7": {
		name:                 "Siemens.S7",
		driverName:           "s7",
		newServer:            fake.NewS7,
		newServerWithLatency: fake.NewS7WithLatency,
		deviceJSON: func(port int, opts Options) string {
			return fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"rack":"0","slot":"1"}`, port)
		},
		// DB1 字节区：连续（gos7 按 PDU 480 分块，每块 ≤462 字节）
		addrName: func(i int) string { return fmt.Sprintf("DB1.DBB%d", i) },
		// 稀疏：步长 10 > 默认 maxGap=8，每个点位自成单点区间
		sparseName: func(i int) string { return fmt.Sprintf("DB1.DBB%d", i*10) },
		maxSparse:  0, // DB 字节地址 24 位，现实点数远达不到上限
		dataType:   "byte",
	},
	"cip": {
		name:                 "Omron.CIP",
		driverName:           "omroncip",
		newServer:            fake.NewCIP,
		newServerWithLatency: fake.NewCIPWithLatency,
		deviceJSON: func(port int, opts Options) string {
			// CIP 批量读：-cip-batch N>0 时注入 maxTagsPerRequest（0x0A 多服务报文），
			// N=1 即逐标签单读（驱动 config.go 判的是 >0，注 1 就落回单读）。
			// 不传（0）则整个字段不出现 = 用驱动默认值 —— 注意驱动默认是 64（批量开着），
			// 与 omroncipdrv/config.go 注释及本文档旧口径「默认 0 = 单读」不符，见 README。
			if opts.CIPBatch > 0 {
				return fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"maxTagsPerRequest":%d}`, port, opts.CIPBatch)
			}
			return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port)
		},
		// 每点一个独立标签（0x4C 读服务按标签名读，无区间合并概念），连续=稀疏
		addrName:   func(i int) string { return fmt.Sprintf("Tag_%d", i) },
		sparseName: func(i int) string { return fmt.Sprintf("Tag_%d", i) },
		maxSparse:  0,
		dataType:   "int16",
	},
	"rockwell": {
		name:                 "Rockwell.CIP",
		driverName:           "rockwellcip",
		newServer:            fake.NewCIPAB,
		newServerWithLatency: fake.NewCIPABWithLatency,
		deviceJSON: func(port int, opts Options) string {
			return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port)
		},
		// 连续布局：同一基数组的下标点位，驱动合并为 ≤125 元素/帧的 Read Tag Elements
		addrName: func(i int) string { return fmt.Sprintf("Arr[%d]", i) },
		// 稀疏布局：独立标签，每点 1 次 0x4C 串行事务（最坏情况）
		sparseName: func(i int) string { return fmt.Sprintf("Tag_%d", i) },
		maxSparse:  0,
		dataType:   "int16",
	},
	"opcua": {
		name:                 "OPC.UA",
		driverName:           "opcua",
		newServer:            fake.NewOpcUa,
		newServerWithLatency: fake.NewOpcUaWithLatency,
		deviceJSON: func(port int, opts Options) string {
			// -opcua-batch N>1 时注入 maxBatch（单 ReadRequest 节点数，驱动默认 100）
			if opts.OpcUaBatch > 0 {
				return fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"maxBatch":%d}`, port, opts.OpcUaBatch)
			}
			return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port)
		},
		// 连续布局：ns=1;i=<n> 数值 NodeID 直接解析（无 I/O），驱动按 maxBatch
		// 分批 Read（默认 100 节点/帧）。「连续」与「稀疏」对 OPC UA 均为
		// 100 节点/帧的批量读，区别仅在帧内 NodeID 是否连续（对服务器寻址
		// 效率有影响，对网关侧帧数无影响），故 sparse 复用同一地址公式。
		addrName:   func(i int) string { return fmt.Sprintf("ns=1;i=%d", i+1001) },
		sparseName: func(i int) string { return fmt.Sprintf("ns=1;i=%d", i+1001) },
		maxSparse:  0,
		dataType:   "int32",
	},
	"dlt645": {
		name:                 "DLT645.TCP",
		driverName:           "dlt645",
		newServer:            fake.NewDLT645,
		newServerWithLatency: fake.NewDLT645WithLatency,
		deviceJSON: func(port int, opts Options) string {
			// 只注入显式传了的键：字段缺席时驱动用自己的默认值，这与现场
			// 「表单没填就走默认」的行为一致，压测才不会测出一个不存在的配置。
			parts := []string{`"host":"127.0.0.1"`, fmt.Sprintf(`"port":%d`, port)}
			// maxDIsPerRead：单请求打包的数据标识个数（驱动上限 12）。
			// 不传则用驱动默认值 12（协议上限）——帧数 = ⌈点数/本值⌉。
			// 传 1 可复现「一帧一个点」的保守配置（2026-09 之前的出厂默认值）。
			if opts.DLT645DIs > 0 {
				parts = append(parts, fmt.Sprintf(`"maxDIsPerRead":%d`, opts.DLT645DIs))
			}
			// interFrameDelayMs：发送后、开始读应答前的等待（TCP 驱动默认 0，串口 30）。
			// 30ms 是 RS-485 收发器换向时间的预留，对 TCP 透传链路是纯空转 ——
			// 它的代价随帧数线性放大，故 TCP 侧已归零。见 README 帧数模型。
			if opts.DLT645InterFrameMs >= 0 {
				parts = append(parts, fmt.Sprintf(`"interFrameDelayMs":%d`, opts.DLT645InterFrameMs))
			}
			return "{" + strings.Join(parts, ",") + "}"
		},
		// 地址即数据标识，显式声明「4 字节 / 2 位小数」。字典只收录 44 个常用项，
		// 而压测要造到 5000 点，故一律走 `DI:字节数:小数位` 形式。
		// 基址 0x10000000 刻意避开字典收录的段（0000xxxx / 0201xxxx / 0400xxxx）：
		// 撞上字典条目会命中各自不同的字节数，与假表的定长应答错开。
		addrName: func(i int) string { return fmt.Sprintf("%08X:%d:2", 0x10000000+i, dlt645DataBytes) },
		// 645 没有区间合并的概念：帧数只由 maxDIsPerRead 决定，地址连不连续无所谓，
		// 故稀疏与连续同址（处理方式同 Omron.CIP / OPC.UA 的 sparse）。
		// 也就是说本协议的「帧数自变量」是 -dlt645-dis，不是 -sparse。
		sparseName:       func(i int) string { return fmt.Sprintf("%08X:%d:2", 0x10000000+i, dlt645DataBytes) },
		maxSparse:        0, // 数据标识 4 字节，现实点数远达不到上限
		dataType:         "float",
		serialFrameBytes: dlt645SerialFrameBytes,
	},
}

// DL/T 645-2007 帧布局常量（与 iot-gateway-drivers 仓库的 dlt645/dlt645drv/codec.go 对齐）。
const (
	// dlt645FrameOverhead 帧定界开销：起始(1)+地址域(6)+起始(1)+控制码(1)+长度(1)+校验(1)+结束(1)。
	dlt645FrameOverhead = 12
	// dlt645DIBytes 2007 版数据标识的长度（1997 版为 2，本框架不覆盖）。
	dlt645DIBytes = 4
	// dlt645DataBytes 每个数据标识的应答数据字节数，即地址 `%08X:4:2` 里声明的那个 4。
	// 改这里必须同步改 testutil/fake 的 dltDefaultDataSize —— 两者是一对约定。
	dlt645DataBytes = 4
	// dlt645DefaultDIs 驱动出厂默认的 maxDIsPerRead（协议上限）。
	// 与 iot-gateway-drivers 仓库 dlt645/dlt645drv 的 defaultMaxDIsPerRead 是同一个值：压测不带
	// -dlt645-dis 时驱动实际用的就是它，两边不一致会算出偏小的线路时间。
	dlt645DefaultDIs = 12
)

// dlt645SerialFrameBytes 返回 645 一次读事务「请求 + 应答」的线路字节数。
//
// 2007 版读命令的数据域是「数据标识 + 数据」的重复，请求只带 N 个数据标识、
// 应答每个标识再附 dataBytes 字节数据：
//
//	请求 = 12 + 4N，应答 = 12 + N*(4 + dataBytes)
func dlt645SerialFrameBytes(opts Options) int {
	n := opts.DLT645DIs
	if n <= 0 {
		n = dlt645DefaultDIs
	}
	return 2*dlt645FrameOverhead + n*dlt645DIBytes + n*(dlt645DIBytes+dlt645DataBytes)
}

// countingSink 实现 collector.RecordSink，只计数不阻塞，量化采集引擎实际产出的记录量。
type countingSink struct {
	mu    sync.Mutex
	total int64
	good  int64
}

func (s *countingSink) PushRecords(recs []collector.CollectedRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total += int64(len(recs))
	for _, r := range recs {
		if r.Quality == driver.QualityGood {
			s.good++
		}
	}
}

func (s *countingSink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total, s.good = 0, 0
}

func (s *countingSink) counts() (total, good int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.good
}

// Options 压测参数。
type Options struct {
	Protocol   string // 驱动注册名（仅用于展示）
	Devices    []int  // 设备数矩阵
	Points     []int  // 单设备点位矩阵
	ScanMs     int    // 采集频率（毫秒）
	RunSec     time.Duration
	Warmup     time.Duration
	Sparse     bool
	CIPBatch   int // CIP 0x0A 多服务批量读：每个报文携带标签数（>1 启用，0/1=单读）
	OpcUaBatch int // OPC UA 单 ReadRequest 节点数（0=驱动默认 100）
	// DLT645DIs DL/T 645 单请求打包的数据标识个数（0=不注入，用驱动默认 1，上限 12）。
	DLT645DIs int
	// DLT645InterFrameMs DL/T 645 收发间延时毫秒数。
	// <0 = 不注入（用驱动默认 30ms）；>=0 = 注入该值，其中 0 是**有意义的取值**（关闭延时），
	// 因此这里不能用 0 当「不注入」的哨兵，与 DLT645DIs 的约定不同。
	DLT645InterFrameMs int
	Workers            int // worker 池并发数（0=默认 NumCPU*2）
}

// seedDB 用真实迁移建库并造数：1 个协议 + devices 台设备 + devices×points 个点位。
// 设备轮询分配到服务器池（round-robin）。返回 DB 与临时文件路径（调用方负责关闭与清理）。
func seedDB(pa protocolAdapter, opts Options, servers []*fake.Server, devices, points int) (db *gorm.DB, dbPath string, err error) {
	dbPath = filepath.Join(os.TempDir(), fmt.Sprintf("iotgw-loadtest-%d-%d.db", os.Getpid(), time.Now().UnixNano()))
	db = dbsqlite.InitDB(&configFile.Config{
		SQLite: configFile.SQLiteConfig{Path: dbPath},
		Log:    configFile.LogConfig{Level: "WARN"},
	})
	if db == nil {
		return nil, "", fmt.Errorf("init sqlite db failed")
	}
	// 静音 GORM 默认 logger（慢 SQL 警告会打到 stdout 且不受 appLogger 级别控制），
	// 造数与 checksum 全表查询必然触发慢 SQL，刷屏且无诊断价值。
	db.Logger = gormlogger.Default.LogMode(gormlogger.Silent)

	// 稀疏布局受协议地址空间限制，超限时钳制并提示
	n := points
	if opts.Sparse && pa.maxSparse > 0 && n > pa.maxSparse {
		fmt.Printf("  [warn] %s sparse layout: points %d capped at %d (address space limit)\n",
			pa.name, n, pa.maxSparse)
		n = pa.maxSparse
	}

	// 协议行：正常部署下由插件加载时供给（见 pluginhost/provision.go），这里按名复用；
	// 压测不拉起插件进程，只有合成协议，故缺行时自行插入。
	// 名称必须与驱动注册名一致（driver.RegisterExternal），采集引擎按此名创建驱动。
	var proto po.IotProtocol
	protoID := ""
	if err := db.Where("name = ?", pa.name).First(&proto).Error; err != nil {
		proto = po.IotProtocol{ID: "proto-loadtest", Name: pa.name}
		if err := db.Create(&proto).Error; err != nil {
			return nil, "", fmt.Errorf("seed protocol: %w", err)
		}
	}
	protoID = proto.ID

	devs := make([]po.Device, 0, devices)
	addrs := make([]po.DeviceAddress, 0, devices*n)
	for d := 0; d < devices; d++ {
		srv := servers[d%len(servers)]
		devID := fmt.Sprintf("dev-%d", d)
		devs = append(devs, po.Device{
			ID:           devID,
			Name:         devID,
			ProtocolID:   protoID,
			ProtocolJSON: pa.deviceJSON(srv.Port(), opts),
			Status:       1,
		})
		for i := 0; i < n; i++ {
			an := pa.addrName(i)
			if opts.Sparse {
				an = pa.sparseName(i)
			}
			addrs = append(addrs, po.DeviceAddress{
				ID:            fmt.Sprintf("%s-a%d", devID, i),
				DeviceID:      devID,
				Name:          an,
				Label:         an,
				DataType:      pa.dataType,
				RwPermission:  "R",
				ScanFrequency: opts.ScanMs,
				Status:        1,
			})
		}
	}
	if err := db.Create(&devs).Error; err != nil {
		return nil, "", fmt.Errorf("seed devices: %w", err)
	}
	if err := db.CreateInBatches(&addrs, 2000).Error; err != nil {
		return nil, "", fmt.Errorf("seed addresses: %w", err)
	}
	return db, dbPath, nil
}

// caseResult 单次压测（一组 设备数×点数）的结果。
type caseResult struct {
	devices       int
	points        int
	expected      int64   // 测量窗口内的预期记录数
	actual        int64   // 实际记录数
	good          int64   // Quality=192 的记录数
	dropPct       float64 // 掉点率（0-100）
	peakHeapMB    uint64
	peakGoroutine int
	errCount      int
	cycleMs       float64 // dev-0 观测到的轮询周期均值（毫秒）
	fps           float64 // 每秒实际记录数
}

// runCase 跑一组 (devices, points)：建库造数 → 起采集引擎 → 预热 → 测量窗口采样 → 停止。
func runCase(pa protocolAdapter, opts Options, servers []*fake.Server, devices, points int) (*caseResult, error) {
	db, dbPath, err := seedDB(pa, opts, servers, devices, points)
	if err != nil {
		return nil, err
	}
	defer func() {
		if sqlDB, e := db.DB(); e == nil {
			sqlDB.Close()
		}
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
	}()

	// 与 wire.go 相同的池参数：默认 NumCPU*2 并发 + 1024 队列；-workers 覆盖并发数
	workers := runtime.NumCPU() * 2
	if opts.Workers > 0 {
		workers = opts.Workers
	}
	pool := workerPool.NewWorkerPool(workers, 1024)
	sink := &countingSink{}
	// stateSink / pointSink 传 nil：压测只关心吞吐，不需要报警状态机
	engine := collector.NewEngine(db, pool, sink, nil, nil, nil)
	if err := engine.Start(); err != nil {
		return nil, fmt.Errorf("engine start: %w", err)
	}

	// 预热：让首轮连接、区间缓存构建等就绪
	time.Sleep(opts.Warmup)

	// 测量窗口：清空预热计数，采样器记录堆/协程/错误/设备轮询周期
	sink.reset()
	var (
		mu            sync.Mutex
		peakHeap      uint64
		peakGoroutine uint64
		errCount      int
		cycSum, cycN  float64
		lastSuccess   time.Time
	)
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				g := runtime.NumGoroutine()
				st := engine.GetStatus()
				ec := 0
				if len(st) > 0 {
					ec = st[0].ErrorCount
				}
				// dev-0 成功时间差分近似轮询周期
				if ts := engine.DeviceLastSuccessTime("dev-0"); ts != "" {
					if t0, perr := time.Parse("2006-01-02 15:04:05", ts); perr == nil {
						if !lastSuccess.IsZero() {
							if d := t0.Sub(lastSuccess); d > 0 {
								cycSum += float64(d.Milliseconds())
								cycN++
							}
						}
						lastSuccess = t0
					}
				}
				mu.Lock()
				if ms.HeapAlloc > peakHeap {
					peakHeap = ms.HeapAlloc
				}
				if uint64(g) > peakGoroutine {
					peakGoroutine = uint64(g)
				}
				if ec > errCount {
					errCount = ec
				}
				mu.Unlock()
			}
		}
	}()

	start := time.Now()
	time.Sleep(opts.RunSec)
	window := time.Since(start).Seconds()
	close(stop)

	engine.Stop()
	total, good := sink.counts()

	// 预期记录数 = 设备数 × 点数 × (窗口内轮询轮数)
	expected := float64(devices*points) * (window * 1000 / float64(opts.ScanMs))
	drop := 1.0 - float64(total)/expected
	if drop < 0 {
		drop = 0
	}

	var cycleMs float64
	if cycN > 0 {
		cycleMs = cycSum / cycN
	}
	return &caseResult{
		devices:       devices,
		points:        points,
		expected:      int64(expected),
		actual:        total,
		good:          good,
		dropPct:       drop * 100,
		peakHeapMB:    peakHeap / 1024 / 1024,
		peakGoroutine: int(peakGoroutine),
		errCount:      errCount,
		cycleMs:       cycleMs,
		fps:           float64(total) / window,
	}, nil
}

// sweep 按 (设备数 × 点数) 矩阵逐组压测并打印结果表。
func sweep(pa protocolAdapter, opts Options, servers []*fake.Server) error {
	fmt.Printf("%-7s %-9s %-9s %-12s %-12s %-7s %-8s %-9s %-7s %-7s %-6s\n",
		"devices", "points", "total", "expected/s", "actual/s", "drop%", "heapMB", "goroutines", "errors", "cycleMs", "good%")
	for _, devs := range opts.Devices {
		for _, pts := range opts.Points {
			res, err := runCase(pa, opts, servers, devs, pts)
			if err != nil {
				fmt.Printf("%-7d %-9d %-9d  ERROR: %v\n", devs, pts, devs*pts, err)
				continue
			}
			expectedPerSec := float64(res.expected) / windowSeconds(opts)
			goodPct := 100.0
			if res.actual > 0 {
				goodPct = float64(res.good) * 100 / float64(res.actual)
			}
			fmt.Printf("%-7d %-9d %-9d %-12.0f %-12.0f %-7.1f %-8d %-9d %-7d %-7.1f %-6.1f\n",
				res.devices, res.points, res.devices*res.points,
				expectedPerSec, res.fps, res.dropPct,
				res.peakHeapMB, res.peakGoroutine, res.errCount, res.cycleMs, goodPct)
		}
	}
	return nil
}

func windowSeconds(opts Options) float64 {
	return opts.RunSec.Seconds()
}
