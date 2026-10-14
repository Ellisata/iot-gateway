// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"testing"
)

func TestParseFINSConfigDefaults(t *testing.T) {
	cfg, err := ParseFINSConfig("")
	if err != nil {
		t.Fatalf("ParseFINSConfig(\"\") = %v", err)
	}
	if cfg.Transport != TransportUDP {
		t.Errorf("default transport = %s, want %s", cfg.Transport, TransportUDP)
	}
	if cfg.Port != defaultPort {
		t.Errorf("default port = %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.TimeoutMS != defaultTimeoutMS {
		t.Errorf("default timeoutMs = %d, want %d", cfg.TimeoutMS, defaultTimeoutMS)
	}
	if cfg.MaxReadWords != defaultMaxReadWords {
		t.Errorf("default maxReadWords = %d, want %d", cfg.MaxReadWords, defaultMaxReadWords)
	}
	if cfg.ByteOrder != ByteOrderBigEndian || cfg.WordOrder != WordOrderBigEndian {
		t.Errorf("default byte/word order = %s/%s, want BIG/BIG", cfg.ByteOrder, cfg.WordOrder)
	}
}

func TestParseFINSConfigValid(t *testing.T) {
	json := `{
		"transport": "TCP",
		"host": "192.168.1.10",
		"port": 9600,
		"dstNode": "10",
		"srcNode": "20",
		"dstUnit": "0",
		"srcUnit": "1",
		"comPort": "COM3",
		"baudRate": "19200",
		"dataBits": "8",
		"stopBits": "1",
		"parity": "E",
		"unitNo": "3",
		"timeoutMs": "3000",
		"mergeWindow": "50",
		"maxReadWords": "200",
		"stringLen": "32",
		"wordOrder": "LITTLE_ENDIAN"
	}`
	cfg, err := ParseFINSConfig(json)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	// JSON 里的 transport 是历史残留键，解析层必须**忽略**它：传输层由协议注册名
	// 固定在驱动侧（Connect/Ping 一律 cfg.Transport = d.transport 覆盖）。
	// 留在这个 JSON 里正是为了钉住「残留键既不报错、也不生效」——
	// 已有的 device.protocol_json 里普遍还带着这个键。
	if cfg.Transport != TransportUDP {
		t.Errorf("transport = %s, 解析不应被 protocol_json 的 transport 影响（应保持默认 %s）",
			cfg.Transport, TransportUDP)
	}
	if cfg.Host != "192.168.1.10" {
		t.Errorf("host = %s", cfg.Host)
	}
	if cfg.Port != 9600 || cfg.DstNode != 10 || cfg.SrcNode != 20 {
		t.Errorf("port/node = %d/%d/%d", cfg.Port, cfg.DstNode, cfg.SrcNode)
	}
	if cfg.DstUnit != 0 || cfg.SrcUnit != 1 {
		t.Errorf("units = %d/%d", cfg.DstUnit, cfg.SrcUnit)
	}
	if cfg.ComPort != "COM3" || cfg.BaudRate != 19200 || cfg.UnitNo != 3 {
		t.Errorf("serial = %s/%d/%d", cfg.ComPort, cfg.BaudRate, cfg.UnitNo)
	}
	if cfg.TimeoutMS != 3000 || cfg.MergeWindow != 50 || cfg.MaxReadWords != 200 || cfg.StringLen != 32 {
		t.Errorf("tuning = %d/%d/%d/%d", cfg.TimeoutMS, cfg.MergeWindow, cfg.MaxReadWords, cfg.StringLen)
	}
	if cfg.WordOrder != WordOrderLittleEndian {
		t.Errorf("wordOrder = %s", cfg.WordOrder)
	}
	if cfg.Timeout.Milliseconds() != 3000 {
		t.Errorf("timeout = %v", cfg.Timeout)
	}
}

func TestParseFINSConfigTolerance(t *testing.T) {
	// 非法传输方式回退 UDP、非法数值回退默认
	json := `{"transport": "BANANA", "port": -1, "dstNode": "999", "maxReadWords": "0"}`
	cfg, err := ParseFINSConfig(json)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	if cfg.Transport != TransportUDP {
		t.Errorf("transport = %s, want UDP (fallback)", cfg.Transport)
	}
	if cfg.Port != defaultPort {
		t.Errorf("port = %d, want default %d", cfg.Port, defaultPort)
	}
	if cfg.DstNode != 0 {
		t.Errorf("dstNode = %d, want 0", cfg.DstNode)
	}
	if cfg.MaxReadWords != defaultMaxReadWords {
		t.Errorf("maxReadWords = %d, want default %d", cfg.MaxReadWords, defaultMaxReadWords)
	}
}

func TestParseFINSConfigInvalidJSON(t *testing.T) {
	if _, err := ParseFINSConfig("{not-json"); err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

// TestParseFINSConfigAutoDstNode dstNode 自动推导的判据是「有没有 Host 可推导」，
// 而不是「是不是以太网传输」。
//
// 后者在解析阶段根本拿不到：传输层由协议注册名固定在驱动侧（Connect/Ping 一律
// cfg.Transport = d.transport 覆盖），解析时读到的永远是 DefaultFINSConfig 的默认值。
// 因此一个串口形态、却又带着 host 的 protocol_json 现在会推导出 dstNode —— 这没有害处：
// DA1 只有 UDP/TCP 会发到线上（Serial/HostLink 的客户端结构体里没有该字段），
// 且现场表单里串口协议本就没有 host 这一项。最后一条用注释钉住这个已知边界。
func TestParseFINSConfigAutoDstNode(t *testing.T) {
	// 未配置 dstNode 时自动取 Host IP 末段
	cfg, err := ParseFINSConfig(`{"host": "192.168.1.10"}`)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	if cfg.DstNode != 10 {
		t.Errorf("dstNode = %d, want 10 (auto from host)", cfg.DstNode)
	}

	// 残留的 transport 键不参与这个判据
	cfg, err = ParseFINSConfig(`{"transport": "TCP", "host": "192.168.1.50"}`)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	if cfg.DstNode != 50 {
		t.Errorf("dstNode = %d, want 50 (残留 transport 键不影响推导)", cfg.DstNode)
	}

	// 显式 dstNode 优先，覆盖自动推导
	cfg, err = ParseFINSConfig(`{"host": "192.168.1.10", "dstNode": "30"}`)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	if cfg.DstNode != 30 {
		t.Errorf("explicit dstNode = %d, want 30", cfg.DstNode)
	}

	// Host 为域名时无法推导，保持默认 0
	cfg, err = ParseFINSConfig(`{"host": "plc.example.com"}`)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	if cfg.DstNode != 0 {
		t.Errorf("hostname dstNode = %d, want 0", cfg.DstNode)
	}

	// 没有 Host（串口/HostLink 的常态）→ 不推导，保持默认 0
	cfg, err = ParseFINSConfig(`{"comPort": "COM3"}`)
	if err != nil {
		t.Fatalf("ParseFINSConfig = %v", err)
	}
	if cfg.DstNode != 0 {
		t.Errorf("no-host dstNode = %d, want 0", cfg.DstNode)
	}
}
