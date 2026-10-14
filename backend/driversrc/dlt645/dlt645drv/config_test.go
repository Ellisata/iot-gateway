// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"reflect"
	"testing"
)

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
	// 串口保留 30ms 收发间延时：RS-485 收发器换向 + 老表处理时间的预留
	if s.InterFrameDelayMS != 30 {
		t.Errorf("串口默认收发间延时 = %d, want 30", s.InterFrameDelayMS)
	}
	if !s.CheckChecksum {
		t.Error("默认应开启校验码校验")
	}

	// 单请求打包数：帧数 = ⌈点数/本值⌉，出厂必须是协议上限，
	// 否则大点位设备的单轮周期直接超出采集频率（设 1 时 2000 点档 100% 掉点）。
	if s.MaxDIsPerRead != maxDIsPerReadLimit {
		t.Errorf("默认单请求标识数 = %d, want %d（协议上限）", s.MaxDIsPerRead, maxDIsPerReadLimit)
	}

	// TCP 透传链路的两处「串口才需要的等待」都必须归零：
	// 唤醒前导由透传设备处理，换向由串口服务器 / DTU 处理 —— 网关这侧等待是纯空转，
	// 且代价随帧数线性放大。
	tp := DefaultDLT645Config(TransportTCP)
	if tp.PreambleBytes != 0 {
		t.Errorf("TCP 默认前导字节数 = %d, want 0", tp.PreambleBytes)
	}
	if tp.InterFrameDelayMS != 0 {
		t.Errorf("TCP 默认收发间延时 = %d, want 0（透传链路无需换向等待）", tp.InterFrameDelayMS)
	}
}

// 传输层由协议注册名固定：即便 protocol_json 里带了 transport，
// 也必须被协议注册名覆盖（否则一个设备能靠配置把自己指到另一条总线上）。
func TestTransportPinnedByProtocol(t *testing.T) {
	cfg, err := ParseDLT645Config(`{"transport":"SERIAL","comPort":"COM3"}`, TransportTCP)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != TransportTCP {
		t.Errorf("Transport = %q, want %q（由协议注册名固定）", cfg.Transport, TransportTCP)
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

// 兼容旧字段名 timeout，以及从 iot-gateway 侧搬来的 maxFollowUpFrames：
// 两者都不在表单里，但必须能被容错解析（老配置不能解析失败）。
func TestLegacyFieldsStillParsed(t *testing.T) {
	cfg, err := ParseDLT645Config(`{"timeout":"7000"}`, TransportSerial)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TimeoutMS != 7000 {
		t.Errorf("旧字段名 timeout 未生效: %d", cfg.TimeoutMS)
	}

	cfg2, err := ParseDLT645Config(`{"maxFollowUpFrames":"3"}`, TransportSerial)
	if err != nil {
		t.Fatalf("maxFollowUpFrames 应被容错解析: %v", err)
	}
	if cfg2.MaxFollowUpFrames != 3 {
		t.Errorf("MaxFollowUpFrames = %d, want 3", cfg2.MaxFollowUpFrames)
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

// ConfigFieldNames 是表单与配置结构体之间的契约，必须覆盖 dlt645ConfigRaw 的全部键，
// 只放行两个**有意为之**的例外。
//
// 它挡住的正是「配置结构体加了一个字段、忘了加进表单」——那时用户配不了那个参数，
// 驱动永远走默认值，而两边都没有任何提示。
func TestConfigFieldNamesCoverRawStruct(t *testing.T) {
	// 结构体里允许不出现在表单里的键，每一项都必须有理由：
	//   timeout            —— timeoutMs 的旧别名，只有解析路径需要它
	//   maxFollowUpFrames  —— 当前实现不拼接后续帧，该字段不参与读取逻辑
	exempt := map[string]bool{"timeout": true, "maxFollowUpFrames": true}

	rt := reflect.TypeOf(dlt645ConfigRaw{})
	all := make(map[string]bool, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Errorf("dlt645ConfigRaw 的字段 %s 没有 json tag", rt.Field(i).Name)
			continue
		}
		all[tag] = true
	}

	// union 是**两份表单合起来**覆盖到的键。
	// 覆盖检查必须按并集做：串口参数（comPort/baudRate/…）只属于 Serial 表单，
	// 以太网参数（host/port）只属于 TCP 表单，逐份去要求全覆盖是错的。
	union := make(map[string]bool, len(all))

	for _, transport := range []string{TransportSerial, TransportTCP} {
		fields := ConfigFieldNames(transport)
		got := make(map[string]bool, len(fields))
		for _, f := range fields {
			if got[f] {
				t.Errorf("%s：ConfigFieldNames 里 %q 重复", transport, f)
			}
			got[f] = true
			union[f] = true
			if !all[f] {
				t.Errorf("%s：ConfigFieldNames 里的 %q 不是 dlt645ConfigRaw 的 json tag"+
					"（表单会提交一个驱动不认识的键）", transport, f)
			}
			if exempt[f] {
				t.Errorf("%s：%q 属于有意排除的键，不应出现在 ConfigFieldNames 里", transport, f)
			}
		}
	}

	for tag := range all {
		if !union[tag] && !exempt[tag] {
			t.Errorf("dlt645ConfigRaw 的 %q 既不在任何一份表单里也不在豁免表中 —— "+
				"用户无法配置它，驱动只会用默认值", tag)
		}
	}

	// 两份表单的键集必须不同：串口参数与以太网参数没有交集，
	// 相同就说明有一份抄错了（表单没有按协议分家）。
	if reflect.DeepEqual(ConfigFieldNames(TransportSerial), ConfigFieldNames(TransportTCP)) {
		t.Error("串口与 TCP 的 ConfigFieldNames 完全相同，说明有一份漏改了")
	}
}
