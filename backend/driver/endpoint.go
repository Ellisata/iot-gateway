// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"encoding/json"
	"strconv"
	"strings"
)

// 以太网目标在 protocol_json 中的字段名。**跨协议约定**（与 SerialPortField 并列）：
// 走 TCP 的协议一律沿用 `host` / `port` 这两个键，网关侧才能在不认识任何具体协议
// 的前提下回答"这个地址当前是不是正被某台设备的采集任务连着"。
//
// 与串口那条约定的**后果不同**：串口是独占资源，取不到端口名等于丢掉了"谁占着
// 这个口"的唯一线索；TCP 目标不是独占资源，取不到它只是少一句提示 —— 同样静默，
// 但不会误导。故这里是**沿用**（当前全部以太网插件都已如此）而不是新增硬性契约，
// 也没有表单契约测试钉它。
//
// 取值失败的表现一律是"提示不出现"，没有任何日志或报错：ExtractEndpoint 取不到
// 只会返回空串。见 docs/driver-plugins.md §3.7。
const (
	HostField = "host"
	PortField = "port"
)

// ExtractEndpoint 从 protocol_json 中取出以太网目标（"host:port" 形式）。
//
// 没有 host（串口协议、非对象 JSON、JSON 非法）→ 返回 ""。
// host 不是字符串 → 返回 ""：整条配置都不足以判定目标是谁。
// port 缺失 / 非法 / 越界 → 只返回 host，而不是拿协议的默认端口去补 ——
// 补出来的值与另一个 JSON 的显式端口不等，提示只是不出现（少说一句），
// 而按默认值"猜"则可能把两台不同端口的设备说成同一个目标。
// port 可以是 JSON 数字（表单默认如此）也可以是字符串（手填），两种都认。
//
// 键名比较用 EqualFold，与 encoding/json 把 JSON 键匹配到 `json:"host"` 结构体
// 字段时的大小写不敏感行为对齐（同 ExtractSerialPort）：否则会出现"驱动收得下
// Host、提示却看不见它"这种自相矛盾的状态。
//
// "我在测哪个地址"与"谁在用这个地址"两条路径必须调用同一个函数，提示才自洽。
func ExtractEndpoint(protocolJSON string) string {
	if protocolJSON == "" {
		return ""
	}
	// 用 RawMessage 而不是 map[string]any：host 只认字符串、port 认数字或字符串，
	// 一次解析出中间类型再判断比先解成 any 更少绕。
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(protocolJSON), &fields); err != nil {
		return ""
	}

	var host string
	for k, raw := range fields {
		if !strings.EqualFold(k, HostField) {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
		host = strings.TrimSpace(s)
		break
	}
	if host == "" {
		return ""
	}

	if port := extractPort(fields); port != "" {
		return host + ":" + port
	}
	return host
}

// extractPort 取规范化后的端口号；缺失 / 非法 / 非正 / 超界一律返回 ""。
//
// 规范化（数字与字符串都过一遍 strconv）是为了让 `"10000"`、`10000` 与
// `"010000"` 落到同一个字面量上：这条链路的比较方是占位提示，它对"同一个端口"
// 的判断必须与驱动解析端口时的判断一致。
func extractPort(fields map[string]json.RawMessage) string {
	for k, raw := range fields {
		if !strings.EqualFold(k, PortField) {
			continue
		}
		var num int64
		var str string
		switch {
		case json.Unmarshal(raw, &str) == nil:
			v, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
			if err != nil {
				return ""
			}
			num = v
		case json.Unmarshal(raw, &num) == nil:
			// JSON 数字：只接受整数写法（10000）。小数端口没有意义，
			// 而 encoding/json 解不出来（10000.0 在这里就会失败），按"没填"处理。
		default:
			return ""
		}
		if num <= 0 || num > 65535 {
			return ""
		}
		return strconv.FormatInt(num, 10)
	}
	return ""
}
