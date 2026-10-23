// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/subscribe"
)

const testSubscriptionMapping = `{
	"version": 1,
	"fields": {
		"deviceId":          {"source": "path", "value": "deviceId"},
		"deviceName":        {"source": "path", "value": "deviceName"},
		"deviceAddressId":   {"source": "path", "value": "tagId"},
		"deviceAddressLabel":{"source": "path", "value": "tagLabel"},
		"value":             {"source": "path", "value": "value"},
		"kind":              {"source": "path", "value": "kind"},
		"quality":           {"source": "path", "value": "quality"}
	}
}`

const testSubscriptionSample = `{"deviceId":"d1","deviceName":"电表1","tagId":"a1","tagLabel":"电流","value":10.2,"kind":"float","quality":192}`

func newMqttSubscriptionTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "mqtt_subscription_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.MqttSubscription{}, &po.MqttSubscriptionDevicePoint{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newTestSubscriptionService(t *testing.T) *MqttSubscriptionService {
	t.Helper()
	db := newMqttSubscriptionTestDB(t)
	return NewMqttSubscriptionService(db, subscribe.NewManager(db, nil))
}

func appErrorCode(t *testing.T, err error) string {
	t.Helper()
	var ae *appError.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not *appError.AppError", err)
	}
	return ae.Code
}

func TestMqttSubscriptionCRUD(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	create := &dto.CreateMqttSubscriptionDTO{
		Name:        "line-1",
		Description: "一号线遥测",
		Broker:      "127.0.0.1",
		Port:        "1883",
		Topic:       "factory/+/telemetry",
		QoS:         1,
		MappingJSON: json.RawMessage(testSubscriptionMapping),
		SampleJSON:  json.RawMessage(testSubscriptionSample),
	}
	if err := svc.CreateMqttSubscription(ctx, create); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 重名
	if err := svc.CreateMqttSubscription(ctx, create); appErrorCode(t, err) != enums.MqttSubscriptionExistsEnum.GetCode() {
		t.Fatalf("duplicate create code = %s", appErrorCode(t, err))
	}

	page, err := svc.PageMqttSubscription(ctx, &dto.PageMqttSubscriptionDTO{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if page.Total != 1 || len(page.Records) != 1 {
		t.Fatalf("page total/records = %d/%d, want 1/1", page.Total, len(page.Records))
	}
	id := page.Records[0].ID

	got, err := svc.GetMqttSubscriptionById(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Topic != "factory/+/telemetry" {
		t.Errorf("topic = %q", got.Topic)
	}

	// 改名 + 停用（mappingJson 不传时沿用原规则）
	status := 0
	if err := svc.UpdateMqttSubscription(ctx, &dto.UpdateMqttSubscriptionDTO{
		ID:     id,
		Name:   "line-1-renamed",
		Broker: "127.0.0.1",
		Port:   "1883",
		Topic:  "factory/+/telemetry",
		QoS:    1,
		Sort:   2,
		Status: &status,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = svc.GetMqttSubscriptionById(ctx, id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Name != "line-1-renamed" || got.Status != 0 {
		t.Errorf("after update name/status = %q/%d", got.Name, got.Status)
	}
	if string(got.MappingJSON) == "" || string(got.MappingJSON) == "null" {
		t.Errorf("mappingJson must be preserved when omitted on update, got %q", string(got.MappingJSON))
	}

	if err := svc.DeleteMqttSubscription(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := svc.DeleteMqttSubscription(ctx, id); appErrorCode(t, err) != enums.MqttSubscriptionNotExistsEnum.GetCode() {
		t.Fatalf("second delete code = %s", appErrorCode(t, err))
	}
}

func TestMqttSubscriptionCreateRejectsInvalidMapping(t *testing.T) {
	svc := newTestSubscriptionService(t)

	err := svc.CreateMqttSubscription(context.Background(), &dto.CreateMqttSubscriptionDTO{
		Name:        "broken",
		Broker:      "127.0.0.1",
		Topic:       "a/b",
		MappingJSON: json.RawMessage(`{"fields":{"deviceId":{"source":"path","value":"id"}}}`),
	})
	if code := appErrorCode(t, err); code != enums.ParamValidEnum.GetCode() {
		t.Fatalf("invalid mapping code = %s, want %s", code, enums.ParamValidEnum.GetCode())
	}
}

// TestMqttSubscriptionCreateRequiresPrimaryFields 钉住保存路径的严格校验：
// 结构性字段齐全但缺名称/类型/质量时，新配置不允许保存。
func TestMqttSubscriptionCreateRequiresPrimaryFields(t *testing.T) {
	svc := newTestSubscriptionService(t)

	err := svc.CreateMqttSubscription(context.Background(), &dto.CreateMqttSubscriptionDTO{
		Name:   "missing-primary",
		Broker: "127.0.0.1",
		Topic:  "a/b",
		MappingJSON: json.RawMessage(`{
			"fields": {
				"deviceId":        {"source": "path", "value": "deviceId"},
				"deviceAddressId": {"source": "path", "value": "tagId"},
				"value":           {"source": "path", "value": "value"}
			}
		}`),
	})
	if code := appErrorCode(t, err); code != enums.ParamValidEnum.GetCode() {
		t.Fatalf("missing primary fields code = %s, want %s", code, enums.ParamValidEnum.GetCode())
	}
	if !strings.Contains(err.Error(), "deviceName") {
		t.Errorf("error = %q, want it to mention deviceName", err)
	}
}

// TestMqttSubscriptionDevicePointPage 分页、过滤与稳定排序。
func TestMqttSubscriptionDevicePointPage(t *testing.T) {
	db := newMqttSubscriptionTestDB(t)
	svc := NewMqttSubscriptionService(db, nil)
	ctx := context.Background()

	rows := []po.MqttSubscriptionDevicePoint{
		{DeviceID: "d1", DeviceAddressID: "a2", DeviceName: "电表1", DeviceAddressLabel: "电压", Kind: "float",
			Source: po.MqttSubscriptionDevicePointSourceManual},
		{DeviceID: "d1", DeviceAddressID: "a1", DeviceName: "电表1", DeviceAddressLabel: "电流", Kind: "float",
			Source: po.MqttSubscriptionDevicePointSourceAuto},
		{DeviceID: "d2", DeviceAddressID: "a1", DeviceName: "电表2", DeviceAddressLabel: "功率", Kind: "float",
			Source: po.MqttSubscriptionDevicePointSourceManual},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed device points: %v", err)
	}

	page, err := svc.PageMqttSubscriptionDevicePoint(ctx, &dto.PageMqttSubscriptionDevicePointDTO{Page: 1, Size: 2})
	if err != nil {
		t.Fatalf("Page device points: %v", err)
	}
	if page.Total != 3 || len(page.Records) != 2 {
		t.Fatalf("page total/records = %d/%d, want 3/2", page.Total, len(page.Records))
	}
	// 稳定排序：device_id ASC, device_address_id ASC
	if page.Records[0].DeviceID != "d1" || page.Records[0].DeviceAddressID != "a1" ||
		page.Records[1].DeviceAddressID != "a2" {
		t.Errorf("unexpected order: %+v", page.Records)
	}

	filtered, err := svc.PageMqttSubscriptionDevicePoint(ctx, &dto.PageMqttSubscriptionDevicePointDTO{
		Page: 1, Size: 10, DeviceID: "d2",
	})
	if err != nil {
		t.Fatalf("filtered page: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Records) != 1 || filtered.Records[0].DeviceName != "电表2" {
		t.Errorf("filtered result = %+v", filtered.Records)
	}

	// 来源过滤：手工 / 自动分开检索，空值表示全部。
	for _, tc := range []struct {
		source string
		want   int64
	}{
		{po.MqttSubscriptionDevicePointSourceManual, 2},
		{po.MqttSubscriptionDevicePointSourceAuto, 1},
		{"", 3},
	} {
		bySource, err := svc.PageMqttSubscriptionDevicePoint(ctx, &dto.PageMqttSubscriptionDevicePointDTO{
			Page: 1, Size: 10, Source: tc.source,
		})
		if err != nil {
			t.Fatalf("source filter %q: %v", tc.source, err)
		}
		if bySource.Total != tc.want {
			t.Errorf("source filter %q total = %d, want %d", tc.source, bySource.Total, tc.want)
		}
		for _, r := range bySource.Records {
			if tc.source != "" && r.Source != tc.source {
				t.Errorf("source filter %q returned %+v", tc.source, r)
			}
		}
	}
}

func TestMqttSubscriptionScriptCRUDAndPreview(t *testing.T) {
	svc := newTestSubscriptionService(t)
	ctx := context.Background()

	scriptCfg := json.RawMessage(`{
		"script": "function parse(msg, ctx) { ctx.emit({deviceId: ctx.get(\"id\"), deviceAddressId: \"temp\", value: ctx.get(\"v\"), kind: \"float\"}); }",
		"timeoutMs": 30
	}`)
	create := &dto.CreateMqttSubscriptionDTO{
		Name:         "scripted",
		Broker:       "127.0.0.1",
		Port:         "1883",
		Topic:        "factory/+/telemetry",
		QoS:          1,
		ParserType:   "script",
		ParserConfig: scriptCfg,
		SampleJSON:   json.RawMessage(`{"id":"d1","v":1.5}`),
		SampleTopic:  "factory/line1/telemetry",
	}
	if err := svc.CreateMqttSubscription(ctx, create); err != nil {
		t.Fatalf("Create(script): %v", err)
	}

	page, err := svc.PageMqttSubscription(ctx, &dto.PageMqttSubscriptionDTO{Page: 1, Size: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("total = %d, want 1", page.Total)
	}
	id := page.Records[0].ID
	got, err := svc.GetMqttSubscriptionById(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ParserType != "script" || len(got.ParserConfig) == 0 || got.SampleTopic != "factory/line1/telemetry" {
		t.Fatalf("script fields = type:%q config:%q topic:%q", got.ParserType, got.ParserConfig, got.SampleTopic)
	}

	// 列表页只改启停：不传解析配置时必须沿用原脚本。
	status := 0
	if err := svc.UpdateMqttSubscription(ctx, &dto.UpdateMqttSubscriptionDTO{
		ID:     id,
		Name:   "scripted",
		Broker: "127.0.0.1",
		Port:   "1883",
		Topic:  "factory/+/telemetry",
		QoS:    1,
		Sort:   1,
		Status: &status,
	}); err != nil {
		t.Fatalf("Update(status only): %v", err)
	}
	got, err = svc.GetMqttSubscriptionById(ctx, id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.ParserType != "script" || !strings.Contains(string(got.ParserConfig), "function parse") {
		t.Fatalf("script config lost on status update: %+v", got)
	}
	// 只改状态不应清空样例（空 RawMessage 会让详情接口序列化失败）。
	if len(got.SampleJSON) == 0 || got.SampleTopic != "factory/line1/telemetry" {
		t.Errorf("sample lost on status update: json=%q topic=%q", got.SampleJSON, got.SampleTopic)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("detail VO must be JSON-serializable: %v", err)
	}

	preview := svc.PreviewMqttSubscription(&dto.PreviewMqttSubscriptionDTO{
		ParserType:   "script",
		ParserConfig: scriptCfg,
		SampleJSON:   json.RawMessage(`{"id":"d1","v":1.5}`),
		SampleTopic:  "factory/line1/telemetry",
	})
	if preview.Error != "" || preview.Count != 1 {
		t.Fatalf("script preview count=%d error=%q", preview.Count, preview.Error)
	}
	if rec := preview.Records[0]; rec.DeviceID != "d1" || rec.DeviceAddressID != "temp" || rec.Value != "1.5" {
		t.Errorf("script preview record = %+v", rec)
	}
}

func TestMqttSubscriptionCreateRejectsBadScript(t *testing.T) {
	svc := newTestSubscriptionService(t)
	err := svc.CreateMqttSubscription(context.Background(), &dto.CreateMqttSubscriptionDTO{
		Name:         "bad-script",
		Broker:       "127.0.0.1",
		Topic:        "a/b",
		ParserType:   "script",
		ParserConfig: json.RawMessage(`{"script":"function parse( {"}`),
	})
	if code := appErrorCode(t, err); code != enums.ParamValidEnum.GetCode() {
		t.Fatalf("bad script code = %s, want %s", code, enums.ParamValidEnum.GetCode())
	}
}

func TestMqttSubscriptionPreviewTopicPattern(t *testing.T) {
	svc := newTestSubscriptionService(t)

	got := svc.PreviewMqttSubscription(&dto.PreviewMqttSubscriptionDTO{
		ParserType: "builtin",
		MappingJSON: json.RawMessage(`{
			"topicPattern": "factory/{deviceId}/telemetry/{tag}",
			"fields": {
				"deviceId":          {"source": "topic", "value": "deviceId"},
				"deviceAddressId":   {"source": "topic", "value": "tag"},
				"value":             {"source": "path", "value": "v"},
				"deviceName":        {"source": "const", "value": "line"},
				"deviceAddressLabel":{"source": "const", "value": "温度"},
				"kind":              {"source": "const", "value": "float"},
				"quality":           {"source": "const", "value": "192"}
			}
		}`),
		SampleJSON:  json.RawMessage(`{"v":21.5}`),
		SampleTopic: "factory/line1/telemetry/temp",
	})
	if got.Error != "" || got.Count != 1 {
		t.Fatalf("topic preview count=%d error=%q", got.Count, got.Error)
	}
	if rec := got.Records[0]; rec.DeviceID != "line1" || rec.DeviceAddressID != "temp" {
		t.Errorf("topic-derived identity = %q/%q", rec.DeviceID, rec.DeviceAddressID)
	}

	missingTopic := svc.PreviewMqttSubscription(&dto.PreviewMqttSubscriptionDTO{
		ParserType:  "builtin",
		MappingJSON: json.RawMessage(`{"topicPattern":"factory/{deviceId}","fields":{"deviceId":{"source":"topic","value":"deviceId"},"deviceAddressId":{"source":"const","value":"a"},"value":{"source":"path","value":"v"}}}`),
		SampleJSON:  json.RawMessage(`{"v":1}`),
	})
	if missingTopic.Error == "" {
		t.Fatal("缺少样例主题时应返回预览错误")
	}
}

func TestMqttSubscriptionPreview(t *testing.T) {
	svc := newTestSubscriptionService(t)

	got := svc.PreviewMqttSubscription(&dto.PreviewMqttSubscriptionDTO{
		MappingJSON: json.RawMessage(testSubscriptionMapping),
		SampleJSON:  json.RawMessage(testSubscriptionSample),
	})
	if got.Error != "" {
		t.Fatalf("preview error = %q", got.Error)
	}
	if got.Count != 1 || len(got.Records) != 1 {
		t.Fatalf("preview count = %d, want 1", got.Count)
	}
	rec := got.Records[0]
	if rec.DeviceID != "d1" || rec.DeviceAddressID != "a1" || rec.Value != "10.2" || rec.Quality != 192 {
		t.Errorf("preview record = %+v", rec)
	}

	// 样例不是合法 JSON：走正常响应里的 error 字段，而不是业务错误
	bad := svc.PreviewMqttSubscription(&dto.PreviewMqttSubscriptionDTO{
		MappingJSON: json.RawMessage(testSubscriptionMapping),
		SampleJSON:  json.RawMessage(`not-json`),
	})
	if bad.Error == "" {
		t.Fatal("expected preview error for invalid sample")
	}
	if bad.Count != 0 {
		t.Errorf("bad preview count = %d, want 0", bad.Count)
	}
}
