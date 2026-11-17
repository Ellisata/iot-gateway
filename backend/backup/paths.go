// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"iot-gateway/configFile"
)

const (
	defaultDBPath    = "data/iot-gateway.db"
	defaultBackupDir = "data/backups"
	defaultPluginDir = "plugins"

	defaultStartupDelay    = 5 * time.Minute
	defaultInterval        = 6 * time.Hour
	defaultKeep            = 14
	defaultMaxTotalMB      = 512
	defaultSnapshotTimeout = 60 * time.Second
)

// DBPath 解析数据库路径：显式覆盖 > 配置 > 默认值。
func DBPath(cfg *configFile.Config, override string) string {
	if override != "" {
		return override
	}
	if cfg != nil && strings.TrimSpace(cfg.SQLite.Path) != "" {
		return cfg.SQLite.Path
	}
	return defaultDBPath
}

// DataDir 返回数据库所在目录。锁文件、恢复时的 pre-restore 副本都放在这里。
func DataDir(cfg *configFile.Config, dbOverride string) string {
	dir := filepath.Dir(DBPath(cfg, dbOverride))
	if dir == "" {
		return "."
	}
	return dir
}

// BackupDir 解析备份目录：显式覆盖 > 配置 > 默认值。
func BackupDir(cfg *configFile.Config, override string) string {
	if override != "" {
		return override
	}
	if cfg != nil && strings.TrimSpace(cfg.Backup.Dir) != "" {
		return cfg.Backup.Dir
	}
	return defaultBackupDir
}

// DefaultScope 返回配置里的默认备份范围（P0 实际只支持 full）。
func DefaultScope(cfg *configFile.Config) string {
	if cfg != nil {
		if s := strings.TrimSpace(cfg.Backup.DefaultScope); s != "" {
			return s
		}
	}
	return ScopeFull
}

// ResolvePluginDir 按 pluginhost 的规则解析插件目录：空或相对路径相对
// os.Executable() 所在目录，而不是 cwd（Windows 服务的 cwd 是 System32）。
func ResolvePluginDir(cfg *configFile.Config) string {
	dir := defaultPluginDir
	if cfg != nil && strings.TrimSpace(cfg.Plugin.Dir) != "" {
		dir = cfg.Plugin.Dir
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), dir)
}

// ResolvePluginGlob 返回插件文件名匹配规则。
func ResolvePluginGlob(cfg *configFile.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Plugin.Glob) != "" {
		return cfg.Plugin.Glob
	}
	return "iot-driver-*"
}

// Effective 是备份配置解析默认值后的结果。
type Effective struct {
	Enabled         bool
	Dir             string
	Scope           string
	StartupDelay    time.Duration
	Interval        time.Duration
	Keep            int
	MaxTotalMB      int
	SnapshotTimeout time.Duration
}

// EffectiveConfig 把 default.yaml 的零值/缺失值补成可用的默认配置。
func EffectiveConfig(cfg *configFile.Config) Effective {
	eff := Effective{
		Enabled:         false,
		Dir:             defaultBackupDir,
		Scope:           ScopeFull,
		StartupDelay:    defaultStartupDelay,
		Interval:        defaultInterval,
		Keep:            defaultKeep,
		MaxTotalMB:      defaultMaxTotalMB,
		SnapshotTimeout: defaultSnapshotTimeout,
	}
	if cfg == nil {
		return eff
	}
	eff.Enabled = cfg.Backup.Enabled
	if d := strings.TrimSpace(cfg.Backup.Dir); d != "" {
		eff.Dir = d
	}
	if s := strings.TrimSpace(cfg.Backup.DefaultScope); s != "" {
		eff.Scope = s
	}
	if cfg.Backup.StartupDelayMinutes > 0 {
		eff.StartupDelay = time.Duration(cfg.Backup.StartupDelayMinutes) * time.Minute
	}
	if cfg.Backup.IntervalHours > 0 {
		eff.Interval = time.Duration(cfg.Backup.IntervalHours) * time.Hour
	}
	if cfg.Backup.Keep > 0 {
		eff.Keep = cfg.Backup.Keep
	}
	if cfg.Backup.MaxTotalMB > 0 {
		eff.MaxTotalMB = cfg.Backup.MaxTotalMB
	}
	if cfg.Backup.SnapshotTimeoutSeconds > 0 {
		eff.SnapshotTimeout = time.Duration(cfg.Backup.SnapshotTimeoutSeconds) * time.Second
	}
	return eff
}
