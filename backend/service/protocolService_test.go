package service

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/model/po"
)

// newProtocolTestDB 构建协议服务测试库（临时文件 SQLite，跨连接可见）。
func newProtocolTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "protocol_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.IotProtocol{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestGetProtocolDataTypes 验证类型列表接口：按协议名/协议ID查询通用+扩展类型。
func TestGetProtocolDataTypes(t *testing.T) {
	db := newProtocolTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProtocolService(db)
	ctx := context.Background()

	// 按协议名查询：返回通用类型 + Modbus 专属扩展（Int/Real）
	vo, err := svc.GetProtocolDataTypes(ctx, "ModBus.TCP", "")
	if err != nil {
		t.Fatalf("query by name: %v", err)
	}
	if vo.Protocol != "ModBus.TCP" {
		t.Errorf("protocol = %q, want ModBus.TCP", vo.Protocol)
	}
	if !slices.Equal(vo.ExtendedTypes, []string{"Int", "Real"}) {
		t.Errorf("extendedTypes = %v, want [Int Real]", vo.ExtendedTypes)
	}
	if len(vo.CommonTypes) != 13 {
		t.Errorf("commonTypes = %d, want 13", len(vo.CommonTypes))
	}
	// AllTypes 应为通用类型在前、扩展类型在后的合并集合
	wantAll := append(append([]string(nil), vo.CommonTypes...), vo.ExtendedTypes...)
	if !slices.Equal(vo.AllTypes, wantAll) {
		t.Errorf("allTypes = %v, want %v", vo.AllTypes, wantAll)
	}
	if len(vo.AllTypes) != len(vo.CommonTypes)+len(vo.ExtendedTypes) {
		t.Errorf("allTypes len = %d, want %d", len(vo.AllTypes), len(vo.CommonTypes)+len(vo.ExtendedTypes))
	}

	// 按协议 ID 查询（内部查协议名）
	vo2, err := svc.GetProtocolDataTypes(ctx, "", "proto-1")
	if err != nil {
		t.Fatalf("query by id: %v", err)
	}
	if vo2.Protocol != "ModBus.TCP" {
		t.Errorf("protocol (by id) = %q, want ModBus.TCP", vo2.Protocol)
	}

	// 协议不存在 → 错误
	if _, err := svc.GetProtocolDataTypes(ctx, "", "nope"); err == nil {
		t.Error("unknown protocol should error")
	}

	// 协议名与协议ID均为空 → 错误
	if _, err := svc.GetProtocolDataTypes(ctx, "", ""); err == nil {
		t.Error("empty params should error")
	}
}
