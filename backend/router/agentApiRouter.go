// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
)

// AgentApiPrefix dsh（AI Agent）调用面的路径前缀。
const AgentApiPrefix = "/agentApi"

// AgentApiRoutes 把领域路由整体挂到 /agentApi 前缀下，供 dsh 调用。
//
// ⚠️ 这个分组**不经任何鉴权**（在 middleware.isPublicPath 里按前缀放行）。
// 任何能访问到网关端口的主机都可以调用下表的全部接口，包括：
//   - POST /agentApi/plugin/importPlugin —— 上传并加载可执行文件（等价于远程代码执行）
//   - GET  /agentApi/device/deleteDevice/:id —— 删除设备（静默停掉产线采集）
//   - GET  /agentApi/device/pageDevice —— 返回的 DeviceVO **带 protocolJson**（PLC 连接配置）
//   - GET  /agentApi/pushChannel/* —— PushChannelVO.configJson 含 MQTT 口令等凭据
//   - POST /agentApi/user/createUser、PUT /agentApi/user/password
//   - POST /agentApi/systemConfig/update —— 改全局运行开关（可关闭局域网发现、
//     设备扫描、变化才推送，或让插件在下次重启后全部失效）
//
// 这是**刻意的设计选择**，不是遗漏：前提是网关部署在隔离的工业内网，
// 或 dsh 与网关同机。**若网关会暴露到不可信网络，这个分组必须先加回鉴权**
// —— 最省事的做法是把下面的 AgentApiRoutes 改成挂 OpenApiAuthMiddleware
// （见 openApiRouter.go），revoke 时只需在 isPublicPath 去掉一行前缀。
//
// 与 /openApi 的分工：那是给第三方系统开发者的**只读**对外契约（X-Api-Key 鉴权、
// 脱敏 VO、带兼容性承诺）；本分组是给 dsh 的**全量**调用面（含全部写接口、
// 不脱敏）。两者受众与风险面不同，不要互相替代。
//
// **接口清单是显式白名单**（照 openApiRouter.go 的先例）：新增领域路由不会自动
// 出现在这里，必须在本函数里显式追加一行。这样 dsh 的暴露面永远是可审阅的一处，
// 而不是"随着别处加路由悄悄变大"。
//
// 复用各领域的 XxxRoutes 而非复制一遍路由表：复制必然漂移，而漂移在这里的
// 后果是"dsh 调到的接口与内部接口行为不一致"。各 XxxRoutes 的入参已放宽为
// gin.IRouter，*gin.Engine 与 *gin.RouterGroup 都满足它，故同一份路由定义
// 既能挂根路径，也能挂这里。
func AgentApiRoutes(
	r *gin.Engine,
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
) {
	g := r.Group(AgentApiPrefix)
	{
		// 接口清单：dsh 用它判断哪些接口只读、哪些要先问人。
		// **必须挂在领域路由之前**——不依赖顺序，只是让读到这里的人先看见它。
		// 分类表本身在 agentApiSurface.go，完整性由测试守住。
		g.GET(surfacePath, SurfaceHandler)

		// 用户
		UserRoutes(g, uc)

		// 设备与点位
		DeviceRoutes(g, dc)
		DeviceAddressRoutes(g, ac)

		// 采集与实时值
		CollectionRoutes(g, cc)
		RealtimeRoutes(g, rc)

		// 大盘聚合（只读）
		DashboardRoutes(g, dbc)

		// 协议与推送
		ProtocolRoutes(g, pc)
		PushChannelRoutes(g, pc2)
		PushChannelFormRoutes(g, pcf)
		MqttSubscriptionRoutes(g, msc)
		PushRoutes(g, pus)

		// 报警
		AlarmRoutes(g, ac2)
		AlarmWebhookRoutes(g, awc)
		AlarmWebhookFormRoutes(g, awfc)

		// 运维
		LogFileRoutes(g, lc)
		OpenApiSecretRoutes(g, oasc)
		GatewayRoutes(g, gc)
		PluginRoutes(g, plc)

		// 局域网设备扫描 + 点位扫描
		DeviceScanRoutes(g, dsc)

		// 运行期配置开关（update / reset 是 write，见 agentApiSurface.go）
		SystemConfigRoutes(g, scc)

		// 刻意不挂 OpenApiRoutes：它自带 X-Api-Key 鉴权中间件，挂进来会得到
		// /agentApi/openApi/* 这样一条既多一层前缀、又仍要密钥的路径 —— 对 dsh
		// 没有意义（它的只读需求已由上表的不脱敏接口覆盖）。
	}
}
