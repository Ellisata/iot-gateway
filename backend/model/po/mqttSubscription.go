// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

import (
	"iot-gateway/utils"

	"gorm.io/gorm"
)

// MqttSubscription MQTT 订阅持久化对象。
//
// 一条记录 = 一个外部 broker 上的一个 topic 订阅 + 一套映射规则：
// 命中的 JSON 报文按 mapping_json 转换为 collector.CollectedRecord，
// 再由订阅管理器投递到时序数据库类推送通道。
type MqttSubscription struct {
	ID          string `gorm:"primaryKey;column:id" json:"id"`
	Name        string `gorm:"column:name;uniqueIndex;not null" json:"name"`
	Description string `gorm:"column:description" json:"description"`
	// Broker 不含协议前缀的主机名/IP；已带 tcp:// 等前缀时直接使用
	Broker   string `gorm:"column:broker;not null" json:"broker"`
	Port     string `gorm:"column:port" json:"port"`
	ClientID string `gorm:"column:client_id" json:"clientId"`
	Username string `gorm:"column:username" json:"username"`
	Password string `gorm:"column:password" json:"password"`
	// Topic 订阅主题，支持 MQTT 通配符 + / #；
	// 多个过滤器用英文逗号分隔，共用本行的 QoS 与解析器。
	Topic string `gorm:"column:topic;not null" json:"topic"`
	QoS   int    `gorm:"column:qos;default:1" json:"qos"`
	// MappingJSON 内置映射（parser_type=builtin）的规则（见 parser/mapping）。
	// builtin 类型沿用本列，保证历史数据与 API 零迁移；其它类型读 ParserConfig。
	MappingJSON string `gorm:"column:mapping_json;not null" json:"mappingJson"`
	// ParserType 解析方式：builtin（默认）/ script / 将来的 plugin:<name>
	ParserType string `gorm:"column:parser_type;default:builtin" json:"parserType"`
	// ParserConfig 非 builtin 解析方式的配置（script 为脚本 + 限制参数）
	ParserConfig string `gorm:"column:parser_config" json:"parserConfig"`
	// SampleJSON 样例报文，仅供管理界面预览，运行期不参与转换
	SampleJSON string `gorm:"column:sample_json" json:"sampleJson"`
	// SampleTopic 样例主题，供 topicPattern 预览；运行期不参与转换
	SampleTopic string `gorm:"column:sample_topic" json:"sampleTopic"`
	Sort        int    `gorm:"column:sort;default:1" json:"sort"`
	Status      int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt   string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (MqttSubscription) TableName() string {
	return "mqtt_subscription"
}

func (m *MqttSubscription) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = utils.GenerateUUIDV7()
	}
	return nil
}
