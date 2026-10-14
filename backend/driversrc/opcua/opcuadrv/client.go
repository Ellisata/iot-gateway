// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"errors"
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

// ReadAttribute 一次读取多个节点的**同一个属性**（如 DataType），
// 返回与 nodes 顺序一致的 DataValue 列表。
//
// 存在的理由与 ReadBatch 是采集路径的优化不同：这里是点位扫描的正确性问题。
// 逐个节点读属性是一个节点一次请求（库的 Node.Attribute 就是这么做的），
// 而一次扫描要问几百上千个变量 —— 那样扫描必然撞上传预算。批量读把请求数
// 除以 MaxBatch，是"一次扫描能扫完多少个点位"的唯一杠杆。
//
// 与 ReadBatch 一样**按序对应**，但**每项的状态要各自判**：服务器完全可能对
// 其中几个节点回 BadNodeIdUnknown/BadAttributeIdInvalid 而其余正常，
// 调用方不得因为一项失败就丢掉整批。
func (c *OpcUaClient) ReadAttribute(ctx context.Context, nodes []*ua.NodeID,
	attr ua.AttributeID) ([]*ua.DataValue, error) {
	toRead := make([]*ua.ReadValueID, len(nodes))
	for i, n := range nodes {
		toRead[i] = &ua.ReadValueID{NodeID: n, AttributeID: attr}
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

// browseNextMaxRounds 单个批次的续页收尾上限。
//
// 纯粹是防御上界，不是性能旋钮：一个**行为异常**的服务器可以一直回"还有下一页"，
// 而循环里没有时间判断的话就是一次不会退出的 RPC —— 撞网关侧超时、累积插件卡死
// 计数，连续几次把整个插件进程连同它下面所有协议、所有设备一起重启。
// 正常服务器几百个引用一两页就收完了。
const browseNextMaxRounds = 64

// BrowseBatch 一次发起多个 BrowseDescription，并**把每个节点的续页都收干净**，
// 返回与 descs 顺序一致的 BrowseResult 列表（缺失的槽位为 nil）。
//
// 返回整个 BrowseResult 而不只是 References：调用方要按 `StatusCode` 逐节点判定。
// 一个节点不可浏览（BadNodeIdUnknown、BadNoMatch）**不是**整次请求的失败 ——
// 它是服务器对"这一支我读不了"的正常答复，丢掉这个区分就会把一台只有某个
// 子分支异常的服务器判成一无所获。
//
// 为什么不直接用库的 Node.References：它一次只发一个 BrowseDescription，而且
// 收尾的 browseNext（node.go）只读 results[0] —— 一次问多个节点时，除第一个之外
// 所有节点的续页都会被**静默丢掉**，表现是"这台设备只列出前一批点位，一个错都不报"。
// 点位扫描一次要问一整层，这正是一批量就撞上的地方。
//
// 续页刻意**合并成一次 BrowseNext**：ContinuationPoints 是切片，Results 与之按序
// 对应，一节点一请求会把请求数再乘一遍页数。RequestedMaxReferencesPerNode 传 0
// （不限制）仍不保证不分页 —— 模块化的服务器一个节点几百条引用是常态。
//
// 错误分类对齐驱动 Read 的既有取舍：**传输级**错误直接上抛（调用方中止扫描），
// **服务级**（ua.StatusCode，如续页句柄失效）只记一行并带着已收到的引用返回 ——
// 少一页好过整次扫描失败。
func (c *OpcUaClient) BrowseBatch(ctx context.Context,
	descs []*ua.BrowseDescription) ([]*ua.BrowseResult, error) {
	resp, err := c.c.Browse(ctx, &ua.BrowseRequest{
		View:                          &ua.ViewDescription{ViewID: ua.NewTwoByteNodeID(0)},
		RequestedMaxReferencesPerNode: 0,
		NodesToBrowse:                 descs,
	})
	if err != nil {
		return nil, err
	}

	out := make([]*ua.BrowseResult, len(descs))
	points := make([][]byte, len(descs)) // 与 descs 同序；nil 表示该节点已收完
	for i := range out {
		if i >= len(resp.Results) || resp.Results[i] == nil {
			continue
		}
		out[i] = resp.Results[i]
		points[i] = resp.Results[i].ContinuationPoint
	}

	for round := 0; round < browseNextMaxRounds; round++ {
		idxs := make([]int, 0, len(points))
		cps := make([][]byte, 0, len(points))
		for i, cp := range points {
			if len(cp) > 0 {
				idxs = append(idxs, i)
				cps = append(cps, cp)
			}
		}
		if len(cps) == 0 {
			return out, nil
		}

		nresp, err := c.c.BrowseNext(ctx, &ua.BrowseNextRequest{
			ContinuationPoints:        cps,
			ReleaseContinuationPoints: false,
		})
		if err != nil {
			var sc ua.StatusCode
			if errors.As(err, &sc) {
				logf("browse 续页被拒（%v），本批按已收到的引用继续", err)
				return out, nil
			}
			return nil, err
		}
		for j, idx := range idxs {
			if j >= len(nresp.Results) || nresp.Results[j] == nil || out[idx] == nil {
				points[idx] = nil
				continue
			}
			out[idx].References = append(out[idx].References, nresp.Results[j].References...)
			points[idx] = nresp.Results[j].ContinuationPoint
		}
	}
	logf("browse 续页超过 %d 轮，按已收到的引用继续（服务器可能行为异常）", browseNextMaxRounds)
	return out, nil
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
