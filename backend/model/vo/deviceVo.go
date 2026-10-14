// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// DeviceVO 设备响应
type DeviceVO struct {
	ID              string `json:"id"`
	IotGatewayID    string `json:"iotGatewayId"`
	Name            string `json:"name"`
	ProtocolID      string `json:"protocolId"`
	ProtocolName    string `json:"protocolName"`
	ProtocolJSON    string `json:"protocolJson"`
	Description     string `json:"description"`
	Status          int    `json:"status"`
	Online          bool   `json:"online"`          // 当前是否在线（无 active 断联报警）
	LastSuccessTime string `json:"lastSuccessTime"` // 最近一次成功采集时间（内存态，"" 表示尚无成功记录）
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// DeviceOverviewVO 设备在线情况统计响应
type DeviceOverviewVO struct {
	Total       int     `json:"total"`       // 全部设备数（启用 + 禁用）
	Enabled     int     `json:"enabled"`     // 启用设备数（status=1）
	Disabled    int     `json:"disabled"`    // 禁用设备数（status != 1）
	Collected   int     `json:"collected"`   // 启用且已接入采集调度、有活跃地址的设备数
	Online      int     `json:"online"`      // 采集正常（无 active 断联报警）
	Offline     int     `json:"offline"`     // 已判定离线
	UnCollected int     `json:"unCollected"` // 启用但未接入采集（协议不支持/无驱动/无活跃地址）
	OnlineRate  float64 `json:"onlineRate"`  // 在线率 = online / collected（保留 2 位小数）
}

// DeviceAddressVO 设备地址响应
//
// 报警条件三列是**平铺**的（而 DTO 里是嵌套对象）：VO 是表格行的投影，其余字段都是平铺的；
// DTO 之所以嵌套只是因为输入需要「整块未传」这一态。这个不对称是刻意的。
type DeviceAddressVO struct {
	ID               string `json:"id"`
	DeviceID         string `json:"deviceId"`
	Name             string `json:"name"`
	Label            string `json:"label"` // 地址 name 的中文说明（如温度、电流）
	CommonDataType   string `json:"commonDataType"`
	DataType         string `json:"dataType"`
	RwPermission     string `json:"rwPermission"`
	ScanFrequency    int    `json:"scanFrequency"`
	Description      string `json:"description"`
	Status           int    `json:"status"`
	ThresholdEnabled int    `json:"thresholdEnabled"` // 0/1
	ThresholdOp      string `json:"thresholdOp"`      // range|gt|ge|lt|le|eq|ne|in
	// ThresholdValue 操作数的 JSON 数组字符串（如 ["10","40"]），由前端自行解析展示 ——
	// 与库里存的原样一致，不做二次包装（包装成对象会让 PO→VO 的映射多一层无谓转换）。
	ThresholdValue string `json:"thresholdValue"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// BatchDeleteAddressResultVO 批量删除设备地址的结果
//
// Deleted 由实际删除的行数得出，可能小于 Requested：勾选后又被人删掉的地址
// 不在删除集里，这种情况是**正常**的（批量删除刻意不因个别 id 不存在而失败），
// 前端据此提示"少删了几条"，而不是把它当错误。
type BatchDeleteAddressResultVO struct {
	Requested int   `json:"requested"`
	Deleted   int64 `json:"deleted"`
}

// BatchDeleteDeviceResultVO 批量删除设备的结果
//
// Deleted 的语义同 BatchDeleteAddressResultVO.Deleted：按实际删除行数给出，
// 可能小于 Requested，前端提示"少删了几条"而不是当成错误。
//
// Addresses 是**随设备一并删掉的点位行数**（表间无外键，不显式删就会留下
// 查不到的孤立行，见 DeviceService.DeleteDevice）。单独返回是为了让确认框能
// 在删之前说清"连带删掉多少点位"、结果里能说清"连带删了多少"—— 只报设备数
// 会让一次点了 3 台设备、实际抹掉 400 个点位的操作看起来只是删了 3 行。
type BatchDeleteDeviceResultVO struct {
	Requested int   `json:"requested"`
	Deleted   int64 `json:"deleted"`
	Addresses int64 `json:"addresses"`
}
