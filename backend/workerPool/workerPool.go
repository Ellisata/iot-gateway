// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package workerPool

import (
	"sync"
	"sync/atomic"

	"iot-gateway/logger"
)

// WorkerPool 固定大小的 goroutine 池，用于并发执行采集任务。
// 提供阻塞提交 (Submit) 和非阻塞尝试 (TrySubmit) 两种方式。
type WorkerPool struct {
	maxWorkers int
	taskChan   chan func()
	quit       chan struct{}
	workerWg   sync.WaitGroup
	running    int32
}

// NewWorkerPool 创建指定大小的 worker 池。
// maxWorkers: 最大并发 goroutine 数
// queueSize:  任务缓冲队列大小
func NewWorkerPool(maxWorkers, queueSize int) *WorkerPool {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}
	return &WorkerPool{
		maxWorkers: maxWorkers,
		taskChan:   make(chan func(), queueSize),
		quit:       make(chan struct{}),
	}
}

// Start 启动所有 worker goroutine。
func (p *WorkerPool) Start() {
	if !atomic.CompareAndSwapInt32(&p.running, 0, 1) {
		return // 已在运行
	}
	// 每次 Start 重建退出通知 channel：
	// Stop 已 close 旧 channel，若复用则新 worker 会立即命中已关闭的 quit 直接退出，
	// 导致 pool 表现为「运行中」却无活 worker（Stop→Start 后采集静默死亡）。
	p.quit = make(chan struct{})
	for i := 0; i < p.maxWorkers; i++ {
		p.workerWg.Add(1)
		go p.worker(i)
	}
	logger.Info("worker pool started with %d workers", p.maxWorkers)
}

// Stop 停止所有 worker，等待当前任务完成。
func (p *WorkerPool) Stop() {
	if !atomic.CompareAndSwapInt32(&p.running, 1, 0) {
		return
	}
	close(p.quit)
	p.workerWg.Wait()
	logger.Info("worker pool stopped")
}

// Submit 阻塞提交任务到队列。
func (p *WorkerPool) Submit(task func()) {
	if atomic.LoadInt32(&p.running) == 0 {
		logger.Warn("worker pool not running, task discarded")
		return
	}
	p.taskChan <- task
}

// TrySubmit 非阻塞提交任务，队列满时立即返回 false。
func (p *WorkerPool) TrySubmit(task func()) bool {
	if atomic.LoadInt32(&p.running) == 0 {
		logger.Warn("worker pool not running, task discarded")
		return false
	}
	select {
	case p.taskChan <- task:
		return true
	default:
		return false
	}
}

// IsRunning 返回 pool 是否在运行中。
func (p *WorkerPool) IsRunning() bool {
	return atomic.LoadInt32(&p.running) == 1
}

// QueueSize 返回当前排队中的任务数。
func (p *WorkerPool) QueueSize() int {
	return len(p.taskChan)
}

// worker 单个 worker goroutine 循环。
func (p *WorkerPool) worker(id int) {
	defer p.workerWg.Done()
	logger.Debug("worker %d started", id)
	for {
		select {
		case <-p.quit:
			logger.Debug("worker %d stopped", id)
			return
		case task := <-p.taskChan:
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("worker %d panic: %v", id, r)
					}
				}()
				task()
			}()
		}
	}
}
