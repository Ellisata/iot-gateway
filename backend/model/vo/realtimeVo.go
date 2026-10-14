// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// RealtimePointVO 单个点位的最新采集值。
//
// 以 device_address 表的活跃点位为基准逐条生成，未采集到的点位同样占位返回
// （collected=false），使前端表格的行数与顺序在轮询之间保持稳定。
type RealtimePointVO struct {
	AddressID      string `json:"addressId"`
	Name           string `json:"name"`
	Label          string `json:"label"` // 点位说明（如温度、电流）
	Value          string `json:"value"` // 最新值；未采集到时为空串，用 collected 区分
	DataType       string `json:"dataType"`
	CommonDataType string `json:"commonDataType"`
	RwPermission   string `json:"rwPermission"`
	ScanFrequency  int    `json:"scanFrequency"`
	// Quality 采集质量码（driver.QualityXxx：类别在高 2 位、成因在低 6 位）。
	// 前端据此把"为什么坏"显示给操作工，而不是只给一个裸的 0。
	Quality   int    `json:"quality"`
	Collected bool   `json:"collected"` // 是否收到过采集值
	Stale     bool   `json:"stale"`     // 值是否已过期（超过 3 个采集周期未更新）
	UpdatedAt string `json:"updatedAt"` // 该值的采集时刻，未采集到为空串
}

// RealtimeSnapshotVO 设备实时数据快照：一次请求同时给出设备状态与本页点位。
//
// 合并成一个响应是刻意的 —— 页面按秒级轮询，拆成两个请求会成倍放大网关与前端开销。
type RealtimeSnapshotVO struct {
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
	ProtocolName string `json:"protocolName"`

	// 三个状态位组合起来可让页面给出确切的「为什么没数据」：
	// collectorRunning=false → 采集未启动；collecting=false → 协议不可用或无活跃点位；
	// online=false → 设备已判离线。
	Online           bool `json:"online"`           // 无 active 断联报警
	Collecting       bool `json:"collecting"`       // 已接入采集调度（协议可用且有活跃点位）
	CollectorRunning bool `json:"collectorRunning"` // 采集引擎是否运行中

	LastSuccessTime string `json:"lastSuccessTime"` // 最近一次成功采集时间（内存态，"" 表示尚无记录）
	ServerTime      string `json:"serverTime"`      // 服务端当前时间，供前端计算数据新鲜度

	Page    int               `json:"page"`
	Size    int               `json:"size"`
	Total   int64             `json:"total"`
	Records []RealtimePointVO `json:"records"`
}
