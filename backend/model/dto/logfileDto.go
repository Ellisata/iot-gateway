// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// ==================== 日志查看 DTO ====================

// ListLogFileDTO 日志文件列表分页。
// page/size 缺省（0）时不校验，由 service 取默认值（1/20）；显式传非法值则报错。
type ListLogFileDTO struct {
	Page int `form:"page" binding:"omitempty,min=1"`
	Size int `form:"size" binding:"omitempty,min=1,max=100"`
}

// ReadLogDTO 日志内容读取（fileName 取末尾 N 行）
type ReadLogDTO struct {
	FileName string `form:"fileName" binding:"required"`
	Tail     int    `form:"tail"` // 末尾 N 行，默认 200，上限 5000（service 内钳制）
}
