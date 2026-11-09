// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package kafka 实现 Apache Kafka 推送通道（注册类型名 "kafka"）。
//
// 通道复用 messagechannel 骨架：编码、断网缓存、补发、重连、监控与
// 预览/测试发送都与 http/mqtt 一致，本包只负责「把一条载荷 produce 到 Kafka」。
// 消息键固定为设备编码（缺失时回落设备 ID），保证同一设备落在同一分区。
package kafka

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultBrokerPort      = "9092"
	defaultClientID        = "iot-gateway"
	defaultTimeoutSeconds  = 10
	maxTimeoutSeconds      = 120
	defaultWorkers         = 1
	maxWorkers             = 64
	defaultSpoolMaxBatches = 100000

	authNone  = "none"
	authPlain = "plain"

	acksAll    = "all"
	acksLeader = "leader"
	acksNone   = "none"
)

// topicPattern Kafka topic 名允许的字符与长度（Kafka 官方约束）。
var topicPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

// config Kafka 推送通道配置（解析自 push_channel.config_json）。
//
// 示例：
//
//	{"brokers":"10.0.0.1:9092,10.0.0.2:9092","topic":"iot-data","clientId":"iot-gateway",
//	 "acks":"all","authType":"plain","saslUsername":"gateway","saslPassword":"secret",
//	 "tlsEnabled":true,"workers":1,"format":"native-json"}
type config struct {
	// Brokers 逗号分隔的 seed broker，可省略端口（默认 9092）。
	Brokers string `json:"brokers"`
	Topic   string `json:"topic"`
	// ClientID 客户端标识，缺省 iot-gateway。
	ClientID string `json:"clientId"`
	// Acks all/leader/none，缺省 all；非 all 会关闭幂等写入。
	Acks string `json:"acks"`
	// AllowAutoTopicCreation 允许 broker 自动创建 topic，缺省 false。
	AllowAutoTopicCreation bool `json:"allowAutoTopicCreation"`

	AuthType     string `json:"authType"`
	SASLUsername string `json:"saslUsername"`
	SASLPassword string `json:"saslPassword"`

	TLSEnabled         bool `json:"tlsEnabled"`
	InsecureSkipVerify bool `json:"insecureSkipVerify"`

	TimeoutSeconds  int  `json:"timeoutSeconds"`
	Workers         int  `json:"workers"`
	SpoolDisabled   bool `json:"spoolDisabled"`
	SpoolMaxBatches int  `json:"spoolMaxBatches"`

	Format  string          `json:"format,omitempty"`
	Mapping json.RawMessage `json:"mapping,omitempty"`

	// GroupIDs 采集组订阅（空 = 接收全部，含未分组记录）。
	GroupIDs []string `json:"groupIds,omitempty"`

	// brokerAddrs 归一化后的 broker 地址（host:port）。
	brokerAddrs []string
}

func parseConfig(raw string) (*config, error) {
	var cfg config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("kafka: 配置解析失败: %w", err)
	}

	addrs, err := parseBrokers(cfg.Brokers)
	if err != nil {
		return nil, err
	}
	cfg.brokerAddrs = addrs

	cfg.Topic = strings.TrimSpace(cfg.Topic)
	if cfg.Topic == "" {
		return nil, fmt.Errorf("kafka: topic 不能为空")
	}
	if !topicPattern.MatchString(cfg.Topic) || cfg.Topic == "." || cfg.Topic == ".." {
		return nil, fmt.Errorf("kafka: topic %q 非法，仅允许字母、数字、点、下划线与短横线", cfg.Topic)
	}

	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	if cfg.ClientID == "" {
		cfg.ClientID = defaultClientID
	}

	cfg.Acks = strings.ToLower(strings.TrimSpace(cfg.Acks))
	if cfg.Acks == "" {
		cfg.Acks = acksAll
	}
	switch cfg.Acks {
	case acksAll, acksLeader, acksNone:
	default:
		return nil, fmt.Errorf("kafka: acks 仅支持 all/leader/none")
	}

	cfg.AuthType = strings.ToLower(strings.TrimSpace(cfg.AuthType))
	if cfg.AuthType == "" {
		cfg.AuthType = authNone
	}
	switch cfg.AuthType {
	case authNone:
	case authPlain:
		if strings.TrimSpace(cfg.SASLUsername) == "" {
			return nil, fmt.Errorf("kafka: authType=plain 需要填写 saslUsername")
		}
	default:
		return nil, fmt.Errorf("kafka: 未知的 authType %q", cfg.AuthType)
	}

	if cfg.InsecureSkipVerify && !cfg.TLSEnabled {
		return nil, fmt.Errorf("kafka: insecureSkipVerify 仅在 tlsEnabled=true 时可用")
	}

	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = defaultTimeoutSeconds
	}
	if cfg.TimeoutSeconds < 1 || cfg.TimeoutSeconds > maxTimeoutSeconds {
		return nil, fmt.Errorf("kafka: timeoutSeconds 超出范围 [1,%d]", maxTimeoutSeconds)
	}

	if cfg.Workers == 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Workers > maxWorkers {
		cfg.Workers = maxWorkers
	}

	if cfg.SpoolMaxBatches <= 0 {
		cfg.SpoolMaxBatches = defaultSpoolMaxBatches
	}
	cfg.GroupIDs = normalizeGroupIDs(cfg.GroupIDs)
	return &cfg, nil
}

// spoolEnabled 本地缓存是否启用（缺省启用，仅显式 spoolDisabled=true 时关闭）。
func (c *config) spoolEnabled() bool { return !c.SpoolDisabled }

// parseBrokers 拆分、去重并补全端口。IPv6 地址会自动加方括号。
func parseBrokers(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		addr, err := normalizeBroker(part)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("kafka: brokers 不能为空")
	}
	return out, nil
}

func normalizeBroker(raw string) (string, error) {
	if strings.Contains(raw, "://") {
		return "", fmt.Errorf("kafka: broker %q 不应包含协议前缀，只写 host[:port]", raw)
	}
	host, port, err := net.SplitHostPort(raw)
	if err == nil {
		if host == "" {
			return "", fmt.Errorf("kafka: broker %q 缺少主机名", raw)
		}
		p, convErr := strconv.Atoi(port)
		if convErr != nil || p < 1 || p > 65535 {
			return "", fmt.Errorf("kafka: broker %q 端口非法", raw)
		}
		return net.JoinHostPort(host, port), nil
	}
	// 没有端口：补默认端口。裸 IPv6（含冒号）交给 JoinHostPort 加方括号。
	host = strings.Trim(raw, "[]")
	if host == "" {
		return "", fmt.Errorf("kafka: broker %q 缺少主机名", raw)
	}
	return net.JoinHostPort(host, defaultBrokerPort), nil
}

// normalizeGroupIDs 去掉空白项与重复项（保持原顺序）。
func normalizeGroupIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
