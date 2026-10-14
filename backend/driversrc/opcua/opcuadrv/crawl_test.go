// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"

	"iot-gateway/driver"
)

// ==================== 可控地址空间的桩服务器 ====================
//
// TestScanPointsTree 走的是真服务器（真 Browse、真 Read），覆盖的是**正常路径**。
// 本文件补的是真服务器上造不出来的那些情形：分页续页、同父重名、触顶截断、
// 请求数触顶、单个节点读不回来。用桩（fakeClient）而不是假服务器，是因为这些
// 情形要么需要服务器行为异常、要么需要构造上千个节点。
//
// 桩的形态刻意与真服务器同构：Browse 按 NodeID 查表、Read 按属性返回，
// 续页用一次 BrowseNext 收干净 —— 与 client.go 的 BrowseBatch 对齐。

type stubNode struct {
	refs []*ua.ReferenceDescription
	// dt 该节点 DataType 属性的值；nil 表示读回来不是 OK（模拟读不到属性的节点）。
	dt *ua.NodeID
}

type treeStub struct {
	nodes map[string]*stubNode
	// pageSize >0 时每个 Browse 只回这么多条引用，逼出续页路径。
	pageSize int

	browses     int
	nodesAsked  int
	reads       int
	readValues  int // Read 请求里一共问了多少个 ReadValueID
	nextBrowses int
}

func newTreeStub() *treeStub {
	return &treeStub{nodes: map[string]*stubNode{}}
}

// set 登记一个节点；refs 为该节点可被下行到的子节点。
func (s *treeStub) set(nid *ua.NodeID, n *stubNode) { s.nodes[nid.String()] = n }

// folder 登记一个只用于下行的对象节点。
func (s *treeStub) folder(nid *ua.NodeID, children ...*ua.ReferenceDescription) {
	s.set(nid, &stubNode{refs: children})
}

// variable 登记一个变量节点（返回它的 NodeID，便于挂到父节点上）。
func (s *treeStub) variable(nid *ua.NodeID, dt *ua.NodeID) *ua.NodeID {
	s.set(nid, &stubNode{dt: dt})
	return nid
}

// objects 登记标准的 Objects 文件夹（起点）。
func (s *treeStub) objects(children ...*ua.ReferenceDescription) {
	s.folder(ua.NewNumericNodeID(0, id.ObjectsFolder), children...)
}

// ref 造一条下行引用。
//
// NodeID 走 `ua.NewExpandedNodeID(nid, ...)` 而**不是**按 nid.IntID() 重造一个
// 数字 NodeID：后者会把字符串标识符整个丢掉（换成 ns=N;i=0），于是"标识符里带 /"
// 这一类用例根本走不到要测的那条分支上。库自己的 Node.AddRef 也是这么写的。
func ref(nid *ua.NodeID, class ua.NodeClass, browseName string, nsIdx uint16) *ua.ReferenceDescription {
	return &ua.ReferenceDescription{
		ReferenceTypeID: ua.NewNumericNodeID(0, id.Organizes),
		IsForward:       true,
		NodeID:          ua.NewExpandedNodeID(nid, "", 0),
		BrowseName:      &ua.QualifiedName{NamespaceIndex: nsIdx, Name: browseName},
		DisplayName:     &ua.LocalizedText{Text: browseName},
		NodeClass:       class,
	}
}

// varRef 是 ref 在"变量"上的常用情形。
func varRef(nid *ua.NodeID, browseName string, nsIdx uint16) *ua.ReferenceDescription {
	return ref(nid, ua.NodeClassVariable, browseName, nsIdx)
}

// objRef 是 ref 在"对象"上的常用情形。
func objRef(nid *ua.NodeID, browseName string, nsIdx uint16) *ua.ReferenceDescription {
	return ref(nid, ua.NodeClassObject, browseName, nsIdx)
}

// client 组装一个按本桩应答的 fakeClient。
func (s *treeStub) client() *fakeClient {
	return &fakeClient{browseFunc: s.browse, nextFunc: s.browseNext, readFunc: s.read}
}

// page 从 offset 起切一页引用；续页句柄是 "节点键|下一个偏移"。
func (s *treeStub) page(key string, refs []*ua.ReferenceDescription, offset int) ([]*ua.ReferenceDescription, []byte) {
	if s.pageSize <= 0 || offset+len(refs) <= s.pageSize {
		if offset >= len(refs) {
			return nil, nil
		}
		return refs[offset:], nil
	}
	end := offset + s.pageSize
	if end >= len(refs) {
		return refs[offset:], nil
	}
	return refs[offset:end], []byte(key + "|" + strconv.Itoa(end))
}

func (s *treeStub) browse(_ context.Context, req *ua.BrowseRequest) (*ua.BrowseResponse, error) {
	s.browses++
	s.nodesAsked += len(req.NodesToBrowse)
	results := make([]*ua.BrowseResult, len(req.NodesToBrowse))
	for i, bd := range req.NodesToBrowse {
		n := s.nodes[bd.NodeID.String()]
		if n == nil {
			results[i] = &ua.BrowseResult{StatusCode: ua.StatusBadNodeIDUnknown}
			continue
		}
		refs, cp := s.page(bd.NodeID.String(), n.refs, 0)
		results[i] = &ua.BrowseResult{StatusCode: ua.StatusOK, References: refs, ContinuationPoint: cp}
	}
	return &ua.BrowseResponse{Results: results}, nil
}

func (s *treeStub) browseNext(_ context.Context, req *ua.BrowseNextRequest) (*ua.BrowseNextResponse, error) {
	s.nextBrowses++
	results := make([]*ua.BrowseResult, len(req.ContinuationPoints))
	for i, cp := range req.ContinuationPoints {
		key, off, ok := parseFakeCP(cp)
		n := s.nodes[key]
		if !ok || n == nil {
			results[i] = &ua.BrowseResult{StatusCode: ua.StatusBadNoMatch}
			continue
		}
		refs, next := s.page(key, n.refs, off)
		results[i] = &ua.BrowseResult{StatusCode: ua.StatusOK, References: refs, ContinuationPoint: next}
	}
	return &ua.BrowseNextResponse{Results: results}, nil
}

func parseFakeCP(cp []byte) (key string, offset int, ok bool) {
	parts := strings.SplitN(string(cp), "|", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	off, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, false
	}
	return parts[0], off, true
}

func (s *treeStub) read(_ context.Context, req *ua.ReadRequest) (*ua.ReadResponse, error) {
	s.reads++
	s.readValues += len(req.NodesToRead)
	results := make([]*ua.DataValue, len(req.NodesToRead))
	for i, rv := range req.NodesToRead {
		n := s.nodes[rv.NodeID.String()]
		if n == nil {
			results[i] = &ua.DataValue{Status: ua.StatusBadNodeIDUnknown}
			continue
		}
		// 起始节点的存在性确认读的是 NodeClass；采集路径的其它读这里不涉及。
		if rv.AttributeID == ua.AttributeIDNodeClass {
			results[i] = &ua.DataValue{Status: ua.StatusOK, Value: ua.MustVariant(uint32(ua.NodeClassObject))}
			continue
		}
		if rv.AttributeID != ua.AttributeIDDataType || n.dt == nil {
			results[i] = &ua.DataValue{Status: ua.StatusBadAttributeIDInvalid}
			continue
		}
		results[i] = &ua.DataValue{Status: ua.StatusOK, Value: ua.MustVariant(n.dt)}
	}
	return &ua.ReadResponse{Results: results}, nil
}

// scanStub 用桩跑一次扫描。
func scanStub(t *testing.T, stub *treeStub, opts json.RawMessage,
	budget *requestBudget) (*driver.PointScanResult, error) {
	t.Helper()
	d := newTestDriver(stub.client())
	d.connect(t)
	defer func() { _ = d.Close() }()
	return scanPoints(&tempSession{drv: d}, opts, time.Now().Add(30*time.Second), budget)
}

// defaultBudget 与生产同值，供大多数用例使用。
func defaultBudget() *requestBudget {
	return &requestBudget{left: pointScanMaxRequests}
}

// ==================== 用例 ====================

// TestCrawlReadsDataTypeInBatches 一块变量的 DataType 必须**批量**读，而不是一点位一请求。
//
// 这是"一次扫描能扫完多少个点位"的第二个杠杆（第一个是批量 Browse）。
// 退化成一点位一请求不会报任何错，只是扫描会在预算耗尽时停在一半 ——
// 症状是"清单莫名其妙断了"，用户完全看不出为什么。
func TestCrawlReadsDataTypeInBatches(t *testing.T) {
	stub := newTreeStub()
	dt := ua.NewNumericNodeID(0, id.Double)

	const n = 250 // 跨过 MaxBatch（默认 100）的多个子批
	refs := make([]*ua.ReferenceDescription, 0, n)
	for i := 0; i < n; i++ {
		nid := ua.NewNumericNodeID(1, 1000+uint32(i))
		stub.variable(nid, dt)
		refs = append(refs, varRef(nid, fmt.Sprintf("P%03d", i), 0))
	}
	stub.objects(refs...)

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != n {
		t.Fatalf("列出 %d 个点位, 期望 %d", len(res.Points), n)
	}
	// Browse 一次问一整块（250 个变量 + 1 个 Objects = 一次请求），
	// Read 按 MaxBatch 切：ceil(250/100) = 3 次。
	if stub.reads != 3 {
		t.Errorf("DataType 读了 %d 次, 期望 3 次（250 个变量按 MaxBatch=100 切子批）—— "+
			"一点位一请求会让扫描在预算耗尽时停在一半", stub.reads)
	}
	if stub.browses != 1 {
		t.Errorf("Browse 发了 %d 次, 期望 1 次（一次问一整块）", stub.browses)
	}
}

// TestCrawlPaginatesBrowse 续页必须收干净。
//
// 少收一页就是**静默少列一批点位**：服务器对"这个文件夹很大"的唯一表达方式
// 就是分页，而分页丢掉之后清单照样渲染得很正常。
func TestCrawlPaginatesBrowse(t *testing.T) {
	stub := newTreeStub()
	stub.pageSize = 2
	dt := ua.NewNumericNodeID(0, id.UInt16)

	refs := make([]*ua.ReferenceDescription, 0, 5)
	for i := 0; i < 5; i++ {
		nid := ua.NewNumericNodeID(1, 2000+uint32(i))
		stub.variable(nid, dt)
		refs = append(refs, varRef(nid, fmt.Sprintf("T%d", i), 0))
	}
	stub.objects(refs...)

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 5 {
		t.Errorf("列出 %d 个点位, 期望 5 —— 续页没收干净（BrowseNext 发了 %d 次）",
			len(res.Points), stub.nextBrowses)
	}
	if stub.nextBrowses == 0 {
		t.Error("分页的服务器应当触发 BrowseNext")
	}
}

// TestCrawlUsesBrowsePathWhenNodeIDUnusable 标识符里带 "/" 的字符串 NodeID 退到浏览路径。
//
// 这是 addressOf 两条分支里**唯一会被实际走到的**场景：parseAddress 把任何含 "/"
// 的串当浏览路径，于是那条 NodeID 地址会被解析成一条别的路径。退到下行时累积出来
// 的浏览路径是安全的（它按构造可达，且渲染完还要再解析回来逐段比对）。
func TestCrawlUsesBrowsePathWhenNodeIDUnusable(t *testing.T) {
	stub := newTreeStub()
	// BrowseName 与标识符无关，且本身不含 "/"，所以路径是可渲染的。
	nid := ua.NewStringNodeID(2, "Room1/Temp")
	stub.variable(nid, ua.NewNumericNodeID(0, id.Double))
	stub.objects(varRef(nid, "Temp", 0))

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 1 {
		t.Fatalf("列出 %d 个点位, 期望 1（含 / 的字符串 NodeID 应当退到浏览路径）", len(res.Points))
	}
	got := res.Points[0].Name
	if got != "Root/Objects/Temp" {
		t.Errorf("地址 = %q, 期望退到浏览路径 Root/Objects/Temp", got)
	}
	// 退回去的地址仍然必须能往返解析（那条唯一的不变量）。
	addr, err := parseAddress(got)
	if err != nil || !addr.isPath {
		t.Errorf("退回去的浏览路径解析失败: %v, isPath=%v", err, addr != nil && addr.isPath)
	}
}

// TestCrawlSkipsDuplicateSiblingPaths 同父下的重名兄弟不能再产出浏览路径。
//
// 两个重名兄弟会渲染出**同一条**地址串：撞 (device_id, name) 唯一键，
// 且 TranslateBrowsePathsToNodeIDs 只取第一个目标 —— 两行都指向同一个节点，
// 其中一行永远采着另一个点的值。这里的两个节点都只能靠路径寻址（标识符里带 "/"），
// 所以它们应当**双双**进"未列出"，而不是产出一对重复地址。
func TestCrawlSkipsDuplicateSiblingPaths(t *testing.T) {
	stub := newTreeStub()
	left := ua.NewStringNodeID(2, "Room1/A")
	right := ua.NewStringNodeID(2, "Room1/B")
	stub.variable(left, ua.NewNumericNodeID(0, id.Double))
	stub.variable(right, ua.NewNumericNodeID(0, id.Double))
	// 同一个 BrowseName ⇒ 从 Root 下来的路径不唯一。
	stub.objects(varRef(left, "Same", 0), varRef(right, "Same", 0))

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 0 {
		names := make([]string, 0, len(res.Points))
		for _, p := range res.Points {
			names = append(names, p.Name)
		}
		t.Errorf("重名兄弟不该产出地址，却给出了 %v", names)
	}
	if res.Unreadable != 2 || res.Scanned != 2 {
		t.Errorf("scanned=%d unreadable=%d, 期望 2/2", res.Scanned, res.Unreadable)
	}

	// 对照组：名字不同时两个都能安全寻址。没有这一格，上面那条可能因为
	// 别的原因（比如路径根本没渲染出来）而假通过。
	stub2 := newTreeStub()
	stub2.variable(left, ua.NewNumericNodeID(0, id.Double))
	stub2.variable(right, ua.NewNumericNodeID(0, id.Double))
	stub2.objects(varRef(left, "Left", 0), varRef(right, "Right", 0))
	res2, err := scanStub(t, stub2, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res2.Points) != 2 {
		t.Errorf("名字不同的兄弟应当都能列出，却只给出了 %d 个", len(res2.Points))
	}
}

// TestCrawlSkipsUnreadableDataType 单个节点的 DataType 读不回来时只跳过它，
// 不能连坐同批的其它点位。
func TestCrawlSkipsUnreadableDataType(t *testing.T) {
	stub := newTreeStub()
	good := ua.NewNumericNodeID(1, 3001)
	bad := ua.NewNumericNodeID(1, 3002)
	unmappable := ua.NewNumericNodeID(1, 3003)

	stub.variable(good, ua.NewNumericNodeID(0, id.Double))
	stub.set(bad, &stubNode{dt: nil})                               // DataType 读回非 OK
	stub.variable(unmappable, ua.NewNumericNodeID(0, id.Structure)) // 结构体：没有通用类型
	stub.objects(varRef(good, "Good", 0), varRef(bad, "Bad", 0), varRef(unmappable, "Struct", 0))

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 1 || res.Points[0].Name != good.String() {
		t.Errorf("只有 Good 应当被列出，得到 %+v", res.Points)
	}
	if res.Readable != 1 || res.Unreadable != 2 || res.Scanned != 3 {
		t.Errorf("scanned=%d readable=%d unreadable=%d, 期望 3/1/2",
			res.Scanned, res.Readable, res.Unreadable)
	}
}

// TestCrawlTruncatesAtMaxPoints 达到单次上限时置 Truncated，且计数不变量不破。
func TestCrawlTruncatesAtMaxPoints(t *testing.T) {
	stub := newTreeStub()
	dt := ua.NewNumericNodeID(0, id.Double)

	total := pointScanMaxPoints + 50
	refs := make([]*ua.ReferenceDescription, 0, total)
	for i := 0; i < total; i++ {
		nid := ua.NewNumericNodeID(1, 10000+uint32(i))
		stub.variable(nid, dt)
		refs = append(refs, varRef(nid, fmt.Sprintf("P%05d", i), 0))
	}
	stub.objects(refs...)

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if !res.Truncated {
		t.Error("超过 MaxPoints 时应当置 Truncated")
	}
	if res.Incomplete {
		t.Error("触顶截断不是「未扫完」，两个标志必须分开")
	}
	if len(res.Points) != pointScanMaxPoints {
		t.Errorf("列出 %d 个点位, 期望正好 %d", len(res.Points), pointScanMaxPoints)
	}
	// **先判截断再计数**：触顶之后那些根本没处理过的点位不该进 Scanned。
	// 反过来的实现会让这条不变量当场破掉。
	if res.Readable+res.Unreadable != res.Scanned {
		t.Errorf("readable(%d)+unreadable(%d) != scanned(%d)",
			res.Readable, res.Unreadable, res.Scanned)
	}
	if res.Scanned != pointScanMaxPoints {
		t.Errorf("scanned = %d, 期望 %d（触顶之后没试过的点位不该计入）",
			res.Scanned, pointScanMaxPoints)
	}
}

// TestCrawlIncompleteOnBudgetExhausted 请求数触顶 ⇒ 带部分结果 + Incomplete，**不报错**。
//
// 契约（specs/point-discovery.md §7 第 1 条）：预算不够时的正确做法是带上已完成
// 的部分正常返回并把 Incomplete 置位。报 error 会让用户丢掉那几百个已经问出来的
// 点位；而返回一份空清单加一句"未扫完"又会被读成"这台设备没有点位"。
func TestCrawlIncompleteOnBudgetExhausted(t *testing.T) {
	stub := newTreeStub()
	dt := ua.NewNumericNodeID(0, id.Double)

	// 一层里放多于一整块（64）的变量，确保第一块就能问出点位。
	refs := make([]*ua.ReferenceDescription, 0, 200)
	for i := 0; i < 200; i++ {
		nid := ua.NewNumericNodeID(1, 20000+uint32(i))
		stub.variable(nid, dt)
		refs = append(refs, varRef(nid, fmt.Sprintf("P%03d", i), 0))
	}
	stub.objects(refs...)

	// 预算只够：Browse 一整块 1 次 + 第一个 DataType 子批 1 次。
	// 第二个子批（100 个变量）开始之前就没额度了 —— 它之后那些变量**根本没问过**，
	// 所以既不该进 Points，也不该进 Scanned。
	res, err := scanStub(t, stub, nil, &requestBudget{left: 2})
	if err != nil {
		t.Fatalf("预算耗尽不该报错: %v", err)
	}
	if !res.Incomplete {
		t.Error("预算耗尽时应当置 Incomplete")
	}
	if res.Truncated {
		t.Error("预算耗尽不是「结果到上限」，两个标志必须分开")
	}
	if len(res.Points) == 0 {
		t.Error("已经问出来的点位必须交回给用户，不能因为没扫完就丢掉")
	}
	if res.Readable+res.Unreadable != res.Scanned {
		t.Errorf("readable(%d)+unreadable(%d) != scanned(%d)",
			res.Readable, res.Unreadable, res.Scanned)
	}
	// **计数必须切在"问过"的边界上**：第一个子批（MaxBatch=100）读到了，第二个
	// 没读。没读的那 100 个既不该进 Points，也不该被算成"未列出" —— 把它们算成
	// 未列出会让用户以为"这些点位的类型映射不了"，而事实是"扫描没跑到那里"。
	if res.Scanned != 100 || res.Readable != 100 || res.Unreadable != 0 {
		t.Errorf("scanned=%d readable=%d unreadable=%d, 期望 100/100/0"+
			"（没问过的那一批不该被算成「未列出」）",
			res.Scanned, res.Readable, res.Unreadable)
	}
}

// TestCrawlRespectsMaxDepth 深度限制只挡往下的层级，不挡当前层。
func TestCrawlRespectsMaxDepth(t *testing.T) {
	stub := newTreeStub()
	dt := ua.NewNumericNodeID(0, id.Double)

	// Objects → L1 → L2 → L3，每层挂一个变量。
	l1 := ua.NewNumericNodeID(1, 4001)
	l2 := ua.NewNumericNodeID(1, 4002)
	l3 := ua.NewNumericNodeID(1, 4003)
	top := ua.NewNumericNodeID(1, 4010)
	mid := ua.NewNumericNodeID(1, 4011)
	deep := ua.NewNumericNodeID(1, 4012)
	for _, n := range []*ua.NodeID{top, mid, deep} {
		stub.variable(n, dt)
	}
	stub.folder(l3, varRef(deep, "Deep", 0))
	stub.folder(l2, objRef(l3, "L3", 0), varRef(mid, "Mid", 0))
	stub.folder(l1, objRef(l2, "L2", 0), varRef(top, "Top", 0))
	stub.objects(objRef(l1, "L1", 0))

	// 深度 1：只下行到 L1 的子节点（L2 对象不展开），因此只应列出 Top。
	opts := json.RawMessage(`{"maxDepth":1}`)
	res, err := scanStub(t, stub, opts, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 1 || res.Points[0].Name != top.String() {
		names := make([]string, 0, len(res.Points))
		for _, p := range res.Points {
			names = append(names, p.Name)
		}
		t.Errorf("maxDepth=1 时列出 %v, 期望只有最上面那一个", names)
	}

	// 深度 0 = 不限：三个都应当列出。
	res, err = scanStub(t, stub, json.RawMessage(`{"maxDepth":0}`), defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 3 {
		t.Errorf("maxDepth=0 应当不限深度，却只列出 %d 个", len(res.Points))
	}
}

// TestCrawlSkipsPerNodeBrowseFailure 单个节点 Browse 不回来时跳过这一支，
// 不当成整次扫描失败。
//
// 一台只有某个子分支异常的服务器（权限不足、节点被删、厂商实现不全）
// 不该被判成一无所获 —— 那会让用户彻底放弃这个功能。
func TestCrawlSkipsPerNodeBrowseFailure(t *testing.T) {
	stub := newTreeStub()
	dt := ua.NewNumericNodeID(0, id.Double)

	broken := ua.NewNumericNodeID(1, 5001) // 没登记 ⇒ Browse 回 BadNodeIDUnknown
	ok := ua.NewNumericNodeID(1, 5002)
	stub.variable(ok, dt)
	stub.objects(objRef(broken, "Broken", 0), varRef(ok, "Ok", 0))

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("单个节点读不了不该让整次扫描失败: %v", err)
	}
	if len(res.Points) != 1 || res.Points[0].Name != ok.String() {
		t.Errorf("应当只列出 Ok，得到 %+v", res.Points)
	}
}

// TestCrawlSkipsNonVariableNodes 方法、视图等非对象非变量节点不计数也不列出。
func TestCrawlSkipsNonVariableNodes(t *testing.T) {
	stub := newTreeStub()
	dt := ua.NewNumericNodeID(0, id.Double)

	method := ua.NewNumericNodeID(1, 6001)
	stub.set(method, &stubNode{dt: dt}) // 即使它有 DataType 也不该被当成点位
	v := ua.NewNumericNodeID(1, 6002)
	stub.variable(v, dt)
	stub.objects(ref(method, ua.NodeClassMethod, "Start", 0), varRef(v, "Run", 0))

	res, err := scanStub(t, stub, nil, defaultBudget())
	if err != nil {
		t.Fatalf("scanPoints: %v", err)
	}
	if len(res.Points) != 1 || res.Points[0].Name != v.String() {
		t.Errorf("方法节点不该被列为点位，得到 %+v", res.Points)
	}
	if res.Scanned != 1 {
		t.Errorf("scanned = %d, 期望 1（只统计变量节点）", res.Scanned)
	}
}
