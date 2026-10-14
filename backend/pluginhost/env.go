// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import "os"

// pluginEnv 构造插件子进程的最小环境变量集合。
//
// 刻意不用 os.Environ()：网关进程里带着 IOT_GATEWAY_JWT_SECRET 等密钥类变量，
// 插件与网关同 UID 运行、并非权限边界，但白送签名密钥没有道理。
//
// 各平台必须保留的系统变量由 platformEnvNames 提供（Windows 靠 SystemRoot
// 定位系统 DLL 与 socket 库；Unix 用 HOME/TMPDIR 让子进程能正常写临时文件）。
func pluginEnv() []string {
	keep := make([]string, 0, len(platformEnvNames)+6)
	keep = append(keep, platformEnvNames...)
	keep = append(keep, "PATH", "TZ", "TEMP", "TMP")

	env := make([]string, 0, len(keep)+1)
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "IOT_GATEWAY_PLUGIN=1")
}
