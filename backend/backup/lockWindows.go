//go:build windows

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile 使用 LockFileEx 做非阻塞排他锁。
//
// 锁住文件的首字节（overlapped 范围为 1 字节），不依赖文件内容；进程退出时
// Windows 会自动释放。失败通常是 ERROR_LOCK_VIOLATION（另一个进程持有）。
func lockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
}

func unlockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
