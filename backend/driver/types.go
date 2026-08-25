package driver

import "encoding/binary"

// DataType 数据类型定义。
//
// 每种协议在 init() 中通过 TypeRegistry 注册自己的编解码器。
// Decode/Encode 接收 binary.ByteOrder 处理字节序，协议特有逻辑（如 Modbus 字序交换）
// 在协议层的 ParseReadResult/EncodeWriteValue 中提前处理。
type DataType struct {
	// Name 内部标识名，全小写，如 "bool", "int16", "float32"
	Name string

	// Kind 数据类型类别（KindBool/KindInt/KindUInt/KindFloat/KindString/KindTime）。
	// 推送通道按 Kind 决定时序库存储表示，而非枚举类型名（见 push 通道 formatFieldValue/
	// parseValueByType）。各协议注册时声明一次，为分类的唯一事实来源。
	Kind string

	// Size 数据占用字节数。
	// 0 表示动态长度（如 string），由协议层自行确定。
	Size int

	// Decode 将原始字节解码为 Go 值。
	// raw 已被协议层预处理过（字序交换等），只做字节序转换。
	Decode func(raw []byte, bo binary.ByteOrder) (any, error)

	// Encode 将值编码为原始字节。
	// val 通常是 float64（来自 JSON 反序列化），也支持原生类型。
	Encode func(val any, bo binary.ByteOrder) ([]byte, error)

	// Format 将解码后的值格式化为字符串（用于存储和展示）。
	Format func(val any) string
}

// PLC 数据类型类别（DataType.Kind 的取值）。
// 所有协议的类型最终坍缩到这 6 类；推送通道按类别分派，新增协议类型只需注册时声明 Kind。
const (
	KindBool   = "bool"   // 布尔
	KindInt    = "int"    // 有符号整数
	KindUInt   = "uint"   // 无符号整数
	KindFloat  = "float"  // 浮点
	KindString = "string" // 文本（string/char）
	KindTime   = "time"   // 日期/时间值（date/time/tod/s5time/dt；当前通道按字符串存储）
)
