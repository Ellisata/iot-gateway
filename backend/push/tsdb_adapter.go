// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import "github.com/Ellisata/iot-gateway/tsdb"

// timeSeriesChannel 把 tsdb.Sink 适配为 push.Channel。
//
// TDengine / InfluxDB 等时序入库通道的实现都在独立组件里，网关侧只需：
// 解析 config_json、构造 SpoolStore 与 Logger、把 Sink 包成 Channel。
// 这层同时补上组件不关心的网关语义：通道类型名、配置签名与 TimeSeries 标记。
type timeSeriesChannel struct {
	sink tsdb.Sink
	id   string
	typ  string
	sig  string
}

func (c *timeSeriesChannel) ChannelID() string { return c.id }
func (c *timeSeriesChannel) ConfigSig() string { return c.sig }

func (c *timeSeriesChannel) Start() { c.sink.Start() }
func (c *timeSeriesChannel) Stop()  { c.sink.Stop() }

func (c *timeSeriesChannel) Enqueue(b PushBatch) { c.sink.Enqueue(b) }

func (c *timeSeriesChannel) TestConnectivity() error { return c.sink.TestConnectivity() }

// IsTimeSeries 标记本通道写入时序数据库（实现 TimeSeriesChannel）。
func (c *timeSeriesChannel) IsTimeSeries() bool { return true }

// Snapshot 返回通道状态快照；Type 以 push_channel 配置为准（兼容旧的 type 缺省）。
func (c *timeSeriesChannel) Snapshot() ChannelStatusVO {
	st := c.sink.Status()
	st.Type = c.typ
	return st
}

// RecentMessages 实现 RecentMessagesProvider：最近写入（新→旧）。
func (c *timeSeriesChannel) RecentMessages(limit int) []RecentPushMessage {
	if p, ok := c.sink.(tsdb.RecentProvider); ok {
		return p.RecentMessages(limit)
	}
	return []RecentPushMessage{}
}

// NewTimeSeriesChannel 把时序入库组件包装成推送引擎可用的通道。
// id/typ/sig 由调用方的工厂按 push_channel 配置提供。
func NewTimeSeriesChannel(sink tsdb.Sink, id, typ, sig string) Channel {
	return &timeSeriesChannel{sink: sink, id: id, typ: typ, sig: sig}
}
