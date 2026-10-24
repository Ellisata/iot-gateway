// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	appLogger "iot-gateway/logger"
)

const (
	// walCheckpointInterval 定期维护间隔。
	//
	// 自动 checkpoint 只在**写提交**越过 wal_autocheckpoint 阈值时触发；配置库在
	// 长时间低写入期里，最后一小段帧会一直留在 -wal 里不合并，文件也不缩小。
	// 每 10 分钟兜一次既不打扰采集/推送，又能在外部读者离开后尽快恢复截断。
	walCheckpointInterval = 10 * time.Minute

	// walCheckpointTimeout 单次 checkpoint 的上界，与驱动默认 busy_timeout(5000) 同量级。
	//
	// 注意：go-sqlite 没有实现 QueryerContext，进行中的 sqlite3_step **不会**被 ctx
	// 打断，真正兜住时间的是 busy_timeout。ctx 只负责连接池获取与给出一个明确的错误。
	walCheckpointTimeout = 5 * time.Second
)

// WALCheckpointer 定期把 WAL 里已合并的帧合并回主库，并在没有读者时把 -wal 截断到 0。
//
// 为什么需要它（而不是只靠 SQLite 的自动 checkpoint）：
//   - 自动 checkpoint 是「按写入量」触发的，不是按时间；低写入期 WAL 不会合并；
//   - PASSIVE checkpoint 遇到旧快照的读者推不过去，此时 WAL 会持续增长 —— 这本身
//     不会让读变慢（读者走 -shm 的 wal-index，不扫 WAL），但会让下一次 checkpoint
//     的拷贝量、启动恢复时间和磁盘占用线性变大，且没有任何告警。
//
// 本项目进程内只有一条 SQLite 连接（MaxOpenConns=1），能钉住 WAL 的只可能是外部
// 进程（sqlite CLI、第二个网关实例、备份工具），runOnce 会把这种情况打成 WARN。
type WALCheckpointer struct {
	db       *gorm.DB
	interval time.Duration

	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

// walCheckpointResult 一次 checkpoint 的结果。
//
// Busy/Log/Checkpointed 取自 PASSIVE 阶段：Log 是 WAL 里的总帧数，Checkpointed 是
// 已合并进主库的帧数。Truncated 表示随后 TRUNCATE 是否成功（失败同样是 busy=1 的
// 普通结果行，不是 error）。
type walCheckpointResult struct {
	Busy         int
	Log          int
	Checkpointed int
	Truncated    bool
}

// NewWALCheckpointer 创建维护器（未启动）。
func NewWALCheckpointer(db *gorm.DB) *WALCheckpointer {
	return &WALCheckpointer{db: db, interval: walCheckpointInterval}
}

// Start 启动后台维护。可重复调用（已启动则 no-op）。
func (w *WALCheckpointer) Start() {
	w.mu.Lock()
	if w.stop != nil {
		w.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	w.stop, w.done = stop, done
	w.mu.Unlock()
	go w.loop(stop, done)
}

// Stop 停止维护并等待协程退出。未启动时是 no-op。
func (w *WALCheckpointer) Stop() {
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

func (w *WALCheckpointer) loop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			w.runOnce()
		}
	}
}

// runOnce 执行一次维护并记录结果。所有数据库错误都只记日志，不影响业务。
func (w *WALCheckpointer) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), walCheckpointTimeout)
	defer cancel()

	r, err := w.checkpoint(ctx)
	if err != nil {
		appLogger.Warn("sqlite wal checkpoint failed: %v", err)
		return
	}

	switch {
	case r.Log < 0:
		// PRAGMA 在非 WAL 模式下把两个计数置为 -1（journal_mode=WAL 没能生效时）。
		appLogger.Debug("sqlite wal checkpoint skipped: database is not in WAL mode")
	case r.Busy != 0:
		// PASSIVE 的 busy=1 只表示另一个进程正持有 checkpoint/写锁（读者不会让它 busy），
		// 此时 log/checkpointed 通常也是 -1，没有可报告的数字。
		appLogger.Warn("sqlite wal checkpoint is busy: another process holds the checkpoint or writer lock, "+
			"will retry in %s", w.interval)
	case r.Checkpointed < r.Log:
		appLogger.Warn("sqlite wal is pinned by an external reader: merged %d/%d frames; "+
			"the WAL will keep growing until that reader finishes (sqlite CLI, a second gateway "+
			"instance, a backup tool?)", r.Checkpointed, r.Log)
	case !r.Truncated:
		// PASSIVE 刚确认全部帧已合并，TRUNCATE 却被挡下：两次调用之间冒出了读者。
		appLogger.Warn("sqlite wal was merged (%d frames) but truncate was blocked by a reader/writer, "+
			"will retry in %s", r.Log, w.interval)
	default:
		appLogger.Debug("sqlite wal checkpoint ok: merged %d frames, -wal truncated", r.Log)
	}
}

// checkpoint 先 PASSIVE 合并帧，确认全部合并后再 TRUNCATE 收缩文件。
//
// 为什么不直接 TRUNCATE：TRUNCATE 会等所有读者（最长 busy_timeout=5s），并且会占住
// 本项目唯一的那条连接 —— 外部进程随便开个未提交的读事务，就能把整个网关的数据库
// 访问卡住 5 秒。PASSIVE 只尝试拿一次锁、立刻返回，安全得多；只有它证明没有读者
// 钉住帧时，才升级为 TRUNCATE。
func (w *WALCheckpointer) checkpoint(ctx context.Context) (walCheckpointResult, error) {
	var r walCheckpointResult

	sqlDB, err := w.db.DB()
	if err != nil {
		return r, fmt.Errorf("get sql.DB: %w", err)
	}

	// PRAGMA 总是返回一行三列 (busy, log, checkpointed)，busy 是列值而不是 error。
	if err := sqlDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").
		Scan(&r.Busy, &r.Log, &r.Checkpointed); err != nil {
		return r, fmt.Errorf("passive checkpoint: %w", err)
	}

	// 读者钉住快照时 PASSIVE 返回 busy=0 但 log > checkpointed —— 这正是不能只看
	// busy 的原因。Log < 0 表示库不在 WAL 模式，没有可收缩的文件。
	if r.Busy != 0 || r.Log < 0 || r.Checkpointed < r.Log {
		return r, nil
	}

	// TRUNCATE 放弃时同样返回一行 busy=1（不是 error），必须扫出来判断。
	var busy, log, checkpointed int
	if err := sqlDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&busy, &log, &checkpointed); err != nil {
		return r, fmt.Errorf("truncate checkpoint: %w", err)
	}
	r.Truncated = busy == 0
	return r, nil
}
