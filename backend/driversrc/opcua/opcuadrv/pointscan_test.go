// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// ==================== 测试基建 ====================

// tempSession 模拟宿主 driversdk.handleDiscoverPoints 的**临时实例**路径：
// 驱动已经连上、交给 finder、finder 返回后由调用方关闭。
//
// 与真实路径同形（本驱动未实现 driver.ConnectionMatcher，selectSession 必然
// 返回 (nil,nil) 走这一条），所以 Do 不加锁 —— 这与生产路径完全一致。
type tempSession struct{ drv driver.Driver }

func (s *tempSession) Do(fn func(drv driver.Driver) error) error { return fn(s.drv) }

// startTreeServer 起一个可浏览地址空间的假服务器，并返回一个**已连接的驱动**。
func startTreeServer(t *testing.T) (driver.Driver, *fake.OpcUaTree, func()) {
	t.Helper()
	srv, tree, err := fake.NewOpcUaTree()
	if err != nil {
		t.Fatalf("起假 OPC UA 服务器失败: %v", err)
	}
	protocolJSON := fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())

	drv := newOpcUaDriver()
	if err := drv.Connect(protocolJSON); err != nil {
		srv.Close()
		t.Fatalf("连接假服务器失败: %v", err)
	}
	return drv, tree, func() {
		_ = drv.Close()
		srv.Close()
	}
}

// pointByName 按地址在结果里找点位。
func pointByName(points []driver.DiscoveredPoint, name string) (driver.DiscoveredPoint, bool) {
	for _, p := range points {
		if p.Name == name {
			return p, true
		}
	}
	return driver.DiscoveredPoint{}, false
}

// ==================== 端到端（真服务器 + 真驱动） ====================

// TestScanPointsTree 走完整条链路：真驱动连真服务器、Browse 一棵真树。
//
// 断言的是**假树里那几个点位都在**，而不是"结果恰好等于这几个" ——
// OPC UA 的 Objects 文件夹下还挂着服务器自己的 Server 对象子树
// （ServerStatus/ServerDiagnostics……），它们**本来就该**被列出来
// （这条写进了 PointDiscovery().Description 的第 ② 条）。
// 断言"恰好等于"会让这条测试在换一个 gopcua 版本时莫名其妙地红。
func TestScanPointsTree(t *testing.T) {
	drv, tree, cleanup := startTreeServer(t)
	defer cleanup()

	res, err := ScanPoints(&tempSession{drv: drv}, nil, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("ScanPoints 返回错误: %v", err)
	}
	if res.Truncated || res.Incomplete {
		t.Fatalf("小树不该触发截断/未扫完: truncated=%v incomplete=%v", res.Truncated, res.Incomplete)
	}
	// 不变量（specs/point-discovery.md §9）。
	if res.Readable+res.Unreadable != res.Scanned {
		t.Errorf("readable(%d)+unreadable(%d) != scanned(%d)",
			res.Readable, res.Unreadable, res.Scanned)
	}
	if len(res.Points) > res.Readable {
		t.Errorf("len(points)=%d 大于 readable=%d", len(res.Points), res.Readable)
	}
	// 不读采样值 ⇒ 结果级类型必须留空（跨端约定：前端据此隐藏「显示 0 值」开关）。
	if res.AssumedCommonDataType != "" {
		t.Errorf("AssumedCommonDataType = %q，本协议不读采样值，必须留空",
			res.AssumedCommonDataType)
	}

	got := make([]string, 0, len(res.Points))
	for _, p := range res.Points {
		got = append(got, p.Name)
		if p.Value != "" {
			t.Errorf("点位 %s 带了采样值 %q —— 本协议不读采样值", p.Name, p.Value)
		}
		// 每一点都必须自带类型：结果级留空时前端没有别的东西可回退。
		if p.CommonDataType == "" {
			t.Errorf("点位 %s 没有 CommonDataType（结果级留空时这是唯一来源）", p.Name)
		}
		if _, ok := CommonTypeMap()[p.CommonDataType]; !ok {
			t.Errorf("点位 %s 的类型 %q 不是 TypeMap 的键，建点位时会被服务层拒掉",
				p.Name, p.CommonDataType)
		}
	}
	sort.Strings(got)
	t.Logf("共列出 %d 个点位: %v", len(got), got)

	for _, want := range tree.Points {
		p, ok := pointByName(res.Points, want.Address)
		if !ok {
			t.Errorf("假树里的 %s 没有被列出来（现有 %v）", want.Address, got)
			continue
		}
		if p.CommonDataType != want.CommonDataType {
			t.Errorf("%s 的类型 = %q, 期望 %q", want.Address, p.CommonDataType, want.CommonDataType)
		}
		if p.Label != want.Label {
			t.Errorf("%s 的标签 = %q, 期望 %q", want.Address, p.Label, want.Label)
		}
		if want.NoteContains == "" {
			if p.Note != "" {
				t.Errorf("%s 的 Note = %q，期望为空", want.Address, p.Note)
			}
		} else if !contains(p.Note, want.NoteContains) {
			t.Errorf("%s 的 Note = %q, 期望包含 %q", want.Address, p.Note, want.NoteContains)
		}
		// **地址往返**：把它当作用户手工输入的地址重新解析一次，必须回到同一个
		// 节点 —— 否则那个点位在采集时要么永远无值、要么更糟：采到另一个节点的值。
		addr, err := parseAddress(p.Name)
		if err != nil {
			t.Errorf("%s 解析不回来: %v", p.Name, err)
			continue
		}
		if addr.isPath {
			t.Errorf("%s 被解析成了浏览路径，但它本该是 NodeID", p.Name)
		}
	}
}

// TestScanPointsStartNodeScope 起始节点把扫描限制在子树内。
//
// 这条同时钉住"起始节点填 NodeID 时路径那一支整体不可用"这条取舍
// （NodeID 起点没有从 Root 出发的路径），但因为这些节点的地址都是 NodeID 写法，
// 结果不受影响 —— 受影响的是"标识符里带 / 的字符串 NodeID"，那类节点在
// TestAddressOfFallsBackToBrowsePath 里单独测。
func TestScanPointsStartNodeScope(t *testing.T) {
	drv, tree, cleanup := startTreeServer(t)
	defer cleanup()

	start := fmt.Sprintf("ns=%d;i=%d", tree.NamespaceIndex, 1000) // FakePLC
	opts, err := json.Marshal(map[string]any{"startNode": start})
	if err != nil {
		t.Fatal(err)
	}

	res, err := ScanPoints(&tempSession{drv: drv}, opts, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("ScanPoints 返回错误: %v", err)
	}
	// 从 FakePLC 起扫到的是同一批点位（Server 子树在 Objects 下，不在 FakePLC 下）。
	for _, want := range tree.Points {
		if _, ok := pointByName(res.Points, want.Address); !ok {
			t.Errorf("从 %s 起扫时漏掉了 %s", start, want.Address)
		}
	}
	if len(res.Points) != len(tree.Points) {
		names := make([]string, 0, len(res.Points))
		for _, p := range res.Points {
			names = append(names, p.Name)
		}
		sort.Strings(names)
		t.Errorf("从 %s 起扫多出了点位（Server 子树不该在这一支下）: %v", start, names)
	}
	// 起始节点是 FakePLC 本身 ⇒ 标签是"从你开始扫的地方往下"的路径，
	// 所以第一条的标签就是它自己的显示名（与默认起点那条测同一件事的两端）。
	if p, ok := pointByName(res.Points, tree.Points[0].Address); ok {
		if p.Label != "Temperature" {
			t.Errorf("NodeID 起点的标签 = %q, 期望 %q", p.Label, "Temperature")
		}
	}
}

// TestScanPointsBogusStartNode 拼错的起始节点要报错，不能给出一份空清单。
//
// 这条防的是一类很隐蔽的误读：ua.ParseNodeID 对裸词是**宽容**的 ——
// 一个没有 i=/s= 前缀的串会被当成 ns=0 的字符串 NodeID（规范允许省略 s=）。
// 于是"Objets"这种拼错不会被本地解析拦下，只会变出一个不存在的 NodeID；
// 不额外确认存在性的话，用户拿到的是一份空清单，而原因是"我把起始节点写错了"
// 这件事在界面上一个字都不会出现。
func TestScanPointsBogusStartNode(t *testing.T) {
	// 左边这一格正是本守卫存在的理由：用户想写"从 Objects 开始"，于是敲了
	// Objects（前端 i18n 的 pitfallOpcuaRoot 里也提示过这个写法的问题）；
	// 解析器不报错，把它当成 ns=0 的字符串 NodeID s=Objects —— 一个不存在的节点。
	// 没有存在性检查的话，用户拿到的是一份空清单，而原因是"起始节点写错了"
	// 这件事在界面上一个字都不会出现。
	for _, start := range []string{"Objects", "ns=1;i=65000"} {
		drv, _, cleanup := startTreeServer(t)
		opts, err := json.Marshal(map[string]any{"startNode": start})
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
		_, err = ScanPoints(&tempSession{drv: drv}, opts, time.Now().Add(10*time.Second))
		cleanup()
		if err == nil {
			t.Errorf("起始节点 %q 不存在，应当报错而不是给出一份空清单", start)
			continue
		}
		if !contains(err.Error(), "起始节点") {
			t.Errorf("起始节点 %q 的错误信息没有指出问题所在: %v", start, err)
		}
	}
}

// TestScanPointsUnparseableStartNode 起始节点**语法上**就解析不了时报错。
//
// 注意裸词不算非法：`ua.ParseNodeID` 会把没有 i=/s= 前缀的串当成 ns=0 的字符串
// NodeID（规范允许省略 s=），所以"拼错了"是**存在性**问题而不是语法问题 ——
// 那一条由 TestScanPointsBogusStartNode 覆盖。这里要的是真的连语法都不成立：
// 一条只剩空段的路径（"Root//"），parseBrowsePath 会以"空路径"报错。
func TestScanPointsUnparseableStartNode(t *testing.T) {
	drv, _, cleanup := startTreeServer(t)
	defer cleanup()

	opts := json.RawMessage(`{"startNode":"Root//"}`)
	_, err := ScanPoints(&tempSession{drv: drv}, opts, time.Now().Add(10*time.Second))
	if err == nil {
		t.Fatal("非法起始节点应当报错")
	}
	if !contains(err.Error(), "起始节点") {
		t.Errorf("错误信息没有指出是起始节点的问题: %v", err)
	}
}

// TestScanPointsUnsupportedSession 没有会话时直接报错（而不是静默返回空清单）。
func TestScanPointsUnsupportedSession(t *testing.T) {
	if _, err := ScanPoints(nil, nil, time.Now().Add(time.Second)); err == nil {
		t.Fatal("没有会话时应当报错")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ==================== 纯函数：地址渲染与往返 ====================

// TestSegmentText 路径段的渲染规则必须与 parsePathSegment 的分支逐一对应。
func TestSegmentText(t *testing.T) {
	cases := []struct {
		name    string
		qn      *ua.QualifiedName
		want    string
		wantOK  bool
		comment string
	}{
		{"裸名", &ua.QualifiedName{Name: "Temperature"}, "Temperature", true,
			"ns=0 且无冒号 ⇒ 裸名（与用户手写的一致）"},
		{"带命名空间", &ua.QualifiedName{NamespaceIndex: 2, Name: "Device"}, "ns=2:Device", true,
			"ns!=0 必须带前缀，否则解析回来是 ns=0"},
		{"ns=0 带冒号", &ua.QualifiedName{Name: "Motor:1"}, "ns=0:Motor:1", true,
			"裸写 Motor:1 会被 parsePathSegment 读成命名空间 Motor 并报错"},
		{"带命名空间带冒号", &ua.QualifiedName{NamespaceIndex: 3, Name: "A:B"}, "ns=3:A:B", true,
			"第一个冒号由前缀吃掉，剩下的 A:B 才是名字"},
		{"含斜杠", &ua.QualifiedName{Name: "Room1/Temp"}, "", false,
			"会被当成段分隔符，一个名字拆成两段"},
		{"空名", &ua.QualifiedName{Name: ""}, "", false,
			"parseBrowsePath 会静默丢掉空段，路径悄悄变短"},
		{"首尾空白", &ua.QualifiedName{Name: " Temp "}, "", false,
			"parseAddress 会 TrimSpace 整串"},
		{"nil", nil, "", false, "防御"},
	}
	for _, c := range cases {
		got, ok := segmentText(c.qn)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%s: segmentText = (%q,%v), 期望 (%q,%v) —— %s",
				c.name, got, ok, c.want, c.wantOK, c.comment)
		}
	}
}

// TestPathRoundTrips 渲染出来的路径必须能被 parseAddress 还原成同一串段。
//
// 这条是 pointaddr.go 那条原则的直接回归：**先渲染、再解析回来、逐段比对**。
func TestPathRoundTrips(t *testing.T) {
	objects := &ua.QualifiedName{Name: "Objects"}
	device := &ua.QualifiedName{NamespaceIndex: 2, Name: "Device"}
	motor := &ua.QualifiedName{Name: "Motor:1"}

	ok := []*ua.QualifiedName{objects, device, motor}
	if !pathRoundTrips("Root/Objects/ns=2:Device/ns=0:Motor:1", ok) {
		t.Error("一条合法的路径没能通过往返验证")
	}
	// 少一段 / 多一段 / 顺序不对，都必须判不通过。
	if pathRoundTrips("Root/Objects/ns=2:Device", ok) {
		t.Error("少一段的路径被误判为通过")
	}
	if pathRoundTrips("Root/Objects/ns=2:Device/ns=0:Motor:1/Extra", ok) {
		t.Error("多一段的路径被误判为通过")
	}
	if pathRoundTrips("Root/ns=2:Device/Objects/ns=0:Motor:1", ok) {
		t.Error("顺序不对的路径被误判为通过")
	}
	// 命名空间对不上：ns=0:Device 与 ns=2:Device 是**两个不同的节点**。
	if pathRoundTrips("Root/Objects/ns=0:Device/ns=0:Motor:1", ok) {
		t.Error("命名空间不匹配的路径被误判为通过")
	}
}

// TestNodeIDRoundTrips 覆盖「字符串标识符里带 /」这个已知陷阱。
//
// 这是本文件里最要紧的一条：parseAddress 把任何含 "/" 的串当浏览路径，
// 于是一条看起来完全正常的 NodeID 地址会被解析成一条**别的**路径。
// 解析不到还算好；解析到一个同名节点才是灾难（永远采到别人的值）。
func TestNodeIDRoundTrips(t *testing.T) {
	cases := []struct {
		text string
		nid  *ua.NodeID
		want bool
	}{
		{"ns=2;s=Temperature", ua.NewStringNodeID(2, "Temperature"), true},
		{"i=2259", ua.NewNumericNodeID(0, 2259), true},
		{"ns=2;i=100", ua.NewNumericNodeID(2, 100), true},
		{"s=Temperature", ua.NewStringNodeID(0, "Temperature"), true},
		{`ns=2;s=Room1/Temp`, ua.NewStringNodeID(2, "Room1/Temp"), false},
		{"", ua.NewStringNodeID(2, "X"), false},
	}
	for _, c := range cases {
		if got := nodeIDRoundTrips(c.text, c.nid); got != c.want {
			t.Errorf("nodeIDRoundTrips(%q) = %v, 期望 %v", c.text, got, c.want)
		}
	}
}

// TestAddressOfFallsBackToBrowsePath 走不了 NodeID 时退到浏览路径，两者都不行就不产出。
func TestAddressOfFallsBackToBrowsePath(t *testing.T) {
	// 标识符里带 "/" 的字符串 NodeID：NodeID 写法会被误判成浏览路径。
	slashed := &crawlNode{nid: ua.NewStringNodeID(2, "Room1/Temp")}
	if got := addressOf(slashed); got != "" {
		t.Errorf("含 / 的字符串 NodeID 没有路径可退时应当放弃，却给出了 %q", got)
	}

	// 同一节点，若下行时累积出了可用路径，就退到路径写法。
	slashed.path = browsePath{
		text: "Root/Objects/2:Room1",
		qns:  []*ua.QualifiedName{{Name: "Objects"}, {NamespaceIndex: 2, Name: "Room1"}},
		ok:   true,
	}
	if got := addressOf(slashed); got != "Root/Objects/2:Room1" {
		t.Errorf("addressOf = %q, 期望退到浏览路径", got)
	}

	// 能用 NodeID 时**优先 NodeID**：路径地址每次重连后首次采集都要一次服务器解析。
	normal := &crawlNode{
		nid:  ua.NewNumericNodeID(1, 1001),
		path: browsePath{text: "Root/Objects/PLC/Temp", qns: []*ua.QualifiedName{{Name: "Objects"}}, ok: true},
	}
	if got := addressOf(normal); got != "ns=1;i=1001" {
		t.Errorf("addressOf = %q, 期望优先用 NodeID 写法", got)
	}
}

// ==================== 类型映射 ====================

// TestCommonTypeOfBuiltin 每个内建码的映射逐项钉住。
//
// 近似映射（exact=false）尤其要钉：改了它们不会报任何错，只会让那一类点位
// 在时序库里按另一个类型存值（Kind 相同，所以连值都看不出来）。
func TestCommonTypeOfBuiltin(t *testing.T) {
	cases := []struct {
		code  uint32
		want  string
		exact bool
	}{
		{id.Boolean, "Boolean", true},
		{id.SByte, "Char", false},
		{id.Byte, "Byte", true},
		{id.Int16, "Short", true},
		{id.UInt16, "Word", true},
		{id.Int32, "Long", false},
		{id.UInt32, "DWord", true},
		{id.Int64, "Long", true},
		{id.UInt64, "DWord", false},
		{id.Float, "Float", true},
		{id.Double, "Double", true},
		{id.String, "String", true},
		{id.DateTime, "Date", true},
		{id.ByteString, "String", false},
	}
	for _, c := range cases {
		m, ok := commonTypeOfBuiltin(c.code)
		if !ok {
			t.Errorf("%s(%d) 应当可映射", id.Name(c.code), c.code)
			continue
		}
		if m.common != c.want || m.exact != c.exact {
			t.Errorf("%s(%d) → (%q, exact=%v), 期望 (%q, exact=%v)",
				id.Name(c.code), c.code, m.common, m.exact, c.want, c.exact)
		}
	}

	// 不可映射的：结构体、抽象基类、以及一切非标量。列出来只会建出一批
	// 永远没有值的点位，用户分不清那是设备坏了还是这个点位本来没数据。
	for _, code := range []uint32{
		id.Structure, id.BaseDataType, id.Number,
		id.NodeID, id.LocalizedText, id.QualifiedName, id.StatusCode, id.GUID,
	} {
		if _, ok := commonTypeOfBuiltin(code); ok {
			t.Errorf("%s(%d) 不该映射（列出来会建出一批永远没有值的点位）", id.Name(code), code)
		}
	}

	// 上溯的终止节点：自定义枚举派生自它们，问不出宽度了，取有符号/无符号的宽类型。
	for _, code := range []uint32{id.Enumeration, id.Integer} {
		m, ok := commonTypeOfBuiltin(code)
		if !ok || m.common != "Long" || m.exact {
			t.Errorf("%s(%d) → (%q, exact=%v)，期望近似映射到 Long", id.Name(code), code, m.common, m.exact)
		}
	}
	if m, ok := commonTypeOfBuiltin(id.UInteger); !ok || m.common != "DWord" || m.exact {
		t.Errorf("UInteger → (%q, exact=%v)，期望近似映射到 DWord", m.common, m.exact)
	}
}

// TestTypeNoteOnlyForApproximate 只有近似映射才该有 Note。
//
// Note 是用户在结果表里唯一能看出"服务器报的类型与这里写的不一样"的地方
// （原生枚举下前端把它渲染在「采样值」那一列），所以它必须只出现在真有差异时：
// 每一行都挂一条会让那一列变成噪声屏蔽掉真正需要注意的行。
func TestTypeNoteOnlyForApproximate(t *testing.T) {
	exact, _ := commonTypeOfBuiltin(id.Double)
	if got := TypeNote("Double", exact); got != "" {
		t.Errorf("同名映射不该有 Note，却给出了 %q", got)
	}
	approx, _ := commonTypeOfBuiltin(id.Int32)
	if got := TypeNote("Int32", approx); got != "OPC UA 基础类型 Int32" {
		t.Errorf("TypeNote = %q", got)
	}
	if got := TypeNote("", approx); got != "" {
		t.Errorf("服务器类型名未知时不该编一个 Note，却给出了 %q", got)
	}
}

// TestBuiltinTypeName 展示名走库的 id.Name，不另立一张会漂移的表。
func TestBuiltinTypeName(t *testing.T) {
	if got := BuiltinTypeName(id.Int32); got != "Int32" {
		t.Errorf("BuiltinTypeName(Int32) = %q", got)
	}
	if got := BuiltinTypeName(999999); got != "" {
		t.Errorf("未知码应当返回空串，却给出了 %q", got)
	}
}

// ==================== 选项解析 ====================

// TestParsePointScanOptions 缺项走默认、坏值报错。
func TestParsePointScanOptions(t *testing.T) {
	opt, err := parsePointScanOptions(nil)
	if err != nil || opt.start != nil || opt.maxDepth != defaultPointScanMaxDepth {
		t.Errorf("空选项应当走默认: start=%v depth=%d err=%v", opt.start, opt.maxDepth, err)
	}

	opt, err = parsePointScanOptions(json.RawMessage(`{"maxDepth":"3"}`))
	if err != nil || opt.maxDepth != 3 {
		t.Errorf("数字字符串形式的 maxDepth 没被接受: depth=%d err=%v", opt.maxDepth, err)
	}

	opt, err = parsePointScanOptions(json.RawMessage(`{"startNode":"ns=2;s=Device","maxDepth":0}`))
	if err != nil || opt.start == nil || opt.start.nodeID == nil {
		t.Errorf("起始 NodeID 没被解析: %+v err=%v", opt.start, err)
	}
	if opt.maxDepth != 0 {
		t.Errorf("maxDepth=0 表示不限，却被改成了 %d", opt.maxDepth)
	}

	if _, err := parsePointScanOptions(json.RawMessage(`{"maxDepth":-1}`)); err == nil {
		t.Error("负数深度应当报错")
	}
	// 空路径（只剩空段）语法上就不成立；裸词不算非法（见
	// TestScanPointsUnparseableStartNode 的说明）。
	if _, err := parsePointScanOptions(json.RawMessage(`{"startNode":"Root//"}`)); err == nil {
		t.Error("语法非法的起始节点应当报错")
	}
}

// ==================== 数据类型解析（假客户端） ====================

// TestResolveTypesBuiltinFastPath 内建码零请求直接映射。
func TestResolveTypesBuiltinFastPath(t *testing.T) {
	c := &fakeClient{}
	client := &OpcUaClient{c: c}
	r := newTypeResolver()
	budget := &requestBudget{left: 10}

	out, err := r.resolveTypes(context.Background(), client,
		[]*ua.NodeID{ua.NewNumericNodeID(0, id.Double), ua.NewNumericNodeID(0, id.UInt16)},
		budget)
	if err != nil {
		t.Fatalf("resolveTypes: %v", err)
	}
	if budget.left != 10 {
		t.Errorf("内建码不该发请求，预算却从 10 掉到 %d", budget.left)
	}
	if len(out.types) != 2 {
		t.Fatalf("解析出 %d 项, 期望 2", len(out.types))
	}
	if got := out.types[ua.NewNumericNodeID(0, id.Double).String()].mapping.common; got != "Double" {
		t.Errorf("Double 映射成了 %q", got)
	}
}

// TestResolveTypesWalksSubtypeChain 自定义枚举沿逆 HasSubtype 上溯到内建类型。
//
// 这条链是现实里最常见的：自定义枚举 → Enumeration → Int32。中间隔着两层，
// 所以"只看直接父节点"的实现会把它判成"不可映射"（从而丢掉一整类点位）。
func TestResolveTypesWalksSubtypeChain(t *testing.T) {
	custom := ua.NewNumericNodeID(5, 500) // 服务器自定义的枚举类型
	enumeration := ua.NewNumericNodeID(0, id.Enumeration)
	int32Type := ua.NewNumericNodeID(0, id.Int32)

	c := &fakeClient{browseFunc: func(_ context.Context, req *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		results := make([]*ua.BrowseResult, len(req.NodesToBrowse))
		for i, bd := range req.NodesToBrowse {
			parent := int32Type
			if bd.NodeID.Equal(custom) {
				parent = enumeration
			}
			results[i] = &ua.BrowseResult{
				StatusCode: ua.StatusOK,
				References: []*ua.ReferenceDescription{{
					IsForward:  false,
					NodeID:     ua.NewNumericExpandedNodeID(0, parent.IntID()),
					BrowseName: &ua.QualifiedName{Name: id.Name(parent.IntID())},
					NodeClass:  ua.NodeClassDataType,
				}},
			}
			if bd.NodeID.Equal(int32Type) {
				// 内建类型的父节点（Number）不该被问到：builtinMappingOf 已经定论。
				t.Errorf("对已在映射表里的 %s 还去问了父类型", bd.NodeID)
			}
		}
		return &ua.BrowseResponse{Results: results}, nil
	}}

	client := &OpcUaClient{c: c}
	r := newTypeResolver()
	budget := &requestBudget{left: 20}

	out, err := r.resolveTypes(context.Background(), client, []*ua.NodeID{custom}, budget)
	if err != nil {
		t.Fatalf("resolveTypes: %v", err)
	}
	rt, ok := out.types[custom.String()]
	if !ok {
		t.Fatal("自定义枚举没被解析出来 —— Enumeration 那一层被漏掉了")
	}
	if rt.mapping.common != "Long" {
		t.Errorf("自定义枚举映射成了 %q, 期望 Long（Enumeration 派生自 Int32）", rt.mapping.common)
	}
	if rt.base != "Enumeration" {
		t.Errorf("基础类型名 = %q, 期望 Enumeration（上溯的终点）", rt.base)
	}
	// 第二次解析必须走缓存，一发请求都不发。
	before := budget.left
	if _, err := r.resolveTypes(context.Background(), client, []*ua.NodeID{custom}, budget); err != nil {
		t.Fatalf("resolveTypes(第二次): %v", err)
	}
	if budget.left != before {
		t.Errorf("第二次解析又发了 %d 个请求（缓存没生效）", before-budget.left)
	}
}

// TestResolveTypesUnmappable 上溯到结构体/抽象基类时判为不可映射，并记住结论。
func TestResolveTypesUnmappable(t *testing.T) {
	custom := ua.NewNumericNodeID(5, 700)
	structure := ua.NewNumericNodeID(0, id.Structure)

	var browses int
	c := &fakeClient{browseFunc: func(_ context.Context, req *ua.BrowseRequest) (*ua.BrowseResponse, error) {
		browses++
		results := make([]*ua.BrowseResult, len(req.NodesToBrowse))
		for i := range req.NodesToBrowse {
			results[i] = &ua.BrowseResult{
				StatusCode: ua.StatusOK,
				References: []*ua.ReferenceDescription{{
					NodeID:     ua.NewNumericExpandedNodeID(0, structure.IntID()),
					BrowseName: &ua.QualifiedName{Name: "Structure"},
					NodeClass:  ua.NodeClassDataType,
				}},
			}
		}
		return &ua.BrowseResponse{Results: results}, nil
	}}

	client := &OpcUaClient{c: c}
	r := newTypeResolver()
	budget := &requestBudget{left: 20}

	out, err := r.resolveTypes(context.Background(), client, []*ua.NodeID{custom}, budget)
	if err != nil {
		t.Fatalf("resolveTypes: %v", err)
	}
	if len(out.types) != 0 {
		t.Errorf("结构体类型不该被映射，却给出了 %+v", out.types)
	}
	// 第二次必须直接走 fail 表：问过一遍就够，同一批点位每一块都重问会白花预算。
	first := browses
	if _, err := r.resolveTypes(context.Background(), client, []*ua.NodeID{custom}, budget); err != nil {
		t.Fatalf("resolveTypes(第二次): %v", err)
	}
	if browses != first {
		t.Errorf("解析不出来的类型被重复上溯了（%d → %d 次 browse）", first, browses)
	}
}

// TestResolveTypesInvalidDataTypeAttribute DataType 属性读不到时按"类型未知"处理。
func TestResolveTypesInvalidDataTypeAttribute(t *testing.T) {
	// 三种形态：状态不是 OK、值不是 NodeID/ExpandedNodeID、整个 DataValue 缺失。
	cases := []struct {
		name string
		dv   *ua.DataValue
	}{
		{"状态非 OK", &ua.DataValue{Status: ua.StatusBadNodeIDUnknown, Value: ua.MustVariant(ua.NewNumericNodeID(0, id.Int32))}},
		{"值不是 NodeID", &ua.DataValue{Status: ua.StatusOK, Value: ua.MustVariant(int32(5))}},
		{"nil", nil},
	}
	for _, c := range cases {
		if _, ok := datatypeOf(c.dv); ok {
			t.Errorf("%s: datatypeOf 应当返回 false", c.name)
		}
	}
	// 两种合法形态都要认：库自带的服务器存的是 ExpandedNodeID，
	// 客户端 SetAttribute 写进去的又往往是 NodeID。
	exp := ua.NewNumericExpandedNodeID(3, 42)
	if got, ok := datatypeOf(&ua.DataValue{Status: ua.StatusOK, Value: ua.MustVariant(exp)}); !ok ||
		got.String() != "ns=3;i=42" {
		t.Errorf("ExpandedNodeID 形态没被认出来: %v %v", got, ok)
	}
	nid := ua.NewNumericNodeID(3, 42)
	if got, ok := datatypeOf(&ua.DataValue{Status: ua.StatusOK, Value: ua.MustVariant(nid)}); !ok ||
		got.String() != "ns=3;i=42" {
		t.Errorf("NodeID 形态没被认出来: %v %v", got, ok)
	}
}
