// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// ABB RWS 插件（driversrc/abb）的集成测试。
//
// 这里编译并加载的是**真实的插件源码产物**，不是测试替身 —— 唯一能在测试里
// 替代它的是一个重写了同样逻辑的冒牌货，那样的测试即使插件写错了也会通过。
//
// 与其它插件的 parity 测试不同，本文件**不读黄金基线**：ABB.RWS 是全新协议，
// 没有"内置实现被移除之前冻下来的"结果可以对照。等价性这条约束对它不成立，
// 取而代之的是两条：插件能不能被正确加载/注销（本文件前半），
// 以及能不能跑通完整采集链路（后半）。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

const (
	abbProtocol = "ABB.RWS"
	abbScope    = "abb"
	abbDir      = "./driversrc/abb"
)

// TestABBPluginRegistersProtocol 插件被加载后，协议、scope、类型映射、
// 配置表单都要在网关侧就位。
//
// 断言的全部是**运行时事实**（driver 注册表 + 数据库 + Status()），
// 不依赖任何预置文件 —— 这正是新协议与搬迁协议在验收方式上的区别。
func TestABBPluginRegistersProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	m, db := startPluginManager(t, abbDir, nil)
	waitForDriverRegistered(t, abbProtocol)

	// Status 应报告一个插件、一个协议、正确的 scope。
	st := m.Status()
	if len(st) != 1 {
		t.Fatalf("Status 条目数 = %d, want 1", len(st))
	}
	if len(st[0].Protocols) != 1 || st[0].Protocols[0] != abbProtocol {
		t.Fatalf("插件协议 = %v, want [%s]", st[0].Protocols, abbProtocol)
	}
	if st[0].Scope != abbScope {
		t.Errorf("scope = %q, want %q", st[0].Scope, abbScope)
	}
	if !st[0].Up {
		t.Error("插件应处于运行状态")
	}

	// 驱动可创建，且 scope 反查正确（前端按 scope 取类型下拉）。
	if _, err := driver.Create(abbProtocol); err != nil {
		t.Fatalf("Create(%s) 失败: %v", abbProtocol, err)
	}
	if got := driver.ScopeOf(abbProtocol); got != abbScope {
		t.Errorf("ScopeOf(%s) = %q, want %q", abbProtocol, got, abbScope)
	}

	// RWS 走 HTTP，不独占串行总线。声明成 true 不会报错，
	// 只会让同一插件的所有设备白白丢掉并发。
	d, err := driver.Create(abbProtocol)
	if err != nil {
		t.Fatalf("Create(%s) 失败: %v", abbProtocol, err)
	}
	if se, ok := d.(driver.SerialExclusive); ok {
		if se.SerialExclusive() {
			t.Error("ABB.RWS 走 HTTP，不该独占串行总线")
		}
	}

	// iot_protocol 行由 Manager 依据 Meta 自动建立 —— 不需要任何 SQL 迁移。
	var row po.IotProtocol
	if err := db.Where("name = ?", abbProtocol).First(&row).Error; err != nil {
		t.Fatalf("协议行应由 Manager 自动建立: %v", err)
	}
	if row.FormJSON == "" || row.FormJSON == "{}" {
		t.Error("协议行的配置表单为空 —— 前端会渲染不出设备表单")
	}
	// 插件供给的行排在内置播种行之后（defaultPluginRowSort），
	// 免得插件的协议把运维手建的排序挤乱。
	if row.Sort != defaultPluginRowSort {
		t.Errorf("协议行 sort = %d, want %d", row.Sort, defaultPluginRowSort)
	}

	// 类型映射：通用名 → 内部裸名，且能在插件进程的类型表里解析出来。
	for common, internal := range map[string]string{
		"Boolean": "bool",
		"Float":   "float",
		"Int":     "", // 不是通用名，走扩展组
		"Auto":    "auto",
	} {
		if internal == "" {
			continue
		}
		got, ok := driver.LookupDataType(abbProtocol, common)
		if !ok || got != internal {
			t.Errorf("LookupDataType(%s, %s) = (%q, %v), 期望 %q", abbProtocol, common, got, ok, internal)
		}
	}

	common, extended := driver.AvailableTypes(abbScope)
	if len(common) == 0 {
		t.Error("通用类型列表为空 —— 前端类型下拉会缺项")
	}
	// Auto 不在 CommonDataTypes 里，落在扩展组（按字母序）。
	found := false
	for _, name := range extended {
		if name == "Auto" {
			found = true
		}
	}
	if !found {
		t.Errorf("扩展类型 %v 里应有 Auto（它是本协议的主推类型）", extended)
	}
}

// TestABBPluginUnregistersProtocol Stop 必须把协议与类型 scope 一并摘掉。
//
// 残留协议的表现最迷惑人：driver.Create 仍然成功（插件行还在注册表里），
// 但底层进程已死，于是设备一直连不上 —— 而日志里什么异常都没有。
func TestABBPluginUnregistersProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	m, _ := startPluginManager(t, abbDir, nil)
	waitForDriverRegistered(t, abbProtocol)

	// 前置：一切都在。
	if _, err := driver.Create(abbProtocol); err != nil {
		t.Fatalf("前置条件不成立: %v", err)
	}
	if got := driver.ScopeOf(abbProtocol); got != abbScope {
		t.Fatalf("前置条件：scope = %q", got)
	}

	m.Stop()

	if _, err := driver.Create(abbProtocol); err == nil {
		t.Errorf("Stop 后 %s 仍可创建（协议未注销）", abbProtocol)
	}
	if got := driver.ScopeOf(abbProtocol); got != "" {
		t.Errorf("Stop 后 scope 仍为 %q（类型映射未注销）", got)
	}
	if _, ok := driver.LookupDataType(abbProtocol, "Boolean"); ok {
		t.Error("Stop 后插件类型映射应已注销")
	}
}

// TestABBPluginCollectsThroughEngine 走完整采集链路：
// 采集引擎 → RPC → 插件进程 → HTTP → RWS 假控制器 → 采集记录。
//
// 与 abbdrv 的单测分工：那里直连驱动验**协议语义**（批量优先、回退、质量码），
// 这里验**网关侧的接入** —— 任务编排、锁粒度、记录上报、协议名透传。
// 两者缺一不可：驱动写得再对，接不进采集引擎也等于没做。
func TestABBPluginCollectsThroughEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 模式跳过需要编译插件的测试")
	}

	srv := startFakeRWS(t)

	bin := buildPlugin(t, abbDir)
	db := newTestDB(t)
	for _, m := range []any{&po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	engine := collector.NewEngine(db, pool, sink, noopStateSink{}, nil, nil)

	m := NewManager(db, pluginConfigFor(filepath.Dir(bin)), engine)
	t.Cleanup(m.Stop)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, abbProtocol)

	var proto po.IotProtocol
	if err := db.Where("name = ?", abbProtocol).First(&proto).Error; err != nil {
		t.Fatalf("插件协议行应由 Manager 自动建立: %v", err)
	}

	dev := po.Device{
		Name:       "ABB 机器人",
		ProtocolID: proto.ID,
		ProtocolJSON: fmt.Sprintf(
			`{"endpoint":%q,"username":"Default User","password":"robotics","rwsVersion":"1.0"}`, srv.URL),
		Status: 1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}

	// 五个命名空间各取一个点位：覆盖插件里的全部取值路径。
	specs := []struct{ name, dataType, label string }{
		{"signal:Local/DRV_1/DI1", "bool", "夹具到位"},
		{"signal:Local/DRV_1/AI1", "float", "气压"},
		{"panel:ctrlstate", "string", "控制器状态"},
		{"motion:ROB_1/joint/rax_1", "float", "1 轴角度"},
		{"system:rwversion", "string", "RobotWare 版本"},
	}
	addrs := make([]po.DeviceAddress, 0, len(specs))
	for _, s := range specs {
		a := po.DeviceAddress{
			Name:          s.name,
			Label:         s.label,
			DeviceID:      dev.ID,
			DataType:      s.dataType,
			ScanFrequency: 200,
			Status:        1,
		}
		if err := db.Create(&a).Error; err != nil {
			t.Fatalf("创建地址 %s: %v", s.name, err)
		}
		addrs = append(addrs, a)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	waitFor(t, 20*time.Second, "全部点位采到", func() bool {
		return len(sink.snapshot()) >= len(addrs)
	})

	got := make(map[string]collector.CollectedRecord)
	for _, r := range sink.snapshot() {
		got[r.DeviceAddressID] = r
	}

	wantValue := map[string]string{
		"signal:Local/DRV_1/DI1":   "1",
		"signal:Local/DRV_1/AI1":   "12.500000",
		"panel:ctrlstate":          "motoron",
		"motion:ROB_1/joint/rax_1": "1.5",
		"system:rwversion":         "7.12.0",
	}

	for _, a := range addrs {
		r, ok := got[a.ID]
		if !ok {
			t.Errorf("地址 %s 缺采集记录", a.Name)
			continue
		}
		if r.Quality != driver.QualityGood {
			t.Errorf("地址 %s Quality = %d, want %d", a.Name, r.Quality, driver.QualityGood)
		}
		if r.DataType != a.DataType {
			t.Errorf("地址 %s DataType = %q, want %q", a.Name, r.DataType, a.DataType)
		}
		// 协议名透传：这是区分"记录来自插件链路"的唯一标记。
		if r.Protocol != abbProtocol {
			t.Errorf("地址 %s Protocol = %q, want %q", a.Name, r.Protocol, abbProtocol)
		}
		if r.DeviceAddressName != a.Name {
			t.Errorf("地址名透传有误：%q, want %q", r.DeviceAddressName, a.Name)
		}
		if w, ok := wantValue[a.Name]; ok && r.Value != w {
			t.Errorf("地址 %s Value = %q, want %q", a.Name, r.Value, w)
		}
	}

	// 会话创建是**按连接**发生的，不是按轮次：RWS 1.0 + sessionRequest=auto
	// 会在每次 Connect 后发一次。若它变成了按轮次（200ms 一轮，1 秒内会有
	// 好几轮），控制器的会话槽会被迅速耗光 —— 所以这里卡一个上界。
	if n := srv.sessionRequests(); n < 1 || n > 3 {
		t.Errorf("会话创建次数 = %d，期望 1~3（按连接一次，绝不按轮次）", n)
	}
}

// ==================== 假 RWS 控制器 ====================

// fakeController 一个只提供采集所需端点的最小 ABB 控制器。
//
// 它不实现认证（插件配置里给了凭据，但没有挑战时不会带）—— 这里要验的是
// **网关侧的接入**，认证链路由 abbdrv 的单测覆盖（那里有会独立重算摘要的
// 假控制器）。
type fakeController struct {
	*httptest.Server
	sessions atomic.Int64
}

func (f *fakeController) sessionRequests() int { return int(f.sessions.Load()) }

func startFakeRWS(t *testing.T) *fakeController {
	t.Helper()
	f := &fakeController{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/rw/elog/0" {
			// 计数发生在 HTTP 处理协程里，而断言在读 —— 必须原子。
			f.sessions.Add(1)
		}
		_, _ = w.Write([]byte(rwsFixture(r.URL.Path)))
	}))
	t.Cleanup(f.Close)
	return f
}

// rwsFixture 按路径返回 HAL 风格的响应，形状对齐真实 RWS。
func rwsFixture(path string) string {
	switch path {
	case "/rw/system":
		return `{"_embedded":{"_state":[{"rwversion":"7.12.0"}]}}`
	case "/rw/iosystem/signals":
		return `{"_embedded":{"_state":[` +
			`{"name":"DI1","unitnm":"DRV_1","network":"Local","lvalue":"1","quality":"1","type":"DI"},` +
			`{"name":"AI1","unitnm":"DRV_1","network":"Local","lvalue":"12.500000","quality":"1","type":"AI"}` +
			`]}}`
	case "/rw/panel/ctrl-state":
		return `{"_embedded":{"_state":[{"ctrlstate":"motoron"}]}}`
	case "/rw/motionsystem/mechunits/ROB_1/jointtarget":
		return `{"_embedded":{"_state":[{"rax_1":"1.5","rax_2":"0.0"}]}}`
	}
	return `{"_embedded":{"_state":[{}]}}`
}
