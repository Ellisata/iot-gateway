// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package middleware

import "testing"

// TestIsPublicPath 钉住免认证双名单。
//
// 这条断言看着琐碎，但它是**唯一**守住「哪些路径不校验凭据」的地方：
// isPublicPath 是放行的唯一出口，一旦这里多放行一个前缀，对应接口就整片
// 变成匿名可调，而网关不会报任何错 —— 直到有人发现端口开着。
//
// 尤其 /agentApi 这一组是**刻意无鉴权**的（前提是隔离内网/同机），
// 它必须放行；但也正因为它是刻意的，更需要一条测试把它钉住，
// 免得日后有人把它当"误放行"顺手删掉、或反过来误以为别的前缀也该放行。
func TestIsPublicPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
		why  string
	}{
		// —— 免认证：这三个前缀是设计如此 ——
		{"/health", true, "健康检查，供探活"},
		{"/user/login", true, "登录本身不能要求已登录"},
		{"/admin", true, "内嵌前端入口"},
		{"/admin/assets/index.js", true, "前端静态资源"},

		// /agentApi：本次新增的 dsh 调用面，刻意的无鉴权
		{"/agentApi", true, "dsh 调用面根路径"},
		{"/agentApi/device/pageDevice", true, "dsh 调用面：设备分页"},
		{"/agentApi/plugin/importPlugin", true, "dsh 调用面：插件上传（风险最高的一条，见 agentApiRouter.go）"},

		// /openApi 免的是 JWT，不是免 X-Api-Key —— 它有自己的中间件
		{"/openApi/device/page", true, "对外只读，走 X-Api-Key"},

		// —— 必须要求鉴权的（回归防线）——
		{"/device/pageDevice", false, "内部接口必须走 JWT"},
		{"/device/deleteDevice/abc", false, "删除必须走 JWT"},
		{"/plugin/importPlugin", false, "内部插件上传必须走 JWT"},
		{"/user/password", false, "改密必须走 JWT"},
		{"/gateway/get", false, "网关身份必须走 JWT"},

		// 前缀匹配必须卡在路径分段边界上，不能靠字符串包含
		{"/agentApiFoo", false, "不是 /agentApi 分段，不能误放行"},
		{"/agentApiX/device", false, "同上，前缀必须到 / 为止"},
		{"/administrator", false, "不是 /admin 分段，不能误放行"},
		{"/openApiSecret/list", false, "密钥管理是内部接口，必须走 JWT"},
	}

	for _, c := range cases {
		if got := isPublicPath(c.path); got != c.want {
			t.Errorf("isPublicPath(%q) = %v, 期望 %v（%s）", c.path, got, c.want, c.why)
		}
	}
}
