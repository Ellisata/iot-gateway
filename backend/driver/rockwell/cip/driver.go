package cip

import (
	"fmt"
	"sync"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"

	"iot-gateway/driver"
	"iot-gateway/logger"
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
		logger.Info("rockwell ping success: host=%s (reused connection)", cfg.Host)
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
	logger.Info("rockwell ping success: host=%s", cfg.Host)
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
	byID := make(map[string]driver.ReadResult, len(addrs))

	// 常规标签组：整组一次 0x4C，各点位按自己的类型解码
	for i := range tags {
		t := &tags[i]

		data, err := client.ReadTag(t.Name)
		if err != nil {
			if isCIPStatusError(err) {
				d.warnOnce(t.Name, fmt.Sprintf("%d points: %s", len(t.Addrs), describeCIPError(err)))
				markTagZero(addrByID, t, byID)
				continue
			}
			return nil, fmt.Errorf("rockwell: read tag %q failed: %w", t.Name, err)
		}

		// 剥离类型码头，纯数据区交由各点位按自己的类型解码
		payload, err := stripTypeCodeHeader(data)
		if err != nil {
			d.warnOnce(t.Name, fmt.Sprintf("%d points: invalid response: %v", len(t.Addrs), err))
			markTagZero(addrByID, t, byID)
			continue
		}
		parseTagResults(t, payload, cfg, byID)
		d.clearWarnLazy(t.Name)
	}

	// 数组区间分块：每块一次 Read Tag Elements 读回连续元素段，
	// 各点位按 (下标-offset)*元素大小 切片解码
	for _, rng := range plan.arrs {
		t := &rng.tag

		raw, err := client.ReadTagElements(t.Name, rng.elemCount)
		if err != nil {
			if isCIPStatusError(err) {
				d.warnOnce(t.Name, fmt.Sprintf("%d points: %s", len(t.Addrs), describeCIPError(err)))
				markTagZero(addrByID, t, byID)
				continue
			}
			return nil, fmt.Errorf("rockwell: read tag %q failed: %w", t.Name, err)
		}
		payload, err := stripTypeCodeHeader(raw)
		if err != nil {
			d.warnOnce(t.Name, fmt.Sprintf("%d points: invalid response: %v", len(t.Addrs), err))
			markTagZero(addrByID, t, byID)
			continue
		}
		parseArrayTagResults(t, rng, plan.sizes[t.Name], payload, cfg, addrByID, byID)
		d.clearWarnLazy(t.Name)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// parseTagResults 将标签读取数据按组内点位各自的类型解码回填到 byID。
func parseTagResults(t *RockwellTag, data []byte, cfg *RockwellConfig, byID map[string]driver.ReadResult) {
	for _, a := range t.Addrs {
		val, err := ParseRockwellValue(data, a.DataType, cfg)
		quality := 192
		value := ""
		if err != nil {
			logger.Warn("rockwell: parse address %q failed: %v", a.Name, err)
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
			logger.Warn("rockwell: array tag %q element %d out of response range (payload %d bytes)",
				t.Name, rng.indices[i], len(payload))
			if addrByID[a.ID] != nil {
				byID[a.ID] = driver.ReadResult{
					DeviceAddressID: a.ID,
					Value:           "",
					DataType:        a.DataType,
					Kind:            typeKind(a.DataType),
					Quality:         0,
				}
			}
			continue
		}

		val, err := ParseRockwellValue(payload[off:off+elemSize], a.DataType, cfg)
		quality := 192
		value := ""
		if err != nil {
			logger.Warn("rockwell: parse address %q failed: %v", a.Name, err)
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
	}
}

// markTagZero 为标签组内点位生成 Quality=0 的空结果（保持原始顺序），
// 用于标签读取失败（通用状态错误）时标记异常点位而不中断整台设备。
func markTagZero(addrByID map[string]*po.DeviceAddress, t *RockwellTag, byID map[string]driver.ReadResult) {
	for _, a := range t.Addrs {
		if addrByID[a.ID] == nil {
			continue
		}
		byID[a.ID] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         0,
		}
	}
}

// warnOnce 标签级日志去重：同一标签持续异常时只在首次或错误描述变化时打 Warn，
// 避免逐周期刷屏。并发安全（Read 可能被多个 worker 调用）。
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
	logger.Warn("rockwell: tag %q unreadable, mark %s", tag, detail)
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
