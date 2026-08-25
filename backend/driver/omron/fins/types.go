package fins

import (
	"errors"
	"fmt"
)

// protocolName FINS 协议类型注册作用域名（TypeRegistry 前缀）。
const protocolName = "fins"

// 协议注册名（driver.Register）。各协议共享同一 FINS 应用层，
// 仅传输层不同，由注册名决定。
const (
	ProtocolFINSUDP         = "Omron.FINS.UDP"
	ProtocolFINSTCP         = "Omron.FINS.TCP"
	ProtocolFINSSerial      = "Omron.FINS.Serial"
	ProtocolFINSHostLinkTCP = "Omron.FINS.HostLinkTCP"
)

// 传输方式常量（config.transport，由协议注册名固定，忽略 JSON 中的值）
const (
	TransportUDP         = "UDP"
	TransportTCP         = "TCP"
	TransportSerial      = "SERIAL"
	TransportHostLinkTCP = "HOSTLINK"
)

// finsArea FINS 内存区字访问码。
//
// 全部点位统一用「字访问码」读取（bool 点位从读回的字中本地取位），
// 避免 FINS 位读「单请求最多 16 位且限同字」的限制，且能与非布尔点位
// 合并成同一读取区间减少往返。
//
// 以下为现代 CS1/CJ/CP 系列代码；旧 C200H/CV 系列不同（如 CIO=0x80、
// DM=0x88），如目标 PLC 型号不符只需调整此处常量。
type finsArea byte

const (
	AreaCIO finsArea = 0xB0 // CIO：I/O 及内部继电器
	AreaWR  finsArea = 0xB1 // WR：工作区
	AreaHR  finsArea = 0xB2 // HR：保持区
	AreaDM  finsArea = 0x82 // DM：数据内存
)

// FINS 命令码
const (
	cmdMemoryAreaRead = 0x0101 // 内存区读取
	// 命令码高低字节（避免 byte(0x0101) 溢出）
	cmdMemoryAreaReadHi = byte(cmdMemoryAreaRead >> 8)
	cmdMemoryAreaReadLo = byte(cmdMemoryAreaRead & 0xFF)
)

// 协议默认值
const (
	defaultPort         = 9600
	defaultTimeoutMS    = 5000
	defaultMergeWindow  = 100
	defaultMaxReadWords = 100
	defaultStringLen    = 16
	defaultSrcNode      = 16 // 未配置 srcNode 且无法推导本机 IP 末段时的兜底源节点号（1..254 合法）
)

// 字节序 / 字序常量
const (
	ByteOrderBigEndian    = "BIG_ENDIAN"
	ByteOrderLittleEndian = "LITTLE_ENDIAN"
	WordOrderBigEndian    = "BIG_ENDIAN"
	WordOrderLittleEndian = "LITTLE_ENDIAN"
)

// FINSAddress 解析后的 FINS 地址。
// 由 ParseFINSAddress 从点位 name 解析得到（Area/Word/Bit）。
type FINSAddress struct {
	Area finsArea // 内存区
	Word uint16   // 字地址（0-based，十进制解释）
	Bit  int      // 位偏移（0..15），非位地址为 -1
}

// IsBit 是否为位地址（bool 点位，地址携带 .bit 后缀）
func (a FINSAddress) IsBit() bool { return a.Bit >= 0 }

// FINSRange 读取区间（字区间）。
// 由 CalcFINSRanges 返回，供 Read 读取整段字后按偏移解码。
type FINSRange struct {
	Area      finsArea
	StartWord uint16 // 起始字地址（含）
	Count     uint16 // 读取字数（含空洞，读取后按 AddressMap 提取点位）
	// AddressMap 将 DeviceAddress.ID 映射为 FINSAddress。解码时直接查此 map。
	AddressMap map[string]FINSAddress
}

// finsEndCodeError FINS 结束码错误（设备响应但拒绝该请求，如非法地址）。
// 结束码非 0 说明连接是通的，不应据此判定连接断开、也不应中断整台设备读取。
type finsEndCodeError struct {
	code uint16
}

func (e *finsEndCodeError) Error() string {
	return fmt.Sprintf("fins: end code 0x%04X", e.code)
}

// newEndCodeError 构造结束码错误
func newEndCodeError(code uint16) error {
	return &finsEndCodeError{code: code}
}

// IsEndCodeError 判断错误是否为 FINS 结束码错误（设备已响应但拒绝请求）。
func IsEndCodeError(err error) bool {
	var e *finsEndCodeError
	return errors.As(err, &e)
}
