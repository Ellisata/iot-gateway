// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"bytes"
	"testing"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"
)

// 本文件是假 Logix 控制器那条 Symbol Object（类 0x6B）分支的**交叉验证**：
// 请求由 goindustrial 的构造器生成、应答由 goindustrial 的解码器解回，
// 中间那一段字节全部由本包独立实现（见 cip.go 里 CIPTag 的说明）。
// 两边对同一个字段的理解不一致时，这里会红。

// encodeRequest 把一条消息路由请求编成 CIP 字节（服务码 | 路径字数 | 路径 | 数据）。
func encodeRequest(t *testing.T, req *cip.MessageRouterRequest) []byte {
	t.Helper()
	b, err := req.Encode()
	if err != nil {
		t.Fatalf("编码 CIP 请求失败: %v", err)
	}
	return b
}

// responseData 从假 PLC 的应答里剥出数据区。
//
// 应答布局：服务码|0x80 | 保留 | 通用状态 | 扩展状态字数 | 数据
func responseData(t *testing.T, resp []byte) (status byte, data []byte) {
	t.Helper()
	if len(resp) < 4 {
		t.Fatalf("应答太短：%d 字节", len(resp))
	}
	if want := cipServiceGetAttrList | 0x80; resp[0] != want {
		t.Errorf("服务码回显 = 0x%02X，期望 0x%02X", resp[0], want)
	}
	return resp[2], resp[4:]
}

// ==================== 路径解析 ====================

// TestSymbolPathInstance 逐格式解析路径，并逐条否掉"不是 Symbol Object"的形状。
//
// 用**手写的字节**而不是库的构造器：解析器要认得的是线上的字节，
// 拿编码器生成再喂回来会掩盖"两边一起写错"的情况。
func TestSymbolPathInstance(t *testing.T) {
	cases := []struct {
		name     string
		path     []byte
		instance uint32
		ok       bool
	}{
		{"类8位+实例8位（实例0）", []byte{0x20, 0x6B, 0x24, 0x00}, 0, true},
		{"类8位+实例8位", []byte{0x20, 0x6B, 0x24, 0x07}, 7, true},
		{"类8位+实例8位（满量程）", []byte{0x20, 0x6B, 0x24, 0xFF}, 0xFF, true},
		{"类8位+实例16位", []byte{0x20, 0x6B, 0x25, 0x00, 0x34, 0x12}, 0x1234, true},
		{"类8位+实例32位", []byte{0x20, 0x6B, 0x26, 0x00, 0x78, 0x56, 0x34, 0x12}, 0x12345678, true},
		{"类16位+实例8位", []byte{0x21, 0x00, 0x6B, 0x00, 0x24, 0x03}, 3, true},
		{"没有实例段（默认 0）", []byte{0x20, 0x6B}, 0, true},
		{"没有类段", []byte{0x24, 0x03}, 0, false},
		{"类号不是 0x6B", []byte{0x20, 0x01, 0x24, 0x03}, 0, false},
		{"不是逻辑段（符号段）", []byte{0x91, 0x04, 'T', 'a', 'g', 0x00}, 0, false},
		{"宽度位保留值", []byte{0x20, 0x6B, 0x27, 0x00}, 0, false},
		{"实例值被截断", []byte{0x20, 0x6B, 0x25, 0x00, 0x34}, 0, false},
		{"空路径", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance, ok := symbolPathInstance(tc.path)
			if ok != tc.ok {
				t.Fatalf("symbolPathInstance(% X) ok = %v，期望 %v", tc.path, ok, tc.ok)
			}
			if ok && instance != tc.instance {
				t.Errorf("实例号 = %d，期望 %d", instance, tc.instance)
			}
		})
	}
}

// TestIsSymbolAttributeRequest 服务码不对、或路径不对，都不能被当成标签枚举请求。
//
// 这条是"0x4C 读路径一个字没改"的守门人：假 PLC 一旦把普通读请求误判成枚举，
// 既有的 parity 黄金与 loadtest 会一起莫名其妙地红。
func TestIsSymbolAttributeRequest(t *testing.T) {
	symbol := encodeRequest(t, cip.NewGetSymbolAttributesRequest(1))
	if !isSymbolAttributeRequest(symbol) {
		t.Error("对类 0x6B 的 Get_Attribute_List 没被认出来")
	}

	// 同一个 0x03 服务码，但路径指向别的类。
	other := []byte{cipServiceGetAttrList, 0x02, 0x20, 0x01, 0x24, 0x00}
	if isSymbolAttributeRequest(other) {
		t.Error("路径指向类 0x01 的 Get_Attribute_List 被误认成标签枚举")
	}

	// 0x4C 读标签（罗克韦尔驱动的日常请求）。
	read := []byte{0x4C, 0x04, 0x91, 0x03, 'F', 'o', 'o', 0x00, 0x01, 0x00}
	if isSymbolAttributeRequest(read) {
		t.Error("0x4C 读标签被误认成标签枚举")
	}

	// 0x0A 多服务读（欧姆龙专属）。
	if isSymbolAttributeRequest([]byte{0x0A, 0x02, 0x4C, 0x00}) {
		t.Error("0x0A 多服务读被误认成标签枚举")
	}
	if isSymbolAttributeRequest(nil) {
		t.Error("空请求被认成标签枚举")
	}
}

// ==================== 应答 ====================

// TestSymbolClassResponse 类属性应答能被库解码器解回（revision + max instance）。
func TestSymbolClassResponse(t *testing.T) {
	tags := []CIPTag{{Name: "A", Type: CIPTypeDINT}, {Name: "B", Type: CIPTypeREAL}}
	resp := buildCIPSymbolResp(encodeRequest(t, cip.NewGetSymbolClassAttributesRequest()), tags)

	status, data := responseData(t, resp)
	if status != 0 {
		t.Fatalf("通用状态 = 0x%02X，期望 0", status)
	}
	revision, maxInstance, err := cip.DecodeSymbolClassAttributesResponse(data)
	if err != nil {
		t.Fatalf("解码类属性失败: %v", err)
	}
	if revision != symbolClassRevision {
		t.Errorf("revision = %d，期望 %d", revision, symbolClassRevision)
	}
	if maxInstance != uint16(len(tags)) {
		t.Errorf("max instance = %d，期望 %d（实例号从 1 起，等于标签个数）", maxInstance, len(tags))
	}
}

// TestSymbolTagResponse 标签属性应答能被库解码器解回（名字 + 类型码）。
//
// 覆盖三种实例号宽度：库会按实例号大小选 8/16/32 位编码，三种都得能解。
func TestSymbolTagResponse(t *testing.T) {
	// 名字长度取奇数，顺带钉住"路径/字符串不做对齐填充"这件事。
	tags := []CIPTag{
		{Name: "Odd", Type: CIPTypeBOOL},
		{Name: "EvenName", Type: CIPTypeSTRING},
		{Name: "Arr", Type: CIPTypeLINT | CIPArrayBit},
	}
	for i, want := range tags {
		t.Run(want.Name, func(t *testing.T) {
			resp := buildCIPSymbolResp(encodeRequest(t, cip.NewGetSymbolAttributesRequest(uint32(i+1))), tags)
			status, data := responseData(t, resp)
			if status != 0 {
				t.Fatalf("通用状态 = 0x%02X，期望 0", status)
			}
			name, typeCode, err := cip.DecodeSymbolAttributesResponse(data)
			if err != nil {
				t.Fatalf("解码标签属性失败: %v", err)
			}
			if name != want.Name {
				t.Errorf("名字 = %q，期望 %q", name, want.Name)
			}
			if uint16(typeCode) != want.Type {
				t.Errorf("类型码 = 0x%04X，期望 0x%04X", uint16(typeCode), want.Type)
			}
		})
	}
}

// TestSymbolResponseInstance16Bit 16/32 位实例编码的请求也要能被应答。
//
// 真控制器上实例号超过 255 是常态（符号实例号不复用，只增不减），
// 库也正是在那时切到宽编码 —— 只认 8 位的话，一大类设备根本枚举不下去。
func TestSymbolResponseInstance16Bit(t *testing.T) {
	tags := make([]CIPTag, 300)
	for i := range tags {
		tags[i] = CIPTag{Name: "T", Type: CIPTypeDINT}
	}
	resp := buildCIPSymbolResp(encodeRequest(t, cip.NewGetSymbolAttributesRequest(300)), tags)
	status, data := responseData(t, resp)
	if status != 0 {
		t.Fatalf("通用状态 = 0x%02X，期望 0", status)
	}
	name, typeCode, err := cip.DecodeSymbolAttributesResponse(data)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if name != "T" || uint16(typeCode) != CIPTypeDINT {
		t.Errorf("解出 (%q, 0x%04X)，期望 (\"T\", 0x%04X)", name, uint16(typeCode), CIPTypeDINT)
	}
}

// TestSymbolResponseOutOfRange 越界实例回"路径不存在"且不带数据。
//
// 这是枚举时跳过空洞的信号，不是错误：真控制器删掉标签后实例号会留下空缺。
func TestSymbolResponseOutOfRange(t *testing.T) {
	tags := []CIPTag{{Name: "A", Type: CIPTypeDINT}}
	resp := buildCIPSymbolResp(encodeRequest(t, cip.NewGetSymbolAttributesRequest(2)), tags)
	status, data := responseData(t, resp)
	if status != symbolStatusPathDestinationUnknown {
		t.Errorf("通用状态 = 0x%02X，期望 0x%02X", status, symbolStatusPathDestinationUnknown)
	}
	if len(data) != 0 {
		t.Errorf("越界实例不该带数据，实得 %d 字节", len(data))
	}
}

// TestSymbolResponseEmptyTagList 没有任何标签的控制器：max instance 为 0。
func TestSymbolResponseEmptyTagList(t *testing.T) {
	resp := buildCIPSymbolResp(encodeRequest(t, cip.NewGetSymbolClassAttributesRequest()), nil)
	_, data := responseData(t, resp)
	_, maxInstance, err := cip.DecodeSymbolClassAttributesResponse(data)
	if err != nil {
		t.Fatalf("解码类属性失败: %v", err)
	}
	if maxInstance != 0 {
		t.Errorf("max instance = %d，期望 0", maxInstance)
	}
}

// TestSingleReadPathUnaffected 加了 0x6B 分支之后，0x4C 读那条路一个字都没变。
//
// 这是既有 parity 黄金与 loadtest 不受影响的前提：同一个假 PLC 即使配了标签表，
// 普通读请求也必须走原来的分支。
func TestSingleReadPathUnaffected(t *testing.T) {
	tags := []CIPTag{{Name: "A", Type: CIPTypeDINT}}

	// 一条 0x4C 单读请求（服务码 + 路径字数 + 符号段 + 元素数）。
	readReq := []byte{0x4C, 0x04, 0x91, 0x03, 'F', 'o', 'o', 0x00, 0x01, 0x00}
	cpf := buildCPFWithCIP(readReq)

	got := cipSendRRDataResp(1, cpf, respAB, tags)
	want := cipSendRRDataResp(1, cpf, respAB, nil)
	if !bytes.Equal(got, want) {
		t.Fatalf("配了标签表之后 0x4C 读的应答变了：\n got % X\nwant % X", got, want)
	}

	// 再确认它就是那条老分支的产物（AB 方言：类型码 DINT…这里是固定的 INT 0xC3）。
	if !bytes.Contains(got, []byte{0xC3, 0x00}) {
		t.Errorf("应答里没有单读分支的类型码，实得 % X", got)
	}
}

// buildCPFWithCIP 把一段 CIP 请求包进 SendRRData 的 CPF 载荷里。
//
// 布局与 handleCIPConn 读到的一致：接口句柄(4) | 超时(2) | 项数(2 LE)=2 |
// Null 地址项 | 0x00B2 数据项。
func buildCPFWithCIP(cipReq []byte) []byte {
	body := make([]byte, 8)
	body[7] = 2 // 项数
	body = append(body, 0x00, 0x00, 0x00, 0x00)
	body = append(body, 0xB2, 0x00, byte(len(cipReq)), byte(len(cipReq)>>8))
	return append(body, cipReq...)
}
