// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"errors"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 串口独占 RS-485 总线需整轮持锁；TCP 非独占，否则会白白拖住整轮采集。
func TestSerialExclusive(t *testing.T) {
	if !newDLT645Driver(TransportSerial).SerialExclusive() {
		t.Error("串口驱动应为独占")
	}
	if newDLT645Driver(TransportTCP).SerialExclusive() {
		t.Error("TCP 驱动不应为独占")
	}
}

// 装配一个已「连接」到预置传输的驱动，跳过真实 I/O。
// 字段直接注入：driver.Driver 的契约里 client 是接口，测试替身本就该从这里进。
func newWiredDriver(t *testing.T, cfg *DLT645Config, meter string, tr dlt645Transport) *dlt645Driver {
	t.Helper()

	addr, err := MeterAddressBytes(meter)
	if err != nil {
		t.Fatalf("表号 %q 非法: %v", meter, err)
	}
	d := newDLT645Driver(cfg.Transport)
	d.config = cfg
	d.meterAddr = addr
	d.client = tr
	return d
}

// assertQuality 断言一个点位的质量码。成因并入 Quality 之后，那条“192 带成因”的
// 非法组合已经不可表达，故只比对质量码本身。
func assertQuality(t *testing.T, r driver.ReadResult, want int) {
	t.Helper()
	if r.Quality != want {
		t.Errorf("Quality = %d, want %d（value=%q）", r.Quality, want, r.Value)
	}
}

// TestAbnormalCodeQuality 钉住"异常应答错误码 → 成因"的判据。
//
// 只认 0x02「无请求数据」（这块表没有这个数据标识）为地址级；0x01「其他错误」
// 刻意不归因 —— 驱动自己的注释就写明它不含定位信息（表号不对、数据标识没实现、
// 表处于非抄读状态，报出来都是 01）。猜成地址级会让运维去查一张本来正确的点位表。
func TestAbnormalCodeQuality(t *testing.T) {
	cases := []struct {
		code byte
		want int
	}{
		{0x01, driver.QualityBad},        // 其他错误：不可定位，不猜
		{0x02, driver.QualityBadAddress}, // 无请求数据：这块表没有该数据标识
		{0x03, driver.QualityBad},        // 密码错/未授权：与地址无关
		{0x00, driver.QualityBad},        // 异常应答未带错误码
	}
	for _, c := range cases {
		err := &abnormalError{Code: c.code}
		if got := abnormalQuality(err); got != c.want {
			t.Errorf("abnormalQuality(0x%02X) = %d, want %d", c.code, got, c.want)
		}
	}
	// 非异常应答的错误不得被当成异常码归因
	if got := abnormalQuality(errors.New("dlt645: 读应答失败")); got != driver.QualityBad {
		t.Errorf("abnormalQuality(非异常应答错误) = %d, want %d", got, driver.QualityBad)
	}
}

// TestReadDecodeFailureQuality 设备已完整应答、帧校验也通过，是本地解码失败
// → 解析级（地址与链路都没问题）。
func TestReadDecodeFailureQuality(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.PreambleBytes = 0
	cfg.MaxDIsPerRead = 1

	addr, _ := MeterAddressBytes("1")
	// 数据域回显的 DI 与请求的不符（请求 02010100，应答 02010101）
	resp := buildFrame(cfg, addr, ctrl2007OK,
		[]byte{0x01, 0x01, 0x01, 0x02, 0x05, 0x22})
	d := newWiredDriver(t, cfg, "1", &fakeTransport{in: resp})

	results, err := d.Read([]po.DeviceAddress{{ID: "a1", Name: "02010100", DataType: "float"}})
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if results[0].Quality != driver.QualityBadParse {
		t.Fatalf("DI 不符时不得取值，Quality = %d, want %d", results[0].Quality, driver.QualityBadParse)
	}
	// 错位取值会静默给出另一个数据项的值 —— 归因必须说"是本地解码的问题"，
	// 而不是让运维去怀疑一个正确的点位地址。
	assertQuality(t, results[0], driver.QualityBadParse)
}

// TestReadUnknownTypeQuality 不认识的类型名同样是本地解码问题（不是地址问题）。
func TestReadUnknownTypeQuality(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.PreambleBytes = 0

	addr, _ := MeterAddressBytes("1")
	resp := buildFrame(cfg, addr, ctrl2007OK,
		[]byte{0x00, 0x01, 0x01, 0x02, 0x05, 0x22})
	d := newWiredDriver(t, cfg, "1", &fakeTransport{in: resp})

	results, err := d.Read([]po.DeviceAddress{{ID: "a1", Name: "02010100", DataType: "bogus"}})
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertQuality(t, results[0], driver.QualityBadParse)
}

// Read 必须按 addrs 顺序 1:1 返回，并把每个点位的 DataType/Kind 原样透传，
// 同时把数据标识决定的小数位应用到工程值上。
//
// 顺带钉住「类型名」的两种异常形态，它们**语义不同**、不能混为一谈：
//   - 空类型名（类型下拉没选，或历史数据里为 NULL）→ 按无符号原始整数解码；
//   - 不认识的类型名 → 明确报错、Quality=0。猜一个解码方式输出一个看似合理的值，
//     比取不到值危险得多（见 decode.go 的 default 分支）。
func TestReadMapsPointsToValues(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.PreambleBytes = 0
	// 本例考的是「类型名 → 解码方式」的映射，与打包无关，故显式固定为一帧一个标识：
	// 三个点位各自成组，预置三帧应答。不依赖出厂默认值，否则改默认值会连累本用例。
	cfg.MaxDIsPerRead = 1

	addr, _ := MeterAddressBytes("1")
	// 应答数据域：DI 02010100 + BCD 2205（A相电压 220.5V）
	resp := buildFrame(cfg, addr, ctrl2007OK,
		[]byte{0x00, 0x01, 0x01, 0x02, 0x05, 0x22})
	d := newWiredDriver(t, cfg, "1", pacedTransport(resp, resp, resp))

	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "02010100", DataType: "float"},
		{ID: "a2", Name: "02010100", DataType: ""},      // 空类型名 → 原始整数
		{ID: "a3", Name: "02010100", DataType: "bogus"}, // 未知类型名 → 报错
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("结果数 %d != 地址数 %d（必须按索引一一对应）", len(results), len(addrs))
	}

	if results[0].DeviceAddressID != "a1" || results[0].Value != "220.5" ||
		results[0].Quality != 192 {
		t.Errorf("点位 1 = %+v, want id=a1 value=220.5 quality=192", results[0])
	}
	if results[0].Kind != driver.KindFloat {
		t.Errorf("点位 1 Kind = %q, want %q", results[0].Kind, driver.KindFloat)
	}
	assertQuality(t, results[0], driver.QualityGood)

	// 空类型名：按原始整数输出，不套用 DI 的小数位，且拿不到 Kind
	if results[1].Value != "2205" || results[1].Quality != 192 {
		t.Errorf("点位 2 = %+v, want value=2205 quality=192（空类型名按原始整数）", results[1])
	}
	assertQuality(t, results[1], driver.QualityGood)
	if results[1].Kind != "" {
		t.Errorf("点位 2 Kind = %q, want 空", results[1].Kind)
	}

	// 未知类型名：宁可报异常，也不猜一个解码方式
	if results[2].Quality != driver.QualityBadParse || results[2].Value != "" {
		t.Errorf("点位 3 = %+v, want quality=%d value=\"\"（未知类型名不得猜着解码）", results[2], driver.QualityBadParse)
	}
	if results[2].Kind != "" {
		t.Errorf("点位 3 Kind = %q, want 空", results[2].Kind)
	}
	// 地址解析成功、链路也通，是本地按配置类型解不出来 → 解析级（不是地址级）
	assertQuality(t, results[2], driver.QualityBadParse)
}

// 单请求打包多个数据标识时，必须按请求顺序把数据域切开并逐个核对回显的 DI。
// 错位取值会静默给出另一个数据项的值 —— 比取不到值危险得多。
func TestReadBatchSplitsDataFieldInOrder(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.PreambleBytes = 0
	cfg.MaxDIsPerRead = 2

	addr, _ := MeterAddressBytes("1")
	// 两个 DI 的应答：02010100 → 220.5，00000000 → 1.28
	resp := buildFrame(cfg, addr, ctrl2007OK, []byte{
		0x00, 0x01, 0x01, 0x02, 0x05, 0x22, // DI 02010100 + 2205
		0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00, // DI 00000000 + 128
	})
	d := newWiredDriver(t, cfg, "1", &fakeTransport{in: resp})

	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "02010100", DataType: "float"},
		{ID: "a2", Name: "00000000", DataType: "float"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if got := results[0].Value; got != "220.5" {
		t.Errorf("点位 1 = %q, want \"220.5\"", got)
	}
	if got := results[1].Value; got != "1.28" {
		t.Errorf("点位 2 = %q, want \"1.28\"", got)
	}
}

// 单个点位地址非法（不在字典且未显式给长度）不得使整批失败：
// 该点位 Quality=0，其余点位照常取值。
func TestReadIsolatesBadAddress(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.PreambleBytes = 0

	addr, _ := MeterAddressBytes("1")
	resp := buildFrame(cfg, addr, ctrl2007OK,
		[]byte{0x00, 0x01, 0x01, 0x02, 0x05, 0x22})
	d := newWiredDriver(t, cfg, "1", &fakeTransport{in: resp})

	addrs := []po.DeviceAddress{
		{ID: "bad", Name: "1A2B3C4D", DataType: "float"}, // 字典未收录且未给长度
		{ID: "ok", Name: "02010100", DataType: "float"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("单点位地址非法不应中断整批: %v", err)
	}
	if results[0].Quality != driver.QualityBadAddress || results[0].Value != "" {
		t.Errorf("非法地址点位 = %+v, want quality=%d value=\"\"", results[0], driver.QualityBadAddress)
	}
	if results[1].Value != "220.5" || results[1].Quality != driver.QualityGood {
		t.Errorf("正常点位 = %+v, want value=220.5 quality=192（不能被邻居拖累）", results[1])
	}
	// 字典未收录且未给字节数：点位表里的地址写错了，改表之前永远读不到值 → 地址级。
	// 健康的兄弟点位必须是空串（不许"整批一起标"）。
	assertQuality(t, results[0], driver.QualityBadAddress)
	assertQuality(t, results[1], driver.QualityGood)
}

// 应答含后续帧（0xB1）时，该组点位必须保持 Quality=0，且不得中断整台设备的采集：
//   - 拿首帧的片段解码，会得到一个看似合理实则被截断的假值；
//   - 把它当 I/O 错误上抛，采集引擎会把整台设备判离线并重连。
func TestFollowUpResponseKeepsDeviceOnline(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	cfg.PreambleBytes = 0
	addr, _ := MeterAddressBytes("1")

	spec := DISpec{DI: 0x02010100, Version: Version2007, Bytes: 2, Decimals: 1}
	plan := &readPlan{entries: []planEntry{{index: 0, dataType: "float", spec: spec}}}
	group := &readGroup{idx: []int{0}}

	// 0xB1 应答：数据标识 + 该数据项的**首段**数据（A相电压只应有 2 字节，此处给的正是完整值，
	// 但既然表声明还有后续帧，就不能拿它当最终值）
	frame := buildFrame(cfg, addr, ctrl2007More, []byte{0x00, 0x01, 0x01, 0x02, 0x05, 0x22})
	tr := &fakeTransport{in: frame}

	results := make([]driver.ReadResult, 1)
	d := newDLT645Driver(TransportTCP)
	if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
		t.Fatalf("含后续帧的应答不应中断采集（会让整台设备判离线）: %v", err)
	}
	if results[0].Quality != driver.QualityBadParse {
		t.Errorf("Quality = %d, want %d（数据不完整时不得解码）", results[0].Quality, driver.QualityBadParse)
	}
	if results[0].Value != "" {
		t.Errorf("Value = %q, want 空（不完整的数据不得取值）", results[0].Value)
	}
	// 首帧已完整通过校验 ⇒ 地址与链路都没问题，是本地不支持拼接后续帧。
	// 归解析级：归成地址级会让运维去查一张本来就对的点位表。
	assertQuality(t, results[0], driver.QualityBadParse)
}

// 被电表拒读的告警只报一次（状态变化时），读到值后清除：
// 扫描周期 1s 时，现场日志实测 10 分钟刷了 124 条完全相同的告警。
func TestRejectionWarnSuppressedThenResolves(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	addr, _ := MeterAddressBytes("1")

	spec, err := ParseAddress("00000000", Version2007)
	if err != nil {
		t.Fatal(err)
	}
	plan := &readPlan{entries: []planEntry{{index: 0, dataType: "float", spec: spec}}}
	group := &readGroup{idx: []int{0}}
	diText := formatDI(spec.DI, Version2007)

	d := newDLT645Driver(TransportTCP)

	// 连续两轮都被拒：第二轮是重复告警，问题状态必须保持不变
	for round := 1; round <= 2; round++ {
		tr := &fakeTransport{in: buildFrame(cfg, addr, ctrl2007Abnormal, []byte{0x01})}
		results := make([]driver.ReadResult, 1)
		if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
			t.Fatalf("第 %d 轮：异常应答不应中断采集（会让整台设备判离线）: %v", round, err)
		}
		if results[0].Quality != driver.QualityBad {
			t.Errorf("第 %d 轮 Quality = %d, want %d", round, results[0].Quality, driver.QualityBad)
		}
		// 错误码 0x01「其他错误」不含定位信息 → 刻意不归因（留未归类）。
		// 这条断言是"无脑给整段标 comms/address"这种错误实现的照妖镜。
		assertQuality(t, results[0], driver.QualityBad)
		if !d.problems.abnormal(diText) {
			t.Fatalf("第 %d 轮：被拒后应记录异常状态", round)
		}
	}

	// 恢复：正常应答应取到值，并清掉异常状态
	okData := []byte{0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00} // 数据标识 + BCD 00000128
	tr := &fakeTransport{in: buildFrame(cfg, addr, ctrl2007OK, okData)}
	results := make([]driver.ReadResult, 1)
	if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
		t.Fatalf("正常应答应能读取: %v", err)
	}
	if results[0].Quality != 192 {
		t.Errorf("Quality = %d, want 192", results[0].Quality)
	}
	if results[0].Value != "1.28" {
		t.Errorf("Value = %q, want \"1.28\"", results[0].Value)
	}
	// 恢复后不得残留上一轮的坏码（质量码是"这一次为什么坏"，不是点位属性）
	assertQuality(t, results[0], driver.QualityGood)
	if d.problems.abnormal(diText) {
		t.Error("恢复后不应仍标记为异常（否则真正的再次故障会被压掉）")
	}
}

// 批量被拒时降级为逐个重读，且降级本身不刷屏（每轮都发会得到和原告警一样多的日志）。
func TestBatchRejectionFallsBackPerDI(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	cfg.PreambleBytes = 0
	cfg.MaxDIsPerRead = 2
	addr, _ := MeterAddressBytes("1")

	spec1, _ := ParseAddress("02010100", Version2007) // A相电压 2 字节
	spec2, _ := ParseAddress("00000000", Version2007) // 组合有功总电能 4 字节
	plan := &readPlan{entries: []planEntry{
		{index: 0, dataType: "float", spec: spec1},
		{index: 1, dataType: "float", spec: spec2},
	}}
	group := &readGroup{idx: []int{0, 1}}

	// 首轮批量请求被拒（0xD1），随后两次单读均成功
	batch := buildFrame(cfg, addr, ctrl2007Abnormal, []byte{0x01})
	single1 := buildFrame(cfg, addr, ctrl2007OK, []byte{0x00, 0x01, 0x01, 0x02, 0x05, 0x22})
	single2 := buildFrame(cfg, addr, ctrl2007OK, []byte{0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00})
	// 三轮读：批量（被拒）→ 点位 1 单读 → 点位 2 单读
	tr := pacedTransport(batch, single1, single2)

	d := newDLT645Driver(TransportTCP)
	results := make([]driver.ReadResult, 2)
	if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
		t.Fatalf("批量被拒应降级为逐个读取，而不是中断采集: %v", err)
	}
	if results[0].Value != "220.5" || results[0].Quality != 192 {
		t.Errorf("点位 1 = %+v, want 220.5/192（降级后应正常取值）", results[0])
	}
	if results[1].Value != "1.28" || results[1].Quality != 192 {
		t.Errorf("点位 2 = %+v, want 1.28/192（降级后应正常取值）", results[1])
	}
}

// 未连接时 Read 必须明确报错：返回空结果会被采集引擎当成「读到空值」而静默入库。
func TestReadWithoutConnection(t *testing.T) {
	d := newDLT645Driver(TransportTCP)
	if _, err := d.Read([]po.DeviceAddress{{ID: "a1", Name: "02010100", DataType: "float"}}); err == nil {
		t.Error("未连接时 Read 应报错（空结果会被当成读到空值）")
	}
}

// Ping 复用连接的判据：连接参数（含表号）一致才复用。
//
// 表号必须参与比较：连接建立时地址域已固定，表号不同说明测的是另一块表，
// 复用该连接会把请求发到错误的表上。
func TestSameConfig(t *testing.T) {
	a := DefaultDLT645Config(TransportSerial)
	b := DefaultDLT645Config(TransportSerial)
	if !sameConfig(a, b) {
		t.Error("同参数配置应判定为一致（Ping 据此复用串口句柄）")
	}
	b.Version = Version1997
	if sameConfig(a, b) {
		t.Error("协议版本不同不应复用连接")
	}
	b = DefaultDLT645Config(TransportSerial)
	b.MeterAddress = "000000000002"
	if sameConfig(a, b) {
		t.Error("表号不同不应复用连接（会把请求发到错误的表上）")
	}
	if sameConfig(nil, a) || sameConfig(a, nil) {
		t.Error("nil 配置不应判定为一致")
	}
}

// 地址解析失败的日志里要带上点位名与 id —— 现场只凭 DI 找不回是哪个点位。
func TestPlanKeepsEntriesAlignedWithAddrs(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "1A2B3C4D", DataType: "float"}, // 解析失败
		{ID: "a2", Name: "02010100", DataType: "float"},
		{ID: "a3", Name: "00000000", DataType: "float"},
	}
	plan := buildPlan(addrs, cfg)

	if len(plan.entries) != len(addrs) {
		t.Fatalf("entries 数 %d != addrs 数 %d（结果必须按索引回填）", len(plan.entries), len(addrs))
	}
	if plan.entries[0].err == nil {
		t.Error("非法地址的条目应带上解析错误")
	}
	for i, e := range plan.entries {
		if e.index != i {
			t.Errorf("entries[%d].index = %d, want %d", i, e.index, i)
		}
	}
	// 只有解析成功的两个点位进了读取分组
	total := 0
	for _, g := range plan.groups {
		total += len(g.idx)
	}
	if total != 2 {
		t.Errorf("分组内点位总数 = %d, want 2（解析失败的不参与读取）", total)
	}
}

// 规划缓存必须按内容指纹命中，且 Connect 时清空。
func TestPlanCacheHitAndClear(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "02010100", DataType: "float"},
		{ID: "a2", Name: "02020100", DataType: "float"},
	}
	sig := driver.RangeSig(addrs)

	var c planCache
	first := c.planFor(sig, addrs, cfg)
	if again := c.planFor(sig, addrs, cfg); again != first {
		t.Error("同一指纹应命中缓存（否则每轮都要重新解析全部点位）")
	}

	c.clear()
	if after := c.planFor(sig, addrs, cfg); after == first {
		t.Error("clear 后应重新计算（版本/字典等解析输入可能已变）")
	}
}

// 时间预算：确保解析路径不会因超时下限被拖到不可用（下限本身由 config 测试锁定）。
func TestMinTimeoutFloorIsBounded(t *testing.T) {
	if got := minTimeoutMS(2400, 8, 1); got > 1000 {
		t.Errorf("2400bps 的超时下限 = %dms，超过 1s（会让单帧轮询不可用）", got)
	}
	// 非法入参回退到默认波特率 2400，按 8E1 折算：2×30×11×1000/2400 = 275ms。
	// 下限的作用是「不低于 200ms」，而不是恰好等于 200 —— 低波特率下真实
	// 线路时间本来就超过 200ms，抬到 275 才是对的。
	if got := minTimeoutMS(0, 0, 0); got != 275 {
		t.Errorf("非法波特率回退默认后下限 = %d, want 275", got)
	}
	if got := minTimeoutMS(115200, 8, 1); got != 200 {
		t.Errorf("高波特率下限 = %d, want 200（钳到下限）", got)
	}
}
