// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"encoding/binary"
	"net"
)

// EtherNet/IP ListIdentity（封装层命令 0x0063）的**假应答构造**。
//
// **刻意不复用插件侧的 cipcore 常量与解析器**：这份实现是从规范（EtherNet/IP
// spec vol.2, Table 2-6.6）另写一遍的，因此拿它去测插件侧的解析器是**一次真正
// 的交叉验证** —— 两边都在同一个字段上写错字节序（最容易错的就是 Socket Address
// 里的端口与 IP，它们是网络序而同一项里其它字段是小端）时，测试会一起通过而
// 现场一起失败。独立实现能挡住这件事。
//
// 供各 EtherNet/IP 厂商插件的设备扫描用例使用（Omron.CIP / Rockwell.CIP…），
// 厂商差异只在 VendorID，帧结构完全相同。

const (
	encapHeaderSize = 24
	encapCmdList    = 0x0063 // ListIdentity
	itemCIPIdentity = 0x000C // CIP Identity Item
	itemEncapVer    = 0x0001
)

// CIPIdentity 一台假设备自述的身份。字段含义与 EtherNet/IP 的 Identity Item 一致。
type CIPIdentity struct {
	VendorID     uint16
	DeviceType   uint16
	ProductCode  uint16
	Major        byte
	Minor        byte
	Status       uint16
	SerialNumber uint32
	ProductName  string

	// IP/Port 设备自述的通信地址。IP 为 nil 时写 0.0.0.0（模拟"设备没填地址"）。
	IP   net.IP
	Port int

	// State 状态字节，默认 0。
	State byte
}

// BuildListIdentityResponse 把若干身份打包成一条 ListIdentity 应答。
//
// 一次应答可以带多个 Identity 项（规范允许），因此入参是变参。
func BuildListIdentityResponse(ids ...CIPIdentity) []byte {
	payload := make([]byte, 2, 64)
	binary.LittleEndian.PutUint16(payload, uint16(len(ids)))
	for _, id := range ids {
		body := buildIdentityItem(id)
		item := make([]byte, 4, 4+len(body))
		binary.LittleEndian.PutUint16(item[0:2], itemCIPIdentity)
		binary.LittleEndian.PutUint16(item[2:4], uint16(len(body)))
		payload = append(payload, append(item, body...)...)
	}
	return append(encapHeader(encapCmdList, len(payload)), payload...)
}

// buildIdentityItem 构造一个 Identity 项的项体。
func buildIdentityItem(id CIPIdentity) []byte {
	body := make([]byte, 0, 64)

	// 封装协议版本(2 LE)
	body = append(body, byte(itemEncapVer), byte(itemEncapVer>>8))

	// Socket Address(16)：family(2 LE) | port(2 **BE**) | addr(4) | zero(8)
	sa := make([]byte, 16)
	binary.LittleEndian.PutUint16(sa[0:2], 2) // AF_INET
	binary.BigEndian.PutUint16(sa[2:4], uint16(id.Port))
	ip := id.IP.To4()
	if ip != nil {
		copy(sa[4:8], ip)
	}
	body = append(body, sa...)

	body = appendLE16(body, id.VendorID)
	body = appendLE16(body, id.DeviceType)
	body = appendLE16(body, id.ProductCode)
	body = append(body, id.Major, id.Minor)
	body = appendLE16(body, id.Status)
	body = appendLE32(body, id.SerialNumber)

	// 产品名：长度字节 + 字符，**含长度字节在内补齐到偶数**（SHORT_STRING 的
	// 对齐规则）。这里按规范补齐，因为被测实现必须能同时吃下补齐与未补齐两种
	// 真机形态。
	body = append(body, byte(len(id.ProductName)))
	body = append(body, []byte(id.ProductName)...)
	if len(body)%2 == 1 {
		body = append(body, 0x00)
	}

	body = append(body, id.State)
	return body
}

// encapHeader 构造 24 字节封装头（命令/长度/会话/状态/上下文/选项，除长度外全 0）。
func encapHeader(cmd uint16, length int) []byte {
	h := make([]byte, encapHeaderSize)
	binary.LittleEndian.PutUint16(h[0:2], cmd)
	binary.LittleEndian.PutUint16(h[2:4], uint16(length))
	return h
}

func appendLE16(b []byte, v uint16) []byte {
	return append(b, byte(v), byte(v>>8))
}

func appendLE32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// NewOmronCIPDiscovery 启动一个只应答 ListIdentity 的 UDP 假设备。
//
// 它**只对 ListIdentity 请求应答**：别的数据报一律不理 —— 与真实设备面对
// 非 EtherNet/IP 流量的行为一致，也正是"端口上跑着别的服务不会误报"这条判据
// 的来源。ids 为空时可以用 NewOmronCIPDiscovery(默认身份) 的组合，
// 但更常见的是显式给一份身份，让断言能逐字对上。
func NewOmronCIPDiscovery(ids ...CIPIdentity) (*UDPServer, error) {
	resp := BuildListIdentityResponse(ids...)
	return NewUDPServer(func(req []byte) []byte {
		if len(req) < 2 || binary.LittleEndian.Uint16(req[0:2]) != encapCmdList {
			return nil
		}
		return resp
	})
}

// OmronNJ501 一个典型的欧姆龙 NJ 系列身份，供用例少写几个字段。
func OmronNJ501(ip string, port int) CIPIdentity {
	return CIPIdentity{
		VendorID:     47,
		DeviceType:   0x000C,
		ProductCode:  0x0066,
		Major:        1,
		Minor:        14,
		SerialNumber: 0x12345678,
		ProductName:  "NJ501-1300",
		IP:           net.ParseIP(ip),
		Port:         port,
	}
}

// NewRockwellCIPDiscovery 启动一个只应答 ListIdentity 的 UDP 假设备。
//
// 与 NewOmronCIPDiscovery **逐字相同**，唯一差别是名字 —— 保留两个入口是因为
// 调用点的可读性："这个用例要一台罗克韦尔设备"与"要一台欧姆龙设备"是两件事，
// 共用构造函数会让读用例的人以为厂商号是构造函数决定的（它不是，厂商号在身份里）。
// 帧结构厂商无关，这正是它俩能共用 BuildListIdentityResponse 的原因。
func NewRockwellCIPDiscovery(ids ...CIPIdentity) (*UDPServer, error) {
	resp := BuildListIdentityResponse(ids...)
	return NewUDPServer(func(req []byte) []byte {
		if len(req) < 2 || binary.LittleEndian.Uint16(req[0:2]) != encapCmdList {
			return nil
		}
		return resp
	})
}

// RockwellL83E 一个典型的罗克韦尔 ControlLogix 身份，供用例少写几个字段。
//
// 厂商号 1 = Allen-Bradley（ODVA 分配），是本插件扫描的唯一通过条件。
func RockwellL83E(ip string, port int) CIPIdentity {
	return CIPIdentity{
		VendorID:     1,
		DeviceType:   0x000E, // Programmable Logic Controller
		ProductCode:  0x006F,
		Major:        32,
		Minor:        11,
		SerialNumber: 0xABCDEF01,
		ProductName:  "1756-L83E",
		IP:           net.ParseIP(ip),
		Port:         port,
	}
}
