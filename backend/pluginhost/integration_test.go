// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

// 全链路集成测试：真实插件进程 + 真实采集引擎。
//
// 覆盖的路径：Manager 发现插件 → 注册驱动 → collector.newGatewayTask 建任务
// → doPollDevice → remoteDriver → RPC → 插件进程 → 回传结果 → toRecords
// → RecordSink。没有任何一环是替身（插件进程、RPC、采集引擎都是真的）。

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
	"iot-gateway/configFile"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// recordingSink 收集采集记录供断言。
type recordingSink struct {
	mu      sync.Mutex
	records []collector.CollectedRecord
}

func (s *recordingSink) PushRecords(records []collector.CollectedRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, records...)
}

func (s *recordingSink) snapshot() []collector.CollectedRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]collector.CollectedRecord, len(s.records))
	copy(out, s.records)
	return out
}

type noopStateSink struct{}

func (noopStateSink) ReportDevicePoll(string, string, bool) {}

// TestEndToEndCollectionThroughPlugin 是本方案的总验收：
// 插件协议的设备能被采集引擎正常采集，且记录内容正确。
func TestEndToEndCollectionThroughPlugin(t *testing.T) {
	pluginPath := installFixture(t)

	// ---- 真实数据库 ----
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	for _, m := range []any{&po.IotProtocol{}, &po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	// ---- 真实采集引擎 ----
	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	engine := collector.NewEngine(db, pool, sink, noopStateSink{}, nil, nil)

	// ---- 真实插件管理器 ----
	cfg := &configFile.Config{
		Plugin: configFile.PluginConfig{
			Enabled:            true,
			Dir:                filepath.Dir(pluginPath),
			Glob:               "iot-driver-*",
			ScanIntervalMs:     3600000,
			AllowInsecureDir:   true,
			HandshakeTimeoutMs: 10000,
		},
	}
	m := NewManager(db, cfg, engine)
	m.opts.Env = append(pluginEnv(), envFixture+"=1")

	// 生产时序：管理器先于采集引擎启动（驱动注册必须先于 collector 建任务）。
	if err := m.Start(); err != nil {
		t.Fatalf("manager Start: %v", err)
	}
	t.Cleanup(m.Stop)
	waitForDriverRegistered(t, fixtureProtocol)

	// ---- 造数据：设备 + 三个不同类型的点位 ----
	var proto po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&proto).Error; err != nil {
		t.Fatalf("插件协议行应由 Manager 自动建立: %v", err)
	}

	dev := po.Device{
		Name:         "集成测试设备",
		ProtocolID:   proto.ID,
		ProtocolJSON: `{}`,
		Status:       1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}

	addrs := []po.DeviceAddress{
		{Name: "p1", Label: "点位一", DeviceID: dev.ID, CommonDataType: "Boolean", DataType: "bool", ScanFrequency: 200, Status: 1},
		{Name: "p2", Label: "点位二", DeviceID: dev.ID, CommonDataType: "Short", DataType: "int16", ScanFrequency: 200, Status: 1},
	}
	for i := range addrs {
		if err := db.Create(&addrs[i]).Error; err != nil {
			t.Fatalf("创建地址: %v", err)
		}
	}

	// ---- 启动采集 ----
	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	// ---- 断言收到记录 ----
	waitFor(t, 15*time.Second, "采集到插件协议的记录", func() bool {
		return len(sink.snapshot()) >= len(addrs)
	})

	got := sink.snapshot()
	byAddr := make(map[string]collector.CollectedRecord, len(got))
	for _, r := range got {
		byAddr[r.DeviceAddressID] = r
	}

	for _, addr := range addrs {
		r, ok := byAddr[addr.ID]
		if !ok {
			t.Errorf("地址 %s 未采集到记录", addr.Name)
			continue
		}
		if r.DeviceID != dev.ID {
			t.Errorf("%s: DeviceID = %q, want %q", addr.Name, r.DeviceID, dev.ID)
		}
		if r.Protocol != fixtureProtocol {
			t.Errorf("%s: Protocol = %q, want %q", addr.Name, r.Protocol, fixtureProtocol)
		}
		// 记录里的名称/标签由采集引擎从 addrs[i] 填充，插件不参与。
		if r.DeviceAddressName != addr.Name || r.DeviceAddressLabel != addr.Label {
			t.Errorf("%s: 名称/标签 = %q/%q, want %q/%q",
				addr.Name, r.DeviceAddressName, r.DeviceAddressLabel, addr.Name, addr.Label)
		}
		// 值来自插件进程内的驱动。
		if want := "fixture-" + addr.Name; r.Value != want {
			t.Errorf("%s: Value = %q, want %q", addr.Name, r.Value, want)
		}
		// Kind 由驱动填充（插件里固定为 int）。
		if r.Kind != driver.KindInt {
			t.Errorf("%s: Kind = %q, want %q", addr.Name, r.Kind, driver.KindInt)
		}
		if r.Quality != driver.QualityGood {
			t.Errorf("%s: Quality = %d, want %d", addr.Name, r.Quality, driver.QualityGood)
		}
	}

	// ---- 多轮采集：确认不是只跑了一轮 ----
	before := len(sink.snapshot())
	waitFor(t, 15*time.Second, "持续采集", func() bool {
		return len(sink.snapshot()) > before
	})
}

// TestCollectionContinuesAcrossPluginCrash 插件进程崩溃后采集不应停摆：
// IsConnected() 变 false → 采集引擎按既有退避重连 → 监管拉起新进程 → 恢复采集。
//
// 这条把「插件自愈」与「采集引擎的重连状态机」两部分接在一起验证，
// 是本设计里唯一会导致永久静默丢数据的失败模式。
func TestCollectionContinuesAcrossPluginCrash(t *testing.T) {
	pluginPath := installFixture(t)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	for _, m := range []any{&po.IotProtocol{}, &po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	engine := collector.NewEngine(db, pool, sink, noopStateSink{}, nil, nil)

	cfg := &configFile.Config{
		Plugin: configFile.PluginConfig{
			Enabled:            true,
			Dir:                filepath.Dir(pluginPath),
			Glob:               "iot-driver-*",
			ScanIntervalMs:     3600000,
			AllowInsecureDir:   true,
			HandshakeTimeoutMs: 10000,
		},
	}
	m := NewManager(db, cfg, engine)
	m.opts.Env = append(pluginEnv(), envFixture+"=1")
	// 缩短重启退避，让崩溃恢复在测试时限内完成。
	m.opts.RestartMin = 10 * time.Millisecond
	m.opts.RestartMax = 50 * time.Millisecond

	if err := m.Start(); err != nil {
		t.Fatalf("manager Start: %v", err)
	}
	t.Cleanup(m.Stop)
	waitForDriverRegistered(t, fixtureProtocol)

	var proto po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&proto).Error; err != nil {
		t.Fatalf("查询协议行: %v", err)
	}

	// 设备配置里带 crash 模式：第一次 Read 会让插件进程直接退出。
	dev := po.Device{Name: "崩溃自愈设备", ProtocolID: proto.ID, ProtocolJSON: `{"mode":"crash"}`, Status: 1}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}
	addr := po.DeviceAddress{
		Name: "p1", Label: "点位一", DeviceID: dev.ID,
		CommonDataType: "Short", DataType: "int16", ScanFrequency: 200, Status: 1,
	}
	if err := db.Create(&addr).Error; err != nil {
		t.Fatalf("创建地址: %v", err)
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	// 第一轮会把插件进程打崩。
	waitFor(t, 15*time.Second, "插件进程崩溃", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, lp := range m.plugins {
			if lp.link.epoch() > 1 {
				return true
			}
		}
		return false
	})
	// 进程已被监管重新拉起（世代号递增）。
	waitFor(t, 15*time.Second, "插件进程被重新拉起", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, lp := range m.plugins {
			return lp.link.isUp()
		}
		return false
	})

	// 让 crash 模式失效，使后续 Read 能成功 —— 采集引擎应在重连后恢复出数。
	// （协议 JSON 由设备配置驱动，改库后采集引擎的 10s watcher 会热加载；
	//  这里直接改配置并等待热加载，验证整条链路自主恢复。）
	if err := db.Model(&po.Device{}).Where("id = ?", dev.ID).
		Update("protocol_json", `{}`).Error; err != nil {
		t.Fatalf("更新设备配置: %v", err)
	}

	waitFor(t, 40*time.Second, "崩溃恢复后重新采到数据", func() bool {
		for _, r := range sink.snapshot() {
			if r.DeviceAddressID == addr.ID && r.Value == "fixture-p1" {
				return true
			}
		}
		return false
	})
}

// countingReloader 包住真实采集引擎并统计 Refresh 次数。
//
// 只有用真实引擎时才需要这层计数：热升级的核心卖点就是**不触发全局 Refresh**
// （真实 Refresh 会停掉整个采集任务、重开内置串口）。fakeReloader 只能证明
// Manager 没调它；这里证明的是真实引擎确实没被驱动集变化牵动。
type countingReloader struct {
	inner *collector.Engine
	calls atomic.Int64
}

func (c *countingReloader) Refresh() error  { c.calls.Add(1); return c.inner.Refresh() }
func (c *countingReloader) IsRunning() bool { return c.inner.IsRunning() }

// TestCollectionContinuesAcrossPluginHotUpgrade 热升级的全链路验收。
//
// 与 TestCollectionContinuesAcrossPluginCrash 覆盖的失败模式相邻但不同：
// 崩溃那条验证「进程没了能自愈」，这条验证「进程被换掉了，采集任务不重建
// 也能自己转到新进程上」—— 后者正是热升级相对重启网关的全部价值。
//
// 同时断言真实采集引擎**没有被 Refresh**：一旦刷了，全部设备（含内置协议）
// 都会白停一轮，这个能力也就没有意义了。
func TestCollectionContinuesAcrossPluginHotUpgrade(t *testing.T) {
	pluginPath := installFixture(t)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	for _, m := range []any{&po.IotProtocol{}, &po.Device{}, &po.DeviceAddress{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	pool := workerPool.NewWorkerPool(4, 64)
	pool.Start()
	sink := &recordingSink{}
	engine := collector.NewEngine(db, pool, sink, noopStateSink{}, nil, nil)
	reloader := &countingReloader{inner: engine}

	cfg := &configFile.Config{
		Plugin: configFile.PluginConfig{
			Enabled:            true,
			Dir:                filepath.Dir(pluginPath),
			Glob:               "iot-driver-*",
			ScanIntervalMs:     3600000,
			AllowInsecureDir:   true,
			HandshakeTimeoutMs: 10000,
		},
	}
	m := NewManager(db, cfg, reloader)
	m.opts.Env = append(pluginEnv(), envFixture+"=1")

	if err := m.Start(); err != nil {
		t.Fatalf("manager Start: %v", err)
	}
	t.Cleanup(m.Stop)
	waitForDriverRegistered(t, fixtureProtocol)

	var proto po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&proto).Error; err != nil {
		t.Fatalf("查询协议行: %v", err)
	}
	dev := po.Device{Name: "热升级设备", ProtocolID: proto.ID, ProtocolJSON: `{}`, Status: 1}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("创建设备: %v", err)
	}
	addrs := []po.DeviceAddress{
		{Name: "p1", Label: "点位一", DeviceID: dev.ID, CommonDataType: "Short", DataType: "int16", ScanFrequency: 200, Status: 1},
		{Name: "p2", Label: "点位二", DeviceID: dev.ID, CommonDataType: "Short", DataType: "int16", ScanFrequency: 200, Status: 1},
	}
	for i := range addrs {
		if err := db.Create(&addrs[i]).Error; err != nil {
			t.Fatalf("创建地址: %v", err)
		}
	}

	if err := engine.Start(); err != nil {
		t.Fatalf("engine Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	// 升级前：两个点位都由旧版本出数。
	waitFor(t, 15*time.Second, "升级前采到旧版本数据", func() bool {
		return hasValue(sink, addrs[0].ID, "fixture-p1") && hasValue(sink, addrs[1].ID, "fixture-p2")
	})

	// ---- 替换插件二进制（过程与现场一致：文件变了 + 新版本行为不同）----
	m.opts.Env = append(pluginEnv(), envFixture+"=1", envFixtureTag+"=v2-")
	replacePluginBinary(t, fixturePluginPath(t, filepath.Dir(pluginPath)))
	m.Reload()

	// 采集任务不重建，但两个点位都应自行切到新版本上出数。
	waitFor(t, 30*time.Second, "升级后采到新版本数据", func() bool {
		return hasValue(sink, addrs[0].ID, "fixture-v2-p1") && hasValue(sink, addrs[1].ID, "fixture-v2-p2")
	})

	if got := m.Status()[0].Upgrades; got != 1 {
		t.Errorf("Upgrades = %d, want 1", got)
	}
	if n := reloader.calls.Load(); n != 0 {
		t.Errorf("热升级触发了 %d 次全局采集刷新，采集任务被白白重建", n)
	}
}

// hasValue 报告某个点位是否已采到指定值。
func hasValue(sink *recordingSink, addrID, value string) bool {
	for _, r := range sink.snapshot() {
		if r.DeviceAddressID == addrID && r.Value == value {
			return true
		}
	}
	return false
}
