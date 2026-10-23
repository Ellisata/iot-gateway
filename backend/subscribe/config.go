// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"iot-gateway/model/po"
	"iot-gateway/parser"
)

// subscription 单个订阅的运行期配置（已解析、已校验）。
type subscription struct {
	id       string
	name     string
	broker   string
	port     string
	clientID string
	username string
	password string
	// topic 是数据库里的原文（可能是逗号分隔的多个过滤器），仅用于展示、日志与导出。
	topic string
	// topics 拆分后的订阅过滤器列表（去空、去重、保序），建连时逐个订阅。
	topics []string
	qos    byte

	// 解析器：类型 + 原始配置 + 已构造实例（可能尚未构造，见 buildParser）。
	parserType   string
	parserConfig json.RawMessage
	parserSig    string
	prs          parser.Parser

	// connSig 只包含连接参数：改脚本/映射不应断开 MQTT 连接。
	connSig string
}

// parseSubscription 解析并构造运行期配置（含解析器实例）。
func parseSubscription(row *po.MqttSubscription) (*subscription, error) {
	cfg, err := parseSubscriptionWith(row, false)
	if err != nil {
		return nil, err
	}
	if err := cfg.buildParser(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseSubscriptionWith 解析配置但不构造解析器实例。
//
// strict=true 时使用保存路径的严格校验（全部主字段必填）；加载路径使用宽松校验，
// 新增的必填项不能把历史订阅整体判死。
func parseSubscriptionWith(row *po.MqttSubscription, strict bool) (*subscription, error) {
	broker := strings.TrimSpace(row.Broker)
	if broker == "" {
		return nil, fmt.Errorf("broker 不能为空")
	}
	topic := strings.TrimSpace(row.Topic)
	topics := splitTopics(topic)
	if len(topics) == 0 {
		return nil, fmt.Errorf("topic 不能为空")
	}
	if row.QoS < 0 || row.QoS > 2 {
		return nil, fmt.Errorf("qos %d 超出范围 [0,2]", row.QoS)
	}
	if row.Port != "" {
		if _, err := strconv.Atoi(row.Port); err != nil {
			return nil, fmt.Errorf("port %q 不是合法数字", row.Port)
		}
	}

	parserType, parserConfig, err := resolveParserConfig(row)
	if err != nil {
		return nil, err
	}
	if strict {
		if err := parser.Validate(parser.Config{Type: parserType, Config: parserConfig}); err != nil {
			return nil, fmt.Errorf("解析配置无效: %w", err)
		}
	}

	cfg := &subscription{
		id:           row.ID,
		name:         row.Name,
		broker:       broker,
		port:         row.Port,
		clientID:     row.ClientID,
		username:     row.Username,
		password:     row.Password,
		topic:        topic,
		topics:       topics,
		qos:          byte(row.QoS),
		parserType:   parserType,
		parserConfig: parserConfig,
		parserSig:    parserType + "|" + string(parserConfig),
	}
	// 用拆分后的过滤器拼签名：只改空白/重复项不应触发重连。
	cfg.connSig = strings.Join([]string{
		row.ID, broker, row.Port, row.ClientID, row.Username,
		row.Password, strings.Join(topics, ","), strconv.Itoa(row.QoS),
	}, "|")
	return cfg, nil
}

// splitTopics 把逗号分隔的 topic 配置拆成订阅过滤器列表。
//
// 与推送通道 buildTopics 的口径一致：只认英文逗号，逐段 trim、丢弃空段并去重。
// 单个 topic 原样返回，因此旧配置的行为不变。
func splitTopics(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

// topicFilters 返回供 SubscribeMultiple 使用的「主题 → QoS」映射。
// 一条记录共用一个 QoS，与单 topic 时代的配置语义一致。
func (s *subscription) topicFilters() map[string]byte {
	filters := make(map[string]byte, len(s.topics))
	for _, t := range s.topics {
		filters[t] = s.qos
	}
	return filters
}

// resolveParserConfig 按解析方式取配置列。
//
// builtin 沿用历史列 mapping_json（现有 API/历史数据零迁移）；
// 其余类型（script、将来的 plugin:<name>）读 parser_config。
// 同一类型只有一处配置真源。
func resolveParserConfig(row *po.MqttSubscription) (string, json.RawMessage, error) {
	parserType := strings.TrimSpace(row.ParserType)
	if parserType == "" {
		parserType = parser.TypeBuiltin
	}
	var raw string
	if parserType == parser.TypeBuiltin {
		raw = row.MappingJSON
	} else {
		raw = row.ParserConfig
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil, fmt.Errorf("解析方式 %q 的配置为空", parserType)
	}
	return parserType, json.RawMessage(raw), nil
}

// buildParser 构造解析器实例。调用方负责 Close。
func (s *subscription) buildParser() error {
	p, err := parser.New(parser.Config{Type: s.parserType, Config: s.parserConfig})
	if err != nil {
		return fmt.Errorf("解析配置无效: %w", err)
	}
	s.prs = p
	return nil
}

// brokerURL 构建 broker 连接地址。
// broker 已含协议前缀（tcp:// 等）时直接使用，否则拼接为 tcp://{broker}:{port}。
func (s *subscription) brokerURL() string {
	if strings.Contains(s.broker, "://") {
		return s.broker
	}
	port := s.port
	if port == "" {
		port = "1883"
	}
	return fmt.Sprintf("tcp://%s:%s", s.broker, port)
}

// endpoint 返回展示用的 broker 地址。
func (s *subscription) endpoint() string {
	return s.brokerURL()
}

// ValidateConfig 校验一条订阅配置（不建连、不落库、不构造运行实例），
// 供保存前校验复用。保存路径要求全部主字段都已配置。
func ValidateConfig(row *po.MqttSubscription) error {
	_, err := parseSubscriptionWith(row, true)
	return err
}

// parseTestConfig 解析未落库的连通性测试参数（不需要解析器实例）。
func parseTestConfig(broker, port, clientID, username, password, topic string) (*subscription, error) {
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("topic 不能为空")
	}
	row := &po.MqttSubscription{
		Name:        "connectivity-test",
		Broker:      broker,
		Port:        port,
		ClientID:    clientID,
		Username:    username,
		Password:    password,
		Topic:       topic,
		QoS:         0,
		MappingJSON: `{"fields":{"deviceId":{"source":"const","value":"d"},"deviceAddressId":{"source":"const","value":"a"},"value":{"source":"const","value":"1"}}}`,
	}
	return parseSubscriptionWith(row, false)
}
