// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gopcua/opcua/ua"
)

// ==================== 点位扫描：地址渲染与往返验证 ====================
//
// 本文件负责点位扫描里最容易出错的一件事：**产出的地址必须能被本协议自己的
// 解析器（nodeid.go 的 parseAddress）还原回同一个节点**。
//
// 为什么这条在这里的分量与其他协议**不同**，值得先写清楚（别照抄 Modbus/S7 的
// 那套论证）：
//
//	Modbus/S7/FINS/MC  一个解析不出来的地址会让**那个点位永远没有值**
//	           （规划期就把它拆出去，只标 QualityBadAddress）。
//	本协议             也一样，但**成因不同**。opcuaDriver.Read 对 *parseError
//	           与 ua.StatusCode 都是**逐点** Quality=0 继续（见 driver.go 的
//	           Read），一个坏地址最多让那一个点位没有值。
//
// 那危险在哪？在**解析到另一个节点**。parseAddress 把任何含 "/" 的串当浏览路径，
// 于是 `ns=2;s=Room1/Temp` 这种字符串 NodeID 会被解析成一条两段路径 —— 它不报错，
// 只是多半解析不到；而只要服务器上恰好有一条同名路径，这个点位就会**永远采到
// 另一个节点的值**。那种错误在时序库里表现为一条完全合理的曲线，比一个死点位
// 难发现得多（同 driversrc/abb/abbdrv/types.go 的 resolveKind 那段论证，
// 也是"看着合理的错值比 panic 更难查"这一类）。
//
// 因此本文件的原则只有一条：**先渲染、再解析回来、逐字比对**。比对不过就不产出
// 这个地址（调用方把它计入"未列出"）。宁可少列一个，也不要列一个会采错值的。

// maxAddressLabel 写进 DiscoveredPoint.Label 的展示路径上限。
//
// 前端的标签输入框是 maxlength=100 的单行框，而一棵深树的全路径很容易超过它。
// 超了**不截断**（截断出来的是半个名字，比没有更误导），而是回落到叶子节点自己
// 的显示名 —— 那时用户至少能看到一个完整的、他在 UaExpert 里认得出的名字。
const maxAddressLabel = 100

// browsePath 一个节点**从 RootFolder 起**逐段累积出来的下行浏览路径。
//
// 之所以逐段累积、而不是由 NodeID 反推：能被 "/" 走到的节点必然有一条从根出发的
// 层次引用链，而那条链正是我们下行时走过的这一段（因此按构造可达）；反推则要依赖
// 服务器支持 TranslateBrowsePathsToNodeIds 且路径唯一，两者都不保证。
type browsePath struct {
	// text 渲染好的地址串（如 "Root/Objects/2:Device/2:Temp"）。
	text string
	// qns text 对应的相对路径段，**不含前导的 Root** —— resolveBrowsePath 正是以
	// RootFolder 为起点，parseBrowsePath 又会把前导的 Root 剥掉，因此这一份就是
	// "解析回来应当得到的东西"。
	qns []*ua.QualifiedName
	// ok 这一段路径是否仍然可用。任一段渲染不回去，从这里往下的整棵子树都不再
	// 产出路径地址（但那些节点仍然可以用 NodeID 寻址，所以照样往下走）。
	ok bool
}

// segmentText 把一个浏览名渲染成一个路径段，渲染不出来返回 ok=false。
//
// 每一条拒绝理由都对应 parsePathSegment（nodeid.go）的一个分支，逐条对上：
//
//	名字为空        parseBrowsePath 会**静默丢掉空段**，路径悄悄变短
//	名字含 '/'      会被当成段分隔符，一个名字拆成两段
//	首尾有空白      parseAddress 会 TrimSpace 整串，只有末段受影响、更难查
//
// 而 ns 前缀的规则是反过来的：**不是**"ns!=0 才写"，而是"ns!=0 **或** 名字里有
// ':' 就写"。parsePathSegment 以**第一个**冒号切分，裸名 "Motor:1" 会被它读成
// 命名空间 "Motor" 并当场报错；写成 "ns=0:Motor:1" 时第一个冒号被前缀吃掉，
// 剩下的 "Motor:1" 才是名字。
func segmentText(qn *ua.QualifiedName) (string, bool) {
	if qn == nil {
		return "", false
	}
	name := qn.Name
	if name == "" || strings.Contains(name, "/") || strings.TrimSpace(name) != name {
		return "", false
	}
	if qn.NamespaceIndex != 0 || strings.Contains(name, ":") {
		return "ns=" + strconv.FormatUint(uint64(qn.NamespaceIndex), 10) + ":" + name, true
	}
	return name, true
}

// pathRoundTrips 验证一段渲染好的路径能被 parseAddress 还原成**同一串段**。
//
// 逐段比对（而不是只比字符串）是必须的：parseBrowsePath 对连续斜杠是容忍的、
// 对前导 Root/Objects 是**剥掉**的，只比字符串会把一条"解析得歪七扭八但没报错"
// 的路径判成通过。
func pathRoundTrips(text string, want []*ua.QualifiedName) bool {
	if text == "" || len(want) == 0 {
		return false
	}
	addr, err := parseAddress(text)
	if err != nil || !addr.isPath || len(addr.path) != len(want) {
		return false
	}
	for i := range want {
		got, exp := addr.path[i], want[i]
		if got == nil || exp == nil ||
			got.NamespaceIndex != exp.NamespaceIndex || got.Name != exp.Name {
			return false
		}
	}
	return true
}

// nodeIDRoundTrips 验证 NodeID 的字符串形式能被 parseAddress 还原成同一个 NodeID。
//
// 比对用字符串而不是结构体：parseAddress 的入口判据就是"含不含 /"，
// 而它对 NodeID 的处理是纯字符串往返（ua.ParseNodeID 再 String()），
// 因此逐字相等恰好就是"这条地址写进 device_address 后会被解析成同一个节点"。
func nodeIDRoundTrips(text string, want *ua.NodeID) bool {
	if text == "" || want == nil {
		return false
	}
	addr, err := parseAddress(text)
	if err != nil || addr.isPath || addr.nodeID == nil {
		return false
	}
	return addr.nodeID.String() == want.String()
}

// joinLabel 拼接展示用的路径名；超过上限时回落到叶子名（见 maxAddressLabel）。
func joinLabel(parent, leaf string) string {
	if parent == "" {
		return leaf
	}
	joined := parent + "/" + leaf
	if utf8.RuneCountInString(joined) > maxAddressLabel {
		return leaf
	}
	return joined
}
