// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"strings"
	"testing"

	"iot-gateway/driver"
)

// TestRTUPingOpenFailureCarriesHint 串口打不开时，错误里必须带上可照做的提示。
//
// 现场报上来的原始症状就是一句 `modbus rtu: ping COM1 failed (serial): Access is denied.` ——
// 既没说是哪个操作失败了，也没给任何下一步。而这一句恰好是最常见的现场问题：
// 端口被别的程序（串口调试工具、Modbus 仿真软件）占着，或者端口名写错了。
//
// 提示必须由**驱动**在打开失败的分支里拼：网关口分不清"打不开端口"与
// "打开了但没应答"，把这句话挂到后者上会把现场引向错误的方向。
//
// 用一个不存在的端口名触发这条分支 —— 它在任何平台上都开不出来。
func TestRTUPingOpenFailureCarriesHint(t *testing.T) {
	const bogus = "/dev/__iot_gateway_no_such_port__"

	err := NewRTU().Ping(`{"comPort":"` + bogus + `","baudRate":"9600"}`)
	if err == nil {
		t.Fatalf("端口 %q 不该能打开 —— 本用例失去了触发条件", bogus)
	}
	if !strings.Contains(err.Error(), bogus) {
		t.Errorf("错误里应带上端口名，实得: %v", err)
	}
	if !strings.Contains(err.Error(), driver.SerialOpenFailureHint) {
		t.Errorf("打开失败的错误里必须带上可照做的提示，实得: %v", err)
	}
}

// TestRTUConnectOpenFailureCarriesHint Connect（采集链路）的打开失败同样要带提示。
//
// Connect 的失败会进采集日志与设备状态，现场排查时看到的往往是它。
func TestRTUConnectOpenFailureCarriesHint(t *testing.T) {
	const bogus = "/dev/__iot_gateway_no_such_port__"

	err := NewRTU().Connect(`{"comPort":"` + bogus + `","baudRate":"9600"}`)
	if err == nil {
		t.Fatalf("端口 %q 不该能打开 —— 本用例失去了触发条件", bogus)
	}
	if !strings.Contains(err.Error(), driver.SerialOpenFailureHint) {
		t.Errorf("打开失败的错误里必须带上可照做的提示，实得: %v", err)
	}
}
