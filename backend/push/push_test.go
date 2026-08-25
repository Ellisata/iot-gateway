package push

import (
	"fmt"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
)

// stubChannel 最小 Channel 实现，用于隔离测试 Engine 的分组扇出与热刷新逻辑，
// 不依赖任何真实通道实现（真实 mqtt 通道的构建由 push/mqtt 包测试覆盖）。
type stubChannel struct {
	id      string
	running bool
	batches []PushBatch
	mu      sync.Mutex
}

func (s *stubChannel) ChannelID() string { return s.id }
func (s *stubChannel) ConfigSig() string { return "sig" }
func (s *stubChannel) Start()            { s.running = true }
func (s *stubChannel) Stop()             { s.running = false }
func (s *stubChannel) Enqueue(b PushBatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, b)
}
func (s *stubChannel) Snapshot() ChannelStatusVO {
	return ChannelStatusVO{ID: s.id, Running: s.running}
}
func (s *stubChannel) TestConnectivity() error { return nil }

func (s *stubChannel) drain() []PushBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	batches := append([]PushBatch(nil), s.batches...)
	s.batches = s.batches[:0]
	return batches
}

// ==================== engine.go ====================

func TestPushRecordsFanOut(t *testing.T) {
	ch1 := &stubChannel{id: "c1"}
	ch2 := &stubChannel{id: "c2"}
	e := NewEngine(nil)
	e.mu.Lock()
	e.channels["c1"] = ch1
	e.channels["c2"] = ch2
	e.mu.Unlock()

	records := []collector.CollectedRecord{
		{DeviceID: "d1", DeviceAddressID: "a1", Value: "1", Quality: 192},
		{DeviceID: "d1", DeviceAddressID: "a2", Value: "2", Quality: 192},
		{DeviceID: "d2", DeviceAddressID: "b1", Value: "3", Quality: 192},
	}
	e.PushRecords(records)

	for name, ch := range map[string]*stubChannel{"ch1": ch1, "ch2": ch2} {
		batches := ch.drain()
		if len(batches) != 2 {
			t.Fatalf("[%s] got %d batches, want 2", name, len(batches))
		}
		if batches[0].DeviceID != "d1" || batches[1].DeviceID != "d2" {
			t.Errorf("[%s] batch order = %s,%s, want d1,d2", name, batches[0].DeviceID, batches[1].DeviceID)
		}
		if len(batches[0].Records) != 2 {
			t.Errorf("[%s] d1 points = %d, want 2", name, len(batches[0].Records))
		}
	}
}

func TestPushRecordsChunksLargeDevice(t *testing.T) {
	ch := &stubChannel{id: "c1"}
	e := NewEngine(nil)
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	n := maxRecordsPerBatch*3 + 17
	records := make([]collector.CollectedRecord, n)
	for i := range records {
		records[i] = collector.CollectedRecord{
			DeviceID:        "d1",
			DeviceAddressID: fmt.Sprintf("a%d", i),
			Value:           "1",
			Quality:         192,
		}
	}
	e.PushRecords(records)

	batches := ch.drain()
	wantBatches := (n + maxRecordsPerBatch - 1) / maxRecordsPerBatch
	if len(batches) != wantBatches {
		t.Fatalf("got %d batches, want %d", len(batches), wantBatches)
	}
	// 每批 ≤ maxRecordsPerBatch，且记录顺序完整
	total := 0
	for i, b := range batches {
		if b.DeviceID != "d1" {
			t.Errorf("batch %d device = %s, want d1", i, b.DeviceID)
		}
		if len(b.Records) > maxRecordsPerBatch {
			t.Errorf("batch %d records = %d, want <= %d", i, len(b.Records), maxRecordsPerBatch)
		}
		for j, r := range b.Records {
			wantID := fmt.Sprintf("a%d", total+j)
			if r.DeviceAddressID != wantID {
				t.Fatalf("batch %d record %d id = %s, want %s (order must be preserved)",
					i, j, r.DeviceAddressID, wantID)
			}
		}
		total += len(b.Records)
	}
	if total != n {
		t.Fatalf("total records = %d, want %d", total, n)
	}
}

func TestPushRecordsKeepsSmallBatch(t *testing.T) {
	ch := &stubChannel{id: "c1"}
	e := NewEngine(nil)
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	records := []collector.CollectedRecord{
		{DeviceID: "d1", DeviceAddressID: "a1", Value: "1", Quality: 192},
		{DeviceID: "d1", DeviceAddressID: "a2", Value: "2", Quality: 192},
	}
	e.PushRecords(records)

	batches := ch.drain()
	if len(batches) != 1 {
		t.Fatalf("got %d batches, want 1 (small device must not split)", len(batches))
	}
	if len(batches[0].Records) != 2 {
		t.Fatalf("records = %d, want 2", len(batches[0].Records))
	}
}

func TestChannelConnectivityOrchestration(t *testing.T) {
	// 注册可校验配置的测试工厂：空配置视为非法
	Register("testconn", func(_ *gorm.DB, ch *po.PushChannel) (Channel, error) {
		if ch.ConfigJSON == "" {
			return nil, fmt.Errorf("testconn: config required")
		}
		return &stubChannel{id: ch.ID}, nil
	})

	// 未注册的通道类型
	if err := TestChannelConnectivity(nil, "unknown", `{}`); err == nil {
		t.Error("unknown channel type should fail")
	}

	// 工厂校验配置失败 → 错误向上传播
	if err := TestChannelConnectivity(nil, "testconn", ""); err == nil {
		t.Error("invalid config should fail")
	}

	// 合法类型 + 合法配置 → 连通成功
	if err := TestChannelConnectivity(nil, "testconn", `{"a":1}`); err != nil {
		t.Errorf("valid channel should connect: %v", err)
	}
}

func TestRefreshKeepsUnchangedChannel(t *testing.T) {
	// 注册一个轻量 stub 工厂，避免测试依赖真实 mqtt 网络连接
	Register("stub", func(_ *gorm.DB, ch *po.PushChannel) (Channel, error) {
		return &stubChannel{id: ch.ID}, nil
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushChannel{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if err := db.Create(&po.PushChannel{ID: "c1", Name: "stub", ConfigJSON: `{}`, Status: 1}).Error; err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	e := NewEngine(db)
	e.refresh()

	e.mu.RLock()
	first := e.channels["c1"]
	e.mu.RUnlock()
	if first == nil {
		t.Fatal("channel not loaded after first refresh")
	}
	if !first.Snapshot().Running {
		t.Fatal("channel should be running after first refresh")
	}

	// 配置未变化再次刷新：必须保留同一个运行实例，而非替换为未启动的新实例
	e.refresh()

	e.mu.RLock()
	second := e.channels["c1"]
	e.mu.RUnlock()
	if second != first {
		t.Fatal("unchanged channel should keep the running instance")
	}
	if !second.Snapshot().Running {
		t.Fatal("retained channel should still be running")
	}

	e.Stop()
}

func TestLoadChecksum(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1) // :memory: 单连接
	if err := db.AutoMigrate(&po.PushChannel{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	e := NewEngine(db)
	empty := e.loadChecksum()

	// 新增通道 → 校验和变化
	if err := db.Create(&po.PushChannel{ID: "c1", Name: "mqtt", ConfigJSON: `{"broker":"h"}`, Status: 1}).Error; err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	afterInsert := e.loadChecksum()
	if afterInsert == empty {
		t.Error("checksum should change after insert")
	}

	// 修改配置 → 校验和变化
	if err := db.Model(&po.PushChannel{}).Where("id = ?", "c1").Update("config_json", `{"broker":"h2"}`).Error; err != nil {
		t.Fatalf("update failed: %v", err)
	}
	afterUpdate := e.loadChecksum()
	if afterUpdate == afterInsert {
		t.Error("checksum should change after config update")
	}

	// 停用 → 校验和变化
	if err := db.Model(&po.PushChannel{}).Where("id = ?", "c1").Update("status", 0).Error; err != nil {
		t.Fatalf("deactivate failed: %v", err)
	}
	afterDeactivate := e.loadChecksum()
	if afterDeactivate == afterUpdate {
		t.Error("checksum should change after deactivate")
	}
}
