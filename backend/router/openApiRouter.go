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
// 此分组下的接口才对外开放；新接口经 OpenApiService 只读扩展后在此追加。
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
		// 可靠性分析：报警口径的设备可用率排行 / 点位异常 top-N，供外部跨网关汇总
		// （见 docs/openApi.md 4.6 / 4.7）
		openApi.GET("/alarm/deviceReliability", oac.DeviceReliability)
		openApi.GET("/alarm/pointAbnormal", oac.PointAbnormal)
	}
}
