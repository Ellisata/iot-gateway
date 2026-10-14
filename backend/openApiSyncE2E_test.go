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

	"iot-gateway/controller"
	"iot-gateway/middleware"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/router"
	"iot-gateway/service"
)

const e2eDevID = "0000000000000000000000000000000a"

// TestOpenApiSyncDevicePointsEndToEnd 把 4.5 分批同步**从 HTTP 请求打到数据库**走一遍：
// 真实中间件（X-Api-Key 校验）→ 真实路由 → 真实控制器绑定 → 真实服务 → 临时 SQLite。
//
// service 层的单测已经覆盖了翻页与映射逻辑，这里补的是它覆盖不到的那一段：
// 路由挂对了没有、query 绑定通不通、响应信封形状对不对、缺密钥是不是 401。
// 这四点任何一处错了，单测都全绿而第三方调用直接失败。
func TestOpenApiSyncDevicePointsEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "openapi_e2e.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.Device{}, &po.IotProtocol{}, &po.DeviceAddress{}, &po.OpenApiSecret{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 一台设备 + 一个点位
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: "ModBus.TCP", FormJSON: "{}"}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if err := db.Create(&po.Device{
		ID: e2eDevID, Name: "1号PLC", ProtocolID: "proto-1", ProtocolJSON: `{"host":"10.0.0.1"}`,
	}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := db.Create(&po.DeviceAddress{
		ID: "addr-1", DeviceID: e2eDevID, Name: "D100", Label: "温度",
		CommonDataType: "Float", DataType: "modbus.float32", RwPermission: "R", ScanFrequency: 1000,
		CreatedAt: "2026-09-01 10:00:00", UpdatedAt: "2026-09-05 12:00:00",
	}).Error; err != nil {
		t.Fatalf("seed address: %v", err)
	}

	// 真实密钥服务：建一把密钥，再把它读出来当请求头用
	secretSvc := service.NewOpenApiSecretService(db)
	ctx := context.Background()
	if err := secretSvc.CreateOpenApiSecret(ctx, &dto.CreateOpenApiSecretDTO{Name: "e2e"}); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	secrets, err := secretSvc.ListOpenApiSecret(ctx)
	if err != nil || len(secrets) != 1 {
		t.Fatalf("list secrets: %v (%d 条)", err, len(secrets))
	}
	apiKey := secrets[0].Key

	// 只挂开放接口这一组，不必拉起整个应用
	openApiCtrl := controller.NewOpenApiController(service.NewOpenApiService(
		service.NewDeviceService(db, nil, nil),
		service.NewDeviceAddressService(db, nil),
		service.NewAlarmService(db),
	))
	engine := gin.New()
	router.OpenApiRoutes(engine, secretSvc, openApiCtrl)

	// ① 带密钥：应拿到统一信封 + 嵌套点位
	body, status := doRequestWithKey(t, engine, apiKey, "/openApi/device/sync?size=10")
	if status != http.StatusOK {
		t.Fatalf("带密钥的请求状态码 = %d，期望 200", status)
	}
	var resp struct {
		Code      string `json:"code"`
		IsSuccess bool   `json:"isSuccess"`
		Data      struct {
			Size      int    `json:"size"`
			HasMore   bool   `json:"hasMore"`
			NextAfter string `json:"nextAfter"`
			Records   []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Addresses []struct {
					ID        string `json:"id"`
					Label     string `json:"label"`
					CreatedAt string `json:"createdAt"`
					UpdatedAt string `json:"updatedAt"`
				} `json:"addresses"`
			} `json:"records"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应失败: %v\n%s", err, body)
	}
	if resp.Code != "0" || !resp.IsSuccess {
		t.Fatalf("code=%q isSuccess=%v，期望 0/true\n%s", resp.Code, resp.IsSuccess, body)
	}
	if len(resp.Data.Records) != 1 {
		t.Fatalf("记录数 = %d，期望 1", len(resp.Data.Records))
	}
	rec := resp.Data.Records[0]
	if rec.ID != e2eDevID || rec.Name != "1号PLC" {
		t.Errorf("设备 = %s/%s，期望 %s/1号PLC", rec.ID, rec.Name, e2eDevID)
	}
	if len(rec.Addresses) != 1 || rec.Addresses[0].ID != "addr-1" || rec.Addresses[0].Label != "温度" {
		t.Errorf("点位未正确嵌套：%+v", rec.Addresses)
	}
	// 点位时间戳要到 HTTP 响应这一层（不只是 service 里映射了）
	if rec.Addresses[0].CreatedAt != "2026-09-01 10:00:00" || rec.Addresses[0].UpdatedAt != "2026-09-05 12:00:00" {
		t.Errorf("点位时间戳 = %q/%q，期望 2026-09-01 10:00:00 / 2026-09-05 12:00:00",
			rec.Addresses[0].CreatedAt, rec.Addresses[0].UpdatedAt)
	}
	if resp.Data.HasMore || resp.Data.NextAfter != "" {
		t.Errorf("只有一页时 hasMore=%v nextAfter=%q，期望 false/空", resp.Data.HasMore, resp.Data.NextAfter)
	}
	// PLC 连接配置不得出现在对外响应里
	if strings.Contains(string(body), "protocolJson") || strings.Contains(string(body), "10.0.0.1") {
		t.Errorf("响应里泄露了 protocolJson / 连接地址：%s", body)
	}

	// ② 不带密钥：401 + 70003（与文档 §2 的约定一致）
	req := httptest.NewRequest("GET", "/openApi/device/sync?size=10", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("缺 X-Api-Key 的状态码 = %d，期望 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "70003") {
		t.Errorf("缺密钥的响应里没有 70003：%s", w.Body.String())
	}

	// ③ 密钥无效：同样 401
	invalidBody, invalidStatus := doRequestWithKey(t, engine, "deadbeef", "/openApi/device/sync?size=10")
	if invalidStatus != http.StatusUnauthorized || !strings.Contains(string(invalidBody), "70004") {
		t.Errorf("无效密钥得到 %d / %s，期望 401 / 70004", invalidStatus, invalidBody)
	}

	// ④ size 缺失：走统一信封的参数校验错误（HTTP 仍是 200）。
	// 注意码值是 PARAM_ERROR 而非 10001 —— 绑定失败走的是 gin 校验错误那条路，
	// 与业务错误码（enums.ParamValidEnum）不是一回事，文档 §5 也是这么列的。
	paramBody, paramStatus := doRequestWithKey(t, engine, apiKey, "/openApi/device/sync")
	if paramStatus != http.StatusOK || !strings.Contains(string(paramBody), "PARAM_ERROR") {
		t.Errorf("缺 size 得到 %d / %s，期望 200 / PARAM_ERROR", paramStatus, paramBody)
	}
}

// doRequestWithKey 带 X-Api-Key 发一个 GET 并返回 (响应体, 状态码)。
func doRequestWithKey(t *testing.T, e *gin.Engine, key, path string) ([]byte, int) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set(middleware.OpenApiKeyHeader, key)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w.Body.Bytes(), w.Code
}
