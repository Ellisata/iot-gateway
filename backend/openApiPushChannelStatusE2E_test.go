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
	"iot-gateway/push"
	"iot-gateway/router"
	"iot-gateway/service"
)

// TestOpenApiPushChannelStatusEndToEnd 验证 4.22 推送通道运行状态从 HTTP 到引擎：
// 真实 X-Api-Key 中间件 → 路由 → 控制器 → 引擎内存快照。
//
// 这里不启动引擎，只验证接线与信封形状：未启动时快照必须是 [] 而不是 null
// （管理平台按数组解析，null 会被前端当成异常）。有通道时的字段内容由 push
// 包自身的单测覆盖。
func TestOpenApiPushChannelStatusEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "openapi_push_status_e2e.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.OpenApiSecret{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

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

	// 只挂 openApi 这一组；未启动的引擎足够验证接线与空快照。
	pushEngine := push.NewEngine(db, nil)
	openApiCtrl := controller.NewOpenApiController(nil, nil, nil, nil, nil, nil, nil, pushEngine)
	engine := gin.New()
	router.OpenApiRoutes(engine, secretSvc, openApiCtrl, controller.NewDeviceScanController(nil))

	body, status := doRequestWithKey(t, engine, apiKey, "/openApi/pushChannel/status")
	if status != http.StatusOK {
		t.Fatalf("带密钥的请求状态码 = %d，期望 200\n%s", status, body)
	}
	var resp struct {
		Code      string          `json:"code"`
		IsSuccess bool            `json:"isSuccess"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应失败: %v\n%s", err, body)
	}
	if resp.Code != "0" || !resp.IsSuccess {
		t.Fatalf("code=%q isSuccess=%v，期望 0/true\n%s", resp.Code, resp.IsSuccess, body)
	}
	if got := strings.TrimSpace(string(resp.Data)); got != "[]" {
		t.Fatalf("未启动引擎时 data = %s，期望 []", got)
	}

	// 缺密钥 / 错密钥：与其它 openApi 接口一致，401 + 70003/70004。
	noKey := httptest.NewRequest("GET", "/openApi/pushChannel/status", nil)
	noKeyW := httptest.NewRecorder()
	engine.ServeHTTP(noKeyW, noKey)
	if noKeyW.Code != http.StatusUnauthorized || !strings.Contains(noKeyW.Body.String(), "70003") {
		t.Errorf("缺密钥得到 %d / %s，期望 401 / 70003", noKeyW.Code, noKeyW.Body.String())
	}

	badKey := httptest.NewRequest("GET", "/openApi/pushChannel/status", nil)
	badKey.Header.Set(middleware.OpenApiKeyHeader, "not-a-key")
	badKeyW := httptest.NewRecorder()
	engine.ServeHTTP(badKeyW, badKey)
	if badKeyW.Code != http.StatusUnauthorized || !strings.Contains(badKeyW.Body.String(), "70004") {
		t.Errorf("错密钥得到 %d / %s，期望 401 / 70004", badKeyW.Code, badKeyW.Body.String())
	}
}
