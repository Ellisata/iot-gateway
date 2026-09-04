// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"errors"
	"strings"
	"testing"
)

func TestGeneralStatusErrorText(t *testing.T) {
	// 状态 0x04：路径错误/变量未找到（真机最常见）
	err := NewGeneralStatusError(0x04)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "tag not found") {
		t.Errorf("error message should hint tag not found, got: %v", err)
	}
}

func TestGeneralStatusErrorUnknown(t *testing.T) {
	err := NewGeneralStatusError(0x7F)
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error message should mark unknown status, got: %v", err)
	}
}

func TestIsGeneralStatusError(t *testing.T) {
	if !IsGeneralStatusError(NewGeneralStatusError(0x04)) {
		t.Fatal("expected true for GeneralStatusError")
	}
	if IsGeneralStatusError(errors.New("some network error")) {
		t.Fatal("expected false for unrelated error")
	}
	if IsGeneralStatusError(nil) {
		t.Fatal("expected false for nil")
	}
}
