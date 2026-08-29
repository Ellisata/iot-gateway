package opcua

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/gopcua/opcua/ua"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

func init() {
	driver.Register("OPC.UA", func() driver.Driver {
		return newOpcUaDriver()
	})
}

// opcuaDriver OPC UA 协议驱动，实现 driver.Driver 接口。
//
// 连接为长连接；读取遇传输级错误自动标记断开，由采集引擎在下一轮重连。
// 底层连接线程安全（gopcua SecureChannel 按 requestID 串行化收发并路由响应），
// 未实现 SerialExclusive，采集引擎仅对「重连 + 单次 Read」加锁。
type opcuaDriver struct {
	mu     sync.RWMutex
	config *OpcUaConfig
	client *OpcUaClient

	// nodeCache 浏览路径解析结果缓存：地址字符串 → NodeID。
	// 浏览路径解析需服务器 TranslateBrowsePathsToNodeIds 服务（每地址一次），
	// 轮询间命中可避免每轮重复解析；驱动实例在配置热刷新时由采集引擎重建，
	// Connect 时也显式清空（配置可能已变化）。
	nodeCacheMu sync.Mutex
	nodeCache   map[string]*ua.NodeID

	// newClient 客户端工厂，测试可注入替身；默认 newRealClient。
	newClient func(ctx context.Context, cfg *OpcUaConfig) (*OpcUaClient, error)
}

func newOpcUaDriver() *opcuaDriver {
	return &opcuaDriver{newClient: newRealClient}
}

func (d *opcuaDriver) Connect(protocolJSON string) error {
	cfg, err := ParseOpcUaConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("opcua: parse config failed: %w", err)
	}

	// 任何重连尝试都使浏览路径缓存失效：配置可能已变化（endpoint/安全参数/...），
	// 新连接必须使用重新解析的路径
	d.nodeCacheMu.Lock()
	d.nodeCache = nil
	d.nodeCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	client, err := d.newClient(ctx, cfg)
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

func (d *opcuaDriver) Ping(protocolJSON string) error {
	cfg, err := ParseOpcUaConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("opcua: ping parse config failed: %w", err)
	}

	// 无状态握手：建立临时连接（TCP + OpenSecureChannel + CreateSession +
	// ActivateSession，含认证校验）后立即关闭，不修改驱动内部状态。
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	client, err := d.newClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("opcua: ping %s failed (policy=%s, mode=%s, auth=%s): %w",
			cfg.Endpoint, cfg.SecurityPolicy, cfg.SecurityMode, cfg.AuthMode, err)
	}
	client.Close()

	logger.Info("opcua ping success: %s (policy=%s, mode=%s, auth=%s)",
		cfg.Endpoint, cfg.SecurityPolicy, cfg.SecurityMode, cfg.AuthMode)
	return nil
}

func (d *opcuaDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("opcua: not connected")
	}

	// 解析全部地址 → NodeID。结果按原始 addrs 顺序回填；缺失点位 Quality=0。
	// 错误分类（对齐 Read 的服务级/传输级拆分）：
	//   - 本地语法错误（*parseError）→ 该点位 Quality=0 继续；
	//   - 服务级（ua.StatusCode，如浏览路径无匹配）→ 该点位 Quality=0 继续；
	//   - 传输级（网络/上下文）→ 断开标记 + 中止（浏览路径解析需服务器服务）。
	nodes := make([]*ua.NodeID, len(addrs))
	resolveCtx, resolveCancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer resolveCancel()
	for i, a := range addrs {
		nid, err := d.nodeIDFor(resolveCtx, client, a.Name)
		if err != nil {
			var sc ua.StatusCode
			var pe *parseError
			switch {
			case errors.As(err, &sc):
				logger.Warn("opcua: address %q (id=%s) unresolvable: %v", a.Name, a.ID, err)
			case errors.As(err, &pe):
				logger.Warn("opcua: invalid address %q (id=%s): %v", a.Name, a.ID, err)
			default:
				client.MarkDisconnected()
				return nil, fmt.Errorf("opcua: resolve address %q (id=%s) failed: %w", a.Name, a.ID, err)
			}
			continue
		}
		nodes[i] = nid
	}

	results := make([]driver.ReadResult, len(addrs))
	for i, a := range addrs {
		results[i] = driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         0,
		}
	}

	// 按 MaxBatch 子批读取，每子批一个 ReadRequest。
	// 服务级错误（BadTooManyOperations/BadTimeout/...）→ 该子批点位 Quality=0 继续，
	// 避免单个超限请求拖垮整台设备；传输级错误 → 断开 + 中止。
	batch := cfg.MaxBatch
	if batch <= 0 {
		batch = 100
	}
	for start := 0; start < len(nodes); start += batch {
		end := start + batch
		if end > len(nodes) {
			end = len(nodes)
		}
		slots := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			if nodes[i] != nil {
				slots = append(slots, i)
			}
		}
		if len(slots) == 0 {
			continue
		}

		reqNodes := make([]*ua.NodeID, len(slots))
		for j, pos := range slots {
			reqNodes[j] = nodes[pos]
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
		dvs, err := client.ReadBatch(ctx, reqNodes)
		cancel()
		if err != nil {
			var sc ua.StatusCode
			if errors.As(err, &sc) {
				logger.Warn("opcua: read batch [%d, %d) service error: %v", start, end, err)
				continue // 子批点位保持 Quality=0
			}
			client.MarkDisconnected()
			logger.Error("opcua: network read batch [%d, %d) failed: %v", start, end, err)
			return nil, fmt.Errorf("opcua: network read failed: %w", err)
		}

		// Results 与 reqNodes 按序对应（OPC UA 规范要求）；防御长度不一致
		for j, pos := range slots {
			var dv *ua.DataValue
			if j < len(dvs) {
				dv = dvs[j]
			}
			if dv == nil {
				continue // 保持 Quality=0
			}
			r := &results[pos]
			r.Quality = statusQuality(dv.Status)
			// Bad（Quality=0）时值不可信，置空（对齐 s7/mc 解析失败即 Quality=0 + 空值）；
			// Good/Uncertain 的值照常格式化（Uncertain 值可用，仅标注质量）。
			if r.Quality != 0 && dv.Value != nil {
				val := dv.Value.Value()
				r.Value = formatVariant(val)
				r.Kind = kindFor(addrs[pos].DataType, val)
			}
		}
	}

	return results, nil
}

// nodeIDFor 返回地址字符串对应的 NodeID：直接 NodeID 直接解析；
// 浏览路径经服务器解析并缓存（仅缓存成功结果）。
func (d *opcuaDriver) nodeIDFor(ctx context.Context, client *OpcUaClient, name string) (*ua.NodeID, error) {
	d.nodeCacheMu.Lock()
	if d.nodeCache != nil {
		if nid, ok := d.nodeCache[name]; ok {
			d.nodeCacheMu.Unlock()
			return nid, nil
		}
	}
	d.nodeCacheMu.Unlock()

	addr, err := parseAddress(name)
	if err != nil {
		return nil, err // 本地解析失败（非法 NodeID / 路径语法）
	}

	var nid *ua.NodeID
	if addr.isPath {
		nid, err = resolveBrowsePath(ctx, client.c, addr.path)
		if err != nil {
			return nil, err
		}
		// 仅浏览路径缓存；直接 NodeID 解析无 I/O 开销，无需缓存
		d.nodeCacheMu.Lock()
		if d.nodeCache == nil {
			d.nodeCache = make(map[string]*ua.NodeID)
		}
		d.nodeCache[name] = nid
		d.nodeCacheMu.Unlock()
	} else {
		nid = addr.nodeID
	}
	return nid, nil
}

// statusQuality 将 OPC UA 状态码 severity 映射为网关质量：
// severity 00=Good → 192（正常），01=Uncertain → 128（不确定），10/11=Bad → 0（异常）。
func statusQuality(code ua.StatusCode) int {
	switch code >> 30 {
	case 0:
		return 192
	case 1:
		return 128
	default:
		return 0
	}
}

func (d *opcuaDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *opcuaDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}
