package cip

// protocolName Rockwell 协议类型注册作用域名（TypeRegistry 前缀）。
const protocolName = "rockwell"

// 协议注册名（driver.Register）。Rockwell 仅 TCP（EtherNet/IP 显式报文）一种传输，单协议名。
const ProtocolRockwellCIP = "Rockwell.CIP"

// 协议默认值
const (
	defaultPort      = 44818 // EtherNet/IP 显式报文标准端口
	defaultTimeoutMS = 5000
	// defaultStringLen Logix STRING 标签解码最大字节数。
	// Logix STRING 为 12 字节头（4 字节长度 + 8 字节保留对齐），标准 STRING 结构总长 88
	// 字节（4 字节长度 + 82 字符 + 2 字节对齐）；解码按 4 字节长度前缀 + 字符处理，
	// 超出部分截断，此值仅作防呆上限（真机核实：Micro800 等变体 STRING 长度可能不同）。
	defaultStringLen = 82
)

// Logix 标准 CIP 类型码（Read Tag 0x4C 响应回显）。
// 注意：与 Omron 私有类型码不同且冲突（见 omron/cip/types.go），不可混用。
// 依据 goindustrial cip/types.go（CIP Vol.1 标准编码）。
const (
	cipTypeBOOL   uint16 = 0x00C1 // BOOL
	cipTypeSINT   uint16 = 0x00C2 // SINT：int8
	cipTypeINT    uint16 = 0x00C3 // INT：int16
	cipTypeDINT   uint16 = 0x00C4 // DINT：int32
	cipTypeLINT   uint16 = 0x00C5 // LINT：int64
	cipTypeREAL   uint16 = 0x00CA // REAL：float32
	cipTypeLREAL  uint16 = 0x00CB // LREAL：float64
	cipTypeDWORD  uint16 = 0x00D3 // DWORD：32 位位阵列
	cipTypeSTRING uint16 = 0x00D0 // STRING：STRUCT（4 字节长度 + 字符）
)
