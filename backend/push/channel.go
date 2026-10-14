// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"fmt"
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

// ChannelFactory 根据推送通道配置构建一个通道实例（未启动）。
// db 注入用于通道级持久化（如断网本地缓存 push_outbox），不用的通道可忽略。
type ChannelFactory func(db *gorm.DB, ch *po.PushChannel) (Channel, error)

// channelFactories 通道类型 → 构建器注册表。
// 仅允许在 init 阶段通过 Register 写入，运行期只读，避免并发竞争。
var channelFactories = make(map[string]ChannelFactory)

// Register 注册通道类型构建器，供各通道子包在 init() 中调用。
// 新增通道类型（如 opcua、netty）在此注册一行即可接入。
func Register(name string, factory ChannelFactory) {
	// 统一小写注册，配合 buildChannels 的小写查找实现大小写不敏感匹配
	channelFactories[strings.ToLower(name)] = factory
}

// TestChannelConnectivity 根据通道类型名与 config_json 测试通道连通性。
// 复用类型工厂构建临时通道实例（同时校验配置合法性），再调用其 TestConnectivity
// 建立连接探测后释放，不落库、不启动后台 goroutine。测试通道若启用断网缓存
// （spool）会绑定 db 做一次 seed 计数，但测试路径无任何写入。
func TestChannelConnectivity(db *gorm.DB, name, configJSON string) error {
	factory, ok := channelFactories[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("push: channel type %s not supported", name)
	}
	ch, err := factory(db, &po.PushChannel{Name: name, ConfigJSON: configJSON})
	if err != nil {
		return err
	}
	return ch.TestConnectivity()
}
