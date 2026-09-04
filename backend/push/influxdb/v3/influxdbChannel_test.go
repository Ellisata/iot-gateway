// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"iot-gateway/collector"
	"iot-gateway/push"
)

// testCfg 由 httptest server URL 构造通道配置(拆分 host/port/useSSL)。
func testCfg(rawURL, token, measurement string) *influxdbConfig {
	u, _ := url.Parse(rawURL)
	host, port, _ := net.SplitHostPort(u.Host)
	return &influxdbConfig{
		Host:        host,
		Port:        portStr(port),
		UseSSL:      u.Scheme == "https",
		Token:       token,
		Database:    "iot",
		Measurement: measurement,
	}
}

// fakeSpool 内存版 push.Spool,用于驱动 drainWindow 的窄化测试(不依赖 SQLite)。
type fakeSpool struct {
	mu      sync.Mutex
	nextID  int64
	batches map[int64]push.SpoolBatch
}

func newFakeSpool() *fakeSpool {
	return &fakeSpool{batches: make(map[int64]push.SpoolBatch)}
}

func (s *fakeSpool) Insert(b push.PushBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.batches[s.nextID] = push.SpoolBatch{ID: s.nextID, DeviceID: b.DeviceID, CollectedAt: b.CollectedAt, Records: b.Records}
	return nil
}

func (s *fakeSpool) FetchOldest(limit int) ([]push.SpoolBatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.batches))
	for id := range s.batches {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if limit < len(ids) {
		ids = ids[:limit]
	}
	out := make([]push.SpoolBatch, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.batches[id])
	}
	return out, nil
}

func (s *fakeSpool) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.batches, id)
	return nil
}

func (s *fakeSpool) DeleteBatch(ids []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.batches, id)
	}
	return nil
}

func (s *fakeSpool) DeleteOldest(n int) error {
	pend, _ := s.FetchOldest(n)
	for _, b := range pend {
		s.Delete(b.ID)
	}
	return nil
}

func (s *fakeSpool) Count() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.batches)), nil
}

// testChannel 构造一个可直接驱动 tryConnect/write 的通道(不启动 goroutine,无需 SQLite)。
func testChannel(cfg *influxdbConfig) *influxdbChannel {
	return &influxdbChannel{
		id:     "test-id",
		name:   "influxdb",
		cfg:    cfg,
		sig:    "test-id|influxdb|sig",
		client: newHTTPClient(cfg),
	}
}

// lastErrNow 加锁读取 lastErr(测试辅助)
func (ch *influxdbChannel) lastErrNow() string {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.lastErr
}

func TestTryConnectSuccess(t *testing.T) {
	var mu sync.Mutex
	var healthAuth, cfgAuth, dbBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/health":
			mu.Lock()
			healthAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/configure/database":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			cfgAuth = r.Header.Get("Authorization")
			dbBody = string(b)
			mu.Unlock()
			w.WriteHeader(http.StatusConflict) // 库已存在:幂等视为成功
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "tok123", "collected_data")
	ch := testChannel(cfg)
	ch.tryConnect()

	if !ch.connected.Load() {
		t.Fatal("tryConnect should mark connected on healthy server")
	}
	if errMsg := ch.lastErrNow(); errMsg != "" {
		t.Errorf("lastErr = %q, want empty", errMsg)
	}
	if healthAuth != "Bearer tok123" {
		t.Errorf("health auth = %q, want Bearer tok123", healthAuth)
	}
	if cfgAuth != "Bearer tok123" {
		t.Errorf("configure auth = %q, want Bearer tok123", cfgAuth)
	}
	if dbBody != `{"db":"iot"}` {
		t.Errorf("configure body = %q, want %q", dbBody, `{"db":"iot"}`)
	}
}

func TestTryConnectHealthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // token 无效
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "bad", "m")
	ch := testChannel(cfg)
	ch.tryConnect()

	if ch.connected.Load() {
		t.Error("should not connect on 401 health check")
	}
	if !strings.Contains(ch.lastErrNow(), "401") {
		t.Errorf("lastErr should mention 401, got %q", ch.lastErrNow())
	}
}

func TestTryConnectNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 端口已关闭 → 连接被拒

	cfg := testCfg(url, "t", "m")
	ch := testChannel(cfg)
	ch.tryConnect()

	if ch.connected.Load() {
		t.Error("should not connect on network error")
	}
	if ch.lastErrNow() == "" {
		t.Error("lastErr should be set on network error")
	}
}

func TestWriteSuccess(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath, gotAuth, gotContentType, gotDB, gotPrecision, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotDB = r.URL.Query().Get("db")
		gotPrecision = r.URL.Query().Get("precision")
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "tok123", "m")
	ch := testChannel(cfg)
	ch.connected.Store(true)

	if !ch.write("collected_data,device_id=d value_int=1i,value_kind=\"int\",quality=0i 123") {
		t.Fatal("write should succeed on 204")
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v3/write_lp" {
		t.Errorf("request = %s %s, want POST /api/v3/write_lp", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth = %q, want Bearer tok123", gotAuth)
	}
	if gotContentType != "text/plain; charset=utf-8" {
		t.Errorf("content-type = %q", gotContentType)
	}
	if gotDB != "iot" || gotPrecision != "millisecond" {
		t.Errorf("query db/precision = %q/%q", gotDB, gotPrecision)
	}
	if gotBody != "collected_data,device_id=d value_int=1i,value_kind=\"int\",quality=0i 123" {
		t.Errorf("body = %q", gotBody)
	}
	if ch.publishCount.Load() != 1 {
		t.Errorf("publishCount = %d, want 1", ch.publishCount.Load())
	}
	if errMsg := ch.lastErrNow(); errMsg != "" {
		t.Errorf("lastErr = %q, want empty", errMsg)
	}
}

func TestWriteNon2xx(t *testing.T) {
	// 4xx(除 429)为请求级错误(数据非法),连接本身健康,不置 disconnected:
	// 置断会触发重连抖动并让实时写入空窗,坏数据交由补发协程毒批次窄化机制隔离。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid line protocol"))
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "t", "m")
	ch := testChannel(cfg)
	ch.connected.Store(true)

	if ch.write("bad line") {
		t.Fatal("write should fail on 400")
	}
	if !ch.connected.Load() {
		t.Error("400 (request-level error) should NOT mark disconnected")
	}
	if !strings.Contains(ch.lastErrNow(), "400") || !strings.Contains(ch.lastErrNow(), "invalid line protocol") {
		t.Errorf("lastErr should carry status and body, got %q", ch.lastErrNow())
	}
	if ch.publishCount.Load() != 0 {
		t.Errorf("publishCount = %d, want 0", ch.publishCount.Load())
	}
}

func TestWriteServerErrorDisconnects(t *testing.T) {
	// 5xx(服务端故障)视为断连,交重连循环退避
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "t", "m")
	ch := testChannel(cfg)
	ch.connected.Store(true)

	if ch.write("line") {
		t.Fatal("write should fail on 500")
	}
	if ch.connected.Load() {
		t.Error("5xx should mark disconnected")
	}
}

func TestWriteTooManyRequestsDisconnects(t *testing.T) {
	// 429 限流视为断连,退避后重试
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "t", "m")
	ch := testChannel(cfg)
	ch.connected.Store(true)

	if ch.write("line") {
		t.Fatal("write should fail on 429")
	}
	if ch.connected.Load() {
		t.Error("429 should mark disconnected")
	}
}

func TestWriteNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 端口已关闭 → 连接被拒

	cfg := testCfg(url, "t", "m")
	ch := testChannel(cfg)
	ch.connected.Store(true)

	if ch.write("line") {
		t.Fatal("write should fail on network error")
	}
	if ch.connected.Load() {
		t.Error("should mark disconnected on network error")
	}
	if ch.lastErrNow() == "" {
		t.Error("lastErr should be set on network error")
	}
}

func TestWriteEmptyBody(t *testing.T) {
	cfg := testCfg("http://127.0.0.1:1", "t", "m")
	ch := testChannel(cfg)
	ch.connected.Store(true)
	// 空 body 视为成功,不发请求(不会因端口不可达失败)
	if !ch.write("") {
		t.Fatal("empty body write should be treated as success")
	}
}

func TestWriteDisconnected(t *testing.T) {
	cfg := testCfg("http://127.0.0.1:1", "t", "m")
	ch := testChannel(cfg)
	// connected=false:直接返回失败,不发请求
	if ch.write("line") {
		t.Fatal("write while disconnected should fail")
	}
}

// spoolBatch 构造一个批量测试记录批次
func spoolBatch(id int64, addressID string) push.SpoolBatch {
	return push.SpoolBatch{
		ID:          id,
		DeviceID:    "dev-1",
		CollectedAt: "2026-08-19 10:00:00.000",
		Records: []collector.CollectedRecord{
			{DeviceID: "dev-1", DeviceAddressID: addressID, Value: "1", DataType: "int16", Quality: 192},
		},
	}
}

func TestDrainWindowNarrowIsolatesBadBatch(t *testing.T) {
	// 坏批次(b3,地址 a3)位于组中,不在组头:普通聚合整组写失败被拒,若按组头判毒会
	// 连坐误删好批次 b1/b2;窄化(narrow)强制单批一组后,应只定位到 b3,好批次先被写成功。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "device_address_id=a3") {
			w.WriteHeader(http.StatusBadRequest) // 模拟 InfluxDB 拒收该批次
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "t", "m")
	ch := testChannel(cfg)
	sp := newFakeSpool()
	ch.spool = sp
	ch.connected.Store(true)

	for _, sb := range []push.SpoolBatch{
		spoolBatch(1, "a1"),
		spoolBatch(2, "a2"),
		spoolBatch(3, "a3"), // 坏批次
		spoolBatch(4, "a4"),
	} {
		if err := sp.Insert(push.PushBatch{DeviceID: sb.DeviceID, CollectedAt: sb.CollectedAt, Records: sb.Records}); err != nil {
			t.Fatal(err)
		}
	}

	// 1) 普通聚合:整组(含坏批次)一次写入失败,failID 是组头 b1(不能据此删 b1)
	pend, _ := sp.FetchOldest(16)
	if ok, failID := ch.drainWindow(pend, false); ok || failID != 1 {
		t.Fatalf("normal drain should fail at group head b1, ok=%v failID=%d", ok, failID)
	}
	// 失败后补发协程会置 poisonBatchID=b1、narrowDrain=true;模拟之
	ch.poisonBatchID = 1
	ch.narrowDrain = true

	// 2) 窄化:逐批试写 —— b1、b2 写成功删除,到 b3 失败,failID 应定位到 b3 而非 b1
	pend, _ = sp.FetchOldest(16)
	if ok, failID := ch.drainWindow(pend, true); ok || failID != 3 {
		t.Fatalf("narrow drain should isolate bad batch b3, ok=%v failID=%d", ok, failID)
	}
	if n, _ := sp.Count(); n != 2 {
		t.Fatalf("after narrow, spool should hold b3+b4, got count=%d", n)
	}
	// b1/b2 已写成功删除;b3 仍保留(毒批次判定在上层),b4 未被连坐
	if _, exists := sp.batches[3]; !exists {
		t.Fatal("b3 should remain in spool for poison decision at loop level")
	}
	if _, exists := sp.batches[4]; !exists {
		t.Fatal("b4 (good) must not be deleted by collateral poison")
	}

	// 3) 再次窄化重试 b3:仍失败 → failID 仍是 b3,与 poisonBatchID 一致可判毒
	pend, _ = sp.FetchOldest(16)
	if ok, failID := ch.drainWindow(pend, true); ok || failID != 3 {
		t.Fatalf("b3 should fail again with failID=3, ok=%v failID=%d", ok, failID)
	}

	// 4) 移除坏批次 b3 后,窄化续取 b4 可全部写成功
	sp.Delete(3)
	pend, _ = sp.FetchOldest(16)
	if ok, _ := ch.drainWindow(pend, true); !ok {
		t.Fatal("after removing bad batch, narrow drain should fully succeed")
	}
	if n, _ := sp.Count(); n != 0 {
		t.Fatalf("spool should be drained, got count=%d", n)
	}
}

func TestDrainWindowNarrowAllGood(t *testing.T) {
	// 全部好批次:窄化模式也能逐批写成功并复位
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL, "t", "m")
	ch := testChannel(cfg)
	sp := newFakeSpool()
	ch.spool = sp
	ch.connected.Store(true)

	for _, sb := range []push.SpoolBatch{
		spoolBatch(1, "a1"),
		spoolBatch(2, "a2"),
	} {
		if err := sp.Insert(push.PushBatch{DeviceID: sb.DeviceID, CollectedAt: sb.CollectedAt, Records: sb.Records}); err != nil {
			t.Fatal(err)
		}
	}

	pend, _ := sp.FetchOldest(16)
	if ok, _ := ch.drainWindow(pend, true); !ok {
		t.Fatal("narrow drain should succeed for all-good batches")
	}
	if n, _ := sp.Count(); n != 0 {
		t.Fatalf("spool should be empty after drain, got %d", n)
	}
}
