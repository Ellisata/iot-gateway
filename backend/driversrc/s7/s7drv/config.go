// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// maxConfigGap maxGap 配置上限。gap 合并仅用于桥接点位间的小间隙，
// 上限 1024 字节防止配置失误产生超大数据读取区间。
const maxConfigGap = 1024

// s7ConfigRaw 匹配 protocol_json 原始数据格式（所有值均为字符串）
type s7ConfigRaw struct {
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Rack           string `json:"rack"`
	Slot           string `json:"slot"`
	ConnectionType string `json:"connectionType"`
	Timeout        string `json:"timeout"`   // 兼容字段名
	TimeoutMS      string `json:"timeoutMs"` // 标准字段名
	StringLen      string `json:"stringLen"`
	MaxGap         string `json:"maxGap"` // 区间合并最大允许字节间隙
}

// ParseS7Config 从 device.protocol_json JSON 字符串解析 S7 配置
// 未设置的字段使用默认值，非法数值容错回退默认值
func ParseS7Config(protocolJSON string) (*S7Config, error) {
	cfg := DefaultS7Config()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw s7ConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("s7 config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (s7ConfigRaw{}) {
		return cfg, nil
	}

	cfg.Host = raw.Host
	if raw.Port != 0 {
		cfg.Port = raw.Port
	}
	if raw.Rack != "" {
		if v, err := strconv.Atoi(raw.Rack); err == nil && v >= 0 {
			cfg.Rack = v
		}
	}
	if raw.Slot != "" {
		if v, err := strconv.Atoi(raw.Slot); err == nil && v >= 0 {
			cfg.Slot = v
		}
	}
	// 连接类型仅接受 1=PG / 2=OP / 3=PC(BASIC)，非法值回退默认 PG
	if raw.ConnectionType != "" {
		if v, err := strconv.Atoi(raw.ConnectionType); err == nil &&
			v >= ConnectionTypePG && v <= ConnectionTypeBasic {
			cfg.ConnectionType = v
		}
	}
	// 优先使用标准字段名 timeoutMs，兼容旧字段名 timeout
	if raw.TimeoutMS != "" {
		if v, err := strconv.Atoi(raw.TimeoutMS); err == nil && v > 0 {
			cfg.TimeoutMS = v
		}
	} else if raw.Timeout != "" {
		if v, err := strconv.Atoi(raw.Timeout); err == nil && v > 0 {
			cfg.TimeoutMS = v
		}
	}
	if raw.StringLen != "" {
		if v, err := strconv.Atoi(raw.StringLen); err == nil && v > 0 && v <= 65535 {
			cfg.StringLen = v
		}
	}
	// maxGap：非负整数，钳制到 [0, maxConfigGap]，非法值回退默认
	if raw.MaxGap != "" {
		if v, err := strconv.Atoi(raw.MaxGap); err == nil && v >= 0 {
			cfg.MaxGap = v
			if cfg.MaxGap > maxConfigGap {
				cfg.MaxGap = maxConfigGap
			}
		}
	}

	// 设置 Timeout
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 5000
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// ParseS7ConfigRaw 直接从字节解析
func ParseS7ConfigRaw(data []byte) (*S7Config, error) {
	return ParseS7Config(string(data))
}
