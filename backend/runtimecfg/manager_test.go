// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package runtimecfg

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/model/po"
)

func testConfig() *configFile.Config {
	return &configFile.Config{
		Discovery:  configFile.DiscoveryConfig{Enabled: true},
		DeviceScan: configFile.DeviceScanConfig{Enabled: true},
		Push:       configFile.PushConfig{ChangeOnly: true},
		Plugin:     configFile.PluginConfig{Enabled: true},
	}
}

// fakeStore 内存实现，用来把「落库失败 / 回滚」这类分支测出来。
type fakeStore struct {
	rows       map[Key]po.SystemConfig
	failSave   bool
	failDelete bool
	failList   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: make(map[Key]po.SystemConfig)}
}

func (s *fakeStore) List(context.Context) ([]po.SystemConfig, error) {
	if s.failList {
		return nil, errors.New("list failed")
	}
	out := make([]po.SystemConfig, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, row)
	}
	return out, nil
}

func (s *fakeStore) Save(_ context.Context, key Key, value, updatedBy, updatedAt string) error {
	if s.failSave {
		return errors.New("save failed")
	}
	s.rows[key] = po.SystemConfig{Key: string(key), Value: value, UpdatedBy: updatedBy, UpdatedAt: updatedAt}
	return nil
}

func (s *fakeStore) Delete(_ context.Context, key Key) error {
	if s.failDelete {
		return errors.New("delete failed")
	}
	delete(s.rows, key)
	return nil
}

func TestManagerUsesDefaultWhenNoOverride(t *testing.T) {
	m := newManager(testConfig(), newFakeStore())

	if !m.Bool(KeyDiscoveryEnabled) || !m.Bool(KeyDeviceScanEnabled) ||
		!m.Bool(KeyPushChangeOnly) || !m.Bool(KeyPluginEnabled) {
		t.Fatal("没有覆盖行时应全部取 default.yaml 的值（此处均为 true）")
	}
	fields := m.List()
	if len(fields) != len(All()) {
		t.Fatalf("List 返回 %d 项，期望 %d 项", len(fields), len(All()))
	}
	for _, f := range fields {
		if f.Source != "default" {
			t.Errorf("%s source=%q，期望 default", f.Spec.Key, f.Source)
		}
		if f.PendingRestart {
			t.Errorf("%s 不应有 pendingRestart", f.Spec.Key)
		}
	}
}

func TestManagerUpdateHotReadThrough(t *testing.T) {
	store := newFakeStore()
	m := newManager(testConfig(), store)

	restart, err := m.Update(context.Background(), KeyDeviceScanEnabled, json.RawMessage("false"), "tester")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if restart {
		t.Fatal("deviceScan.enabled 应即时生效，不应要求重启")
	}
	if m.Bool(KeyDeviceScanEnabled) {
		t.Fatal("生效值没有变成 false")
	}
	row, ok := store.rows[KeyDeviceScanEnabled]
	if !ok || row.Value != "false" || row.UpdatedBy != "tester" {
		t.Fatalf("覆盖行不正确: %+v", row)
	}

	f := findField(t, m.List(), KeyDeviceScanEnabled)
	if f.Source != "override" || f.Value {
		t.Fatalf("列表状态不正确: %+v", f)
	}
}

func TestManagerRestartOnlyPendingRestart(t *testing.T) {
	store := newFakeStore()
	m := newManager(testConfig(), store)

	restart, err := m.Update(context.Background(), KeyPluginEnabled, json.RawMessage("false"), "tester")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !restart {
		t.Fatal("plugin.enabled 应要求重启")
	}
	if m.Bool(KeyPluginEnabled) {
		t.Fatal("生效值（重启后要用的值）应是 false")
	}
	if !m.BootBool(KeyPluginEnabled) {
		t.Fatal("启动值应保持 true")
	}
	f := findField(t, m.List(), KeyPluginEnabled)
	if !f.PendingRestart || f.Hot {
		t.Fatalf("restart-only 项应标记 pendingRestart 且 Hot=false: %+v", f)
	}
}

func TestManagerResetRestoresDefault(t *testing.T) {
	store := newFakeStore()
	m := newManager(testConfig(), store)

	if _, err := m.Update(context.Background(), KeyPluginEnabled, json.RawMessage("false"), "tester"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	restart, err := m.Reset(context.Background(), KeyPluginEnabled, "tester")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !restart {
		t.Fatal("重置 plugin.enabled 仍应要求重启")
	}
	if !m.Bool(KeyPluginEnabled) {
		t.Fatal("重置后应回到 default.yaml 的 true")
	}
	if _, ok := store.rows[KeyPluginEnabled]; ok {
		t.Fatal("重置应删除覆盖行")
	}
	f := findField(t, m.List(), KeyPluginEnabled)
	if f.Source != "default" || f.PendingRestart {
		t.Fatalf("重置后状态不正确: %+v", f)
	}

	// 没有覆盖行时 reset 是幂等无操作。
	restart, err = m.Reset(context.Background(), KeyPluginEnabled, "tester")
	if err != nil || restart {
		t.Fatalf("重复 Reset 应为无操作（restart=%v, err=%v）", restart, err)
	}
}

func TestManagerRejectsUnknownAndInvalidValues(t *testing.T) {
	m := newManager(testConfig(), newFakeStore())

	cases := []struct {
		name string
		key  Key
		raw  string
		want error
	}{
		{"未知 key", Key("nope"), `true`, ErrUnknownKey},
		{"字符串布尔", KeyPluginEnabled, `"false"`, ErrInvalidValue},
		{"数字", KeyPluginEnabled, `1`, ErrInvalidValue},
		{"null", KeyPluginEnabled, `null`, ErrInvalidValue},
		{"空值", KeyPluginEnabled, ``, ErrInvalidValue},
		{"尾随垃圾", KeyPluginEnabled, `true false`, ErrInvalidValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Update(context.Background(), tc.key, json.RawMessage(tc.raw), "tester")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v，期望 %v", err, tc.want)
			}
		})
	}
}

func TestManagerApplyFailureRollsBack(t *testing.T) {
	store := newFakeStore()
	m := newManager(testConfig(), store)

	applied := []bool{}
	m.RegisterApplier(KeyDiscoveryEnabled, func(_, new any) error {
		b, _ := new.(bool)
		applied = append(applied, b)
		if !b {
			return errors.New("listen failed")
		}
		return nil
	})

	restart, err := m.Update(context.Background(), KeyDiscoveryEnabled, json.RawMessage("false"), "tester")
	if !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("err=%v，期望 ErrApplyFailed", err)
	}
	if restart {
		t.Fatal("失败时不应返回 requiresRestart")
	}
	if !m.Bool(KeyDiscoveryEnabled) {
		t.Fatal("applier 失败后内存值应回滚为 true")
	}
	if _, ok := store.rows[KeyDiscoveryEnabled]; ok {
		t.Fatal("applier 失败后不应留下覆盖行")
	}
	if len(applied) == 0 || !applied[len(applied)-1] {
		t.Fatalf("应尝试用旧值重新应用，实际调用: %v", applied)
	}
}

func TestManagerApplyFailureRestoresPreviousOverride(t *testing.T) {
	store := newFakeStore()
	m := newManager(testConfig(), store)

	fail := false
	m.RegisterApplier(KeyDiscoveryEnabled, func(_, new any) error {
		if fail {
			return errors.New("listen failed")
		}
		return nil
	})

	if _, err := m.Update(context.Background(), KeyDiscoveryEnabled, json.RawMessage("false"), "first"); err != nil {
		t.Fatalf("第一次 Update: %v", err)
	}
	fail = true
	if _, err := m.Update(context.Background(), KeyDiscoveryEnabled, json.RawMessage("true"), "second"); !errors.Is(err, ErrApplyFailed) {
		t.Fatalf("第二次 Update err=%v，期望 ErrApplyFailed", err)
	}
	if m.Bool(KeyDiscoveryEnabled) {
		t.Fatal("失败后应回滚到上一次覆盖值 false")
	}
	row, ok := store.rows[KeyDiscoveryEnabled]
	if !ok || row.Value != "false" || row.UpdatedBy != "first" {
		t.Fatalf("回滚后覆盖行不正确: %+v", row)
	}
}

func TestManagerRequiresApplierForHotAction(t *testing.T) {
	m := newManager(testConfig(), newFakeStore())
	_, err := m.Update(context.Background(), KeyDiscoveryEnabled, json.RawMessage("false"), "tester")
	if err == nil {
		t.Fatal("标记为 hot-action 但没有 applier 时应拒绝修改（接线错误）")
	}
}

func TestManagerPersistFailureDoesNotChangeBehavior(t *testing.T) {
	store := newFakeStore()
	store.failSave = true
	m := newManager(testConfig(), store)

	_, err := m.Update(context.Background(), KeyDeviceScanEnabled, json.RawMessage("false"), "tester")
	if !errors.Is(err, ErrPersist) {
		t.Fatalf("err=%v，期望 ErrPersist", err)
	}
	if !m.Bool(KeyDeviceScanEnabled) {
		t.Fatal("落库失败时运行行为不应改变")
	}
}

func TestManagerListFailureFallsBackToDefaults(t *testing.T) {
	store := newFakeStore()
	store.failList = true
	m := newManager(testConfig(), store)
	if !m.Bool(KeyPushChangeOnly) {
		t.Fatal("读取覆盖表失败时应退回 default.yaml")
	}
}

func TestManagerIgnoresUnknownAndInvalidRows(t *testing.T) {
	store := newFakeStore()
	store.rows[Key("removed.key")] = po.SystemConfig{Key: "removed.key", Value: "true"}
	store.rows[KeyDeviceScanEnabled] = po.SystemConfig{Key: string(KeyDeviceScanEnabled), Value: `"oops"`}

	m := newManager(testConfig(), store)
	if !m.Bool(KeyDeviceScanEnabled) {
		t.Fatal("非法覆盖值应被忽略，退回默认值")
	}
}

func TestManagerPersistsAcrossRestart(t *testing.T) {
	db := openTestDB(t)
	cfg := testConfig()

	m1 := NewManager(db, cfg)
	if _, err := m1.Update(context.Background(), KeyDeviceScanEnabled, json.RawMessage("false"), "tester"); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// 模拟重启：同一张表、同一份默认配置，重新构造管理器。
	m2 := NewManager(db, cfg)
	if m2.Bool(KeyDeviceScanEnabled) {
		t.Fatal("重启后应仍读到覆盖值 false")
	}
	if _, err := m2.Reset(context.Background(), KeyDeviceScanEnabled, "tester"); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	m3 := NewManager(db, cfg)
	if !m3.Bool(KeyDeviceScanEnabled) {
		t.Fatal("重置并重启后应回到默认值 true")
	}
}

func TestManagerMissingTableFallsBack(t *testing.T) {
	db := openTestDBNoMigrate(t)
	m := NewManager(db, testConfig())
	if !m.Bool(KeyDeviceScanEnabled) {
		t.Fatal("system_config 表缺失时应退回默认值而不是崩溃")
	}
}

func TestManagerCloseRejectsUpdates(t *testing.T) {
	m := newManager(testConfig(), newFakeStore())
	m.Close()
	if _, err := m.Update(context.Background(), KeyDeviceScanEnabled, json.RawMessage("false"), "tester"); !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v，期望 ErrClosed", err)
	}
}

func findField(t *testing.T, fields []Field, key Key) Field {
	t.Helper()
	for _, f := range fields {
		if f.Spec.Key == key {
			return f
		}
	}
	t.Fatalf("列表里没有 %s", key)
	return Field{}
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTestDBNoMigrate(t)
	if err := db.AutoMigrate(&po.SystemConfig{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

func openTestDBNoMigrate(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtimecfg_test.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	return db
}
