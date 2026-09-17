// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
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

// ==================== 断网本地缓存配置 ====================
// 队列/缓存/补发本身的语义由 push 包统一实现与覆盖（见 push/outbox_test.go），
// 此处只覆盖 mqtt 侧的配置解析。

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

// ==================== 断网缓存补发（drainWindow） ====================
// 队列/落盘/唤醒的语义由 push.Outbox 统一覆盖（见 push/outbox_test.go），
// 此处只覆盖 mqtt 特有的「逐批发布 + 成功批次合并删除 + failID 定位」。

// newTestChannelWithSpool 构造带 SQLite 断网缓存的通道（不 Start，仅备好缓存）。
func newTestChannelWithSpool(t *testing.T, cfg *mqttConfig) (*mqttChannel, *push.SqliteSpool) {
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

	ch := &mqttChannel{id: "c1", name: "mqtt", cfg: cfg}
	ch.ob = push.NewOutbox(push.OutboxConfig{
		Tag:          "mqtt",
		ID:           "c1",
		Name:         "mqtt",
		DB:           db,
		SpoolEnabled: true,
		SpoolCap:     100,
		Conn:         ch,
	})
	spool, ok := ch.ob.Spool().(*push.SqliteSpool)
	if !ok {
		t.Fatalf("spool type = %T, want *push.SqliteSpool", ch.ob.Spool())
	}
	return ch, spool
}

// stubToken 立即完成、带预设错误的 paho Token。
type stubToken struct{ err error }

func (t stubToken) Wait() bool                     { return true }
func (t stubToken) WaitTimeout(time.Duration) bool { return true }
func (t stubToken) Done() <-chan struct{}          { return closedCh }
func (t stubToken) Error() error                   { return t.err }

var closedCh = func() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// stubClient 只实现发布路径用到的 paho Client 子集：命中 failOn 主题即失败，
// 其余主题记录为成功发布。
type stubClient struct {
	connected bool
	failOn    string // 命中该主题即返回错误；空串表示全部成功
	mu        sync.Mutex
	published []string
}

func (c *stubClient) IsConnected() bool                    { return c.connected }
func (c *stubClient) IsConnectionOpen() bool               { return c.connected }
func (c *stubClient) Connect() mqtt.Token                  { return stubToken{} }
func (c *stubClient) Disconnect(quiesce uint)              {}
func (c *stubClient) AddRoute(string, mqtt.MessageHandler) {}
func (c *stubClient) OptionsReader() mqtt.ClientOptionsReader {
	return mqtt.NewOptionsReader(mqtt.NewClientOptions())
}

func (c *stubClient) Publish(topic string, _ byte, _ bool, _ interface{}) mqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	if topic == c.failOn {
		return stubToken{err: errors.New("rejected")}
	}
	c.published = append(c.published, topic)
	return stubToken{}
}

func (c *stubClient) Subscribe(string, byte, mqtt.MessageHandler) mqtt.Token { return stubToken{} }
func (c *stubClient) SubscribeMultiple(map[string]byte, mqtt.MessageHandler) mqtt.Token {
	return stubToken{}
}
func (c *stubClient) Unsubscribe(...string) mqtt.Token { return stubToken{} }

func TestDrainWindowDeletesOnlyPublishedBatches(t *testing.T) {
	// 逐批发布：成功的批次必须删除，首个失败的批次及其后保留待重连重试，
	// 且 failID 必须精确定位到失败批次（而非组头），否则上层会误判毒批次。
	ch, spool := newTestChannelWithSpool(t, &mqttConfig{Topic: "/t/+/read"})

	for _, dev := range []string{"d1", "d2", "d3"} {
		if err := spool.Insert(push.PushBatch{DeviceID: dev, CollectedAt: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	pend, err := spool.FetchOldest(16)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	client := &stubClient{connected: true, failOn: "/t/d2/read"}
	ok, failID := ch.drainWindow(client, pend)
	if ok {
		t.Fatal("drainWindow should report failure when one batch is rejected")
	}
	if failID != 2 {
		t.Fatalf("failID = %d, want 2 (首个失败批次)", failID)
	}

	n, err := spool.Count()
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("spool count = %d, want 2 (d1 已删，d2/d3 保留)", n)
	}
}

func TestDrainWindowAllSuccess(t *testing.T) {
	ch, spool := newTestChannelWithSpool(t, &mqttConfig{Topic: "/t/+/read"})

	for _, dev := range []string{"d1", "d2"} {
		if err := spool.Insert(push.PushBatch{DeviceID: dev, CollectedAt: "t"}); err != nil {
			t.Fatal(err)
		}
	}
	pend, err := spool.FetchOldest(16)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	client := &stubClient{connected: true}
	if ok, failID := ch.drainWindow(client, pend); !ok || failID != 0 {
		t.Fatalf("drainWindow = (%v, %d), want (true, 0)", ok, failID)
	}

	n, err := spool.Count()
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("spool count = %d, want 0", n)
	}
}
