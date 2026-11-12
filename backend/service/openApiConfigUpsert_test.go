// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/runtimecfg"
	"iot-gateway/testutil/drivertest"

	_ "iot-gateway/push/mqtt"
)

func newCfgTestService(t *testing.T) (*OpenApiConfigService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cfg.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(
		&po.ThingModel{}, &po.ThingModelProperty{}, &po.IotProtocol{}, &po.Device{},
		&po.DeviceAddress{}, &po.CollectorGroup{}, &po.PushChannel{}, &po.PushChannelForm{},
		&po.AlarmWebhook{}, &po.AlarmWebhookForm{}, &po.SystemConfig{}, &po.TriggerRule{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	drivertest.EnsureSyntheticProtocol()

	manager := runtimecfg.NewManager(db, &configFile.Config{})
	return &OpenApiConfigService{
		sqliteDB:                db,
		thingModelService:       NewThingModelService(db),
		pushChannelService:      NewPushChannelService(db, nil),
		pushChannelFormService:  NewPushChannelFormService(db),
		alarmWebhookService:     NewAlarmWebhookService(db, nil),
		alarmWebhookFormService: NewAlarmWebhookFormService(db),
		systemConfigService:     NewSystemConfigService(manager),
		protocolService:         NewProtocolService(db, nil),
		gatewayService:          NewGatewayService(db),
		deviceService:           NewDeviceService(db, nil, nil),
		deviceAddressService:    NewDeviceAddressService(db, nil),
		collectorGroupService:   NewCollectorGroupService(db),
	}, db
}

func TestOpenApiPushChannelMaskAndKeepSecret(t *testing.T) {
	svc, db := newCfgTestService(t)
	ctx := context.Background()

	create := &dto.OpenApiPushChannelBatchUpsertDTO{Items: []dto.OpenApiPushChannelUpsertDTO{{
		Type: "mqtt", Name: "e2e",
		Config: json.RawMessage(`{"broker":"127.0.0.1","port":"1883","clientId":"c","qos":1,"topic":"old","username":"u","password":"s3cr3t"}`),
	}}}
	res, err := svc.BatchUpsertPushChannels(ctx, create)
	if err != nil || res.Success != 1 {
		t.Fatalf("create: %v %+v", err, res)
	}

	page, err := svc.PagePushChannels(ctx, &dto.OpenApiPushChannelPageDTO{Page: 1, Size: 10})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("page: %v %+v", err, page)
	}
	payload := string(page.Records[0].Payload)
	if !strings.Contains(payload, `"password":"***"`) {
		t.Fatalf("password not masked: %s", payload)
	}
	if strings.Contains(payload, "s3cr3t") {
		t.Fatalf("plain password leaked: %s", payload)
	}

	update := &dto.OpenApiPushChannelBatchUpsertDTO{Items: []dto.OpenApiPushChannelUpsertDTO{{
		Type: "mqtt", Name: "e2e",
		Config: json.RawMessage(`{"password":"***","topic":"new"}`),
	}}}
	res, err = svc.BatchUpsertPushChannels(ctx, update)
	if err != nil || res.Success != 1 {
		t.Fatalf("update: %v %+v", err, res)
	}
	var row po.PushChannel
	if err := db.Where("type = ? AND name = ?", "mqtt", "e2e").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row.ConfigJSON, "s3cr3t") || !strings.Contains(row.ConfigJSON, `"topic":"new"`) {
		t.Fatalf("config not merged: %s", row.ConfigJSON)
	}
}

func TestOpenApiAlarmWebhookMaskAndKeepSecret(t *testing.T) {
	svc, db := newCfgTestService(t)
	ctx := context.Background()

	create := &dto.OpenApiAlarmWebhookBatchUpsertDTO{Items: []dto.OpenApiAlarmWebhookUpsertDTO{{
		Name: "生产群", Type: "custom", URL: strPtr("https://example.com/hook"),
		Secret: strPtr("hook-secret"), Status: intPtr(1),
	}}}
	res, err := svc.BatchUpsertAlarmWebhooks(ctx, create)
	if err != nil || res.Success != 1 {
		t.Fatalf("create: %v %+v", err, res)
	}
	page, err := svc.PageAlarmWebhooks(ctx, &dto.OpenApiAlarmWebhookPageDTO{Page: 1, Size: 10})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("page: %v %+v", err, page)
	}
	if !strings.Contains(string(page.Records[0].Payload), `"secret":"***"`) {
		t.Fatalf("secret not masked: %s", page.Records[0].Payload)
	}

	update := &dto.OpenApiAlarmWebhookBatchUpsertDTO{Items: []dto.OpenApiAlarmWebhookUpsertDTO{{
		Name: "生产群", Type: "custom", Secret: strPtr("***"),
	}}}
	res, err = svc.BatchUpsertAlarmWebhooks(ctx, update)
	if err != nil || res.Success != 1 {
		t.Fatalf("update: %v %+v", err, res)
	}
	var row po.AlarmWebhook
	if err := db.Where("name = ?", "生产群").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Secret != "hook-secret" {
		t.Fatalf("secret overwritten: %q", row.Secret)
	}
}

func TestOpenApiSystemConfigBatch(t *testing.T) {
	svc, _ := newCfgTestService(t)
	ctx := context.Background()

	res, err := svc.BatchUpdateSystemConfig(ctx, &dto.OpenApiSystemConfigBatchUpdateDTO{Items: []dto.OpenApiSystemConfigUpdateDTO{
		{Key: "push.changeOnly", Value: json.RawMessage("true")},
	}}, "test")
	if err != nil || res.Success != 1 {
		t.Fatalf("update: %v %+v", err, res)
	}
	list, err := svc.ListSystemConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range list {
		if f.Key == "push.changeOnly" {
			found = true
			if f.Value != true {
				t.Fatalf("value = %#v, want true", f.Value)
			}
		}
	}
	if !found {
		t.Fatal("push.changeOnly not found")
	}

	res, err = svc.BatchResetSystemConfig(ctx, &dto.OpenApiSystemConfigBatchResetDTO{Keys: []string{"push.changeOnly"}}, "test")
	if err != nil || res.Success != 1 {
		t.Fatalf("reset: %v %+v", err, res)
	}
}

func TestOpenApiDeviceConfigUpsertReplaceAndDelete(t *testing.T) {
	svc, db := newCfgTestService(t)
	ctx := context.Background()

	if err := db.Create(&po.IotProtocol{Name: drivertest.TestProtocol, Description: "", FormJSON: "{}"}).Error; err != nil {
		t.Fatal(err)
	}
	model := po.ThingModel{Code: "th_e2e", Name: "E2E模型", Category: "sensor", Version: "1.0.0", Status: 1}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	prop := po.ThingModelProperty{ModelID: model.ID, Identifier: "temperature", Name: "温度", Kind: "float", AccessMode: "R", SortOrder: 10, Status: 1}
	if err := db.Create(&prop).Error; err != nil {
		t.Fatal(err)
	}

	device := dto.OpenApiDeviceConfigUpsertDTO{
		Mode: "replace", Code: "dev_e2e", Name: "设备E2E", ProtocolName: drivertest.TestProtocol,
		ProtocolJSON: json.RawMessage(`{"host":"127.0.0.1","port":502,"password":"protocol-secret"}`), ModelCode: "th_e2e", Status: intPtr(1),
		Groups: []dto.OpenApiCollectorGroupUpsertDTO{{Code: "fast", Name: "fast", IntervalMs: intPtr(1000)}},
		Points: []dto.OpenApiDevicePointUpsertDTO{{
			Name: "%MW100", Label: "温度", CommonDataType: driver.TypeFloat, RwPermission: "R",
			PropertyIdentifier: strPtr("temperature"), GroupName: strPtr("fast"),
			Transform: json.RawMessage("[]"),
		}},
	}
	res, err := svc.BatchUpsertDeviceConfigs(ctx, &dto.OpenApiDeviceConfigBatchUpsertDTO{Items: []dto.OpenApiDeviceConfigUpsertDTO{device}})
	if err != nil || res.Success != 1 {
		t.Fatalf("upsert: %v %+v", err, res)
	}

	page, err := svc.PageDeviceConfigs(ctx, &dto.OpenApiDeviceConfigPageDTO{Page: 1, Size: 20})
	if err != nil || len(page.Records) != 1 {
		t.Fatalf("page: %v %+v", err, page)
	}
	if page.Records[0].TargetKey != "dev_e2e" || page.Records[0].ContentHash == "" {
		t.Fatalf("unexpected record: %+v", page.Records[0])
	}
	if !strings.Contains(string(page.Records[0].Payload), `"password":"***"`) {
		t.Fatalf("protocolJson secret not masked: %s", page.Records[0].Payload)
	}

	// protocolJson 中的 "***" 应保留网关现有密钥，而不是落库字面量。
	device.ProtocolJSON = json.RawMessage(`{"password":"***"}`)
	res, err = svc.BatchUpsertDeviceConfigs(ctx, &dto.OpenApiDeviceConfigBatchUpsertDTO{Items: []dto.OpenApiDeviceConfigUpsertDTO{device}})
	if err != nil || res.Success != 1 {
		t.Fatalf("keep secret: %v %+v", err, res)
	}
	var stored po.Device
	if err := db.Where("code = ?", "dev_e2e").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.ProtocolJSON, "protocol-secret") {
		t.Fatalf("protocolJson secret overwritten: %s", stored.ProtocolJSON)
	}
	var addrCount int64
	if err := db.Model(&po.DeviceAddress{}).Count(&addrCount).Error; err != nil {
		t.Fatal(err)
	}
	if addrCount != 1 {
		t.Fatalf("address count = %d, want 1", addrCount)
	}

	// replace 语义：payload 不再包含点位 → 点位被删除。
	device.Points = nil
	res, err = svc.BatchUpsertDeviceConfigs(ctx, &dto.OpenApiDeviceConfigBatchUpsertDTO{Items: []dto.OpenApiDeviceConfigUpsertDTO{device}})
	if err != nil || res.Success != 1 {
		t.Fatalf("replace: %v %+v", err, res)
	}
	if err := db.Model(&po.DeviceAddress{}).Count(&addrCount).Error; err != nil {
		t.Fatal(err)
	}
	if addrCount != 0 {
		t.Fatalf("address count after replace = %d, want 0", addrCount)
	}

	del, err := svc.BatchDeleteDeviceConfigs(ctx, &dto.OpenApiDeviceConfigBatchDeleteDTO{Items: []dto.OpenApiDeviceConfigKeyDTO{{Code: "dev_e2e"}}})
	if err != nil || del.Deleted != 1 {
		t.Fatalf("delete: %v %+v", err, del)
	}
}
