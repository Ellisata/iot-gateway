// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"net"
	"testing"

	"iot-gateway/testutil/fake"
)

// 本文件用 testutil/fake 里**独立实现**的应答构造器来测解析器 ——
// 那份实现是从规范另写一遍的，因此两边不会在同一处字节序上一起错
//（见 fake/cipidentity.go 的文件头说明）。
//
// 本包与 driversrc/omroncip/cipcore 是**两份互不依赖的副本**（理由见
// listidentity.go 的文件头）。它们共用同一套独立实现的假应答，正是防漂移的
// 那道护栏：任何一边在字节序或字段偏移上写错，那边自己的测试就会红。

func TestBuildListIdentity(t *testing.T) {
	req := BuildListIdentity()
	if len(req) != EncapHeaderSize {
		t.Fatalf("请求长度 = %d，期望 %d（空载荷）", len(req), EncapHeaderSize)
	}
	cmd := binary.LittleEndian.Uint16(req[0:2])
	if cmd != CmdListIdentity {
		t.Errorf("命令码 = 0x%04X，期望 0x%04X", cmd, CmdListIdentity)
	}
	if length := binary.LittleEndian.Uint16(req[2:4]); length != 0 {
		t.Errorf("长度字段 = %d，期望 0", length)
	}
	// 发现阶段还没有会话，句柄必须是 0 —— 带上一个陈旧的句柄会被设备拒。
	if session := binary.LittleEndian.Uint32(req[4:8]); session != 0 {
		t.Errorf("会话句柄 = 0x%08X，期望 0", session)
	}
}

// TestParseListIdentity 逐字段核对一台设备自述的全部身份。
func TestParseListIdentity(t *testing.T) {
	want := fake.RockwellL83E("192.168.1.50", 44818)
	resp := fake.BuildListIdentityResponse(want)

	ids, err := ParseListIdentity(resp)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("应解析出 1 项，实际 %d 项", len(ids))
	}

	got := ids[0]
	if got.VendorID != VendorAllenBradley {
		t.Errorf("厂商号 = %d，期望 %d（Allen-Bradley）", got.VendorID, VendorAllenBradley)
	}
	if got.DeviceType != want.DeviceType {
		t.Errorf("设备类型 = 0x%04X，期望 0x%04X", got.DeviceType, want.DeviceType)
	}
	if got.ProductCode != want.ProductCode {
		t.Errorf("产品代码 = 0x%04X，期望 0x%04X", got.ProductCode, want.ProductCode)
	}
	if got.RevisionMajor != 32 || got.RevisionMinor != 11 {
		t.Errorf("版本 = %d.%d，期望 32.11", got.RevisionMajor, got.RevisionMinor)
	}
	if got.SerialNumber != 0xABCDEF01 {
		t.Errorf("序列号 = 0x%08X，期望 0xABCDEF01", got.SerialNumber)
	}
	if got.ProductName != "1756-L83E" {
		t.Errorf("产品名 = %q，期望 1756-L83E", got.ProductName)
	}
	if got.IP != "192.168.1.50" {
		t.Errorf("IP = %q，期望 192.168.1.50", got.IP)
	}
	if got.Port != 44818 {
		t.Errorf("端口 = %d，期望 44818", got.Port)
	}
}

// TestVendorConstantsPinned 厂商号是**两条插件互不重叠的边界**，钉死。
//
// 47 与 1 各错一处，症状都是"一台设备被两个插件同时收下"或"两边都不收"，
// 而扫描页上只会表现为多一行或少一行 —— 没有人能从界面上看出是这里错了。
func TestVendorConstantsPinned(t *testing.T) {
	if VendorAllenBradley != 1 {
		t.Errorf("VendorAllenBradley = %d，期望 1（ODVA 分配给 Allen-Bradley 的编号）",
			VendorAllenBradley)
	}
}

// TestParseListIdentitySocketAddressByteOrder Socket Address 里的端口与 IP 是
// **网络序（大端）**，而同一项里其它字段都是小端。
//
// 这是本协议最容易错的一处，也是唯一一处混用字节序的地方。写错的症状不是报错，
// 而是"IP 变成 168.192.x.x"或"端口被读成 57097"—— 看着像个设备，地址却是错的，
// 用户会照着去建设备然后连不上。这里挑一个**在小端解释下明显不同**的取值钉住它：
// 22.33.44.55 与端口 44818（0xAF12）。
func TestParseListIdentitySocketAddressByteOrder(t *testing.T) {
	id := fake.CIPIdentity{
		VendorID:    VendorAllenBradley,
		ProductName: "X",
		IP:          net.IPv4(22, 33, 44, 55),
		Port:        44818,
	}
	ids, err := ParseListIdentity(fake.BuildListIdentityResponse(id))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("应解析出 1 项，实际 %d 项", len(ids))
	}
	if ids[0].IP != "22.33.44.55" {
		t.Errorf("IP = %q，期望 22.33.44.55 —— Socket Address 里的地址是网络序", ids[0].IP)
	}
	if ids[0].Port != 44818 {
		t.Errorf("端口 = %d，期望 44818 —— Socket Address 里的端口是网络序，"+
			"按小端读会得到 4623", ids[0].Port)
	}
}

// TestParseListIdentityUnspecifiedAddress 设备自述 0.0.0.0 时返回空地址，
// 交给调用方回落到数据报的源地址。
//
// 尚未取到 IP、或走 NAT 的设备会这么填。直接采信它就会建出一台 host=0.0.0.0
// 的设备 —— 而那种设备连不上，且在列表里看着完全正常。
func TestParseListIdentityUnspecifiedAddress(t *testing.T) {
	id := fake.CIPIdentity{VendorID: VendorAllenBradley, ProductName: "X", Port: 44818} // IP 为 nil → 0.0.0.0
	ids, err := ParseListIdentity(fake.BuildListIdentityResponse(id))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("应解析出 1 项，实际 %d 项", len(ids))
	}
	if ids[0].IP != "" {
		t.Errorf("IP = %q，期望空字符串（0.0.0.0 表示设备没填地址）", ids[0].IP)
	}
	if ids[0].Port != 44818 {
		t.Errorf("端口 = %d，期望 44818", ids[0].Port)
	}
}

// TestParseListIdentityMultipleItems 一条应答可以带多项。
func TestParseListIdentityMultipleItems(t *testing.T) {
	resp := fake.BuildListIdentityResponse(
		fake.RockwellL83E("192.168.1.50", 44818),
		fake.RockwellL83E("192.168.1.51", 44818),
		fake.RockwellL83E("192.168.1.52", 44818),
	)
	ids, err := ParseListIdentity(resp)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("应解析出 3 项，实际 %d 项", len(ids))
	}
	for i, want := range []string{"192.168.1.50", "192.168.1.51", "192.168.1.52"} {
		if ids[i].IP != want {
			t.Errorf("第 %d 项的 IP = %q，期望 %q", i, ids[i].IP, want)
		}
	}
}

// TestParseListIdentityEmptyPayload 零项的应答不是错误，返回空列表。
func TestParseListIdentityEmptyPayload(t *testing.T) {
	ids, err := ParseListIdentity(fake.BuildListIdentityResponse())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("应解析出 0 项，实际 %d 项", len(ids))
	}
}

// TestParseListIdentityBadFrames 结构有问题的帧一律报错。
//
// 报错而不是返回空列表：调用方据此判定"这不是 ListIdentity 应答"
// （端口上跑着别的服务），与"应答合法但里面没有设备"是两件事。
func TestParseListIdentityBadFrames(t *testing.T) {
	good := fake.BuildListIdentityResponse(fake.RockwellL83E("192.168.1.50", 44818))

	cases := []struct {
		name  string
		frame []byte
	}{
		{"空帧", nil},
		{"只有半个头", good[:12]},
		{"声明长度超出实际", func() []byte {
			f := append([]byte(nil), good...)
			binary.LittleEndian.PutUint16(f[2:4], 4096)
			return f
		}()},
		{"命令码不是 ListIdentity", func() []byte {
			f := append([]byte(nil), good...)
			binary.LittleEndian.PutUint16(f[0:2], 0x006F) // SendRRData
			return f
		}()},
		{"状态非零", func() []byte {
			f := append([]byte(nil), good...)
			binary.LittleEndian.PutUint32(f[8:12], 0x00000001)
			return f
		}()},
		{"项数声明大于实际", func() []byte {
			f := append([]byte(nil), good...)
			binary.LittleEndian.PutUint16(f[24:26], 9)
			return f
		}()},
		{"项长度超出实际", func() []byte {
			f := append([]byte(nil), good...)
			// 第一个项的 length 字段在 24(载荷起) + 2(项数) + 2(类型码) = 28
			binary.LittleEndian.PutUint16(f[28:30], 4096)
			return f
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseListIdentity(c.frame); err == nil {
				t.Fatalf("应报错，实际把 %d 字节当成了合法应答", len(c.frame))
			}
		})
	}
}

// TestParseListIdentitySkipsBadItem 一项坏掉不该让同一帧里其它设备一起消失。
//
// 与 probe "逐条失败不中断整批"的契约一致：一条字段不足的 Identity 项只影响它
// 自己。产品名的长度字段比项体还长是最现实的坏法（设备截断了应答）。
func TestParseListIdentitySkipsBadItem(t *testing.T) {
	good := fake.BuildListIdentityResponse(fake.RockwellL83E("192.168.1.50", 44818))

	// 手工拼一条"产品名长度大于项体"的坏项，追加在好项后面。
	badBody := make([]byte, 33)
	badBody[32] = 200 // 产品名长度 200，远超项体
	badItem := make([]byte, 4, 4+len(badBody))
	binary.LittleEndian.PutUint16(badItem[0:2], ItemIdentity)
	binary.LittleEndian.PutUint16(badItem[2:4], uint16(len(badBody)))
	badItem = append(badItem, badBody...)

	frame := append([]byte(nil), good...)
	binary.LittleEndian.PutUint16(frame[24:26], 2) // 项数改成 2
	payloadLen := int(binary.LittleEndian.Uint16(frame[2:4])) + len(badItem)
	binary.LittleEndian.PutUint16(frame[2:4], uint16(payloadLen))
	frame = append(frame, badItem...)

	ids, err := ParseListIdentity(frame)
	if err != nil {
		t.Fatalf("一条坏项不该让整帧报错: %v", err)
	}
	if len(ids) != 1 || ids[0].IP != "192.168.1.50" {
		t.Fatalf("好项应被保留、坏项应被跳过，实际 %+v", ids)
	}
}

// TestParseListIdentitySkipsUnknownItemType 非 Identity 项跳过而不是报错。
//
// 规范里还有 Socket Address Item（0x0100）等类型，设备可能一并带上。
// 多一类项不是错误。
func TestParseListIdentitySkipsUnknownItemType(t *testing.T) {
	good := fake.BuildListIdentityResponse(fake.RockwellL83E("192.168.1.50", 44818))

	other := make([]byte, 4, 12)
	binary.LittleEndian.PutUint16(other[0:2], 0x0100) // Socket Address Item
	binary.LittleEndian.PutUint16(other[2:4], 8)
	other = append(other, make([]byte, 8)...)

	frame := append([]byte(nil), good...)
	binary.LittleEndian.PutUint16(frame[24:26], 2)
	payloadLen := int(binary.LittleEndian.Uint16(frame[2:4])) + len(other)
	binary.LittleEndian.PutUint16(frame[2:4], uint16(payloadLen))
	frame = append(frame, other...)

	ids, err := ParseListIdentity(frame)
	if err != nil {
		t.Fatalf("未知项类型不该让整帧报错: %v", err)
	}
	if len(ids) != 1 || ids[0].IP != "192.168.1.50" {
		t.Fatalf("Identity 项应被保留，实际 %+v", ids)
	}
}

// TestParseListIdentityUnpaddedProductName 产品名**未按规范补齐到偶数**时也要能解析。
//
// 规范要求 SHORT_STRING（含长度字节）总长补齐到偶数，但真机并不都遵守。
// 只认补齐后的形态，会让那批设备的型号读不出来 —— 而结果看着只是"型号为空"。
func TestParseListIdentityUnpaddedProductName(t *testing.T) {
	// 造一个长度为偶数的产品名（1 + 5 = 6，已对齐，不补 0）与奇数长度（1 + 4 = 5，需补）两种。
	for _, name := range []string{"L83E", "L83"} {
		t.Run(name, func(t *testing.T) {
			resp := unpaddedIdentityResponse(VendorAllenBradley, name)
			ids, err := ParseListIdentity(resp)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if len(ids) != 1 {
				t.Fatalf("应解析出 1 项，实际 %d 项", len(ids))
			}
			if ids[0].ProductName != name {
				t.Errorf("产品名 = %q，期望 %q", ids[0].ProductName, name)
			}
		})
	}
}

// unpaddedIdentityResponse 构造一条**不做 SHORT_STRING 对齐**的应答。
func unpaddedIdentityResponse(vendor uint16, name string) []byte {
	body := make([]byte, 0, 64)
	body = append(body, 0x01, 0x00)          // 封装协议版本(2 LE)
	body = append(body, make([]byte, 16)...) // Socket Address：全 0
	body = append(body, byte(vendor), byte(vendor>>8))
	body = append(body, 0x0E, 0x00, 0x6F, 0x00) // 设备类型 / 产品代码
	body = append(body, 0x20, 0x0B)             // 版本 32.11
	body = append(body, 0x00, 0x00)             // 状态字
	body = append(body, 0x01, 0xEF, 0xCD, 0xAB) // 序列号
	body = append(body, byte(len(name)))        // 产品名（**不补齐**）
	body = append(body, []byte(name)...)
	body = append(body, 0x00) // 状态字节

	item := make([]byte, 4, 4+len(body))
	binary.LittleEndian.PutUint16(item[0:2], ItemIdentity)
	binary.LittleEndian.PutUint16(item[2:4], uint16(len(body)))
	item = append(item, body...)

	payload := make([]byte, 2, 2+len(item))
	binary.LittleEndian.PutUint16(payload, 1)
	payload = append(payload, item...)

	header := make([]byte, EncapHeaderSize)
	binary.LittleEndian.PutUint16(header[0:2], CmdListIdentity)
	binary.LittleEndian.PutUint16(header[2:4], uint16(len(payload)))
	return append(header, payload...)
}
