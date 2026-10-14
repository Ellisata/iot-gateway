// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"fmt"
	"net"
)

// ==================== EtherNet/IP ListIdentity（设备发现） ====================
//
// ListIdentity 是 EtherNet/IP 的**标准发现机制**（EtherNet/IP spec vol.2，
// Encapsulation 命令 0x0063）：向 44818/UDP 发一条空载荷请求，网段内的设备各自
// 回一条自述身份（厂商、设备类型、产品名、版本、序列号、通信地址）。
//
// 它属于**厂商无关**的封装层，因此和 Register Session / SendRRData 一起放在
// cipcore，而不是塞进 omroncipdrv。
//
// **注意：Rockwell 插件用的是它自己那份副本，不是本包。** 两边刻意不共享 ——
// 共用一份源码会让两个插件的构建互相牵连（对方目录一改名，这边报一句
// `package not found`），而插件在本仓库里是要能独立改名/删除/拆仓库的产物。
// 代价是两份实现可能漂移，护栏是两边的解析器测试都由同一套独立实现的假应答
// （testutil/fake/cipidentity.go）驱动。完整论证见
// driversrc/rockwellcip/cipcore/listidentity.go 的文件头。
//
// 字节序是本文件最容易错的地方，也是唯一一处**混用**的地方：
//
//	封装头与 Identity 项的字段      小端（LE）
//	Socket Address 里的端口与 IP    大端（网络序）
//
// 这条不对称来自 EtherNet/IP 规范本身（Socket Address 直接搬了 BSD socket 结构），
// 而写错的症状是"端口被识别成 57097"或"IP 变成 168.192.x.x"——
// 看着像个设备，只是地址是错的。listidentity_test.go 里逐字节钉住了它。

// CmdListIdentity EtherNet/IP 封装层命令码：列出身份。
const CmdListIdentity = 0x0063

// ItemIdentity CIP Identity Item 的项类型码（EtherNet/IP spec vol.2, Table 2-6.1）。
const ItemIdentity = 0x000C

// VendorOmron 欧姆龙在 ODVA 的厂商编号。
//
// 本插件的扫描只上报这个厂商的设备：**这不是过滤洁癖，而是正确性要求** ——
// 一键建出来的设备会用 Omron.CIP 协议去采，而 Omron.CIP 说的是欧姆龙的
// 0x4C 数据表读取与变量寻址。把一台 Allen-Bradley 或西门子的 EtherNet/IP
// 设备报成"欧姆龙 PLC"，用户会建出一台永远采不到值的设备。
// 其它厂商的 EtherNet/IP 设备是各自插件的事（见 driversrc/rockwellcip）。
const VendorOmron = 47

// BuildListIdentity 构建 ListIdentity 请求。
//
// 载荷为空、会话句柄为 0：这条命令是**无状态**的，发现阶段还没有会话。
// 同一个请求既可走 UDP（广播或单播）也可走 TCP，应答格式一致。
func BuildListIdentity() []byte {
	return BuildEncapHeader(CmdListIdentity, 0, 0)
}

// Identity 一台 EtherNet/IP 设备自述的身份。
//
// 全部字段都来自设备自己的应答（ListIdentity 就是"设备自报家门"），
// 因此这里填进 DiscoveredDevice 的 Vendor/Model/Version 是**有依据的**，
// 与 FINS/S7 那种"协议应答里根本没有厂商字段、只能留空"的情形不同。
type Identity struct {
	// EncapVersion 设备实现的 EtherNet/IP 封装协议版本。
	EncapVersion uint16

	VendorID    uint16
	DeviceType  uint16
	ProductCode uint16

	// RevisionMajor/Minor 设备固件版本（如 3.1）。
	RevisionMajor byte
	RevisionMinor byte

	// Status 设备状态字。非 0 表示设备自己报了异常（如已进入故障状态）。
	Status uint16

	// SerialNumber 设备序列号（32 位）。
	SerialNumber uint32

	// ProductName 产品名（如 "NJ501-1300"）。设备自述，可能为空。
	ProductName string

	// State 设备状态字节（0x00..0xFF，含义随设备类型）。
	State byte

	// IP/Port 设备自述的通信地址（Socket Address 项）。
	//
	// **不能无条件信它**：部分设备（尤其是尚未拿到 IP、或走 NAT 的设备）
	// 会填 0.0.0.0。调用方应当在它为零值时回落到数据报的源地址。
	IP   string
	Port int
}

// ParseListIdentity 解析一条 ListIdentity 应答，返回其中全部 Identity 项。
//
// 一次应答**可以包含多个 Identity 项**（规范允许，真机上的多口设备也会），
// 因此返回切片而不是单值。
//
// 结构性问题（帧太短、项长度越界、命令码不对）返回 error；单个 Identity 项
// 自身字段不足时**跳过该项**而不是让整条应答失败 —— 一条坏项不该让同一帧里
// 其它能用的设备一起消失，与 probe 逐条失败不中断整批的契约一致。
func ParseListIdentity(resp []byte) ([]Identity, error) {
	cmd, _, status, data, err := ParseEncapResponse(resp)
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return nil, fmt.Errorf("cip: list identity status 0x%08X", status)
	}
	if cmd != CmdListIdentity {
		return nil, fmt.Errorf("cip: unexpected list identity response command 0x%04X", cmd)
	}
	return parseIdentityItems(data)
}

// parseIdentityItems 解析项列表（载荷 = 项数(2 LE) + 项…）。
func parseIdentityItems(data []byte) ([]Identity, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("cip: short list identity payload (%d bytes)", len(data))
	}
	count := int(binary.LittleEndian.Uint16(data[0:2]))

	var out []Identity
	offset := 2
	for i := 0; i < count; i++ {
		if offset+4 > len(data) {
			return nil, fmt.Errorf("cip: list identity item %d header truncated", i)
		}
		itemType := binary.LittleEndian.Uint16(data[offset : offset+2])
		itemLen := int(binary.LittleEndian.Uint16(data[offset+2 : offset+4]))
		offset += 4
		if itemLen < 0 || offset+itemLen > len(data) {
			return nil, fmt.Errorf("cip: list identity item %d length %d truncated", i, itemLen)
		}
		body := data[offset : offset+itemLen]
		offset += itemLen

		// 只认 Identity 项。规范里还有类型 0x0100 的 Socket Address 项等，
		// 跳过它们而不是报错 —— 多一类项不是错误。
		if itemType != ItemIdentity {
			continue
		}
		id, ok := parseIdentityItem(body)
		if !ok {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// parseIdentityItem 解析一个 Identity 项的项体。字段不足时返回 false。
//
// 项体布局（EtherNet/IP spec vol.2, Table 2-6.6），偏移均相对项体起点：
//
//	0  : 封装协议版本(2 LE)
//	2  : Socket Address(16)
//	18 : 厂商 ID(2 LE)
//	20 : 设备类型(2 LE)
//	22 : 产品代码(2 LE)
//	24 : 版本 major(1) + minor(1)
//	26 : 状态字(2 LE)
//	28 : 序列号(4 LE)
//	32 : 产品名（1 字节长度 + 字符，总长补齐到偶数）
//	32+产品名 : 状态字节(1)
const identityFixedLen = 32

func parseIdentityItem(body []byte) (Identity, bool) {
	if len(body) < identityFixedLen+1 { // +1：产品名的长度字节
		return Identity{}, false
	}

	var id Identity
	id.EncapVersion = binary.LittleEndian.Uint16(body[0:2])
	id.IP, id.Port = parseSocketAddress(body[2:18])
	id.VendorID = binary.LittleEndian.Uint16(body[18:20])
	id.DeviceType = binary.LittleEndian.Uint16(body[20:22])
	id.ProductCode = binary.LittleEndian.Uint16(body[22:24])
	id.RevisionMajor = body[24]
	id.RevisionMinor = body[25]
	id.Status = binary.LittleEndian.Uint16(body[26:28])
	id.SerialNumber = binary.LittleEndian.Uint32(body[28:32])

	// 产品名：1 字节长度 + n 个字符（SHORT_STRING）。
	nameLen := int(body[identityFixedLen])
	nameEnd := identityFixedLen + 1 + nameLen
	if nameEnd > len(body) {
		// 长度字段比项体还长：**整项作废**而不是截断取用。
		// 截出来的名字可能只差几个字符，用户看不出来 —— 而型号错了
		// 比型号空着更糟。
		return Identity{}, false
	}
	id.ProductName = string(body[identityFixedLen+1 : nameEnd])

	// 状态字节紧随产品名之后。规范要求产品名总长（含长度字节）补齐到偶数，
	// 但真机并不都遵守，因此这里按**实际**长度取，越界就不填。
	if nameEnd < len(body) {
		id.State = body[nameEnd]
	} else if nameEnd+1 < len(body) {
		id.State = body[nameEnd+1]
	}
	return id, true
}

// parseSocketAddress 解析 16 字节 Socket Address，返回 IP 与端口。
//
// **端口与 IP 是网络序（大端）**，而同一项里其它字段都是小端 —— 见文件头的说明。
// 结构：sin_family(2 LE=2) | sin_port(2 BE) | sin_addr(4) | sin_zero(8)。
func parseSocketAddress(b []byte) (string, int) {
	if len(b) < 16 {
		return "", 0
	}
	port := int(binary.BigEndian.Uint16(b[2:4]))
	ip := net.IPv4(b[4], b[5], b[6], b[7])
	if ip.Equal(net.IPv4zero) {
		// 设备没填地址（尚未取到 IP / 走 NAT）：交给调用方回落。
		return "", port
	}
	return ip.String(), port
}
