// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"sync"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/logger"
)

const (
	// configDebounce 是配置变更后合并写入的静默窗口。一次 Excel 导入或平台
	// 下发会连写很多张表，窗口内只生成一份 config 包。持续写入会不断重置
	// 计时器，包在最后一次写入的 60 秒后生成。
	configDebounce = 60 * time.Second
)

// Watcher 在配置表发生写入后生成 config 范围备份。
//
// 启动后先等待 startupDelay 生成一份基线包（给迁移与首轮配置稳定留时间），
// 之后只对配置白名单表的写入做出反应；full 定时备份仍由 Scheduler 负责。
type Watcher struct {
	cfg     *configFile.Config
	version string

	kick chan struct{}

	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

// NewWatcher 创建变更监听器（未启动）。
func NewWatcher(cfg *configFile.Config, version string) *Watcher {
	return &Watcher{
		cfg:     cfg,
		version: version,
		kick:    make(chan struct{}, 1),
	}
}

// Notify 由 sqlite 写回调调用；非配置表直接忽略。
func (w *Watcher) Notify(table string) {
	if !IsConfigTable(table) {
		return
	}
	select {
	case w.kick <- struct{}{}:
	default: // 已有未处理的变更，合并
	}
}

// Start 启动基线 + 防抖循环。backup.enabled=false 时静默返回。
func (w *Watcher) Start() {
	eff := EffectiveConfig(w.cfg)
	if !eff.Enabled {
		return
	}
	w.mu.Lock()
	if w.stop != nil {
		w.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	w.stop, w.done = stop, done
	w.mu.Unlock()

	logger.Info("backup: config watcher started (debounce=%s)", configDebounce)
	go w.loop(stop, done, eff)
}

// Stop 停止监听并等待循环退出。未启动时是 no-op。
func (w *Watcher) Stop() {
	w.mu.Lock()
	stop, done := w.stop, w.done
	w.stop, w.done = nil, nil
	w.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

func (w *Watcher) loop(stop <-chan struct{}, done chan<- struct{}, eff Effective) {
	defer close(done)

	// 基线：等启动延迟后先备一份 config，之后才有“最近一份配置”可用。
	//
	// 基线计时不能阻塞变更处理：否则启动后的第一次配置修改会被压在
	// startupDelay 之后才开始防抖，实际出包时间远超 60 秒。
	baseline := time.NewTimer(eff.StartupDelay)
	defer baseline.Stop()

	var debounce *time.Timer
	var debounceC <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()

	var lastRun time.Time
	run := func() {
		w.runOnce(eff)
		lastRun = time.Now()
	}
	resetDebounce := func() {
		if debounce == nil {
			debounce = time.NewTimer(configDebounce)
		} else {
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(configDebounce)
		}
		debounceC = debounce.C
	}

	for {
		select {
		case <-stop:
			return
		case <-baseline.C:
			// 若启动延迟内已经因变更生成过包，基线就是多余的。
			if lastRun.IsZero() {
				run()
			}
		case <-w.kick:
			resetDebounce()
		case <-debounceC:
			debounceC = nil
			run()
		}
	}
}

func (w *Watcher) runOnce(eff Effective) {
	opts := BuildOptionsFromConfig(w.cfg, "", "", ScopeConfig, "watcher")
	opts.Now = time.Now()
	if w.version != "" {
		opts.Version = w.version
	}
	b, err := CreateBundle(context.Background(), opts)
	if err != nil {
		logger.Warn("backup: config change backup failed: %v", err)
		Audit(eff.Dir, AuditEvent{
			Action:   "create",
			Operator: "watcher",
			Source:   "watcher",
			Scope:    ScopeConfig,
			Result:   "failed",
			Error:    err.Error(),
		})
		return
	}
	logger.Debug("backup: config change backup created %s", b.Path)

	deleted, err := ApplyRetention(eff.Dir, eff.Keep, eff.MaxTotalMB)
	if err != nil {
		logger.Warn("backup: retention failed: %v", err)
		return
	}
	if len(deleted) > 0 {
		logger.Info("backup: retention removed %d old bundle(s)", len(deleted))
	}
}
