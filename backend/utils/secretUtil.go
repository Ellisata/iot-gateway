// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateSecretKey 生成 32 字节随机密钥并 hex 编码为 64 位字符串。
//
// 开放接口密钥的两条产出路径共用这一份实现：手工新建（OpenApiSecretService）与
// 启动播种默认密钥（database/sqlite 的 seedDefaultOpenApiSecret）。两边都要求
// "不可预测"，各写一份迟早会漂移成参数不同的两个版本 —— 而那种漂移不会有任何
// 测试发现，只会表现为"某一条密钥比另一条短"。
func GenerateSecretKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
