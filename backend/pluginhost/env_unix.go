// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

//go:build !windows

package pluginhost

import (
	"os/exec"
	"syscall"
)

// platformEnvNames Unix 侧需要保留的变量（HOME/TMPDIR 让子进程能写临时文件，
// LANG 影响插件日志的编码输出）。
var platformEnvNames = []string{"HOME", "TMPDIR", "LANG"}

// pluginSysProcAttr Unix 上无需特殊属性。
func pluginSysProcAttr() *syscall.SysProcAttr { return nil }

// pluginAfterStart Unix 上没有与 Windows Job Object 等效且安全的机制。
//
// 特意**不用** syscall.SysProcAttr.Pdeathsig：它在 Linux 上绑定的是
// 「创建子进程的那个**线程**」而非进程，而 Go 的 goroutine 会在 OS 线程间迁移 ——
// 一旦那个线程退出（Go 运行时回收线程是常态），内核就会给插件发 SIGKILL，
// **即使网关还活得好好的**。误杀比泄漏严重得多，所以这里不做：
// 孤儿防护退回 stdin EOF 那一层（覆盖全部使用 driversdk.Serve 的插件），
// 边界说明见 docs/driver-plugins.md 的「防孤儿进程」。
func pluginAfterStart(*exec.Cmd) error { return nil }
