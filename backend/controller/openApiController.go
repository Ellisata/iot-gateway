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

// OpenApiController 开放接口控制器（供外部系统凭密钥调用）。
//
// 除控制下发与物模型批量 upsert 外均为只读查询。控制下发仍受 control.enabled、
// 协议 Writable、点位 rw_permission 与物模型属性 access_mode 四层约束，见
// service.ControlService；物模型 upsert 只维护配置，不触达现场设备。
type OpenApiController struct {
	openApiService    *service.OpenApiService
	controlService    *service.ControlService
	thingModelService *service.ThingModelService
	configService     *service.OpenApiConfigService
}

// NewOpenApiController 创建开放接口控制器
func NewOpenApiController(
	openApiService *service.OpenApiService,
	controlService *service.ControlService,
	thingModelService *service.ThingModelService,
	configService *service.OpenApiConfigService,
) *OpenApiController {
	return &OpenApiController{
		openApiService:    openApiService,
		controlService:    controlService,
		thingModelService: thingModelService,
		configService:     configService,
	}
}

// Capabilities 配置同步能力协商。
func (ctr *OpenApiController) Capabilities(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.configService.Capabilities(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageThingModelsForConfig 物模型配置分页读（含属性与 contentHash）。
func (ctr *OpenApiController) PageThingModelsForConfig(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiConfigPageDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.PageThingModels(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageDeviceConfigs 设备配置分页读（设备 + 采集组 + 点位 + contentHash）。
func (ctr *OpenApiController) PageDeviceConfigs(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiDeviceConfigPageDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.PageDeviceConfigs(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PagePushChannelsForConfig 推送通道配置分页读（config 脱敏 + contentHash）。
func (ctr *OpenApiController) PagePushChannelsForConfig(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiPushChannelPageDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.PagePushChannels(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListPushChannelForms 推送通道动态表单。
func (ctr *OpenApiController) ListPushChannelForms(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.configService.ListPushChannelForms(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListPushChannelTypes 推送通道类型元数据。
func (ctr *OpenApiController) ListPushChannelTypes(c *gin.Context) {
	c.JSON(200, response.Success(ctr.configService.ListPushChannelTypes()))
}

// PageAlarmWebhooksForConfig 告警 Webhook 配置分页读（secret 脱敏 + contentHash）。
func (ctr *OpenApiController) PageAlarmWebhooksForConfig(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiAlarmWebhookPageDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.PageAlarmWebhooks(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListAlarmWebhookForms 告警 Webhook 动态表单。
func (ctr *OpenApiController) ListAlarmWebhookForms(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.configService.ListAlarmWebhookForms(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListAlarmWebhookTypes 告警 Webhook 类型元数据。
func (ctr *OpenApiController) ListAlarmWebhookTypes(c *gin.Context) {
	c.JSON(200, response.Success(ctr.configService.ListAlarmWebhookTypes()))
}

// ListSystemConfigForOpenApi 运行开关全量读。
func (ctr *OpenApiController) ListSystemConfigForOpenApi(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.configService.ListSystemConfig(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListProtocolsForConfig 协议与点位数据类型。
func (ctr *OpenApiController) ListProtocolsForConfig(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.configService.ListProtocols(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchUpsertPushChannels 推送通道批量 upsert（自然键 type/name）。
func (ctr *OpenApiController) BatchUpsertPushChannels(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiPushChannelBatchUpsertDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchUpsertPushChannels(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchDeletePushChannels 推送通道批量删除。
func (ctr *OpenApiController) BatchDeletePushChannels(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiPushChannelBatchDeleteDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchDeletePushChannels(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchUpsertAlarmWebhooks 告警 Webhook 批量 upsert（自然键 name）。
func (ctr *OpenApiController) BatchUpsertAlarmWebhooks(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiAlarmWebhookBatchUpsertDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchUpsertAlarmWebhooks(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchDeleteAlarmWebhooks 告警 Webhook 批量删除。
func (ctr *OpenApiController) BatchDeleteAlarmWebhooks(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiAlarmWebhookBatchDeleteDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchDeleteAlarmWebhooks(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchUpdateSystemConfig 运行开关批量修改。
func (ctr *OpenApiController) BatchUpdateSystemConfig(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiSystemConfigBatchUpdateDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchUpdateSystemConfig(ctx, &req, "openapi")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchResetSystemConfig 运行开关批量恢复默认。
func (ctr *OpenApiController) BatchResetSystemConfig(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiSystemConfigBatchResetDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchResetSystemConfig(ctx, &req, "openapi")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchUpsertDeviceConfigs 设备配置批量 upsert（设备 + 采集组 + 点位）。
func (ctr *OpenApiController) BatchUpsertDeviceConfigs(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiDeviceConfigBatchUpsertDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchUpsertDeviceConfigs(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchDeleteDeviceConfigs 设备配置批量删除。
func (ctr *OpenApiController) BatchDeleteDeviceConfigs(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiDeviceConfigBatchDeleteDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.configService.BatchDeleteDeviceConfigs(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageDevices 对外设备分页查询
func (ctr *OpenApiController) PageDevices(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageDeviceDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PageOpenApiDevices(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListDeviceNames 批量查询设备名称（JSON Body 传设备ID数组）
func (ctr *OpenApiController) ListDeviceNames(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiDeviceNamesDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.ListOpenApiDeviceNames(ctx, req.IDs)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListAddressLabels 批量查询点位标签（JSON Body 传设备ID数组 + 点位ID数组）
func (ctr *OpenApiController) ListAddressLabels(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiAddressLabelsDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.ListOpenApiAddressLabels(ctx, req.DeviceIDs, req.AddressIDs)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageDeviceAddresses 对外设备地址分页查询（按设备ID）
func (ctr *OpenApiController) PageDeviceAddresses(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageDeviceAddressDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PageOpenApiDeviceAddresses(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// SyncDevicePoints 对外「设备 + 点位」游标分批同步查询（供外部系统全量遍历）
func (ctr *OpenApiController) SyncDevicePoints(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiSyncDevicePointsDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.SyncDevicePoints(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// SyncSubscriptionDevicePoints 对外「订阅设备 + 点位」游标分批同步查询（供外部系统全量遍历）
func (ctr *OpenApiController) SyncSubscriptionDevicePoints(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiSyncSubscriptionDevicePointsDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.SyncSubscriptionDevicePoints(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// DeviceReliability 对外设备维度可靠性统计（可用率 / 故障段数 / 停机时长 / MTTR）
func (ctr *OpenApiController) DeviceReliability(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.DeviceReliabilityDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.DeviceReliability(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PointAbnormal 对外点位维度异常 top-N（采集质量异常与超限分开计数）
func (ctr *OpenApiController) PointAbnormal(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PointAbnormalDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PointAbnormal(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageAlarm 对外报警历史分页查询。
func (ctr *OpenApiController) PageAlarm(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageAlarmDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PageAlarms(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// ListActiveAlarm 对外当前活跃离线报警（= 离线设备 + 离线通道）。
func (ctr *OpenApiController) ListActiveAlarm(c *gin.Context) {
	ctx := c.Request.Context()
	data, err := ctr.openApiService.ListActiveAlarms(ctx)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// PageThresholdRecord 对外越限记录分页查询。
func (ctr *OpenApiController) PageThresholdRecord(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.PageThresholdRecordDTO
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.openApiService.PageThresholdRecords(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchUpsertThingModels 对外物模型与属性批量 upsert。
//
// 按 code / identifier 作自然键：没有则创建，有则更新；每个模型一个事务，
// 跨模型部分成功。只维护配置，不删除请求中未出现的属性，也不触达现场设备。
func (ctr *OpenApiController) BatchUpsertThingModels(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiThingModelBatchUpsertDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.thingModelService.BatchUpsertOpenApiThingModels(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchDeleteThingModels 对外物模型批量删除（按 code）。
//
// 删除模型会连带删除其属性；已被设备绑定或其属性已被点位绑定的模型会被拒绝。
// 逐条结果互不影响，顶层响应仍为成功信封。
func (ctr *OpenApiController) BatchDeleteThingModels(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiThingModelBatchDeleteDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.thingModelService.BatchDeleteOpenApiThingModels(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// BatchDeleteThingModelProperties 对外物模型属性批量删除（按 modelCode + identifier）。
func (ctr *OpenApiController) BatchDeleteThingModelProperties(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.OpenApiThingModelPropertyBatchDeleteDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.thingModelService.BatchDeleteOpenApiThingModelProperties(ctx, &req)
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}

// WriteControl 对外控制下发。
//
// 这是开放接口里**唯一会改变现场设备状态**的端点。仍受 control.enabled、
// 协议 Writable、点位 rw_permission 与物模型属性 access_mode 约束；审计 source
// 记为 "openapi"。
// X-Api-Key 中间件只做存在性校验，不注入操作者名称，因此 operator 固定为
// "openapi"（不记录密钥本身，避免审计表泄露凭据）。
func (ctr *OpenApiController) WriteControl(c *gin.Context) {
	ctx := c.Request.Context()
	var req dto.ControlDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	data, err := ctr.controlService.Write(ctx, &req, "openapi", "openapi")
	if err != nil {
		c.JSON(200, appError.HandleErrorCtx(ctx, err))
		return
	}
	c.JSON(200, response.Success(data))
}
