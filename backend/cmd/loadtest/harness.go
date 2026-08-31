package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"iot-gateway/collector"
	"iot-gateway/configFile"
	dbsqlite "iot-gateway/database/sqlite"
	"iot-gateway/model/po"
	"iot-gateway/testutil/fake"
	"iot-gateway/workerPool"
)

// protocolAdapter 描述一种协议在压测中的表现方式：
// 假服务器工厂 + 设备连接配置生成 + 点位地址名生成 + 数据类型。
// 采集引擎通过 iot_protocol.name（驱动注册名）匹配驱动，name 必须与 driver.Register 完全一致。
type protocolAdapter struct {
	name      string // iot_protocol.name（驱动注册名）
	newServer func() (*fake.Server, error)
	// newServerWithLatency 带 RTT 模拟的假服务器工厂（可选，nil = 该协议不支持 -latency）
	newServerWithLatency func(time.Duration) (*fake.Server, error)
	deviceJSON           func(port int, opts Options) string // 生成指向某假服务器端口的 protocol_json
	addrName             func(i int) string                  // 连续布局：第 i 个点位的地址名
	sparseName           func(i int) string                  // 稀疏布局：第 i 个点位的地址名（间隙 > 合并窗口）
	sparseStride         int                                 // 稀疏步长（地址间隙，用于地址空间上限估算）
	maxSparse            int                                 // 稀疏布局点数上限（地址空间限制，0=不限制）
	dataType             string                              // 点位数据类型（驱动内部类型名）
}

var adapters = map[string]protocolAdapter{
	"modbus": {
		name:       "ModBus.TCP",
		newServer:  fake.NewModbusTCP,
		deviceJSON: func(port int, opts Options) string { return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port) },
		// 传统 Modicon 保持寄存器：4%05d → FC3, 0-based addr = i（连续可合并）
		addrName: func(i int) string { return fmt.Sprintf("4%05d", i+1) },
		// 稀疏：步长 126 > 合并窗口 125，每个点位自成单点区间（1 帧/点）
		sparseName: func(i int) string { return fmt.Sprintf("4%05d", i*126+1) },
		maxSparse:  520, // 寄存器地址空间 65535 / 126
		dataType:   "int16",
	},
	"mc": {
		name:       "Mitsubishi.MC.TCP",
		newServer:  fake.NewMC3E,
		deviceJSON: func(port int, opts Options) string { return fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port) },
		// 字设备 D：连续（默认 maxReadWords=100，逐 100 字一帧）
		addrName: func(i int) string { return fmt.Sprintf("D%d", i) },
		// 稀疏：步长 10 > 默认 maxGap=8，每个点位自成单点区间（1 帧/点）
		sparseName: func(i int) string { return fmt.Sprintf("D%d", i*10) },
		maxSparse:  0, // D 设备地址空间 0xFFFFFF，现实点数远达不到上限
		dataType:   "int16",
	},
	"fins": {
		name:      "Omron.FINS.TCP",
		newServer: fake.NewFINSTCP,
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
		name:      "Siemens.S7",
		newServer: fake.NewS7,
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
		newServer:            fake.NewCIP,
		newServerWithLatency: fake.NewCIPWithLatency,
		deviceJSON: func(port int, opts Options) string {
			// CIP 批量读：-cip-batch N>1 时注入 maxTagsPerRequest（0x0A 多服务报文）
			if opts.CIPBatch > 1 {
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
		if r.Quality == 192 {
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
	Protocol string // 驱动注册名（仅用于展示）
	Devices  []int  // 设备数矩阵
	Points   []int  // 单设备点位矩阵
	ScanMs   int    // 采集频率（毫秒）
	RunSec   time.Duration
	Warmup   time.Duration
	Sparse   bool
	CIPBatch int // CIP 0x0A 多服务批量读：每个报文携带标签数（>1 启用，0/1=单读）
	Workers  int // worker 池并发数（0=默认 NumCPU*2）
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

	// 协议行：迁移已内置标准协议（如 ModBus.TCP），按名复用；未内置的协议才插入。
	// 名称必须与驱动注册名一致（driver.Register），采集引擎按此名创建驱动。
	var proto po.IotProtocol
	protoID := ""
	if err := db.Where("name = ?", pa.name).First(&proto).Error; err != nil {
		proto = po.IotProtocol{ID: "proto-loadtest", Name: pa.name, Status: 1}
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
	engine := collector.NewEngine(db, pool, sink, nil)
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
