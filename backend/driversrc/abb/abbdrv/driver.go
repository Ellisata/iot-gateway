// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// readBudget 单次 Read 的总预算。
//
// 必须显著低于网关给插件的 Read 超时（pluginhost 的 readTimeoutMs，默认 60s）。
// 越过的表现是"网关判未连接 → 重连 → 重新探测"，比慢一轮糟得多。
const readBudget = 45 * time.Second

// connectBudgetCap Connect/Ping 的总预算上限。
// 探测每个候选最多 3s（三个候选 9s）+ 一次验证请求（含 digest 重试共两次），
// 25s 留有余量，又低于网关切过来的 30s。
const connectBudgetCap = 25 * time.Second

// listKeySignals 粘性禁用状态里 IO 信号列表的键。
const listKeySignals = "signals"

// newClientFunc 建立并完成握手的客户端工厂。测试可注入替身。
type newClientFunc func(ctx context.Context, cfg *Config, withSession bool) (*rwsClient, error)

// newRealClient 生产用的客户端工厂。
// withSession 为 false 时不创建 RWS 会话（Ping 走这条路）。
func newRealClient(ctx context.Context, cfg *Config, withSession bool) (*rwsClient, error) {
	c := newRawClient(cfg)
	if err := c.connect(ctx, withSession); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// abbDriver ABB RWS 协议驱动，实现 driver.Driver 接口。
//
// 底层是 HTTP，**没有长连接**：连接状态由本结构自维护（rwsClient 的
// connected 标记），由 Read 遇到传输级错误或鉴权失败时置位，供采集引擎
// 判定重连。不实现 SerialExclusive —— HTTP 不是串行总线，采集引擎按
// 非独占驱动处理（仅对「重连 + 单次 Read」加锁），所以缓存与计数器
// 都必须自己保证并发安全。
type abbDriver struct {
	mu     sync.RWMutex
	cfg    *Config
	client *rwsClient

	newClient newClientFunc

	plans planCache

	// hints 回退提示：点位名 → 该点位该不该继续走批量/是否已确认不存在。
	// 跨轮存活，所以键必须 strings.Clone（见 buildPlan 的说明）。
	hintMu sync.Mutex
	hints  map[string]hint

	// warned "这个点位取不到值"的告警去重：键 → 上次打日志的时间。
	//
	// 与 hints **刻意分开**：hints 会改变读取路径（不再发请求），而这张表
	// 只影响日志。混在一起的话，"给日志消音"就会顺带变成"不再采集" ——
	// 一个只想安静点的改动会静默改掉采到的值。
	warnMu sync.Mutex
	warned map[string]time.Time

	// disables 列表请求的粘性禁用状态。
	disableMu sync.Mutex
	disables  map[string]*disabledList
}

func newAbbDriver() *abbDriver {
	return &abbDriver{newClient: newRealClient}
}

// Connect 探测端点、验证凭据并建立会话。
func (d *abbDriver) Connect(protocolJSON string) error {
	cfg, err := ParseConfig(protocolJSON)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectBudget(cfg))
	defer cancel()

	client, err := d.newClientOr(ctx, cfg, true)
	if err != nil {
		return err
	}

	d.mu.Lock()
	if d.client != nil {
		d.client.Close()
	}
	d.cfg = cfg
	d.client = client
	d.mu.Unlock()

	// 配置可能已变化（地址、凭据、批量开关），所有跨轮缓存一律失效。
	d.plans.clear()
	d.clearHints()
	d.clearDisables()
	d.clearWarns()
	return nil
}

// Ping 测试连通性，**不分配会话资源**。
//
// 契约要求（见 driver.PingDevice 与 driversdk 的 ping 处理）：Ping 用的临时
// 驱动实例不会有人调用 Close，网关也不会保存任何状态。所以这里只做
// "探测 + 一次带凭据的 GET + 释放连接"，绝不发会话创建请求
// （那是唯一会占用控制器会话槽的动作），也不碰驱动实例的任何字段 ——
// Ping 之后 IsConnected 必须仍然是 false。
func (d *abbDriver) Ping(protocolJSON string) error {
	cfg, err := ParseConfig(protocolJSON)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectBudget(cfg))
	defer cancel()

	client, err := d.newClientOr(ctx, cfg, false)
	if err != nil {
		return err
	}
	base, version, auth := client.base, client.version, client.authScheme()
	client.Close()

	logf("ping success: %s (rws %s, auth %s)", base, version, auth)
	return nil
}

// newClientOr 调注入的工厂；未注入时回退到生产实现。
func (d *abbDriver) newClientOr(ctx context.Context, cfg *Config, withSession bool) (*rwsClient, error) {
	fn := d.newClient
	if fn == nil {
		fn = newRealClient
	}
	return fn(ctx, cfg, withSession)
}

// snapshot 取当前配置与客户端（连接可能已被其它 goroutine 换掉）。
func (d *abbDriver) snapshot() (*Config, *rwsClient) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.cfg, d.client
}

// Read 读取一批点位。
//
// 策略是**批量优先 + 单点回退**：
//
//  1. 按本轮地址里出现的资源分组，每类资源只发一次请求
//     （IO 信号全量列表、每个 panel/motion/system 资源各一次）；
//  2. 列表里没有的点位再走单点 GET，且受条数与时间双重预算约束。
//
// 典型一轮是 2~3 个请求覆盖上千个点位 —— 控制器有 <20 req/s 的硬限流，
// 逐点 GET 在现场必然撞墙（何况示教器还在用同一条通道）。
//
// **RAPID 符号是例外，它永远走单点 GET。** RWS 的 /rw/rapid/symbols 搜索端点
// 返回的是符号**描述符**（名字、类型、路径），不含值，所以"先拉列表再索引"
// 对它省不下任何请求。与其发一次必然白跑的搜索请求，不如直说：RAPID 点位
// 不参与批量，请把数量控制在合理范围（几十个），把 IO 信号交给批量路径。
//
// 另需留意：signal 列表之所以**每轮都重新拉**，是因为它的响应里直接带着值
// （lvalue），缓存它等于把值冻住，症状是"曲线变成一条水平线"。本驱动因此
// 不做任何响应缓存 —— 跨轮存活的状态只有两类与值无关的东西：怎么解析地址
// （planCache）和取不到值怎么办（hints / warned）。要加缓存，先确认要缓存的
// 是这两类中的哪一类。
func (d *abbDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	cfg, client := d.snapshot()
	if client == nil || !client.IsConnected() || client.IsClosed() {
		return nil, errors.New("abb: not connected")
	}

	// 先按入参构造结果骨架：DataType 原样回填、Kind 取配置类型的类别、
	// Quality 一律是“坏、未归类”，取到值才置 driver.QualityGood / QualityUncertain。
	results := make([]driver.ReadResult, len(addrs))
	for i := range addrs {
		results[i] = driver.ReadResult{
			DeviceAddressID: addrs[i].ID,
			DataType:        addrs[i].DataType,
			Kind:            typeKind(addrs[i].DataType),
			Quality:         driver.QualityBad,
		}
	}
	if len(addrs) == 0 {
		return results, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), readBudget)
	defer cancel()

	plan := d.planFor(addrs)
	done := make([]bool, len(addrs))
	rc := newRoundCache()

	if err := d.readBulk(ctx, client, cfg, addrs, plan, results, done, rc); err != nil {
		client.MarkDisconnected()
		return nil, err
	}
	if err := d.readFallback(ctx, client, cfg, addrs, plan, results, done, rc); err != nil {
		client.MarkDisconnected()
		return nil, err
	}
	return results, nil
}

// readBulk 批量阶段：每类资源一次请求。
//
// 返回非 nil 表示传输级/鉴权失败 —— 调用方据此置未连接并中止整轮。
// 服务级失败（不支持的资源、响应不可解析）只降级到单点回退，**不动连接状态**。
func (d *abbDriver) readBulk(
	ctx context.Context, client *rwsClient, cfg *Config,
	addrs []po.DeviceAddress, plan *readPlan,
	results []driver.ReadResult, done []bool, rc *roundCache,
) error {
	// ---- IO 信号全量列表 ----
	var sigIdx *stateIndex
	if plan.needsSignals && cfg.BulkRead && !d.listDisabled(listKeySignals) &&
		!d.allSignalsOverridden(addrs, plan) {
		idx, err := client.fetchSignalIndex(ctx)
		switch {
		case err == nil:
			sigIdx = idx
			d.noteListSuccess(listKeySignals)
			logDebug("IO 信号列表：%d 条（本轮 %d 个信号点位）", idx.len(), plan.signalCount)
		case isFatal(err):
			return fmt.Errorf("abb: 读取 IO 信号列表失败: %w", err)
		default:
			// 服务级：本轮该类作废，信号点位退化为单点 GET。
			d.noteListFailure(listKeySignals, err)
			logDebug("IO 信号列表不可用，退化为单点读取：%v", err)
		}
	}

	// ---- 逐点位取值 ----
	for i := range plan.slots {
		slot := plan.slots[i]
		if slot.addr == nil {
			// 解析失败：Quality 保持 0，且不产生任何请求（见 parseAddress 的
			// 说明 —— 一个手滑写错的地址不该每轮都换回一次必然 404 的往返）。
			//
			// 但**必须说一声**：这个点位在界面上只是恒为 Quality=0，除此之外
			// 没有任何线索；buildPlan 存下的 err 是"为什么"的唯一出处，也是
			// 这里唯一该打日志的地方。去重依靠 warnOnce —— 这个条件每轮都成立。
			//
			// 成因归地址级：地址写法本地就解析不出来，改点位表之前永远如此。
			results[i].Quality = driver.QualityBadAddress
			d.warnOnce("parse:"+addrs[i].Name,
				"地址 %q 解析失败，该点位不会被采集：%v", addrs[i].Name, slot.err)
			continue
		}
		a := slot.addr

		// 已确认不存在的点位：本轮不再请求（负缓存，见 hint）。
		if h, ok := d.hintFor(addrs[i].Name); ok {
			switch h.kind {
			case hintMissing:
				// 负缓存本身就说明"这个点位不存在"，成因与地址级同义。
				// 这里必须写：hints 跨轮存活，而分类代码只在首次确认那一轮跑过 ——
				// 不写的话成因只在 1/N 的轮次出现，按"连续 N 轮"判定的报警永远不触发。
				results[i].Quality = driver.QualityBadAddress
				continue
			case hintOverride:
				continue // 列表不覆盖它，直接交给单点阶段
			}
		}

		switch a.ns {
		case nsSignal:
			if sigIdx == nil {
				continue // 列表不可用/关了批量 → 交给单点回退
			}
			st, ok := sigIdx.lookup(a.signalPath())
			if !ok {
				continue // 列表成功但不含该点位 → 交给单点回退
			}
			d.fill(&results[i], a, st, addrs[i].Name)
			done[i] = true

		case nsRapid:
			// 永远走单点：搜索端点只返回描述符、不含值。见 Read 的说明。
			continue

		default:
			// panel / motion / system：每个资源一条端点，同一资源的多点位
			// （如六个关节轴）共用一次请求，由 roundCache 兜住。
			states, err := d.statesForResource(ctx, client, cfg, a, rc)
			done[i] = true // 已尝试：回退阶段再来一次只会撞上同样的结果
			switch {
			case err == nil:
				d.fill(&results[i], a, states[0], addrs[i].Name)
			case isFatal(err):
				return fmt.Errorf("abb: 读取 %s 失败: %w", a.ns, err)
			default:
				results[i].Quality = abbQualityOf(err)
				logDebug("读取 %s 资源失败（%q）：%v", a.ns, addrs[i].Name, err)
			}
		}
	}
	return nil
}

// allSignalsOverridden 判断本轮的全部信号点位是否都已确认"不在列表里"。
//
// 是的话，拉全量列表就是纯浪费 —— 那些点位本来就只能靠单点 GET 取值
// （控制器按权限过滤、或该设备不被 /rw/iosystem/signals 枚举）。
// 这正是 hintOverride 存在的意义：没有这个判断，它就只是省了一次
// map 查找，不值一提。
//
// 提示有 300s 的 TTL，所以新加的信号最迟 5 分钟后就会被重新纳入列表。
func (d *abbDriver) allSignalsOverridden(addrs []po.DeviceAddress, plan *readPlan) bool {
	any := false
	for i := range plan.slots {
		if plan.slots[i].addr == nil || plan.slots[i].addr.ns != nsSignal {
			continue
		}
		h, ok := d.hintFor(addrs[i].Name)
		if !ok || h.kind != hintOverride {
			return false
		}
		any = true
	}
	return any
}

// readFallback 单点回退阶段：批量没覆盖到的点位逐个 GET。
//
// 双重预算（条数 + 时间）不是可选项：网关给插件的 Read 超时是 60s，
// 2000 个僵尸点位在 8 req/s 下要 250s 才发得完 —— 网关会先超时、判未连接、
// 重连、重新探测，表现为设备在"已连接/未连接"之间反复跳。
//
// 预算不够时的分配方式是**轮转**，不是"每轮都从头取前 N 个"：后者在需要
// 回退的点位多于配额时，排在后面的点位永远轮不到 —— 界面上是一批恒为
// Quality=0 的点位，而日志说"本轮跳过 N 个"，听起来像下轮会补上。RAPID
// 点位只能走这条路（见 Read 的说明），配几十个以上就会撞上。
// 游标存在计划里（fallbackNext），所以停下的位置是**跨轮**的。
func (d *abbDriver) readFallback(
	ctx context.Context, client *rwsClient, cfg *Config,
	addrs []po.DeviceAddress, plan *readPlan,
	results []driver.ReadResult, done []bool, rc *roundCache,
) error {
	n := len(plan.slots)
	if n == 0 {
		return nil
	}

	deadline := time.Now().Add(defaultFallbackBudget)
	attempts, skipped := 0, 0
	stopped := -1 // 本轮第一个因预算而没轮上的下标（下一轮从这里继续）

	start := int(plan.fallbackNext.Load()) % n
	for k := 0; k < n; k++ {
		i := (start + k) % n
		if done[i] {
			continue
		}
		done[i] = true
		slot := plan.slots[i]
		if slot.addr == nil {
			continue
		}

		// 已确认不存在的点位在静默期内不再请求。
		// 这个检查必须在预算判断**之前**：被负缓存挡下的点位不该消耗回退配额，
		// 否则一批僵尸点位会把配额占光，真正需要回退的点位反而取不到值。
		if h, ok := d.hintFor(addrs[i].Name); ok && h.kind == hintMissing {
			results[i].Quality = driver.QualityBadAddress
			continue
		}

		if attempts >= defaultMaxFallbackPerRound || time.Now().After(deadline) {
			skipped++
			if stopped < 0 {
				stopped = i
			}
			continue
		}
		attempts++

		a := slot.addr
		states, err := d.statesForResource(ctx, client, cfg, a, rc)
		if err != nil {
			if isFatal(err) {
				return fmt.Errorf("abb: 单点读取 %q 失败: %w", addrs[i].Name, err)
			}
			// 只有 4xx（点位确实不存在）才值得负缓存；5xx 是暂时的，
			// 缓存它会让一次控制器抖动变成一分钟的采不到值。
			var se *rwsError
			if errors.As(err, &se) && se.status >= 400 && se.status < 500 {
				d.noteMiss(addrs[i].Name)
			}
			results[i].Quality = abbQualityOf(err)
			logDebug("单点读取 %q 失败：%v", addrs[i].Name, err)
			continue
		}
		d.noteOverride(addrs[i].Name)
		d.fill(&results[i], a, states[0], addrs[i].Name)
	}

	// 下一轮从本轮停下的位置继续；整轮走完则回到起点（此时全部点位都已处理过，
	// 起点在哪都不影响公平性，取 0 只是为了让行为可预期、可测）。
	if stopped >= 0 {
		plan.fallbackNext.Store(uint32(stopped))
	} else {
		plan.fallbackNext.Store(0)
	}

	if skipped > 0 {
		// 不静默截断：被预算挡下的点位在界面上只是 Quality=0，看不出原因。
		// 去重是必须的 —— 配额不够是**稳态**，不是偶发，不去重就是每轮一行。
		// 措辞也不能再暗示"这些点位有问题"：它们只是这一轮没轮上。
		d.warnOnce("fallback-budget",
			"单点回退配额已用尽：本轮尝试 %d 个、跳过 %d 个（上限 %d 个 / %s）；"+
				"跳过的点位会在后续轮次轮转补齐，如需更快可在点位表里拆成多个采集分组",
			attempts, skipped, defaultMaxFallbackPerRound, defaultFallbackBudget)
	}
	return nil
}

// statesForResource 取该点位所属资源的全部状态条目（同一资源在本轮内只请求一次）。
func (d *abbDriver) statesForResource(
	ctx context.Context, client *rwsClient, cfg *Config, a *parsedAddr, rc *roundCache,
) ([]map[string]any, error) {
	path, err := resourcePath(a, cfg)
	if err != nil {
		// 本地解析问题：不再发请求，直接当作服务级失败。
		return nil, &rwsError{status: 0, msg: err.Error()}
	}
	return rc.fetch(ctx, client, path)
}

// fill 把一条状态条目写进结果：取值 → 定 Kind → 按 Kind 归一化 → 定质量。
func (d *abbDriver) fill(r *driver.ReadResult, a *parsedAddr, st map[string]any, name string) {
	raw, ok := valueOf(st, a)
	if !ok {
		// 键不存在与"值是空串/false"是两回事（见 fieldOf 的说明），
		// 走到这里说明真的没有这个字段，多半是地址里的字段名不对。
		//
		// 去重不是可选的：这个条件在每个点位、每一轮都成立，而写错字段名的
		// 点位往往是一批（复制粘贴出来的一列）。不设间隔的话，1s 周期下
		// 几十个这样的点位就是每秒几十行日志，且行行相同。
		//
		// 归地址级：资源请求成功了（拿到了 st），是地址里的字段名在响应里
		// 不存在 —— 与"地址解析失败"同一性质，都是改点位表才能好。
		r.Quality = driver.QualityBadAddress
		d.warnOnce("field:"+name, "地址 %q：响应里没有字段 %q，请检查地址写法", name, a.field)
		return
	}

	kind := resolveKind(a, r.DataType, declaredTypeOf(st, a))
	r.Value = formatValue(raw, kind)
	r.Kind = kind
	r.Quality = driver.QualityGood
	if a.ns == nsSignal && isSimulated(st) {
		// 仿真信号的值来自控制器内部模拟，不代表真实过程 IO ——
		// 标成"不确定"让下游自己决定采信与否，而不是当正常值存进去。
		r.Quality = driver.QualityUncertain
	}
}

// planFor 返回批次的读取计划（内容指纹缓存）。
// 计划除轮转游标外只读，并发 Read 复用同一缓存安全。
func (d *abbDriver) planFor(addrs []po.DeviceAddress) *readPlan {
	sig := driver.RangeSig(addrs)

	d.plans.mu.Lock()
	if p, ok := d.plans.plans[sig]; ok {
		d.plans.mu.Unlock()
		return p
	}
	d.plans.mu.Unlock()

	p := buildPlan(addrs)

	d.plans.mu.Lock()
	// plans 为 nil 是常态：Connect 会把缓存清成 nil（配置可能已变化）。
	if d.plans.plans == nil {
		d.plans.plans = make(map[uint64]*readPlan)
	}
	// 满了只淘汰**一条**，绝不整表清空。清空的后果不是"少缓存一点"，而是
	// 一满就退化成"每个批次每轮都重新解析地址"—— 批次集合大于上限时
	// （单设备 33+ 个批次），缓存命中率从 100% 直接掉到 0，且没有任何日志，
	// 只表现为 CPU 悄悄升高。随机淘汰对这个规模的缓存足够：被淘汰的那条
	// 下次重算一遍，代价与"解析一趟地址"同量级。
	if len(d.plans.plans) >= driver.RangeCacheMaxEntries {
		for k := range d.plans.plans {
			delete(d.plans.plans, k)
			break
		}
	}
	d.plans.plans[sig] = p
	d.plans.mu.Unlock()
	return p
}

// buildPlan 解析地址并汇总本轮需要哪些资源请求。
func buildPlan(addrs []po.DeviceAddress) *readPlan {
	p := &readPlan{slots: make([]planSlot, len(addrs))}
	for i := range addrs {
		// **必须 strings.Clone。** addrs[i].Name 引用的是 gateway 那帧 RPC 载荷的
		// 缓冲（pluginwire 的 zeroCopyStrings=true，driversdk/binary.go 里
		// a.Name = d.String() 直接切片），而本计划会跨 Read 调用存活在缓存里 ——
		// 不 clone 就等于把整帧几百 KB 钉住不放，不报错、只涨内存。
		// 一次 clone 覆盖全部字段：解析出的子串都指向这个新分配的副本。
		name := strings.Clone(addrs[i].Name)

		a, err := parseAddress(name)
		if err != nil {
			p.slots[i] = planSlot{err: err}
			continue
		}
		p.slots[i] = planSlot{addr: a}
		if a.ns == nsSignal {
			p.needsSignals = true
			p.signalCount++
		}
	}
	return p
}

func (d *abbDriver) IsConnected() bool {
	d.mu.RLock()
	c := d.client
	d.mu.RUnlock()
	return c != nil && c.IsConnected() && !c.IsClosed()
}

// Close 关闭连接释放资源。幂等、不阻塞。
func (d *abbDriver) Close() error {
	d.mu.Lock()
	c := d.client
	d.client = nil
	d.cfg = nil
	d.mu.Unlock()

	if c != nil {
		c.Close()
	}
	return nil
}

// ==================== 读取计划与缓存 ====================

// planSlot 与入参 addrs 一一对应的解析结果。
//
// addr 为 nil 表示解析失败，此时 err 是失败原因。err 由 readBulk 取出来打
// 日志（经 warnOnce 去重）—— 它必须被读到：解析失败的点位既不请求也不报错，
// 是界面上唯一表现为"恒为 Quality=0、毫无线索"的一类。
type planSlot struct {
	addr *parsedAddr
	err  error
}

// readPlan 一批点位的读取计划。
//
// 除 fallbackNext 外全部只读：计划被并发 Read 共享（见 planFor），
// 任何写字段都会打破那个前提。
type readPlan struct {
	slots []planSlot

	needsSignals bool // 本轮是否要拉 IO 信号全量列表
	signalCount  int  // 仅用于日志

	// fallbackNext 单点回退的轮转游标：下一轮从哪个下标开始尝试。
	// 放在计划里而不是驱动上，是因为"该轮到谁"天然是**批次**的属性，
	// 而计划的存活期与批次一一对应（配置变化时随缓存一起清空）。
	// 用原子：并发 Read 共享同一份计划。
	fallbackNext atomic.Uint32
}

// planCache 计划缓存（内容指纹 → 计划）。
type planCache struct {
	mu    sync.Mutex
	plans map[uint64]*readPlan
}

func (c *planCache) clear() {
	c.mu.Lock()
	c.plans = nil
	c.mu.Unlock()
}

// roundCache 单轮内的资源请求缓存：同一个资源路径在本轮只请求一次。
//
// **不并发保护**：每个 Read 调用建一个，只在该调用内使用。
// （驱动实例本身是并发共享的，但这份缓存不是。）
type roundCache struct {
	states map[string][]map[string]any
	errs   map[string]error
}

func newRoundCache() *roundCache {
	return &roundCache{
		states: make(map[string][]map[string]any),
		errs:   make(map[string]error),
	}
}

func (rc *roundCache) fetch(ctx context.Context, c *rwsClient, path string) ([]map[string]any, error) {
	if st, ok := rc.states[path]; ok {
		return st, nil
	}
	if err, ok := rc.errs[path]; ok {
		return nil, err
	}
	st, err := c.getStates(ctx, path)
	if err != nil {
		rc.errs[path] = err
		return nil, err
	}
	rc.states[path] = st
	return st, nil
}

// ==================== 回退提示（负缓存 / 覆盖缓存） ====================

// hintKind 回退提示的两种语义。
type hintKind uint8

const (
	// hintMissing 单点 GET 返回 4xx：点位确实不存在，静默期内不再请求。
	// 没有它，一个已删除的信号会在每一轮里都产生一次必然 404 的往返。
	hintMissing hintKind = iota
	// hintOverride 列表不覆盖该点位、但单点 GET 成功（例如列表被控制器
	// 分页或按权限过滤了）。此后跳过索引查找直接单点，省掉每轮一次白查。
	hintOverride
)

type hint struct {
	kind  hintKind
	until time.Time
}

func (d *abbDriver) hintFor(name string) (hint, bool) {
	d.hintMu.Lock()
	defer d.hintMu.Unlock()
	h, ok := d.hints[name]
	if !ok || time.Now().After(h.until) {
		return hint{}, false
	}
	return h, true
}

func (d *abbDriver) noteMiss(name string) {
	d.putHint(name, hintMissing, defaultNegativeCacheTTL)
}

func (d *abbDriver) noteOverride(name string) {
	d.putHint(name, hintOverride, defaultOverrideCacheTTL)
}

func (d *abbDriver) putHint(name string, kind hintKind, ttl time.Duration) {
	d.hintMu.Lock()
	defer d.hintMu.Unlock()
	if d.hints == nil {
		d.hints = make(map[string]hint)
	}
	if len(d.hints) >= fallbackHintMaxEntries {
		d.hints = make(map[string]hint) // 有界：超限清空重建
	}
	// strings.Clone：name 来自 RPC 载荷缓冲，这张表会跨 Read 存活。
	d.hints[strings.Clone(name)] = hint{kind: kind, until: time.Now().Add(ttl)}
}

func (d *abbDriver) clearHints() {
	d.hintMu.Lock()
	d.hints = nil
	d.hintMu.Unlock()
}

// ==================== 告警去重 ====================

// warnOnce 对同一个键，在 defaultFieldWarnTTL 内只打一次日志。
//
// 用在"每轮、每点位都重新成立"的失败条件上（地址解析失败、响应缺字段）：
// 它们不设间隔地打日志会把网关日志刷屏，而重复的行不携带任何新信息。
// 静默期过后再打一次是有意的 —— 漏看了第一行的人还有机会看到。
func (d *abbDriver) warnOnce(key, format string, args ...any) {
	if !d.shouldWarn(key) {
		return
	}
	logf(format, args...)
}

// shouldWarn 报告该键距上次告警是否已超过静默期，并记录本次。
func (d *abbDriver) shouldWarn(key string) bool {
	now := time.Now()

	d.warnMu.Lock()
	defer d.warnMu.Unlock()
	if t, ok := d.warned[key]; ok && now.Sub(t) < defaultFieldWarnTTL {
		return false
	}
	if d.warned == nil || len(d.warned) >= fallbackHintMaxEntries {
		d.warned = make(map[string]time.Time) // 有界：超限清空重建
	}
	// strings.Clone：键里含着来自 RPC 载荷缓冲的点位名，本表会跨 Read 存活
	// （理由与 hints 完全相同，见 buildPlan）。
	d.warned[strings.Clone(key)] = now
	return true
}

func (d *abbDriver) clearWarns() {
	d.warnMu.Lock()
	d.warned = nil
	d.warnMu.Unlock()
}

// ==================== 列表请求的粘性禁用 ====================

// disabledList 一类列表请求的连续失败计数。
type disabledList struct {
	fails int
	until time.Time
}

// listDisabled 判断该类列表是否处于粘性禁用期。
// 到期自动解除，并允许再探一次 —— 固件升级或配置修正后不必重启网关。
func (d *abbDriver) listDisabled(key string) bool {
	d.disableMu.Lock()
	defer d.disableMu.Unlock()
	st := d.disables[key]
	// until 为零表示"还没到粘性禁用的阈值"，只是计数器在涨 ——
	// 这种情况**不能**删条目，否则连续失败计数会在每轮开头被清零，
	// 阈值永远达不到，粘性禁用形同虚设。
	if st == nil || st.until.IsZero() {
		return false
	}
	if time.Now().Before(st.until) {
		return true
	}
	// 到期：解除并允许重探一次 —— 固件升级或配置修正后不必重启网关。
	delete(d.disables, key)
	return false
}

// noteListFailure 记录一次列表请求的服务级失败，连续多次后粘性禁用。
//
// 404/501 这类"这个固件根本没有该资源"的失败每轮重试只是白烧请求配额，
// 而控制器的配额是全现场共享的（示教器、RobotStudio 都在用）。
func (d *abbDriver) noteListFailure(key string, err error) {
	d.disableMu.Lock()
	defer d.disableMu.Unlock()
	if d.disables == nil {
		d.disables = make(map[string]*disabledList)
	}
	st := d.disables[key]
	if st == nil {
		st = &disabledList{}
		d.disables[key] = st
	}
	st.fails++
	if st.fails >= stickyDisableThreshold && st.until.IsZero() {
		st.until = time.Now().Add(stickyDisableTTL)
		logf("连续 %d 次取不到 %s 列表，暂停该类请求 %s：%v",
			st.fails, key, stickyDisableTTL, err)
	}
}

func (d *abbDriver) noteListSuccess(key string) {
	d.disableMu.Lock()
	delete(d.disables, key)
	d.disableMu.Unlock()
}

func (d *abbDriver) clearDisables() {
	d.disableMu.Lock()
	d.disables = nil
	d.disableMu.Unlock()
}

// ==================== 错误分类 ====================

// abbQualityOf 把一次资源请求的服务级失败映射为坏点位的质量子码。
//
// 判据是**控制器有没有能力在将来给出不同的答复**：
//   - status==0：resourcePath 本地解析失败（请求根本没发出去）→ 配置错，地址级；
//   - 4xx：控制器明确说"这个东西我这儿没有"（404/405 等）→ 配置错，地址级；
//   - 5xx：控制器自己出错了 → 瞬时，通信级；
//   - 其余（errNotJSON 等）：无法判断，留空串 —— 不猜。
//
// 4xx 的边界与 noteMiss 的负缓存判据**刻意用同一个区间**：能被负缓存的失败
// 与被判成 QualityBadAddress 的失败必须是同一批，否则会出现"质量码说地址错、
// 但驱动下轮还在重试"或反过来的自相矛盾。
func abbQualityOf(err error) int {
	var se *rwsError
	if !errors.As(err, &se) {
		return driver.QualityBad // 非服务级错误已由 isFatal 拦下，走不到这里
	}
	switch {
	case se.status == 0:
		return driver.QualityBadAddress
	case se.status >= 400 && se.status < 500:
		return driver.QualityBadAddress
	case se.status >= 500:
		return driver.QualityBadComms
	}
	return driver.QualityBad
}

// isFatal 判断错误是否应当**中止整轮并置为未连接**。
//
// 鉴权失败与传输级错误都属于这一类：前者说明整台设备都用不了（该在界面上
// 显示为异常，而不是静默一片 Quality=0），后者说明链路断了（该让采集引擎
// 退避重连）。服务级错误则相反 —— 一个点位的 404 说明不了控制器失联。
func isFatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errAuthFailed) {
		return true
	}
	return !isServiceError(err)
}

// connectBudget 计算 Connect/Ping 的总预算。
func connectBudget(cfg *Config) time.Duration {
	budget := probeTimeoutCap*maxProbeCandidates + cfg.Timeout*2
	if budget > connectBudgetCap {
		return connectBudgetCap
	}
	return budget
}
