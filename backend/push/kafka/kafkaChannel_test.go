// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package kafka

import (
	"strings"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
	"iot-gateway/push"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushOutbox{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func previewBatch() push.PushBatch {
	rec := collector.CollectedRecord{
		DeviceID:           "d1",
		DeviceCode:         "dev001",
		DeviceName:         "一号机",
		DeviceAddressID:    "a1",
		DeviceAddressName:  "temperature",
		DeviceAddressLabel: "温度",
		Value:              "23.5",
		DataType:           "float32",
		Kind:               driver.KindFloat,
		Protocol:           "Demo",
		Quality:            driver.QualityGood,
		CollectedAt:        "2026-11-09 10:00:00.000",
	}
	return push.PushBatch{
		DeviceID:    rec.DeviceID,
		DeviceCode:  rec.DeviceCode,
		CollectedAt: rec.CollectedAt,
		Records:     []collector.CollectedRecord{rec},
	}
}

func TestKafkaChannelRegistration(t *testing.T) {
	meta, ok := push.RegisteredTypeMeta("kafka")
	if !ok {
		t.Fatal("kafka 未注册")
	}
	if !meta.MessageBased {
		t.Error("kafka 应为消息类通道")
	}
	if meta.DefaultFormat != push.FormatNativeJSON {
		t.Errorf("defaultFormat = %q, want %q", meta.DefaultFormat, push.FormatNativeJSON)
	}
	if !meta.SupportsPreview || !meta.SupportsTestSend {
		t.Errorf("preview/testSend = %v/%v, want true/true", meta.SupportsPreview, meta.SupportsTestSend)
	}
	if len(meta.Formats) != len(push.SupportedFormats()) {
		t.Errorf("formats = %v", meta.Formats)
	}
}

func TestKafkaChannelFactoryAndPreview(t *testing.T) {
	db := newTestDB(t)
	ch, err := push.NewTemporaryChannel(db, "kafka", "k1",
		`{"brokers":"127.0.0.1:9092","topic":"iot-data"}`)
	if err != nil {
		t.Fatalf("new channel: %v", err)
	}
	defer push.ReleaseChannel(ch)

	if _, ok := ch.(push.Previewer); !ok {
		t.Fatal("kafka channel should implement push.Previewer")
	}
	if _, ok := ch.(push.TestSender); !ok {
		t.Fatal("kafka channel should implement push.TestSender")
	}
	if _, ok := ch.(push.Releaser); !ok {
		t.Fatal("kafka channel should implement push.Releaser")
	}

	res := ch.(push.Previewer).Preview(previewBatch())
	if res.Error != "" {
		t.Fatalf("preview error: %s", res.Error)
	}
	if len(res.Payloads) != 1 {
		t.Fatalf("preview payloads = %d, want 1", len(res.Payloads))
	}
	if !strings.Contains(res.Payloads[0], `"dev001"`) {
		t.Errorf("preview payload missing device code: %s", res.Payloads[0])
	}
}

func TestKafkaChannelRejectsInvalidConfig(t *testing.T) {
	db := newTestDB(t)
	if _, err := push.NewTemporaryChannel(db, "kafka", "k1", `{"brokers":"127.0.0.1"}`); err == nil {
		t.Fatal("missing topic should fail")
	}
	if _, err := push.NewTemporaryChannel(db, "kafka", "k1",
		`{"brokers":"127.0.0.1","topic":"iot-data","format":"native-json","mapping":{"x":1}}`); err == nil {
		t.Fatal("native-json with mapping should fail")
	}
}
