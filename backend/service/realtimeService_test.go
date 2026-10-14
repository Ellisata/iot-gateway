// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/driver"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/realtime"
	"iot-gateway/workerPool"
)

// newRealtimeTestDB 构建设备实时数据测试库（临时文件 SQLite）。
func newRealtimeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "realtime_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}, &po.DeviceAddress{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// newRealtimeTestService 构建待测服务。
// 采集引擎刻意不 Start：本组测试只关心「库中点位清单 × 内存快照」的拼装，
// 不启动引擎也就不会有真实轮询干扰断言（ActiveDeviceIDs 为空 → collecting=false）。
func newRealtimeTestService(t *testing.T, db *gorm.DB) (*RealtimeService, *realtime.Store) {
	t.Helper()
	store := realtime.NewStore()
	engine := collector.NewEngine(db, workerPool.NewWorkerPool(2, 16), nil, nil, nil, nil)
	deviceService := NewDeviceService(db, engine, nil)
	return NewRealtimeService(db, store, engine, deviceService), store
}

// seedRealtimeDevice 播种一台设备及其点位。
// createdAt 由调用方指定，便于构造「同一秒创建」的排序边界用例。
func seedRealtimeDevice(t *testing.T, db *gorm.DB, deviceID, protocolID, createdAt string, addrs []po.DeviceAddress) {
	t.Helper()
	dev := po.Device{
		ID:           deviceID,
		Name:         "dev-" + deviceID,
		ProtocolID:   protocolID,
		ProtocolJSON: "{}",
		Status:       1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	for i := range addrs {
		addrs[i].DeviceID = deviceID
		if addrs[i].CreatedAt == "" {
			addrs[i].CreatedAt = createdAt
			addrs[i].UpdatedAt = createdAt
		}
		// 必须在 Create 前取走意图中的 status：po.DeviceAddress.Status 带 gorm default:1，
		// Create 会省略零值字段改用库默认值，并把默认值回写进结构体（Status 从 0 变成 1）。
		// 因此 Create 之后再读 addrs[i].Status 已经看不到"这是个停用点位"了。
		wantStatus := addrs[i].Status
		if err := db.Create(&addrs[i]).Error; err != nil {
			t.Fatalf("create address %s: %v", addrs[i].ID, err)
		}
		if wantStatus != 1 {
			if err := db.Model(&po.DeviceAddress{}).Where("id = ?", addrs[i].ID).
				UpdateColumn("status", wantStatus).Error; err != nil {
				t.Fatalf("disable address %s: %v", addrs[i].ID, err)
			}
		}
	}
}

// addr 造一个启用点位；label 与 name 均可用于检索，故两者都参与断言。
func addr(id, name, label string, status int) po.DeviceAddress {
	return po.DeviceAddress{
		ID:             id,
		Name:           name,
		Label:          label,
		CommonDataType: "Float",
		DataType:       "float",
		RwPermission:   "R",
		ScanFrequency:  1000,
		Status:         status,
	}
}

func req(deviceID string, page, size int, name string) *dto.RealtimeSnapshotDTO {
	return &dto.RealtimeSnapshotDTO{DeviceID: deviceID, Page: page, Size: size, Name: name}
}

const testCreatedAt = "2026-09-22 10:00:00"

// 点位清单以库中 status=1 的点位为准，值从内存快照按点位 ID 对齐；
// 库里已停用的点位不得出现，快照里没有的点位也要占位返回（collected=false）。
func TestRealtimeSnapshotAlignsPointsAndValues(t *testing.T) {
	db := newRealtimeTestDB(t)
	seedRealtimeDevice(t, db, "d1", "p1", testCreatedAt, []po.DeviceAddress{
		addr("a1", "temp", "温度", 1),
		addr("a2", "pressure", "压力", 1),
		addr("a3", "flow", "流量", 1),
		addr("a4", "disabled", "已停用点位", 0),
	})

	svc, store := newRealtimeTestService(t, db)
	// 只给 a1 一个值：a2/a3 模拟「刚启用还没轮询到」
	store.PushRecords([]collector.CollectedRecord{{
		DeviceID: "d1", DeviceAddressID: "a1", DeviceAddressName: "temp",
		DeviceAddressLabel: "温度", Value: "23.5", DataType: "float",
	}})

	got, err := svc.GetDeviceSnapshot(context.Background(), req("d1", 1, 50, ""))
	if err != nil {
		t.Fatalf("GetDeviceSnapshot: %v", err)
	}

	if got.Total != 3 {
		t.Errorf("total = %d, want 3（status=0 的点位不应计入）", got.Total)
	}
	if len(got.Records) != 3 {
		t.Fatalf("records = %d, want 3", len(got.Records))
	}

	byID := map[string]int{}
	for i, r := range got.Records {
		byID[r.AddressID] = i
	}
	if _, ok := byID["a4"]; ok {
		t.Error("已停用点位 a4 不应出现在结果里")
	}

	a1 := got.Records[byID["a1"]]
	if !a1.Collected || a1.Value != "23.5" {
		t.Errorf("a1 = {collected:%v value:%q}, want {true \"23.5\"}", a1.Collected, a1.Value)
	}
	if a1.UpdatedAt == "" {
		t.Error("a1 已采集，updatedAt 不应为空")
	}
	if a1.Stale {
		t.Error("a1 刚写入，不应判为过期")
	}

	a2 := got.Records[byID["a2"]]
	if a2.Collected || a2.Value != "" {
		t.Errorf("a2 未采集应为占位：collected=%v value=%q", a2.Collected, a2.Value)
	}
	if a2.UpdatedAt != "" {
		t.Errorf("a2 未采集，updatedAt 应为空，得到 %q", a2.UpdatedAt)
	}
	// 未采集也要带上点位元信息，否则前端表格无法显示这一行
	if a2.Name != "pressure" || a2.Label != "压力" {
		t.Errorf("a2 元信息缺失：name=%q label=%q", a2.Name, a2.Label)
	}
}

// 名称检索同时命中点位名与说明，且不得跨设备串台。
func TestRealtimeSnapshotNameFilterMatchesNameOrLabel(t *testing.T) {
	db := newRealtimeTestDB(t)
	seedRealtimeDevice(t, db, "d1", "p1", testCreatedAt, []po.DeviceAddress{
		addr("a1", "temp", "车间温度", 1),
		addr("a2", "pressure", "总管压力", 1),
	})
	// 另一台设备上有一个同样能命中「温度」的点位，用来验证过滤条件被正确限定在本设备内
	seedRealtimeDevice(t, db, "d2", "p1", testCreatedAt, []po.DeviceAddress{
		addr("b1", "temp2", "车间温度", 1),
	})

	svc, _ := newRealtimeTestService(t, db)

	cases := []struct {
		name      string
		keyword   string
		wantTotal int64
		wantIDs   []string
	}{
		{name: "按说明命中", keyword: "温度", wantTotal: 1, wantIDs: []string{"a1"}},
		{name: "按点位名命中", keyword: "press", wantTotal: 1, wantIDs: []string{"a2"}},
		{name: "无命中", keyword: "zzz", wantTotal: 0, wantIDs: nil},
		{name: "空关键字返回全部", keyword: "", wantTotal: 2, wantIDs: []string{"a1", "a2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := svc.GetDeviceSnapshot(context.Background(), req("d1", 1, 50, c.keyword))
			if err != nil {
				t.Fatalf("GetDeviceSnapshot: %v", err)
			}
			if got.Total != c.wantTotal {
				t.Errorf("total = %d, want %d", got.Total, c.wantTotal)
			}
			gotIDs := make([]string, len(got.Records))
			for i, r := range got.Records {
				gotIDs[i] = r.AddressID
				if r.AddressID == "b1" {
					t.Errorf("关键字 %q 泄漏了其它设备的点位 b1", c.keyword)
				}
			}
			if len(gotIDs) != len(c.wantIDs) {
				t.Fatalf("ids = %v, want %v", gotIDs, c.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != c.wantIDs[i] {
					t.Errorf("ids = %v, want %v", gotIDs, c.wantIDs)
				}
			}
		})
	}
}

// 分页边界：所有点位 created_at 相同（同一秒创建）时，翻页不得出现重复或漏行。
// 这条守的是 SQL 里的 "created_at ASC, id ASC" —— 只按 created_at 排序时
// 同秒点位的次序由 SQLite 自由决定，翻页结果就不稳定。
func TestRealtimeSnapshotPaginationStableWithinSameSecond(t *testing.T) {
	db := newRealtimeTestDB(t)
	seedRealtimeDevice(t, db, "d1", "p1", testCreatedAt, []po.DeviceAddress{
		addr("a1", "p1", "点位1", 1),
		addr("a2", "p2", "点位2", 1),
		addr("a3", "p3", "点位3", 1),
		addr("a4", "p4", "点位4", 1),
		addr("a5", "p5", "点位5", 1),
	})

	svc, _ := newRealtimeTestService(t, db)

	seen := map[string]int{}
	var order []string
	for page := 1; page <= 3; page++ {
		got, err := svc.GetDeviceSnapshot(context.Background(), req("d1", page, 2, ""))
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if got.Total != 5 {
			t.Errorf("page %d: total = %d, want 5", page, got.Total)
		}
		for _, r := range got.Records {
			seen[r.AddressID]++
			order = append(order, r.AddressID)
		}
	}

	if len(seen) != 5 {
		t.Errorf("翻页共取到 %d 个不同点位，want 5（有重复或漏行）：order=%v", len(seen), order)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("点位 %s 被取到 %d 次，want 1", id, n)
		}
	}
}

// 设备不存在时返回错误（复用 DeviceService 的错误语义），而不是一个空快照——
// 后者会让前端把「设备被删了」显示成「设备无点位」。
func TestRealtimeSnapshotDeviceNotFound(t *testing.T) {
	db := newRealtimeTestDB(t)
	svc, _ := newRealtimeTestService(t, db)

	if _, err := svc.GetDeviceSnapshot(context.Background(), req("nope", 1, 10, "")); err == nil {
		t.Fatal("设备不存在应返回错误")
	}
}

// 采集引擎未启动时：collecting=false、collectorRunning=false，
// 前端据此提示「采集已停止」，故这两个状态位必须如实反映。
func TestRealtimeSnapshotReportsCollectorState(t *testing.T) {
	db := newRealtimeTestDB(t)
	seedRealtimeDevice(t, db, "d1", "p1", testCreatedAt, []po.DeviceAddress{
		addr("a1", "temp", "温度", 1),
	})

	svc, _ := newRealtimeTestService(t, db)
	got, err := svc.GetDeviceSnapshot(context.Background(), req("d1", 1, 10, ""))
	if err != nil {
		t.Fatalf("GetDeviceSnapshot: %v", err)
	}
	if got.CollectorRunning {
		t.Error("引擎未启动，collectorRunning 应为 false")
	}
	if got.Collecting {
		t.Error("引擎未启动，collecting 应为 false")
	}
	if got.DeviceName != "dev-d1" {
		t.Errorf("deviceName = %q, want %q", got.DeviceName, "dev-d1")
	}
}

// isStale 按点位自身的采集周期判定，而非全表统一阈值：
// 60 秒周期的点位更新于 30 秒前仍是新鲜的，1 秒周期的点位同样时间就是掉数了。
func TestIsStale(t *testing.T) {
	now := time.Date(2026, 9, 22, 10, 0, 0, 0, time.Local)

	tests := []struct {
		name          string
		ago           time.Duration
		scanFrequency int
		want          bool
	}{
		{name: "1秒周期刚更新", ago: 500 * time.Millisecond, scanFrequency: 1000, want: false},
		{name: "1秒周期超3个周期", ago: 3*time.Second + time.Millisecond, scanFrequency: 1000, want: true},
		{name: "1秒周期恰在宽限内", ago: 3 * time.Second, scanFrequency: 1000, want: false},
		{name: "60秒周期30秒前仍新鲜", ago: 30 * time.Second, scanFrequency: 60000, want: false},
		{name: "60秒周期超3个周期", ago: 181 * time.Second, scanFrequency: 60000, want: true},
		{name: "频率未配置按1秒兜底", ago: 5 * time.Second, scanFrequency: 0, want: true},
		{name: "频率为负同样兜底", ago: 5 * time.Second, scanFrequency: -1, want: true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			if got := isStale(now, now.Add(-c.ago), c.scanFrequency); got != c.want {
				t.Errorf("isStale(ago=%s, freq=%d) = %v, want %v", c.ago, c.scanFrequency, got, c.want)
			}
		})
	}
}

// TestRealtimeSnapshotQualityJSONShape 质量码只以**一个整数**出报文，不再有
// qualityCause 那一维 —— 这是给外部调用方（前端、Agent）的契约，用原始 JSON 钉。
//
// 看结构体是没用的：字段删掉之后结构体侧怎么都对，调用方看到的是字节。
// 这里刻意让 a1 是一个坏点位，确认坏点位也只多出一个数字、不多出一个键。
func TestRealtimeSnapshotQualityJSONShape(t *testing.T) {
	db := newRealtimeTestDB(t)
	seedRealtimeDevice(t, db, "d1", "p1", testCreatedAt, []po.DeviceAddress{
		addr("a1", "temp", "温度", 1),
		addr("a2", "pressure", "压力", 1),
	})
	svc, store := newRealtimeTestService(t, db)
	store.PushRecords([]collector.CollectedRecord{
		{DeviceID: "d1", DeviceAddressID: "a1", DeviceAddressName: "temp", DeviceAddressLabel: "温度",
			Value: "", DataType: "float", Quality: driver.QualityBadAddress},
		{DeviceID: "d1", DeviceAddressID: "a2", DeviceAddressName: "pressure", DeviceAddressLabel: "压力",
			Value: "0.5", DataType: "float", Quality: driver.QualityGood},
	})

	got, err := svc.GetDeviceSnapshot(context.Background(), req("d1", 1, 50, ""))
	if err != nil {
		t.Fatalf("GetDeviceSnapshot: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, "qualityCause") {
		t.Errorf("报文里不该再有 qualityCause 字段：%s", s)
	}
	// 带尾逗号，否则 `"quality":1` 会把 `"quality":192` 也算进去（子串陷阱）。
	if !strings.Contains(s, `"quality":1,`) || !strings.Contains(s, `"quality":192,`) {
		t.Errorf("坏点位与好点位应分别带 quality=1 与 quality=192：%s", s)
	}
}
