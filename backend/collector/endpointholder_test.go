// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// TestEndpointHolders 反推"哪些设备可能正连着某个以太网目标"。
//
// 与 TestSerialPortHolders 是同一次反推的两面（共用 eachLiveDevice）：
// 设备配了 host:port、且确有驱动实例。第二条是必需的 —— 协议不支持的设备
// 在 newGatewayTask 里就被跳过了，不可能连过任何地址，纳入就是纯误报。
func TestEndpointHolders(t *testing.T) {
	drv := &mockDriver{}
	task := &gatewayTask{
		devices: []po.Device{
			// 有驱动 + 配了以太网目标 → 应出现
			{ID: "dev-1", Name: "1号PLC", ProtocolID: "p-1", ProtocolJSON: `{"host":"10.62.100.22","port":10000}`},
			// 有驱动但走串口 → 不出现
			{ID: "dev-2", Name: "串口电表", ProtocolID: "p-2", ProtocolJSON: `{"comPort":"COM1"}`},
			// 配了目标但**没有驱动实例**（协议不支持被跳过）→ 不出现
			{ID: "dev-3", Name: "无驱动设备", ProtocolID: "p-1", ProtocolJSON: `{"host":"10.62.100.15","port":9600}`},
			// 端口写成字符串 → 应出现，且与数字写法落在同一个字面量上
			{ID: "dev-4", Name: "字符串端口", ProtocolID: "p-1", ProtocolJSON: `{"host":"10.62.100.16","port":"44818"}`},
		},
		drivers: map[string]driver.Driver{
			"dev-1": drv,
			"dev-2": drv,
			"dev-4": drv,
		},
		protocols: map[string]string{
			"dev-1": "Mitsubishi.MC.TCP",
			"dev-2": "DLT645.Serial",
			"dev-4": "Omron.CIP",
		},
	}

	got := task.endpointHolders()

	byID := make(map[string]EndpointHolder, len(got))
	for _, h := range got {
		byID[h.DeviceID] = h
	}

	if len(got) != 2 {
		t.Fatalf("命中 %d 台设备（%v），want 2（dev-1 与 dev-4）", len(got), byID)
	}
	if _, ok := byID["dev-2"]; ok {
		t.Error("串口设备不该出现 —— 它没有以太网目标")
	}
	if _, ok := byID["dev-3"]; ok {
		t.Error("没有驱动实例的设备不该出现 —— 它不可能连过任何地址")
	}

	h1, ok := byID["dev-1"]
	if !ok {
		t.Fatal("dev-1 应出现")
	}
	if h1.Endpoint != "10.62.100.22:10000" || h1.DeviceName != "1号PLC" || h1.Protocol != "Mitsubishi.MC.TCP" {
		t.Errorf("dev-1 = %+v，目标/名称/协议都应原样带出", h1)
	}
	if !h1.Connected {
		t.Error("mockDriver 恒报已连接，Connected 应为 true")
	}

	if h4, ok := byID["dev-4"]; !ok || h4.Endpoint != "10.62.100.16:44818" {
		t.Errorf("dev-4 = %+v，字符串端口应与数字端口归一成同一个字面量", h4)
	}
}

// TestEndpointHolders_EmptyTask 空任务与 nil 字段不得 panic
// （与 TestSerialPortHolders_EmptyTask 同一条要求）。
func TestEndpointHolders_EmptyTask(t *testing.T) {
	task := &gatewayTask{}
	if got := task.endpointHolders(); len(got) != 0 {
		t.Errorf("空任务应返回空切片，实得 %v", got)
	}
}
