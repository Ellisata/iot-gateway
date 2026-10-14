// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

// newOpenApiSyncTestDB 构建分批同步测试库（临时文件 SQLite，含设备/协议/点位三张表）。
func newOpenApiSyncTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "openapi_sync_test.db")
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

// 固定 32 位 hex 的 id，让游标顺序可预期（真实环境是 UUIDv7，长度相同、字典序即时间序）。
const (
	devIDA = "0000000000000000000000000000000a"
	devIDB = "0000000000000000000000000000000b"
	devIDC = "0000000000000000000000000000000c"
)

// seedOpenApiSyncFixture 造 3 台设备：A 有 2 个点位、B **零点位**、C 有 1 个点位。
// 设备与点位都刻意打乱插入顺序，确认结果按 id / 归属稳定，而不是靠插入序。
func seedOpenApiSyncFixture(t *testing.T, db *gorm.DB) {
	t.Helper()

	proto := &po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", FormJSON: "{}"}
	if err := db.Create(proto).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}

	devices := []po.Device{
		{ID: devIDC, Name: "C号设备", ProtocolID: proto.ID, ProtocolJSON: "{}"},
		{ID: devIDA, Name: "A号设备", ProtocolID: proto.ID, ProtocolJSON: "{}"},
		{ID: devIDB, Name: "B号设备（无点位）", ProtocolID: proto.ID, ProtocolJSON: "{}"},
	}
	if err := db.Create(&devices).Error; err != nil {
		t.Fatalf("seed devices: %v", err)
	}

	addrs := []po.DeviceAddress{
		{ID: "addr-a2", DeviceID: devIDA, Name: "D101", Label: "压力", CommonDataType: "Float", DataType: "modbus.float32", RwPermission: "R", CreatedAt: "2026-09-02 10:00:00", UpdatedAt: "2026-09-03 11:00:00"},
		{ID: "addr-c1", DeviceID: devIDC, Name: "D200", Label: "流量", CommonDataType: "Float", DataType: "modbus.float32", RwPermission: "R", CreatedAt: "2026-09-04 10:00:00", UpdatedAt: "2026-09-04 10:00:00"},
		{ID: "addr-a1", DeviceID: devIDA, Name: "D100", Label: "温度", CommonDataType: "Float", DataType: "modbus.float32", RwPermission: "RW", CreatedAt: "2026-09-01 10:00:00", UpdatedAt: "2026-09-05 12:00:00"},
	}
	if err := db.Create(&addrs).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}
}

// newOpenApiSyncService 用同一份库构造 OpenApiService（无采集引擎）。
// AlarmService 只是构造出来满足依赖，本文件的用例不碰可靠性那两个接口。
func newOpenApiSyncService(db *gorm.DB) *OpenApiService {
	return NewOpenApiService(
		NewDeviceService(db, nil, nil),
		NewDeviceAddressService(db, nil),
		NewAlarmService(db),
	)
}

// TestSyncDevicePointsCursorPaging 覆盖游标分批同步的核心契约：
// 逐页取尽全部设备，两页之间不重不漏；设备按 id 升序；零点位设备不消失。
func TestSyncDevicePointsCursorPaging(t *testing.T) {
	db := newOpenApiSyncTestDB(t)
	seedOpenApiSyncFixture(t, db)
	svc := newOpenApiSyncService(db)
	ctx := context.Background()

	// 第 1 页：size=2 → 取到 A、B 两台，还有下一页
	page1, err := svc.SyncDevicePoints(ctx, &dto.OpenApiSyncDevicePointsDTO{Size: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Records) != 2 {
		t.Fatalf("page1 记录数 = %d，期望 2", len(page1.Records))
	}
	if !page1.HasMore {
		t.Errorf("page1.HasMore = false，期望 true（共 3 台）")
	}
	if page1.NextAfter != devIDB {
		t.Errorf("page1.NextAfter = %q，期望 %q（本页最后一条设备 id）", page1.NextAfter, devIDB)
	}
	if page1.Records[0].ID != devIDA || page1.Records[1].ID != devIDB {
		t.Errorf("page1 顺序 = [%s, %s]，期望按 id 升序 [A, B]",
			page1.Records[0].ID, page1.Records[1].ID)
	}

	// 设备名与协议名被富化（复用 PageDevice 的那套映射）
	if page1.Records[0].Name != "A号设备" {
		t.Errorf("page1.Records[0].Name = %q，期望 %q", page1.Records[0].Name, "A号设备")
	}
	if page1.Records[0].ProtocolName != "ModBus.TCP" {
		t.Errorf("page1.Records[0].ProtocolName = %q，期望 %q", page1.Records[0].ProtocolName, "ModBus.TCP")
	}

	// A 的 2 个点位都挂在 A 下，且按 id 升序
	a := page1.Records[0]
	if len(a.Addresses) != 2 {
		t.Fatalf("A 的点位数 = %d，期望 2", len(a.Addresses))
	}
	if a.Addresses[0].ID != "addr-a1" || a.Addresses[1].ID != "addr-a2" {
		t.Errorf("A 的点位顺序 = [%s, %s]，期望按 id 升序 [addr-a1, addr-a2]",
			a.Addresses[0].ID, a.Addresses[1].ID)
	}
	if a.Addresses[0].DeviceID != devIDA {
		t.Errorf("点位 deviceId = %q，期望 %q", a.Addresses[0].DeviceID, devIDA)
	}
	// 点位的时间戳一并返回（与设备对象对齐，供外部系统判断点位是否新配/刚改过）
	if a.Addresses[0].CreatedAt != "2026-09-01 10:00:00" || a.Addresses[0].UpdatedAt != "2026-09-05 12:00:00" {
		t.Errorf("点位时间戳 = %q/%q，期望 2026-09-01 10:00:00 / 2026-09-05 12:00:00",
			a.Addresses[0].CreatedAt, a.Addresses[0].UpdatedAt)
	}

	// **零点位设备不消失**：B 在流里，且 addresses 是非 nil 空切片（JSON 里是 []，不是 null）
	b := page1.Records[1]
	if b.ID != devIDB {
		t.Fatalf("page1.Records[1].ID = %q，期望 %q", b.ID, devIDB)
	}
	if b.Addresses == nil {
		t.Errorf("零点位设备的 Addresses 为 nil —— 期望非 nil 空切片（对外应是 [] 而非 null）")
	}
	if len(b.Addresses) != 0 {
		t.Errorf("零点位设备的点位数 = %d，期望 0", len(b.Addresses))
	}

	// 第 2 页：用第 1 页的 nextAfter 续读 → 只剩 C，且已到末尾
	page2, err := svc.SyncDevicePoints(ctx, &dto.OpenApiSyncDevicePointsDTO{After: page1.NextAfter, Size: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Records) != 1 || page2.Records[0].ID != devIDC {
		t.Fatalf("page2 = %+v，期望仅 C 一台", page2.Records)
	}
	if page2.HasMore {
		t.Errorf("page2.HasMore = true，期望 false（已是最后一页）")
	}
	if page2.NextAfter != "" {
		t.Errorf("page2.NextAfter = %q，期望空串（hasMore 为 false 时约定为空）", page2.NextAfter)
	}
	if len(page2.Records[0].Addresses) != 1 || page2.Records[0].Addresses[0].ID != "addr-c1" {
		t.Errorf("C 的点位 = %+v，期望仅 addr-c1", page2.Records[0].Addresses)
	}

	// 两页合起来恰好是全部 3 台、无重复
	seen := map[string]int{}
	for _, r := range append(page1.Records, page2.Records...) {
		seen[r.ID]++
	}
	if len(seen) != 3 {
		t.Errorf("两页共取到 %d 台，期望 3：%v", len(seen), seen)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("设备 %s 出现 %d 次，期望恰好 1 次", id, n)
		}
	}
}

// TestSyncDevicePointsTrimsProbe 钉住"多取一条判 hasMore 后裁掉"这件事：
// 恰好整除时（3 台 / size=3）不应把探针那条混进结果，也不该误报还有下一页。
func TestSyncDevicePointsTrimsProbe(t *testing.T) {
	db := newOpenApiSyncTestDB(t)
	seedOpenApiSyncFixture(t, db)
	svc := newOpenApiSyncService(db)

	page, err := svc.SyncDevicePoints(context.Background(), &dto.OpenApiSyncDevicePointsDTO{Size: 3})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(page.Records) != 3 {
		t.Fatalf("记录数 = %d，期望 3（探针条应被裁掉）", len(page.Records))
	}
	if page.HasMore {
		t.Errorf("HasMore = true，期望 false（恰好取尽）")
	}
	if page.NextAfter != "" {
		t.Errorf("NextAfter = %q，期望空串", page.NextAfter)
	}
}

// TestSyncDevicePointsEmptyStore 空库：records 是非 nil 空切片、hasMore=false、nextAfter 空。
func TestSyncDevicePointsEmptyStore(t *testing.T) {
	db := newOpenApiSyncTestDB(t)
	svc := newOpenApiSyncService(db)

	page, err := svc.SyncDevicePoints(context.Background(), &dto.OpenApiSyncDevicePointsDTO{Size: 10})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if page.Records == nil {
		t.Errorf("空库的 Records 为 nil，期望非 nil 空切片")
	}
	if len(page.Records) != 0 || page.HasMore || page.NextAfter != "" {
		t.Errorf("空库结果 = %+v，期望空且无下一页", page)
	}
}

// TestSyncDevicePointsOmitsSensitiveFields 对外裁剪必须守住：
// 序列化后的设备对象里不得出现 protocolJson 等内部字段，点位里不得出现阈值配置。
func TestSyncDevicePointsOmitsSensitiveFields(t *testing.T) {
	db := newOpenApiSyncTestDB(t)
	seedOpenApiSyncFixture(t, db)
	svc := newOpenApiSyncService(db)

	page, err := svc.SyncDevicePoints(context.Background(), &dto.OpenApiSyncDevicePointsDTO{Size: 10})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)

	for _, banned := range []string{"protocolJson", "protocolId", "iotGatewayId", "thresholdOp", "thresholdValue", "thresholdEnabled"} {
		if strings.Contains(got, banned) {
			t.Errorf("响应里出现了内部字段 %q —— 对外裁剪漏了：%s", banned, got)
		}
	}
	// 设备字段被匿名内嵌平铺（不是嵌在 "OpenApiDeviceVO" 子对象里）
	if !strings.Contains(got, `"protocolName":"ModBus.TCP"`) {
		t.Errorf("设备字段没有平铺到同步对象的顶层：%s", got)
	}
	// 零点位设备序列化成 []，不是 null
	if !strings.Contains(got, `"addresses":[]`) {
		t.Errorf("零点位设备的 addresses 未序列化成 []：%s", got)
	}
}

// TestSyncDevicePointsDTOValidation 钉住参数约束：size 必填且 1~100。
func TestSyncDevicePointsDTOValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{name: "合法", query: "size=50", wantErr: false},
		{name: "带游标", query: "after=abc&size=1", wantErr: false},
		{name: "恰好上限", query: "size=100", wantErr: false},
		{name: "缺 size", query: "", wantErr: true},
		{name: "size 为 0", query: "size=0", wantErr: true},
		{name: "size 超上限", query: "size=101", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/openApi/device/sync?"+tt.query, nil)

			var req dto.OpenApiSyncDevicePointsDTO
			err := c.ShouldBindQuery(&req)
			if tt.wantErr && err == nil {
				t.Errorf("query %q 绑定成功，期望参数校验失败", tt.query)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("query %q 绑定失败: %v", tt.query, err)
			}
		})
	}
}
