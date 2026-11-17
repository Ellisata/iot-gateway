// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package backup 实现网关本机数据备份与停机恢复（P0）。
//
// 备份单位是 .igbak 包（zip）：manifest + VACUUM INTO 一致性快照 + 外部配置 +
// 插件清单。运行期在线快照不阻塞采集/推送；恢复必须停服后用 CLI 执行，替换
// iot-gateway.db / -wal / -shm 三件套。完整设计见 specs/data-backup.md。
package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const lockFileName = ".gateway.lock"

// ErrLocked 表示数据目录已被另一个网关进程（或正在执行的恢复）占用。
var ErrLocked = errors.New("数据目录已被另一个网关进程占用")

// Lock 是数据目录的进程级排他锁。
//
// 它解决两件事：
//   - 同一数据目录不允许两个网关实例同时运行（SQLite 单写，双实例还会重复
//     轮询 PLC、重复发布 MQTT、在 discovery 里广播同一个 ID）；
//   - CLI restore 必须确认网关已停止才能替换数据库文件。Windows 下打开中的
//     文件 rename 会失败，但 Linux 下 rename 会成功而进程继续使用旧 inode，
//     产生"恢复成功但没生效"的假象，所以不能只靠文件占用判断。
//
// 锁是 OS 级的（Unix flock / Windows LockFileEx），进程退出自动释放；锁文件
// 本身保留在数据目录里，不删除，避免释放与下一个进程加锁之间的竞态。
type Lock struct {
	f    *os.File
	path string
}

// AcquireLock 非阻塞地获取数据目录的排他锁。
//
// 目录不存在时会创建（与 InitDB 的 MkdirAll 同权限）。拿不到锁返回包装了
// ErrLocked 的错误，调用方可用 errors.Is 判断。
func AcquireLock(dir string) (*Lock, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", abs, err)
	}

	path := filepath.Join(abs, lockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open gateway lock %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s (%v)", ErrLocked, abs, err)
	}
	return &Lock{f: f, path: path}, nil
}

// Path 返回锁文件路径，供日志与排错使用。
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Release 释放锁。可重复调用。
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	unlockErr := unlockFile(f)
	closeErr := f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
