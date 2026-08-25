//go:build !windows

package main

// runAsWindowsService 在非 Windows 平台恒为 false，走前台模式（如 Linux systemd 由 install.sh 托管）。
func runAsWindowsService() bool {
	return false
}
