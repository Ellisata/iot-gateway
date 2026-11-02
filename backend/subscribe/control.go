// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"context"

	"iot-gateway/model/dto"
	"iot-gateway/model/vo"
)

// ControlWriter 控制下发能力。
//
// 定义在 subscribe 侧而不是直接 import service，是为了避免
// subscribe → service → subscribe 的编译期循环。生产实现是 *service.ControlService，
// 由 wire 按方法集做结构绑定。
type ControlWriter interface {
	Write(ctx context.Context, req *dto.ControlDTO, source, operator string) (*vo.ControlResultVO, error)
}

// controlPayload MQTT 下行控制报文。
//
// 两种写法二选一：
//
//	{"deviceId":"...","requestId":"...","verify":true,
//	 "items":[{"deviceAddressId":"...","value":"42"}]}
//
// 或单点简写：
//
//	{"deviceId":"...","requestId":"...","addressId":"...","value":"42"}
//
// requestId 必填：MQTT QoS1 会重投，没有幂等键就无法阻止重复写 PLC。
type controlPayload struct {
	DeviceID  string               `json:"deviceId"`
	RequestID string               `json:"requestId"`
	Verify    bool                 `json:"verify"`
	Items     []dto.ControlItemDTO `json:"items"`

	// 单点简写
	AddressID string `json:"addressId"`
	Value     string `json:"value"`
}
