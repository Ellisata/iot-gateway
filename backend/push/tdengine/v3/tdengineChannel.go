// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package v3 是 TDengine 3.x 写入端在网关侧的适配层。
//
// 真正的入库实现（连接/建表/攒批/SQL 构造/断网补发）在独立组件
// github.com/Ellisata/iot-gateway/tsdb/tdengine/v3 中；本包只做两件事：
//   - 向 push 注册通道类型；
//   - 把 push_channel 的配置与 SQLite 连接翻译成组件 Options。
package v3

import (
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway/tsdb"
	tdengine "github.com/Ellisata/iot-gateway/tsdb/tdengine/v3"

	"iot-gateway/model/po"
	"iot-gateway/push"
)

// init 向 push 包注册 tdengine 通道工厂。
// 依赖入口处(main.go)对本包的空导入触发,Engine 的 buildChannels 自动发现。
func init() {
	push.RegisterType(push.ChannelTypeMeta{
		Type:  "tdengine-v3",
		Label: "TDengine v3",
	}, func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newTdengineChannel(db, ch)
	})
}

// newTdengineChannel 根据推送通道配置创建通道运行时(未启动)。
// db(SQLite)用于构建断网本地缓存(spoolEnabled 时)。
func newTdengineChannel(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
	cfg, err := tdengine.ParseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	channelType := ch.Type
	if channelType == "" {
		channelType = "tdengine"
	}

	var store tsdb.SpoolStore
	if cfg.SpoolEnabled() && db != nil {
		store = push.NewSqliteSpool(db, ch.ID)
	}
	sink := tdengine.New(*cfg, tsdb.Options{
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
