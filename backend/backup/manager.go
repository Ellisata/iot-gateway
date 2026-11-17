// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/database/sqlite"
	"iot-gateway/logger"
)

// ErrDisabled 表示配置里关闭了备份功能，页面/API 入口不可用（CLI 不受影响）。
var ErrDisabled = errors.New("备份功能已关闭（backup.enabled=false）")

// ErrBundleNotFound 表示备份目录里没有这个包。
var ErrBundleNotFound = errors.New("备份包不存在")

// ErrInvalidBundleName 表示文件名不符合本服务生成的命名规则（防目录穿越）。
var ErrInvalidBundleName = errors.New("非法的备份包文件名")

// Manager 是进程内备份功能的门面：启动调度与变更监听、供页面/API 调用。
//
// 单实例：由 Wire 在应用依赖图里构造一次，main 负责 Start/Stop。
type Manager struct {
	cfg     *configFile.Config
	version string

	scheduler *Scheduler
	watcher   *Watcher
}

// NewManager 构造备份管理器并注册配置写回调（变更防抖的数据来源）。
func NewManager(cfg *configFile.Config) *Manager {
	version := configFile.ResolveGatewayVersion(cfg)
	m := &Manager{
		cfg:       cfg,
		version:   version,
		scheduler: NewScheduler(cfg, version),
		watcher:   NewWatcher(cfg, version),
	}
	// sqlite 包不认识备份，只负责在写配置表后回调表名。
	sqlite.RegisterWriteHook(m.watcher.Notify)
	return m
}

// Start 启动定时 full 备份与配置变更监听。
func (m *Manager) Start() {
	eff := EffectiveConfig(m.cfg)
	if !eff.Enabled {
		logger.Info("backup: manager disabled (backup.enabled=false); CLI still available")
		return
	}
	m.scheduler.Start()
	m.watcher.Start()
}

// Stop 停止两个后台循环。幂等。
func (m *Manager) Stop() {
	if m.watcher != nil {
		m.watcher.Stop()
	}
	if m.scheduler != nil {
		m.scheduler.Stop()
	}
}

// Enabled 返回配置是否启用备份。
func (m *Manager) Enabled() bool {
	return EffectiveConfig(m.cfg).Enabled
}

// BackupDir 返回生效的备份目录。
func (m *Manager) BackupDir() string { return BackupDir(m.cfg, "") }

// DataDir 返回数据库所在目录。
func (m *Manager) DataDir() string { return DataDir(m.cfg, "") }

// List 列出本服务生成的备份包（新到旧）。
func (m *Manager) List() ([]BundleInfo, error) {
	return ListBundles(m.BackupDir())
}

// Create 立即生成一份备份（页面手动触发）。
func (m *Manager) Create(scope, operator string) (*Bundle, error) {
	if !m.Enabled() {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(scope) == "" {
		scope = DefaultScope(m.cfg)
	}
	if scope != ScopeFull && scope != ScopeConfig {
		return nil, fmt.Errorf("未知备份范围 %q（可选 full / config）", scope)
	}
	eff := EffectiveConfig(m.cfg)
	opts := BuildOptionsFromConfig(m.cfg, "", "", scope, operator)
	opts.Now = time.Now()
	if m.version != "" {
		opts.Version = m.version
	}
	b, err := CreateBundle(context.Background(), opts)
	if err != nil {
		Audit(m.BackupDir(), AuditEvent{
			Action: "create", Operator: operator, Source: "api", Scope: scope,
			Result: "failed", Error: err.Error(),
		})
		return nil, err
	}
	if _, err := ApplyRetention(eff.Dir, eff.Keep, eff.MaxTotalMB); err != nil {
		logger.Warn("backup: retention failed: %v", err)
	}
	return b, nil
}

// BundlePath 校验文件名并返回包路径。
func (m *Manager) BundlePath(name string) (string, error) {
	if !bundleNameRe.MatchString(name) || filepath.Base(name) != name {
		return "", ErrInvalidBundleName
	}
	path := filepath.Join(m.BackupDir(), name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrBundleNotFound
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", ErrBundleNotFound
	}
	return path, nil
}

// Verify 校验备份包并返回 manifest 摘要。
func (m *Manager) Verify(name string) (*Manifest, error) {
	path, err := m.BundlePath(name)
	if err != nil {
		return nil, err
	}
	vb, err := VerifyBundle(path)
	if err != nil {
		return nil, err
	}
	defer vb.Cleanup()
	manifest := vb.Manifest
	return &manifest, nil
}

// RecordDownload 写一条下载审计。
func (m *Manager) RecordDownload(name, operator, source string) {
	path, err := m.BundlePath(name)
	if err != nil {
		return
	}
	sum, _, _ := fileSHA256(path)
	Audit(m.BackupDir(), AuditEvent{
		Action: "download", Operator: operator, Source: source, Bundle: name, SHA256: sum,
		Result: "ok",
	})
}

// Delete 删除一个备份包。
func (m *Manager) Delete(name, operator string) error {
	path, err := m.BundlePath(name)
	if err != nil {
		return err
	}
	sum, _, _ := fileSHA256(path)
	if err := os.Remove(path); err != nil {
		Audit(m.BackupDir(), AuditEvent{
			Action: "delete", Operator: operator, Source: "api", Bundle: name,
			SHA256: sum, Result: "failed", Error: err.Error(),
		})
		return err
	}
	Audit(m.BackupDir(), AuditEvent{
		Action: "delete", Operator: operator, Source: "api", Bundle: name, SHA256: sum, Result: "ok",
	})
	return nil
}

// Stage 暂存一个已有备份包，等下次重启恢复。
func (m *Manager) Stage(name, identity, operator string) (*PendingInfo, error) {
	if !m.Enabled() {
		return nil, ErrDisabled
	}
	path, err := m.BundlePath(name)
	if err != nil {
		return nil, err
	}
	return StageRestore(m.DataDir(), m.BackupDir(), path, identity, operator, time.Now())
}

// StageUpload 暂存一个上传的备份包。filename 用于审计与待恢复任务展示。
func (m *Manager) StageUpload(filename string, r io.Reader, identity, operator string) (*PendingInfo, error) {
	if !m.Enabled() {
		return nil, ErrDisabled
	}
	return StageUploadedBundle(m.DataDir(), m.BackupDir(), filename, r, identity, operator, time.Now())
}

// CancelPending 取消待恢复任务。
func (m *Manager) CancelPending(operator string) error {
	return CancelPending(m.DataDir(), m.BackupDir(), operator)
}

// Pending 返回当前待恢复任务。
func (m *Manager) Pending() (*PendingInfo, error) {
	return ReadPending(m.DataDir())
}

// Status 是页面顶部的状态摘要。
type Status struct {
	Enabled         bool         `json:"enabled"`
	Dir             string       `json:"dir"`
	DefaultScope    string       `json:"defaultScope"`
	Keep            int          `json:"keep"`
	MaxTotalMB      int          `json:"maxTotalMB"`
	IntervalSeconds int          `json:"intervalSeconds"`
	DebounceSeconds int          `json:"debounceSeconds"`
	Pending         *PendingInfo `json:"pending,omitempty"`
}

// Status 汇总调度配置与待恢复任务。
func (m *Manager) Status() (*Status, error) {
	eff := EffectiveConfig(m.cfg)
	pending, err := ReadPending(m.DataDir())
	if err != nil {
		return nil, err
	}
	return &Status{
		Enabled:         eff.Enabled,
		Dir:             eff.Dir,
		DefaultScope:    eff.Scope,
		Keep:            eff.Keep,
		MaxTotalMB:      eff.MaxTotalMB,
		IntervalSeconds: int(eff.Interval.Seconds()),
		DebounceSeconds: int(configDebounce.Seconds()),
		Pending:         pending,
	}, nil
}
