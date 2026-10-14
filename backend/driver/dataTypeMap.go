// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"iot-gateway/model/po"
)

// ==================== 通用数据类型 ====================
//
// 各协议在配置界面 / API 中统一使用以下通用数据类型名称，
// 通过 DataTypeMap 映射到协议内部注册的数据类型名（裸名，可经
// TypeRegistry.ForProtocol(protocol).Get(name) 解析）。

const (
	TypeBoolean = "Boolean"
	TypeDate    = "Date"
	TypeString  = "String"
	TypeByte    = "Byte"
	TypeChar    = "Char"
	TypeShort   = "Short"
	TypeWord    = "Word"
	TypeDWord   = "DWord"
	TypeLong    = "Long"
	TypeFloat   = "Float"
	TypeDouble  = "Double"
	TypeBCD     = "BCD"
	TypeLBCD    = "LBCD"
)

// CommonDataTypes 通用数据类型列表（顺序即配置界面展示顺序）。
var CommonDataTypes = []string{
	TypeBoolean, TypeDate, TypeString, TypeByte, TypeChar,
	TypeShort, TypeWord, TypeDWord, TypeLong, TypeFloat,
	TypeDouble, TypeBCD, TypeLBCD,
}

// ==================== 协议作用域（scope）组织 ====================
//
// 全部协议驱动都已搬成进程外插件（见 driversrc/ 与 docs/driver-plugins.md），
// 主二进制里**不再有任何协议实现**：类型映射、协议名、驱动工厂一律由插件在
// 加载时通过 driversdk.Meta 上报，落在下面的"运行时扩展层"里。

// scopeTypeMap 内置 scope 基线：TypeRegistry scope 前缀 → 通用/扩展类型映射。
//
// **已清空**，且应当保持清空 —— 它是内置驱动的遗迹，不是留给新协议的位置。
// 留着这两个空表（而不是删掉整段机制）是为了保住两条护栏：
//
//  1. RegisterScope 会拒绝与基线撞名的 scope（含大小写变体）。若将来有人
//     再把某个协议编回主二进制，会在这里被挡住，而不是让插件与内置实现
//     悄悄共用同一个类型命名空间互相覆盖；
//  2. scopeTypes / lookupScopeProtocol 的"先查基线、命中即终结"读取规则不变，
//     将来真有基线条目时，插件的行为影响不到它。
//
// 新协议一律做成插件（docs/driver-plugins.md §2.5），不要往这里加条目。
var scopeTypeMap = map[string]map[string]string{}

// scopeProtocols scope 前缀 → 该 scope 下的 config 协议名（iot_protocol.name）。
// 一个 scope 可能对应多个 config 协议名（如 Modbus 的 TCP 与 RTU 共用同一套类型注册）。
// 与 scopeTypeMap 一样已清空且应保持清空，理由见上。
var scopeProtocols = map[string][]string{}

// protocolScope config 协议名 → scope 前缀（由 scopeProtocols 派生）。
var protocolScope = func() map[string]string {
	m := make(map[string]string, len(scopeProtocols))
	for scope, protocols := range scopeProtocols {
		for _, p := range protocols {
			m[p] = scope
		}
	}
	return m
}()

// scopeLookup 小写化 config 协议名 → scope 前缀（ScopeOf 查询用）。
var scopeLookup = func() map[string]string {
	m := make(map[string]string, len(protocolScope))
	for protocol, scope := range protocolScope {
		m[strings.ToLower(protocol)] = scope
	}
	return m
}()

// ==================== 运行时 scope 扩展层 ====================
//
// 上面几张表是内置驱动的**基线**（现已清空）。下面这层承载全部协议插件在
// 运行期注册的 scope —— 内置实现搬空之后，这层已经是唯一路径，不再是补丁。
//
// 读取规则仍是「先查基线，命中即终结」（见 scopeTypes / lookupScopeProtocol）：
// 基线为空时这条规则等价于直查扩展层，但保留它可以让将来真出现基线条目时，
// 插件的行为影响不到它。
//
// 并发约定：扩展层的 map 一旦发布就**永不原地修改**（注册时构造新 map 整体替换，
// 注销时整个条目摘除），所以 scopeTypes 可以在释放锁后把 map 引用交给调用方读。
// 违反这条约定会引入数据竞争。
//
// 一个直接后果：插件未加载时 ScopeOf 对任何协议都返回空串，于是
// AvailableTypes 返回空、NormalizeAddressType 原样透传。这在启动早期是正常的
// （pluginhost 的首次扫描在采集引擎之前完成，见 main.go 的启动顺序），
// 但意味着**没有插件就等于没有协议** —— 这也是 plugin.enabled 出厂即为 true、
// 且 Manager 在零协议时会告警的原因。
var (
	extMu             sync.RWMutex
	extScopes         = map[string]map[string]string{} // scope → {类型名 → 内部裸名}
	extScopeProtocols = map[string][]string{}          // scope → []协议名
	extProtocolScope  = map[string]string{}            // lower(协议名) → scope
)

// RegisterScope 运行期注册一个插件 scope 及其协议名与类型映射。
//
// protocolTypes: 通用/扩展类型名 → 内部裸名（裸名须已在 TypeRegistry 注册，
// 否则 AvailableTypes 会静默隐藏该条目而不报错）。
//
// 原子性：先完成全部校验再写入各表，任一校验失败返回 error 且不留任何部分生效的状态。
//
// 约束：scope 不得与内置基线 scope 相同（基线已清空，正常情况下永不触发），
// 也不得与**另一个已注册的插件 scope** 相同。后者才是现实中会遇到的路径：
// 两个插件共用同一个 scope 时，后到的会把先到的类型条目整批覆盖掉
// （TypeRegistry.Register 是覆盖写），而**双方作者都看不出是自己的问题**。
//
// **冲突判定一律不区分大小写**：scope 前缀最终是经 ProtocolScope（ForProtocol）
// 小写化后拼成 "{scope}.{type}" 的（见 typeRegistry.go），所以 "ModBus" 与
// "modbus" 落在同一个类型命名空间里 —— 按原样比对会把它放过去，而
// TypeRegistry.Register 是覆盖写，插件那批**没有 Decode/Encode/Format** 的类型
// 会把先到的顶掉。驱动包用 sync.Once 按前缀扫注册表建缓存，于是解析时调到
// nil 的 Decode：worker pool 吞掉 panic，症状是「某台设备的点位永远采不到值」，
// 而两个插件的加载日志上明明都写着成功 —— 这是本文件里最该防住的一类故障。
func RegisterScope(scope string, protocols []string, protocolTypes map[string]string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return errors.New("driver: scope must not be empty")
	}
	// scopeTypeMap 的键全是小写，因此小写化后查表即可实现不区分大小写的比对。
	if _, ok := scopeTypeMap[strings.ToLower(scope)]; ok {
		return fmt.Errorf("driver: scope %q collides with a built-in scope", scope)
	}
	if len(protocols) == 0 {
		return errors.New("driver: at least one protocol name is required")
	}
	if len(protocolTypes) == 0 {
		return errors.New("driver: type map must not be empty")
	}

	// 类型名大小写不敏感去重：LookupScopeType 按 EqualFold 匹配，
	// 同名不同大小写的两个 key 会因 map 遍历顺序不同而析出不同结果。
	seenType := make(map[string]string, len(protocolTypes))
	for name, internal := range protocolTypes {
		if strings.TrimSpace(name) == "" {
			return errors.New("driver: type name must not be empty")
		}
		if strings.TrimSpace(internal) == "" {
			return fmt.Errorf("driver: type %q maps to an empty internal name", name)
		}
		lower := strings.ToLower(name)
		if prev, ok := seenType[lower]; ok {
			return fmt.Errorf("driver: type names %q and %q differ only by case", prev, name)
		}
		seenType[lower] = name
	}

	extMu.Lock()
	defer extMu.Unlock()

	// 已注册的插件 scope 同样按小写比对（理由见函数头）：否则 "Acme" 与 "acme"
	// 会同时注册成功，共用 "acme." 类型命名空间互相覆盖。
	for existing := range extScopes {
		if strings.EqualFold(existing, scope) {
			return fmt.Errorf("driver: scope %q is already registered (as %q)", scope, existing)
		}
	}

	seenProto := make(map[string]string, len(protocols))
	for _, proto := range protocols {
		proto = strings.TrimSpace(proto)
		if proto == "" {
			return errors.New("driver: protocol name must not be empty")
		}
		lower := strings.ToLower(proto)
		if _, ok := scopeLookup[lower]; ok {
			return fmt.Errorf("driver: protocol %q collides with a built-in protocol", proto)
		}
		if prev, ok := extProtocolScope[lower]; ok {
			return fmt.Errorf("driver: protocol %q is already registered by scope %q", proto, prev)
		}
		if prev, ok := seenProto[lower]; ok {
			return fmt.Errorf("driver: protocol names %q and %q differ only by case", prev, proto)
		}
		seenProto[lower] = proto
	}

	// 校验全部通过，构造新 map 整体发布（不原地修改已发布的 map）。
	types := make(map[string]string, len(protocolTypes))
	for name, internal := range protocolTypes {
		types[name] = internal
	}
	names := make([]string, 0, len(seenProto))
	for _, proto := range seenProto {
		names = append(names, proto)
	}
	sort.Strings(names)

	extScopes[scope] = types
	extScopeProtocols[scope] = names
	for _, proto := range names {
		extProtocolScope[strings.ToLower(proto)] = scope
	}
	return nil
}

// UnregisterScope 注销运行期 scope（内置 scope 不可注销），返回其是否曾注册。
//
// 只摘除 dataTypeMap 的三张表，**不清 TypeRegistry 中的类型**：可能仍有设备地址
// 引用这些内部类型名，保留孤儿类型条目无害（仅占内存），删除会破坏
// NormalizeAddressType 对既有地址的判定。
func UnregisterScope(scope string) bool {
	extMu.Lock()
	defer extMu.Unlock()

	protocols, ok := extScopeProtocols[scope]
	if !ok {
		return false
	}
	delete(extScopes, scope)
	delete(extScopeProtocols, scope)
	for _, proto := range protocols {
		// 仅在该协议仍指向本 scope 时摘除，防止误删他人条目。
		if extProtocolScope[strings.ToLower(proto)] == scope {
			delete(extProtocolScope, strings.ToLower(proto))
		}
	}
	return true
}

// scopeTypes 返回 scope 的类型映射，第二个返回值表示该 scope 是否已知。
// 内置基线优先；扩展层返回的是已发布 map 的引用，调用方可安全只读遍历。
func scopeTypes(scope string) (map[string]string, bool) {
	if types, ok := scopeTypeMap[scope]; ok {
		return types, true
	}
	extMu.RLock()
	defer extMu.RUnlock()
	types, ok := extScopes[scope]
	return types, ok
}

// lookupScopeProtocol 返回协议名（小写）对应的 scope 前缀，基线优先。
func lookupScopeProtocol(lowerProtocol string) string {
	if scope, ok := scopeLookup[lowerProtocol]; ok {
		return scope
	}
	extMu.RLock()
	defer extMu.RUnlock()
	return extProtocolScope[lowerProtocol]
}

// ==================== 查询接口 ====================

// ScopeOf 返回 config 协议名对应的 TypeRegistry scope 前缀（如 "ModBus.TCP" → "modbus"）。
// 协议名不区分大小写；未知协议（无驱动注册）返回空串。内置协议优先于插件协议。
func ScopeOf(protocol string) string {
	return lookupScopeProtocol(strings.ToLower(protocol))
}

// LookupScopeType 按 scope 前缀查询通用/扩展类型 → 内部类型名（裸名）。
// scope 与类型名均不区分大小写；scope 未知或该 scope 不支持该类型时返回 false。
func LookupScopeType(scope, commonType string) (string, bool) {
	types, ok := scopeTypes(scope)
	if !ok {
		return "", false
	}
	// 映射表 key 为规范写法（如 "Float"），小写化匹配保证不区分大小写
	for name, internal := range types {
		if strings.EqualFold(name, commonType) {
			return internal, true
		}
	}
	return "", false
}

// LookupDataType 根据协议名和通用数据类型名查询该协议对应的内部类型名。
// 协议名与类型名均不区分大小写（如 "ModBus.TCP@Boolean" 与 "modbus.tcp@boolean" 等价）。
// 协议不支持该通用类型时返回 false。
func LookupDataType(protocol, commonType string) (string, bool) {
	scope := ScopeOf(protocol)
	if scope == "" {
		return "", false
	}
	return LookupScopeType(scope, commonType)
}

// AvailableTypes 返回协议 scope 支持的全部可配置类型名，供配置界面类型下拉使用。
// 返回值：
//
//	common   通用类型（CommonDataTypes 子集，按展示顺序）
//	extended 协议专属扩展类型（不在通用列表中，按字母序，如 Modbus 的 Int/Real）
//
// 候选类型映射到的内部类型必须已在 TypeRegistry 注册才返回，
// 避免"映射表有、注册表无"的新漂移（此时配置界面不应展示该类型）。
func AvailableTypes(scope string) (common, extended []string) {
	types, ok := scopeTypes(scope)
	if !ok {
		return nil, nil
	}
	reg := GetTypeRegistry().ForProtocol(scope)

	for _, name := range CommonDataTypes {
		internal, ok := types[name]
		if !ok {
			continue
		}
		if _, ok := reg.Get(internal); !ok {
			continue
		}
		common = append(common, name)
	}

	for name, internal := range types {
		if slices.Contains(common, name) {
			continue
		}
		if _, ok := reg.Get(internal); !ok {
			continue
		}
		extended = append(extended, name)
	}
	sort.Strings(extended)
	return common, extended
}

// NormalizeAddressType 读取路径的类型回退：返回地址应使用的协议内部类型名。
//
// 规则（保守，只修坏的）：
//  1. DataType 内部名仍能在协议 scope 注册表解析 → 原样返回（不干扰有效但不同的类型）；
//  2. 否则用 CommonDataType 经 LookupDataType 重新解析；
//  3. 两者都失败 → 原样返回 DataType（驱动照常报"unsupported data type"）。
//
// 用于采集引擎加载点位时修正漂移的 data_type 派生缓存（协议注册名变更 / 旧数据 / 外部改库）。
func NormalizeAddressType(protocol string, addr po.DeviceAddress) string {
	scope := ScopeOf(protocol)
	if scope == "" {
		return addr.DataType
	}
	if _, ok := GetTypeRegistry().ForProtocol(scope).Get(addr.DataType); ok {
		return addr.DataType
	}
	if internal, ok := LookupDataType(protocol, addr.CommonDataType); ok {
		return internal
	}
	return addr.DataType
}
