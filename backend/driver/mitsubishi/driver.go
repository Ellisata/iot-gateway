// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"fmt"
	"strings"
	"sync"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

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
	rangeCacheMu sync.Mutex
	rangeCache   map[uint64][]MCRange
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
	// 新连接必须使用重新计算的区间
	d.rangeCacheMu.Lock()
	d.rangeCache = nil
	d.rangeCacheMu.Unlock()

	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("mc: connect failed: %w", err)
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

func (d *mcDriver) Ping(protocolJSON string) error {
	cfg, err := ParseMCConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("mc: ping parse config failed: %w", err)
	}
	cfg.Transport = d.transport

	// 若当前驱动实例已持有同参数的连接（如采集引擎运行中已 Connect），
	// 直接复用现有连接做轻量读，避免对同一串口再开一个句柄——
	// Windows 下串口被独占时二次打开会报 Access is denied。
	d.mu.RLock()
	live := d.client
	d.mu.RUnlock()
	if live != nil && live.IsConnected() && sameConfig(d.config, cfg) {
		if err := pingRead(live, cfg); err != nil {
			return fmt.Errorf("mc: ping failed (reused connection): %w", err)
		}
		logger.Info("mc ping success: transport=%s (reused connection)", cfg.Transport)
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
	logger.Info("mc ping success: transport=%s", cfg.Transport)
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

	ranges, err := d.rangesFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		return nil, fmt.Errorf("mc: calc address ranges failed: %w", err)
	}

	// 预构建 id → addr 索引，解码时 O(1) 取回 DataType/Name，避免海量点位下线性扫描
	addrByID := make(map[string]*po.DeviceAddress, len(addrs))
	for i := range addrs {
		addrByID[addrs[i].ID] = &addrs[i]
	}

	// 逐区间读取。结束码错误（设备响应但拒绝）→ 该区间点位 Quality=0，
	// 继续读其它区间，单个异常区间不拖垮整台设备；
	// 网络/超时错误 → 向上返回，由采集引擎判定断连并重连。
	byID := make(map[string]driver.ReadResult, len(addrs))
	for i := range ranges {
		r := &ranges[i]
		data, err := client.Read(r.Device, r.Start, r.Points, r.BitMode)
		if err != nil {
			if IsEndCodeError(err) {
				logger.Warn("mc: range device=%s bit=%v [%d, %d) unreadable, mark %d points quality=0: %v",
					r.Device.name, r.BitMode, r.Start, r.Start+uint32(r.Points), len(r.AddressMap), err)
				markRangeZero(addrByID, r, byID)
				continue
			}
			return nil, fmt.Errorf("mc: read range device=%s bit=%v [%d, %d) failed: %w",
				r.Device.name, r.BitMode, r.Start, r.Start+uint32(r.Points), err)
		}
		parseRangeResults(addrByID, r, data, cfg, byID)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应；
	// 缺失点位（区间读取失败）保持预填充的 Quality=0 空结果
	results := make([]driver.ReadResult, len(addrs))
	index := make(map[string]int, len(addrs))
	for i, a := range addrs {
		index[a.ID] = i
		results[i] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         0,
		}
	}
	for id, r := range byID {
		if pos, ok := index[id]; ok {
			results[pos] = r
		}
	}
	return results, nil
}

// rangesFor 返回点位的读取区间：命中内容指纹缓存直接返回，未命中则计算并缓存。
// 区间计算结果只读共享（解码路径不修改 MCRange），并发 Read 复用同一缓存安全。
// 缓存大小有界：超上限时清空重建，正确性不受影响（仅失去命中）。
func (d *mcDriver) rangesFor(sig uint64, addrs []po.DeviceAddress, cfg *MCConfig) ([]MCRange, error) {
	d.rangeCacheMu.Lock()
	if d.rangeCache == nil {
		d.rangeCache = make(map[uint64][]MCRange)
	}
	cached, ok := d.rangeCache[sig]
	d.rangeCacheMu.Unlock()
	if ok {
		return cached, nil
	}

	ranges, err := CalcMCRanges(addrs, cfg)
	if err != nil {
		return nil, err
	}

	d.rangeCacheMu.Lock()
	// 再次检查，避免并发首轮重复计算同批区间
	if cached, ok := d.rangeCache[sig]; ok {
		d.rangeCacheMu.Unlock()
		return cached, nil
	}
	if len(d.rangeCache) >= driver.RangeCacheMaxEntries {
		d.rangeCache = make(map[uint64][]MCRange)
	}
	d.rangeCache[sig] = ranges
	d.rangeCacheMu.Unlock()
	return ranges, nil
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
				logger.Warn("mc: address %q (id=%s) out of read range", a.Name, id)
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
				logger.Warn("mc: address %q (id=%s) out of read range [%d, %d)",
					a.Name, id, r.Start, r.Start+uint32(r.Points))
				continue
			}
			raw = data[offset : offset+bytesLen]
		}

		val, err := ParseMCValue(raw, maddr, a.DataType)
		quality := 192
		value := ""
		if err != nil {
			logger.Warn("mc: parse address %q failed: %v", a.Name, err)
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
	}
}

// markRangeZero 为区间内点位生成 Quality=0 的空结果，用于区间读取失败（结束码错误）时
// 标记异常点位而不中断整台设备。
func markRangeZero(addrByID map[string]*po.DeviceAddress, r *MCRange, byID map[string]driver.ReadResult) {
	for id := range r.AddressMap {
		if a, ok := addrByID[id]; ok && a != nil {
			byID[id] = driver.ReadResult{
				DeviceAddressID: id,
				Value:           "",
				DataType:        a.DataType,
				Kind:            typeKind(a.DataType),
				Quality:         0,
			}
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
		a.ComPort == b.ComPort &&
		a.BaudRate == b.BaudRate && a.DataBits == b.DataBits &&
		a.StopBits == b.StopBits && strings.EqualFold(a.Parity, b.Parity)
}
