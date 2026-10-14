// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"

	"iot-gateway/driver"
)

// ==================== 点位扫描（原生枚举） ====================
//
// 本文件实现 Rockwell.CIP 的点位扫描。**形态与 Modbus/S7/FINS 完全不同**，
// 值得先把它是什么说清楚：
//
//	Modbus/S7/FINS  扫一段**地址范围**，回答"这段里哪些地址读得到"。
//	                产出的是一份候选清单 —— 设备不会告诉你它有哪些点位。
//	本文件           用 CIP Symbol Object（类 0x6B）**原生枚举**控制器上的全部
//	                标签，回答"这台设备上到底有哪些点位"。产出的是**设备自报的
//	                标签名本身**，每个还带着自己的 CIP 类型码。
//
// 这是 specs/point-discovery.md §1 里点名的少数例外之一（与 OPC UA 的 Browse、
// ABB 的 RWS 信号表、DLT645 的数据标识字典同类），也是 driversdk 的
// `FindPointsRequest.Session` 注释里预留的那个位置："**原生枚举**类实现根本不需要
// 读设备，它们只用 Options 就能列出点位。这条留白就是本框架为它们留的位置。"
//
// **但 Rockwell 与它们有一处不同：它仍然需要设备**（要连上去问控制器），
// 所以照样要走 Session。
//
// ---------------------------------------------------------------------------
// 为什么用库的 ListTags 而不是照着手册写一遍 0x6B 的请求/应答
// ---------------------------------------------------------------------------
//
// 这正是当初 Omron.CIP 拒绝实现点位扫描的那条理由（"Symbol Object 的布局在本
// 仓库里没有第二处依据可对"）。罗克韦尔这边那条理由不成立：本仓库的既有依赖
// github.com/iceisfun/goindustrial v0.3.0 已经实现了整条链路
//（protocol/ethernetip/cip/symbol.go 的请求构造与应答解码 + client.go 的
// Client.ListTags）。**手搓一份就是本仓库反复挡过的"两份实现迟早漂移"**，
// 而漂移的症状是返回一份错位的标签表 —— 用户不会发现。
//
// 代价是这条路径的可观测性依赖库：库不设读超时（见下），也不给部分结果。
//
// ---------------------------------------------------------------------------
// 三条必须知道的局限（用户可见的那部分写在 PointDiscovery().Description 里）
// ---------------------------------------------------------------------------
//
//  1. **可能挂住，所以必须自己兜 deadline。** goindustrial 的 TCP 传输从头到尾
//     不调 SetReadDeadline，`Session.SendRRData` → `conn.Receive()` 连 ctx 都不看。
//     一个"TCP 通、CIP 不应答"的设备会让 ListTags 永久阻塞。若不管，这次 RPC 会
//     一直挂到网关侧超时并累积插件卡死计数 —— 连续几次就把整个插件进程连同它下面
//     所有协议、所有设备一起重启。所以 runEnumeration 用 goroutine + select 兜底，
//     **一定在预算内返回**。
//
//     那个被落下的 goroutine 由调用方收尾时关掉：插件宿主的
//     driversdk.handleDiscoverPoints 在 finder 返回后会 `defer drv.Close()`，
//     一路走到 SessionCloser.Close 的 TCPConn.Close()，阻塞中的 Receive 立刻
//     报错退出。**这条依赖库的内部行为**（DirectTransport.Close 不持锁、
//     Unregister 只写不等）由 client_test.go 的 TestClientListTagsUnblocksOnClose
//     钉住：库哪天改了这里，那条测试会红。
//
//  2. **拿不到部分结果。** ListTags 是"整只对象"的操作 —— 它要么返回全部标签，
//     要么一个都不返回。因此超预算时手上零个结果，**没有"部分"可交**。
//     契约（specs/point-discovery.md §7 第 1 条）要求超预算时带上已完成的部分结果
//     并把 Incomplete 置位；本 finder 做不到，于是**报错**而不是返回一份空清单。
//     这是**有意的偏离**，理由见 runEnumeration 的注释。
//
//  3. **Session.Do 只调一次。** 不是偷懒：枚举没有分块，所以 §6 那条"块与块之间
//     必须让锁"对本 finder 是**空洞而不是违反**。而且 Rockwell 没实现
//     ConnectionMatcher，selectSession 必然返回 (nil, nil) 走**临时实例**路径
//     （另开一条 44818 连接，与 S7/Modbus TCP 现状一致），那条路上 Do 不做任何
//     加锁。**不要**给它加 MatchConnection：那会把采集会话的锁交给一次可能跑几秒的
//     枚举，正是 specs §6 在防的事。

// 点位扫描的规模与预算。
const (
	// pointScanMaxPoints 单次扫描最多返回多少个点位。
	//
	// 上限存在的意义不是性能而是**事故半径**：应答帧要走 pluginwire 的写锁，
	// 而那份锁是该插件进程上所有协议共用的 —— 一份几百 KB 的应答会把它们一起
	// 头阻塞。2000 个标签（每个一行、几十字节）是几十 KB 的量级，一次能过管道。
	//
	// 与 PointDiscovery().MaxPoints 必须一致 —— 后者只是把它报给网关与前端，
	// 由 driversdk.Meta.Validate 校验落在 [1,20000] 内。
	pointScanMaxPoints = 2000

	// pointScanCloseMargin 从预算里给调用方的收尾留的余量。
	//
	// finder 返回之后，宿主还要 drv.Close()（注销会话 + 关套接字）再把应答帧写出去
	// （2000 个点位是几十 KB）。这些都不算在 finder 的预算里，但都算在 RPC 超时里。
	// req.Deadline 已经是"网关钳过的预算"，RPC 还额外有 pointScanRPCGrace 的余量，
	// 所以这 100ms 是白拿的。
	pointScanCloseMargin = 100 * time.Millisecond
)

// pointScanBudgetMs 本协议声明的整段扫描预算（毫秒）。
//
// 与 Modbus 一样报一个比网关上限大的值：真正生效的永远是
// min(本值, pluginhost 的 pointScanGatewayBudgetCapMs = 7000ms)，因为这条链路是
// **同步 HTTP**，超时会拖住网关停机（见 specs/point-discovery.md §4）。
// 声明成 20000 只是如实表达"枚举上千个标签本来就需要这么久"，而不是承诺能让它跑完。
const pointScanBudgetMs = 20000

// pointScanForm 点位扫描的参数表单。
//
// **刻意是空的**：枚举没有旋钮可调 —— 扫多少、扫哪儿都由设备自己决定。
// 与其塞一个没有意义的输入框（用户会以为调了有用），不如不给。
// form-create 渲染一张空表单是合法的，前端的结果区照常显示。
const pointScanForm = `{
  "rule": [],
  "options": {
    "form":{"inline":false,"hideRequiredAsterisk":false,"labelPosition":"right","size":"default","labelWidth":"125px"},
    "resetBtn":{"show":false,"innerText":"重置"},
    "submitBtn":{"show":false,"innerText":"提交"}
  }
}`

// PointDiscovery 返回 Rockwell.CIP 的点位扫描能力声明，供 main.go 填进
// Meta.Protocols。
//
// Description 是用户理解这个功能性质的**主要来源**（常驻显示在弹窗顶部），
// 所以四条局限必须写在里面，一条都不能省 —— 尤其是"不读采样值"与
// "标签上千时可能整次失败"，用户不会从界面上别的地方看出来。
func PointDiscovery() *driver.PointDiscoverySpec {
	return &driver.PointDiscoverySpec{
		Title: "枚举 Rockwell 标签",
		Description: "用 CIP Symbol Object（类 0x6B）**原生枚举**控制器上的全部标签 —— " +
			"这是少数能真正列出点位的协议之一：列出来的是**标签名本身**，不是「某段地址里读得到的那些」。" +
			"四条局限请先看：① **不读采样值**，只列标签与类型（多读一遍值等于把请求数翻倍）；" +
			"② 数组标签**只列第 0 个元素**（设备不提供维度信息），其余元素请手工补；" +
			"③ 结构体（含 TIMER/COUNTER/UDT）以及 LINT、BYTE/WORD/DWORD 这类没有通用类型可映射的标签" +
			"**会被跳过**，跳过数在「未列出」里可见 —— 「未列出」不等于「读不到」；" +
			"④ 枚举每个标签要一次请求，标签上千时可能超出预算而**整次失败**：" +
			"本协议是原生枚举（一次列完），中途停下来拿不到部分结果，所以那时会给一句明确的报错，" +
			"而不是一份残缺的清单",
		FormJSON: json.RawMessage(pointScanForm),
		// "Long" 是 TypeMap 的键（系统里叫 DINT，Logix 的默认整型）——
		// 写成内部裸名 "int32" 会被 Meta.Validate 拒掉。
		// 它只是**兜底初值**：本 finder 产出的每一行都自带 CommonDataType，
		// 只有那一项为空时前端才会用到这个默认值。
		DefaultCommonDataType: driver.TypeLong,
		MaxBudgetMs:           pointScanBudgetMs,
		MaxPoints:             pointScanMaxPoints,
	}
}

// SessionAccess 与 driversdk.SessionAccess 同形，但**刻意本地声明而不 import
// driversdk**：与 probe.go 的 Probe 收 target/options/budget 三个原始值（而不是
// driversdk.ProbeRequest）是同一条取舍 —— 协议实现只依赖 driver 这一份契约，
// 不被整套插件开发套件绑住，本包因此也能脱离插件进程单独测试。
type SessionAccess interface {
	Do(fn func(drv driver.Driver) error) error
}

// tagEnumerator 采集路径的传输契约（rockwellTransport）之外的一项可选能力。
//
// **刻意不进 rockwellTransport 接口**：那个接口是采集路径的东西，加一个只有扫描
// 用的方法会逼每个测试桩都实现它，也会让"能读标签"与"能枚举标签"在类型上混为
// 一谈。用类型断言取，与 Modbus 的 scanSessionOf 用类型开关取驱动内部资源同理。
type tagEnumerator interface {
	ListTags(ctx context.Context) ([]cip.SymbolInstance, error)
}

// ScanPoints 枚举一台已存在的罗克韦尔设备上的全部标签。
//
// 返回的 error 与结果**不会同时非 nil**（与 Modbus/S7/FINS 那条"带着部分结果
// 一起返回"的形态不同）：枚举是整只对象的操作，失败时没有任何东西可交。
// 理由与取舍见 runEnumeration。
//
// deadline 是网关给的**绝对**截止时刻（= 调用时刻 + 被钳过的预算），
// 本函数保证在它之前返回。
func ScanPoints(sess SessionAccess, options json.RawMessage, deadline time.Time) (*driver.PointScanResult, error) {
	if err := parsePointScanOptions(options); err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, errors.New("rockwell: 没有可用的采集会话，无法扫描")
	}

	started := time.Now()

	var result *driver.PointScanResult
	// 整个枚举都在这一次 Do 里：fn 返回即放锁，因此宿主的会话不会被长期占住。
	// 只调一次是因为枚举不可分块（见文件头第 3 条）。
	err := sess.Do(func(drv driver.Driver) error {
		enum, err := tagEnumeratorOf(drv)
		if err != nil {
			return err
		}
		syms, err := runEnumeration(enum, deadline)
		if err != nil {
			return err
		}
		result = buildScanResult(syms, started)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		// 走不到：Do 返回 nil 就意味着 fn 返回了 nil，而那条路径一定给 result 赋了值。
		// 留着是为了让"没结果也没错误"不在下游变成一次 panic。
		return nil, errors.New("rockwell: 标签枚举没有返回结果")
	}
	return result, nil
}

// tagEnumeratorOf 从驱动实例取出枚举能力。
//
// 每次 Do 现取、用完即弃（这里只取一次，因为只有一次 Do）：热刷新会重建驱动实例、
// 重连会换掉 client，缓存下来的指针读的是一个已经 Close 的句柄。
//
// **这里确实把 client 带出了 RLock 的临界区**，与"不要缓存指针"那条规则看着相像，
// 其实不是一回事：那条规则防的是**跨多次 Do** 缓存（中间别人可能重建实例），
// 而本 finder 只有一次 Do、且枚举全程在那个实例上跑完；临时实例（Rockwell 的唯一
// 路径）更是只有本函数一个使用者，没有第二个线程会换掉 d.client。
// 真要在临界区里跑完枚举，就得持着 d.mu 走完整段网络等待 —— 那反而会让
// Close（它要写锁）从"关掉套接字把阻塞解开"退化成"等一个不会自己结束的调用"。
//
// 用类型开关而不是给 driver.Driver 加方法：那会要求全部协议都实现它，
// 而这里只有 Rockwell 需要（同 Modbus 的 scanSessionOf）。
func tagEnumeratorOf(drv driver.Driver) (tagEnumerator, error) {
	d, ok := drv.(*rockwellDriver)
	if !ok {
		return nil, fmt.Errorf("rockwell: 不支持的驱动类型 %T", drv)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.client == nil || !d.client.IsConnected() {
		return nil, errors.New("rockwell: 设备未连接，无法扫描")
	}
	enum, ok := d.client.(tagEnumerator)
	if !ok {
		return nil, fmt.Errorf("rockwell: 传输层 %T 不支持标签枚举", d.client)
	}
	return enum, nil
}

// errEnumerationBudgetSpent 预算在这次 Do 拿到锁之前就已经花光。
//
// 单列一条而不是复用超时那句：两者的原因与处置完全不同。这里通常意味着
// selectSession 为了确认"能不能复用采集会话"等满了 pointScanSessionBudget（5s），
// 而 Rockwell 永远匹配不上（未实现 ConnectionMatcher），白等一场。
var errEnumerationBudgetSpent = errors.New(
	"rockwell: 预算在枚举开始前就已耗尽（等待采集会话释放占去了这段时间），请稍后重试")

// runEnumeration 在预算内跑完一次标签枚举。
//
// **一定是它把"必须在预算内返回"这条契约守住**：库的 TCP 传输不设读超时、也不看
// ctx，一个不应答的设备会让 ListTags 永久阻塞。所以这里开一个 goroutine 去跑，
// 自己在 timer 上等 —— 超时就返回错误，把那个 goroutine 留给调用方关套接字时回收
// （见文件头第 1 条）。
//
// **超预算时返回 error 而不是空结果 + Incomplete**，与契约第 1 条正面冲突，是有意的：
//   - ListTags 要么全给要么不给，超时那一刻手上**零个标签**，"部分结果"根本不存在；
//   - 返回空 + Incomplete 会渲染成一张 0 行的表加一句"未扫完"，而用户会从
//     "一个标签都没有"得出"这台设备没有点位" —— specs §9 通篇在防的就是这一类误读；
//   - 报错则一行结果都不显示，只给一句能自解释的话。
//
// 注意与**截断**的不对称：达到 MaxPoints 时枚举其实已经跑完了，只是映射停手，
// 那种情况**是**有部分结果的（Truncated + 已列出的那些）。
func runEnumeration(enum tagEnumerator, deadline time.Time) ([]cip.SymbolInstance, error) {
	// budget 只为超时文案用。取一次就固定下来：后面时间在走，
	// 而用户该看到的是"这次扫描开始枚举时还剩多少"，不是一个仍在减少的数字。
	// 注意它可能**已经小于网关给的预算** —— selectSession 确认"能不能复用采集会话"
	// 最多要花 5s，那段时间是从预算里扣掉的（见 errEnumerationBudgetSpent）。
	budget := time.Until(deadline)
	remaining := budget - pointScanCloseMargin
	if remaining <= 0 {
		return nil, errEnumerationBudgetSpent
	}

	// 这个 ctx 对当前的 goindustrial 只是摆设（它不看 ctx、也不设 deadline），
	// 留着是因为库哪天补上读超时之后，它就是唯一生效的那道闸 —— **不要**因为
	// "看不出来有什么用"就把它删掉，删了会在库升级后静默失去保护。
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()

	type outcome struct {
		tags []cip.SymbolInstance
		err  error
	}
	// 缓冲 1：超时之后没人再来收，孤儿 goroutine 也不能因此卡在发送上。
	ch := make(chan outcome, 1)
	go func() {
		tags, err := enum.ListTags(ctx)
		ch <- outcome{tags: tags, err: err}
	}()

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case r := <-ch:
		if r.err != nil {
			// 与超时分开报：这条是"枚举发起不了/被拒绝"（设备不支持 0x6B、
			// 链路断了），用户看到它才知道该去查什么。超时那条说的是"太多标签"。
			return nil, fmt.Errorf("rockwell: 标签枚举失败（设备可能不支持 CIP Symbol Object 类 0x6B）: %w", r.err)
		}
		return r.tags, nil
	case <-timer.C:
		return nil, fmt.Errorf(
			"rockwell: 标签枚举未在预算（%s）内完成 —— 这台控制器的标签数超出了预算内的枚举能力"+
				"（CIP Symbol Object 每列一个标签就要一次请求）。本协议是原生枚举：一次列完，"+
				"中途停下来拿不到部分结果，所以这里报失败而不是给出一份残缺的清单。"+
				"请稍后重试；若始终超时，说明标签数已经超出网关的点位扫描预算",
			budget.Round(time.Second))
	}
}

// parsePointScanOptions 校验扫描选项。
//
// 本协议没有任何可调参数（枚举就是枚举），所以合法的选项只有"空"与"空对象"。
// 但**坏 JSON 仍然报错**而不是静默忽略：那说明选项在传输途中被改坏了，
// 悄悄继续会让这类问题永远查不出来。
func parsePointScanOptions(options json.RawMessage) error {
	if len(options) == 0 {
		return nil
	}
	var probe map[string]any
	if err := json.Unmarshal(options, &probe); err != nil {
		return fmt.Errorf("解析扫描选项失败: %w", err)
	}
	return nil
}

// buildScanResult 把枚举结果映射成点位清单。
//
// 计数的语义与"读一段地址"的协议**不同**，必须按这个协议的性质来读：
//
//	Scanned    设备枚举出的符号实例总数（截断时只算已处理过的）
//	Readable   已列出的标签数
//	Unreadable **被跳过的**：类型没有通用类型可映射，或名字无法安全寻址
//
// 「未列出」不等于「读不到」—— 这点写进了 PointDiscovery().Description 与前端文案。
// 不变式 Readable + Unreadable == Scanned 与别的协议一致。
//
// Value **一律不填**：本协议不读采样值（多读一遍等于把请求数翻倍）。
// 结果级的 AssumedCommonDataType 也因此**留空** —— 前端据它为空判断"这次没有采样值"，
// 从而隐藏「显示 0 值」这枚开关（那一列全是空的，开关只会误导）。
func buildScanResult(syms []cip.SymbolInstance, started time.Time) *driver.PointScanResult {
	res := &driver.PointScanResult{
		// 空切片而不是 nil：nil 过线是 JSON null，前端得为它多写一条分支。
		Points: make([]driver.DiscoveredPoint, 0, len(syms)),
	}

	for _, sym := range syms {
		// 先判截断再计数：触顶之后那些**根本没试过**的标签不该进 Scanned，
		// 否则 Readable + Unreadable == Scanned 这条不变式当场就破了。
		if len(res.Points) >= pointScanMaxPoints {
			res.Truncated = true
			logf("pointscan: 标签数达到上限 %d，其余标签未列出", pointScanMaxPoints)
			break
		}
		res.Scanned++

		name, common, note, ok := describeSymbol(sym)
		if !ok {
			res.Unreadable++
			continue
		}
		res.Readable++
		res.Points = append(res.Points, driver.DiscoveredPoint{
			Name:           name,
			CommonDataType: common,
			Note:           note,
		})
	}

	res.ElapsedMs = int(time.Since(started).Milliseconds())
	return res
}

// describeSymbol 把一个符号实例渲染成一行点位。
//
// **硬门槛是名字，不是类型。** 判据只有 ParseRockwellAddress 一个 —— 不用也不该
// 另立一套：它正是采集侧规划前用的那一个（range.go 的 partitionBadTags），所以
// 这里放行的名字，采集一定认得出；这里挡下的名字，采集一定认不出。
//
// **注意"挡下"与"放行"的后果现在都不严重了，但这条门槛仍然要有。** 采集侧已经
// 不会因为一个解析不出的名字整批失败（见 partitionBadTags）：那种点位会被摘出计划、
// 单独标 QualityBadAddress（报警「地址无效」），设备保持在线。所以这里跳过它的理由
// 不再是"否则整台设备全挂"，而是**列出来就等于建一批永远没有值的点位**，而用户
// 分不清那是设备坏了、还是这个点位本来就没数据 —— 与下面类型那一格的取舍同源。
//
// 类型层面是**软处理**：一个类型映射不出来，最坏只是那一个点位 Quality=0
// （driver.go 的 parseTagContents 按点隔离解码失败），不会拖垮整台设备。
// 同样选择**跳过**而不是列出来，理由与上一条相同。
func describeSymbol(sym cip.SymbolInstance) (name, common, note string, ok bool) {
	common, mapped := commonTypeOfCIP(sym.Type)
	if !mapped {
		logDebug("pointscan: 跳过标签 %q（类型 %s 没有对应的通用类型）", sym.Name, sym.Type.String())
		return "", "", "", false
	}

	candidate := strings.TrimSpace(sym.Name)
	if sym.Type.IsArray() && !strings.HasSuffix(candidate, "]") {
		// 数组只列第 0 个元素。它是**一定存在**的元素，所以这是"正确可用的地址"
		// 而不是猜测；SymbolInstance 不带维度信息，再多写一个下标就是编造。
		// 加不加 [0] 读的是同一个元素（ReadTag("Arr") 本来就是第 0 个），
		// 区别只在于把这件事写在明面上。
		candidate += "[0]"
	}

	name, valid := ParseRockwellAddress(candidate)
	if !valid {
		// 含空白/控制字符/非 ASCII 的标签名。设备允许这种名字，但本驱动的地址
		// 语法不允许 —— 放它进去就是建一个**永远没有值**的点位（采集侧会把它摘出
		// 计划、标 QualityBadAddress，见 range.go 的 partitionBadTags）。
		logDebug("pointscan: 跳过标签 %q（名字不是可安全寻址的标签路径）", sym.Name)
		return "", "", "", false
	}

	if sym.Type.IsArray() {
		note = "数组类型（" + sym.Type.String() + "）：这里只列出第 0 个元素，其余请按「" +
			strings.TrimSuffix(name, "[0]") + "[1]、[2]…」手工补录（设备不提供数组维度）"
	}
	return name, common, note, true
}

// commonTypeOfCIP 把 CIP 数据类型码映射成**通用类型名**（TypeMap 的键）。
//
// 只映射 commonTypeMap（typemap.go）里有入口的那七种，**其余一律不映射**，
// 理由逐类写清楚，因为每一个"顺手补上"都会造成实打实的错误：
//
//	LINT(0x00C5) 与无符号族 USINT/UINT/UDINT/ULINT(0x00C6..0x00C9)
//	    commonTypeMap 里没有入口，而补一个要同时动冻结点位类型映射的黄金基线
//	    （pluginhost/testdata/parity/typemap/rockwell.json）与 typemap.go 里
//	    "Logix 数值类型原生有符号，无 CIP 标准无符号类型码"那条既有取舍。
//	    int64 在本插件内部是注册过的，但没有通用类型名能到达它（见 metadata.go 的说明）。
//	BYTE/WORD/DWORD/LWORD(0x00D1..0x00D4)
//	    同上，typemap.go 明写"Byte/Word/DWord 不映射（位访问用 TagName.N 语法）"。
//	STRING2/STRINGN/STRINGI(0x00D5/0x00D9/0x00DE)
//	    值与 STRING 的布局不同且在本仓库里未经验证，映射成 String 会解出一串
//	    看着完全合理的乱码 —— 比报错更难发现。
//	STRUCT(0x02A0) 及一切 Logix 派生类型（TIMER/COUNTER/UDT、模块自定义类型）
//	    都回这个码，而成员布局在这里拿不到。
//
// 数组位（bit 15）先 Base() 掉再判：数组的元素类型就是它的基础类型。
func commonTypeOfCIP(t cip.DataType) (string, bool) {
	switch t.Base() {
	case cip.TypeBOOL:
		return driver.TypeBoolean, true
	case cip.TypeSINT:
		return driver.TypeChar, true
	case cip.TypeINT:
		return driver.TypeShort, true
	case cip.TypeDINT:
		return driver.TypeLong, true
	case cip.TypeREAL:
		return driver.TypeFloat, true
	case cip.TypeLREAL:
		return driver.TypeDouble, true
	case cip.TypeSTRING:
		return driver.TypeString, true
	default:
		return "", false
	}
}
