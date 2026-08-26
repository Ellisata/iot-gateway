package cip

import (
	"fmt"
	"sync"

	"iot-gateway/driver"
	cipcore "iot-gateway/driver/cip"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

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

	return nil
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
		logger.Info("cip ping success: host=%s (reused connection)", cfg.Host)
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
	logger.Info("cip ping success: host=%s", cfg.Host)
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

	tags, err := CalcCIPTags(addrs)
	if err != nil {
		return nil, fmt.Errorf("cip: calc tag groups failed: %w", err)
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
	batch := cfg.MaxTagsPerRequest

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
			return fmt.Errorf("cip: batch read %d tags failed: %w", len(pending), err)
		}
		for i := range pending {
			t := &pending[i]
			if results[i].Err != nil {
				logger.Warn("cip: tag %q unreadable, mark %d points quality=0: %v",
					t.Name, len(t.Addrs), results[i].Err)
				markTagZero(addrByID, t, byID)
				continue
			}
			parseTagResults(t, results[i].Data, cfg, byID)
		}
		pending = pending[:0]
		return nil
	}

	for i := range tags {
		t := &tags[i]
		// 用组内第一个点位的类型码发请求，各点位按自己的类型解码
		code, ok := cipTypeCode(t.Addrs[0].DataType)
		if !ok {
			logger.Warn("cip: tag %q group has unsupported data type %q, mark %d points quality=0",
				t.Name, t.Addrs[0].DataType, len(t.Addrs))
			markTagZero(addrByID, t, byID)
			continue
		}

		// 动态长度类型（string）或未开启批量 → 冲刷待批后单读
		if batch <= 1 || !cipFixedSizeType(t.Addrs[0].DataType) {
			if err := flush(); err != nil {
				return nil, err
			}
			data, err := client.ReadTag(t.Name, code)
			if err != nil {
				if cipcore.IsGeneralStatusError(err) {
					logger.Warn("cip: tag %q unreadable, mark %d points quality=0: %v",
						t.Name, len(t.Addrs), err)
					markTagZero(addrByID, t, byID)
					continue
				}
				return nil, fmt.Errorf("cip: read tag %q failed: %w", t.Name, err)
			}
			parseTagResults(t, data, cfg, byID)
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
		quality := 192
		value := ""
		if err != nil {
			logger.Warn("cip: parse address %q failed: %v", a.Name, err)
			quality = 0
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

// markTagZero 为标签组内点位生成 Quality=0 的空结果（保持原始顺序），
// 用于标签读取失败（通用状态错误）时标记异常点位而不中断整台设备。
func markTagZero(addrByID map[string]*po.DeviceAddress, t *CIPTag, byID map[string]driver.ReadResult) {
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
