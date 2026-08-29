package opcua

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/ua"
)

// uaClient 驱动依赖的 gopcua 客户端接口。
// 内嵌 opcua.ClientInterface：Node 需返回 *opcua.Node（其构造唯一途径是
// opcua.NewNode(id, ClientInterface)，故替身须实现完整接口）。
// 真实实现为 *opcua.Client；测试可注入替身。
type uaClient interface {
	opcua.ClientInterface
	Close(ctx context.Context) error
}

// OpcUaClient gopcua 客户端封装。
//
// gopcua 的 SecureChannel 对并发请求按 requestID 串行化收发并路由响应，
// 同一客户端并发 Read 安全（对齐 Modbus TCP / S7 的非独占线程安全驱动）。
// 连接状态由本结构自维护（atomic.Bool）：gopcua 不保证同步暴露连接态，
// 由 Read 遇到传输级错误时 MarkDisconnected 置位，供采集引擎判定重连。
type OpcUaClient struct {
	c         uaClient
	connected atomic.Bool
}

// newRealClient 建立到 OPC UA 服务器的真实客户端（TCP + OpenSecureChannel +
// CreateSession + ActivateSession 握手）。成功后 connected=true。
func newRealClient(ctx context.Context, cfg *OpcUaConfig) (*OpcUaClient, error) {
	c, err := opcua.NewClient(cfg.Endpoint, cfg.opts()...)
	if err != nil {
		return nil, fmt.Errorf("opcua: new client failed: %w", err)
	}
	if err := c.Connect(ctx); err != nil {
		return nil, fmt.Errorf("opcua: connect %s failed: %w", cfg.Endpoint, err)
	}
	oc := &OpcUaClient{c: c}
	oc.connected.Store(true)
	return oc, nil
}

// ReadBatch 一次读取多个节点的 Value 属性，返回与 nodes 顺序一致的 DataValue 列表。
func (c *OpcUaClient) ReadBatch(ctx context.Context, nodes []*ua.NodeID) ([]*ua.DataValue, error) {
	toRead := make([]*ua.ReadValueID, len(nodes))
	for i, n := range nodes {
		toRead[i] = &ua.ReadValueID{NodeID: n, AttributeID: ua.AttributeIDValue}
	}
	resp, err := c.c.Read(ctx, &ua.ReadRequest{
		TimestampsToReturn: ua.TimestampsToReturnNeither,
		NodesToRead:        toRead,
	})
	if err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// IsConnected 返回连接状态（驱动自维护标记）。
func (c *OpcUaClient) IsConnected() bool {
	return c != nil && c.connected.Load()
}

// MarkDisconnected 标记连接断开（由 Read 遇到传输级错误时调用）。
func (c *OpcUaClient) MarkDisconnected() {
	if c != nil {
		c.connected.Store(false)
	}
}

// Close 关闭连接。会话可能已断开，Close 报错忽略（连接对象随即废弃）。
func (c *OpcUaClient) Close() {
	if c == nil || c.c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.c.Close(ctx)
	c.connected.Store(false)
}
