// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// LogFileVO 日志文件信息
type LogFileVO struct {
	FileName   string `json:"fileName"`
	Size       int64  `json:"size"`
	ModifyTime string `json:"modifyTime"`
}

// LogReadVO 日志内容读取结果
type LogReadVO struct {
	FileName   string   `json:"fileName"`
	TotalLines int      `json:"totalLines"`
	Lines      []string `json:"lines"`
}
