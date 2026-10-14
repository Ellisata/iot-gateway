//go:build !windows

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

// runAsWindowsService 在非 Windows 平台恒为 false，走前台模式（如 Linux systemd 由 install.sh 托管）。
func runAsWindowsService() bool {
	return false
}
