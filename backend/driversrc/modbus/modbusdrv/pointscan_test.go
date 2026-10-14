// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
	"iot-gateway/testutil/fake"
)

// ==================== 测试脚手架 ====================

// directSession 把一个驱动实例直接包成 SessionAccess。
//
// 等价于 SDK 里的 instanceAccess（临时实例那条路）：没有第二方碰这个实例，
// Do 就是一次直接调用。SDK 那侧的会话复用/锁纪律由 driversdk 的用例覆盖，
// 这里只关心扫出来的东西对不对。
type directSession struct{ drv driver.Driver }

func (s *directSession) Do(fn func(drv driver.Driver) error) error { return fn(s.drv) }

// tcpSession 起一个连着假从站的 Modbus TCP 驱动，返回会话与协议配置模板。
//
// protocolJSON 由调用方拼（要能表达 startAddress 这类配置模式字段）。
func tcpSession(t *testing.T, srv *fake.Server, protocolJSON string) *directSession {
	t.Helper()
	drv := NewTCP()
	if err := drv.Connect(protocolJSON); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	return &directSession{drv: drv}
}

// addrOf 把假从站的监听地址拆成 host/port 供拼 protocolJSON。
func addrOf(t *testing.T, srv *fake.Server) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatalf("解析地址 %q: %v", srv.Addr(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析端口 %q: %v", portStr, err)
	}
	return host, port
}

// tcpJSON 拼一份 Modbus TCP 的 protocol_json（自动模式）。
func tcpJSON(t *testing.T, srv *fake.Server) string {
	t.Helper()
	host, port := addrOf(t, srv)
	return fmt.Sprintf(`{"host":%q,"port":%d,"unitId":1}`, host, port)
}

// opts 拼一份扫描选项。
func opts(functionCode int, start, count int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"functionCode":%d,"startAddress":%d,"count":%d}`, functionCode, start, count))
}

// scanDeadline 用例用的截止时刻：远大于本地假设备的往返耗时，
// 但不足以让"预算耗尽"那条路径被意外触发。
func scanDeadline() time.Time { return time.Now().Add(5 * time.Second) }

// pointNames 取出结果里的地址清单。
func pointNames(r *driver.PointScanResult) []string {
	out := make([]string, 0, len(r.Points))
	for _, p := range r.Points {
		out = append(out, p.Name)
	}
	return out
}

// ==================== 基本扫描 ====================

// TestScanPointsRegisterRange 扫一段寄存器：非零的回值、零的只给说明。
func TestScanPointsRegisterRange(t *testing.T) {
	srv, err := fake.NewModbusTCPWithRegisters(map[uint16]uint16{
		100: 1234, 102: 65535, 104: 1,
	})
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(3, 100, 5), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}

	if got := pointNames(res); len(got) != 5 || got[0] != "%MW100" || got[4] != "%MW104" {
		t.Fatalf("地址清单 = %v，期望 M100..M104 那一段（%%MW 前缀）", got)
	}
	if res.Scanned != 5 || res.Readable != 5 || res.Unreadable != 0 {
		t.Fatalf("统计 = scanned %d / readable %d / unreadable %d，期望 5/5/0",
			res.Scanned, res.Readable, res.Unreadable)
	}
	if res.AssumedCommonDataType != "Word" {
		t.Errorf("AssumedCommonDataType = %q，期望 Word", res.AssumedCommonDataType)
	}
	if res.Truncated || res.Incomplete {
		t.Errorf("不该被截断或标未扫完: %+v", res)
	}

	want := map[string]string{"%MW100": "1234", "%MW101": "", "%MW102": "65535", "%MW103": "", "%MW104": "1"}
	for _, p := range res.Points {
		if p.Value != want[p.Name] {
			t.Errorf("%s 的值 = %q，期望 %q", p.Name, p.Value, want[p.Name])
		}
		// 值为 0 的点位**照样列出来**，只是没有 value、带一句说明。
		// 插件侧刻意不做零值过滤：所有寄存器都是 0 的设备会得到一份空列表，
		// 用户据此得出"这里没有点位"——与"没扫到"同型的错误。
		if want[p.Name] == "" && p.Note == "" {
			t.Errorf("%s 值为 0，应给出说明", p.Name)
		}
	}
}

// TestScanPointsZeroValuesAreReturned 全零的范围也必须逐条列出来。
func TestScanPointsZeroValuesAreReturned(t *testing.T) {
	srv, err := fake.NewModbusTCP() // 默认全零
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(3, 0, 8), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	if len(res.Points) != 8 || res.Readable != 8 {
		t.Fatalf("全零的范围也应逐条列出，实际 %d 条 / readable=%d", len(res.Points), res.Readable)
	}
	for _, p := range res.Points {
		if p.Value != "" {
			t.Errorf("%s 全零不该有值，实际 %q", p.Name, p.Value)
		}
	}
}

// TestScanPointsBitRange 位区按 Boolean 假设读，前缀是 %MX/%IX。
func TestScanPointsBitRange(t *testing.T) {
	srv, err := fake.NewModbusTCPWithOption(fake.WithModbusBits(map[uint16]bool{0: true, 7: true}))
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(1, 0, 8), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	if res.AssumedCommonDataType != "Boolean" {
		t.Errorf("位区假设类型 = %q，期望 Boolean", res.AssumedCommonDataType)
	}
	got := map[string]string{}
	for _, p := range res.Points {
		got[p.Name] = p.Value
	}
	if got["%MX0"] != "1" || got["%MX7"] != "1" {
		t.Errorf("置位的线圈应回 1，实际 %v", got)
	}
	if got["%MX1"] != "" {
		t.Errorf("未置位的线圈应无值，实际 %q", got["%MX1"])
	}
}

// TestScanPointsInputRegistersAndDiscreteInputs 四个功能区各自的前缀都要对。
func TestScanPointsInputRegistersAndDiscreteInputs(t *testing.T) {
	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()
	sess := tcpSession(t, srv, tcpJSON(t, srv))

	for _, c := range []struct {
		fc     int
		prefix string
	}{{3, "%MW"}, {4, "%IW"}, {1, "%MX"}, {2, "%IX"}} {
		res, err := ScanPoints(sess, opts(c.fc, 10, 3), scanDeadline())
		if err != nil {
			t.Fatalf("FC%d: %v", c.fc, err)
		}
		if len(res.Points) != 3 {
			t.Fatalf("FC%d 应列出 3 条，实际 %d", c.fc, len(res.Points))
		}
		for i, p := range res.Points {
			want := fmt.Sprintf("%s%d", c.prefix, 10+i)
			if p.Name != want {
				t.Errorf("FC%d 第 %d 条地址 = %q，期望 %q", c.fc, i, p.Name, want)
			}
		}
	}
}

// ==================== 地址写法的往返 ====================

// TestPointNameRoundTrip 生成的地址必须能被本协议自己的解析器还原回
// **同一个 (功能区, 偏移)**。
//
// 这条不变量比它看起来重要得多：一个解析不出来的地址写进 device_address 之后，
// 这个点位永远采不到值（planFor 在规划前就把它拆出去，只报 QualityBadAddress）。
// 覆盖 0 / 9999 / 10000 / 65534 / 65535 —— 传统 5 位与 6 位
// 写法恰恰在这几个值上跨功能区或直接溢出。
func TestPointNameRoundTrip(t *testing.T) {
	fcs := []struct {
		fc     byte
		prefix string
	}{
		{FuncCodeReadHoldingRegisters, "%MW"},
		{FuncCodeReadInputRegisters, "%IW"},
		{FuncCodeReadCoils, "%MX"},
		{FuncCodeReadDiscreteInputs, "%IX"},
	}
	addrs := []uint16{0, 1, 9999, 10000, 32767, 65534, 65535}

	for _, c := range fcs {
		for _, addr := range addrs {
			name := formatPointName(c.fc, addr)
			if want := fmt.Sprintf("%s%d", c.prefix, addr); name != want {
				t.Errorf("formatPointName(%d, %d) = %q，期望 %q", c.fc, addr, name, want)
			}
			gotFC, gotAddr, ok := ParsePLCAddress(name)
			if !ok {
				t.Errorf("地址 %q 解析不出来 —— 写进库会让这个点位永远没有值", name)
				continue
			}
			if gotFC != c.fc || gotAddr != addr {
				t.Errorf("地址 %q 往返后变成 (FC=%d, addr=%d)，期望 (FC=%d, addr=%d)",
					name, gotFC, gotAddr, c.fc, addr)
			}
		}
	}
}

// TestTraditionalFormatWouldBreakThis 把"为什么必须用 %MW"钉死。
//
// 这条是 formatPointName 那段注释的可执行版本：它断言**传统写法确实坏**，
// 好让"顺手改回 40001 风格"这个改动在 CI 里立刻失败。三个事实都按
// ParsePLCAddress 的实测行为写死（不是照抄注释里的推测）。
func TestTraditionalFormatWouldBreakThis(t *testing.T) {
	// ① 满量程溢出：(FC3, 65535) 的传统写法是 "465536"
	//（4 + 6 位 1-based 65536），而 ParseUint(s[1:], 10, 16) 解不出 65536 →
	// 整个串**解析不出来** → 该点位被摘出区间规划、永远采不到值
	// （修复前更糟：区间规划直接报错 ⇒ 整台设备采集全挂 + 误报断联）。
	if _, _, ok := ParsePLCAddress("465536"); ok {
		t.Error("465536 不该解析得出来 —— 这正是满量程地址不能用传统写法的原因")
	}
	// 对照：同样的位置用 IEC 写法毫无问题（这是本驱动实际生成的形态）。
	if fc, addr, ok := ParsePLCAddress("%MW65535"); !ok || fc != FuncCodeReadHoldingRegisters || addr != 65535 {
		t.Errorf("%%MW65535 应解析成 (FC=3, addr=65535)，实际 (%d, %d, %v)", fc, addr, ok)
	}

	// ② 差一错位：传统写法是 1-based，所以 "465535" 指的是偏移 65534 而不是 65535。
	if fc, addr, ok := ParsePLCAddress("465535"); !ok || fc != FuncCodeReadHoldingRegisters || addr != 65534 {
		t.Errorf("465535 应解析成 (FC=3, addr=65534)，实际 (%d, %d, %v)", fc, addr, ok)
	}

	// ③ 功能区丢失：线圈区的偏移 10000 写成 5 位是 "10000"，它落进了
	// **纯数字**分支（functionCode=0，表示"未知"），而不是线圈区 ——
	// groupAddrsByFC 随后会拿 cfg.FunctionCode 去读它。
	fc, addr, ok := ParsePLCAddress("10000")
	if !ok {
		t.Fatal("10000 应被解析成纯数字地址")
	}
	if fc != 0 || addr != 10000 {
		t.Errorf("10000 被解析成 (FC=%d, addr=%d) —— 期望功能区丢失（FC=0）", fc, addr)
	}
	if fc == FuncCodeReadCoils {
		t.Error("不该被当成线圈区：传统写法在 5 位时会与 1xxxx 前缀撞车")
	}
}

// ==================== 空洞与降级 ====================

// TestScanPointsBisectsGap 含空洞的范围：整段读失败后一路对半拆到单点，
// 把真的读不到的地址摘出来。
//
// 这是本文件最重要的一条：readRegisterRanges 那条路在整段失败时直接
// markRangeZero 把整段标 0，"整块→子段→单地址"根本不会发生。
//
// 空洞是**区间内部**的（104-109 不存在，两侧都存在），而不是边界截断 ——
// 后者靠"缩小区间"就能解决，前者必须靠二分。
func TestScanPointsBisectsGap(t *testing.T) {
	// 只有 [100,103] 与 [110,111] 存在，中间的 104-109 是空洞。
	regs := map[uint16]uint16{100: 11, 101: 22, 102: 33, 103: 44, 110: 55, 111: 66}
	srv, err := fake.NewModbusTCPWithOption(
		fake.WithModbusRegisters(regs),
		fake.WithModbusSpan(100, 103),
		fake.WithModbusSpan(110, 111),
	)
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(3, 100, 12), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}

	if res.Scanned != 12 {
		t.Fatalf("Scanned = %d，期望 12（范围内的每一个地址都要有结论）", res.Scanned)
	}
	if res.Readable+res.Unreadable != res.Scanned {
		t.Fatalf("不变量被破坏：readable %d + unreadable %d != scanned %d",
			res.Readable, res.Unreadable, res.Scanned)
	}
	if res.Readable != 6 || res.Unreadable != 6 {
		t.Fatalf("可读 %d / 读不到 %d，期望 6/6（104-109 是空洞）"+
			" —— 整段失败却没降级成 12 个「读不到」", res.Readable, res.Unreadable)
	}
	if len(res.Points) != 6 {
		t.Fatalf("结果条数 = %d，期望 6 —— 读不到的地址不该出现在清单里", len(res.Points))
	}
	got := map[string]bool{}
	for _, p := range res.Points {
		got[p.Name] = true
	}
	for _, want := range []string{"%MW100", "%MW103", "%MW110", "%MW111"} {
		if !got[want] {
			t.Errorf("结果里缺少 %s", want)
		}
	}
	for _, bad := range []string{"%MW104", "%MW109"} {
		if got[bad] {
			t.Errorf("空洞里的 %s 不该出现在结果里", bad)
		}
	}
}

// TestScanPointsEntireRangeUnreadable 整段都读不到：空结果**不是错误**。
//
// 报 error 会让用户以为功能坏了，而实际上"这段地址这台设备没有"是完全正常的答案。
func TestScanPointsEntireRangeUnreadable(t *testing.T) {
	srv, err := fake.NewModbusTCPWithUnreadableGap(1000, 1009)
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(3, 0, 4), scanDeadline())
	if err != nil {
		t.Fatalf("读不到不是 error，实际 %v", err)
	}
	if len(res.Points) != 0 {
		t.Fatalf("不该有点位，实际 %v", pointNames(res))
	}
	if res.Scanned != 4 || res.Unreadable != 4 || res.Readable != 0 {
		t.Fatalf("统计 = %d/%d/%d，期望 4 扫过、0 可读、4 读不到",
			res.Scanned, res.Readable, res.Unreadable)
	}
}

// ==================== 错误分类 ====================

// TestScanPointsAbortsOnConnectionError 连接断掉必须**中止扫描**，
// 而不是把整段地址记成「读不到」。
//
// 记成「读不到」的后果：用户从一份几乎全是"不存在"的清单里得出与事实完全相反的
// 结论（设备明明是掉线了），而采集侧此时也已经在重连。
func TestScanPointsAbortsOnConnectionError(t *testing.T) {
	srv, err := fake.NewModbusTCPWithRegisters(map[uint16]uint16{0: 1})
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	// 把假设备撤掉：从下一个请求开始，读会撞上连接被关（EOF）。
	srv.Close()

	res, err := ScanPoints(sess, opts(3, 0, 40), scanDeadline())
	if err == nil {
		t.Fatal("连接断了应中止扫描并返回错误")
	}
	if !strings.Contains(err.Error(), "连接中断") {
		t.Errorf("错误文案应指明是连接问题而不是地址问题，实际 %v", err)
	}
	// 已经扫到的部分要一并返回：丢掉它们对用户没有任何好处。
	if res == nil {
		t.Fatal("中止时也应把已完成的部分结果返回")
	}
	if res.Unreadable > 0 {
		t.Errorf("连接错误不该被记成「读不到」：unreadable = %d", res.Unreadable)
	}
}

// ==================== 配置模式 ====================

// TestScanPointsRejectsConfigMode 配置模式（startAddress > 0）必须**明确报错**。
//
// 这是约束 1 的收口。那种设备上地址的含义由配置决定：扫描按功能区生成的地址会被
// 配置里的 FunctionCode 读走，而解析是越界安全的 —— 不报错、不 panic，
// 只是静默给出一份错位的值。更糟的是这些地址一旦建成 device_address，
// 那个点位会**永远取到别处的数据**（在时序库里表现为一条完全合理的曲线）。
//
// **这个字段目前只能由本包内部置起**：protocol_json 的解析结构
// （modbusConfigRaw）刻意不声明 startAddress（那是内置时代留给"配置模式"的
// 兼容字段，表单从不暴露）。所以这里直接改驱动内部状态来构造该形态 ——
// 守卫是防"将来有人把它加回表单"的那道网，而不是当前可达路径。
func TestScanPointsRejectsConfigMode(t *testing.T) {
	srv, err := fake.NewModbusTCPWithRegisters(map[uint16]uint16{100: 7, 101: 8})
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	drv := NewTCP()
	if err := drv.Connect(tcpJSON(t, srv)); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })

	sess := &directSession{drv: drv}
	// 自动模式（StartAddress 恒为 0）下扫描是好的。
	if _, err := ScanPoints(sess, opts(3, 100, 2), scanDeadline()); err != nil {
		t.Fatalf("自动模式不该被拒: %v", err)
	}

	// 打开配置模式。
	tcpDrv, ok := drv.(*modbusTCPDriver)
	if !ok {
		t.Fatalf("驱动类型 = %T", drv)
	}
	tcpDrv.mu.Lock()
	tcpDrv.config.StartAddress = 100
	tcpDrv.config.Quantity = 10
	tcpDrv.mu.Unlock()

	res, err := ScanPoints(sess, opts(3, 100, 2), scanDeadline())
	if err == nil {
		t.Fatalf("配置模式必须报错，实际返回 %+v", res)
	}
	if !strings.Contains(err.Error(), "配置模式") {
		t.Errorf("错误文案应点明配置模式，实际 %v", err)
	}
	if res != nil {
		t.Error("拒绝时不该返回结果（否则用户会拿它去建点位）")
	}
}

// ==================== 分块 ====================

// TestScanPointsChunksWithinFrameLimit 单帧不得超过协议上限 125（寄存器）。
//
// 判据用**请求帧数**而不是"没报错"：实现退化成"整段一帧"时，只要假设备
// 不校验数量就会一切正常，而真实设备会直接拒掉。假设备刻意不校验数量
// （见其注释），所以帧数是这里唯一有效的判据。
func TestScanPointsChunksWithinFrameLimit(t *testing.T) {
	var reqs atomic.Int64
	srv, err := fake.NewModbusTCPWithOption(
		fake.WithModbusRegisters(map[uint16]uint16{0: 1}),
		fake.WithModbusRequests(&reqs),
	)
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	const count = 300 // 300 / 125 = 3 块
	res, err := ScanPoints(sess, opts(3, 0, count), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	if len(res.Points) != count {
		t.Fatalf("结果条数 = %d，期望 %d", len(res.Points), count)
	}
	if got := reqs.Load(); got != 3 {
		t.Fatalf("请求帧数 = %d，期望 3（300 个寄存器按 125/帧切成 3 块）", got)
	}
}

// TestScanPointsBitChunkLimit 位区单帧上限是 2000：1500 个位只该发 1 帧。
func TestScanPointsBitChunkLimit(t *testing.T) {
	var reqs atomic.Int64
	srv, err := fake.NewModbusTCPWithRequestCounter(&reqs)
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(2, 0, 1500), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	if len(res.Points) != 1500 {
		t.Fatalf("结果条数 = %d，期望 1500", len(res.Points))
	}
	if got := reqs.Load(); got != 1 {
		t.Fatalf("请求帧数 = %d，期望 1（1500 位在 2000/帧之内）", got)
	}
}

// ==================== 上限与预算 ====================

// TestScanPointsTruncatesAtMaxPoints 达到 MaxPoints 即截断，且统计与列表自洽。
func TestScanPointsTruncatesAtMaxPoints(t *testing.T) {
	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	res, err := ScanPoints(sess, opts(3, 0, pointScanMaxPoints+500), scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	if !res.Truncated {
		t.Fatal("超过 MaxPoints 应置 Truncated")
	}
	if res.Incomplete {
		t.Error("Truncated 与 Incomplete 是两回事，不该同时置位")
	}
	if len(res.Points) != pointScanMaxPoints {
		t.Fatalf("结果条数 = %d，期望被截到 %d", len(res.Points), pointScanMaxPoints)
	}
	// 截断时统计与列表必须自洽：被丢掉的地址"根本没试"，不该计入 Scanned。
	if res.Scanned != pointScanMaxPoints || res.Readable != pointScanMaxPoints {
		t.Fatalf("统计 = %d/%d，期望都等于 %d（截断后没试的地址不计入）",
			res.Scanned, res.Readable, pointScanMaxPoints)
	}
}

// TestScanPointsBudgetExhaustedSetsIncomplete 预算耗尽要置 Incomplete 且正常返回。
func TestScanPointsBudgetExhaustedSetsIncomplete(t *testing.T) {
	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	sess := tcpSession(t, srv, tcpJSON(t, srv))
	// 已经过期的截止时刻：第一块之前就会收工。
	res, err := ScanPoints(sess, opts(3, 0, 500), time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("预算耗尽不是错误，实际 %v", err)
	}
	if !res.Incomplete {
		t.Fatal("预算耗尽应置 Incomplete")
	}
	if res.Truncated {
		t.Error("Incomplete 与 Truncated 是两回事，不该同时置位")
	}
}

// ==================== 选项校验 ====================

// TestParsePointScanOptions 选项缺失/非法一律报错。
//
// 与局域网扫描的 scanOptions 取舍不同（那边缺项时静默回默认值）："扫多少"
// 没有合理的默认值 —— 猜一个 64 会让用户拿到一份他并没有要求的清单，
// 而多扫的那部分在串口上要花掉几秒。
func TestParsePointScanOptions(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
		check   func(t *testing.T, o pointScanOptions)
	}{
		{name: "空选项", in: ``, wantErr: true},
		{name: "缺 count", in: `{"functionCode":3,"startAddress":0}`, wantErr: true},
		{name: "count 为 0", in: `{"functionCode":3,"startAddress":0,"count":0}`, wantErr: true},
		{name: "count 为负", in: `{"functionCode":3,"startAddress":0,"count":-5}`, wantErr: true},
		{name: "功能码非法", in: `{"functionCode":6,"startAddress":0,"count":10}`, wantErr: true},
		{name: "起始地址越界", in: `{"functionCode":3,"startAddress":70000,"count":10}`, wantErr: true},
		{name: "不是 JSON", in: `{不是 JSON`, wantErr: true},
		{
			name: "字符串形态的选项也要认（form-create 的 select 给字符串）",
			in:   `{"functionCode":"4","startAddress":"100","count":"8"}`,
			check: func(t *testing.T, o pointScanOptions) {
				if o.functionCode != FuncCodeReadInputRegisters || o.start != 100 || o.count != 8 {
					t.Fatalf("解析结果 = %+v", o)
				}
			},
		},
		{
			name: "缺 functionCode 时默认保持寄存器",
			in:   `{"startAddress":0,"count":4}`,
			check: func(t *testing.T, o pointScanOptions) {
				if o.functionCode != FuncCodeReadHoldingRegisters {
					t.Fatalf("默认功能码 = %d，期望 3", o.functionCode)
				}
			},
		},
		{
			name: "越过地址空间末尾时截断而不是报错",
			in:   `{"functionCode":3,"startAddress":65530,"count":100}`,
			check: func(t *testing.T, o pointScanOptions) {
				if o.start != 65530 || o.count != 6 {
					t.Fatalf("解析结果 = %+v，期望截断到 [65530, 65536)", o)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parsePointScanOptions(json.RawMessage(c.in))
			if c.wantErr {
				if err == nil {
					t.Fatalf("应报错，实际得到 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

// ==================== 驱动类型与连接 ====================

// TestScanSessionRejectsForeignDriver 不认识的驱动类型要如实报错，
// 而不是拿一个零值配置去读。
func TestScanSessionRejectsForeignDriver(t *testing.T) {
	if _, err := scanSessionOf(&foreignDriver{}); err == nil {
		t.Fatal("不支持的驱动类型应报错")
	}
}

// TestScanPointsWithoutConnection 没连接时要给出明确的错误。
func TestScanPointsWithoutConnection(t *testing.T) {
	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	drv := NewTCP() // 只创建、不 Connect
	sess := &directSession{drv: drv}
	if _, err := ScanPoints(sess, opts(3, 0, 4), scanDeadline()); err == nil {
		t.Fatal("未连接时应报错")
	}
}

// TestPointDiscoverySpec 能力声明本身的形状。
func TestPointDiscoverySpec(t *testing.T) {
	d := PointDiscovery()
	if d.Title == "" || d.Description == "" {
		t.Error("Title 与 Description 都是用户理解本功能性质的来源，不能为空")
	}
	if d.MaxPoints != pointScanMaxPoints {
		t.Errorf("MaxPoints = %d，与实现里的上限 %d 不一致 —— "+
			"声明的上限决定了网关与前端怎么显示截断", d.MaxPoints, pointScanMaxPoints)
	}
	if len(d.FormJSON) == 0 {
		t.Error("必须给扫描表单：没有它前端渲染不出参数区")
	}
	// 表单的 field 必须与解析器的键逐一对应，否则用户填了也不生效
	// （与 main_test.go 的 TestFormsMatchConfigStruct 同一条理由）。
	var form struct {
		Rule []struct {
			Field string `json:"field"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(d.FormJSON, &form); err != nil {
		t.Fatalf("解析扫描表单: %v", err)
	}
	known := map[string]bool{"functionCode": true, "startAddress": true, "count": true}
	if len(form.Rule) != len(known) {
		t.Errorf("表单字段数 = %d，期望 %d", len(form.Rule), len(known))
	}
	for _, r := range form.Rule {
		if !known[r.Field] {
			t.Errorf("表单字段 %q 不在解析器的键里 —— 用户填了也不生效", r.Field)
		}
	}
}

// foreignDriver 不属于 modbusdrv 的驱动实现，用来钉住"不认识的驱动类型要报错"。
type foreignDriver struct{}

func (d *foreignDriver) Connect(string) error { return nil }
func (d *foreignDriver) Ping(string) error    { return nil }
func (d *foreignDriver) IsConnected() bool    { return true }
func (d *foreignDriver) Close() error         { return nil }
func (d *foreignDriver) Read([]po.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, nil
}
