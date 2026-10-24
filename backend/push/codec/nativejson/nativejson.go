// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package nativejson 实现网关的默认 MQTT 载荷格式。
//
// 这里是 native 载荷的唯一实现。字段、顺序、逐条 collectedAt 回落规则
// 与历史 MQTT 通道逐字节一致；改动会破坏下游契约。
package nativejson

import (
	"encoding/json"
	"fmt"

	"iot-gateway/collector"
	"iot-gateway/push"
)

// PushPoint 推送到 MQTT 的单条点位数据。
// 字段顺序即 JSON 输出顺序，不要调整。
type PushPoint struct {
	DeviceID          string `json:"deviceId"`
	DeviceName        string `json:"deviceName"`
	DeviceAddressID   string `json:"deviceAddressId"`
	DeviceAddressName string `json:"deviceAddressName"`
	Value             string `json:"value"`
	DataType          string `json:"dataType"`
	Kind              string `json:"kind"`
	Protocol          string `json:"protocol"`
	// Quality 采集质量码（driver.QualityXxx：类别在高 2 位、成因在低 6 位）。
	// 正常点位恒为 192，报文与引入成因那一维之前**逐字节一致**。
	// 注意坏点位不再是 0：判"坏"要写 quality < 128，不能写 quality == 0。
	Quality     int    `json:"quality"`
	CollectedAt string `json:"collectedAt"`
}

// Encoder native-json 编码器。
type Encoder struct{}

// New 创建 native-json 编码器。
func New() *Encoder { return &Encoder{} }

// Encode 把一批记录序列化为一个 JSON 数组（一条消息）。
func (e *Encoder) Encode(batch push.PushBatch) ([]push.EncodedMessage, error) {
	payload, err := MarshalBatch(batch.Records, batch.CollectedAt)
	if err != nil {
		return nil, err
	}
	return []push.EncodedMessage{{Payload: payload}}, nil
}

// MarshalBatch 将同一设备的采集记录序列化为 JSON 数组。
func MarshalBatch(records []collector.CollectedRecord, now string) ([]byte, error) {
	points := make([]PushPoint, 0, len(records))
	for _, r := range records {
		// 外部订阅记录可携带逐条采集时间；本地点位留空，统一用批次时间。
		collectedAt := r.CollectedAt
		if collectedAt == "" {
			collectedAt = now
		}
		points = append(points, PushPoint{
			DeviceID:          r.DeviceID,
			DeviceName:        r.DeviceName,
			DeviceAddressID:   r.DeviceAddressID,
			DeviceAddressName: r.DeviceAddressName,
			Value:             r.Value,
			DataType:          r.DataType,
			Kind:              r.Kind,
			Protocol:          r.Protocol,
			Quality:           r.Quality,
			CollectedAt:       collectedAt,
		})
	}
	payload, err := json.Marshal(points)
	if err != nil {
		return nil, fmt.Errorf("nativejson: marshal batch failed: %w", err)
	}
	return payload, nil
}
