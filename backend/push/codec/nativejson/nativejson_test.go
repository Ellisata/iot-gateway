// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package nativejson

import (
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

func TestEncoderProducesOneMessage(t *testing.T) {
	enc := New()
	msgs, err := enc.Encode(push.PushBatch{
		DeviceID:    "d1",
		CollectedAt: "2026-10-24 10:00:00.000",
		Records: []collector.CollectedRecord{{
			DeviceID:           "d1",
			DeviceName:         "Device 1",
			DeviceAddressID:    "a1",
			DeviceAddressName:  "temp",
			DeviceAddressLabel: "不应出现",
			Value:              "23.5",
			DataType:           "float32",
			Kind:               "float",
			Protocol:           "ModBus.TCP",
			Quality:            192,
		}},
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	const want = `[{"deviceId":"d1","deviceName":"Device 1","deviceAddressId":"a1","deviceAddressName":"temp","value":"23.5","dataType":"float32","kind":"float","protocol":"ModBus.TCP","quality":192,"collectedAt":"2026-10-24 10:00:00.000"}]`
	if string(msgs[0].Payload) != want {
		t.Errorf("payload:\n got: %s\nwant: %s", msgs[0].Payload, want)
	}
}
