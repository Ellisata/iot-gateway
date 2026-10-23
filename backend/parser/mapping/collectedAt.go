// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"strconv"
	"strings"
	"time"
)

// collectedAtLayout 与推送引擎、时序通道使用的毫秒精度本地时间格式一致。
const collectedAtLayout = "2006-01-02 15:04:05.000"

// collectedAtLayouts 常见的外部时间写法。
// RFC3339 带时区，按字符串里的时区解析后再转本地时间。
var collectedAtLayouts = []string{
	collectedAtLayout,
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
	"2006/01/02 15:04",
	"2006-01-02T15:04:05",
	"2006-01-02",
	time.RFC3339Nano,
	time.RFC3339,
}

// normalizeCollectedAt 把报文里的采集时间归一成网关统一格式。
//
// 支持：
//   - epoch 秒 / 毫秒 / 微秒 / 纳秒（数字或数字字符串，按数量级识别）
//   - "2006-01-02 15:04:05.123" / "2006-01-02 15:04:05"
//   - "2006/01/02 15:04:05" / "2006-01-02"
//   - RFC3339（"2026-10-19T17:05:30+08:00" / "...Z"）
//
// 无法识别时原样返回：时序通道的 collectedAtMillis 会把解析失败回落到批次时间，
// 不会阻断整批写入。
func normalizeCollectedAt(v any) string {
	if v == nil {
		return ""
	}

	s := strings.TrimSpace(toString(v))
	if s == "" {
		return ""
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return epochMillisToLocal(epochToMillis(n))
	}
	// 带小数/指数的 epoch 秒（如 1729328730.5）；日期串不会走到这里。
	if strings.ContainsAny(s, ".eE") {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return epochMillisToLocal(int64(f * 1000))
		}
	}

	for _, layout := range collectedAtLayouts {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Format(collectedAtLayout)
		}
	}
	return s
}

// epochToMillis 按数量级推断 epoch 单位并换算成毫秒。
//
//	>= 1e17 纳秒   >= 1e14 微秒   >= 1e12 毫秒   其余按秒
func epochToMillis(n int64) int64 {
	a := n
	if a < 0 {
		a = -a
	}
	switch {
	case a >= 1e17:
		return n / 1e6
	case a >= 1e14:
		return n / 1e3
	case a >= 1e12:
		return n
	default:
		return n * 1000
	}
}

func epochMillisToLocal(ms int64) string {
	return time.UnixMilli(ms).Format(collectedAtLayout)
}
