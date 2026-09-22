// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import "testing"

func TestDefaultConfig(t *testing.T) {
	s := DefaultDLT645Config(TransportSerial)
	if s.Version != Version2007 {
		t.Errorf("默认版本 = %q, want %q", s.Version, Version2007)
	}
	if s.BaudRate != 2400 {
		t.Errorf("默认波特率 = %d, want 2400", s.BaudRate)
	}
	if s.Parity != "E" {
		t.Errorf("默认校验位 = %q, want \"E\"（645 电表几乎全为偶校验）", s.Parity)
	}
	if s.PreambleBytes != 4 {
		t.Errorf("串口默认前导字节数 = %d, want 4", s.PreambleBytes)
	}
	if !s.CheckChecksum {
		t.Error("默认应开启校验码校验")
	}
	// TCP 透传链路无需唤醒字节
	tp := DefaultDLT645Config(TransportTCP)
	if tp.PreambleBytes != 0 {
		t.Errorf("TCP 默认前导字节数 = %d, want 0", tp.PreambleBytes)
	}
	// 收发间延时同样只对串口有意义：透传链路上换向由串口服务器 / DTU 自己处理，
	// 网关这侧的等待是纯空转，而它的代价随帧数线性放大。
	if tp.InterFrameDelayMS != 0 || tp.InterFrameDelay != 0 {
		t.Errorf("TCP 默认收发间延时 = %dms/%v, want 0（透传链路无需等待）",
			tp.InterFrameDelayMS, tp.InterFrameDelay)
	}
	if s.InterFrameDelayMS != defaultInterFrameMS {
		t.Errorf("串口默认收发间延时 = %dms, want %d（RS-485 换向 + 表处理时间）",
			s.InterFrameDelayMS, defaultInterFrameMS)
	}
	// 打包上限默认取协议上限：帧数 = ⌈点数/maxDIsPerRead⌉，逐个读会让大点位设备
	// 的单轮周期直接超出采集频率。
	if s.MaxDIsPerRead != maxDIsPerReadLimit {
		t.Errorf("默认单请求标识数 = %d, want %d（协议上限）", s.MaxDIsPerRead, maxDIsPerReadLimit)
	}
	if tp.MaxDIsPerRead != maxDIsPerReadLimit {
		t.Errorf("TCP 默认单请求标识数 = %d, want %d", tp.MaxDIsPerRead, maxDIsPerReadLimit)
	}
}

func TestParseConfigOverrides(t *testing.T) {
	cfg, err := ParseDLT645Config(`{
		"protocolVersion":"1997",
		"meterAddress":"1234",
		"comPort":"COM7",
		"baudRate":"9600",
		"dataBits":"7",
		"stopBits":"2",
		"parity":"o",
		"timeoutMs":"8000",
		"interFrameDelayMs":"50",
		"preambleBytes":"2",
		"maxDIsPerRead":"4",
		"maxFollowUpFrames":"5",
		"checkChecksum":"false"
	}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}

	// 表号补零到 12 位
	want := struct {
		Version, MeterAddress, ComPort, Parity string
		BaudRate, DataBits, StopBits           int
		TimeoutMS, InterFrameDelayMS           int
		PreambleBytes, MaxDIsPerRead           int
		MaxFollowUpFrames                      int
		CheckChecksum                          bool
	}{Version1997, "000000001234", "COM7", "O", 9600, 7, 2, 8000, 50, 2, 4, 5, false}

	got := struct {
		Version, MeterAddress, ComPort, Parity string
		BaudRate, DataBits, StopBits           int
		TimeoutMS, InterFrameDelayMS           int
		PreambleBytes, MaxDIsPerRead           int
		MaxFollowUpFrames                      int
		CheckChecksum                          bool
	}{cfg.Version, cfg.MeterAddress, cfg.ComPort, cfg.Parity,
		cfg.BaudRate, cfg.DataBits, cfg.StopBits,
		cfg.TimeoutMS, cfg.InterFrameDelayMS,
		cfg.PreambleBytes, cfg.MaxDIsPerRead,
		cfg.MaxFollowUpFrames, cfg.CheckChecksum}

	if got != want {
		t.Errorf("解析结果 =\n %+v\nwant\n %+v", got, want)
	}
}

// 字段缺席时必须保留默认值——尤其前导唤醒字节数（0 与「未配置」语义不同，
// 缺席被当成 0 会让串口默认的 4 个唤醒字节被静默清掉）。
func TestParseConfigKeepsDefaultsWhenAbsent(t *testing.T) {
	cfg, err := ParseDLT645Config(`{"comPort":"COM3"}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PreambleBytes != 4 {
		t.Errorf("缺席时前导字节数 = %d, want 保留默认 4", cfg.PreambleBytes)
	}
	if cfg.BaudRate != 2400 || cfg.Parity != "E" || cfg.Version != Version2007 {
		t.Errorf("缺席字段未保留默认值: baud=%d parity=%q version=%q",
			cfg.BaudRate, cfg.Parity, cfg.Version)
	}
	if cfg.ComPort != "COM3" {
		t.Errorf("显式配置的串口名被改动: %q", cfg.ComPort)
	}
	// 串口名缺席时同样保留默认值（与 host 一致），而不是清成空串
	empty, err := ParseDLT645Config(`{}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}
	if empty.ComPort != defaultComPort {
		t.Errorf("缺席时串口名 = %q, want 保留默认 %q", empty.ComPort, defaultComPort)
	}
	if !cfg.CheckChecksum {
		t.Error("缺席时校验开关应保留默认 true")
	}

	// 显式 0 应能真正关闭前导字节
	off, err := ParseDLT645Config(`{"preambleBytes":"0"}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}
	if off.PreambleBytes != 0 {
		t.Errorf("显式 0 未生效，得到 %d", off.PreambleBytes)
	}
}

// 非法值一律回退默认，绝不使整份配置解析失败。
func TestParseConfigTolerantToGarbage(t *testing.T) {
	cfg, err := ParseDLT645Config(`{
		"protocolVersion":"2007x",
		"meterAddress":"12x4",
		"baudRate":"不是数字",
		"dataBits":"99",
		"stopBits":"7",
		"parity":"Z",
		"preambleBytes":"99",
		"maxDIsPerRead":"999"
	}`, TransportSerial)
	if err != nil {
		t.Fatalf("非法字段不应导致解析失败: %v", err)
	}
	if cfg.Version != Version2007 {
		t.Errorf("非法版本应回退 2007，得到 %q", cfg.Version)
	}
	if cfg.MeterAddress != defaultMeterAddress {
		t.Errorf("非法表号应回退默认，得到 %q", cfg.MeterAddress)
	}
	if cfg.BaudRate != 2400 || cfg.DataBits != 8 || cfg.StopBits != 1 || cfg.Parity != "E" {
		t.Errorf("非法串口参数未回退默认: %d/%d/%d/%q",
			cfg.BaudRate, cfg.DataBits, cfg.StopBits, cfg.Parity)
	}
	if cfg.PreambleBytes != maxPreambleBytes {
		t.Errorf("前导字节数应钳制到 %d，得到 %d", maxPreambleBytes, cfg.PreambleBytes)
	}
	if cfg.MaxDIsPerRead != maxDIsPerReadLimit {
		t.Errorf("单请求标识数应钳制到 %d，得到 %d", maxDIsPerReadLimit, cfg.MaxDIsPerRead)
	}
}

// 超时下限：645 表速率低，过小的超时会截断应答帧，
// 现象是「偶发解析失败」而非明确的超时，极难排查。
func TestMinTimeoutFloor(t *testing.T) {
	cfg, err := ParseDLT645Config(`{"baudRate":"1200","timeoutMs":"1"}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TimeoutMS < minTimeoutMS(1200, 8, 1) {
		t.Errorf("超时 %dms 低于波特率下限 %dms", cfg.TimeoutMS, minTimeoutMS(1200, 8, 1))
	}
	// 显式配置的合理超时不应被抬高
	cfg2, _ := ParseDLT645Config(`{"baudRate":"9600","timeoutMs":"8000"}`, TransportSerial)
	if cfg2.TimeoutMS != 8000 {
		t.Errorf("合理超时被改动: %d, want 8000", cfg2.TimeoutMS)
	}
}

// 兼容旧字段名 timeout。
func TestLegacyTimeoutAlias(t *testing.T) {
	cfg, err := ParseDLT645Config(`{"timeout":"7000"}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TimeoutMS != 7000 {
		t.Errorf("旧字段名 timeout 未生效: %d", cfg.TimeoutMS)
	}
}

func TestParseConfigInvalidJSON(t *testing.T) {
	if _, err := ParseDLT645Config(`{`, TransportSerial); err == nil {
		t.Error("非法 JSON 应报错")
	}
	if cfg, err := ParseDLT645Config("", TransportSerial); err != nil || cfg == nil {
		t.Errorf("空配置应返回默认值: %v, %v", cfg, err)
	}
}
