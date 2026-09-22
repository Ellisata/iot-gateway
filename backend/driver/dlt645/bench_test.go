// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/testutil/fake"
)

// TestMain 把日志静音到 ERROR 并重定向到临时目录。
//
// 这不是「降噪」，而是**测量正确性的前提**：读取热路径上每帧都有
// logger.Debug("dlt645: 发送 raw=% X", frame)（codec.go 的 exchange），
// 测试默认按 backend/default.yaml 是 DEBUG 级，benchmark 会实测成
// 「格式化 + 写日志文件」的速度，而不是帧编解码的速度。
// 临时目录是为了不让测试运行在包目录下建出 log/。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dlt645-bench")
	if err != nil {
		os.Exit(m.Run())
	}
	cfg := &configFile.Config{}
	cfg.Log.Level = "ERROR"
	cfg.Log.OutputDir = dir
	logger.InitLogger(cfg)

	code := m.Run()

	logger.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

// benchDIs 典型配电房点表用到的数据标识，覆盖解码的各个分支：
// 4 字节电量、2 字节电压、3 字节有符号电流、3 字节有符号功率、
// 2 字节二进制状态字、4 字节日期（布局解码）。
var benchDIs = []string{
	"00000000", "00010000", "00020000", "00030000",
	"02010100", "02010200", "02010300",
	"02020100", "02020200", "02020300",
	"02030000", "02030100", "02030200", "02030300",
	"02050000", "02050100", "02800001",
	"04000101", "04000501",
}

// benchAddrs 生成 n 个点位（循环使用典型数据标识），模拟一台表的完整点表。
func benchAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := 0; i < n; i++ {
		addrs[i] = po.DeviceAddress{
			ID:       "addr-" + strconv.Itoa(i),
			Name:     benchDIs[i%len(benchDIs)],
			DataType: "float",
		}
	}
	return addrs
}

// benchTransport 固定应答的传输层：读缓冲耗尽后回卷，让同一个应答可被反复交换
// （benchmark 每轮都要发一次请求、收一次应答）。
// Write 不累积输出——否则 b.N 次写入会把内存撑爆，测出的是分配器压力。
type benchTransport struct {
	in  []byte
	pos int
}

func (t *benchTransport) Lock()                           {}
func (t *benchTransport) Unlock()                         {}
func (t *benchTransport) Drain()                          {}
func (t *benchTransport) MarkDirty()                      {}
func (t *benchTransport) SetReadDeadline(time.Time) error { return nil }
func (t *benchTransport) IsConnected() bool               { return true }
func (t *benchTransport) Close() error                    { return nil }

func (t *benchTransport) Write(p []byte) (int, error) { return len(p), nil }

func (t *benchTransport) Read(p []byte) (int, error) {
	if t.pos >= len(t.in) {
		t.pos = 0 // 应答回卷：模拟「每轮都得到同样的应答」
	}
	n := copy(p, t.in[t.pos:])
	t.pos += n
	return n, nil
}

// benchMeterTransport 按收到的请求实时合成应答，让 driver.Read 的全链路
// （规划 → 编码 → 交换 → 解析 → 解码 → 回填）能在脱离硬件的情况下跑完整。
// 它不做任何 I/O 等待，因此测到的是纯网关侧 CPU 成本。
type benchMeterTransport struct {
	cfg  *DLT645Config
	addr [6]byte
	resp []byte
	// diLen 数据标识 → 数据字节数。构造应答时按它给出每个标识的真实长度，
	// 且预先算好、不放进被测量的循环里（否则测到的是基准自己解析地址的速度）。
	diLen map[uint32]int
}

// dataLenOf 返回数据标识对应的数据字节数（未收录的按 4 字节）。
func (t *benchMeterTransport) dataLenOf(di uint32) int {
	if t.diLen == nil {
		t.diLen = make(map[uint32]int, len(benchDIs))
		for _, text := range benchDIs {
			spec, err := ParseAddress(text, Version2007)
			if err != nil {
				continue
			}
			t.diLen[spec.DI] = spec.Bytes
		}
	}
	if n, ok := t.diLen[di]; ok {
		return n
	}
	return 4
}

func (t *benchMeterTransport) Lock()                           {}
func (t *benchMeterTransport) Unlock()                         {}
func (t *benchMeterTransport) Drain()                          {}
func (t *benchMeterTransport) MarkDirty()                      {}
func (t *benchMeterTransport) SetReadDeadline(time.Time) error { return nil }
func (t *benchMeterTransport) IsConnected() bool               { return true }
func (t *benchMeterTransport) Close() error                    { return nil }

func (t *benchMeterTransport) Write(req []byte) (int, error) {
	t.resp = t.reply(req)
	return len(req), nil
}

func (t *benchMeterTransport) Read(p []byte) (int, error) {
	return copy(p, t.resp), nil
}

// reply 从请求帧还原数据标识列表，回一个「数据标识 + 该标识真实长度的 BCD 数据」的应答。
//
// 每个数据标识的数据字节数**必须按该标识自身的规格**（spec.Bytes）给出，
// 不能一律 4 字节：真实电表就是这么回的，而驱动也据此切分数据域。
// 用固定长度会让混合点表（4 字节电量 + 2 字节电压 + 3 字节电流）在第一个
// 长度不符的标识处开始错位，其后同组的点位全部判为 Quality=0——
// 那样测到的是错误路径（problemLog + 日志格式化），而不是批量读取的成本。
func (t *benchMeterTransport) reply(req []byte) []byte {
	raw := make([]byte, len(req)-frameOverhead)
	copy(raw, req[10:len(req)-2])
	unoffsetData(raw) // 还原为未偏移的数据域，buildFrame 会重新施加偏移

	diBytes := t.cfg.DIBytes()
	out := make([]byte, 0, len(raw)*2)
	for p := 0; p+diBytes <= len(raw); p += diBytes {
		out = append(out, raw[p:p+diBytes]...)
		out = append(out, bcdPayload(t.dataLenOf(readDI(raw[p:p+diBytes])))...)
	}
	return buildFrame(t.cfg, t.addr, ctrl2007OK, out)
}

// bcdPayload 造 n 字节合法 BCD 数据（值为 1.28 的尾部对齐形式）。
func bcdPayload(n int) []byte {
	out := make([]byte, n)
	if n > 0 {
		out[0] = 0x28
	}
	if n > 1 {
		out[1] = 0x01
	}
	return out
}

// benchConfig 返回一份适合基准测试的配置：帧间延时清零。
//
// 默认 30ms 的收发间延时是真实部署下容量的主导项之一（见 specs/capacity-testing.md），
// 但它是 sleep 而非 CPU 成本，留着会把所有 benchmark 都压成同一个数。
func benchConfig(maxDIs int) *DLT645Config {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	cfg.PreambleBytes = 0
	cfg.InterFrameDelay = 0
	cfg.MaxDIsPerRead = maxDIs
	return cfg
}

// ==================== 帧级：编码 / 解析 / 解码 ====================

// BenchmarkBuildReadFrame 请求帧组装成本（含数据域 +0x33 偏移与校验和）。
func BenchmarkBuildReadFrame(b *testing.B) {
	cfg := benchConfig(1)
	addr, _ := MeterAddressBytes("000000000001")

	for _, n := range []int{1, 12} {
		specs := make([]DISpec, n)
		for i := range specs {
			specs[i], _ = ParseAddress(benchDIs[i%len(benchDIs)], Version2007)
		}
		b.Run(fmt.Sprintf("%dDI", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = buildReadFrame(cfg, addr, specs)
			}
		})
	}
}

// BenchmarkParseResponse 解析一个已定位的应答帧（4 字节数据标识 + 4 字节数据）。
//
// parseAt 是**单点**解析：多字节前导/噪声对其调用方（readResponse）是逐字节重试，
// 那部分成本由 BenchmarkReadResponseChunked 覆盖，此处只测帧本身最坏情况（含校验和全字节求和）。
func BenchmarkParseResponse(b *testing.B) {
	cfg := benchConfig(1)
	addr, _ := MeterAddressBytes("000000000001")
	frame := buildFrame(cfg, addr, ctrl2007OK, []byte{0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, state := parseAt(frame, 0, cfg, addr); state != parseFound {
			b.Fatalf("parseAt state = %d, want parseFound", state)
		}
	}
}

// BenchmarkReadResponseChunked 分片投递下的读应答成本（DTU / 串口服务器最坏情况）。
//
// 逐字节到达时每读一字节都要重扫一遍缓冲，是解析路径的最坏情况；
// 现场大量串口服务器按 1~N 字节分片投递，因此这个数比整帧到达更接近实际。
func BenchmarkReadResponseChunked(b *testing.B) {
	cfg := benchConfig(1)
	addr, _ := MeterAddressBytes("000000000001")
	full := buildFrame(cfg, addr, ctrl2007OK, []byte{0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00})

	for _, chunk := range []int{0, 8, 1} { // 0 = 整帧一次到达
		name := "wholeFrame"
		if chunk > 0 {
			name = fmt.Sprintf("chunk%d", chunk)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tr := &chunkedTransport{benchTransport: &benchTransport{in: full}, chunk: chunk}
				if _, err := readResponse(tr, cfg, addr, time.Now().Add(time.Second)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// chunkedTransport 按 chunk 字节分片投递（chunk<=0 时整块投递）。
type chunkedTransport struct {
	*benchTransport
	chunk int
}

func (t *chunkedTransport) Read(p []byte) (int, error) {
	if t.chunk > 0 && len(p) > t.chunk {
		p = p[:t.chunk]
	}
	return t.benchTransport.Read(p)
}

// BenchmarkExchange 一次完整请求-应答的网关侧固定成本：
// 清缓冲 → 编码 → 发送 → 解析应答 → 控制码分派。
//
// 不含帧间延时（benchConfig 已清零）与线路往返，因此这是**每帧的地板成本**：
// 真实链路上每帧还要叠加 (请求+应答字节数)/波特率 + 表处理时间。
func BenchmarkExchange(b *testing.B) {
	cfg := benchConfig(1)
	addr, _ := MeterAddressBytes("000000000001")
	spec, _ := ParseAddress(benchDIs[0], Version2007)
	req := buildReadFrame(cfg, addr, []DISpec{spec})
	resp := buildFrame(cfg, addr, ctrl2007OK, []byte{0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00})
	tr := &benchTransport{in: resp}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.pos = 0
		if _, err := exchange(tr, cfg, addr, req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeValue 数值解码（BCD / 二进制 / 时间布局各自的分支）。
func BenchmarkDecodeValue(b *testing.B) {
	cases := []struct {
		name     string
		addr     string
		internal string
	}{
		{"BCD4_energy", "00000000", "float"},         // 4 字节 BCD，2 位小数
		{"BCD3_signed_current", "02020100", "float"}, // 3 字节有符号 BCD，3 位小数
		{"Binary2_status", "04000501", "bcd"},        // 2 字节二进制
		{"Date_layout", "04000101", "datetime"},      // 4 字节日期布局
		{"String_addr", "04000401", "string"},        // 6 字节 BCD 数字串
	}
	for _, c := range cases {
		spec, err := ParseAddress(c.addr, Version2007)
		if err != nil {
			b.Fatalf("parse %s: %v", c.addr, err)
		}
		raw := make([]byte, spec.Bytes)
		if spec.Layout == layoutDate {
			raw = []byte{0x01, 0x05, 0x03, 0x26} // 周一 2026-03-05
		} else if !spec.Binary {
			raw[0] = 0x28 // BCD 合法位，避免走到错误返回路径
			raw[1] = 0x01
		} else {
			raw[0] = 0x01
		}
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := DecodeValue(raw, &spec, c.internal); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ==================== 规划缓存 ====================

// BenchmarkPlanCacheHit 批次内容指纹命中时的规划获取（热路径每轮都会走一次）。
func BenchmarkPlanCacheHit(b *testing.B) {
	cfg := benchConfig(1)
	addrs := benchAddrs(500)
	var c planCache
	sig := driver.RangeSig(addrs)
	c.planFor(sig, addrs, cfg) // 预热缓存

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p := c.planFor(sig, addrs, cfg); p == nil {
			b.Fatal("nil plan")
		}
	}
}

// BenchmarkBuildPlan 规划计算成本（仅缓存未命中时执行一次）。
func BenchmarkBuildPlan(b *testing.B) {
	cfg := benchConfig(1)
	addrs := benchAddrs(500)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildPlan(addrs, cfg)
	}
}

// ==================== 驱动整链路 ====================

// BenchmarkDriverRead 驱动 Read 全链路（规划缓存命中 + 逐组交换 + 解码 + 回填），
// 传输层为内存应答，不含线路时间。逐维度扫描点位规模与单请求打包数。
func BenchmarkDriverRead(b *testing.B) {
	for _, maxDIs := range []int{1, 12} {
		for _, points := range []int{10, 100, 1000} {
			b.Run(fmt.Sprintf("points%d_maxDIs%d", points, maxDIs), func(b *testing.B) {
				cfg := benchConfig(maxDIs)
				addr, _ := MeterAddressBytes("000000000001")
				d := &dlt645Driver{
					transport: TransportTCP,
					config:    cfg,
					meterAddr: addr,
					client:    &benchMeterTransport{cfg: cfg, addr: addr},
				}
				addrs := benchAddrs(points)
				if _, err := d.Read(addrs); err != nil { // 预热：构建并缓存规划
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					res, err := d.Read(addrs)
					if err != nil {
						b.Fatal(err)
					}
					if res[0].Quality != 192 {
						b.Fatalf("quality = %d, want 192（基准必须走成功路径）", res[0].Quality)
					}
				}
			})
		}
	}
}

// BenchmarkDriverReadParallel 并发 Read（TCP 传输非独占，采集引擎允许并发调用，
// 请求-应答由传输层互斥锁串行化）。用于观察锁竞争随并发度的退化。
func BenchmarkDriverReadParallel(b *testing.B) {
	cfg := benchConfig(12)
	addr, _ := MeterAddressBytes("000000000001")
	d := &dlt645Driver{
		transport: TransportTCP,
		config:    cfg,
		meterAddr: addr,
		client:    &benchMeterTransport{cfg: cfg, addr: addr},
	}
	addrs := benchAddrs(100)
	if _, err := d.Read(addrs); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := d.Read(addrs); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// benchEndToEndAddrs 造 n 个「数据标识 + 字节数 + 小数位」形式的点位地址，
// 与压测造数（testutil/fake 的 %08X:4:2 约定）一致。
//
// 不复用 benchAddrs：那份是给进程内替身用的固定点表，地址不带字节数规格，
// 而这里的假表按地址里声明的字节数回数据，两者必须对上。
func benchEndToEndAddrs(n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, n)
	for i := range addrs {
		addrs[i] = po.DeviceAddress{
			ID:       "id-" + strconv.Itoa(i),
			Name:     fmt.Sprintf("%08X:4:2", 0x10000000+i),
			DataType: "float",
		}
	}
	return addrs
}

// BenchmarkReadTCP 测「一整批点位读一遍」的耗时：对 5000 点 / maxDIsPerRead=12
// 即 417 帧，用 ns/op ÷ 417 得到**单帧往返成本**——这是 645 容量的唯一自变量
// （帧数 = ⌈点数/maxDIsPerRead⌉）。
//
// 与上面的 BenchmarkDriverRead* 不同，这里走真实 TCP 与真实假表，
// 因此覆盖到传输层：Drain 的空等、收发间延时、TCP 往返，这些恰恰是
// 单帧成本的大头（进程内替身测不到）。
func BenchmarkReadTCP(b *testing.B) {
	srv, err := fake.NewDLT645()
	if err != nil {
		b.Fatalf("启动假表失败: %v", err)
	}
	defer srv.Close()

	cfgJSON := fmt.Sprintf(
		`{"host":"127.0.0.1","port":%d,"maxDIsPerRead":12,"interFrameDelayMs":0}`, srv.Port())
	d := newDLT645Driver(TransportTCP)
	if err := d.Connect(cfgJSON); err != nil {
		b.Fatalf("连接失败: %v", err)
	}
	defer d.Close()

	addrs := benchEndToEndAddrs(5000)
	if _, err := d.Read(addrs); err != nil { // 预热：建规划缓存
		b.Fatalf("预热失败: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := d.Read(addrs)
		if err != nil {
			b.Fatalf("读取失败: %v", err)
		}
		if res[0].Quality != 192 {
			b.Fatalf("首点质量为 %d，期望 192", res[0].Quality)
		}
	}
	b.StopTimer()

	frames := float64(b.N) * float64(benchFrameCount(5000, 12))
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/frames, "ns/frame")
	b.ReportMetric(frames/b.Elapsed().Seconds(), "frames/s")
}

// benchFrameCount 一轮采集的帧数：⌈点数/每帧标识数⌉。
func benchFrameCount(points, perRead int) int {
	return (points + perRead - 1) / perRead
}

// BenchmarkReadTCPDefaults 同上，但**只给连接参数、其余走默认值**。
//
// 与 BenchmarkReadTCP 的差别正是要测的东西：那份显式写了 interFrameDelayMs=0，
// 把「TCP 默认该不该等 30ms 换向时间」这个决策绕开了；这里交给 DefaultDLT645Config
// 决定——旧默认会让每帧白等 30ms，417 帧即每轮 12.5s。
func BenchmarkReadTCPDefaults(b *testing.B) {
	srv, err := fake.NewDLT645()
	if err != nil {
		b.Fatalf("启动假表失败: %v", err)
	}
	defer srv.Close()

	cfgJSON := fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())
	d := newDLT645Driver(TransportTCP)
	if err := d.Connect(cfgJSON); err != nil {
		b.Fatalf("连接失败: %v", err)
	}
	defer d.Close()

	perRead := d.config.MaxDIsPerRead
	addrs := benchEndToEndAddrs(5000)
	if _, err := d.Read(addrs); err != nil { // 预热：建规划缓存
		b.Fatalf("预热失败: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := d.Read(addrs)
		if err != nil {
			b.Fatalf("读取失败: %v", err)
		}
		if res[0].Quality != 192 {
			b.Fatalf("首点质量为 %d，期望 192", res[0].Quality)
		}
	}
	b.StopTimer()

	frames := float64(b.N) * float64(benchFrameCount(5000, perRead))
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/frames, "ns/frame")
	b.ReportMetric(frames/b.Elapsed().Seconds(), "frames/s")
}
