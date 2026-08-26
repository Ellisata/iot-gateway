package collector

import (
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// reserveClosedPort 绑定一个临时端口后立即关闭，返回该端口号。
// 之后对它的 TCP 拨号会立即得到 connection refused，保证 modbus Connect 快速失败。
func reserveClosedPort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen temp port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// TestEngineWatchLoopHotReload 端到端验证 watchLoop 定时巡检：
// 设备地址 status 改为 0 后，watcher 应在下一轮巡检检测到 checksum 变化，
// 触发 Refresh 热加载，新任务不再采集该地址。
func TestEngineWatchLoopHotReload(t *testing.T) {
	// 用临时文件库而非 :memory:：内存 SQLite 是每个连接独立一份，
	// 会引入跨连接不可见的问题；文件库与生产一致，所有连接共享同一份数据。
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		// 在 engine.Stop 之后关闭（defer 逆序执行，此 defer 先注册、后执行）
		defer sqlDB.Close()
	}
	for _, m := range []interface{}{&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	// 造数据：1 个协议 + 1 台活跃设备 + 1 个活跃地址
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	port := reserveClosedPort(t)
	dev := po.Device{
		ID:           "dev-1",
		Name:         "dev1",
		ProtocolID:   "proto-1",
		ProtocolJSON: fmt.Sprintf(`{"host":"127.0.0.1","port":"%d","node":"1","timeout":"500"}`, port),
		Status:       1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}
	addr := po.DeviceAddress{
		ID:            "addr-1",
		DeviceID:      "dev-1",
		Name:          "40001",
		DataType:      "int16",
		RwPermission:  "R",
		ScanFrequency: 1000,
		Status:        1,
	}
	if err := db.Create(&addr).Error; err != nil {
		t.Fatal(err)
	}

	pool := workerPool.NewWorkerPool(2, 16)
	engine := NewEngine(db, pool, nil, nil)
	engine.watchInterval = 150 * time.Millisecond // 缩短巡检间隔，让测试快速验证
	if err := engine.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer engine.Stop()

	// 初始应采集 1 个地址
	st := engine.GetStatus()
	if len(st) == 0 || st[0].AddressCount != 1 {
		t.Fatalf("expected 1 address initially, got %+v", st)
	}

	// 等 watcher 建立初始 checksum 基线（约一个 tick），
	// 否则改配置发生在基线建立之前，会被当作"本来就如此"而检测不到。
	time.Sleep(250 * time.Millisecond)

	// 模拟用户将地址 status 改为 0
	if err := db.Model(&po.DeviceAddress{}).Where("id = ?", addr.ID).Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}

	// 等待 watcher 巡检到变化并完成热刷新
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := engine.GetStatus(); len(st) > 0 && st[0].AddressCount == 0 {
			return // 热刷新成功：已停止采集该地址
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("watchLoop did not hot-reload: address still collected after status set to 0")
}

// newWatchTestDB 构建 watchLoop 测试库：临时文件 SQLite（文件库保证跨连接可见，与生产一致）
func newWatchTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	for _, m := range []interface{}{&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

// startWatchTestEngine 启动一个缩短巡检间隔的测试引擎
func startWatchTestEngine(t *testing.T, db *gorm.DB) *Engine {
	pool := workerPool.NewWorkerPool(2, 16)
	engine := NewEngine(db, pool, nil, nil)
	engine.watchInterval = 150 * time.Millisecond // 缩短巡检间隔，让测试快速验证
	if err := engine.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	t.Cleanup(engine.Stop)
	return engine
}

// TestEngineWatchLoopDeviceActivate 端到端验证：设备 status 由 0 改为 1 后，
// watcher 应检测到 checksum 变化并热加载，新任务开始采集该设备的地址。
func TestEngineWatchLoopDeviceActivate(t *testing.T) {
	db := newWatchTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	port := reserveClosedPort(t)
	mkDev := func(id string, status int) *po.Device {
		return &po.Device{
			ID:           id,
			Name:         id,
			ProtocolID:   "proto-1",
			ProtocolJSON: fmt.Sprintf(`{"host":"127.0.0.1","port":"%d","node":"1","timeout":"500"}`, port),
			Status:       status,
		}
	}
	mkAddr := func(id, devID string) *po.DeviceAddress {
		return &po.DeviceAddress{
			ID:            id,
			DeviceID:      devID,
			Name:          id,
			DataType:      "int16",
			RwPermission:  "R",
			ScanFrequency: 1000,
			Status:        1,
		}
	}
	// dev-active 活跃、dev-inactive 停用，地址均为活跃
	if err := db.Create(mkDev("dev-active", 1)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(mkDev("dev-inactive", 1)).Error; err != nil {
		t.Fatal(err)
	}
	// 与生产 UpdateDevice 一致：用 map 更新把状态置 0（struct 零值会被 GORM default 吞掉）
	if err := db.Model(&po.Device{}).Where("id = ?", "dev-inactive").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(mkAddr("addr-active", "dev-active")).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(mkAddr("addr-inactive", "dev-inactive")).Error; err != nil {
		t.Fatal(err)
	}

	engine := startWatchTestEngine(t, db)
	if st := engine.GetStatus(); len(st) == 0 || st[0].AddressCount != 1 {
		t.Fatalf("expected 1 address initially, got %+v", st)
	}

	time.Sleep(250 * time.Millisecond) // 等 watcher 建立初始 checksum 基线

	// 模拟用户将停用设备 status 改为 1（0 -> 1）
	if err := db.Model(&po.Device{}).Where("id = ?", "dev-inactive").Update("status", 1).Error; err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := engine.GetStatus(); len(st) > 0 && st[0].AddressCount == 2 {
			return // 热刷新成功：已开始采集该设备地址
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("watchLoop did not hot-reload: device activated (0->1) but address still not collected")
}

// TestEngineWatchLoopReactivateAfterDeactivateAll 回归测试：唯一的活跃设备先停用（1->0）、再启用（0->1）时，
// 停用阶段 Refresh 曾因「无活跃设备」失败导致 baseline checksum 停滞，重新启用后 checksum 回到旧值，
// watchLoop 检测不到变化、设备无法恢复采集。修复后应能正常热加载。
func TestEngineWatchLoopReactivateAfterDeactivateAll(t *testing.T) {
	db := newWatchTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	port := reserveClosedPort(t)
	mkDev := func(id string, status int) *po.Device {
		return &po.Device{
			ID:           id,
			Name:         id,
			ProtocolID:   "proto-1",
			ProtocolJSON: fmt.Sprintf(`{"host":"127.0.0.1","port":"%d","node":"1","timeout":"500"}`, port),
			Status:       status,
		}
	}
	mkAddr := func(id, devID string) *po.DeviceAddress {
		return &po.DeviceAddress{
			ID:            id,
			DeviceID:      devID,
			Name:          id,
			DataType:      "int16",
			RwPermission:  "R",
			ScanFrequency: 1000,
			Status:        1,
		}
	}
	if err := db.Create(mkDev("dev-a", 1)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(mkDev("dev-b", 1)).Error; err != nil {
		t.Fatal(err)
	}
	// dev-b 初始停用（用 map 更新置 0）
	if err := db.Model(&po.Device{}).Where("id = ?", "dev-b").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(mkAddr("addr-a", "dev-a")).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(mkAddr("addr-b", "dev-b")).Error; err != nil {
		t.Fatal(err)
	}

	engine := startWatchTestEngine(t, db)
	if drv := engine.GetDriver("dev-b"); drv != nil {
		t.Fatal("dev-b should not have a driver initially")
	}

	time.Sleep(250 * time.Millisecond) // 等 watcher 建立初始 checksum 基线

	// 1) 停用唯一的活跃设备 dev-a（1 -> 0）：旧实现 Refresh 会因无活跃设备失败、checksum 停滞
	if err := db.Model(&po.Device{}).Where("id = ?", "dev-a").Update("status", 0).Error; err != nil {
		t.Fatal(err)
	}
	// 2) 启用 dev-b（0 -> 1）：应检测到变化并热加载，dev-b 拥有驱动
	if err := db.Model(&po.Device{}).Where("id = ?", "dev-b").Update("status", 1).Error; err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if drv := engine.GetDriver("dev-b"); drv != nil {
			return // 热刷新成功：dev-b 已被加载
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("watchLoop did not hot-reload: dev-b reactivated (0->1) but not collected (checksum stuck)")
}

// TestEngineActiveDeviceIDs 验证在线统计依赖的采集引擎状态：
// 已调度设备应被 ActiveDeviceIDs 返回；指向已关闭端口、采集必然失败时，
// DeviceLastSuccessTime 应为空（尚无成功记录）。
func TestEngineActiveDeviceIDs(t *testing.T) {
	db := newWatchTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	port := reserveClosedPort(t)
	dev := po.Device{
		ID:           "dev-1",
		Name:         "dev1",
		ProtocolID:   "proto-1",
		ProtocolJSON: fmt.Sprintf(`{"host":"127.0.0.1","port":"%d","node":"1","timeout":"500"}`, port),
		Status:       1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}
	addr := po.DeviceAddress{
		ID:            "addr-1",
		DeviceID:      "dev-1",
		Name:          "40001",
		DataType:      "int16",
		RwPermission:  "R",
		ScanFrequency: 1000,
		Status:        1,
	}
	if err := db.Create(&addr).Error; err != nil {
		t.Fatal(err)
	}

	engine := startWatchTestEngine(t, db)

	// collected 以"已调度"为准：有驱动且至少一个活跃地址即返回，与轮询成败无关
	ids := engine.ActiveDeviceIDs()
	if len(ids) != 1 || ids[0] != "dev-1" {
		t.Fatalf("expected active device ids [dev-1], got %v", ids)
	}

	// 指向已关闭端口：连接必然失败，无成功记录 -> 最近成功采集时间为空
	if got := engine.DeviceLastSuccessTime("dev-1"); got != "" {
		t.Fatalf("expected empty last success time (poll to closed port fails), got %q", got)
	}
	if got := engine.DeviceLastSuccessTime("no-such-device"); got != "" {
		t.Fatalf("expected empty last success time for unknown device, got %q", got)
	}
}
