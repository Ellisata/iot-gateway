// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbusdrv

import (
	"encoding/json"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/testutil/fake"
)

// targetFor 把一个 "host:port" 地址串变成探测目标。
// fake.Server.Addr() 返回的就是这个形式。
func targetFor(t *testing.T, addr string) driver.ScanTarget {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("解析地址 %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("解析端口 %q: %v", portStr, err)
	}
	return driver.ScanTarget{Host: host, Port: port}
}

// budget 用例使用的探测预算。远大于本地假设备的往返耗时，但不足以让
// "预算耗尽提前返回"那条路径被意外触发。
const testBudget = 3 * time.Second

func TestProbeTCPFindsDeviceWithDeviceID(t *testing.T) {
	srv, err := fake.NewModbusTCPWithDeviceID()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	devices, err := ProbeTCP(targetFor(t, srv.Addr()), nil, testBudget)
	if err != nil {
		t.Fatalf("ProbeTCP 不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(devices), devices)
	}

	d := devices[0]
	// 假设备只回基本三件套（无 0x05 模型名），因此 model() 应退到产品代码。
	if d.Vendor != "iot-gateway" {
		t.Errorf("厂商 = %q，期望 iot-gateway", d.Vendor)
	}
	if d.Model != "FX3U-64M" {
		t.Errorf("型号 = %q，期望 FX3U-64M（应从 ProductCode 退取）", d.Model)
	}
	if d.Version != "1.0.0" {
		t.Errorf("版本 = %q，期望 1.0.0", d.Version)
	}
	if d.Name != "iot-gateway FX3U-64M" {
		t.Errorf("展示名 = %q", d.Name)
	}
	if d.UnitID != 1 {
		t.Errorf("站号 = %d，期望默认 1", d.UnitID)
	}

	// ProtocolJSON 必须能被本包自己的配置解析器吃下，且落在目标上。
	cfg, err := ParseModbusTcpConfig(d.ProtocolJSON)
	if err != nil {
		t.Fatalf("探测器给出的连接配置解析失败: %v（原文 %s）", err, d.ProtocolJSON)
	}
	want := targetFor(t, srv.Addr())
	if cfg.Host != want.Host || cfg.Port != want.Port {
		t.Errorf("连接配置指向 %s:%d，期望 %s:%d", cfg.Host, cfg.Port, want.Host, want.Port)
	}
	if cfg.UnitID != 1 {
		t.Errorf("连接配置里站号 = %d，期望 1", cfg.UnitID)
	}
}

// TestProbeTCPFindsDeviceWithoutFC43 覆盖多数真实 PLC 的情形：
// 不支持读设备标识，但它是货真价实的 Modbus 从站（FC3 有应答）。
func TestProbeTCPFindsDeviceWithoutFC43(t *testing.T) {
	srv, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	devices, err := ProbeTCP(targetFor(t, srv.Addr()), nil, testBudget)
	if err != nil {
		t.Fatalf("ProbeTCP 不应报错: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台", len(devices))
	}
	d := devices[0]
	// 读不出标识不影响"发现了设备"，但厂商/型号应如实为空。
	if d.Vendor != "" || d.Model != "" || d.Version != "" {
		t.Errorf("不支持 FC43 时应无设备标识，实际 vendor=%q model=%q version=%q",
			d.Vendor, d.Model, d.Version)
	}
	if d.Name != "Modbus 设备" {
		t.Errorf("展示名 = %q，期望「Modbus 设备」", d.Name)
	}
	// 识别方式要说清是"退而求其次"判出来的，用户才知道为什么标识为空。
	if len(d.Extra) == 0 || !strings.Contains(d.Extra[0].Value, "不支持读设备标识") {
		t.Errorf("应标注识别方式，实际 %+v", d.Extra)
	}
}

// TestProbeTCPSkipsNonModbusListener 端口开着但说的不是 Modbus。
//
// 这是扫描时最常见的误报来源：502 上开着 HTTP，或者某厂商的私有协议。
// 判据是 MBAP 校验（协议号 / 事务号回显 / 长度自洽），这里用一个回垃圾字节的
// 监听器验证它确实被挡掉，而不是被当成一台设备。
func TestProbeTCPSkipsNonModbusListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 512)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
					// 一段与 MBAP 毫无关系的内容。
					if _, err := c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n")); err != nil {
						return
					}
				}
			}(conn)
		}
	}()

	devices, err := ProbeTCP(targetFor(t, ln.Addr().String()), nil, testBudget)
	if err != nil {
		t.Fatalf("对端不是 Modbus 不是错误: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("不应把非 Modbus 服务报成设备，实际 %+v", devices)
	}
}

// TestProbeTCPUnreachableIsNotAnError "没扫到"不是错误 —— 这是整个扫描能
// 在 /24 上跑的前提（否则一次扫描会产出两百多条"错误"噪声）。
func TestProbeTCPUnreachableIsNotAnError(t *testing.T) {
	// 拿一个刚关闭的监听器端口：连接会被立即拒绝。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	target := targetFor(t, ln.Addr().String())
	_ = ln.Close()

	devices, err := ProbeTCP(target, nil, testBudget)
	if err != nil {
		t.Fatalf("目标不可达必须是 (nil, nil)，实际 err = %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("目标不可达不应产出设备，实际 %+v", devices)
	}
}

// TestProbeTCPUnitSweep 站号扫描：列出来的应答、没列出来的不出现。
func TestProbeTCPUnitSweep(t *testing.T) {
	// 假设备只认 1 号与 5 号站，其余静默丢弃（模拟 Modbus TCP→RTU 网关）。
	srv, err := fake.NewModbusTCPWithUnitIDs(1, 5)
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	target := targetFor(t, srv.Addr())
	options := json.RawMessage(`{"unitIds":"1,3,5"}`)

	devices, err := ProbeTCP(target, options, testBudget)
	if err != nil {
		t.Fatalf("ProbeTCP 不应报错: %v", err)
	}
	got := map[int]bool{}
	for _, d := range devices {
		got[d.UnitID] = true
	}
	if !got[1] || !got[5] {
		t.Errorf("1 号与 5 号站都应答，实际只发现 %v", keysOf(got))
	}
	if got[3] {
		t.Errorf("3 号站不应答，不该被报成设备（实际发现 %v）", keysOf(got))
	}
	if len(devices) != 2 {
		t.Errorf("应发现 2 台，实际 %d 台", len(devices))
	}
	// 每条结果的连接配置必须带上自己的站号，否则建出来的设备会全指向 1 号站。
	for _, d := range devices {
		cfg, err := ParseModbusTcpConfig(d.ProtocolJSON)
		if err != nil {
			t.Fatalf("连接配置解析失败: %v", err)
		}
		if int(cfg.UnitID) != d.UnitID {
			t.Errorf("站号 %d 的连接配置里写的是 %d", d.UnitID, cfg.UnitID)
		}
	}
}

func TestParseUnitIDs(t *testing.T) {
	cases := []struct {
		in      string
		want    []int
		wantErr bool
	}{
		{in: "", want: nil},
		{in: "1", want: []int{1}},
		{in: "1,2,5", want: []int{1, 2, 5}},
		{in: "1-4", want: []int{1, 2, 3, 4}},
		{in: "1-3,7", want: []int{1, 2, 3, 7}},
		{in: " 2 , 3 ", want: []int{2, 3}},
		// 去重且保序：重复列出同一个站号不该扫两遍。
		{in: "1,1,2", want: []int{1, 2}},
		{in: "3-4,2", want: []int{3, 4, 2}},
		// 非法输入一律报错而不是静默丢弃：静默丢弃会让用户看到"少扫到几台"，
		// 而把排查方向引向网络。
		{in: "0", wantErr: true},
		{in: "248", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "5-1", wantErr: true},
		{in: "1-100", wantErr: true},
		{in: "1,a", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseUnitIDs(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseUnitIDs(%q) 应报错，实际得到 %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUnitIDs(%q) 报错: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("parseUnitIDs(%q) = %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseUnitIDs(%q) = %v，期望 %v", c.in, got, c.want)
				break
			}
		}
	}
}

// TestParseScanOptionsRejectsBadOption 选项非法应当**报错**（进而让用户看到
// 原因），而不是悄悄退回默认值扫一遍什么也没发现。
func TestParseScanOptionsRejectsBadOption(t *testing.T) {
	if _, err := ProbeTCP(driver.ScanTarget{Host: "127.0.0.1", Port: 1},
		json.RawMessage(`{"unitIds":"not-a-number"}`), testBudget); err == nil {
		t.Fatal("非法站号应报错")
	}
	// 空选项（长度为 0，非 JSON 串）走默认值，不是错误。
	if _, err := parseScanOptions(nil); err != nil {
		t.Fatalf("空选项不该报错: %v", err)
	}
}

// TestScanOptionsAcceptStringNumbers form-create 的不同控件对数值字段给出
// 的 JSON 形态不一致（数字 / 字符串）。声明成 int 时一份字符串会让整份选项
// 解析失败 —— 这是 config.go 的 rawInt 已经踩过的坑，扫描选项沿用同一处理。
func TestScanOptionsAcceptStringNumbers(t *testing.T) {
	opt, err := parseScanOptions(json.RawMessage(`{"dialTimeoutMs":"2000","requestTimeoutMs":2500}`))
	if err != nil {
		t.Fatalf("字符串形态的数值不该导致解析失败: %v", err)
	}
	if opt.dialTimeout != 2*time.Second {
		t.Errorf("dialTimeout = %s，期望 2s", opt.dialTimeout)
	}
	if opt.requestTimeout != 2500*time.Millisecond {
		t.Errorf("requestTimeout = %s，期望 2.5s", opt.requestTimeout)
	}
}

// TestProbeTCPRespectsTinyBudget 预算耗尽时必须正常返回而不是报错。
//
// 预算撞满会撞网关侧 RPC 超时并累积插件卡死计数，连续几次会把整个插件进程
// 连同它下面所有协议、所有设备一起重启 —— 所以"超预算"绝不能表现为 error。
func TestProbeTCPRespectsTinyBudget(t *testing.T) {
	// 一个只接受连接、从不回数据的监听器：正常探测会一直等到超时。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 故意不读不写，让对端一直等。
			_ = conn
		}
	}()

	start := time.Now()
	devices, err := ProbeTCP(targetFor(t, ln.Addr().String()), nil, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("预算耗尽必须正常返回，实际 err = %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("没有应答就不该有设备，实际 %+v", devices)
	}
	// 允许一点调度余量，但必须明显在预算量级内 —— 若这里跑到秒级，
	// 说明预算没有被真正传递到每一次交换上。
	if elapsed > 2*time.Second {
		t.Fatalf("预算 300ms 却耗时 %s，预算没有被贯彻", elapsed)
	}
}

func keysOf(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// scanFormFields 取出扫描表单里的全部 field 名。
func scanFormFields(t *testing.T, raw string) map[string]bool {
	t.Helper()
	var form struct {
		Rule []struct {
			Field string `json:"field"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(raw), &form); err != nil {
		t.Fatalf("解析扫描表单失败: %v", err)
	}
	out := make(map[string]bool, len(form.Rule))
	for _, r := range form.Rule {
		out[r.Field] = true
	}
	return out
}

// TestScanFormMatchesOptions 钉住扫描表单的字段名与选项解析结构**双向一致**。
//
// 与 main_test.go 的 TestFormsMatchConfigStruct 同源（那边是协议配置表单，
// 这边是扫描选项表单）：表单里的 field 一旦与解析结构的 json tag 对不上，
// 用户在页面上填的选项就永远不会生效，而两边都不报任何错。反方向同样要管 ——
// 解析结构里多出一个表单没有的选项，它就只能靠手写 API 调用去设置，
// 那等于一个没人知道的暗门。
func TestScanFormMatchesOptions(t *testing.T) {
	// 解析层认识的字段 = scanOptionsRaw 的 json tag。
	valid := make(map[string]bool)
	rt := reflect.TypeOf(scanOptionsRaw{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" {
			valid[tag] = true
		}
	}

	form := scanFormFields(t, scanForm)
	for field := range form {
		if !valid[field] {
			t.Errorf("扫描表单字段 %q 不在 scanOptionsRaw 里 —— 用户填了也不生效", field)
		}
	}
	for field := range valid {
		if !form[field] {
			t.Errorf("scanOptionsRaw 的字段 %q 不在扫描表单里 —— 只能靠手写 API 调用去设置", field)
		}
	}
}

// TestDiscoverySpecIsServable 扫描能力声明的取值范围必须能过 Meta.Validate
// 的校验（那几条上下界是硬约束，越界会让插件启动失败）。
func TestDiscoverySpecIsServable(t *testing.T) {
	spec := Discovery()
	if spec.Title == "" {
		t.Error("Title 不能为空")
	}
	if spec.DefaultPort != 502 {
		t.Errorf("默认端口 = %d，期望 502", spec.DefaultPort)
	}
	if spec.MaxBudgetMs < 500 || spec.MaxBudgetMs > 60000 {
		t.Errorf("MaxBudgetMs = %d，超出 Meta.Validate 允许的 [500,60000]", spec.MaxBudgetMs)
	}
	if !json.Valid(spec.FormJSON) {
		t.Error("FormJSON 必须是合法 JSON")
	}
	if spec.TargetMode != driver.TargetModeUnicast {
		t.Errorf("TargetMode = %q，期望 unicast", spec.TargetMode)
	}
}
