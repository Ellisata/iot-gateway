package opcua

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
)

// opcuaAddress 单个点位地址的解析结果：直接 NodeID 或浏览路径段。
type opcuaAddress struct {
	nodeID *ua.NodeID          // 直接 NodeID（isPath=false 时有效）
	path   []*ua.QualifiedName // 浏览路径段（isPath=true 时有效）
	isPath bool
}

// parseError 地址字符串本地解析失败（语法/格式层面），区别于浏览路径解析的
// 服务器响应错误（ua.StatusCode）与网络传输错误。驱动 Read 据此将本地解析
// 失败的点位标记 Quality=0 继续，而不误判为传输级断连。
type parseError struct{ err error }

func (e *parseError) Error() string { return e.err.Error() }

func (e *parseError) Unwrap() error { return e.err }

// parseAddress 解析地址字符串（存于 po.DeviceAddress.Name）。
//
// 支持两种形式：
//   - NodeID：标准 OPC UA NodeID 字符串，如 "ns=2;s=Temperature"、"i=2259"、
//     "ns=2;i=100"、"ns=2;g=<guid>"、"ns=2;b=<opaque>"（经 ua.ParseNodeID）；
//   - 浏览路径："/" 分隔的层次路径，如 "Root/Objects/Device/Temperature"，
//     段可用 "ns=<n>:Name" 指定命名空间（如 "Root/Objects/2:Device/2:Temp"）。
//     起点支持前导 "Root"（RootFolder）或 "Objects"（ObjectsFolder），
//     其余从起点沿层次引用向下解析；本实现仅支持 "/" 下行操作符
//     （不支持 OPC UA 相对路径的 "." 子节点 / "#" 子类型 / 反向引用）。
//
// NodeID 的标准形式（i=/s=/g=/b= + ns=）不含 "/"，据此区分两种输入。
// 本地语法错误一律包装为 *parseError。
func parseAddress(name string) (*opcuaAddress, error) {
	s := strings.TrimSpace(name)
	if s == "" {
		return nil, &parseError{fmt.Errorf("opcua: empty address")}
	}
	if strings.Contains(s, "/") {
		path, err := parseBrowsePath(s)
		if err != nil {
			return nil, &parseError{err}
		}
		return &opcuaAddress{path: path, isPath: true}, nil
	}
	nodeID, err := ua.ParseNodeID(s)
	if err != nil {
		return nil, &parseError{fmt.Errorf("opcua: invalid node id %q: %w", s, err)}
	}
	return &opcuaAddress{nodeID: nodeID}, nil
}

// parseBrowsePath 解析浏览路径字符串为相对路径段。
func parseBrowsePath(s string) ([]*ua.QualifiedName, error) {
	segs := strings.Split(s, "/")
	// 可选前导起点：Root（RootFolder）/ Objects（ObjectsFolder）
	if len(segs) > 0 && (strings.EqualFold(segs[0], "Root") || strings.EqualFold(segs[0], "Objects")) {
		segs = segs[1:]
	}
	path := make([]*ua.QualifiedName, 0, len(segs))
	for _, seg := range segs {
		if seg == "" {
			continue // 容忍连续斜杠
		}
		qn, err := parsePathSegment(seg)
		if err != nil {
			return nil, err
		}
		path = append(path, qn)
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("opcua: empty browse path %q", s)
	}
	return path, nil
}

// parsePathSegment 解析单个路径段："ns=<n>:Name" 或裸 "Name"。
func parsePathSegment(seg string) (*ua.QualifiedName, error) {
	if i := strings.IndexByte(seg, ':'); i >= 0 {
		nsStr := seg[:i]
		name := seg[i+1:]
		if name == "" {
			return nil, fmt.Errorf("opcua: empty browse name in segment %q", seg)
		}
		if strings.HasPrefix(strings.ToLower(nsStr), "ns=") {
			nsStr = nsStr[3:]
		}
		ns, err := strconv.Atoi(nsStr)
		if err != nil || ns < 0 || ns > 65535 {
			return nil, fmt.Errorf("opcua: invalid namespace index in segment %q", seg)
		}
		return &ua.QualifiedName{NamespaceIndex: uint16(ns), Name: name}, nil
	}
	return &ua.QualifiedName{Name: seg}, nil
}

// resolveBrowsePath 将浏览路径段解析为具体 NodeID（服务器 TranslateBrowsePathsToNodeIds 服务）。
// c 需已连接；以 RootFolder（ns=0;i=84）为起点。
func resolveBrowsePath(ctx context.Context, c opcua.ClientInterface, path []*ua.QualifiedName) (*ua.NodeID, error) {
	root := c.Node(ua.NewNumericNodeID(0, id.RootFolder))
	return root.TranslateBrowsePathsToNodeIDs(ctx, path)
}
