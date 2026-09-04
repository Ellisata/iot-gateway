// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import "strings"

// ParseRockwellAddress 规范化并校验 Logix 标签名。
//
// 标签寻址：点位 Name 即 Logix 标签名（可含结构体成员、数组下标、程序作用域与
// 位访问语法，如 "MotorSpeed"、"Recipe.Setpoint"、"Array[3]"、"Matrix[2,3]"、
// "Program:MainProgram.MyTag"、"MyDINT.5"），原样透传给 0x4C Read Tag 请求。
// 完整语法合法性由 goindustrial cip.ParseTagPath 在读时校验，失败以单标签
// 通用状态错误处理（该标签点位 Quality=0，不影响其它标签）。
//
// 校验规则（与 ParseCIPAddress 一致）：
//   - TrimSpace 后非空；
//   - 仅含可打印 ASCII（字母数字、下划线、点、方括号、冒号、逗号、减号等）；
//   - 不含空白与控制字符；
//   - 长度 ≤ 1024（防呆上限，实际以设备为准）。
func ParseRockwellAddress(name string) (string, bool) {
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
