// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"fmt"
	"sort"
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
// 到期后按合并计划重读一次：设备侧把那几个地址补上（文件寄存器没配置的软元件、
// 被清掉的链接区）之后，采集能自己恢复，不需要人工重启网关或改配置。
// 取值与 ABB 的 hints 静默期同量级（300s）。
//
// 注意它只在**已有降级计划**上倒计时，且 storeDegradedPlan 不刷新已有时间戳
// （见该函数的说明）—— 否则批次里只要有另一个区间每轮都在降级，这个窗口就永远
// 不会到期，被摘出的地址永远回不来。
const degradedPlanTTL = 5 * time.Minute

// warnTTL 同一条去重日志重新出现的间隔（见 warnOnce）。
const warnTTL = 10 * time.Minute

func init() {
	driver.Register(ProtocolMCTCP, func() driver.Driver {
		return newMCDriver(TransportTCP)
	})
	driver.Register(ProtocolMCSerial, func() driver.Driver {
		return newMCDriver(TransportSerial)
	})
}

// newMCDriver 创建绑定指定传输层的 MC 驱动实例。
// 两种协议（Mitsubishi.MC.TCP / Mitsubishi.MC.Serial）共享同一 MC 应用层
// （设备码、地址解析、区间计算、数据解码），仅底层传输与封帧不同。
func newMCDriver(transport string) *mcDriver {
	return &mcDriver{transport: transport}
}

// mcDriver 三菱 MC 协议驱动，实现 driver.Driver 接口。
//
// 传输层由协议注册名固定（transport 字段），不随 protocol_json 的 transport 切换：
//   - TCP：3E 帧二进制（Q/L/iQ-R/iQ-F 内置以太网口）
//   - Serial：4C 帧 Format5 二进制（C24 串口模块）
type mcDriver struct {
	mu        sync.RWMutex
	transport string // 传输层：TransportTCP / TransportSerial（注册名固定）
	config    *MCConfig
	client    mcTransport

	// rangeCache 区间内容指纹缓存：规避每轮重复解析地址与计算区间。
	// 区间计算是 (addrs, cfg) 的纯函数，轮询间批次内容不变即可命中；
	// 驱动实例在配置热刷新时由采集引擎重建，Connect 时也显式清空，缓存随之失效。
	//
	// 条目既可能是 CalcMCRanges 的合并计划，也可能是对半降级后的计划
	// （见 mcPlan.degraded）—— 后者带 TTL，到期删除以让被摘出的地址重新
	// 回到合并区间里试一次。
	rangeCacheMu sync.Mutex
	rangeCache   map[uint64]mcPlan

	// warned 日志去重：要去的重是"每轮都成立的条件"（降级预算耗尽、某个地址
	// 恒不可读），不去重就是每个采集周期把同一行刷一遍。
	warnMu sync.Mutex
	warned map[string]warnEntry
}

// mcPlan 一个批次的区间读取计划（driver.RangeSig 内容指纹 → 计划）。
type mcPlan struct {
	ranges []MCRange
	// degraded 为 true 表示这是对半降级后的计划：读不到的地址已被摘出合并区间。
	// 这类计划按 degradedPlanTTL 过期 —— 不设 TTL 的话，设备侧后来补上的地址
	// 会永远拿不回来（它已经被排除在所有区间之外了）。
	degraded bool
	// bad 已知读不到的地址 → 质量码（恒为 QualityBadAddress）。两个来源：
	//   ① **本地就进不了计划**（partitionBadAddrs 挑出来的，一个字节都没上过线）——
	//      地址解析不出来，或单点位跨度就超过 maxReadWords。非降级计划里就有它，
	//      是纯函数式的集合，没有 TTL；
	//   ② **设备已确认不存在**（对半降级到单点仍被拒）—— 只在降级计划里有。
	// 两者都必须缓存：这些地址已被摘出计划、不再参与任何一次解析，不带进来的话此后
	// 每轮的质量码都会退化成“未归类”，"这个地址坏"在数据上就看不出来（点位级报警按
	// 连续 N 轮判定时永远触发不了）。
	// 降级计划里的 bad 生命周期与 ranges 一致（同一条 TTL）；非降级计划的 bad 随
	// RangeSig 变化自然重算 —— 签名含 Name 与 DataType，把地址或类型改对就换了新计划，
	// 这正是①不需要 TTL 的原因。
	bad map[string]int
	at  time.Time
}

// SerialExclusive 串口传输独占串行总线，需整轮持锁（重连+全部批次读取+转换+推送）；
// 以太网（TCP）内部已线程安全，仅对「重连 + 单次 Read」加锁。
func (d *mcDriver) SerialExclusive() bool {
	return d.transport == TransportSerial
}

func (d *mcDriver) Connect(protocolJSON string) error {
	cfg, err := ParseMCConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("mc: parse config failed: %w", err)
	}
	// 传输层由协议注册名固定，覆盖 JSON 中的 transport 字段
	cfg.Transport = d.transport

	// 任何重连尝试都使区间缓存失效：配置可能已变化（MaxGap/MaxReadWords/StringLen/…），
	// 新连接必须使用重新计算的区间。降级计划也在同一张表里，一并失效 ——
	// 重连后设备侧可能已经补上了那个地址。
	d.rangeCacheMu.Lock()
	d.rangeCache = nil
	d.rangeCacheMu.Unlock()

	// 告警去重表同样清空：新连接下的失败是"新情况"，该重新说一次
	d.warnMu.Lock()
	d.warned = nil
	d.warnMu.Unlock()

	// 先摘下旧连接、释放串口，**再**建立新连接。
	//
	// Windows 下串口是独占的：不先释放旧句柄，newTransport 里的 CreateFile
	// 会直接 Access is denied。而串口读失败只把 connected 置 false、并不释放
	// 句柄，所以这里必须显式 Close —— 否则一次读超时就会让此后每次重连永久失败
	// （采集引擎的 ensureConnected 会反复调本方法），退避一路顶到上限。
	d.mu.Lock()
	old := d.client
	d.client = nil
	d.config = nil
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}

	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("mc: connect failed: %w", err)
	}

	d.mu.Lock()
	d.config = cfg
	d.client = client
	d.mu.Unlock()

	return nil
}

// matchConfig 返回本实例当前持有的、能直接服务 cfg 的那条传输（没有则 nil）。
//
// 返回传输对象而不是 bool，是为了让调用方拿到"刚才被判定可用的那一个" ——
// 否则判定与取用是两次独立的快照，中间 Connect 换掉 client 时会对着一个
// 已关闭的连接收发。
//
// 只有 Serial 参与复用：TCP 不独占端口，复用收益为零。
func (d *mcDriver) matchConfig(cfg *MCConfig) mcTransport {
	if d.transport != TransportSerial {
		return nil
	}
	d.mu.RLock()
	live, cur := d.client, d.config
	d.mu.RUnlock()

	if live == nil || !live.IsConnected() || !sameConfig(cur, cfg) {
		return nil
	}
	return live
}

// MatchConnection 见 driver.ConnectionMatcher。
func (d *mcDriver) MatchConnection(protocolJSON string) bool {
	if d.transport != TransportSerial {
		return false
	}
	cfg, err := ParseMCConfig(protocolJSON)
	if err != nil {
		// JSON 非法时不复用，交给 Ping 去报那条真正的解析错误
		return false
	}
	// 与 Connect/Ping 保持一致：传输层由协议注册名固定，JSON 里的 transport
	// 不参与比较，否则协议表单里残留 transport=tcp 的串口设备永远匹配不上。
	cfg.Transport = d.transport
	return d.matchConfig(cfg) != nil
}

func (d *mcDriver) Ping(protocolJSON string) error {
	cfg, err := ParseMCConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("mc: ping parse config failed: %w", err)
	}
	cfg.Transport = d.transport

	// 若当前驱动实例已持有同参数的连接（如采集引擎运行中已 Connect），
	// 直接复用现有连接做轻量读，避免对同一串口再开一个句柄——
	// Windows 下串口被独占时二次打开会报 Access is denied。
	//
	// 判据与 MatchConnection 共用同一个 matchConfig，"这次 ping 该不该复用"
	// 在会话层与驱动层只有一个答案，不会各判各的。
	if live := d.matchConfig(cfg); live != nil {
		if err := pingRead(live, cfg); err != nil {
			return fmt.Errorf("mc: ping failed (reused connection): %w", err)
		}
		logf("mc ping success: transport=%s (reused connection)", cfg.Transport)
		return nil
	}

	// 无状态握手：建立临时连接做轻量读后立即关闭，不修改驱动内部状态
	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("mc: ping connect failed: %w", err)
	}
	defer client.Close()

	if err := pingRead(client, cfg); err != nil {
		return fmt.Errorf("mc: ping failed: %w", err)
	}
	logf("mc ping success: transport=%s", cfg.Transport)
	return nil
}

// pingRead 轻量连通性验证：读取 D 设备 0 号（1 点字）。
// 结束码错误（设备响应但拒绝，如 D0 越界）同样证明设备可达，视为成功；
// 仅网络/超时错误判定不可达。
func pingRead(t mcTransport, _ *MCConfig) error {
	dev, _ := lookupDevice("D")
	_, err := t.Read(dev, 0, 1, false)
	if err != nil && !IsEndCodeError(err) {
		return err
	}
	return nil
}

func (d *mcDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("mc: not connected")
	}

	sig := driver.RangeSig(addrs)
	ranges, err := d.planFor(sig, addrs, cfg)
	if err != nil {
		return nil, fmt.Errorf("mc: calc address ranges failed: %w", err)
	}

	// 预构建 id → addr 索引（解码时 O(1) 取回 DataType/Name）并预填结果骨架。
	//
	// 骨架必须先填：读不到的点位（拆到单点仍被设备拒绝、或降级预算耗尽）保持
	// Quality=0，但 DataType/Kind 得原样回填 —— 空着就是往下游发一条没有类型的
	// 记录，而下游（MQTT 负载、时序库表结构）正靠它决定怎么写。
	// 上一轮已确认读不到的地址 → 质量码。它们已被摘出计划、本轮不会再被任何解析碰到，
	// 只有在这里带进来，其质量码才不会在后续每轮退化成“未归类”。
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

	// 逐区间读取。整段读通 → 正常解析；结束码错误（设备响应但拒绝整段）→
	// 对半降级，只把真正读不到的点位标 Quality=0，同区间的其余点位照常取值；
	// 网络/超时错误 → 向上返回，由采集引擎判定断连并重连。
	//
	// 降级只在失败的区间上发生，但一个批次可能有多个失败区间，所以预算在整次
	// Read 内共享 —— 否则每个区间各花满一份，总耗时随区间数线性放大。
	budget := newIsolateBudget()
	next := make([]MCRange, 0, len(ranges))
	degraded := false
	for i := range ranges {
		r := &ranges[i]
		st := &degradeState{b: budget}
		if err := d.readRange(client, cfg, r, addrByID, byID, st); err != nil {
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
		d.warnOnce("budget", "mc: degrade budget exhausted, %d points left unread this round (continues next round)", budget.skipped)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应；
	// 缺失点位（确认不可读、或降级预算耗尽）保持预填充的 Quality=0 空结果
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// planFor 返回本批次的读取计划。
//
// 命中缓存直接返回。命中的是降级计划且已过 degradedPlanTTL 时删除条目、重新
// 按合并计划算 —— 让被摘出的地址回到合并区间里试一次（设备侧补上那个地址后，
// 采集能自愈，不必人工重启或改配置）。
//
// 区间计算结果只读共享（解码路径不修改 MCRange），并发 Read 复用同一缓存安全；
// 缓存大小有界，超上限时清空重建（仅失去命中，正确性不受影响）。
func (d *mcDriver) planFor(sig uint64, addrs []po.DeviceAddress, cfg *MCConfig) ([]MCRange, error) {
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

	// 进不了区间规划的点位（解析不出 / 单点位跨度超限）一个字节都不该发出去，
	// 而是随计划带出去、由预填骨架标成 QualityBadAddress（见 partitionBadAddrs 与
	// mcPlan.bad）。拆在锁外：warnOnce 有自己的锁，不该在 rangeCacheMu 内获取。
	valid, bad := d.partitionBadAddrs(addrs, cfg)

	var ranges []MCRange
	if len(valid) > 0 {
		// 空输入直接跳过 CalcMCRanges —— 它对 len(addrs)==0 是报错，
		// 而「这批点位一个都不能用」不该是一次失败。
		var err error
		ranges, err = CalcMCRanges(valid, cfg)
		if err != nil {
			return nil, err
		}
	}

	d.rangeCacheMu.Lock()
	// 再次检查，避免并发首轮重复计算同批区间
	if p, ok := d.rangeCache[sig]; ok {
		d.rangeCacheMu.Unlock()
		return p.ranges, nil
	}
	if d.rangeCache == nil || len(d.rangeCache) >= driver.RangeCacheMaxEntries {
		d.rangeCache = make(map[uint64]mcPlan)
	}
	d.rangeCache[sig] = mcPlan{ranges: ranges, bad: bad, at: now}
	d.rangeCacheMu.Unlock()
	return ranges, nil
}

// partitionBadAddrs 挑出**进不了区间规划**的点位：地址解析不出来的，以及单点位
// 跨度就超过单请求上限的。
//
// 返回可用的点位，以及「不可用」的点位按 id → QualityBadAddress 的记账 ——
// 后者直接喂给既有的 badFor / 预填骨架那条路，与降级路径的 bad 同构：两者都是
// 「永不自我恢复的地址级失败」，正是 driver.QualityBadAddress 的含义。
//
// 两个来源的判据都取自 range.go 里 CalcMCRanges 用的那一套（mcParseAddr 与
// mcSpanTooLarge），不另立校验器 —— 位/字模式的判定、字符串长度、上限钳制全部复用，
// 两条路径因此不可能对同一个点位给出不同答案。
//
// 为什么必须在上游拆、而不是让 CalcMCRanges 容错：CalcMCRanges 的契约是
// 「全量解析、遇非法地址或超限跨度报错」（range_test.go 与 pointscan_test.go 钉着它），
// 拆在它之前它见到的就永远是合法输入，那两条错误退化为防御性代码。
//
// 一个点位不可用，修复前会让整个 Read 返回 error → 采集侧把设备判为离线
// （断联报警，而设备全程在线），并把该批之后的批次一起跳过。现在受损范围收敛到
// 这一个点位：它标 QualityBadAddress，由点位质量报警报出「地址无效」。
func (d *mcDriver) partitionBadAddrs(addrs []po.DeviceAddress, cfg *MCConfig) ([]po.DeviceAddress, map[string]int) {
	var valid []po.DeviceAddress
	var bad map[string]int
	mark := func(a *po.DeviceAddress, format string, args ...any) {
		if bad == nil {
			bad = make(map[string]int, 2)
		}
		bad[a.ID] = driver.QualityBadAddress
		// 不自己写协议前缀：logf 统一加 "mitsubishi: "（见 log.go）。
		// 键用 "badaddr:"+id 而不是 "bad:"+id —— 后者是 markBadPoint 给**设备已确认**
		// 坏点用的键，分开才能在日志里分辨「本地写错」与「设备拒绝」。
		d.warnOnce("badaddr:"+a.ID, format, args...)
	}
	stringLen := mcStringLen(cfg)
	maxWords := mcMaxReadWords(cfg)
	for i := range addrs {
		a := &addrs[i]
		addr, _, ok := mcParseAddr(*a, stringLen)
		if !ok {
			mark(a, "address %q (id=%s) invalid format, mark quality=bad-address", a.Name, a.ID)
			continue
		}
		// 写得出来的地址、但声明的类型/长度放不进单次请求：与解析失败一样无处可去
		// （一次请求的数据须连续，不能拆块），同样摘出计划。
		if mcSpanTooLarge(addr, maxWords) {
			mark(a, "address %q (id=%s) spans %d words, exceeds maxReadWords %d, mark quality=bad-address",
				a.Name, a.ID, addr.SpanWords, maxWords)
			continue
		}
		valid = append(valid, *a)
	}
	return valid, bad
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
// 设备侧补上地址之后采集**永远**自愈不了，而不是"最迟 5 分钟"。
func (d *mcDriver) storeDegradedPlan(sig uint64, ranges []MCRange, bad map[string]int) {
	at := time.Now()

	d.rangeCacheMu.Lock()
	if prev, ok := d.rangeCache[sig]; ok && prev.degraded {
		at = prev.at
	}
	if d.rangeCache == nil || len(d.rangeCache) >= driver.RangeCacheMaxEntries {
		d.rangeCache = make(map[uint64]mcPlan)
	}
	d.rangeCache[sig] = mcPlan{ranges: ranges, degraded: true, bad: bad, at: at}
	d.rangeCacheMu.Unlock()
}

// badFor 返回缓存中已知读不到的地址及其质量码，供本轮预填骨架回填。
// 没有计划、或降级计划已过期时返回 nil（过期由 planFor 负责删除，这里只判定不删）。
//
// 非降级计划也要回填：它的 bad 是**地址解析失败**的那几个点位（见 partitionBadAddrs），
// 这是个纯函数式的集合（随 RangeSig 重算），不需要任何跨轮持久化机制，也没有 TTL ——
// 降级计划才需要 TTL（摘出去的地址靠它到期回到合并区间重试一次）。
func (d *mcDriver) badFor(sig uint64) map[string]int {
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
func (d *mcDriver) warnOnce(key, format string, args ...any) {
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

// warnEntry 一条去重记录：内容 + 上次打印的时间。
type warnEntry struct {
	msg string
	at  time.Time
}

// readRange 读取一个合并区间并回填结果。
//
//	整段读通                → 正常解析回填
//	地址类结束码错误          → 转入对半降级（st.degraded 置位）
//	非地址类结束码错误 / 网络错误 → 见下，分别是"整个区间下轮重试"与"上抛"
func (d *mcDriver) readRange(client mcTransport, cfg *MCConfig, r *MCRange,
	addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult, st *degradeState) error {

	data, err := client.Read(r.Device, r.Start, r.Points, r.BitMode)
	if err == nil {
		parseRangeResults(addrByID, r, data, cfg, byID)
		return nil
	}
	if !IsEndCodeError(err) {
		return fmt.Errorf("mc: read range device=%s bit=%v [%d, %d) failed: %w",
			r.Device.name, r.BitMode, r.Start, r.Start+uint32(r.Points), err)
	}

	// 结束码不是"地址问题"（如 0xC0EF 通信错误 / PLC 忙 / 程序运行中）：这条错误
	// **说明不了任何一个地址是否存在**，所以既不降级也不确认任何点位 ——
	// 降级会把整段点位逐个"确认"成地址不存在，还会把这份误判缓存住。
	// 整段保持 Quality=0（预填骨架）并原样留在计划里，下轮重试。
	//
	// 复用点扫那条路已经有的白名单（pointscan.go 的 isAddressEndCode），不另立判据：
	// 同一个问题在两条路径上给出两种答案，是最难查的那种漂移。
	if !isAddressEndCode(err) {
		// 不能确认任何地址存在与否，只能到通信级：整段仍留在计划里下轮重试。
		for id := range r.AddressMap {
			markQuality(byID, id, driver.QualityBadComms)
		}
		d.warnOnce(fmt.Sprintf("transient:%s:%d:%v", r.Device.name, r.Start, r.BitMode),
			"mc: range device=%s bit=%v [%d, %d) not readable now (%v), keep %d points for retry",
			r.Device.name, r.BitMode, r.Start, r.Start+uint32(r.Points), err, len(r.AddressMap))
		return nil
	}

	// 设备拒绝了整段，且这是地址类错误：多半是区间里混进了一个设备上没有的地址
	//（越界的软元件号、没配置的文件寄存器区、被清掉的链接区）。直接整段标
	// Quality=0 会把同区间里本来读得到的点位一起丢掉，所以这里按点位对半降级，
	// 把真正读不到的摘出来。
	//
	// 已经知道整段读不通，所以这里直接拆，不重读这一整段
	//（degradeSpan 是"试读一段"，它会先读一次；再走它等于白付一次必然失败的往返）。
	pts := rangePoints(r, addrByID)
	logf("mc: range device=%s bit=%v [%d, %d) rejected (%v), degrade per point over %d points",
		r.Device.name, r.BitMode, r.Start, r.Start+uint32(r.Points), err, len(pts))
	st.degraded = true
	return d.degradeBadSpan(client, cfg, r, pts, addrByID, byID, st, err)
}

// degradeSpan 试读一段点位。
//
//	读得通              → 解析回填，并把该子区间收进计划（下轮直接照它读）
//	读不通（地址类）      → 交给 degradeBadSpan 拆
//	预算耗尽 / 非地址错误 → 该段收进 st.pending：本轮保持 Quality=0 并**原样留在
//	                      计划里**，下轮接着摘
//
// 网络/超时错误原样上抛 —— 否则一次断连会被静默成"点位质量 0"，沉进本轮数据里，
// 还永远不触发重连。
func (d *mcDriver) degradeSpan(client mcTransport, cfg *MCConfig, r *MCRange,
	pts []rangePoint, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	st *degradeState) error {

	if len(pts) == 0 {
		return nil
	}
	start, points := ptsBounds(r, pts)
	if !st.b.take() {
		st.pending = append(st.pending, MCRange{
			Device: r.Device, BitMode: r.BitMode, Start: start, Points: points, AddressMap: ptsMap(pts),
		})
		st.b.noteUnresolved(len(pts))
		return nil
	}

	data, err := client.Read(r.Device, start, points, r.BitMode)
	if err == nil {
		sub := MCRange{
			Device:     r.Device,
			BitMode:    r.BitMode,
			Start:      start,
			Points:     points,
			AddressMap: ptsMap(pts),
		}
		parseRangeResults(addrByID, &sub, data, cfg, byID)
		st.subs = append(st.subs, sub)
		return nil
	}
	if !IsEndCodeError(err) {
		return fmt.Errorf("mc: degraded read device=%s bit=%v [%d, %d) failed: %w",
			r.Device.name, r.BitMode, start, start+uint32(points), err)
	}
	if !isAddressEndCode(err) {
		// 同上：非地址类结束码说明不了这个地址是否存在。整段原样留着下轮再试，
		// 不确认任何点位 —— 也不能把它当成"拆到单点仍读不到"。
		for _, p := range pts {
			markQuality(byID, p.id, driver.QualityBadComms)
		}
		st.pending = append(st.pending, MCRange{
			Device: r.Device, BitMode: r.BitMode, Start: start, Points: points, AddressMap: ptsMap(pts),
		})
		return nil
	}
	return d.degradeBadSpan(client, cfg, r, pts, addrByID, byID, st, err)
}

// degradeBadSpan 处理一段**已知读不通**的点位（cause 为那次拒绝的错误，必为地址类）。
//
//	只剩单点 → 确认这个地址在设备上不存在：保持预填的 Quality=0，并从计划里摘掉
//	还有多点 → 一分为二，两半各自重试
//
// 递归必然终止：len(pts) >= 2 时 mid >= 1 且 mid < len(pts)，两半都严格变小。
func (d *mcDriver) degradeBadSpan(client mcTransport, cfg *MCConfig, r *MCRange,
	pts []rangePoint, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	st *degradeState, cause error) error {

	if len(pts) == 1 {
		// 拆到单点仍被拒。保持 Quality=0（预填骨架），从计划里摘掉 ——
		// 留在计划里就是每轮为它重付一次必然失败的往返。
		//
		// 是 QualityBadAddress 而非 QualityBadComms：走到这里意味着设备明确回了地址类结束码
		// 且对半降到单点仍被拒，即**地址本身**的问题，设备是好的 —— 它不会自己恢复，
		// 下一次仍会被摘在计划之外。
		markQuality(byID, pts[0].id, driver.QualityBadAddress)
		d.markBadPoint(pts[0].id, addrByID, cause)
		return nil
	}
	mid := len(pts) / 2
	if err := d.degradeSpan(client, cfg, r, pts[:mid], addrByID, byID, st); err != nil {
		return err
	}
	return d.degradeSpan(client, cfg, r, pts[mid:], addrByID, byID, st)
}

// markBadPoint 记下一个已确认读不到的地址。
//
// 日志按地址去重：这个条件每个采集周期都成立，不去重就是每秒一行；而它大约每
// degradedPlanTTL 才会重新走一遍（降级计划过期后重新纳入合并区间）。
func (d *mcDriver) markBadPoint(id string, addrByID map[string]*po.DeviceAddress, cause error) {
	name := id
	if a := addrByID[id]; a != nil {
		name = a.Name
	}
	d.warnOnce("bad:"+id, "mc: address %q (id=%s) unreadable, mark quality=0: %v", name, id, cause)
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
// 时间窗口在**首个降级请求**时才开始计时，不是在 Read 开头：串口链路上一个批次
// 的常规读取本身就可能花掉十几秒（9600bps，几百个离散区间），从 Read 开头锚定的话
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
// `pending` 是本轮没摘完的子区间（预算耗尽，或设备回了非地址类结束码）。
// pending 留在计划里而不是丢掉，是"下轮接着摘"能成立的前提 —— 见 Read 里的说明。
type degradeState struct {
	b        *isolateBudget
	subs     []MCRange // 读得通的子区间
	pending  []MCRange // 本轮没摘完的子区间（下轮接着摘）
	degraded bool      // 本区间是否真的降过级
}

// rangePoint 区间内一个点位（id + 已填充 Word/SpanWords/BitMode 的地址）。
type rangePoint struct {
	id   string
	addr MCAddress
}

// rangePoints 取出区间内的点位并按**该区间的地址单位**排序 —— 对半降级按这个
// 顺序切，两半覆盖的范围才大致相当。**不能直接用 r.AddressMap 的遍历顺序**：
// map 顺序是随机的，按它切等于随机分割，降级会退化成线性扫描。
func rangePoints(r *MCRange, addrByID map[string]*po.DeviceAddress) []rangePoint {
	pts := make([]rangePoint, 0, len(r.AddressMap))
	for id, addr := range r.AddressMap {
		if addrByID[id] == nil {
			continue
		}
		pts = append(pts, rangePoint{id: id, addr: addr})
	}
	sort.Slice(pts, func(i, j int) bool {
		li, lj := pointLo(pts[i], r.BitMode), pointLo(pts[j], r.BitMode)
		if li != lj {
			return li < lj
		}
		// 同址重名时用 id 兜底：map 遍历顺序随机，排序不稳定的话分点是随机的，
		// 降级能不能收敛就变成了碰运气。
		return pts[i].id < pts[j].id
	})
	return pts
}

// ptsBounds 返回一段点位在**本区间自己的单位**下覆盖的读取范围。
//
// 单位随区间语义而定，两者不能混用：位模式（BitMode）按位（Number 即位地址，
// 每点 1 位），字模式按字（Word，位设备的字访问已按 16 位对齐，见 CalcMCRanges）。
// 拿位地址去算字模式的点数会把读取头落到别的字上 —— 读到的是**别处**的数据，
// 而值看着完全合理。
//
// 子区间恒是父区间的子集（只按点位切、不向外延伸），点数因此不会超过父区间的
// Points，不会溢出 uint16 —— 父区间的 Points 本身受 maxReadWords 限制。
func ptsBounds(r *MCRange, pts []rangePoint) (uint32, uint16) {
	lo, hi := pointLo(pts[0], r.BitMode), pointHi(pts[0], r.BitMode)
	for _, p := range pts[1:] {
		if v := pointLo(p, r.BitMode); v < lo {
			lo = v
		}
		if v := pointHi(p, r.BitMode); v > hi {
			hi = v
		}
	}
	return lo, uint16(hi - lo)
}

// pointLo / pointHi 返回一个点位在该区间单位下占用的半开区间 [lo, hi)。
func pointLo(p rangePoint, bitMode bool) uint32 {
	if bitMode {
		return p.addr.Number
	}
	return p.addr.Word
}

func pointHi(p rangePoint, bitMode bool) uint32 {
	if bitMode {
		return p.addr.Number + 1
	}
	return p.addr.Word + p.addr.SpanWords
}

// ptsMap 由点位列表重建 AddressMap，供 parseRangeResults 按区间偏移解码。
func ptsMap(pts []rangePoint) map[string]MCAddress {
	m := make(map[string]MCAddress, len(pts))
	for _, p := range pts {
		m[p.id] = p.addr
	}
	return m
}

// markQuality 给一个点位标上质量码。
//
// 两条守卫，都是为了让这一维只在"确实异常"时出现：
//   - 记录不在 byID 里（不属于本批点位）时跳过；
//   - **Quality==QualityGood 的一律不动** —— 正常点位不该被标坏，也保证某轮
//     侥幸读通的点位不会带着上一轮的质量码（解析函数是整条记录覆写，本就不会残留，
//     这里再加一道保险）。
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
// 质量码都会退化成“未归类”（见 mcPlan.bad）。
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

// parseRangeResults 将区间读取数据按 AddressMap 解码回填到 byID。
// addrByID 为 id → addr 的预构建索引。
func parseRangeResults(addrByID map[string]*po.DeviceAddress, r *MCRange, data []byte, _ *MCConfig, byID map[string]driver.ReadResult) {
	for id, maddr := range r.AddressMap {
		a, ok := addrByID[id]
		if !ok || a == nil {
			continue
		}

		var raw []byte
		if r.BitMode {
			// 位模式：1 点 = 1 字节（0x00/0x01），按位地址偏移
			offset := int(maddr.Number) - int(r.Start)
			if offset < 0 || offset >= len(data) {
				logf("mc: address %q (id=%s) out of read range", a.Name, id)
				markQuality(byID, id, driver.QualityBadParse)
				continue
			}
			raw = data[offset : offset+1]
		} else {
			// 字模式：按字地址偏移（每字 2 字节）。
			// 位设备字访问：Word/Start 为 16 对齐的位地址，需按 16 位/字折算；
			// 字设备：Word/Start 为字地址，直接偏移。
			var offset int
			if r.Device.isBit {
				offset = (int(maddr.Word) - int(r.Start)) / 16 * 2
			} else {
				offset = (int(maddr.Word) - int(r.Start)) * 2
			}
			bytesLen := int(maddr.SpanWords) * 2
			if offset < 0 || offset+bytesLen > len(data) {
				logf("mc: address %q (id=%s) out of read range [%d, %d)",
					a.Name, id, r.Start, r.Start+uint32(r.Points))
				markQuality(byID, id, driver.QualityBadParse)
				continue
			}
			raw = data[offset : offset+bytesLen]
		}

		val, err := ParseMCValue(raw, maddr, a.DataType)
		quality := driver.QualityGood
		value := ""
		if err != nil {
			logf("mc: parse address %q failed: %v", a.Name, err)
			quality = 0
		} else {
			value = FormatMCValue(val, a.DataType)
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

func (d *mcDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *mcDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}

// sameConfig 判断两份配置的关键连接参数是否一致，用于 Ping 复用已有连接。
func sameConfig(a, b *MCConfig) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Transport == b.Transport &&
		a.Host == b.Host && a.Port == b.Port &&
		// EqualFold：Windows 的串口名大小写不敏感，"com1" 与 "COM1" 是同一个端口，
		// 用 == 比会把本可复用的情况误判成不可复用，退回临时实例后撞 Access is denied。
		strings.EqualFold(a.ComPort, b.ComPort) &&
		a.BaudRate == b.BaudRate && a.DataBits == b.DataBits &&
		a.StopBits == b.StopBits && strings.EqualFold(a.Parity, b.Parity)
}

// 编译期断言：签名漂移时类型断言会静默失败 → 复用退回临时实例 → 又变回
// Access is denied，且没有任何编译错误提示。这条断言是它的唯一防线。
var _ driver.ConnectionMatcher = (*mcDriver)(nil)
