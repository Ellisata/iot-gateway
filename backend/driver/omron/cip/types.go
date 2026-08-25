package cip

// protocolName CIP 协议类型注册作用域名（TypeRegistry 前缀）。
const protocolName = "cip"

// 协议注册名（driver.Register）。CIP 仅 TCP（EtherNet/IP 显式报文）一种传输，单协议名。
const ProtocolCIP = "Omron.CIP"

// 协议默认值
const (
	defaultPort      = 44818 // EtherNet/IP 显式报文标准端口
	defaultTimeoutMS = 5000
	defaultStringLen = 80 // STRING 标签读取最大字节数
)

// CIP 服务码
const (
	cipServiceDataTableRead = 0x4C // Omron 专有：数据表读取（Data Table Read）
)

// Omron 数据表读取专有类型码。
//
// 注意：与通用 CIP（Allen-Bradley 风格）数据类型码不同且冲突（如通用 INT=0x00C3，
// Omron INT=0x00C3），仅适用于 Omron 的 0x4C 数据表读取服务。以下取值依据
// HslCommunication OmronCipNet / gologix 真机验证实现，标注「待真机核实」。
const (
	cipTypeBool   = 0x00C2 // BOOL
	cipTypeSINT   = 0x00D2 // SINT：int8
	cipTypeINT    = 0x00C3 // INT：int16
	cipTypeDINT   = 0x00C4 // DINT：int32
	cipTypeUSINT  = 0x00D3 // USINT：uint8
	cipTypeUINT   = 0x00C8 // UINT：uint16
	cipTypeUDINT  = 0x00C9 // UDINT：uint32
	cipTypeWORD   = 0x00CA // WORD：16 位
	cipTypeDWORD  = 0x00D1 // DWORD：32 位
	cipTypeREAL   = 0x00C6 // REAL：float32
	cipTypeLREAL  = 0x00CD // LREAL：float64
	cipTypeSTRING = 0x00CE // STRING
)

// cipTypeCodeByTypeName 内部类型名 → Omron 数据表类型码（构建 0x4C 请求用）。
// bcd/lbcd 无原生类型码，以 WORD/DWORD 读回后本地 BCD 解码（与 FINS 语义一致）。
var cipTypeCodeByTypeName = map[string]uint16{
	"bool":    cipTypeBool,
	"int8":    cipTypeSINT,
	"int16":   cipTypeINT,
	"int32":   cipTypeDINT,
	"uint8":   cipTypeUSINT,
	"uint16":  cipTypeUINT,
	"word":    cipTypeWORD,
	"uint32":  cipTypeUDINT,
	"float32": cipTypeREAL,
	"float64": cipTypeLREAL,
	"string":  cipTypeSTRING,
	"bcd":     cipTypeWORD,
	"lbcd":    cipTypeDWORD,
}

// cipTypeCode 查询内部类型名对应的 Omron 数据表类型码。
func cipTypeCode(dataType string) (uint16, bool) {
	code, ok := cipTypeCodeByTypeName[dataType]
	return code, ok
}
