// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package triggerrule

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

// ---------------- fakes ----------------

type fakeReader struct {
	mu      sync.Mutex
	calls   int
	records []collector.CollectedRecord
	err     error
	block   chan struct{}
}

func (r *fakeReader) ReadRecords(ctx context.Context, deviceID string, addrs []po.DeviceAddress) ([]collector.CollectedRecord, error) {
	r.mu.Lock()
	r.calls++
	block := r.block
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	if r.err != nil {
		return nil, r.err
	}
	out := make([]collector.CollectedRecord, 0, len(r.records))
	out = append(out, r.records...)
	return out, nil
}

func (r *fakeReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakePusher struct {
	mu    sync.Mutex
	calls int
	ok    bool
	err   error
	recs  []collector.CollectedRecord
}

func (p *fakePusher) PushRecordsToChannel(channelID string, records []collector.CollectedRecord) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.recs = append(p.recs, records...)
	return p.ok, p.err
}

func (p *fakePusher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// blockingReader 按设备阻塞，用于验证慢设备不会耗尽全局执行能力。
type blockingReader struct {
	mu      sync.Mutex
	calls   map[string]int
	blocks  map[string]chan struct{}
	records map[string][]collector.CollectedRecord
}

func (r *blockingReader) ReadRecords(ctx context.Context, deviceID string,
	_ []po.DeviceAddress) ([]collector.CollectedRecord, error) {
	r.mu.Lock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[deviceID]++
	ch := r.blocks[deviceID]
	recs := r.records[deviceID]
	r.mu.Unlock()

	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	out := make([]collector.CollectedRecord, len(recs))
	copy(out, recs)
	return out, nil
}

// ---------------- fixtures ----------------

func newTriggerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "trigger_rule_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.TriggerRule{}, &po.TriggerRuleRecord{},
		&po.Device{}, &po.DeviceAddress{}, &po.CollectorGroup{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := db.Create(&po.Device{ID: "dev-1", Name: "dev1", ProtocolID: "p1",
		ProtocolJSON: "{}", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range []po.DeviceAddress{
		{ID: "a1", DeviceID: "dev-1", Name: "40001", Label: "温度", DataType: "float",
			CommonDataType: "Float", RwPermission: "R", ScanFrequency: 1000, Status: 1},
		{ID: "a2", DeviceID: "dev-1", Name: "40002", Label: "压力", DataType: "float",
			CommonDataType: "Float", RwPermission: "R", ScanFrequency: 1000, Status: 1},
	} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedRule(t *testing.T, db *gorm.DB, cooldownMs int) {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	rule := &po.TriggerRule{
		ID:               "r1",
		Code:             "rule-1",
		Name:             "rule-1",
		Status:           1,
		ActionDeviceID:   "dev-1",
		ActionAddressIDs: `["a2"]`,
		TargetChannelID:  "c1",
		ConditionsJSON:   `{"groups":[{"conditions":[{"addressId":"a1","op":"gt","values":["10"]}]}]}`,
		CooldownMs:       cooldownMs,
		TimeoutMs:        2000,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := db.Create(rule).Error; err != nil {
		t.Fatal(err)
	}
}

func startEngine(t *testing.T, db *gorm.DB, reader PointReader, pusher *fakePusher) *Engine {
	t.Helper()
	e := NewEngine(db, pusher)
	e.BindCollector(reader)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(e.Stop)
	return e
}

func sampleRecord(value string) []collector.CollectedRecord {
	return []collector.CollectedRecord{{
		DeviceID: "dev-1", DeviceAddressID: "a1", Value: value,
		Kind: driver.KindFloat, Quality: driver.QualityGood,
	}}
}

func waitRecords(t *testing.T, db *gorm.DB, want int) []po.TriggerRuleRecord {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var rows []po.TriggerRuleRecord
		if err := db.Order("created_at ASC").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) >= want {
			return rows
		}
		time.Sleep(10 * time.Millisecond)
	}
	var rows []po.TriggerRuleRecord
	_ = db.Find(&rows).Error
	t.Fatalf("等待 %d 条触发记录超时，实际 %d 条", want, len(rows))
	return nil
}

// fire 让条件走一次 false → true 上升沿。
func fire(e *Engine) {
	e.PushRecords(sampleRecord("20")) // 预热
	e.PushRecords(sampleRecord("5"))
	e.PushRecords(sampleRecord("20"))
}

// ---------------- tests ----------------

func TestEngineSuccessPath(t *testing.T) {
	db := newTriggerTestDB(t)
	seedRule(t, db, 0)
	reader := &fakeReader{records: []collector.CollectedRecord{{
		DeviceID: "dev-1", DeviceAddressID: "a2", Value: "88",
		Kind: driver.KindFloat, Quality: driver.QualityGood,
	}}}
	pusher := &fakePusher{ok: true}
	e := startEngine(t, db, reader, pusher)

	fire(e)
	rows := waitRecords(t, db, 1)
	if rows[0].Status != StatusSuccess {
		t.Fatalf("status = %s，error=%s", rows[0].Status, rows[0].Error)
	}
	if rows[0].TriggerID == "" || rows[0].RuleName != "rule-1" || rows[0].ConditionJSON == "" {
		t.Errorf("记录快照不完整：%+v", rows[0])
	}
	if pusher.callCount() != 1 {
		t.Errorf("push 调用 %d 次，期望 1", pusher.callCount())
	}
	if reader.callCount() != 1 {
		t.Errorf("read 调用 %d 次，期望 1", reader.callCount())
	}
	if len(pusher.recs) == 0 || pusher.recs[0].Trigger == nil {
		t.Fatal("动作记录缺少触发元数据")
	}
	tr := pusher.recs[0].Trigger
	if tr.Source != "trigger" || tr.RuleID != "r1" || tr.RuleCode != "rule-1" ||
		tr.RuleName != "rule-1" || tr.TriggerID == "" || tr.FiredAt == "" {
		t.Fatalf("触发元数据 = %+v", tr)
	}
}

func TestEngineClassifiesFailures(t *testing.T) {
	tests := []struct {
		name   string
		reader *fakeReader
		pusher *fakePusher
		want   string
	}{
		{"设备忙", &fakeReader{err: fmt.Errorf("%w: %w", collector.ErrDeviceBusy, context.DeadlineExceeded)},
			&fakePusher{ok: true}, StatusTimeout},
		{"离线", &fakeReader{err: collector.ErrDeviceOffline}, &fakePusher{ok: true}, StatusOffline},
		{"读失败", &fakeReader{err: errors.New("boom")}, &fakePusher{ok: true}, StatusReadFailed},
		{"空结果", &fakeReader{}, &fakePusher{ok: true}, StatusReadFailed},
		{"通道不存在", &fakeReader{records: []collector.CollectedRecord{{DeviceID: "dev-1", DeviceAddressID: "a2"}}},
			&fakePusher{err: push.ErrChannelNotFound}, StatusDropped},
		{"推送失败", &fakeReader{records: []collector.CollectedRecord{{DeviceID: "dev-1", DeviceAddressID: "a2"}}},
			&fakePusher{err: push.ErrPushNotRunning}, StatusPushFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTriggerTestDB(t)
			seedRule(t, db, 0)
			e := startEngine(t, db, tt.reader, tt.pusher)
			fire(e)
			rows := waitRecords(t, db, 1)
			if rows[0].Status != tt.want {
				t.Fatalf("status = %s，期望 %s（error=%s）", rows[0].Status, tt.want, rows[0].Error)
			}
		})
	}
}

func TestEngineReadFinishingAfterTimeoutIsNotPushed(t *testing.T) {
	db := newTriggerTestDB(t)
	seedRule(t, db, 0)
	if err := db.Model(&po.TriggerRule{}).Where("id = ?", "r1").
		Update("timeout_ms", 100).Error; err != nil {
		t.Fatal(err)
	}

	block := make(chan struct{})
	reader := &fakeReader{
		block: block,
		records: []collector.CollectedRecord{{
			DeviceID: "dev-1", DeviceAddressID: "a2", Value: "1",
		}},
	}
	pusher := &fakePusher{ok: true}
	e := startEngine(t, db, reader, pusher)

	fire(e)
	// 驱动 Read 不可被 context 中断：先等规则超时，再放行读取。
	time.Sleep(200 * time.Millisecond)
	close(block)

	rows := waitRecords(t, db, 1)
	if rows[0].Status != StatusTimeout {
		t.Fatalf("status = %s，期望 %s（error=%s）", rows[0].Status, StatusTimeout, rows[0].Error)
	}
	if pusher.callCount() != 0 {
		t.Fatalf("超时后不应再推送，push 调用 %d 次", pusher.callCount())
	}
}

// 两台规则挤在同一台慢设备上时，不能让它们吃掉全部全局 worker；
// 其他设备的规则必须照常执行。
func TestEngineSlowDeviceDoesNotStarveOtherDevices(t *testing.T) {
	db := newTriggerTestDB(t)
	if err := db.Create(&po.Device{ID: "dev-2", Name: "dev2", ProtocolID: "p1",
		ProtocolJSON: "{}", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.DeviceAddress{ID: "a3", DeviceID: "dev-2", Name: "30001",
		Label: "压力2", DataType: "float", CommonDataType: "Float",
		RwPermission: "R", ScanFrequency: 1000, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	conds := `{"groups":[{"conditions":[{"addressId":"a1","op":"gt","values":["10"]}]}]}`
	for _, r := range []po.TriggerRule{
		{ID: "r1", Code: "slow-1", Name: "slow-1", Status: 1, ActionDeviceID: "dev-1",
			ActionAddressIDs: `["a2"]`, TargetChannelID: "c1", ConditionsJSON: conds,
			TimeoutMs: 5000, CreatedAt: now, UpdatedAt: now},
		{ID: "r1b", Code: "slow-2", Name: "slow-2", Status: 1, ActionDeviceID: "dev-1",
			ActionAddressIDs: `["a2"]`, TargetChannelID: "c1", ConditionsJSON: conds,
			TimeoutMs: 5000, CreatedAt: now, UpdatedAt: now},
		{ID: "r2", Code: "fast", Name: "fast", Status: 1, ActionDeviceID: "dev-2",
			ActionAddressIDs: `["a3"]`, TargetChannelID: "c1", ConditionsJSON: conds,
			TimeoutMs: 5000, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}

	block := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(block) }) }
	defer unblock()

	reader := &blockingReader{
		blocks: map[string]chan struct{}{"dev-1": block},
		records: map[string][]collector.CollectedRecord{
			"dev-1": {{DeviceID: "dev-1", DeviceAddressID: "a2", Value: "1",
				Kind: driver.KindFloat, Quality: driver.QualityGood}},
			"dev-2": {{DeviceID: "dev-2", DeviceAddressID: "a3", Value: "2",
				Kind: driver.KindFloat, Quality: driver.QualityGood}},
		},
	}
	pusher := &fakePusher{ok: true}
	e := startEngine(t, db, reader, pusher)

	fire(e)

	// dev-1 的两条规则都卡住时，dev-2 的规则仍应完成。
	deadline := time.Now().Add(1 * time.Second)
	var rows []po.TriggerRuleRecord
	for time.Now().Before(deadline) {
		if err := db.Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(rows) == 0 {
		t.Fatal("慢设备占用时，其他设备的规则未被调度")
	}
	if rows[0].RuleID != "r2" || rows[0].Status != StatusSuccess {
		t.Fatalf("首条记录 = %s/%s，期望 r2/success", rows[0].RuleID, rows[0].Status)
	}

	unblock()
	waitRecords(t, db, 3)
}

func TestEngineCooldownPreventsSecondTrigger(t *testing.T) {
	db := newTriggerTestDB(t)
	seedRule(t, db, 60000)
	reader := &fakeReader{records: []collector.CollectedRecord{{DeviceID: "dev-1", DeviceAddressID: "a2", Value: "1"}}}
	pusher := &fakePusher{ok: true}
	e := startEngine(t, db, reader, pusher)

	fire(e)
	waitRecords(t, db, 1)
	fire(e)
	time.Sleep(200 * time.Millisecond)

	var count int64
	if err := db.Model(&po.TriggerRuleRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("冷却期内产生了 %d 条记录，期望 1", count)
	}
}

func TestEngineSingleFlightCoalescesPendingEdge(t *testing.T) {
	db := newTriggerTestDB(t)
	seedRule(t, db, 0)
	block := make(chan struct{})
	reader := &fakeReader{
		block:   block,
		records: []collector.CollectedRecord{{DeviceID: "dev-1", DeviceAddressID: "a2", Value: "1"}},
	}
	pusher := &fakePusher{ok: true}
	e := startEngine(t, db, reader, pusher)

	fire(e) // 任务在飞行中阻塞
	deadline := time.Now().Add(2 * time.Second)
	for reader.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// 飞行期间再来一次完整边沿：只置 pending，不并行读。
	e.PushRecords(sampleRecord("5"))
	e.PushRecords(sampleRecord("20"))
	close(block)

	waitRecords(t, db, 2)
	if got := reader.callCount(); got != 2 {
		t.Errorf("读取 %d 次，期望 2（首次 + pending 补发）", got)
	}
}

func TestEnqueueDropsWhenQueueFull(t *testing.T) {
	now := time.Now()
	e := newStateTestEngine(testRule(), now)
	e.jobs = make(chan job) // 无缓冲且无 worker：必然入队失败
	st := e.rules["r1"]
	st.initialized = true
	st.lastMet = false
	e.samples["a1"] = sample{value: "20", kind: driver.KindFloat, quality: driver.QualityGood, at: now}

	e.evaluateRuleLocked("r1", now)
	if len(e.drops) != 1 {
		t.Fatalf("队列满时应投递 dropped 记录，实际 %d", len(e.drops))
	}
	j := <-e.drops
	if j.presetStatus != StatusDropped || j.presetError == "" {
		t.Fatalf("dropped job = %+v", j)
	}
}

func TestApplySnapshotPreservesUnchangedStateAndResetsChanged(t *testing.T) {
	rule := testRule()
	rule.sig = 1
	st := &ruleState{rule: rule, initialized: true, lastMet: true}
	e := &Engine{
		rules:         map[string]*ruleState{"r1": st},
		samples:       map[string]sample{"a1": {}, "gone": {}},
		ruleByAddress: map[string][]string{},
		conditionIDs:  map[string]struct{}{},
	}

	// 签名不变：保留状态。
	same := testRule()
	same.sig = 1
	e.applySnapshotLocked(&ruleSnapshot{rules: []*compiledRule{same}})
	if e.rules["r1"] != st || !st.initialized || !st.lastMet {
		t.Fatal("签名未变的规则应保留边沿状态")
	}

	// 签名变化：重置为预热。
	changed := testRule()
	changed.sig = 2
	e.applySnapshotLocked(&ruleSnapshot{rules: []*compiledRule{changed}})
	if e.rules["r1"] == st || e.rules["r1"].initialized || e.rules["r1"].lastMet {
		t.Fatal("签名变化的规则应重置状态")
	}
	if _, ok := e.samples["gone"]; ok {
		t.Fatal("不再被引用的样本应被清理")
	}
	if _, ok := e.samples["a1"]; !ok {
		t.Fatal("仍被引用的样本不应被清理")
	}
}

func TestEngineRecordsRecentTrigger(t *testing.T) {
	db := newTriggerTestDB(t)
	seedRule(t, db, 0)
	reader := &fakeReader{records: []collector.CollectedRecord{{
		DeviceID: "dev-1", DeviceAddressID: "a2", DeviceAddressName: "40002",
		Value: "88", Kind: driver.KindFloat, Quality: driver.QualityGood,
	}}}
	e := startEngine(t, db, reader, &fakePusher{ok: true})

	fire(e)
	waitRecords(t, db, 1)

	var events []RecentTrigger
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events = e.RecentTriggers("", 0)
		if len(events) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(events) != 1 {
		t.Fatalf("recent = %d，期望 1", len(events))
	}
	ev := events[0]
	if ev.Status != StatusSuccess || ev.RuleID != "r1" || ev.RuleName != "rule-1" {
		t.Fatalf("事件 = %+v", ev)
	}
	if ev.ActionDeviceID != "dev-1" || ev.ChannelID != "c1" {
		t.Fatalf("设备/通道 = %q/%q", ev.ActionDeviceID, ev.ChannelID)
	}
	if len(ev.Records) != 1 || ev.Records[0].Value != "88" || ev.Records[0].AddressName != "40002" {
		t.Fatalf("动作点位 = %+v", ev.Records)
	}
	if got := e.RecentTriggers("r1", 0); len(got) != 1 {
		t.Fatalf("按规则过滤 = %d，期望 1", len(got))
	}
	if got := e.RecentTriggers("missing", 0); len(got) != 0 {
		t.Fatalf("未知规则应返回空，实际 %d", len(got))
	}
}
