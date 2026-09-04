// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// mcConfigRaw 匹配 protocol_json 原始数据格式（port 为数字，其余值为字符串）
type mcConfigRaw struct {
	Transport    string `json:"transport"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	ComPort      string `json:"comPort"`
	BaudRate     string `json:"baudRate"`
	DataBits     string `json:"dataBits"`
	StopBits     string `json:"stopBits"`
	Parity       string `json:"parity"`
	Timeout      string `json:"timeout"` // 兼容字段名
	TimeoutMS    string `json:"timeoutMs"`
	StringLen    string `json:"stringLen"`
	MaxGap       string `json:"maxGap"`
	MaxReadWords string `json:"maxReadWords"`
}

// ParseMCConfig 从 device.protocol_json JSON 字符串解析 MC 配置。
// 未设置的字段使用默认值，非法数值容错回退默认值。
func ParseMCConfig(protocolJSON string) (*MCConfig, error) {
	cfg := DefaultMCConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw mcConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("mc config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (mcConfigRaw{}) {
		return cfg, nil
	}

	// 传输方式：仅接受 TCP / Serial，非法值回退默认 TCP
	if raw.Transport != "" {
		switch strings.ToUpper(raw.Transport) {
		case TransportSerial:
			cfg.Transport = TransportSerial
		default:
			cfg.Transport = TransportTCP
		}
	}
	cfg.Host = raw.Host
	if raw.Port > 0 {
		cfg.Port = raw.Port
	}

	// 串口配置
	cfg.ComPort = raw.ComPort
	if raw.BaudRate != "" {
		if v, err := strconv.Atoi(raw.BaudRate); err == nil && v > 0 {
			cfg.BaudRate = v
		}
	}
	if raw.DataBits != "" {
		if v, err := strconv.Atoi(raw.DataBits); err == nil && (v == 5 || v == 6 || v == 7 || v == 8) {
			cfg.DataBits = v
		}
	}
	if raw.StopBits != "" {
		if v, err := strconv.Atoi(raw.StopBits); err == nil && (v == 1 || v == 2) {
			cfg.StopBits = v
		}
	}
	if raw.Parity != "" {
		p := strings.ToUpper(raw.Parity)
		if p == "N" || p == "E" || p == "O" {
			cfg.Parity = p
		}
	}

	// 超时：优先标准字段名 timeoutMs，兼容旧字段名 timeout
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
		if v, err := strconv.Atoi(raw.StringLen); err == nil && v > 0 {
			cfg.StringLen = v
		}
	}
	// maxGap：非负整数，非法值回退默认
	if raw.MaxGap != "" {
		if v, err := strconv.Atoi(raw.MaxGap); err == nil && v >= 0 {
			cfg.MaxGap = v
		}
	}
	// maxReadWords：正整数，钳制到硬上限，非法值回退默认
	if raw.MaxReadWords != "" {
		if v, err := strconv.Atoi(raw.MaxReadWords); err == nil && v > 0 {
			cfg.MaxReadWords = v
			if cfg.MaxReadWords > maxReadWordsLimit {
				cfg.MaxReadWords = maxReadWordsLimit
			}
		}
	}

	// 设置 Timeout
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = defaultTimeoutMS
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// ParseMCConfigRaw 直接从字节解析
func ParseMCConfigRaw(data []byte) (*MCConfig, error) {
	return ParseMCConfig(string(data))
}
