// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import "iot-gateway/driversdk"

// Debug 控制 Debug 级日志是否输出，默认关闭。
//
// 内置实现走网关的 logger，级别由 default.yaml 控制；插件这边没有等价物 ——
// 子进程环境变量是网关从零构造的（见 pluginhost.pluginEnv），也没有配置下发通道。
// 所以它是个**源码级开关**：现场要开调试日志，就把它改成 true 重新编译插件。
//
// 默认关闭是必要的：range.go 的区间规划会对每个读到标签打一行明细，而 Read
// 每轮轮询都跑，网关又会把插件的 stderr 逐行记进自己的日志，打开即刷屏。更糟的是
// 插件 stderr 是同步管道，量大时反压插件、拖慢采集 —— 与内置时代"日志级别可在运行期
// 调高"不同，这里的明细默认必须闭嘴。
var Debug bool

// logf 输出一行日志。
//
// 一律走 driversdk.Logf（stderr）：stdout 是插件与网关之间的帧通道，往里写任何
// 非帧数据都会被判定失步并杀掉进程。网关按行收进自己的日志（当前整条流统一记在
// INFO 级别，见 pluginhost 的 logStderrLines），因此这里不再自造 info/warn 分级 ——
// 两份函数体完全相同、只是名字不同的"分级"是自欺欺人，需要降噪的消息走 logDebug。
func logf(format string, args ...any) {
	driversdk.Logf("rockwell: "+format, args...)
}

// logDebug 输出一行 Debug 级日志（默认不输出，见 Debug 说明）。
//
// 原先的 logger.Debug 调用点（range.go 的区间规划明细）就在采集热路径上，
// 必须保留门控：插件 stderr 是同步管道，无门控的逐区间明细会刷屏并反压采集。
func logDebug(format string, args ...any) {
	if Debug {
		logf(format, args...)
	}
}
