// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// TestSerialPortHolders 反推"哪些设备可能占着串口"。
//
// 网关侧拿不到插件进程里的句柄，只能靠两件事筛：设备配了 comPort、且确有驱动
// 实例。第二条是必需的而非优化 —— 协议不支持的设备根本没有实例，
// 不可能打开过任何句柄，纳入就是纯误报。
func TestSerialPortHolders(t *testing.T) {
	drv := &mockDriver{}
	task := &gatewayTask{
		devices: []po.Device{
			// 有驱动 + 配了串口 → 应出现
			{ID: "dev-1", Name: "1号电表", ProtocolID: "p-1", ProtocolJSON: `{"comPort":"COM1","meterAddress":"1"}`},
			// 有驱动但走以太网 → 不出现
			{ID: "dev-2", Name: "TCP设备", ProtocolID: "p-2", ProtocolJSON: `{"host":"192.168.1.10","port":502}`},
			// 配了串口但**没有驱动实例**（协议不支持被跳过）→ 不出现
			{ID: "dev-3", Name: "无驱动设备", ProtocolID: "p-1", ProtocolJSON: `{"comPort":"COM1"}`},
			// 串口名大小写变体 → 应出现，且原样保留
			{ID: "dev-4", Name: "小写端口", ProtocolID: "p-1", ProtocolJSON: `{"comPort":"com2"}`},
		},
		drivers: map[string]driver.Driver{
			"dev-1": drv,
			"dev-2": drv,
			"dev-4": drv,
		},
		protocols: map[string]string{
			"dev-1": "DLT645.Serial",
			"dev-2": "ModBus.TCP",
			"dev-4": "ModBus.RTU",
		},
	}

	got := task.serialPortHolders()

	byID := make(map[string]SerialPortHolder, len(got))
	for _, h := range got {
		byID[h.DeviceID] = h
	}

	if len(got) != 2 {
		t.Fatalf("命中 %d 台设备（%v），want 2（dev-1 与 dev-4）", len(got), byID)
	}
	if _, ok := byID["dev-2"]; ok {
		t.Error("以太网设备不该出现 —— 它不占用串口")
	}
	if _, ok := byID["dev-3"]; ok {
		t.Error("没有驱动实例的设备不该出现 —— 它不可能打开过句柄")
	}

	h1, ok := byID["dev-1"]
	if !ok {
		t.Fatal("dev-1 应出现")
	}
	if h1.Port != "COM1" || h1.DeviceName != "1号电表" || h1.Protocol != "DLT645.Serial" {
		t.Errorf("dev-1 = %+v，端口/名称/协议都应原样带出", h1)
	}
	if !h1.Connected {
		t.Error("mockDriver 恒报已连接，Connected 应为 true")
	}

	if h4, ok := byID["dev-4"]; !ok || h4.Port != "com2" {
		t.Errorf("dev-4 = %+v，端口名应原样保留（大小写归一由比较方负责）", h4)
	}
}

// TestSerialPortHolders_EmptyTask 空任务与 nil 字段不得 panic。
func TestSerialPortHolders_EmptyTask(t *testing.T) {
	task := &gatewayTask{}
	if got := task.serialPortHolders(); len(got) != 0 {
		t.Errorf("空任务应返回空切片，实得 %v", got)
	}
}
