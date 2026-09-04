// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"errors"
	"fmt"

	goburrowModbus "github.com/goburrow/modbus"

	"iot-gateway/driver"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// isProtocolErr 判断错误是否为 Modbus 协议异常（设备响应但拒绝该请求，如 IllegalDataAddress）。
// 协议异常说明连接是通的，不应据此判定连接断开、也不应中断整台设备读取。
func isProtocolErr(err error) bool {
	var me *goburrowModbus.ModbusError
	return errors.As(err, &me)
}

// groupAddrsByFC 将点位按功能码分组。
//
// 配置模式（StartAddress>0）：全部归入 cfg.FunctionCode，保持向后兼容。
// 自动推导模式：按 name 地址解析出的 FC 分组（线圈/保持寄存器等不同地址空间互不干扰），
// 纯数字/十六进制点位（解析不出 FC，FC=0）归入 cfg.FunctionCode。
// 这样同一台设备同时配置线圈与保持寄存器点位时可分组各自读取，而不是整组报错。
func groupAddrsByFC(addrs []po.DeviceAddress, cfg *ModbusTcpConfig) (map[byte][]po.DeviceAddress, error) {
	groups := make(map[byte][]po.DeviceAddress)

	if cfg.StartAddress > 0 {
		groups[cfg.FunctionCode] = append(groups[cfg.FunctionCode], addrs...)
		return groups, nil
	}

	for _, a := range addrs {
		nameFC, _, ok := ParsePLCAddress(a.Name)
		if !ok {
			return nil, fmt.Errorf("modbus: address %q (id=%s) invalid format", a.Name, a.ID)
		}
		fc := nameFC
		if fc == 0 {
			fc = cfg.FunctionCode
		}
		groups[fc] = append(groups[fc], a)
	}
	return groups, nil
}

// readRegisterRanges 对多个 AddressRange 执行寄存器读取（FC3/FC4）。
// 与 readBitRanges 供 Modbus TCP / RTU 驱动共享。
//
// 逐区间读取并解析为 ReadResult；当某合并区间（含空洞，len(AddressMap) < Quantity）
// 整段读取失败时，自动降级为该区间点位按严格连续子段重读，避免设备对空洞
// 寄存器返回异常导致整批丢弃。
//
// 降级后仍失败的区间按错误类型分流：
//   - 协议异常（设备响应但拒绝该地址）→ 该区间点位 Quality=0，继续读其它区间，
//     单个异常点位不再拖垮整台设备的采集；
//   - 连接错误 → 照常向上返回错误，由上层判定断连并重连。
func readRegisterRanges(readFn func(uint16, uint16) ([]byte, error),
	addrs []po.DeviceAddress, ranges []AddressRange, cfg *ModbusTcpConfig) ([]driver.ReadResult, error) {

	var allResults []driver.ReadResult
	for i := range ranges {
		r := &ranges[i]
		results, err := readRegistersChunked(readFn, r.StartAddress, r.Quantity)
		if err != nil {
			// 含空洞的合并区间：降级为严格子段重读
			if len(r.AddressMap) < int(r.Quantity) {
				if sub, subErr := readRegisterRangesStrict(readFn, addrs, r, cfg); subErr == nil {
					allResults = append(allResults, sub...)
					continue
				} else {
					err = subErr
				}
			}
			if isProtocolErr(err) {
				logger.Warn("modbus: register range [%d, %d) unreadable, mark %d points quality=0: %v",
					r.StartAddress, r.StartAddress+r.Quantity, len(r.AddressMap), err)
				allResults = append(allResults, markRangeZero(addrs, r)...)
				continue
			}
			return nil, fmt.Errorf("modbus: read registers [%d, %d) failed: %w",
				r.StartAddress, r.StartAddress+r.Quantity, err)
		}
		rangeAddrs := filterAddrsByMap(addrs, r.AddressMap)
		allResults = append(allResults, parseRegisterResults(results, rangeAddrs, cfg, r)...)
	}
	return allResults, nil
}

// readBitRanges 对多个 AddressRange 执行位读取（FC1/FC2）。
// 语义与 readRegisterRanges 相同，仅针对线圈/离散输入。
func readBitRanges(readFn func(uint16, uint16) ([]byte, error),
	addrs []po.DeviceAddress, ranges []AddressRange) ([]driver.ReadResult, error) {

	var allResults []driver.ReadResult
	for i := range ranges {
		r := &ranges[i]
		results, err := readBitsChunked(readFn, r.StartAddress, r.Quantity)
		if err != nil {
			// 含空洞的合并区间：降级为严格子段重读
			if len(r.AddressMap) < int(r.Quantity) {
				if sub, subErr := readBitRangesStrict(readFn, addrs, r); subErr == nil {
					allResults = append(allResults, sub...)
					continue
				} else {
					err = subErr
				}
			}
			if isProtocolErr(err) {
				logger.Warn("modbus: bit range [%d, %d) unreadable, mark %d points quality=0: %v",
					r.StartAddress, r.StartAddress+r.Quantity, len(r.AddressMap), err)
				allResults = append(allResults, markRangeZero(addrs, r)...)
				continue
			}
			return nil, fmt.Errorf("modbus: read bits [%d, %d) failed: %w",
				r.StartAddress, r.StartAddress+r.Quantity, err)
		}
		rangeAddrs := filterAddrsByMap(addrs, r.AddressMap)
		allResults = append(allResults, parseBitResults(results, rangeAddrs, r)...)
	}
	return allResults, nil
}

// markRangeZero 为区间内点位生成 Quality=0 的空结果（保持原始顺序），
// 用于区间读取失败（协议异常）时标记异常点位而不中断整台设备。
func markRangeZero(addrs []po.DeviceAddress, r *AddressRange) []driver.ReadResult {
	rangeAddrs := filterAddrsByMap(addrs, r.AddressMap)
	out := make([]driver.ReadResult, 0, len(rangeAddrs))
	for _, a := range rangeAddrs {
		out = append(out, driver.ReadResult{
			DeviceAddressID: a.ID,
			Value:           "",
			DataType:        a.DataType,
			Kind:            typeKind(a.DataType),
			Quality:         0,
		})
	}
	return out
}

// readRegisterRangesStrict 对合并区间内的点位重新按严格连续子段读取。
// 用于整段读取失败（通常因空洞寄存器不可读）时的降级路径：
// 只读实际点位，绕过空洞，牺牲帧数换回数据可用性。
func readRegisterRangesStrict(readFn func(uint16, uint16) ([]byte, error),
	addrs []po.DeviceAddress, r *AddressRange, cfg *ModbusTcpConfig) ([]driver.ReadResult, error) {

	subAddrs := filterAddrsByMap(addrs, r.AddressMap)
	subRanges, err := CalcReadRanges(subAddrs, CalcReadRangeOptions{}) // 严格模式，不合并
	if err != nil {
		return nil, err
	}
	logger.Warn("modbus: merged range [%d, %d) has %d gaps, read failed, degraded to %d strict sub-ranges",
		r.StartAddress, r.StartAddress+r.Quantity, int(r.Quantity)-len(r.AddressMap), len(subRanges))

	var out []driver.ReadResult
	for i := range subRanges {
		sr := &subRanges[i]
		data, err := readRegistersChunked(readFn, sr.StartAddress, sr.Quantity)
		if err != nil {
			return nil, fmt.Errorf("modbus: strict sub-range [%d, %d) failed: %w",
				sr.StartAddress, sr.StartAddress+sr.Quantity, err)
		}
		srAddrs := filterAddrsByMap(subAddrs, sr.AddressMap)
		out = append(out, parseRegisterResults(data, srAddrs, cfg, sr)...)
	}
	return out, nil
}

// readBitRangesStrict 同 readRegisterRangesStrict，针对位类型（FC1/FC2）。
func readBitRangesStrict(readFn func(uint16, uint16) ([]byte, error),
	addrs []po.DeviceAddress, r *AddressRange) ([]driver.ReadResult, error) {

	subAddrs := filterAddrsByMap(addrs, r.AddressMap)
	subRanges, err := CalcReadRanges(subAddrs, CalcReadRangeOptions{}) // 严格模式，不合并
	if err != nil {
		return nil, err
	}
	logger.Warn("modbus: merged bit range [%d, %d) has %d gaps, read failed, degraded to %d strict sub-ranges",
		r.StartAddress, r.StartAddress+r.Quantity, int(r.Quantity)-len(r.AddressMap), len(subRanges))

	var out []driver.ReadResult
	for i := range subRanges {
		sr := &subRanges[i]
		data, err := readBitsChunked(readFn, sr.StartAddress, sr.Quantity)
		if err != nil {
			return nil, fmt.Errorf("modbus: strict sub-range [%d, %d) failed: %w",
				sr.StartAddress, sr.StartAddress+sr.Quantity, err)
		}
		srAddrs := filterAddrsByMap(subAddrs, sr.AddressMap)
		out = append(out, parseBitResults(data, srAddrs, sr)...)
	}
	return out, nil
}
