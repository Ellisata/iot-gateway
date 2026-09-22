//go:build windows

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fionreadWSA WSAIoctl 的 FIONREAD 命令码：查询接收缓冲中可读的字节数。
//
// x/sys/windows 未导出该常量（只导出了 WSAIoctl 函数），故在此按 winsock2.h
// 的定义写出：IOC_OUT(0x40000000) | IOC_WS2(0x08000000) | (3 << 16) | 127。
const fionreadWSA = 0x4004667F

// sockPendingBytes 返回 TCP 连接接收缓冲中**已到达但尚未被读取**的字节数。
//
// 供 Drain 判断「有没有残留可丢」：返回 0 时链路上没有已到达的字节，
// 无需再进入带截止时间的读循环去空等一个超时周期。
// ok=false 表示无法查询（连接不支持裸 fd），调用方退化为原有的定时读——
// 探测失败只丢性能，不影响 Drain 的正确性。
func sockPendingBytes(conn *net.TCPConn) (int, bool) {
	rc, err := conn.SyscallConn()
	if err != nil {
		return 0, false
	}
	var n uint32
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		var returned uint32
		ioErr = windows.WSAIoctl(windows.Handle(fd), fionreadWSA,
			nil, 0, (*byte)(unsafe.Pointer(&n)), 4, &returned, nil, 0)
	}); err != nil {
		return 0, false
	}
	if ioErr != nil {
		return 0, false
	}
	return int(n), true
}
