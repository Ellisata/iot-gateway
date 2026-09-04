// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"

	"iot-gateway/model/po"
)

// freePort 获取一个空闲的本地端口（探测后立即释放，存在极小竞态窗口，测试可接受）。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestOpcUaDriverIntegration 使用 gopcua 进程内服务器验证真实连接：Ping、Connect、
// Read 直接 NodeID（值/质量/Kind）、Bad 节点（不存在）质量 0。
//
// 注：进程内服务器的 TranslateBrowsePathsToNodeIds 返回 serviceUnsupported，
// 浏览路径解析不在此集成测试覆盖（由 TestReadBrowsePathCached 单测覆盖）。
func TestOpcUaDriverIntegration(t *testing.T) {
	port := freePort(t)
	srv := server.New(server.EndPoint("127.0.0.1", port))
	defer srv.Close()

	ns := server.NewNodeNameSpace(srv, "urn:test:opcua")
	nsID := ns.ID()
	ns.AddNode(server.NewVariableNode(ua.NewNumericNodeID(nsID, 1001), "Temperature", float32(21.5)))
	ns.AddNode(server.NewVariableNode(ua.NewNumericNodeID(nsID, 1002), "Counter", int32(42)))
	ns.AddNode(server.NewVariableNode(ua.NewNumericNodeID(nsID, 1003), "Switch", true))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start test server: %v", err)
	}

	cfgJSON := fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, port)
	d := newOpcUaDriver()

	// Ping：临时连接握手后关闭
	if err := d.Ping(cfgJSON); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}

	// Connect + Read
	if err := d.Connect(cfgJSON); err != nil {
		t.Fatalf("Connect() error: %v", err)
	}
	defer d.Close()
	if !d.IsConnected() {
		t.Fatal("IsConnected() = false after Connect")
	}

	addrs := []po.DeviceAddress{
		{ID: "temp", Name: fmt.Sprintf("ns=%d;i=1001", nsID), DataType: "float32"},
		{ID: "cnt", Name: fmt.Sprintf("ns=%d;i=1002", nsID), DataType: "int32"},
		{ID: "sw", Name: fmt.Sprintf("ns=%d;i=1003", nsID), DataType: "bool"},
		{ID: "missing", Name: fmt.Sprintf("ns=%d;i=9999", nsID), DataType: "float32"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}

	want := []struct {
		val  string
		q    int
		kind string
	}{
		{"21.5", 192, "float"},
		{"42", 192, "int"},
		{"1", 192, "bool"},
		{"", 0, "float"}, // 不存在的节点：Bad → Quality 0，值置空
	}
	for i, w := range want {
		if results[i].DeviceAddressID != addrs[i].ID {
			t.Errorf("result[%d].id = %q, want %q", i, results[i].DeviceAddressID, addrs[i].ID)
		}
		if results[i].Value != w.val {
			t.Errorf("result[%d].Value = %q, want %q", i, results[i].Value, w.val)
		}
		if results[i].Quality != w.q {
			t.Errorf("result[%d].Quality = %d, want %d", i, results[i].Quality, w.q)
		}
		if results[i].Kind != w.kind {
			t.Errorf("result[%d].Kind = %q, want %q", i, results[i].Kind, w.kind)
		}
	}
}
