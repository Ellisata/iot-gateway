package collector

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// TestNewGatewayTaskSelfHealDataType 验证加载点位时对漂移的 data_type 做回退自愈：
// 地址 data_type 为无效内部名、common_data_type 为有效通用名时，
// newGatewayTask 应将内存中的 data_type 修正为重新解析出的内部名，并回写库。
func TestNewGatewayTaskSelfHealDataType(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "selfheal.db")
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

	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.Net.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.Device{
		ID:           "dev-1",
		Name:         "dev1",
		ProtocolID:   "proto-1",
		ProtocolJSON: "{}",
		Status:       1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID:             "addr-1",
		DeviceID:       "dev-1",
		Name:           "40001",
		CommonDataType: "Float",
		DataType:       "bogus", // 漂移的 data_type：内部名不再可解析
		RwPermission:   "R",
		ScanFrequency:  1000,
		Status:         1,
	}).Error; err != nil {
		t.Fatal(err)
	}

	pool := workerPool.NewWorkerPool(2, 16)
	task, err := newGatewayTask(db, mockSink{}, pool, nil)
	if err != nil {
		t.Fatalf("newGatewayTask: %v", err)
	}

	// 内存中的地址 data_type 已被修正
	got := task.groups[0].addrGroups["dev-1"][0].DataType
	if got != "float32" {
		t.Fatalf("in-memory data_type = %q, want %q", got, "float32")
	}

	// 库中已自愈
	var addr po.DeviceAddress
	if err := db.Where("id = ?", "addr-1").First(&addr).Error; err != nil {
		t.Fatal(err)
	}
	if addr.DataType != "float32" {
		t.Fatalf("db data_type = %q, want %q", addr.DataType, "float32")
	}
}
