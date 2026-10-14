// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/server/attrs"
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

// ==================== 可浏览地址空间的假服务器 ====================
//
// 上面那套（NewOpcUa）是**压测**用的：地址空间不预建 Node 对象，ns=1;i=<n>
// 全部由覆盖的 Read 处理器按公式返回，因此它**没有引用关系、Browse 不到任何
// 东西**。点位扫描恰恰只走 Browse，拿它测等于什么都没测。
//
// 下面这套专门给点位扫描用：真的建一棵有引用关系的树，让 Browse 能走。
//
// 两处非用不可的写法（都是 gopcua server 包的脾气，写错了不会报错、只会
// 悄悄列不出东西）：
//
//  1. **节点必须挂在 ns=0 的 Objects 文件夹上**，而 `NewNodeNameSpace` 建的是
//     `ns;i=85`（命名空间**局部**的 Objects），Browse `i=85` 走的是 ns=0 那份。
//     所以这里从 `srv.Namespace(0)` 取标准 Objects 节点，再 AddRef 挂上去。
//  2. **不能用 `AddObject`/`AddVariable`**：它们会给子节点留一个 nil 的 ns
//     反向指针，且不把节点登记进命名空间的 map（Read 会回 BadNodeIDUnknown）。
//     一律 `NewNode`/`NewVariableNode` + 各自 `AddRef`。

// OpcUaFakePoint 假树里一个**应当被扫出来**的变量的期望描述。
//
// 期望值逐字写在这里而不是在测试里另抄一份：抄一份就等于把"假树长什么样"
// 记了两遍，而两遍不一致时失败的是测试而不是被测代码。
type OpcUaFakePoint struct {
	// Address 期望的地址（NodeID 写法 —— 这些节点的标识符里都没有 "/"，
	// 走的是 addressOf 的第一条分支）。
	Address string
	// CommonDataType 期望的通用类型名（TypeMap 的键）。
	CommonDataType string
	// NoteContains 期望 Note 里包含的片段（空表示期望 Note 为空）。
	NoteContains string
	// Label 期望的标签列初值（从起始节点起的显示名路径）。
	Label string
}

// OpcUaTree 假服务器地址空间的描述，供测试断言。
type OpcUaTree struct {
	// NamespaceIndex 假服务器自定义命名空间的索引（1）。
	NamespaceIndex uint16
	// Points 期望被扫出来的变量。
	Points []OpcUaFakePoint
	// Objects 起始节点（ns=0;i=85）的地址写法。
	Objects string
}

// opcUaTreeBase 假树节点的 NodeID 起始值（ns=1;i=<base>+k）。
const opcUaTreeBase = 1000

// NewOpcUaTree 启动一个带可浏览地址空间的 OPC UA 假服务器。
//
// 地址空间（全部挂在标准 Objects 文件夹下，因此**默认起点就能扫到**）：
//
//	Objects
//	└── FakePLC                     (Object)
//	    ├── Temperature             (Double)
//	    ├── Pressure                (Float)
//	    ├── Counter                 (Int32)   ← 无同名通用类型，映射成 Long + Note
//	    ├── Running                 (Boolean)
//	    ├── Stamp                   (DateTime)
//	    ├── Serial                  (String)
//	    └── Sub                     (Object)
//	        └── Deep                (UInt16)
func NewOpcUaTree() (*Server, *OpcUaTree, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	log.SetOutput(&opcUaLogWriter{})
	defer log.SetOutput(log.Writer())

	// EnableSecurity + EnableAuthMode 是**必需**的：GetEndpoints 只返回
	// s.endpoints 里已有的条目，而那是 initEndpoints 按 cfg.enabledSec 填的。
	// 不调 EnableSecurity 时端点表为空 —— 服务器照样连得上（客户端 Connect 直接
	// Dial，不看端点表），但设备扫描的 GetEndpoints 会拿到一份空表。
	srv := server.New(
		server.EndPoint("127.0.0.1", port),
		server.EnableSecurity("None", ua.MessageSecurityModeNone),
		server.EnableAuthMode(ua.UserTokenTypeAnonymous),
	)
	ns := server.NewNodeNameSpace(srv, "urn:iot-gateway:pointscan")
	nsID := ns.ID()

	ns0, err := srv.Namespace(0)
	if err != nil {
		return nil, nil, fmt.Errorf("取 ns=0 命名空间失败: %w", err)
	}
	objects := ns0.Node(ua.NewNumericNodeID(0, id.ObjectsFolder))
	if objects == nil {
		return nil, nil, errors.New("ns=0 的 Objects 文件夹不存在（gopcua server 的 NodeSet 变了？）")
	}

	plc := fakeObject(ua.NewNumericNodeID(nsID, opcUaTreeBase), "FakePLC")
	ns.AddNode(plc)
	objects.AddRef(plc, server.RefTypeIDOrganizes, true)

	sub := fakeObject(ua.NewNumericNodeID(nsID, opcUaTreeBase+10), "Sub")
	ns.AddNode(sub)
	plc.AddRef(sub, server.RefTypeIDHasComponent, true)

	tree := &OpcUaTree{NamespaceIndex: nsID, Objects: "i=85"}
	add := func(parent *server.Node, nid uint32, name string, value any,
		builtin uint32, common, note string, label string) error {
		n := server.NewVariableNode(ua.NewNumericNodeID(nsID, nid), name, value)
		if err := n.SetAttribute(ua.AttributeIDDataType,
			server.DataValueFromValue(ua.NewNumericNodeID(0, builtin))); err != nil {
			return fmt.Errorf("设置 %s 的 DataType 失败: %w", name, err)
		}
		ns.AddNode(n)
		parent.AddRef(n, server.RefTypeIDHasComponent, true)
		tree.Points = append(tree.Points, OpcUaFakePoint{
			Address:        ua.NewNumericNodeID(nsID, nid).String(),
			CommonDataType: common,
			NoteContains:   note,
			Label:          label,
		})
		return nil
	}

	specs := []struct {
		parent       *server.Node
		nid          uint32
		name         string
		value        any
		builtin      uint32
		common, note string
		label        string
	}{
		{plc, opcUaTreeBase + 1, "Temperature", float64(21.5), id.Double,
			"Double", "", "FakePLC/Temperature"},
		{plc, opcUaTreeBase + 2, "Pressure", float32(1.5), id.Float,
			"Float", "", "FakePLC/Pressure"},
		{plc, opcUaTreeBase + 3, "Counter", int32(7), id.Int32,
			"Long", "OPC UA 基础类型 Int32", "FakePLC/Counter"},
		{plc, opcUaTreeBase + 4, "Running", true, id.Boolean,
			"Boolean", "", "FakePLC/Running"},
		{plc, opcUaTreeBase + 5, "Stamp", time.Now(), id.DateTime,
			"Date", "", "FakePLC/Stamp"},
		{plc, opcUaTreeBase + 6, "Serial", "SN-0001", id.String,
			"String", "", "FakePLC/Serial"},
		{sub, opcUaTreeBase + 11, "Deep", uint16(9), id.UInt16,
			"Word", "", "FakePLC/Sub/Deep"},
	}
	for _, s := range specs {
		if err := add(s.parent, s.nid, s.name, s.value, s.builtin,
			s.common, s.note, s.label); err != nil {
			return nil, nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		return nil, nil, err
	}
	return newManagedServer(
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
		func() {
			cancel()
			_ = srv.Close()
		},
	), tree, nil
}

// fakeObject 造一个只有身份属性的 Object 节点（用于文件夹）。
//
// **不用 server.NewFolderNode**：它会给自己挂一条指向 ObjectsFolder 的**正向**
// 引用（"我组织了 Objects 文件夹"），于是 Browse 这个文件夹会把 Objects 本身
// 当成它的子节点返回 —— 遍历会绕回去，点位清单里凭空多出一整棵重复的树。
func fakeObject(nodeID *ua.NodeID, name string) *server.Node {
	return server.NewNode(
		nodeID,
		map[ua.AttributeID]*ua.DataValue{
			ua.AttributeIDNodeClass:   server.DataValueFromValue(uint32(ua.NodeClassObject)),
			ua.AttributeIDBrowseName:  server.DataValueFromValue(attrs.BrowseName(name)),
			ua.AttributeIDDisplayName: server.DataValueFromValue(attrs.DisplayName(name, name)),
		},
		nil,
		nil,
	)
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
