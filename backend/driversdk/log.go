// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driversdk

import (
	"fmt"
	"os"
)

// Logf 把一行日志写到 stderr，由网关捕获后按行转成自己的日志（带插件名前缀）。
//
// 插件作者**必须**用它（或任何 stderr 输出）代替 fmt.Println：
// stdout 是帧通道，直接写入会破坏帧流，网关会判定失步并杀掉进程。
// Serve 已把 os.Stdout 重定向到 stderr 作为兜底，但显式用 Logf 意图更清晰。
func Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
