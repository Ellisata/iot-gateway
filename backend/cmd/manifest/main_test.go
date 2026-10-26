// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

func TestFrameMagicString(t *testing.T) {
	if got := frameMagicString(pluginwire.FrameMagic); got != "IGP2" {
		t.Errorf("frameMagicString(FrameMagic) = %q, want IGP2", got)
	}
	if got := frameMagicString(0x00010203); got != "0x00010203" {
		t.Errorf("不可打印魔数应退回十六进制，got %q", got)
	}
}

func TestReadVersionFileTrimsCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drivers.version")
	if err := os.WriteFile(path, []byte("v0.1.0\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readVersionFile(path)
	if err != nil {
		t.Fatalf("readVersionFile: %v", err)
	}
	if got != "v0.1.0" {
		t.Errorf("readVersionFile = %q, want v0.1.0（CRLF 必须被 trim）", got)
	}
}

func TestReadVersionFileEmptyPath(t *testing.T) {
	got, err := readVersionFile("")
	if err != nil {
		t.Fatalf("空路径不应报错: %v", err)
	}
	if got != "" {
		t.Errorf("空路径应返回空串，got %q", got)
	}
}

func TestListPlugins(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("iot-driver-modbus", "modbus")
	write("iot-driver-s7.exe", "s7")
	write("README.txt", "ignored")
	if err := os.Mkdir(filepath.Join(dir, "iot-driver-subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	plugins, err := listPlugins(dir, "v0.1.0")
	if err != nil {
		t.Fatalf("listPlugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("len(plugins) = %d, want 2（只认 iot-driver-* 普通文件）", len(plugins))
	}
	if plugins[0].Name != "iot-driver-modbus" || plugins[1].Name != "iot-driver-s7" {
		t.Errorf("插件名应去掉 .exe 并按名排序，got %q, %q", plugins[0].Name, plugins[1].Name)
	}
	if plugins[0].Version != "v0.1.0" {
		t.Errorf("Version = %q, want v0.1.0", plugins[0].Version)
	}

	sum := sha256.Sum256([]byte("modbus"))
	if plugins[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %q, want %q", plugins[0].SHA256, hex.EncodeToString(sum[:]))
	}
}

func TestListPluginsEmptyDir(t *testing.T) {
	plugins, err := listPlugins("", "v0.1.0")
	if err != nil {
		t.Fatalf("空目录参数不应报错: %v", err)
	}
	if plugins == nil || len(plugins) != 0 {
		t.Errorf("空目录应返回空切片而不是 nil，got %#v", plugins)
	}
}
