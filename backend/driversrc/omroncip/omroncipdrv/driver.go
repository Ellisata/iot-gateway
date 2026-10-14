// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"fmt"
	"sync"
	"time"

	"iot-gateway/driver"
	cipcore "iot-gateway/driversrc/omroncip/cipcore"
	"iot-gateway/model/po"
)

// batchHintTTL 「批量读在本设备上不可用」的静默期。
//
// 到期后会自动重试一次批量：现场把 PLC 的配置/固件修好、或换了一台吃 0x0A 的设备
// 之后，不必重启网关就能恢复批量读（重试若再失败，重新进入静默期）。
//
// 为什么不干脆永久关掉：一次失败的批量要多赔一整轮逐标签回退，点位多时那是几百次
// 网络往返；5 分钟一次的探测成本可以忽略，而"永远不恢复"在现场是不可接受的 ——
// 谁也说不清当初那次拒绝是设备真的不支持，还是一次瞬态。
//
// 5 分钟与其它插件的降级计划 TTL 取同一个量级（degradedPlanTTL）。
const batchHintTTL = 5 * time.Minute

func init() {
	driver.Register(ProtocolCIP, func() driver.Driver {
		return newCIPDriver()
	})
}

// newCIPDriver 创建 CIP 驱动实例。
func newCIPDriver() *cipDriver {
	return &cipDriver{}
}

// cipDriver 欧姆龙 EtherNet/IP (CIP) 协议驱动，实现 driver.Driver 接口。
//
// 通过 EtherNet/IP 显式报文（UCMM）以标签寻址读取 Omron NJ/NX、CJ/CS 系列 PLC
// 变量。TCP 长连接，内部线程安全（cipClient 单次请求+响应持锁），
// 故不实现 SerialExclusive，collector 视为非独占驱动。
type cipDriver struct {
	mu     sync.RWMutex
	config *CIPConfig
	client cipTransport

	// batchBadUntil 「批量读在本设备上不可用」的静默期截止时刻（零值 = 批量可用）。
	// 单独一把锁：Read 在路上只持 mu 的读锁，而这里要在 Read 进行一次之后写入。
	batchMu       sync.Mutex
	batchBadUntil time.Time
}

func (d *cipDriver) Connect(protocolJSON string) error {
	cfg, err := ParseCIPConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("cip: parse config failed: %w", err)
	}

	client, err := newCIPClient(cfg)
	if err != nil {
		return fmt.Errorf("cip: connect failed: %w", err)
	}

	d.mu.Lock()
	if d.client != nil {
		d.client.Close()
	}
	d.config = cfg
	d.client = client
	d.mu.Unlock()

	// 新连接 = 设备侧可能已经变了（换了台设备、改了配置），静默期作废重试批量。
	d.clearBatchHint()

	return nil
}

// batchUnusable 返回批量读是否仍处于"被本设备证伪"的静默期内。
func (d *cipDriver) batchUnusable() bool {
	d.batchMu.Lock()
	defer d.batchMu.Unlock()
	return !d.batchBadUntil.IsZero() && time.Now().Before(d.batchBadUntil)
}

// markBatchUnusable 记下「本设备的批量读不可用」，静默期内直接走逐标签单读。
//
// 日志不另做去重（其它插件用 warnSet 是因为条件每轮都成立）：这里写上之后
// 静默期内根本不会再发批量，所以下一次打日志必然是在 TTL 到期重试又失败之后 ——
// 每设备 5 分钟一条。
func (d *cipDriver) markBatchUnusable(err error) {
	logf("批量读被设备拒绝，%s 内改为逐标签单读（到期自动重试一次）：%v", batchHintTTL, err)
	d.batchMu.Lock()
	d.batchBadUntil = time.Now().Add(batchHintTTL)
	d.batchMu.Unlock()
}

// clearBatchHint 作废静默期。
func (d *cipDriver) clearBatchHint() {
	d.batchMu.Lock()
	d.batchBadUntil = time.Time{}
	d.batchMu.Unlock()
}

func (d *cipDriver) Ping(protocolJSON string) error {
	cfg, err := ParseCIPConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("cip: ping parse config failed: %w", err)
	}

	// 若当前驱动实例已持有同参数的连接（如采集引擎运行中已 Connect），
	// 直接复用现有连接做轻量读，避免重复建连。
	d.mu.RLock()
	live := d.client
	d.mu.RUnlock()
	if live != nil && live.IsConnected() && sameConfig(d.config, cfg) {
		if err := pingReadTag(live, cfg); err != nil {
			return fmt.Errorf("cip: ping failed (reused connection): %w", err)
		}
		logf("ping success: host=%s (reused connection)", cfg.Host)
		return nil
	}

	// 无状态握手：建立临时连接后立即关闭，不修改驱动内部状态
	client, err := newCIPClient(cfg)
	if err != nil {
		return fmt.Errorf("cip: ping connect failed: %w", err)
	}
	defer client.Close()

	if err := pingReadTag(client, cfg); err != nil {
		return fmt.Errorf("cip: ping failed: %w", err)
	}
	logf("ping success: host=%s", cfg.Host)
	return nil
}

// pingReadTag 轻量连通性验证。
// pingTag 非空时以 WORD 类型码读取该标签；通用状态错误（设备响应但拒绝，
// 如标签类型不符）同样证明设备可达，视为成功；仅网络/超时错误判定不可达。
// pingTag 为空则仅完成 Register Session（建连成功）即视为可达。
func pingReadTag(t cipTransport, cfg *CIPConfig) error {
	if cfg.PingTag == "" {
		return nil
	}
	_, err := t.ReadTag(cfg.PingTag, cipTypeWORD)
	if err != nil && !cipcore.IsGeneralStatusError(err) {
		return err
	}
	return nil
}

func (d *cipDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("cip: not connected")
	}

	// 标签名本地就解析不出来的点位不进标签分组（它们一个字节都不该发出去），
	// 而是随本轮单独标 QualityBadAddress —— 见 partitionBadTags。
	valid, bad := partitionBadTags(addrs)

	var tags []CIPTag
	if len(valid) > 0 {
		// 空输入直接跳过 CalcCIPTags —— 它对 len(addrs)==0 是报错，
		// 而「这批点位一个都解析不出来」不该是一次失败。
		var err error
		tags, err = CalcCIPTags(valid)
		if err != nil {
			return nil, fmt.Errorf("cip: calc tag groups failed: %w", err)
		}
	}

	// 预构建 id → addr 索引，解码时 O(1) 取回 DataType/Name，避免海量点位下线性扫描
	addrByID := make(map[string]*po.DeviceAddress, len(addrs))
	for i := range addrs {
		addrByID[addrs[i].ID] = &addrs[i]
	}

	// 逐标签读取。通用状态错误（设备响应但拒绝）→ 该标签点位 Quality=0，
	// 继续读其它标签，单个异常标签不拖垮整台设备；
	// 网络/超时错误 → 向上返回，由采集引擎判定断连并重连。
	byID := make(map[string]driver.ReadResult, len(addrs))
	// 先播种被摘出的点位：它们不在任何标签组里，末尾「按 addrs 顺序重组」取不到
	// 它们 —— 不补的话那条重组会产出一条**零值记录**（连 DeviceAddressID 都为空），
	// 违反「addrs 里每个点位恰好有一条记录」，而下游 MQTT 负载与时序库表结构正靠
	// DataType 决定怎么写。
	for i := range addrs {
		a := &addrs[i]
		q, ok := bad[a.ID]
		if !ok {
			continue
		}
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         q,
		}
	}
	batch := cfg.MaxTagsPerRequest
	// 批量已被本设备证伪（见 markBatchUnusable）：静默期内直接逐标签单读，
	// 不再每轮先赔上一次注定失败的批量往返。
	if batch > 1 && d.batchUnusable() {
		batch = 1
	}

	// 批量读：定长类型标签按 maxTagsPerRequest 合入 0x0A 多服务报文（1 次往返读 batch 个标签）；
	// string 等动态长度类型与 batch<=1 时保持单读。批量是 CIP 点位容量扩展的关键：
	// 帧数从「1 点/往返」降到「batch 点/往返」。见 config.MaxTagsPerRequest 真机核实说明。
	var pending []CIPTag
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		specs := make([]TagSpec, len(pending))
		for i := range pending {
			code, _ := cipTypeCode(pending[i].Addrs[0].DataType)
			specs[i] = TagSpec{Name: pending[i].Name, Code: code}
		}
		results, err := client.ReadTags(specs)
		if err != nil {
			// 整批请求未被设备接受。两种形态，处置相同：
			//   ① 应答不成其为多服务应答（设备不认 0x0A）—— 见 errBatchUnusable；
			//   ② 设备以通用状态错误整体拒绝本批（如 0x08 服务不支持）。
			// 设备是**通了话**的，绝不该据此判它离线（此前正是这么干的：整台设备被
			// 反复判离线重连，日志里只有一句 "read device failed"）。正确做法是当场
			// 退回逐标签单读把值救回来，并记下"这台设备不吃批量"。
			//
			// ② 今天已无实际来路（单标签调用经 singleTagResult 落进逐标签 Err，
			// 多标签的解析失败带上 ① 的哨兵），保留它是防"批量请求被整体拒绝"这个
			// 语义将来换了别的实现形态。多一条 || 的代价，换的是不重演上一次事故。
			if isBatchUnusable(err) || cipcore.IsGeneralStatusError(err) {
				d.markBatchUnusable(err)
				batch = 1 // 本轮余下标签直接单读，不再重试批量
				ferr := readIndividually(client, cfg, pending, addrByID, byID)
				pending = pending[:0]
				return ferr
			}
			return fmt.Errorf("cip: batch read %d tags failed: %w", len(pending), err)
		}
		for i := range pending {
			t := &pending[i]
			if results[i].Err != nil {
				logf("tag %q unreadable, mark %d points quality=0: %v",
					t.Name, len(t.Addrs), results[i].Err)
				markTagZero(addrByID, t, byID, qualityOfCIPErr(results[i].Err))
				continue
			}
			parseTagResults(t, results[i].Data, cfg, byID)
		}
		pending = pending[:0]
		return nil
	}

	for i := range tags {
		t := &tags[i]
		// 先按组内第一个点位的类型码校验（请求本身的类型码由 flush/readIndividually
		// 各自取），各点位最终仍按自己的类型解码回填
		if _, ok := cipTypeCode(t.Addrs[0].DataType); !ok {
			logf("tag %q group has unsupported data type %q, mark %d points quality=0",
				t.Name, t.Addrs[0].DataType, len(t.Addrs))
			// 本地配置问题：配置的类型码这台网关根本不支持，请求都没发出去。
			// 与地址写错同一类 —— 不去改配置就永远不会好，故归为地址级。
			markTagZero(addrByID, t, byID, driver.QualityBadAddress)
			continue
		}

		// 动态长度类型（string）或未开启批量 → 冲刷待批后单读
		if batch <= 1 || !cipFixedSizeType(t.Addrs[0].DataType) {
			if err := flush(); err != nil {
				return nil, err
			}
			if err := readIndividually(client, cfg, []CIPTag{*t}, addrByID, byID); err != nil {
				return nil, err
			}
			continue
		}

		pending = append(pending, *t)
		if len(pending) >= batch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// parseTagResults 将标签读取数据按组内点位各自的类型解码回填到 byID。
func parseTagResults(t *CIPTag, data []byte, cfg *CIPConfig, byID map[string]driver.ReadResult) {
	for _, a := range t.Addrs {
		val, err := ParseCIPValue(data, a.DataType, cfg)
		quality := driver.QualityGood
		value := ""
		if err != nil {
			// 设备答了、字节也收到了，是本地解码失败 —— 地址与链路都没问题。
			logf("parse address %q failed: %v", a.Name, err)
			quality = driver.QualityBadParse
		} else {
			value = FormatCIPValue(val, a.DataType)
		}

		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           value,
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         quality,
		}
	}
}

// readIndividually 逐标签单读（0x4C Data Table Read）并回填 byID。
//
// 两条路径共用同一份语义：常规单读（string 等动态长度类型、batch<=1），
// 以及批量被设备拒绝后的**当场回退**。合并成一处是有意的 —— 回退路径若自己
// 复制一遍逐标签循环，两份实现迟早会分叉成"同样的失败在批量回退下和单读下
// 表现不同"，而这类分叉在界面上只表现为"偶尔有一批点位没值"。
//
// 语义：设备拒绝某个标签（通用状态错误）只让该标签的点位 Quality=0，其余标签
// 照读；只有链路错误才上抛（由采集引擎判设备离线）。
//
// 这条"拒绝只落在标签上"的规矩有来历：真机 2026-09-29 现场（10.62.100.16，NX1P2）
// 上一个名为 Recipe.Setpoint 的点回通用状态 0x05。若把设备拒绝升级成函数级错误，
// 采集引擎会把整台设备判离线并反复重连 —— 而 PLC 一直是通的。
func readIndividually(client cipTransport, cfg *CIPConfig, tags []CIPTag,
	addrByID map[string]*po.DeviceAddress, byID map[string]driver.ReadResult) error {
	for i := range tags {
		t := &tags[i]
		// 请求体只带路径与元素个数，类型码不编入报文（见 buildDataTableRead），
		// 这里取它只为与批量路径保持同一处取值逻辑。
		code, _ := cipTypeCode(t.Addrs[0].DataType)
		data, err := client.ReadTag(t.Name, code)
		if err != nil {
			if cipcore.IsGeneralStatusError(err) {
				logf("tag %q unreadable, mark %d points quality=0: %v",
					t.Name, len(t.Addrs), err)
				markTagZero(addrByID, t, byID, qualityOfCIPErr(err))
				continue
			}
			return fmt.Errorf("cip: read tag %q failed: %w", t.Name, err)
		}
		parseTagResults(t, data, cfg, byID)
	}
	return nil
}

// markTagZero 为标签组内点位生成坏点位的空结果（保持原始顺序），
// 用于标签读取失败（通用状态错误）时标记异常点位而不中断整台设备。
// quality 为质量码（driver.QualityXxx 的坏子码），必须由调用方按**失败的那一支**
// 判定后传入 —— 它不是 err 的纯函数（同一个通用状态码在"设备拒绝"与
// "本地不支持该类型"两条路径下含义不同）。
func markTagZero(addrByID map[string]*po.DeviceAddress, t *CIPTag, byID map[string]driver.ReadResult, quality int) {
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

// qualityOfCIPErr 把 CIP 通用状态错误映射为质量码。
//
// 只有"这个路径/标签我这儿没有"那三个状态码归为地址级（见
// cipcore.IsAddressStatusError）：它们是配置错，设备状态怎么恢复都不会变。
// 其余通用状态（资源不可用、部分传输、连接丢失…）都是设备**可恢复**的拒绝，
// 下一轮原样重试就可能拿到值 —— 归为通信级。
func qualityOfCIPErr(err error) int {
	if cipcore.IsAddressStatusError(err) {
		return driver.QualityBadAddress
	}
	return driver.QualityBadComms
}

func (d *cipDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *cipDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}
