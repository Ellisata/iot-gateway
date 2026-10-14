// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	cipcore "iot-gateway/driversrc/omroncip/cipcore"
)

// ==================== 单标签分类 ====================

// TestSingleTagResultClassification 钉住 ReadTags 的契约：设备拒绝 ≠ 读取失败。
//
// 单标签调用退化为 ReadTag，但**不能**因此把通用状态错误升级成函数级错误。
// 这条分支此前直接 return nil, err，与 cipTransport 接口上写明的语义
// （"结果 Err 非 nil 表示该标签被设备拒绝（连接仍通）"）相反。
func TestSingleTagResultClassification(t *testing.T) {
	t.Run("成功", func(t *testing.T) {
		res, err := singleTagResult([]byte{0x64, 0x00}, nil)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(res) != 1 || res[0].Err != nil {
			t.Fatalf("res = %+v, want 单条无错结果", res)
		}
		if string(res[0].Data) != "\x64\x00" {
			t.Errorf("Data = % X, want 64 00", res[0].Data)
		}
	})

	t.Run("设备拒绝_落进逐标签Err", func(t *testing.T) {
		for _, status := range []byte{0x04, 0x05} { // 0x04 标签不存在、0x05 路径目的未知
			general := cipcore.NewGeneralStatusError(status)
			res, err := singleTagResult(nil, general)
			if err != nil {
				t.Errorf("status 0x%02X: err = %v, want nil（设备拒绝不是读取失败）", status, err)
				continue
			}
			if len(res) != 1 {
				t.Fatalf("status 0x%02X: len(res) = %d, want 1", status, len(res))
			}
			if !cipcore.IsGeneralStatusError(res[0].Err) {
				t.Errorf("status 0x%02X: res[0].Err = %v, want GeneralStatusError", status, res[0].Err)
			}
		}
	})

	t.Run("网络错误_函数级上抛", func(t *testing.T) {
		netErr := errors.New("read failed: i/o timeout")
		res, err := singleTagResult(nil, netErr)
		if !errors.Is(err, netErr) {
			t.Errorf("err = %v, want %v（网络错误必须上抛，采集引擎据此判断连重连）", err, netErr)
		}
		if res != nil {
			t.Errorf("res = %+v, want nil", res)
		}
	})
}

// ==================== 走真实套接字的回归 ====================

// buildSendRRDataResponse 构造 SendRRData 响应帧（24 字节封装头 + 载荷），
// 载荷内嵌 cipResp 作为 0x00B2 数据项。
//
//	载荷 = 接口句柄(4) | 超时(2) | 项数(2 LE)=1 | 类型(2 LE)=0x00B2 | 长度(2 LE) | cipResp
func buildSendRRDataResponse(session uint32, cipResp []byte) []byte {
	body := make([]byte, 0, 16+len(cipResp))
	body = append(body, 0, 0, 0, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint16(body[6:8], 1)
	body = append(body, 0xB2, 0x00, 0x00, 0x00)
	binary.LittleEndian.PutUint16(body[len(body)-2:], uint16(len(cipResp)))
	body = append(body, cipResp...)

	h := cipcore.BuildEncapHeader(cipcore.CmdSendRRData, session, len(body))
	return append(h, body...)
}

// connectFakeCIPPeer 起一个只回固定 CIP 应答的假 PLC，返回已连上的客户端。
// cipResp 为 nil 表示"只收不回"（立刻关连接），用于模拟网络错误。
//
// 起真实 TCP 回环而不是 mock 传输层，是因为出问题的那一行就在 cipClient.ReadTags
// 里 —— mock 恰好绕开了它。这个洞此前无人覆盖：mock 测试全绿，真机上整台设备离线。
func connectFakeCIPPeer(t *testing.T, cipResp []byte) *cipClient {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var sess uint32 = 0x055D0170

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // 监听已关，测试收尾
		}
		defer conn.Close()
		if cipResp == nil {
			return
		}
		for {
			hdr := make([]byte, cipcore.EncapHeaderSize)
			if _, err := io.ReadFull(conn, hdr); err != nil {
				return
			}
			length := int(binary.LittleEndian.Uint16(hdr[2:4]))
			if _, err := io.ReadFull(conn, make([]byte, length)); err != nil {
				return
			}
			if binary.LittleEndian.Uint16(hdr[0:2]) != cipcore.CmdSendRRData {
				continue // 只应答显式报文，Register/Unregister 忽略
			}
			if _, err := conn.Write(buildSendRRDataResponse(sess, cipResp)); err != nil {
				return
			}
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		ln.Close()
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(); ln.Close() })

	return &cipClient{conn: conn, timeout: 2 * time.Second, session: sess, connected: true}
}

// TestReadTagsSingleTagDeviceDenied 单标签批量调用（ReadTags 退化为 ReadTag）被设备
// 拒绝时，必须回逐标签 Err 而不是函数级错误。
//
// 回归现场 2026-09-29：10.62.100.16（NX1P2-1140DT）上唯一的点位名为 Recipe.Setpoint，
// 设备回 4 字节错误响应 CC 00 05 00。此前这条路径把 0x05 当函数级错误上抛 →
// driver.Read 返回错误 → 采集引擎置设备离线并反复重连，而 PLC 一直是通的。
// 同一台设备不存在的**扁平**标签名（MotorSpeed）回的是 0x04，两条路径的差别
// 只在于名字里有没有点号。
func TestReadTagsSingleTagDeviceDenied(t *testing.T) {
	c := connectFakeCIPPeer(t, []byte{0xCC, 0x00, 0x05, 0x00})

	res, err := c.ReadTags([]TagSpec{{Name: "Recipe.Setpoint", Code: cipTypeWORD}})
	if err != nil {
		t.Fatalf("设备拒绝该标签不该上抛函数级错误，got: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("len(res) = %d, want 1", len(res))
	}
	if !cipcore.IsGeneralStatusError(res[0].Err) {
		t.Errorf("res[0].Err = %v, want GeneralStatusError(0x05)", res[0].Err)
	}
	if c.IsConnected() != true {
		t.Error("设备拒绝一个标签不是断连，IsConnected 应仍为 true")
	}
}

// TestReadTagsSingleTagNetworkError 反面对照：连不上/读不到时必须上抛函数级错误，
// 不能因为上面那条修正就把所有失败都吞成逐标签 Err —— 那样断连会被静默成"点位质量 0"，
// 采集引擎永远不会触发重连。
func TestReadTagsSingleTagNetworkError(t *testing.T) {
	c := connectFakeCIPPeer(t, nil) // 假 PLC 立刻断开

	if _, err := c.ReadTags([]TagSpec{{Name: "Recipe.Setpoint", Code: cipTypeWORD}}); err == nil {
		t.Fatal("expected 函数级错误 for 网络失败")
	}
	if c.IsConnected() {
		t.Error("读失败后 IsConnected 应为 false（采集引擎据此重连）")
	}
}
