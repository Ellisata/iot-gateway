// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

func init() {
	driver.Register(ProtocolDLT645Serial, func() driver.Driver {
		return newDLT645Driver(TransportSerial)
	})
	driver.Register(ProtocolDLT645TCP, func() driver.Driver {
		return newDLT645Driver(TransportTCP)
	})

	// 启动自检内置数据标识字典，把表内笔误变成一行日志
	verifyDictionary()
}

// newDLT645Driver 创建绑定指定传输层的驱动实例。
// 两个协议（DLT645.Serial / DLT645.TCP）共享同一 645 应用层
// （帧编解码、数据标识解析、数值解码），仅底层 I/O 不同。
func newDLT645Driver(transport string) *dlt645Driver {
	return &dlt645Driver{transport: transport}
}

// dlt645Driver DL/T 645 协议驱动，实现 driver.Driver 接口（只读采集）。
//
// 传输层由协议注册名固定（transport 字段），不随 protocol_json 的 transport 切换。
type dlt645Driver struct {
	mu        sync.RWMutex
	transport string // TransportSerial / TransportTCP（注册名固定）
	config    *DLT645Config
	meterAddr [6]byte // 打包后的表地址域（低字节在前）
	client    dlt645Transport
	plans     planCache
	problems  problemLog // 抑制「每轮采集都报一次」的重复告警（见 problemLog.go）
}

// SerialExclusive 串口独占 RS-485 总线，需整轮持锁（重连 + 全部读取 + 转换 + 推送）；
// 以太网（TCP）内部已用传输层互斥锁保证请求-应答不交错，仅对「重连 + 单次 Read」加锁。
func (d *dlt645Driver) SerialExclusive() bool {
	return d.transport == TransportSerial
}

func (d *dlt645Driver) Connect(protocolJSON string) error {
	cfg, err := ParseDLT645Config(protocolJSON, d.transport)
	if err != nil {
		return fmt.Errorf("dlt645: 解析配置失败: %w", err)
	}
	addr, err := MeterAddressBytes(cfg.MeterAddress)
	if err != nil {
		return fmt.Errorf("dlt645: %w", err)
	}

	// 先关闭旧连接再建立新连接：Windows 下串口被独占时，
	// 不先释放旧句柄会直接报 Access is denied。
	d.mu.Lock()
	old := d.client
	d.client = nil
	d.config = nil
	d.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	// 任何重连都使规划缓存失效：版本 / 数据标识字典等解析输入可能已变化
	d.plans.clear()
	// 问题状态同样作废：重连后大概率是另一块表，旧表的问题不该继续压着新表的告警
	d.problems.clear()

	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("dlt645: 建立连接失败: %w", err)
	}

	d.mu.Lock()
	d.config = cfg
	d.meterAddr = addr
	d.client = client
	d.mu.Unlock()

	logger.Info("dlt645 驱动已连接：transport=%s version=%s meter=%s",
		cfg.Transport, cfg.Version, cfg.MeterAddress)
	return nil
}

func (d *dlt645Driver) Ping(protocolJSON string) error {
	cfg, err := ParseDLT645Config(protocolJSON, d.transport)
	if err != nil {
		return fmt.Errorf("dlt645: ping 解析配置失败: %w", err)
	}
	addr, err := MeterAddressBytes(cfg.MeterAddress)
	if err != nil {
		return fmt.Errorf("dlt645: ping %w", err)
	}

	// 若当前驱动实例已持有同参数的连接（采集引擎运行中已 Connect），
	// 直接复用现有连接做轻量读，避免对同一串口再开一个句柄——
	// Windows 下串口被独占时二次打开会报 Access is denied。
	d.mu.RLock()
	live := d.client
	cur := d.config
	d.mu.RUnlock()
	if live != nil && live.IsConnected() && sameConfig(cur, cfg) {
		if err := pingRead(live, cfg, addr); err != nil {
			return fmt.Errorf("dlt645: ping 失败（复用已有连接）: %w", err)
		}
		logger.Info("dlt645 ping 成功：transport=%s（复用已有连接）", cfg.Transport)
		return nil
	}

	// 无状态握手：建立临时连接做轻量读后立即关闭，不修改驱动内部状态
	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("dlt645: ping 建立连接失败: %w", err)
	}
	defer client.Close()

	if err := pingRead(client, cfg, addr); err != nil {
		return fmt.Errorf("dlt645: ping 失败: %w", err)
	}
	logger.Info("dlt645 ping 成功：transport=%s", cfg.Transport)
	return nil
}

// pingRead 轻量连通性验证：读各版本最普遍实现的数据标识（正向有功总电能）。
// 异常应答（设备响应但拒绝）同样证明设备可达，视为成功；
// 仅 I/O/超时错误判定不可达。
func pingRead(t dlt645Transport, cfg *DLT645Config, addr [6]byte) error {
	const (
		pingDI2007 = 0x00000000 // 组合有功总电能
		pingDI1997 = 0x9010     // 正向有功总电能
	)
	di := uint32(pingDI2007)
	if !cfg.Is2007() {
		di = pingDI1997
	}
	spec := DISpec{DI: di, Bytes: 4, Decimals: 2}

	t.Lock()
	defer t.Unlock()

	_, err := exchange(t, cfg, addr, buildReadFrame(cfg, addr, []DISpec{spec}))
	if err != nil && !isAbnormalErr(err) {
		return err
	}
	return nil
}

func (d *dlt645Driver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client, cfg, addr := d.client, d.config, d.meterAddr
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("dlt645: 未连接")
	}

	plan := d.plans.planFor(driver.RangeSig(addrs), addrs, cfg)

	// 预填结果：按 addrs 顺序 1:1，默认 Quality=0（地址解析失败或读取失败的点位保持此值）
	results := make([]driver.ReadResult, len(addrs))
	for i := range addrs {
		results[i] = driver.ReadResult{
			DeviceAddressID: addrs[i].ID,
			Value:           "",
			DataType:        addrs[i].DataType,
			Kind:            typeKind(addrs[i].DataType),
			Quality:         0,
		}
	}

	// 逐组读取。I/O/超时错误直接上抛，由采集引擎判定断连并重连；
	// 协议层问题（异常应答、解码失败）只把相关点位标记为 Quality=0，不中断整台设备。
	for gi := range plan.groups {
		if err := d.readGroup(client, cfg, addr, plan, &plan.groups[gi], results); err != nil {
			return nil, err
		}
	}

	return results, nil
}

// readGroup 执行一组数据标识的读取并回填结果。
func (d *dlt645Driver) readGroup(t dlt645Transport, cfg *DLT645Config, addr [6]byte,
	plan *readPlan, g *readGroup, results []driver.ReadResult) error {

	specs := make([]DISpec, len(g.idx))
	for i, ei := range g.idx {
		specs[i] = plan.entries[ei].spec
	}

	t.Lock()
	req := buildReadFrame(cfg, addr, specs)
	resp, err := exchange(t, cfg, addr, req)
	t.Unlock()

	if err != nil {
		if !isAbnormalErr(err) {
			return err // I/O / 超时：上抛，交由引擎重连
		}
		// 设备正常应答但拒绝本次请求：连接是通的。
		if len(g.idx) > 1 {
			// 多标识打包时异常应答不回显是哪一个失败的，降级为逐个重读。
			// 降级后每个数据标识各自上报问题，这里只解释「为什么发了一串单读」，
			// 同样要抑制重复——否则批量被拒时的刷屏只是换了个位置。
			d.problems.report(batchKey(specs, cfg.Version),
				fmt.Sprintf("本组 %d 个数据标识批量读取被拒绝（%v），降级为逐个读取",
					len(g.idx), err))
			return d.readGroupPerDI(t, cfg, addr, plan, g, results)
		}
		d.reportRejected(cfg, specs[0], req, err)
		return nil
	}

	if hasFollowUp(cfg, resp) {
		// 应答含后续帧（0xB1 / 0xB2）：首帧数据只是该数据项的一部分。
		// 这里**必须放弃解码**——拿首帧的片段去解码会得到一个看似合理实则被截断的值。
		// 也绝不重启连接：首帧已完整通过校验，说明链路与设备都是通的，
		// 把一次后续帧标志升级成设备离线是远比取不到值更严重的后果。
		diText := formatDI(specs[0].DI, cfg.Version)
		d.problems.report(diText,
			fmt.Sprintf("数据标识 %s（本组 %d 个点位）的应答含后续帧（控制码 %02X），"+
				"当前版本不支持读后续数据拼接，这些点位将保持异常；"+
				"请改用单帧数据标识，或按电表手册拆分该数据项",
				diText, len(g.idx), resp.C))
		return nil
	}

	d.fillResults(cfg, plan, g, specs, resp.Data, results)
	return nil
}

// readGroupPerDI 把一组多个数据标识拆成单标识请求逐个重读（降级路径）。
func (d *dlt645Driver) readGroupPerDI(t dlt645Transport, cfg *DLT645Config, addr [6]byte,
	plan *readPlan, g *readGroup, results []driver.ReadResult) error {

	for i := range g.idx {
		single := readGroup{idx: g.idx[i : i+1]}
		if err := d.readGroup(t, cfg, addr, plan, &single, results); err != nil {
			return err
		}
	}
	return nil
}

// fillResults 按请求顺序把数据域切分并解码回填到 results。
//
// 数据域布局为「数据标识(低字节在前) + 数据」重复，逐个校验应答回显的数据标识
// 与请求一致——不一致说明响应顺序错位或数据标识字节序有误，
// 此时宁可不取值：错位取值会静默地给出另一个数据项的值，比报异常危险得多。
func (d *dlt645Driver) fillResults(cfg *DLT645Config, plan *readPlan, g *readGroup,
	specs []DISpec, data []byte, results []driver.ReadResult) {

	diBytes := cfg.DIBytes()
	pos := 0

	for i, spec := range specs {
		entry := plan.entries[g.idx[i]]
		diText := formatDI(spec.DI, cfg.Version)

		if pos+diBytes > len(data) {
			d.problems.report(diText, fmt.Sprintf(
				"数据域长度不足，无法读取数据标识 %s（点位 id=%d）", diText, entry.index))
			continue
		}
		gotDI := readDI(data[pos : pos+diBytes])
		pos += diBytes
		if gotDI != spec.DI {
			d.problems.report(diText, fmt.Sprintf(
				"应答数据标识 %s 与请求 %s 不符（点位 id=%d），跳过解码",
				formatDI(gotDI, cfg.Version), diText, entry.index))
			continue
		}
		if pos+spec.Bytes > len(data) {
			d.problems.report(diText, fmt.Sprintf(
				"数据标识 %s 的数据被截断（需 %d 字节，剩余 %d 字节）",
				diText, spec.Bytes, len(data)-pos))
			continue
		}
		raw := data[pos : pos+spec.Bytes]
		pos += spec.Bytes

		val, err := DecodeValue(raw, &spec, entry.dataType)
		if err != nil {
			d.problems.report(diText, fmt.Sprintf(
				"点位 %q（数据标识 %s）解码失败 raw=% X: %v",
				spec.Name, diText, raw, err))
			continue
		}

		results[entry.index].Value = FormatValue(val, entry.dataType)
		results[entry.index].Quality = 192
		d.problems.resolve(diText, "数据标识 "+diText)
	}
}

// reportRejected 上报「电表正常应答但拒绝本次读」。
//
// 这类拒绝（尤其错误码 01「其他错误」）本身不含任何可定位信息，
// 因此把请求与应答的原始报文一并带上——现场只能靠它跟电表手册/抓包比对。
// 报文只在问题首次出现（或内容变化）时打印，重复告警由 problemLog 抑制。
func (d *dlt645Driver) reportRejected(cfg *DLT645Config, spec DISpec, req []byte, err error) {
	diText := formatDI(spec.DI, cfg.Version)
	d.problems.report(diText, fmt.Sprintf("数据标识 %s 读取被拒绝，点位保持异常: %v%s",
		diText, err, rawDetail(req, err)))
}

// rawDetail 展开异常应答的原始报文，供告警里直接比对；
// 非异常应答（或没有留底报文）时返回空串。
func rawDetail(req []byte, err error) string {
	var ae *abnormalError
	if !errors.As(err, &ae) || len(ae.Raw) == 0 {
		return ""
	}
	return fmt.Sprintf("；请求 raw=% X；应答 raw=% X", req, ae.Raw)
}

// batchKey 成组问题的抑制键：由本组数据标识拼成，不同组互不干扰，
// 也不会与单点位问题的键（数据标识书写形式）相撞。
func batchKey(specs []DISpec, version string) string {
	var b strings.Builder
	b.WriteString("批量读取")
	for i := range specs {
		b.WriteByte(':')
		b.WriteString(formatDI(specs[i].DI, version))
	}
	return b.String()
}

func (d *dlt645Driver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *dlt645Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		_ = d.client.Close()
		d.client = nil
	}
	d.config = nil
	d.plans.clear()
	d.problems.clear()
	return nil
}

// sameConfig 判断两份配置的关键连接参数是否一致，用于 Ping 复用已有连接。
//
// 表号必须参与比较：连接建立时地址域已固定，表号不同说明测的是另一块表，
// 复用该连接会把请求发到错误的表上。
func sameConfig(a, b *DLT645Config) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Transport == b.Transport &&
		a.Version == b.Version &&
		a.MeterAddress == b.MeterAddress &&
		a.Host == b.Host && a.Port == b.Port &&
		a.ComPort == b.ComPort &&
		a.BaudRate == b.BaudRate && a.DataBits == b.DataBits &&
		a.StopBits == b.StopBits && strings.EqualFold(a.Parity, b.Parity)
}
