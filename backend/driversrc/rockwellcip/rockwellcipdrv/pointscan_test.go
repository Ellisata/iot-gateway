// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// ==================== 类型映射 ====================

// TestCommonTypeOfCIP 映射的正反两面都要断言。
//
// 反面尤其重要：多映射一个（比如把 LINT 接到 Long 上）不会报错，只会让用户建出
// 一批解错值或永远没有值的点位 —— 比 panic 难发现得多。
func TestCommonTypeOfCIP(t *testing.T) {
	mapped := []struct {
		code   cip.DataType
		common string
	}{
		{cip.TypeBOOL, driver.TypeBoolean},
		{cip.TypeSINT, driver.TypeChar},
		{cip.TypeINT, driver.TypeShort},
		{cip.TypeDINT, driver.TypeLong},
		{cip.TypeREAL, driver.TypeFloat},
		{cip.TypeLREAL, driver.TypeDouble},
		{cip.TypeSTRING, driver.TypeString},
	}
	for _, tc := range mapped {
		got, ok := commonTypeOfCIP(tc.code)
		if !ok || got != tc.common {
			t.Errorf("commonTypeOfCIP(%s) = (%q, %v)，期望 (%q, true)",
				tc.code.String(), got, ok, tc.common)
		}
		// 数组位不影响元素类型。
		gotArr, okArr := commonTypeOfCIP(tc.code | 0x8000)
		if !okArr || gotArr != tc.common {
			t.Errorf("commonTypeOfCIP(%s[]) = (%q, %v)，期望 (%q, true)",
				tc.code.String(), gotArr, okArr, tc.common)
		}
	}

	// 没映射的：加了通用类型名就要动冻结的黄金基线，且与 typemap.go 既有取舍冲突。
	for _, code := range []cip.DataType{
		cip.TypeLINT, cip.TypeUSINT, cip.TypeUINT, cip.TypeUDINT, cip.TypeULINT,
		cip.TypeBYTE, cip.TypeWORD, cip.TypeDWORD, cip.TypeLWORD,
		cip.TypeSTRING2, cip.TypeSTRINGN, cip.TypeSTRINGI,
		cip.TypeSTRUCT, 0x1234,
	} {
		if got, ok := commonTypeOfCIP(code); ok {
			t.Errorf("commonTypeOfCIP(%s) 不该映射，实得 %q —— "+
				"它会建出用户分不清是设备坏了还是本来就没数据的点位",
				code.String(), got)
		}
	}
}

// TestEveryMappedCommonTypeIsInTypeMap 映射结果必须都能被网关解析成内部类型。
//
// 前端把 CommonDataType 原样回传，服务层拿它做 LookupDataType(protocol, name)；
// 一个不在 TypeMap 里的名字会让整行建点位失败，而插件这边完全看不出来。
func TestEveryMappedCommonTypeIsInTypeMap(t *testing.T) {
	tm := CommonTypeMap()
	for _, code := range []cip.DataType{
		cip.TypeBOOL, cip.TypeSINT, cip.TypeINT, cip.TypeDINT,
		cip.TypeREAL, cip.TypeLREAL, cip.TypeSTRING,
	} {
		common, ok := commonTypeOfCIP(code)
		if !ok {
			t.Fatalf("%s 没有映射", code.String())
		}
		if _, ok := tm[common]; !ok {
			t.Errorf("commonTypeOfCIP 给出的 %q 不是 TypeMap 的键 —— 建点位时会失败", common)
		}
	}
}

// ==================== 名字门槛（承重不变量） ====================

// TestPointNameRoundTrip 产出的每一个名字都必须能被驱动的地址解析器还原。
//
// 一个解析不出来的地址写进 device_address 之后，那个点位**永远没有值**（采集侧
// 会把它摘出计划、标 QualityBadAddress，见 range.go 的 partitionBadTags ——
// 修复前更糟：buildPlan 整批报错、整台设备采集全挂加误报断联）。
// 所以 describeSymbol 的硬门槛就是 ParseRockwellAddress，这里逐类钉住。
func TestPointNameRoundTrip(t *testing.T) {
	syms := []cip.SymbolInstance{
		{Name: "MotorSpeed", Type: cip.TypeDINT},
		{Name: "Recipe.Setpoint", Type: cip.TypeREAL},
		{Name: "Matrix[2,3]", Type: cip.TypeDINT},
		{Name: "Program:MainProgram.MyTag", Type: cip.TypeBOOL},
		{Name: "MyDINT.5", Type: cip.TypeBOOL},
		{Name: "Array[3]", Type: cip.TypeDINT},
		{Name: "Arr", Type: cip.TypeDINT | 0x8000}, // 数组位
		{Name: "Tag123", Type: cip.TypeSTRING},
		{Name: "A" + strings.Repeat("x", 1000), Type: cip.TypeINT}, // 贴长度上限
	}
	for _, sym := range syms {
		t.Run(sym.Name, func(t *testing.T) {
			name, _, _, ok := describeSymbol(sym)
			if !ok {
				t.Fatalf("标签 %q 被跳过，但它的名字本该是合法地址", sym.Name)
			}
			back, valid := ParseRockwellAddress(name)
			if !valid {
				t.Fatalf("产出的名字 %q 过不了 ParseRockwellAddress —— "+
					"建出的点位会永远没有值（只报「地址无效」）", name)
			}
			if back != name {
				t.Errorf("往返不一致：%q → %q", name, back)
			}
		})
	}
}

// TestUnsafeNameIsSkippedAndCounted 名字不安全的标签被跳过，且计入 Unreadable。
func TestUnsafeNameIsSkippedAndCounted(t *testing.T) {
	unsafe := []cip.SymbolInstance{
		{Name: "has space", Type: cip.TypeDINT},
		{Name: "tab\tname", Type: cip.TypeDINT},
		{Name: "温度", Type: cip.TypeDINT},                      // 非 ASCII
		{Name: "", Type: cip.TypeDINT},                        // 空名
		{Name: strings.Repeat("x", 2000), Type: cip.TypeDINT}, // 超 1024
	}
	for _, sym := range unsafe {
		if name, _, _, ok := describeSymbol(sym); ok {
			t.Errorf("标签 %q 被当成可用地址 %q —— 它会让 buildPlan 报错", sym.Name, name)
		}
	}

	res := buildScanResult(unsafe, time.Now())
	if res.Scanned != len(unsafe) || res.Unreadable != len(unsafe) || res.Readable != 0 {
		t.Errorf("计数 = scanned %d / readable %d / unreadable %d，期望 %d / 0 / %d",
			res.Scanned, res.Readable, res.Unreadable, len(unsafe), len(unsafe))
	}
	if len(res.Points) != 0 {
		t.Errorf("跳过的标签不该进清单，实得 %d 条", len(res.Points))
	}
}

// TestUnsupportedTypeIsSkippedAndCounted 类型映射不出来的标签也被跳过。
//
// 不能让它们进清单：那会建出一批永远 Quality=0 的点位，而用户分不清
// "设备坏了"与"这个点位本来就没数据"。
func TestUnsupportedTypeIsSkippedAndCounted(t *testing.T) {
	syms := []cip.SymbolInstance{
		{Name: "Good", Type: cip.TypeDINT},
		{Name: "Big", Type: cip.TypeLINT},
		{Name: "Struct1", Type: cip.TypeSTRUCT},
		{Name: "Timer1", Type: cip.TypeSTRUCT}, // TIMER/COUNTER 也回 STRUCT
		{Name: "Word1", Type: cip.TypeDWORD},
	}
	res := buildScanResult(syms, time.Now())

	if res.Scanned != 5 || res.Readable != 1 || res.Unreadable != 4 {
		t.Errorf("计数 = scanned %d / readable %d / unreadable %d，期望 5 / 1 / 4",
			res.Scanned, res.Readable, res.Unreadable)
	}
	if len(res.Points) != 1 || res.Points[0].Name != "Good" {
		t.Errorf("清单 = %+v，期望只有 Good", res.Points)
	}
}

// TestArrayTagExpandsToElementZero 数组标签产出 Name[0] 并带上说明。
func TestArrayTagExpandsToElementZero(t *testing.T) {
	sym := cip.SymbolInstance{Name: "Recipe", Type: cip.TypeDINT | 0x8000}
	name, common, note, ok := describeSymbol(sym)
	if !ok {
		t.Fatal("数组标签被跳过了")
	}
	if name != "Recipe[0]" {
		t.Errorf("名字 = %q，期望 Recipe[0]", name)
	}
	if common != driver.TypeLong {
		t.Errorf("类型 = %q，期望 %q（数组位不影响元素类型）", common, driver.TypeLong)
	}
	if !strings.Contains(note, "数组") || !strings.Contains(note, "Recipe[1]") {
		t.Errorf("说明 = %q，期望提到数组并给出 Recipe[1] 的写法", note)
	}
	if _, valid := ParseRockwellAddress(name); !valid {
		t.Errorf("产出的数组地址 %q 过不了解析器", name)
	}
}

// TestArrayNameAlreadyIndexedIsNotRewritten 设备已经报了带下标的名字时不重复加 [0]。
func TestArrayNameAlreadyIndexedIsNotRewritten(t *testing.T) {
	sym := cip.SymbolInstance{Name: "Recipe[0]", Type: cip.TypeREAL | 0x8000}
	name, _, _, ok := describeSymbol(sym)
	if !ok {
		t.Fatal("标签被跳过了")
	}
	if name != "Recipe[0]" {
		t.Errorf("名字 = %q，期望保持 Recipe[0]", name)
	}
}

// ==================== 截断与空结果 ====================

// TestMaxPointsTruncates 达到上限即停，且不变式仍然成立。
//
// 关键是"触顶之后那些**根本没试过**的标签不进 Scanned"：
// 少了这一条，Readable + Unreadable == Scanned 当场就破。
func TestMaxPointsTruncates(t *testing.T) {
	// 前一半可映射、后一半不可映射，确保截断发生在"跳过"与"列出"的混合流里。
	syms := make([]cip.SymbolInstance, 0, pointScanMaxPoints*2)
	for i := 0; i < pointScanMaxPoints*2; i++ {
		code := cip.TypeDINT
		if i%3 == 0 {
			code = cip.TypeLINT // 不可映射
		}
		syms = append(syms, cip.SymbolInstance{Name: "T" + itoa(i), Type: code})
	}

	res := buildScanResult(syms, time.Now())

	if !res.Truncated {
		t.Error("Truncated = false，期望 true")
	}
	if len(res.Points) != pointScanMaxPoints || res.Readable != pointScanMaxPoints {
		t.Errorf("清单 %d 条 / readable %d，期望都是 %d",
			len(res.Points), res.Readable, pointScanMaxPoints)
	}
	// 触顶即停：剩下的标签**根本没试过**，既不该进 Scanned 也不该进 Unreadable。
	// 注意 Scanned 可以大于 MaxPoints —— 中途被跳过的标签照样算"试过"。
	if res.Scanned >= len(syms) {
		t.Errorf("Scanned = %d（共 %d 个标签）—— 触顶之后还在往下扫",
			res.Scanned, len(syms))
	}
	if res.Readable+res.Unreadable != res.Scanned {
		t.Errorf("不变式破了：readable %d + unreadable %d != scanned %d",
			res.Readable, res.Unreadable, res.Scanned)
	}
}

// TestEmptyEnumerationIsNotAnError 设备一个标签都没有是正常答案，不是错误。
//
// Points 必须是**非 nil 的空切片**：nil 过线是 JSON null，下游
// （service 的"带着部分结果的失败"判定、前端的行构造）都得为它多写一条分支。
func TestEmptyEnumerationIsNotAnError(t *testing.T) {
	res := buildScanResult(nil, time.Now())
	if res == nil {
		t.Fatal("buildScanResult 返回了 nil")
	}
	if res.Points == nil {
		t.Error("Points 是 nil —— 过线会变成 JSON null")
	}
	if len(res.Points) != 0 || res.Scanned != 0 || res.Readable != 0 || res.Unreadable != 0 {
		t.Errorf("空枚举的计数应为全 0，实得 %+v", res)
	}
	if res.Truncated || res.Incomplete {
		t.Error("空枚举不该置任何标志")
	}
}

// TestBuildScanResultLeavesNoSampleValues 本协议的产出里没有采样值。
//
// 这是一条跨端约定：AssumedCommonDataType 留空 == "本次没有采样值"，
// 前端据它隐藏「显示 0 值」开关。填了值或填了类型都会让那条约定失效。
func TestBuildScanResultLeavesNoSampleValues(t *testing.T) {
	res := buildScanResult([]cip.SymbolInstance{{Name: "A", Type: cip.TypeDINT}}, time.Now())
	if res.AssumedCommonDataType != "" {
		t.Errorf("AssumedCommonDataType = %q，期望空（本协议不读采样值）",
			res.AssumedCommonDataType)
	}
	for _, p := range res.Points {
		if p.Value != "" {
			t.Errorf("点位 %q 带了值 %q —— 本协议不读采样值", p.Name, p.Value)
		}
	}
	// 反过来：每一行都必须带自己的类型，否则前端只能落到结果级的兜底类型上。
	if res.Points[0].CommonDataType != driver.TypeLong {
		t.Errorf("逐点类型 = %q，期望 %q", res.Points[0].CommonDataType, driver.TypeLong)
	}
}

// ==================== 选项解析 ====================

func TestParsePointScanOptions(t *testing.T) {
	cases := []struct {
		name    string
		options string
		wantErr bool
	}{
		{"未填（长度 0）", "", false},
		{"空对象", "{}", false},
		{"多余字段照样接受", `{"whatever":1}`, false},
		{"坏 JSON", `{"a":`, true},
		{"不是对象", `[1,2]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			if tc.options != "" {
				raw = json.RawMessage(tc.options)
			}
			err := parsePointScanOptions(raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v，期望 wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// ==================== finder 的契约 ====================

// enumSession 把一次扫描交给一个现成的枚举器，模拟宿主的两条会话路径
// （复用 / 临时实例）。Do 是直调 —— 两条路上的 fn 语义一致，差别只在锁，
// 而锁不是这里要测的东西（守护逻辑在 drivesdk 与 client_test.go）。
type enumSession struct {
	drv     driver.Driver
	doCalls int
	doErr   error
}

func (s *enumSession) Do(fn func(drv driver.Driver) error) error {
	s.doCalls++
	if s.doErr != nil {
		return s.doErr
	}
	return fn(s.drv)
}

// newScanDriver 造一个"已连接、能枚举"的驱动实例。
func newScanDriver(enum func(ctx context.Context) ([]cip.SymbolInstance, error)) *rockwellDriver {
	return &rockwellDriver{
		client: &mockTransport{connected: true, listTags: enum},
		warned: map[string]string{},
	}
}

// TestScanPointsReturnsPoints 走通一次正常枚举。
func TestScanPointsReturnsPoints(t *testing.T) {
	drv := newScanDriver(func(context.Context) ([]cip.SymbolInstance, error) {
		return []cip.SymbolInstance{
			{Name: "MotorSpeed", Type: cip.TypeDINT},
			{Name: "Temperature", Type: cip.TypeREAL},
			{Name: "Run", Type: cip.TypeBOOL},
			{Name: "Name", Type: cip.TypeSTRING},
			{Name: "Big", Type: cip.TypeLINT}, // 会被跳过
		}, nil
	})
	sess := &enumSession{drv: drv}

	res, err := ScanPoints(sess, nil, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("ScanPoints = %v", err)
	}
	if len(res.Points) != 4 || res.Scanned != 5 || res.Readable != 4 || res.Unreadable != 1 {
		t.Fatalf("结果 = %+v（%d 条），期望 5 扫 / 4 列 / 1 跳过",
			res, len(res.Points))
	}
	want := map[string]string{
		"MotorSpeed":  driver.TypeLong,
		"Temperature": driver.TypeFloat,
		"Run":         driver.TypeBoolean,
		"Name":        driver.TypeString,
	}
	for _, p := range res.Points {
		if want[p.Name] != p.CommonDataType {
			t.Errorf("点位 %q 的类型 = %q，期望 %q", p.Name, p.CommonDataType, want[p.Name])
		}
	}
	if sess.doCalls != 1 {
		t.Errorf("Do 调了 %d 次，期望 1 次（枚举不可分块，没有第二次）", sess.doCalls)
	}
}

// TestScanPointsRespectsBudget 枚举卡住时必须在预算内**返回错误**，不返回空结果。
//
// 这是本 finder 与 Modbus/S7/FINS 最要紧的一处不同：那边超预算时给的是
// "部分结果 + incomplete"，这边给不了部分结果（ListTags 是整只对象的操作），
// 所以只能报错。**绝不能**返回空 + incomplete —— 那会渲染成一张 0 行的表，
// 而用户会从"一个标签都没有"得出"这台设备没有点位"。
//
// 场景同时也是真的：库的 TCP 传输不设读超时，一个"TCP 通、CIP 不应答"的设备
// 会让 ListTags 永久阻塞。
func TestScanPointsRespectsBudget(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)

	drv := newScanDriver(func(ctx context.Context) ([]cip.SymbolInstance, error) {
		<-blocked // 模拟设备不应答：永远不返回
		return nil, errors.New("never reached")
	})
	sess := &enumSession{drv: drv}

	budget := 300 * time.Millisecond
	start := time.Now()
	res, err := ScanPoints(sess, nil, time.Now().Add(budget))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("期望超时错误，实得结果 %+v", res)
	}
	if res != nil {
		t.Errorf("超预算时必须返回 nil 结果（拿不到部分结果），实得 %+v", res)
	}
	if !strings.Contains(err.Error(), "预算") {
		t.Errorf("错误信息没提预算：%v", err)
	}
	// 上限给得宽松：这条断言要抓的是"根本没兜住"（会是几十秒，撞上网关侧超时），
	// 不是几十毫秒的调度抖动。
	if elapsed > budget+2*time.Second {
		t.Errorf("耗时 %v 远超预算 %v —— 守卫没兜住", elapsed, budget)
	}
}

// TestScanPointsBudgetAlreadySpent 预算在拿到锁之前就被等光了，要报得明白。
//
// 这条路径真实存在：selectSession 为了确认"能不能复用采集会话"最多等
// pointScanSessionBudget（5s），而 Rockwell 永远匹配不上（没有 ConnectionMatcher）。
func TestScanPointsBudgetAlreadySpent(t *testing.T) {
	drv := newScanDriver(func(context.Context) ([]cip.SymbolInstance, error) {
		t.Error("预算已经耗尽，不该发起枚举")
		return nil, nil
	})
	res, err := ScanPoints(&enumSession{drv: drv}, nil, time.Now().Add(-time.Second))
	if err == nil || res != nil {
		t.Fatalf("期望一条明确的「预算已耗尽」错误，实得 (%+v, %v)", res, err)
	}
	if !errors.Is(err, errEnumerationBudgetSpent) {
		t.Errorf("错误 = %v，期望 errEnumerationBudgetSpent", err)
	}
}

// TestScanPointsRejectsDisconnectedDriver 未连接的设备要有一句能看懂的话。
func TestScanPointsRejectsDisconnectedDriver(t *testing.T) {
	drv := &rockwellDriver{client: &mockTransport{connected: false}, warned: map[string]string{}}
	res, err := ScanPoints(&enumSession{drv: drv}, nil, time.Now().Add(5*time.Second))
	if err == nil || res != nil {
		t.Fatalf("期望「未连接」错误，实得 (%+v, %v)", res, err)
	}
	if !strings.Contains(err.Error(), "未连接") {
		t.Errorf("错误信息 = %v，期望提到未连接", err)
	}
}

// TestScanPointsWrapsEnumerationError 枚举失败时把根因包在一条可读的话里。
func TestScanPointsWrapsEnumerationError(t *testing.T) {
	root := errors.New("boom")
	drv := newScanDriver(func(context.Context) ([]cip.SymbolInstance, error) {
		return nil, root
	})
	res, err := ScanPoints(&enumSession{drv: drv}, nil, time.Now().Add(5*time.Second))
	if err == nil || res != nil {
		t.Fatalf("期望错误，实得 (%+v, %v)", res, err)
	}
	if !errors.Is(err, root) {
		t.Errorf("根因没被保留：%v", err)
	}
	if !strings.Contains(err.Error(), "0x6B") {
		t.Errorf("错误信息 = %v，期望指出可能不支持 Symbol Object 类 0x6B", err)
	}
}

// TestScanPointsRejectsBadOptions 坏选项在发起枚举之前就被拒。
func TestScanPointsRejectsBadOptions(t *testing.T) {
	drv := newScanDriver(func(context.Context) ([]cip.SymbolInstance, error) {
		t.Error("选项非法时不该发起枚举")
		return nil, nil
	})
	if _, err := ScanPoints(&enumSession{drv: drv}, json.RawMessage(`{"a":`),
		time.Now().Add(5*time.Second)); err == nil {
		t.Fatal("坏 JSON 选项没被拒")
	}
}

// TestScanPointsNilSession 没有会话时给一句明确的话，而不是 panic。
func TestScanPointsNilSession(t *testing.T) {
	if _, err := ScanPoints(nil, nil, time.Now().Add(time.Second)); err == nil {
		t.Fatal("nil 会话没被拒")
	}
}

// ==================== 端到端（真实假 PLC） ====================

// TestScanPointsAgainstFakePLC 对着一个真实的假控制器跑通整条链路：
// 建驱动 → Connect → ScanPoints。中间经过的每一层都是真的（TCP、会话注册、
// CIP 消息路由、Symbol Object 应答），只有 PLC 是假的。
//
// 这一层是**驱动包内的单测补不上的**：它验的是"库的 ListTags 与我们的假应答
// 确实对得上"，也就是请求路径、属性表布局、类型码字节序这几处最容易两边一起写错
// 的地方。
func TestScanPointsAgainstFakePLC(t *testing.T) {
	srv, err := fake.NewCIPABWithTags(
		fake.CIPTag{Name: "MotorSpeed", Type: fake.CIPTypeDINT},
		fake.CIPTag{Name: "Temperature", Type: fake.CIPTypeREAL},
		fake.CIPTag{Name: "Run", Type: fake.CIPTypeBOOL},
		fake.CIPTag{Name: "Recipe", Type: fake.CIPTypeDINT | fake.CIPArrayBit},
		fake.CIPTag{Name: "BigCounter", Type: fake.CIPTypeLINT}, // 无通用类型 → 跳过
		fake.CIPTag{Name: "Timer1", Type: fake.CIPTypeSTRUCT},   // 结构体 → 跳过
		fake.CIPTag{Name: "Odd Name", Type: fake.CIPTypeINT},    // 名字含空格 → 跳过
	)
	if err != nil {
		t.Fatalf("起假 PLC 失败: %v", err)
	}
	defer srv.Close()

	drv := newRockwellDriver()
	cfg := `{"host":"127.0.0.1","port":` + itoa(srv.Port()) + `}`
	if err := drv.Connect(cfg); err != nil {
		t.Fatalf("连接假 PLC 失败: %v", err)
	}
	defer drv.Close()

	// 用宿主同款的一次性会话（临时实例路径，Rockwell 的点扫必然走这条）。
	res, err := ScanPoints(&enumSession{drv: drv}, nil, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("ScanPoints = %v", err)
	}

	if res.Scanned != 7 {
		t.Errorf("Scanned = %d，期望 7（设备一共 7 个标签）", res.Scanned)
	}
	if res.Readable != 4 || res.Unreadable != 3 {
		t.Errorf("readable %d / unreadable %d，期望 4 / 3", res.Readable, res.Unreadable)
	}

	got := map[string]string{}
	for _, p := range res.Points {
		got[p.Name] = p.CommonDataType
		if _, ok := ParseRockwellAddress(p.Name); !ok {
			t.Errorf("产出的地址 %q 过不了解析器", p.Name)
		}
	}
	want := map[string]string{
		"MotorSpeed":  driver.TypeLong,
		"Temperature": driver.TypeFloat,
		"Run":         driver.TypeBoolean,
		"Recipe[0]":   driver.TypeLong,
	}
	for name, common := range want {
		if got[name] != common {
			t.Errorf("点位 %q 的类型 = %q，期望 %q（实得清单：%v）",
				name, got[name], common, got)
		}
	}
}

// TestScanPointsAgainstEmptyFakePLC 一台没有任何标签的控制器：空清单，不是错误。
func TestScanPointsAgainstEmptyFakePLC(t *testing.T) {
	srv, err := fake.NewCIPABWithTags()
	if err != nil {
		t.Fatalf("起假 PLC 失败: %v", err)
	}
	defer srv.Close()

	drv := newRockwellDriver()
	if err := drv.Connect(`{"host":"127.0.0.1","port":` + itoa(srv.Port()) + `}`); err != nil {
		t.Fatalf("连接假 PLC 失败: %v", err)
	}
	defer drv.Close()

	res, err := ScanPoints(&enumSession{drv: drv}, nil, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("空枚举被当成错误：%v", err)
	}
	if res.Points == nil || len(res.Points) != 0 {
		t.Errorf("Points = %v，期望非 nil 的空切片", res.Points)
	}
	if res.Scanned != 0 {
		t.Errorf("Scanned = %d，期望 0", res.Scanned)
	}
}

// itoa 是本文件的小工具：避免为了一个 strconv 再引一个 import。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestDescribeSymbolNoteIsValidUTF8 说明文案里拼进去的标签名不能把消息弄成坏串。
//
// 非 UTF-8 的标签名会被解析器挡在门外（它只收可打印 ASCII），所以正常走不到；
// 这条防的是"门槛被放松之后"文案先坏掉。
func TestDescribeSymbolNoteIsValidUTF8(t *testing.T) {
	sym := cip.SymbolInstance{Name: "Arr", Type: cip.TypeDINT | 0x8000}
	_, _, note, _ := describeSymbol(sym)
	if !utf8.ValidString(note) {
		t.Fatalf("说明不是合法 UTF-8: %q", note)
	}
}
