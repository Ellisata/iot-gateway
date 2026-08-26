package cip

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	cipcore "iot-gateway/driver/cip"
	"iot-gateway/logger"
)

// cipTransport CIP 传输层接口。供 cipDriver 持有，测试可注入 mock。
type cipTransport interface {
	// ReadTag 读取单个标签，返回响应数据区字节。
	// 通用状态错误返回 cipcore.GeneralStatusError（连接是通的，设备拒绝）；
	// 网络/超时错误返回普通 error。
	ReadTag(tagName string, dataTypeCode uint16) ([]byte, error)
	// ReadTags 批量读取多个标签（0x0A 多服务报文），返回与 specs 顺序对应的逐标签结果。
	// 单标签调用退化为 ReadTag 语义。结果 Err 非 nil 表示该标签被设备拒绝（连接仍通）。
	ReadTags(specs []TagSpec) ([]TagResult, error)
	// IsConnected 返回连接状态
	IsConnected() bool
	// Close 关闭连接释放资源
	Close() error
}

// cipClient EtherNet/IP 显式报文客户端（UCMM 无连接方式）。
//
// 连接生命周期：TCP 连接（44818）→ Register Session（0x0065，取会话句柄）
// → 每次读取 SendRRData（0x006F）携带一个 0x4C Data Table Read。
// 单次请求+响应内部持互斥锁，并发 ReadTag 安全。采集轮询本身持续通信即可保活连接。
// （ForwardOpen 连接消息与 0x0A 多标签批量报文为未来优化，暂不实现。）
// 封装层（会话管理与 Common Packet Format）为厂商无关逻辑，见 iot-gateway/driver/cip。
type cipClient struct {
	mu        sync.Mutex
	conn      net.Conn
	timeout   time.Duration
	session   uint32 // Register Session 返回的会话句柄
	connected bool
}

// newCIPClient 创建并建立 CIP 连接（TCP + Register Session）。
func newCIPClient(cfg *CIPConfig) (cipTransport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("cip: host is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("cip: dial %s failed: %w", addr, err)
	}

	c := &cipClient{
		conn:      conn,
		timeout:   timeout,
		connected: true,
	}
	if err := c.registerSession(); err != nil {
		conn.Close()
		c.connected = false
		return nil, err
	}
	return c, nil
}

// registerSession 执行 Register Session 握手，取得会话句柄。
func (c *cipClient) registerSession() error {
	frame := cipcore.BuildRegisterSession(0, cipcore.EncapProtocolVersion)
	logger.Debug("cip: register session req: % X", frame)
	if err := c.writeAll(frame); err != nil {
		return fmt.Errorf("cip: register session write failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		return fmt.Errorf("cip: register session response failed: %w", err)
	}
	logger.Debug("cip: register session resp: % X", resp)
	cmd, session, status, _, err := cipcore.ParseEncapResponse(resp)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("cip: register session status 0x%08X", status)
	}
	if cmd != cipcore.CmdRegisterSession {
		return fmt.Errorf("cip: unexpected register session response command 0x%04X", cmd)
	}
	c.session = session

	logger.Info("cip client connected (session 0x%08X)", session)
	return nil
}

// ReadTag 通过 SendRRData 读取单个标签。
func (c *cipClient) ReadTag(tagName string, dataTypeCode uint16) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readTagLocked(tagName, dataTypeCode)
}

// readTagLocked 单标签 0x4C 读（调用方需持有 c.mu）。
func (c *cipClient) readTagLocked(tagName string, dataTypeCode uint16) ([]byte, error) {
	if c.conn == nil || !c.connected {
		return nil, fmt.Errorf("cip: not connected")
	}

	cipReq := buildDataTableRead(tagName, dataTypeCode)
	frame := cipcore.BuildSendRRData(c.session, cipReq)

	if err := c.writeAll(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("cip: write failed: %w", err)
	}

	resp, err := c.readFrame()
	if err != nil {
		c.connected = false
		return nil, fmt.Errorf("cip: read failed: %w", err)
	}
	cmd, _, status, payload, err := cipcore.ParseEncapResponse(resp)
	if err != nil {
		c.connected = false
		return nil, err
	}
	if status != 0 {
		c.connected = false
		return nil, fmt.Errorf("cip: encap status 0x%08X (resp % X)", status, resp)
	}
	if cmd != cipcore.CmdSendRRData {
		// 协议级异常响应（非网络错误）视为连接失效，对齐 FINS 的断连语义
		c.connected = false
		return nil, fmt.Errorf("cip: unexpected data response command 0x%04X", cmd)
	}

	cipResp, err := cipcore.ParseSendRRData(payload)
	if err != nil {
		return nil, err
	}

	// 通用状态错误（设备响应但拒绝）不置 connected=false —— 连接是通的
	return parseDataTableRead(cipResp)
}

// ReadTags 通过一个 0x0A 多服务报文批量读取多个标签。
//
// 把 N 次 SendRRData 往返收敛为 1 次，是 CIP 点位容量扩展的关键（帧数从 1 点/往返
// 降到 batch 点/往返）。各子响应无长度分隔，按标签类型码的固定尺寸切分；单标签调用
// 退化为 ReadTag。
func (c *cipClient) ReadTags(specs []TagSpec) ([]TagResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil || !c.connected {
		return nil, fmt.Errorf("cip: not connected")
	}
	if len(specs) == 1 {
		data, err := c.readTagLocked(specs[0].Name, specs[0].Code)
		if err != nil {
			return nil, err
		}
		return []TagResult{{Data: data}}, nil
	}

	// 各标签固定数据尺寸（响应解析按此切分）
	sizes := make([]int, len(specs))
	for i := range specs {
		size, ok := cipTypeSize(specs[i].Code)
		if !ok {
			return nil, fmt.Errorf("cip: no fixed size for type code 0x%04X", specs[i].Code)
		}
		sizes[i] = size
	}

	cipReq := buildMultipleDataTableRead(specs)
	frame := cipcore.BuildSendRRData(c.session, cipReq)

	if err := c.writeAll(frame); err != nil {
		c.connected = false
		return nil, fmt.Errorf("cip: write failed: %w", err)
	}
	resp, err := c.readFrame()
	if err != nil {
		c.connected = false
		return nil, fmt.Errorf("cip: read failed: %w", err)
	}
	cmd, _, status, payload, err := cipcore.ParseEncapResponse(resp)
	if err != nil {
		c.connected = false
		return nil, err
	}
	if status != 0 {
		c.connected = false
		return nil, fmt.Errorf("cip: encap status 0x%08X", status)
	}
	if cmd != cipcore.CmdSendRRData {
		c.connected = false
		return nil, fmt.Errorf("cip: unexpected data response command 0x%04X", cmd)
	}
	cipResp, err := cipcore.ParseSendRRData(payload)
	if err != nil {
		return nil, err
	}
	return parseMultipleDataTableRead(cipResp, sizes)
}

// readFrame 读取一个完整封装帧（24 字节头 + 载荷）。
func (c *cipClient) readFrame() ([]byte, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	hdr := make([]byte, cipcore.EncapHeaderSize)
	if _, err := io.ReadFull(c.conn, hdr); err != nil {
		return nil, err
	}
	length := int(binary.LittleEndian.Uint16(hdr[2:4]))
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return nil, err
	}
	return append(hdr, payload...), nil
}

// writeAll 写入全部字节
func (c *cipClient) writeAll(b []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return err
	}
	for len(b) > 0 {
		n, err := c.conn.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// IsConnected 返回连接状态
func (c *cipClient) IsConnected() bool {
	return c != nil && c.connected
}

// Close 关闭连接：尽力发送 Unregister Session 后关闭 TCP。
func (c *cipClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	// 尽力注销会话，忽略错误（会话由 PLC 超时回收）
	_ = c.writeAll(cipcore.BuildEncapHeader(cipcore.CmdUnregisterSession, c.session, 0))
	err := c.conn.Close()
	c.connected = false
	logger.Info("cip client closed")
	return err
}
