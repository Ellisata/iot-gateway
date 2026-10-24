// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"testing"

	"iot-gateway/collector"
)

// TestMarshalBatchGolden 钉住 native-json 的线上格式。
//
// 这是北向 Encoder 重构的安全绳：native 载荷的字段、顺序、逐条 collectedAt
// 回落规则在重构前后必须逐字节一致。字段顺序由 pushPoint 的结构体定义决定，
// 改变它等于悄悄改了下游契约。
func TestMarshalBatchGolden(t *testing.T) {
	records := []collector.CollectedRecord{
		{
			DeviceID:           "d1",
			DeviceName:         "Device 1",
			DeviceAddressID:    "a1",
			DeviceAddressName:  "温度",
			DeviceAddressLabel: "温度说明（不应出现在载荷里）",
			Value:              "23.5",
			DataType:           "float32",
			Kind:               "float",
			Protocol:           "ModBus.TCP",
			Quality:            192,
			CollectedAt:        "2026-10-19 17:05:30.123",
		},
		{
			DeviceID:          "d1",
			DeviceName:        "Device 1",
			DeviceAddressID:   "a2",
			DeviceAddressName: "开关",
			Value:             "0",
			DataType:          "bool",
			Kind:              "bool",
			Protocol:          "ModBus.TCP",
			Quality:           2,
			// 留空：必须回落到批次时间
		},
	}

	got, err := marshalBatch(records, "2026-10-24 10:00:00.000")
	if err != nil {
		t.Fatalf("marshalBatch: %v", err)
	}

	const want = `[{"deviceId":"d1","deviceName":"Device 1","deviceAddressId":"a1","deviceAddressName":"温度","value":"23.5","dataType":"float32","kind":"float","protocol":"ModBus.TCP","quality":192,"collectedAt":"2026-10-19 17:05:30.123"},{"deviceId":"d1","deviceName":"Device 1","deviceAddressId":"a2","deviceAddressName":"开关","value":"0","dataType":"bool","kind":"bool","protocol":"ModBus.TCP","quality":2,"collectedAt":"2026-10-24 10:00:00.000"}]`
	if string(got) != want {
		t.Errorf("native payload changed:\n got: %s\nwant: %s", got, want)
	}
}
