// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

// ==================== mqttConfig.go ====================

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(`{"broker":"172.22.5.23","port":"1888","clientId":"iot-gateway","qos":1,"topic":"/IIoT/+/+/read,wer","username":"admin","password":"admin"}`)
	if err != nil {
		t.Fatalf("parse valid config failed: %v", err)
	}
	if cfg.Broker != "172.22.5.23" {
		t.Errorf("broker = %q, want 172.22.5.23", cfg.Broker)
	}
	if string(cfg.Port) != "1888" {
		t.Errorf("port = %q, want 1888", cfg.Port)
	}
	if cfg.ClientID != "iot-gateway" {
		t.Errorf("clientId = %q, want iot-gateway", cfg.ClientID)
	}
	if cfg.QoS != 1 {
		t.Errorf("qos = %d, want 1", cfg.QoS)
	}
	if cfg.Topic != "/IIoT/+/+/read,wer" {
		t.Errorf("topic = %q", cfg.Topic)
	}
	if cfg.Username != "admin" || cfg.Password != "admin" {
		t.Errorf("auth = %q/%q, want admin/admin", cfg.Username, cfg.Password)
	}
}

func TestParseConfigNumericPort(t *testing.T) {
	cfg, err := parseConfig(`{"broker":"h","port":1888,"topic":"t"}`)
	if err != nil {
		t.Fatalf("parse numeric port failed: %v", err)
	}
	if string(cfg.Port) != "1888" {
		t.Errorf("port = %q, want 1888", cfg.Port)
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"missing broker", `{"port":"1883","topic":"t"}`},
		{"invalid port", `{"broker":"h","port":"abc","topic":"t"}`},
		{"qos out of range", `{"broker":"h","topic":"t","qos":3}`},
		{"missing topic", `{"broker":"h"}`},
		{"bad json", `not-json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parseConfig(c.json); err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}

func TestPublishWorkers(t *testing.T) {
	cases := []struct {
		name   string
		config int // 配置的 publishWorkers 值（0 表示缺省/未配置）
		want   int
	}{
		{"default when absent", 0, defaultPublishWorkers},
		{"default when negative", -3, defaultPublishWorkers},
		{"explicit", 8, 8},
		{"single worker", 1, 1},
		{"clamp to max", 1000, maxPublishWorkers},
		{"exactly max", maxPublishWorkers, maxPublishWorkers},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := mqttConfig{PublishWorkers: c.config}
			if got := cfg.publishWorkers(); got != c.want {
				t.Errorf("publishWorkers(%d) = %d, want %d", c.config, got, c.want)
			}
		})
	}
}

func TestParseConfigPublishWorkers(t *testing.T) {
	cfg, err := parseConfig(`{"broker":"h","topic":"t","publishWorkers":8}`)
	if err != nil {
		t.Fatalf("parse config with publishWorkers failed: %v", err)
	}
	if cfg.PublishWorkers != 8 {
		t.Errorf("PublishWorkers = %d, want 8", cfg.PublishWorkers)
	}

	// 缺省时不配置该字段，解析为 0，运行期回落默认值
	cfg, err = parseConfig(`{"broker":"h","topic":"t"}`)
	if err != nil {
		t.Fatalf("parse config without publishWorkers failed: %v", err)
	}
	if cfg.PublishWorkers != 0 {
		t.Errorf("PublishWorkers = %d, want 0 (absent)", cfg.PublishWorkers)
	}
	if got := cfg.publishWorkers(); got != defaultPublishWorkers {
		t.Errorf("publishWorkers() = %d, want default %d", got, defaultPublishWorkers)
	}
}

func TestBrokerURL(t *testing.T) {
	cases := []struct {
		name string
		cfg  mqttConfig
		want string
	}{
		{"host and port", mqttConfig{Broker: "172.22.5.23", Port: "1888"}, "tcp://172.22.5.23:1888"},
		{"default port", mqttConfig{Broker: "127.0.0.1"}, "tcp://127.0.0.1:1883"},
		{"with scheme", mqttConfig{Broker: "ssl://broker.example.com", Port: "8883"}, "ssl://broker.example.com"},
		{"with scheme embedded port", mqttConfig{Broker: "tcp://broker.example.com:1884"}, "tcp://broker.example.com:1884"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.brokerURL(); got != c.want {
				t.Errorf("brokerURL = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildTopics(t *testing.T) {
	// 通配符替换
	got := buildTopics("/IIoT/+/+/read", "dev-1")
	want := []string{"/IIoT/dev-1/dev-1/read"}
	if !equalTopics(got, want) {
		t.Errorf("buildTopics = %v, want %v", got, want)
	}

	// 逗号分隔多主题
	got = buildTopics("/IIoT/+/+/read,wer", "dev-1")
	want = []string{"/IIoT/dev-1/dev-1/read", "wer"}
	if !equalTopics(got, want) {
		t.Errorf("buildTopics = %v, want %v", got, want)
	}

	// 空白过滤
	got = buildTopics("  a, ,b  ,c,", "d")
	want = []string{"a", "b", "c"}
	if !equalTopics(got, want) {
		t.Errorf("buildTopics = %v, want %v", got, want)
	}
}

func equalTopics(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ==================== marshal.go ====================

func TestMarshalBatch(t *testing.T) {
	records := []collector.CollectedRecord{
		{DeviceID: "d1", DeviceName: "路由器", DeviceAddressID: "a1", DeviceAddressName: "410001", Value: "1827", DataType: "Word", Quality: 192},
		{DeviceID: "d1", DeviceName: "路由器", DeviceAddressID: "a2", DeviceAddressName: "410002", Value: "0", DataType: "Bool", Quality: 192},
	}
	payload, err := marshalBatch(records, "2026-07-31 12:00:00")
	if err != nil {
		t.Fatalf("marshalBatch failed: %v", err)
	}

	var points []pushPoint
	if err := json.Unmarshal(payload, &points); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("len(points) = %d, want 2", len(points))
	}
	if points[0].DeviceID != "d1" || points[0].DeviceAddressName != "410001" || points[0].Value != "1827" || points[0].Quality != 192 {
		t.Errorf("point[0] = %+v", points[0])
	}
	if points[0].CollectedAt != "2026-07-31 12:00:00" {
		t.Errorf("collectedAt = %q", points[0].CollectedAt)
	}
}

// ==================== mqttChannel.go ====================

func TestEnqueueDropOldest(t *testing.T) {
	ch := &mqttChannel{id: "c", name: "c", cfg: &mqttConfig{}, outbox: make(chan push.PushBatch, 2)}
	ch.Enqueue(push.PushBatch{DeviceID: "a"})
	ch.Enqueue(push.PushBatch{DeviceID: "b"})

	// outbox 已满，再入队应丢弃最旧 "a"，保留最新
	ch.Enqueue(push.PushBatch{DeviceID: "c"})

	if got := <-ch.outbox; got.DeviceID != "b" {
		t.Errorf("after drop-oldest, first = %s, want b", got.DeviceID)
	}
	if got := <-ch.outbox; got.DeviceID != "c" {
		t.Errorf("after drop-oldest, second = %s, want c", got.DeviceID)
	}
	if ch.droppedCount.Load() != 1 {
		t.Errorf("droppedCount = %d, want 1", ch.droppedCount.Load())
	}
}

// ==================== 注册接入（push.ChannelFactories） ====================

func TestEngineBuildsMqttChannel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushChannel{}, &po.PushOutbox{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if err := db.Create(&po.PushChannel{ID: "c1", Name: "mqtt", ConfigJSON: `{"broker":"127.0.0.1","topic":"/t/+/read"}`, Status: 1}).Error; err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 通过公开 API 验证本包 init() 注册的 mqtt 工厂被 Engine 自动发现并启动
	e := push.NewEngine(db)
	if err := e.Start(); err != nil {
		t.Fatalf("engine start failed: %v", err)
	}
	defer e.Stop()

	statuses := e.GetStatus()
	if len(statuses) != 1 {
		t.Fatalf("got %d channels, want 1", len(statuses))
	}
	if statuses[0].Name != "mqtt" || !statuses[0].Running {
		t.Errorf("status = %+v", statuses[0])
	}
}

// ==================== 断网本地缓存（push_outbox） ====================

// newTestChannelWithSpool 构造带 SQLite 本地缓存的通道（未调用 run，仅启动写协程）
func newTestChannelWithSpool(t *testing.T, cfg *mqttConfig, outboxCap int) (*mqttChannel, *push.SqliteSpool) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushOutbox{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	spool := push.NewSqliteSpool(db, "c1")
	ch := &mqttChannel{
		id:          "c1",
		name:        "mqtt",
		cfg:         cfg,
		spool:       spool,
		outbox:      make(chan push.PushBatch, outboxCap),
		spoolCh:     make(chan push.PushBatch, spoolChSize),
		drainNotify: make(chan struct{}, 1),
	}
	ch.quit = make(chan struct{})
	ch.spoolDone = make(chan struct{})
	go ch.spoolWriteLoop()
	return ch, spool
}

// stopWriteLoop 停止测试通道的写协程（等价于 Stop 的后半段）
func (ch *mqttChannel) stopWriteLoop() {
	if ch.quit == nil {
		return
	}
	close(ch.quit)
	if ch.spoolDone != nil {
		<-ch.spoolDone
	}
}

// waitSpoolCount 轮询等待本地缓存批次数量达到期望值（写协程异步落盘）
func waitSpoolCount(t *testing.T, spool *push.SqliteSpool, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, err := spool.Count(); err == nil && n == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	n, _ := spool.Count()
	t.Fatalf("spool count = %d, want %d", n, want)
}

func TestParseConfigSpool(t *testing.T) {
	// 缺省启用缓存，上限回落默认值
	cfg, err := parseConfig(`{"broker":"h","topic":"t"}`)
	if err != nil {
		t.Fatalf("parse default config failed: %v", err)
	}
	if !cfg.spoolEnabled() {
		t.Errorf("spoolEnabled() = false, want true (default on)")
	}
	if got := cfg.spoolBatchCap(); got != defaultSpoolMaxBatches {
		t.Errorf("spoolBatchCap() = %d, want default %d", got, defaultSpoolMaxBatches)
	}

	// 显式关闭
	cfg, err = parseConfig(`{"broker":"h","topic":"t","spoolDisabled":true}`)
	if err != nil {
		t.Fatalf("parse disabled config failed: %v", err)
	}
	if cfg.spoolEnabled() {
		t.Errorf("spoolEnabled() = true, want false when spoolDisabled")
	}

	// 自定义上限
	cfg, err = parseConfig(`{"broker":"h","topic":"t","spoolMaxBatches":50}`)
	if err != nil {
		t.Fatalf("parse cap config failed: %v", err)
	}
	if got := cfg.spoolBatchCap(); got != 50 {
		t.Errorf("spoolBatchCap() = %d, want 50", got)
	}
}

func TestEnqueueSpoolWhenDisconnected(t *testing.T) {
	ch, spool := newTestChannelWithSpool(t, &mqttConfig{}, 4)
	defer ch.stopWriteLoop()

	// 未连接（connected=false）时入队应直接落盘，不进内存队列
	ch.Enqueue(push.PushBatch{DeviceID: "d1", CollectedAt: "t1"})
	ch.Enqueue(push.PushBatch{DeviceID: "d1", CollectedAt: "t2"})

	waitSpoolCount(t, spool, 2)
	if got := len(ch.outbox); got != 0 {
		t.Errorf("outbox depth = %d, want 0 (disconnected → spool)", got)
	}
}

func TestEnqueueSpillsToSpoolWhenOutboxFull(t *testing.T) {
	ch, spool := newTestChannelWithSpool(t, &mqttConfig{}, 2)
	defer ch.stopWriteLoop()

	ch.connected = true
	ch.Enqueue(push.PushBatch{DeviceID: "a"})
	ch.Enqueue(push.PushBatch{DeviceID: "b"}) // 内存队列已满
	ch.Enqueue(push.PushBatch{DeviceID: "c"}) // 溢写本地缓存而非丢最旧

	waitSpoolCount(t, spool, 1)
	if got := len(ch.outbox); got != 2 {
		t.Errorf("outbox depth = %d, want 2", got)
	}
	if got := ch.droppedCount.Load(); got != 0 {
		t.Errorf("droppedCount = %d, want 0 (spill to spool)", got)
	}
}

func TestEnqueueLegacyDropOldestWhenSpoolDisabled(t *testing.T) {
	// spool 为 nil（未启用）时回退为纯内存丢最旧行为
	ch := &mqttChannel{id: "c", name: "c", cfg: &mqttConfig{SpoolDisabled: true}, outbox: make(chan push.PushBatch, 2)}
	ch.Enqueue(push.PushBatch{DeviceID: "a"})
	ch.Enqueue(push.PushBatch{DeviceID: "b"})
	ch.Enqueue(push.PushBatch{DeviceID: "c"})

	if got := <-ch.outbox; got.DeviceID != "b" {
		t.Errorf("after drop-oldest, first = %s, want b", got.DeviceID)
	}
	if got := <-ch.outbox; got.DeviceID != "c" {
		t.Errorf("after drop-oldest, second = %s, want c", got.DeviceID)
	}
	if ch.droppedCount.Load() != 1 {
		t.Errorf("droppedCount = %d, want 1", ch.droppedCount.Load())
	}
}

func TestSpoolWriteSignalsDrain(t *testing.T) {
	// 落盘（含断连直落）应 ping 补发唤醒，事件驱动补发无需轮询
	ch, _ := newTestChannelWithSpool(t, &mqttConfig{}, 4)
	defer ch.stopWriteLoop()

	ch.Enqueue(push.PushBatch{DeviceID: "d1", CollectedAt: "t1"})

	select {
	case <-ch.drainNotify:
		// 写协程已 ping
	case <-time.After(2 * time.Second):
		t.Fatal("spool write did not signal drain")
	}
}

func TestDrainSignalWakeSemantics(t *testing.T) {
	ch := &mqttChannel{drainNotify: make(chan struct{}, 1)}
	ch.quit = make(chan struct{})

	// ping 立即唤醒等待
	ch.signalDrain()
	if !ch.waitSpoolDrain() {
		t.Fatal("waitSpoolDrain returned false after signal")
	}

	// quit 关闭后立即返回 false（停止信号）
	close(ch.quit)
	if ch.waitSpoolDrain() {
		t.Fatal("waitSpoolDrain returned true after quit")
	}
}

func TestDrainSignalNilChannelNoop(t *testing.T) {
	// spool 未启用（drainNotify 为 nil）时 signalDrain 必须 no-op，不得阻塞
	ch := &mqttChannel{cfg: &mqttConfig{SpoolDisabled: true}}
	ch.signalDrain() // 若阻塞则测试超时
	ch.signalDrain()
}
