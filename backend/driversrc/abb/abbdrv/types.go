// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package abb

import (
	"strconv"
	"strings"
	"sync"

	"iot-gateway/driver"
)

// pluginScope 类型注册 scope 前缀（driver.TypeRegistry.ForProtocol 用），
// 同时也是 driversdk.Meta.Scope。
const pluginScope = "abb"

// 内部类型裸名。它们在 typemap.go 里被映射到通用类型名，在 address 的
// data_type 列里被逐字存下来（网关侧会拼上 scope 前缀存成 "abb.int"）。
const (
	typeAuto   = "auto"
	typeBool   = "bool"
	typeInt    = "int"
	typeUint   = "uint"
	typeFloat  = "float"
	typeString = "string"
	typeTime   = "time"
)

// abbScope 类型注册表作用域缓存。
var abbScope = driver.GetTypeRegistry().ForProtocol(pluginScope)

var (
	abbLookupOnce sync.Once
	abbTypes      map[string]*driver.DataType
)

// abbLookup 按裸名查 DataType：优先命中静态缓存（零分配、无锁），
// 未命中回退注册表查询。类型注册表在 init() 后不再变化，按裸名缓存
// 可消除每点位对全局注册表 RWMutex 的查询与锁竞争（对齐 s7/opcua 模式）。
func abbLookup(name string) (*driver.DataType, bool) {
	abbLookupOnce.Do(func() {
		m := make(map[string]*driver.DataType)
		prefix := pluginScope + "."
		for _, full := range driver.GetTypeRegistry().Names() {
			bare, ok := strings.CutPrefix(full, prefix)
			if !ok {
				continue
			}
			dt, ok := abbScope.Get(bare)
			if !ok {
				continue
			}
			m[bare] = dt
		}
		abbTypes = m
	})
	if dt, ok := abbTypes[name]; ok {
		return dt, true
	}
	return abbScope.Get(name)
}

func init() {
	// ========== 注册 ABB RWS 数据类型（自动添加 "abb." 前缀） ==========
	//
	// RWS 在响应里以**已解析的字符串**返回值（`"lvalue":"0"`、`"rax_1":"-0.000000"`），
	// 不需要字节级 Decode/Encode；Kind 是推送通道分类的唯一依据。
	//
	// 同理没有 Format 函数：值的最终形态由本包的 formatValue 按 Kind 归一化后
	// 直接放进 ReadResult.Value（见 driver/types.go 的序列化契约 —— 那三个函数
	// 本来也出不了插件进程）。
	reg := func(name, kind string) {
		abbScope.Register(&driver.DataType{Name: name, Kind: kind})
	}

	// auto 的 Kind 是**占位**：Read 时由 resolveKind 按资源声明的 RWS 类型覆盖。
	// 注册成 string 而非留空，是因为 Meta.Validate 拒绝 Kind 为空的类型，
	// 而 string 恰好也是 resolveKind 兜底时的落点 —— 两者一致，不会误导。
	reg(typeAuto, driver.KindString)
	reg(typeBool, driver.KindBool)
	reg(typeInt, driver.KindInt)
	reg(typeUint, driver.KindUInt)
	reg(typeFloat, driver.KindFloat)
	reg(typeString, driver.KindString)
	reg(typeTime, driver.KindTime)
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（驱动回退为按资源声明推断，见 resolveKind）。
func typeKind(name string) string {
	if dt, ok := abbLookup(name); ok {
		return dt.Kind
	}
	return ""
}

// resolveKind 返回该点位应使用的 Kind。
//
// 判定顺序（见 docs 的「数据类型」一节）：
//
//  1. **用户显式配置的类型永远优先**，包括"用户选错了" —— 显式选择是运维的
//     最后手段，驱动不该越过它去猜。配置的类型未注册（历史数据漂移）时才落回
//     第 2 步，而不是返回空 Kind。
//  2. 配置为 auto（或未配置）时，按**资源自身声明的 RWS 类型**解析
//     （信号的 DO/AI/GI、RAPID 符号的 num/bool/string、运动资源的固定语义）。
//  3. 一律兜底 string。
//
// 为什么 auto **只信声明、绝不按值推断**：RWS 的值全是字符串（`"0"` 既可当
// bool 也可当 int），按值推断必然出错的场景是模拟量恒为 0/1 时被推断成 bool。
// 这类错误只会在时序库里表现为"字段类型变了"，比 panic 更难发现。
//
// 兜底选 string 而非 float：string 是唯一不会在时序库产生错误聚合的 Kind
// （push 通道把 string/time 都写进 value_str，不会与数值列混）。
func resolveKind(a *parsedAddr, configured, declared string) string {
	if configured != "" && !strings.EqualFold(configured, typeAuto) {
		if k := typeKind(configured); k != "" {
			return k
		}
	}
	return autoKind(a, declared)
}

// autoKind 按 namespace 与字段语义解析 Kind。
// declared 是资源在响应里声明的 RWS 类型（信号 DO/DI/AO/AI/GI/GO、RAPID num/bool/...），
// 可能为空（字段缺失或该版本不返回）。
func autoKind(a *parsedAddr, declared string) string {
	if a == nil {
		return driver.KindString
	}
	switch a.ns {
	case nsSignal:
		// 字段决定语义，与信号自身的类型无关：这些都不是"信号值"。
		switch a.field {
		case fieldSignalValue: // lvalue / pvalue —— 跟随信号声明的类型
			return signalKind(declared)
		case "quality", "ltime-sec", "ltime-microsec", "ptime-sec", "ptime-microsec":
			return driver.KindInt
		default: // lstate / phstate / type / category / unitnm / ...
			return driver.KindString
		}

	case nsRapid:
		if a.field == "" {
			// 整取：RAPID 的复合类型（pos/robtarget/...）格式化后是文本，
			// 只有标量类型才谈得上数值语义。
			return rapidScalarKind(declared)
		}
		// 取分量：只有已知全数值的复合类型才敢当 float。
		if numericRapidTypes[strings.ToLower(declared)] {
			return driver.KindFloat
		}
		return driver.KindString

	case nsPanel:
		if a.field == "speedratio" {
			return driver.KindInt
		}
		return driver.KindString

	case nsMotion:
		if a.field == "" || a.field == "extjoint" {
			return driver.KindString // 复合值，整取即文本
		}
		return driver.KindFloat // rax_*/rex_*/x/y/z/q*

	case nsSystem:
		return driver.KindString
	}
	return driver.KindString
}

// signalKind 把 RWS 的信号类型码映射为 Kind。
//
// DO/DI 是数字量（bool）、AO/AI 是模拟量（float）、GI/GO 是组信号（整套位，
// 按整数处理）。声明缺失或无法识别时返回 string —— 宁可让现场在时序库里看到
// 文本列，也不要按猜测的数值列存进错误聚合。
func signalKind(declared string) string {
	switch strings.ToUpper(strings.TrimSpace(declared)) {
	case "DO", "DI":
		return driver.KindBool
	case "AO", "AI":
		return driver.KindFloat
	case "GO", "GI":
		return driver.KindInt
	default:
		return driver.KindString
	}
}

// rapidScalarKind 把 RAPID 符号类型名映射为 Kind。
// 复合类型（pos/robtarget/orient/...）整取时是格式化文本，一律 string。
func rapidScalarKind(declared string) string {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case "num", "dnum":
		return driver.KindFloat
	case "bool":
		return driver.KindBool
	default:
		return driver.KindString
	}
}

// numericRapidTypes 分量可以安全当数值的 RAPID 复合类型。
// 白名单而非黑名单：将来 ABB 新增复合类型时，默认落 string（安全），
// 而不是被当成全数值取到一堆文本。
var numericRapidTypes = map[string]bool{
	"pos":         true,
	"pose":        true,
	"robtarget":   true,
	"jointtarget": true,
	"robjoint":    true,
	"orient":      true,
	"extjoint":    true,
}

// formatValue 把 RWS 返回的原始字符串按 Kind 归一化，供推送通道消费。
//
// 归一化的唯一目的是**不让通道因为格式差异把值降级到错误的列**：
// push 侧对 bool 用 strconv.ParseBool、对数值用 ParseInt/ParseFloat，
// 解析失败会静默落到字符串列 —— 那时点位看起来"有值"，但时序库里的字段类型
// 已经变了，发现得很晚。
//
// 数值**原样透传**（只 trim），不做 float64 往返：那会把 "-0.000000" 变成 "-0"，
// 白白丢掉控制器给出的精度信息。
func formatValue(raw, kind string) string {
	s := strings.TrimSpace(raw)
	switch kind {
	case driver.KindBool:
		return normalizeBool(s)
	case driver.KindInt, driver.KindUInt:
		return normalizeInteger(s)
	case driver.KindFloat:
		return normalizeDecimal(s)
	default:
		return s
	}
}

// normalizeBool 把 RWS 的布尔表示归一到 "1"/"0"。
//
// 通道侧用 strconv.ParseBool，它只认 1/t/T/TRUE/true/True 与 0/f/F/FALSE/false/False；
// ABB 控制器在组信号与 RAPID bool 上还可能出现 on/off/yes/no。
// 无法识别时原样返回 —— 编造一个 "0" 比留下解析失败更糟。
func normalizeBool(s string) string {
	switch strings.ToLower(s) {
	case "1", "true", "t", "on", "yes":
		return "1"
	case "0", "false", "f", "off", "no":
		return "0"
	default:
		return s
	}
}

// normalizeInteger 把整数值归一到十进制字面量。
//
// 模拟量信号在小数点后全是零时也会返回 "12.000000"（RWS 不区分数值的书写形式），
// 直接透传会让通道的 ParseInt 失败并降级到浮点列 —— 对 GI/GO 这类整型点位，
// 那是一次无谓的类型漂移。整值的小数形式在这里收敛回整数。
func normalizeInteger(s string) string {
	if s == "" {
		return ""
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return strconv.FormatInt(n, 10)
	}
	if u, err := strconv.ParseUint(s, 10, 64); err == nil {
		return strconv.FormatUint(u, 10)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return normalizeDecimal(s)
}

// normalizeDecimal 归一化数值字面量的小数点。
//
// 唯一的改动点是**本地化小数点**：ABB 控制器随系统语言可能返回 "1,5" 而不是 "1.5"。
// 规则收紧到"整串恰好形如 -?\d+,\d+"才替换"，所以含逗号的文本值
// （如 "waiting, paused"）绝不会被误伤 —— 这也是它只对数值 Kind
// 生效的原因（formatValue 只在 KindInt/KindUInt/KindFloat 分支调用它）。
func normalizeDecimal(s string) string {
	if strings.IndexByte(s, ',') < 0 {
		return s
	}
	body := s
	if strings.HasPrefix(body, "-") || strings.HasPrefix(body, "+") {
		body = body[1:]
	}
	before, after, found := strings.Cut(body, ",")
	if !found || before == "" || after == "" || !allDigits(before) || !allDigits(after) {
		return s
	}
	return strings.Replace(s, ",", ".", 1)
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
