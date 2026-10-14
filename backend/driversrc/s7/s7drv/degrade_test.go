// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeS7 假 S7 PLC：missing 里的字节偏移属于不存在的地址。
//
// 任何**覆盖**了它的读取都被整段拒绝为 "CPU : Address out of range"（gos7 把项级
// 错误码 0x05 翻译成的文案，见 pointscan.go 的 unreadableMarkers）—— 这正是现场
// 形态：设备拒绝的是整个请求，不会逐项回一个"这一个失败"。
//
// 读得通时返回的字节里写着**它自己的偏移**（首字节），于是"降级后按新起点算偏移"
// 一旦算错，断言里的值立刻对不上，而不是表现成一条看着完全合理的曲线。
type fakeS7 struct {
	mu           sync.Mutex
	missing      map[int]bool
	refuse       string // 非空：所有读取都回这条错误（模拟瞬时状态）
	disconnected bool
	calls        []s7Call
}

type s7Call struct {
	start, end int
}

func newFakeS7(missing ...int) *fakeS7 {
	f := &fakeS7{missing: make(map[int]bool, len(missing))}
	for _, off := range missing {
		f.missing[off] = true
	}
	return f
}

// drop 让某个偏移变得可读（模拟现场把 DB 建出来 / 把地址改对）。
func (f *fakeS7) drop(off int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.missing, off)
}

// refuseWith 让设备回一条与地址无关的错误（瞬时状态）。
func (f *fakeS7) refuseWith(msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refuse = msg
}

func (f *fakeS7) readRange(r S7Range, buf []byte) error {
	f.mu.Lock()
	f.calls = append(f.calls, s7Call{start: r.StartOffset, end: r.EndOffset})
	refuse := f.refuse
	f.mu.Unlock()

	if refuse != "" {
		return errors.New(refuse)
	}
	for i := range buf {
		off := r.StartOffset + i
		f.mu.Lock()
		miss := f.missing[off]
		f.mu.Unlock()
		if miss {
			return errors.New("CPU : Address out of range")
		}
		buf[i] = byte(off) // 自描述：值就是它的偏移
	}
	return nil
}

func (f *fakeS7) MarkDisconnected() {
	f.mu.Lock()
	f.disconnected = true
	f.mu.Unlock()
}

func (f *fakeS7) callsSince(n int) []s7Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]s7Call(nil), f.calls[n:]...)
}

// runRound 跑**驱动真实的一轮采集**（s7Driver.readOnce），只把 client 换成假 PLC。
//
// 刻意不在测试里重写那个循环：重写的版本与生产代码是两份实现，用例量到的是
// 测试自己那版，"回写降级计划""下轮接着摘"这些行为一旦和驱动里写的不同，
// 测试反而会替错的实现背书。
func runRound(t *testing.T, d *s7Driver, reader s7RangeReader, sig uint64,
	addrs []po.DeviceAddress, cfg *S7Config) []driver.ReadResult {

	t.Helper()
	results, err := d.readOnce(reader, addrs, cfg, sig)
	if err != nil {
		t.Fatalf("readOnce = %v", err)
	}
	return results
}

// mkDBPoints 造 n 个 DB1.DBB 起的字节点位（每个 1 字节，便于自描述断言）。
//
// 类型用 S7 自己的 "byte"（KindUInt，Size 1）—— 地址名里的 B 也是 1 字节宽度，
// 于是跨度恰好 1 字节，假 PLC 的"值=偏移"可以直接比对。
func mkDBPoints(base, n int) []po.DeviceAddress {
	addrs := make([]po.DeviceAddress, 0, n)
	for i := 0; i < n; i++ {
		addrs = append(addrs, po.DeviceAddress{
			ID:             fmt.Sprintf("id%d", i),
			Name:           fmt.Sprintf("DB1.DBB%d", base+i),
			DataType:       "byte",
			CommonDataType: "byte",
		})
	}
	return addrs
}

// mergedOnce 计算合并计划并断言它确实是一个区间（场景的前提）。
func mergedOnce(t *testing.T, addrs []po.DeviceAddress, cfg *S7Config) []S7Range {
	t.Helper()
	ranges, err := CalcS7Ranges(addrs, cfg.StringLen, cfg.MaxGap)
	if err != nil {
		t.Fatalf("CalcS7Ranges = %v", err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 merged range, got %d: %+v", len(ranges), ranges)
	}
	return ranges
}

// TestReadDegradesOnlyBadPoint 合并区间里混进一个设备上不存在的地址时，
// 只有那一个点位 Quality=0，同区间其余点位照常取值。
//
// 这正是本次修复前的形态：整段被拒 → 区间里**所有**点位一起 Quality=0，
// 而设备其实只是不认识 DB1.DBB102 这一个字节。
func TestReadDegradesOnlyBadPoint(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16 // 让 100..103 合并成一个区间
	addrs := mkDBPoints(100, 4)
	mergedOnce(t, addrs, cfg) // 场景前提：四点合并成一个区间

	f := newFakeS7(102)
	d := &s7Driver{config: cfg}
	results := runRound(t, d, f, driver.RangeSig(addrs), addrs, cfg)

	if len(results) != 4 {
		t.Fatalf("want 4 results, got %d", len(results))
	}
	// 100/101/103 正常取到值（值=偏移，降级后算错偏移会立刻对不上）
	for _, i := range []int{0, 1, 3} {
		want := fmt.Sprintf("%d", 100+i)
		if results[i].Value != want || results[i].Quality != 192 {
			t.Errorf("result[%d] = %+v, want val=%s quality=192", i, results[i], want)
		}
		if results[i].Quality != driver.QualityGood {
			t.Errorf("result[%d] 质量码不是 QualityGood，got %d", i, results[i].Quality)
		}
	}
	// 102 确实不存在：只有它是坏码，且 DataType/Kind 必须原样回填
	if results[2].Value != "" || results[2].Quality != driver.QualityBadAddress {
		t.Errorf("102 = %+v, want val=\"\" quality=%d", results[2], driver.QualityBadAddress)
	}
	// 成因必须是 address：拆到单点仍被拒 = 这个 DB 偏移在设备上不存在，
	// 与"CPU 拒绝但说不清是哪个地址"那种下轮会恢复的通信级失败完全不同。
	if results[2].Quality != driver.QualityBadAddress {
		t.Errorf("102 Quality = %d, want %d", results[2].Quality, driver.QualityBadAddress)
	}
	if results[2].DataType != "byte" || results[2].Kind == "" {
		t.Errorf("102 读不到时 DataType/Kind 仍须回填，got %+v", results[2])
	}

	// 整段先被拒一次，随后对半拆：左半 [100,102) 读通，右半 [102,104) 再拆成两个单字节
	wantCalls := []s7Call{{100, 104}, {100, 102}, {102, 104}, {102, 103}, {103, 104}}
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

// TestReadNonAddressErrorKeepsWholeRange 无法**确认**地址不存在的错误（如
// "CLI : function refused by CPU" —— CPUError 的兜底桶，S7-1500 的项级错误码都
// 落到这里）说明不了哪个地址有问题，因此**不得**触发降级、也不得缓存降级计划：
//
//	① 不发对半降级的请求（一次请求就够 —— 逐个去问只会白挨一遍）；
//	② 整段原样留在计划里（下轮照旧整段重试，而不是被拆成单点、更不是被摘掉）；
//	③ 设备恢复正常之后一轮就全部取到值。
//
// 兜底桶在点扫那边是当作"读不到"收下的（那里判错的代价只是清单多一条），
// 但在采集侧它意味着"摘掉这个点位并缓存 5 分钟"—— 判错的代价是把好点位丢掉。
// 两条路的判据刻意不同，见 confirmMarkers 的说明。
func TestReadNonAddressErrorKeepsWholeRange(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16
	addrs := mkDBPoints(100, 3)
	mergedOnce(t, addrs, cfg) // 场景前提：三点合并成一个区间

	f := newFakeS7(102)
	f.refuseWith("CLI : function refused by CPU")
	d := &s7Driver{config: cfg}
	sig := driver.RangeSig(addrs)

	results := runRound(t, d, f, sig, addrs, cfg)
	for i, r := range results {
		if r.Value != "" {
			t.Errorf("result[%d] = %+v, want 空值（本轮取不到值）", i, r)
		}
		// 兜底桶说明不了任何地址是否存在 → 成因只能到通信级。
		// 标成 address 会让下游报警把一次"CPU 拒绝"报成"测点永久坏了"。
		if r.Quality != driver.QualityBadComms {
			t.Errorf("result[%d] Quality = %d, want %d", i, r.Quality, driver.QualityBadComms)
		}
	}

	// ① 只发了那一次整段请求：没有降级出去逐个问
	if n := len(f.callsSince(0)); n != 1 {
		t.Errorf("兜底桶不该触发降级，发了 %d 次请求，want 1", n)
	}

	// ② 没有留下降级计划：下轮仍按合并区间整段重试
	d.rangeCacheMu.Lock()
	plan := d.rangeCache[sig]
	d.rangeCacheMu.Unlock()
	if plan.degraded {
		t.Error("兜底桶被缓存成了降级计划")
	}

	// ③ 设备恢复正常：好点位一轮内取到值
	f.refuseWith("")
	results = runRound(t, d, f, sig, addrs, cfg)
	if results[0].Value != "100" || results[0].Quality != 192 {
		t.Errorf("100 恢复后 = %+v, want val=100 quality=192", results[0])
	}
	if results[0].Quality != driver.QualityGood {
		t.Errorf("100 恢复后质量码不是 QualityGood，got %d", results[0].Quality)
	}
	if results[2].Quality != driver.QualityBadAddress {
		t.Errorf("102 仍不存在，应为 QualityBadAddress，got %+v", results[2])
	}
	// 本轮 102 是被设备**明确确认**不存在的（不再是兜底桶）→ 地址级。
	if results[2].Quality != driver.QualityBadAddress {
		t.Errorf("102 Quality = %d, want %d（本轮是明确的地址不存在）",
			results[2].Quality, driver.QualityBadAddress)
	}
}

// TestReadDegradedPlanCached 降级结果进计划缓存：下一轮直接读可读子区间，
// 不再重发那个必然失败的合并区间。
//
// 不回写的代价不是"多几个请求"，而是**每轮**都为同一个坏地址重跑一遍降级，
// 采集周期被拖垮（1s 周期下几十个坏地址就顶不住）。
func TestReadDegradedPlanCached(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16
	addrs := mkDBPoints(100, 3)
	mergedOnce(t, addrs, cfg) // 场景前提：三点合并成一个区间

	f := newFakeS7(102)
	d := &s7Driver{config: cfg}
	sig := driver.RangeSig(addrs)

	runRound(t, d, f, sig, addrs, cfg)
	afterFirst := len(f.callsSince(0))

	results := runRound(t, d, f, sig, addrs, cfg)

	// 第二轮只发读得通的那些子区间：坏地址已被摘出计划。
	// 对半降级产出的是**没被重新合并过**的子区间（100 与 101 各一条），
	// 不再合并是刻意的：带缺口重新合并会把刚摘掉的坏地址又并回来。
	wantCalls := []s7Call{{100, 101}, {101, 102}}
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
	// **跨轮保持**：102 第二轮已不在计划里、不会再被解析，成因只能靠缓存带过来。
	// 不缓存的话这里就是空串 —— "设备上根本没有这个地址"只在确认那一轮可见，
	// 而点位级报警按连续 N 轮判定，那 1/N 的可见度等于永远触发不了。
	if results[2].Quality != driver.QualityBadAddress {
		t.Errorf("第二轮 102 Quality = %d, want %d（成因必须随降级计划一起缓存）",
			results[2].Quality, driver.QualityBadAddress)
	}
}

// TestReadDegradedPlanExpires 降级计划到期后重新按合并区间试一次：
// 设备侧把 DB 建出来 / 把地址改对之后，采集能自己恢复。
//
// 两条一起钉：① 续写降级计划**不刷新时间戳**（每轮刷新会让 TTL 变成"永不过期"，
// 批次里只要还有另一个区间在降级，被摘出的地址就永远回不来）；② 到期后确实回退到
// 合并计划并取到值。
func TestReadDegradedPlanExpires(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16
	addrs := mkDBPoints(100, 3)
	merged := mergedOnce(t, addrs, cfg)

	f := newFakeS7(102)
	d := &s7Driver{config: cfg}
	sig := driver.RangeSig(addrs)
	runRound(t, d, f, sig, addrs, cfg)

	// ① 续写不刷新时间戳
	d.rangeCacheMu.Lock()
	first := d.rangeCache[sig].at
	d.rangeCacheMu.Unlock()
	d.storeDegradedPlan(sig, merged, map[string]int{"102": driver.QualityBadAddress})
	d.rangeCacheMu.Lock()
	again := d.rangeCache[sig].at
	d.rangeCacheMu.Unlock()
	if !again.Equal(first) {
		t.Errorf("续写降级计划刷新了时间戳（%v → %v）：TTL 会变成永不过期", first, again)
	}

	// ② 到期后回退到合并计划；设备侧补上 102 之后一轮就全取到值
	f.drop(102)
	d.rangeCacheMu.Lock()
	plan := d.rangeCache[sig]
	plan.at = time.Now().Add(-degradedPlanTTL - time.Second)
	d.rangeCache[sig] = plan
	d.rangeCacheMu.Unlock()

	results := runRound(t, d, f, sig, addrs, cfg)
	for i, want := range []string{"100", "101", "102"} {
		if results[i].Value != want || results[i].Quality != 192 {
			t.Errorf("result[%d] = %+v, want val=%s quality=192（到期后应重新合并读取）", i, results[i], want)
		}
	}
}

// TestReadConnectionErrorPropagates 链路错误必须上抛并标记断开（触发重连），
// 不能被静默成"点位质量 0"。
func TestReadConnectionErrorPropagates(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 16
	addrs := mkDBPoints(100, 4)
	merged := mergedOnce(t, addrs, cfg)

	f := &errS7Reader{err: io.EOF}
	d := &s7Driver{config: cfg}
	addrByID := map[string]*po.DeviceAddress{addrs[0].ID: &addrs[0]}
	byID := map[string]driver.ReadResult{}
	st := &degradeState{b: newIsolateBudget()}

	if err := d.readRangeDegrading(f, &merged[0], addrByID, byID, st); err == nil {
		t.Fatal("链路错误应上抛，got nil")
	}
	if !f.disconnected {
		t.Error("链路错误必须标记断开（否则永不触发重连）")
	}
}

// errS7Reader 所有读取都返回给定错误。
type errS7Reader struct {
	err          error
	disconnected bool
}

func (e *errS7Reader) readRange(S7Range, []byte) error { return e.err }
func (e *errS7Reader) MarkDisconnected()               { e.disconnected = true }

// TestReadDegradeConvergesAcrossRounds 预算不够一轮摘完时，"下轮接着摘"必须真的
// 发生：**好点位不能因为落在未访问的子树里而永远停在 Quality=0**。
//
// 场景刻意做成一轮摘不完：80 个点合并成一个区间，前 40 个（DBB100..139）在设备上
// 都不存在、后 40 个（DBB140..179）是好的。摘完前 40 个要 2*40-1 = 79 次请求，
// 超过 64 的预算，所以第一轮必然在中途停下 —— 此时后 40 个点位一次都没被读过。
//
// 修复前的形态是"把父区间原样放回计划"：下一轮的排序、分点、预算都与上一轮完全
// 相同，于是每次都在同一个节点停下，DBB140..179 永远是 Quality=0（设备答得好好的）。
func TestReadDegradeConvergesAcrossRounds(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 0 // 相邻字节本来就连续，无需跨缺口合并
	addrs := mkDBPoints(100, 80)
	merged := mergedOnce(t, addrs, cfg)
	if merged[0].StartOffset != 100 || merged[0].EndOffset != 180 {
		t.Fatalf("merged range = [%d, %d), want [100, 180)", merged[0].StartOffset, merged[0].EndOffset)
	}
	_ = merged

	f := newFakeS7()
	for off := 100; off < 140; off++ {
		f.missing[off] = true
	}
	d := &s7Driver{config: cfg}
	sig := driver.RangeSig(addrs)

	const healthyFrom = 140
	for round := 0; round < 6; round++ {
		results := runRound(t, d, f, sig, addrs, cfg)
		if len(results) != len(addrs) {
			t.Fatalf("round %d: want %d results, got %d", round, len(addrs), len(results))
		}
		ok := true
		for i, r := range results {
			off := 100 + i
			if off >= healthyFrom {
				if r.Quality != driver.QualityGood || r.Value != fmt.Sprintf("%d", off) {
					ok = false
				}
			} else if r.Quality != driver.QualityBadAddress && r.Quality != driver.QualityBad {
				// 未收敛前允许仍是「未归类」（本轮还没轮到它），但绝不能是 comms/parse/Good
				t.Fatalf("round %d: DBB%d 设备上不存在，却报了 quality=%d", round, off, r.Quality)
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
				if off := 100 + i; off < healthyFrom && r.Quality != driver.QualityBadAddress {
					t.Errorf("round %d: DBB%d Quality = %d, want %d（成因未跨轮保持）",
						round, off, r.Quality, driver.QualityBadAddress)
				}
			}
			return
		}
	}
	t.Fatal("6 轮之后 DBB140..179 仍取不到值：未摘完的子区间没有留在计划里，下轮没有接着摘")
}

// TestReadDegradeBudgetBounds 一整段地址在设备上都不存在时，对半降级会退化成一棵
// 满二叉树。请求数上限必须把它兜住：每一轮的请求数都有界、点位如实保持 Quality=0。
func TestReadDegradeBudgetBounds(t *testing.T) {
	cfg := DefaultS7Config()
	cfg.MaxGap = 0
	addrs := mkDBPoints(100, 40)
	merged := mergedOnce(t, addrs, cfg)
	if merged[0].StartOffset != 100 || merged[0].EndOffset != 140 {
		t.Fatalf("merged range = [%d, %d), want [100, 140)", merged[0].StartOffset, merged[0].EndOffset)
	}

	f := newFakeS7()
	for off := 100; off < 140; off++ {
		f.missing[off] = true
	}
	d := &s7Driver{config: cfg}
	sig := driver.RangeSig(addrs)

	for round := 0; round < 5; round++ {
		before := len(f.callsSince(0))
		results := runRound(t, d, f, sig, addrs, cfg)
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
			t.Errorf("round %d: calls = %d, want <= %d（预算没有兜住对半降级）", round, n, 1+degradeMaxRequests)
		}
	}

	// 全部确认读不到之后，计划里不该再留着它们（留着就是每轮重付一次必然失败的往返）
	d.rangeCacheMu.Lock()
	left := len(d.rangeCache[sig].ranges)
	d.rangeCacheMu.Unlock()
	if left != 0 {
		t.Errorf("全部确认读不到之后计划里仍有 %d 个区间，want 0", left)
	}
}
