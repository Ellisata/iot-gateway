// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

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
//
// 串口链路参数（波特率/数据位/停止位/校验位）必须参与比较：finsSerialClient 的
// 这些参数在打开串口时就固定死了，复用一条 9600/N 的连接去测 19200/E 的配置，
// 会给出一个与被测配置无关的成功结论。
func sameConfig(a, b *FINSConfig) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Transport, b.Transport) &&
		a.Host == b.Host && a.Port == b.Port &&
		a.ComPort == b.ComPort &&
		a.BaudRate == b.BaudRate && a.DataBits == b.DataBits &&
		a.StopBits == b.StopBits && strings.EqualFold(a.Parity, b.Parity) &&
		a.DstNode == b.DstNode && a.SrcNode == b.SrcNode &&
		a.DstUnit == b.DstUnit && a.SrcUnit == b.SrcUnit &&
		a.UnitNo == b.UnitNo &&
		strings.EqualFold(a.FCSMode, b.FCSMode)
}

// MatchConnection 实现 driver.SerialOwner：本实例当前持有的连接能否服务这次测试。
//
// 只有 Serial（Host Link 本地串口）参与连接复用。UDP/TCP/HostLinkTCP 都不独占端口，
// 复用一条可能早已失效的长连接只会误报失败，而新开一条连接的代价为零。
func (d *finsDriver) MatchConnection(protocolJSON string) bool {
	if d.transport != TransportSerial {
		return false
	}
	probe, err := ParseFINSConfig(protocolJSON)
	if err != nil {
		return false // JSON 非法：交给 Ping 去报解析错误，不走复用
	}
	// 与 Connect/Ping 保持一致：传输层由协议注册名固定，JSON 里的 transport 不参与比较
	probe.Transport = d.transport

	d.mu.RLock()
	cur := d.config
	d.mu.RUnlock()
	return sameConfig(cur, probe)
}

// SerialResource 实现 driver.SerialOwner：返回本实例当前独占的串口名。
//
// 刻意不看 IsConnected()：「持有句柄」与「链路可用」是两回事——串口读失败只把连接
// 标记为断开、并不释放句柄，那个端口在重连之前仍然被本实例占着，
// 而这正是需要提示「被谁占用」的时刻。
func (d *finsDriver) SerialResource() string {
	if d.transport != TransportSerial {
		return ""
	}
	d.mu.RLock()
	cfg, client := d.config, d.client
	d.mu.RUnlock()
	if cfg == nil || client == nil {
		return ""
	}
	return cfg.ComPort
}
