// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"

	"iot-gateway/driver"
)

// ==================== 点位扫描（原生枚举） ====================
//
// 本文件实现 OPC.UA 的点位扫描。**形态与 Modbus/S7/FINS 完全不同**，值得先把
// 它是什么说清楚：
//
//	Modbus/S7/FINS  扫一段**地址范围**，回答"这段里哪些地址读得到"。
//	                产出的是一份候选清单 —— 设备不会告诉你它有哪些点位。
//	本文件           用 OPC UA 的 Browse 服务**原生枚举**服务器地址空间里的节点，
//	                回答"这台服务器上到底有哪些点位"。产出的是**服务器自报的
//	                节点**，每个还带着它自报的类型。
//
// 这正是 specs/point-discovery.md §1 点名的少数例外之一（与 Rockwell 的
// CIP Symbol Object、ABB 的 RWS 信号表、DLT645 的数据标识字典同类），也是
// driversdk 的 FindPointsRequest.Session 注释里预留的那个位置。
//
// **但 OPC UA 与它们有一处关键差别：枚举必须读设备**（要连上去 Browse），
// 所以照样要走 Session —— 这一点与 Rockwell 同形，与"只用 Options 就能列出
// 点位"的留白不同。
//
// ---------------------------------------------------------------------------
// 三条必须知道的实现事实
// ---------------------------------------------------------------------------
//
//  1. **不读采样值。** 与 Rockwell 的选择相同：结果级的 AssumedCommonDataType
//     留空（这是一条**跨端约定**，前端据此隐藏「显示 0 值」开关并换用
//     「列出/未列出」的措辞），每个点位的类型由 DiscoveredPoint.CommonDataType
//     逐点给出。类型来自 DataType 属性，与值无关，所以不读值不影响类型。
//
//  2. **每块一次 Session.Do，块间让锁。** 一次 Do 覆盖整段扫描 = 把采集卡死
//     几十秒（与 pingSessionBudget 同一条理由，见 specs/point-discovery.md §6）。
//     本协议走的是**临时实例**路径（未实现 driver.ConnectionMatcher，
//     selectSession 必然返回 (nil,nil)），那条路上 Do 加锁与否其实无关紧要 ——
//     但主循环仍按"一块一次 Do"来写：将来若给本驱动补上 MatchConnection，
//     这里不必再改一次。
//
//  3. **拿得到部分结果，所以超预算不报错。** 遍历是按层推进的，停在哪里、
//     手上就有多少 —— 与 Rockwell 那种"整只对象一次给完"的操作不同。
//     因此本 finder 严格遵守契约：超预算**带上已完成的部分正常返回**并把
//     Incomplete 置位，不返回 error。

// 点位扫描的规模与预算。
const (
	// pointScanMaxPoints 单次扫描最多返回多少个点位。
	//
	// 上限存在的意义不是性能而是**事故半径**：应答帧要走 pluginwire 的写锁，
	// 而那份锁是该插件进程上所有协议共用的 —— 一份几百 KB 的应答会把它们一起
	// 头阻塞。2000 个节点（每个一行、几十字节）是几十 KB 的量级，一次能过管道。
	//
	// 与 PointDiscovery().MaxPoints 必须一致 —— 后者只是把它报给网关与前端，
	// 由 driversdk.Meta.Validate 校验落在 [1,20000] 内。
	pointScanMaxPoints = 2000

	// pointScanBudgetMs 本协议声明的整段扫描预算（毫秒）。
	//
	// 报一个比网关上限大的值：真正生效的永远是
	// min(本值, pluginhost 的 pointScanGatewayBudgetCapMs = 7000ms)，因为这条
	// 链路是**同步 HTTP**，超时会拖住网关停机（见 specs/point-discovery.md §4）。
	// 声明成 20000 只是如实表达"枚举上万节点的服务器本来就需要这么久"，
	// 而不是承诺能让它跑完 —— 跑不完时按上面第 3 条带部分结果返回。
	pointScanBudgetMs = 20000

	// pointScanMaxRequests 单次扫描的请求数上界。
	//
	// 只靠 deadline 挡不住两件相反的事：本地服务器上几百次请求可能只要几百
	// 毫秒（于是照跑，把一个上万节点的地址空间整个搬回来），而慢链路上同样
	// 几百次又跑不完（于是停在一半）。它是**上界而不是配额**，正常服务器远远
	// 用不到；触顶按"未扫完"处理。
	pointScanMaxRequests = 400

	// pointScanBrowseChunk 一个 BrowseRequest 覆盖多少个节点。
	//
	// 一次问一整层能把往返数除以块大小，但块太大又会让"让锁"的粒度变粗
	// （一块 = 一次 Do）。64 是个折中：一次请求的应答在几十 KB 量级，
	// 而 64 个节点的浏览在局域网里是毫秒级。
	pointScanBrowseChunk = 64

	// defaultPointScanMaxDepth 表单里"最大深度"的默认值。
	//
	// 现实里的标签树（Objects → 设备 → 组 → 点位）是三到四层，8 层足够宽裕；
	// 再深多半是服务器的元数据分支，而不是用户想要的点位。
	defaultPointScanMaxDepth = 8

	// pointScanCloseMargin 从预算里给调用方的收尾留的余量。
	//
	// finder 返回之后，宿主还要 drv.Close()（注销会话 + 关套接字）再把应答帧写
	// 出去（2000 个点位是几十 KB）。这些都不算在 finder 的预算里，但都算在 RPC
	// 超时里。req.Deadline 已经是"网关钳过的预算"，RPC 还额外有余量，
	// 所以这 100ms 是白拿的。
	pointScanCloseMargin = 100 * time.Millisecond
)

// pointScanForm 点位扫描的参数表单。
//
// 两项都**有合理的默认值**（留空即从 Objects 开始、8 层），所以缺项走默认、
// 不报错；只有**写坏了**（起始节点解析不了）才报错 —— 那说明用户确实填了东西，
// 悄悄忽略会让"我明明限制了范围，怎么还是全量扫了一遍"变成一个查不出的谜。
const pointScanForm = `{
  "rule": [
    {"type":"input","field":"startNode","title":"起始节点","$required":false,
     "info":"留空即从 Objects 文件夹开始（绝大多数服务器把业务节点挂在它下面）。大服务器上从一个设备的子树开始，能把扫描时间从几十秒压到几秒。可填 NodeID（如 ns=2;s=Device）或浏览路径（如 Root/Objects/2:Device —— 从 Root 写起，只写 Objects/... 会被剥掉那一段）"},
    {"type":"inputNumber","field":"maxDepth","title":"最大深度","$required":false,"value":8,
     "info":"默认 8。从起始节点往下最多走几层；0 表示不限（仍受单次上限与预算约束）"}
  ],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// 两种"提前收工"的信号，由主循环翻译成结果上的两个标志。
//
// 它们**必须分开**：Truncated 是"结果条数到上限"，Incomplete 是"范围还没扫到"，
// 而它们与"未列出"是三回事。合并成一句话会让用户得出错误的结论
// （specs/point-discovery.md §9 通篇在防的就是这一类误读）。
var (
	// errScanBudgetSpent 预算或请求数上界用尽。**不是错误**：
	// 调用方带上已扫到的部分正常返回并把 Incomplete 置位。
	errScanBudgetSpent = errors.New("opcua: 点位扫描预算已用尽")

	// errScanTruncated 结果条数达到 MaxPoints，后面的节点**根本没试**。
	errScanTruncated = errors.New("opcua: 点位扫描结果已达单次上限")
)

// SessionAccess 与 driversdk.SessionAccess 同形，但**刻意本地声明而不 import
// driversdk**：与 probe.go 的 Probe 收 target/options/budget 三个原始值
// （而不是 driversdk.ProbeRequest）是同一条取舍 —— 协议实现只依赖 driver 这一份
// 契约，不被整套插件开发套件绑住，本包因此也能脱离插件进程单独测试。
type SessionAccess interface {
	Do(fn func(drv driver.Driver) error) error
}

// requestBudget 本次扫描的请求数上界（见 pointScanMaxRequests）。
type requestBudget struct{ left int }

// spend 消耗一次请求额度，额度用尽返回 false。
func (b *requestBudget) spend() bool {
	if b.left <= 0 {
		return false
	}
	b.left--
	return true
}

// PointDiscovery 返回 OPC.UA 的点位扫描能力声明，供 main.go 填进 Meta.Protocols。
//
// Description 是用户理解这个功能性质的**主要来源**（常驻显示在弹窗顶部），
// 所以下面五条局限必须写在里面，一条都不能省 —— 尤其是"不读采样值"、
// "未列出 ≠ 读不到"与"类型名是 Kind 等价映射"，用户不会从界面上别的地方看出来。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "枚举 OPC UA 节点",
		Description: "用 OPC UA 的 Browse 服务**原生枚举**服务器地址空间里的节点 —— " +
			"这是少数能真正列出点位的协议之一：列出来的是**服务器自报的节点**，" +
			"不是「某段地址里读得到的那些」。五条局限请先看：" +
			"① **不读采样值**，只列节点与类型（多读一遍值等于把请求数翻倍）；" +
			"② 列出的是**变量**节点，文件夹只用来往下走、不列出；" +
			"服务器自己的 `Server` 诊断节点也会被列出来；" +
			"③ 自定义/枚举类型会沿继承关系上溯到 OPC UA 的基础类型，没有对应通用类型的" +
			"（结构体、Guid、NodeID、LocalizedText……）**会被跳过**，跳过数在「未列出」里可见 —— " +
			"「未列出」不等于「读不到」；" +
			"④ 地址优先写成 NodeID（如 `ns=2;s=Temperature`）；少数标识符里带 `/` 的字符串 " +
			"NodeID 写不成 NodeID（会被当成浏览路径），这类节点改用从 Root 起的浏览路径，" +
			"代价是**每次设备重连后的首次采集要多一次服务器解析**；" +
			"⑤ 预算（网关侧实际 7 秒）内没扫完时会**带着已扫到的部分正常返回**并在结果上方标注" +
			"「未扫完」，不会报错 —— 缩小起始节点或深度重扫即可拿全。" +
			"类型列按 **Kind 等价**映射：Int32→Long、UInt64→DWord、SByte→Char、ByteString→String，" +
			"与服务器类型不一致时会在「采样值」那一列标出 OPC UA 的基础类型名",
		FormJSON: json.RawMessage(pointScanForm),
		// "Long" 是 TypeMap 的键。它只是**兜底初值**：本 finder 产出的每一行都
		// 自带 CommonDataType，只有那一项为空时前端才会用到这个默认值。
		DefaultCommonDataType: driver.TypeLong,
		MaxBudgetMs:           pointScanBudgetMs,
		MaxPoints:             pointScanMaxPoints,
	}
}

// pointScanOptions 解析后的扫描选项。
type pointScanOptions struct {
	// start 起始节点；nil 表示 Objects 文件夹（Root/Objects）。
	start *opcuaAddress
	// maxDepth 从起始节点往下的最大层数；0 表示不限。
	maxDepth int
}

// pointScanOptionsRaw 扫描选项的原始形态。键名与 pointScanForm 的 field 一一对应。
type pointScanOptionsRaw struct {
	StartNode string `json:"startNode"`
	MaxDepth  rawInt `json:"maxDepth"`
}

// parsePointScanOptions 解析扫描选项。options 为空（长度 0）时全部走默认值。
//
// 起始节点**复用驱动的地址解析器**（parseAddress）：用户在这里填的就是
// device_address 里那一套写法，用同一个解析器才不会出现"扫描页认、地址页不认"
// 这种两边对不上的事。解析不了直接报错并把原因带上（用户填错了，值得原样说出来）。
func parsePointScanOptions(options json.RawMessage) (pointScanOptions, error) {
	opt := pointScanOptions{maxDepth: defaultPointScanMaxDepth}
	if len(options) == 0 {
		return opt, nil
	}

	var raw pointScanOptionsRaw
	if err := json.Unmarshal(options, &raw); err != nil {
		return opt, fmt.Errorf("解析扫描选项失败: %w", err)
	}
	if s := strings.TrimSpace(raw.StartNode); s != "" {
		addr, err := parseAddress(s)
		if err != nil {
			return opt, fmt.Errorf("起始节点无法解析（可填 NodeID 如 ns=2;s=Device，"+
				"或从 Root 写起的浏览路径如 Root/Objects/2:Device）: %w", err)
		}
		opt.start = addr
	}
	if raw.MaxDepth.set {
		if raw.MaxDepth.val < 0 {
			return opt, fmt.Errorf("最大深度不能是负数: %d", raw.MaxDepth.val)
		}
		opt.maxDepth = raw.MaxDepth.val
	}
	return opt, nil
}

// crawlNode 遍历队列里的一个节点。
type crawlNode struct {
	nid   *ua.NodeID
	path  browsePath
	depth int
	// label 展示用：从起始节点起逐级拼起来的显示名路径（前端标签列的初值）。
	label string
}

// child 计算一个子节点的遍历项。返回 nil 表示这个子节点根本没法用
// （拿不到 NodeID），调用方直接丢掉。
//
// **路径算不出来不算"没法用"**：那种节点仍然可以用 NodeID 寻址，所以照样返回，
// 只是 path.ok=false（它的子孙也就都不再有路径可用）。
func (n *crawlNode) child(ref *ua.ReferenceDescription) *crawlNode {
	if ref == nil {
		return nil
	}
	nid := ua.NewNodeIDFromExpandedNodeID(ref.NodeID)
	if nid == nil {
		return nil
	}

	display := nid.String()
	if ref.DisplayName != nil && strings.TrimSpace(ref.DisplayName.Text) != "" {
		display = strings.TrimSpace(ref.DisplayName.Text)
	} else if ref.BrowseName != nil && ref.BrowseName.Name != "" {
		display = ref.BrowseName.Name
	}

	c := &crawlNode{nid: nid, depth: n.depth + 1, label: joinLabel(n.label, display)}
	if !n.path.ok {
		return c
	}

	seg, ok := segmentText(ref.BrowseName)
	if !ok {
		return c
	}
	qns := make([]*ua.QualifiedName, 0, len(n.path.qns)+1)
	qns = append(qns, n.path.qns...)
	qns = append(qns, ref.BrowseName)
	text := n.path.text + "/" + seg
	if !pathRoundTrips(text, qns) {
		return c
	}
	c.path = browsePath{text: text, qns: qns, ok: true}
	return c
}

// addressOf 给一个变量节点挑一个**能往返解析**的地址，挑不出来返回 ""。
//
// 顺序不能反：NodeID 形式是**恒定的**（不依赖服务器解析、重连后零额外请求），
// 浏览路径每次重连后首次采集都要一次 TranslateBrowsePathsToNodeIds；
// 反过来只在 NodeID 形式确实用不了时才退到路径。
func addressOf(v *crawlNode) string {
	if s := v.nid.String(); nodeIDRoundTrips(s, v.nid) {
		return s
	}
	if v.path.ok && pathRoundTrips(v.path.text, v.path.qns) {
		return v.path.text
	}
	return ""
}

// ScanPoints 枚举一台已存在的 OPC UA 服务器上的节点。
//
// deadline 是网关给的**绝对**截止时刻（= 调用时刻 + 被钳过的预算），
// 本函数保证在它之前返回。返回的 error 与结果**可以同时非 nil**：链路级失败
// （传输错误）时带着已经扫到的部分一起返回 —— 网关侧两者都会透给用户，
// 丢掉一份已经跑了几秒的结果对用户没有任何好处。
func ScanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time) (*driver.PointScanResult, error) {
	return scanPoints(sess, options, deadline, &requestBudget{left: pointScanMaxRequests})
}

// scanPoints 与 ScanPoints 同体，只多一个**注入的请求预算**。
//
// 拆出这一层的唯一理由是让"请求数触顶 ⇒ 未扫完"这条分支能被测到：
// 真要用生产预算（400）撞到它，得先造几百个节点的假服务器，
// 而那样的测试会因为跑得慢、依赖时序而变成又一个 dlt645drv 式的偶发红。
func scanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time,
	budget *requestBudget) (*driver.PointScanResult, error) {
	opt, err := parsePointScanOptions(options)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, errors.New("opcua: 没有可用的采集会话，无法扫描")
	}

	res := &driver.PointScanResult{Points: make([]driver.DiscoveredPoint, 0, 256)}
	started := time.Now()
	finish := func() *driver.PointScanResult {
		res.ElapsedMs = int(time.Since(started).Milliseconds())
		return res
	}

	// 收尾余量（见 pointScanCloseMargin）。这里**不返回错误**：留给收尾的时间
	// 都不够，说明网关给的预算已经被别处花光了，如实标成"未扫完"即可。
	remaining := time.Until(deadline) - pointScanCloseMargin
	if remaining <= 0 {
		res.Incomplete = true
		return finish(), nil
	}
	deadline = time.Now().Add(remaining)

	resolver := newTypeResolver()

	// 起始节点单独解析一拍：它可能是一个浏览路径，要问服务器（见 resolveStart）。
	// 单独一拍而不是塞进第一块，是为了让主循环的形状保持"一块一次 Do"这一条。
	var start *crawlNode
	err = sess.Do(func(drv driver.Driver) error {
		c, _, cerr := liveClientOf(drv)
		if cerr != nil {
			return cerr
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		start, cerr = resolveStart(ctx, c, opt, budget)
		return cerr
	})
	if err != nil {
		// 起始节点都没解析出来（预算就用光了）：那不是链路故障，是"还没开始"。
		if errors.Is(err, errScanBudgetSpent) {
			res.Incomplete = true
			return finish(), nil
		}
		return finish(), err
	}

	frontier := []*crawlNode{start}

	for len(frontier) > 0 {
		// 收工判据都放在**块之前**：块中途才开始判的话，最后一块会跑过截止时刻。
		if time.Now().After(deadline) {
			res.Incomplete = true
			break
		}
		if len(res.Points) >= pointScanMaxPoints {
			res.Truncated = true
			break
		}

		chunkSize := pointScanBrowseChunk
		if len(frontier) < chunkSize {
			chunkSize = len(frontier)
		}
		chunk, rest := frontier[:chunkSize], frontier[chunkSize:]

		var (
			next     []*crawlNode
			chunkErr error
		)
		err := sess.Do(func(drv driver.Driver) error {
			c, maxBatch, cerr := liveClientOf(drv)
			if cerr != nil {
				return cerr
			}
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			defer cancel()

			vars, children, cerr := browseChunk(ctx, c, chunk, opt.maxDepth, budget)
			if cerr != nil {
				return cerr
			}
			next = children
			chunkErr = readAndEmit(ctx, c, maxBatch, vars, resolver, budget, res)
			return nil
		})
		if err != nil {
			// 浏览这一块之前预算就见底了：与链路故障是两回事，标成"未扫完"。
			if errors.Is(err, errScanBudgetSpent) {
				res.Incomplete = true
				break
			}
			// 链路级失败：带着已经扫到的部分返回（契约第 3 条）。
			return finish(), err
		}

		frontier = append(rest, next...)
		switch {
		case errors.Is(chunkErr, errScanTruncated):
			res.Truncated = true
			frontier = nil
		case errors.Is(chunkErr, errScanBudgetSpent):
			res.Incomplete = true
			frontier = nil
		}
	}

	return finish(), nil
}

// resolveStart 把起始节点解析成一个可下行的 crawlNode。
//
// 默认是 Objects 文件夹（i=85）而不是 Root：Root 下面还挂着 Types 与 Views
// 两棵与点位无关的大树（Types 里是全部类型定义，动辄上万节点），
// 从 Root 起扫会把预算全花在那里。
//
// **起始节点的 label 一律留空**：标签列给的是"从你开始扫的地方往下"的路径。
// 从默认的 Objects 起扫时，多一个 "Objects/" 前缀只是噪声；从一台设备的子树
// 起扫时，用户已经知道自己从哪里开始，前缀同样没有信息量。节点的显示名从
// 起始节点的**子节点**才开始拼（见 crawlNode.child 的 joinLabel）。
func resolveStart(ctx context.Context, c *OpcUaClient, opt pointScanOptions, budget *requestBudget) (*crawlNode, error) {
	node := &crawlNode{
		nid: ua.NewNumericNodeID(0, id.ObjectsFolder),
		path: browsePath{
			text: "Root/Objects",
			qns:  []*ua.QualifiedName{{Name: "Objects"}},
			ok:   true,
		},
	}
	if opt.start == nil {
		return node, nil
	}

	if !opt.start.isPath {
		// 直接给了 NodeID：它本身就是可用地址，但没有从 Root 出发的路径，
		// 因此这一支下面的节点只能靠 NodeID 寻址（path.ok=false）。
		//
		// **但先确认它真的存在**：解析器对裸词是宽容的（ua.ParseNodeID 会把
		// 一个没有 i=/s= 前缀的串当 ns=0 的字符串 NodeID），于是一个拼错的
		// 起始节点会变成"另一个不存在的 NodeID"，扫出来一份空清单。用户从
		// "一个点位都没有"得不出"我把起始节点写错了"——与 specs 通篇在防的
		// 那类误读同型。一次读就够，且顺带把"这台服务器上确实有它"钉死。
		if !budget.spend() {
			return nil, errScanBudgetSpent
		}
		dvs, err := c.ReadAttribute(ctx, []*ua.NodeID{opt.start.nodeID}, ua.AttributeIDNodeClass)
		if err != nil {
			return nil, err
		}
		if dv := dataValueAt(dvs, 0); dv == nil || dv.Status != ua.StatusOK {
			status := "没有应答"
			if dv != nil {
				status = dv.Status.Error()
			}
			return nil, fmt.Errorf("起始节点 %s 在这台服务器上不存在（%s）"+
				"——请检查 NodeID 是否写错，或改用从 Root 写起的浏览路径",
				opt.start.nodeID, status)
		}
		return &crawlNode{nid: opt.start.nodeID}, nil
	}

	if !budget.spend() {
		return nil, errScanBudgetSpent
	}
	nid, err := resolveBrowsePath(ctx, c.c, opt.start.path)
	if err != nil {
		return nil, fmt.Errorf("起始节点路径解析失败（该路径在这台服务器上找不到）: %w", err)
	}

	// 按解析器自己的规则重新渲染这一段路径，再验证它能原样解析回去 ——
	// 用户手写的路径可能用了一些我们渲染不出来的写法（含 /、含空段），
	// 那种情况下从这里往下的地址都会漂，宁可整支不要路径。
	segs := make([]string, 0, len(opt.start.path)+1)
	segs = append(segs, "Root")
	renderable := true
	for _, qn := range opt.start.path {
		s, ok := segmentText(qn)
		if !ok {
			renderable = false
			break
		}
		segs = append(segs, s)
	}
	path := browsePath{}
	if renderable {
		text := strings.Join(segs, "/")
		if pathRoundTrips(text, opt.start.path) {
			path = browsePath{text: text, qns: opt.start.path, ok: true}
		}
	}
	return &crawlNode{nid: nid, path: path}, nil
}

// browseChunk 浏览一块节点，返回其中的变量与下一层的可下行节点。
//
// **一次 BrowseRequest 覆盖整块**（见 OpcUaClient.BrowseBatch 的说明）：
// 一节点一请求会把往返数乘以块大小，而预算正是由往返数决定的。
func browseChunk(ctx context.Context, c *OpcUaClient, chunk []*crawlNode,
	maxDepth int, budget *requestBudget) ([]*crawlNode, []*crawlNode, error) {
	if !budget.spend() {
		return nil, nil, errScanBudgetSpent
	}

	descs := make([]*ua.BrowseDescription, len(chunk))
	for i, n := range chunk {
		descs[i] = &ua.BrowseDescription{
			NodeID:          n.nid,
			BrowseDirection: ua.BrowseDirectionForward,
			// 只走层次引用（并含其子类型）：非层次引用（HasTypeDefinition、
			// 事件类型……）不是"下级的点位"，跟着走会把地址空间翻好几倍。
			ReferenceTypeID: ua.NewNumericNodeID(0, id.HierarchicalReferences),
			IncludeSubtypes: true,
			// 只关心对象与变量：方法、视图、类型定义都不是点位。
			NodeClassMask: uint32(ua.NodeClassObject | ua.NodeClassVariable),
			ResultMask:    uint32(ua.BrowseResultMaskAll),
		}
	}

	results, err := c.BrowseBatch(ctx, descs)
	if err != nil {
		return nil, nil, err
	}

	var vars, next []*crawlNode
	for i, parent := range chunk {
		if i >= len(results) || results[i] == nil {
			continue
		}
		// 单个节点不可浏览（BadNodeIdUnknown、BadNoMatch……）是服务器对
		// "这一支我读不了"的正常答复，不是整次扫描的失败 —— 跳过它，
		// 别把一台只有某个子分支异常的服务器判成一无所获。
		if results[i].StatusCode != ua.StatusOK {
			logDebug("pointscan: 节点 %s 不可浏览（%v），跳过这一支", parent.nid, results[i].StatusCode)
			continue
		}

		dup := duplicateNames(results[i].References)
		for _, ref := range results[i].References {
			child := parent.child(ref)
			if child == nil {
				continue
			}
			if dup[qualifiedKey(ref.BrowseName)] {
				// 同父下重名 ⇒ 从 Root 下来的路径不唯一，两个兄弟会渲染出**同一条**
				// 地址串（撞 (device_id, name) 唯一键，解析时还会都指向第一个）。
				// 路径那一支整体废掉，但节点本身仍然可以用 NodeID 寻址 ——
				// 所以**不丢**这些点位，只是不再给它们路径。
				child.path = browsePath{}
			}
			switch ref.NodeClass {
			case ua.NodeClassVariable:
				vars = append(vars, child)
			case ua.NodeClassObject:
				if maxDepth > 0 && child.depth > maxDepth {
					continue
				}
				next = append(next, child)
			}
		}
	}
	return vars, next, nil
}

// duplicateNames 找出同一批引用里重复出现的浏览名（见 browseChunk 的用法）。
func duplicateNames(refs []*ua.ReferenceDescription) map[string]bool {
	counts := make(map[string]int, len(refs))
	for _, ref := range refs {
		if ref == nil || ref.BrowseName == nil {
			continue
		}
		counts[qualifiedKey(ref.BrowseName)]++
	}
	dup := make(map[string]bool)
	for k, n := range counts {
		if n > 1 {
			dup[k] = true
		}
	}
	return dup
}

// qualifiedKey 浏览名的去重键：命名空间 + 名字。
//
// **不能只用名字**：不同命名空间下的同名节点是两回事，合并它们会让一个正常的
// 服务器被误判成"有重名兄弟"，从而白白丢掉一批本来能安全寻址的点位。
func qualifiedKey(qn *ua.QualifiedName) string {
	if qn == nil {
		return ""
	}
	return strconv.FormatUint(uint64(qn.NamespaceIndex), 10) + "\x00" + qn.Name
}

// readAndEmit 取一块变量的 DataType，渲染成点位并并进结果。
//
// 返回 errScanBudgetSpent 或 errScanTruncated 表示"提前收工"，其余返回 nil。
// 只有真正的链路级失败才会返回别的错误。
func readAndEmit(ctx context.Context, c *OpcUaClient, maxBatch int, vars []*crawlNode,
	resolver *typeResolver, budget *requestBudget, res *driver.PointScanResult) error {
	if len(vars) == 0 {
		return nil
	}
	if maxBatch <= 0 {
		maxBatch = defaultMaxBatch
	}

	// 一块的变量共用尽可能少的 ReadRequest：DataType 属性按 MaxBatch 切子批。
	// 这是"一次扫描能扫完多少个点位"的第二个杠杆（第一个是批量 Browse）。
	nodes := make([]*ua.NodeID, len(vars))
	for i, v := range vars {
		nodes[i] = v.nid
	}
	types := make([]resolvedType, len(vars))
	known := make([]bool, len(vars))

	// readUpTo 已**问过** DataType 的下标上界。预算中途用尽时，它之后那些变量
	// 根本没问过 —— 既不该进 Points，也不该进 Scanned/Unreadable。把它们算成
	// "未列出"会让用户以为"这些点位的类型映射不了"，而事实是"扫描没跑到那里"：
	// specs §9 写得很清楚，incomplete 时三个计数都只是**已完成部分**的统计。
	readUpTo := len(vars)
	var pendingErr error
	for start := 0; start < len(nodes); start += maxBatch {
		end := start + maxBatch
		if end > len(nodes) {
			end = len(nodes)
		}
		if !budget.spend() {
			readUpTo, pendingErr = start, errScanBudgetSpent
			break
		}

		dvs, err := c.ReadAttribute(ctx, nodes[start:end], ua.AttributeIDDataType)
		if err != nil {
			return err
		}

		want := make([]*ua.NodeID, 0, end-start)
		dtOf := make([]*ua.NodeID, end-start)
		for i := start; i < end; i++ {
			dt, ok := datatypeOf(dataValueAt(dvs, i-start))
			if !ok {
				continue
			}
			dtOf[i-start] = dt
			want = append(want, dt)
		}
		outcome, err := resolver.resolveTypes(ctx, c, want, budget)
		if err != nil {
			return err
		}
		for i := start; i < end; i++ {
			if dt := dtOf[i-start]; dt != nil {
				if rt, ok := outcome.types[dt.String()]; ok {
					types[i], known[i] = rt, true
				}
			}
		}
		// 类型上溯把预算花光了：这一子批**读到了属性但没问出类型**，再往后
		// 也一样。把计数切在这个子批的起点，与上面那条边界保持同一个口径。
		if outcome.ranOut {
			readUpTo, pendingErr = start, errScanBudgetSpent
			break
		}
	}

	// 逐个渲染。**先判截断再计数**：触顶之后那些根本没处理过的点位不该进
	// Scanned，否则 readable + unreadable == scanned 这条不变式当场就破
	//（照 rockwellcipdrv/pointscan.go 的同一条说明）。
	for i, v := range vars[:readUpTo] {
		if len(res.Points) >= pointScanMaxPoints {
			return errScanTruncated
		}
		res.Scanned++

		if !known[i] {
			res.Unreadable++
			logDebug("pointscan: 跳过 %s（类型没有对应的通用类型）", v.nid)
			continue
		}
		name := addressOf(v)
		if name == "" {
			res.Unreadable++
			logDebug("pointscan: 跳过 %s（名字无法安全寻址）", v.nid)
			continue
		}
		res.Readable++
		res.Points = append(res.Points, driver.DiscoveredPoint{
			Name:  name,
			Label: v.label,
			// Value 一律留空：本协议不读采样值（见文件头第 1 条）。
			CommonDataType: types[i].mapping.common,
			Note:           TypeNote(types[i].base, types[i].mapping),
		})
	}
	return pendingErr
}

// dataValueAt 取切片里的一项，越界返回 nil。
//
// 应答与请求按序对应是规范要求，但畸形服务器少回几项不会让任何人提前发现 ——
// 越界 panic 会被宿主的 recover 咽成一次 RPC 错误，那一次扫描就白做了。
func dataValueAt(dvs []*ua.DataValue, i int) *ua.DataValue {
	if i < 0 || i >= len(dvs) {
		return nil
	}
	return dvs[i]
}

// liveClientOf 取出驱动当前活着的客户端与它配置的批量大小。
//
// **每次 Do 现取、不跨 Do 缓存**：热刷新会重建驱动实例、重连会换掉 client，
// 缓存下来的指针读的是一个已经 Close 的句柄（见 specs/point-discovery.md §6）。
//
// 这里确实把 client 带出了 RLock 的临界区，与"不要缓存指针"那条规则看着相像，
// 其实不是一回事：那条规则防的是**跨多次 Do** 缓存。临停时实例更是只有本函数
// 一个使用者。真要在临界区里跑完整个块的网络往返，反而会让 Close（它要写锁）
// 从"关掉套接字把阻塞解开"退化成"等一个不会自己结束的调用"。
func liveClientOf(drv driver.Driver) (*OpcUaClient, int, error) {
	d, ok := drv.(*opcuaDriver)
	if !ok {
		return nil, 0, fmt.Errorf("opcua: 不支持的驱动类型 %T", drv)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil || !d.client.IsConnected() {
		return nil, 0, errors.New("opcua: 设备未连接，无法扫描")
	}
	batch := defaultMaxBatch
	if d.config != nil && d.config.MaxBatch > 0 {
		batch = d.config.MaxBatch
	}
	return d.client, batch, nil
}
