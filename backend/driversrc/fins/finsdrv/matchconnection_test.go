// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import "testing"

// serialConfig 复刻 driver.Connect 的真实路径：先按 JSON 解析，再由协议注册名
// 覆盖 transport。测试里手工拼结构体会漏掉 ParseFINSConfig 填的那一堆默认值
// （DstUnit/SrcUnit/UnitNo/FCSMode…），那种"看起来更直接的"写法测的不是生产路径。
func serialConfig(t *testing.T, protocolJSON string) *FINSConfig {
	t.Helper()
	cfg, err := ParseFINSConfig(protocolJSON)
	if err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	cfg.Transport = TransportSerial
	return cfg
}

const finsSerialJSON = `{"comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`

// TestSameConfigCoversSerialParams 串口链路参数必须参与复用判定。
//
// 漏掉它们的症状是**假成功**：用户在表单里把波特率从 9600 改成 19200 再点
// 「测试连接」，驱动复用了那条按 9600 打开的活动连接并返回成功 —— 一个与被测
// 配置完全无关的绿色结论。这条测试就是那个缺陷的回归网。
func TestSameConfigCoversSerialParams(t *testing.T) {
	base := serialConfig(t, finsSerialJSON)

	if !sameConfig(base, serialConfig(t, finsSerialJSON)) {
		t.Error("完全相同的配置应判定为可复用")
	}
	// 端口名大小写不敏感：Windows 的 COM1 与 com1 是同一个端口，
	// 用 == 比会把本可复用的情况误判成不可复用，退回临时实例后撞 Access is denied。
	t.Run("端口大小写不敏感", func(t *testing.T) {
		lower := serialConfig(t, finsSerialJSON)
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
			if sameConfig(base, serialConfig(t, js)) {
				t.Errorf("%s 不同却判定为同一配置 —— 会返回假成功", name)
			}
		})
	}
}

// TestMatchConnectionSerialOnly 只有串口传输参与复用。
//
// UDP/TCP/HostLinkTCP 都不独占端口，复用收益为零，反而会让这次 ping 白排在一个
// 可能长达数十秒的批量读取后面。
func TestMatchConnectionSerialOnly(t *testing.T) {
	cases := []struct {
		name      string
		transport string
		want      bool
	}{
		{"串口", TransportSerial, true},
		{"UDP", TransportUDP, false},
		{"TCP", TransportTCP, false},
		{"HostLink TCP", TransportHostLinkTCP, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &finsDriver{transport: c.transport}
			d.config = serialConfig(t, finsSerialJSON)
			d.client = &fakeTransport{connected: true}

			if got := d.MatchConnection(finsSerialJSON); got != c.want {
				t.Errorf("MatchConnection = %v, want %v", got, c.want)
			}
		})
	}
}

// TestMatchConnectionIgnoresJSONTransport 表单里残留的 transport 不得影响判定。
//
// 传输层由协议注册名固定（Connect/Ping 里一律 cfg.Transport = d.transport），
// 而现场的表单/旧 protocol_json 里可能留着 transport=tcp。若把它纳入比较，
// 这些串口设备永远匹配不上可复用的会话，退回临时实例后撞 Access is denied。
func TestMatchConnectionIgnoresJSONTransport(t *testing.T) {
	d := &finsDriver{transport: TransportSerial}
	d.config = serialConfig(t, finsSerialJSON)
	d.client = &fakeTransport{connected: true}

	dirty := `{"transport":"tcp","comPort":"COM1","baudRate":"9600","dataBits":"8","stopBits":"1","parity":"N"}`
	if !d.MatchConnection(dirty) {
		t.Error("JSON 里的 transport 不该参与比较 —— 残留脏值的串口设备会永远匹配不上")
	}
}

// TestMatchConnectionRequiresLiveLink 链路已断开时不得复用。
func TestMatchConnectionRequiresLiveLink(t *testing.T) {
	d := &finsDriver{transport: TransportSerial}
	d.config = serialConfig(t, finsSerialJSON)
	d.client = &fakeTransport{connected: false}

	if d.MatchConnection(finsSerialJSON) {
		t.Error("连接已断开却允许复用 —— 会对着一个死连接收发")
	}
}

// TestMatchConnectionNoClient 从未连接过（懒加载尚未发生）时不得复用。
func TestMatchConnectionNoClient(t *testing.T) {
	d := &finsDriver{transport: TransportSerial}
	if d.MatchConnection(finsSerialJSON) {
		t.Error("没有任何连接却判定为可复用")
	}
}

// fakeTransport 只用于 MatchConnection 判定，不需要真的收发。
type fakeTransport struct {
	connected bool
}

func (f *fakeTransport) Close() error                                  { return nil }
func (f *fakeTransport) IsConnected() bool                             { return f.connected }
func (f *fakeTransport) Read(finsArea, uint16, uint16) ([]byte, error) { return nil, nil }
