// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uasc"
)

// opcUaLogWriter 吞掉 gopcua server 包的 log 输出（Start 时打印监听地址等），
// 避免压测热路径的日志 I/O 干扰测量；原输出在恢复前仅服务本包测试。
type opcUaLogWriter struct{}

func (*opcUaLogWriter) Write(p []byte) (int, error) { return len(p), nil }

// opcUaBaseNodeID 假服务器变量节点的起始 NodeID 数值（ns=1;i=<base>+k）。
const opcUaBaseNodeID = 1000

// NewOpcUa 启动一个 OPC UA 模拟器。
//
// 与其它协议的手写帧假服务器不同：OPC UA 握手（HEL/ACK、OpenSecureChannel、
// CreateSession/ActivateSession）与安全策略耦合且实现量大，直接复用
// gopcua 的进程内 server 包（与 driver/opcua 集成测试同一方案），在其上做
// 两处压测定制（见 opcUaReadHandler）。
//
// 地址空间：ns=1;i=<n>，n ≥ 1001 均可读，值 = int32(n-1000)。
func NewOpcUa() (*Server, error) {
	return newOpcUaServer(0)
}

// NewOpcUaWithLatency 同 NewOpcUa，但每次 Read 事务注入固定延迟，模拟真实网络 RTT。
// OPC UA 驱动按 MaxBatch（默认 100 节点/请求）分批读，RTT 直接叠加进轮询周期，
// 是容量评估的主导项。
func NewOpcUaWithLatency(d time.Duration) (*Server, error) {
	return newOpcUaServer(d)
}

func newOpcUaServer(latency time.Duration) (*Server, error) {
	// gopcua server 的监听地址在 Start 内部经 uacp.Listen 解析 endpoint，
	// 无注入 net.Listener 的途径：先探测空闲端口再配置（微小竞态窗口，
	// 压测可接受，与驱动集成测试 freePort 的取舍一致）。
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	// 静默 gopcua server 的全局 log 输出（log.Printf 无注入 logger 的路径，
	// server.SetLogger 只覆盖 cfg.logger），测量窗口结束即恢复。
	log.SetOutput(&opcUaLogWriter{})
	defer log.SetOutput(log.Writer())

	// 匿名 + SecurityPolicy None：压测关注容量而非加解密开销（签名/加密
	// 在两端同时发生，网关侧真实部署多为 None/Sign 混合，此处取开销下限）。
	srv := server.New(
		server.EndPoint("127.0.0.1", port),
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
	)
	ns := server.NewNodeNameSpace(srv, "urn:iot-gateway:loadtest")
	nsID := ns.ID()

	ctx, cancel := context.WithCancel(context.Background())

	// Read 覆盖处理器必须在 Start 前 RegisterHandler（Start 后 dispatch 已并发进行）。
	// 地址空间不预建 Node 对象：ns=1;i=<n>（n>1000）全部由覆盖处理器按公式返回，
	// 不受点数上限约束（真实 PLC 地址空间为索引结构，百万点仍 O(1)）。
	srv.RegisterHandler(uint16(id.ReadRequest_Encoding_DefaultBinary),
		func(sc *uasc.SecureChannel, r ua.Request, reqID uint32) (ua.Response, error) {
			return opcUaReadHandler(nsID, latency, r, reqID)
		})

	if err := srv.Start(ctx); err != nil {
		cancel()
		return nil, err
	}

	// 假服务器没有外层 TCP 骨架（gopcua server 自带监听与连接管理），
	// 端口取启动前探测值，Close 走 ctx cancel + srv.Close 组合。
	return newManagedServer(
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
		func() {
			cancel()
			_ = srv.Close()
		},
	), nil
}

// opcUaReadHandler 压测定制的 Read 服务：
//   - ns=1;i=<n>（n ≥ 1001）→ 固定 int32 值 n-1000（含预建节点，行为一致）；
//   - 其余（不存在/Bad NodeID）→ 逐点 BadNodeIDUnknown，服务级 StatusGood，
//     与真实服务器「批量请求内部分节点失败」的行为一致；
//   - latency > 0 时每事务注入固定延迟（模拟 RTT，不影响会话握手）。
//
// 不走 gopcua 原生 Read（namespace → Node map 查找 + 每节点 DataValue 组装）：
// 百万级点位下假服务器的 map/组装开销远超真实 PLC（真实地址空间为索引结构），
// 查表化把测量热点收敛回网关侧。
func opcUaReadHandler(nsID uint16, latency time.Duration, r ua.Request, reqID uint32) (ua.Response, error) {
	req, ok := r.(*ua.ReadRequest)
	if !ok {
		return nil, ua.StatusBadRequestTypeInvalid
	}
	if latency > 0 {
		time.Sleep(latency)
	}

	results := make([]*ua.DataValue, len(req.NodesToRead))
	for i, rv := range req.NodesToRead {
		results[i] = opcUaValueFor(nsID, rv.NodeID)
	}
	// 客户端 Connect 尾部经 NamespaceArray（ns=0;i=2255）拉取命名空间数组，
	// 期望 []string；覆盖处理器需额外满足该固定读（gopcua 原生路径已在此前
	// 为 ns=0 预建了该节点，但本处理器拦截了全部 Read）。
	for i, rv := range req.NodesToRead {
		if rv.NodeID == nil || rv.NodeID.Namespace() != 0 || rv.NodeID.IntID() != id.Server_NamespaceArray {
			continue
		}
		if rv.AttributeID != 0 && rv.AttributeID != ua.AttributeIDValue {
			continue
		}
		results[i] = &ua.DataValue{
			EncodingMask:    ua.DataValueValue | ua.DataValueStatusCode,
			Value:           ua.MustVariant([]string{"http://opcfoundation.org/UA/", "urn:iot-gateway:loadtest"}),
			SourceTimestamp: time.Now(),
			Status:          ua.StatusOK,
		}
	}
	return &ua.ReadResponse{
		ResponseHeader: opcUaResponseHeader(req.RequestHeader.RequestHandle),
		Results:        results,
	}, nil
}

// opcUaResponseHeader 构造 Read 响应头（RequestHandle 回显 + StatusGood）。
func opcUaResponseHeader(reqHandle uint32) *ua.ResponseHeader {
	return &ua.ResponseHeader{
		Timestamp:          time.Now(),
		RequestHandle:      reqHandle,
		ServiceResult:      ua.StatusOK,
		ServiceDiagnostics: &ua.DiagnosticInfo{},
		StringTable:        []string{},
		AdditionalHeader:   ua.NewExtensionObject(nil),
	}
}

// opcUaValueFor 按压测地址空间公式返回节点的 Value DataValue。
func opcUaValueFor(nsID uint16, nid *ua.NodeID) *ua.DataValue {
	if nid == nil || int(nid.Namespace()) != int(nsID) || nid.IntID() <= opcUaBaseNodeID {
		// EncodingMask 必须显式含 StatusCode：Encode 按 mask 逐字段写出，
		// 零 mask 会导致 Status 不上线、客户端解码为 Good 空值
		return &ua.DataValue{EncodingMask: ua.DataValueStatusCode, Status: ua.StatusBadNodeIDUnknown}
	}
	return &ua.DataValue{
		EncodingMask:    ua.DataValueValue | ua.DataValueStatusCode,
		Value:           ua.MustVariant(int32(nid.IntID() - opcUaBaseNodeID)),
		SourceTimestamp: time.Now(),
		Status:          ua.StatusOK,
	}
}
