// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"math"
	"time"

	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
	"github.com/gopcua/opcua/uasc"
)

// translateBrowsePathsHandler 覆盖 gopcua 默认的 TranslateBrowsePathsToNodeIDs。
//
// gopcua 的服务端实现直接返回 BadServiceUnsupported，而 UaExpert 把点位拖进
// Data Access View 时会调用它计算 NODE PATH，于是日志里会出现
// "TranslateBrowsePathsToNodeIds failed [ret = BadServiceUnsupported]"。
//
// 请求里的 RelativePath 只包含 BrowseName（引用类型/方向在显示路径时用不到），
// 因此用建树时同步维护的 browseIndex 按名字逐层匹配即可。
func translateBrowsePathsHandler(index *browseIndex) server.Handler {
	return func(_ *uasc.SecureChannel, r ua.Request, _ uint32) (ua.Response, error) {
		req, ok := r.(*ua.TranslateBrowsePathsToNodeIDsRequest)
		if !ok {
			return nil, ua.StatusBadRequestTypeInvalid
		}

		var requestHandle uint32
		if req.RequestHeader != nil {
			requestHandle = req.RequestHeader.RequestHandle
		}
		results := make([]*ua.BrowsePathResult, len(req.BrowsePaths))
		for i, path := range req.BrowsePaths {
			results[i] = index.translate(path)
		}
		return &ua.TranslateBrowsePathsToNodeIDsResponse{
			ResponseHeader: &ua.ResponseHeader{
				Timestamp:          time.Now(),
				RequestHandle:      requestHandle,
				ServiceResult:      ua.StatusOK,
				ServiceDiagnostics: &ua.DiagnosticInfo{},
				StringTable:        []string{},
			},
			Results:         results,
			DiagnosticInfos: []*ua.DiagnosticInfo{},
		}, nil
	}
}

// translate 沿 RelativePath.Elements 逐层匹配子节点的 BrowseName。
//
// 只支持正向、按名字查找：地址空间没有同名兄弟，且本服务的调用方
// （UaExpert 的路径显示、常见客户端）都只用正向 HierarchicalReferences。
func (b *browseIndex) translate(path *ua.BrowsePath) *ua.BrowsePathResult {
	if path == nil || path.StartingNode == nil || path.RelativePath == nil ||
		len(path.RelativePath.Elements) == 0 {
		return &ua.BrowsePathResult{StatusCode: ua.StatusBadNothingToDo}
	}

	current := b.nodes[path.StartingNode.String()]
	if current == nil {
		return &ua.BrowsePathResult{StatusCode: ua.StatusBadNodeIDUnknown}
	}

	for _, element := range path.RelativePath.Elements {
		if element == nil || element.TargetName == nil || element.TargetName.Name == "" {
			return &ua.BrowsePathResult{StatusCode: ua.StatusBadBrowseNameInvalid}
		}
		if element.IsInverse {
			return &ua.BrowsePathResult{StatusCode: ua.StatusBadNoMatch}
		}

		var next *browseNode
		for _, child := range current.children {
			if child.name == element.TargetName.Name {
				next = child
				break
			}
		}
		if next == nil {
			return &ua.BrowsePathResult{StatusCode: ua.StatusBadNoMatch}
		}
		current = next
	}

	return &ua.BrowsePathResult{
		StatusCode: ua.StatusGood,
		Targets: []*ua.BrowsePathTarget{{
			TargetID:           ua.NewExpandedNodeID(current.id, "", 0),
			RemainingPathIndex: math.MaxUint32,
		}},
	}
}
