// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import "testing"

// TestExtractEndpoint 以太网目标的提取规则。
//
// 与 TestExtractSerialPort 同一条理由：网关侧不认识任何具体协议，只能靠约定好的
// host / port 键取值，用来判断"这个地址是不是正被某台设备的采集任务连着"。
// 取不到值的症状同样是静默的（提示永远不出现，没有日志），故取值规则必须被钉住。
//
// 与串口那条的差别只有一处：**这里宁可少说一句，也不猜**。port 缺失时只返回
// host（而不是补该协议的默认端口），因为补出来的值与另一个 JSON 的显式端口不等，
// 提示只是不出现；而按默认值猜可能把两台不同端口的设备说成同一个目标。
func TestExtractEndpoint(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"常规（端口是数字）", `{"host":"10.62.100.22","port":10000}`, "10.62.100.22:10000"},
		{"端口是字符串", `{"host":"10.62.100.22","port":"10000"}`, "10.62.100.22:10000"},
		{"端口带前导零", `{"host":"10.62.100.22","port":"010000"}`, "10.62.100.22:10000"},
		{"与其它字段共存", `{"host":"10.62.100.12","port":102,"rack":"0","slot":"1"}`, "10.62.100.12:102"},
		// 键名大小写不敏感，与 encoding/json 把 JSON 键匹配到 `json:"host"`
		// 结构体字段时的行为对齐（同 ExtractSerialPort）。
		{"键名大小写变体", `{"Host":"10.62.100.22","Port":10000}`, "10.62.100.22:10000"},
		{"两端空白被裁掉", `{"host":" 10.62.100.22 ","port":10000}`, "10.62.100.22:10000"},
		{"主机名不归一大小写（比较方负责）", `{"host":"PLC-01","port":5007}`, "PLC-01:5007"},

		// 以下都是"宁可少说一句"：返回空串或只返回 host。
		{"没有端口只认 host", `{"host":"10.62.100.22"}`, "10.62.100.22"},
		{"端口是 0", `{"host":"10.62.100.22","port":0}`, "10.62.100.22"},
		{"端口超界", `{"host":"10.62.100.22","port":70000}`, "10.62.100.22"},
		{"端口是小数", `{"host":"10.62.100.22","port":10000.5}`, "10.62.100.22"},
		{"端口不是数字", `{"host":"10.62.100.22","port":"abc"}`, "10.62.100.22"},
		{"端口是 null", `{"host":"10.62.100.22","port":null}`, "10.62.100.22"},

		{"没有 host（串口协议）", `{"comPort":"COM3","baudRate":"9600"}`, ""},
		{"host 是空串", `{"host":"","port":10000}`, ""},
		{"host 只有空白", `{"host":"   ","port":10000}`, ""},
		{"host 不是字符串", `{"host":10000,"port":10000}`, ""},
		{"host 是 null", `{"host":null,"port":10000}`, ""},
		{"嵌套同名键不算", `{"device":{"host":"10.62.100.22","port":10000}}`, ""},
		{"非法 JSON", `{"host":`, ""},
		{"空输入", ``, ""},
		{"非对象 JSON", `"10.62.100.22"`, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractEndpoint(c.in); got != c.want {
				t.Errorf("ExtractEndpoint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestEndpointFieldsPinned host / port 是跨协议约定（同 SerialPortField）：
// 改一个字，以太网设备侧的提示就全线失效（同样没有日志、没有报错）。
func TestEndpointFieldsPinned(t *testing.T) {
	if HostField != "host" || PortField != "port" {
		t.Errorf("HostField = %q, PortField = %q，各以太网插件的 raw 结构体用的都是 json:\"host\" / json:\"port\"",
			HostField, PortField)
	}
}
