// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/model/dto"
)

// TestSerialBusyHint 占用提示的判据与措辞。
//
// 提示只在 ping 失败时拼接（由调用方保证），本函数只负责"这个口被谁占着"。
// 措辞刻意陈述事实而不断言因果：即便真实失败原因是别的（比如从站号填错），
// 这句话依然是真的，不会把现场引到沟里。
func TestSerialBusyHint(t *testing.T) {
	holders := []collector.SerialPortHolder{
		{DeviceID: "d1", DeviceName: "1号电表", Port: "COM1", Connected: true},
		{DeviceID: "d2", DeviceName: "2号电表", Port: "com1", Connected: true}, // 大小写变体
		{DeviceID: "d3", DeviceName: "备用表", Port: "COM9", Connected: false},
	}

	cases := []struct {
		name         string
		protocolJSON string
		wantEmpty    bool
		wantContains []string
	}{
		{
			name:         "同一端口（大小写不敏感）命中",
			protocolJSON: `{"comPort":"COM1"}`,
			wantContains: []string{"COM1", "1号电表", "2号电表", "占用"},
		},
		{
			name:         "小写被查也命中",
			protocolJSON: `{"comPort":"com1"}`,
			wantContains: []string{"1号电表"},
		},
		{
			name:         "无人在此端口",
			protocolJSON: `{"comPort":"COM7"}`,
			wantEmpty:    true,
		},
		{
			name:         "请求里没有串口名（以太网协议）",
			protocolJSON: `{"host":"192.168.1.10","port":502}`,
			wantEmpty:    true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := serialBusyHint(c.protocolJSON, holders)
			if c.wantEmpty {
				if got != "" {
					t.Fatalf("不该给提示，实得 %q", got)
				}
				return
			}
			for _, want := range c.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("提示 %q 里应包含 %q", got, want)
				}
			}
		})
	}

	t.Run("只有未连接的持有者时措辞留余地", func(t *testing.T) {
		// 设备懒连接 / 正处于重连退避窗口内时句柄可能已释放，
		// 此时断言"被占用"就是凭空断因果了。
		only := []collector.SerialPortHolder{{DeviceName: "备用表", Port: "COM9", Connected: false}}
		got := serialBusyHint(`{"comPort":"COM9"}`, only)
		if !strings.Contains(got, "备用表") {
			t.Fatalf("应点名设备，实得 %q", got)
		}
		if !strings.Contains(got, "未连接") {
			t.Errorf("应说明该设备当前未连接（句柄未必仍被占用），实得 %q", got)
		}
	})

	t.Run("多台持有者时截断", func(t *testing.T) {
		many := []collector.SerialPortHolder{
			{DeviceName: "A", Port: "COM1", Connected: true},
			{DeviceName: "B", Port: "COM1", Connected: true},
			{DeviceName: "C", Port: "COM1", Connected: true},
			{DeviceName: "D", Port: "COM1", Connected: true},
		}
		got := serialBusyHint(`{"comPort":"COM1"}`, many)
		if !strings.Contains(got, "等 4 台设备") {
			t.Errorf("超过 3 台应折叠成「等 N 台设备」，实得 %q", got)
		}
	})

	t.Run("已连接的排在前面", func(t *testing.T) {
		// 已连接的才是"句柄现在确实被打开着"的那一个，应优先点名。
		mixed := []collector.SerialPortHolder{
			{DeviceName: "断链设备", Port: "COM1", Connected: false},
			{DeviceName: "在线设备", Port: "COM1", Connected: true},
		}
		got := serialBusyHint(`{"comPort":"COM1"}`, mixed)
		if strings.Index(got, "在线设备") > strings.Index(got, "断链设备") {
			t.Errorf("已连接的设备应排在前面，实得 %q", got)
		}
	})
}

// TestEndpointBusyHint 以太网目标提示的判据与措辞。
//
// 判据只有一条：**这个目标配给了谁**（串口那边同理，区别只在分档依据 —— 那边是
// "句柄是否打开"，这边是"链路是否通着"）。**不按 Connected 过滤**：设备没在采集
// 恰恰是最需要提示的时刻之一，初版按 Connected 过滤被真机打回过（见下面那条用例）。
//
// 背景（真机实测，10.62.100.22:10000，三菱内置以太网）：PLC 只服务一条 MC 连接，
// 第二条连接会在 TCP 层被收下、协议层永不应答，用户看到的是与真相毫无关系的
// "i/o timeout"；更进一步，那条唯一的连接槽被一条**已死**的连接占住之后，PLC 会
// 一律拒绝新连接，此时网关侧没有任何"正在采集"的设备。
func TestEndpointBusyHint(t *testing.T) {
	holders := []collector.EndpointHolder{
		{DeviceID: "d1", DeviceName: "1号PLC", Endpoint: "10.62.100.22:10000", Connected: true},
		{DeviceID: "d2", DeviceName: "断链PLC", Endpoint: "10.62.100.15:9600", Connected: false},
	}

	cases := []struct {
		name         string
		protocolJSON string
		wantEmpty    bool
		wantContains []string
	}{
		{
			name:         "同一目标且正在采集",
			protocolJSON: `{"host":"10.62.100.22","port":10000}`,
			wantContains: []string{"10.62.100.22:10000", "1号PLC", "正在采集", "再测试"},
		},
		{
			// 端口写成字符串（手填）与数字（表单默认）必须落在同一个目标上，
			// 否则这条提示会在两种写法下时有时无。
			name:         "端口写法不同也命中",
			protocolJSON: `{"host":"10.62.100.22","port":"10000"}`,
			wantContains: []string{"1号PLC"},
		},
		{
			name:         "无人在此目标",
			protocolJSON: `{"host":"10.62.100.99","port":10000}`,
			wantEmpty:    true,
		},
		{
			name:         "端口不同不算同一目标",
			protocolJSON: `{"host":"10.62.100.22","port":10001}`,
			wantEmpty:    true,
		},
		{
			name:         "请求里没有 host（串口协议）",
			protocolJSON: `{"comPort":"COM1"}`,
			wantEmpty:    true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := endpointBusyHint(c.protocolJSON, holders)
			if c.wantEmpty {
				if got != "" {
					t.Fatalf("不该给提示，实得 %q", got)
				}
				return
			}
			for _, want := range c.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("提示 %q 里应包含 %q", got, want)
				}
			}
		})
	}

	t.Run("未连接的持有者照样提示，但措辞留余地", func(t *testing.T) {
		// 这一档是补上的：初版按 Connected 过滤，结果在真机最需要提示的时刻
		// 一声不吭 —— 设备侧被一条已死的连接占住槽位时，网关侧没有"正在采集"
		// 的设备，而未连接的这台恰恰是那个地址唯一的历史持有者。
		got := endpointBusyHint(`{"host":"10.62.100.15","port":9600}`, holders)
		if got == "" {
			t.Fatal("未连接的持有者也应提示 —— 设备侧那条连接未必已释放")
		}
		if !strings.Contains(got, "断链PLC") {
			t.Errorf("应点名设备，实得 %q", got)
		}
		if strings.Contains(got, "正在采集") {
			t.Errorf("它并未连接，不能声称正在采集，实得 %q", got)
		}
		if !strings.Contains(got, "当前未连接") {
			t.Errorf("应说明该设备当前未连接，实得 %q", got)
		}
	})

	t.Run("已连接的排在前面", func(t *testing.T) {
		mixed := []collector.EndpointHolder{
			{DeviceName: "断链设备", Endpoint: "10.62.100.15:9600", Connected: false},
			{DeviceName: "在线设备", Endpoint: "10.62.100.15:9600", Connected: true},
		}
		got := endpointBusyHint(`{"host":"10.62.100.15","port":9600}`, mixed)
		if strings.Index(got, "在线设备") > strings.Index(got, "断链设备") {
			t.Errorf("已连接的设备应排在前面，实得 %q", got)
		}
		if !strings.Contains(got, "正在采集") {
			t.Errorf("有在线设备时应按「正在采集」那一档措辞，实得 %q", got)
		}
	})

	t.Run("多台设备时截断", func(t *testing.T) {
		many := []collector.EndpointHolder{
			{DeviceName: "A", Endpoint: "10.62.100.22:10000", Connected: true},
			{DeviceName: "B", Endpoint: "10.62.100.22:10000", Connected: true},
			{DeviceName: "C", Endpoint: "10.62.100.22:10000", Connected: true},
			{DeviceName: "D", Endpoint: "10.62.100.22:10000", Connected: true},
		}
		got := endpointBusyHint(`{"host":"10.62.100.22","port":10000}`, many)
		if !strings.Contains(got, "等 4 台设备") {
			t.Errorf("超过 3 台应折叠成「等 N 台设备」，实得 %q", got)
		}
	})
}

// TestBusyHint 说明块的组装：包装（换行 + 括号）只有这一处，三个入口
// （测试连接 / 点扫失败 / 点扫中断）追加出来的东西才长得一样。
func TestBusyHint(t *testing.T) {
	serial := []collector.SerialPortHolder{
		{DeviceName: "1号电表", Port: "COM1", Connected: true},
	}
	endpoints := []collector.EndpointHolder{
		{DeviceName: "1号PLC", Endpoint: "10.62.100.22:10000", Connected: true},
	}

	t.Run("都没命中时是空串", func(t *testing.T) {
		if got := busyHint(`{"host":"10.0.0.1","port":502}`, serial, endpoints); got != "" {
			t.Errorf("不该给提示，实得 %q", got)
		}
	})

	t.Run("命中即自带换行与括号", func(t *testing.T) {
		got := busyHint(`{"host":"10.62.100.22","port":10000}`, nil, endpoints)
		if !strings.HasPrefix(got, "\n（") || !strings.HasSuffix(got, "）") {
			t.Errorf("包装应由 busyHint 一次完成，实得 %q", got)
		}
		if !strings.Contains(got, "1号PLC") {
			t.Errorf("应点名正在采集的设备，实得 %q", got)
		}
	})

	t.Run("两类各说各的，不合并成一段", func(t *testing.T) {
		// 怪配置（comPort 与 host 同时存在）下两句话都要出现：
		// 串口说"这个口被谁占着"，以太网说"这个地址正被谁采着"。
		got := busyHint(`{"comPort":"COM1","host":"10.62.100.22","port":10000}`, serial, endpoints)
		if !strings.Contains(got, "1号电表") || !strings.Contains(got, "1号PLC") {
			t.Errorf("两句话应都在，实得 %q", got)
		}
		if strings.Count(got, "\n（") != 2 {
			t.Errorf("两句话应各自成块，实得 %q", got)
		}
	})
}

// TestBuildPingFailMessage 失败提示必须带上驱动原文。
//
// 这是本功能的要害：驱动原文（"COM1 已被…占用"这类）是唯一能指出问题的信息，
// 此前被丢掉只进日志，前端只看到一句「设备连接测试失败」。
//
// hint 由 busyHintFor 拼好后传入（判据见 TestBusyHint / TestSerialBusyHint /
// TestEndpointBusyHint），这里只管前缀与原文。
func TestBuildPingFailMessage(t *testing.T) {
	pingErr := errors.New("modbus rtu: ping COM1 failed (serial): Access is denied")

	t.Run("无提示时只有前缀与原文", func(t *testing.T) {
		got := buildPingFailMessage(context.Background(), pingErr, "")
		if !strings.Contains(got, "Access is denied") {
			t.Errorf("必须带上驱动原文，实得 %q", got)
		}
		if strings.Contains(got, "占用") {
			t.Errorf("没有占用者时不该提占用，实得 %q", got)
		}
	})

	t.Run("有提示时原样追加", func(t *testing.T) {
		hint := busyHint(`{"comPort":"COM1"}`, []collector.SerialPortHolder{
			{DeviceName: "1号电表", Port: "COM1", Connected: true},
		}, nil)
		got := buildPingFailMessage(context.Background(), pingErr, hint)
		if !strings.Contains(got, "Access is denied") {
			t.Errorf("必须带上驱动原文，实得 %q", got)
		}
		if !strings.Contains(got, "1号电表") {
			t.Errorf("应点名占用者，实得 %q", got)
		}
	})
}

// TestDescribeHolders 名称列表的折叠规则。
func TestDescribeHolders(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"A"}, "设备「A」"},
		{[]string{"A", "B"}, "设备「A」、设备「B」"},
		{[]string{"A", "B", "C"}, "设备「A」、设备「B」、设备「C」"},
		{[]string{"A", "B", "C", "D"}, "设备「A」、设备「B」、设备「C」等 4 台设备"},
	}
	for _, c := range cases {
		if got := describeHolders(c.in); got != c.want {
			t.Errorf("describeHolders(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTestDeviceConnection_NilCollector 采集引擎未注入时不得 panic。
//
// DeviceService 的构造器不保证 collector 非 nil（测试与部分装配路径就是直接
// 字面量构造的），而失败提示要经它去查"端口被谁占着"。少了守卫，
// 「测试连接」会在那条路径上直接空指针崩溃 —— 一个可选的诊断入口
// 不该把整个请求打成 500。
func TestTestDeviceConnection_NilCollector(t *testing.T) {
	svc := &DeviceService{} // collector == nil

	// 协议名为空：走前置校验分支。
	err := svc.TestDeviceConnection(context.Background(), &dto.TestDeviceConnectionDTO{})
	if err == nil {
		t.Error("协议名为空应报错")
	}

	// 协议名非空但未注册：走 PingDevice 失败分支，提示里要查持有者 —— 这里会碰到 nil collector。
	err = svc.TestDeviceConnection(context.Background(), &dto.TestDeviceConnectionDTO{
		ProtocolName: "No.Such.Protocol",
		ProtocolJSON: json.RawMessage(`{"comPort":"COM1"}`),
	})
	if err == nil {
		t.Fatal("未注册的协议应报错")
	}
	if !strings.Contains(err.Error(), "No.Such.Protocol") {
		t.Errorf("错误里应带驱动原文（未注册协议名），实得 %q", err.Error())
	}
}
