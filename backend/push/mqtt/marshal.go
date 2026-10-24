// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"iot-gateway/collector"
	"iot-gateway/push/codec/nativejson"
)

// pushPoint 保留本包内别名，供既有测试反序列化 native 载荷。
type pushPoint = nativejson.PushPoint

// marshalBatch 保留本包入口，实际实现在 nativejson（唯一真源）。
func marshalBatch(records []collector.CollectedRecord, now string) ([]byte, error) {
	return nativejson.MarshalBatch(records, now)
}
