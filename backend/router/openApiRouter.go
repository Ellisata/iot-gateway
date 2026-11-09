// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
	"iot-gateway/middleware"
	"iot-gateway/service"
)

// OpenApiRoutes 开放接口路由：仅做 X-Api-Key 密钥鉴权（不走 JWT），
// 供外部系统调用。该分组即对外白名单——默认关闭，只有显式挂载到
// 此分组下的接口才对外开放；新接口在此追加。
//
// 除 /control/write（控制下发，高风险管理动作）与 /thingModel/*（物模型配置
// 写入 / 删除）外，其余接口均为只读查询。
func OpenApiRoutes(r gin.IRouter, oass *service.OpenApiSecretService, oac *controller.OpenApiController) {
	openApi := r.Group("/openApi", middleware.OpenApiAuthMiddleware(oass))
	{
		// 对外只读查询
		openApi.GET("/device/page", oac.PageDevices)
		openApi.POST("/device/names", oac.ListDeviceNames)
		openApi.POST("/deviceAddress/labels", oac.ListAddressLabels)
		openApi.GET("/deviceAddress/page", oac.PageDeviceAddresses)
		// 全量同步：游标分批返回「设备 + 其全部点位」（见 docs/openApi.md 4.5）
		openApi.GET("/device/sync", oac.SyncDevicePoints)
		// 全量同步：游标分批返回「订阅设备 + 其全部点位」，数据源是订阅报文
		// 维护的元信息索引（见 docs/openApi.md 4.12）
		openApi.GET("/subscription/devicePoint/sync", oac.SyncSubscriptionDevicePoints)
		// 可靠性分析：报警口径的设备可用率排行 / 点位异常 top-N，供外部跨网关汇总
		// （见 docs/openApi.md 4.6 / 4.7）
		openApi.GET("/alarm/deviceReliability", oac.DeviceReliability)
		openApi.GET("/alarm/pointAbnormal", oac.PointAbnormal)
		// 报警明细：历史分页 / 当前活跃 / 越限记录。与 4.6/4.7 的聚合报表互补 ——
		// 那两条回答"谁最差"，这三条回答"具体是哪几条报警"（见 docs/openApi.md 4.9-4.11）。
		openApi.GET("/alarm/pageAlarm", oac.PageAlarm)
		openApi.GET("/alarm/listActive", oac.ListActiveAlarm)
		openApi.GET("/alarm/pageThresholdRecord", oac.PageThresholdRecord)

		// 控制下发（**唯一会改变现场设备状态的对外接口**）。
		// 仍需 control.enabled + 协议 Writable + 点位 W 权限三层同时满足。
		openApi.POST("/control/write", oac.WriteControl)

		// 物模型配置写入：按 code / identifier upsert 物模型与属性。
		// 只改网关配置、不触达现场设备；每个模型一个事务，跨模型部分成功；
		// 请求中未出现的属性不删除（见 docs/openApi.md 4.13）。
		openApi.POST("/thingModel/batchUpsert", oac.BatchUpsertThingModels)

		// 物模型配置删除（见 docs/openApi.md 4.14 / 4.15）：按自然键批量删除
		// 模型（连带其属性）或单个属性；逐条独立，绑定保护与单条删除一致。
		openApi.POST("/thingModel/batchDelete", oac.BatchDeleteThingModels)
		openApi.POST("/thingModelProperty/batchDelete", oac.BatchDeleteThingModelProperties)
	}
}
