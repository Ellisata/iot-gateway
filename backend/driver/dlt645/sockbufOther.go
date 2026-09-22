//go:build !windows && !linux

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import "net"

// sockPendingBytes 在 Linux 与 Windows 之外的平台不可用，恒返回 ok=false。
//
// 网关只发布 linux（含 arm/arm64）与 windows 产物，这些平台上的实现见
// sockbufUnix.go / sockbufWindows.go。这里保留一个如实降级的实现，是为了让
// darwin/BSD 上的开发机也能编译测试：ok=false 会让 Drain 走原有的定时读，
// 只丢性能、不影响正确性（见 transport.go 对 Drain 的约定）。
//
// 不在此处硬编码 TIOCINQ/FIONREAD：darwin 与 BSD 的该 ioctl 号与 Linux 不同，
// 写死一个值等于在没有真机验证的情况下猜语义——正是本驱动一贯避免的做法。
func sockPendingBytes(*net.TCPConn) (int, bool) { return 0, false }
