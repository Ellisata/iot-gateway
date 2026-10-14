// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

// ==================== 点位扫描：DataType 属性 → 通用类型名 ====================
//
// 一个 OPC UA 变量的类型是**服务器自报的**（`DataType` 属性的值是另一个节点的
// NodeID），这正是契约里 DiscoveredPoint.CommonDataType 存在的理由 ——
// 结果级只有一个 AssumedCommonDataType，而 DINT 与 REAL 混在同一张表里，
// 用一个值去表达必然出错（见 driver/pointDiscovery.go 的说明）。
//
// 麻烦在于那个 NodeID **不一定**指向内建类型：自定义枚举与厂商结构体都活在
// 服务器自己的命名空间里。本文件就干两件事：
//
//  1. ns=0 的内建码 → 零请求直接映射（typemap.go 的 commonTypeOfBuiltin）；
//  2. 其余 → 沿**逆 HasSubtype** 往上溯到内建类型，**一层一个 BrowseRequest**。
//
// 第 2 步的批量化是这一层唯一值得优化、也最容易写错的地方：一台服务器上几千个
// 点位往往只有几种自定义类型，而请求数直接决定"一次扫描能扫完多少个点位" ——
// 一个类型一个请求的实现会在预算耗尽时只扫完一小半，症状是"清单莫名其妙断在
// 一半"，而用户完全看不出为什么。

// typeResolveHopLimit 自定义类型沿逆 HasSubtype 上溯的层数上限。
//
// 现实里最长的链是"自定义枚举 → Enumeration → Int32"（两跳）；留到四跳是为了
// 容忍厂商插在中间的抽象类型。再长说明这棵类型树不是我们能解释的，
// 与其继续问下去（每一跳都是一次网络往返），不如把它计入"未列出"。
const typeResolveHopLimit = 4

// resolvedType 一个 DataType 解析成功后的结果。
type resolvedType struct {
	mapping builtinMapping
	// base OPC UA 那一侧的基础类型名（如 "Int32"），供 TypeNote 使用。
	// 取自**上溯的终点**，因此一个自定义枚举的 base 是它派生自的那个内建类型，
	// 而不是它自己的名字（服务器命名空间里的名字我们拿不到）。
	base string
}

// datatypeOf 从 DataValue 里取出 DataType 属性指向的 NodeID。
//
// **两种形态都要认**：这个属性的值可能是 NodeID，也可能是 ExpandedNodeID ——
// 库自带的服务器实现存的就是后者（它的 NewVariableNode 写进去的是一个
// ExpandedNodeID），而客户端 SetAttribute 写进去的又往往是前者。
// 只认一种的表现是"某一类服务器列不出任何点位，而一个错都不报"。
func datatypeOf(dv *ua.DataValue) (*ua.NodeID, bool) {
	if dv == nil || dv.Status != ua.StatusOK || dv.Value == nil {
		return nil, false
	}
	switch v := dv.Value.Value().(type) {
	case *ua.NodeID:
		if v == nil {
			return nil, false
		}
		return v, true
	case *ua.ExpandedNodeID:
		if v == nil || v.NodeID == nil {
			return nil, false
		}
		return v.NodeID, true
	default:
		// 服务器把 DataType 写成了别的东西（畸形应答）。当作"类型未知"处理，
		// 由调用方计入未列出 —— 猜一个类型会让那个点位永远采到按错类型渲染的值。
		return nil, false
	}
}

// builtinMappingOf 判断一个 DataType NodeID 是否**直接**对应一个可映射的类型。
//
// 返回 (映射, 可映射, 已定论)：
//
//	(_,  true,  true)   ns=0 且映射表里有
//	(_,  false, true)   ns=0 但映射表里没有（结构体、抽象基类、Guid……）—— 定论
//	(_,  false, false)  不在 ns=0：自定义类型，需要沿逆 HasSubtype 继续上溯
//
// 按规范，自定义类型**必须**活在非零命名空间里，所以"ns=0 但表里没有"是可以
// 直接下结论的（不可映射），不必再去问服务器 —— 一次请求省下来的不多，
// 但省掉的是"每遇到一个抽象基类就问一次"这种成片的浪费。
func builtinMappingOf(nid *ua.NodeID) (builtinMapping, bool, bool) {
	if nid == nil {
		return builtinMapping{}, false, false
	}
	if nid.Namespace() != 0 {
		return builtinMapping{}, false, false
	}
	m, ok := commonTypeOfBuiltin(nid.IntID())
	return m, ok, true
}

// typeResolver 一次扫描内复用的数据类型解析器。
//
// 缓存是必需的而不是优化：同一台服务器上的几千个点位往往共用几种自定义类型，
// 不去重的话"每个点位都要上溯一次"。**只缓存成功的**，解析不出来的另记一张
// fail 表，免得每一块都把同一批问不出结果的类型再问一遍。
type typeResolver struct {
	cache map[string]resolvedType
	fail  map[string]bool
}

func newTypeResolver() *typeResolver {
	return &typeResolver{
		cache: make(map[string]resolvedType),
		fail:  make(map[string]bool),
	}
}

// typeResolveOutcome 一次批量解析的结果。
type typeResolveOutcome struct {
	// types key（DataType NodeID 的字符串）→ 结果，**只含解析成功的项**；
	// 解析不出来的不进 map，由调用方计入"未列出"。
	types map[string]resolvedType
	// ranOut 请求预算在解析途中用尽。调用方据此把本次扫描标成"未扫完" ——
	// 类型解析失败与**没来得及解析**是两件事，混在一起会让用户以为
	// "这些点位没有类型"，而其实是扫描没跑完。
	ranOut bool
}

// resolveTypes 把一批 DataType NodeID 解析成通用类型名。
//
// error 只用于链路级的失败（Browse 的传输错误），调用方据此中止扫描。
func (r *typeResolver) resolveTypes(ctx context.Context, c *OpcUaClient,
	want []*ua.NodeID, budget *requestBudget) (typeResolveOutcome, error) {
	out := make(map[string]resolvedType, len(want))

	// 第一遍：缓存与快路径，**零请求**。先走一遍再决定要问什么，
	// 是为了让"这块里所有类型都认识"这个常见情形一次网络都不发。
	level := make([]string, 0, len(want))
	nodes := make(map[string]*ua.NodeID, len(want))
	for _, dt := range want {
		if dt == nil {
			continue
		}
		key := dt.String()
		if _, done := out[key]; done {
			continue
		}
		if rt, ok := r.cache[key]; ok {
			out[key] = rt
			continue
		}
		if r.fail[key] {
			continue
		}
		if m, ok, known := builtinMappingOf(dt); known {
			if ok {
				rt := resolvedType{mapping: m, base: BuiltinTypeName(dt.IntID())}
				out[key] = rt
				r.cache[key] = rt
			} else {
				r.fail[key] = true
			}
			continue
		}
		if _, seen := nodes[key]; seen {
			continue
		}
		nodes[key] = dt
		level = append(level, key)
	}
	if len(level) == 0 {
		return typeResolveOutcome{types: out}, nil
	}
	// level 里剩下的就是"服务器自报的自定义类型"，要沿逆 HasSubtype 问上去。
	origins := level

	// 逐层上溯。**一层一个 BrowseRequest**（见文件头）。
	var (
		parentOf = make(map[string]string, len(level))
		resolved = make(map[string]resolvedType, len(level))
		ranOut   bool
	)

	for hop := 0; hop < typeResolveHopLimit && len(level) > 0; hop++ {
		if !budget.spend() {
			ranOut = true
			break
		}
		descs := make([]*ua.BrowseDescription, len(level))
		for i, key := range level {
			descs[i] = &ua.BrowseDescription{
				NodeID:          nodes[key],
				BrowseDirection: ua.BrowseDirectionInverse,
				ReferenceTypeID: ua.NewNumericNodeID(0, id.HasSubtype),
				// IncludeSubtypes 必须是 false：要的是**直接**父类型。
				// 打开它会把 Enumeration/Int32/Integer 一层层全列回来，
				// 于是我们拿不到"最接近的那个父类型"，链的走向就不确定了。
				IncludeSubtypes: false,
				NodeClassMask:   uint32(ua.NodeClassDataType),
				ResultMask:      uint32(ua.BrowseResultMaskAll),
			}
		}
		results, err := c.BrowseBatch(ctx, descs)
		if err != nil {
			return typeResolveOutcome{types: out}, err
		}

		next := make([]string, 0, len(level))
		queued := make(map[string]bool, len(level))
		for i, key := range level {
			parent := firstReference(results, i)
			if parent == nil {
				continue // 上溯到头（或这个类型节点读不了）：不可映射
			}
			parentKey := parent.String()
			if m, ok, known := builtinMappingOf(parent); known {
				if ok {
					resolved[key] = resolvedType{mapping: m, base: BuiltinTypeName(parent.IntID())}
				}
				continue
			}
			parentOf[key] = parentKey
			if _, seen := nodes[parentKey]; !seen {
				nodes[parentKey] = parent
			}
			if !queued[parentKey] {
				queued[parentKey] = true
				next = append(next, parentKey)
			}
		}
		level = next
	}
	// level 还剩东西 = 触到 hopLimit 或预算用尽，那些类型按不可映射处理。

	// 把上溯结果沿链传下来：一个自定义枚举的终点是 Int32，中间还隔着
	// Enumeration，所以不能只认"直接父节点已解析"。
	for _, key := range origins {
		if _, ok := out[key]; ok {
			continue // 第一遍的快路径已经解出来了
		}
		rt, ok := walkUp(key, parentOf, resolved)
		if !ok {
			// 触到 hopLimit、或这一支根本问不到内建类型。记进 fail 表：
			// 这台服务器上同类点位有很多，每一块都重问一次只是白花预算。
			r.fail[key] = true
			continue
		}
		out[key] = rt
		r.cache[key] = rt
	}

	// 预算是全局的：中途用尽时把**已经解出来的**部分一并交回 —— 丢掉它们只会让
	// 用户少看到一批本来已经问出来的点位。ranOut 让调用方把这次扫描标成
	// "未扫完"，而不是让用户以为剩下那些点位没有类型。
	return typeResolveOutcome{types: out, ranOut: ranOut}, nil
}

// walkUp 沿 parentOf 链向上找一个已经确定下来的结果。
func walkUp(key string, parentOf map[string]string, resolved map[string]resolvedType) (resolvedType, bool) {
	for cur, hop := key, 0; cur != "" && hop <= typeResolveHopLimit; hop++ {
		if rt, ok := resolved[cur]; ok {
			return rt, true
		}
		cur = parentOf[cur]
	}
	return resolvedType{}, false
}

// firstReference 取一个 BrowseResult 里的第一条引用，转成 NodeID。
//
// 逆 HasSubtype 正常情况下只有一条引用，所以"取第一条"不是抽样而是取全部。
// 状态码不是 OK（BadNodeIdUnknown、BadNoMatch……）时返回 nil —— 那是服务器
// 对"这个类型节点我读不了"的正常答复，不是要上抛的错误。
func firstReference(results []*ua.BrowseResult, i int) *ua.NodeID {
	if i >= len(results) || results[i] == nil || results[i].StatusCode != ua.StatusOK {
		return nil
	}
	for _, ref := range results[i].References {
		if ref == nil {
			continue
		}
		if nid := ua.NewNodeIDFromExpandedNodeID(ref.NodeID); nid != nil {
			return nid
		}
	}
	return nil
}
