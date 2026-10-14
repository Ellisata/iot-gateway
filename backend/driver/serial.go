// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver

import (
	"encoding/json"
	"strings"
)

// SerialPortField 串口名在 protocol_json 中的字段名。**跨协议约定**：
// 所有串口类协议（SerialExclusive 为 true 的那些）都必须使用这个键。
//
// 为什么它是网关侧的契约而不是各插件内部的事：串口是独占资源，"测试连接"
// 失败时网关要能回答"这个口当前被哪台设备的采集任务占着"，而网关口拿到的
// 只有 protocol_json —— 它不认识任何具体协议，只能靠这个约定好的键取值。
//
// 违反约定的症状很隐蔽：占用提示**永远不出现**，且没有任何日志或报错，
// 因为 ExtractSerialPort 取不到值只会返回空串。故该约定由
// docs/driver-plugins.md §3.6 与各插件的表单契约测试共同钉住。
const SerialPortField = "comPort"

// SerialOpenFailureHint 串口打开失败时的统一提示，由各串口驱动在**打开失败的分支里**
// 拼到错误后面。
//
// 为什么这句话必须由驱动来说：网关口分不清"打不开端口"和"打开了但没应答" ——
// 两者在它眼里都是一条 RPC 错误。而这句话只对前者成立，挂到后者上会把现场引向
// "去关别的程序"，实际问题却在设备或链路参数上。驱动正站在打开失败的分支里，
// 它知道得最准。
//
// 另一个原因是可读性：底层抛上来的是 `Access is denied.`（在中文 Windows 上
// 还是"拒绝访问。"），既不说明是什么操作失败，也不给任何可照做的动作。
//
// 网关侧还会在此基础上补出**具体是哪个设备的采集任务占着**（见 service 的占用
// 提示）；两句都是真的，叠在一起也读得通，只是那种情况本就少见 ——
// 网关自己的设备占着端口时，"测试连接"正常应当走复用成功。
const SerialOpenFailureHint = "（串口打开失败：请核对端口名是否正确，或该端口已被其它程序占用）"

// ExtractSerialPort 从 protocol_json 中取出串口名。
//
// 不存在、不是字符串、或去掉空白后为空 → 返回 ""。
//
// 键名比较用 EqualFold，与 encoding/json 把 JSON 键匹配到 `json:"comPort"`
// 结构体字段时的大小写不敏感行为对齐 —— 否则会出现「驱动收得下 ComPort、
// 占用提示却看不见它」这种自相矛盾的状态。
//
// "我在测哪个口"与"谁占着这个口"两条路径必须调用同一个函数，提示才自洽。
func ExtractSerialPort(protocolJSON string) string {
	if protocolJSON == "" {
		return ""
	}
	// 用 RawMessage 而不是 map[string]any：后者会把数字、布尔也解出来，
	// 而这里只认字符串，多一步类型判断没有意义。
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(protocolJSON), &fields); err != nil {
		return ""
	}
	for k, raw := range fields {
		if !strings.EqualFold(k, SerialPortField) {
			continue
		}
		var port string
		if err := json.Unmarshal(raw, &port); err != nil {
			return ""
		}
		return strings.TrimSpace(port)
	}
	return ""
}
