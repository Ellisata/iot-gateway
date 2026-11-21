// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"time"

	"github.com/Ellisata/iot-gateway/tsdb"
)

// ChannelStatusVO 推送通道运行状态响应。
//
// 与 tsdb.Status 同一类型：时序通道的 Status() 可直接作为通道状态返回。
type ChannelStatusVO = tsdb.Status

// FormatTime 格式化时间为字符串，供各通道子包在 Snapshot 中复用。
func FormatTime(t time.Time) string { return tsdb.FormatTime(t) }
