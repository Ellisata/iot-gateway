package fins

import (
	"fmt"
	"sync"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

func init() {
	driver.Register(ProtocolFINSUDP, func() driver.Driver {
		return newFINSDriver(TransportUDP)
	})
	driver.Register(ProtocolFINSTCP, func() driver.Driver {
		return newFINSDriver(TransportTCP)
	})
	driver.Register(ProtocolFINSSerial, func() driver.Driver {
		return newFINSDriver(TransportSerial)
	})
	driver.Register(ProtocolFINSHostLinkTCP, func() driver.Driver {
		return newFINSDriver(TransportHostLinkTCP)
	})
}

// newFINSDriver 创建绑定指定传输层的 FINS 驱动实例。
// 四种协议（Omron.Net.FINS.UDP / TCP / Serial / HostLinkTCP）共享同一 FINS 应用层
// （内存区读取命令、地址解析、区间计算、数据解码），仅底层传输不同。
func newFINSDriver(transport string) *finsDriver {
	return &finsDriver{transport: transport}
}

// finsDriver 欧姆龙 FINS 协议驱动，实现 driver.Driver 接口。
//
// 传输层由协议注册名固定（transport 字段），不随 protocol_json 的 transport 切换：
//   - UDP：FINS/UDP（端口 9600）
//   - TCP：FINS/TCP（含连接握手，供 PLC 自带以太网口）
//   - Serial：Host Link（FINS over serial，本地串口）
//   - HostLinkTCP：Host Link over TCP（经串口服务器采集远端 PLC 串口）
type finsDriver struct {
	mu        sync.RWMutex
	transport string // 传输层：TransportUDP / TransportTCP / TransportSerial（注册名固定）
	config    *FINSConfig
	client    finsTransport
}

// SerialExclusive 串口传输独占串行总线，需整轮持锁（重连+全部批次读取+转换+推送）；
// 以太网（UDP/TCP/HostLinkTCP）内部已线程安全，仅对「重连 + 单次 Read」加锁。
//
// 传输层由协议注册名固定，这里直接按 transport 判定；
// 各传输客户端自身对单次请求+响应持互斥锁，帧不会在字节级交错。
func (d *finsDriver) SerialExclusive() bool {
	return d.transport == TransportSerial
}

func (d *finsDriver) Connect(protocolJSON string) error {
	cfg, err := ParseFINSConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("fins: parse config failed: %w", err)
	}
	// 传输层由协议注册名固定，覆盖 JSON 中的 transport 字段
	cfg.Transport = d.transport

	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("fins: connect failed: %w", err)
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

func (d *finsDriver) Ping(protocolJSON string) error {
	cfg, err := ParseFINSConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("fins: ping parse config failed: %w", err)
	}
	// 传输层由协议注册名固定，覆盖 JSON 中的 transport 字段
	cfg.Transport = d.transport

	// 若当前驱动实例已持有同参数的连接（如采集引擎运行中已 Connect），
	// 直接复用现有连接做轻量读，避免对同一串口再开一个句柄——
	// Windows 下串口被独占时二次打开会报 Access is denied。
	d.mu.RLock()
	live := d.client
	d.mu.RUnlock()
	if live != nil && live.IsConnected() && sameConfig(d.config, cfg) {
		if err := pingRead(live, cfg); err != nil {
			return fmt.Errorf("fins: ping failed (reused connection): %w", err)
		}
		logger.Info("fins ping success: transport=%s (reused connection)", cfg.Transport)
		return nil
	}

	// 无状态握手：建立临时连接做轻量读后立即关闭，不修改驱动内部状态
	client, err := newTransport(cfg)
	if err != nil {
		return fmt.Errorf("fins: ping connect failed: %w", err)
	}
	defer client.Close()

	if err := pingRead(client, cfg); err != nil {
		return fmt.Errorf("fins: ping failed: %w", err)
	}
	logger.Info("fins ping success: transport=%s", cfg.Transport)
	return nil
}

// pingRead 轻量连通性验证：读取 DM 字 0。
// 结束码错误（设备响应但拒绝，如 DM0 越界）同样证明设备可达，视为成功；
// 仅网络/超时错误判定不可达。
func pingRead(t finsTransport, _ *FINSConfig) error {
	_, err := t.Read(AreaDM, 0, 1)
	if err != nil && !IsEndCodeError(err) {
		return err
	}
	return nil
}

func (d *finsDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("fins: not connected")
	}

	ranges, err := CalcFINSRanges(addrs, cfg)
	if err != nil {
		return nil, fmt.Errorf("fins: calc address ranges failed: %w", err)
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
		data, err := client.Read(r.Area, r.StartWord, r.Count)
		if err != nil {
			if IsEndCodeError(err) {
				logger.Warn("fins: range area=0x%02X [%d, %d) unreadable, mark %d points quality=0: %v",
					r.Area, r.StartWord, r.StartWord+r.Count, len(r.AddressMap), err)
				markRangeZero(addrByID, r, byID)
				continue
			}
			return nil, fmt.Errorf("fins: read range area=0x%02X [%d, %d) failed: %w",
				r.Area, r.StartWord, r.StartWord+r.Count, err)
		}
		parseRangeResults(addrByID, r, data, cfg, byID)
	}

	// 按原始 addrs 顺序重组，保证返回结果与 addrs 一一对应
	results := make([]driver.ReadResult, 0, len(addrs))
	for _, a := range addrs {
		results = append(results, byID[a.ID])
	}
	return results, nil
}

// parseRangeResults 将区间读取数据按 AddressMap 解码回填到 byID。
// addrByID 为 id → addr 的预构建索引。
func parseRangeResults(addrByID map[string]*po.DeviceAddress, r *FINSRange, data []byte, cfg *FINSConfig, byID map[string]driver.ReadResult) {
	for id, faddr := range r.AddressMap {
		a, ok := addrByID[id]
		if !ok || a == nil {
			continue
		}

		// 计算在读取缓冲区中的字节偏移（每字 2 字节）
		words := finsTypeWords(a.DataType, cfg.StringLen)
		offset := (int(faddr.Word) - int(r.StartWord)) * 2
		bytesLen := int(words) * 2
		if offset < 0 || offset+bytesLen > len(data) {
			logger.Warn("fins: address %q (id=%s) out of read range [%d, %d)",
				a.Name, id, r.StartWord, r.StartWord+r.Count)
			continue
		}

		raw := data[offset : offset+bytesLen]
		val, err := ParseFINSValue(raw, faddr, a.DataType, cfg)
		quality := 192
		value := ""
		if err != nil {
			logger.Warn("fins: parse address %q failed: %v", a.Name, err)
			quality = 0
		} else {
			value = FormatFINSValue(val, a.DataType)
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

// markRangeZero 为区间内点位生成 Quality=0 的空结果（保持原始顺序），
// 用于区间读取失败（结束码错误）时标记异常点位而不中断整台设备。
func markRangeZero(addrByID map[string]*po.DeviceAddress, r *FINSRange, byID map[string]driver.ReadResult) {
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

func (d *finsDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *finsDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}
