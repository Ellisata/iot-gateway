// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"testing"
)

// rtuClientFrom 造一个与 cfg 完全一致的「已连接」RTU 客户端。
// 字段是包内的，可以直接构造，不需要真串口。
func rtuClientFrom(t *testing.T, cfg *ModbusRTUConfig) *ModbusRTUClient {
	t.Helper()
	return &ModbusRTUClient{
		comPort:   cfg.ComPort,
		baudRate:  cfg.BaudRate,
		dataBits:  cfg.DataBits,
		stopBits:  cfg.StopBits,
		parity:    cfg.Parity,
		slaveID:   cfg.UnitID,
		connected: true,
	}
}

func mustParseRTU(t *testing.T, protocolJSON string) *ModbusRTUConfig {
	t.Helper()
	cfg, err := ParseModbusRTUConfig(protocolJSON)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	return cfg
}

// matchConfig 必须逐项比较链路参数。
//
// 从站号这一条是重点：Ping 用的是 handler 里既有的 SlaveId，只比串口号的话，
// 「测试 2 号站」的请求会发到总线上的 1 号站，1 号站正常应答 → 测试连接报成功，
// 而用户想验的 2 号站根本没被问到。这比直接报错危险得多。
func TestRTUClientMatchConfig(t *testing.T) {
	base := mustParseRTU(t, `{"comPort":"COM1","unitId":1}`)
	client := rtuClientFrom(t, base)

	cases := map[string]struct {
		mutate func(*ModbusRTUConfig)
		want   bool
	}{
		"完全一致":      {func(*ModbusRTUConfig) {}, true},
		"从站号不同":     {func(c *ModbusRTUConfig) { c.UnitID = 2 }, false},
		"串口不同":      {func(c *ModbusRTUConfig) { c.ComPort = "COM2" }, false},
		"波特率不同":     {func(c *ModbusRTUConfig) { c.BaudRate = 19200 }, false},
		"数据位不同":     {func(c *ModbusRTUConfig) { c.DataBits = 7 }, false},
		"停止位不同":     {func(c *ModbusRTUConfig) { c.StopBits = 2 }, false},
		"校验位不同":     {func(c *ModbusRTUConfig) { c.Parity = "E" }, false},
		"校验位大小写不敏感": {func(c *ModbusRTUConfig) { c.Parity = "n" }, true},
	}
	for name, c := range cases {
		probe := mustParseRTU(t, `{"comPort":"COM1","unitId":1}`)
		c.mutate(probe)
		if got := client.matchConfig(probe); got != c.want {
			t.Errorf("%s: matchConfig = %v, want %v", name, got, c.want)
		}
	}

	// 连接已断开时不得判定可复用
	client.connected = false
	if client.matchConfig(base) {
		t.Error("已断开的连接不应判定为可复用")
	}
}

func TestRTUDriverMatchConnection(t *testing.T) {
	const cfgJSON = `{"comPort":"COM1","unitId":1}`
	cfg := mustParseRTU(t, cfgJSON)

	d := &modbusRTUDriver{}
	if d.MatchConnection(cfgJSON) {
		t.Error("尚未连接的驱动不应判定为可复用")
	}

	d = &modbusRTUDriver{config: cfg, client: rtuClientFrom(t, cfg)}
	if !d.MatchConnection(cfgJSON) {
		t.Error("参数一致时应判定可复用")
	}
	if got := d.SerialResource(); got != "COM1" {
		t.Errorf("SerialResource = %q, want COM1", got)
	}
}

// H1 回归：从站号不同必须判为「不可复用」。
func TestRTUDriverMatchConnectionRejectsDifferentUnitID(t *testing.T) {
	cfg := mustParseRTU(t, `{"comPort":"COM1","unitId":1}`)
	d := &modbusRTUDriver{config: cfg, client: rtuClientFrom(t, cfg)}

	if d.MatchConnection(`{"comPort":"COM1","unitId":2}`) {
		t.Error("从站号不同的连接被判定为可复用：测试 2 号站的请求会打到 1 号站，得到假绿")
	}
}

// JSON 非法时走 Ping 的解析错误路径，而不是在匹配阶段悄悄咽掉。
func TestRTUDriverMatchConnectionBadJSON(t *testing.T) {
	cfg := mustParseRTU(t, `{"comPort":"COM1","unitId":1}`)
	d := &modbusRTUDriver{config: cfg, client: rtuClientFrom(t, cfg)}

	if d.MatchConnection("not json") {
		t.Error("非法 JSON 不应判定为可复用")
	}
}
