// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

func init() {
	driver.Register(ProtocolRockwellCIP, func() driver.Driver {
		return newRockwellDriver()
	})
}

// newRockwellDriver 创建 Rockwell 驱动实例。
func newRockwellDriver() *rockwellDriver {
	return &rockwellDriver{warned: make(map[string]string)}
}

// rockwellDriver 罗克韦尔（Allen-Bradley Logix）EtherNet/IP (CIP) 协议驱动，
// 实现 driver.Driver 接口。
//
// 通过 EtherNet/IP 显式报文（UCMM）以标签寻址读取 ControlLogix/CompactLogix 系列
// PLC 变量。协议帧层基于 github.com/iceisfun/goindustrial（见 client.go 适配器）。
// TCP 长连接，内部线程安全（适配器单次请求+响应持锁），故不实现 SerialExclusive，
// collector 视为非独占驱动。
//
// 仅支持 Logix 标签寻址；SLC 5/00、MicroLogix 等 PCCC 数据表寻址设备不在范围内。
type rockwellDriver struct {
	mu     sync.RWMutex
	config *RockwellConfig
	client rockwellTransport
	// warned 记录已告警过的标签及其错误描述，标签持续不可读时只在
	// 首次或错误变化时打日志，避免每个采集周期刷屏（与 mu 同锁保护）。
	warned map[string]string
}

func (d *rockwellDriver) Connect(protocolJSON string) error {
	cfg, err := ParseRockwellConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("rockwell: parse config failed: %w", err)
	}

	client, err := newRockwellClient(cfg)
	if err != nil {
		return fmt.Errorf("rockwell: connect failed: %w", err)
	}

	d.mu.Lock()
	if d.client != nil {
		d.client.Close()
	}
	d.config = cfg
	d.client = client
	d.warned = make(map[string]string)
	d.mu.Unlock()

	return nil
}

func (d *rockwellDriver) Ping(protocolJSON string) error {
	cfg, err := ParseRockwellConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("rockwell: ping parse config failed: %w", err)
	}

	// 若当前驱动实例已持有同参数的连接（如采集引擎运行中已 Connect），
	// 直接复用现有连接做轻量读，避免重复建连。
	d.mu.RLock()
	live := d.client
	cfgLive := d.config
	d.mu.RUnlock()
	if live != nil && live.IsConnected() && sameConfig(cfgLive, cfg) {
		if err := pingReadTag(live, cfg); err != nil {
			return fmt.Errorf("rockwell: ping failed (reused connection): %w", err)
		}
		logf("rockwell ping success: host=%s (reused connection)", cfg.Host)
		return nil
	}

	// 无状态握手：建立临时连接后立即关闭，不修改驱动内部状态
	client, err := newRockwellClient(cfg)
	if err != nil {
		return fmt.Errorf("rockwell: ping connect failed: %w", err)
	}
	defer client.Close()

	if err := pingReadTag(client, cfg); err != nil {
		return fmt.Errorf("rockwell: ping failed: %w", err)
	}
	logf("rockwell ping success: host=%s", cfg.Host)
	return nil
}

// pingReadTag 轻量连通性验证。
// pingTag 非空时读取该标签；通用状态错误（设备响应但拒绝，如标签不存在、
// 类型不符）同样证明设备可达，视为成功；仅网络/超时错误判定不可达。
// pingTag 为空则仅完成 Register Session（建连成功）即视为可达。
func pingReadTag(t rockwellTransport, cfg *RockwellConfig) error {
	if cfg.PingTag == "" {
		return nil
	}
	_, err := t.ReadTag(cfg.PingTag)
	if err != nil && !isCIPStatusError(err) {
		return err
	}
	return nil
}

func (d *rockwellDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("rockwell: not connected")
	}

	// 读取计划（标签分组 + 数组区间分块）按点位指纹缓存：地址表任务运行期不变，
	// 命中时跳过每周期的逐点解析与 map 聚合（纯 CPU/GC 开销）。
	plan, err := getPlan(addrs)
	if err != nil {
		return nil, fmt.Errorf("rockwell: calc tag groups failed: %w", err)
	}
	tags := plan.tags

	// 预构建 id → addr 索引，解码时 O(1) 取回 DataType/Name，避免海量点位下线性扫描
	addrByID := make(map[string]*po.DeviceAddress, len(addrs))
	for i := range addrs {
		addrByID[addrs[i].ID] = &addrs[i]
	}

	// 逐标签读取。通用状态错误（设备响应但拒绝）→ 该标签点位 Quality=0，
	// 继续读其它标签，单个异常标签不拖垮整台设备；
	// 网络/超时错误 → 向上返回，由采集引擎判定断连并重连。
	//
	// 结果骨架先按 addrs 预填：数组块的降级会把"确认不存在"的元素摘掉，摘掉之后
	// 它不会再出现在任何一次解析里，只有预填才能保证下游仍收到一条带 DataType/Kind
	// 的记录（空着就是往下游发一条没有类型的记录）。
	byID := make(map[string]driver.ReadResult, len(addrs))
	for i := range addrs {
		a := &addrs[i]
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         driver.QualityBad,
		}
	}
	// 计划期被摘出的点位（标签名本地就解析不出来，见 partitionBadTags）：改判成
	// 地址级并留一行日志。它们不在任何标签组里，上面那圈预填给的是 QualityBad
	// （未归类），而"标签名写错了"是个**永不自愈**的地址级失败 —— 成因必须区分出来，
	// 否则点位质量报警会把它当"认不出来的状态"而一言不发。
	// warnOnce 自带去重且按标签名恢复时清除：这类点位永远不会自己恢复，所以一个驱动
	// 实例内只打一行，改配置后热刷新出来的新实例再打一次。
	for i := range addrs {
		a := &addrs[i]
		if plan.bad[a.ID] == 0 {
			continue
		}
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         driver.QualityBadAddress,
		}
		d.warnOnce(a.Name, "quality=bad-address（标签名本地就解析不出来：只收可打印 ASCII，空格与中文都会拒）")
	}

	// 常规标签组：整组一次 0x4C，各点位按自己的类型解码
	for i := range tags {
		t := &tags[i]

		data, err := client.ReadTag(t.Name)
		if err != nil {
			if isCIPStatusError(err) {
				d.warnOnce(t.Name, fmt.Sprintf("%d points: %s", len(t.Addrs), describeCIPError(err)))
				// 标量 tag 组由单个完整 tag 名成键，general-status 拒绝就是"这个 tag
				// 不存在"（路径段错 / 路径目标未知 / 对象不存在）→ address；其余状态
				// （资源不可用、长度不符…）说明不了地址是否存在 → comms。
				//
				// 修掉一处不对称：此前只有数组块那条路分类，标量 tag 一律不分类。
				q := driver.QualityBadComms
				if isAddressStatusError(err) {
					q = driver.QualityBadAddress
				}
				markTagZero(addrByID, t, byID, q)
				continue
			}
			return nil, tagReadError(t.Name, err)
		}

		// 剥离类型码头，纯数据区交由各点位按自己的类型解码
		payload, err := stripTypeCodeHeader(data)
		if err != nil {
			d.warnOnce(t.Name, fmt.Sprintf("%d points: invalid response: %v", len(t.Addrs), err))
			markTagZero(addrByID, t, byID, driver.QualityBadParse)
			continue
		}
		parseTagResults(t, payload, cfg, byID)
		d.clearWarnLazy(t.Name)
	}

	// 数组区间分块：每块一次 Read Tag Elements 读回连续元素段，
	// 各点位按 (下标-offset)*元素大小 切片解码
	budget := newIsolateBudget()
	for _, rng := range plan.arrs {
		t := &rng.tag
		elemSize := plan.sizes[t.Name]

		// 请求路径必须带上本块的起始下标：CIP 的 Read Tag 只带元素个数、没有偏移，
		// 发裸基础名会每次都从元素 0 读，而解码是按 (下标-offset) 切片的 ——
		// 第二块之后的点位会静默读到别的元素的值（见 rangeRequestName 的说明）。
		raw, err := client.ReadTagElements(rangeRequestName(rng), rng.elemCount)
		if err != nil {
			if !isCIPStatusError(err) {
				return nil, tagReadError(t.Name, err)
			}
			if !isAddressStatusError(err) {
				// 非地址类状态（资源不可用 / 部分传输 / 长度不符…）：它说明不了
				// **哪个元素**不存在，所以既不降级也不确认任何点位。整块保持
				// quality=0 并在下一轮重试 —— 成因只能到通信级。
				d.warnOnce(t.Name, fmt.Sprintf("%d points: %s", len(t.Addrs), describeCIPError(err)))
				markTagZero(addrByID, t, byID, driver.QualityBadComms)
				continue
			}
			// 设备确认"这个路径/元素不存在"：一块最多 125 个元素，整块标 0 会把
			// 界内的元素一起丢掉，所以按元素下标对半拆，把真正不存在的摘出来。
			if err := d.degradeChunk(client, rng, elemSize, cfg, addrByID, byID, budget); err != nil {
				return nil, err
			}
			continue
		}
		payload, err := stripTypeCodeHeader(raw)
		if err != nil {
			// 应答本身不可用（类型码头缺失/数据不足）：这是形状问题，不是地址问题，
			// 不降级、也不确认任何点位。整块保持 quality=0 下轮重试，成因是 parse。
			d.warnOnce(t.Name, fmt.Sprintf("%d points: invalid response: %v", len(t.Addrs), err))
			markTagZero(addrByID, t, byID, driver.QualityBadParse)
			continue
		}
		parseArrayTagResults(t, rng, elemSize, payload, cfg, addrByID, byID)
		d.clearWarnLazy(t.Name)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// ==================== 数组分块失败的对半降级 ====================

// 对半降级的预算上限，**单次 Read 共享**。
//
// 两个上限都要，理由与其它协议同一条：请求数上限兜住"设备应答很快但整块拒绝"，
// 时间上限兜住"设备应答很慢"（每个请求都吃满超时）。上限必须远小于网关侧 Read 的
// 60s RPC 超时 —— 连续 3 次超时会把整个插件进程判死。
const (
	degradeMaxRequests = 64
	degradeMaxDuration = 15 * time.Second
)

// isAddressStatusError 这条 CIP 状态是否等于设备在说"这个路径/元素我这儿没有"。
//
// 只认三个明确表示"不存在"的状态码：
//
//	0x04 路径段错误        （标签路径解析不了）
//	0x05 路径目标未知      （路径指向的标签不存在）
//	0x16 对象不存在
//
// 其余一律**不是**地址问题：0x02 资源不可用、0x06 部分传输、0x13/0x15 数据长度不符
// 等都是**可恢复的设备状态**，拿它们去"确认"某个元素不存在，会把整块点位误摘掉 ——
// 那几类照旧整块标 quality=0 并在下一轮重试（与修复前一致，不会更糟）。
//
// 与 isCIPStatusError 的关系：后者是"设备响应但拒绝"（决定要不要判设备掉线），
// 这里是"拒绝的原因是关于地址的"（决定能不能据此确认某个元素不存在）。
// 两个问题，两个判据。
func isAddressStatusError(err error) bool {
	var ce cip.Error
	if !errors.As(err, &ce) {
		return false
	}
	switch ce.Status {
	case cip.StatusPathSegmentError, cip.StatusPathDestinationUnknown, cip.StatusObjectDoesNotExist:
		return true
	default:
		return false
	}
}

// isolateBudget 对半降级的预算，**单次 Read 内共享**。
type isolateBudget struct {
	left     int
	deadline time.Time // 首个降级请求时锚定（零值 = 还没开始）
}

func newIsolateBudget() *isolateBudget {
	return &isolateBudget{left: degradeMaxRequests}
}

// take 申请一次降级请求。false 表示预算已尽（请求数或时间，任一到了就算尽）。
//
// 时间窗口在**首个降级请求**时才开始计时：一个批次的常规读取本身就可能花掉十几秒，
// 从 Read 开头锚定的话预算一出生就过期了。
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

// chunkPoint 数组块内一个元素点位：点位 + 它的绝对元素下标。
type chunkPoint struct {
	addr  *po.DeviceAddress
	index int
}

// degradeChunk 处理一个**已知整块读不通**的数组块：按元素下标对半拆，逐半重读。
//
// **这里刻意不做降级计划缓存**（与 FINS/MC/Modbus/S7 不同）：那四个协议的合并区间
// 可以含上千个点位、且"地址不存在"通常是永久的配置错误，所以值得把摘出来的结果
// 缓存住；而这里的块上限是 125 个元素，摘一个坏元素约 2*log2(125) ≈ 14 次请求，
// 且 Rockwell 的读取计划来自**进程级**的内容指纹缓存（没有 TTL），把一份带 TTL 的
// 每驱动状态混进同一张缓存只会让两者的生命周期互相矛盾。代价是每轮重付这十几次
// 请求，换来的是每轮都自愈（设备侧补上下标之后下一轮就恢复）。
func (d *rockwellDriver) degradeChunk(client rockwellTransport, rng *arrayRange, elemSize int,
	cfg *RockwellConfig, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	budget *isolateBudget) error {

	// 整块已经被拒过一次（调用点那次），所以这里直接拆，不重读这一整块 ——
	// degradePoints 是"试读一段"，它会先读一次；再走它等于白付一次必然失败的往返。
	return d.degradeBadPoints(client, rng.tag.Name, chunkPoints(rng), elemSize, cfg, addrByID, byID, budget)
}

// degradeBadPoints 处理一段**已知读不通**的元素点位（设备回的是地址类状态）。
//
//	只剩单元素 → 确认这个下标在设备上不存在：保持预填的 quality=0（不再请求）
//	还有多个   → 一分为二，两半各自重试
func (d *rockwellDriver) degradeBadPoints(client rockwellTransport, base string, pts []chunkPoint, elemSize int,
	cfg *RockwellConfig, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	budget *isolateBudget) error {

	if len(pts) == 1 {
		a := pts[0].addr
		// 成因是 address 而非 comms：走到这里意味着设备明确回了地址类状态
		//（路径段错 / 目标未知 / 对象不存在）且对半拆到单元素仍被拒 —— 地址本身的问题。
		markQuality(byID, a.ID, driver.QualityBadAddress)
		d.warnOnce(fmt.Sprintf("elem:%s[%d]", base, pts[0].index),
			fmt.Sprintf("element %q (id=%s) unreadable, mark quality=0", a.Name, a.ID))
		return nil
	}
	mid := len(pts) / 2
	if err := d.degradePoints(client, base, pts[:mid], elemSize, cfg, addrByID, byID, budget); err != nil {
		return err
	}
	return d.degradePoints(client, base, pts[mid:], elemSize, cfg, addrByID, byID, budget)
}

// degradePoints 试读一段元素点位。
//
//	读得通              → 解析回填（界内元素照常取值）
//	读不通（地址类）      → 交给 degradeBadPoints 拆
//	预算耗尽 / 非地址状态 → 该段保持预填的 quality=0，本轮不再探测，下轮重来
func (d *rockwellDriver) degradePoints(client rockwellTransport, base string, pts []chunkPoint, elemSize int,
	cfg *RockwellConfig, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult,
	budget *isolateBudget) error {

	if len(pts) == 0 {
		return nil
	}
	sub := subChunk(base, pts)
	if !budget.take() {
		return nil // 预算耗尽：这一段保持预填的 quality=0，下轮重来
	}

	raw, err := client.ReadTagElements(rangeRequestName(sub), sub.elemCount)
	if err != nil {
		if !isCIPStatusError(err) {
			return tagReadError(base, err)
		}
		if !isAddressStatusError(err) {
			// 非地址状态：不确认任何元素，保持未归类下轮重试 —— 只能到通信级。
			markPointsQuality(byID, pts, driver.QualityBadComms)
			return nil
		}
		return d.degradeBadPoints(client, base, pts, elemSize, cfg, addrByID, byID, budget)
	}

	payload, err := stripTypeCodeHeader(raw)
	if err != nil {
		// 应答不可用：不是地址问题，整段保持 0 下轮重试，成因是 parse。
		d.warnOnce(base, fmt.Sprintf("invalid response while isolating %d elements: %v", len(pts), err))
		markPointsQuality(byID, pts, driver.QualityBadParse)
		return nil
	}
	parseArrayTagResults(&sub.tag, sub, elemSize, payload, cfg, addrByID, byID)
	d.clearWarnLazy(base)
	return nil
}

// chunkPoints 取出块内点位并按绝对元素下标排序 —— 对半降级按这个顺序切，
// 两半覆盖的下标范围才大致相当。**不能直接用 tag.Addrs 的顺序**：它是按点位
// 在设备地址表里的出现顺序聚合的，与下标无关。
func chunkPoints(rng *arrayRange) []chunkPoint {
	pts := make([]chunkPoint, 0, len(rng.tag.Addrs))
	for i, a := range rng.tag.Addrs {
		pts = append(pts, chunkPoint{addr: a, index: rng.indices[i]})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].index != pts[j].index {
			return pts[i].index < pts[j].index
		}
		return pts[i].addr.ID < pts[j].addr.ID
	})
	return pts
}

// subChunk 由一段元素点位构造读取区间：下标范围 [min, max] 连续覆盖，
// 请求路径由 rangeRequestName 带上起点下标。
func subChunk(base string, pts []chunkPoint) *arrayRange {
	lo := pts[0].index
	hi := pts[0].index
	for _, p := range pts[1:] {
		if p.index < lo {
			lo = p.index
		}
		if p.index > hi {
			hi = p.index
		}
	}
	r := &arrayRange{
		offset:    lo,
		elemCount: uint16(hi - lo + 1),
		indices:   make([]int, 0, len(pts)),
	}
	r.tag.Name = base
	for _, p := range pts {
		r.tag.Addrs = append(r.tag.Addrs, p.addr)
		r.indices = append(r.indices, p.index)
	}
	return r
}

// tagReadError 把一次标签读取失败包装成上抛给采集侧的错误。
//
// 封装层拒绝（设备答了、但拒绝执行这条封装命令）与网络故障是两件事，虽然都会让
// Read 返回 error：前者说明**设备在线**、问题在协议/设备本身（它没有 CIP 报文
// 路由器，见 cipError.go 的 encapStatus[0x01]），后者才是"设备断联"。把成因写进
// 文案里，运维就不会照着"设备断联"去查网络、重启 PLC —— 这正是本仓库反复挡过的
// 那类误导（见 CLAUDE.md 点位扫描那节对"配置错误被报成设备故障"的检讨）。
//
// **为什么不改判成点位级（quality=comms）让设备保持在线**：点位质量报警引擎刻意
// 把 QualityBadComms 当**中性**（见 alarm/point.go 的说明 —— 它成组出现、按定义是
// 瞬时的，计入失败会让一次区间拒绝同时点亮上百个点位报警）。把这次失败改判成它，
// 结果是既不报警也不报成功 —— 整个静默掉，比现在更糟。要走点位级，得先给报警引擎
// 加一个"设备在线但不支持该命令"的**永久性**成因（与 QualityBadAddress 同类），
// 那是跨驱动的契约改动，是另一个决定。
func tagReadError(tag string, err error) error {
	if _, ok := encapStatusOf(err); ok {
		// 状态码与含义由 encapError 自己带上（见 cipError.go），这里只补结论 ——
		// 重复一遍状态码只会让这一行更难读。
		return fmt.Errorf("rockwell: read tag %q failed: %w —— 设备在线但拒绝执行这条命令，不是网络故障", tag, err)
	}
	return fmt.Errorf("rockwell: read tag %q failed: %w", tag, err)
}

// parseTagResults 将标签读取数据按组内点位各自的类型解码回填到 byID。
func parseTagResults(t *RockwellTag, data []byte, cfg *RockwellConfig, byID map[string]driver.ReadResult) {
	for _, a := range t.Addrs {
		val, err := ParseRockwellValue(data, a.DataType, cfg)
		quality := driver.QualityGood
		value := ""
		if err != nil {
			logf("parse address %q failed: %v", a.Name, err)
			quality = 0
		} else {
			value = FormatRockwellValue(val, a.DataType)
		}

		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           value,
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         quality,
		}
		if quality != driver.QualityGood {
			// 设备已经应答了、本地解不出来 —— 与整块被拒（comms）不同，质量码是 QualityBadParse。
			markQuality(byID, a.ID, driver.QualityBadParse)
		}
	}
}

// parseArrayTagResults 将数组合并读取的响应按组内点位切片解码回填到 byID。
//
// payload 为 stripTypeCodeHeader 之后的纯数据区：elemCount 个连续小端元素，
// 每个元素 elemSize 字节。点位 i 对应元素下标 indices[i]，切片
// payload[(indices[i]-offset)*elemSize : ...+elemSize] 交由其类型解码。
// 越界（响应短于预期，如设备实际数组更小）的点位标记 Quality=0，不中断整组。
func parseArrayTagResults(t *RockwellTag, rng *arrayRange, elemSize int, payload []byte, cfg *RockwellConfig, addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult) {
	for i, a := range t.Addrs {
		off := (rng.indices[i] - rng.offset) * elemSize
		if off < 0 || off+elemSize > len(payload) {
			// 响应数据不足（设备实际数组区间小于请求）：标记异常点位
			logf("array tag %q element %d out of response range (payload %d bytes)",
				t.Name, rng.indices[i], len(payload))
			if addrByID[a.ID] != nil {
				byID[a.ID] = driver.ReadResult{
					DeviceAddressID: a.ID,
					Value:           "",
					DataType:        a.DataType,
					Kind:            typeKind(a.DataType),
					// 响应比请求短：设备应答了但本地切不出这个元素 —— QualityBadParse。
					Quality: driver.QualityBadParse,
				}
			}
			continue
		}

		val, err := ParseRockwellValue(payload[off:off+elemSize], a.DataType, cfg)
		quality := driver.QualityGood
		value := ""
		if err != nil {
			logf("parse address %q failed: %v", a.Name, err)
			quality = 0
		} else {
			value = FormatRockwellValue(val, a.DataType)
		}
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           value,
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         quality,
		}
		if quality != driver.QualityGood {
			markQuality(byID, a.ID, driver.QualityBadParse)
		}
	}
}

// markQuality 给一个点位标上质量码。
//
// 两条守卫，都是为了让这一维只在"确实异常"时出现：
//   - 记录不在 byID 里（不属于本批点位）时跳过；
//   - **Quality==192 的一律不动** —— 守住"正常即无成因"这条不变量，也保证某轮
//     侥幸读通的点位不会带着上一轮的成因。
func markQuality(byID map[string]driver.ReadResult, id string, quality int) {
	r, ok := byID[id]
	if !ok || r.Quality == driver.QualityGood {
		return
	}
	r.Quality = quality
	byID[id] = r
}

// markPointsQuality 给一段元素点位标上质量码（数组块的降级路径用）。
func markPointsQuality(byID map[string]driver.ReadResult, pts []chunkPoint, quality int) {
	for _, p := range pts {
		markQuality(byID, p.addr.ID, quality)
	}
}

// markTagZero 为标签组内点位生成坏点位的空结果（保持原始顺序），
// 用于标签/整块读取失败时标记异常点位而不中断整台设备。
//
// quality 由调用方的分类判定给出，见 driver.QualityXxx 的坏子码：
// 地址类状态 → address（设备明说这个 tag/路径不存在）；其余状态 → comms；
// 应答形状不可用（剥离类型码头失败）→ parse。
func markTagZero(addrByID map[string]*po.DeviceAddress, t *RockwellTag, byID map[string]driver.ReadResult, quality int) {
	for _, a := range t.Addrs {
		if addrByID[a.ID] == nil {
			continue
		}
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         quality,
		}
	}
}

// warnOnce 标签级日志去重：同一标签持续异常时只在首次或错误描述变化时打一行日志，
// 避免逐周期刷屏。并发安全（Read 可能被多个 worker 调用）。
//
// 插件日志无级别（见 log.go），去重比内置时代更重要：少了级别这道运行时闸门，
// 不去重就是每个采集周期把同一条警告重刷一遍。
func (d *rockwellDriver) warnOnce(tag, detail string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.warned == nil {
		d.warned = make(map[string]string)
	}
	if prev, ok := d.warned[tag]; ok && prev == detail {
		return
	}
	d.warned[tag] = detail
	logf("tag %q unreadable, mark %s", tag, detail)
}

// clearWarnLazy 标签读取恢复正常后清除去重记录，使下次异常能重新告警。
// 快路径（map 为空，即无任何异常标签的正常周期）不取写锁——海量点位下
// 每周期每标签都会走到这里，无锁空转即可，避免对全局 mu 的无谓写竞争。
func (d *rockwellDriver) clearWarnLazy(tag string) {
	d.mu.RLock()
	empty := len(d.warned) == 0
	d.mu.RUnlock()
	if empty {
		return
	}
	d.mu.Lock()
	delete(d.warned, tag)
	d.mu.Unlock()
}

func (d *rockwellDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *rockwellDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}

// 编译期保证 cip.Error 实现了 error 接口（isCIPStatusError 依赖其错误链语义）。
var _ error = cip.Error{}
