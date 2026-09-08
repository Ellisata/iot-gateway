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
func OpenApiRoutes(r *gin.Engine, oass *service.OpenApiSecretService, oac *controller.OpenApiController) {
	openApi := r.Group("/openApi", middleware.OpenApiAuthMiddleware(oass))
	{
		// 对外只读查询
		openApi.GET("/device/page", oac.PageDevices)
		openApi.POST("/device/names", oac.ListDeviceNames)
		openApi.POST("/deviceAddress/labels", oac.ListAddressLabels)
		openApi.GET("/deviceAddress/page", oac.PageDeviceAddresses)
	}
}
