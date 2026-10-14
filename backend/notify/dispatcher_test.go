// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/model/po"
)

// newTestDB 临时文件 SQLite + AutoMigrate（与生产一致，文件库保证跨连接可见）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "notify.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.AlarmWebhook{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// fastOptions 毫秒级退避，让重试相关用例快速跑完。
func fastOptions() Options {
	return Options{
		IngressSize:    64,
		QueueSize:      8,
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     40 * time.Millisecond,
		HTTPTimeout:    2 * time.Second,
		StopDrain:      2 * time.Second,
		WatchInterval:  time.Hour, // 用例里不依赖轮询，手动 reload
	}
}

// insertWebhook 插入一条启用中的 webhook 配置。
func insertWebhook(t *testing.T, db *gorm.DB, cfg *po.AlarmWebhook) {
	t.Helper()
	insertWebhookStatus(t, db, cfg, 1)
}

// insertWebhookStatus 插入指定启用状态的 webhook 配置（status=0 为停用）。
func insertWebhookStatus(t *testing.T, db *gorm.DB, cfg *po.AlarmWebhook, status int) {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	cfg.Status = status
	cfg.CreatedAt, cfg.UpdatedAt = now, now
	if err := db.Create(cfg).Error; err != nil {
		t.Fatalf("insert webhook: %v", err)
	}
	if status == 0 {
		// status 带 gorm default:1，零值会被 GORM 当作「未设置」而套用默认值，
		// 因此停用状态必须在插入后显式再更新一次。
		if err := db.Model(&po.AlarmWebhook{}).Where("id = ?", cfg.ID).
			Update("status", 0).Error; err != nil {
			t.Fatalf("disable webhook: %v", err)
		}
	}
}

// waitFor 轮询等待条件成立，避免依赖固定 sleep。
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// recorder 记录收到的请求次数与最近一次 body。
type recorder struct {
	mu     sync.Mutex
	count  int
	bodies [][]byte
	delay  time.Duration
	fail   atomic.Int32 // >0 时按次返回 500
	block  chan struct{}
}

func (r *recorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.count++
		r.mu.Unlock()

		if r.block != nil {
			<-r.block
		}
		if r.delay > 0 {
			time.Sleep(r.delay)
		}
		if r.fail.Load() > 0 {
			r.fail.Add(-1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		r.mu.Lock()
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := req.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		r.bodies = append(r.bodies, body)
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func newTestDispatcher(t *testing.T, db *gorm.DB, opts Options) *Dispatcher {
	t.Helper()
	d := newDispatcherWithOptions(db, opts)
	if err := d.Start(); err != nil {
		t.Fatalf("start dispatcher: %v", err)
	}
	t.Cleanup(d.Stop)
	return d
}

func TestDispatcherSendsAlarm(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	d := newTestDispatcher(t, db, fastOptions())

	d.Notify(testOfflineEvent())

	waitFor(t, 2*time.Second, "one delivered alarm", func() bool { return rec.calls() == 1 })
	waitFor(t, 2*time.Second, "sent counter", func() bool {
		st := d.GetStatus()
		return len(st.Webhooks) == 1 && st.Webhooks[0].SentCount == 1
	})

	st := d.GetStatus()
	if st.Webhooks[0].FailedCount != 0 {
		t.Fatalf("expected no failures, got %+v", st.Webhooks[0])
	}
	if st.Webhooks[0].LastSuccessTime == "" {
		t.Fatal("expected lastSuccessTime to be stamped")
	}
}

// 失败后重试成功：请求次数 = 失败次数 + 1，且最终计入成功。
func TestDispatcherRetriesThenSucceeds(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{}
	rec.fail.Store(2) // 前两次 500
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	d := newTestDispatcher(t, db, fastOptions())

	d.Notify(testOfflineEvent())

	waitFor(t, 3*time.Second, "retry to succeed", func() bool {
		st := d.GetStatus()
		return len(st.Webhooks) == 1 && st.Webhooks[0].SentCount == 1
	})
	if got := rec.calls(); got != 3 {
		t.Fatalf("expected 3 attempts (2 failures + 1 success), got %d", got)
	}
	if st := d.GetStatus(); st.Webhooks[0].FailedCount != 0 {
		t.Fatalf("expected no failures after eventual success, got %+v", st.Webhooks[0])
	}
}

// 永久失败（400）不重试。
func TestDispatcherPermanentFailureNoRetry(t *testing.T) {
	db := newTestDB(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	d := newTestDispatcher(t, db, fastOptions())

	d.Notify(testOfflineEvent())

	waitFor(t, 2*time.Second, "failure recorded", func() bool {
		st := d.GetStatus()
		return len(st.Webhooks) == 1 && st.Webhooks[0].FailedCount == 1
	})
	time.Sleep(200 * time.Millisecond) // 留出「本不该发生」的重试窗口
	if got := calls.Load(); got != 1 {
		t.Fatalf("permanent failure must not be retried, got %d attempts", got)
	}
	if st := d.GetStatus(); st.Webhooks[0].LastErr == "" {
		t.Fatal("expected lastErr to be recorded")
	}
}

// 重试耗尽后丢弃并计数。
func TestDispatcherExhaustsAttempts(t *testing.T) {
	db := newTestDB(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	opts := fastOptions()
	opts.MaxAttempts = 3
	d := newTestDispatcher(t, db, opts)

	d.Notify(testOfflineEvent())

	waitFor(t, 3*time.Second, "attempts exhausted", func() bool {
		st := d.GetStatus()
		return len(st.Webhooks) == 1 && st.Webhooks[0].FailedCount == 1
	})
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected exactly MaxAttempts(3) attempts, got %d", got)
	}
}

// 核心契约：队列打满时 Notify 必须立即返回，绝不阻塞报警状态机。
func TestDispatcherNotifyNeverBlocks(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{block: make(chan struct{})} // handler 阻塞住，让队列迅速积压
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()
	defer close(rec.block)

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	opts := fastOptions()
	opts.QueueSize = 2
	opts.IngressSize = 4
	d := newTestDispatcher(t, db, opts)

	start := time.Now()
	for i := 0; i < 200; i++ {
		d.Notify(testOfflineEvent())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Notify blocked: 200 calls took %s", elapsed)
	}

	st := d.GetStatus()
	if st.IngressDropped == 0 && st.Webhooks[0].DroppedCount == 0 {
		t.Fatalf("expected drops to be counted under overflow, got %+v", st)
	}
}

// 未启动时 Notify 直接丢弃并计数（不 panic、不积压）。
func TestDispatcherNotifyBeforeStartDrops(t *testing.T) {
	db := newTestDB(t)
	d := newDispatcherWithOptions(db, fastOptions())

	d.Notify(testOfflineEvent()) // 不应 panic

	if got := d.GetStatus().IngressDropped; got != 1 {
		t.Fatalf("expected 1 ingress drop before start, got %d", got)
	}
}

// 热加载：URL 变更后新事件走新地址；无关行的实例保持原样（指针不变）。
func TestDispatcherReloadKeepsUnchangedInstances(t *testing.T) {
	db := newTestDB(t)
	recOld := &recorder{}
	srvOld := httptest.NewServer(recOld.handler())
	defer srvOld.Close()
	recNew := &recorder{}
	srvNew := httptest.NewServer(recNew.handler())
	defer srvNew.Close()

	insertWebhook(t, db, &po.AlarmWebhook{ID: "w-1", Name: "群A", Type: TypeCustom, URL: srvOld.URL, Status: 1})
	insertWebhook(t, db, &po.AlarmWebhook{ID: "w-2", Name: "群B", Type: TypeCustom, URL: srvNew.URL, Status: 1})

	d := newTestDispatcher(t, db, fastOptions())
	waitFor(t, time.Second, "two workers", func() bool { return len(d.GetStatus().Webhooks) == 2 })

	d.mu.RLock()
	beforeB := d.workers["w-2"]
	d.mu.RUnlock()

	// 只改 w-1 的地址
	if err := db.Model(&po.AlarmWebhook{}).Where("id = ?", "w-1").
		Update("url", srvNew.URL).Error; err != nil {
		t.Fatal(err)
	}
	d.reload()

	d.mu.RLock()
	afterB := d.workers["w-2"]
	afterA := d.workers["w-1"]
	d.mu.RUnlock()

	if beforeB != afterB {
		t.Fatal("untouched webhook should keep its runtime instance across reload")
	}
	if afterA == nil {
		t.Fatal("changed webhook should have a rebuilt instance")
	}

	// 变更后事件应走新地址
	before := recOld.calls()
	d.Notify(testOfflineEvent())
	waitFor(t, 2*time.Second, "new endpoint to receive", func() bool { return recNew.calls() >= 1 })
	time.Sleep(100 * time.Millisecond)
	if after := recOld.calls(); after != before {
		t.Fatalf("old endpoint should no longer receive, got %d -> %d calls", before, after)
	}
}

// 停用/配置非法的行不产生实例，且不影响其他行。
func TestDispatcherSkipsDisabledAndInvalid(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{ID: "w-1", Name: "启用", Type: TypeCustom, URL: srv.URL})
	insertWebhookStatus(t, db, &po.AlarmWebhook{ID: "w-2", Name: "停用", Type: TypeCustom, URL: srv.URL}, 0)
	insertWebhook(t, db, &po.AlarmWebhook{ID: "w-3", Name: "非法", Type: "telegram", URL: srv.URL})

	d := newTestDispatcher(t, db, fastOptions())
	waitFor(t, time.Second, "one worker", func() bool { return len(d.GetStatus().Webhooks) == 1 })

	if name := d.GetStatus().Webhooks[0].Name; name != "启用" {
		t.Fatalf("unexpected worker: %s", name)
	}
}

// 停机排空：结束前把队列里的事件尽力发完。
func TestDispatcherStopDrainsQueue(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{delay: 30 * time.Millisecond}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	opts := fastOptions()
	opts.StopDrain = 3 * time.Second
	d := newDispatcherWithOptions(db, opts)
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		d.Notify(testOfflineEvent())
	}
	time.Sleep(20 * time.Millisecond) // 让事件进入 fanout/worker 队列

	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within budget")
	}

	if got := rec.calls(); got != 3 {
		t.Fatalf("expected all 3 queued alarms to be attempted during drain, got %d", got)
	}
}

// Stop 之后再 Start 应能重新工作（不因已关闭的通道而失效）。
func TestDispatcherRestartable(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})

	d := newDispatcherWithOptions(db, fastOptions())
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	d.Stop()

	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	defer d.Stop()

	d.Notify(testOfflineEvent())
	waitFor(t, 2*time.Second, "delivery after restart", func() bool { return rec.calls() >= 1 })
}

// 事件内容按目标类型正确渲染（这里用报警包的真实事件构造）。
func TestDispatcherRendersChannelRecoverEvent(t *testing.T) {
	db := newTestDB(t)
	rec := &recorder{}
	srv := httptest.NewServer(rec.handler())
	defer srv.Close()

	insertWebhook(t, db, &po.AlarmWebhook{Name: "自定义群", Type: TypeCustom, URL: srv.URL})
	d := newTestDispatcher(t, db, fastOptions())

	d.Notify(alarm.Event{
		AlarmID: "a-9", TargetID: "ch-1", TargetName: "mqtt1",
		TargetType: alarm.TypeChannel, AlarmType: alarm.TypeRecover,
		Level: "warning", Content: "推送通道 mqtt1 恢复通信",
		Status:         alarm.StatusCleared,
		FirstOccurTime: "2026-09-24 10:05:00", ClearTime: "2026-09-24 10:05:00",
		OccurredAt: time.Now(),
	})

	waitFor(t, 2*time.Second, "one delivered alarm", func() bool { return rec.calls() == 1 })

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var payload map[string]any
	if err := json.Unmarshal(rec.bodies[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload["targetType"] != "channel" || payload["alarmType"] != "recover" {
		t.Fatalf("unexpected payload: %v", payload)
	}
}
