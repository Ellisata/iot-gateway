// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"
)

// hostLinkTCPServer 本地 TCP 假「串口服务器 + PLC」：接收 Host Link 请求帧，
// 回显同一 SID 构造响应帧（透传语义与真实串口服务器一致）。
func hostLinkTCPServer(t *testing.T, respond func(sid byte, body []byte) []byte) (string, func() error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				req, err := readHostLinkFrame(conn)
				if err != nil {
					return
				}
				// 请求帧：@ 单元 FA 等待 + hex 体，body[3] 为 SID
				body, err := hex.DecodeString(string(req[6 : len(req)-4]))
				if err != nil {
					return
				}
				resp := respond(body[3], body)
				if len(resp) > 0 {
					conn.Write(resp)
				}
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr).String()
	closeFn := func() error {
		ln.Close()
		<-done
		return nil
	}
	return addr, closeFn
}

// readHostLinkFrame 逐字节读取直至 CR 终止符。
func readHostLinkFrame(conn net.Conn) ([]byte, error) {
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 0, 128)
	tmp := make([]byte, 1)
	for {
		n, err := conn.Read(tmp)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		buf = append(buf, tmp[0])
		if tmp[0] == '\r' {
			return buf, nil
		}
	}
}

func TestFINSHostLinkTCPClientRead(t *testing.T) {
	addr, closeFn := hostLinkTCPServer(t, func(sid byte, _ []byte) []byte {
		return buildTestResponse(t, sid, []byte{0x00, 0x00}, []byte{0x00, 0x64})
	})
	defer closeFn()

	host, port := splitHostPort(t, addr)
	client, err := newFINSHostLinkTCPClient(&FINSConfig{
		Host: host, Port: port, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("newFINSHostLinkTCPClient: %v", err)
	}
	defer client.Close()

	data, err := client.Read(AreaDM, 100, 1)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.EqualFold(hex.EncodeToString(data), "0064") {
		t.Errorf("data = %X, want 0064", data)
	}
}

func TestFINSHostLinkTCPClientEndCode(t *testing.T) {
	addr, closeFn := hostLinkTCPServer(t, func(sid byte, _ []byte) []byte {
		return buildTestResponse(t, sid, []byte{0x11, 0x01}, nil)
	})
	defer closeFn()

	host, port := splitHostPort(t, addr)
	client, err := newFINSHostLinkTCPClient(&FINSConfig{
		Host: host, Port: port, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("newFINSHostLinkTCPClient: %v", err)
	}
	defer client.Close()

	_, err = client.Read(AreaDM, 0, 1)
	if err == nil || !IsEndCodeError(err) {
		t.Fatalf("expected end code error, got %v", err)
	}
}

func TestFINSHostLinkTCPClientDialFailed(t *testing.T) {
	// 监听后立即关闭，端口被占用的连接会失败
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	host, port := splitHostPort(t, addr)
	if _, err := newFINSHostLinkTCPClient(&FINSConfig{
		Host: host, Port: port, Timeout: 200 * time.Millisecond,
	}); err == nil {
		t.Fatalf("expected dial error")
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %s: %v", addr, err)
	}
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return host, port
}
