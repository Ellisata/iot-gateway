// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// startFakeMC 启动本地假 PLC（3E 帧 TCP），收到读请求后按 resp 字节流响应。
// 返回监听器地址端口与请求接收通道。
func startFakeMC(t *testing.T, resp []byte) (int, chan []byte, chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	reqCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		req := make([]byte, 21)
		if _, err := io.ReadFull(conn, req); err != nil {
			errCh <- err
			return
		}
		reqCh <- req
		if _, err := conn.Write(resp); err != nil {
			errCh <- err
			return
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, reqCh, errCh
}

// newTCPTestClient 创建连到 127.0.0.1:port 的 TCP 客户端。
func newTCPTestClient(t *testing.T, port int) mcTransport {
	t.Helper()
	cfg := DefaultMCConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = port
	cfg.Timeout = 2 * time.Second

	client, err := newMCTCPClient(cfg)
	if err != nil {
		t.Fatalf("newMCTCPClient = %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

// TestTCPClientRoundTrip 本地假 PLC 回环验证：请求帧为 21 字节标准 3E 帧，
// 响应按「数据长度 + 结束码 + 数据」正确解析。
func TestTCPClientRoundTrip(t *testing.T) {
	resp := []byte{
		0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, // 子头 + 网络/PC/IO/站（I/O 小端）
		0x06, 0x00, // 响应数据长度 = 结束码(2) + 数据(4)（小端）
		0x00, 0x00, // 结束码：正常
		0x34, 0x12, 0x01, 0x00, // 数据：2 字（0x1234, 0x0001）
	}
	port, reqCh, errCh := startFakeMC(t, resp)
	client := newTCPTestClient(t, port)

	dev, _ := lookupDevice("D")
	data, err := client.Read(dev, 100, 2, false)
	if err != nil {
		t.Fatalf("Read = %v", err)
	}
	if !bytes.Equal(data, []byte{0x34, 0x12, 0x01, 0x00}) {
		t.Errorf("Read data = % X, want 34 12 01 00", data)
	}

	select {
	case req := <-reqCh:
		want := buildReadFrame(dev, 100, 2, false)
		if !bytes.Equal(req, want) {
			t.Errorf("request frame =\n % X\n want\n % X", req, want)
		}
	case err := <-errCh:
		t.Fatalf("server: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not receive request")
	}
}

// TestTCPClientEndCode 假 PLC 返回非 0 结束码（地址范围外）：
// 客户端应返回结束码错误、且不标记断连（连接是通的）。
func TestTCPClientEndCode(t *testing.T) {
	// 结束码非 0 时响应不含数据：响应数据长度 = 2（仅结束码）
	resp := []byte{
		0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x02, 0x00, // 响应数据长度 = 结束码 2（小端）
		0x56, 0xC0, // 结束码：地址范围外（小端 0xC056）
	}
	port, _, _ := startFakeMC(t, resp)
	client := newTCPTestClient(t, port)

	dev, _ := lookupDevice("D")
	_, err := client.Read(dev, 100, 2, false)
	if err == nil || !IsEndCodeError(err) {
		t.Fatalf("Read = %v, want end code error", err)
	}
	if !client.IsConnected() {
		t.Errorf("connection should stay connected on end code error")
	}
}

// TestTCPClientEndCodeWithBody 结束码非 0 **且带数据段**的应答必须整帧读走。
//
// 真机（10.62.100.22:10000）在软元件越界时回的正是这种形状：响应数据长度 11 =
// 结束码(2) + 「出错站信息」9 字节（网络/PC/I-O/站 + 命令回显）。旧实现一看到
// 结束码非 0 就直接返回、不读数据段，那 9 字节留在流里 —— 下一条请求读到的是
// **错位的窗口**（子头不再是 0xD0），报出来的是"unexpected response subheader"，
// 与真正的故障（地址越界）毫无关系，而这条连接此后再也读不到一次成功。
//
// 上面那条 TestTCPClientEndCode 用的是「数据长度 2、没有数据段」的形状，
// 所以它抓不到这个错 —— 两种形状都要有用例。
func TestTCPClientEndCodeWithBody(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	// 第 1 条：软元件越界（0x4031），带 9 字节出错站信息。
	errResp := []byte{
		0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x0B, 0x00, // 响应数据长度 = 结束码(2) + 出错站信息(9)
		0x31, 0x40, // 结束码：指定软元件编号超出范围（小端 0x4031）
		0x00, 0xFF, 0xFF, 0x03, 0x00, 0x01, 0x04, 0x00, 0x00, // 出错站信息 + 命令回显
	}
	// 第 2 条：正常应答（结束码 0 + 1 个字）。只有第 1 条被整帧读走，它才会被解析对。
	okResp := []byte{
		0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x04, 0x00, // 响应数据长度 = 结束码(2) + 数据(2)
		0x00, 0x00,
		0x00, 0x00,
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for _, resp := range [][]byte{errResp, okResp} {
			req := make([]byte, 21)
			if _, err := io.ReadFull(conn, req); err != nil {
				return
			}
			if _, err := conn.Write(resp); err != nil {
				return
			}
		}
	}()

	client := newTCPTestClient(t, ln.Addr().(*net.TCPAddr).Port)
	dev, _ := lookupDevice("D")

	_, err = client.Read(dev, 32768, 1, false)
	if err == nil || !IsEndCodeError(err) {
		t.Fatalf("Read = %v, want end code error", err)
	}
	if !client.IsConnected() {
		t.Error("结束码错误只说明这次读取被拒，不该标记断连")
	}

	data, err := client.Read(dev, 100, 1, false)
	if err != nil {
		t.Fatalf("第二条请求必须能正常读到 —— 结束码错误的应答体没读干净会让整条连接错位: %v", err)
	}
	if !bytes.Equal(data, []byte{0x00, 0x00}) {
		t.Errorf("Read data = % X, want 00 00", data)
	}
}
