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

// matchConfig 返回本实例当前持有的、能直接服务 cfg 的那条传输（没有则 nil）。
//
// 返回传输对象而不是 bool，是为了让调用方拿到"刚才被判定可用的那一个" ——
// 否则判定与取用是两次独立的快照，中间 Connect 换掉 client 时会对着一个
// 已关闭的连接收发。
//
// 只有 Serial（Host Link 本地串口）参与复用：UDP/TCP/HostLinkTCP 都不独占端口，
// 复用它们的收益为零，反而会让 ping 白排在一个可能长达数十秒的批量读取后面。
func (d *finsDriver) matchConfig(cfg *FINSConfig) finsTransport {
	if d.transport != TransportSerial {
		return nil
	}
	d.mu.RLock()
	live, cur := d.client, d.config
	d.mu.RUnlock()

	if live == nil || !live.IsConnected() || !sameConfig(cur, cfg) {
		return nil
	}
	return live
}

// MatchConnection 见 driver.ConnectionMatcher。
func (d *finsDriver) MatchConnection(protocolJSON string) bool {
	if d.transport != TransportSerial {
		return false
	}
	cfg, err := ParseFINSConfig(protocolJSON)
	if err != nil {
		// JSON 非法时不复用，交给 Ping 去报那条真正的解析错误
		return false
	}
	// 与 Connect/Ping 保持一致：传输层由协议注册名固定，JSON 里的 transport
	// 不参与比较，否则协议表单里残留 transport=tcp 的串口设备永远匹配不上。
	cfg.Transport = d.transport
	return d.matchConfig(cfg) != nil
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
// **串口链路参数必须参与比较**：finsSerialClient 的端口/波特率/数据位/停止位/
// 校验位在打开串口时就固化死了，复用一条 9600/N 的连接去测 19200/E 的配置，
// 会给出一个与被测配置无关的结论 —— 用户改了波特率再点「测试连接」，
// 拿到的是一个绿色的、什么也没验证的成功。
//
// ComPort 用 EqualFold：Windows 的串口名大小写不敏感，"com1" 与 "COM1" 是同一个
// 端口，用 == 比会把本可复用的情况误判成不可复用，退回临时实例后撞 Access is denied。
func sameConfig(a, b *FINSConfig) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Transport, b.Transport) &&
		a.Host == b.Host && a.Port == b.Port &&
		strings.EqualFold(a.ComPort, b.ComPort) &&
		a.BaudRate == b.BaudRate && a.DataBits == b.DataBits &&
		a.StopBits == b.StopBits && strings.EqualFold(a.Parity, b.Parity) &&
		a.DstNode == b.DstNode && a.SrcNode == b.SrcNode &&
		a.DstUnit == b.DstUnit && a.SrcUnit == b.SrcUnit &&
		a.UnitNo == b.UnitNo &&
		strings.EqualFold(a.FCSMode, b.FCSMode)
}
