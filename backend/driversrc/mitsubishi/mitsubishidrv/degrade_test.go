// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeMC 假 MC 设备：missing 里的地址不存在。
//
// 任何**覆盖**了不存在地址的读取都被整段拒绝（结束码非 0）——这正是现场形态，
// 也是本组用例针对的那一件事：设备拒绝的是整个请求，不会逐项回一个"这几项失败"。
// 所以一个地址写错，整段点位一起没有值。
//
// **missing 里的地址是"请求帧里携带的那一个"**，不是原始点位地址：字设备是字地址，
// 位设备的字访问是 16 对齐后的位地址（见 CalcMCRanges），位模式是位地址。
// 假设备按请求里的 head 判定，正是为了钉住"降级重切时用的是哪个单位" ——
// 单位混用会把读取头落到别的字上，读到的是**别处**的数据，而值看着完全合理。
type fakeMC struct {
	mu      sync.Mutex
	missing map[string]map[uint32]bool // 设备名 → 不存在的地址
	calls   []mcCall
}

type mcCall struct {
	device  string
	head    uint32
	points  uint16
	bitMode bool
}

func newFakeMC() *fakeMC {
	return &fakeMC{missing: make(map[string]map[uint32]bool)}
}

// miss 标记某设备的某个地址不存在。
func (f *fakeMC) miss(device string, addrs ...uint32) *fakeMC {
	m := f.missing[device]
	if m == nil {
		m = make(map[uint32]bool)
		f.missing[device] = m
	}
	for _, a := range addrs {
		m[a] = true
	}
	return f
}

// drop 让某设备的某个地址变得可读（模拟现场把软元件补上 / 启用文件寄存器区）。
func (f *fakeMC) drop(device string, addr uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.missing[device], addr)
}

func (f *fakeMC) Read(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, mcCall{device: device.name, head: head, points: points, bitMode: bitMode})
	f.mu.Unlock()

	miss := f.missing[device.name]
	if bitMode {
		data := make([]byte, points)
		for i := 0; i < int(points); i++ {
			a := head + uint32(i)
			if miss[a] {
				return nil, newEndCodeError(endCodeAddrRange)
			}
			// 位值取地址奇偶：读头落错位置时值会跟着变，而不是只差个 0/1
			if a%2 == 1 {
				data[i] = 1
			}
		}
		return data, nil
	}

	// 字模式：每点 2 字节小端，内容就是**它自己的字地址**
	data := make([]byte, int(points)*2)
	for i := 0; i < int(points); i++ {
		w := head + uint32(i)
		if miss[w] {
			return nil, newEndCodeError(endCodeAddrRange)
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(w))
	}
	return data, nil
}

func (f *fakeMC) IsConnected() bool { return true }
func (f *fakeMC) Close() error      { return nil }

// callsSince 返回第 n 次调用之后的读取序列（用于断言"这一轮发了哪些请求"）。
func (f *fakeMC) callsSince(n int) []mcCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mcCall(nil), f.calls[n:]...)
}

// TestDriverReadDegradesOnlyBadPoint 合并区间里混进一个设备上没有的地址时，
// 只有那一个点位 Quality=0，同区间其余点位照常取值。
//
// 这正是本次修复前的形态：整段被拒 → 区间里**所有**点位一起 Quality=0，
// 而设备其实只是不认识 D102 这一个字（D100 完全是好的）。
func TestDriverReadDegradesOnlyBadPoint(t *testing.T) {
	cfg := DefaultMCConfig() // MaxGap=8 → D100 与 D102 合并成一个区间 [100, 3)
	f := newFakeMC().miss("D", 102)
	d := &mcDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}

	// D100 正常取到值（假设备把字地址写进值里，降级后偏移算错会立刻对不上）
	if results[0].DeviceAddressID != "1" || results[0].Value != "100" || results[0].Quality != 192 {
		t.Errorf("D100 = %+v, want val=100 quality=192", results[0])
	}
	if results[0].Quality != driver.QualityGood {
		t.Errorf("D100 质量码不是 QualityGood，got %d", results[0].Quality)
	}
	// D102 确实不存在：只有它 Quality=0，且 DataType/Kind 必须原样回填
	if results[1].DeviceAddressID != "2" || results[1].Value != "" || results[1].Quality != driver.QualityBadAddress {
		t.Errorf("D102 = %+v, want val=\"\" quality=%d", results[1], driver.QualityBadAddress)
	}
	// 成因必须是 address：拆到单点仍被拒 = 这个字地址在设备上不存在，
	// 与"设备拒绝但说不清是哪个地址"那种下轮会恢复的通信级失败完全不同。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("D102 Quality = %d, want %d", results[1].Quality, driver.QualityBadAddress)
	}
	if results[1].DataType != "int16" || results[1].Kind == "" {
		t.Errorf("D102 读不到时 DataType/Kind 仍须回填，got %+v", results[1])
	}

	// 整段先被拒一次，随后拆成两个单点各读一次
	want := []mcCall{
		{"D", 100, 3, false},
		{"D", 100, 1, false},
		{"D", 102, 1, false},
	}
	if got := f.callsSince(0); !sameCalls(got, want) {
		t.Errorf("read calls = %+v, want %+v", got, want)
	}
}

// TestDriverReadDegradesMiddlePoint 坏地址夹在中间：前后两个点位都要取到值，
// 只有中间那个为 0（对半降级必须递归到把中间的坏地址单独摘出来）。
func TestDriverReadDegradesMiddlePoint(t *testing.T) {
	cfg := DefaultMCConfig() // 三点合并成一个区间 [100, 11)
	f := newFakeMC().miss("D", 105)
	d := &mcDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D105", "int16"),
		mkAddr("3", "D110", "int16"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}

	// 两头正常点位的成因必须是空串（不变量：正常即无成因），中间的坏地址是 address。
	want := []struct {
		id, val string
		q       int
	}{
		{"1", "100", driver.QualityGood},
		{"2", "", driver.QualityBadAddress},
		{"3", "110", driver.QualityGood},
	}
	for i, w := range want {
		if results[i].DeviceAddressID != w.id || results[i].Value != w.val || results[i].Quality != w.q {
			t.Errorf("result[%d] = %+v, want id=%s val=%q quality=%d", i, results[i], w.id, w.val, w.q)
		}
	}
}

// TestDriverReadDegradesBitMode 位模式（bool 位设备）走的是**位地址**：
// 降级重切时必须继续按位切，不能退化成按字。
//
// 混用的后果不是报错，而是把读取头落到别的位/字上 —— 读到的是别处的数据。
func TestDriverReadDegradesBitMode(t *testing.T) {
	cfg := DefaultMCConfig() // MaxGap=8 → M11/M13 合并成位区间 [11, 3)
	f := newFakeMC().miss("M", 13)
	d := &mcDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "M11", "bool"),
		mkAddr("2", "M13", "bool"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	// M11 是奇数位 → 假设备的位值为 1；M13 不存在 → Quality=0
	if results[0].Value != "1" || results[0].Quality != 192 {
		t.Errorf("M11 = %+v, want val=1 quality=192", results[0])
	}
	if results[0].Quality != driver.QualityGood {
		t.Errorf("M11 质量码不是 QualityGood，got %d", results[0].Quality)
	}
	if results[1].Value != "" || results[1].Quality != driver.QualityBadAddress {
		t.Errorf("M13 = %+v, want val=\"\" quality=%d", results[1], driver.QualityBadAddress)
	}
	// 位模式下拆到单个位仍被拒 = 这个位地址在设备上不存在 → 地址级成因。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("M13 Quality = %d, want %d", results[1].Quality, driver.QualityBadAddress)
	}

	want := []mcCall{
		{"M", 11, 3, true},
		{"M", 11, 1, true},
		{"M", 13, 1, true},
	}
	if got := f.callsSince(0); !sameCalls(got, want) {
		t.Errorf("read calls = %+v, want %+v（位模式降级必须按位切）", got, want)
	}
}

// TestDriverReadDegradesBitDeviceWordAccess 位设备的**字访问**（M100 配 int16）：
// 请求里的 head 是 16 对齐后的位地址（100 → 96），降级重切必须沿用这个单位。
//
// 用 Number（100）当 head 发出去，设备会读到 D 区间之外或直接拒绝 ——
// 而这条路径上没有任何东西会报错。
func TestDriverReadDegradesBitDeviceWordAccess(t *testing.T) {
	cfg := DefaultMCConfig()
	cfg.MaxGap = 20 // 96 与 112 的间隙 14 才会被合并成一个区间
	f := newFakeMC().miss("M", 112)
	d := &mcDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "M100", "int16"), // Word = floor(100/16)*16 = 96
		mkAddr("2", "M120", "int16"), // Word = floor(120/16)*16 = 112
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	// 字 96 的值就是 96：head 用 Number（100）去读会拿到别的字
	if results[0].Value != "96" || results[0].Quality != 192 {
		t.Errorf("M100 = %+v, want val=96 quality=192", results[0])
	}
	if results[0].Quality != driver.QualityGood {
		t.Errorf("M100 质量码不是 QualityGood，got %d", results[0].Quality)
	}
	if results[1].Value != "" || results[1].Quality != driver.QualityBadAddress {
		t.Errorf("M120 = %+v, want val=\"\" quality=%d", results[1], driver.QualityBadAddress)
	}
	// 字访问下拆到单个字仍被拒 = 这个（16 对齐后的）字地址不存在 → 地址级成因。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("M120 Quality = %d, want %d", results[1].Quality, driver.QualityBadAddress)
	}

	want := []mcCall{
		{"M", 96, 17, false},
		{"M", 96, 1, false},
		{"M", 112, 1, false},
	}
	if got := f.callsSince(0); !sameCalls(got, want) {
		t.Errorf("read calls = %+v, want %+v（位设备字访问的 head 须为 16 对齐后的位地址）", got, want)
	}
}

// TestDriverReadDegradedPlanCached 降级结果进区间缓存：下一轮直接读可读子区间，
// 不再重发那个必然失败的合并区间。
//
// 不回写的代价不是"多几个请求"，而是**每轮**都为同一个坏地址重跑一遍降级，
// 采集周期被拖垮（1s 周期下几十个坏地址就顶不住）。
func TestDriverReadDegradedPlanCached(t *testing.T) {
	cfg := DefaultMCConfig()
	f := newFakeMC().miss("D", 102)
	d := &mcDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read = %v", err)
	}
	afterFirst := len(f.callsSince(0))

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("second Read = %v", err)
	}
	// 第二轮只发那一个读得通的子区间：坏地址已被摘出计划
	want := []mcCall{{"D", 100, 1, false}}
	if got := f.callsSince(afterFirst); !sameCalls(got, want) {
		t.Errorf("second-round calls = %+v, want %+v (degraded plan not cached?)", got, want)
	}
	if results[0].Value != "100" || results[0].Quality != driver.QualityGood {
		t.Errorf("D100 on second round = %+v, want val=100 quality=192", results[0])
	}
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("D102 on second round = %+v, want quality=%d", results[1], driver.QualityBadAddress)
	}
	// **跨轮保持**：D102 第二轮已不在计划里、不会再被解析，成因只能靠缓存带过来。
	// 不缓存的话这里就是空串 —— "设备上根本没有这个地址"只在确认那一轮可见，
	// 而点位级报警按连续 N 轮判定，那 1/N 的可见度等于永远触发不了。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("第二轮 D102 Quality = %d, want %d（成因必须随降级计划一起缓存）",
			results[1].Quality, driver.QualityBadAddress)
	}
}

// TestDriverReadDegradedPlanExpires 降级计划到期后重新按合并区间试一次：
// 设备侧把软元件补上（或启用文件寄存器区）之后，采集能自己恢复，不必人工重启
// 网关或改配置。不设 TTL 的话，被摘出去的地址永远回不到计划里。
//
// 这条对 MC 尤其要紧：文件寄存器区（ZR/R）出厂在设备侧是"不使用"，
// 现场在 GX Works 里启用之后，采集应该自己开始出数。
func TestDriverReadDegradedPlanExpires(t *testing.T) {
	cfg := DefaultMCConfig()
	f := newFakeMC().miss("D", 102)
	d := &mcDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read = %v", err)
	}

	// 现场把 D102 补上了，并让降级计划过期
	f.drop("D", 102)
	d.rangeCacheMu.Lock()
	for sig, p := range d.rangeCache {
		p.at = time.Now().Add(-degradedPlanTTL - time.Second)
		d.rangeCache[sig] = p
	}
	d.rangeCacheMu.Unlock()

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read after TTL = %v", err)
	}
	for i, want := range []string{"100", "102"} {
		if results[i].Value != want || results[i].Quality != 192 {
			t.Errorf("result[%d] = %+v, want val=%s quality=192（计划过期后应重新合并读取）",
				i, results[i], want)
		}
	}
}

// TestDriverReadDegradeNetworkErrorPropagates 降级过程中遇到网络错误必须上抛。
//
// 把网络错误也当成"设备拒绝"就地降级，会把一次断连静默成"这些点位质量 0"：
// 数据沉进本轮记录里看着正常，重连却永远不触发。
func TestDriverReadDegradeNetworkErrorPropagates(t *testing.T) {
	cfg := DefaultMCConfig()
	// 第一次读（合并区间）回结束码错误，逼进降级；降级里的读取回网络错误
	mt := &countingTransport{failAfter: 1}
	d := &mcDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("expected network error to propagate, got nil")
	}
}

// countingTransport 第 1 次读取回结束码错误、之后回网络错误。
type countingTransport struct {
	calls int
	// failAfter：前几次调用回结束码错误（逼进降级路径）
	failAfter int
}

func (c *countingTransport) Read(mcDevice, uint32, uint16, bool) ([]byte, error) {
	c.calls++
	if c.calls <= c.failAfter {
		return nil, newEndCodeError(endCodeAddrRange)
	}
	return nil, fmt.Errorf("boom")
}
func (c *countingTransport) IsConnected() bool { return true }
func (c *countingTransport) Close() error      { return nil }

// TestDriverReadDegradeBudgetBounds 一整段地址在设备上都不存在时，对半降级会退化
// 成一棵满二叉树。请求数上限必须把它兜住：Read 要返回、**每一轮**的请求数都有界、
// 点位如实保持 Quality=0（不静默截断，也不无限对半下去）。
func TestDriverReadDegradeBudgetBounds(t *testing.T) {
	cfg := DefaultMCConfig() // MaxGap=8、MaxReadWords=100 → 40 个点合并成一个区间
	f := newFakeMC()
	addrs := make([]po.DeviceAddress, 0, 40)
	for i := uint32(0); i < 40; i++ {
		f.miss("D", 100+i)
		addrs = append(addrs, mkAddr(fmt.Sprintf("id%d", i), fmt.Sprintf("D%d", 100+i), "int16"))
	}
	d := &mcDriver{config: cfg, client: f}

	for round := 0; round < 5; round++ {
		before := len(f.callsSince(0))
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatalf("round %d: Read = %v", round, err)
		}
		if len(results) != len(addrs) {
			t.Fatalf("round %d: want %d results, got %d", round, len(addrs), len(results))
		}
		deferred := 0
		for i, r := range results {
			if (r.Quality != driver.QualityBadAddress && r.Quality != driver.QualityBad) || r.Value != "" {
				t.Fatalf("round %d: result[%d] = %+v, want 坏码（已确认 address 或未归类）/empty（整段都读不到）", round, i, r)
			}
			// 这一批全是"设备上不存在"，故成因只能是 address（已确认）或 ""（预算耗尽、
			// 本轮暂缓 —— 那是延后，不是设备判定）。**绝不能是 comms/parse**：
			// 把暂缓当成通信级，正是"无脑给预填骨架全标 comms"那种实现的症状。
			switch r.Quality {
			case driver.QualityBadAddress:
			case driver.QualityBad:
				deferred++
			default:
				t.Fatalf("round %d: result[%d] Quality = %d, 只允许 %d 或 %d（未归类）",
					round, i, r.Quality, driver.QualityBadAddress, driver.QualityBad)
			}
		}
		// 首轮必然摘不完（摘完 40 个要 79 次请求 > 64 的预算），必须留下暂缓的点位。
		if round == 0 && deferred == 0 {
			t.Error("首轮没有任何暂缓点位：预算没有生效，或暂缓点位被误标了成因")
		}
		// 首轮整段 1 次 + 降级最多 degradeMaxRequests 次。后续轮次同样有界 ——
		// 预算是每轮新发的，不能靠"上轮已经花掉了"来兜底。
		if n := len(f.callsSince(before)); n > 1+degradeMaxRequests {
			t.Errorf("round %d: read calls = %d, want <= %d（预算没有兜住对半降级）", round, n, 1+degradeMaxRequests)
		}
	}

	// 全部地址都确认读不到之后，计划里不该再留着它们（留着就是每轮重付一次
	// 必然失败的往返）。
	d.rangeCacheMu.Lock()
	plan := d.rangeCache[driver.RangeSig(addrs)]
	d.rangeCacheMu.Unlock()
	if len(plan.ranges) != 0 {
		t.Errorf("全部确认读不到之后计划里仍有 %d 个区间，want 0", len(plan.ranges))
	}
}

// TestDriverReadDegradeConvergesAcrossRounds 预算不够一轮摘完时，"下轮接着摘"
// 必须真的发生：**好点位不能因为落在未访问的子树里而永远停在 Quality=0**。
//
// 场景刻意做成一轮摘不完：80 个点合并成一个区间，前 40 个（D100..D139）在设备上
// 都不存在、后 40 个（D140..D179）是好的。摘完前 40 个要 2*40-1 = 79 次请求，
// 超过 64 的预算，所以第一轮必然在中途停下 —— 此时后 40 个点位一次都没被读过。
//
// 修复前的形态是"把父区间原样放回计划"：下一轮的排序、分点、预算都与上一轮完全
// 相同，于是每次都在同一个节点停下，D140..D179 永远是 Quality=0（设备答得好好的）。
func TestDriverReadDegradeConvergesAcrossRounds(t *testing.T) {
	cfg := DefaultMCConfig()
	cfg.MaxGap = 100 // 80 个连续点合并成一个区间（默认 8 只吃相邻的）
	f := newFakeMC()
	for w := uint32(100); w < 140; w++ {
		f.miss("D", w)
	}
	d := &mcDriver{config: cfg, client: f}

	addrs := make([]po.DeviceAddress, 0, 80)
	for i := uint32(0); i < 80; i++ {
		addrs = append(addrs, mkAddr(fmt.Sprintf("id%d", i), fmt.Sprintf("D%d", 100+i), "int16"))
	}

	const healthyFrom = 140
	for round := 0; round < 6; round++ {
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatalf("round %d: Read = %v", round, err)
		}
		ok := true
		for i, r := range results {
			w := 100 + uint32(i)
			if w >= healthyFrom {
				// 设备答得出来的点位：任何一轮都不该被丢掉或标成异常
				if r.Quality != driver.QualityGood || r.Value != fmt.Sprintf("%d", w) {
					ok = false
				}
			} else if r.Quality != driver.QualityBadAddress && r.Quality != driver.QualityBad {
				// 未收敛前允许仍是「未归类」（本轮还没轮到它），但绝不能是 comms/parse/Good
				t.Fatalf("round %d: D%d 设备上不存在，却报了 quality=%d", round, w, r.Quality)
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
				if w := 100 + uint32(i); w < healthyFrom && r.Quality != driver.QualityBadAddress {
					t.Errorf("round %d: D%d Quality = %d, want %d（成因未跨轮保持）",
						round, w, r.Quality, driver.QualityBadAddress)
				}
			}
			return
		}
	}
	t.Fatal("6 轮之后 D140..D179 仍取不到值：未摘完的子区间没有留在计划里，下轮没有接着摘")
}

// TestDriverReadNonAddressEndCodeKeepsWholeRange 非地址类结束码（0xC0EF 通信错误 /
// PLC 忙 / 程序运行中）说明不了任何一个地址是否存在，因此**不得**触发降级、
// 也不得把区间缓存成降级计划：
//
//	① 不发对半降级的请求（一次请求就够 —— 逐个去问只会白挨一遍）；
//	② 整段原样留在计划里（下轮照旧整段重试，而不是被拆成单点、更不是被摘掉）；
//	③ 设备恢复正常之后一轮就全部取到值。
//
// 把它误当成地址问题时，一次"PLC 忙"会把整段点位逐个"确认"成地址不存在并缓存
// 5 分钟 —— 比修复前（一轮 Quality=0、下轮重试）还糟。
func TestDriverReadNonAddressEndCodeKeepsWholeRange(t *testing.T) {
	cfg := DefaultMCConfig()
	f := newFakeMC().miss("D", 102) // D102 确实不存在，但设备回的是"忙"
	busy := true
	calls := 0
	d := &mcDriver{config: cfg, client: &funcTransport{fn: func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
		calls++
		if busy {
			return nil, newEndCodeError(endCodeCommError)
		}
		return f.Read(device, head, points, bitMode)
	}}}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("transient end code should not fail the read, got %v", err)
	}
	for i, r := range results {
		if r.Quality != driver.QualityBadComms {
			t.Errorf("result[%d] = %+v, want quality=%d（本轮取不到值，通信级）", i, r, driver.QualityBadComms)
		}
		// "PLC 忙"说明不了任何地址是否存在 → 成因只能到通信级。
		// 标成 address 会让下游报警把一次"PLC 忙"报成"测点永久坏了"。
		if r.Quality != driver.QualityBadComms {
			t.Errorf("result[%d] Quality = %d, want %d", i, r.Quality, driver.QualityBadComms)
		}
	}

	// ① 只发了那一次整段请求：没有降级出去逐个问
	if calls != 1 {
		t.Errorf("非地址类结束码不该触发降级，发了 %d 次请求，want 1", calls)
	}

	// ② 没有留下降级计划：下轮仍按合并区间整段重试
	d.rangeCacheMu.Lock()
	plan, ok := d.rangeCache[driver.RangeSig(addrs)]
	d.rangeCacheMu.Unlock()
	if ok && plan.degraded {
		t.Errorf("非地址类结束码被缓存成了降级计划：%+v", plan)
	}

	// ③ 设备恢复正常：好点位一轮内取到值
	busy = false
	results, err = d.Read(addrs)
	if err != nil {
		t.Fatalf("Read after recovery = %v", err)
	}
	if results[0].Value != "100" || results[0].Quality != driver.QualityGood {
		t.Errorf("D100 恢复后 = %+v, want val=100 quality=192", results[0])
	}
	if results[0].Quality != driver.QualityGood {
		t.Errorf("D100 恢复后质量码不是 QualityGood，got %d", results[0].Quality)
	}
	// 本轮 D102 是被设备**明确确认**不存在的（endCodeAddrRange 不再是"忙"）→ 地址级。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("D102 Quality = %d, want %d（本轮是明确的地址不存在）",
			results[1].Quality, driver.QualityBadAddress)
	}
}

// funcTransport 用函数当传输层，便于在用例中途改变设备行为（如"先忙后好"）。
type funcTransport struct {
	fn func(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error)
}

func (t *funcTransport) Read(device mcDevice, head uint32, points uint16, bitMode bool) ([]byte, error) {
	return t.fn(device, head, points, bitMode)
}
func (t *funcTransport) IsConnected() bool { return true }
func (t *funcTransport) Close() error      { return nil }

// sameCalls 比较两次读取序列。
func sameCalls(got, want []mcCall) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
