// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"fmt"
	"strconv"
	"strings"

	"iot-gateway/model/po"
	"iot-gateway/subscribe/mapping"
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
	topic    string
	qos      byte
	rule     *mapping.Rule
	sig      string
}

// parseSubscription 把持久化行解析为运行期配置。
// 配置非法时返回 error，由 Manager 跳过该订阅并记日志（不影响其它订阅启动）。
//
// 加载路径使用宽松校验：新增的必填项不能把历史订阅整体判死。
func parseSubscription(row *po.MqttSubscription) (*subscription, error) {
	return parseSubscriptionWith(row, false)
}

// parseSubscriptionWith strict=true 时使用保存路径的严格校验（全部主字段必填）。
func parseSubscriptionWith(row *po.MqttSubscription, strict bool) (*subscription, error) {
	broker := strings.TrimSpace(row.Broker)
	if broker == "" {
		return nil, fmt.Errorf("broker 不能为空")
	}
	topic := strings.TrimSpace(row.Topic)
	if topic == "" {
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

	var rule *mapping.Rule
	var err error
	if strict {
		rule, err = mapping.ParseStrict([]byte(row.MappingJSON))
	} else {
		rule, err = mapping.Parse([]byte(row.MappingJSON))
	}
	if err != nil {
		return nil, fmt.Errorf("映射规则无效: %w", err)
	}

	return &subscription{
		id:       row.ID,
		name:     row.Name,
		broker:   broker,
		port:     row.Port,
		clientID: row.ClientID,
		username: row.Username,
		password: row.Password,
		topic:    topic,
		qos:      byte(row.QoS),
		rule:     rule,
		sig: strings.Join([]string{
			row.ID, row.Name, broker, row.Port, row.ClientID, row.Username,
			row.Password, topic, strconv.Itoa(row.QoS), row.MappingJSON,
		}, "|"),
	}, nil
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

// ValidateConfig 校验一条订阅配置（不建连、不落库），供保存前校验复用。
// 保存路径要求全部主字段都已配置；加载路径仍走 parseSubscription 的宽松校验。
func ValidateConfig(row *po.MqttSubscription) error {
	_, err := parseSubscriptionWith(row, true)
	return err
}

// parseTestConfig 解析未落库的连通性测试参数。
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
	return parseSubscription(row)
}
