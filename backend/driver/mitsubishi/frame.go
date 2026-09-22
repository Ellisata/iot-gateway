// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MC 协议固定头字段（QnA 兼容 3E/4C 帧）。
// 网络号/PC 号/I-O 号/站号：单 CPU 系统的标准取值，与 HslCommunication 默认一致。
// 注意 3E/SLMP 二进制帧所有多字节字段均小端（低位在前）：I/O 号 0x03FF 线上为 FF 03。
const (
	mcNetNo   = 0x00 // 网络号
	mcPCNo    = 0xFF // PC 号
	mcIONoHi  = 0x03 // I/O 号高字节（0x03FF）
	mcIONoLo  = 0xFF // I/O 号低字节
	mcStation = 0x00 // 站号
)

// 请求子头与命令/子命令码。
const (
	subHeaderRead = 0x5000 // 子头：批量读
	cmdRead       = 0x0401 // 命令：批量读
	subCmdWord    = 0x0000 // 子命令：字单位
	subCmdBit     = 0x0001 // 子命令：位单位
)

// 响应子头高字节（0xD000 = 读响应，0xD001 = 写响应）。
const respSubHeaderHi = 0xD0

// MC 协议结束码（响应码，2 字节大端）。
// 结束码非 0 表示设备可达但拒绝了该请求（连接是通的）。
const (
	endCodeNormal        = 0x0000 // 正常
	endCodeDeviceIllegal = 0xC051 // 设备指定错误
	endCodeAddrRange     = 0xC056 // 地址范围外
	endCodePointsError   = 0xC05B // 点数指定错误
	endCodeCommError     = 0xC0EF // 通信错误（PLC 忙 / 程序运行中）
)

// mcEndCodeError 设备响应但拒绝请求的错误（连接是通的）。
// 驱动据此将该区间点位标 Quality=0 而非判定断连（对齐 fins IsEndCodeError）。
type mcEndCodeError struct {
	code uint16
}

func (e *mcEndCodeError) Error() string {
	return fmt.Sprintf("mc: end code 0x%04X", e.code)
}

func newEndCodeError(code uint16) error {
	return &mcEndCodeError{code: code}
}

// IsEndCodeError 判断是否为结束码错误（设备可达但拒绝）。
func IsEndCodeError(err error) bool {
	var e *mcEndCodeError
	return errors.As(err, &e)
}

// buildMCBody 构建请求体（命令 + 子命令 + 首地址号 + 设备码 + 点数，10 字节）。
// TCP（3E 帧）与串口（4C Format5）共用此应用层编码。
// 3E/SLMP 二进制帧所有多字节字段均小端（低位在前）：命令 0x0401 线上为 01 04。
//
//	命令(2 LE) | 子命令(2 LE) | 首地址号(3 LE) | 设备码(1) | 点数(2 LE)
//
// 软元件的两段是「首地址号在前、设备码在后」，且设备码只占 1 字节 ——
// 与 ASCII 帧的「2 字符设备码 + 6 位地址」正好相反，别照 ASCII 的顺序写，
// 也别照 FINS 那种「2 字节区域码 + 地址」的形状写。写反了 PLC 会把首地址号的
// 低字节当成设备码、把设备码当成地址的一部分，请求被解析成非法软元件，
// 一律回 0xC05A/C056/C05B 结束码（真机实测：顺序正确时同一地址回 0x0000）。
func buildMCBody(dev mcDevice, head uint32, points uint16, bitMode bool) []byte {
	body := make([]byte, 0, 10)
	body = append(body, byte(cmdRead&0xFF), byte(cmdRead>>8)) // 命令：批量读（小端）
	if bitMode {
		body = append(body, byte(subCmdBit&0xFF), byte(subCmdBit>>8)) // 位单位
	} else {
		body = append(body, byte(subCmdWord&0xFF), byte(subCmdWord>>8)) // 字单位
	}
	body = append(body, byte(head), byte(head>>8), byte(head>>16)) // 首地址号（3 字节 LE）
	body = append(body, byte(dev.code))                            // 设备码（1 字节，设备表取值恒 < 0x100）
	body = append(body, byte(points), byte(points>>8))             // 点数（2 字节 LE）
	return body
}

// 3E 帧请求数据长度（= 监视定时器 2 字节 + 请求体 10 字节 = 12）与监视定时器。
// 监视定时器 250ms/单位，0x0010 = 4s，为 PLC 侧请求处理超时；
// 这两段字段在标准 3E 帧中必须存在——缺了请求数据长度，PLC 会把其后的命令字节
// 误读为长度并一直等待剩余字节，导致不返回任何响应（表现为读响应头 i/o timeout）。
// 与帧内其余多字节字段一致，均为小端：长度 0x000C → 0C 00，定时器 0x0010 → 10 00。
const (
	reqDataLenRead = 0x000C // 请求数据长度：监视定时器(2) + 请求体(10)
	monitorTimer   = 0x0010 // 监视定时器：16 × 250ms = 4s
)

// buildReadFrame 构建 3E 帧 TCP 读请求（21 字节，无 STX/ETX/和校验）。
//
//	子头 0x50 0x00 | 网络 0x00 | PC 0xFF | I/O 0x03 0xFF（小端→FF 03）| 站 0x00 |
//	请求数据长度(2 LE) | 监视定时器(2 LE) | [buildMCBody]
func buildReadFrame(dev mcDevice, head uint32, points uint16, bitMode bool) []byte {
	frame := make([]byte, 0, 21)
	frame = append(frame, byte((subHeaderRead>>8)&0xFF), byte(subHeaderRead&0xFF)) // 子头：读
	frame = append(frame, mcNetNo, mcPCNo, mcIONoLo, mcIONoHi, mcStation)
	frame = append(frame, byte(reqDataLenRead&0xFF), byte(reqDataLenRead>>8)) // 请求数据长度（小端）
	frame = append(frame, byte(monitorTimer&0xFF), byte(monitorTimer>>8))     // 监视定时器（小端）
	frame = append(frame, buildMCBody(dev, head, points, bitMode)...)
	return frame
}

// parseTCPEndCode 解析 3E 帧响应头（11 字节：子头 2 + 网络/PC/IO/站 5 + 响应数据长度 2 + 结束码 2）。
// 返回结束码；结束码非 0 时返回 mcEndCodeError。
// 结束码为 2 字节小端：0xC056（地址范围外）线上为 56 C0。
// 子头不做强校验（仅要求高字节为 0xD0 读响应），防止真机方言差异误杀。
func parseTCPEndCode(header []byte) (uint16, error) {
	if len(header) < 11 {
		return 0, fmt.Errorf("mc tcp: short response header (%d bytes)", len(header))
	}
	if header[0] != respSubHeaderHi {
		return 0, fmt.Errorf("mc tcp: unexpected response subheader 0x%02X", header[0])
	}
	endCode := binary.LittleEndian.Uint16(header[9:11])
	if endCode != 0 {
		return endCode, newEndCodeError(endCode)
	}
	return 0, nil
}

// tcpResponseDataLen 解析 3E 帧响应头中的数据长度（2 字节小端，[7:9]）。
// 数据长度 = 结束码(2) + 数据字节数。
func tcpResponseDataLen(header []byte) int {
	if len(header) < 9 {
		return 0
	}
	return int(binary.LittleEndian.Uint16(header[7:9]))
}
