// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package v2 是 Apache IoTDB 2.x 写入端在网关侧的适配层。
//
// 真正的入库实现（原生 RPC 连接/建库/攒批/行映射/断网补发）在独立组件
// github.com/Ellisata/iot-gateway/tsdb/iotdb/v2 中；本包只做两件事：
//   - 向 push 注册通道类型；
//   - 把 push_channel 的配置与 SQLite 连接翻译成组件 Options。
package v2

import (
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway/tsdb"
	iotdb "github.com/Ellisata/iot-gateway/tsdb/iotdb/v2"

	"iot-gateway/model/po"
	"iot-gateway/push"
)

// init 向 push 包注册 iotdb 通道工厂。
// 依赖入口处(main.go)对本包的空导入触发,Engine 的 buildChannels 自动发现。
func init() {
	push.RegisterType(push.ChannelTypeMeta{
		Type:  "iotdb-v2",
		Label: "Apache IoTDB v2",
	}, func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newIotdbChannel(db, ch)
	})
}

// newIotdbChannel 根据推送通道配置创建通道运行时(未启动)。
// db(SQLite)用于构建断网本地缓存(spoolEnabled 时)。
func newIotdbChannel(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
	cfg, err := iotdb.ParseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	channelType := ch.Type
	if channelType == "" {
		channelType = "iotdb-v2"
	}

	var store tsdb.SpoolStore
	if cfg.SpoolEnabled() && db != nil {
		store = push.NewSqliteSpool(db, ch.ID)
	}
	sink := iotdb.New(*cfg, tsdb.Options{
		ID:     ch.ID,
		Name:   ch.Name,
		Store:  store,
		Logger: push.ComponentLogger(),
	})
	return push.NewTimeSeriesChannel(
		sink,
		ch.ID,
		channelType,
		ch.ID+"|"+ch.Name+"|"+ch.ConfigJSON,
	), nil
}
