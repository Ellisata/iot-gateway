// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"iot-gateway/logger"
)

const (
	auditFileName = "audit.log"
	auditMaxBytes = 10 << 20 // 10MB，超出后滚动为 audit.log.1
)

// AuditEvent 是一条备份审计记录。
//
// 备份包内含明文凭据，创建/下载/删除/暂存恢复都必须留下“谁、从哪、对哪个包、
// 结果如何”的痕迹。审计同时写 logger 与备份目录下的 audit.log：前者便于实时
// 排查，后者在整台机器迁移时可随目录一起带走。
type AuditEvent struct {
	Time     string `json:"time"`
	Action   string `json:"action"` // create | download | delete | stage | restore | restore-failed
	Operator string `json:"operator,omitempty"`
	Source   string `json:"source,omitempty"` // scheduler | watcher | cli | api | startup
	Bundle   string `json:"bundle,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Identity string `json:"identity,omitempty"`
	Result   string `json:"result"` // ok | failed
	Error    string `json:"error,omitempty"`
}

var auditMu sync.Mutex

// Audit 追加一条审计记录。写文件失败只记日志，不影响备份/恢复本身。
func Audit(dir string, ev AuditEvent) {
	if ev.Time == "" {
		ev.Time = time.Now().Format(time.RFC3339)
	}
	if ev.Result == "" {
		ev.Result = "ok"
	}
	if ev.Error != "" {
		logger.Warn("backup audit: action=%s operator=%s bundle=%s result=%s error=%s",
			ev.Action, ev.Operator, ev.Bundle, ev.Result, ev.Error)
	} else {
		logger.Info("backup audit: action=%s operator=%s bundle=%s scope=%s result=%s",
			ev.Action, ev.Operator, ev.Bundle, ev.Scope, ev.Result)
	}
	if strings.TrimSpace(dir) == "" {
		return
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		logger.Warn("backup audit: marshal event failed: %v", err)
		return
	}

	auditMu.Lock()
	defer auditMu.Unlock()
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("backup audit: create directory %s failed: %v", dir, err)
		return
	}
	path := filepath.Join(dir, auditFileName)
	rotateAudit(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		logger.Warn("backup audit: open %s failed: %v", path, err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		logger.Warn("backup audit: write %s failed: %v", path, err)
	}
}

func rotateAudit(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < auditMaxBytes {
		return
	}
	_ = os.Rename(path, path+".1")
}
