// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/driversdk"
	"iot-gateway/pluginwire"
)

func sampleAddrs(n int) []driversdk.WireAddr {
	addrs := make([]driversdk.WireAddr, n)
	for i := range addrs {
		addrs[i] = driversdk.WireAddr{
			ID:       fmt.Sprintf("0192f3a1-0000-7000-8000-%012d", i),
			Name:     fmt.Sprintf("point_%d", i),
			DataType: "int16",
		}
	}
	return addrs
}

// sampleReply 造一批应答。**刻意混入三种坏子码**（值空、质量码各不相同），
// 否则整条 round-trip 比对两侧都是零值，encodeReadResult 漏写 Quality 不会被发现 ——
// 症状会是插件侧的质量码在生产上静默消失，而测试全绿。
func sampleReply(n int) driversdk.ReadReply {
	res := make([]driver.ReadResult, n)
	for i := range res {
		r := driver.ReadResult{
			DeviceAddressID: fmt.Sprintf("0192f3a1-0000-7000-8000-%012d", i),
			Value:           fmt.Sprintf("%d.5", 1000+i),
			DataType:        "int16",
			Kind:            driver.KindInt,
			Quality:         driver.QualityGood,
		}
		switch i % 4 {
		case 0: // 地址级：值空、坏子码
			r.Value, r.Quality = "", driver.QualityBadAddress
		case 1: // 通信级
			r.Value, r.Quality = "", driver.QualityBadComms
		case 2: // 解析级
			r.Value, r.Quality = "", driver.QualityBadParse
		}
		res[i] = r
	}
	return driversdk.ReadReply{Results: res, Connected: true}
}

// TestBinaryRejectsLegacyPayloadWithCause 钉住"成因那一维真的从载荷里消失了"。
//
// v3 的载荷比 v4 每条记录多一个字符串字段（成因），而它最常见的取值是空串 ——
// 在长度前缀编码下恰好只占 1 个字节。所以只要往合法载荷的**末尾**补一个 0x00，
// 得到的就是一份"旧版写出来的、成因全为空"的字节流。它必须**有声地失败**：
// 解码端的 Done() 会因尾随字节报错。
//
// 这条是 APIVersion 3→4 在源码层面的守卫：谁把成因字段加回去（哪怕只加个空串），
// 都会让它变红 —— 而它变红正是"新旧插件无法互相解读、必须整体重编"那句话的证据。
func TestBinaryRejectsLegacyPayloadWithCause(t *testing.T) {
	reply := driversdk.ReadReply{Connected: true, Results: []driver.ReadResult{{
		DeviceAddressID: "addr-1",
		DataType:        "int16",
		Kind:            driver.KindInt,
		Quality:         driver.QualityBadAddress,
	}}}

	enc := reply.EncodeBinary()

	var got driversdk.ReadReply
	if err := got.DecodeBinary(enc); err != nil {
		t.Fatalf("DecodeBinary: %v", err)
	}
	if got.Results[0].Quality != driver.QualityBadAddress {
		t.Fatalf("Quality = %d, want %d —— 线协议丢了质量码", got.Results[0].Quality, driver.QualityBadAddress)
	}

	// 末尾补一个空串长度前缀 = 旧版每条记录多出来的那个成因字段。
	legacy := append(append([]byte{}, enc...), 0x00)
	if err := got.DecodeBinary(legacy); err == nil {
		t.Fatal("带成因字段的旧格式载荷应解码失败，却成功了 —— Done() 没守住尾随字节？")
	}
}

// TestBinaryRoundTripMatchesJSON 是这套编码的**等价性基线**：
// 同一份数据，二进制解出来的必须与 JSON 解出来的逐字段相同。
// 任何一处的字段顺序/多写少写都会在这里现形。
func TestBinaryRoundTripMatchesJSON(t *testing.T) {
	for _, n := range []int{0, 1, 2, 17} {
		// ---- ReadParams ----
		params := driversdk.ReadParams{Session: 7, Addrs: sampleAddrs(n)}

		viaJSON, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		viaBin := params.EncodeBinary()

		var fromJSON, fromBin driversdk.ReadParams
		if err := json.Unmarshal(viaJSON, &fromJSON); err != nil {
			t.Fatalf("unmarshal JSON: %v", err)
		}
		if err := fromBin.DecodeBinary(viaBin); err != nil {
			t.Fatalf("DecodeBinary(n=%d): %v", n, err)
		}
		if fromBin.Session != params.Session || len(fromBin.Addrs) != len(params.Addrs) {
			t.Fatalf("n=%d 会话/点位数不符: %+v", n, fromBin)
		}
		for i := range params.Addrs {
			if fromBin.Addrs[i] != params.Addrs[i] {
				t.Errorf("n=%d 点位[%d] = %+v, want %+v", n, i, fromBin.Addrs[i], params.Addrs[i])
			}
		}
		if len(fromJSON.Addrs) != len(fromBin.Addrs) {
			t.Errorf("n=%d 二进制与 JSON 解出的点位数不一致", n)
		}

		// ---- ReadReply ----
		reply := sampleReply(n)
		viaJSON, err = json.Marshal(reply)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		viaBin = reply.EncodeBinary()

		var rj, rb driversdk.ReadReply
		if err := json.Unmarshal(viaJSON, &rj); err != nil {
			t.Fatalf("unmarshal JSON: %v", err)
		}
		if err := rb.DecodeBinary(viaBin); err != nil {
			t.Fatalf("DecodeBinary reply(n=%d): %v", n, err)
		}
		if rb.Connected != reply.Connected || len(rb.Results) != len(reply.Results) {
			t.Fatalf("n=%d 应答头不符: %+v", n, rb)
		}
		for i := range reply.Results {
			if rb.Results[i] != reply.Results[i] {
				t.Errorf("n=%d 结果[%d] = %+v, want %+v", n, i, rb.Results[i], reply.Results[i])
			}
		}
		if len(rj.Results) != len(rb.Results) {
			t.Errorf("n=%d 二进制与 JSON 解出的结果数不一致", n)
		}
	}
}

// TestBinaryHandlesEmptyAndUnicode 空字符串与多字节 UTF-8 都要原样往返。
// 长度前缀按**字节**计数，按符文数写就会在这条上崩。
func TestBinaryHandlesEmptyAndUnicode(t *testing.T) {
	reply := driversdk.ReadReply{
		Connected: false,
		Results: []driver.ReadResult{
			{DeviceAddressID: "", Value: "", DataType: "", Kind: "", Quality: driver.QualityBad},
			{DeviceAddressID: "点位一", Value: "温度 12.5℃", DataType: "int16", Kind: driver.KindInt, Quality: driver.QualityGood},
		},
	}
	var got driversdk.ReadReply
	if err := got.DecodeBinary(reply.EncodeBinary()); err != nil {
		t.Fatalf("DecodeBinary: %v", err)
	}
	for i := range reply.Results {
		if got.Results[i] != reply.Results[i] {
			t.Errorf("结果[%d] = %+v, want %+v", i, got.Results[i], reply.Results[i])
		}
	}
	if got.Connected {
		t.Error("Connected 应原样回传 false")
	}
}

// TestBinaryRejectsMalformed 固化「畸形输入必须报错而不是解出一堆零值」。
//
// 这一条比往返测试更要紧：二进制没有 JSON 的自描述边界，错位之后每个字段
// 都还能"读出个东西来"，静默解出零值的结果会带着错值一路推到下游。
func TestBinaryRejectsMalformed(t *testing.T) {
	valid := driversdk.ReadParams{Session: 1, Addrs: sampleAddrs(3)}.EncodeBinary()

	cases := []struct {
		name string
		body []byte
	}{
		{"空", nil},
		{"只有 session", valid[:1]},
		{"点位中途截断", valid[:len(valid)-4]},
		{"尾随多余字节", append(append([]byte{}, valid...), 0x00)},
		{"session 是畸形 varint", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p driversdk.ReadParams
			if err := p.DecodeBinary(tc.body); err == nil {
				t.Fatalf("畸形载荷应报错，却解出了 %+v", p)
			}
		})
	}
}

// TestBinaryRejectsLengthBomb 长度字段声称的点位数远超载荷实际长度时，
// 必须**在分配内存前**拒绝。没有这道闸，一个 10 字节的畸形帧就能换来
// 一次 TB 级 make —— 而载荷是对端进程送来的。
func TestBinaryRejectsLengthBomb(t *testing.T) {
	// session=1，随后声明 2^40 个点位，然后就没了。
	bomb := []byte{0x01}
	bomb = append(bomb, 0x80, 0x80, 0x80, 0x80, 0x80, 0x20) // 2^40 的 uvarint

	var p driversdk.ReadParams
	if err := p.DecodeBinary(bomb); err == nil {
		t.Fatalf("长度炸弹应被拒绝，却解出了 %d 个点位", len(p.Addrs))
	}
	if len(p.Addrs) > 64 {
		t.Fatalf("拒绝前不应按声明的长度预分配，实际分配了 %d", len(p.Addrs))
	}

	var r driversdk.ReadReply
	if err := r.DecodeBinary(append([]byte{0x01}, bomb[1:]...)); err == nil {
		t.Fatalf("应答侧长度炸弹应被拒绝，却解出了 %d 条结果", len(r.Results))
	}
}

// TestEncodeBodyDispatch 固化类型分发：热路径载荷必须落到二进制，
// 冷路径载荷必须落到 JSON。分发一旦悄悄退化成全 JSON，性能会掉回去
// 而所有功能测试仍然全绿 —— 所以这条断言的是**编码标记**本身。
func TestEncodeBodyDispatch(t *testing.T) {
	hot := []any{
		driversdk.ReadParams{Session: 1},
		driversdk.ReadReply{Connected: true},
	}
	for _, v := range hot {
		_, codec, err := pluginwire.EncodeBody(v)
		if err != nil {
			t.Fatalf("EncodeBody(%T): %v", v, err)
		}
		if codec != pluginwire.CodecBinary {
			t.Errorf("%T 应走二进制，实际 codec=%d —— 多半是 EncodeBinary 被改成了指针接收者", v, codec)
		}
	}

	cold := []any{
		driversdk.ConnectParams{Protocol: "X"},
		driversdk.SessionParams{Session: 1},
		driversdk.PingParams{Protocol: "X"},
		driversdk.ConnectResult{Session: 1},
		struct{}{},
	}
	for _, v := range cold {
		_, codec, err := pluginwire.EncodeBody(v)
		if err != nil {
			t.Fatalf("EncodeBody(%T): %v", v, err)
		}
		if codec != pluginwire.CodecJSON {
			t.Errorf("%T 应走 JSON，实际 codec=%d", v, codec)
		}
	}

	// nil 载荷：空体 + JSON 标记。
	body, codec, err := pluginwire.EncodeBody(nil)
	if err != nil || body != nil || codec != pluginwire.CodecJSON {
		t.Errorf("nil 载荷 = (%v, %d, %v), want (nil, CodecJSON, nil)", body, codec, err)
	}
}

// TestDecodeBodyRejectsMismatch 编码端声明二进制、解码端类型接不住时必须报错。
// 回落 JSON 会把二进制字节当 JSON 解，报出来的错与真实原因相隔十万八千里。
func TestDecodeBodyRejectsMismatch(t *testing.T) {
	var s driversdk.SessionParams
	err := pluginwire.DecodeBody(pluginwire.CodecBinary, []byte{0x01}, &s)
	if err == nil {
		t.Fatal("SessionParams 未实现 BinaryDecoder，应报错")
	}

	var unknown struct{ X int }
	if err := pluginwire.DecodeBody(0x7f, []byte{0x01}, &unknown); err == nil {
		t.Fatal("未知编码标记应报错")
	}

	// 空体不触碰目标值：与「对端没回结果」的既有语义一致。
	target := driversdk.ReadReply{Connected: true}
	if err := pluginwire.DecodeBody(pluginwire.CodecBinary, nil, &target); err != nil {
		t.Fatalf("空体不应报错: %v", err)
	}
	if !target.Connected {
		t.Error("空体不应改动目标值")
	}
}
