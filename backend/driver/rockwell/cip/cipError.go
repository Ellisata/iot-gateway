// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"fmt"

	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"
)

// cipGeneralStatus 常用 CIP 通用状态码的可读描述（现场日志诊断用）。
var cipGeneralStatus = map[uint8]string{
	0x01: "connection failure (连接失败)",
	0x02: "resource unavailable (资源不足)",
	0x03: "invalid parameter value (参数值非法)",
	0x04: "path segment error (路径段错误)",
	0x05: "path destination unknown (标签路径不存在，常见于标签名/Program 作用域错误或设备不支持该作用域)",
	0x06: "partial transfer (部分传输)",
	0x08: "service not supported (服务不支持)",
	0x09: "invalid attribute value (属性值非法)",
	0x0C: "object state conflict (对象状态冲突)",
	0x14: "attribute not supported (属性不支持)",
	0x15: "too much data (数据过多)",
	0x16: "object does not exist (对象不存在)",
}

// logixExtStatus Logix 常见扩展状态字（首字）的可读描述。
var logixExtStatus = map[cip.UINT]string{
	0x2105: "path segment error",
	0x2106: "path destination unknown (标签不存在或 Program 名错误)",
	0x2107: "path segment error: port (端口段错误)",
	0x2108: "path segment error: link address (链路地址错误)",
}

// describeCIPStatus 返回 CIP 通用状态码的可读描述，未知码返回原始十六进制。
func describeCIPStatus(status cip.USINT) string {
	if s, ok := cipGeneralStatus[uint8(status)]; ok {
		return s
	}
	return fmt.Sprintf("unknown status 0x%02X", uint8(status))
}

// describeCIPError 从错误链提取 cip.Error 并附加状态码可读描述，
// 便于现场日志直接区分"标签不存在"与"路径非法"等原因；非 CIP 状态错误原样返回。
func describeCIPError(err error) string {
	if err == nil {
		return ""
	}
	var cipErr cip.Error
	if !asCIPError(err, &cipErr) {
		return err.Error()
	}
	if len(cipErr.ExtStatus) == 0 {
		return fmt.Sprintf("%v (%s)", err, describeCIPStatus(cipErr.Status))
	}
	if desc, ok := logixExtStatus[cipErr.ExtStatus[0]]; ok {
		return fmt.Sprintf("%v (%s; ext=%04X %s)", err, describeCIPStatus(cipErr.Status), cipErr.ExtStatus[0], desc)
	}
	return fmt.Sprintf("%v (%s; ext=%04X)", err, describeCIPStatus(cipErr.Status), cipErr.ExtStatus[0])
}
