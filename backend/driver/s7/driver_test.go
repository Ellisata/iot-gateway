package s7

import (
	"testing"

	"iot-gateway/driver"
)

// TestDriverRegistration 验证驱动已注册且可通过注册表创建
func TestDriverRegistration(t *testing.T) {
	d, err := driver.Create("Siemens.Net.S7")
	if err != nil {
		t.Fatalf("driver.Create(Siemens.Net.S7) = %v", err)
	}
	if d == nil {
		t.Fatal("driver is nil")
	}
	if d.IsConnected() {
		t.Error("new driver should not be connected")
	}
	if err := d.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestPingUnreachableHost 验证对未监听端口 Ping 快速失败且不修改驱动状态
func TestPingUnreachableHost(t *testing.T) {
	d, err := driver.Create("Siemens.Net.S7")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	cfg := `{"host":"127.0.0.1","port":"1","rack":0,"slot":1,"timeout":"500"}`
	if err := d.Ping(cfg); err == nil {
		t.Error("Ping to unreachable host should fail")
	}
	// Ping 无副作用：不应让驱动进入已连接状态
	if d.IsConnected() {
		t.Error("Ping must not leave driver connected")
	}
}
