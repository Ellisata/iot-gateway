// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/response"
	"iot-gateway/service"
)

// DeviceScanController 局域网设备扫描发现控制器
type DeviceScanController struct {
	deviceScanService *service.DeviceScanService
}

// NewDeviceScanController 创建局域网设备扫描控制器
func NewDeviceScanController(deviceScanService *service.DeviceScanService) *DeviceScanController {
	return &DeviceScanController{deviceScanService: deviceScanService}
}

// Capabilities 扫描页初始化：可扫描的协议、本机网段、功能开关。
//
// 三个信息合成一个响应，是因为进页面时都要拿到 —— 分三个接口就是三次鉴权
// 加三次往返，而它们本身都是本地查询。
func (ctr *DeviceScanController) Capabilities(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.deviceScanService.Capabilities(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Start 发起扫描。
//
// **登记任务即返回**，不等扫描结束：一个 /24 网段的扫描是十几秒到几十秒的量级，
// 而 http.Server.Shutdown 不会杀掉在飞的 handler，同步等待会把停机拖到超时。
// 前端拿到 jobId 后轮询 /deviceScan/status。
func (ctr *DeviceScanController) Start(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.StartDeviceScanDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	data, err := ctr.deviceScanService.Start(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Status 查询扫描进度与结果（前端轮询）。
//
// withDevices 省略即完整快照；前端轮询传 false 只取进度与计数，发现数变化时
// 再补一次完整请求 —— 理由见 dto.ScanJobQueryDTO.WithDevices。
func (ctr *DeviceScanController) Status(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.ScanJobQueryDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	withDevices := req.WithDevices == nil || *req.WithDevices
	data, err := ctr.deviceScanService.Status(ctx, req.JobID, withDevices)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// Cancel 取消扫描。
func (ctr *DeviceScanController) Cancel(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.CancelDeviceScanDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	if err := ctr.deviceScanService.Cancel(ctx, req.JobID); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(nil))
}

// CreateDevices 把勾选的扫描结果批量建成设备。
//
// 逐条成败明细走统一响应，**HTTP 始终 200** —— 与 Excel 导入、插件上传一致：
// 部分成功是预期结果，不是请求级错误。
func (ctr *DeviceScanController) CreateDevices(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.CreateDevicesFromScanDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	result, err := ctr.deviceScanService.CreateDevices(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}

// ==================== 点位范围扫描 ====================
//
// 三个 handler 与上面那组同构（同一页面、同一开关、同一套前端组件），
// 只是作用对象从"网段里的设备"换成"某台设备上的点位"。

// PointCapability 查询某个协议的点位扫描能力。
//
// **不支持是正常答案（200 + supported=false），不是错误**：前端进页面就问一次，
// 据它决定按钮是可用、置灰还是给出理由。
func (ctr *DeviceScanController) PointCapability(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.PointCapabilityQueryDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	data, err := ctr.deviceScanService.PointCapability(ctx, req.ProtocolName)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// DiscoverPoints 对一台已存在的设备发起一次范围扫描。
//
// **同步返回**（与 Start 的"登记任务即返回"相反）：插件侧一次扫描覆盖整段、
// 没有中途取结果的入口，做不成可轮询的任务。因此它的耗时必须可控 ——
// 网关侧把预算钳在 9s 以内，见 pluginhost 的 pointScanGatewayBudgetCapMs。
// 前端也要相应地把这个请求的 axios 超时放宽（axios 默认 15s 够用，但要显式覆盖，
// 免得将来有人在全局把它调小）。
func (ctr *DeviceScanController) DiscoverPoints(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.DiscoverPointsDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	data, err := ctr.deviceScanService.DiscoverPoints(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// CreateAddresses 把勾选的点位批量建成 device_address 行。
//
// 与 CreateDevices 一致：逐条成败明细走统一响应，**HTTP 始终 200** ——
// 部分成功是预期结果，不是请求级错误。
func (ctr *DeviceScanController) CreateAddresses(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.CreateAddressesFromScanDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}

	result, err := ctr.deviceScanService.CreateAddresses(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(result))
}
