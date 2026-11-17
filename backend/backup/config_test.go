// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/database/sqlite"
)

func execTestSQL(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlite.CloseDB(db)
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

func countRows(t *testing.T, path, table string) int64 {
	t.Helper()
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open db %s: %v", path, err)
	}
	defer sqlite.CloseDB(db)
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM " + table).Scan(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func seedRuntimeTables(t *testing.T, path string) {
	t.Helper()
	execTestSQL(t, path,
		`CREATE TABLE IF NOT EXISTS push_outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, channel_id TEXT NOT NULL DEFAULT '', payload TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS alarm (id TEXT PRIMARY KEY, target_id TEXT NOT NULL DEFAULT '')`,
	)
}

func TestConfigBundleExcludesRuntimeTables(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "config-scope")
	seedRuntimeTables(t, dbPath)
	execTestSQL(t, dbPath,
		`INSERT INTO push_outbox(channel_id, payload) VALUES ('ch-1', 'cached')`,
		`INSERT INTO alarm(id, target_id) VALUES ('alarm-1', 'dev-1')`,
	)

	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeConfig,
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle config: %v", err)
	}
	if b.Manifest.Scope != ScopeConfig {
		t.Fatalf("scope = %q, want config", b.Manifest.Scope)
	}

	vb, err := VerifyBundle(b.Path)
	if err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	defer vb.Cleanup()

	if got := countRows(t, vb.DBPath, "device"); got != 1 {
		t.Fatalf("device rows = %d, want 1", got)
	}
	// 运行表存在于 schema（迁移器建出），但白名单拷贝不会带入任何行。
	if got := countRows(t, vb.DBPath, "push_outbox"); got != 0 {
		t.Fatalf("push_outbox rows = %d, want 0", got)
	}
	if got := countRows(t, vb.DBPath, "alarm"); got != 0 {
		t.Fatalf("alarm rows = %d, want 0", got)
	}
}

func TestConfigRestorePreservesRuntimeTables(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "配置-旧值")
	seedRuntimeTables(t, dbPath)
	execTestSQL(t, dbPath, `INSERT INTO push_outbox(channel_id, payload) VALUES ('ch-1', 'keep-1')`)

	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeConfig,
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle config: %v", err)
	}

	// 备份之后改配置、再写一条运行数据。
	updateDeviceName(t, dbPath, "配置-新值")
	execTestSQL(t, dbPath, `INSERT INTO push_outbox(channel_id, payload) VALUES ('ch-2', 'keep-2')`)

	result, err := Restore(context.Background(), RestoreOptions{
		BundlePath: b.Path,
		DBPath:     dbPath,
		Identity:   identityTakeover,
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Restore config: %v", err)
	}
	if got := queryDeviceName(t, dbPath); got != "配置-旧值" {
		t.Fatalf("restored device = %q, want 配置-旧值", got)
	}
	if got := countRows(t, dbPath, "push_outbox"); got != 2 {
		t.Fatalf("push_outbox rows = %d, want 2 (runtime data must survive config restore)", got)
	}
	if !contains(result.TablesRestored, "device") {
		t.Fatalf("TablesRestored = %v, want to include device", result.TablesRestored)
	}
	if len(result.PreRestore) == 0 {
		t.Fatal("expected pre-restore full copy")
	}

	// pre-restore 副本是恢复前的完整库：应能看到“新值”和两条 outbox。
	pre := result.PreRestore[0]
	if got := queryDeviceName(t, pre); got != "配置-新值" {
		t.Fatalf("pre-restore device = %q, want 配置-新值", got)
	}
	if got := countRows(t, pre, "push_outbox"); got != 2 {
		t.Fatalf("pre-restore outbox rows = %d, want 2", got)
	}
}

func TestTableClassificationIsComplete(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "schema.db")
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := sqlite.Migrate(db); err != nil {
		sqlite.CloseDB(db)
		t.Fatalf("migrate: %v", err)
	}

	var tables []string
	if err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).
		Scan(&tables).Error; err != nil {
		sqlite.CloseDB(db)
		t.Fatalf("list tables: %v", err)
	}
	sqlite.CloseDB(db)

	for _, table := range tables {
		if !IsKnownTable(table) {
			t.Errorf("table %q is neither a config table nor a runtime table; update tables.go", table)
		}
		if IsConfigTable(table) && contains(runtimeTables, table) {
			t.Errorf("table %q is classified as both config and runtime", table)
		}
	}
	if len(tables) == 0 {
		t.Fatal("no tables found after migrate")
	}
}

func TestPendingStageAndApply(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "pending-旧值")

	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeConfig,
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	updateDeviceName(t, dbPath, "pending-新值")

	info, err := StageRestore(filepath.Dir(dbPath), backupDir, b.Path, identityTakeover, "tester", time.Now())
	if err != nil {
		t.Fatalf("StageRestore: %v", err)
	}
	if info.State != pendingStatePending || info.Scope != ScopeConfig {
		t.Fatalf("pending = %+v", info)
	}

	cfg := &configFile.Config{
		SQLite: configFile.SQLiteConfig{Path: dbPath},
		Backup: configFile.BackupConfig{Enabled: true, Dir: backupDir},
	}
	lock, err := AcquireLock(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	// 模拟 main：持有数据目录锁后应用 pending（ApplyPendingRestore 不再取锁）。
	if err := ApplyPendingRestore(context.Background(), cfg, "test"); err != nil {
		lock.Release()
		t.Fatalf("ApplyPendingRestore: %v", err)
	}
	lock.Release()

	if got := queryDeviceName(t, dbPath); got != "pending-旧值" {
		t.Fatalf("device after pending restore = %q, want pending-旧值", got)
	}
	remaining, err := ReadPending(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if remaining != nil {
		t.Fatalf("pending task still present after apply: %+v", remaining)
	}
}

func TestApplyRetentionPrefersConfigAndProtectsFull(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"iot-gateway_ab12cd34_20260101-000000_full.igbak",
		"iot-gateway_ab12cd34_20260102-000000_config.igbak",
		"iot-gateway_ab12cd34_20260103-000000_full.igbak",
		"iot-gateway_ab12cd34_20260104-000000_config.igbak",
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for i, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		ts := base.Add(time.Duration(i) * 24 * time.Hour)
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	// 4 份、keep=3：优先删最旧的 config，两份 full 都保留。
	deleted, err := ApplyRetention(dir, 3, 0)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != names[1] {
		t.Fatalf("deleted = %v, want [%s]", deleted, names[1])
	}
	for _, name := range []string{names[0], names[2], names[3]} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s removed: %v", name, err)
		}
	}
}

func TestWatcherNotifyFiltersRuntimeTables(t *testing.T) {
	w := NewWatcher(nil, "")
	w.Notify("push_outbox")
	select {
	case <-w.kick:
		t.Fatal("runtime table write should not trigger config backup")
	default:
	}
	w.Notify("device")
	select {
	case <-w.kick:
	default:
		t.Fatal("config table write should trigger a pending backup")
	}
}

func TestStageRestoreRejectsSecondPending(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "x")
	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath: dbPath, BackupDir: backupDir, Scope: ScopeConfig, WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	dataDir := filepath.Dir(dbPath)
	if _, err := StageRestore(dataDir, backupDir, b.Path, identityTakeover, "a", time.Now()); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	if _, err := StageRestore(dataDir, backupDir, b.Path, identityTakeover, "b", time.Now()); err == nil {
		t.Fatal("second stage should fail while a pending task exists")
	}
}

func TestApplyPendingRestoreKeepsOldDBOnFailure(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "still-here")

	dataDir := filepath.Dir(dbPath)
	pending := filepath.Join(dataDir, pendingDirName)
	if err := os.MkdirAll(pending, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 伪造一个损坏的包：校验必须失败，pending 目录改名，旧库不受影响。
	if err := os.WriteFile(filepath.Join(pending, pendingBundleName), []byte("not a zip"), 0600); err != nil {
		t.Fatalf("write fake bundle: %v", err)
	}
	meta := &pendingMeta{State: pendingStatePending, Scope: ScopeConfig, Identity: identityTakeover}
	if err := writePendingMeta(pending, meta); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	cfg := &configFile.Config{
		SQLite: configFile.SQLiteConfig{Path: dbPath},
		Backup: configFile.BackupConfig{Enabled: true, Dir: backupDir},
	}
	if err := ApplyPendingRestore(context.Background(), cfg, "test"); err == nil {
		t.Fatal("expected failure for corrupt bundle")
	}
	if got := queryDeviceName(t, dbPath); got != "still-here" {
		t.Fatalf("old database changed after failed pending restore: %q", got)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	foundFailed := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), pendingDirName+".failed-") {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Fatalf("failed pending directory not preserved: %v", entries)
	}
}

func TestWriteHookNotifiesConfigTables(t *testing.T) {
	var mu sync.Mutex
	var got []string
	sqlite.RegisterWriteHook(func(table string) {
		mu.Lock()
		got = append(got, table)
		mu.Unlock()
	})
	t.Cleanup(func() { sqlite.RegisterWriteHook(nil) })

	// 用 OpenStandalone 的库不会安装回调；这里直接验证登记 + 回调链路。
	sqlite.NotifyWrite("device")
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "device" {
		t.Fatalf("hook got %v, want [device]", got)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
