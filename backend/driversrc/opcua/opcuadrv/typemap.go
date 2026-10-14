// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"strings"

	"github.com/gopcua/opcua/id"

	"iot-gateway/driver"
)

// commonTypeMap OPC UA 通用数据类型 → 内部类型名。
//
// **本文件是 OPC UA 类型映射的唯一真源**：内置时代它叫 opcuaTypes、放在
// driver/dataTypeMap.go 里由 driver.LookupDataType 查询；搬迁成插件后
// 随实现一并搬到这里，由 driversdk.Meta.TypeMap 上报网关
// （见 docs/driver-plugins.md §2.5）。driver/dataTypeMap.go 里已不再保留
// OPC UA 的条目。
//
// OPC UA 为节点寻址，无位/寄存器概念，仅声明值的类别；解码由服务器返回的
// 原生值决定（驱动按实际值类型推断 Kind，配置类型仅作默认展示）。
// OPC UA 无独立 BCD 类型，故不映射；Long 对应 Int64（64 位有符号）。
//
// 内容与内置时代逐项一致：改一个 value 就等于改现场点位取到的 Kind
// （服务器返回原生值，不走字节级解码），会让推送通道按错误的类型存值。
var commonTypeMap = map[string]string{
	driver.TypeBoolean: "bool",
	driver.TypeDate:    "datetime",
	driver.TypeString:  "string",
	driver.TypeByte:    "byte",
	driver.TypeChar:    "char",
	driver.TypeShort:   "int16",
	driver.TypeWord:    "uint16",
	driver.TypeDWord:   "uint32",
	driver.TypeLong:    "int64",
	driver.TypeFloat:   "float32",
	driver.TypeDouble:  "float64",
}

// CommonTypeMap 返回通用类型名 → 内部裸名，供 driversdk.Meta.TypeMap 使用。
// 返回副本，调用方修改不会影响包内真源。
func CommonTypeMap() map[string]string {
	out := make(map[string]string, len(commonTypeMap))
	for name, internal := range commonTypeMap {
		out[name] = internal
	}
	return out
}

// LookupCommonType 按通用类型名查内部裸名，语义照搬内置时代的
// driver.LookupScopeType：大小写不敏感，未命中返回 false。
//
// 先精确命中（零分配，主路径），未命中再回退 EqualFold —— 历史配置里可能
// 存着 "Float" 之外的写法（如 "float"）。
func LookupCommonType(common string) (string, bool) {
	if internal, ok := commonTypeMap[common]; ok {
		return internal, true
	}
	for name, internal := range commonTypeMap {
		if strings.EqualFold(name, common) {
			return internal, true
		}
	}
	return "", false
}

// ==================== 点位扫描：OPC UA 内建类型 → 通用类型名 ====================
//
// 这一段与上面的 commonTypeMap 是**两件相反的事**，别混：
//
//	commonTypeMap        通用类型名 → 本协议的内部裸名。是**配置界面**用的那张表，
//	                     它的键集被黄金基线钉死（pluginhost/testdata/parity/
//	                     typemap/opcua.json），多一个键就要重新生成黄金 ——
//	                     而那份黄金只能由已经移除的内置实现生成。
//	本段                 服务器自报的数据类型码 → 通用类型名。是**点位扫描**用的
//	                     单向映射，不新增任何键，因此一个字节的黄金都不动。
//
// 为什么只能近似：OPC UA 的 int32 / uint64 / byteString 在系统里没有**同名**的
// 通用类型（见 metadata.go 的说明：它们只有内部裸名，没有通用名入口）。
// 于是按 **Kind 等价**取最近的一个。这在行为上是正确的，而不是将就：
// 本协议的采集路径**从不做字节级解码**（服务器返回原生值，DataType 只决定
// Kind，见 types.go 的 kindFor），而 int32 与 int64 的 Kind 都是 KindInt、
// uint64 与 uint32 都是 KindUInt、byteString 与 string 都是 KindString ——
// 名称不同，落库的类别与格式化结果完全一致。
//
// 名字上的差异由点位扫描给该行加一条 Note（见 pointscan.go 的 typeNote）。

// builtinMapping 一个内建数据类型的映射结果。
type builtinMapping struct {
	// common 通用类型名（commonTypeMap 的键），直接写进 DiscoveredPoint.CommonDataType。
	common string
	// exact 为 false 表示本协议没有**同名**的通用类型，这里取的是 Kind 等价的
	// 最近一个 —— 服务器报的类型与界面上的名字对不上，值得给用户一条 Note。
	exact bool
}

// opcuaBuiltinTypeMap ns=0 的内建数据类型码 → 通用类型名。
//
// 只列**可映射**的 14 项；其余（Guid/NodeID/LocalizedText/QualifiedName/StatusCode/
// XmlElement/ExtensionObject/DataValue/Variant/DiagnosticInfo 与抽象基类）一律不列，
// 由 commonTypeOfBuiltin 返回 ok=false 让点位扫描跳过它们。
//
// 抽象基类（Structure/BaseDataType/Number）刻意**不映射**：落到它们身上说明
// 我们没能问出真实类型，而 Structure 的字段布局在本仓库里无从得知 —— 列出来
// 只会建出一批永远没有值的点位，用户分不清那是设备坏了还是这个点位本来没数据。
// Enumeration/Integer/UInteger 是例外，见 subtypeTerminal。
var opcuaBuiltinTypeMap = map[uint32]builtinMapping{
	id.Boolean:    {driver.TypeBoolean, true},
	id.SByte:      {driver.TypeChar, false},   // 8 位有符号、KindInt，与 int8 同形
	id.Byte:       {driver.TypeByte, true},    // KindUInt
	id.Int16:      {driver.TypeShort, true},   //
	id.UInt16:     {driver.TypeWord, true},    //
	id.Int32:      {driver.TypeLong, false},   // 无 "Int32" 通用名；KindInt 与 int64 一致
	id.UInt32:     {driver.TypeDWord, true},   //
	id.Int64:      {driver.TypeLong, true},    //
	id.UInt64:     {driver.TypeDWord, false},  // 无 "UInt64" 通用名；KindUInt 一致
	id.Float:      {driver.TypeFloat, true},   //
	id.Double:     {driver.TypeDouble, true},  //
	id.String:     {driver.TypeString, true},  //
	id.DateTime:   {driver.TypeDate, true},    //
	id.ByteString: {driver.TypeString, false}, // KindString 一致（formatVariant 渲染成 hex）
}

// subtypeTerminal 子类型上溯的**终止节点** → 通用类型名。
//
// 自定义类型（枚举、厂商结构）的属性里存的是它自己的 NodeID，必须沿逆 HasSubtype
// 往上走到 ns=0 的内建类型才能映射。现实里最常见的两类会先撞到这里：
//
//	Enumeration  自定义枚举几乎都直接派生自它，而它是 Int32 的子类型（→ Long）
//	Integer      派生自 Int32/Int64 —— 已经问不出宽度了，取有符号的 Long
//	UInteger     派生自 UInt32/UInt64 —— 取无符号的 DWord
//
// 这三项都**不是同名映射**（exact=false），落到这里说明上溯没能问出确切类型。
var subtypeTerminal = map[uint32]builtinMapping{
	id.Enumeration: {driver.TypeLong, false},
	id.Integer:     {driver.TypeLong, false},
	id.UInteger:    {driver.TypeDWord, false},
}

// commonTypeOfBuiltin 把 ns=0 的内建数据类型码映射成通用类型名。
//
// ok=false 表示这个类型**没有**可用的通用类型名（Guid、结构体、抽象基类……），
// 调用方应当跳过该点位并计入 Unreadable —— 绝不能塞一个空串或内部裸名进去：
// 服务层的 driver.LookupDataType 只认通用名，收到裸名会直接拒掉整行
// （service/devicePointScanService.go 的 "协议不支持该数据类型"）。
func commonTypeOfBuiltin(code uint32) (builtinMapping, bool) {
	if m, ok := opcuaBuiltinTypeMap[code]; ok {
		return m, true
	}
	if m, ok := subtypeTerminal[code]; ok {
		return m, true
	}
	return builtinMapping{}, false
}

// TypeNote 近似映射的说明，供 DiscoveredPoint.Note 使用。
//
// 只有 exact=false（本协议没有同名的通用类型，写进结果的是一个 Kind 等价的
// 近邻）时才有内容，形如 "OPC UA 基础类型 Int32"。**必须给出 OPC UA 那一侧
// 的名字**：用户在结果表里看到类型列写着 "Long"、而设备手册上写的是 Int32 时，
// 唯一能对上号的线索就是这一格 —— 前端在没有采样值时正是把 Note 渲染在
// 「采样值」那一列（见 PointDiscoveryDialog 的 sampled 分支）。
//
// 措辞是"**基础**类型"而不是"类型"：这个值来自上溯的终点，一个自定义枚举的
// 基础类型是 Int32，而它自己的类型名（服务器命名空间里的那个）我们拿不到。
// 说成"类型 Int32"会让用户以为节点自己报的就是 Int32。
func TypeNote(baseType string, m builtinMapping) string {
	if m.exact || baseType == "" {
		return ""
	}
	return "OPC UA 基础类型 " + baseType
}

// BuiltinTypeName 内建数据类型码的展示名（如 "Int32"）；**本包不认识的码返回 ""**。
//
// 走库自带的 id.Name（id_names_gen.go），不另立一张名字表：那张表与 OPC UA
// 规范同步，手抄一份必然漂移，而漂移的症状是用户看到 "Int 32" 这种不存在的类型名。
//
// 先过一道 commonTypeOfBuiltin 再问名字，是因为 id.Name 对不认识的码会**回退成
// 十进制字符串**（"999999"）—— 那个值一旦流进点位结果，用户会在类型列旁边看到
// 一个像节点号的东西。这里的契约是"能映射才有名字"，与调用方的用法一致
// （它只在映射成功后才取名字）。
func BuiltinTypeName(code uint32) string {
	if _, ok := commonTypeOfBuiltin(code); !ok {
		return ""
	}
	return id.Name(code)
}
