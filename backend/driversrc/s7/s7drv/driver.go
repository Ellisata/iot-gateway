// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// 对半降级的预算上限，**单次 Read 共享**。
//
// 两个上限都要，理由与点位扫描那边同一条：请求数上限兜住"设备应答很快但整段
// 拒绝"（否则一次 Read 会发出几百个必然失败的请求），时间上限兜住"设备应答很慢"
// （每个请求都吃满超时）。
//
// 上限必须远小于网关侧 Read 的 60s RPC 超时：连续 3 次超时会把整个插件进程判死，
// 该插件下所有协议、所有设备一起断。所以余量要留给同批次的其余区间。
const (
	degradeMaxRequests = 64
	degradeMaxDuration = 15 * time.Second
)

// degradedPlanTTL 降级计划的有效期。
//
// 到期后按合并计划重读一次：设备侧把那个 DB 建出来、把地址改对之后，采集能自己
// 恢复，不需要人工重启网关或改配置。取值与 ABB 的 hints 静默期同量级（300s）。
//
// 注意它只在**已有降级计划**上倒计时，且 storeDegradedPlan 不刷新已有时间戳
// （见该函数的说明）—— 否则批次里只要有另一个区间每轮都在降级，这个窗口就永远
// 不会到期，被摘出的地址永远回不来。
const degradedPlanTTL = 5 * time.Minute

// warnTTL 同一条去重日志重新出现的间隔（见 warnOnce）。
const warnTTL = 10 * time.Minute

// confirmMarkers 能用来**确认"这个地址不存在"**的错误文案。
//
// 它是 pointscan.go 的 unreadableMarkers 的**真子集**：去掉了 "CLI : function
// refused by CPU" 那个兜底桶。两条路问的是两个不同的问题，判据**刻意不同**：
//
//   - 点扫（isUnreadableAddrErr）：问"要不要停止扫描 / 记成读不到"。兜底桶判成
//     "读不到"，最多让候选清单里多一条错的条目；判成中止则丢掉整份清单。
//   - 采集（这里）：问"要不要**确认**这个地址永久不可读"—— 确认意味着把它摘出
//     计划并缓存 5 分钟。而兜底桶的含义恰恰是"PLC 应答了、但不知道这个错误码是
//     什么意思"，它**可能是可恢复的设备状态**；拿它确认，一个本来好用的点位会被
//     静默摘掉 5 分钟。所以这里只认明确表示"地址/对象不存在"的那几条，其余一律
//     当瞬时状态、整段原样重试（与修复前的行为一致：这一轮标 0、下轮重试）。
var confirmMarkers = []string{
	"CPU : Address out of range",
	"CPU : Item not available",
	"CPU : Function not available",
	"address invalid",
	"object doesn",
}

// isAddressNotFoundErr 这条错误是否等于设备在说"这个地址/对象我这儿没有"。
func isAddressNotFoundErr(err error) bool {
	if err == nil || isConnErr(err) {
		return false
	}
	msg := err.Error()
	for _, m := range confirmMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

func init() {
	driver.Register("Siemens.S7", func() driver.Driver {
		return &s7Driver{}
	})
}

// s7Driver 西门子 S7 协议驱动，实现 driver.Driver 接口。
//
// 支持 S7-1200/1500/300/400 通过 ISO-on-TCP 读取 DB / M / I / Q 区数据。
// 连接为长连接，读取遇网络错误自动标记断开，由采集引擎在下一轮重连。
type s7Driver struct {
	mu     sync.RWMutex
	config *S7Config
	client *S7Client

	// rangeCache 区间读取计划缓存：规避每轮重复解析地址与计算区间。
	// 区间计算是 (addrs, cfg) 的纯函数，轮询间批次内容不变即可命中；
	// 驱动实例在配置热刷新时由采集引擎重建，Connect 时也显式清空，缓存随之失效。
	//
	// 条目既可能是 CalcS7Ranges 的合并计划，也可能是对半降级后的计划
	// （见 s7Plan.degraded）—— 后者带 TTL，到期删除以让被摘出的地址重新
	// 回到合并区间里试一次。
	rangeCacheMu sync.Mutex
	rangeCache   map[uint64]s7Plan

	// warned 日志去重：要去的重是"每轮都成立的条件"（降级预算耗尽、某个地址
	// 恒不可读），不去重就是每个采集周期把同一行刷一遍。
	warnMu sync.Mutex
	warned map[string]warnEntry
}

// s7Plan 一个批次的区间读取计划（driver.RangeSig 内容指纹 → 计划）。
type s7Plan struct {
	ranges []S7Range
	// degraded 为 true 表示这是对半降级后的计划：读不到的地址已被摘出合并区间。
	// 这类计划按 degradedPlanTTL 过期 —— 不设 TTL 的话，设备侧后来建出来的 DB
	// 会永远拿不回来（它已经被排除在所有区间之外了）。
	degraded bool
	// bad 已知读不到的地址 → 质量码（恒为 QualityBadAddress）。两个来源：
	//   ① **本地解析失败**（partitionBadAddrs 挑出来的，一个字节都没上过线）——
	//      非降级计划里就有它，是纯函数式的集合，没有 TTL；
	//   ② **设备已确认不存在**（对半降级到单点仍被拒）—— 只在降级计划里有。
	// 两者都必须缓存：这些地址已被摘出计划、不再参与任何一次解析，不带进来的话此后
	// 每轮的质量码都会退化成"未归类"（QualityBad），"这个地址坏"在数据上就看不出来
	// （点位级报警按连续 N 轮判定时永远触发不了）。
	// 降级计划里的 bad 生命周期与 ranges 一致（同一条 TTL，地址回到合并区间重试时它
	// 同时消失）；非降级计划的 bad 随 RangeSig 变化自然重算。
	bad map[string]int
	at  time.Time
}

// warnEntry 一条去重记录：内容 + 上次打印的时间。
type warnEntry struct {
	msg string
	at  time.Time
}

func (d *s7Driver) Connect(protocolJSON string) error {
	cfg, err := ParseS7Config(protocolJSON)
	if err != nil {
		return fmt.Errorf("s7: parse config failed: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 任何重连尝试都使区间缓存失效：配置可能已变化（MaxGap/StringLen/…），
	// 新连接必须使用重新计算的区间。降级计划也在同一张表里，一并失效 ——
	// 重连后设备侧可能已经建出了那个 DB。
	d.rangeCacheMu.Lock()
	d.rangeCache = nil
	d.rangeCacheMu.Unlock()

	// 告警去重表同样清空：新连接下的失败是"新情况"，该重新说一次
	d.warnMu.Lock()
	d.warned = nil
	d.warnMu.Unlock()

	client, err := NewS7Client(cfg.Host, strconv.Itoa(cfg.Port), cfg.Rack, cfg.Slot, cfg.ConnectionType, timeout)
	if err != nil {
		return err
	}

	d.mu.Lock()
	if d.client != nil {
		d.client.Close()
	}
	d.config = cfg
	d.client = client
	d.mu.Unlock()

	return nil
}

func (d *s7Driver) Ping(protocolJSON string) error {
	cfg, err := ParseS7Config(protocolJSON)
	if err != nil {
		return fmt.Errorf("s7: ping parse config failed: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 无状态握手：建立临时连接（TCP + ISO + S7 PDU 协商）后立即关闭，
	// 不修改驱动内部状态，供连接测试复用。
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	handler := newTCPHandler(addr, cfg.Rack, cfg.Slot, cfg.ConnectionType, timeout)

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("s7: ping %s failed (rack=%d, slot=%d, connectType=%d): %w",
			addr, cfg.Rack, cfg.Slot, cfg.ConnectionType, err)
	}
	handler.Close()

	logf("s7 ping success: %s (rack=%d, slot=%d, connectType=%d)",
		addr, cfg.Rack, cfg.Slot, cfg.ConnectionType)
	return nil
}

func (d *s7Driver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("s7: not connected")
	}

	return d.readOnce(client, addrs, cfg, driver.RangeSig(addrs))
}

// readOnce 执行一轮采集：按计划逐区间读取、必要时对半降级、并把降级后的计划
// 回写缓存。
//
// 与 Read 分开是为了让**这一整段**可测：S7Client 是具体类型、自带连接与会话状态，
// 造不出来，而"降级有没有回写、下轮有没有接着摘"恰恰只在这段里。测试传入一台假
// PLC（s7RangeReader），量到的就是生产代码本身 —— 在测试里照抄一遍这个循环是
// 另一种写法，迟早与这里漂移。
func (d *s7Driver) readOnce(reader s7RangeReader, addrs []po.DeviceAddress,
	cfg *S7Config, sig uint64) ([]driver.ReadResult, error) {

	ranges, err := d.planFor(sig, addrs, cfg)
	if err != nil {
		return nil, fmt.Errorf("s7: calc address ranges failed: %w", err)
	}

	// 预构建 id → addr 索引（解码时 O(1) 取回 DataType/Name）并预填结果骨架。
	//
	// 骨架必须先填：读不到的点位（拆到单点仍被设备确认不存在、或降级预算耗尽）
	// 保持 Quality=0，但 DataType/Kind 得原样回填 —— 空着就是往下游发一条没有类型的
	// 记录，而下游（MQTT 负载、时序库表结构）正靠它决定怎么写。
	// 上一轮已确认读不到的地址 → 质量码（恒为 QualityBadAddress）。它们已被摘出计划、
	// 本轮不会再被任何解析碰到，只有在这里带进来，其质量码才不会在后续每轮退化成“未归类”。
	bad := d.badFor(sig)
	addrByID := make(map[string]*po.DeviceAddress, len(addrs))
	byID := make(map[string]driver.ReadResult, len(addrs))
	for i := range addrs {
		a := &addrs[i]
		addrByID[a.ID] = a
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         bad[a.ID],
		}
	}

	// 逐区间读取。整段读通 → 正常解析；设备确认某个地址不存在 → 对半降级，只把
	// 真正读不到的点位标 Quality=0，同区间的其余点位照常取值；
	// 链路错误 → 标记断开并向上返回，由采集引擎判定断连并重连。
	//
	// 降级只在失败的区间上发生，但一个批次可能有多个失败区间，所以预算在整次
	// Read 内共享 —— 否则每个区间各花满一份，总耗时随区间数线性放大。
	budget := newIsolateBudget()
	next := make([]S7Range, 0, len(ranges))
	degraded := false
	for i := range ranges {
		r := &ranges[i]
		st := &degradeState{b: budget}
		if err := d.readRangeDegrading(reader, r, addrByID, byID, st); err != nil {
			return nil, err
		}
		if !st.degraded {
			// 整段读通，或整段被以非地址类错误拒绝（瞬时状态）：原样留着下轮再来
			next = append(next, *r)
			continue
		}
		// 降级过的区间：计划换成"读得通的子区间 + 本轮没摘完的子区间"。
		//
		// **不能退回原区间**：本轮没摘完的那部分若以原区间（父区间）的形态留在
		// 计划里，下轮的遍历会从头重来一遍 —— `rangePoints` 的排序、对半切的分点、
		// 每轮全新的预算都是确定的，于是每次都停在同一个节点，"下轮接着摘"
		// 一次也不会真的发生，落在未访问子树里的**好点位**会永远停在 quality=0。
		// 留下 pending（停在哪一段）才让下一轮从那里继续。
		next = append(next, st.subs...)
		next = append(next, st.pending...)
		degraded = true
	}
	if degraded {
		d.storeDegradedPlan(sig, next, badQualities(byID))
	}
	if budget.skipped > 0 {
		d.warnOnce("budget", "degrade budget exhausted, %d points left unread this round (continues next round)", budget.skipped)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// markQuality 给一个点位标上质量码。
//
// 两条守卫，都是为了让坏子码只在"确实异常"时出现：
//   - 记录不在 byID 里（不属于本批点位）时跳过；
//   - **Quality==QualityGood 的一律不动** —— 正常点位不该被标坏，也保证某轮侥幸读通的点位
//     不会带着上一轮的坏码（解析函数是整条记录覆写，本就不会残留，这里再加一道保险）。
func markQuality(byID map[string]driver.ReadResult, id string, quality int) {
	r, ok := byID[id]
	if !ok || r.Quality == driver.QualityGood {
		return
	}
	r.Quality = quality
	byID[id] = r
}

// badQualities 取出本轮结果里**已确认在设备上不存在**的点位及其质量码，供回写计划缓存。
//
// 只取 address：comms 是瞬时的、点位仍留在计划里下轮重试，parse 是本地解码问题、
// 下一轮还会被重读 —— 只有 address 的点位被摘出了计划，不缓存的话此后每轮它的
// 质量码都会退化成"未归类"（见 s7Plan.bad）。
func badQualities(byID map[string]driver.ReadResult) map[string]int {
	var bad map[string]int
	for id, r := range byID {
		if r.Quality != driver.QualityBadAddress {
			continue
		}
		if bad == nil {
			bad = make(map[string]int, 4)
		}
		bad[id] = r.Quality
	}
	return bad
}

// parseS7Range 把一段读回的数据按 AddressMap 解码回填到 byID。
// addrByID 为 id → addr 的预构建索引。
func parseS7Range(addrByID map[string]*po.DeviceAddress, r *S7Range, data []byte, byID map[string]driver.ReadResult) {
	for id, saddr := range r.AddressMap {
		a := addrByID[id]
		if a == nil {
			continue
		}
		// 计算在读取缓冲区中的字节偏移
		localOffset := saddr.ByteOffset - r.StartOffset
		if localOffset < 0 || localOffset+saddr.Span > len(data) {
			logf("s7: address %q (id=%s) out of read range [%d, %d)",
				a.Name, id, r.StartOffset, r.EndOffset)
			markQuality(byID, id, driver.QualityBadParse)
			continue
		}

		raw := data[localOffset : localOffset+saddr.Span]
		val, err := ParseS7Value(raw, saddr, a.DataType)
		quality := driver.QualityGood
		value := ""
		if err != nil {
			logf("s7: parse address %q failed: %v", a.Name, err)
			quality = 0
		} else {
			value = FormatS7Value(val, a.DataType)
		}
		byID[id] = driver.ReadResult{
			DeviceAddressID: id,
			Value:           value,
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         quality,
		}
		if quality != driver.QualityGood {
			// 设备已经应答了、本地解不出来 —— 与整段被拒（comms）不同，质量码是 QualityBadParse。
			markQuality(byID, id, driver.QualityBadParse)
		}
	}
}

// ==================== 区间失败的对半降级 ====================

// planFor 返回本批次的读取计划。
//
// 命中缓存直接返回。命中的是降级计划且已过 degradedPlanTTL 时删除条目、重新
// 按合并计划算 —— 让被摘出的地址回到合并区间里试一次（设备侧建出那个 DB 之后，
// 采集能自愈，不必人工重启或改配置）。
//
// 区间计算结果只读共享（解码路径不修改 S7Range），并发 Read 复用同一缓存安全；
// 缓存大小有界，超上限时清空重建（仅失去命中，正确性不受影响）。
func (d *s7Driver) planFor(sig uint64, addrs []po.DeviceAddress, cfg *S7Config) ([]S7Range, error) {
	now := time.Now()

	d.rangeCacheMu.Lock()
	if p, ok := d.rangeCache[sig]; ok {
		if !p.degraded || now.Sub(p.at) < degradedPlanTTL {
			d.rangeCacheMu.Unlock()
			return p.ranges, nil
		}
		delete(d.rangeCache, sig)
	}
	d.rangeCacheMu.Unlock()

	ranges, err := d.rangesFor(sig, addrs, cfg)
	if err != nil {
		return nil, err
	}
	return ranges, nil
}

// storeDegradedPlan 记下对半降级后的计划（只有降级过的批次才写这条）。
//
// 不回写的话，每轮都要为同一个坏地址重付一遍整段失败 + 对半降级的往返；
// 回写之后坏地址被摘在计划之外，后续轮次只发那些读得通的子区间。
//
// bad 是**本轮为止**全部已知坏点的质量码，由 badQualities(byID) 给出 —— 它天然是
// "上一轮缓存下来的（预填时已回填进 byID）+ 本轮新确认的"并集。
//
// **已有降级计划时保留它原来的时间戳**，不刷新成 now：每轮刷新会把 TTL 变成
// "永不过期"，而被摘出的地址正是靠 TTL 到期才回到合并区间里重试一次 ——
// 只要批次里还有另一个区间每轮都在降级（或每轮预算耗尽），它就会一直被推迟，
// 设备侧把地址改对之后采集**永远**自愈不了，而不是"最迟 5 分钟"。
func (d *s7Driver) storeDegradedPlan(sig uint64, ranges []S7Range, bad map[string]int) {
	at := time.Now()

	d.rangeCacheMu.Lock()
	if prev, ok := d.rangeCache[sig]; ok && prev.degraded {
		at = prev.at
	}
	if d.rangeCache == nil || len(d.rangeCache) >= driver.RangeCacheMaxEntries {
		d.rangeCache = make(map[uint64]s7Plan)
	}
	d.rangeCache[sig] = s7Plan{ranges: ranges, degraded: true, bad: bad, at: at}
	d.rangeCacheMu.Unlock()
}

// badFor 返回缓存中已知读不到的地址及其质量码，供本轮预填骨架回填。
// 没有计划、或降级计划已过期时返回 nil（过期由 planFor 负责删除，这里只判定不删）。
//
// 非降级计划也要回填：它的 bad 是**地址解析失败**的那几个点位（见 partitionBadAddrs），
// 这是个纯函数式的集合（随 RangeSig 重算），不需要任何跨轮持久化机制，也没有 TTL ——
// 降级计划才需要 TTL（摘出去的地址靠它到期回到合并区间重试一次）。
func (d *s7Driver) badFor(sig uint64) map[string]int {
	d.rangeCacheMu.Lock()
	defer d.rangeCacheMu.Unlock()
	p, ok := d.rangeCache[sig]
	if !ok || (p.degraded && time.Since(p.at) >= degradedPlanTTL) {
		return nil
	}
	return p.bad
}

// warnOnce 日志去重：同一 key 下内容不变就不重复打。
//
// 去重不是可选的：这里要打的两类日志（某个地址恒不可读、降级预算耗尽）每个采集
// 周期都成立，而插件日志没有级别这道运行时闸门，不去重就是每秒把同一行刷一遍。
//
// 但也不能是一次性的：坏地址的日志只出现在它被摘出去的那一轮，此后它已不在计划里、
// 不再被读取，自然也不再打日志 —— 后来才翻日志的人会找不到任何线索（修复前那条
// 日志反倒是每轮都打的）。按 warnTTL 重新说一次，既不刷屏，痕迹也留得下。
func (d *s7Driver) warnOnce(key, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	now := time.Now()

	d.warnMu.Lock()
	defer d.warnMu.Unlock()
	if d.warned == nil {
		d.warned = make(map[string]warnEntry)
	}
	if prev, ok := d.warned[key]; ok && prev.msg == msg && now.Sub(prev.at) < warnTTL {
		return
	}
	d.warned[key] = warnEntry{msg: msg, at: now}
	logf("%s", msg)
}

// s7RangeReader 读一段区间所需的两个动作。
//
// 抽成接口是为了让降级路径**可测**：drv 实际传的是 *S7Client，测试传一台假 PLC。
// S7Client 是具体类型、自带连接与会话状态，造不出来（与 Modbus 那两个 client 同理），
// 而降级路径恰恰是最需要逐请求断言的地方。
type s7RangeReader interface {
	readRange(r S7Range, buf []byte) error
	MarkDisconnected()
}

// readRangeDegrading 读取一个合并区间并回填结果。
//
//	整段读通              → 正常解析回填
//	确认地址不存在         → 转入对半降级（st.degraded 置位）
//	链路错误              → 标记断开 + 上抛，由采集引擎判定断连并重连
//	其余设备拒绝（瞬时状态） → 整段原样留在计划里，下轮重试
func (d *s7Driver) readRangeDegrading(reader s7RangeReader, r *S7Range,
	addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult, st *degradeState) error {

	buf := make([]byte, r.EndOffset-r.StartOffset)
	err := reader.readRange(*r, buf)
	if err == nil {
		parseS7Range(addrByID, r, buf, byID)
		return nil
	}
	if isConnErr(err) {
		reader.MarkDisconnected()
		logf("s7: network read range [%d, %d) failed: %v", r.StartOffset, r.EndOffset, err)
		return fmt.Errorf("s7: network read failed: %w", err)
	}

	// 设备拒绝了整段，但错误**不能确认某个地址不存在**（见 confirmMarkers 的说明）：
	// 既不降级也不确认任何点位，整段保持 Quality=0 并原样留在计划里，下轮重试。
	if !isAddressNotFoundErr(err) {
		// 不能确认任何地址存在与否，只能到通信级：整段仍留在计划里下轮重试。
		for id := range r.AddressMap {
			markQuality(byID, id, driver.QualityBadComms)
		}
		d.warnOnce(fmt.Sprintf("transient:%d:%d", r.StartOffset, r.EndOffset),
			"range [%d, %d) not readable now (%v), keep %d points for retry",
			r.StartOffset, r.EndOffset, err, len(r.AddressMap))
		return nil
	}

	// 确认是"地址/对象不存在"：多半是区间里混进了一个设备上不存在的地址
	//（没建的 DB、越界的偏移）。直接整段标 Quality=0 会把同区间里本来读得到的
	// 点位一起丢掉，所以这里按点位对半降级，把真正读不到的那几个摘出来。
	//
	// 已经知道整段读不通，所以这里直接拆，不重读这一整段
	//（degradeSpan 是"试读一段"，它会先读一次；再走它等于白付一次必然失败的往返）。
	pts := rangePoints(r, addrByID)
	logf("range [%d, %d) rejected (%v), degrade per point over %d points",
		r.StartOffset, r.EndOffset, err, len(pts))
	st.degraded = true
	return d.degradeBadSpan(reader, r, pts, addrByID, byID, st, err)
}

// degradeSpan 试读一段点位。
//
//	读得通              → 解析回填，并把该子区间收进计划（下轮直接照它读）
//	读不通且能确认地址问题 → 交给 degradeBadSpan 拆
//	预算耗尽 / 不能确认   → 该段收进 st.pending：本轮保持 Quality=0 并**原样留在
//	                      计划里**，下轮接着摘
func (d *s7Driver) degradeSpan(reader s7RangeReader, r *S7Range,
	pts []rangePoint, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	st *degradeState) error {

	if len(pts) == 0 {
		return nil
	}
	start, end := ptsBounds(pts)
	sub := S7Range{
		Area: r.Area, DB: r.DB,
		StartOffset: start, EndOffset: end,
		AddressMap: ptsMap(pts),
	}
	if !st.b.take() {
		st.pending = append(st.pending, sub)
		st.b.noteUnresolved(len(pts))
		return nil
	}

	buf := make([]byte, end-start)
	err := reader.readRange(sub, buf)
	if err == nil {
		parseS7Range(addrByID, &sub, buf, byID)
		st.subs = append(st.subs, sub)
		return nil
	}
	if isConnErr(err) {
		reader.MarkDisconnected()
		return fmt.Errorf("s7: network read sub-range [%d, %d) failed: %w", start, end, err)
	}
	if !isAddressNotFoundErr(err) {
		// 同上：不能确认地址问题时原样留着下轮再试，不确认任何点位。
		for _, p := range pts {
			markQuality(byID, p.id, driver.QualityBadComms)
		}
		st.pending = append(st.pending, sub)
		return nil
	}
	return d.degradeBadSpan(reader, r, pts, addrByID, byID, st, err)
}

// degradeBadSpan 处理一段**已确认读不通**的点位（cause 为那次拒绝的错误）。
//
//	只剩单点 → 确认这个地址在设备上不存在：保持 Quality=0，并从计划里摘掉
//	还有多点 → 一分为二，两半各自重试
//
// 递归必然终止：len(pts) >= 2 时 mid >= 1 且 mid < len(pts)，两半都严格变小。
func (d *s7Driver) degradeBadSpan(reader s7RangeReader, r *S7Range,
	pts []rangePoint, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	st *degradeState, cause error) error {

	if len(pts) == 1 {
		// 拆到单点仍被拒。保持 Quality=0（预填骨架），从计划里摘掉 ——
		// 留在计划里就是每轮为它重付一次必然失败的往返。
		//
		// 是 QualityBadAddress 而非 QualityBadComms：走到这里意味着设备明确回"这个地址/对象不存在"
		// 且对半降到单点仍被拒，即**地址本身**的问题（没建的 DB、越界的偏移），
		// 设备是好的 —— 它不会自己恢复，下一次仍会被摘在计划之外。
		name := pts[0].id
		if a := addrByID[pts[0].id]; a != nil {
			name = a.Name
		}
		markQuality(byID, pts[0].id, driver.QualityBadAddress)
		d.warnOnce("bad:"+pts[0].id,
			"address %q (id=%s) unreadable, mark quality=0: %v", name, pts[0].id, cause)
		return nil
	}
	mid := len(pts) / 2
	if err := d.degradeSpan(reader, r, pts[:mid], addrByID, byID, st); err != nil {
		return err
	}
	return d.degradeSpan(reader, r, pts[mid:], addrByID, byID, st)
}

// isolateBudget 对半降级的预算，**单次 Read 内共享**。
type isolateBudget struct {
	left     int
	deadline time.Time // 首个降级请求时锚定（零值 = 还没开始）
	skipped  int       // 因预算耗尽而本轮未取到值的点位数（收尾时汇总打一行）
}

func newIsolateBudget() *isolateBudget {
	return &isolateBudget{left: degradeMaxRequests}
}

// take 申请一次降级请求。false 表示预算已尽（请求数或时间，任一到了就算尽）。
//
// 时间窗口在**首个降级请求**时才开始计时，不是在 Read 开头：一个批次的常规读取
// 本身就可能花掉十几秒（离散区间多、每条 PDU 一来一回），从 Read 开头锚定的话
// 预算一出生就过期了 —— 恰好是最需要降级的那些设备上，降级一次都不会发生。
func (b *isolateBudget) take() bool {
	if b.left <= 0 {
		return false
	}
	now := time.Now()
	if b.deadline.IsZero() {
		b.deadline = now.Add(degradeMaxDuration)
	}
	if now.After(b.deadline) {
		return false
	}
	b.left--
	return true
}

func (b *isolateBudget) noteUnresolved(points int) { b.skipped += points }

// degradeState 一次区间降级的账。
//
// 两个出口都要**原样留在计划里**，只是理由不同：`subs` 是读得通的子区间，
// `pending` 是本轮没摘完的子区间（预算耗尽，或错误无法确认地址问题）。
type degradeState struct {
	b        *isolateBudget
	subs     []S7Range // 读得通的子区间
	pending  []S7Range // 本轮没摘完的子区间（下轮接着摘）
	degraded bool      // 本区间是否真的降过级
}

// rangePoint 区间内一个点位：id + **原始的** S7Address。
//
// 必须整体带着原地址，不能只留「偏移 + 跨度」再拼一个回去：S7Address 里还有
// BitOffset（非位地址是 -1、位地址是 0..7）与 IsString/Width 等字段，
// 丢一个就会让解码走错分支 —— 尤其 BitOffset 丢了默认成 0、`IsBit()` 立刻为真，
// 于是每个点位都按位解析、全部解析失败，界面上是一片 quality=0。
type rangePoint struct {
	id   string
	addr S7Address
}

// rangePoints 取出区间内的点位并按字节偏移排序 —— 对半降级按这个顺序切，
// 两半覆盖的范围才大致相当。**不能直接用 r.AddressMap 的遍历顺序**：map 顺序是
// 随机的，按它切等于随机分割，降级会退化成线性扫描（也难以收敛）。
func rangePoints(r *S7Range, addrByID map[string]*po.DeviceAddress) []rangePoint {
	pts := make([]rangePoint, 0, len(r.AddressMap))
	for id, saddr := range r.AddressMap {
		if addrByID[id] == nil {
			continue
		}
		pts = append(pts, rangePoint{id: id, addr: saddr})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].addr.ByteOffset != pts[j].addr.ByteOffset {
			return pts[i].addr.ByteOffset < pts[j].addr.ByteOffset
		}
		// 同址重名时用 id 兜底：map 遍历顺序随机，排序不稳定的话分点是随机的
		return pts[i].id < pts[j].id
	})
	return pts
}

// ptsBounds 返回一段点位覆盖的字节区间 [start, end)。
//
// 子区间恒是父区间的子集（只按点位切、不向外延伸），所以不会越出父区间。
func ptsBounds(pts []rangePoint) (int, int) {
	start := pts[0].addr.ByteOffset
	end := start + pts[0].addr.Span
	for _, p := range pts[1:] {
		if p.addr.ByteOffset < start {
			start = p.addr.ByteOffset
		}
		if e := p.addr.ByteOffset + p.addr.Span; e > end {
			end = e
		}
	}
	return start, end
}

// ptsMap 由点位列表重建 AddressMap，供 parseS7Range 按区间偏移解码。
func ptsMap(pts []rangePoint) map[string]S7Address {
	m := make(map[string]S7Address, len(pts))
	for _, p := range pts {
		m[p.id] = p.addr
	}
	return m
}

// partitionBadAddrs 挑出协议自己的解析器（ParseS7Address）认不出的点位。
//
// 返回可解析的点位，以及「地址非法」的点位按 id → QualityBadAddress 的记账 ——
// 后者直接喂给既有的 badFor / 预填骨架那条路，与降级路径的 bad 同构：两者都是
// 「永不自我恢复的地址级失败」，正是 driver.QualityBadAddress 的含义。
//
// 为什么必须在上游拆、而不是让 CalcS7Ranges 容错：CalcS7Ranges 的契约是
// 「全量解析、遇非法地址报错」（range_test.go 钉着它），拆在它之前它见到的就永远是
// 可解析的输入，那条错误退化为防御性代码。
//
// 一个地址本地解析不出来，修复前会让整个 Read 返回 error → 采集侧把设备判为离线
// （断联报警，而设备全程在线），并把该批之后的批次一起跳过。现在受损范围收敛到
// 这一个点位：它标 QualityBadAddress，由点位质量报警报出「地址无效」。
func (d *s7Driver) partitionBadAddrs(addrs []po.DeviceAddress) ([]po.DeviceAddress, map[string]int) {
	var valid []po.DeviceAddress
	var bad map[string]int
	for i := range addrs {
		a := &addrs[i]
		if _, ok := ParseS7Address(a.Name); ok {
			valid = append(valid, *a)
			continue
		}
		if bad == nil {
			bad = make(map[string]int, 2)
		}
		bad[a.ID] = driver.QualityBadAddress
		// 不自己写协议前缀：logf 统一加 "s7: "（见 log.go）。
		// 键用 "badaddr:"+id 而不是 "bad:"+id —— 后者是 markBadPoint 给**设备已确认**
		// 坏点用的键，分开才能在日志里分辨「本地写错」与「设备拒绝」。
		d.warnOnce("badaddr:"+a.ID,
			"address %q (id=%s) invalid format, mark quality=bad-address", a.Name, a.ID)
	}
	return valid, bad
}

// rangesFor 返回批次的合并读取区间：命中内容指纹缓存直接返回，未命中则计算并缓存。
// 区间计算结果只读共享（读取路径不修改 S7Range），并发 Read 复用同一缓存安全。
// 缓存大小有界：超上限时清空重建，正确性不受影响（仅失去命中）。
//
// 只算**合并**计划；降级后的计划由 planFor 从同一张缓存里取（见 s7Plan.degraded）。
func (d *s7Driver) rangesFor(sig uint64, addrs []po.DeviceAddress, cfg *S7Config) ([]S7Range, error) {
	d.rangeCacheMu.Lock()
	cached, ok := d.rangeCache[sig]
	d.rangeCacheMu.Unlock()
	if ok && !cached.degraded {
		return cached.ranges, nil
	}

	// 地址解析不出来的点位不进区间规划（它们一个字节都不该发出去），而是随计划
	// 带出去、由预填骨架标成 QualityBadAddress（见 partitionBadAddrs 与 s7Plan.bad）。
	// 拆在锁外：warnOnce 有自己的锁，不该在 rangeCacheMu 内获取。
	valid, bad := d.partitionBadAddrs(addrs)

	var ranges []S7Range
	if len(valid) > 0 {
		// 空输入直接跳过 CalcS7Ranges —— 它对 len(addrs)==0 是报错，
		// 而「这批点位一个都解析不出来」不该是一次失败。
		var err error
		ranges, err = CalcS7Ranges(valid, cfg.StringLen, cfg.MaxGap)
		if err != nil {
			return nil, err
		}
	}

	d.rangeCacheMu.Lock()
	// 再次检查，避免并发首轮重复计算同批区间
	if cached, ok := d.rangeCache[sig]; ok && !cached.degraded {
		d.rangeCacheMu.Unlock()
		return cached.ranges, nil
	}
	if d.rangeCache == nil || len(d.rangeCache) >= driver.RangeCacheMaxEntries {
		d.rangeCache = make(map[uint64]s7Plan)
	}
	d.rangeCache[sig] = s7Plan{ranges: ranges, bad: bad}
	d.rangeCacheMu.Unlock()
	return ranges, nil
}

func (d *s7Driver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *s7Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}
