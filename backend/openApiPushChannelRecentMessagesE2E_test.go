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

// TestOpenApiPushChannelRecentMessagesEndToEnd 验证 4.23 最近推送报文从 HTTP 到引擎：
// 真实 X-Api-Key 中间件 → 路由 → 控制器 → 引擎内存环形缓冲。
//
// 这里不启动引擎，只验证接线与信封形状：没有运行中的通道时必须返回
// active=false 与空数组（不是 null），管理平台据此显示「未运行」；
// channelId 必填，缺参数走统一的 PARAM_ERROR 信封。有报文时的字段转换
// 由 newPushRecentMessagesVO 与 push 包自身的单测覆盖。
func TestOpenApiPushChannelRecentMessagesEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "openapi_push_recent_e2e.db")
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

	pushEngine := push.NewEngine(db, nil)
	openApiCtrl := controller.NewOpenApiController(nil, nil, nil, nil, nil, nil, nil, pushEngine)
	engine := gin.New()
	router.OpenApiRoutes(engine, secretSvc, openApiCtrl, controller.NewDeviceScanController(nil))

	body, status := doRequestWithKey(t, engine, apiKey, "/openApi/pushChannel/recentMessages?channelId=missing")
	if status != http.StatusOK {
		t.Fatalf("带密钥的请求状态码 = %d，期望 200\n%s", status, body)
	}
	var resp struct {
		Code      string `json:"code"`
		IsSuccess bool   `json:"isSuccess"`
		Data      struct {
			Active   bool            `json:"active"`
			Messages json.RawMessage `json:"messages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应失败: %v\n%s", err, body)
	}
	if resp.Code != "0" || !resp.IsSuccess {
		t.Fatalf("code=%q isSuccess=%v，期望 0/true\n%s", resp.Code, resp.IsSuccess, body)
	}
	if resp.Data.Active {
		t.Fatalf("未启动引擎时 active = true，期望 false\n%s", body)
	}
	if got := strings.TrimSpace(string(resp.Data.Messages)); got != "[]" {
		t.Fatalf("未启动引擎时 messages = %s，期望 []", got)
	}

	// channelId 必填：与内部接口一致的参数校验。
	body, _ = doRequestWithKey(t, engine, apiKey, "/openApi/pushChannel/recentMessages")
	var badResp struct {
		Code      string `json:"code"`
		IsSuccess bool   `json:"isSuccess"`
	}
	if err := json.Unmarshal(body, &badResp); err != nil {
		t.Fatalf("解析缺参响应失败: %v\n%s", err, body)
	}
	if badResp.Code != "PARAM_ERROR" || badResp.IsSuccess {
		t.Errorf("缺 channelId 得到 code=%q isSuccess=%v，期望 PARAM_ERROR/false\n%s", badResp.Code, badResp.IsSuccess, body)
	}

	// 缺密钥 / 错密钥：与其它 openApi 接口一致，401 + 70003/70004。
	noKey := httptest.NewRequest("GET", "/openApi/pushChannel/recentMessages?channelId=missing", nil)
	noKeyW := httptest.NewRecorder()
	engine.ServeHTTP(noKeyW, noKey)
	if noKeyW.Code != http.StatusUnauthorized || !strings.Contains(noKeyW.Body.String(), "70003") {
		t.Errorf("缺密钥得到 %d / %s，期望 401 / 70003", noKeyW.Code, noKeyW.Body.String())
	}

	badKey := httptest.NewRequest("GET", "/openApi/pushChannel/recentMessages?channelId=missing", nil)
	badKey.Header.Set(middleware.OpenApiKeyHeader, "not-a-key")
	badKeyW := httptest.NewRecorder()
	engine.ServeHTTP(badKeyW, badKey)
	if badKeyW.Code != http.StatusUnauthorized || !strings.Contains(badKeyW.Body.String(), "70004") {
		t.Errorf("错密钥得到 %d / %s，期望 401 / 70004", badKeyW.Code, badKeyW.Body.String())
	}
}
