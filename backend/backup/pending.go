// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/logger"
)

const (
	pendingDirName    = "restore-pending"
	pendingBundleName = "bundle.igbak"
	pendingMetaName   = "restore.json"

	pendingStatePending  = "pending"
	pendingStatePrepared = "prepared"
)

// ErrPendingExists 表示已经有一个待恢复任务，需要先取消或完成它。
var ErrPendingExists = errors.New("已存在待恢复任务，请先取消或重启网关")

// PendingInfo 描述一个已暂存、等待重启生效的恢复任务。
type PendingInfo struct {
	State        string `json:"state"`
	Scope        string `json:"scope"`
	Identity     string `json:"identity"`
	Bundle       string `json:"bundle,omitempty"`
	BundleSHA256 string `json:"bundleSha256"`
	UploadedBy   string `json:"uploadedBy"`
	CreatedAt    string `json:"createdAt"`
	BundlePath   string `json:"-"`
	Size         int64  `json:"size"`
}

type pendingMeta struct {
	State        string `json:"state"`
	Scope        string `json:"scope"`
	Identity     string `json:"identity"`
	Bundle       string `json:"bundle,omitempty"`
	BundleSHA256 string `json:"bundleSha256"`
	UploadedBy   string `json:"uploadedBy"`
	CreatedAt    string `json:"createdAt"`
}

func pendingDir(dataDir string) string {
	return filepath.Join(dataDir, pendingDirName)
}

// StageRestore 校验备份包并复制到 <data-dir>/restore-pending/，等下次启动应用。
//
// 页面/API 只做“上传 + 校验 + 暂存 + 提示重启”，不在运行中热替换数据库。
func StageRestore(dataDir, backupDir, bundlePath, identity, operator string, now time.Time) (*PendingInfo, error) {
	return stageRestore(dataDir, backupDir, bundlePath, filepath.Base(bundlePath), identity, operator, now)
}

// stageRestore 与 StageRestore 相同，但可指定写入审计与待恢复元数据的展示名。
// 上传流落到临时文件后文件名没有意义，用调用方给的原始文件名。
func stageRestore(dataDir, backupDir, bundlePath, displayName, identity, operator string, now time.Time) (*PendingInfo, error) {
	identity, err := normalizeIdentity(identity)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}

	dir := pendingDir(dataDir)
	if _, err := os.Stat(filepath.Join(dir, pendingMetaName)); err == nil {
		return nil, ErrPendingExists
	}

	vb, err := VerifyBundle(bundlePath)
	if err != nil {
		return nil, err
	}
	manifest := vb.Manifest
	vb.Cleanup()

	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("clear pending directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create pending directory: %w", err)
	}
	target := filepath.Join(dir, pendingBundleName)
	if err := copyFile(bundlePath, target, 0600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("stage bundle: %w", err)
	}
	sum, size, err := fileSHA256(target)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("hash staged bundle: %w", err)
	}
	meta := pendingMeta{
		State:        pendingStatePending,
		Scope:        manifest.Scope,
		Identity:     identity,
		Bundle:       displayName,
		BundleSHA256: sum,
		UploadedBy:   operator,
		CreatedAt:    now.Format(time.RFC3339),
	}
	if err := writePendingMeta(dir, &meta); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	Audit(backupDir, AuditEvent{
		Action:   "stage",
		Operator: operator,
		Source:   "api",
		Bundle:   displayName,
		SHA256:   sum,
		Scope:    manifest.Scope,
		Identity: identity,
		Result:   "ok",
	})
	return &PendingInfo{
		State:        meta.State,
		Scope:        meta.Scope,
		Identity:     meta.Identity,
		Bundle:       meta.Bundle,
		BundleSHA256: meta.BundleSHA256,
		UploadedBy:   meta.UploadedBy,
		CreatedAt:    meta.CreatedAt,
		BundlePath:   target,
		Size:         size,
	}, nil
}

// StageUploadedBundle 把上传流写入临时文件、校验后暂存。
// 校验失败时不会在 pending 目录留下任何东西。
func StageUploadedBundle(dataDir, backupDir, filename string, r io.Reader, identity, operator string, now time.Time) (*PendingInfo, error) {
	if _, err := normalizeIdentity(identity); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "iot-gateway-upload-*.igbak")
	if err != nil {
		return nil, fmt.Errorf("create upload temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("save uploaded bundle: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("flush uploaded bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close uploaded bundle: %w", err)
	}
	if strings.TrimSpace(filename) == "" {
		filename = pendingBundleName
	}
	return stageRestore(dataDir, backupDir, tmpName, filepath.Base(filename), identity, operator, now)
}

// ReadPending 返回当前待恢复任务；没有时返回 (nil, nil)。
func ReadPending(dataDir string) (*PendingInfo, error) {
	dir := pendingDir(dataDir)
	meta, err := readPendingMeta(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	bundle := filepath.Join(dir, pendingBundleName)
	info, err := os.Stat(bundle)
	if err != nil {
		return nil, fmt.Errorf("pending bundle missing: %w", err)
	}
	return &PendingInfo{
		State:        meta.State,
		Scope:        meta.Scope,
		Identity:     meta.Identity,
		Bundle:       meta.Bundle,
		BundleSHA256: meta.BundleSHA256,
		UploadedBy:   meta.UploadedBy,
		CreatedAt:    meta.CreatedAt,
		BundlePath:   bundle,
		Size:         info.Size(),
	}, nil
}

// CancelPending 删除待恢复任务。没有任务时返回 nil。
func CancelPending(dataDir, backupDir, operator string) error {
	dir := pendingDir(dataDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove pending restore: %w", err)
	}
	Audit(backupDir, AuditEvent{
		Action:   "cancel-pending",
		Operator: operator,
		Source:   "api",
		Result:   "ok",
	})
	return nil
}

// ApplyPendingRestore 在启动时（InitDB 之前、数据目录锁已由调用方持有）应用
// 暂存的恢复任务。
//
// 失败不会阻止网关启动：整个 pending 目录改名为 restore-pending.failed-<ts>，
// 继续用旧库启动，避免现场变砖。返回的错误只用于日志。
func ApplyPendingRestore(ctx context.Context, cfg *configFile.Config, version string) error {
	dataDir := DataDir(cfg, "")
	auditDir := BackupDir(cfg, "")
	pending, err := ReadPending(dataDir)
	if err != nil {
		Audit(auditDir, AuditEvent{
			Action: "restore", Source: "startup", Result: "failed", Error: err.Error(),
		})
		failPendingDir(dataDir)
		return err
	}
	if pending == nil {
		// 没有 restore.json 的残留目录（上次删除时被文件句柄挡住等）直接清掉；
		// StageRestore 也会在写入前清理，这里只是让启动后目录保持干净。
		_ = os.RemoveAll(pendingDir(dataDir))
		return nil
	}

	// 标记 prepared：即使进程在替换过程中崩溃，下次启动仍会重试这份包。
	dir := pendingDir(dataDir)
	if meta, err := readPendingMeta(dir); err == nil {
		meta.State = pendingStatePrepared
		_ = writePendingMeta(dir, meta)
	}

	opts := RestoreOptions{
		BundlePath:     pending.BundlePath,
		DBPath:         DBPath(cfg, ""),
		Identity:       pending.Identity,
		CurrentVersion: version,
		Now:            time.Now(),
	}
	absDB, vb, err := prepareRestore(opts)
	if err != nil {
		Audit(auditDir, AuditEvent{
			Action: "restore", Source: "startup", Bundle: pending.Bundle,
			SHA256: pending.BundleSHA256, Scope: pending.Scope, Identity: pending.Identity,
			Result: "failed", Error: err.Error(),
		})
		failPendingDir(dataDir)
		return err
	}
	_ = absDB
	defer vb.Cleanup()

	// 调用方（main）已持有数据目录锁，不再重复获取。
	result, err := restoreVerified(ctx, vb, absDB, opts)
	// 必须先关闭 zip 再删/改名 pending 目录：Windows 上未关闭的文件句柄会让
	// bundle.igbak 无法删除或重命名，导致“恢复成功但任务永远留在 pending”。
	vb.Cleanup()
	if err != nil {
		Audit(auditDir, AuditEvent{
			Action: "restore", Source: "startup", Bundle: pending.Bundle,
			SHA256: pending.BundleSHA256, Scope: pending.Scope, Identity: pending.Identity,
			Result: "failed", Error: err.Error(),
		})
		failPendingDir(dataDir)
		return err
	}

	if err := os.RemoveAll(dir); err != nil {
		logger.Warn("backup: pending restore applied but directory cleanup failed: %v", err)
	}
	Audit(auditDir, AuditEvent{
		Action:   "restore",
		Operator: pending.UploadedBy,
		Source:   "startup",
		Bundle:   pending.Bundle,
		SHA256:   pending.BundleSHA256,
		Scope:    result.Manifest.Scope,
		Identity: result.Identity,
		Result:   "ok",
	})
	logger.Info("backup: pending restore applied (scope=%s, identity=%s, pre-restore=%v)",
		result.Manifest.Scope, result.Identity, result.PreRestore)
	return nil
}

func normalizeIdentity(identity string) (string, error) {
	if strings.TrimSpace(identity) == "" {
		return identityTakeover, nil
	}
	if identity != identityTakeover && identity != identityClone {
		return "", fmt.Errorf("未知的身份模式 %q（可选 takeover / clone）", identity)
	}
	return identity, nil
}

func readPendingMeta(dir string) (*pendingMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, pendingMetaName))
	if err != nil {
		return nil, err
	}
	var meta pendingMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", pendingMetaName, err)
	}
	if meta.State == "" {
		meta.State = pendingStatePending
	}
	return &meta, nil
}

func writePendingMeta(dir string, meta *pendingMeta) error {
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending metadata: %w", err)
	}
	return writeFileSynced(filepath.Join(dir, pendingMetaName), raw, 0600)
}

func failPendingDir(dataDir string) {
	dir := pendingDir(dataDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return
	}
	failed := uniquePath(dir + ".failed-" + time.Now().Format("20060102-150405"))
	if err := os.Rename(dir, failed); err != nil {
		logger.Error("backup: move failed pending restore to %s: %v", failed, err)
		return
	}
	logger.Error("backup: pending restore failed; kept for inspection at %s", failed)
}
