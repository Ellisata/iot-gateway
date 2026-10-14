// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"sort"
	"strings"

	"iot-gateway/driver"
)

// 类型内部裸名。这些名字既是本文件查找表的键、也是 addr.DataType 的取值
// （网关按插件 TypeMap 把配置层的通用类型名归一化为裸名后再下发）。
const (
	typeNameBool     = "bool"
	typeNameInt      = "int"
	typeNameUint     = "uint"
	typeNameFloat    = "float"
	typeNameBCD      = "bcd"
	typeNameLBCD     = "lbcd"
	typeNameString   = "string"
	typeNameDateTime = "datetime"
)

// typeTable 本插件的**类型唯一真源**：内部裸名 → DataType。
//
// 与内置实现的区别：内置包把类型注册进全局 TypeRegistry 的 "dlt645" scope，
// 再用 sync.Once 缓存裸名 → *DataType 映射；插件里类型表只此一份，直接查表即可。
//
// ========== 关于 Size 与 Decode 的重要说明 ==========
//
// 所有类型 Size 均为 0，因为 DL/T 645 的数据长度由**数据标识 DI** 决定，
// 与通用数据类型无关（见 dictionary.go / address.go）。
//
// 因此这里**刻意不填 Decode/Encode/Format**，与 modbus 等兄弟插件的写法不同：
//   - 通用类型不携带任何解码所需的信息（字节数、小数位、符号、编码方式全在
//     数据标识里），任何"无规格上下文"的缺省解码都只能靠猜，猜出来的值
//     （例如把 4 字节电量按 0 位小数输出）看似合理实则错误；
//   - 读取路径一律走 decode.go 的 DecodeValue/FormatValue —— 它们持有 DISpec，
//     是解码的唯一事实来源（见 driver.go 的 fillResults）；
//   - 插件进程里没有任何代码会调用这三个字段（driversdk.Serve 只把它们登记进
//     类型表，pluginhost 侧反序列化时按 `json:"-"` 丢弃），网关侧更是**永不调用**
//     （见 driver.DataType 的序列化契约）。
//
// 内置版留着它们是为了满足"注册表自洽"这一形式要求；插件里没有这条要求，
// 留下一份谁都调不到、调了还会返回错误值的"缺省解码"只会误导后来人。
var typeTable = map[string]*driver.DataType{
	typeNameBool: {
		Name: typeNameBool,
		Kind: driver.KindBool,
		Size: 0,
	},
	typeNameInt: {
		Name: typeNameInt,
		Kind: driver.KindInt,
		Size: 0,
	},
	typeNameUint: {
		Name: typeNameUint,
		Kind: driver.KindUInt,
		Size: 0,
	},
	typeNameFloat: {
		Name: typeNameFloat,
		Kind: driver.KindFloat,
		Size: 0,
	},
	typeNameBCD: {
		Name: typeNameBCD,
		Kind: driver.KindUInt,
		Size: 0,
	},
	typeNameLBCD: {
		Name: typeNameLBCD,
		Kind: driver.KindUInt,
		Size: 0,
	},
	typeNameString: {
		Name: typeNameString,
		Kind: driver.KindString,
		Size: 0,
	},
	typeNameDateTime: {
		Name: typeNameDateTime,
		Kind: driver.KindTime,
		Size: 0,
	},
}

// commonTypeMap 通用/扩展类型名 → 内部裸名（上报给网关的 Meta.TypeMap）。
//
// 内容与内置 DL/T 645 的 dlt645Types（iot-gateway 的 driver/dataTypeMap.go）逐项一致：
// 除 String/Date/Float 外，其余通用类型都坍缩到「原始整数」或「工程值」两类，
// 因为 645 的输出形态由数据标识决定，而不是由下拉框决定 ——
//
//	Float / Double        → float：工程值，应用 DI 的小数位
//	BCD / LBCD / Word /   → uint / int：原始整数，不应用小数位
//	DWord / Byte / Short / Long / Char
//	String                → string：BCD 数字串（表号 / 通信地址）
//	Date                  → datetime：日期时间
//	Boolean               → bool：数值非零判定
//
// 13 个通用类型全部有映射，使 AvailableTypes("dlt645") 返回完整列表，
// 且 NormalizeAddressType 无需回退 —— 少映射一个，前端类型下拉就缺一项。
//
// 刻意与内置保持一致而非"重新设计"：两边是同一协议的两个入口，
// 类型语义不同会让同一份现场配置在两个入口下采出不同的值。
var commonTypeMap = map[string]string{
	driver.TypeBoolean: typeNameBool,
	driver.TypeDate:    typeNameDateTime,
	driver.TypeString:  typeNameString,
	driver.TypeByte:    typeNameUint,
	driver.TypeChar:    typeNameInt,
	driver.TypeShort:   typeNameInt,
	driver.TypeWord:    typeNameUint,
	driver.TypeDWord:   typeNameUint,
	driver.TypeLong:    typeNameInt,
	driver.TypeFloat:   typeNameFloat,
	driver.TypeDouble:  typeNameFloat,
	driver.TypeBCD:     typeNameBCD,
	driver.TypeLBCD:    typeNameLBCD,
}

// CommonTypeMap 返回通用/扩展类型名 → 内部裸名，供 driversdk.Meta.TypeMap 使用。
// 返回副本，调用方修改不会影响包内真源。
func CommonTypeMap() map[string]string {
	out := make(map[string]string, len(commonTypeMap))
	for name, internal := range commonTypeMap {
		out[name] = internal
	}
	return out
}

// Types 返回上报给网关的插件类型表（driversdk.Meta.Types）。
//
// 上报**全部**已注册类型，而不只是 CommonTypeMap 能到达的那些。
// 少报的症状很隐蔽：网关的 NormalizeAddressType 靠 reg.Get(data_type) 判断
// 地址里存的内部名是否仍然有效 —— 插件漏报一个类型，历史数据里写着它的点位
// 就会被判为"漂移"，按 common_data_type 改写成别的名字，并**静默回写库**。
// 值恰好一样，所以现场只会看到 data_type 自己变了。
//
// 内置实现是把整个 scope 注册进注册表的，所以上报全集才是行为等价的做法。
// 顺带保证不变式「TypeMap 的每个 value 都出现在 Types 中」天然成立。
//
// Name 为裸名（driversdk 会按插件的 scope 重新加前缀）。
func Types() []*driver.DataType {
	out := make([]*driver.DataType, 0, len(typeTable))
	for _, dt := range typeTable {
		copied := *dt
		out = append(out, &copied)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookupType 按裸名查 DataType。精确命中为主路径（零分配），
// 未命中回退大小写不敏感匹配 —— 与内置版经 TypeRegistry 查询的语义一致
// （注册与查询链路一律 ToLower）。
func lookupType(name string) (*driver.DataType, bool) {
	if dt, ok := typeTable[name]; ok {
		return dt, true
	}
	lower := strings.ToLower(name)
	for bare, dt := range typeTable {
		if strings.ToLower(bare) == lower {
			return dt, true
		}
	}
	return nil, false
}

// typeKind 解析(裸)内部类型名对应的类别 Kind，供构建 ReadResult 时填充。
// 未注册类型返回 ""（推送通道回退为数值推断）。
func typeKind(name string) string {
	if dt, ok := lookupType(name); ok {
		return dt.Kind
	}
	return ""
}
