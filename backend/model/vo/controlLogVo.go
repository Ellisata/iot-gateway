// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// ControlLogVO 一条控制下发审计记录。
//
// 只暴露审计所需的字段；不含任何协议内部连接配置或凭据。
type ControlLogVO struct {
	ID        string `json:"id"`
	RequestID string `json:"requestId"`
	Source    string `json:"source"`
	Operator  string `json:"operator"`

	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	ProtocolName string `json:"protocolName"`

	AddressID    string `json:"addressId"`
	AddressName  string `json:"addressName"`
	AddressLabel string `json:"addressLabel"`
	DataType     string `json:"dataType"`
	RwPermission string `json:"rwPermission"`

	RequestValue  string `json:"requestValue"`
	ResultValue   string `json:"resultValue"`
	ReadbackValue string `json:"readbackValue"`
	Verified      bool   `json:"verified"`

	Code      int    `json:"code"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}
