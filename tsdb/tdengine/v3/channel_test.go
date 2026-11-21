// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tdengine

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway/tsdb"
)

// TestTestConnectivityUnreachableFailsFast 钉住 TCP 预检：端口不可达时必须立刻
// 失败，而不是等操作系统的 TCP 超时（Windows 约 21s），并且错误里要带出真实地址。
// 回归背景:taosWS 驱动忽略 ctx,没有预检时 5s 的 connectTimeout 形同虚设,
// 最终 database/sql 报出的是一句误导性的 "context deadline exceeded"。
func TestTestConnectivityUnreachableFailsFast(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	// 立刻关闭:对该端口的连接会被系统直接拒绝,而不是静默丢包等到超时。
	_ = l.Close()

	cfg, err := ParseConfig(`{"host":"127.0.0.1","port":` + strconv.Itoa(port) + `}`)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	start := time.Now()
	err = NewWriter(*cfg, tsdb.Options{}).TestConnectivity()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected connectivity error, got nil")
	}
	if !strings.Contains(err.Error(), "connect failed") {
		t.Errorf("error = %q, want it to mention connect failed", err)
	}
	if !strings.Contains(err.Error(), ":"+strconv.Itoa(port)) {
		t.Errorf("error = %q, want it to mention the target port %d", err, port)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("TestConnectivity took %v; TCP 预检没有生效", elapsed)
	}
}
