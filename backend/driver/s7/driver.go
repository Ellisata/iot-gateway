package s7

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

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

	// rangeCache 区间内容指纹缓存：规避每轮重复解析地址与计算区间。
	// 区间计算是 (addrs, cfg) 的纯函数，轮询间批次内容不变即可命中；
	// 驱动实例在配置热刷新时由采集引擎重建，Connect 时也显式清空，缓存随之失效。
	rangeCacheMu sync.Mutex
	rangeCache   map[uint64][]S7Range
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
	// 新连接必须使用重新计算的区间
	d.rangeCacheMu.Lock()
	d.rangeCache = nil
	d.rangeCacheMu.Unlock()

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

	logger.Info("s7 ping success: %s (rack=%d, slot=%d, connectType=%d)",
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

	ranges, err := d.rangesFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		return nil, fmt.Errorf("s7: calc address ranges failed: %w", err)
	}

	// 逐区间读取。
	// 网络错误 → 标记断开 + 返回 error（触发采集引擎重连）；
	// 协议错误（如越界）→ 仅该区间点位 Quality=0，其它区间照常读取。
	type rangeResult struct {
		r      S7Range
		data   []byte
		failed bool
	}
	rr := make([]rangeResult, len(ranges))
	for i := range ranges {
		r := &ranges[i]
		buf := make([]byte, r.EndOffset-r.StartOffset)
		if err := client.readRange(*r, buf); err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) {
				client.MarkDisconnected()
				logger.Error("s7: network read range [%d, %d) failed: %v",
					r.StartOffset, r.EndOffset, err)
				return nil, fmt.Errorf("s7: network read failed: %w", err)
			}
			logger.Warn("s7: read range [%d, %d) failed: %v", r.StartOffset, r.EndOffset, err)
			rr[i] = rangeResult{r: *r, failed: true}
			continue
		}
		rr[i] = rangeResult{r: *r, data: buf}
	}

	// 结果按原始 addrs 顺序回填，缺失点位 Quality=0
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

	for i := range rr {
		rc := rr[i]
		if rc.failed {
			continue // 区间读取失败，点位保持 Quality=0
		}
		for id, saddr := range rc.r.AddressMap {
			pos, ok := index[id]
			if !ok {
				continue
			}
			// 计算在读取缓冲区中的字节偏移
			localOffset := saddr.ByteOffset - rc.r.StartOffset
			if localOffset < 0 || localOffset+saddr.Span > len(rc.data) {
				logger.Warn("s7: address %q (id=%s) out of read range [%d, %d)",
					addrs[pos].Name, id, rc.r.StartOffset, rc.r.EndOffset)
				continue
			}

			raw := rc.data[localOffset : localOffset+saddr.Span]
			val, err := ParseS7Value(raw, saddr, addrs[pos].DataType)
			quality := 192
			value := ""
			if err != nil {
				logger.Warn("s7: parse address %q failed: %v", addrs[pos].Name, err)
				quality = 0
			} else {
				value = FormatS7Value(val, addrs[pos].DataType)
			}

			results[pos] = driver.ReadResult{
				DeviceAddressID: id,
				Value:           value,
				DataType:        addrs[pos].DataType,
				Kind:            typeKind(addrs[pos].DataType),
				Quality:         quality,
			}
		}
	}

	return results, nil
}

// rangesFor 返回批次的读取区间：命中内容指纹缓存直接返回，未命中则计算并缓存。
// 区间计算结果只读共享（读取路径不修改 S7Range），并发 Read 复用同一缓存安全。
// 缓存大小有界：超上限时清空重建，正确性不受影响（仅失去命中）。
func (d *s7Driver) rangesFor(sig uint64, addrs []po.DeviceAddress, cfg *S7Config) ([]S7Range, error) {
	d.rangeCacheMu.Lock()
	if d.rangeCache == nil {
		d.rangeCache = make(map[uint64][]S7Range)
	}
	cached, ok := d.rangeCache[sig]
	d.rangeCacheMu.Unlock()
	if ok {
		return cached, nil
	}

	ranges, err := CalcS7Ranges(addrs, cfg.StringLen, cfg.MaxGap)
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
		d.rangeCache = make(map[uint64][]S7Range)
	}
	d.rangeCache[sig] = ranges
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
