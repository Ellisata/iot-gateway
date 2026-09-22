// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"iot-gateway/configFile"
	"iot-gateway/database/sqlite"
	"iot-gateway/driver"
	"iot-gateway/model/po"
)

func TestDriverRegistered(t *testing.T) {
	for _, proto := range []string{ProtocolDLT645Serial, ProtocolDLT645TCP} {
		d, err := driver.Create(proto)
		if err != nil {
			t.Fatalf("driver.Create(%q) 失败: %v", proto, err)
		}
		if d == nil {
			t.Fatalf("driver.Create(%q) 返回 nil", proto)
		}
	}
}

// 串口独占 RS-485 总线需整轮持锁；TCP 非独占，否则会白白拖住整轮采集。
func TestSerialExclusive(t *testing.T) {
	if !newDLT645Driver(TransportSerial).SerialExclusive() {
		t.Error("串口驱动应为独占")
	}
	if newDLT645Driver(TransportTCP).SerialExclusive() {
		t.Error("TCP 驱动不应为独占")
	}
}

// 协议名 → scope 映射与类型映射表必须同时生效，否则保存点位会直接报类型错误。
func TestScopeAndTypeMap(t *testing.T) {
	for _, proto := range []string{ProtocolDLT645Serial, ProtocolDLT645TCP} {
		if got := driver.ScopeOf(proto); got != protocolName {
			t.Errorf("ScopeOf(%q) = %q, want %q", proto, got, protocolName)
		}
	}

	// 13 个通用类型应全部可配置（无隐藏），且不存在扩展类型
	common, extended := driver.AvailableTypes(protocolName)
	if len(common) != len(driver.CommonDataTypes) {
		t.Errorf("AvailableTypes common = %v, want 全部 %v", common, driver.CommonDataTypes)
	}
	if len(extended) != 0 {
		t.Errorf("AvailableTypes extended = %v, want 空", extended)
	}

	want := map[string]string{
		"Boolean": "bool", "Date": "datetime", "String": "string",
		"Byte": "uint", "Char": "int", "Short": "int", "Word": "uint",
		"DWord": "uint", "Long": "int", "Float": "float", "Double": "float",
		"BCD": "bcd", "LBCD": "lbcd",
	}
	for commonType, internal := range want {
		got, ok := driver.LookupDataType(ProtocolDLT645Serial, commonType)
		if !ok || got != internal {
			t.Errorf("LookupDataType(%s) = (%q,%v), want (%q,true)", commonType, got, ok, internal)
		}
		// 映射到的内部类型必须真的注册过，否则 AvailableTypes 会静默隐藏它
		if _, ok := dltScope.Get(got); !ok {
			t.Errorf("内部类型 %q 未在 %s scope 注册", got, protocolName)
		}
	}
}

// 漂移自愈：内部类型名损坏时应由通用类型名回退，不干扰有效值。
func TestNormalizeAddressType(t *testing.T) {
	valid := driver.NormalizeAddressType(ProtocolDLT645TCP,
		po.DeviceAddress{DataType: "float", CommonDataType: "Float"})
	if valid != "float" {
		t.Errorf("有效内部名应原样保留，得到 %q", valid)
	}
	drifted := driver.NormalizeAddressType(ProtocolDLT645TCP,
		po.DeviceAddress{DataType: "bogus", CommonDataType: "Float"})
	if drifted != "float" {
		t.Errorf("漂移应回退为 float，得到 %q", drifted)
	}
}

// 应答含后续帧（0xB1）时，该组点位必须保持 Quality=0，且不得中断整台设备的采集：
//   - 拿首帧的片段解码，会得到一个看似合理实则被截断的假值；
//   - 把它当 I/O 错误上抛，采集引擎会把整台设备判离线并重连。
func TestFollowUpResponseKeepsDeviceOnline(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	cfg.PreambleBytes = 0
	addr, _ := MeterAddressBytes("1")

	spec := DISpec{DI: 0x02010100, Version: Version2007, Bytes: 2, Decimals: 1}
	plan := &readPlan{entries: []planEntry{{index: 0, dataType: "float", spec: spec}}}
	group := &readGroup{idx: []int{0}}

	// 0xB1 应答：数据标识 + 该数据项的**首段**数据（A相电压只应有 2 字节，此处给的正是完整值，
	// 但既然表声明还有后续帧，就不能拿它当最终值）
	frame := buildFrame(cfg, addr, ctrl2007More, []byte{0x00, 0x01, 0x01, 0x02, 0x05, 0x22})
	tr := &fakeTransport{in: frame}

	results := make([]driver.ReadResult, 1)
	d := newDLT645Driver(TransportTCP)
	if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
		t.Fatalf("含后续帧的应答不应中断采集（会让整台设备判离线）: %v", err)
	}
	if results[0].Quality != 0 {
		t.Errorf("Quality = %d, want 0（数据不完整时不得解码）", results[0].Quality)
	}
	if results[0].Value != "" {
		t.Errorf("Value = %q, want 空（不完整的数据不得取值）", results[0].Value)
	}
}

// 被电表拒读的告警只报一次（状态变化时），读到值后清除：
// 扫描周期 1s 时，现场日志实测 10 分钟刷了 124 条完全相同的告警。
func TestRejectionWarnSuppressedThenResolves(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	addr, _ := MeterAddressBytes("1")

	spec, err := ParseAddress("00000000", Version2007)
	if err != nil {
		t.Fatal(err)
	}
	plan := &readPlan{entries: []planEntry{{index: 0, dataType: "float", spec: spec}}}
	group := &readGroup{idx: []int{0}}
	diText := formatDI(spec.DI, Version2007)

	d := newDLT645Driver(TransportTCP)

	// 连续两轮都被拒：第二轮是重复告警，问题状态必须保持不变
	for round := 1; round <= 2; round++ {
		tr := &fakeTransport{in: buildFrame(cfg, addr, ctrl2007Abnormal, []byte{0x01})}
		results := make([]driver.ReadResult, 1)
		if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
			t.Fatalf("第 %d 轮：异常应答不应中断采集（会让整台设备判离线）: %v", round, err)
		}
		if results[0].Quality != 0 {
			t.Errorf("第 %d 轮 Quality = %d, want 0", round, results[0].Quality)
		}
		if !d.problems.abnormal(diText) {
			t.Fatalf("第 %d 轮：被拒后应记录异常状态", round)
		}
	}

	// 恢复：正常应答应取到值，并清掉异常状态
	okData := []byte{0x00, 0x00, 0x00, 0x00, 0x28, 0x01, 0x00, 0x00} // 数据标识 + BCD 00000128
	tr := &fakeTransport{in: buildFrame(cfg, addr, ctrl2007OK, okData)}
	results := make([]driver.ReadResult, 1)
	if err := d.readGroup(tr, cfg, addr, plan, group, results); err != nil {
		t.Fatalf("正常应答应能读取: %v", err)
	}
	if results[0].Quality != 192 {
		t.Errorf("Quality = %d, want 192", results[0].Quality)
	}
	if results[0].Value != "1.28" {
		t.Errorf("Value = %q, want \"1.28\"", results[0].Value)
	}
	if d.problems.abnormal(diText) {
		t.Error("恢复后不应仍标记为异常（否则真正的再次故障会被压掉）")
	}
}

// newLiveSerialDriver 造一个「已连上串口」的驱动实例，配置来自同一份 JSON，
// 因此 MatchConnection/Ping 的复用分支应当命中。
func newLiveSerialDriver(t *testing.T, json string) *dlt645Driver {
	t.Helper()
	cfg, err := ParseDLT645Config(json, TransportSerial)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	d := newDLT645Driver(TransportSerial)
	d.mu.Lock()
	d.config = cfg
	d.client = &fakeTransport{}
	d.mu.Unlock()
	return d
}

// MatchConnection 只在串口上成立。
//
// TCP 复用是有害的：采集引擎那条连接可能早已失效（对端串口服务器重启）而 connected
// 仍为 true，复用会把一次本可成功的拨号测试报成失败，而新开一条 TCP 连接的代价为零。
func TestMatchConnectionSerialOnly(t *testing.T) {
	const cfgJSON = `{"protocolVersion":"1997","meterAddress":"1","comPort":"COM1"}`

	tcp := newDLT645Driver(TransportTCP)
	tcp.mu.Lock()
	tcp.config = DefaultDLT645Config(TransportTCP)
	tcp.client = &fakeTransport{}
	tcp.mu.Unlock()
	if tcp.MatchConnection(cfgJSON) {
		t.Error("TCP 驱动不应参与串口连接复用")
	}

	if !newLiveSerialDriver(t, cfgJSON).MatchConnection(cfgJSON) {
		t.Error("串口驱动参数一致时应判定可复用")
	}
}

// 参数对不上就不许复用：表号与版本已固化在连接里，复用会把请求发到别的表上。
func TestMatchConnectionRejectsMismatch(t *testing.T) {
	const cfgJSON = `{"protocolVersion":"1997","meterAddress":"1","comPort":"COM1"}`
	d := newLiveSerialDriver(t, cfgJSON)

	cases := map[string]string{
		"表号不同":    `{"protocolVersion":"1997","meterAddress":"2","comPort":"COM1"}`,
		"版本不同":    `{"protocolVersion":"2007","meterAddress":"1","comPort":"COM1"}`,
		"串口不同":    `{"protocolVersion":"1997","meterAddress":"1","comPort":"COM2"}`,
		"JSON 非法": "not json",
	}
	for name, in := range cases {
		if d.MatchConnection(in) {
			t.Errorf("%s：不应判定为可复用", name)
		}
	}

	// 未连接过（config 为 nil）时不得判定可复用
	fresh := newDLT645Driver(TransportSerial)
	if fresh.MatchConnection(cfgJSON) {
		t.Error("未连接过的实例不应判定为可复用")
	}
}

// SerialResource 报的是「句柄在谁手里」，与链路是否可用无关：
// 串口读失败只把连接标记为断开、并不释放句柄，那个端口仍被本实例占着。
func TestSerialResource(t *testing.T) {
	const cfgJSON = `{"protocolVersion":"1997","meterAddress":"1","comPort":"COM1"}`

	if got := newLiveSerialDriver(t, cfgJSON).SerialResource(); got != "COM1" {
		t.Errorf("SerialResource = %q, want COM1", got)
	}

	tcp := newDLT645Driver(TransportTCP)
	tcp.mu.Lock()
	tcp.config = DefaultDLT645Config(TransportTCP)
	tcp.client = &fakeTransport{}
	tcp.mu.Unlock()
	if got := tcp.SerialResource(); got != "" {
		t.Errorf("TCP 驱动不持有串口，SerialResource = %q, want 空", got)
	}

	fresh := newDLT645Driver(TransportSerial)
	if got := fresh.SerialResource(); got != "" {
		t.Errorf("未连接过的实例不持有串口，SerialResource = %q, want 空", got)
	}
}

// Ping 复用已有连接——「测试连接报 Access is denied」的回归用例。
//
// 断言手法：把实例的 ComPort 设成一个**必然打不开的假串口**。
// 一旦 Ping 走了「开临时连接」那条路就必定失败，只有在复用了已持有连接时才能成功。
func TestPingReusesLiveSerialConnection(t *testing.T) {
	const cfgJSON = `{"protocolVersion":"1997","meterAddress":"3","comPort":"COM_NOT_EXIST_1"}`

	d := newLiveSerialDriver(t, cfgJSON)
	// 响应报文只对上了表号，才不会在地址域核对时被当成「别的表的应答」丢掉
	tr := &fakeTransport{in: real1997Response}
	d.mu.Lock()
	d.client = tr
	d.mu.Unlock()

	if err := d.Ping(cfgJSON); err != nil {
		t.Fatalf("Ping 应复用已有连接并成功，实际报错: %v", err)
	}
	if len(tr.out) == 0 {
		t.Error("复用路径没有向已有连接发出任何帧")
	}
}

func TestSameConfig(t *testing.T) {
	a := DefaultDLT645Config(TransportSerial)
	b := DefaultDLT645Config(TransportSerial)
	if !sameConfig(a, b) {
		t.Error("同参数配置应判定为一致（Ping 据此复用串口句柄）")
	}
	b.Version = Version1997
	if sameConfig(a, b) {
		t.Error("协议版本不同不应复用连接")
	}
	if sameConfig(nil, a) || sameConfig(a, nil) {
		t.Error("nil 配置不应判定为一致")
	}
}

// 迁移必须能真正应用，且 form_json 的 field 名必须与 Go 配置结构体的 json tag 一致。
//
// 这是驱动与它的种子数据之间的契约：字段名对不上时，表单里填的值会被驱动静默忽略
// （永远落到默认值），排查成本极高，因此在这里用反射把两侧钉死。
func TestMigrationFormFieldsMatchConfigStruct(t *testing.T) {
	dir := t.TempDir()
	cfg := &configFile.Config{}
	cfg.SQLite.Path = filepath.Join(dir, "t.db")
	cfg.Log.Level = "ERROR"

	db := sqlite.InitDB(cfg)
	if db == nil {
		t.Fatal("InitDB 返回 nil")
	}
	// 关闭连接，否则 Windows 下 t.TempDir 清理会因文件被占用而失败
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	var rows []struct {
		Name     string
		FormJSON string
	}
	if err := db.Table("iot_protocol").
		Select("name, form_json").
		Where("name LIKE ?", "DLT645%").
		Scan(&rows).Error; err != nil {
		t.Fatalf("查询 iot_protocol 失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("迁移后 DLT645 协议数 = %d, want 2", len(rows))
	}

	// 收集 Go 配置结构体可接受的 json 字段名
	valid := map[string]bool{}
	rt := reflect.TypeOf(DLT645Config{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			valid[name] = true
		}
	}

	seen := map[string]bool{}
	for _, r := range rows {
		var form struct {
			Rule []struct {
				Field string `json:"field"`
				Type  string `json:"type"`
			} `json:"rule"`
		}
		if err := json.Unmarshal([]byte(r.FormJSON), &form); err != nil {
			t.Fatalf("%s 的 form_json 解析失败: %v", r.Name, err)
		}
		if len(form.Rule) == 0 {
			t.Fatalf("%s 的 form_json 没有字段", r.Name)
		}
		for _, f := range form.Rule {
			// 串口名由现场实际接了什么决定，网关侧枚举不出来 ——
			// 固定选项的 select 会把 COM7、/dev/ttyUSB1 这类端口直接挡在门外，
			// 而前端并未给 select 开 filterable/allow-create（已核对 web/dist 产物）。
			if f.Field == "comPort" && f.Type != "input" {
				t.Errorf("%s 的 comPort 是 %q 控件，必须是可自由输入的 input", r.Name, f.Type)
			}
			if !valid[f.Field] {
				t.Errorf("%s 的表单字段 %q 在 DLT645Config 中没有对应的 json tag（填了也会被驱动忽略）",
					r.Name, f.Field)
			}
		}
		seen[r.Name] = true
	}
	for _, proto := range []string{ProtocolDLT645Serial, ProtocolDLT645TCP} {
		if !seen[proto] {
			t.Errorf("迁移缺少协议 %s", proto)
		}
	}
}

// 端到端回归：采集引擎登记的串口实例，必须能被 HTTP「测试连接」那条 PingDevice 路径复用。
//
// 这是用户报的那个场景的完整复刻——引擎持着 COM1 采集，用户在设备页点测试连接。
// 上一层的 TestPingReusesLiveSerialConnection 只验到驱动，这里把「按协议名反查实例」
// 这一段也接上：漏了它就是原来那个 Access is denied。
func TestPingDeviceReusesRegisteredSerialDriver(t *testing.T) {
	const cfgJSON = `{"protocolVersion":"1997","meterAddress":"3","comPort":"COM_NOT_EXIST_1"}`

	d := newLiveSerialDriver(t, cfgJSON)
	tr := &fakeTransport{in: real1997Response}
	d.mu.Lock()
	d.client = tr
	d.mu.Unlock()

	driver.RegisterSerialOwner(ProtocolDLT645Serial, "dlt1997-串口", d)
	defer driver.UnregisterSerialOwner(d)

	if err := driver.PingDevice(ProtocolDLT645Serial, cfgJSON); err != nil {
		t.Fatalf("PingDevice 应复用已登记的串口连接并成功，实际报错: %v", err)
	}
	if len(tr.out) == 0 {
		t.Error("复用路径没有向已有连接发出任何帧")
	}

	// 注销之后就该退回临时实例路径：COM_NOT_EXIST_1 打不开，必然失败
	driver.UnregisterSerialOwner(d)
	if err := driver.PingDevice(ProtocolDLT645Serial, cfgJSON); err == nil {
		t.Error("注销后不应再复用该实例（临时实例打不开假串口，应当报错）")
	}
}
