// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake

import (
	"fmt"
	"testing"

	"iot-gateway/driver"
	_ "iot-gateway/driver/mitsubishi"   // 注册 MC 驱动
	_ "iot-gateway/driver/modbus"       // 注册 Modbus 驱动
	_ "iot-gateway/driver/omron/cip"    // 注册 CIP 驱动
	_ "iot-gateway/driver/omron/fins"   // 注册 FINS 驱动
	_ "iot-gateway/driver/opcua"        // 注册 OPC UA 驱动
	_ "iot-gateway/driver/rockwell/cip" // 注册 Rockwell CIP 驱动
	_ "iot-gateway/driver/s7"           // 注册 S7 驱动
	"iot-gateway/model/po"
)

// TestModbusDriverAgainstFake 用真实 Modbus TCP 驱动连假服务器读取，
// 验证假服务器的帧格式与驱动客户端解封一致（防止帧格式漂移导致压测结果失真）。
func TestModbusDriverAgainstFake(t *testing.T) {
	srv, err := NewModbusTCP()
	if err != nil {
		t.Fatalf("new fake modbus: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("ModBus.TCP")
	if err != nil {
		t.Fatalf("create modbus driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	// 连续 + 空洞 + 多寄存器类型混合，覆盖区间合并与多帧读取
	addrs := []po.DeviceAddress{
		{ID: "a1", Name: "40001", DataType: "int16"},
		{ID: "a2", Name: "40002", DataType: "int16"},
		{ID: "a3", Name: "40003", DataType: "int32"}, // 占 2 寄存器
		{ID: "a4", Name: "40008", DataType: "float32"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestFINSDriverAgainstFake 用真实 FINS/TCP 驱动连假服务器读取（含连接握手 + 内存区读）。
func TestFINSDriverAgainstFake(t *testing.T) {
	srv, err := NewFINSTCP()
	if err != nil {
		t.Fatalf("new fake fins: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Omron.FINS.TCP")
	if err != nil {
		t.Fatalf("create fins driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"transport":"TCP"}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	addrs := []po.DeviceAddress{
		{ID: "d1", Name: "D0", DataType: "int16"},
		{ID: "d2", Name: "D1", DataType: "int16"},
		{ID: "d3", Name: "D2", DataType: "int32"}, // 占 2 字
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestS7DriverAgainstFake 用真实 S7 驱动连假服务器读取（ISO 握手 + PDU 协商 + 读响应）。
func TestS7DriverAgainstFake(t *testing.T) {
	srv, err := NewS7()
	if err != nil {
		t.Fatalf("new fake s7: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Siemens.S7")
	if err != nil {
		t.Fatalf("create s7 driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"rack":"0","slot":"1"}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	addrs := []po.DeviceAddress{
		{ID: "b1", Name: "DB1.DBB0", DataType: "byte"},
		{ID: "b2", Name: "DB1.DBB1", DataType: "byte"},
		{ID: "w1", Name: "DB1.DBW4", DataType: "int"}, // 16 位有符号：S7 注册名 "int"
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestCIPDriverAgainstFake 用真实 CIP 驱动连假服务器读取（Register Session + SendRRData）。
func TestCIPDriverAgainstFake(t *testing.T) {
	srv, err := NewCIP()
	if err != nil {
		t.Fatalf("new fake cip: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Omron.CIP")
	if err != nil {
		t.Fatalf("create cip driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	addrs := []po.DeviceAddress{
		{ID: "t1", Name: "Tag_0", DataType: "int16"},
		{ID: "t2", Name: "Tag_1", DataType: "int16"},
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestCIPDriverBatchAgainstFake 用真实 CIP 驱动以 0x0A 多服务报文连假服务器批量读取，
// 验证批量读的帧组装与响应解析在端到端链路可用（帧数从 N 次往返降到 ceil(N/batch) 次）。
func TestCIPDriverBatchAgainstFake(t *testing.T) {
	srv, err := NewCIP()
	if err != nil {
		t.Fatalf("new fake cip: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Omron.CIP")
	if err != nil {
		t.Fatalf("create cip driver: %v", err)
	}
	// maxTagsPerRequest=4：8 个标签分 2 个 0x0A 报文
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"maxTagsPerRequest":4}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	addrs := make([]po.DeviceAddress, 0, 8)
	for i := 0; i < 8; i++ {
		addrs = append(addrs, po.DeviceAddress{
			ID:       fmt.Sprintf("t%d", i),
			Name:     fmt.Sprintf("Tag_%d", i),
			DataType: "int16",
		})
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestRockwellDriverAgainstFake 用真实 Rockwell CIP 驱动连 AB 假服务器读取，
// 覆盖两条读路径：独立标签逐个 0x4C 单读 + base[N] 数组区间合并 Read Tag Elements
// （请求带 ElementCount，验证假服务器按元素数回数据与驱动切片解码一致）。
func TestRockwellDriverAgainstFake(t *testing.T) {
	srv, err := NewCIPAB()
	if err != nil {
		t.Fatalf("new fake cip: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Rockwell.CIP")
	if err != nil {
		t.Fatalf("create rockwell driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	// Arr[0..7] 同基数组合并成 1 次 Read Tag Elements（8 元素）；
	// Tag_9/Tag_10 独立标签走单读
	addrs := make([]po.DeviceAddress, 0, 10)
	for i := 0; i < 8; i++ {
		addrs = append(addrs, po.DeviceAddress{
			ID:       fmt.Sprintf("a%d", i),
			Name:     fmt.Sprintf("Arr[%d]", i),
			DataType: "int16",
		})
	}
	addrs = append(addrs,
		po.DeviceAddress{ID: "t9", Name: "Tag_9", DataType: "int16"},
		po.DeviceAddress{ID: "t10", Name: "Tag_10", DataType: "int16"},
	)
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestMC3EDriverAgainstFake 用真实 MC 3E 驱动连假服务器读取（字单位 + 位单位）。
func TestMC3EDriverAgainstFake(t *testing.T) {
	srv, err := NewMC3E()
	if err != nil {
		t.Fatalf("new fake mc: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("Mitsubishi.MC.TCP")
	if err != nil {
		t.Fatalf("create mc driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	addrs := []po.DeviceAddress{
		{ID: "d1", Name: "D0", DataType: "int16"},
		{ID: "d2", Name: "D1", DataType: "int16"},
		{ID: "d3", Name: "D2", DataType: "float32"}, // 占 2 字
		{ID: "m1", Name: "M0", DataType: "bool"},    // 位设备位单位
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for _, r := range results {
		if r.Quality != 192 {
			t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
		}
	}
}

// TestOpcUaDriverAgainstFake 用真实 OPC UA 驱动连假服务器读取（gopcua 进程内服务器），
// 覆盖握手（HEL/OpenSecureChannel/CreateSession/ActivateSession）、批量 Read 分批
// （>maxBatch 点位跨多请求）、Bad 节点 Quality=0 与 int32 值格式化。
func TestOpcUaDriverAgainstFake(t *testing.T) {
	srv, err := NewOpcUa()
	if err != nil {
		t.Fatalf("new fake opcua: %v", err)
	}
	defer srv.Close()

	d, err := driver.Create("OPC.UA")
	if err != nil {
		t.Fatalf("create opcua driver: %v", err)
	}
	if err := d.Connect(fmt.Sprintf(`{"host":"127.0.0.1","port":%d}`, srv.Port())); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer d.Close()

	// 连续 250 点（>默认 maxBatch=100，覆盖分批）+ 不存在节点（Quality=0）
	addrs := make([]po.DeviceAddress, 0, 251)
	for i := 0; i < 250; i++ {
		addrs = append(addrs, po.DeviceAddress{
			ID:       fmt.Sprintf("a%d", i),
			Name:     fmt.Sprintf("ns=1;i=%d", i+1001),
			DataType: "int32",
		})
	}
	addrs = append(addrs, po.DeviceAddress{ID: "missing", Name: "ns=1;i=777", DataType: "int32"})

	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(results) != len(addrs) {
		t.Fatalf("got %d results, want %d", len(results), len(addrs))
	}
	for i, r := range results {
		if i < 250 {
			if r.Quality != 192 {
				t.Errorf("address %s quality=%d, want 192", r.DeviceAddressID, r.Quality)
			}
			if r.Value != fmt.Sprintf("%d", i+1) {
				t.Errorf("address %s value=%q, want %d", r.DeviceAddressID, r.Value, i+1)
			}
		} else if r.Quality != 0 {
			t.Errorf("missing node quality=%d, want 0", r.Quality)
		}
	}
}
