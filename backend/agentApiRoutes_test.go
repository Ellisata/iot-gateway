// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"iot-gateway/controller"
	"iot-gateway/router"
	"iot-gateway/service"
)

// newTestEngine 用**真实的** ProvideRouteOptions + SetupRouter 组装引擎。
//
// 全部 controller 传 nil：路由注册只是把「方法值」登记到 gin 的树里
// （nil 接收者的方法值在 Go 里合法），handler 要到真正被调用时才会解引用，
// 所以这个测试不需要数据库、配置或任何运行时依赖 —— 而它断言的是**真实接线**，
// 不是测试里另抄一份的路由表。
func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return router.SetupRouter(ProvideRouteOptions(
		(*controller.UserController)(nil),
		(*controller.DeviceController)(nil),
		(*controller.DeviceAddressController)(nil),
		(*controller.CollectionController)(nil),
		(*controller.RealtimeController)(nil),
		(*controller.ProtocolController)(nil),
		(*controller.PushChannelController)(nil),
		(*controller.PushChannelFormController)(nil),
		(*controller.PushController)(nil),
		(*controller.AlarmController)(nil),
		(*controller.AlarmWebhookController)(nil),
		(*controller.AlarmWebhookFormController)(nil),
		(*controller.LogFileController)(nil),
		(*controller.OpenApiSecretController)(nil),
		(*service.OpenApiSecretService)(nil),
		(*controller.OpenApiController)(nil),
		(*controller.GatewayController)(nil),
		(*controller.PluginController)(nil),
		(*controller.DeviceScanController)(nil),
	))
}

// registeredRoutes 返回 "METHOD /path" 的集合。
func registeredRoutes(e *gin.Engine) map[string]bool {
	set := make(map[string]bool)
	for _, ri := range e.Routes() {
		set[ri.Method+" "+ri.Path] = true
	}
	return set
}

// isMirroredElsewhere 标记那些**不该**出现在 /agentApi 下的路径。
//
//   - /health、/admin/*：一个裸探活、一个前端静态资源，都不是领域 API；
//   - /openApi/*：它自带 X-Api-Key 中间件，挂到 /agentApi 下会得到一条
//     "既多一层前缀、又仍要密钥"的路径，对 dsh 没有意义（理由见 agentApiRouter.go）。
//
// 注意 /openApiSecret/* 不在此列 —— 它以 /openApiSecret 开头，不是 /openApi/ 分段，
// 属于内部接口，应当在 /agentApi 下有镜像。
func isMirroredElsewhere(path string) bool {
	return path == "/health" ||
		path == "/admin" || strings.HasPrefix(path, "/admin/") ||
		strings.HasPrefix(path, "/openApi/")
}

// TestAgentApiMirrorsInternalRoutes 是 agentApiRouter.go 的**防漂移守卫**。
//
// 它守的是一件很容易发生、且发生时没有任何报错的事：往 wire.go 的
// ProvideRouteOptions 里加了一条新的 WithXxxRoutes，却忘了在
// AgentApiRoutes 里也挂一行 —— 于是内部接口有了、dsh 那边没有，
// 表现为"dsh 说这个接口不存在"，而网关一切正常。
//
// 反过来也守：AgentApiRoutes 里挂了某个领域、但 ProvideRouteOptions 里没有它，
// 或者前缀拼错（/agentapi、/agent_api），这里同样会红。
func TestAgentApiMirrorsInternalRoutes(t *testing.T) {
	routes := registeredRoutes(newTestEngine())

	const prefix = router.AgentApiPrefix

	var internal, mirrored int
	var missing []string
	for key := range routes {
		// 拆出 method 与 path（path 里不会含空格）
		method, path, ok := strings.Cut(key, " ")
		if !ok {
			t.Fatalf("路由键格式意外: %q", key)
		}
		if strings.HasPrefix(path, prefix) {
			mirrored++
			continue
		}
		if isMirroredElsewhere(path) {
			continue
		}
		internal++
		if !routes[method+" "+prefix+path] {
			missing = append(missing, method+" "+path)
		}
	}

	if len(missing) > 0 {
		t.Errorf("有 %d 个内部接口没有出现在 %s 下（在 AgentApiRoutes 里补挂，或在 isMirroredElsewhere 里说明理由）：\n  %s",
			len(missing), prefix, strings.Join(missing, "\n  "))
	}

	// 两个方向都要非空：任一侧为 0 说明路由根本没注册上，
	// 只比较"缺不缺"会把这个更严重的情况当成通过。
	if internal == 0 {
		t.Fatal("内部路由为 0 —— 路由注册整体失效，测试失去意义")
	}
	if mirrored == 0 {
		t.Fatalf("%s 下一条路由都没有 —— AgentApiRoutes 没被 ProvideRouteOptions 调用，或挂载失败", prefix)
	}
	t.Logf("内部路由 %d 条，%s 下镜像 %d 条", internal, prefix, mirrored)
}

// TestAgentApiIsUnauthenticated 钉住本次改动的**核心承诺**：/agentApi 不走凭据校验。
//
// 上面那条守卫只保证"路由挂上了"；挂上了却被 AuthMiddleware 拦住同样是坏的
// —— 对 dsh 而言两者都是"调不通"，但成因完全不同（一个改 agentApiRouter.go，
// 一个改 authMiddleware.go）。这条把后半段钉死。
//
// 用**哨兵 handler** 而不是真接口来探：真 handler 的 controller 是 nil，
// 一调就 panic（靠 gin.Recovery 兜住）。那样虽然也能断言"不是 401"，
// 但会打出一大段 panic 栈，让后来的人误以为测试坏了。
// 哨兵只回一个固定状态码，把"是否被鉴权挡下"这件事从"handler 能否跑通"里剥离出来。
func TestAgentApiIsUnauthenticated(t *testing.T) {
	engine := newTestEngine()

	// 同一个 handler 挂两个位置：内部根路径 与 /agentApi 镜像路径。
	const sentinel = 299
	hit := func(c *gin.Context) { c.Status(sentinel) }

	const internalProbe = "/__authProbe"
	agentProbe := router.AgentApiPrefix + internalProbe
	engine.GET(internalProbe, hit)
	engine.GET(agentProbe, hit)

	// 内部路径：不带 token 必须被 JWT 拦下
	if got := doRequest(engine, "GET", internalProbe); got != 401 {
		t.Errorf("GET %s 无 token 得到 %d，期望 401 —— 内部接口的 JWT 校验失效了",
			internalProbe, got)
	}

	// /agentApi 镜像：不带 token 必须放行到 handler（拿到哨兵状态码）
	if got := doRequest(engine, "GET", agentProbe); got != sentinel {
		t.Errorf("GET %s 无 token 得到 %d，期望 %d（直抵 handler）—— "+
			"/agentApi 免鉴权失效，检查 middleware.isPublicPath 里的 agentApiPrefix 分支",
			agentProbe, got, sentinel)
	}
}

// doRequest 发一个请求并返回状态码，全程不走网络。
func doRequest(e *gin.Engine, method, path string) int {
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		panic(err)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w.Code
}

// TestAgentApiSurfaceIsComplete 是 agentApiSurface.go 分类表的**完整性守卫**。
//
// 这张表是 dsh 判断"能不能直接调"的唯一依据，而它是一份**手写的**清单 ——
// 手写清单的宿命是漂移。这里用两个方向把它钉住：
//
//	① 路由有、表里没有 → dsh 遇到一个没有分类的接口，只能猜（或直接报错拒调）
//	② 表里有、路由没有   → dsh 按清单去调一个 404
//
// 两种漂移都不会有任何运行时症状，只有 dsh 那边会莫名其妙地失败，
// 而排查方向会指向 dsh 而不是这张表。所以必须由测试来咬。
func TestAgentApiSurfaceIsComplete(t *testing.T) {
	routes := registeredRoutes(newTestEngine())

	// 表里声明的（去掉 /agentApi 前缀，还原成键的形式）
	declared := map[string]bool{}
	for _, s := range router.AgentApiSurface() {
		rel := strings.TrimPrefix(s.Path, router.AgentApiPrefix)
		if rel == s.Path {
			t.Fatalf("清单条目 %q 不以 %s 开头 —— AgentApiSurface 拼路径时漏了前缀",
				s.Path, router.AgentApiPrefix)
		}
		declared[s.Method+" "+rel] = true
	}

	// 引擎里真实注册的（还原成与表相同的 "METHOD /相对路径" 形式）
	actual := map[string]bool{}
	for key := range routes {
		method, path, ok := strings.Cut(key, " ")
		if !ok || !strings.HasPrefix(path, router.AgentApiPrefix+"/") {
			continue
		}
		actual[method+" "+strings.TrimPrefix(path, router.AgentApiPrefix)] = true
	}

	var unclassified, stale []string
	for key := range actual {
		if !declared[key] {
			unclassified = append(unclassified, key)
		}
	}
	for key := range declared {
		if !actual[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)

	if len(unclassified) > 0 {
		t.Errorf("有 %d 条 /agentApi 路由没有分类（在 agentApiSurface.go 里登记）：\n  %s",
			len(unclassified), strings.Join(unclassified, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("有 %d 条分类指向了不存在的路由（路由已删/改名，分类表没跟上）：\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}

	if len(actual) == 0 {
		t.Fatal("引擎里没有 /agentApi 路由，测试失去意义")
	}
	t.Logf("/agentApi 路由 %d 条，分类表 %d 条", len(actual), len(declared))
}

// TestAgentApiSurfaceConfirmDerivation 钉住"确认与否"的推导规则。
//
// Confirm 刻意不写进表里（见 SurfaceEntry 的注释）：一处规则好过 83 处抄写。
// 但"不写"就意味着它只能靠推导 —— 这条测试确保推导没有反过来把 read 也算成要确认，
// 那会让 dsh 对每个查询都弹一次确认，实际结果是使用者把确认当噪音点掉。
func TestAgentApiSurfaceConfirmDerivation(t *testing.T) {
	var read, needConfirm int
	for _, s := range router.AgentApiSurface() {
		wantConfirm := s.Access != router.AccessRead
		if s.Confirm != wantConfirm {
			t.Errorf("%s %s: Confirm=%v 但 Access=%s（期望 Confirm=%v）",
				s.Method, s.Path, s.Confirm, s.Access, wantConfirm)
		}
		switch s.Access {
		case router.AccessRead:
			read++
		case router.AccessProbe, router.AccessWrite:
			needConfirm++
		default:
			t.Errorf("%s %s: 未知的 Access 取值 %q", s.Method, s.Path, s.Access)
		}
	}
	if read == 0 || needConfirm == 0 {
		t.Fatalf("read=%d needConfirm=%d —— 两个方向都应有条目，否则推导规则没被真正验证", read, needConfirm)
	}
	t.Logf("read %d 条，需确认 %d 条", read, needConfirm)
}

// TestAgentApiSurfaceParamsWellFormed 守参数表的**结构不变量**。
//
// 参数表是 dsh 拼请求的依据，错一条就是一次 400 —— 或更糟：把
// updateAlarmWebhook 的 secret 传成 "" 会清空密钥。反射导出的表有它的好处
// （与 DTO 同源、不会漂移），但也有它的症状：某个 DTO 字段被改掉 tag 之后，
// 表里会**静默**少一个参数或掉一条约束，而网关侧一切正常，只有 dsh 那边开始猜。
//
// 这里守机器能判的那部分。语义对不对（desc 说得准不准、该不该 required）只能靠人看。
func TestAgentApiSurfaceParamsWellFormed(t *testing.T) {
	validIn := map[router.ParamIn]bool{
		router.ParamInPath:  true,
		router.ParamInQuery: true,
		router.ParamInBody:  true,
		router.ParamInForm:  true,
	}

	var total, withParams, nested int
	for _, s := range router.AgentApiSurface() {
		id := s.Method + " " + s.Path

		if s.Params == nil {
			// 正常走不到这里：AgentApiSurface 对 nil 直接 panic。留着是为了
			// 万一哪天那条 panic 被去掉，这条测试仍能给出可读的失败。
			t.Errorf("%s: Params 为 nil —— 无参数的接口应显式写 argNone()", id)
			continue
		}
		if len(s.Params) > 0 {
			withParams++
		}

		seen := map[string]bool{}
		var hasBody, hasForm bool
		var declaredPath []string

		var walk func(ps []router.ParamSpec, prefix string)
		walk = func(ps []router.ParamSpec, prefix string) {
			for _, p := range ps {
				key := prefix + p.Name
				if p.Name == "" || p.Type == "" {
					t.Errorf("%s: 参数 %q 缺 name 或 type —— 反射没能识别它的类型", id, key)
					continue
				}
				if !validIn[p.In] {
					t.Errorf("%s: 参数 %s 的 in=%q 非法（只允许 path/query/body/form）", id, key, p.In)
				}
				if seen[string(p.In)+" "+key] {
					t.Errorf("%s: 参数 %s 在同一位置重复声明", id, key)
				}
				seen[string(p.In)+" "+key] = true

				switch p.In {
				case router.ParamInPath:
					declaredPath = append(declaredPath, p.Name)
					if !strings.Contains(s.Path, ":"+p.Name) {
						t.Errorf("%s: 声明了路径参数 %s，但路由串里没有 :%s", id, p.Name, p.Name)
					}
				case router.ParamInBody:
					hasBody = true
				case router.ParamInForm:
					hasForm = true
				}

				total++
				if len(p.Fields) > 0 {
					nested++
					walk(p.Fields, key+".")
				}
			}
		}
		walk(s.Params, "")

		// body 与 form 在 HTTP 上互斥：multipart 接口没有 JSON 请求体。
		// 两个都声明会让 dsh 同时发两份，其中一份必然被忽略。
		if hasBody && hasForm {
			t.Errorf("%s: 同时声明了 body 与 form 参数 —— multipart 接口没有 JSON 请求体", id)
		}

		// 路径参数必须与路由串里的 :占位符**一一对应**：漏一条 dsh 不知道该填，
		// 多一条 dsh 会去找一个不存在的参数。两者都是它拿到 404 之后才会发现。
		wantPath := make([]string, 0, 1)
		for _, seg := range strings.Split(s.Path, "/") {
			if name, ok := strings.CutPrefix(seg, ":"); ok && name != "" {
				wantPath = append(wantPath, name)
			}
		}
		sort.Strings(wantPath)
		sort.Strings(declaredPath)
		if !slices.Equal(declaredPath, wantPath) {
			t.Errorf("%s: 路径参数 %v 与路由串里的占位符 %v 不一致", id, declaredPath, wantPath)
		}
	}

	// 下限守卫：整套机制有可能"静默退化"成满表 argNone() —— 那时上面每一条
	// 仍然全绿，而 dsh 又回到靠猜。故钉一个下限，并单独钉住几条**本次改动
	// 就是为它们而做**的接口（见 agentApiParams.go 文件头）。
	if withParams == 0 {
		t.Fatal("没有任何接口声明了参数 —— 参数表整体失效，测试失去意义")
	}
	if total < 100 {
		t.Errorf("全部参数只有 %d 条，远低于预期 —— 反射导出可能大面积失败了", total)
	}

	mustDeclare := []struct {
		route string
		param string
		in    router.ParamIn
	}{
		{"POST /agentApi/deviceAddress/createDeviceAddress", "rwPermission", router.ParamInBody},
		{"POST /agentApi/deviceScan/createDevices", "items", router.ParamInBody},
		{"POST /agentApi/alarmWebhook/updateAlarmWebhook", "secret", router.ParamInBody},
		{"POST /agentApi/plugin/importPlugin", "files", router.ParamInForm},
		{"POST /agentApi/deviceAddress/importDeviceAddress", "file", router.ParamInForm},
		{"POST /agentApi/deviceAddress/importDeviceAddress", "deviceId", router.ParamInForm},
	}
	routes := map[string]router.SurfaceRoute{}
	for _, s := range router.AgentApiSurface() {
		routes[s.Method+" "+s.Path] = s
	}
	for _, want := range mustDeclare {
		s, ok := routes[want.route]
		if !ok {
			t.Errorf("%s: 清单里没有这条路由（改名了？）", want.route)
			continue
		}
		idx := slices.IndexFunc(s.Params, func(p router.ParamSpec) bool {
			return p.Name == want.param && p.In == want.in
		})
		if idx < 0 {
			t.Errorf("%s: 缺少参数 %s（in=%s）—— 这正是本次加参数表要解决的问题",
				want.route, want.param, want.in)
		}
	}

	// oneof 必须被翻成 enum：rwPermission 是 R/W/RW 三选一，写成别的值服务端直接拒。
	// 它是"靠猜必错"的典型 —— 模型的第一反应是 "read" / "write"。
	if s, ok := routes["POST /agentApi/deviceAddress/createDeviceAddress"]; ok {
		idx := slices.IndexFunc(s.Params, func(p router.ParamSpec) bool { return p.Name == "rwPermission" })
		if idx >= 0 && !slices.Equal(s.Params[idx].Enum, []string{"R", "W", "RW"}) {
			t.Errorf("rwPermission 的 enum 为 %v，期望 [R W RW]（binding tag 里的 oneof 没被翻出来）",
				s.Params[idx].Enum)
		}
	}

	t.Logf("参数 %d 条（%d 条路由带参数，嵌套展开 %d 层次）", total, withParams, nested)
}
