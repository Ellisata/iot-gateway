// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/service"
)

func checksumContext(t *testing.T, header string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/openApi/plugin/importPlugin", nil)
	if header != "" {
		c.Request.Header.Set(pluginChecksumHeader, header)
	}
	return c
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestVerifyPluginChecksumAcceptsMatchingHash 摘要一致时放行。
func TestVerifyPluginChecksumAcceptsMatchingHash(t *testing.T) {
	data := []byte("plugin-binary")
	c := checksumContext(t, sha256Of(data))

	err := verifyPluginChecksum(c, []service.PluginUpload{{Name: "iot-driver-abb", Data: data}})
	if err != nil {
		t.Fatalf("matching checksum rejected: %v", err)
	}
}

// TestVerifyPluginChecksumRejectsMismatch 摘要不一致时按 90007 拒绝。
//
// 这是传输完整性的唯一执行点：放过去就等于把一个可能被截断/篡改的二进制
// 写进会自动加载的目录。
func TestVerifyPluginChecksumRejectsMismatch(t *testing.T) {
	c := checksumContext(t, sha256Of([]byte("expected")))

	err := verifyPluginChecksum(c, []service.PluginUpload{{Name: "iot-driver-abb", Data: []byte("tampered")}})
	appErr, ok := err.(*appError.AppError)
	if !ok {
		t.Fatalf("error = %T, want *appError.AppError", err)
	}
	if appErr.Code != enums.PluginChecksumMismatchEnum.GetCode() {
		t.Errorf("code = %q, want %q", appErr.Code, enums.PluginChecksumMismatchEnum.GetCode())
	}
}

// TestVerifyPluginChecksumRejectsMultiFile 一个头无法描述多个文件，带头的多文件
// 上传必须整单拒绝，而不是随便挑一个文件去比。
func TestVerifyPluginChecksumRejectsMultiFile(t *testing.T) {
	c := checksumContext(t, sha256Of([]byte("a")))
	files := []service.PluginUpload{
		{Name: "iot-driver-a", Data: []byte("a")},
		{Name: "iot-driver-b", Data: []byte("b")},
	}

	err := verifyPluginChecksum(c, files)
	appErr, ok := err.(*appError.AppError)
	if !ok {
		t.Fatalf("error = %T, want *appError.AppError", err)
	}
	if appErr.Code != enums.ParamValidEnum.GetCode() {
		t.Errorf("code = %q, want param error %q", appErr.Code, enums.ParamValidEnum.GetCode())
	}
}

// TestVerifyPluginChecksumSkipsWhenAbsent 不带头的调用（管理端手工上传）行为不变。
func TestVerifyPluginChecksumSkipsWhenAbsent(t *testing.T) {
	c := checksumContext(t, "")

	if err := verifyPluginChecksum(c, []service.PluginUpload{{Name: "x", Data: []byte("x")}}); err != nil {
		t.Fatalf("header absent should skip checksum: %v", err)
	}
}

// TestVerifyPluginChecksumDefersRejectedFile 文件在读取阶段已被判定超限时，
// 校验和不该掩盖真正的原因（超限），交给服务层记录。
func TestVerifyPluginChecksumDefersRejectedFile(t *testing.T) {
	c := checksumContext(t, "deadbeef")

	err := verifyPluginChecksum(c, []service.PluginUpload{{Name: "x", RejectReason: "too large"}})
	if err != nil {
		t.Fatalf("rejected file should defer to service layer, got: %v", err)
	}
}
