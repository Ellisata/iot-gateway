// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import "iot-gateway/driversdk"

// Debug 控制 Debug 级日志是否输出，默认关闭。
//
// 内置实现走网关的 logger，级别由 default.yaml 控制；插件这边没有等价物 ——
// 子进程环境变量是网关从零构造的（见 pluginhost.pluginEnv），也没有配置下发通道。
// 所以它是个**源码级开关**：现场要开调试日志，就把它改成 true 重新编译插件。
//
// 本包当前没有 Debug 级调用点，保留该开关是为了与其它插件同形（新增明细日志时
// 直接可用），也避免后来者误以为"插件不支持分级"而在热路径上直接 logf 刷屏。
var Debug bool

// logf 输出一行日志。
//
// 一律走 driversdk.Logf（stderr）：stdout 是插件与网关之间的帧通道，往里写任何
// 非帧数据都会被判定失步并杀掉进程。网关按行收进自己的日志（当前整条流统一记在
// INFO 级别，见 pluginhost 的 logStderrLines），因此这里不再自造 info/warn 分级 ——
// 两份函数体完全相同、只是名字不同的"分级"是自欺欺人，需要降噪的消息走 logDebug。
func logf(format string, args ...any) {
	driversdk.Logf("opcua: "+format, args...)
}

func logDebug(format string, args ...any) {
	if Debug {
		logf(format, args...)
	}
}
