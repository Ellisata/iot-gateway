// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import "iot-gateway/driversdk"

// Debug 控制 Debug 级日志是否输出，默认关闭。
//
// 内置实现走网关的 logger，级别由 default.yaml 控制；插件这边没有等价物 ——
// 子进程环境变量是网关从零构造的（见 pluginhost.pluginEnv），也没有配置下发通道。
// 所以它是个**源码级开关**：现场要开调试日志，就把它改成 true 重新编译插件。
//
// 默认关闭对本驱动是硬要求：帧收发路径上每帧两行（发送 / 收到），
// 而 645 默认一个数据标识一个请求（maxDIsPerRead=1），点位多时一帧一个点位，
// 按 1s 扫描周期打开即刷屏 —— 网关会把插件的 stderr 逐行记进自己的日志。
var Debug bool

// logf 输出一行日志。
//
// 一律走 driversdk.Logf（stderr）：stdout 是插件与网关之间的帧通道，往里写任何
// 非帧数据都会被判定失步并杀掉进程。网关按行收进自己的日志（当前整条流统一记在
// INFO 级别，见 pluginhost 的 logStderrLines），因此这里不再自造 info/warn 分级 ——
// 两份函数体完全相同、只是名字不同的"分级"是自欺欺人，需要降噪的消息走 logDebug。
//
// 内置版此处是 logger.Warn / logger.Info，搬迁时一律降为 logf：它们承载的是
// 「现场需要看见」的信息（连接建立、告警、字典自检），不能因为插件没有分级就丢掉。
func logf(format string, args ...any) {
	driversdk.Logf("dlt645: "+format, args...)
}

// logDebug 输出一行调试日志（受 Debug 开关控制）。
//
// 内置版对应 logger.Debug，大多是逐帧报文转储 —— 也是本驱动唯一会刷屏的日志源。
func logDebug(format string, args ...any) {
	if Debug {
		logf(format, args...)
	}
}
