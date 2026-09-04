// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"crypto/md5"
	"fmt"
)

// MD5 计算 MD5 哈希
func MD5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
