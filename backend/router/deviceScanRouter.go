// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// DeviceScanRoutes 局域网设备扫描发现 + 设备点位范围扫描路由。
//
// 独立于 /device/* 而不是挂进去：扫描的生命周期（发起→轮询→取消→导入）
// 与设备的增删改查没有共享状态，前端也把它当一个独立页面。
//
// 点扫那三条**刻意不走 /device/* 或 /deviceAddress/**：它们与设备扫描共用
// 总开关（deviceScan.enabled）与错误码，同一个 controller 出口才能保证两个入口
// 对同一种失败的答复一致。路径上仍要分清楚 —— 后端的 /deviceScan 是"扫网段"，
// /deviceScan/*Point* 是"扫设备"。
func DeviceScanRoutes(r gin.IRouter, dsc *controller.DeviceScanController) {
	scan := r.Group("/deviceScan")
	{
		scan.GET("/capabilities", dsc.Capabilities)
		scan.POST("/start", dsc.Start)
		scan.POST("/continue", dsc.Continue)
		scan.GET("/status", dsc.Status)
		scan.POST("/cancel", dsc.Cancel)
		scan.POST("/createDevices", dsc.CreateDevices)

		// 设备点位范围扫描（设备地址页的「自动发现点位」）
		scan.GET("/pointCapability", dsc.PointCapability)
		scan.POST("/discoverPoints", dsc.DiscoverPoints)
		scan.POST("/createAddresses", dsc.CreateAddresses)
	}
}
