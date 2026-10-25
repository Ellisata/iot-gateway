// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gopcua/opcua"
	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&po.Device{}, &po.DeviceAddress{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Windows 上 SQLite 文件句柄不释放会让 t.TempDir 的清理失败；
	// 先关连接再删目录（t.Cleanup 是后进先出）。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func seedDevice(t *testing.T, db *gorm.DB, deviceID, deviceName string, deviceStatus int, addrID, addrName, label, commonType string, addrStatus int) {
	t.Helper()
	if err := db.Create(&po.Device{
		ID: deviceID, Name: deviceName, ProtocolID: "ModBus.TCP", ProtocolJSON: "{}",
		Status: deviceStatus, CreatedAt: "2026-10-24 00:00:00", UpdatedAt: "2026-10-24 00:00:00",
	}).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: addrID, DeviceID: deviceID, Name: addrName, Label: label,
		CommonDataType: commonType, DataType: commonType, RwPermission: "R",
		Status: addrStatus, ScanFrequency: 1000,
		CreatedAt: "2026-10-24 00:00:00", UpdatedAt: "2026-10-24 00:00:00",
	}).Error; err != nil {
		t.Fatalf("create address: %v", err)
	}
	// status 列带 `default:1`，GORM 在 Create 时会把零值替换成默认值；
	// 需要「停用」时必须显式更新。
	if deviceStatus == 0 {
		if err := db.Model(&po.Device{}).Where("id = ?", deviceID).Update("status", 0).Error; err != nil {
			t.Fatalf("disable device: %v", err)
		}
	}
	if addrStatus == 0 {
		if err := db.Model(&po.DeviceAddress{}).Where("id = ?", addrID).Update("status", 0).Error; err != nil {
			t.Fatalf("disable address: %v", err)
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestConfigDefaults(t *testing.T) {
	cfg, err := parseConfig("{}")
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.ListenHost != defaultListenHost || cfg.ListenPort != defaultListenPort {
		t.Errorf("listen = %s:%d, want %s:%d", cfg.ListenHost, cfg.ListenPort, defaultListenHost, defaultListenPort)
	}
	if cfg.SecurityPolicy != securityNone || cfg.SecurityMode != "None" {
		t.Errorf("security = %s/%s, want None/None", cfg.SecurityPolicy, cfg.SecurityMode)
	}
	if cfg.NodeIDStyle != nodeIDStyleID {
		t.Errorf("nodeIdStyle = %s, want id", cfg.NodeIDStyle)
	}
	if len(cfg.DeviceIDs) != 0 {
		t.Errorf("deviceIds = %v, want empty", cfg.DeviceIDs)
	}
}

func TestConfigDeviceIDsAcceptsStringAndArray(t *testing.T) {
	for _, raw := range []string{
		`{"deviceIds":" d2, d1 ,d2, "}`,
		`{"deviceIds":["d2","d1","d2"]}`,
	} {
		cfg, err := parseConfig(raw)
		if err != nil {
			t.Fatalf("parseConfig(%s): %v", raw, err)
		}
		if len(cfg.DeviceIDs) != 2 || cfg.DeviceIDs[0] != "d1" || cfg.DeviceIDs[1] != "d2" {
			t.Errorf("deviceIds(%s) = %v, want [d1 d2]", raw, cfg.DeviceIDs)
		}
	}
}

func TestConfigAdvertiseHosts(t *testing.T) {
	cfg, err := parseConfig(`{"listenHost":"0.0.0.0","advertiseHosts":" gw.example.com , 203.0.113.10 "}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if len(cfg.AdvertiseHosts) != 2 || cfg.AdvertiseHosts[0] != "203.0.113.10" || cfg.AdvertiseHosts[1] != "gw.example.com" {
		t.Fatalf("advertiseHosts = %v, want [203.0.113.10 gw.example.com]", cfg.AdvertiseHosts)
	}

	// 只接受裸主机名/IPv4：带协议或端口的是常见误填，直接拒绝。
	for _, raw := range []string{
		`{"advertiseHosts":"opc.tcp://gw.example.com:4840"}`,
		`{"advertiseHosts":"gw.example.com:4840"}`,
		`{"advertiseHosts":"[::1]"}`,
	} {
		if _, err := parseConfig(raw); err == nil {
			t.Errorf("parseConfig(%s) should fail", raw)
		}
	}
}

func TestLoadTopologyOnlyEnabledAndSelected(t *testing.T) {
	db := newTestDB(t)
	seedDevice(t, db, "d1", "PLC-1", 1, "a1", "Temp-1", "温度", "Double", 1)
	seedDevice(t, db, "d2", "PLC-2", 1, "a2", "Temp-2", "温度", "Double", 1)
	seedDevice(t, db, "d3", "PLC-3", 0, "a3", "Temp-3", "温度", "Double", 1)
	seedDevice(t, db, "d4", "PLC-4", 1, "a4", "Temp-4", "温度", "Double", 0)

	cfg, err := parseConfig(`{"deviceIds":["d1","d4"]}`)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	topo, err := loadTopology(context.Background(), db, cfg)
	if err != nil {
		t.Fatalf("loadTopology: %v", err)
	}
	if len(topo.devices) != 2 || topo.devices[0].ID != "d1" || topo.devices[1].ID != "d4" {
		t.Fatalf("devices = %+v, want d1,d4", topo.devices)
	}
	if len(topo.addrs["d1"]) != 1 {
		t.Errorf("d1 points = %d, want 1", len(topo.addrs["d1"]))
	}
	// d4 被选中，但它的点位 status=0。
	if len(topo.addrs["d4"]) != 0 {
		t.Errorf("d4 points = %d, want 0", len(topo.addrs["d4"]))
	}

	// 签名只受结构影响：重复加载应稳定。
	again, err := loadTopology(context.Background(), db, cfg)
	if err != nil {
		t.Fatalf("loadTopology again: %v", err)
	}
	if again.sig != topo.sig {
		t.Errorf("signature changed: %s != %s", again.sig, topo.sig)
	}
}

// TestGetEndpointsMatchesAnyHost 覆盖 gopcua 默认实现按 EndpointURL 精确匹配的问题。
// 服务端监听 0.0.0.0、客户端用 127.0.0.1 请求时，默认实现会返回空端点，
// 客户端因此报「不支持配置的安全策略」。
func TestGetEndpointsMatchesAnyHost(t *testing.T) {
	port := freePort(t)
	cfg, err := parseConfig(fmt.Sprintf(
		`{"listenHost":"0.0.0.0","listenPort":%d,"advertiseHosts":"gateway.example.com"}`, port))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	st, err := newServerState(cfg, &topology{addrs: map[string][]po.DeviceAddress{}, sig: "test"})
	if err != nil {
		t.Fatalf("newServerState: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(st.close)

	endpoint := fmt.Sprintf("opc.tcp://127.0.0.1:%d", port)
	client, err := opcua.NewClient(endpoint,
		opcua.SecurityPolicy("None"),
		opcua.SecurityMode(ua.MessageSecurityModeNone),
		opcua.AuthAnonymous(),
		opcua.AutoReconnect(false),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(ctx) })

	res, err := client.GetEndpoints(ctx)
	if err != nil {
		t.Fatalf("GetEndpoints: %v", err)
	}
	if len(res.Endpoints) != 1 {
		t.Fatalf("endpoints = %d, want 1 (only the matching host)", len(res.Endpoints))
	}
	ep := res.Endpoints[0]
	if ep.SecurityPolicyURI != ua.SecurityPolicyURINone || ep.SecurityMode != ua.MessageSecurityModeNone {
		t.Fatalf("endpoint = %s/%s, want None/None", ep.SecurityPolicyURI, ep.SecurityMode)
	}
	if ep.EndpointURL != endpoint {
		t.Errorf("EndpointURL = %q, want %q (client-facing URL)", ep.EndpointURL, endpoint)
	}

	// CreateSession 也按 EndpointURL 精确匹配服务端端点表；表里必须真的有
	// 客户端使用的地址，否则 UaExpert 会在 ActivateSession 报 BadConfigurationError。
	foundRegistered := false
	for _, registered := range st.srv.Endpoints() {
		if registered.EndpointURL == endpoint {
			foundRegistered = true
			break
		}
	}
	if !foundRegistered {
		t.Errorf("server endpoint table has no %q; CreateSession will return empty ServerEndpoints", endpoint)
	}

	// advertiseHosts 只登记端点描述，让客户端用别名/NAT 地址也能匹配上。
	alias := fmt.Sprintf("opc.tcp://gateway.example.com:%d", port)
	foundAlias := false
	for _, registered := range st.srv.Endpoints() {
		if registered.EndpointURL == alias {
			foundAlias = true
			break
		}
	}
	if !foundAlias {
		t.Errorf("advertiseHosts not registered: missing %q", alias)
	}
}

// TestChannelEndToEnd 走完整链路：DB 拓扑 → gopcua server → 真实客户端 Read。
func TestChannelEndToEnd(t *testing.T) {
	db := newTestDB(t)
	seedDevice(t, db, "d1", "PLC-1", 1, "a1", "Temperature", "温度", "Double", 1)

	port := freePort(t)
	rawCfg := fmt.Sprintf(`{"listenHost":"127.0.0.1","listenPort":%d}`, port)
	chIface, err := newChannel(db, &po.PushChannel{ID: "ch-1", Name: "opcua-1", Type: "opcua", ConfigJSON: rawCfg})
	if err != nil {
		t.Fatalf("newChannel: %v", err)
	}
	ch := chIface.(*channel)
	ch.Start()
	t.Cleanup(ch.Stop)

	ch.mu.RLock()
	st := ch.state
	ch.mu.RUnlock()
	if st == nil {
		t.Fatal("channel did not build an address space")
	}
	nodeID := ua.NewStringNodeID(st.nsID, "pt:d1:a1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := opcua.NewClient(ch.cfg.endpoint(),
		opcua.SecurityPolicy("None"),
		opcua.SecurityMode(ua.MessageSecurityModeNone),
		opcua.AuthAnonymous(),
		opcua.AutoReconnect(false),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect %s: %v", ch.cfg.endpoint(), err)
	}
	t.Cleanup(func() { _ = client.Close(ctx) })

	read := func() *ua.DataValue {
		resp, err := client.Read(ctx, &ua.ReadRequest{
			NodesToRead: []*ua.ReadValueID{{NodeID: nodeID, AttributeID: ua.AttributeIDValue}},
		})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("read results = %d, want 1", len(resp.Results))
		}
		return resp.Results[0]
	}

	// 客户端必须能从标准 Objects 一路浏览到点位（UaExpert 的地址空间树）。
	browse := func(parent *ua.NodeID) []*ua.ReferenceDescription {
		refs, err := opcua.NewNode(parent, client).References(
			ctx, id.HierarchicalReferences, ua.BrowseDirectionForward, ua.NodeClassAll, true)
		if err != nil {
			t.Fatalf("browse %s: %v", parent, err)
		}
		return refs
	}
	find := func(refs []*ua.ReferenceDescription, name string) *ua.ReferenceDescription {
		for _, ref := range refs {
			if ref.BrowseName != nil && ref.BrowseName.Name == name {
				return ref
			}
		}
		return nil
	}

	rootRef := find(browse(ua.NewNumericNodeID(0, id.ObjectsFolder)), "iot-gateway")
	if rootRef == nil {
		t.Fatal("root object 'iot-gateway' is not browsable under Objects")
	}
	devRef := find(browse(rootRef.NodeID.NodeID), "PLC-1")
	if devRef == nil {
		t.Fatal("device 'PLC-1' is not browsable under the root object")
	}
	pointRef := find(browse(devRef.NodeID.NodeID), "Temperature")
	if pointRef == nil {
		t.Fatal("point 'Temperature' is not browsable under the device object")
	}
	if got := pointRef.NodeID.NodeID.String(); got != nodeID.String() {
		t.Errorf("browsed point NodeID = %s, want %s", got, nodeID)
	}

	// UaExpert 会查询这两个可选属性；缺失时日志会报 EU Information / Value Precision。
	pointChildren := browse(pointRef.NodeID.NodeID)
	for _, name := range []string{"EngineeringUnits", "ValuePrecision"} {
		if find(pointChildren, name) == nil {
			t.Errorf("point property %q is not browsable", name)
		}
	}

	// DataType 属性按 Part 3 §5.6.2 是 NodeId 类型。gopcua 默认写 ExpandedNodeId，
	// UaExpert 解码失败后当成 i=0，再去读 i=0 的 NodeClass/BrowseName/DisplayName，
	// 日志里就是 "NodeClass of i=0 has bad statuscode: BadNodeIdUnknown"。
	pointRead := opcua.NewNode(nodeID, client)
	dataTypeVariant, err := pointRead.Attribute(ctx, ua.AttributeIDDataType)
	if err != nil {
		t.Fatalf("read DataType: %v", err)
	}
	dataTypeID, ok := dataTypeVariant.Value().(*ua.NodeID)
	if !ok {
		t.Fatalf("DataType variant = %T, want *ua.NodeID", dataTypeVariant.Value())
	}
	if dataTypeID.Namespace() != 0 || dataTypeID.IntID() != id.Double {
		t.Errorf("DataType = %s, want ns=0;i=%d (Double)", dataTypeID, id.Double)
	}
	// UaExpert 拿到 DataType 后还会解析这个类型节点的三个基本属性。
	typeNode := opcua.NewNode(dataTypeID, client)
	if nc, err := typeNode.NodeClass(ctx); err != nil || nc != ua.NodeClassDataType {
		t.Errorf("DataType node NodeClass = %v, %v; want DataType", nc, err)
	}
	if bn, err := typeNode.BrowseName(ctx); err != nil || bn.Name == "" {
		t.Errorf("DataType node BrowseName = %v, %v; want non-empty", bn, err)
	}

	// 标量点位的 ValueRank = -1；缺失时报 ValueRank BadAttributeIdInvalid。
	rankVariant, err := pointRead.Attribute(ctx, ua.AttributeIDValueRank)
	if err != nil {
		t.Fatalf("read ValueRank: %v", err)
	}
	if rank, ok := rankVariant.Value().(int32); !ok || rank != -1 {
		t.Errorf("ValueRank = %#v, want int32(-1)", rankVariant.Value())
	}

	// TranslateBrowsePathsToNodeIds：UaExpert 拖入点位时用它算 NODE PATH。
	var translateResp *ua.TranslateBrowsePathsToNodeIDsResponse
	err = client.Send(ctx, &ua.TranslateBrowsePathsToNodeIDsRequest{
		BrowsePaths: []*ua.BrowsePath{{
			StartingNode: ua.NewNumericNodeID(0, id.ObjectsFolder),
			RelativePath: &ua.RelativePath{Elements: []*ua.RelativePathElement{
				{ReferenceTypeID: ua.NewNumericNodeID(0, id.HierarchicalReferences), IncludeSubtypes: true,
					TargetName: &ua.QualifiedName{Name: "iot-gateway"}},
				{ReferenceTypeID: ua.NewNumericNodeID(0, id.HierarchicalReferences), IncludeSubtypes: true,
					TargetName: &ua.QualifiedName{Name: "PLC-1"}},
				{ReferenceTypeID: ua.NewNumericNodeID(0, id.HierarchicalReferences), IncludeSubtypes: true,
					TargetName: &ua.QualifiedName{Name: "Temperature"}},
			}},
		}},
	}, func(v ua.Response) error {
		var ok bool
		translateResp, ok = v.(*ua.TranslateBrowsePathsToNodeIDsResponse)
		if !ok {
			return fmt.Errorf("unexpected response %T", v)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("TranslateBrowsePathsToNodeIDs: %v", err)
	}
	if len(translateResp.Results) != 1 {
		t.Fatalf("translate results = %d, want 1", len(translateResp.Results))
	}
	res := translateResp.Results[0]
	if res.StatusCode != ua.StatusGood || len(res.Targets) != 1 {
		t.Fatalf("translate result = %v/%d targets, want Good/1", res.StatusCode, len(res.Targets))
	}
	if got := res.Targets[0].TargetID.NodeID.String(); got != nodeID.String() {
		t.Errorf("translated NodeID = %s, want %s", got, nodeID)
	}

	// 还没收到采集数据：节点必须存在，状态是 WaitingForInitialData。
	if dv := read(); dv.Status != ua.StatusBadWaitingForInitialData {
		t.Fatalf("initial status = %v, want BadWaitingForInitialData", dv.Status)
	}

	ch.Enqueue(push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 12:00:00.000",
		Records: []collector.CollectedRecord{{
			DeviceID: "d1", DeviceAddressID: "a1", Value: "21.5",
			Kind: "float", Quality: driver.QualityGood,
		}},
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		dv := read()
		if dv.Status == ua.StatusGood {
			if got := dv.Value.Value(); got != float64(21.5) {
				t.Fatalf("value = %#v, want 21.5", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("value did not arrive, last status = %v", dv.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 点位只读：客户端写入必须被拒绝，且不能破坏后续采集更新。
	wresp, err := client.Write(ctx, &ua.WriteRequest{NodesToWrite: []*ua.WriteValue{{
		NodeID: nodeID, AttributeID: ua.AttributeIDValue,
		Value: &ua.DataValue{EncodingMask: ua.DataValueValue, Value: ua.MustVariant(float64(99))},
	}}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wresp.Results) != 1 || wresp.Results[0] != ua.StatusBadUserAccessDenied {
		t.Fatalf("write results = %v, want BadUserAccessDenied", wresp.Results)
	}
	if got := read().Value.Value(); got != float64(21.5) {
		t.Fatalf("value after rejected write = %#v, want 21.5", got)
	}

	// 通信故障：状态变坏，但仍保留上一个可用值。
	ch.Enqueue(push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 12:00:01.000",
		Records: []collector.CollectedRecord{{
			DeviceID: "d1", DeviceAddressID: "a1", Value: "",
			Kind: "float", Quality: driver.QualityBadComms,
		}},
	})
	deadline = time.Now().Add(5 * time.Second)
	for {
		dv := read()
		if dv.Status == ua.StatusBadCommunicationError {
			if got := dv.Value.Value(); got != float64(21.5) {
				t.Errorf("value after bad quality = %#v, want previous 21.5", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bad quality did not arrive, last status = %v", dv.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	status := ch.Snapshot()
	if status.PublishCount == 0 || !status.Running || !status.Connected {
		t.Errorf("snapshot = %+v, want running/connected with publishes", status)
	}

	// 停止后端口必须释放，否则重建/重启会一直撞 EADDRINUSE。
	_ = client.Close(ctx)
	ch.Stop()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port not released after Stop: %v", err)
	}
	_ = ln.Close()
}
