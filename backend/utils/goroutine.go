package utils

import (
	"runtime"
	"strconv"
)

// GoID 获取当前 goroutine 的短 ID（解析自 runtime.Stack）
// 类似 Java 的 Thread.currentThread().getId()，但 Go 不暴露此 API，
// 此处通过解析栈前缀 "goroutine N" 来获取，性能尚可，适合日志场景。
func GoID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// buf[:n] 格式: "goroutine 42 [running]:..."
	// 跳过 "goroutine " 前缀（10 字节）后解析数字
	prefixLen := 10
	idStr := ""
	for i := prefixLen; i < n; i++ {
		if buf[i] < '0' || buf[i] > '9' {
			break
		}
		idStr += string(buf[i])
	}
	if idStr == "" {
		return 0
	}
	id, _ := strconv.ParseInt(idStr, 10, 64)
	return id
}
