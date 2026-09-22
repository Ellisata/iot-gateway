// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/driver"
	"iot-gateway/model/po"
	"iot-gateway/workerPool"
)

// 采集引擎必须在任务构建时登记串口驱动、停止时注销。
//
// 登记是「测试连接」能复用采集引擎串口连接的前提：HTTP 只拿得到协议名 + protocol_json，
// 拿不到设备 ID，只能靠这张表反查引擎正在使用的实例。
// 漏登记 → 测试连接退回临时实例 → Windows 下二次打开串口报 Access is denied（原始 bug）。
// 漏注销 → 已停用设备的实例赖在表里，测试连接可能复用到一条已经不采集的链路。
//
// 刻意只种协议与设备、**不种地址**：没有地址就没有采集分组，
// Start 不调度、不 Connect、不碰真实串口，但 drvMap 里仍有驱动，
// 登记 / 注销这条路径被完整走到。
func TestGatewayTaskRegistersAndUnregistersSerialOwners(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		defer sqlDB.Close()
	}
	for _, m := range []interface{}{&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "DLT645.Serial", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	dev := po.Device{
		ID:           "dev-1",
		Name:         "dlt1997-串口",
		ProtocolID:   "proto-1",
		ProtocolJSON: `{"protocolVersion":"1997","meterAddress":"1","comPort":"COM1"}`,
		Status:       1,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}

	base := driver.SerialOwnerCount()

	task, err := newGatewayTask(db, nil, workerPool.NewWorkerPool(2, 16), nil)
	if err != nil {
		t.Fatalf("newGatewayTask 失败: %v", err)
	}
	if got := driver.SerialOwnerCount(); got != base+1 {
		t.Fatalf("建任务后登记的串口驱动数 = %d, want %d", got, base+1)
	}

	task.Start()
	task.Stop()

	if got := driver.SerialOwnerCount(); got != base {
		t.Errorf("停任务后登记的串口驱动数 = %d, want %d（未注销，实例会赖在表里）", got, base)
	}
}
