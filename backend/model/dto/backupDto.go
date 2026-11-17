// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// CreateBackupDTO 手动创建备份。
type CreateBackupDTO struct {
	// Scope full | config；留空取 backup.defaultScope。
	Scope string `json:"scope"`
}

// StageBackupDTO 把备份目录里已有的包暂存为“下次启动恢复”。
type StageBackupDTO struct {
	Name string `json:"name" binding:"required"`
	// Identity takeover | clone；留空为 takeover。
	Identity string `json:"identity"`
}
