// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"unsafe"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

const (
	signalsList = "/rw/iosystem/signals"
	jointtarget = "/rw/motionsystem/mechunits/ROB_1/jointtarget"
	rapidSymbol = "/rw/rapid/symbol/data/RAPID/T_ROB1/Mod1/nTest"
)

// connectDriver 建一个连到假控制器的驱动，并在 Connect 之后清空请求记录 ——
// 后续断言关心的都是**采集**产生了哪些请求，不该被探测/验证的往返干扰。
func connectDriver(t *testing.T, f *fakeRWS, extraJSON string) *abbDriver {
	t.Helper()
	if extraJSON != "" && !strings.HasPrefix(extraJSON, ",") {
		extraJSON = "," + extraJSON
	}
	js := fmt.Sprintf(
		`{"endpoint":%q,"username":"Default User","password":"robotics",`+
			`"rwsVersion":"1.0","maxRequestsPerSecond":100%s}`, f.URL, extraJSON)

	d := newAbbDriver()
	if err := d.Connect(js); err != nil {
		t.Fatalf("Connect 失败: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	f.reset()
	return d
}

func addr(id, name string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: typeAuto}
}

func addrT(id, name, dataType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType}
}

// assertContract 校验 driver.Read 的返回契约 —— 违反会被 pluginhost 直接判错。
func assertContract(t *testing.T, addrs []po.DeviceAddress, results []driver.ReadResult) {
	t.Helper()
	if len(results) != len(addrs) {
		t.Fatalf("返回 %d 条结果，入参 %d 个点位", len(results), len(addrs))
	}
	seen := make(map[string]int, len(results))
	for _, r := range results {
		seen[r.DeviceAddressID]++
	}
	for _, a := range addrs {
		if seen[a.ID] != 1 {
			t.Errorf("点位 id=%s 在结果里出现了 %d 次，应恰好 1 次", a.ID, seen[a.ID])
		}
	}
}

func resultByID(t *testing.T, results []driver.ReadResult, id string) driver.ReadResult {
	t.Helper()
	for _, r := range results {
		if r.DeviceAddressID == id {
			return r
		}
	}
	t.Fatalf("结果里没有 id=%s", id)
	return driver.ReadResult{}
}

// assertQuality 断言一个点位的质量码。成因并入 Quality 之后，那条“192 带成因”的
// 非法组合已经不可表达，故只比对质量码本身。
func assertQuality(t *testing.T, r driver.ReadResult, id string, want int) {
	t.Helper()
	if r.Quality != want {
		t.Errorf("id=%s Quality = %d, want %d（value=%q）", id, r.Quality, want, r.Value)
	}
}

// TestReadAllNamespaces 五个命名空间混在一批里，逐项校验值 / Kind / 质量。
func TestReadAllNamespaces(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "quality": "1", "type": "DI"},
		map[string]string{"name": "AI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "12.500000", "type": "AI"},
	))
	f.routeJSON(jointtarget, `{"_embedded":{"_state":[{"rax_1":"1.5","rax_2":"-0.000000"}]}}`)
	f.routeJSON("/rw/panel/ctrl-state", `{"_embedded":{"_state":[{"ctrlstate":"motoron"}]}}`)
	f.routeJSON(rapidSymbol, `{"_embedded":{"_state":[{"name":"nTest","type":"num","value":"23"}]}}`)
	f.routeJSON(systemPath, `{"_embedded":{"_state":[{"rwversion":"7.12.0"}]}}`)

	d := connectDriver(t, f, "")

	addrs := []po.DeviceAddress{
		addr("1", "signal:Local/DRV_1/DI1"),
		addr("2", "signal:Local/DRV_1/AI1"),
		addr("3", "signal:Local/DRV_1/DI1#quality"),
		addr("4", "panel:ctrlstate"),
		addr("5", "motion:ROB_1/joint/rax_1"),
		addr("6", "motion:ROB_1/joint/rax_2"),
		addr("7", "rapid:T_ROB1/Mod1/nTest"),
		addr("8", "system:rwversion"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	want := map[string]struct {
		value string
		kind  string
		qual  int
	}{
		"1": {"1", driver.KindBool, driver.QualityGood},          // DI → bool
		"2": {"12.500000", driver.KindFloat, driver.QualityGood}, // AI → float，原样透传
		"3": {"1", driver.KindInt, driver.QualityGood},           // 字段 quality → int
		"4": {"motoron", driver.KindString, driver.QualityGood},
		"5": {"1.5", driver.KindFloat, driver.QualityGood},
		"6": {"-0.000000", driver.KindFloat, driver.QualityGood}, // 精度不做 float 往返
		"7": {"23", driver.KindFloat, driver.QualityGood},
		"8": {"7.12.0", driver.KindString, driver.QualityGood},
	}
	for id, w := range want {
		got := resultByID(t, results, id)
		if got.Value != w.value || got.Kind != w.kind || got.Quality != w.qual {
			t.Errorf("id=%s: (Value=%q Kind=%q Quality=%d)，期望 (%q %q %d)",
				id, got.Value, got.Kind, got.Quality, w.value, w.kind, w.qual)
		}
	}

	// 六个关节轴里的两个共用一次 jointtarget 请求 —— 这是 motion 命名空间
	// "同一资源的多点位共用一次 GET"的实现承诺。
	if n := f.pathHits(jointtarget); n != 1 {
		t.Errorf("同一关节资源的两个点位应共用 1 次请求，实际 %d 次", n)
	}
}

// TestReadBulkHitTakesNoSingleGet 是本文件里最重要的性能承诺。
//
// 控制器有 <20 req/s 的硬限流，而现场还有示教器在用同一条通道。
// 只要信号都在全量列表里，就不能产生任何单点 GET。
func TestReadBulkHitTakesNoSingleGet(t *testing.T) {
	f := newFakeRWS(t, authNone)

	// 100 个信号全在列表里。
	entries := make([]map[string]string, 0, 100)
	addrs := make([]po.DeviceAddress, 0, 100)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("DI%03d", i)
		entries = append(entries, map[string]string{
			"name": name, "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI",
		})
		addrs = append(addrs, addr(fmt.Sprintf("id%d", i), "signal:Local/DRV_1/"+name))
	}
	f.routeJSON(signalsList, signalListBody(entries...))

	d := connectDriver(t, f, "")
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	if n := f.pathHits(signalsList); n != 1 {
		t.Errorf("100 个信号应只用 1 次列表请求，实际 %d 次", n)
	}
	for _, r := range results {
		if r.Quality != driver.QualityGood {
			t.Fatalf("id=%s 质量 = %d，期望 %d", r.DeviceAddressID, r.Quality, driver.QualityGood)
		}
	}

	// 关键断言：单点路径一次都没被碰过。
	for _, p := range f.pathSeenSnapshot() {
		if p != signalsList && p != systemPath && strings.HasPrefix(p, signalsList+"/") {
			t.Errorf("不该出现单点 GET，实际请求了 %q", p)
		}
	}
}

// TestReadFallsBackToSingleGet 列表成功但不含该点位时，恰好回退一次单点 GET。
func TestReadFallsBackToSingleGet(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "OTHER", "unitnm": "DRV_1", "network": "Local", "lvalue": "0"},
	))
	f.routeJSON(signalsList+"/Local/DRV_1/DI1",
		`{"_embedded":{"_state":[{"lvalue":"1","quality":"1","type":"DI"}]}}`)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/DI1")}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	if got := resultByID(t, results, "1"); got.Value != "1" || got.Quality != driver.QualityGood {
		t.Errorf("单点回退取到的值不对: %+v", got)
	}
	if n := f.pathHits(signalsList + "/Local/DRV_1/DI1"); n != 1 {
		t.Errorf("应恰好 1 次单点 GET，实际 %d 次", n)
	}
}

// TestReadNegativeCache 确认不存在的点位（4xx）在静默期内不该再产生请求。
//
// 没有它，一个被删掉的信号会在每一轮里都发一次必然 404 的往返。
func TestReadNegativeCache(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "OTHER", "unitnm": "DRV_1", "network": "Local", "lvalue": "0"},
	))
	f.routeStatus(signalsList+"/Local/DRV_1/GONE", http.StatusNotFound)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/GONE")}

	for round := 1; round <= 3; round++ {
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatalf("第 %d 轮 Read 失败: %v", round, err)
		}
		if got := resultByID(t, results, "1"); got.Quality != driver.QualityBadAddress {
			t.Errorf("第 %d 轮：取不到值应为 QualityBadAddress，实际 %d", round, got.Quality)
		}
		// **每一轮**都必须带地址级质量码，包括被负缓存挡下、一个请求都不发的那两轮。
		//
		// hints 跨轮存活，而分类代码只在首次确认那一轮跑过 —— 成因不回填的话
		// 它就只在 1/N 的轮次出现，下游按"连续 N 轮"判定的点位级报警永远不触发。
		assertQuality(t, resultByID(t, results, "1"), "1", driver.QualityBadAddress)
	}

	// 只有第一轮真的发了单点请求。
	if n := f.pathHits(signalsList + "/Local/DRV_1/GONE"); n != 1 {
		t.Errorf("负缓存生效时单点 GET 应只发生 1 次，实际 %d 次", n)
	}
}

// TestReadOverrideHintSkipsListEntirely 全部信号都"不在列表里"时，
// 连列表请求本身都该省掉 —— 那一次往返是纯浪费。
func TestReadOverrideHintSkipsListEntirely(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "OTHER", "unitnm": "DRV_1", "network": "Local", "lvalue": "0"},
	))
	f.routeJSON(signalsList+"/Local/DRV_1/DI1",
		`{"_embedded":{"_state":[{"lvalue":"1","type":"DI"}]}}`)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/DI1")}

	// 第一轮：列表成功但不含该点位 → 回退单点，并记下"覆盖"提示。
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("第一轮 Read 失败: %v", err)
	}
	if n := f.pathHits(signalsList); n != 1 {
		t.Fatalf("第一轮应拉一次列表，实际 %d 次", n)
	}

	// 第二轮：全部信号都有覆盖提示 → 列表不再拉。
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("第二轮 Read 失败: %v", err)
	}
	if n := f.pathHits(signalsList); n != 1 {
		t.Errorf("第二轮不该再拉列表，累计次数 = %d", n)
	}
	if n := f.pathHits(signalsList + "/Local/DRV_1/DI1"); n != 2 {
		t.Errorf("两轮都应走单点，累计 %d 次", n)
	}
}

// TestReadListServiceErrorDegrades 列表的服务级失败只能降级到单点，
// **不能**把连接判成断开，也不能连累别的命名空间。
func TestReadListServiceErrorDegrades(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeStatus(signalsList, http.StatusInternalServerError)
	f.routeJSON(signalsList+"/Local/DRV_1/DI1",
		`{"_embedded":{"_state":[{"lvalue":"1","type":"DI"}]}}`)
	f.routeJSON("/rw/panel/ctrl-state", `{"_embedded":{"_state":[{"ctrlstate":"motoron"}]}}`)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{
		addr("1", "signal:Local/DRV_1/DI1"),
		addr("2", "panel:ctrlstate"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("列表 500 不该让整轮失败: %v", err)
	}
	assertContract(t, addrs, results)

	if got := resultByID(t, results, "1"); got.Value != "1" || got.Quality != driver.QualityGood {
		t.Errorf("信号点位应退化为单点并取到值: %+v", got)
	}
	if got := resultByID(t, results, "2"); got.Value != "motoron" {
		t.Errorf("别的命名空间不该受连累: %+v", got)
	}
	if !d.IsConnected() {
		t.Error("服务级失败不该把连接判成断开")
	}
}

// TestReadListTransportErrorAborts 传输级失败必须中止整轮并置为未连接，
// 让采集引擎在下一轮重连。
func TestReadListTransportErrorAborts(t *testing.T) {
	f := newFakeRWS(t, authNone)
	d := connectDriver(t, f, "")

	f.Close() // 控制器没了

	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/DI1")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("控制器失联时 Read 应返回错误")
	}
	if d.IsConnected() {
		t.Error("传输级失败后应处于未连接状态")
	}
}

// TestReadListStickyDisable 连续失败到阈值后，该类列表请求被粘性禁用 ——
// 404/501 通常意味着这个固件的 RWS 根本没有该资源，
// 每轮重试只是白烧全现场共享的请求配额。
func TestReadListStickyDisable(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeStatus(signalsList, http.StatusNotImplemented)
	f.routeStatus(signalsList+"/Local/DRV_1/DI1", http.StatusNotFound)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/DI1")}

	for round := 1; round <= stickyDisableThreshold+1; round++ {
		if _, err := d.Read(addrs); err != nil {
			t.Fatalf("第 %d 轮 Read 失败: %v", round, err)
		}
	}

	if n := f.pathHits(signalsList); n != stickyDisableThreshold {
		t.Errorf("粘性禁用后列表请求应停在 %d 次，实际 %d 次", stickyDisableThreshold, n)
	}
}

// TestReadFallbackBudget 回退有**条数**上限，且被丢掉的点位不该静默消失。
func TestReadFallbackBudget(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "OTHER", "unitnm": "DRV_1", "network": "Local", "lvalue": "0"},
	))
	// 所有单点都成功（但每轮只允许尝试有限个）。
	f.route(signalsList+"/", func(*http.Request) (int, string) {
		return http.StatusOK, `{"_embedded":{"_state":[{"lvalue":"1","type":"DI"}]}}`
	})

	d := connectDriver(t, f, "")

	const total = 200
	addrs := make([]po.DeviceAddress, 0, total)
	for i := 0; i < total; i++ {
		addrs = append(addrs, addr(fmt.Sprintf("id%d", i), fmt.Sprintf("signal:Local/DRV_1/S%03d", i)))
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	singles := f.hits(signalsList + "/")
	if len(singles) > defaultMaxFallbackPerRound {
		t.Errorf("单轮回退次数 %d 超过上限 %d", len(singles), defaultMaxFallbackPerRound)
	}
	if len(singles) == 0 {
		t.Error("应当发生回退，实际一次都没有")
	}

	// 未取到的点位必须是明确的坏码，不能是"看起来正常但没有值"。
	bad := 0
	for _, r := range results {
		if r.Quality == driver.QualityBad {
			bad++
			if r.Value != "" {
				t.Errorf("坏码的点位不该带值，id=%s value=%q", r.DeviceAddressID, r.Value)
			}
			// 被**预算**挡下的点位必须仍是"坏、未归类"：那是"暂缓"，不是设备判定。
			// 无脑给整段标 comms/address 会让「这轮没轮上」看起来像「测点坏了」，
			// 报警层据此把一次正常的轮转报成故障。这条断言是那种实现的照妖镜。
			assertQuality(t, r, r.DeviceAddressID, driver.QualityBad)
		}
	}
	if bad != total-len(singles) {
		t.Errorf("Quality=0 的点位数 = %d, 期望 %d（总数 - 实际回退数）", bad, total-len(singles))
	}
}

// TestQualityMapping 三种质量码逐一对上。
func TestQualityMapping(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "OK", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI"},
		// 被勾了仿真的信号：值来自控制器内部模拟，不代表真实过程 IO。
		map[string]string{"name": "SIM", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "lstate": "simulated", "type": "DI"},
	))
	// 单点 404：点位不存在。
	f.routeStatus(signalsList+"/Local/DRV_1/GONE", http.StatusNotFound)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{
		addr("ok", "signal:Local/DRV_1/OK"),
		addr("sim", "signal:Local/DRV_1/SIM"),
		addr("gone", "signal:Local/DRV_1/GONE"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	if got := resultByID(t, results, "ok"); got.Quality != driver.QualityGood {
		t.Errorf("正常信号质量 = %d, 期望 %d", got.Quality, driver.QualityGood)
	}
	if got := resultByID(t, results, "sim"); got.Quality != driver.QualityUncertain {
		t.Errorf("仿真信号质量 = %d, 期望 %d（值不代表真实过程）", got.Quality, driver.QualityUncertain)
	}
	if got := resultByID(t, results, "gone"); got.Quality != driver.QualityBadAddress || got.Value != "" {
		t.Errorf("不存在的点位应为 QualityBadAddress 且值为空: %+v", got)
	}
	// 单点 GET 回 404 = 控制器明说这个信号不存在 → 地址级（改点位表才能好）。
	assertQuality(t, resultByID(t, results, "gone"), "gone", driver.QualityBadAddress)
	// 192 与 128 都不是故障：128 是"值可用但标注质量"。
	assertQuality(t, resultByID(t, results, "ok"), "ok", driver.QualityGood)
	assertQuality(t, resultByID(t, results, "sim"), "sim", driver.QualityUncertain)
}

// TestServiceErrorCause 服务级失败按控制器的答复分档：4xx（东西不存在）归地址级，
// 5xx（控制器自己出错）归通信级。
//
// 这条分档与 noteMiss 的负缓存判据**刻意用同一个区间**：能被负缓存的失败与被判成
// 地址级的失败必须是同一批，否则会出现"质量码说地址错、但驱动下轮还在重试"的自相矛盾。
func TestServiceErrorQuality(t *testing.T) {
	cases := []struct {
		status int
		want   int
		why    string
	}{
		{http.StatusNotFound, driver.QualityBadAddress, "404：这个东西控制器上没有"},
		{http.StatusMethodNotAllowed, driver.QualityBadAddress, "405：同上"},
		{http.StatusInternalServerError, driver.QualityBadComms, "500：控制器自己出错，瞬时"},
		{http.StatusBadGateway, driver.QualityBadComms, "502：同上"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("status_%d", c.status), func(t *testing.T) {
			f := newFakeRWS(t, authNone)
			f.routeStatus("/rw/panel/ctrl-state", c.status)

			d := connectDriver(t, f, "")
			addrs := []po.DeviceAddress{addr("1", "panel:ctrlstate")}

			results, err := d.Read(addrs)
			if err != nil {
				t.Fatalf("服务级失败不该让整轮失败: %v", err)
			}
			if !d.IsConnected() {
				t.Error("服务级失败不该把连接判成断开")
			}
			got := resultByID(t, results, "1")
			if got.Quality != c.want {
				t.Fatalf("Quality = %d, want %d（%s）", got.Quality, c.want, c.why)
			}
		})
	}
}

// TestLocalParseErrorIsolated 本地语法错的点位既不发请求，也不影响同批其它点位。
func TestLocalParseErrorIsolated(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI"},
	))

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{
		addr("bad1", "foo:bar"),                  // 未知命名空间
		addr("bad2", "Local/DRV_1/DI1"),          // 裸地址（刻意不支持）
		addr("bad3", "motion:ROB_1/joint/rax_9"), // 白名单外字段
		addr("good", "signal:Local/DRV_1/DI1"),
	}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	for _, id := range []string{"bad1", "bad2", "bad3"} {
		if got := resultByID(t, results, id); got.Quality != driver.QualityBadAddress || got.Value != "" {
			t.Errorf("非法地址 %s 应为 QualityBadAddress 且无值: %+v", id, got)
		}
		// 地址写法本地就解析不出来 → 地址级（改点位表之前永远如此）
		assertQuality(t, resultByID(t, results, id), id, driver.QualityBadAddress)
	}
	if got := resultByID(t, results, "good"); got.Value != "1" || got.Quality != driver.QualityGood {
		t.Errorf("合法点位不该被连累: %+v", got)
	}
	// 同批健康点位不许被连坐
	assertQuality(t, resultByID(t, results, "good"), "good", driver.QualityGood)

	// 整批只该产生一次列表请求 —— 三个非法地址一个请求都不该发。
	if n := len(f.requests()); n != 1 {
		t.Errorf("整轮应只有 1 个请求（信号列表），实际 %d 个：%q", n, f.requests())
	}
}

// TestReadConfiguredTypeWins 用户显式配置的类型覆盖 auto 推断。
func TestReadConfiguredTypeWins(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI"},
	))

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addrT("1", "signal:Local/DRV_1/DI1", "float")}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if got := resultByID(t, results, "1"); got.Kind != driver.KindFloat {
		t.Errorf("配置为 float 时 Kind 应为 float（auto 本会判成 bool），实际 %q", got.Kind)
	}
}

// TestPingNoSession Ping 绝不能创建会话 —— 契约要求它不分配会话资源
// （网关调完 Ping 就直接丢弃临时实例，不会有人 Close 它）。
func TestPingNoSession(t *testing.T) {
	f := newFakeRWS(t, authDigest)
	js := fmt.Sprintf(
		`{"endpoint":%q,"username":"Default User","password":"robotics","rwsVersion":"1.0","sessionRequest":"true"}`,
		f.URL)

	d := newAbbDriver()
	if err := d.Ping(js); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}

	if n := f.hitCount(sessionRequestPath); n != 0 {
		t.Errorf("Ping 不该创建 RWS 会话，实际发了 %d 次 %s", n, sessionRequestPath)
	}
}

// TestPingDoesNotTouchDriverState Ping 用的临时实例不该影响驱动自身状态。
func TestPingDoesNotTouchDriverState(t *testing.T) {
	f := newFakeRWS(t, authNone)
	js := fmt.Sprintf(`{"endpoint":%q,"username":"Default User","password":"robotics","rwsVersion":"1.0"}`, f.URL)

	d := newAbbDriver()
	if d.IsConnected() {
		t.Fatal("新建的驱动不该是已连接状态")
	}
	if err := d.Ping(js); err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
	if d.IsConnected() {
		t.Error("Ping 之后驱动仍应是未连接状态")
	}
	// 没 Connect 过就 Read，应当明确报错而不是 panic。
	if _, err := d.Read([]po.DeviceAddress{addr("1", "signal:L/D/X")}); err == nil {
		t.Error("未连接时 Read 应返回错误")
	}
}

// TestReadAfterCloseFails 关闭之后不该再发请求。
func TestReadAfterCloseFails(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody())
	d := connectDriver(t, f, "")

	if err := d.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close 应幂等: %v", err)
	}
	f.reset()

	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/DI1")}
	if _, err := d.Read(addrs); err == nil {
		t.Fatal("关闭后 Read 应返回错误")
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("关闭后不该再发请求，实际发了 %d 个", n)
	}
}

// TestBuildPlanClonesAddressStrings 计划里保存的地址字符串必须与 RPC 载荷
// **不共享底层数组**。
//
// addrs[i].Name 引用的是网关那帧载荷的缓冲（pluginwire 的 zeroCopyStrings），
// 而计划会跨 Read 调用存活在缓存里 —— 不 clone 就等于把整帧几百 KB 钉住不放，
// 不报错、只涨内存。这条测试用 unsafe 直接比对数据指针，是唯一能真正验证
// "没有别名"的办法。
func TestBuildPlanClonesAddressStrings(t *testing.T) {
	const addrText = "signal:Local/DRV_1/DI1"
	// 造一个"共享大缓冲"的字符串，模拟 pluginwire 从帧里切出来的 Name。
	backing := addrText + strings.Repeat("x", 1<<16)
	name := backing[:len(addrText)]

	p := buildPlan([]po.DeviceAddress{{ID: "1", Name: name}})
	if p.slots[0].addr == nil {
		t.Fatal("解析失败")
	}
	got := p.slots[0].addr.signalName

	if got != "DI1" {
		t.Fatalf("signalName = %q, 期望 DI1", got)
	}
	if unsafe.StringData(got) == unsafe.StringData(backing) {
		t.Fatal("计划里的字符串仍指向 RPC 载荷缓冲 —— 缓存会把整帧钉在内存里")
	}
}

// TestPlanCacheReusesPlan 同一批点位重复读时走缓存（不重复解析地址）。
func TestPlanCacheReusesPlan(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI"},
	))
	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addr("1", "signal:Local/DRV_1/DI1")}

	p1 := d.planFor(addrs)
	p2 := d.planFor(addrs)
	if p1 != p2 {
		t.Error("同一批点位应命中同一个计划")
	}

	// Connect 会清空缓存，避免配置变化后复用陈旧计划。
	if err := d.Connect(fmt.Sprintf(
		`{"endpoint":%q,"username":"Default User","password":"robotics","rwsVersion":"1.0"}`, f.URL)); err != nil {
		t.Fatalf("重新 Connect 失败: %v", err)
	}
	if p3 := d.planFor(addrs); p3 == p1 {
		t.Error("Connect 之后计划缓存应被清空")
	}
}

// TestReadEmptyBatch 空批次不该发任何请求，也不该报错。
func TestReadEmptyBatch(t *testing.T) {
	f := newFakeRWS(t, authNone)
	d := connectDriver(t, f, "")

	results, err := d.Read(nil)
	if err != nil {
		t.Fatalf("Read(nil) 失败: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("空批次应返回 0 条结果，实际 %d 条", len(results))
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("空批次不该发请求，实际发了 %d 个", n)
	}
}

// TestReadRapidAlwaysSingleGet 钉住一个有意的设计决定：
// RAPID 符号**永远**走单点 GET。
//
// RWS 的 /rw/rapid/symbols 搜索端点返回的是符号描述符（名字、类型、路径），
// 不含值，所以"先拉列表再索引"对它省不下任何请求。与其发一次必然白跑的
// 搜索请求，不如直说：RAPID 点位的数量应当控制在合理范围。
func TestReadRapidAlwaysSingleGet(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(rapidSymbol, `{"_embedded":{"_state":[{"name":"nTest","type":"num","value":"23"}]}}`)
	// 即便有人注册了搜索端点，也不该被调用。
	f.routeJSON("/rw/rapid/symbols", `{"_embedded":{"_state":[]}}`)

	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{addr("1", "rapid:T_ROB1/Mod1/nTest")}

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	assertContract(t, addrs, results)

	if got := resultByID(t, results, "1"); got.Value != "23" {
		t.Errorf("RAPID 取值失败: %+v", got)
	}
	if n := f.pathHits("/rw/rapid/symbols"); n != 0 {
		t.Errorf("不该调用 RAPID 搜索端点（它只返回描述符不含值），实际 %d 次", n)
	}
	if n := f.pathHits(rapidSymbol); n != 1 {
		t.Errorf("RAPID 应恰好 1 次单点 GET，实际 %d 次", n)
	}
}

// pathSeenSnapshot 返回收到的全部请求路径（副本）。
func (f *fakeRWS) pathSeenSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pathSeen...)
}

// ==================== 告警去重 ====================

// captureStderr 把 plugin 日志（driversdk.Logf 直写 os.Stderr）接进内存缓冲。
//
// Logf 没有可注入的出口，只能替换 os 包的包级变量 —— 因此本辅助函数**不是
// 并发安全**的，只在本包"测试之间串行执行"的前提下成立（本包没有任何
// t.Parallel()，加之前先看这里）。
//
// 返回的函数停止捕获并给出内容；重复调用是安全的（提前失败时由 t.Cleanup
// 兜底还原 stderr，避免把后续测试的输出也吞掉）。
func captureStderr(t *testing.T) func() string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}
	os.Stderr = w

	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&buf, r)
	}()

	restore := func() {
		if os.Stderr == w {
			os.Stderr = orig
		}
		_ = w.Close() // 关闭写端，isCopy 才会返回
		<-done        // 等拷贝结束，之后读 buf 才没有竞态
		_ = r.Close()
	}
	t.Cleanup(restore)
	return func() string {
		restore()
		return buf.String()
	}
}

// TestParseErrorIsReportedOnce 地址解析失败必须留下日志，且不能每轮重复。
//
// 解析失败的点位既不请求也不报错：界面上只有一个恒为 Quality=0 的点位。
// 那句解释（"缺少命名空间前缀，请写成 signal:Local/DRV_1/DI1 这样"）是现场
// 唯一的线索，此前被 buildPlan 存进 planSlot.err 之后无人读取，直接丢掉了。
//
// 另一半同样重要：这个条件在**每一轮**都重新成立，不去重就是每秒一行。
func TestParseErrorIsReportedOnce(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI"},
	))
	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{
		addr("bad", "Local/DRV_1/DI1"), // 裸地址：刻意不支持
		addr("good", "signal:Local/DRV_1/DI1"),
	}

	logs := captureStderr(t)
	for round := 1; round <= 3; round++ {
		if _, err := d.Read(addrs); err != nil {
			t.Fatalf("第 %d 轮 Read 失败: %v", round, err)
		}
	}
	got := logs()

	n, line := countLogLines(got, "解析失败")
	if n != 1 {
		t.Fatalf("三轮应只留下 1 行解析失败告警，实际 %d 行（输出：%q）", n, got)
	}
	if !strings.Contains(line, "Local/DRV_1/DI1") {
		t.Errorf("日志应指出是哪个地址，实际：%q", line)
	}
	// 解析失败的点位仍然是"不发请求、Quality=0"，去重只影响日志。
	if n := len(f.requests()); n != 3 {
		t.Errorf("三轮应各只有 1 个列表请求，实际 %d 个", n)
	}
}

// TestMissingFieldWarnIsRateLimited 字段缺失同样是每轮都成立的条件。
//
// 写错字段名的点位现场通常是一批（复制粘贴出来的一列），不去重的话
// 1s 周期下就是每秒几十行完全相同的日志。
func TestMissingFieldWarnIsRateLimited(t *testing.T) {
	f := newFakeRWS(t, authNone)
	f.routeJSON(signalsList, signalListBody(
		map[string]string{"name": "DI1", "unitnm": "DRV_1", "network": "Local", "lvalue": "1", "type": "DI"},
	))
	d := connectDriver(t, f, "")
	addrs := []po.DeviceAddress{
		addr("bad", "signal:Local/DRV_1/DI1#nosuchfield"),
	}
	// 对照：同一批里正常点位的日志与去重互不影响（本用例里它不产生日志）。

	logs := captureStderr(t)
	for round := 1; round <= 3; round++ {
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatalf("第 %d 轮 Read 失败: %v", round, err)
		}
		if got := resultByID(t, results, "bad"); got.Quality != driver.QualityBadAddress || got.Value != "" {
			t.Fatalf("第 %d 轮：取不到字段的点位应为 QualityBadAddress 且无值: %+v", round, got)
		}
		// 资源请求成功了，是地址里的字段名在响应里不存在 → 地址级。
		// 与"这个点位不存在"同一性质：复制粘贴写错字段名是现场最常见的形态。
		assertQuality(t, resultByID(t, results, "bad"), "bad", driver.QualityBadAddress)
	}
	got := logs()

	n, line := countLogLines(got, "没有字段")
	if n != 1 {
		t.Fatalf("三轮应只留下 1 行字段缺失告警，实际 %d 行（输出：%q）", n, got)
	}
	if !strings.Contains(line, "nosuchfield") {
		t.Errorf("日志应指出缺的是哪个字段，实际：%q", line)
	}
}

// logLines 返回捕获到的非空日志行。
// logf 每条消息写一行，因此行数就是"打了几次"。
func logLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// countLogLines 数"含有 marker 的日志行"有几行。
//
// 不能直接 strings.Count 数子串：一行的地址与原因里都带着点位名，会被算成
// 两行。也不能数总行数：并发残留的输出（上个用例的假服务器、在途请求）会
// 混进来。按行匹配兼顾两者。
func countLogLines(s, marker string) (int, string) {
	n := 0
	first := ""
	for _, line := range logLines(s) {
		if strings.Contains(line, marker) {
			n++
			if first == "" {
				first = line
			}
		}
	}
	return n, first
}

// TestWarnOnceIsBounded warn 表必须有界，且与 hints 相互独立。
//
// 与 hints 分开是刻意的：hints 会改变读取路径（不再发请求），warn 只影响日志。
// 混在一起的话，"给日志消音"会被顺带读成"不再采集"。
func TestWarnOnceIsBounded(t *testing.T) {
	d := newAbbDriver()

	if !d.shouldWarn("k1") {
		t.Fatal("首次告警应放行")
	}
	if d.shouldWarn("k1") {
		t.Error("静默期内的重复告警应被抑制")
	}
	if !d.shouldWarn("k2") {
		t.Error("不同键之间不该互相影响")
	}

	// 超限清空重建：不 panic、不无限增长。
	for i := 0; i < fallbackHintMaxEntries+1; i++ {
		d.shouldWarn(fmt.Sprintf("bulk-%d", i))
	}
	if n := len(d.warned); n > fallbackHintMaxEntries {
		t.Errorf("warn 表条目数 = %d，超过上限 %d", n, fallbackHintMaxEntries)
	}

	d.clearWarns()
	if len(d.warned) != 0 {
		t.Errorf("clearWarns 后仍有 %d 条", len(d.warned))
	}
}

// ==================== 回退预算的分配 ====================

// TestFallbackRotationCoversAllPoints 需要回退的点位多于单轮配额时，
// **每一个**都要在有限轮次内轮到，而不是只有排在前面的那些。
//
// RAPID 点位只能走单点回退（见 Read 的说明），所以"配了 40 个 RAPID 变量、
// 配额 32"是现场很容易出现的形状。此前的实现每轮都从下标 0 开始取前 32 个，
// 后 8 个点位永远 Quality=0 —— 而日志说的是"本轮跳过 8 个"，听起来下轮会补。
func TestFallbackRotationCoversAllPoints(t *testing.T) {
	f := newFakeRWS(t, authNone)
	// RAPID 符号一律走单点 GET，每个都给个值。
	f.route("/rw/rapid/symbol/data/RAPID/", func(*http.Request) (int, string) {
		return http.StatusOK, `{"_embedded":{"_state":[{"type":"num","value":"7"}]}}`
	})

	d := connectDriver(t, f, "")

	total := defaultMaxFallbackPerRound + 8
	addrs := make([]po.DeviceAddress, 0, total)
	for i := 0; i < total; i++ {
		addrs = append(addrs, addr(fmt.Sprintf("id%d", i),
			fmt.Sprintf("rapid:T_ROB1/Mod1/n%03d", i)))
	}

	seen := make(map[string]bool, total)
	const maxRounds = 4 // 40 个点位 / 32 的配额，两轮就该覆盖完；给足余量
	for round := 1; round <= maxRounds && len(seen) < total; round++ {
		results, err := d.Read(addrs)
		if err != nil {
			t.Fatalf("第 %d 轮 Read 失败: %v", round, err)
		}
		assertContract(t, addrs, results)
		for _, r := range results {
			if r.Quality == driver.QualityGood {
				seen[r.DeviceAddressID] = true
			}
		}
	}

	if len(seen) != total {
		var missing []string
		for _, a := range addrs {
			if !seen[a.ID] {
				missing = append(missing, a.Name)
			}
		}
		t.Errorf("%d 轮后仍有 %d 个点位从未取到值（前 %d 个点位的饥饿）：%v",
			maxRounds, len(missing), defaultMaxFallbackPerRound, missing)
	}
}

// TestPlanCacheEvictsOneNotAll 计划缓存满时只淘汰一条，不能整表清空。
//
// 全清的后果不是"少缓存一点"，而是**命中率从 100% 掉到 0**：单设备批次多于
// 上限（33+ 个批次）时，每个批次每轮都要重新解析一遍全部地址，且没有任何日志。
//
// 断言"新插一条之后，原有批次还剩几个"，而不是断言缓存条数：后者在插入次数
// 恰好落在容量整数倍上时，清空与淘汰单条的**末态条数是一样的**，测试会漏判。
func TestPlanCacheEvictsOneNotAll(t *testing.T) {
	d := newAbbDriver()

	// 先塞满上限，逐个记下计划指针。
	batches := make([][]po.DeviceAddress, 0, driver.RangeCacheMaxEntries)
	plans := make([]*readPlan, 0, driver.RangeCacheMaxEntries)
	for i := 0; i < driver.RangeCacheMaxEntries; i++ {
		a := []po.DeviceAddress{{
			ID:   fmt.Sprintf("id%d", i),
			Name: fmt.Sprintf("signal:Local/DRV_1/DI%03d", i),
		}}
		batches = append(batches, a)
		plans = append(plans, d.planFor(a))
	}

	// 再插入一个全新的批次：只该挤掉一条。
	d.planFor([]po.DeviceAddress{{ID: "new", Name: "signal:Local/DRV_1/DINEW"}})

	// 直接查缓存表而不是再调 planFor：planFor 未命中时会写表，
	// 逐个复验的过程本身就会把后面待查的条目挤掉。
	survived := 0
	d.plans.mu.Lock()
	for i, a := range batches {
		if p, ok := d.plans.plans[driver.RangeSig(a)]; ok && p == plans[i] {
			survived++
		}
	}
	n := len(d.plans.plans)
	d.plans.mu.Unlock()

	if n > driver.RangeCacheMaxEntries {
		t.Errorf("缓存条目数 = %d，超过上限 %d", n, driver.RangeCacheMaxEntries)
	}
	if want := driver.RangeCacheMaxEntries - 1; survived < want {
		t.Errorf("新插入一条后被淘汰的批次有 %d 个（仅剩 %d 个存活，期望至少 %d）——"+
			"说明满之后被整表清空，命中率归零", driver.RangeCacheMaxEntries-survived, survived, want)
	}
}
