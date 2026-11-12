// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"testing"
)

// TestOpenApiRoutesRegistered 钉住对外接口的**方法 + 路径**。
//
// `/openApi/*` 是发给第三方系统的契约：路径改一个字，对方就会 404，而网关侧一切正常
// （本地测试、管理端、/agentApi 都不走这条路，没有任何症状）。所以这里对着**真实接线**
// （newTestEngine 用的是 ProvideRouteOptions + SetupRouter，不是另抄一份路由表）断言。
//
// 说明为什么没有走 TestAgentApiMirrorsInternalRoutes 那张表：那条守卫**刻意排除**了
// /openApi/*（见 isMirroredElsewhere），它只保证"内部接口都有 /agentApi 镜像"，
// 对外接口不在它的守备范围内 —— 所以需要这条。
func TestOpenApiRoutesRegistered(t *testing.T) {
	routes := registeredRoutes(newTestEngine(true))

	want := []string{
		"GET /openApi/device/page",
		"POST /openApi/device/names",
		"POST /openApi/deviceAddress/labels",
		"GET /openApi/deviceAddress/page",
		"GET /openApi/device/sync",
		"GET /openApi/subscription/devicePoint/sync",
		"GET /openApi/alarm/deviceReliability",
		"GET /openApi/alarm/pointAbnormal",
		"GET /openApi/alarm/pageAlarm",
		"GET /openApi/alarm/listActive",
		"GET /openApi/alarm/pageThresholdRecord",
		"POST /openApi/control/write",
		"POST /openApi/thingModel/batchUpsert",
		"POST /openApi/thingModel/batchDelete",
		"POST /openApi/thingModelProperty/batchDelete",
		// 配置同步（管理平台统一配置管理）
		"GET /openApi/config/capabilities",
		"GET /openApi/config/protocols",
		"GET /openApi/thingModel/page",
		"GET /openApi/deviceConfig/page",
		"GET /openApi/pushChannel/page",
		"GET /openApi/pushChannel/types",
		"GET /openApi/pushChannel/forms",
		"GET /openApi/alarmWebhook/page",
		"GET /openApi/alarmWebhook/types",
		"GET /openApi/alarmWebhook/forms",
		"GET /openApi/systemConfig/list",
		"POST /openApi/pushChannel/batchUpsert",
		"POST /openApi/pushChannel/batchDelete",
		"POST /openApi/alarmWebhook/batchUpsert",
		"POST /openApi/alarmWebhook/batchDelete",
		"POST /openApi/systemConfig/batchUpdate",
		"POST /openApi/systemConfig/batchReset",
		"POST /openApi/deviceConfig/batchUpsert",
		"POST /openApi/deviceConfig/batchDelete",
		// 协议插件二进制下发（管理平台按平台选包）
		"GET /openApi/plugin/info",
		"GET /openApi/plugin/status",
		"POST /openApi/plugin/importPlugin",
	}
	for _, key := range want {
		if !routes[key] {
			t.Errorf("对外接口 %s 没有注册 —— 第三方按文档调用会拿到 404", key)
		}
	}
}
