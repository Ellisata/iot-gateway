// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// mqttConfig MQTT 推送通道配置（解析自 push_channel.config_json）
//
// 示例：
// {"broker":"172.22.5.23","port":"1888","clientId":"iot-gateway",
//
//	"qos":1,"topic":"/IIoT/+/+/read,wer","username":"admin","password":"admin",
//	"publishWorkers":8}
type mqttConfig struct {
	Broker   string  `json:"broker"`
	Port     portStr `json:"port"` // 兼容字符串与数字两种写法，如 "1888" 或 1888
	ClientID string  `json:"clientId"`
	QoS      byte    `json:"qos"`
	Topic    string  `json:"topic"`
	Username string  `json:"username"`
	Password string  `json:"password"`

	// PublishWorkers 并发发布 worker 数（可选，缺省/≤0 用 defaultPublishWorkers，
	// 见 publishWorkers()）。broker 慢或跨广域时调大以提升吞吐。
	PublishWorkers int `json:"publishWorkers"`

	// SpoolDisabled 关闭断网本地缓存（可选，缺省=false 即默认启用）。
	// 启用时断连/内存队列满的批次写入 SQLite push_outbox，重连后补发，发布成功删除。
	// 关闭后回退为「丢最旧」的纯内存行为（见 mqttChannel.Enqueue）。
	SpoolDisabled bool `json:"spoolDisabled"`
	// SpoolMaxBatches 本地缓存最大批次上限（可选，缺省/≤0 用 defaultSpoolMaxBatches）。
	// 达到上限后裁剪最旧批次以约束磁盘占用。
	SpoolMaxBatches int `json:"spoolMaxBatches"`
}

// spoolEnabled 本地缓存是否启用（缺省启用，仅显式 spoolDisabled=true 时关闭）。
func (c *mqttConfig) spoolEnabled() bool {
	return !c.SpoolDisabled
}

// spoolBatchCap 返回实际生效的缓存批次上限，缺省/≤0 回落默认值。
func (c *mqttConfig) spoolBatchCap() int {
	if c.SpoolMaxBatches <= 0 {
		return defaultSpoolMaxBatches
	}
	return c.SpoolMaxBatches
}

// publishWorkers 返回实际生效的发布 worker 数：缺省/≤0 回落默认值，并 clamp 上限，
// 防止配置异常导致 goroutine 数量失控。
func (c *mqttConfig) publishWorkers() int {
	n := c.PublishWorkers
	if n < 1 {
		return defaultPublishWorkers
	}
	if n > maxPublishWorkers {
		return maxPublishWorkers
	}
	return n
}

// portStr 兼容字符串/数字形式的端口配置
type portStr string

// UnmarshalJSON 同时接受 "1888" 与 1888 两种 JSON 类型
func (p *portStr) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*p = portStr(s)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*p = portStr(strconv.FormatInt(n, 10))
		return nil
	}
	return fmt.Errorf("mqtt: invalid port %s", string(b))
}

// parseConfig 解析 config_json 为 mqtt 配置
func parseConfig(configJSON string) (*mqttConfig, error) {
	var cfg mqttConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, fmt.Errorf("mqtt: parse config failed: %w", err)
	}
	if cfg.Broker == "" {
		return nil, fmt.Errorf("mqtt: config broker is required")
	}
	if cfg.Port != "" {
		if _, err := strconv.Atoi(string(cfg.Port)); err != nil {
			return nil, fmt.Errorf("mqtt: config port %q invalid", cfg.Port)
		}
	}
	if cfg.QoS > 2 {
		return nil, fmt.Errorf("mqtt: config qos %d out of range [0,2]", cfg.QoS)
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("mqtt: config topic is required")
	}
	return &cfg, nil
}

// brokerURL 构建 broker 连接地址
// broker 已含协议前缀（tcp:// 等）时直接使用，否则拼接为 tcp://{broker}:{port}
func (c *mqttConfig) brokerURL() string {
	if strings.Contains(c.Broker, "://") {
		return c.Broker
	}
	port := c.Port
	if port == "" {
		port = "1883"
	}
	return fmt.Sprintf("tcp://%s:%s", c.Broker, port)
}

// buildTopics 根据配置主题模板与设备 ID 生成实际发布主题
//
// MQTT 发布禁止含通配符（+/#）的主题，因此把配置中的 + 替换为设备 ID
// （如 /IIoT/+/+/read → /IIoT/{deviceId}/{deviceId}/read），接收端订阅
// /IIoT/+/+/read 仍可收到全量。逗号分隔视作多个主题分别发布。
func buildTopics(template, deviceID string) []string {
	var topics []string
	for _, t := range strings.Split(template, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		topics = append(topics, strings.ReplaceAll(t, "+", deviceID))
	}
	return topics
}
