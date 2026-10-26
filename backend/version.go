// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

// version 由构建脚本以 -X main.version=<tag> 注入（见 build.sh / build.bat /
// package.bat / release.sh / release.bat / Dockerfile）。未注入时为 "dev"，
// 此时网关身份版本回退到 default.yaml 的 gateway.version —— 优先级链见
// configFile.ResolveGatewayVersion。
//
// 它必须留在 package main 且是可写 string 变量：-X 只对这类符号生效。
var version = "dev"
