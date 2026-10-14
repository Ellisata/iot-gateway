// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"io"
	"net"
	"time"
)

// NewCIP 启动一个欧姆龙 EtherNet/IP（CIP）显式报文模拟器。
//
//	欧姆龙 CIP：0x0A 多服务读（按 maxTagsPerRequest 批量），每标签固定 2 字节 INT
//
// 帧格式与 driver/omron/cip 的解封一致——0x4C 读响应 = 服务回显 0xCC | 保留 |
// 通用状态 | 类型码(2 LE) | 数据（欧姆龙方言，无 CIP 扩展状态字数字节）。
//
//	封装帧 = 命令(2 LE) | 长度(2 LE) | 会话句柄(4 LE) | 状态(4 LE) | 发送者上下文(8) | 选项(4) | 载荷
//	Register Session（0x0065）：载荷 = 协议版本(2) + 选项(2) → 回会话句柄 + 载荷回显
//	SendRRData（0x006F）：载荷 = 接口句柄(4)+超时(2)+项数(2)+CPF 项 → 回同构 CPF，
//	          数据项（0x00B2）内含 CIP 响应
func NewCIP() (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respOmron, 0, nil) })
}

// NewCIPWithLatency 同 NewCIP，但每次 SendRRData 事务注入固定延迟，模拟真实网络 RTT。
// CIP 驱动逐标签串行读，RTT 直接叠加进轮询周期，是容量评估的主导项。
func NewCIPWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respOmron, d, nil) })
}

// NewCIPAB 启动一个罗克韦尔（Allen-Bradley Logix）EtherNet/IP 模拟器。
//
// 响应为标准 CIP Message Router 方言：0x4C 读响应 = 服务回显 0xCC | 保留 |
// 通用状态 | 扩展状态字数(1，成功为 0) | 类型码(2 LE) | 数据。
// goindustrial 严格按该规范解码（扩展状态字数字节缺失时类型码首字节会被
// 误读为字数导致 EOF），与欧姆龙方言的差异见 NewCIP 注释。
// 数组 Read Tag Elements 按请求末 2 字节 ElementCount 回等量元素。
func NewCIPAB() (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respAB, 0, nil) })
}

// NewCIPABWithLatency 同 NewCIPAB，但每次事务注入固定延迟。
func NewCIPABWithLatency(d time.Duration) (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respAB, d, nil) })
}

// ==================== Symbol Object（类 0x6B，标签枚举） ====================

// CIPTag 假 Logix 控制器上的一个标签（Symbol Object 的一个实例）。
//
// 实例号按入参顺序从 1 起分配 —— 真控制器上实例号是**不复用**的（删掉一个标签
// 会留下空洞，新标签拿更大的号），所以"跳号"在真机上是常态；要造那种形状，
// 用 CIPTag{...} 之外的手段做不到，但 GapCIPTag 可以（见下）。
type CIPTag struct {
	Name string
	Type uint16 // CIP 数据类型码；带 0x8000 位表示数组
}

// CIP 数据类型码。**刻意在这里重写一遍而不是 import 插件包**：
// 假 PLC 与插件是同一份理解的两次独立实现时，测试才是一次真正的交叉验证 ——
// 两边在同一个字节上写错（如把类型码当大端写）会两边一起通过、现场一起失败。
// 这条与 cipidentity.go 文件头对 ListIdentity 的取舍完全一致。
const (
	CIPTypeBOOL   uint16 = 0x00C1
	CIPTypeSINT   uint16 = 0x00C2
	CIPTypeINT    uint16 = 0x00C3
	CIPTypeDINT   uint16 = 0x00C4
	CIPTypeLINT   uint16 = 0x00C5
	CIPTypeDWORD  uint16 = 0x00D3
	CIPTypeREAL   uint16 = 0x00CA
	CIPTypeLREAL  uint16 = 0x00CB
	CIPTypeSTRING uint16 = 0x00D0
	CIPTypeSTRUCT uint16 = 0x02A0

	// CIPArrayBit 类型码的数组位（bit 15）。置位表示该标签是数组；
	// 与其他位或起来用，如 CIPTypeDINT | CIPArrayBit。
	CIPArrayBit uint16 = 0x8000
)

// NewCIPABWithTags 启动一个**带标签表**的罗克韦尔模拟器。
//
// 除 0x4C 读之外，它还会应答 CIP Symbol Object（类 0x6B）的
// Get_Attribute_List（服务码 0x03）：实例 0 回类属性（revision + max instance），
// 实例 N 回该标签的名字与类型码。这正是点位扫描（原生枚举）要走的路，
// 因此这条路子能端到端地测到真实插件进程里。
//
// tags 为空时 maxInstance 为 0 —— 一个**格式正确但没有任何标签**的控制器，
// 正是"空枚举不是错误"那条用例要的形状。
func NewCIPABWithTags(tags ...CIPTag) (*Server, error) {
	return newServer(func(c net.Conn) { handleCIPConn(c, respAB, 0, tags) })
}

// NewCIPABHanging 启动一个"TCP 通、CIP 不应答"的罗克韦尔模拟器：
// 会话注册（0x0065）照常应答 —— 否则客户端连都连不上、测不到想测的东西 ——
// 但此后所有 SendRRData 一律收下不回。
//
// 用来钉住两件事：① 点位扫描的枚举守卫（库的 TCP 传输不设读超时，不应答的设备
// 会让 ListTags 永久阻塞，finder 必须自己按预算返回）；② 收尾的 Close 能把那次
// 阻塞解开（关掉套接字 → 阻塞中的 Receive 报错退出）。
func NewCIPABHanging() (*Server, error) {
	return newServer(func(c net.Conn) {
		hdr := make([]byte, 24)
		for {
			if _, err := io.ReadFull(c, hdr); err != nil {
				return
			}
			length := int(binary.LittleEndian.Uint16(hdr[2:4]))
			payload := make([]byte, length)
			if _, err := io.ReadFull(c, payload); err != nil {
				return
			}
			if binary.LittleEndian.Uint16(hdr[0:2]) == 0x0065 {
				// 只回会话注册，让客户端认为连接建好了。
				if _, err := c.Write(buildEncapFrame(0x0065, 0x00000001, payload)); err != nil {
					return
				}
			}
			// 其余（含 SendRRData）收下即丢弃：客户端会一直等在 Receive 上。
		}
	})
}

// respDialect CIP 0x4C 读响应方言：欧姆龙与 AB 在通用状态后的字段布局不同。
type respDialect int

const (
	respOmron respDialect = iota
	respAB
)

func handleCIPConn(conn net.Conn, dialect respDialect, latency time.Duration, tags []CIPTag) {
	hdr := make([]byte, 24)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		length := int(binary.LittleEndian.Uint16(hdr[2:4]))
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		cmd := binary.LittleEndian.Uint16(hdr[0:2])
		session := binary.LittleEndian.Uint32(hdr[4:8])
		var resp []byte
		switch cmd {
		case 0x0065: // Register Session：分配会话句柄 1，载荷回显
			resp = buildEncapFrame(0x0065, 0x00000001, payload)
		case 0x006F: // SendRRData：按请求 CPF 回同构响应
			resp = cipSendRRDataResp(session, payload, dialect, tags)
		case 0x0066: // Unregister Session：关闭
			return
		default:
			return
		}
		if latency > 0 {
			time.Sleep(latency)
		}
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// cipSendRRDataResp 构造 SendRRData 响应：解析请求 CPF 中的 0x00B2 数据项，
// 按 CIP 服务码与路径分派：
//
//	0x03 且路径寻址 Symbol Object（类 0x6B）→ 类属性 / 标签属性（标签枚举）
//	0x0A                                    → 0x8A 拼接各子响应（欧姆龙专属）
//	**其余（含单 0x4C 读）**                 → 回 0xCC（今天的行为，一个字没改）
//
// 0x03 那条**只认类 0x6B 的路径**：任何一个别的 Get_Attribute_List 都会落到
// default 上，与加这个分支之前完全一致。这是既有的 parity 黄金与 loadtest
// 不受影响的前提（tags 为 nil 时更是连类属性都不会被问到）。
func cipSendRRDataResp(session uint32, req []byte, dialect respDialect, tags []CIPTag) []byte {
	cipReq := extractCIPItem(req)
	var cipResp []byte
	switch {
	case isSymbolAttributeRequest(cipReq):
		cipResp = buildCIPSymbolResp(cipReq, tags)
	case len(cipReq) > 0 && cipReq[0] == 0x0A:
		cipResp = buildCIPMultiReadResp(cipReq, dialect)
	default:
		cipResp = buildCIPSingleReadResp(cipReq, dialect)
	}

	body := make([]byte, 8) // 接口句柄(4) + 超时(2) + 项数(2，回填)
	binary.LittleEndian.PutUint16(body[6:8], 2)
	// 地址项：Null（0x0000，长度 0）
	body = append(body, 0x00, 0x00, 0x00, 0x00)
	// 数据项：0x00B2 + 长度 + CIP 响应
	body = append(body, 0xB2, 0x00)
	body = append(body, byte(len(cipResp)), byte(len(cipResp)>>8))
	body = append(body, cipResp...)
	return buildEncapFrame(0x006F, session, body)
}

// extractCIPItem 解析 SendRRData 载荷（CPF），返回 0x00B2 数据项中的 CIP 请求字节。
// 载荷 = 接口句柄(4) | 超时(2) | 项数(2 LE) | 项...（每项: 类型(2 LE) | 长度(2 LE) | 载荷）
func extractCIPItem(cpf []byte) []byte {
	if len(cpf) < 8 {
		return nil
	}
	itemCount := int(binary.LittleEndian.Uint16(cpf[6:8]))
	offset := 8
	for i := 0; i < itemCount && offset+4 <= len(cpf); i++ {
		itemType := binary.LittleEndian.Uint16(cpf[offset : offset+2])
		itemLen := int(binary.LittleEndian.Uint16(cpf[offset+2 : offset+4]))
		offset += 4
		if offset+itemLen > len(cpf) {
			return nil
		}
		if itemType == 0x00B2 {
			return cpf[offset : offset+itemLen]
		}
		offset += itemLen
	}
	return nil
}

// buildCIPSingleReadResp 单 0x4C 读响应。
//
// ElementCount 取 0x4C 请求末 2 字节 LE（请求 = 服务码 | 路径字数 | tag path | 元素数）：
// 欧姆龙单读/多服务子读请求元素数恒为 1，响应 2 字节数据与历史行为一致；
// 罗克韦尔数组 Read Tag Elements 请求 N 个元素，回 N×2 字节 INT 确定性数据
// （第 i 个元素 LE 编码 i%1000），保证数组合并读路径端到端可测。
//
// 方言差异（见 NewCIP/NewCIPAB 注释）：
//
//	欧姆龙：CC | 00 | 状态 | 类型码(2) | 数据
//	AB    ：CC | 00 | 状态 | 扩展状态字数(1) | 类型码(2) | 数据
func buildCIPSingleReadResp(cipReq []byte, dialect respDialect) []byte {
	count := 1
	if len(cipReq) >= 2 {
		count = int(binary.LittleEndian.Uint16(cipReq[len(cipReq)-2:]))
		if count < 1 {
			count = 1
		}
	}
	resp := []byte{
		0xCC, 0x00, 0x00, // 服务回显 + 保留 + 通用状态 0
	}
	if dialect == respAB {
		resp = append(resp, 0x00) // 扩展状态字数 0
	}
	resp = append(resp, 0xC3, 0x00) // 数据类型码：INT（2 字节）
	for i := 0; i < count; i++ {
		v := uint16(i % 1000)
		resp = append(resp, byte(v), byte(v>>8))
	}
	return resp
}

// buildCIPMultiReadResp 0x0A 多服务读响应：0x8A | 服务数(1) | [0x4C 子响应] x N。
// 每个子响应与单读一致（元素数 1 → 2 字节数据）；子响应间无长度分隔，
// 驱动按各标签固定尺寸切分。
func buildCIPMultiReadResp(cipReq []byte, dialect respDialect) []byte {
	count := int(cipReq[1])
	resp := []byte{0x8A, byte(count)}
	for i := 0; i < count; i++ {
		resp = append(resp, buildCIPSingleReadResp(nil, dialect)...)
	}
	return resp
}

// ==================== Symbol Object（类 0x6B）请求与应答 ====================
//
// 这一段是**独立于插件实现**的另一份编码（见 CIPTag 的说明），因此拿它去测插件侧
// 的标签枚举是一次真正的交叉验证：两边在同一个字节上写错（如类型码写成大端）
// 会两边一起通过、现场一起失败。

// CIP 逻辑段的编码位。与 goindustrial cip/path.go 的常量同值，
// 但在这里独立写一遍 —— 理由同上。
const (
	cipSegmentLogical      byte = 0x20 // 001xxxxx
	cipLogicalTypeClass    byte = 0x00 // 段类型：类
	cipLogicalTypeInstance byte = 0x04 // 段类型：实例
	cipSymbolClass         byte = 0x6B // Symbol Object 的类号

	// cipServiceGetAttrList Get_Attribute_List 服务码。
	cipServiceGetAttrList byte = 0x03

	// symbolClassRevision 类属性 1（Revision）回的值。真控制器上是 1，恒为 1。
	symbolClassRevision uint16 = 1

	// symbolStatusPathDestinationUnknown 通用状态：路径指向的实例不存在。
	//
	// 真控制器上"这个实例号已经没有标签了"就是这个状态（符号实例号不复用，
	// 删掉一个标签会留下空洞），枚举方据此跳过 —— 所以它是常态而不是错误。
	symbolStatusPathDestinationUnknown byte = 0x05
)

// symbolPathInstance 解析 CIP 消息路由路径，判断它是否寻址 Symbol Object。
//
// 路径是一串逻辑段，每个段头的高三位标识段类型、次三位标识寻址对象、低两位标识
// 值的宽度：
//
//	类段    0x20 | 0x6B            8 位格式：段头 + 1 字节值
//	        0x21 | 0x00 | lo hi    16 位格式：段头 + 1 字节对齐填充 + 2 字节值
//	实例段  0x24 / 0x25 / 0x26     同上，宽度分别 1 / 2 / 4 字节
//
// 认不出来的一律返回 false：不是逻辑段、宽度位非法、越界、压根没有类段。
// **宁可不认**，也不能把一个别的 CIP 请求当成标签枚举 —— 那会让这个假 PLC
// 在无关的用例里回一份莫名其妙的应答。
func symbolPathInstance(path []byte) (uint32, bool) {
	class := -1
	var instance uint32

	for off := 0; off < len(path); {
		seg := path[off]
		if seg&0xE0 != cipSegmentLogical {
			return 0, false
		}
		kind := seg & 0x1C
		off++

		var width int
		switch seg & 0x03 {
		case 0:
			width = 1
		case 1:
			width = 2
		case 2:
			width = 4
		default:
			return 0, false
		}
		if width > 1 {
			off++ // 16/32 位格式前有一个对齐填充字节
		}
		if off+width > len(path) {
			return 0, false
		}
		var v uint32
		for i := 0; i < width; i++ {
			v |= uint32(path[off+i]) << (8 * uint(i))
		}
		off += width

		switch kind {
		case cipLogicalTypeClass:
			class = int(v)
		case cipLogicalTypeInstance:
			instance = v
		default:
			return 0, false
		}
	}
	return instance, class == int(cipSymbolClass)
}

// cipRequestPath 取出 CIP 请求里的路径字节。
//
// 请求布局：服务码(1) | 路径字数(1，**单位是 16 位字**) | 路径 | 请求数据
func cipRequestPath(cipReq []byte) []byte {
	if len(cipReq) < 2 {
		return nil
	}
	n := int(cipReq[1]) * 2
	if 2+n > len(cipReq) {
		return nil
	}
	return cipReq[2 : 2+n]
}

// isSymbolAttributeRequest 这条 CIP 请求是不是"对 Symbol Object 取属性表"。
func isSymbolAttributeRequest(cipReq []byte) bool {
	if len(cipReq) < 2 || cipReq[0] != cipServiceGetAttrList {
		return false
	}
	_, ok := symbolPathInstance(cipRequestPath(cipReq))
	return ok
}

// buildCIPSymbolResp 构造 Get_Attribute_List 应答。
//
//	实例 0   → 类属性：Revision(1) 与 Max Instance(2)。枚举要先问它才知道循环到几号。
//	实例 N   → 该标签的 Name(1) 与 Type(2)。
//	越界实例 → 通用状态 0x05，不带数据（真机的"这个号没有标签了"）。
//
// 应答布局：服务码|0x80 | 保留 | 通用状态 | 扩展状态字数 | 数据
// 数据（属性表）：项数(2 LE) | [属性号(2 LE) | 状态(2 LE) | 值] × 项数
// 其中 Name 的"值"是 STRING：长度(2 LE) + 字符。
func buildCIPSymbolResp(cipReq []byte, tags []CIPTag) []byte {
	instance, _ := symbolPathInstance(cipRequestPath(cipReq))

	var data []byte
	status := byte(0)
	switch {
	case instance == 0:
		data = buildSymbolClassData(len(tags))
	case int(instance) <= len(tags):
		data = buildSymbolTagData(tags[instance-1])
	default:
		status = symbolStatusPathDestinationUnknown
	}

	resp := []byte{cipServiceGetAttrList | 0x80, 0x00, status, 0x00}
	return append(resp, data...)
}

// buildSymbolClassData 类属性表：Revision 与 Max Instance。
func buildSymbolClassData(tagCount int) []byte {
	data := binary.LittleEndian.AppendUint16(nil, 2) // 项数
	// 属性 1：Revision
	data = binary.LittleEndian.AppendUint16(data, 1)
	data = binary.LittleEndian.AppendUint16(data, 0)
	data = binary.LittleEndian.AppendUint16(data, symbolClassRevision)
	// 属性 2：Max Instance —— 实例号从 1 起，所以就是标签个数。
	data = binary.LittleEndian.AppendUint16(data, 2)
	data = binary.LittleEndian.AppendUint16(data, 0)
	return binary.LittleEndian.AppendUint16(data, uint16(tagCount))
}

// buildSymbolTagData 单个标签的属性表：Name 与 Type。
func buildSymbolTagData(tag CIPTag) []byte {
	name := []byte(tag.Name)
	data := binary.LittleEndian.AppendUint16(nil, 2) // 项数
	// 属性 1：Name（STRING）
	data = binary.LittleEndian.AppendUint16(data, 1)
	data = binary.LittleEndian.AppendUint16(data, 0)
	data = binary.LittleEndian.AppendUint16(data, uint16(len(name)))
	data = append(data, name...)
	// 属性 2：Type
	data = binary.LittleEndian.AppendUint16(data, 2)
	data = binary.LittleEndian.AppendUint16(data, 0)
	return binary.LittleEndian.AppendUint16(data, tag.Type)
}

// buildEncapFrame 组装 24 字节封装头 + 载荷。
func buildEncapFrame(cmd uint16, session uint32, payload []byte) []byte {
	frame := make([]byte, 0, 24+len(payload))
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint16(hdr[0:2], cmd)
	binary.LittleEndian.PutUint16(hdr[2:4], uint16(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], session)
	return append(append(frame, hdr...), payload...)
}
