// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ModbusTcpConfig Modbus TCP 协议配置（对应 device.protocol_json 反序列化）
type ModbusTcpConfig struct {
	Host         string        `json:"host"`         // 设备主机地址，默认 "127.0.0.1"
	Port         int           `json:"port"`         // 设备端口，默认 502
	UnitID       byte          `json:"unitId"`       // 从站地址，默认 1
	FunctionCode byte          `json:"functionCode"` // 功能码，默认 3 (ReadHoldingRegisters)
	StartAddress uint16        `json:"startAddress"` // 起始地址，默认 0
	Quantity     uint16        `json:"quantity"`     // 读取数量（寄存器数），默认 10
	ByteOrder    string        `json:"byteOrder"`    // 字节序：BIG_ENDIAN / LITTLE_ENDIAN
	WordOrder    string        `json:"wordOrder"`    // 字序：BIG_ENDIAN / LITTLE_ENDIAN
	Timeout      time.Duration `json:"-"`            // 超时时间（由 TimeoutMS 转换）
	TimeoutMS    int           `json:"timeoutMs"`    // 超时毫秒数，默认 5000
	// MergeWindow 自动模式下的合并窗口（单位同功能码：FC3/4 为寄存器、FC1/2 为位）。
	// >0 时，把跨度在窗口内但有小空洞的相邻段合并进同一区间，减少串行往返；
	// =0 时仅按严格连续性聚类。默认 125（单帧寄存器上限）。
	MergeWindow uint16 `json:"mergeWindow"`
	// StringLen string 类型单点读取字节数（每个寄存器 2 字节）。默认 16。
	// 地址名不含长度声明，string 点位的读取跨度由此值决定；
	// 自动模式下 CalcReadRanges 会把区间末端扩展到覆盖该跨度。
	StringLen int `json:"stringLen"`
}

// DefaultModbusTcpConfig 返回默认 Modbus TCP 配置
func DefaultModbusTcpConfig() *ModbusTcpConfig {
	return &ModbusTcpConfig{
		Host:         "127.0.0.1",
		Port:         502,
		UnitID:       1,
		FunctionCode: FuncCodeReadHoldingRegisters,
		StartAddress: 0,
		Quantity:     10,
		ByteOrder:    ByteOrderBigEndian,
		WordOrder:    WordOrderBigEndian,
		TimeoutMS:    5000,
		MergeWindow:  defaultMergeWindow,
		StringLen:    defaultStringLen,
	}
}

// modbusConfigRaw 匹配 protocol_json 原始数据格式
// （数值字段为原生 JSON 类型：unitId/mergeWindow/stringLen，不再是全字符串）
type modbusConfigRaw struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	UnitID      byte   `json:"unitId"`
	ByteOrder   string `json:"byteOrder"`
	WordOrder   string `json:"wordOrder"`
	MergeWindow uint16 `json:"mergeWindow"`
	StringLen   int    `json:"stringLen"`
}

// ParseModbusTcpConfig 从 device.protocol_json JSON 字符串解析 Modbus 配置
// 未设置的字段使用默认值
func ParseModbusTcpConfig(protocolJSON string) (*ModbusTcpConfig, error) {
	cfg := DefaultModbusTcpConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw modbusConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("modbus config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (modbusConfigRaw{}) {
		return cfg, nil
	}

	cfg.Host = raw.Host
	cfg.Port = raw.Port
	// 从站地址（原生 byte；未配置时保持默认 1）
	if raw.UnitID != 0 {
		cfg.UnitID = raw.UnitID
	}
	// 独立配置优先：byteOrder/wordOrder 非空时覆盖 headSortType 推导值，
	// 允许按需组合（如字节大端 + 字小端的 "CD AB" 变体）。
	if raw.ByteOrder != "" {
		if strings.EqualFold(raw.ByteOrder, ByteOrderLittleEndian) {
			cfg.ByteOrder = ByteOrderLittleEndian
		} else {
			cfg.ByteOrder = ByteOrderBigEndian
		}
	}
	if raw.WordOrder != "" {
		if strings.EqualFold(raw.WordOrder, WordOrderLittleEndian) {
			cfg.WordOrder = WordOrderLittleEndian
		} else {
			cfg.WordOrder = WordOrderBigEndian
		}
	}

	// 合并窗口：未配置时保持默认 125
	if raw.MergeWindow > 0 {
		cfg.MergeWindow = raw.MergeWindow
	}

	// string 读取长度：未配置时保持默认 16
	if raw.StringLen > 0 {
		cfg.StringLen = raw.StringLen
	}

	// 设置 Timeout
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 5000
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// ParseModbusTcpConfigRaw 直接从字节解析
func ParseModbusTcpConfigRaw(data []byte) (*ModbusTcpConfig, error) {
	return ParseModbusTcpConfig(string(data))
}
