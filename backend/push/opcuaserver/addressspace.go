// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/server/attrs"
	"github.com/gopcua/opcua/ua"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// pointNode 一个暴露给 OPC UA 的点位。
//
// 值放在 atomic.Pointer 里而不是调用 Node.SetAttribute：gopcua 的
// SetAttribute 替换内部闭包时不加锁，而服务端 Read/订阅会在其它 goroutine
// 里调用它，并发写会构成数据竞争。用 ValueFunc 闭包读原子指针，读写都安全。
type pointNode struct {
	deviceID  string
	addressID string
	node      *server.Node
	codec     valueCodec
	val       atomic.Pointer[ua.DataValue]
}

// browseNode 地址空间里的一个可浏览节点，仅用于 TranslateBrowsePaths。
// gopcua 不提供读取 Node.refs 的公开接口，所以建树时另存一份索引。
type browseNode struct {
	id       *ua.NodeID
	name     string
	children []*browseNode
}

// browseIndex NodeID → 节点。构建后只读，可被服务端多个连接并发查询。
type browseIndex struct {
	nodes map[string]*browseNode
}

func newBrowseIndex() *browseIndex {
	return &browseIndex{nodes: make(map[string]*browseNode)}
}

func (b *browseIndex) add(n *browseNode) {
	b.nodes[n.id.String()] = n
}

func (b *browseIndex) addChild(parent, child *browseNode) {
	parent.children = append(parent.children, child)
	b.add(child)
}

// topology 一次从数据库读出的地址空间拓扑快照。
type topology struct {
	devices []po.Device
	addrs   map[string][]po.DeviceAddress
	sig     string
}

// loadTopology 读取启用中的设备与点位，并计算结构签名。
//
// 只读 status=1：停用的设备/点位不该出现在 OPC UA 地址空间里。
// 签名只包含影响树结构的字段，值变化不触发重建。
func loadTopology(ctx context.Context, db *gorm.DB, cfg *config) (*topology, error) {
	if db == nil {
		return &topology{addrs: map[string][]po.DeviceAddress{}, sig: "empty"}, nil
	}

	var devices []po.Device
	if err := db.WithContext(ctx).Where("status = ?", 1).Order("id ASC").Find(&devices).Error; err != nil {
		return nil, fmt.Errorf("opcua: 读取设备失败: %w", err)
	}
	allow := cfg.deviceSet()
	if allow != nil {
		filtered := devices[:0]
		for _, d := range devices {
			if _, ok := allow[d.ID]; ok {
				filtered = append(filtered, d)
			}
		}
		devices = filtered
	}

	ids := make(map[string]struct{}, len(devices))
	for _, d := range devices {
		ids[d.ID] = struct{}{}
	}

	addrs := make(map[string][]po.DeviceAddress, len(devices))
	if len(ids) > 0 {
		var rows []po.DeviceAddress
		if err := db.WithContext(ctx).Where("status = ?", 1).Order("id ASC").Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("opcua: 读取点位失败: %w", err)
		}
		for _, a := range rows {
			if _, ok := ids[a.DeviceID]; ok {
				addrs[a.DeviceID] = append(addrs[a.DeviceID], a)
			}
		}
	}

	topo := &topology{devices: devices, addrs: addrs}
	topo.sig = topo.signature()
	return topo, nil
}

func (t *topology) signature() string {
	h := fnv.New64a()
	write := func(parts ...string) {
		for _, p := range parts {
			_, _ = io.WriteString(h, p)
			_, _ = io.WriteString(h, "\x1f")
		}
		_, _ = io.WriteString(h, "\x1e")
	}
	for _, d := range t.devices {
		write("d", d.ID, d.Name, d.Description)
		for _, a := range t.addrs[d.ID] {
			write("a", a.ID, a.Name, a.Label, a.CommonDataType, a.DataType, a.Description)
		}
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func pointKey(deviceID, addressID string) string {
	return deviceID + "\x00" + addressID
}

func deviceNodeID(cfg *config, nsID uint16, dev po.Device) *ua.NodeID {
	if cfg.NodeIDStyle == nodeIDStyleName {
		return ua.NewStringNodeID(nsID, "dev:"+dev.Name)
	}
	return ua.NewStringNodeID(nsID, "dev:"+dev.ID)
}

func pointNodeID(cfg *config, nsID uint16, dev po.Device, addr po.DeviceAddress) *ua.NodeID {
	if cfg.NodeIDStyle == nodeIDStyleName {
		return ua.NewStringNodeID(nsID, "pt:"+dev.Name+"/"+addr.Name)
	}
	return ua.NewStringNodeID(nsID, "pt:"+dev.ID+":"+addr.ID)
}

// newObjectNode 造一个只有身份属性的 Object 节点（设备/根文件夹）。
//
// 刻意不用 server.NewFolderNode：它会给自己挂一条指向 ObjectsFolder 的
// 正向引用，Browse 这个文件夹会把 Objects 本身当成子节点返回，客户端遍历
// 时会绕回去。同样的坑在 testutil/fake/opcua.go 里已经踩过。
func newObjectNode(nodeID *ua.NodeID, name, description string) *server.Node {
	n := server.NewNode(
		nodeID,
		map[ua.AttributeID]*ua.DataValue{
			ua.AttributeIDNodeClass:     server.DataValueFromValue(uint32(ua.NodeClassObject)),
			ua.AttributeIDBrowseName:    server.DataValueFromValue(attrs.BrowseName(name)),
			ua.AttributeIDDisplayName:   server.DataValueFromValue(attrs.DisplayName(name, name)),
			ua.AttributeIDDescription:   server.DataValueFromValue(attrs.DisplayName(description, "")),
			ua.AttributeIDEventNotifier: server.DataValueFromValue(int16(0)),
		},
		nil,
		nil,
	)
	return n
}

// newPointNode 把 device_address 建成一个 Variable 节点。
func newPointNode(cfg *config, nsID uint16, dev po.Device, addr po.DeviceAddress) *pointNode {
	codec := codecFor(addr.CommonDataType, "")
	p := &pointNode{
		deviceID:  dev.ID,
		addressID: addr.ID,
		codec:     codec,
	}
	p.val.Store(&ua.DataValue{
		EncodingMask:    ua.DataValueValue | ua.DataValueStatusCode | ua.DataValueServerTimestamp,
		Value:           ua.MustVariant(codec.zero()),
		Status:          ua.StatusBadWaitingForInitialData,
		ServerTimestamp: time.Now(),
	})

	p.node = server.NewVariableNode(
		pointNodeID(cfg, nsID, dev, addr),
		addr.Name,
		server.ValueFunc(func() *ua.DataValue { return p.val.Load() }),
	)
	// NewVariableNode 默认把 DataType 指向 VariableNode，这里必须改成真实类型，
	// 否则客户端按错误类型解码。
	//
	// 值类型必须是 NodeId 而不是 ExpandedNodeId（Part 3 §5.6.2：DataType 属性的
	// 数据类型就是 NodeId）。gopcua 默认写 ExpandedNodeId，UaExpert 解码失败后
	// 拿到空 NodeId（i=0），再去读 i=0 的 NodeClass/BrowseName/DisplayName，
	// 日志里就是「NodeClass of i=0 has bad statuscode: BadNodeIdUnknown」。
	setNodeIDAttribute(p.node, ua.AttributeIDDataType, ua.NewNumericNodeID(0, codec.typeID))
	// 标量点位的 ValueRank = -1。gopcua 默认不提供该属性，缺了 UaExpert 会把
	// "ValueRank ... BadAttributeIdInvalid" 记进日志。
	_ = p.node.SetAttribute(
		ua.AttributeIDValueRank,
		server.DataValueFromValue(int32(-1)),
	)
	// 只读：gopcua 的 Access 在属性缺失时默认「全局可读写」，而 Write 服务会
	// 直接把 Value 换成静态值 —— 一旦被客户端写一次，后续采集的原子指针就
	// 不再被读取（ValueFunc 被覆盖），点位从此冻在那个值上。
	readOnly := server.DataValueFromValue(uint8(ua.AccessLevelTypeCurrentRead))
	_ = p.node.SetAttribute(ua.AttributeIDAccessLevel, readOnly)
	_ = p.node.SetAttribute(ua.AttributeIDUserAccessLevel, readOnly)
	label := addr.Label
	if label == "" {
		label = addr.Name
	}
	// 空 Locale 表示“适用于任何语言”，避免只接受本地区域的客户端把它过滤掉。
	p.node.SetDisplayName(label, "")
	p.node.SetDescription(fmt.Sprintf("%s / %s", dev.Name, label), "")
	return p
}

// addTypeDefinition 给节点补一条 HasTypeDefinition 引用。
// 标准客户端（UaExpert 等）靠它区分 Folder / BaseDataVariable；
// 缺了也能 Read，但地址空间不完整。
func addTypeDefinition(n *server.Node, typeNode *server.Node) {
	if n == nil || typeNode == nil {
		return
	}
	n.AddRef(typeNode, server.RefType(id.HasTypeDefinition), true)
}

// setNodeIDAttribute 写入一个值类型为 NodeId 的属性。
//
// server.DataValueFromValue 按传入值的 Go 类型选择 Variant 编码：传 *ua.NodeID
// 得到 NodeId，传 *ua.ExpandedNodeID 得到 ExpandedNodeId。DataType 属性按
// Part 3 §5.6.2 必须是 NodeId —— 严格客户端（UaExpert）拿到 ExpandedNodeId
// 会解码成空 NodeId，再去读 i=0 并报 BadNodeIdUnknown。
func setNodeIDAttribute(n *server.Node, attr ua.AttributeID, nodeID *ua.NodeID) {
	_ = n.SetAttribute(attr, server.DataValueFromValue(nodeID))
}

// newPropertyNode 造一个只读属性节点（EngineeringUnits / ValuePrecision）。
func newPropertyNode(nodeID *ua.NodeID, name string, dataType uint32, value any) *server.Node {
	n := server.NewVariableNode(nodeID, name, value)
	setNodeIDAttribute(n, ua.AttributeIDDataType, ua.NewNumericNodeID(0, dataType))
	_ = n.SetAttribute(
		ua.AttributeIDValueRank,
		server.DataValueFromValue(int32(-1)),
	)
	readOnly := server.DataValueFromValue(uint8(ua.AccessLevelTypeCurrentRead))
	_ = n.SetAttribute(ua.AttributeIDAccessLevel, readOnly)
	_ = n.SetAttribute(ua.AttributeIDUserAccessLevel, readOnly)
	return n
}

// addPointProperties 给点位补上 UaExpert 会查询的两个标准可选属性。
// 缺了不影响读数/订阅，但客户端日志会持续报
// "Failed to get EU Information" / "Failed to get Value Precision"。
func addPointProperties(ns *server.NodeNameSpace, nsID uint16, owner *server.Node,
	ownerEntry *browseNode, index *browseIndex, baseVarType *server.Node) {

	identifier := owner.ID().StringID()
	euInfo := ua.NewExtensionObject(&ua.EUInformation{
		NamespaceURI: "http://www.opcfoundation.org/UA/units/un/cefact",
		UnitID:       0, // 0 = dimensionless
		DisplayName:  ua.NewLocalizedText("dimensionless"),
		Description:  ua.NewLocalizedText("dimensionless"),
	})
	eu := newPropertyNode(ua.NewStringNodeID(nsID, identifier+":eu"), "EngineeringUnits", id.EUInformation, euInfo)
	addTypeDefinition(eu, baseVarType)
	ns.AddNode(eu)
	owner.AddRef(eu, server.RefType(id.HasProperty), true)
	index.addChild(ownerEntry, &browseNode{id: eu.ID(), name: "EngineeringUnits"})

	precision := newPropertyNode(ua.NewStringNodeID(nsID, identifier+":vp"), "ValuePrecision", id.Double, float64(0))
	addTypeDefinition(precision, baseVarType)
	ns.AddNode(precision)
	owner.AddRef(precision, server.RefType(id.HasProperty), true)
	index.addChild(ownerEntry, &browseNode{id: precision.ID(), name: "ValuePrecision"})
}

// buildAddressSpace 在尚未 Start 的 gopcua server 上建好整棵树。
// 先建树再 Start：避免客户端在节点还没挂完时 Browse 到半棵树。
func buildAddressSpace(cfg *config, ns *server.NodeNameSpace, nsID uint16, root *server.Node,
	ns0 server.NameSpace, topo *topology) (map[string]*pointNode, *browseIndex) {

	folderType := ns0.Node(ua.NewNumericNodeID(0, id.FolderType))
	baseVarType := ns0.Node(ua.NewNumericNodeID(0, id.BaseDataVariableType))

	index := newBrowseIndex()
	// 标准 Objects 节点也要能作为 TranslateBrowsePaths 的起点。
	objectsEntry := &browseNode{id: ua.NewNumericNodeID(0, id.ObjectsFolder), name: "Objects"}
	index.add(objectsEntry)
	rootEntry := &browseNode{id: root.ID(), name: cfg.RootName}
	index.addChild(objectsEntry, rootEntry)

	points := make(map[string]*pointNode)
	for _, dev := range topo.devices {
		devNode := newObjectNode(deviceNodeID(cfg, nsID, dev), dev.Name, dev.Description)
		addTypeDefinition(devNode, folderType)
		ns.AddNode(devNode)
		root.AddRef(devNode, server.RefTypeIDOrganizes, true)
		devEntry := &browseNode{id: devNode.ID(), name: dev.Name}
		index.addChild(rootEntry, devEntry)

		for _, addr := range topo.addrs[dev.ID] {
			p := newPointNode(cfg, nsID, dev, addr)
			addTypeDefinition(p.node, baseVarType)
			ns.AddNode(p.node)
			devNode.AddRef(p.node, server.RefTypeIDHasComponent, true)
			points[pointKey(dev.ID, addr.ID)] = p

			pointEntry := &browseNode{id: p.node.ID(), name: addr.Name}
			index.addChild(devEntry, pointEntry)
			addPointProperties(ns, nsID, p.node, pointEntry, index, baseVarType)
		}
	}
	return points, index
}
