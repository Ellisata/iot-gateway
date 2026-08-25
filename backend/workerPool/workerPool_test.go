package workerPool

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestRestartAfterStop 回归测试：Stop 后再次 Start，worker 应能正常工作。
// 旧实现 Stop close(quit) 后不重建 channel，重启的新 worker 会立即命中已关闭的 quit
// 直接退出，导致 pool 表现为「运行中」却无活 worker，提交的任务永不执行。
func TestRestartAfterStop(t *testing.T) {
	p := NewWorkerPool(2, 4)
	p.Start()

	var executed int32
	p.Submit(func() { atomic.AddInt32(&executed, 1) })
	waitExecuted(t, &executed, 1, "first phase")

	p.Stop()

	// 重新 Start 后再次提交：若 quit 未重建，此任务将不被执行
	p.Start()
	p.Submit(func() { atomic.AddInt32(&executed, 1) })
	waitExecuted(t, &executed, 2, "restart phase")

	p.Stop()
}

func waitExecuted(t *testing.T, executed *int32, want int32, phase string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(executed) != want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(executed); got != want {
		t.Fatalf("%s: task not executed, got %d want %d", phase, got, want)
	}
}
