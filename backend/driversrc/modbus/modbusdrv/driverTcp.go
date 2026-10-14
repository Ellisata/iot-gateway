// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// NewTCP 创建 Modbus TCP 驱动实例（插件工厂用）。
func NewTCP() driver.Driver {
	return &modbusTCPDriver{}
}

// modbusTCPDriver Modbus TCP 协议驱动，实现 driver.Driver 接口
type modbusTCPDriver struct {
	mu     sync.RWMutex
	config *ModbusTcpConfig
	client *ModbusClient

	// plans 批次区间规划缓存：规避每轮重复解析地址、按功能码分组与计算区间。
	// 规划是 (addrs, cfg) 的纯函数，轮询间批次内容不变即可命中；
	// 驱动实例在配置热刷新时由采集引擎重建，Connect 时也显式清空，缓存随之失效。
	// 对半降级后的区间也在同一张缓存里（见 planCache.degraded）。
	plans planCache

	// warns 日志去重（读路径的坏地址与瞬时状态各一条，每轮都成立）
	warns warnSet
}

func (d *modbusTCPDriver) Connect(protocolJSON string) error {
	cfg, err := ParseModbusTcpConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("modbus: parse config failed: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// 任何重连尝试都使规划缓存失效：配置可能已变化，新连接必须使用重新计算的规划。
	// 降级结果也在同一张缓存里，一并失效 —— 重连后设备侧可能已经补上了那个地址。
	d.plans.clear()
	d.warns.clear()

	client, err := NewModbusTCPClient(cfg.Host, cfg.Port, timeout)
	if err != nil {
		return fmt.Errorf("modbus: connect %s:%d failed: %w", cfg.Host, cfg.Port, err)
	}
	client.SetUnitID(cfg.UnitID)

	d.mu.Lock()
	if d.client != nil {
		d.client.Close()
	}
	d.config = cfg
	d.client = client
	d.mu.Unlock()

	return nil
}

// Ping 建立临时连接做两层校验，不修改驱动内部状态。
// 与其他驱动的 Ping 契约一致：所有临时资源都在本方法内释放
// （driver.PingDevice 不会调用 Close）。
func (d *modbusTCPDriver) Ping(protocolJSON string) error {
	cfg, err := ParseModbusTcpConfig(protocolJSON)
	if err != nil {
		return fmt.Errorf("modbus: ping parse config failed: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// Step 1: TCP 层连通性检查
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("modbus: ping %s failed (tcp): %w", addr, err)
	}
	conn.Close()

	// Step 2: Modbus 协议层握手（建立 handler 连接即完成协议协商）
	handler := goburrowModbus.NewTCPClientHandler(addr)
	handler.Timeout = timeout
	handler.IdleTimeout = timeout * 2
	handler.SlaveId = cfg.UnitID

	if err := handler.Connect(); err != nil {
		return fmt.Errorf("modbus: ping %s failed (protocol): %w", addr, err)
	}
	handler.Close()

	logf("ping success: %s (unit=%d)", addr, cfg.UnitID)
	return nil
}

func (d *modbusTCPDriver) Read(addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	d.mu.RLock()
	client := d.client
	cfg := d.config
	d.mu.RUnlock()

	if client == nil || !client.IsConnected() {
		return nil, fmt.Errorf("modbus: not connected")
	}

	client.SetUnitID(cfg.UnitID)

	// 按功能码分组 + 每组区间计算一次性规划并缓存（内容指纹命中则跳过重复解析）。
	// 线圈/保持寄存器等是不同的地址空间，同一设备混合点位时分组各自读取，而非整组报错。
	sig := driver.RangeSig(addrs)
	plan, err := d.plans.planFor(sig, addrs, cfg)
	if err != nil {
		return nil, fmt.Errorf("modbus: calc address ranges failed: %w", err)
	}

	return readBatch("modbus", client, plan, &d.plans, sig, addrs, cfg, &d.warns)
}

func (d *modbusTCPDriver) IsConnected() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client != nil && d.client.IsConnected()
}

func (d *modbusTCPDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil {
		d.client.Close()
		d.client = nil
	}
	d.config = nil
	return nil
}

// markParseCause 给解析结果里所有仍未被归类的记录标上 QualityBadParse。
//
// 两个 parse* 函数产出的记录只有三种会是 Quality=0：地址不在区间映射里、偏移超出
// 读到的字节数、单点解码失败。三者都是「设备已经应答了、本地解不出来」，与整段被
// 拒（comms）或地址不存在（address）都不同，故成因统一是 parse。
//
// 收在这里做而不是逐个字面量加：那三处散在四个 append 里，加漏一处的症状是
// "某些解析失败的点质量码还是未归类"，不报错、只在数据上少一维。
// 只动仍是 QualityBad 的：已归类的（含 QualityGood）一律不碰。
func markParseCause(records []driver.ReadResult) []driver.ReadResult {
	for i := range records {
		if records[i].Quality == driver.QualityBad {
			records[i].Quality = driver.QualityBadParse
		}
	}
	return records
}

// parseRegisterResults 解析寄存器读取结果（保持寄存器/输入寄存器）
func parseRegisterResults(results []byte, addrs []po.DeviceAddress, cfg *ModbusTcpConfig, ar *AddressRange) []driver.ReadResult {
	records := make([]driver.ReadResult, 0, len(addrs))

	for _, addr := range addrs {
		// 从预解析的地址 map 中直接获取地址，避免重复解析 name
		regAddr, ok := ar.AddressMap[addr.ID]
		if !ok {
			logf("address %q (id=%s) not found in range map, skip", addr.Name, addr.ID)
			records = append(records, driver.ReadResult{
				DeviceAddressID: addr.ID,
				Value:           "",
				DataType:        addr.DataType,
				Kind:            typeKind(addr.DataType),
				Quality:         driver.QualityBad,
			})
			continue
		}

		// 计算在读取缓冲区中的字节偏移量
		offset := int(regAddr-ar.StartAddress) * 2
		bytesLen := getDataTypeBytes(addr.DataType, cfg.StringLen)

		if offset < 0 || offset+bytesLen > len(results) {
			logf("address %q (reg=%d) out of read range [%d, %d) (offset=%d, need=%d, have=%d)",
				addr.Name, regAddr, ar.StartAddress, ar.StartAddress+ar.Quantity,
				offset, bytesLen, len(results))
			records = append(records, driver.ReadResult{
				DeviceAddressID: addr.ID,
				Value:           "",
				DataType:        addr.DataType,
				Kind:            typeKind(addr.DataType),
				Quality:         driver.QualityBad,
			})
			continue
		}

		raw := results[offset : offset+bytesLen]
		parsed, err := ParseReadResult(raw, addr.DataType, cfg.ByteOrder, cfg.WordOrder)
		quality := driver.QualityGood
		value := ""
		if err != nil {
			logf("parse address %q failed: %v", addr.Name, err)
			quality = 0
		} else {
			value = FormatValue(parsed, addr.DataType)
		}

		records = append(records, driver.ReadResult{
			DeviceAddressID: addr.ID,
			Value:           value,
			DataType:        addr.DataType,
			Kind:            typeKind(addr.DataType),
			Quality:         quality,
		})
	}

	return markParseCause(records)
}

// parseBitResults 解析位类型读取结果（线圈/离散输入）
func parseBitResults(results []byte, addrs []po.DeviceAddress, ar *AddressRange) []driver.ReadResult {
	records := make([]driver.ReadResult, 0, len(addrs))

	for _, addr := range addrs {
		// 从预解析的地址 map 中直接获取地址
		regAddr, ok := ar.AddressMap[addr.ID]
		if !ok {
			logf("bit address %q (id=%s) not found in range map, skip", addr.Name, addr.ID)
			records = append(records, driver.ReadResult{
				DeviceAddressID: addr.ID,
				Value:           "",
				DataType:        addr.DataType,
				Kind:            typeKind(addr.DataType),
				Quality:         driver.QualityBad,
			})
			continue
		}

		regOffset := int(regAddr - ar.StartAddress)
		byteIdx := regOffset / 8
		bitIdx := regOffset % 8

		var value string
		quality := driver.QualityGood

		if byteIdx < len(results) {
			if results[byteIdx]&(1<<bitIdx) != 0 {
				value = "1"
			} else {
				value = "0"
			}
		} else {
			quality = 0
		}

		records = append(records, driver.ReadResult{
			DeviceAddressID: addr.ID,
			Value:           value,
			DataType:        addr.DataType,
			Kind:            typeKind(addr.DataType),
			Quality:         quality,
		})
	}

	return markParseCause(records)
}

// filterAddrsByMap 从 addrs 中筛选出 ID 出现在 addrMap 中的点位，保持原顺序不变。
func filterAddrsByMap(addrs []po.DeviceAddress, addrMap map[string]uint16) []po.DeviceAddress {
	result := make([]po.DeviceAddress, 0, len(addrMap))
	for _, a := range addrs {
		if _, ok := addrMap[a.ID]; ok {
			result = append(result, a)
		}
	}
	return result
}

// CalcReadRangeOptions CalcReadRanges 的参数。
type CalcReadRangeOptions struct {
	StartAddress uint16 // 配置模式起始地址；0 = 自动推导模式
	Quantity     uint16 // 配置模式读取数量
	MergeWindow  uint16 // 自动模式下跨段合并的最大窗口跨度；0 = 仅严格连续聚类
	StringLen    int    // string 类型单点读取字节数（默认 16），用于计算多寄存器跨度
}

// CalcReadRanges 从点位列表计算读取范围。
//
// 当 opts.StartAddress > 0 时使用配置值（向后兼容模式），所有 name 必须可解析且
// 地址 ≥ StartAddress，返回单个范围的切片，忽略 MergeWindow。
//
// 否则自动从所有 name 推导地址，分两阶段聚类：
//  1. 严格连续聚类：连续地址合并为一个 AddressRange，不连续的地址分到不同段，
//     稠密块始终保持完整（不会因窗口合并被拆小）。
//  2. 跨段合并：当 opts.MergeWindow > 0 时，相邻且合并后跨度 ≤ MergeWindow 的段
//     并入同一区间，把段间小空洞填充进同一帧读取——海量稀疏点位的往返次数
//     从"点数"降到"窗口数"（默认窗口 125 = 单帧寄存器上限）。
//
// 每个返回的 AddressRange 包含该段独有的 AddressMap，供 parseRegisterResults /
// parseBitResults 直接使用。
func CalcReadRanges(addrs []po.DeviceAddress, opts CalcReadRangeOptions) ([]AddressRange, error) {
	cfgStartAddress := opts.StartAddress
	cfgQuantity := opts.Quantity
	mergeWindow := opts.MergeWindow

	if len(addrs) == 0 {
		return nil, fmt.Errorf("modbus: no addresses to read")
	}

	// 先解析所有 name，全量校验；同时计算每个点位的寄存器跨度
	// （int32/string 等多寄存器类型按字节数折算，保证区间覆盖其完整数据）。
	type parsedAddr struct {
		id   string
		addr uint16
		span uint16 // 该点位占用的寄存器跨度（≥1）
	}
	parsed := make([]parsedAddr, 0, len(addrs))
	for _, a := range addrs {
		_, addr, ok := ParsePLCAddress(a.Name)
		if !ok {
			return nil, fmt.Errorf("modbus: address %q (id=%s) invalid format", a.Name, a.ID)
		}
		bytes := getDataTypeBytes(a.DataType, opts.StringLen)
		span := uint16((bytes + 1) / 2)
		if span < 1 {
			span = 1
		}
		parsed = append(parsed, parsedAddr{id: a.ID, addr: addr, span: span})
	}

	if cfgStartAddress > 0 {
		// 配置模式：起始地址和读取数量由 protocol_json 决定，name 只做校验
		addrMap := make(map[string]uint16, len(parsed))
		for _, p := range parsed {
			if p.addr < cfgStartAddress {
				return nil, fmt.Errorf("modbus: address id=%s reg=%d < startAddress=%d",
					p.id, p.addr, cfgStartAddress)
			}
			addrMap[p.id] = p.addr
		}
		return []AddressRange{{
			StartAddress: cfgStartAddress,
			Quantity:     cfgQuantity,
			AddressMap:   addrMap,
		}}, nil
	}

	// 自动推导模式：按地址排序
	sort.Slice(parsed, func(i, j int) bool {
		return parsed[i].addr < parsed[j].addr
	})

	// 阶段 1：严格连续聚类（稠密块合并为单个段）。
	// end 跟踪当前段的寄存器末端（含多寄存器点位的跨度），
	// 使 string 等多寄存器点位落在区间末尾时缓冲区恰好覆盖其完整数据。
	var ranges []AddressRange
	start := parsed[0].addr
	end := parsed[0].addr + parsed[0].span - 1
	currentMap := map[string]uint16{parsed[0].id: parsed[0].addr}

	for _, p := range parsed[1:] {
		if p.addr > end+1 {
			// 地址不连续（gap > 1），结束当前段，写入新段
			ranges = append(ranges, AddressRange{
				StartAddress: start,
				Quantity:     end - start + 1,
				AddressMap:   currentMap,
			})
			start = p.addr
			end = p.addr + p.span - 1
			currentMap = map[string]uint16{p.id: p.addr}
		} else {
			// 地址连续（或相等），扩展当前段
			if pe := p.addr + p.span - 1; pe > end {
				end = pe
			}
			currentMap[p.id] = p.addr
		}
	}
	// 写入最后一段
	ranges = append(ranges, AddressRange{
		StartAddress: start,
		Quantity:     end - start + 1,
		AddressMap:   currentMap,
	})

	// 阶段 2：跨段合并。仅合并空洞段，绝不合并且不拆分已有稠密块。
	if mergeWindow > 0 && len(ranges) > 1 {
		merged := make([]AddressRange, 0, len(ranges))
		merged = append(merged, ranges[0])
		for _, r := range ranges[1:] {
			cur := &merged[len(merged)-1]
			rEnd := r.StartAddress + r.Quantity - 1
			if rEnd-cur.StartAddress <= mergeWindow {
				// 合并区间跨度不超过窗口：填充空洞后并入当前区间
				cur.Quantity = rEnd - cur.StartAddress + 1
				for k, v := range r.AddressMap {
					cur.AddressMap[k] = v
				}
			} else {
				merged = append(merged, r)
			}
		}
		ranges = merged
	}

	logDebug("auto-calc %d address ranges from %d addresses (mergeWindow=%d)",
		len(ranges), len(parsed), mergeWindow)
	for _, r := range ranges {
		logDebug("  range [%d, %d] quantity=%d (%d points)",
			r.StartAddress, r.StartAddress+r.Quantity-1, r.Quantity, len(r.AddressMap))
	}

	return ranges, nil
}

// readRegistersChunked 拆分读取寄存器（FC3/FC4），自动处理单次最多 125 个寄存器的协议限制。
//
// Modbus 协议限定 FC3/FC4 单帧最多读 125 个寄存器。当 quantity > 125 时，
// 自动拆分为多次 Modbus 请求，结果合并为一个连续 []byte（每个寄存器 2 字节）。
// 对上层解析函数完全透明。
func readRegistersChunked(readFn func(uint16, uint16) ([]byte, error), startAddr uint16, quantity uint16) ([]byte, error) {
	const maxPerRead = 125

	if quantity <= maxPerRead {
		return readFn(startAddr, quantity)
	}

	totalBytes := int(quantity) * 2
	buf := make([]byte, totalBytes)
	remaining := quantity
	addr := startAddr
	for remaining > 0 {
		chunkSize := remaining
		if chunkSize > maxPerRead {
			chunkSize = maxPerRead
		}
		chunk, err := readFn(addr, chunkSize)
		if err != nil {
			return nil, fmt.Errorf("modbus: chunk read [%d, %d) failed: %w", addr, addr+chunkSize, err)
		}
		copy(buf[(addr-startAddr)*2:], chunk)
		addr += chunkSize
		remaining -= chunkSize
	}
	logDebug("split register read qty=%d into %d chunks (max=%d)",
		quantity, (int(quantity)+maxPerRead-1)/maxPerRead, maxPerRead)
	return buf, nil
}

// readBitsChunked 拆分读取位（FC1/FC2），自动处理单次最多 2000 个位的协议限制。
//
// Modbus 协议限定 FC1/FC2 单帧最多读 2000 个位。当 quantity > 2000 时，
// 自动拆分为多次 Modbus 请求，结果合并为一个连续 []byte（每 8 位一字节）。
func readBitsChunked(readFn func(uint16, uint16) ([]byte, error), startAddr uint16, quantity uint16) ([]byte, error) {
	const maxPerRead = 2000

	if quantity <= maxPerRead {
		return readFn(startAddr, quantity)
	}

	totalBytes := (int(quantity) + 7) / 8
	buf := make([]byte, totalBytes)
	remaining := quantity
	addr := startAddr
	for remaining > 0 {
		chunkSize := remaining
		if chunkSize > maxPerRead {
			chunkSize = maxPerRead
		}
		chunk, err := readFn(addr, chunkSize)
		if err != nil {
			return nil, fmt.Errorf("modbus: chunk read bits [%d, %d) failed: %w", addr, addr+chunkSize, err)
		}
		// 每个分片的起始地址都是 8 的倍数（maxPerRead=2000 可被 8 整除），
		// 因此字节偏移量直接为 (addr-startAddr)/8
		byteOffset := int(addr-startAddr) / 8
		copy(buf[byteOffset:], chunk)
		addr += chunkSize
		remaining -= chunkSize
	}
	logDebug("split bit read qty=%d into %d chunks (max=%d)",
		quantity, (int(quantity)+maxPerRead-1)/maxPerRead, maxPerRead)
	return buf, nil
}

// getDataTypeBytes 返回数据类型对应的字节数。
// 通过包内类型表查询，未注册类型默认返回 2（一个寄存器）；
// 动态长度类型（string）返回配置的 stringLen（默认 16）。
func getDataTypeBytes(dataType string, stringLen int) int {
	dt, ok := lookupType(dataType)
	if !ok {
		return 2
	}
	if dt.Size <= 0 {
		if stringLen <= 0 {
			return defaultStringLen
		}
		return stringLen
	}
	return dt.Size
}
