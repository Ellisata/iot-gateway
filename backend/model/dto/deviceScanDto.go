// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

import "encoding/json"

// ==================== 局域网设备扫描发现 DTO ====================

// StartDeviceScanDTO 发起一次局域网扫描请求。
type StartDeviceScanDTO struct {
	// Protocol 要使用的协议名，必须来自 GET /deviceScan/protocols 的返回值。
	Protocol string `json:"protocol" binding:"required"`

	// Options 扫描选项，形状由该协议的扫描表单（scanFormJson）定义。
	// 与 TestDeviceConnectionDTO.ProtocolJSON 同样是原样透传的 RawMessage ——
	// 不同协议的扫描选项差别很大（Modbus 是站号与超时，广播类协议可能是别的），
	// 服务端不做固定结构解析。
	Options json.RawMessage `json:"options"`

	// Subnets 要扫描的网段（CIDR）。**只能从 GET /deviceScan/subnets 的返回值里选**：
	// 不接受任意 CIDR，否则这个接口就成了一个可以扫任何地址的通用端口扫描器。
	// 留空 = 全部本机网段。
	Subnets []string `json:"subnets"`

	// Port 覆盖协议声明的默认端口。0 或省略 = 用默认端口。
	Port int `json:"port"`
}

// ScanJobQueryDTO 查询扫描任务状态。前端轮询用。
type ScanJobQueryDTO struct {
	JobID string `form:"jobId" binding:"required"`

	// WithDevices 是否在快照里带上已发现的设备与探测失败清单。**默认 true**，
	// 省略即完整快照（与加这个字段之前的行为完全一致）。
	//
	// 轮询应传 false 拿轻量快照：结果清单是只增的，一个扫到几百台的任务每 800ms
	// 回传整份清单，而绝大多数拍子并没有新设备。前端的做法是拿轻量快照里的
	// found（与完整快照同源）比对自己已经持有的那一份，发现数变了再补一次
	// withDevices=true 的请求。
	//
	// **用 *bool 而不是 bool**：省略与显式 false 必须能分开。bool 的零值会把
	// "没传"解释成"不要清单"，那是一处会让既有调用方静默拿不到结果的默认值反转
	//（前端轮询之外的入口 —— 比如把 jobId 贴进浏览器 —— 都是省略的）。
	WithDevices *bool `form:"withDevices"`
}

// CancelDeviceScanDTO 取消扫描任务。
type CancelDeviceScanDTO struct {
	JobID string `json:"jobId" binding:"required"`
}

// CreateDevicesFromScanDTO 把勾选的扫描结果批量建成设备。
type CreateDevicesFromScanDTO struct {
	// JobID 结果所属的扫描任务。服务端**只认自己持有的那份结果**：
	// protocolJson 由服务端从任务快照里取，不采信客户端传来的值。
	JobID string `json:"jobId" binding:"required"`

	// Items 要创建的设备。Index 指的是该任务快照里 Devices 数组的下标。
	Items []ScanDeviceItemDTO `json:"items" binding:"required,min=1"`

	// ConfirmDuplicate 是否已确认"扫描地址与库内某台设备相同，仍要新建"。
	//
	// 省略（false）时这种行**不建**，只作为一条带 code=addressExists 的明细
	// 返回，由用户看过之后带上 true 重提。做成两段式是因为同一地址上多台设备
	// 既合法又常见（Modbus TCP 串口服务器后面挂多个从站、多通道串口服务器的
	// 不同端口、同一台 PLC 同时开 S7 与 Modbus）—— 不提示就默默建是错的，
	// 直接拦死也是错的。判重规则见 service.scanIdentityOf。
	ConfirmDuplicate bool `json:"confirmDuplicate"`
}

// ScanDeviceItemDTO 一条待创建的设备。
type ScanDeviceItemDTO struct {
	// Index 在任务快照 Devices 数组里的下标。
	Index int `json:"index" binding:"min=0"`

	// Name 设备名。留空时服务端按"协议-IP"生成一个。
	Name string `json:"name"`

	// Description 设备描述。
	Description string `json:"description"`
}
