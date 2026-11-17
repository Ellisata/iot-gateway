// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"iot-gateway/appError"
	"iot-gateway/backup"
	"iot-gateway/model/dto"
	"iot-gateway/model/vo"
)

// BackupService 备份与恢复的页面/API 服务。
//
// 真正的快照、校验、替换逻辑都在 backup 包；这里只做 VO 投影与错误透传。
// 恢复不在本服务里执行：页面只负责“校验 + 暂存”，重启时由 backup 包应用。
type BackupService struct {
	manager *backup.Manager
}

// NewBackupService 创建备份服务
func NewBackupService(manager *backup.Manager) *BackupService {
	return &BackupService{manager: manager}
}

// List 列出备份包（新到旧）。
func (s *BackupService) List() ([]vo.BackupBundleVO, error) {
	infos, err := s.manager.List()
	if err != nil {
		return nil, wrapBackupError(err)
	}
	out := make([]vo.BackupBundleVO, 0, len(infos))
	for _, info := range infos {
		out = append(out, vo.BackupBundleVO{
			Name:    info.Name,
			Scope:   info.Scope,
			Size:    info.Size,
			ModTime: info.ModTime.Format("2006-01-02 15:04:05"),
		})
	}
	return out, nil
}

// Create 立即生成一份备份。
func (s *BackupService) Create(req *dto.CreateBackupDTO, operator string) (*vo.BackupBundleVO, error) {
	scope := ""
	if req != nil {
		scope = req.Scope
	}
	b, err := s.manager.Create(scope, operator)
	if err != nil {
		return nil, wrapBackupError(err)
	}
	info, err := backupStat(b.Path)
	if err != nil {
		return nil, wrapBackupError(err)
	}
	return &vo.BackupBundleVO{
		Name:    info.Name,
		Scope:   b.Manifest.Scope,
		Size:    info.Size,
		ModTime: time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

// Delete 删除一个备份包。
func (s *BackupService) Delete(name, operator string) error {
	return wrapBackupError(s.manager.Delete(name, operator))
}

// Download 返回包路径并记录下载审计。
func (s *BackupService) Download(name, operator string) (string, error) {
	path, err := s.manager.BundlePath(name)
	if err != nil {
		return "", wrapBackupError(err)
	}
	s.manager.RecordDownload(name, operator, "api")
	return path, nil
}

// Stage 暂存已有备份包，等下次重启恢复。
func (s *BackupService) Stage(req *dto.StageBackupDTO, operator string) (*vo.BackupPendingVO, error) {
	info, err := s.manager.Stage(req.Name, req.Identity, operator)
	if err != nil {
		return nil, wrapBackupError(err)
	}
	return toPendingVO(info), nil
}

// Upload 暂存上传的备份包。
func (s *BackupService) Upload(filename string, r io.Reader, identity, operator string) (*vo.BackupPendingVO, error) {
	info, err := s.manager.StageUpload(filename, r, identity, operator)
	if err != nil {
		return nil, wrapBackupError(err)
	}
	return toPendingVO(info), nil
}

// CancelPending 取消待恢复任务。
func (s *BackupService) CancelPending(operator string) error {
	return wrapBackupError(s.manager.CancelPending(operator))
}

// Status 返回调度状态与待恢复任务。
func (s *BackupService) Status() (*vo.BackupStatusVO, error) {
	st, err := s.manager.Status()
	if err != nil {
		return nil, wrapBackupError(err)
	}
	out := &vo.BackupStatusVO{
		Enabled:         st.Enabled,
		Dir:             st.Dir,
		DefaultScope:    st.DefaultScope,
		Keep:            st.Keep,
		MaxTotalMB:      st.MaxTotalMB,
		IntervalSeconds: st.IntervalSeconds,
		DebounceSeconds: st.DebounceSeconds,
	}
	if st.Pending != nil {
		out.Pending = toPendingVO(st.Pending)
	}
	return out, nil
}

func toPendingVO(info *backup.PendingInfo) *vo.BackupPendingVO {
	if info == nil {
		return nil
	}
	return &vo.BackupPendingVO{
		State:        info.State,
		Scope:        info.Scope,
		Identity:     info.Identity,
		Bundle:       info.Bundle,
		BundleSHA256: info.BundleSHA256,
		UploadedBy:   info.UploadedBy,
		CreatedAt:    info.CreatedAt,
		Size:         info.Size,
	}
}

// wrapBackupError 把 backup 包的原始错误透出给调用方。
//
// 恢复/校验失败的原因必须让运维看到（包里哪个文件校验不符、目标库被占用、
// schema 过新……），统一压成 “system error” 无法指导现场处理。
func wrapBackupError(err error) error {
	if err == nil {
		return nil
	}
	return appError.NewAppError("BACKUP_ERROR", err.Error())
}

type backupFileInfo struct {
	Name string
	Size int64
}

func backupStat(path string) (*backupFileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &backupFileInfo{Name: filepath.Base(path), Size: info.Size()}, nil
}
