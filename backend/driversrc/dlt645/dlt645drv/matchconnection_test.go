// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"testing"
	"time"
)

// dltSerialConfig 走 driver.Connect 的真实路径：ParseDLT645Config 已带上传输层。
func dltSerialConfig(t *testing.T, protocolJSON string) *DLT645Config {
	t.Helper()
	cfg, err := ParseDLT645Config(protocolJSON, TransportSerial)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	return cfg
}

const dltSerialJSON = `{"protocolVersion":"2007","meterAddress":"000000000001",` +
	`"comPort":"COM1","baudRate":"2400","dataBits":"8","stopBits":"1","parity":"E"}`

// matchFakeTransport 只用于 MatchConnection 判定，不需要真的收发。
// 与 codec_test.go 里的 fakeTransport 区分开：那个的 IsConnected 恒为 true，
// 测不出"链路已断开不得复用"这一条。
type matchFakeTransport struct {
	connected bool
}

func (f *matchFakeTransport) Lock()                           {}
func (f *matchFakeTransport) Unlock()                         {}
func (f *matchFakeTransport) Write(p []byte) (int, error)     { return len(p), nil }
func (f *matchFakeTransport) Read(p []byte) (int, error)      { return 0, nil }
func (f *matchFakeTransport) SetReadDeadline(time.Time) error { return nil }
func (f *matchFakeTransport) Drain()                          {}
func (f *matchFakeTransport) MarkDirty()                      {}
func (f *matchFakeTransport) IsConnected() bool               { return f.connected }
func (f *matchFakeTransport) Close() error                    { return nil }

// TestDLTSameConfigCoversSerialParams 串口链路参数必须参与复用判定，
// 否则改了波特率再点「测试连接」会复用到一条与配置无关的活连接并报成功。
func TestDLTSameConfigCoversSerialParams(t *testing.T) {
	base := dltSerialConfig(t, dltSerialJSON)

	if !sameConfig(base, dltSerialConfig(t, dltSerialJSON)) {
		t.Error("完全相同的配置应判定为可复用")
	}
	t.Run("端口大小写不敏感", func(t *testing.T) {
		lower := dltSerialConfig(t, dltSerialJSON)
		lower.ComPort = "com1"
		if !sameConfig(base, lower) {
			t.Error("COM1 与 com1 是同一个端口，应判定为可复用")
		}
	})

	cases := map[string]string{
		"端口": `{"protocolVersion":"2007","meterAddress":"000000000001",` +
			`"comPort":"COM2","baudRate":"2400","dataBits":"8","stopBits":"1","parity":"E"}`,
		"波特率": `{"protocolVersion":"2007","meterAddress":"000000000001",` +
			`"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"E"}`,
		"校验位": `{"protocolVersion":"2007","meterAddress":"000000000001",` +
			`"comPort":"COM1","baudRate":"2400","dataBits":"8","stopBits":"1","parity":"N"}`,
		// 表号必须参与比较：连接建立时地址域已固定，表号不同说明测的是另一块表，
		// 复用该连接会把请求发到错误的表上，而那块表正常应答 → 报出假成功。
		"表号": `{"protocolVersion":"2007","meterAddress":"000000000002",` +
			`"comPort":"COM1","baudRate":"2400","dataBits":"8","stopBits":"1","parity":"E"}`,
		"协议版本": `{"protocolVersion":"1997","meterAddress":"000000000001",` +
			`"comPort":"COM1","baudRate":"2400","dataBits":"8","stopBits":"1","parity":"E"}`,
	}
	for name, js := range cases {
		t.Run(name, func(t *testing.T) {
			if sameConfig(base, dltSerialConfig(t, js)) {
				t.Errorf("%s 不同却判定为同一配置", name)
			}
		})
	}
}

// TestDLTMatchConnectionSerialOnly 只有串口传输参与复用。
//
// TCP 的半死连接 connected 仍可能为 true，复用它会把本可成功的重连测试误判为
// 失败；而新开一个 TCP 连接的代价为零。
func TestDLTMatchConnectionSerialOnly(t *testing.T) {
	for _, c := range []struct {
		transport string
		want      bool
	}{
		{TransportSerial, true},
		{TransportTCP, false},
	} {
		d := &dlt645Driver{transport: c.transport}
		d.config = dltSerialConfig(t, dltSerialJSON)
		d.client = &matchFakeTransport{connected: true}

		if got := d.MatchConnection(dltSerialJSON); got != c.want {
			t.Errorf("transport=%s: MatchConnection = %v, want %v", c.transport, got, c.want)
		}
	}
}

// TestDLTMatchConnectionRequiresLiveLink 链路已断开 / 从未连接时不得复用。
func TestDLTMatchConnectionRequiresLiveLink(t *testing.T) {
	d := &dlt645Driver{transport: TransportSerial}
	d.config = dltSerialConfig(t, dltSerialJSON)
	d.client = &matchFakeTransport{connected: false}
	if d.MatchConnection(dltSerialJSON) {
		t.Error("连接已断开却允许复用 —— 会对着一个死连接收发")
	}

	d2 := &dlt645Driver{transport: TransportSerial}
	if d2.MatchConnection(dltSerialJSON) {
		t.Error("没有任何连接却判定为可复用")
	}
}

// TestDLTMatchConnectionBadJSON 非法 JSON 不复用（交给 Ping 去报真正的解析错误）。
func TestDLTMatchConnectionBadJSON(t *testing.T) {
	d := &dlt645Driver{transport: TransportSerial}
	d.config = dltSerialConfig(t, dltSerialJSON)
	d.client = &matchFakeTransport{connected: true}

	if d.MatchConnection(`{`) {
		t.Error("JSON 非法时不该复用")
	}
}
