// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import "testing"

// TestExtractSerialPort 串口名的提取规则。
//
// 这个函数是"测试连接失败时该提示哪个口被占"的唯一依据：网关侧不认识任何
// 具体协议，只能靠约定好的 comPort 键取值。它取不到值的症状是占用提示
// **永远不出现**，且没有任何日志 —— 所以取值规则本身必须被钉住。
func TestExtractSerialPort(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"常规", `{"comPort":"COM3"}`, "COM3"},
		{"与其它字段共存", `{"comPort":"COM3","baudRate":"9600","unitId":1}`, "COM3"},
		// 键名大小写不敏感，与 encoding/json 把 JSON 键匹配到 `json:"comPort"`
		// 结构体字段时的行为对齐。否则会出现"驱动收得下 ComPort、占用提示却
		// 看不见它"这种自相矛盾的状态。
		{"键名大小写变体", `{"ComPort":"COM3"}`, "COM3"},
		{"键名全大写", `{"COMPORT":"COM3"}`, "COM3"},
		{"两端空白被裁掉", `{"comPort":"  COM3  "}`, "COM3"},
		{"空串", `{"comPort":""}`, ""},
		{"只有空白", `{"comPort":"   "}`, ""},
		{"没有这个键", `{"host":"192.168.1.10","port":502}`, ""},
		{"嵌套同名键不算", `{"serial":{"comPort":"COM3"}}`, ""},
		{"值不是字符串", `{"comPort":3}`, ""},
		{"值为 null", `{"comPort":null}`, ""},
		{"非法 JSON", `{"comPort":`, ""},
		{"空输入", ``, ""},
		{"非对象 JSON", `"COM3"`, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractSerialPort(c.in); got != c.want {
				t.Errorf("ExtractSerialPort(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSerialPortFieldPinned comPort 是跨协议约定，改一个字会让占用提示全线失效
// （没有日志、没有报错），所以把它钉在这里。
func TestSerialPortFieldPinned(t *testing.T) {
	if SerialPortField != "comPort" {
		t.Errorf("SerialPortField = %q，四个串口驱动的 raw 结构体用的都是 json:\"comPort\"",
			SerialPortField)
	}
}
