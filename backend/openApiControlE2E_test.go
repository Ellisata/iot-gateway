// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/configFile"
	"iot-gateway/controller"
	"iot-gateway/middleware"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/router"
	"iot-gateway/service"
)

type openAPIFakeRunner struct {
	deviceID string
	writes   []driver.WriteRequest
}

func (f *openAPIFakeRunner) Write(_ context.Context, deviceID string, reqs []driver.WriteRequest) ([]driver.WriteResult, error) {
	f.deviceID = deviceID
	f.writes = append(f.writes, reqs...)
	out := make([]driver.WriteResult, len(reqs))
	for i, r := range reqs {
		out[i] = driver.WriteResult{DeviceAddressID: r.DeviceAddressID, Code: driver.WriteCodeOK, Value: r.Value}
	}
	return out, nil
}

func (f *openAPIFakeRunner) ReadPoints(context.Context, string, []po.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, nil
}

type openAPIWritable struct{}

func (openAPIWritable) ProtocolWritable(string) bool { return true }

// TestOpenApiControlWriteEndToEnd 验证 /openApi/control/write 从 X-Api-Key 鉴权
// 到控制服务、审计落库的完整链路。
func TestOpenApiControlWriteEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "openapi_control.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}, &po.DeviceAddress{}, &po.OpenApiSecret{}, &po.ControlLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", FormJSON: "{}"}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if err := db.Create(&po.Device{ID: "dev-1", Name: "PLC-1", ProtocolID: "proto-1", Status: 1}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: "dev-1", Name: "40001", Label: "温度",
		CommonDataType: "Short", DataType: "int16", RwPermission: "RW", Status: 1,
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	secretSvc := service.NewOpenApiSecretService(db)
	if err := secretSvc.CreateOpenApiSecret(context.Background(), &dto.CreateOpenApiSecretDTO{Name: "e2e"}); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	secrets, err := secretSvc.ListOpenApiSecret(context.Background())
	if err != nil || len(secrets) != 1 {
		t.Fatalf("list secrets: %v (%d)", err, len(secrets))
	}
	apiKey := secrets[0].Key

	runner := &openAPIFakeRunner{}
	cfg := &configFile.Config{}
	cfg.Control.Enabled = true
	controlSvc := service.NewControlService(db, runner, openAPIWritable{}, cfg, nil)
	openApiCtrl := controller.NewOpenApiController(service.NewOpenApiService(
		service.NewDeviceService(db, nil, nil),
		service.NewDeviceAddressService(db, nil),
		service.NewAlarmService(db),
		service.NewMqttSubscriptionService(db, nil),
	), controlSvc, service.NewThingModelService(db), nil, nil)

	engine := gin.New()
	router.OpenApiRoutes(engine, secretSvc, openApiCtrl)

	body := `{"deviceId":"dev-1","requestId":"openapi-req-1","items":[{"deviceAddressId":"addr-1","value":"42"}]}`
	req := httptest.NewRequest("POST", "/openApi/control/write", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.OpenApiKeyHeader, apiKey)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", w.Code)
	}
	var resp struct {
		Code      string             `json:"code"`
		IsSuccess bool               `json:"isSuccess"`
		Data      vo.ControlResultVO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解码响应: %v (%s)", err, w.Body.String())
	}
	if resp.Code != "0" || !resp.IsSuccess || len(resp.Data.Results) != 1 || !resp.Data.Results[0].OK {
		t.Fatalf("响应 = %+v", resp)
	}
	if runner.deviceID != "dev-1" || len(runner.writes) != 1 || runner.writes[0].Value != "42" {
		t.Errorf("驱动收到 device=%s writes=%+v", runner.deviceID, runner.writes)
	}

	var logs []po.ControlLog
	if err := db.Find(&logs).Error; err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(logs) != 1 || logs[0].Source != "openapi" || logs[0].Code != 0 {
		t.Fatalf("审计 = %+v，期望 source=openapi 且成功", logs)
	}
}
