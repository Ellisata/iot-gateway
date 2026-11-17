//go:build !windows

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile 使用 flock 做非阻塞排他锁。
//
// 只对本地文件系统可靠；数据目录本来就不允许放在 NFS/SMB 上（SQLite 的
// 锁语义在网络文件系统上无法保证），这里不再额外探测。
func lockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
