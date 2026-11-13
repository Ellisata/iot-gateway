// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/controller"
	"iot-gateway/middleware"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/router"
	"iot-gateway/service"
)

// TestOpenApiThingModelBatchUpsertEndToEnd 把 4.13 物模型批量 upsert
// 从 HTTP 请求打到数据库走一遍：X-Api-Key 中间件 → 路由 → JSON 绑定 →
// 服务层事务 → 临时 SQLite，验证「徽章之外」的那段接线。
func TestOpenApiThingModelBatchUpsertEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "openapi_thing_model_e2e.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.ThingModel{}, &po.ThingModelProperty{},
		&po.IotProtocol{}, &po.Device{}, &po.DeviceAddress{}, &po.OpenApiSecret{}); err != nil {
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

	controlSvc := service.NewControlService(db, nil, nil, &configFile.Config{}, nil)
	openApiCtrl := controller.NewOpenApiController(service.NewOpenApiService(
		service.NewDeviceService(db, nil, nil),
		service.NewDeviceAddressService(db, nil),
		service.NewAlarmService(db),
		service.NewMqttSubscriptionService(db, nil),
	), controlSvc, service.NewThingModelService(db), nil, nil)
	engine := gin.New()
	router.OpenApiRoutes(engine, secretSvc, openApiCtrl, controller.NewDeviceScanController(nil))

	const body = `{"models":[{
		"code":"th_sensor",
		"name":"温湿度传感器",
		"category":"sensor",
		"properties":[
			{"identifier":"temperature","name":"温度","kind":"float","accessMode":"R","unit":"℃"}
		]
	}]}`

	// ① 缺密钥 / 错误密钥：401 + 70003 / 70004
	noKeyW := httptest.NewRecorder()
	engine.ServeHTTP(noKeyW, httptest.NewRequest("POST", "/openApi/thingModel/batchUpsert",
		bytes.NewBufferString(body)))
	if noKeyW.Code != http.StatusUnauthorized || !strings.Contains(noKeyW.Body.String(), "70003") {
		t.Fatalf("缺密钥得到 %d / %s，期望 401 / 70003", noKeyW.Code, noKeyW.Body.String())
	}
	badKeyW := httptest.NewRecorder()
	badKeyReq := httptest.NewRequest("POST", "/openApi/thingModel/batchUpsert", bytes.NewBufferString(body))
	badKeyReq.Header.Set(middleware.OpenApiKeyHeader, "deadbeef")
	engine.ServeHTTP(badKeyW, badKeyReq)
	if badKeyW.Code != http.StatusUnauthorized || !strings.Contains(badKeyW.Body.String(), "70004") {
		t.Fatalf("无效密钥得到 %d / %s，期望 401 / 70004", badKeyW.Code, badKeyW.Body.String())
	}

	// ② 首次提交：创建模型 + 属性
	respBody, status := doPostJSONWithKey(t, engine, apiKey, "/openApi/thingModel/batchUpsert", body)
	if status != http.StatusOK {
		t.Fatalf("首次提交状态码 = %d，期望 200：%s", status, respBody)
	}
	resp := decodeUpsertResponse(t, respBody)
	if resp.Code != "0" || !resp.IsSuccess {
		t.Fatalf("首次提交 code=%q isSuccess=%v：%s", resp.Code, resp.IsSuccess, respBody)
	}
	if resp.Data.Total != 1 || resp.Data.Success != 1 || resp.Data.Created != 1 ||
		resp.Data.PropertiesCreated != 1 {
		t.Fatalf("首次统计不符: %+v", resp.Data)
	}
	first := resp.Data.Models[0]
	if first.Action != "created" || first.ID == "" ||
		len(first.Properties) != 1 || first.Properties[0].Action != "created" {
		t.Fatalf("首次逐条结果不符: %+v", first)
	}

	var modelCount, propCount int64
	if err := db.Model(&po.ThingModel{}).Where("code = ?", "th_sensor").Count(&modelCount).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if err := db.Model(&po.ThingModelProperty{}).Where("model_id = ?", first.ID).Count(&propCount).Error; err != nil {
		t.Fatalf("count properties: %v", err)
	}
	if modelCount != 1 || propCount != 1 {
		t.Fatalf("落库行数 = %d/%d，期望 1/1", modelCount, propCount)
	}

	// ③ 同一自然键再次提交：原地更新，不新增行
	respBody, status = doPostJSONWithKey(t, engine, apiKey, "/openApi/thingModel/batchUpsert", body)
	if status != http.StatusOK {
		t.Fatalf("二次提交状态码 = %d：%s", status, respBody)
	}
	resp = decodeUpsertResponse(t, respBody)
	second := resp.Data.Models[0]
	if second.Action != "updated" || second.ID != first.ID ||
		resp.Data.Updated != 1 || resp.Data.Created != 0 || resp.Data.Failed != 0 {
		t.Fatalf("二次提交应为原地更新: %+v", second)
	}
	if err := db.Model(&po.ThingModel{}).Count(&modelCount).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if err := db.Model(&po.ThingModelProperty{}).Count(&propCount).Error; err != nil {
		t.Fatalf("count properties: %v", err)
	}
	if modelCount != 1 || propCount != 1 {
		t.Fatalf("二次提交后行数 = %d/%d，期望 1/1（幂等）", modelCount, propCount)
	}

	// ④ 空 models / 超过 100 条：顶层参数校验失败（FAIL 在信封上，不是逐条结果）
	for name, payload := range map[string]string{
		"空 models": `{"models":[]}`,
		"超过上限":     oversizedModelsBody(),
	} {
		b, st := doPostJSONWithKey(t, engine, apiKey, "/openApi/thingModel/batchUpsert", payload)
		if st != http.StatusOK || !strings.Contains(string(b), "PARAM_ERROR") {
			t.Errorf("%s 得到 %d / %s，期望 200 / PARAM_ERROR", name, st, b)
		}
	}

	// ⑤ 跨模型部分成功：合法模型 + 非法模型
	partial := `{"models":[
		{"code":"th_ok","name":"合法模型"},
		{"code":"1bad","name":"非法模型"}
	]}`
	respBody, status = doPostJSONWithKey(t, engine, apiKey, "/openApi/thingModel/batchUpsert", partial)
	if status != http.StatusOK {
		t.Fatalf("部分成功状态码 = %d：%s", status, respBody)
	}
	resp = decodeUpsertResponse(t, respBody)
	if resp.Code != "0" || !resp.IsSuccess {
		t.Fatalf("部分成功仍应是成功信封: %s", respBody)
	}
	if resp.Data.Success != 1 || resp.Data.Failed != 1 {
		t.Fatalf("部分成功统计不符: %+v", resp.Data)
	}
	if resp.Data.Models[1].Action != "failed" || resp.Data.Models[1].ErrorCode != "41007" {
		t.Fatalf("非法模型结果不符: %+v", resp.Data.Models[1])
	}

	// ⑥ 属性批量删除：删掉 th_sensor.temperature
	propDel := `{"items":[{"modelCode":"th_sensor","identifier":"temperature"}]}`
	respBody, status = doPostJSONWithKey(t, engine, apiKey, "/openApi/thingModelProperty/batchDelete", propDel)
	if status != http.StatusOK {
		t.Fatalf("属性删除状态码 = %d：%s", status, respBody)
	}
	del := decodeDeleteResponse(t, respBody)
	if del.Code != "0" || del.Data.Requested != 1 || del.Data.Deleted != 1 || del.Data.Failed != 0 {
		t.Fatalf("属性删除结果不符: %s", respBody)
	}
	if len(del.Data.Results) != 1 || del.Data.Results[0].Action != "deleted" {
		t.Fatalf("属性删除逐条结果不符: %+v", del.Data.Results)
	}

	// ⑦ 模型批量删除：删 th_sensor，附带一个不存在的 th_missing
	modelDel := `{"codes":["th_sensor","th_missing"]}`
	respBody, status = doPostJSONWithKey(t, engine, apiKey, "/openApi/thingModel/batchDelete", modelDel)
	if status != http.StatusOK {
		t.Fatalf("模型删除状态码 = %d：%s", status, respBody)
	}
	del = decodeDeleteResponse(t, respBody)
	if del.Code != "0" || del.Data.Requested != 2 || del.Data.Deleted != 1 || del.Data.Failed != 1 {
		t.Fatalf("模型删除统计不符: %s", respBody)
	}
	if del.Data.Results[1].Action != "failed" || del.Data.Results[1].ErrorCode != "41002" {
		t.Fatalf("不存在模型的结果不符: %+v", del.Data.Results[1])
	}

	if err := db.Model(&po.ThingModel{}).Count(&modelCount).Error; err != nil {
		t.Fatalf("count models: %v", err)
	}
	if err := db.Model(&po.ThingModelProperty{}).Count(&propCount).Error; err != nil {
		t.Fatalf("count properties: %v", err)
	}
	if modelCount != 1 || propCount != 0 {
		t.Fatalf("删除后剩 model=%d prop=%d，期望 1/0", modelCount, propCount)
	}
}

type upsertResponse struct {
	Code      string `json:"code"`
	IsSuccess bool   `json:"isSuccess"`
	Data      struct {
		Total             int `json:"total"`
		Success           int `json:"success"`
		Failed            int `json:"failed"`
		Created           int `json:"created"`
		Updated           int `json:"updated"`
		PropertiesCreated int `json:"propertiesCreated"`
		PropertiesUpdated int `json:"propertiesUpdated"`
		Models            []struct {
			Code         string `json:"code"`
			Name         string `json:"name"`
			Action       string `json:"action"`
			ID           string `json:"id"`
			ErrorCode    string `json:"errorCode"`
			ErrorMessage string `json:"errorMessage"`
			Properties   []struct {
				Identifier   string `json:"identifier"`
				Action       string `json:"action"`
				ID           string `json:"id"`
				ErrorCode    string `json:"errorCode"`
				ErrorMessage string `json:"errorMessage"`
			} `json:"properties"`
		} `json:"models"`
	} `json:"data"`
}

func decodeUpsertResponse(t *testing.T, body []byte) upsertResponse {
	t.Helper()
	var resp upsertResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析响应失败: %v\n%s", err, body)
	}
	return resp
}

type deleteResponse struct {
	Code      string `json:"code"`
	IsSuccess bool   `json:"isSuccess"`
	Data      struct {
		Requested int `json:"requested"`
		Deleted   int `json:"deleted"`
		Failed    int `json:"failed"`
		Results   []struct {
			Code         string `json:"code"`
			ModelCode    string `json:"modelCode"`
			Identifier   string `json:"identifier"`
			Action       string `json:"action"`
			ErrorCode    string `json:"errorCode"`
			ErrorMessage string `json:"errorMessage"`
		} `json:"results"`
	} `json:"data"`
}

func decodeDeleteResponse(t *testing.T, body []byte) deleteResponse {
	t.Helper()
	var resp deleteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析删除响应失败: %v\n%s", err, body)
	}
	return resp
}

func doPostJSONWithKey(t *testing.T, e *gin.Engine, key, path, body string) ([]byte, int) {
	t.Helper()
	req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.OpenApiKeyHeader, key)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	return w.Body.Bytes(), w.Code
}

func oversizedModelsBody() string {
	var b strings.Builder
	b.WriteString(`{"models":[`)
	for i := 0; i <= 100; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"code":"th_%d","name":"模型%d"}`, i, i)
	}
	b.WriteString(`]}`)
	return b.String()
}
