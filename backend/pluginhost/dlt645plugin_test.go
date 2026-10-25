// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// DL/T 645 插件（iot-gateway-drivers/dlt645）的集成测试。
//
// 这里编译并加载的是**真实的插件源码产物**，不是测试替身 —— 唯一能在测试里
// 替代它的是一个重写了同样逻辑的冒牌货，那样的测试即使插件写错了也会通过。
//
// 与其它插件的 parity 测试不同，本文件**不读黄金基线**：DL/T 645 在 premium 侧
// 是全新协议，仓库里没有「内置实现被移除之前冻下来的」结果可以对照（它只在
// iot-gateway 那边以内置驱动的形式存在过）。等价性这条约束对它不成立，
// 取而代之的是两条：插件能不能被正确加载/注销（本文件前半），
// 以及能不能跑通完整采集链路（后半）。
//
// 串口路径不在集成测试里验（Windows / Linux 都没有可用的假串口），
// 由 dlt645drv 的单测用传输层替身覆盖；这里走 TCP —— 两个协议共用同一
// 应用层，TCP 通了就说明应用层被正确接进了网关。

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

const (
	dlt645SerialProtocol = "DLT645.Serial"
	dlt645TCPProtocol    = "DLT645.TCP"
	dlt645Scope          = "dlt645"
	dlt645Dir            = "dlt645"
)

// TestDLT645PluginRegistersProtocol 插件被加载后，两个协议、scope、类型映射、
// 配置表单都要在网关侧就位。
//
// 断言的全部是**运行时事实**（driver 注册表 + 数据库 + Status()），
// 不依赖任何预置文件 —— 这正是新协议与搬迁协议在验收方式上的区别。
func TestDLT645PluginRegistersProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	m, db := startPluginManager(t, dlt645Dir, nil)
	waitForDriverRegistered(t, dlt645TCPProtocol)

	// Status 应报告一个插件、两个协议、正确的 scope。
	st := m.Status()
	if len(st) != 1 {
		t.Fatalf("Status 条目数 = %d, want 1", len(st))
	}
	if len(st[0].Protocols) != 2 {
		t.Fatalf("插件协议 = %v, want 两个", st[0].Protocols)
	}
	wantProtocols := map[string]bool{dlt645SerialProtocol: true, dlt645TCPProtocol: true}
	for _, p := range st[0].Protocols {
		if !wantProtocols[p] {
			t.Errorf("出现未预期的协议 %q", p)
		}
		delete(wantProtocols, p)
	}
	for p := range wantProtocols {
		t.Errorf("插件缺少协议 %q", p)
	}
	if st[0].Scope != dlt645Scope {
		t.Errorf("scope = %q, want %q", st[0].Scope, dlt645Scope)
	}
	if !st[0].Up {
		t.Error("插件应处于运行状态")
	}

	// 两个协议都能创建驱动，且 scope 反查正确（前端按 scope 取类型下拉）。
	for _, proto := range []string{dlt645SerialProtocol, dlt645TCPProtocol} {
		if _, err := driver.Create(proto); err != nil {
			t.Fatalf("Create(%s) 失败: %v", proto, err)
		}
		if got := driver.ScopeOf(proto); got != dlt645Scope {
			t.Errorf("ScopeOf(%s) = %q, want %q", proto, got, dlt645Scope)
		}
	}

	// 串行独占按协议声明：串口 true（整轮持锁，保证 485 上帧不交错），
	// TCP false（客户端内部已对单次请求+响应持锁，声明成 true 只会白白丢并发）。
	for proto, want := range map[string]bool{
		dlt645SerialProtocol: true,
		dlt645TCPProtocol:    false,
	} {
		d, err := driver.Create(proto)
		if err != nil {
			t.Fatalf("Create(%s) 失败: %v", proto, err)
		}
		se, ok := d.(driver.SerialExclusive)
		if !ok {
			t.Errorf("%s 未实现 SerialExclusive 接口", proto)
			continue
		}
		if se.SerialExclusive() != want {
			t.Errorf("%s 的 SerialExclusive() = %v, want %v", proto, se.SerialExclusive(), want)
		}
	}

	// iot_protocol 行由 Manager 依据 Meta 自动建立 —— 不需要任何 SQL 迁移。
	for _, proto := range []string{dlt645SerialProtocol, dlt645TCPProtocol} {
		var row po.IotProtocol
		if err := db.Where("name = ?", proto).First(&row).Error; err != nil {
			t.Fatalf("协议行 %s 应由 Manager 自动建立: %v", proto, err)
		}
		if row.FormJSON == "" || row.FormJSON == "{}" {
			t.Errorf("协议行 %s 的配置表单为空 —— 前端会渲染不出设备表单", proto)
		}
		// 插件供给的行排在内置播种行之后（defaultPluginRowSort），
		// 免得插件的协议把运维手建的排序挤乱。
		if row.Sort != defaultPluginRowSort {
			t.Errorf("协议行 %s 的 sort = %d, want %d", proto, row.Sort, defaultPluginRowSort)
		}
	}

	// 类型映射：通用名 → 内部裸名。645 的 13 个通用类型必须全部有映射，
	// 少一个前端类型下拉就缺一项，且 NormalizeAddressType 会退化成放行。
	wantTypeMap := map[string]string{
		"Boolean": "bool", "Date": "datetime", "String": "string",
		"Byte": "uint", "Char": "int", "Short": "int", "Word": "uint",
		"DWord": "uint", "Long": "int", "Float": "float", "Double": "float",
		"BCD": "bcd", "LBCD": "lbcd",
	}
	for common, internal := range wantTypeMap {
		got, ok := driver.LookupDataType(dlt645TCPProtocol, common)
		if !ok || got != internal {
			t.Errorf("LookupDataType(%s, %s) = (%q, %v), 期望 %q", dlt645TCPProtocol, common, got, ok, internal)
		}
	}

	common, extended := driver.AvailableTypes(dlt645Scope)
	if len(common) != len(driver.CommonDataTypes) {
		t.Errorf("通用类型 = %v, want 全部 %v", common, driver.CommonDataTypes)
	}
	if len(extended) != 0 {
		t.Errorf("扩展类型 = %v, want 空（本协议没有通用名以外的类型）", extended)
	}
}

// TestDLT645PluginUnregistersProtocol Stop 必须把两个协议与类型 scope 一并摘掉。
//
// 残留协议的表现最迷惑人：driver.Create 仍然成功（插件行还在注册表里），
// 但底层进程已死，于是设备一直连不上 —— 而日志里什么异常都没有。
func TestDLT645PluginUnregistersProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	m, _ := startPluginManager(t, dlt645Dir, nil)
	waitForDriverRegistered(t, dlt645TCPProtocol)

	// 前置：一切都在。
	for _, proto := range []string{dlt645SerialProtocol, dlt645TCPProtocol} {
		if _, err := driver.Create(proto); err != nil {
			t.Fatalf("前置条件不成立（%s）: %v", proto, err)
		}
		if got := driver.ScopeOf(proto); got != dlt645Scope {
			t.Fatalf("前置条件：ScopeOf(%s) = %q", proto, got)
		}
	}

	m.Stop()

	for _, proto := range []string{dlt645SerialProtocol, dlt645TCPProtocol} {
		if _, err := driver.Create(proto); err == nil {
			t.Errorf("Stop 后 %s 仍可创建（协议未注销）", proto)
		}
		if got := driver.ScopeOf(proto); got != "" {
			t.Errorf("Stop 后 %s 的 scope 仍为 %q（类型映射未注销）", proto, got)
		}
	}
	if _, ok := driver.LookupDataType(dlt645TCPProtocol, "Float"); ok {
		t.Error("Stop 后插件类型映射应已注销")
	}
}

// TestDLT645PluginCollectsThroughEngine 走完整采集链路：
// 采集引擎 → RPC → 插件进程 → TCP → 假电能表 → 采集记录。
//
// 与 dlt645drv 的单测分工：那里直连驱动验**协议语义**（帧容错、批量拆包、
// 解码、质量码），这里验**网关侧的接入** —— 任务编排、锁粒度、记录上报、
// 协议名与表号透传。两者缺一不可：驱动写得再对，接不进采集引擎也等于没做。
func TestDLT645PluginCollectsThroughEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	meter := startFakeMeter(t)

	bin := buildPlugin(t, dlt645Dir)
	db := newTestDB(t)
	for _, m := range []any{&po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	engine := collector.NewEngine(db, pool, sink, noopStateSink{}, nil, nil)

	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), engine)
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, dlt645TCPProtocol)

	var proto po.IotProtocol
	if err := db.Where("name = ?", dlt645TCPProtocol).First(&proto).Error; err != nil {
		t.Fatalf("插件协议行应由 Manager 自动建立: %v", err)
	}

	host, port, err := net.SplitHostPort(meter.addr())
	if err != nil {
		t.Fatalf("解析假表地址失败: %v", err)
	}
	dev := po.Device{
		Name:       "1 号电能表",
		ProtocolID: proto.ID,
		ProtocolJSON: fmt.Sprintf(
			`{"host":%q,"port":%s,"meterAddress":"000000000001","protocolVersion":"2007"}`, host, port),
		Status: 1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}

	// 四个点位覆盖不同的数据标识规格：2/3/4 字节、有符号、不同小数位。
	// 期望值见 meterFixture —— 假表按数据标识回放**固定字节**，这里断言的是
	// 那些字节经驱动解码后的工程值，两侧不共用任何解码代码。
	specs := []struct{ name, label, want string }{
		{"02010100", "A相电压", "220.5"},
		{"02020100", "A相电流", "-1.000"},
		{"02800001", "电网频率", "50.00"},
		{"00000000", "组合有功总电能", "1.28"},
	}
	addrs := make([]po.DeviceAddress, 0, len(specs))
	wantValue := make(map[string]string, len(specs))
	for _, s := range specs {
		a := po.DeviceAddress{
			Name:          s.name,
			Label:         s.label,
			DeviceID:      dev.ID,
			DataType:      "float",
			ScanFrequency: 200,
			Status:        1,
		}
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("创建地址 %s: %v", s.name, err)
		}
		addrs = append(addrs, a)
		wantValue[a.Name] = s.want
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	waitFor(t, 20*time.Second, "全部点位采到", func() bool {
		return len(sink.snapshot()) >= len(addrs)
	})

	got := make(map[string]collector.CollectedRecord)
	for _, r := range sink.snapshot() {
		got[r.DeviceAddressName] = r
	}

	for _, a := range addrs {
		r, ok := got[a.Name]
		if !ok {
			t.Errorf("地址 %s 缺采集记录", a.Name)
			continue
		}
		if r.Quality != driver.QualityGood {
			t.Errorf("地址 %s Quality = %d, want %d", a.Name, r.Quality, driver.QualityGood)
		}
		if r.DataType != a.DataType {
			t.Errorf("地址 %s DataType = %q, want %q", a.Name, r.DataType, a.DataType)
		}
		// 协议名透传：这是区分「记录来自插件链路」的唯一标记。
		if r.Protocol != dlt645TCPProtocol {
			t.Errorf("地址 %s Protocol = %q, want %q", a.Name, r.Protocol, dlt645TCPProtocol)
		}
		if w := wantValue[a.Name]; r.Value != w {
			t.Errorf("地址 %s（%s）Value = %q, want %q", a.Name, a.Label, r.Value, w)
		}
	}

	// 表号必须真的进了帧：假表按**请求里的地址域**合成应答，地址域可选是驱动
	// 最容易漏配的一环（漏了就是所有表都收不到应答）。这里顺带确认请求确实到达过。
	//
	// 帧数按单请求打包数折算：本设备的 protocol_json 刻意不写 maxDIsPerRead，
	// 走的就是出厂默认，故 4 个点位打包进 1 帧、只要 ≥ 1 帧即可。
	// （地址域漏配时假表会回一个地址不匹配的帧，驱动判为「总线上他人的应答」整帧跳过，
	// 4 个点位的值断言会全部失败——那条断言才是地址域的真正防线。）
	const defaultPack = 12 // dlt645drv 出厂默认的 maxDIsPerRead，见其 TestDefaultConfig
	wantFrames := (len(addrs) + defaultPack - 1) / defaultPack
	if n := meter.frames(); n < wantFrames {
		t.Errorf("假表收到的读请求帧数 = %d, want ≥ %d", n, wantFrames)
	}
}

// ==================== 假电能表 ====================

// meterFixture 数据标识 → 应答数据字节，**低字节在前**（645 的数据项先传低字节）。
//
// 假表回放固定字节而不是按公式造数：期望值由这里独立确定，测试断言的是
// 「这些字节经驱动解码后的工程值」，驱动与假表不共用任何解码代码。
// 各条目的期望值见 TestDLT645PluginCollectsThroughEngine 的 specs。
var meterFixture = map[uint32][]byte{
	0x02010100: {0x05, 0x22},             // BCD 2205 → 220.5（2B，1 位小数）
	0x02020100: {0x00, 0x10, 0x80},       // 有符号 BCD 1000 取负 → -1.000（3B，3 位小数）
	0x02800001: {0x00, 0x50},             // BCD 5000 → 50.00（2B，2 位小数）
	0x00000000: {0x28, 0x01, 0x00, 0x00}, // BCD 128 → 1.28（4B，2 位小数）
}

const (
	meterFrameStart = 0x68
	meterFrameEnd   = 0x16
	meterPreamble   = 0xFE
	meterOverhead   = 12
	meterHeaderMin  = 10
	meterDataOffset = 0x33
	meterMaxDataLen = 200
	meterCtrlRead   = 0x11 // 2007 版读数据
	meterCtrlOK     = 0x91 // 2007 版读数据正常应答
	meterDIBytes    = 4    // 2007 版数据标识长度
	meterDataStart  = 10   // 数据域起始偏移：起始(1)+地址域(6)+起始(1)+C(1)+L(1)
)

// fakeMeter 一个最小可用的 DL/T 645-2007 电能表模拟器（真实 TCP 监听）。
//
// 只实现读数据命令：回显请求中的每个数据标识，各附一段固定字节的数据。
// 帧定界按无边界字节流自建（645 没有传输层语义），容错策略与驱动侧对称。
type fakeMeter struct {
	ln net.Listener

	mu         sync.Mutex
	conns      map[net.Conn]struct{}
	frameCount int // 已应答的读请求帧数
}

func (f *fakeMeter) addr() string { return f.ln.Addr().String() }

// frames 返回累计应答的读请求帧数，用于确认请求确实到达过假表。
func (f *fakeMeter) frames() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frameCount
}

func startFakeMeter(t *testing.T) *fakeMeter {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	f := &fakeMeter{ln: ln, conns: make(map[net.Conn]struct{})}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // 监听器已关闭
			}
			f.mu.Lock()
			f.conns[conn] = struct{}{}
			f.mu.Unlock()
			go f.serve(conn)
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		f.mu.Lock()
		for c := range f.conns {
			_ = c.Close()
		}
		f.mu.Unlock()
	})
	return f
}

// serve 单连接处理循环：按帧定界解析请求，逐帧合成应答。
//
// 与串口 / DTU 一致地把连接当作无边界字节流处理，一次 Read 可能带回多个请求帧，
// 也可能只带回半个 —— 缓冲就地前移，避免切片头持续前移导致底层数组泄漏。
func (f *fakeMeter) serve(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			return
		}

		for len(buf) > 0 {
			consumed, resp, ok := f.nextFrame(buf)
			if !ok {
				break // 数据不足，等下一次 Read
			}
			buf = append(buf[:0], buf[consumed:]...)
			if resp == nil {
				continue // 噪声或非读命令：只消费字节，不应答
			}
			if _, err := conn.Write(resp); err != nil {
				return
			}
		}
	}
}

// nextFrame 尝试从缓冲头部解析一个请求帧并合成应答。
//
// 返回 ok=false 表示数据不足需继续读取（此时 consumed 无意义）；
// resp 为 nil 且 ok=true 表示该帧合法但无需应答。
func (f *fakeMeter) nextFrame(buf []byte) (consumed int, resp []byte, ok bool) {
	if buf[0] == meterPreamble || buf[0] != meterFrameStart {
		return 1, nil, true
	}
	if len(buf) < meterHeaderMin {
		return 0, nil, false
	}
	if buf[7] != meterFrameStart {
		return 1, nil, true // 数据里的孤立 68H，不是帧头
	}

	ctrl := buf[8]
	dataLen := int(buf[9])
	if dataLen == 0 || dataLen > meterMaxDataLen {
		return 1, nil, true
	}
	total := meterOverhead + dataLen
	if len(buf) < total {
		return 0, nil, false
	}
	frame := buf[:total]
	if meterChecksum(frame[:total-2]) != frame[total-2] {
		return 1, nil, true // 校验码不符视为线路噪声
	}
	// 只应答 2007 版读数据命令；其余控制码（写、校时等）合法但本模拟器不实现
	if ctrl != meterCtrlRead {
		return total, nil, true
	}

	f.mu.Lock()
	f.frameCount++
	f.mu.Unlock()

	// 还原数据域偏移（线上 +0x33 → 原值）
	reqData := make([]byte, dataLen)
	for i := 0; i < dataLen; i++ {
		reqData[i] = frame[meterDataStart+i] - meterDataOffset
	}

	// 表地址原样回显：驱动侧允许任意 meterAddress，回显才能让配置自由取值
	return total, meterResponse(frame[1:7], reqData), true
}

// meterResponse 合成读数据正常应答：数据域 = 每个数据标识 + 对应固定字节，
// 再统一施加 +0x33 偏移。
func meterResponse(meterAddr, reqData []byte) []byte {
	payload := make([]byte, 0, len(reqData)/meterDIBytes*(meterDIBytes+4))
	for p := 0; p+meterDIBytes <= len(reqData); p += meterDIBytes {
		di := readDI(reqData[p : p+meterDIBytes])
		payload = append(payload, reqData[p:p+meterDIBytes]...)
		payload = append(payload, meterFixture[di]...) // 未收录的数据标识回放空数据
	}
	for i := range payload {
		payload[i] += meterDataOffset
	}

	frame := make([]byte, 0, meterOverhead+len(payload))
	frame = append(frame, meterFrameStart)
	frame = append(frame, meterAddr...)
	frame = append(frame, meterFrameStart, meterCtrlOK, byte(len(payload)))
	frame = append(frame, payload...)
	return append(frame, meterChecksum(frame), meterFrameEnd)
}

// readDI 从数据域头部还原数据标识（线上为低字节在前）。
func readDI(b []byte) uint32 {
	var v uint32
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint32(b[i])
	}
	return v
}

// meterChecksum 计算校验码：从第一个帧起始符到当前末字节的算术和 mod 256。
func meterChecksum(frame []byte) byte {
	var sum byte
	for _, b := range frame {
		sum += b
	}
	return sum
}

// TestDLT645PluginDiscoverPoints 用**真实的插件产物**跑一次点位扫描。
//
// 与 TestDLT645PluginCollectsThroughEngine（采集链路）成对：那条验"这台表能不能采到值"，
// 这条验"这台表上有哪些点位"。它钉住三件只有真实二进制才验得到的事：
//
//   - 能力声明确实穿过了 Meta 的序列化 —— PointDiscovery 是可选字段，插件漏传它的话
//     网关侧的 PointDiscoverySpecs() 会返回空，前端连"自动发现点位"按钮都不出现；
//   - 结果确实穿过了帧 RPC（载荷编码、进程边界）与插件进程内的会话选择
//     （本进程内没有会话 ⇒ 走临时实例，由 handleDiscoverPoints 建连）；
//   - 生成的地址写法能被解析回同一个位置，且**逐点类型**能落到网关的类型表里。
//
// 假表对未收录的数据标识回**空数据**（见 meterResponse），所以这本字典里只有 4 条
// 读得出来 —— 这恰好是真实电能表的常态（字典归字典，表实现归表实现），
// 顺带钉住了"数据域短了不能整体采信、要降级逐个重问"那条路径。
func TestDLT645PluginDiscoverPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	meter := startFakeMeter(t)

	bin := buildPlugin(t, dlt645Dir)
	db := newTestDB(t)
	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), &fakeReloader{})
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, dlt645TCPProtocol)

	// 串口与 TCP 都该声明点扫能力 —— 与 Discovery（本插件有意不做）正相反。
	// 串口设备同样有"它有哪些点位"这个问题，而且串口恰是会话复用最重要的场景。
	declared := map[string]bool{}
	for _, s := range m.PointDiscoverySpecs() {
		declared[s.Protocol] = true
		if len(s.Spec.FormJSON) == 0 {
			t.Errorf("%s 的扫描表单没随 Meta 过线，前端会渲染出一个空参数区", s.Protocol)
		}
		if s.Spec.Title == "" || s.Spec.Description == "" {
			t.Errorf("%s 的点扫声明缺 Title 或 Description", s.Protocol)
		}
	}
	for _, p := range []string{dlt645SerialProtocol, dlt645TCPProtocol} {
		if !declared[p] {
			t.Errorf("%s 应声明点位扫描能力", p)
		}
	}
	// 设备扫描有意不做（串口被契约禁止、TCP 目标是 DTU）——声明了就该在这张表里。
	for _, s := range m.DiscoverySpecs() {
		if s.Protocol == dlt645SerialProtocol || s.Protocol == dlt645TCPProtocol {
			t.Errorf("%s 声明了设备扫描 —— 本插件有意不做（见 main.go 的 meta() 注释）", s.Protocol)
		}
	}

	host, port, err := net.SplitHostPort(meter.addr())
	if err != nil {
		t.Fatalf("解析假表地址失败: %v", err)
	}
	protocolJSON := fmt.Sprintf(
		`{"host":%q,"port":%s,"meterAddress":"000000000001","protocolVersion":"2007"}`, host, port)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 不加附加数据标识：整本 2007 字典（44 条）。
	res, err := m.DiscoverPoints(ctx, dlt645TCPProtocol, protocolJSON, nil)
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}

	wantScanned := 44
	if res.Scanned != wantScanned {
		t.Fatalf("Scanned = %d，期望 %d（字典里的每一条都要有结论）", res.Scanned, wantScanned)
	}
	if res.Readable+res.Unreadable != res.Scanned {
		t.Fatalf("不变量被破坏：readable %d + unreadable %d != scanned %d",
			res.Readable, res.Unreadable, res.Scanned)
	}
	if len(res.Points) > res.Readable {
		t.Fatalf("points %d > readable %d（不变量 len(points) <= readable 被破坏）",
			len(res.Points), res.Readable)
	}
	if res.Truncated || res.Incomplete {
		t.Errorf("Truncated=%v Incomplete=%v，44 条字典不该触及任何一个上限",
			res.Truncated, res.Incomplete)
	}

	// 假表只对 meterFixture 里的 4 个数据标识回了数据，其余回空 —— 那些就是"这块表
	// 没实现"，如实记成读不到（而不是把整块丢掉，也不是当整台设备掉线）。
	if res.Readable != len(meterFixture) {
		t.Errorf("可读 %d 条，期望 %d 条（假表只对 meterFixture 里的回了数据）",
			res.Readable, len(meterFixture))
	}

	// 结果级类型非空是**有意的**：它是前端"本次结果带采样值"的判据（据它显示
	// "显示 0 值"开关）。逐点类型另有 Points[].CommonDataType。
	if res.AssumedCommonDataType != driver.TypeFloat {
		t.Errorf("结果级类型 = %q，期望 %q", res.AssumedCommonDataType, driver.TypeFloat)
	}

	got := map[string]driver.DiscoveredPoint{}
	for _, p := range res.Points {
		got[p.Name] = p
	}

	// 期望值由 meterFixture 的字节独立确定（见那边的注释），期望的类型由字典确定。
	// 两条都对上才说明"值来自正确的数据标识"且"类型与解码方式一致"。
	cases := []struct {
		name, value, label, common string
	}{
		{"02010100", "220.5", "A相电压（V）", driver.TypeFloat},
		{"02020100", "-1.000", "A相电流（A）", driver.TypeFloat},
		{"02800001", "50.00", "电网频率（Hz）", driver.TypeFloat},
		{"00000000", "1.28", "组合有功总电能（kWh）", driver.TypeFloat},
	}
	for _, c := range cases {
		p, ok := got[c.name]
		if !ok {
			t.Errorf("结果里缺 %s —— 它是假表答得上来的数据标识", c.name)
			continue
		}
		if p.Value != c.value {
			t.Errorf("%s 的值 = %q，期望 %q", c.name, p.Value, c.value)
		}
		if p.Label != c.label {
			t.Errorf("%s 的标签 = %q，期望 %q（字典自带的中文名 + 单位）", c.name, p.Label, c.label)
		}
		if p.CommonDataType != c.common {
			t.Errorf("%s 的逐点类型 = %q，期望 %q", c.name, p.CommonDataType, c.common)
		}
	}

	// 读不到的数据标识**不该**出现在清单里（契约：len(points) <= readable）。
	if _, ok := got["02010300"]; ok {
		t.Error("假表没回数据的 C相电压 不该出现在结果里")
	}

	// 逐点类型必须能在网关侧解析成内部类型 —— 前端拿它初始化下拉，而它最终会写进
	// device_address.common_data_type。缺一条映射，建点位时那一条就会被判"协议不支持该数据类型"。
	for _, ct := range []string{driver.TypeFloat, driver.TypeDate, driver.TypeString, driver.TypeWord} {
		if _, ok := driver.LookupDataType(dlt645TCPProtocol, ct); !ok {
			t.Errorf(`driver.LookupDataType(%q, %q) 应命中`, dlt645TCPProtocol, ct)
		}
	}

	// 结果里**每一行**都必须自带类型：字典枚举的结果逐行给出类型，空着会让前端
	// 回退到结果级那一级，而结果级只有一个值，表达不了表号（String）与状态字（Word）。
	// 上面那 4 个可读点位恰好都是 Float，所以这两种形态只能在这里逐行兜住。
	for _, p := range res.Points {
		if p.CommonDataType == "" {
			t.Errorf("%s 没有逐点类型 —— 字典枚举的结果必须逐行给出类型", p.Name)
			continue
		}
		if _, ok := driver.LookupDataType(dlt645TCPProtocol, p.CommonDataType); !ok {
			t.Errorf("%s 的类型 %q 在网关侧解析不出来 —— 建点位时这一条会被判"+
				"「协议不支持该数据类型」", p.Name, p.CommonDataType)
		}
	}

	if meter.frames() == 0 {
		t.Error("假表一帧都没收到 —— 扫描根本没连上")
	}
}
