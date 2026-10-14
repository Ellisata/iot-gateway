// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"fmt"
	"strings"
)

// 地址命名空间。它们是地址里 ":" 之前的那一段，也是驱动内部的分派键。
const (
	nsSignal = "signal" // IO 信号：net/unit/name
	nsRapid  = "rapid"  // RAPID 变量/常量：task/module/symbol
	nsPanel  = "panel"  // 控制器面板：ctrlstate / opmode / speedratio
	nsMotion = "motion" // 运动状态：<mechunit>/joint|<cartesian>/<field>
	nsSystem = "system" // 系统信息：<field>
)

// fieldSignalValue 是 signal / rapid 的"取信号值本身"字段名。
// 它同时对应 RWS 响应里的 lvalue 键，见 parse.go 的候选名表。
const fieldSignalValue = "lvalue"

// defaultMechUnit 是 motion 地址省略机械单元时的默认值。
// ROB_1 是单机器人控制器的标准单元名（多机器人系统才需要显式写）。
const defaultMechUnit = "ROB_1"

// parseError 地址字符串本地解析失败（语法/格式层面），区别于 HTTP 请求失败
// （服务级）与网络传输失败（传输级）。
//
// 驱动 Read 据此把本地解析失败的点位标记 Quality=0 并**跳过请求计划**：
// 一个手滑写错的地址不应该在每一轮里都产生一次必然 404 的 HTTP 往返。
// 形状对齐 driversrc/opcua/opcuadrv/nodeid.go 的同名类型。
type parseError struct{ err error }

func (e *parseError) Error() string { return e.err.Error() }

func (e *parseError) Unwrap() error { return e.err }

func newParseError(format string, args ...any) *parseError {
	return &parseError{fmt.Errorf(format, args...)}
}

// parsedAddr 单个点位地址的解析结果。
//
// 各 namespace 的定位信息用各自的槽位表达，而不是塞进一个 []string ——
// 驱动的取值逻辑要按 namespace 读不同的段，具名字段能让那几处分支一眼看懂。
// **字段之间是互斥的**：一次解析只会填满对应 namespace 的那几个。
type parsedAddr struct {
	ns string

	// signal：IO 信号路径。三段中缺失的段为空串（RWS 允许只写信号名）。
	signalNet  string
	signalUnit string
	signalName string

	// rapid
	rapidTask   string
	rapidModule string
	rapidSymbol string

	// panel：规范化后的资源令牌（ctrlstate / opmode / speedratio）
	panelRes string

	// motion
	mechUnit  string
	motionRes string // motionResJoint | motionResCartesian

	// system：规范化后的字段名
	systemField string

	// field 是**生效的**字段选择器：各 namespace 的默认值已在解析时填好，
	// 调用方不需要再判空。空串表示"整个值"（rapid 的复合值、motion 的整组），
	// 仅对这两个 namespace 成立。
	field string
}

// motionRes* 是 motion namespace 的两种资源。
const (
	motionResJoint     = "joint"     // 关节角，端点 .../jointtarget
	motionResCartesian = "cartesian" // 笛卡尔位姿，端点 .../robtarget
)

// 解析语法：
//
//	address := [<ns> ":"] <body> ["#" <field>]      ; 方言一：命名空间前缀
//	         | "/rw/" <nativepath> ["#" <field>]    ; 方言二：RWS 原生路径
//	ns      := signal | rapid | panel | motion | system     ; 大小写不敏感
//
// **裸地址（无前缀）一律拒绝。** `Local/DRV_1/DI1` 与 `rapid:` 的三段 body 在
// 语法上完全同形，任何"没前缀就当 IO 信号"的回退都是在猜；而猜错的症状是
// **静默读到另一个资源的值** —— 推送通道只按 Kind 存数，发现不了。补偿手段是
// 方言二：用户可以把自己在浏览器里验证过的 RWS 路径整条粘进来。
func parseAddress(name string) (*parsedAddr, error) {
	s := strings.TrimSpace(name)
	if s == "" {
		return nil, newParseError("abb: 空地址")
	}

	body, field, err := splitField(s)
	if err != nil {
		return nil, err
	}

	// 方言二：归一化成方言一的字符串，再走同一条解析路径。
	// 「归一化而非分支」保证两种写法解析出的结构逐字段相同（有测试钉住）。
	if strings.HasPrefix(strings.ToLower(body), "/rw/") || strings.EqualFold(strings.TrimRight(body, "/"), "/rw") {
		body, err = normalizeNativePath(body, field)
		if err != nil {
			return nil, err
		}
	}

	ns, rest, ok := strings.Cut(body, ":")
	if !ok {
		return nil, newParseError(
			"abb: 地址 %q 缺少命名空间前缀，请写成 signal:Local/DRV_1/DI1 这样；"+
				"也可以直接粘贴 RWS 路径（如 /rw/iosystem/signals/Local/DRV_1/DI1）", name)
	}
	nsLower := strings.ToLower(strings.TrimSpace(ns))

	var a *parsedAddr
	switch nsLower {
	case nsSignal:
		a, err = parseSignalBody(rest)
	case nsRapid:
		a, err = parseRapidBody(rest)
	case nsPanel:
		a, err = parsePanelBody(rest)
	case nsMotion:
		a, err = parseMotionBody(rest)
	case nsSystem:
		a, err = parseSystemBody(rest)
	default:
		return nil, newParseError("abb: 未知的命名空间 %q（可用：signal / rapid / panel / motion / system）", ns)
	}
	if err != nil {
		return nil, err
	}
	a.ns = nsLower

	// 显式字段选择器覆盖 namespace 的默认值。
	if field != "" {
		a.field = field
	}
	return a, nil
}

// splitField 切出 "#field" 选择器。
// "#" 之后的空白一律去掉；给了 "#" 却是空的，是明确的书写错误。
func splitField(s string) (body, field string, err error) {
	body, field, has := strings.Cut(s, "#")
	if !has {
		return strings.TrimSpace(s), "", nil
	}
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" {
		return "", "", newParseError("abb: 地址 %q 的 \"#\" 后面没有字段名", s)
	}
	return strings.TrimSpace(body), field, nil
}

// parseSignalBody 解析 signal 的 body：1~3 段路径 [net/unit/]name。
func parseSignalBody(rest string) (*parsedAddr, error) {
	segs := splitSegments(rest)
	// RWS 用 ";" 分隔资源属性后缀，信号上唯一有文档的是 "值-only" 的 ;state
	// （与不带它等价）。这里从**第一个分号**截断，而不是只认 ;state：
	// ABB 的信号名只允许字母数字与下划线，";" 不可能出现在名字里，所以截断是
	// 安全的；而放行未知后缀的代价是每轮一次必然 404 的往返（直到负缓存生效）。
	if n := len(segs); n > 0 {
		if i := strings.IndexByte(segs[n-1], ';'); i >= 0 {
			segs[n-1] = segs[n-1][:i]
		}
	}
	switch len(segs) {
	case 1:
		return &parsedAddr{signalName: segs[0], field: fieldSignalValue}, nil
	case 2:
		return &parsedAddr{signalUnit: segs[0], signalName: segs[1], field: fieldSignalValue}, nil
	case 3:
		return &parsedAddr{signalNet: segs[0], signalUnit: segs[1], signalName: segs[2], field: fieldSignalValue}, nil
	case 0:
		return nil, newParseError("abb: signal 地址缺少信号名")
	default:
		return nil, newParseError(
			"abb: signal 地址最多三段（网络/设备/信号），实际 %d 段；示例：signal:Local/DRV_1/DI1", len(segs))
	}
}

// parseRapidBody 解析 rapid 的 body：task/module/symbol，可带可选的 RAPID 前缀。
func parseRapidBody(rest string) (*parsedAddr, error) {
	segs := splitSegments(rest)
	if len(segs) > 0 && strings.EqualFold(segs[0], "RAPID") {
		segs = segs[1:]
	}
	if len(segs) != 3 {
		return nil, newParseError(
			"abb: rapid 地址必须是 task/module/symbol 三段，实际 %d 段；示例：rapid:T_ROB1/Module1/nTest", len(segs))
	}
	return &parsedAddr{rapidTask: segs[0], rapidModule: segs[1], rapidSymbol: segs[2]}, nil
}

// panelResources panel namespace 的资源白名单（规范化后）。
//
// 这里**必须**白名单：写错一个字母会变成一个每轮都必然 404 的请求，
// 而 404 的响应里没有任何信息指向"是你打错了"。
var panelResources = map[string]string{
	"ctrlstate":  "ctrlstate",
	"opmode":     "opmode",
	"speedratio": "speedratio",
}

// parsePanelBody 解析 panel 的 body：单个资源令牌。
// "ctrl-state" 与 "ctrlstate" 等价（去掉连字符后比对）。
func parsePanelBody(rest string) (*parsedAddr, error) {
	token := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(rest), "-", ""))
	if token == "" {
		return nil, newParseError("abb: panel 地址缺少资源名；可用：panel:ctrlstate / panel:opmode / panel:speedratio")
	}
	res, ok := panelResources[token]
	if !ok {
		return nil, newParseError(
			"abb: panel 不支持的资源 %q；可用：panel:ctrlstate / panel:opmode / panel:speedratio", rest)
	}
	// 默认取的字段与资源同名（响应条目里就是 ctrlstate/opmode/speedratio 三个键）。
	return &parsedAddr{panelRes: res, field: res}, nil
}

// motionFields 各运动资源的字段白名单。
//
// 同 panel 的理由：写错字段名会变成一个每轮都取不到值的点位，而现场的排查手段
// 只有翻日志。关节/笛卡尔的字段集合由 ABB 定义且极稳定（六轴 + 外部轴），
// 白名单不会误伤。
var motionFields = map[string]map[string]bool{
	motionResJoint: {
		"rax_1": true, "rax_2": true, "rax_3": true,
		"rax_4": true, "rax_5": true, "rax_6": true,
		"rex_1": true, "rex_2": true, "rex_3": true,
		"rex_4": true, "rex_5": true, "rex_6": true,
		"extjoint": true,
	},
	motionResCartesian: {
		"x": true, "y": true, "z": true,
		"q1": true, "q2": true, "q3": true, "q4": true,
		"extjoint": true,
	},
}

// parseMotionBody 解析 motion 的 body，接受四种写法：
//
//	<mechunit>/joint/<field>      完整形式（推荐）
//	<mechunit>/joint              整组关节角（复合值，Kind=string）
//	joint/<field>                 省略机械单元，回退 ROB_1
//	joint                         同上，整组
//
// 首段命中资源名时视为省略了机械单元 —— 反之亦然，两者不会同时命中，
// 因为 "joint"/"cartesian" 不是合法的机械单元名。
func parseMotionBody(rest string) (*parsedAddr, error) {
	segs := splitSegments(rest)
	if len(segs) == 0 {
		return nil, newParseError("abb: motion 地址缺少内容；示例：motion:ROB_1/joint/rax_1")
	}
	mech := defaultMechUnit
	if !isMotionResource(segs[0]) {
		mech = segs[0]
		segs = segs[1:]
	}
	if len(segs) == 0 {
		return nil, newParseError("abb: motion 地址 %q 缺少资源；可用：joint / cartesian", rest)
	}
	res, err := normalizeMotionResource(segs[0])
	if err != nil {
		return nil, err
	}
	segs = segs[1:]

	a := &parsedAddr{mechUnit: mech, motionRes: res}
	switch len(segs) {
	case 0:
		return a, nil // 整组复合值，field 留空
	case 1:
		field, err := normalizeMotionField(res, segs[0])
		if err != nil {
			return nil, err
		}
		a.field = field
		return a, nil
	default:
		return nil, newParseError("abb: motion 地址段数过多；示例：motion:ROB_1/joint/rax_1")
	}
}

func isMotionResource(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case motionResJoint, motionResCartesian:
		return true
	}
	return false
}

func normalizeMotionResource(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case motionResJoint, "jointtarget":
		return motionResJoint, nil
	case motionResCartesian, "robtarget":
		return motionResCartesian, nil
	}
	return "", newParseError("abb: motion 不支持的资源 %q；可用：joint / cartesian", s)
}

// normalizeMotionField 归一化字段别名并校验白名单。
//
// 现场习惯把 rax_1 写成 rax1（示教器上显示的就是 RAPID 的 rax1 风格），
// 两种写法都接受，统一成响应里的键名 rax_1。
func normalizeMotionField(res, s string) (string, error) {
	f := strings.ToLower(strings.TrimSpace(s))
	// rax1 → rax_1：仅对 "r" + "ax"/"ex" + 单个数字 这一形状生效，
	// 所以 "extjoint" 之类不会被误改。
	if len(f) == 4 && f[0] == 'r' && (f[1] == 'a' || f[1] == 'e') && f[2] == 'x' && isDigit(f[3]) {
		f = f[:3] + "_" + f[3:]
	}
	if !motionFields[res][f] {
		return "", newParseError(
			"abb: motion/%s 不支持的字段 %q；可用字段见 motionFields 白名单（rax_1..rax_6 / rex_1..rex_6 / x,y,z,q1..q4 / extjoint）", res, s)
	}
	return f, nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// parseSystemBody 解析 system 的 body：单个字段名。
//
// **不设白名单**：系统资源（/rw/system）的字段名随 RobotWare 版本增减，
// 白名单会把"这个版本有、下个版本才有"的字段判成语法错误，把可用性变没了。
// 取不到时按服务级回落（Quality=0 + 一行日志），用户至少能看出是字段名不对。
func parseSystemBody(rest string) (*parsedAddr, error) {
	f := strings.ToLower(strings.TrimSpace(rest))
	if f == "" {
		return nil, newParseError("abb: system 地址缺少字段名；示例：system:rwversion")
	}
	return &parsedAddr{systemField: f, field: f}, nil
}

// normalizeNativePath 把方言二（RWS 原生路径）改写成方言一的字符串。
//
// field 参与其中：/rw/system 本身不带字段，写成 /rw/system#rwversion 才构成
// 一个完整地址——这是唯一一处原生路径需要外部信息才能归一化的情况。
//
// 认不出的原生路径**返回 parseError 而不是原样透传**：透传会走到"缺少命名空间
// 前缀"的分支，报错信息指向错误的方向（用户明明写了 /rw/）。
func normalizeNativePath(p, field string) (string, error) {
	segs := splitSegments(p)
	// 去掉开头的 "rw"（splitSegments 已丢掉空段，所以 /rw/... 的首段就是 "rw"）。
	if len(segs) == 0 || !strings.EqualFold(segs[0], "rw") {
		return "", newParseError("abb: 无法识别的 RWS 路径 %q", p)
	}
	segs = segs[1:]
	if len(segs) == 0 {
		return "", newParseError("abb: 无法识别的 RWS 路径 %q（根路径下没有可取的资源）", p)
	}

	switch strings.ToLower(segs[0]) {
	case "iosystem":
		// /rw/iosystem/signals/<net>/<unit>/<name>
		if len(segs) < 3 || !strings.EqualFold(segs[1], "signals") {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；只支持 /rw/iosystem/signals/<网络>/<设备>/<信号>", p)
		}
		rest := segs[2:]
		if len(rest) > 3 {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；信号路径最多三段", p)
		}
		return nsSignal + ":" + strings.Join(rest, "/"), nil

	case "rapid":
		// /rw/rapid/symbol/data/RAPID/<task>/<module>/<symbol>
		// 也接受省略 data 段、省略 RAPID 段的写法。
		if len(segs) < 3 || !strings.EqualFold(segs[1], "symbol") {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；只支持 /rw/rapid/symbol/data/RAPID/<任务>/<模块>/<符号>", p)
		}
		rest := segs[2:]
		if len(rest) > 0 && strings.EqualFold(rest[0], "data") {
			rest = rest[1:]
		}
		if len(rest) > 0 && strings.EqualFold(rest[0], "RAPID") {
			rest = rest[1:]
		}
		if len(rest) != 3 {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；RAPID 符号需要任务/模块/符号三段", p)
		}
		return nsRapid + ":" + strings.Join(rest, "/"), nil

	case "panel":
		if len(segs) != 2 {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；只支持 /rw/panel/<资源>", p)
		}
		return nsPanel + ":" + segs[1], nil

	case "motionsystem":
		// /rw/motionsystem/mechunits/<mechunit>/<jointtarget|robtarget|cartesian>
		if len(segs) != 4 || !strings.EqualFold(segs[1], "mechunits") {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；只支持 /rw/motionsystem/mechunits/<机械单元>/<jointtarget|robtarget>", p)
		}
		res, err := normalizeMotionResource(segs[3])
		if err != nil {
			return "", err
		}
		return nsMotion + ":" + segs[2] + "/" + res, nil

	case "system":
		// /rw/system 自己不带字段，靠 #field 补全。
		if len(segs) != 1 {
			return "", newParseError("abb: 无法识别的 RWS 路径 %q；系统信息请用 /rw/system#<字段> 或 system:<字段>", p)
		}
		if field == "" {
			return "", newParseError(
				"abb: /rw/system 需要配合字段使用，例如 /rw/system#rwversion 或 system:rwversion")
		}
		return nsSystem + ":" + field, nil
	}
	return "", newParseError("abb: 无法识别的 RWS 路径 %q（不支持的资源类型 %q）", p, segs[0])
}

// splitSegments 按 "/" 切分并丢弃空段（容忍重复斜杠与首尾斜杠）。
func splitSegments(s string) []string {
	raw := strings.Split(s, "/")
	out := make([]string, 0, len(raw))
	for _, seg := range raw {
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// signalPath 返回 IO 信号在批量索引里的完整键（net/unit/name 中非空的部分）。
func (a *parsedAddr) signalPath() string {
	parts := make([]string, 0, 3)
	for _, s := range []string{a.signalNet, a.signalUnit, a.signalName} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}

// rapidPath 返回 RAPID 符号在批量索引里的完整键（task/module/symbol）。
func (a *parsedAddr) rapidPath() string {
	return a.rapidTask + "/" + a.rapidModule + "/" + a.rapidSymbol
}
