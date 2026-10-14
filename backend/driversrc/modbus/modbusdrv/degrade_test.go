// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeModbus 假 Modbus 设备：missing 里的寄存器在设备上不存在。
//
// 任何**覆盖**了不存在寄存器的读取都被整段拒绝为 IllegalDataAddress (0x02) ——
// 这正是现场形态，也是本组用例针对的那一件事：设备拒绝的是整个请求，不会逐项回
// 一个"这一个失败"。所以一个寄存器写错，整段点位一起没有值。
//
// 读得通时返回的寄存器里写着**它自己的寄存器地址**（int16、大端，与
// DefaultModbusTcpConfig 的字节序一致）：于是"降级后按新起点算偏移"一旦算错，
// 断言里的值立刻对不上，而不是表现成一条看着完全合理的曲线。
type fakeModbus struct {
	mu      sync.Mutex
	missing map[uint16]bool
	busy    bool // 从站忙（非地址类异常）
	calls   []mockReadCall
}

func newFakeModbus(missing ...uint16) *fakeModbus {
	f := &fakeModbus{missing: make(map[uint16]bool, len(missing))}
	for _, r := range missing {
		f.missing[r] = true
	}
	return f
}

// drop 让某个寄存器变得可读（模拟现场把寄存器补上 / 修对 string 的声明长度）。
func (f *fakeModbus) drop(reg uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.missing, reg)
}

// setBusy 让设备回"从站忙"（一个与地址无关、可恢复的状态）。
func (f *fakeModbus) setBusy(busy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy = busy
}

func (f *fakeModbus) read(start, quantity uint16) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, mockReadCall{addr: start, qty: quantity})
	busy := f.busy
	f.mu.Unlock()

	if busy {
		return nil, modbusExc(goburrowModbus.ExceptionCodeServerDeviceBusy)
	}

	data := make([]byte, int(quantity)*2)
	for i := 0; i < int(quantity); i++ {
		reg := start + uint16(i)
		f.mu.Lock()
		miss := f.missing[reg]
		f.mu.Unlock()
		if miss {
			return nil, modbusExc(goburrowModbus.ExceptionCodeIllegalDataAddress)
		}
		binary.BigEndian.PutUint16(data[i*2:], reg)
	}
	return data, nil
}

func (f *fakeModbus) callsSince(n int) []mockReadCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mockReadCall(nil), f.calls[n:]...)
}

// 四个读方法让假设备满足 modbusPointReader（驱动真实读路径的入参）。
func (f *fakeModbus) ReadHoldingRegisters(addr, qty uint16) ([]byte, error) { return f.read(addr, qty) }
func (f *fakeModbus) ReadInputRegisters(addr, qty uint16) ([]byte, error)   { return f.read(addr, qty) }
func (f *fakeModbus) ReadCoils(addr, qty uint16) ([]byte, error)            { return f.read(addr, qty) }
func (f *fakeModbus) ReadDiscreteInputs(addr, qty uint16) ([]byte, error)   { return f.read(addr, qty) }

// runRound 跑**驱动真实的一轮采集**（readBatch），只把客户端换成假设备。
//
// 刻意不在测试里重写那个循环：重写的版本与生产代码是两份实现，用例量到的是
// 测试自己那版，"回写降级计划""下轮接着摘"这些行为一旦和驱动里写的不同，
// 测试反而会替错的实现背书。
func runRound(t *testing.T, reader modbusPointReader, cache *planCache, sig uint64,
	addrs []po.DeviceAddress, cfg *ModbusTcpConfig) []driver.ReadResult {

	t.Helper()
	plan, err := cache.planFor(sig, addrs, cfg)
	if err != nil {
		t.Fatalf("planFor = %v", err)
	}
	out, err := readBatch("modbus", reader, plan, cache, sig, addrs, cfg, new(warnSet))
	if err != nil {
		t.Fatalf("readBatch = %v", err)
	}
	return out
}

// calcRanges 算出这批点位的合并区间，并断言确实只有一个（各用例的场景前提）。
func calcRanges(t *testing.T, addrs []po.DeviceAddress) []AddressRange {
	t.Helper()
	ranges, err := CalcReadRanges(addrs, CalcReadRangeOptions{MergeWindow: 125})
	if err != nil {
		t.Fatalf("CalcReadRanges failed: %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("场景前提：want 1 merged range, got %d: %+v", len(ranges), ranges)
	}
	return ranges
}

// mkRegs 造 n 个从 base 起的 int16 点位。
func mkRegs(base, n uint16) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, 0, n)
	for i := uint16(0); i < n; i++ {
		addrs = append(addrs, po.DeviceAddress{
			ID: fmt.Sprintf("id%d", i), Name: fmt.Sprintf("%d", base+i),
			DataType: "int16", CommonDataType: "int16",
		})
	}
	return addrs
}

// TestReadRegisterRanges_SingleBadPointOnly 合并区间里混进一个设备上不存在的
// 寄存器时，只有那一个点位 Quality=0，同区间其余点位照常取值。
//
// 这正是本次修复前的形态：整段被拒 → 区间里**所有**点位一起 Quality=0，
// 而设备其实只是不认识 102 这一个寄存器（100 完全是好的）。
func TestReadRegisterRanges_SingleBadPointOnly(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "p1", Name: "100", DataType: "int16", CommonDataType: "int16"},
		{ID: "p2", Name: "101", DataType: "int16", CommonDataType: "int16"},
		{ID: "p3", Name: "102", DataType: "int16", CommonDataType: "int16"},
		{ID: "p4", Name: "103", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()
	// 四个点连续，合并成一个区间 [100, 4)
	ranges := calcRanges(t, addrs)
	if ranges[0].Quantity != 4 {
		t.Fatalf("场景前提：want 1 dense range of qty 4, got %v", ranges)
	}

	f := newFakeModbus(102)
	results, err := readRegisterRanges(f.read, addrs, ranges, cfg, FuncCodeReadHoldingRegisters, newReadPlanState(nil))
	if err != nil {
		t.Fatalf("readRegisterRanges = %v", err)
	}

	want := map[string]struct {
		val string
		q   int
	}{
		"p1": {"100", driver.QualityGood},
		"p2": {"101", driver.QualityGood},
		// 必须是 QualityBadAddress：拆到单点仍被拒 = 这个寄存器在设备上不存在，
		// 与"从站忙"那种下轮会恢复的通信级失败完全不是一回事。
		"p3": {"", driver.QualityBadAddress},
		"p4": {"103", driver.QualityGood},
	}
	if len(results) != 4 {
		t.Fatalf("want 4 results, got %d", len(results))
	}
	for _, r := range results {
		w, ok := want[r.DeviceAddressID]
		if !ok {
			t.Fatalf("意外点位 %s", r.DeviceAddressID)
		}
		if r.Value != w.val || r.Quality != w.q {
			t.Errorf("%s = %+v, want val=%q quality=%d",
				r.DeviceAddressID, r, w.val, w.q)
		}
		if r.DataType != "int16" || r.Kind == "" {
			t.Errorf("%s 的 DataType/Kind 必须回填，got %+v", r.DeviceAddressID, r)
		}
	}

	// 整段先被拒一次，随后对半拆：左半 [100,2) 读通，右半 [102,2) 再拆成两个单点
	wantCalls := []mockReadCall{{100, 4}, {100, 2}, {102, 2}, {102, 1}, {103, 1}}
	got := f.callsSince(0)
	if len(got) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", got, wantCalls)
	}
	for i := range got {
		if got[i] != wantCalls[i] {
			t.Fatalf("calls = %v, want %v", got, wantCalls)
		}
	}
}

// TestReadBitRanges_SingleBadPointOnly 位区间（FC1/FC2）同样只摘坏点。
//
// 位与寄存器是两套单位：拿位数当寄存器数去切不会报错，只会把读头落到别的位/寄存器
// 上 —— 所以这条单独钉一次，请求序列逐条断言。
func TestReadBitRanges_SingleBadPointOnly(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "b1", Name: "10", DataType: "bool", CommonDataType: "bool"},
		{ID: "b2", Name: "11", DataType: "bool", CommonDataType: "bool"},
		{ID: "b3", Name: "12", DataType: "bool", CommonDataType: "bool"},
	}
	ranges := calcRanges(t, addrs)
	if ranges[0].Quantity != 3 {
		t.Fatalf("场景前提：want 1 bit range of qty 3, got %v", ranges)
	}

	// 位区间：设备按位地址回 IllegalDataAddress
	f := newFakeModbus(12)
	results, err := readBitRanges(f.read, addrs, ranges, FuncCodeReadCoils, newReadPlanState(nil))
	if err != nil {
		t.Fatalf("readBitRanges = %v", err)
	}

	qualities := map[string]int{}
	for _, r := range results {
		qualities[r.DeviceAddressID] = r.Quality
	}
	if qualities["b1"] != driver.QualityGood || qualities["b2"] != driver.QualityGood {
		t.Errorf("正常点位的质量码必须是 QualityGood，got %v", qualities)
	}
	if qualities["b3"] != driver.QualityBadAddress {
		t.Errorf("b3 Quality = %d, want %d（位区间同样只摘坏点，是地址级）",
			qualities["b3"], driver.QualityBadAddress)
	}

	wantCalls := []mockReadCall{{10, 3}, {10, 1}, {11, 2}, {11, 1}, {12, 1}}
	calls := f.callsSince(0)
	if len(calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
	for i := range calls {
		if calls[i] != wantCalls[i] {
			t.Fatalf("calls = %v, want %v", calls, wantCalls)
		}
	}
}

// TestReadRanges_NonAddressExceptionKeepsRange 非地址类异常（0x06 从站忙）说明不了
// 任何一个地址是否存在，因此**不得**触发降级、也不得把区间缓存成降级计划：
//
//	① 不发对半降级的请求（一次请求就够 —— 逐个去问只会白挨一遍）；
//	② 整段原样留在计划里（下轮照旧整段重试，而不是被拆成单点、更不是被摘掉）；
//	③ 设备恢复正常之后一轮就全部取到值。
//
// 把它误当成地址问题时，一次"从站忙"会把整段点位逐个"确认"成地址不存在并缓存
// 5 分钟 —— 比修复前（一轮 quality=0、下轮重试）还糟。
func TestReadRanges_NonAddressExceptionKeepsRange(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "p1", Name: "100", DataType: "int16", CommonDataType: "int16"},
		{ID: "p2", Name: "102", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()
	calcRanges(t, addrs) // 场景前提：合并成一个区间

	f := newFakeModbus(102) // 102 确实不存在，但设备回的是"忙"
	f.setBusy(true)
	cache := &planCache{}
	sig := driver.RangeSig(addrs)

	results := runRound(t, f, cache, sig, addrs, cfg)
	for i, r := range results {
		if r.Value != "" {
			t.Errorf("result[%d] = %+v, want 空值（本轮取不到值）", i, r)
		}
		// 整段被拒且说明不了任何地址是否存在 → 只能到通信级。
		// 标成 address 会误导下游报警把一次"从站忙"报成"测点坏了"。
		if r.Quality != driver.QualityBadComms {
			t.Errorf("result[%d] Quality = %d, want %d", i, r.Quality, driver.QualityBadComms)
		}
	}

	// ① 只发了那一次整段请求：没有降级出去逐个问
	if n := len(f.callsSince(0)); n != 1 {
		t.Errorf("非地址类异常不该触发降级，发了 %d 次请求，want 1", n)
	}

	// ② 没有留下降级计划：下轮仍按合并区间整段重试
	cache.mu.Lock()
	_, degraded := cache.degraded[sig]
	cache.mu.Unlock()
	if degraded {
		t.Error("非地址类异常被缓存成了降级计划")
	}

	// ③ 设备恢复正常：好点位一轮内取到值
	f.setBusy(false)
	results = runRound(t, f, cache, sig, addrs, cfg)
	if results[0].Value != "100" || results[0].Quality != driver.QualityGood {
		t.Errorf("100 恢复后 = %+v, want val=100 quality=%d", results[0], driver.QualityGood)
	}
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("102 仍不存在，应为 QualityBadAddress，got %+v", results[1])
	}
	// 恢复的这一轮 100 正常 → 不变量：正常即无成因。
	if results[0].Quality != driver.QualityGood {
		t.Errorf("100 恢复后质量码不是 QualityGood，got %d", results[0].Quality)
	}
	// 102 这次是设备**明确拒绝**（不再是忙），拆到单点仍被拒 → 地址级。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("102 Quality = %d, want %d（本轮是明确的地址类拒绝）",
			results[1].Quality, driver.QualityBadAddress)
	}
}

// TestReadRanges_DegradedPlanCached 降级结果进规划缓存：下一轮直接读可读子区间，
// 不再重发那个必然失败的合并区间。
//
// 不回写的代价不是"多几个请求"，而是**每轮**都为同一个坏地址重跑一遍降级，
// 采集周期被拖垮（1s 周期下几十个坏地址就顶不住）。
func TestReadRanges_DegradedPlanCached(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "p1", Name: "100", DataType: "int16", CommonDataType: "int16"},
		{ID: "p2", Name: "101", DataType: "int16", CommonDataType: "int16"},
		{ID: "p3", Name: "102", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()
	calcRanges(t, addrs) // 场景前提：合并成一个区间

	f := newFakeModbus(102)
	cache := &planCache{}
	sig := driver.RangeSig(addrs)

	runRound(t, f, cache, sig, addrs, cfg)
	afterFirst := len(f.callsSince(0))

	results := runRound(t, f, cache, sig, addrs, cfg)

	// 第二轮只发读得通的那些子区间：坏地址已被摘出计划。
	// 对半降级产出的是**没被重新合并过**的子区间（100 与 101 各一条），
	// 不再合并是刻意的：带缺口重新合并会把刚摘掉的坏地址又并回来。
	wantCalls := []mockReadCall{{100, 1}, {101, 1}}
	calls := f.callsSince(afterFirst)
	if len(calls) != len(wantCalls) {
		t.Fatalf("第二轮 calls = %v, want %v（降级结果没进缓存？）", calls, wantCalls)
	}
	for i := range calls {
		if calls[i] != wantCalls[i] {
			t.Fatalf("第二轮 calls = %v, want %v（降级结果没进缓存？）", calls, wantCalls)
		}
	}
	if results[0].Value != "100" || results[0].Quality != driver.QualityGood {
		t.Errorf("第二轮 100 = %+v, want val=100 quality=%d", results[0], driver.QualityGood)
	}
	if results[2].Quality != driver.QualityBadAddress {
		t.Errorf("第二轮 102 = %+v, want quality=%d", results[2], driver.QualityBadAddress)
	}
	// **跨轮保持**：102 第二轮已经不在计划里、不会再被解析，成因只能靠缓存带过来。
	// 不缓存的话这里就是空串 —— 那意味着"设备上根本没有这个地址"这件事只在
	// 确认那一轮可见（1/N 的轮次），点位级报警按连续 N 轮判定时永远触发不了。
	if results[2].Quality != driver.QualityBadAddress {
		t.Errorf("第二轮 102 Quality = %d, want %d（成因必须随降级计划一起缓存）",
			results[2].Quality, driver.QualityBadAddress)
	}
}

// TestReadRanges_DegradedPlanExpires 降级结果到期后重新按合并区间试一次：
// 设备侧把寄存器补上（或把 string 的声明长度改对）之后，采集能自己恢复。
//
// 两条一起钉：① 到期后确实回退到合并规划；② 续写降级结果**不刷新时间戳** ——
// 每轮刷新会让 TTL 变成"永不过期"，批次里只要还有另一个区间在降级，被摘出的
// 地址就永远回不来（见 storeDegraded 的说明）。
func TestReadRanges_DegradedPlanExpires(t *testing.T) {
	addrs := []po.DeviceAddress{
		{ID: "p1", Name: "100", DataType: "int16", CommonDataType: "int16"},
		{ID: "p2", Name: "102", DataType: "int16", CommonDataType: "int16"},
	}
	cfg := DefaultModbusTcpConfig()
	calcRanges(t, addrs) // 场景前提：合并成一个区间

	f := newFakeModbus(102)
	cache := &planCache{}
	sig := driver.RangeSig(addrs)
	runRound(t, f, cache, sig, addrs, cfg)

	// ① 续写不刷新时间戳
	cache.mu.Lock()
	first := cache.degraded[sig].at
	cache.degraded[sig] = degradedPlan{ // 模拟又降级了一次
		ranges: cache.degraded[sig].ranges, at: first,
	}
	cache.mu.Unlock()
	cache.storeDegraded(sig,
		map[byte][]AddressRange{FuncCodeReadHoldingRegisters: {{StartAddress: 100, Quantity: 1, AddressMap: map[string]uint16{"p1": 100}}}},
		map[string]int{"p2": driver.QualityBadAddress})
	cache.mu.Lock()
	again := cache.degraded[sig].at
	cache.mu.Unlock()
	if !again.Equal(first) {
		t.Errorf("续写降级结果刷新了时间戳（%v → %v）：TTL 会变成永不过期", first, again)
	}

	// ② 到期后回退到合并规划；设备侧补上 102 之后一轮就全取到值
	f.drop(102)
	cache.mu.Lock()
	d := cache.degraded[sig]
	d.at = time.Now().Add(-degradedPlanTTL - time.Second)
	cache.degraded[sig] = d
	cache.mu.Unlock()

	results := runRound(t, f, cache, sig, addrs, cfg)
	for i, want := range []string{"100", "102"} {
		if results[i].Value != want || results[i].Quality != 192 {
			t.Errorf("result[%d] = %+v, want val=%s quality=192（到期后应重新合并读取）", i, results[i], want)
		}
	}
}

// TestReadRanges_DegradeConvergesAcrossRounds 预算不够一轮摘完时，"下轮接着摘"
// 必须真的发生：**好点位不能因为落在未访问的子树里而永远停在 Quality=0**。
//
// 场景刻意做成一轮摘不完：80 个点合并成一个区间，前 40 个（100..139）在设备上
// 都不存在、后 40 个（140..179）是好的。摘完前 40 个要 2*40-1 = 79 次请求，
// 超过 64 的预算，所以第一轮必然在中途停下 —— 此时后 40 个点位一次都没被读过。
//
// 修复前的形态（把父区间原样放回计划，或"含空洞才降级"那条路）下，下一轮不会
// 有任何进展，140..179 永远是 Quality=0（设备答得好好的）。
func TestReadRanges_DegradeConvergesAcrossRounds(t *testing.T) {
	cfg := DefaultModbusTcpConfig()
	addrs := mkRegs(100, 80)
	ranges := calcRanges(t, addrs)
	if ranges[0].Quantity != 80 {
		t.Fatalf("场景前提：want 1 merged range of qty 80, got %v", ranges)
	}

	f := newFakeModbus()
	for reg := uint16(100); reg < 140; reg++ {
		f.missing[reg] = true
	}
	cache := &planCache{}
	sig := driver.RangeSig(addrs)

	const healthyFrom = 140
	for round := 0; round < 6; round++ {
		results := runRound(t, f, cache, sig, addrs, cfg)
		if len(results) != len(addrs) {
			t.Fatalf("round %d: want %d results, got %d", round, len(addrs), len(results))
		}
		ok := true
		for i, r := range results {
			reg := 100 + uint16(i)
			if reg >= healthyFrom {
				if r.Quality != driver.QualityGood || r.Value != fmt.Sprintf("%d", reg) {
					ok = false
				}
			} else if r.Quality != driver.QualityBadAddress && r.Quality != driver.QualityBad {
				// 未收敛前允许仍是「未归类」（本轮还没轮到它），但绝不能是 comms/parse/Good
				t.Fatalf("round %d: %d 设备上不存在，却报了 quality=%d", round, reg, r.Quality)
			}
		}
		if ok {
			if round > 3 {
				t.Errorf("花了 %d 轮才把好点位取回来（应在一两轮内收敛）", round+1)
			}
			// 收敛时**每一个**坏点位都必须带着 address 成因，而不只是本轮刚确认的那些。
			// 这是跨轮保持的守卫：成因只在确认那一轮由降级路径算出，此后该地址已被摘出
			// 计划、不再参与任何解析 —— 不随降级计划一起缓存，除最后一轮外全都会退化成空串。
			for i, r := range results {
				if reg := 100 + uint16(i); reg < healthyFrom && r.Quality != driver.QualityBadAddress {
					t.Errorf("round %d: %d Quality = %d, want %d（成因未跨轮保持）",
						round, reg, r.Quality, driver.QualityBadAddress)
				}
			}
			return
		}
	}
	t.Fatal("6 轮之后 140..179 仍取不到值：未摘完的子区间没有留在计划里，下轮没有接着摘")
}

// TestReadRanges_DegradeBudgetBounds 一整段寄存器在设备上都不存在时，对半降级会
// 退化成一棵满二叉树。请求数上限必须把它兜住：每一轮的请求数都有界、
// 点位如实保持 Quality=0（不静默截断，也不无限对半下去）。
func TestReadRanges_DegradeBudgetBounds(t *testing.T) {
	cfg := DefaultModbusTcpConfig()
	addrs := mkRegs(100, 40)
	calcRanges(t, addrs) // 场景前提：合并成一个区间

	f := newFakeModbus()
	for reg := uint16(100); reg < 140; reg++ {
		f.missing[reg] = true
	}
	cache := &planCache{}
	sig := driver.RangeSig(addrs)

	for round := 0; round < 5; round++ {
		before := len(f.callsSince(0))
		results := runRound(t, f, cache, sig, addrs, cfg)
		deferred := 0
		for i, r := range results {
			if (r.Quality != driver.QualityBadAddress && r.Quality != driver.QualityBad) || r.Value != "" {
				t.Fatalf("round %d: result[%d] = %+v, want 坏码（已确认 address 或未归类）/empty（整段都读不到）", round, i, r)
			}
			// 这一批全是"设备上不存在"，故质量码只能是 QualityBadAddress（已确认）或
			// QualityBad（预算耗尽、本轮暂缓 —— 那是延后，不是设备判定）。
			// **绝不能是 comms/parse**：把暂缓当成通信级，正是"无脑给预填骨架全标 comms"
			// 那种实现的症状。
			switch r.Quality {
			case driver.QualityBadAddress:
			case driver.QualityBad:
				deferred++
			default:
				t.Fatalf("round %d: result[%d] Quality = %d, 只允许 %d 或未归类",
					round, i, r.Quality, driver.QualityBadAddress)
			}
		}
		// 首轮必然摘不完（摘完 40 个要 79 次请求 > 64 的预算），必须留下暂缓的点位。
		if round == 0 && deferred == 0 {
			t.Error("首轮没有任何暂缓点位：预算没有生效，或暂缓点位被误标了成因")
		}
		// 首轮整段 1 次 + 降级最多 degradeMaxRequests 次。后续轮次同样有界 ——
		// 预算是每轮新发的，不能靠"上轮已经花掉了"来兜底。
		if n := len(f.callsSince(before)); n > 1+degradeMaxRequests {
			t.Errorf("round %d: calls = %d, want <= %d（预算没有兜住对半降级）", round, n, 1+degradeMaxRequests)
		}
	}

	// 全部确认读不到之后，计划里不该再留着它们（留着就是每轮重付一次必然失败的往返）
	cache.mu.Lock()
	left := cache.degraded[sig].ranges[FuncCodeReadHoldingRegisters]
	cache.mu.Unlock()
	if len(left) != 0 {
		t.Errorf("全部确认读不到之后计划里仍有 %d 个区间，want 0", len(left))
	}
}
