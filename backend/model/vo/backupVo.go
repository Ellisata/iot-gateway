// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// BackupBundleVO 备份列表中的一项。
type BackupBundleVO struct {
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

// BackupPendingVO 待恢复任务。
type BackupPendingVO struct {
	State        string `json:"state"`
	Scope        string `json:"scope"`
	Identity     string `json:"identity"`
	Bundle       string `json:"bundle,omitempty"`
	BundleSHA256 string `json:"bundleSha256"`
	UploadedBy   string `json:"uploadedBy"`
	CreatedAt    string `json:"createdAt"`
	Size         int64  `json:"size"`
}

// BackupStatusVO 备份页顶部的状态摘要。
type BackupStatusVO struct {
	Enabled         bool             `json:"enabled"`
	Dir             string           `json:"dir"`
	DefaultScope    string           `json:"defaultScope"`
	Keep            int              `json:"keep"`
	MaxTotalMB      int              `json:"maxTotalMB"`
	IntervalSeconds int              `json:"intervalSeconds"`
	DebounceSeconds int              `json:"debounceSeconds"`
	Pending         *BackupPendingVO `json:"pending,omitempty"`
}
