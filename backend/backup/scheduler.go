// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"os"
	"sync"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/logger"
)

// Scheduler 在网关进程内定时生成本机备份，并按保留策略清理旧包。
//
// 快照走独立连接，不占用主库那条唯一写连接；单次运行失败只记日志，等下一个
// 周期重试。备份目录/间隔/保留份数来自 default.yaml 的 backup 段。
type Scheduler struct {
	cfg     *configFile.Config
	version string

	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

// NewScheduler 创建调度器（未启动）。
func NewScheduler(cfg *configFile.Config, version string) *Scheduler {
	return &Scheduler{cfg: cfg, version: version}
}

// Start 启动后台循环。backup.enabled=false 时静默返回。
func (s *Scheduler) Start() {
	eff := EffectiveConfig(s.cfg)
	if !eff.Enabled {
		logger.Info("backup: scheduler disabled (backup.enabled=false)")
		return
	}
	if eff.Scope != ScopeFull {
		// 定时任务固定 full（含报警历史与断网缓存）；config 由变更监听器负责。
		logger.Info("backup: scheduled backups always use %q; config scope is handled by the change watcher",
			ScopeFull)
		eff.Scope = ScopeFull
	}
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.stop, s.done = stop, done
	s.mu.Unlock()

	logger.Info("backup: scheduler started (dir=%s, interval=%s, keep=%d)",
		eff.Dir, eff.Interval, eff.Keep)
	go s.loop(stop, done, eff)
}

// Stop 停止后台循环并等待当前一轮结束。未启动时是 no-op。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
	logger.Info("backup: scheduler stopped")
}

func (s *Scheduler) loop(stop <-chan struct{}, done chan<- struct{}, eff Effective) {
	defer close(done)

	delay := eff.StartupDelay
	for {
		select {
		case <-stop:
			return
		case <-time.After(delay):
		}
		s.runOnce(eff)
		// 固定间隔而不是 ticker：一轮超时不会堆积下一次。
		delay = eff.Interval
	}
}

func (s *Scheduler) runOnce(eff Effective) {
	opts := BuildOptionsFromConfig(s.cfg, "", "", eff.Scope, "scheduler")
	opts.Now = time.Now()
	if s.version != "" {
		opts.Version = s.version
	}
	if _, err := CreateBundle(context.Background(), opts); err != nil {
		logger.Warn("backup: scheduled backup failed: %v", err)
		Audit(eff.Dir, AuditEvent{
			Action: "create", Operator: "scheduler", Source: "scheduler", Scope: ScopeFull,
			Result: "failed", Error: err.Error(),
		})
		return
	}

	deleted, err := ApplyRetention(eff.Dir, eff.Keep, eff.MaxTotalMB)
	if err != nil {
		logger.Warn("backup: retention failed: %v", err)
		return
	}
	if len(deleted) > 0 {
		logger.Info("backup: retention removed %d old bundle(s)", len(deleted))
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
