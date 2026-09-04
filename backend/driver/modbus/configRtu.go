// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// modbusRTUConfigRaw 匹配 protocol_json 原始数据格式
// （数值字段为原生 JSON 类型：unitId/mergeWindow/stringLen，与 TCP 一致，不再是全字符串）
type modbusRTUConfigRaw struct {
	ComPort     string `json:"comPort"`
	UnitID      uint8  `json:"unitId"`
	ByteOrder   string `json:"byteOrder"`
	WordOrder   string `json:"wordOrder"`
	MergeWindow uint16 `json:"mergeWindow"`
	StringLen   int    `json:"stringLen"`
}

// ModbusRTUConfig Modbus RTU 协议配置（对应 device.protocol_json 反序列化）
type ModbusRTUConfig struct {
	ComPort      string        `json:"comPort"`      // 串口名称，如 "COM1"、"/dev/ttyUSB0"
	BaudRate     int           `json:"baudRate"`     // 波特率，默认 9600
	DataBits     int           `json:"dataBits"`     // 数据位（5/6/7/8），默认 8
	StopBits     int           `json:"stopBits"`     // 停止位（1 或 2），默认 1
	Parity       string        `json:"parity"`       // 校验位："N"=无、"E"=偶、"O"=奇，默认 "N"
	UnitID       uint8         `json:"unitId"`       // 从站地址，默认 1
	FunctionCode byte          `json:"functionCode"` // 功能码，默认 3 (ReadHoldingRegisters)
	StartAddress uint16        `json:"startAddress"` // 起始地址，默认 0
	Quantity     uint16        `json:"quantity"`     // 读取数量（寄存器数），默认 10
	ByteOrder    string        `json:"byteOrder"`    // 字节序：BIG_ENDIAN / LITTLE_ENDIAN
	WordOrder    string        `json:"wordOrder"`    // 字序：BIG_ENDIAN / LITTLE_ENDIAN
	Timeout      time.Duration `json:"-"`            // 超时时间（由 TimeoutMS 转换）
	TimeoutMS    int           `json:"timeoutMs"`    // 超时毫秒数，默认 5000
	MergeWindow  uint16        `json:"mergeWindow"`  // 自动模式下的合并窗口，默认 125
	StringLen    int           `json:"stringLen"`    // string 类型单点读取字节数，默认 16
}

// DefaultModbusRTUConfig 返回默认 Modbus RTU 配置
func DefaultModbusRTUConfig() *ModbusRTUConfig {
	return &ModbusRTUConfig{
		ComPort:      "COM1",
		BaudRate:     9600,
		DataBits:     8,
		StopBits:     1,
		Parity:       "N",
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

// ParseModbusRTUConfig 从 JSON 字符串解析 Modbus RTU 配置
// 未设置的字段使用默认值
func ParseModbusRTUConfig(protocolJSON string) (*ModbusRTUConfig, error) {
	cfg := DefaultModbusRTUConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw modbusRTUConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("modbus rtu config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (modbusRTUConfigRaw{}) {
		return cfg, nil
	}

	// 串口配置
	cfg.ComPort = raw.ComPort

	// 从站地址（原生 byte；未配置时保持默认 1）
	if raw.UnitID != 0 {
		cfg.UnitID = raw.UnitID
	}

	// 字节序/字序：只由显式 byteOrder/wordOrder 决定（headSortType 推导已随迁移移除）
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
