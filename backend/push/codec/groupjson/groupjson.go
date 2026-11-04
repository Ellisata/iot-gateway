// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package groupjson 实现按采集组输出的 group-json 载荷。
//
// 一帧 = 一个 (设备, 采集组) 的一次轮询快照：帧头带 collectedAt/device/group，
// points 是本组全部点位，正常与坏质量都在其中，下游按 quality 区分
// （< 128 为坏）。点位字段名与 native-json 的 PushPoint 对齐；设备级信息
// 只在帧头出现一次，不随点位重复。这是 Neuron 风格的分组推送格式；
// native-json 的逐点数组契约保持不变，二者互补。
package groupjson

import (
	"encoding/json"
	"fmt"

	"iot-gateway/collector"
	"iot-gateway/push"
)

// Frame 一帧采集组数据。
//
// Points 是本组全部点位（含坏质量点），顺序与批次记录一致。坏点不剔除：
// 其 Value 通常是空串，下游据 Quality < 128 识别，不能用 Value 是否为空判断。
type Frame struct {
	// CollectedAt 采集时间，与 native-json 的同名字段同义（毫秒精度本地时间）；
	// 是帧级字段，同一帧内的点位共享，因此不随点位重复。
	CollectedAt string `json:"collectedAt"`
	DeviceID    string `json:"deviceId"`
	DeviceCode  string `json:"deviceCode"`
	DeviceName  string `json:"deviceName"`
	// Protocol 是 dataType 的命名空间（如 "ModBus.TCP"）；同一帧内的点位来自
	// 同一设备，协议是常量，因此只放帧头。
	Protocol        string  `json:"protocol"`
	ThingModelCode  string  `json:"thingModelCode"`
	GroupID         string  `json:"groupId"`
	GroupCode       string  `json:"groupCode"`
	GroupName       string  `json:"groupName"`
	GroupIntervalMs int     `json:"groupIntervalMs"`
	Points          []Point `json:"points"`
	// Trigger 事件触发来源；仅事件触发定向推送的帧携带，正常采集帧为 nil。
	// 同一批次内所有记录的 Trigger 相同，因此只放帧头，不随点位重复。
	Trigger *collector.RecordTrigger `json:"trigger,omitempty"`
}

// Point 组帧内的一个点位。
//
// 字段名与 native-json 的 PushPoint 一致（native-json 是逐点契约的唯一真源，
// 两处同名同义）。设备级信息（deviceId/deviceCode/deviceName/protocol/
// thingModelCode）与帧级的 collectedAt/trigger 只放帧头，均不随点位重复；
// 需要逐点设备元信息时用 native-json。
type Point struct {
	DeviceAddressID   string `json:"deviceAddressId"`
	DeviceAddressName string `json:"deviceAddressName"`
	Value             string `json:"value"`
	DataType          string `json:"dataType"`
	Kind              string `json:"kind"`
	Quality           int    `json:"quality"`
	PropertyCode      string `json:"propertyCode"`
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

	collectedAt := batch.CollectedAt
	if collectedAt == "" {
		collectedAt = batch.Records[0].CollectedAt
	}
	deviceName := batch.Records[0].DeviceName
	// 帧头设备级字段与 spool.ToPushBatch 同口径：批次列为空时从首条记录回退，
	// 保证断网补发重建的帧与首发的帧一致。
	deviceCode, thingModelCode := batch.DeviceCode, batch.ThingModelCode
	if deviceCode == "" {
		deviceCode = batch.Records[0].DeviceCode
	}
	if thingModelCode == "" {
		thingModelCode = batch.Records[0].ThingModelCode
	}
	groupCode := batch.GroupCode
	if groupCode == "" {
		groupCode = batch.Records[0].GroupCode
	}
	// 同一批次只含一台设备，协议是常量。
	protocol := batch.Records[0].Protocol
	var trigger *collector.RecordTrigger
	for i := range batch.Records {
		if batch.Records[i].Trigger != nil {
			trigger = batch.Records[i].Trigger
			break
		}
	}

	points := make([]Point, 0, len(batch.Records))
	for _, r := range batch.Records {
		points = append(points, Point{
			DeviceAddressID:   r.DeviceAddressID,
			DeviceAddressName: r.DeviceAddressName,
			Value:             r.Value,
			DataType:          r.DataType,
			Kind:              r.Kind,
			Quality:           r.Quality,
			PropertyCode:      r.PropertyCode,
		})
	}

	payload, err := json.Marshal(Frame{
		CollectedAt:     collectedAt,
		DeviceID:        batch.DeviceID,
		DeviceCode:      deviceCode,
		DeviceName:      deviceName,
		Protocol:        protocol,
		ThingModelCode:  thingModelCode,
		GroupID:         batch.GroupID,
		GroupCode:       groupCode,
		GroupName:       batch.GroupName,
		GroupIntervalMs: batch.GroupIntervalMs,
		Points:          points,
		Trigger:         trigger,
	})
	if err != nil {
		return nil, fmt.Errorf("groupjson: marshal frame failed: %w", err)
	}
	return []push.EncodedMessage{{Payload: payload}}, nil
}
