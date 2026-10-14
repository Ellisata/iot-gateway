// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import "testing"

// mcSerialConfig 复刻 driver.Connect 的真实路径：先按 JSON 解析，再由协议
// 注册名覆盖 transport。手工拼结构体会漏掉 ParseMCConfig 填的默认值。
func mcSerialConfig(t *testing.T, protocolJSON string) *MCConfig {
	t.Helper()
	cfg, err := ParseMCConfig(protocolJSON)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	cfg.Transport = TransportSerial
	return cfg
}

const mcSerialJSON = `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`

// fakeTransport 只用于 MatchConnection 判定，不需要真的收发。
type fakeTransport struct {
	connected bool
}

func (f *fakeTransport) Close() error      { return nil }
func (f *fakeTransport) IsConnected() bool { return f.connected }
func (f *fakeTransport) Read(mcDevice, uint32, uint16, bool) ([]byte, error) {
	return nil, nil
}

// TestMCSameConfigCoversSerialParams 串口链路参数必须参与复用判定，
// 否则改了波特率再点「测试连接」会复用到一条与配置无关的活连接并报成功。
func TestMCSameConfigCoversSerialParams(t *testing.T) {
	base := mcSerialConfig(t, mcSerialJSON)

	if !sameConfig(base, mcSerialConfig(t, mcSerialJSON)) {
		t.Error("完全相同的配置应判定为可复用")
	}
	t.Run("端口大小写不敏感", func(t *testing.T) {
		lower := mcSerialConfig(t, mcSerialJSON)
		lower.ComPort = "com1"
		if !sameConfig(base, lower) {
			t.Error("COM1 与 com1 是同一个端口，应判定为可复用")
		}
	})

	cases := map[string]string{
		"端口":  `{"comPort":"COM2","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`,
		"波特率": `{"comPort":"COM1","baudRate":"19200","dataBits":"8","stopBits":"1","parity":"N"}`,
		"数据位": `{"comPort":"COM1","baudRate":"9600","dataBits":"7","stopBits":"1","parity":"N"}`,
		"停止位": `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"2","parity":"N"}`,
		"校验位": `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"E"}`,
	}
	for name, js := range cases {
		t.Run(name, func(t *testing.T) {
			if sameConfig(base, mcSerialConfig(t, js)) {
				t.Errorf("%s 不同却判定为同一配置 —— 会返回假成功", name)
			}
		})
	}
}

// TestMCMatchConnectionSerialOnly 只有串口传输参与复用（TCP 不独占端口，
// 复用只会让这次 ping 白排在一个长批量读取后面）。
func TestMCMatchConnectionSerialOnly(t *testing.T) {
	for _, c := range []struct {
		transport string
		want      bool
	}{
		{TransportSerial, true},
		{TransportTCP, false},
	} {
		d := &mcDriver{transport: c.transport}
		d.config = mcSerialConfig(t, mcSerialJSON)
		d.client = &fakeTransport{connected: true}

		if got := d.MatchConnection(mcSerialJSON); got != c.want {
			t.Errorf("transport=%s: MatchConnection = %v, want %v", c.transport, got, c.want)
		}
	}
}

// TestMCMatchConnectionIgnoresJSONTransport 表单里残留的 transport 不得影响判定。
//
// 本插件的表单里确实留着一个 transport 字段（内置时代的历史包袱，值还是反的），
// 驱动一律用协议注册名覆盖它。若把它纳入比较，串口设备永远匹配不上可复用的
// 会话，退回临时实例后撞 Access is denied。
func TestMCMatchConnectionIgnoresJSONTransport(t *testing.T) {
	d := &mcDriver{transport: TransportSerial}
	d.config = mcSerialConfig(t, mcSerialJSON)
	d.client = &fakeTransport{connected: true}

	dirty := `{"transport":"tcp","comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`
	if !d.MatchConnection(dirty) {
		t.Error("JSON 里的 transport 不该参与比较 —— 残留脏值的串口设备会永远匹配不上")
	}
}

// TestMCMatchConnectionRequiresLiveLink 链路已断开 / 从未连接时不得复用。
func TestMCMatchConnectionRequiresLiveLink(t *testing.T) {
	d := &mcDriver{transport: TransportSerial}
	d.config = mcSerialConfig(t, mcSerialJSON)
	d.client = &fakeTransport{connected: false}
	if d.MatchConnection(mcSerialJSON) {
		t.Error("连接已断开却允许复用 —— 会对着一个死连接收发")
	}

	d2 := &mcDriver{transport: TransportSerial}
	if d2.MatchConnection(mcSerialJSON) {
		t.Error("没有任何连接却判定为可复用")
	}
}
