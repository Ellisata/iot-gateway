// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
)

// ==================== 扫描选项 ====================

// TestParseScanOptions 缺项走默认、给了就用给的，且数字的两种 JSON 形态都收。
//
// 形态那一半是本文件里最实际的一条：form-create 的 inputNumber 送 JSON 数字，
// 但同一份表单在别的控件/别的前端版本下会送字符串，而声明成 int 时一个字符串
// 会让 json.Unmarshal **整体**失败 —— 整次扫描全挂，而不是那一栏失效。
func TestParseScanOptions(t *testing.T) {
	cases := []struct {
		name    string
		options string
		want    time.Duration
	}{
		{"空选项走默认", ``, defaultScanTimeoutMs * time.Millisecond},
		{"数字形态", `{"timeoutMs":800}`, 800 * time.Millisecond},
		{"字符串形态", `{"timeoutMs":"800"}`, 800 * time.Millisecond},
		{"零与负数走默认", `{"timeoutMs":0}`, defaultScanTimeoutMs * time.Millisecond},
		{"认不出的值走默认", `{"timeoutMs":"abc"}`, defaultScanTimeoutMs * time.Millisecond},
		{"多余的键被忽略", `{"timeoutMs":900,"who":"x"}`, 900 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opts json.RawMessage
			if c.options != "" {
				opts = json.RawMessage(c.options)
			}
			got, err := parseScanOptions(opts)
			if err != nil {
				t.Fatalf("parseScanOptions = %v", err)
			}
			if got.timeout != c.want {
				t.Errorf("timeout = %v, want %v", got.timeout, c.want)
			}
		})
	}

	// 非法的选项 JSON 必须**报错**：这是用户的输入错误，静默回默认值会让用户
	// 以为"选项生效了"，而实际用的是另一个值。
	if _, err := parseScanOptions(json.RawMessage(`{"timeoutMs":`)); err == nil {
		t.Error("非法 JSON 应报错，而不是静默回默认值")
	}
}

// ==================== 探测 ====================

// mcReadResp 组装一条"批量读成功"的 3E 响应（数据长度 = 结束码 2 字节 + 数据）。
func mcReadResp(data []byte) []byte {
	resp := []byte{
		0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00, // 子头 + 网络/PC/IO/站（I/O 小端）
	}
	dl := 2 + len(data)
	resp = append(resp, byte(dl&0xFF), byte(dl>>8)) // 响应数据长度（小端）
	resp = append(resp, 0x00, 0x00)                 // 结束码：正常
	return append(resp, data...)
}

// mcEndCodeResp 组装一条只带结束码的 3E 响应（数据长度 = 2）。
func mcEndCodeResp(code uint16) []byte {
	return []byte{
		0xD0, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x00,
		0x02, 0x00, // 响应数据长度 = 结束码 2（小端）
		byte(code & 0xFF), byte(code >> 8), // 结束码（小端）
	}
}

// TestProbeTCPFindsDevice 假 PLC 正常应答时，探测必须报出设备，
// 且伪造的连接配置能被**插件自己的**配置解析器吃下。
//
// 后半条是本测试的重点：ProtocolJSON 里的键名/类型与 mcConfigRaw 只要有一处
// 对不上（例如把 port 写成字符串），一键建出来的设备就连不上，而扫描结果页
// 看着完全正常。
func TestProbeTCPFindsDevice(t *testing.T) {
	port, reqCh, errCh := startFakeMC(t, mcReadResp([]byte{0x34, 0x12}))
	target := driver.ScanTarget{Host: "127.0.0.1", Port: port}

	devs, err := ProbeTCP(target, nil, 3*time.Second)
	if err != nil {
		t.Fatalf("ProbeTCP = %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("应发现 1 台设备，实际 %d 台: %+v", len(devs), devs)
	}
	d := devs[0]

	if d.Host != "127.0.0.1" || d.Port != port {
		t.Errorf("地址 = %s:%d，期望 127.0.0.1:%d", d.Host, d.Port, port)
	}
	// MC 没有任何设备标识读取手段，这三项必须留空 —— 填 "Mitsubishi" 是拿协议名
	// 冒充设备自述，而 SLMP 并非只有三菱在用。
	if d.Vendor != "" || d.Model != "" || d.Version != "" {
		t.Errorf("Vendor/Model/Version = %q/%q/%q，MC 读不出设备标识，应全部留空",
			d.Vendor, d.Model, d.Version)
	}
	if d.Name == "" {
		t.Error("Name 为空，扫描结果列表里会是一行空白")
	}
	// UnitID 留 0：3E 帧的站号在驱动里是写死的常量，不是可配项。
	if d.UnitID != 0 {
		t.Errorf("UnitID = %d，MC 的站号不可配，应为 0", d.UnitID)
	}
	if !strings.Contains(extraValue(d, "识别方式"), "D 区数据") {
		t.Errorf("识别方式 = %q，读到数据时应说明是读到 D 区数据得来", extraValue(d, "识别方式"))
	}

	// 关键：这份配置必须能被插件自己的解析器还原成同一个地址。
	cfg, err := ParseMCConfig(d.ProtocolJSON)
	if err != nil {
		t.Fatalf("扫描结果里的连接配置解析失败（一键建设备会连不上）: %v（原文 %s）", err, d.ProtocolJSON)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != port {
		t.Errorf("解析回 %s:%d，期望 127.0.0.1:%d（配置原文 %s）",
			cfg.Host, cfg.Port, port, d.ProtocolJSON)
	}

	// 探测确实发出了**一条标准读帧**，读的是 D0 一个字 —— 不是握手、不是别的地址。
	select {
	case req := <-reqCh:
		want, _ := lookupDevice("D")
		if exp := buildReadFrame(want, 0, 1, false); string(req) != string(exp) {
			t.Errorf("请求帧 =\n % X\n want\n % X", req, exp)
		}
	case err := <-errCh:
		t.Fatalf("server: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("假 PLC 没收到请求")
	}
}

// TestProbeTCPEndCodeStillDevice 设备回结束码错误时**照样报出来**。
//
// 这一格最容易写错：把"设备明确拒绝"当成"没扫到"，会让一台真实存在的 PLC
// 因为一句不认识的结束码而从扫描结果里消失，而用户在页面上完全看不出发生过什么。
// 与 finsdrv「结束码错误也算设备」是同一条取舍。
func TestProbeTCPEndCodeStillDevice(t *testing.T) {
	for _, code := range []uint16{endCodeAddrRange, endCodeDeviceIllegal, endCodeCommError} {
		t.Run(strings.ToUpper(strconvHex(code)), func(t *testing.T) {
			port, _, _ := startFakeMC(t, mcEndCodeResp(code))
			devs, err := ProbeTCP(driver.ScanTarget{Host: "127.0.0.1", Port: port}, nil, 3*time.Second)
			if err != nil {
				t.Fatalf("ProbeTCP = %v", err)
			}
			if len(devs) != 1 {
				t.Fatalf("结束码 0x%04X：应仍报出 1 台设备（它应答了），实际 %d 台", code, len(devs))
			}
			// 这一格的价值就是"能不能建出可用的设备"，所以配置照样必须可解析。
			if _, err := ParseMCConfig(devs[0].ProtocolJSON); err != nil {
				t.Errorf("连接配置解析失败: %v", err)
			}
			if !strings.Contains(extraValue(devs[0], "识别方式"), "结束码") {
				t.Errorf("识别方式 = %q，应说明是靠结束码应答认定的", extraValue(devs[0], "识别方式"))
			}
		})
	}
}

// strconvHex 只用于子测试名，避免为一个名字引入 fmt 的格式化。
func strconvHex(v uint16) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[(v>>12)&0xF], digits[(v>>8)&0xF], digits[(v>>4)&0xF], digits[v&0xF]})
}

// TestProbeTCPIgnoresSilentService 连得上但一直不说话的端口**不是设备**。
//
// 这是探测里最常见的"假阳性来源"：端口开着（串口服务器、SCADA 网关、
// 别的私有协议）但不会回一条 MC 形状的应答。判据必须落在**应答的形状**上，
// 而不是"TCP 连上了"。
//
// 同时钉住预算那条承诺：用户把超时填到 1500ms，而本次探测只分到 300ms，
// 实际等待必须跟着 300ms 走 —— 否则一个填大的超时值就能把"prober 一定在
// Budget 内返回"变成摆设，进而撞上网关侧 RPC 超时、累积插件卡死计数。
func TestProbeTCPIgnoresSilentService(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 收下请求就闭嘴：不回任何字节，也不关连接。
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	budget := 300 * time.Millisecond
	start := time.Now()
	devs, err := ProbeTCP(
		driver.ScanTarget{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port},
		json.RawMessage(`{"timeoutMs":1500}`), // 用户填的超时远大于本次预算
		budget)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("沉默的服务不是错误（「没扫到」是正常结果）: %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("沉默的服务被报成了设备: %+v", devs)
	}
	// 允许一点调度与拨号余量，但不能接近用户填的 1500ms。
	if elapsed > budget+500*time.Millisecond {
		t.Errorf("探测耗时 %v，远超本次预算 %v —— 用户的超时值没有被预算钳住", elapsed, budget)
	}
}

// TestProbeTCPDialFailureIsNotDevice 端口上没人监听时返回"没扫到"，不是错误。
//
// 一次 /24 扫描里绝大多数地址都走这条分支；把它当 error 会产生两百多条噪声。
func TestProbeTCPDialFailureIsNotDevice(t *testing.T) {
	// 先占一个端口再关掉，拿到一个几乎确定没人在听的端口号。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	devs, err := ProbeTCP(driver.ScanTarget{Host: "127.0.0.1", Port: port}, nil, 2*time.Second)
	if err != nil {
		t.Fatalf("拨号失败不是错误: %v", err)
	}
	if len(devs) != 0 {
		t.Errorf("不可达的目标被报成了设备: %+v", devs)
	}
}

// TestProbeTCPSkipsBroadcastAndEmptyHost 两条防御性分支。
//
// 广播目标不该出现（本协议声明的是 unicast），真出现时朝子网广播地址连 TCP
// 等于同时对整个网段发起连接；空 host 则是引擎侧的数据问题，两者都直接返回。
func TestProbeTCPSkipsBroadcastAndEmptyHost(t *testing.T) {
	if devs, err := ProbeTCP(driver.ScanTarget{Host: "192.168.1.255", Broadcast: true}, nil, time.Second); err != nil || len(devs) != 0 {
		t.Errorf("广播目标应被跳过，得到 %+v / %v", devs, err)
	}
	if devs, err := ProbeTCP(driver.ScanTarget{Host: "   "}, nil, time.Second); err != nil || len(devs) != 0 {
		t.Errorf("空 host 应被跳过，得到 %+v / %v", devs, err)
	}
}

// TestScanConfigCarriesBudget scanConfig 里两个超时字段必须指向**本次分到的预算**。
//
// TimeoutMS 一并写上不是冗余：newMCTCPClient 在 `timeout <= 0` 时会回落到 5s，
// 而 5s 远超探测预算 —— 只有两个字段都被赋值，读哪个都得到同一个受钳的值。
func TestScanConfigCarriesBudget(t *testing.T) {
	cfg := scanConfig("10.255.255.1", 5008, 200*time.Millisecond)
	if cfg.Transport != TransportTCP {
		t.Errorf("Transport = %q，探测必须走 3E 帧 TCP", cfg.Transport)
	}
	if cfg.Host != "10.255.255.1" || cfg.Port != 5008 {
		t.Errorf("地址 = %s:%d，期望原样带走 10.255.255.1:5008", cfg.Host, cfg.Port)
	}
	if cfg.Timeout != 200*time.Millisecond || cfg.TimeoutMS != 200 {
		t.Errorf("Timeout/TimeoutMS = %v/%d，应取本次分到的预算（否则客户端会回落到 5s，远超预算）",
			cfg.Timeout, cfg.TimeoutMS)
	}
}

// TestBuildMCDeviceProtocolJSONKeys 连接配置的键名与类型必须与 mcConfigRaw 对上。
//
// 单独一条测试盯着字面量：port 写成字符串是这里最容易犯的错，而它会被
// json.Unmarshal **静默**丢弃、回落到默认端口 —— 扫描结果正常，建出来的设备连不上。
func TestBuildMCDeviceProtocolJSONKeys(t *testing.T) {
	d := buildMCDevice("192.168.1.10", 5008, true)

	var raw map[string]any
	if err := json.Unmarshal([]byte(d.ProtocolJSON), &raw); err != nil {
		t.Fatalf("连接配置不是合法 JSON: %v", err)
	}
	if got, ok := raw["host"].(string); !ok || got != "192.168.1.10" {
		t.Errorf("host = %v (%T)，期望字符串 192.168.1.10", raw["host"], raw["host"])
	}
	if got, ok := raw["port"].(float64); !ok || int(got) != 5008 {
		t.Errorf("port = %v (%T)，期望**数字** 5008（写成字符串会被静默丢弃）", raw["port"], raw["port"])
	}

	cfg, err := ParseMCConfig(d.ProtocolJSON)
	if err != nil {
		t.Fatalf("ParseMCConfig = %v", err)
	}
	if cfg.Port != 5008 {
		t.Errorf("解析回端口 %d，期望 5008 —— 非默认端口必须原样带走", cfg.Port)
	}
}

// extraValue 取 DiscoveredDevice.Extra 里某一项的取值。
func extraValue(d driver.DiscoveredDevice, name string) string {
	for _, a := range d.Extra {
		if a.Name == name {
			return a.Value
		}
	}
	return ""
}
