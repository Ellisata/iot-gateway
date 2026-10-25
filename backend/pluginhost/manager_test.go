// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/configFile"
	"iot-gateway/model/po"
)

// ==================== 测试辅助 ====================

// fakeReloader 记录 Refresh 调用，用于断言防抖与启动期不刷新。
type fakeReloader struct {
	running atomic.Bool
	calls   atomic.Int64
}

func (f *fakeReloader) Refresh() error  { f.calls.Add(1); return nil }
func (f *fakeReloader) IsRunning() bool { return f.running.Load() }

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 用临时文件库而非 :memory:：内存 SQLite 每个连接一份，会引入跨连接不可见问题。
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	for _, m := range []any{&po.IotProtocol{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return db
}

// newTestManager 安装夹具插件并把 Manager 指向该目录。
func newTestManager(t *testing.T, reload Reloader) (*Manager, *gorm.DB, string) {
	t.Helper()
	pluginPath := installFixture(t)
	dir := filepath.Dir(pluginPath)

	db := newTestDB(t)
	cfg := &configFile.Config{
		Plugin: configFile.PluginConfig{
			Enabled:            true,
			Dir:                dir, // 绝对路径：跳过相对可执行文件的解析
			Glob:               "iot-driver-*",
			ScanIntervalMs:     3600000, // 测试用 Reload 手动扫描，不靠 ticker
			AllowInsecureDir:   true,    // 临时目录在部分平台上权限位不满足检查
			HandshakeTimeoutMs: 10000,
		},
	}
	m := NewManager(db, cfg, reload)
	m.opts.Env = append(pluginEnv(), envFixture+"=1")
	m.refreshDelay = 50 * time.Millisecond

	t.Cleanup(m.Stop)
	return m, db, dir
}

// waitForDriverRegistered 等驱动注册表里出现该协议。
func waitForDriverRegistered(t *testing.T, protocol string) {
	t.Helper()
	waitFor(t, 10*time.Second, "协议 "+protocol+" 完成注册", func() bool {
		_, err := driver.Create(protocol)
		return err == nil
	})
}

// ==================== 测试 ====================

// TestManagerDisabledIsNoOp 默认关闭时必须是彻底的静默无操作。
func TestManagerDisabledIsNoOp(t *testing.T) {
	db := newTestDB(t)
	reload := &fakeReloader{}
	m := NewManager(db, &configFile.Config{}, reload) // Enabled=false

	if err := m.Start(); err != nil {
		t.Fatalf("禁用状态下 Start 不应报错: %v", err)
	}
	m.Stop()

	if n := reload.calls.Load(); n != 0 {
		t.Errorf("禁用状态下不应触发刷新，实际 %d 次", n)
	}
	if got := m.Status(); len(got) != 0 {
		t.Errorf("禁用状态下不应加载任何插件，实际 %d 个", len(got))
	}
}

// TestManagerDiscoversAndRegisters 端到端：丢一个插件文件进目录 →
// 驱动可创建、类型可查询、协议行已建。
func TestManagerDiscoversAndRegisters(t *testing.T) {
	reload := &fakeReloader{}
	m, db, _ := newTestManager(t, reload)

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	// 驱动实例可创建并可用。
	d, err := driver.Create(fixtureProtocol)
	if err != nil {
		t.Fatalf("driver.Create: %v", err)
	}
	if d == nil {
		t.Fatal("driver.Create 返回 nil")
	}

	// 类型 scope 已注册，类型下拉接口可用。
	if got := driver.ScopeOf(fixtureProtocol); got != "fixture" {
		t.Errorf("ScopeOf = %q, want fixture", got)
	}
	common, extended := driver.AvailableTypes("fixture")
	if len(common) != 2 || common[0] != driver.TypeBoolean || common[1] != driver.TypeShort {
		t.Errorf("AvailableTypes common = %v, want [Boolean Short]", common)
	}
	// "Bool" 是插件定义的扩展类型（不在 CommonDataTypes 中）。
	if len(extended) != 1 || extended[0] != "Bool" {
		t.Errorf("AvailableTypes extended = %v, want [Bool]", extended)
	}
	// 地址校验路径可用。
	if internal, ok := driver.LookupDataType(fixtureProtocol, "Short"); !ok || internal != "int16" {
		t.Errorf("LookupDataType(Short) = %q,%v; want int16,true", internal, ok)
	}
	// 采集读取路径的类型兜底可用。
	if got := driver.NormalizeAddressType(fixtureProtocol, sdkAddr(po.DeviceAddress{DataType: "int16"})); got != "int16" {
		t.Errorf("NormalizeAddressType = %q, want int16", got)
	}

	// iot_protocol 行已自动建立（前端协议下拉才能看到它）。
	var row po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&row).Error; err != nil {
		t.Fatalf("iot_protocol 行未建立: %v", err)
	}
	if row.FormJSON == "" {
		t.Errorf("协议行内容不完整: %+v", row)
	}
	if row.Sort != defaultPluginRowSort {
		t.Errorf("Sort = %d, want %d（插件协议应排在内置之后）", row.Sort, defaultPluginRowSort)
	}

	// Status 诊断可用且带 SHA-256 审计信息。
	st := m.Status()
	if len(st) != 1 {
		t.Fatalf("Status 条目数 = %d, want 1", len(st))
	}
	if len(st[0].Protocols) != 1 || st[0].Protocols[0] != fixtureProtocol || !st[0].Up || len(st[0].SHA256) != 64 {
		t.Errorf("Status 内容异常: %+v", st[0])
	}
}

// TestManagerUnchangedFileIsNoop 指纹未变时不得重复加载。
func TestManagerUnchangedFileIsNoop(t *testing.T) {
	m, _, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	first := m.Status()[0]
	for i := 0; i < 3; i++ {
		m.Reload()
	}
	after := m.Status()[0]

	if after.Epoch != first.Epoch {
		t.Errorf("未变化的插件不应被重新加载：epoch %d → %d", first.Epoch, after.Epoch)
	}
	if after.SHA256 != first.SHA256 {
		t.Errorf("未变化的插件不应被重新加载：sha 变了")
	}
}

// TestManagerRemovalUnregistersButKeepsRow 插件文件消失：注销运行时能力，
// 但保留 iot_protocol 行。
//
// 不删行的理由：文件操作不该产生破坏性的数据副作用；且既有设备按 protocol_id
// 引用它，删行会让采集引擎每轮为每个设备刷「协议找不到」的警告。
func TestManagerRemovalUnregistersButKeepsRow(t *testing.T) {
	reload := &fakeReloader{}
	m, db, dir := newTestManager(t, reload)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	// 删掉插件文件。
	matches, _ := filepath.Glob(filepath.Join(dir, "iot-driver-*"))
	if len(matches) != 1 {
		t.Fatalf("期望 1 个插件文件，实际 %d", len(matches))
	}
	saved := readFile(t, matches[0])

	// Windows 不允许删除正在运行的 exe（Linux 无此限制）。先停掉插件进程，
	// 模拟"运维停掉/替换插件后文件消失"这一真实可达的状态。
	m.mu.Lock()
	for _, lp := range m.plugins {
		lp.link.retireCurrent()
	}
	m.mu.Unlock()

	if err := os.Remove(matches[0]); err != nil {
		t.Fatalf("删除插件: %v", err)
	}

	m.Reload()

	// 运行时能力已摘除。
	if _, err := driver.Create(fixtureProtocol); err == nil {
		t.Error("插件移除后 driver.Create 应失败")
	}
	if got := driver.ScopeOf(fixtureProtocol); got != "" {
		t.Errorf("插件移除后 ScopeOf 应为空，实际 %q", got)
	}
	if _, ok := driver.LookupDataType(fixtureProtocol, "Short"); ok {
		t.Error("插件移除后类型映射应已注销")
	}
	if got := m.Status(); len(got) != 0 {
		t.Errorf("插件移除后 Status 应为空，实际 %d 条", len(got))
	}

	// 但 iot_protocol 行必须保留。
	var row po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&row).Error; err != nil {
		t.Errorf("插件移除后 iot_protocol 行应保留，实际查询失败: %v", err)
	}

	// 插件被放回后应能重新加载（注销是可逆的）。
	if err := os.WriteFile(matches[0], saved, 0o755); err != nil {
		t.Fatalf("放回插件: %v", err)
	}
	m.Reload()
	waitForDriverRegistered(t, fixtureProtocol)
}

// TestManagerRefreshIsDebouncedAndGuarded 刷新必须防抖，且采集引擎未运行时不得刷新。
func TestManagerRefreshIsDebouncedAndGuarded(t *testing.T) {
	reload := &fakeReloader{}
	m, _, _ := newTestManager(t, reload)

	// 采集引擎未运行：即便发现插件也不应刷新
	//（首次发现早于 CollectorEngine.Start，此时无需刷新）。
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	time.Sleep(200 * time.Millisecond)
	if n := reload.calls.Load(); n != 0 {
		t.Fatalf("采集引擎未运行时不应刷新，实际 %d 次", n)
	}

	// 引擎运行后：多次驱动集变化应合并为一次刷新。
	reload.running.Store(true)

	// 通过反复注册/注销制造多次变化事件。
	for i := 0; i < 5; i++ {
		m.scheduleRefresh()
	}

	waitFor(t, 3*time.Second, "刷新被触发", func() bool { return reload.calls.Load() > 0 })
	time.Sleep(200 * time.Millisecond) // 留出窗口，确认没有第二次

	if n := reload.calls.Load(); n != 1 {
		t.Errorf("5 次变化应合并为 1 次刷新（防抖），实际 %d 次", n)
	}
}

// TestManagerProvisionNeverOverwrites 已存在的协议行绝不被覆盖：
// 运维可能已按现场需求改过表单。
func TestManagerProvisionNeverOverwrites(t *testing.T) {
	m, db, _ := newTestManager(t, &fakeReloader{})

	const customForm = `{"rule":[{"type":"input","field":"custom","title":"运维改过的字段"}],"options":{}}`
	if err := db.Create(&po.IotProtocol{
		ID: "existing-1", Name: fixtureProtocol, Description: "运维写的说明",
		FormJSON: customForm, Sort: 7,
		CreatedAt: "2026-01-01 00:00:00", UpdatedAt: "2026-01-01 00:00:00",
	}).Error; err != nil {
		t.Fatalf("预置协议行: %v", err)
	}

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	var row po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&row).Error; err != nil {
		t.Fatalf("查询协议行: %v", err)
	}
	if row.FormJSON != customForm {
		t.Errorf("既有表单被覆盖：\n got: %s\nwant: %s", row.FormJSON, customForm)
	}
	if row.Description != "运维写的说明" {
		t.Errorf("既有说明被覆盖: %q", row.Description)
	}
	if row.Sort != 7 {
		t.Errorf("既有 Sort 被覆盖: %d", row.Sort)
	}
	if row.ID != "existing-1" {
		t.Errorf("不应新建协议行，ID = %q", row.ID)
	}
}

// TestManagerPluginProtocolForm 读取插件声明的表单 —— 「恢复默认表单」的数据来源。
//
// 它与 TestManagerProvisionNeverOverwrites 是一对：那条守着「插件绝不覆盖运维的表单」，
// 这条提供运维主动采纳插件表单的那一份，两者合起来才是完整的语义。
func TestManagerPluginProtocolForm(t *testing.T) {
	m, db, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	got, err := m.PluginProtocolForm(fixtureProtocol)
	if err != nil {
		t.Fatalf("PluginProtocolForm: %v", err)
	}
	if !json.Valid(got) {
		t.Fatalf("返回的表单不是合法 JSON: %s", got)
	}

	// 必须与 provision 写进库里的完全一致，否则「恢复默认」会恢复出另一份东西。
	var row po.IotProtocol
	if err := db.Where("name = ?", fixtureProtocol).First(&row).Error; err != nil {
		t.Fatalf("查询协议行: %v", err)
	}
	if string(got) != row.FormJSON {
		t.Errorf("插件声明的表单与库内不一致：\n plugin: %s\n row:    %s", got, row.FormJSON)
	}

	// 无插件提供该协议：哨兵错误，供上层翻译成「该协议没有已加载的插件提供」。
	if _, err := m.PluginProtocolForm("No.Such.Protocol"); !errors.Is(err, ErrProtocolNotProvisioned) {
		t.Errorf("未知协议应返回 ErrProtocolNotProvisioned，实际 %v", err)
	}

	// 返回的是副本：调用方改写不得污染已加载插件的 meta。
	got[0] = 'X'
	again, err := m.PluginProtocolForm(fixtureProtocol)
	if err != nil {
		t.Fatalf("二次读取: %v", err)
	}
	if again[0] == 'X' {
		t.Error("返回的表单被调用方改写后，污染了已加载插件的 meta")
	}
}

// TestManagerProvisionDisabled 可关闭协议行自动维护。
func TestManagerProvisionDisabled(t *testing.T) {
	pluginPath := installFixture(t)
	db := newTestDB(t)
	cfg := &configFile.Config{
		Plugin: configFile.PluginConfig{
			Enabled:                        true,
			Dir:                            filepath.Dir(pluginPath),
			Glob:                           "iot-driver-*",
			ScanIntervalMs:                 3600000,
			AllowInsecureDir:               true,
			DisableProtocolRowProvisioning: true,
		},
	}
	m := NewManager(db, cfg, &fakeReloader{})
	m.opts.Env = append(pluginEnv(), envFixture+"=1")
	t.Cleanup(m.Stop)

	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForDriverRegistered(t, fixtureProtocol)

	var count int64
	if err := db.Model(&po.IotProtocol{}).Where("name = ?", fixtureProtocol).Count(&count).Error; err != nil {
		t.Fatalf("统计协议行: %v", err)
	}
	if count != 0 {
		t.Errorf("已关闭供给时不应建行，实际 %d 行", count)
	}
}

// TestManagerRejectsPluginShadowingExistingProtocol 插件不得顶替已被占用的协议名。
//
// 真实二进制里内置协议由 collector 包的空导入注册（早于 Manager.Start），
// 这里用预注册同名协议来复现同一场景 —— 重点是验证被拒之后**没有部分生效**：
// register() 的顺序是 类型注册表 → 类型 scope → 驱动注册表，
// 最后一步失败必须把前两步回滚掉，否则会留下"类型查得到、驱动却创建不了"的脏状态。
func TestManagerRejectsPluginShadowingExistingProtocol(t *testing.T) {
	const existing = fixtureProtocol

	// 预先占用协议名（模拟内置驱动或另一个插件已注册）。
	if err := driver.RegisterExternal(existing, func() driver.Driver { return nil }); err != nil {
		t.Fatalf("预注册协议: %v", err)
	}
	t.Cleanup(func() { driver.Unregister(existing) })

	m, db, _ := newTestManager(t, &fakeReloader{})
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 加载必定失败：不应有任何插件进入已加载状态。
	time.Sleep(1 * time.Second)
	if got := m.Status(); len(got) != 0 {
		t.Fatalf("协议名冲突时不应加载成功，实际 %d 个", len(got))
	}

	// 关键：类型 scope 必须已回滚，不能残留。
	if got := driver.ScopeOf(existing); got != "" {
		t.Errorf("注册失败后类型 scope 应已回滚，实际 ScopeOf = %q", got)
	}
	if _, ok := driver.LookupDataType(existing, "Short"); ok {
		t.Error("注册失败后类型映射应已回滚")
	}

	// 原占用者不受影响。
	if _, err := driver.Create(existing); err != nil {
		t.Errorf("原协议注册项应保持可用: %v", err)
	}
	// 协议行也不应建立（加载在供给之前就失败了）。
	var count int64
	if err := db.Model(&po.IotProtocol{}).Where("name = ?", existing).Count(&count).Error; err != nil {
		t.Fatalf("统计协议行: %v", err)
	}
	if count != 0 {
		t.Errorf("加载失败时不应建立协议行，实际 %d 行", count)
	}
}

// TestResolvePluginDirIsExecutableRelative 相对目录必须相对可执行文件解析，
// 而不是 cwd（Windows 服务由 sc create 注册，cwd 是 System32）。
func TestResolvePluginDirIsExecutableRelative(t *testing.T) {
	// 用一个明显不是默认值的相对路径：若实现只是把默认值原样吐回来，这里会露馅。
	const rel = "my-plugins"
	got, err := resolvePluginDir(rel)
	if err != nil {
		t.Fatalf("resolvePluginDir: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("应返回绝对路径，实际 %q", got)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	want := filepath.Join(filepath.Dir(exe), rel)
	if got != want {
		t.Errorf("resolvePluginDir = %q, want %q（应相对可执行文件而非 cwd）", got, want)
	}

	// 空值走默认目录（跟着常量走，别再抄一遍字面量）；绝对路径原样返回。
	if got, _ := resolvePluginDir(""); got != filepath.Join(filepath.Dir(exe), defaultPluginDir) {
		t.Errorf("空目录应回落到默认值 %q，实际 %q", defaultPluginDir, got)
	}
	abs := filepath.Join(t.TempDir(), "x")
	if got, _ := resolvePluginDir(abs); got != abs {
		t.Errorf("绝对路径应原样返回，实际 %q", got)
	}
}

// ==================== 小工具 ====================

func appendBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatalf("打开 %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatalf("追加写入: %v", err)
	}
	// 确保 mtime 变化可被指纹捕捉。
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return b
}
