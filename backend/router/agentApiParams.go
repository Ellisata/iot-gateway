// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package router

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// 本文件给 agentApiSurface.go 的分类表补上**参数表**。
//
// 为什么需要：分类表回答"能不能直接调"，参数表回答"怎么调"。只有前者时，dsh
// 背后的模型只能按字段名和常识编请求 —— 于是 page/size 被塞进 JSON body
//（它们其实在 query）、rwPermission 被写成 "read"（只接受 R/W/RW）、
// 导入插件的 multipart 字段名被猜成 "file"（实际是 "files"）。这些都不会报错，
// 只会得到 400；更糟的是 updateAlarmWebhook —— 把 secret 传成 "" 会**清空密钥**，
// 而"猜一个空值表示不改"是最自然的误判。
//
// 与 Access 等级的**关键差异**：等级是**判断**，只能由人逐个判定（理由见
// agentApiSurface.go 文件头那四条反例）；参数是**事实**，已经编码在控制器绑定的
// DTO 上。所以这里不手写字段名与约束，而是用反射把**同一份 struct** 的 tag 读出来
// —— "表与代码漂移"这个失败模式在物理上不存在，除非有人改了控制器绑定的 DTO 类型，
// 而那处改动就在注册同一条路由的隔壁几行。
//
// 手写的只剩三类，理由都是"机器读不出来"：
//   - desc：给"光看字段名看不出来"的字段写一句人话（取舍标准与 SurfaceEntry.Note 相同）；
//   - multipart 字段名：它不在任何 DTO 上（控制器用 FormFile/PostForm 按字面量取）；
//   - 裸 c.Query 取的两个参数：同理，它们连结构体都没有。

// ParamIn 参数的位置。取值同时是对 dsh 的指令：path 替换 URL 里的 :name，
// query 拼在 URL 上，body 放进 JSON 请求体，form 走 multipart/form-data。
type ParamIn string

const (
	// ParamInPath 路径参数，出现在路由串里（如 /device/getDeviceById/:id 的 id）。
	ParamInPath ParamIn = "path"

	// ParamInQuery URL 查询串参数（gin 的 form tag 绑定的就是这一类）。
	ParamInQuery ParamIn = "query"

	// ParamInBody JSON 请求体字段。
	ParamInBody ParamIn = "body"

	// ParamInForm multipart/form-data 的部件。与 body **互斥** ——
	// 带文件的接口没有 JSON 请求体，其余字段与文件同在一个表单里。
	ParamInForm ParamIn = "form"
)

// ParamSpec 一个接口参数。
//
// 字段名照 JSON Schema 起 —— dsh 多半要把它转成工具定义，另起一套词汇只会多一层
// 翻译，而翻译处就是出错处。
//
// 约束刻意按类型分成三组关键字（minimum/maximum 是**取值**、minLength/maxLength 是
// **长度**、minItems/maxItems 是**元素数**）：gin 的 binding tag 把三者都写成 min/max，
// 合成本表的一个字段就无法再翻回 JSON Schema —— 而"能直接翻"正是本表存在的意义。
type ParamSpec struct {
	Name     string   `json:"name"`
	In       ParamIn  `json:"in"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
	Format   string   `json:"format,omitempty"`

	Minimum   *int `json:"minimum,omitempty"`   // 数字取值下界
	Maximum   *int `json:"maximum,omitempty"`   // 数字取值上界
	MinLength *int `json:"minLength,omitempty"` // 字符串长度下界
	MaxLength *int `json:"maxLength,omitempty"` // 字符串长度上界
	MinItems  *int `json:"minItems,omitempty"`  // 数组元素数下界
	MaxItems  *int `json:"maxItems,omitempty"`  // 数组元素数上界

	// Desc 人类可读的补充说明，写给 dsh 背后的模型看（不参与机器解析）。
	// 只写"光看字段名看不出来"的事：值的写法、服务端会忽略它、传空等于清空这类。
	Desc string `json:"desc,omitempty"`

	// Fields 嵌套对象的子字段（body 里的 items[] 等）。展开而不是笼统报成
	// object，因为最该被写清的那几条恰恰藏在里面 —— 如 createDevices 的
	// items[].index 指的是**服务端快照里 devices 数组的下标**，靠猜不可能对。
	Fields []ParamSpec `json:"fields,omitempty"`
}

// rawMessageType 是 json.RawMessage 的类型。
//
// 必须特判：它的底层类型是 []byte，照 Kind 走会被报成 "[]int" —— 而它真正的含义
// 是"一段结构由协议决定的 JSON，服务端原样透传不做解析"。
var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// argNone 显式声明"这个接口没有参数"。
//
// 存在的理由：表里 nil 表示**忘了声明**，空切片表示"确实没有"。两者在 Go 里能分开，
// 于是测试可以逼出一次有意识的选择 —— 否则新增无参数接口时，"漏写"与"没得可写"
// 长得一模一样，而这两种情况对 dsh 的差别是"去猜"与"不用传"。
func argNone() []ParamSpec { return []ParamSpec{} }

// args 收集手写参数，用于不经过任何 DTO 的接口（裸 c.Query、multipart 字段名）。
func args(ps ...ParamSpec) []ParamSpec {
	out := make([]ParamSpec, 0, len(ps))
	return append(out, ps...)
}

// argBody 从控制器绑定的请求体 DTO 导出 body 参数。descs 键是字段名，
// 嵌套字段用点号路径（"items.index"）；没有要补充的字段就传 nil。
func argBody(sample any, descs map[string]string) []ParamSpec {
	return structParams(sample, ParamInBody, "json", descs)
}

// argQuery 从控制器绑定的查询 DTO 导出 query 参数（读的是 form tag）。
func argQuery(sample any, descs map[string]string) []ParamSpec {
	return structParams(sample, ParamInQuery, "form", descs)
}

// argQueryStr 一个手写的 query 标量参数（控制器直接 c.Query 取的那些，没有 DTO）。
func argQueryStr(name string, required bool, desc string) ParamSpec {
	return ParamSpec{Name: name, In: ParamInQuery, Type: "string", Required: required, Desc: desc}
}

// argFile 一个 multipart 文件部件。
//
// 字段名只能手写 —— 它不在任何 DTO 上（控制器用 FormFile 按字面量取），
// 而猜错字段名的表现是"服务端说没收到文件"，从这条错误里看不出该填哪个名字。
func argFile(name, desc string) ParamSpec {
	return ParamSpec{Name: name, In: ParamInForm, Type: "file", Required: true, Desc: desc}
}

// argFormStr 一个 multipart 普通字段（与文件同在一个表单里，不放进 JSON body）。
func argFormStr(name string, required bool, desc string) ParamSpec {
	return ParamSpec{Name: name, In: ParamInForm, Type: "string", Required: required, Desc: desc}
}

// pathParams 从路由串导出路径参数（:id 这类）。
//
// 不进表：路径串本身就是真源，抄一遍只是给漂移开一个入口 —— 而这里漂移的症状是
// dsh 把 ":id" 当字面量塞进 URL，得到一堆 404 却看不出原因。
//
// 返回值恒为非 nil（表里用它区分"声明了"与"忘了声明"）。
func pathParams(rel string) []ParamSpec {
	out := make([]ParamSpec, 0, 1)
	for _, seg := range strings.Split(rel, "/") {
		name, ok := strings.CutPrefix(seg, ":")
		if !ok || name == "" {
			continue
		}
		out = append(out, ParamSpec{
			Name:     name,
			In:       ParamInPath,
			Type:     "string",
			Required: true,
			Desc:     "路径参数：替换 URL 里的 :" + name,
		})
	}
	return out
}

// structParams 从一个 DTO 导出参数表。tag 决定读哪个标签（body 读 json，query 读 form）。
func structParams(sample any, in ParamIn, tag string, descs map[string]string) []ParamSpec {
	t := reflect.TypeOf(sample)
	if t == nil || t.Kind() != reflect.Struct {
		// 样本必须是控制器**真正绑定的那个 DTO**，否则这张表就又变回手写的了。
		// 传错类型是编码错误（编译期抓不到它），故 panic 而不是回退空表 ——
		// 回退会让这条目的参数静默消失，而 dsh 会照着空表去猜。
		panic("agentApiSurface 的参数样本必须是 struct（即控制器绑定的那个 DTO）")
	}
	return fieldSpecs(t, in, tag, "", descs)
}

// fieldSpecs 导出一层 struct 的字段。prefix 是嵌套路径（"items."），
// 只用来在 descs 里取描述，不写进输出。
func fieldSpecs(t reflect.Type, in ParamIn, tag, prefix string, descs map[string]string) []ParamSpec {
	out := make([]ParamSpec, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, ok := taggedName(f, tag)
		if !ok {
			continue
		}
		spec := ParamSpec{
			Name: name,
			In:   in,
			Desc: descs[prefix+name],
		}
		base := f.Type
		for base.Kind() == reflect.Ptr {
			base = base.Elem()
		}
		spec.Type, spec.Fields = typeAndFields(base, in, tag, prefix+name+".", descs)
		applyBinding(&spec, base, f.Tag.Get("binding"))
		out = append(out, spec)
	}
	return out
}

// taggedName 取字段在指定 tag 里的名字。没有该 tag、或名为 "-" 的字段不进表
// （gin 的绑定器同样跳过它们，故它们也确实收不到值）。
func taggedName(f reflect.StructField, tag string) (string, bool) {
	raw, ok := f.Tag.Lookup(tag)
	if !ok {
		return "", false
	}
	name, _, _ := strings.Cut(raw, ",")
	if name == "" || name == "-" {
		return "", false
	}
	return name, true
}

// typeAndFields 返回字段的对外类型名，以及嵌套子字段（只有对象/对象数组才有）。
func typeAndFields(t reflect.Type, in ParamIn, tag, prefix string, descs map[string]string) (string, []ParamSpec) {
	switch {
	case t == rawMessageType:
		// 结构由协议决定、服务端原样透传。报成 object 是诚实的：具体字段要去查
		// 该协议的 formJson / protocolJson / scanFormJson，本表给不出。
		return "object", nil
	case t.Kind() == reflect.Struct:
		return "object", fieldSpecs(t, in, tag, prefix, descs)
	case t.Kind() == reflect.Slice, t.Kind() == reflect.Array:
		elemType, elemFields := typeAndFields(t.Elem(), in, tag, prefix, descs)
		if elemType == "object" {
			return "[]object", elemFields
		}
		return "[]" + elemType, nil
	case t.Kind() == reflect.String:
		return "string", nil
	case t.Kind() == reflect.Bool:
		return "bool", nil
	case isNumeric(t):
		if t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64 {
			return "number", nil
		}
		return "int", nil
	default:
		// 出现了本表没见过的类型：报 unknown 而不是瞎猜一个像样的名字 ——
		// dsh 看到一个 unknown 会去查文档，看到 "string" 就直接用了。
		return "unknown", nil
	}
}

// isNumeric 判断是否数字类型（不含 byte 之类的别名，它们在本表里也未出现）。
func isNumeric(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// applyBinding 把 gin 的 binding tag 翻成约束。
//
// 只翻**含义确定**的记号。其余（omitempty、dive、gtfield 等）不输出 ——
// 输出一个 dsh 读不懂的记号，比不输出更糟：它会被当成一条真约束去满足。
func applyBinding(spec *ParamSpec, t reflect.Type, binding string) {
	if binding == "" {
		return
	}
	for _, tok := range strings.Split(binding, ",") {
		key, val, _ := strings.Cut(tok, "=")
		switch key {
		case "required":
			spec.Required = true
		case "oneof":
			spec.Enum = strings.Fields(val)
		case "min":
			setBound(spec, t, val, true)
		case "max":
			setBound(spec, t, val, false)
		case "email":
			spec.Format = "email"
		case "http_url", "url":
			spec.Format = "uri"
		}
	}
}

// setBound 按字段类型把 min/max 写到正确的关键字上。
func setBound(spec *ParamSpec, t reflect.Type, val string, isMin bool) {
	n, err := strconv.Atoi(val)
	if err != nil {
		return
	}
	switch {
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		if isMin {
			spec.MinItems = &n
		} else {
			spec.MaxItems = &n
		}
	case isNumeric(t):
		if isMin {
			spec.Minimum = &n
		} else {
			spec.Maximum = &n
		}
	default:
		if isMin {
			spec.MinLength = &n
		} else {
			spec.MaxLength = &n
		}
	}
}
