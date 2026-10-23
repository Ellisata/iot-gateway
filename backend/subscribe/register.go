// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

// 解析器的注册入口。订阅是当前唯一消费方，放在这里保证任何二进制路径
// （含测试）都能解析配置；将来新增 HTTP/CoAP 接入时再移到 main 统一注册。
import (
	_ "iot-gateway/parser/mapping"
	_ "iot-gateway/parser/script"
)
