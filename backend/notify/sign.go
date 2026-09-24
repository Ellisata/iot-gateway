// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// dingTalkSign 计算钉钉群机器人加签值（未做 URL 编码，拼查询串时由
// url.Values.Encode 完成转义）。
//
// 算法：sign = base64(HMAC-SHA256(key = secret, data = timestamp + "\n" + secret))，
// 时间戳单位为**毫秒**。
func dingTalkSign(secret string, timestampMillis int64) string {
	stringToSign := fmt.Sprintf("%d\n%s", timestampMillis, secret)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuSign 计算飞书群机器人加签值。
//
// 算法与钉钉**不同**，极易混淆：sign = base64(HMAC-SHA256(key = timestamp + "\n" + secret,
// data = ""))——整个 stringToSign 作为 HMAC 的 key，待签名数据为空串。
// 时间戳单位为**秒**，且 timestamp/sign 需放在请求 **body** 顶层（放 URL 查询串验签不通过）。
func feishuSign(secret string, timestampSeconds int64) string {
	stringToSign := fmt.Sprintf("%d\n%s", timestampSeconds, secret)
	mac := hmac.New(sha256.New, []byte(stringToSign))
	mac.Write(nil)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
