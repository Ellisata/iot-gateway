// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package groupjson 实现按采集组输出的 group-json 载荷。
//
// 一帧 = 一个 (设备, 采集组) 的一次轮询快照：帧头带 timestamp/device/group，
// 点位按质量码分成 values（正常）与 errors（质量码）两个字典。这是 Neuron
// 风格的分组推送格式；native-json 的逐点数组契约保持不变，二者互补。
package groupjson

import (
	"encoding/json"
	"fmt"

	"iot-gateway/push"
)

// Frame 一帧采集组数据。
//
// Values 的键是点位名（DeviceAddressName，库内按设备唯一；为空时回退 address id），
// Errors 的键相同，值为采集质量码 —— 下游据此区分「哪个点没读到、为什么」。
type Frame struct {
	Timestamp  string            `json:"timestamp"`
	DeviceID   string            `json:"deviceId"`
	DeviceName string            `json:"deviceName"`
	GroupID    string            `json:"groupId"`
	GroupName  string            `json:"groupName"`
	IntervalMs int               `json:"intervalMs"`
	Values     map[string]string `json:"values"`
	Errors     map[string]int    `json:"errors"`
}

// Encoder group-json 编码器。
type Encoder struct{}

// New 创建 group-json 编码器。
func New() *Encoder { return &Encoder{} }

// Encode 把一批记录编码为一帧；空批不产出消息。
func (e *Encoder) Encode(batch push.PushBatch) ([]push.EncodedMessage, error) {
	if len(batch.Records) == 0 {
		return nil, nil
	}

	timestamp := batch.CollectedAt
	if timestamp == "" {
		timestamp = batch.Records[0].CollectedAt
	}
	deviceName := batch.Records[0].DeviceName

	values := make(map[string]string, len(batch.Records))
	errs := make(map[string]int)
	for _, r := range batch.Records {
		key := r.DeviceAddressName
		if key == "" {
			key = r.DeviceAddressID
		}
		if r.Quality >= 128 {
			values[key] = r.Value
		} else {
			errs[key] = r.Quality
		}
	}

	payload, err := json.Marshal(Frame{
		Timestamp:  timestamp,
		DeviceID:   batch.DeviceID,
		DeviceName: deviceName,
		GroupID:    batch.GroupID,
		GroupName:  batch.GroupName,
		IntervalMs: batch.GroupIntervalMs,
		Values:     values,
		Errors:     errs,
	})
	if err != nil {
		return nil, fmt.Errorf("groupjson: marshal frame failed: %w", err)
	}
	return []push.EncodedMessage{{Payload: payload}}, nil
}
