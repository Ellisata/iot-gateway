// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// Channel 推送通道通用接口。
//
// Engine 仅依赖此接口，不关心具体通道类型。各类型通道（mqtt、opcua...）
// 以独立子包实现本接口，并在该子包的 init() 中调用 Register 注册一行工厂，
// 再在入口（main.go）空导入子包即可接入，Engine 的 buildChannels 会自动发现。
//
// 方法必须导出：子包跨包实现接口，未导出方法在不同包中不构成同一方法。
type Channel interface {
	ChannelID() string
	ConfigSig() string
	Start()
	Stop()
	Enqueue(PushBatch)
	Snapshot() ChannelStatusVO
	// TestConnectivity 测试当前配置的连通性（同步建立连接探测后关闭，不启动通道）。
	// 供管理界面保存配置前的"测试连接"校验，失败返回具体错误原因。
	TestConnectivity() error
}

// TimeSeriesChannel 标记接口：该通道的数据最终落在时序数据库（TDengine / InfluxDB）。
//
// MQTT 订阅的数据经此接口定向投递，避免再经推送引擎里的 MQTT 通道回环发布
// （订阅主题与发布主题相同时会形成自激）。用可选接口而不是给 Channel 增加方法，
// 是为了不强迫将来新增的非时序通道（如 Webhook）也回答一个与自身无关的问题。
type TimeSeriesChannel interface {
	Channel
	IsTimeSeries() bool
}

// ChannelFactory 根据推送通道配置构建一个通道实例（未启动）。
// db 注入用于通道级持久化（如断网本地缓存 push_outbox），不用的通道可忽略。
type ChannelFactory func(db *gorm.DB, ch *po.PushChannel) (Channel, error)

// ChannelTypeMeta 通道类型的展示与能力元数据。
//
// MessageBased 决定管理端是否显示「输出格式/映射/预览」；Formats 只在
// MessageBased=true 时有意义。TDengine/InfluxDB 通过旧 Register 注册，
// 元数据为 MessageBased=false。
type ChannelTypeMeta struct {
	Type             string       `json:"type"`
	Label            string       `json:"label"`
	MessageBased     bool         `json:"messageBased"`
	Formats          []FormatMeta `json:"formats,omitempty"`
	DefaultFormat    string       `json:"defaultFormat,omitempty"`
	SupportsPreview  bool         `json:"supportsPreview"`
	SupportsTestSend bool         `json:"supportsTestSend"`
}

// channelFactories 通道类型 → 构建器注册表。
// 仅允许在 init 阶段通过 Register/RegisterType 写入，运行期只读，避免并发竞争。
var channelFactories = make(map[string]ChannelFactory)

// channelTypeMetas 通道类型 → 元数据，与 channelFactories 同生命周期。
var channelTypeMetas = make(map[string]ChannelTypeMeta)

// Register 注册通道类型构建器（无消息类能力的旧入口）。
func Register(name string, factory ChannelFactory) {
	RegisterType(ChannelTypeMeta{Type: name, Label: name}, factory)
}

// RegisterType 注册通道类型构建器与元数据，供各通道子包在 init() 中调用。
func RegisterType(meta ChannelTypeMeta, factory ChannelFactory) {
	name := strings.ToLower(strings.TrimSpace(meta.Type))
	if name == "" {
		panic("push: RegisterType requires a non-empty type")
	}
	if _, exists := channelFactories[name]; exists {
		panic("push: channel type registered twice: " + name)
	}
	meta.Type = name
	channelFactories[name] = factory
	channelTypeMetas[name] = meta
}

// SupportedChannelTypeMetas 返回全部已注册类型，按 Type 排序。
func SupportedChannelTypeMetas() []ChannelTypeMeta {
	out := make([]ChannelTypeMeta, 0, len(channelTypeMetas))
	for _, meta := range channelTypeMetas {
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// RegisteredTypeMeta 查询某个类型的元数据。
func RegisteredTypeMeta(name string) (ChannelTypeMeta, bool) {
	meta, ok := channelTypeMetas[strings.ToLower(strings.TrimSpace(name))]
	return meta, ok
}

// NewTemporaryChannel 根据类型与配置构建一个未启动的通道实例。
// 用于连通性测试、载荷预览与测试发送：走与运行期完全相同的工厂路径。
func NewTemporaryChannel(db *gorm.DB, channelType, name, configJSON string) (Channel, error) {
	typ := strings.ToLower(strings.TrimSpace(channelType))
	factory, ok := channelFactories[typ]
	if !ok {
		return nil, fmt.Errorf("push: channel type %s not supported", channelType)
	}
	if strings.TrimSpace(name) == "" {
		name = typ
	}
	return factory(db, &po.PushChannel{Name: name, Type: typ, ConfigJSON: configJSON})
}

// TestChannelConnectivity 根据通道类型名与 config_json 测试通道连通性。
// 复用类型工厂构建临时通道实例（同时校验配置合法性），再调用其 TestConnectivity
// 建立连接探测后释放，不落库、不启动后台 goroutine。测试通道若启用断网缓存
// （spool）会绑定 db 做一次 seed 计数，但测试路径无任何写入。
func TestChannelConnectivity(db *gorm.DB, channelType, configJSON string) error {
	ch, err := NewTemporaryChannel(db, channelType, channelType, configJSON)
	if err != nil {
		return err
	}
	return ch.TestConnectivity()
}
