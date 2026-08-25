package utils

import (
	"crypto/md5"
	"fmt"
)

// MD5 计算 MD5 哈希
func MD5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}
