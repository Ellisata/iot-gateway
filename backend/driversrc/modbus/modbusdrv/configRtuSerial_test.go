// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"strings"
	"testing"
)

// TestParseModbusRTUConfig_SerialParams 串口链路参数必须真的被解析。
//
// 这是那个缺陷的直接回归测试：modbusRTUConfigRaw 此前**没有声明**
// baudRate/dataBits/stopBits/parity/timeoutMs，于是这几个值被钉死在
// DefaultModbusRTUConfig() 的 9600/8/1/N/5000 —— 表单改不了，protocol_json
// 也改不了，非默认链路的设备永远连不上，而且没有任何报错。
func TestParseModbusRTUConfig_SerialParams(t *testing.T) {
	cfg, err := ParseModbusRTUConfig(`{
		"comPort":"COM7","baudRate":"19200","dataBits":"8","stopBits":"2",
		"parity":"E","timeoutMs":8000,"unitId":3,"wordOrder":"BIG_ENDIAN"
	}`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if cfg.ComPort != "COM7" {
		t.Errorf("ComPort = %q, want COM7", cfg.ComPort)
	}
	if cfg.BaudRate != 19200 {
		t.Errorf("BaudRate = %d, want 19200（此前恒为默认 9600）", cfg.BaudRate)
	}
	if cfg.DataBits != 8 {
		t.Errorf("DataBits = %d, want 8", cfg.DataBits)
	}
	if cfg.StopBits != 2 {
		t.Errorf("StopBits = %d, want 2（此前恒为默认 1）", cfg.StopBits)
	}
	if cfg.Parity != "E" {
		t.Errorf("Parity = %q, want E（此前恒为默认 N）", cfg.Parity)
	}
	if cfg.TimeoutMS != 8000 {
		t.Errorf("TimeoutMS = %d, want 8000（此前恒为默认 5000）", cfg.TimeoutMS)
	}
	if cfg.UnitID != 3 {
		t.Errorf("UnitID = %d, want 3", cfg.UnitID)
	}
}

// TestParseModbusRTUConfig_ValueForms 数值字段要同时吃字符串与数字。
//
// form-create 的 select 产出**字符串**（"9600"），inputNumber 产出数字（9600）。
// 若 raw 结构体声明成 int，一份带字符串波特率的 JSON 会让 json.Unmarshal
// 整体报错 —— 不是那一个字段失效，而是整份配置解析失败、设备再也连不上。
func TestParseModbusRTUConfig_ValueForms(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"select 产出的字符串", `{"comPort":"COM1","baudRate":"19200","dataBits":"7","stopBits":"2","timeoutMs":"7000"}`},
		{"inputNumber 产出的数字", `{"comPort":"COM1","baudRate":19200,"dataBits":7,"stopBits":2,"timeoutMs":7000}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseModbusRTUConfig(c.json)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if cfg.BaudRate != 19200 || cfg.DataBits != 7 || cfg.StopBits != 2 || cfg.TimeoutMS != 7000 {
				t.Errorf("得到 baud=%d data=%d stop=%d timeout=%d，want 19200/7/2/7000",
					cfg.BaudRate, cfg.DataBits, cfg.StopBits, cfg.TimeoutMS)
			}
		})
	}
}

// TestParseModbusRTUConfig_InvalidFallsBackToDefault 非法值一律回退默认，不报错。
//
// 这几个值错了的表现本来就只是"不通"，再多一层"配置解析失败"只会让现场
// 更摸不着头脑。
func TestParseModbusRTUConfig_InvalidFallsBackToDefault(t *testing.T) {
	def := DefaultModbusRTUConfig()

	cases := []struct {
		name string
		json string
	}{
		{"波特率非数字", `{"comPort":"COM1","baudRate":"abc"}`},
		{"波特率为 0", `{"comPort":"COM1","baudRate":0}`},
		{"数据位越界", `{"comPort":"COM1","dataBits":9}`},
		{"停止位越界", `{"comPort":"COM1","stopBits":3}`},
		{"校验位非法", `{"comPort":"COM1","parity":"X"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseModbusRTUConfig(c.json)
			if err != nil {
				t.Fatalf("非法值不该让整份配置解析失败: %v", err)
			}
			if cfg.BaudRate != def.BaudRate || cfg.DataBits != def.DataBits ||
				cfg.StopBits != def.StopBits || cfg.Parity != def.Parity {
				t.Errorf("得到 %d/%d/%d/%q，want 默认 %d/%d/%d/%q",
					cfg.BaudRate, cfg.DataBits, cfg.StopBits, cfg.Parity,
					def.BaudRate, def.DataBits, def.StopBits, def.Parity)
			}
		})
	}
}

// TestParseModbusRTUConfig_ComPortGuard 缺 comPort 时保留默认 COM1。
//
// 无条件赋值会把默认端口清成空串，直到 Connect 才报 "comPort is required" ——
// 错误跑到离现场很远的地方才冒出来。
func TestParseModbusRTUConfig_ComPortGuard(t *testing.T) {
	cfg, err := ParseModbusRTUConfig(`{"unitId":1}`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cfg.ComPort != DefaultModbusRTUConfig().ComPort {
		t.Errorf("ComPort = %q，缺字段时应保留默认 %q",
			cfg.ComPort, DefaultModbusRTUConfig().ComPort)
	}
}

// TestParseModbusRTUConfig_TimeoutClamped 单帧超时必须被夹进允许区间。
//
// 上限是硬约束：网关侧 readTimeoutMs 默认 60s，协议自身超时若接近它，
// 每次读取都会在网关侧先超时而插件仍在跑，重试会在串行总线上堆积。
func TestParseModbusRTUConfig_TimeoutClamped(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"过大夹到上限", `{"comPort":"COM1","timeoutMs":120000}`, maxRTUTimeoutMS},
		{"过小夹到下限", `{"comPort":"COM1","timeoutMs":10}`, minRTUTimeoutMS},
		{"区间内原样保留", `{"comPort":"COM1","timeoutMs":7000}`, 7000},
		{"未配置用默认", `{"comPort":"COM1"}`, DefaultModbusRTUConfig().TimeoutMS},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := ParseModbusRTUConfig(c.in)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if cfg.TimeoutMS != c.want {
				t.Errorf("TimeoutMS = %d, want %d", cfg.TimeoutMS, c.want)
			}
		})
	}
}

// TestModbusRTUDriver_MatchConnection RTU 驱动的复用判据。
//
// 每个"不该复用"的情形都对应一种真实的误判：
//   - 端口不同 → 复用会把请求发到另一条总线上；
//   - 波特率不同 → 给出与被测配置无关的成功；
//   - 从站号不同 → 用户想验的 2 号站根本没被问到，1 号站的应答冒充了它。
//
// 驱动实例不经过 Connect（那会真的去开串口），直接构造内部状态。
func TestModbusRTUDriver_MatchConnection(t *testing.T) {
	newDriver := func(comPort string, baud int, slave byte) *modbusRTUDriver {
		d := &modbusRTUDriver{}
		d.config = &ModbusRTUConfig{ComPort: comPort, BaudRate: baud, DataBits: 8,
			StopBits: 1, Parity: "N", UnitID: slave}
		d.client = &ModbusRTUClient{
			comPort: comPort, baudRate: baud, dataBits: 8, stopBits: 1,
			parity: "N", slaveID: slave, connected: true,
		}
		return d
	}

	cases := []struct {
		name string
		drv  *modbusRTUDriver
		json string
		want bool
	}{
		{"完全一致", newDriver("COM1", 9600, 1), `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N","unitId":1}`, true},
		{"端口大小写不同视为同一端口", newDriver("COM1", 9600, 1), `{"comPort":"com1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N","unitId":1}`, true},
		{"端口不同", newDriver("COM1", 9600, 1), `{"comPort":"COM2","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N","unitId":1}`, false},
		{"波特率不同", newDriver("COM1", 9600, 1), `{"comPort":"COM1","baudRate":"19200","dataBits":"8","stopBits":"1","parity":"N","unitId":1}`, false},
		{"停止位不同", newDriver("COM1", 9600, 1), `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"2","parity":"N","unitId":1}`, false},
		{"校验位不同", newDriver("COM1", 9600, 1), `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"E","unitId":1}`, false},
		{"从站号不同", newDriver("COM1", 9600, 1), `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N","unitId":2}`, false},
		{"JSON 非法", newDriver("COM1", 9600, 1), `{`, false},
		// 缺 comPort 时解析器给出默认 COM1（见 TestParseModbusRTUConfig_ComPortGuard），
		// 与持有的连接一致 → 复用是对的：用户这份配置生效后就是走 COM1。
		{"缺 comPort 时按默认 COM1 判定", newDriver("COM1", 9600, 1), `{}`, true},
		{"缺 comPort 而持有的是别口", newDriver("COM2", 9600, 1), `{}`, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.drv.MatchConnection(c.json); got != c.want {
				t.Errorf("MatchConnection = %v, want %v", got, c.want)
			}
		})
	}
}

// TestModbusRTUDriver_MatchConnection_NotConnected 句柄已断开时不得复用。
func TestModbusRTUDriver_MatchConnection_NotConnected(t *testing.T) {
	d := &modbusRTUDriver{}
	d.client = &ModbusRTUClient{
		comPort: "COM1", baudRate: 9600, dataBits: 8, stopBits: 1,
		parity: "N", slaveID: 1, connected: false,
	}
	d.config = &ModbusRTUConfig{ComPort: "COM1", BaudRate: 9600, DataBits: 8,
		StopBits: 1, Parity: "N", UnitID: 1}

	if d.MatchConnection(`{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N","unitId":1}`) {
		t.Error("连接已断开却允许复用 —— 会对着一个死句柄收发")
	}
}

// TestConfigFieldNames_CoversSerialForm 表单契约里的字段名必须都是解析器认的键。
//
// 反向覆盖由 main_test.go 的 TestFormsMatchConfigStruct 完成；这里只钉住
// "清单本身没写错"——清单多一个解析器不认的键，那条测试就会假通过。
func TestConfigFieldNames_CoversSerialForm(t *testing.T) {
	raw := modbusRTUConfigRaw{}
	known := map[string]bool{
		"comPort": raw.ComPort == "", "baudRate": !raw.BaudRate.set,
		"dataBits": !raw.DataBits.set, "stopBits": !raw.StopBits.set,
		"parity": raw.Parity == "", "timeoutMs": !raw.TimeoutMS.set,
		"unitId": raw.UnitID == 0, "byteOrder": raw.ByteOrder == "",
		"wordOrder": raw.WordOrder == "", "mergeWindow": raw.MergeWindow == 0,
		"stringLen": raw.StringLen == 0,
	}
	for _, name := range configFieldsSerial {
		if !known[name] {
			t.Errorf("configFieldsSerial 里的 %q 不是 modbusRTUConfigRaw 的 json tag", name)
		}
		delete(known, name)
	}
	for name := range known {
		t.Logf("提示：raw 字段 %q 未出现在 configFieldsSerial 里（表单不暴露）", name)
	}

	if !strings.EqualFold("rtu", "RTU") {
		t.Fatal("EqualFold 前置条件不成立")
	}
}
