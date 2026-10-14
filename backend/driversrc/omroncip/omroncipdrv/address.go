// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import "strings"

// ParseCIPAddress 规范化并校验 Omron 标签名。
//
// 标签寻址：点位 Name 即 Omron 标签名（可含结构体成员与数组下标语法，
// 如 "MotorSpeed"、"Recipe.Setpoint"、"Array[3]"），原样透传给 0x4C 请求，
// 不做进一步拆分（与 FINS 内存区地址解析不同）。
//
// 校验规则：
//   - TrimSpace 后非空；
//   - 仅含可打印 ASCII（字母数字、下划线、点、方括号、减号等）；
//   - 不含空白与控制字符；
//   - 长度 ≤ 1024（防呆上限，实际以设备为准）。
func ParseCIPAddress(name string) (string, bool) {
	s := strings.TrimSpace(name)
	if s == "" || len(s) > 1024 {
		return "", false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7E {
			// 0x21..0x7E 为可打印 ASCII，其余（空白、控制字符、非 ASCII）均拒绝
			return "", false
		}
	}
	return s, true
}
