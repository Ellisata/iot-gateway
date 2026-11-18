// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/model/po"
)

// TestUninstallRemovesLoadedPlugin 卸载已加载插件：进程退役、注册摘除、
// 文件删除，并触发一次采集刷新。
//
// 协议行**不在本层删除**（设备引用判断在服务层），这里顺带钉住这个分层：
// 卸载后 iot_protocol 行仍在。
func TestUninstallRemovesLoadedPlugin(t *testing.T) {
	reload := &fakeReloader{}
	reload.running.Store(true)
	m, db, _ := newTestManager(t, reload)

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("Status 条目数 = %d, want 1", len(statuses))
	}
	file := filepath.Base(statuses[0].Path)

	if err := m.Uninstall(file); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(statuses[0].Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("插件文件仍存在（err=%v）", err)
	}
	if _, err := driver.Create(fixtureProtocol); err == nil {
		t.Error("协议注册未被摘除")
	}
	if got := m.Status(); len(got) != 0 {
		t.Errorf("卸载后 Status 条目数 = %d, want 0", len(got))
	}

	// 驱动集变化必须触发采集刷新（防抖窗口 50ms）。
	waitFor(t, 3*time.Second, "卸载后触发采集刷新", func() bool {
		return reload.calls.Load() >= 1
	})

	// 分层契约：pluginhost 不删协议行。
	var row po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&row).Error; err != nil {
		t.Errorf("iot_protocol 行不应由 pluginhost 删除: %v", err)
	}
}

// TestUninstallRejectsInvalidNames 只接受插件目录内的、匹配 glob 的文件名。
func TestUninstallRejectsInvalidNames(t *testing.T) {
	m, _, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	for _, name := range []string{"../iot-driver-modbus", `..\iot-driver-modbus`, "", ".", ".."} {
		if err := m.Uninstall(name); err == nil {
			t.Errorf("Uninstall(%q) 应被拒绝", name)
		}
	}
	if err := m.Uninstall("not-a-plugin.txt"); err == nil {
		t.Error("不匹配 glob 的文件名应被拒绝")
	}

	// 合法名字但文件不存在、也未加载。
	if err := m.Uninstall("iot-driver-missing"); !errors.Is(err, ErrPluginNotFound) {
		t.Errorf("Uninstall(不存在的插件) = %v, want ErrPluginNotFound", err)
	}

	// 拒绝路径不得误伤已加载插件。
	if got := m.Status(); len(got) != 1 {
		t.Errorf("非法请求后 Status 条目数 = %d, want 1", len(got))
	}
}
