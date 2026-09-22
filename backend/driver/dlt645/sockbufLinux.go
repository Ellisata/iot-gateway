//go:build linux

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"net"

	"golang.org/x/sys/unix"
)

// sockPendingBytes 返回 TCP 连接接收缓冲中**已到达但尚未被读取**的字节数。
//
// 供 Drain 判断「有没有残留可丢」：返回 0 时链路上没有已到达的字节，
// 无需再进入带截止时间的读循环去空等一个超时周期。
// ok=false 表示无法查询（连接不支持裸 fd），调用方退化为原有的定时读——
// 探测失败只丢性能，不影响 Drain 的正确性。
//
// TIOCINQ 即 FIONREAD（Linux 上两者同值 0x541B），语义为「可读字节数」。
// x/sys 只在 Linux 的 zerrors 里定义了该常量（darwin/BSD 的 TIOCINQ 是另一个值
// 且未导出），故本文件限定 linux，其余平台见 sockbufOther.go。
func sockPendingBytes(conn *net.TCPConn) (int, bool) {
	rc, err := conn.SyscallConn()
	if err != nil {
		return 0, false
	}
	n := 0
	ok := false
	if err := rc.Control(func(fd uintptr) {
		v, err := unix.IoctlGetInt(int(fd), unix.TIOCINQ)
		if err != nil {
			return
		}
		n, ok = v, true
	}); err != nil || !ok {
		return 0, false
	}
	return n, true
}
