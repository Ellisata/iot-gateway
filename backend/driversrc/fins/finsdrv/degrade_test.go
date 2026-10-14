// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeFINS 假 FINS 设备：missing 里的字地址在设备上不存在。
//
// 任何**覆盖**了不存在字地址的读取都被整段拒绝（结束码错误）——这正是现场形态，
// 也是本组用例针对的那一件事：设备拒绝的是整个请求，不会逐项回一个"这几项失败"。
// 所以一个地址写错，整段点位一起没有值。
//
// 读得通时返回的字里写着**它自己的地址**（int16、大端，与 DefaultFINSConfig 的
// 字节序一致）：于是"降级后按新起点算偏移"一旦算错，断言里的值立刻对不上，
// 而不是表现成一条看着完全合理的曲线。
type fakeFINS struct {
	mu      sync.Mutex
	missing map[uint16]bool
	calls   []readCall
}

type readCall struct {
	area        finsArea
	word, count uint16
}

func newFakeFINS(missing ...uint16) *fakeFINS {
	f := &fakeFINS{missing: make(map[uint16]bool, len(missing))}
	for _, w := range missing {
		f.missing[w] = true
	}
	return f
}

// drop 让某个地址变得可读（模拟现场把地址补上 / 把内存区扩到那儿）。
func (f *fakeFINS) drop(word uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.missing, word)
}

func (f *fakeFINS) Read(area finsArea, word, count uint16) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, readCall{area: area, word: word, count: count})
	f.mu.Unlock()

	data := make([]byte, int(count)*2)
	for i := 0; i < int(count); i++ {
		w := word + uint16(i)
		if f.missing[w] {
			// 结束码错误：设备响应了，但拒绝这个请求（字地址不存在）
			return nil, newEndCodeError(0x1101)
		}
		binary.BigEndian.PutUint16(data[i*2:], w)
	}
	return data, nil
}

func (f *fakeFINS) IsConnected() bool { return true }
func (f *fakeFINS) Close() error      { return nil }

// funcTransport 用函数当传输层，便于在用例中途改变设备行为（如"先忙后好"）。
type funcTransport struct {
	fn func(area finsArea, word, count uint16) ([]byte, error)
}

func (t *funcTransport) Read(area finsArea, word, count uint16) ([]byte, error) {
	return t.fn(area, word, count)
}
func (t *funcTransport) IsConnected() bool { return true }
func (t *funcTransport) Close() error      { return nil }

// callsSince 返回第 n 次调用之后的读取序列（用于断言"这一轮发了哪些请求"）。
func (f *fakeFINS) callsSince(n int) []readCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]readCall(nil), f.calls[n:]...)
}

// TestDriverReadDegradesOnlyBadPoint 合并区间里混进一个设备上不存在的地址时，
// 只有那一个点位 Quality=0，同区间其余点位照常取值。
//
// 这正是本次修复前的形态：整段被拒 → 区间里**所有**点位一起 Quality=0，
// 而设备其实只是不认识 D102 这一个字（D100 完全是好的）。
func TestDriverReadDegradesOnlyBadPoint(t *testing.T) {
	cfg := cfgWith(10, 100) // D100 与 D102 合并成一个区间 [100, 3)
	f := newFakeFINS(102)
	d := &finsDriver{config: cfg, client: f}

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

	// D100 正常取到值（假设备把地址写进值里，偏移算错会立刻对不上）
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
	want := []readCall{
		{AreaDM, 100, 3},
		{AreaDM, 100, 1},
		{AreaDM, 102, 1},
	}
	if got := f.callsSince(0); !sameCalls(got, want) {
		t.Errorf("read calls = %+v, want %+v", got, want)
	}
}

// TestDriverReadDegradesMiddlePoint 坏地址夹在中间：前后两个点位都要取到值，
// 只有中间那个为 0（对半降级必须递归到把中间的坏地址单独摘出来）。
func TestDriverReadDegradesMiddlePoint(t *testing.T) {
	cfg := cfgWith(10, 100) // 三点合并成一个区间 [100, 11)
	f := newFakeFINS(105)
	d := &finsDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D105", "int16"),
		mkAddr("3", "D110", "int16"),
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}

	// 两头正常点位是 QualityGood，中间的坏地址是 QualityBadAddress。
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

// TestDriverReadDegradedPlanCached 降级结果进区间缓存：下一轮直接读可读子区间，
// 不再重发那个必然失败的合并区间。
//
// 不回写的代价不是"多几个请求"，而是**每轮**都为同一个坏地址重跑一遍降级，
// 采集周期被拖垮（1s 周期下几十个坏地址就顶不住）。
func TestDriverReadDegradedPlanCached(t *testing.T) {
	cfg := cfgWith(10, 100)
	f := newFakeFINS(102)
	d := &finsDriver{config: cfg, client: f}

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
	want := []readCall{{AreaDM, 100, 1}}
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
// 设备侧把地址补上（或把内存区扩到那儿）之后，采集能自己恢复，不必人工重启网关
// 或改配置。不设 TTL 的话，被摘出去的地址永远回不到计划里。
func TestDriverReadDegradedPlanExpires(t *testing.T) {
	cfg := cfgWith(10, 100)
	f := newFakeFINS(102)
	d := &finsDriver{config: cfg, client: f}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read = %v", err)
	}

	// 现场把 D102 补上了，并让降级计划过期
	f.drop(102)
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
	cfg := cfgWith(10, 100)
	calls := 0
	mt := &mockTransport{fn: func(area finsArea, word, count uint16) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, newEndCodeError(0x1101) // 逼进降级分支
		}
		return nil, fmt.Errorf("boom") // 降级里的读取：网络错误
	}}
	d := &finsDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("expected network error to propagate, got nil")
	}
}

// TestDriverReadDegradeBudgetBounds 一整段地址在设备上都不存在时，对半降级会退化
// 成一棵满二叉树。请求数上限必须把它兜住：Read 要返回、**每一轮**的请求数都有界、
// 点位如实保持 Quality=0（不静默截断，也不无限对半下去）。
func TestDriverReadDegradeBudgetBounds(t *testing.T) {
	cfg := DefaultFINSConfig() // MergeWindow=100，40 个点合并成一个区间
	f := newFakeFINS()
	for w := uint16(100); w < 140; w++ {
		f.missing[w] = true
	}
	d := &finsDriver{config: cfg, client: f}

	addrs := make([]po.DeviceAddress, 0, 40)
	for i := uint16(0); i < 40; i++ {
		addrs = append(addrs, mkAddr(fmt.Sprintf("id%d", i), fmt.Sprintf("D%d", 100+i), "int16"))
	}
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
	// 必然失败的往返），且不该把已确认的点位又翻出来重试。
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
	cfg := DefaultFINSConfig() // MergeWindow=100、MaxReadWords=100 → 80 个点一个区间
	f := newFakeFINS()
	for w := uint16(100); w < 140; w++ {
		f.missing[w] = true
	}
	d := &finsDriver{config: cfg, client: f}

	addrs := make([]po.DeviceAddress, 0, 80)
	for i := uint16(0); i < 80; i++ {
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
			w := 100 + uint16(i)
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
				if w := 100 + uint16(i); w < healthyFrom && r.Quality != driver.QualityBadAddress {
					t.Errorf("round %d: D%d Quality = %d, want %d（成因未跨轮保持）",
						round, w, r.Quality, driver.QualityBadAddress)
				}
			}
			return
		}
	}
	t.Fatal("6 轮之后 D140..D179 仍取不到值：未摘完的子区间没有留在计划里，下轮没有接着摘")
}

// TestDriverReadNonAddressEndCodeKeepsWholeRange 非地址类结束码（0x0205 目标节点忙）
// 说明不了任何一个地址是否存在，因此**不得**触发降级、也不得把区间缓存成降级计划：
//
//	① 不发对半降级的请求（一次请求就够 —— 逐个去问只会白挨一遍）；
//	② 整段原样留在计划里（下轮照旧整段重试，而不是被拆成单点、更不是被摘掉）；
//	③ 设备恢复正常之后一轮就全部取到值。
//
// 把它误当成地址问题时，一次"节点忙"会把整段点位逐个"确认"成地址不存在并缓存
// 5 分钟 —— 比修复前（一轮 Quality=0、下轮重试）还糟。
func TestDriverReadNonAddressEndCodeKeepsWholeRange(t *testing.T) {
	cfg := cfgWith(10, 100)
	f := newFakeFINS(102) // D102 确实不存在，但设备回的是"忙"
	busy := true
	d := &finsDriver{config: cfg, client: &funcTransport{fn: func(area finsArea, word, count uint16) ([]byte, error) {
		if busy {
			// 0x02 主码 = 目标节点错误（忙 / 响应超时），与地址无关
			return nil, newEndCodeError(0x0205)
		}
		return f.Read(area, word, count)
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
		// "节点忙"说明不了任何地址是否存在 → 成因只能到通信级。
		// 标成 address 会让下游报警把一次"节点忙"报成"测点永久坏了"。
		if r.Quality != driver.QualityBadComms {
			t.Errorf("result[%d] Quality = %d, want %d", i, r.Quality, driver.QualityBadComms)
		}
	}

	// ① 只发了那一次整段请求：没有降级出去逐个问
	if n := len(f.callsSince(0)); n != 0 {
		t.Errorf("非地址类结束码不该触发降级，却发了 %d 次探测请求", n)
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
	// 本轮 D102 是被设备**明确确认**不存在的（0x1101 不再是"忙"）→ 地址级。
	if results[1].Quality != driver.QualityBadAddress {
		t.Errorf("D102 Quality = %d, want %d（本轮是明确的地址不存在）",
			results[1].Quality, driver.QualityBadAddress)
	}
}

// sameCalls 比较两次读取序列。
func sameCalls(got, want []readCall) bool {
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
