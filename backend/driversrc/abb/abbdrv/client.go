// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"context"
	"net/url"
	"strings"
)

// ==================== RWS 端点常量表 ====================
//
// **真机路径的唯一出处。** RW6 与 RW7 在不同小版本上对个别路径的拼写有出入
// （例如 /rw/panel/ctrl-state 的连字符），真机对不上时只改这一处，
// 不用在取值逻辑里翻找。
const (
	systemPath         = "/rw/system"
	signalsListPath    = "/rw/iosystem/signals"
	rapidSymbolData    = "/rw/rapid/symbol/data/RAPID/"
	sessionRequestPath = "/rw/elog/0"
	motionSystemBase   = "/rw/motionsystem/mechunits/"

	// RWS 2.0 的表示层协商头。JSON 只能通过带版本号的 Accept 要到：
	// 设成 application/json 会被回 406。
	acceptV2      = "application/hal+json;v=2.0"
	contentTypeV2 = "application/x-www-form-urlencoded;v=2.0"
)

// panelResourcePaths 面板资源 → 端点路径。
// 注意 ctrlstate 在 URL 里写作 ctrl-state（响应字段名则没有连字符）。
var panelResourcePaths = map[string]string{
	"ctrlstate":  "/rw/panel/ctrl-state",
	"opmode":     "/rw/panel/opmode",
	"speedratio": "/rw/panel/speedratio",
}

// resourcePath 返回该点位对应的取值端点路径（不含 base URL）。
func resourcePath(a *parsedAddr, cfg *Config) (string, error) {
	switch a.ns {
	case nsSignal:
		return signalsListPath + "/" + escapePath(a.signalPath()), nil

	case nsRapid:
		return rapidSymbolData + escapePath(a.rapidPath()), nil

	case nsPanel:
		p, ok := panelResourcePaths[a.panelRes]
		if !ok {
			return "", newParseError("abb: 未知的 panel 资源 %q", a.panelRes)
		}
		return p, nil

	case nsMotion:
		base := motionSystemBase + url.PathEscape(a.mechUnit) + "/"
		if a.motionRes == motionResJoint {
			return base + "jointtarget", nil
		}
		if cfg.CartesianSource == "cartesian" {
			q := url.Values{
				"tool":       {cfg.CartesianTool},
				"wobj":       {cfg.CartesianWobj},
				"coordinate": {cfg.CartesianCoordinate},
			}
			return base + "cartesian?" + q.Encode(), nil
		}
		return base + "robtarget", nil

	case nsSystem:
		return systemPath, nil
	}
	return "", newParseError("abb: 未知的命名空间 %q", a.ns)
}

// escapePath 逐段转义路径，保留 "/" 作为分隔符。
// 信号名与 RAPID 符号名里可能有空格或非 ASCII 字符。
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// ==================== 状态条目取值 ====================

// valueCandidates 返回该点位在状态条目里取值的候选字段名（按优先级）。
//
// 候选而非单名，是为了吸收跨版本的字段改名：RWS 1.0 的 lvalue 在部分
// 固件/资源上叫 value，lstate 也有 l-state 的写法。钉死一个名字的失败方式
// 是"某个版本下所有点位静默变空"，而空值不会报错。
func valueCandidates(a *parsedAddr) []string {
	switch a.ns {
	case nsSignal:
		switch a.field {
		case "lvalue":
			return []string{"lvalue", "value", "lval"}
		case "pvalue":
			return []string{"pvalue", "pval"}
		case "lstate":
			return []string{"lstate", "l-state"}
		case "phstate":
			return []string{"phstate", "ph-state"}
		}
		return []string{a.field}

	case nsRapid:
		// 整取（field 为空）：值是顶层字段。取分量的情况在 valueOf 里单独处理，
		// 因为那要先取出嵌套对象再下钻。
		return []string{"value", "lvalue"}

	case nsPanel:
		switch a.panelRes {
		case "ctrlstate":
			return []string{"ctrlstate", "ctrl-state", "state"}
		case "opmode":
			return []string{"opmode", "op-mode"}
		case "speedratio":
			return []string{"speedratio", "speed-ratio"}
		}

	case nsSystem:
		switch a.systemField {
		case "rwversion":
			return []string{"rwversion", "rw-version", "robotwareversion"}
		case "sysid":
			return []string{"sysid", "systemid"}
		}
		return []string{a.systemField}
	}
	return []string{a.field}
}

// valueOf 从一条状态条目里取出该点位请求的字段，返回**未按 Kind 归一化**的
// 原始字符串。
//
// 未归一化是刻意的：Kind 的解析可能要用到这条条目里的其它字段（如信号的
// type），归一化必须在 Kind 定下来之后做（见 driver.go 的组装顺序）。
func valueOf(st map[string]any, a *parsedAddr) (string, bool) {
	// RAPID 取分量：值是嵌套对象（pos/robtarget 之类），先取外层再取内层。
	if a.ns == nsRapid && a.field != "" {
		outer, ok := fieldOf(st, "value", "lvalue")
		if !ok {
			return "", false
		}
		inner, ok := outer.(map[string]any)
		if !ok {
			return "", false
		}
		sub, ok := fieldOf(inner, a.field)
		if !ok {
			return "", false
		}
		return scalarOrCompound(sub), true
	}

	v, ok := fieldOf(st, valueCandidates(a)...)
	if !ok {
		return "", false
	}
	return scalarOrCompound(v), true
}

// scalarOrCompound 标量转字符串；复合值走 compoundString（自描述文本）。
func scalarOrCompound(v any) string {
	if s, ok := scalarString(v); ok {
		return s
	}
	return compoundString(v)
}

// declaredTypeOf 取资源自己声明的 RWS 类型（信号的 DO/DI/AO/AI/GI/GO、
// RAPID 符号的 num/bool/string/pos/...）。auto 类型的 Kind 解析靠它。
func declaredTypeOf(st map[string]any, a *parsedAddr) string {
	var candidates []string
	switch a.ns {
	case nsSignal:
		candidates = []string{"type", "signaltype"}
	case nsRapid:
		candidates = []string{"type", "value-type", "valuetype"}
	default:
		return ""
	}
	v, ok := fieldOf(st, candidates...)
	if !ok {
		return ""
	}
	s, ok := scalarString(v)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// isSimulated 判断该信号是否被勾了仿真。
//
// 仿真信号的值来自控制器内部的模拟而非真实过程 IO，质量标记为"不确定"
// （128）而不是"正常"（192）—— 下游据此可以决定要不要采信。
func isSimulated(st map[string]any) bool {
	v, ok := fieldOf(st, "lstate", "l-state")
	if !ok {
		return false
	}
	s, ok := scalarString(v)
	return ok && strings.EqualFold(strings.TrimSpace(s), "simulated")
}

// ==================== 批量索引 ====================

// stateIndex 资源路径 → 状态条目。
//
// 只用于**列表里带值的资源**：IO 信号的列表响应里就有 lvalue，
// 所以一轮只发一次 GET 就能覆盖成百上千个点位。RAPID 不走这里，见 driver.go
// 的说明。
type stateIndex struct {
	byKey map[string]map[string]any
	dup   map[string]bool
}

func newStateIndex() *stateIndex {
	return &stateIndex{byKey: make(map[string]map[string]any), dup: make(map[string]bool)}
}

// add 记录一个键 → 条目的映射。
//
// **歧义键会被剔除**：同一个键（如裸信号名 DI1）被两个条目写入时，
// 说明光凭这个键无法确定是哪一个。此时宁可让查找落空、走一次单点 GET，
// 也不能随便选一个 —— 选错的表现是"把 B 设备的值记到 A 点位上"，
// 而时序库里看不出任何异常。
func (ix *stateIndex) add(key string, st map[string]any) {
	if key == "" || ix.dup[key] {
		return
	}
	if _, exists := ix.byKey[key]; exists {
		delete(ix.byKey, key)
		ix.dup[key] = true
		return
	}
	ix.byKey[key] = st
}

// lookup 按键取条目。歧义键与不存在的键都返回 false。
func (ix *stateIndex) lookup(key string) (map[string]any, bool) {
	if ix == nil {
		return nil, false
	}
	st, ok := ix.byKey[key]
	return st, ok
}

func (ix *stateIndex) len() int {
	if ix == nil {
		return 0
	}
	return len(ix.byKey)
}

// fetchSignalIndex 拉取 IO 信号全量列表并建索引。
//
// 这是"批量优先"的全部价值所在：一台典型控制器的信号在几百到几千个，
// 逐点 GET 会立刻撞上控制器 <20 req/s 的限流（何况现场还有示教器在用），
// 而列表只花一次请求。
func (c *rwsClient) fetchSignalIndex(ctx context.Context) (*stateIndex, error) {
	states, err := c.getStates(ctx, signalsListPath)
	if err != nil {
		return nil, err
	}
	return buildSignalIndex(states), nil
}

// buildSignalIndex 从信号列表的状态条目建索引。
//
// 地址允许写成 1~3 段（name / unit/name / net/unit/name），所以三级后缀键
// 都要建 —— 现场既要能写全限定名（避免同名歧义），也要能只写信号名。
func buildSignalIndex(states []map[string]any) *stateIndex {
	ix := newStateIndex()
	for _, st := range states {
		name := fieldString(st, "name")
		if name == "" {
			continue
		}
		for _, key := range signalKeys(st, name) {
			ix.add(key, st)
		}
	}
	return ix
}

// signalKeys 返回一条信号条目应建立的索引键，短键在前（name → unit/name →
// net/unit/name）。
//
// 网络段与设备段**优先取条目的具名字段，缺失时才从路径字段里切**：
// RobotWare 8.1.1 的信号列表条目根本不带 network / unitnm / devicenm，
// 完整路径只在 _title（以及 _links.self.href）里。只按具名字段拼键时，
// 索引里就只剩裸名键 —— 写全限定路径的点位永远命中不了批量列表，每轮退回
// 一次单点 GET：值取得回来，但白烧一次请求配额，点位多了还会撞上单轮回退
// 配额。两边都在时不重新解读，已有固件上的行为因此逐键不变。
func signalKeys(st map[string]any, name string) []string {
	net := fieldString(st, "network", "net")
	unit := fieldString(st, "unitnm", "unit", "device")

	if net == "" || unit == "" {
		pathNet, pathUnit, ok := splitSignalPath(entrySignalPath(st), name)
		if ok {
			if net == "" {
				net = pathNet
			}
			if unit == "" {
				unit = pathUnit
			}
		}
	}

	parts := []string{net, unit, name}
	start := 0
	for start < len(parts) && parts[start] == "" {
		start++
	}
	// 同一个条目内的重复键只建一次：stateIndex.add 把"同一个键被写入两次"
	// 读作**歧义**并剔除该键，条目内部的重合会把自己的键误删掉。
	out := make([]string, 0, len(parts)-start)
	seen := make(map[string]bool, len(parts)-start)
	for i := start; i < len(parts); i++ {
		k := strings.Join(parts[i:], "/")
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// entrySignalPath 取条目里记录的完整信号路径（网络/设备/信号），取不到返回空串。
//
// _title 是首选：它就是这条资源的路径，且各版本的列表里都在。
func entrySignalPath(st map[string]any) string {
	if s := fieldString(st, "_title"); s != "" {
		return s
	}
	// 退路是 _links.self.href，形如 "signals/<网络>/<设备>/<信号>"。
	// 取**最后一个** "signals" 段之后的内容，所以相对路径与完整 URL 都能吃下。
	links, ok := st["_links"].(map[string]any)
	if !ok {
		return ""
	}
	self, ok := links["self"].(map[string]any)
	if !ok {
		return ""
	}
	segs := splitSegments(fieldString(self, "href"))
	for i := len(segs) - 1; i >= 0; i-- {
		if strings.EqualFold(segs[i], "signals") && i+1 < len(segs) {
			return strings.Join(segs[i+1:], "/")
		}
	}
	return ""
}

// splitSignalPath 把完整路径切成网络段与设备段，并用信号名做一次校验。
//
// 末段必须等于 name，这不是装饰：_title 在别的资源上是别的东西（如面板资源的
// "ctrlstate"），而采信一条对不上的路径，症状是**读到另一个资源的值** ——
// 推送通道只按 Kind 存数，发现不了。段数超过三段（不是信号路径该有的形状）
// 同样不采信。
func splitSignalPath(path, name string) (net, unit string, ok bool) {
	segs := splitSegments(path)
	if len(segs) == 0 || segs[len(segs)-1] != name {
		return "", "", false
	}
	switch segs = segs[:len(segs)-1]; len(segs) {
	case 0:
		return "", "", true
	case 1:
		return "", segs[0], true
	case 2:
		return segs[0], segs[1], true
	}
	return "", "", false
}

// fieldString 取一个字符串字段（大小写不敏感），取不到返回空串。
func fieldString(m map[string]any, candidates ...string) string {
	v, ok := fieldOf(m, candidates...)
	if !ok {
		return ""
	}
	s, _ := scalarString(v)
	return strings.TrimSpace(s)
}
