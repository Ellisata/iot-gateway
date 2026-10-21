// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
	"iot-gateway/middleware"
	"iot-gateway/service"
	"iot-gateway/web"
)

// RouteOption 路由选项，用于按需注册领域路由
type RouteOption func(*gin.Engine)

// SetupRouter 配置全局路由
//
//	接收 RouteOption 切片，新增模块仅需在 ProvideRouteOptions 追加一行即可，
//	无需修改 SetupRouter 签名。
func SetupRouter(opts []RouteOption) *gin.Engine {
	r := gin.New()

	// 注册中间件
	r.Use(gin.Recovery())
	r.Use(middleware.CorsMiddleware())
	r.Use(middleware.RequestLogMiddleware())
	r.Use(middleware.AreaMiddleware())
	// 全局 JWT 认证：除登录/健康检查/前端静态资源外，所有 API 统一校验 token
	r.Use(middleware.AuthMiddleware())

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 注册领域路由
	for _, opt := range opts {
		opt(r)
	}

	// 前端 SPA：所有 /app/* 路径由嵌入的 Vue 前端处理
	app := r.Group("/admin")
	app.GET("/*filepath", web.ServeStatic("/admin"))

	return r
}

// WithUserRoutes 用户模块路由选项
func WithUserRoutes(uc *controller.UserController) RouteOption {
	return func(r *gin.Engine) {
		UserRoutes(r, uc)
	}
}

// WithCollectionRoutes 采集控制模块路由选项
func WithCollectionRoutes(cc *controller.CollectionController) RouteOption {
	return func(r *gin.Engine) {
		CollectionRoutes(r, cc)
	}
}

// WithRealtimeRoutes 设备实时数据模块路由选项
func WithRealtimeRoutes(rc *controller.RealtimeController) RouteOption {
	return func(r *gin.Engine) {
		RealtimeRoutes(r, rc)
	}
}

// WithDeviceRoutes 设备对象模块路由选项
func WithDeviceRoutes(dc *controller.DeviceController) RouteOption {
	return func(r *gin.Engine) {
		DeviceRoutes(r, dc)
	}
}

// WithProtocolRoutes 协议模块路由选项
func WithProtocolRoutes(pc *controller.ProtocolController) RouteOption {
	return func(r *gin.Engine) {
		ProtocolRoutes(r, pc)
	}
}

// WithDeviceAddressRoutes 设备地址模块路由选项
func WithDeviceAddressRoutes(ac *controller.DeviceAddressController) RouteOption {
	return func(r *gin.Engine) {
		DeviceAddressRoutes(r, ac)
	}
}

// WithPushChannelRoutes 数据推送通道模块路由选项
func WithPushChannelRoutes(pc *controller.PushChannelController) RouteOption {
	return func(r *gin.Engine) {
		PushChannelRoutes(r, pc)
	}
}

// WithPushChannelFormRoutes 数据推送通道表单模块路由选项
func WithPushChannelFormRoutes(pcf *controller.PushChannelFormController) RouteOption {
	return func(r *gin.Engine) {
		PushChannelFormRoutes(r, pcf)
	}
}

// WithPushRoutes 数据推送模块路由选项
func WithPushRoutes(pc *controller.PushController) RouteOption {
	return func(r *gin.Engine) {
		PushRoutes(r, pc)
	}
}

// WithMqttSubscriptionRoutes MQTT 订阅管理模块路由选项
func WithMqttSubscriptionRoutes(mc *controller.MqttSubscriptionController) RouteOption {
	return func(r *gin.Engine) {
		MqttSubscriptionRoutes(r, mc)
	}
}

// WithAlarmWebhookRoutes 报警通知配置模块路由选项
func WithAlarmWebhookRoutes(awc *controller.AlarmWebhookController) RouteOption {
	return func(r *gin.Engine) {
		AlarmWebhookRoutes(r, awc)
	}
}

// WithAlarmWebhookFormRoutes 报警通知表单模块路由选项
func WithAlarmWebhookFormRoutes(awfc *controller.AlarmWebhookFormController) RouteOption {
	return func(r *gin.Engine) {
		AlarmWebhookFormRoutes(r, awfc)
	}
}

// WithAlarmRoutes 设备断联报警模块路由选项
func WithAlarmRoutes(ac *controller.AlarmController) RouteOption {
	return func(r *gin.Engine) {
		AlarmRoutes(r, ac)
	}
}

// WithLogFileRoutes 日志查看模块路由选项
func WithLogFileRoutes(lc *controller.LogFileController) RouteOption {
	return func(r *gin.Engine) {
		LogFileRoutes(r, lc)
	}
}

// WithOpenApiSecretRoutes 开放接口密钥模块路由选项
func WithOpenApiSecretRoutes(oasc *controller.OpenApiSecretController) RouteOption {
	return func(r *gin.Engine) {
		OpenApiSecretRoutes(r, oasc)
	}
}

// WithOpenApiRoutes 开放接口路由选项（X-Api-Key 密钥鉴权，不走 JWT）
func WithOpenApiRoutes(oass *service.OpenApiSecretService, oac *controller.OpenApiController) RouteOption {
	return func(r *gin.Engine) {
		OpenApiRoutes(r, oass, oac)
	}
}

// WithGatewayRoutes 网关自身身份模块路由选项
func WithGatewayRoutes(gc *controller.GatewayController) RouteOption {
	return func(r *gin.Engine) {
		GatewayRoutes(r, gc)
	}
}

// WithPluginRoutes 协议插件安装模块路由选项
func WithPluginRoutes(plc *controller.PluginController) RouteOption {
	return func(r *gin.Engine) {
		PluginRoutes(r, plc)
	}
}

// WithDeviceScanRoutes 局域网设备扫描发现模块路由选项
func WithDeviceScanRoutes(dsc *controller.DeviceScanController) RouteOption {
	return func(r *gin.Engine) {
		DeviceScanRoutes(r, dsc)
	}
}

// WithSystemConfigRoutes 运行期配置开关模块路由选项
func WithSystemConfigRoutes(scc *controller.SystemConfigController) RouteOption {
	return func(r *gin.Engine) {
		SystemConfigRoutes(r, scc)
	}
}

// WithDashboardRoutes 大盘聚合数据模块路由选项
func WithDashboardRoutes(dbc *controller.DashboardController) RouteOption {
	return func(r *gin.Engine) {
		DashboardRoutes(r, dbc)
	}
}

// WithAgentApiRoutes dsh（AI Agent）调用面路由选项（**无鉴权**）。
//
// 与其余 With* 的差别：它是**整组**挂载 —— 把各领域路由的同一份定义再挂一遍到
// /agentApi 前缀下，而不是新写一套路由表。风险与设计取舍见 agentApiRouter.go。
func WithAgentApiRoutes(
	uc *controller.UserController,
	dc *controller.DeviceController,
	ac *controller.DeviceAddressController,
	cc *controller.CollectionController,
	rc *controller.RealtimeController,
	pc *controller.ProtocolController,
	pc2 *controller.PushChannelController,
	pcf *controller.PushChannelFormController,
	msc *controller.MqttSubscriptionController,
	pus *controller.PushController,
	ac2 *controller.AlarmController,
	awc *controller.AlarmWebhookController,
	awfc *controller.AlarmWebhookFormController,
	lc *controller.LogFileController,
	oasc *controller.OpenApiSecretController,
	gc *controller.GatewayController,
	plc *controller.PluginController,
	dsc *controller.DeviceScanController,
	scc *controller.SystemConfigController,
	dbc *controller.DashboardController,
) RouteOption {
	return func(r *gin.Engine) {
		AgentApiRoutes(r, uc, dc, ac, cc, rc, pc, pc2, pcf, msc, pus, ac2, awc, awfc, lc, oasc, gc, plc, dsc, scc, dbc)
	}
}
