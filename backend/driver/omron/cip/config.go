package cip

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CIPConfig CIP 协议配置（对应 device.protocol_json 反序列化）。
type CIPConfig struct {
	Host      string        `json:"host"`      // PLC IP 地址
	Port      int           `json:"port"`      // 端口，默认 44818
	PingTag   string        `json:"pingTag"`   // 连接测试读此标签；为空仅做 Register Session
	TimeoutMS int           `json:"timeoutMs"` // 超时毫秒数，默认 5000
	Timeout   time.Duration `json:"-"`         // 超时时间（由 TimeoutMS 转换）
	StringLen int           `json:"stringLen"` // string 类型单点读取最大字节数，默认 80
}

// DefaultCIPConfig 返回默认 CIP 配置
func DefaultCIPConfig() *CIPConfig {
	return &CIPConfig{
		Host:      "127.0.0.1",
		Port:      defaultPort,
		TimeoutMS: defaultTimeoutMS,
		Timeout:   time.Duration(defaultTimeoutMS) * time.Millisecond,
		StringLen: defaultStringLen,
	}
}

// cipConfigRaw 匹配 protocol_json 原始数据格式（port 为数字，其余值为字符串）
type cipConfigRaw struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	PingTag   string `json:"pingTag"`
	Timeout   string `json:"timeout"`   // 兼容字段名
	TimeoutMS string `json:"timeoutMs"` // 标准字段名
	StringLen string `json:"stringLen"`
}

// ParseCIPConfig 从 device.protocol_json JSON 字符串解析 CIP 配置。
// 未设置的字段使用默认值，非法数值容错回退默认值。
func ParseCIPConfig(protocolJSON string) (*CIPConfig, error) {
	cfg := DefaultCIPConfig()

	if protocolJSON == "" {
		return cfg, nil
	}

	// 先解析为原始格式，校验 JSON 合法性
	var raw cipConfigRaw
	if err := json.Unmarshal([]byte(protocolJSON), &raw); err != nil {
		return nil, fmt.Errorf("cip config: invalid JSON: %w", err)
	}

	// 如果 JSON 为空，直接返回默认值
	if raw == (cipConfigRaw{}) {
		return cfg, nil
	}

	cfg.Host = raw.Host
	if raw.Port > 0 {
		cfg.Port = raw.Port
	}
	cfg.PingTag = strings.TrimSpace(raw.PingTag)

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

	// 设置 Timeout
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = defaultTimeoutMS
	}
	cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond

	return cfg, nil
}

// ParseCIPConfigRaw 直接从字节解析
func ParseCIPConfigRaw(data []byte) (*CIPConfig, error) {
	return ParseCIPConfig(string(data))
}

// sameConfig 判断两份配置的关键连接参数是否一致，用于 Ping 复用已有连接。
func sameConfig(a, b *CIPConfig) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Host == b.Host && a.Port == b.Port &&
		a.PingTag == b.PingTag && a.StringLen == b.StringLen
}
