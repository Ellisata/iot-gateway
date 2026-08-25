package fins

import (
	"net"
	"strings"
)

// resolveSrcNode 解析 FINS 帧 SA1 源节点号（UDP/TCP 传输使用；Serial/HostLink 用 unitNo 不涉及）。
//
// 优先级：配置显式 srcNode（1..254）> 本机源 IP 末段（FINS 惯例：节点号=IP 末段）> 默认 defaultSrcNode。
//
// 真机核实（2026-08-17，10.62.100.15 FINS/UDP）：该设备（NJ 类）拒绝 SA1=0 的命令帧，
// 返回结束码 0x2108（"data cannot be changed"），HslCommunicationDemo 能读到 D100 而本驱动
// 0x2108 的差异即在此；SA1 只要落在 1..254 即可正常读取，DA1 允许为 0。
func resolveSrcNode(configured byte, localIP net.IP) byte {
	if configured != 0 && configured != 255 {
		return configured
	}
	if ip4 := localIP.To4(); ip4 != nil {
		if n := ip4[3]; n != 0 && n != 255 {
			return n
		}
	}
	return defaultSrcNode
}

// finsTransport FINS 传输层接口。
// 各传输（UDP/TCP/Serial）共享同一 FINS 应用层帧与解析，仅底层 I/O 不同。
type finsTransport interface {
	// Read 读取指定内存区从 word 起的 count 个字，返回原始字节（2*count，大端）。
	// 结束码错误返回 finsEndCodeError（连接是通的）；网络/超时错误返回普通 error。
	Read(area finsArea, word, count uint16) ([]byte, error)
	// IsConnected 返回连接状态
	IsConnected() bool
	// Close 关闭连接释放资源
	Close() error
}

// newTransport 按配置的传输方式创建对应传输层实例。
func newTransport(cfg *FINSConfig) (finsTransport, error) {
	switch strings.ToUpper(cfg.Transport) {
	case TransportTCP:
		return newFINSTCPClient(cfg)
	case TransportSerial:
		return newFINSSerialClient(cfg)
	case TransportHostLinkTCP:
		return newFINSHostLinkTCPClient(cfg)
	default:
		return newFINSUDPClient(cfg)
	}
}

// sameConfig 判断两份配置的关键连接参数是否一致，用于 Ping 复用已有连接。
func sameConfig(a, b *FINSConfig) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Transport, b.Transport) &&
		a.Host == b.Host && a.Port == b.Port &&
		a.ComPort == b.ComPort &&
		a.DstNode == b.DstNode && a.SrcNode == b.SrcNode &&
		a.DstUnit == b.DstUnit && a.SrcUnit == b.SrcUnit &&
		a.UnitNo == b.UnitNo &&
		strings.EqualFold(a.FCSMode, b.FCSMode)
}
