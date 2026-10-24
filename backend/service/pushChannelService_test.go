// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/push"

	_ "iot-gateway/push/mqtt"
	_ "iot-gateway/push/webhook"
)

func newPushChannelTestService(t *testing.T) *PushChannelService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&po.PushChannel{},
		&po.PushOutbox{},
		&po.Device{},
		&po.DeviceAddress{},
		&po.IotProtocol{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewPushChannelService(db, nil)
}

func pushErrCode(err error) string {
	var ae *appError.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestCreatePushChannelSupportsMultipleInstances(t *testing.T) {
	s := newPushChannelTestService(t)
	cfg := json.RawMessage(`{"url":"http://127.0.0.1:1/hook","spoolDisabled":true}`)

	for _, name := range []string{"MES-生产", "ERP-质检"} {
		if err := s.CreatePushChannel(context.Background(), &dto.CreatePushChannelDTO{
			Type: "http", Name: name, ConfigJSON: cfg,
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	err := s.CreatePushChannel(context.Background(), &dto.CreatePushChannelDTO{
		Type: "http", Name: "MES-生产", ConfigJSON: cfg,
	})
	if code := pushErrCode(err); code != "60001" {
		t.Fatalf("duplicate (type,name) code = %q, want 60001 (err=%v)", code, err)
	}

	page, err := s.PagePushChannel(context.Background(), &dto.PagePushChannelDTO{Page: 1, Size: 10, Type: "http"})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("total = %d, want 2", page.Total)
	}
	for _, r := range page.Records {
		if r.Type != "http" {
			t.Errorf("record type = %q, want http", r.Type)
		}
	}
}

func TestCreatePushChannelLegacyNameFallback(t *testing.T) {
	s := newPushChannelTestService(t)
	if err := s.CreatePushChannel(context.Background(), &dto.CreatePushChannelDTO{
		Name:       "mqtt",
		ConfigJSON: json.RawMessage(`{"broker":"127.0.0.1","topic":"t","spoolDisabled":true}`),
	}); err != nil {
		t.Fatalf("legacy create: %v", err)
	}
	var row po.PushChannel
	if err := s.sqliteDB.First(&row).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	if row.Type != "mqtt" {
		t.Errorf("legacy Type = %q, want mqtt", row.Type)
	}
}

func TestCreatePushChannelRejectsUnknownTypeAndBadMapping(t *testing.T) {
	s := newPushChannelTestService(t)

	err := s.CreatePushChannel(context.Background(), &dto.CreatePushChannelDTO{
		Type: "nope", Name: "x", ConfigJSON: json.RawMessage(`{}`),
	})
	if code := pushErrCode(err); code != "10001" {
		t.Fatalf("unknown type code = %q, want 10001", code)
	}

	err = s.CreatePushChannel(context.Background(), &dto.CreatePushChannelDTO{
		Type: "http", Name: "bad-map",
		ConfigJSON: json.RawMessage(`{"url":"http://127.0.0.1:1","format":"mapping-json","mapping":{}}`),
	})
	if code := pushErrCode(err); code != "10001" || !strings.Contains(err.Error(), "字段映射") {
		t.Fatalf("bad mapping error = %v (code %q)", err, code)
	}
}

func TestUpdatePushChannelRejectsTypeChange(t *testing.T) {
	s := newPushChannelTestService(t)
	if err := s.CreatePushChannel(context.Background(), &dto.CreatePushChannelDTO{
		Type: "http", Name: "h", ConfigJSON: json.RawMessage(`{"url":"http://127.0.0.1:1","spoolDisabled":true}`),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	var row po.PushChannel
	if err := s.sqliteDB.First(&row).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	err := s.UpdatePushChannel(context.Background(), &dto.UpdatePushChannelDTO{
		ID: row.ID, Type: "mqtt",
	})
	if code := pushErrCode(err); code != "10001" {
		t.Fatalf("type change code = %q, want 10001 (err=%v)", code, err)
	}
}

func TestListPushChannelTypes(t *testing.T) {
	s := newPushChannelTestService(t)
	metas := s.ListPushChannelTypes()
	byType := make(map[string]bool)
	for _, m := range metas {
		byType[m.Type] = true
	}
	if !byType["http"] || !byType["mqtt"] {
		t.Fatalf("types = %+v, want http and mqtt", metas)
	}
}

func TestPreviewAndTestSendHTTP(t *testing.T) {
	s := newPushChannelTestService(t)
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := json.RawMessage(`{
		"url":"` + server.URL + `",
		"spoolDisabled":true,
		"format":"mapping-json",
		"mapping":{"version":1,"fields":{
			"deviceId":{"source":"path","value":"deviceId"},
			"value":{"source":"path","value":"value","type":"number"}
		}}
	}`)

	preview := s.PreviewPushChannel(context.Background(), &dto.PreviewPushChannelDTO{Type: "http", ConfigJSON: cfg})
	if preview.Error != "" {
		t.Fatalf("preview error: %s", preview.Error)
	}
	if preview.Count != 1 || !strings.Contains(preview.Payload, `"deviceId":"demo-device"`) {
		t.Fatalf("preview = %+v", preview)
	}

	res := s.TestSendPushChannel(context.Background(), &dto.TestSendPushChannelDTO{Type: "http", ConfigJSON: cfg})
	if !res.Success || res.StatusCode != 200 || res.Messages != 1 {
		t.Fatalf("test send = %+v", res)
	}
	if !strings.Contains(body, `"deviceId":"demo-device"`) {
		t.Fatalf("server body = %s", body)
	}

	bad := s.PreviewPushChannel(context.Background(), &dto.PreviewPushChannelDTO{
		Type:       "http",
		ConfigJSON: json.RawMessage(`{"url":"http://127.0.0.1:1","format":"mapping-json","mapping":{"fields":{}}}`),
	})
	if bad.Error == "" {
		t.Fatal("preview with bad mapping should return Error")
	}
}

func TestPreviewPushChannelMapping(t *testing.T) {
	s := newPushChannelTestService(t)
	ctx := context.Background()

	preview := func(raw string) *vo.PushChannelPreviewVO {
		return s.PreviewPushChannelMapping(ctx, &dto.PreviewPushChannelMappingDTO{
			Mapping: json.RawMessage(raw),
		})
	}

	t.Run("array", func(t *testing.T) {
		res := preview(`{"version":1,"fields":{
			"deviceId":{"source":"path","value":"deviceId"},
			"value":{"source":"path","value":"value","type":"number"}
		}}`)
		if res.Error != "" {
			t.Fatalf("error: %s", res.Error)
		}
		if res.Count != 1 || !strings.Contains(res.Payload, `"deviceId":"demo-device"`) ||
			!strings.Contains(res.Payload, `"value":23.5`) {
			t.Fatalf("preview = %+v", res)
		}
	})

	t.Run("object batch fields", func(t *testing.T) {
		res := preview(`{"version":1,"envelope":"data","batchFields":{
			"site":{"source":"const","value":"plant-a"},
			"count":{"source":"path","value":"count","type":"number"}
		},"fields":{"id":{"source":"path","value":"deviceAddressId"}}}`)
		if res.Error != "" {
			t.Fatalf("error: %s", res.Error)
		}
		if !strings.Contains(res.Payload, `"data":[`) ||
			!strings.Contains(res.Payload, `"site":"plant-a"`) ||
			!strings.Contains(res.Payload, `"count":1`) {
			t.Fatalf("payload = %s", res.Payload)
		}
	})

	t.Run("record emit", func(t *testing.T) {
		res := preview(`{"version":1,"emit":"record","fields":{"id":{"source":"path","value":"deviceAddressId"}}}`)
		if res.Error != "" {
			t.Fatalf("error: %s", res.Error)
		}
		if res.Count != 1 || res.Payload != `{"id":"demo-point"}` {
			t.Fatalf("preview = %+v", res)
		}
	})

	t.Run("script format", func(t *testing.T) {
		res := s.PreviewPushChannelMapping(ctx, &dto.PreviewPushChannelMappingDTO{
			Format:  push.FormatScript,
			Mapping: json.RawMessage(`{"script":"function encode(batch) { var r = batch.records[0]; return JSON.stringify({id: r.deviceAddressId, value: Number(r.value)}) }"}`),
		})
		if res.Error != "" {
			t.Fatalf("error: %s", res.Error)
		}
		if res.Count != 1 || res.Payload != `{"id":"demo-point","value":23.5}` {
			t.Fatalf("preview = %+v", res)
		}
	})

	bad := map[string]struct {
		raw  string
		want string
	}{
		"empty":          {`{}`, "至少需要配置一个字段映射"},
		"null":           {`null`, "mapping 不能为空"},
		"blank":          {``, "mapping 不能为空"},
		"unknown type":   {`{"fields":{"v":{"source":"path","value":"value","type":"date"}}}`, "未知的输出类型"},
		"envelope clash": {`{"envelope":"data","batchFields":{"data.x":{"source":"const","value":"1"}},"fields":{"v":{"source":"path","value":"value"}}}`, "冲突"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			res := preview(c.raw)
			if res.Error == "" || !strings.Contains(res.Error, c.want) {
				t.Fatalf("Error = %q, want contains %q", res.Error, c.want)
			}
		})
	}
}

func TestTestPushChannelSurfacesConcreteError(t *testing.T) {
	s := newPushChannelTestService(t)
	err := s.TestPushChannel(context.Background(), &dto.TestPushChannelDTO{
		Type:       "http",
		ConfigJSON: json.RawMessage(`{"url":"http://127.0.0.1:1","spoolDisabled":true}`),
	})
	if code := pushErrCode(err); code != "60003" {
		t.Fatalf("code = %q, want 60003 (err=%v)", code, err)
	}
	if !strings.Contains(err.Error(), "连接失败:") {
		t.Fatalf("error should carry the concrete reason: %v", err)
	}
}
