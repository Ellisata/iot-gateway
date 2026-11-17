// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/database/sqlite"
	"iot-gateway/logger"
)

func createTestDB(t *testing.T, path, deviceID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer sqlite.CloseDB(db)

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT '')`,
		`INSERT OR REPLACE INTO schema_migrations(version) VALUES ('036_device_address_transform.sql')`,
		`CREATE TABLE IF NOT EXISTS iot_gateway (
			id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, firmware_version TEXT NOT NULL DEFAULT '')`,
		`INSERT OR REPLACE INTO iot_gateway(id, name, firmware_version) VALUES ('abcdef1234567890', 'test-gw', 'v1.0.0')`,
		`CREATE TABLE IF NOT EXISTS device (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	if err := db.Exec(`DELETE FROM device`).Error; err != nil {
		t.Fatalf("clear device: %v", err)
	}
	if err := db.Exec(`INSERT INTO device(id, name) VALUES (?, ?)`, "dev-1", deviceID).Error; err != nil {
		t.Fatalf("insert device: %v", err)
	}
}

func updateDeviceName(t *testing.T, path, name string) {
	t.Helper()
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlite.CloseDB(db)
	if err := db.Exec(`UPDATE device SET name = ? WHERE id = ?`, name, "dev-1").Error; err != nil {
		t.Fatalf("update device: %v", err)
	}
}

func queryDeviceName(t *testing.T, path string) string {
	t.Helper()
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open db %s: %v", path, err)
	}
	defer sqlite.CloseDB(db)
	var name string
	if err := db.Raw(`SELECT name FROM device WHERE id = ?`, "dev-1").Scan(&name).Error; err != nil {
		t.Fatalf("query device: %v", err)
	}
	return name
}

func queryGateway(t *testing.T, path string) (string, string) {
	t.Helper()
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		t.Fatalf("open db %s: %v", path, err)
	}
	defer sqlite.CloseDB(db)
	var row struct {
		ID   string `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT id, name FROM iot_gateway LIMIT 1`).Scan(&row).Error; err != nil {
		t.Fatalf("query gateway: %v", err)
	}
	return row.ID, row.Name
}

func TestCreateAndVerifyBundle(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "泵站-1")

	now := time.Date(2026, 11, 17, 3, 17, 0, 0, time.Local)
	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeFull,
		CreatedBy:  "test",
		WorkingDir: root,
		Now:        now,
	})
	if err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	if _, err := os.Stat(b.Path); err != nil {
		t.Fatalf("bundle not created: %v", err)
	}
	if b.Manifest.Gateway.ID != "abcdef1234567890" {
		t.Fatalf("unexpected gateway id: %q", b.Manifest.Gateway.ID)
	}
	if b.Manifest.SchemaVersion != "036_device_address_transform.sql" {
		t.Fatalf("unexpected schema: %q", b.Manifest.SchemaVersion)
	}
	if len(b.Manifest.DB.SHA256) != 64 {
		t.Fatalf("unexpected db sha: %q", b.Manifest.DB.SHA256)
	}

	vb, err := VerifyBundle(b.Path)
	if err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	defer vb.Cleanup()
	if got := queryDeviceName(t, vb.DBPath); got != "泵站-1" {
		t.Fatalf("snapshot device = %q, want 泵站-1", got)
	}
}

func TestRestoreReplacesDatabaseAndKeepsPreRestoreCopy(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "恢复前-旧值")

	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeFull,
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}

	// 备份之后改库，恢复后应回到备份时的值。
	updateDeviceName(t, dbPath, "恢复前-新值")

	result, err := Restore(context.Background(), RestoreOptions{
		BundlePath: b.Path,
		DBPath:     dbPath,
		Identity:   identityTakeover,
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := queryDeviceName(t, dbPath); got != "恢复前-旧值" {
		t.Fatalf("restored device = %q, want 恢复前-旧值", got)
	}
	if len(result.PreRestore) == 0 {
		t.Fatal("expected pre-restore copy")
	}
	oldDB := result.PreRestore[0]
	if strings.HasSuffix(oldDB, "-wal") || strings.HasSuffix(oldDB, "-shm") {
		oldDB = strings.TrimSuffix(strings.TrimSuffix(oldDB, "-wal"), "-shm")
	}
	if got := queryDeviceName(t, oldDB); got != "恢复前-新值" {
		t.Fatalf("pre-restore device = %q, want 恢复前-新值", got)
	}
}

func TestRestoreRefusesWhileLocked(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "locked")

	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeFull,
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}

	lock, err := AcquireLock(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer lock.Release()

	_, err = Restore(context.Background(), RestoreOptions{
		BundlePath: b.Path,
		DBPath:     dbPath,
		Identity:   identityTakeover,
	})
	if err == nil {
		t.Fatal("Restore succeeded while data directory was locked")
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("error = %v, want ErrLocked", err)
	}
}

func TestRestoreCloneIdentity(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "clone")

	b, err := CreateBundle(context.Background(), CreateOptions{
		DBPath:     dbPath,
		BackupDir:  backupDir,
		Scope:      ScopeFull,
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}

	result, err := Restore(context.Background(), RestoreOptions{
		BundlePath: b.Path,
		DBPath:     dbPath,
		Identity:   identityClone,
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	id, name := queryGateway(t, dbPath)
	if id != result.NewID || id == "abcdef1234567890" {
		t.Fatalf("gateway id = %q, want new %q", id, result.NewID)
	}
	if !strings.HasPrefix(name, "test-gw-copy-") {
		t.Fatalf("gateway name = %q, want test-gw-copy-*", name)
	}
}

func TestCheckSchemaCompatibility(t *testing.T) {
	if err := CheckSchemaCompatibility(sqlite.LatestMigration()); err != nil {
		t.Fatalf("current schema rejected: %v", err)
	}
	if err := CheckSchemaCompatibility("999_future.sql"); err == nil {
		t.Fatal("newer schema was accepted")
	}
	if err := CheckSchemaCompatibility(""); err != nil {
		t.Fatalf("empty schema rejected: %v", err)
	}
}

func TestApplyRetentionKeepsNewestAndForeignFiles(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"iot-gateway_ab12cd34_20260101-000000_full.igbak",
		"iot-gateway_ab12cd34_20260102-000000_full.igbak",
		"iot-gateway_ab12cd34_20260103-000000_full.igbak",
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
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("keep me"), 0600); err != nil {
		t.Fatalf("write foreign: %v", err)
	}

	deleted, err := ApplyRetention(dir, 2, 0)
	if err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != names[0] {
		t.Fatalf("deleted = %v, want [%s]", deleted, names[0])
	}
	if _, err := os.Stat(filepath.Join(dir, names[1])); err != nil {
		t.Fatalf("newer bundle removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, names[2])); err != nil {
		t.Fatalf("newest bundle removed: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file removed: %v", err)
	}
}

func TestSchedulerRunOnceCreatesAndPrunes(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data", "iot-gateway.db")
	backupDir := filepath.Join(root, "backups")
	createTestDB(t, dbPath, "scheduler")
	// 调度器会通过 logger 打日志。logger 的全局初始化在首次 Info 时兜底，
	// 默认写到包目录下的 log/；测试结束时关掉句柄并清掉，避免污染源码树。
	t.Cleanup(func() {
		logger.Close()
		_ = os.RemoveAll("log")
	})
	cfg := &configFile.Config{
		SQLite: configFile.SQLiteConfig{Path: dbPath},
		Backup: configFile.BackupConfig{
			Enabled:      true,
			Dir:          backupDir,
			DefaultScope: ScopeFull,
			Keep:         1,
			MaxTotalMB:   64,
		},
	}
	s := NewScheduler(cfg, "test")
	eff := EffectiveConfig(cfg)
	s.runOnce(eff)
	bundles, err := ListBundles(backupDir)
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("bundles after first run = %d, want 1", len(bundles))
	}
	// 同秒内再跑一次：文件名自动加 -N，keep=1 删掉最旧的一份。
	s.runOnce(eff)
	bundles, err = ListBundles(backupDir)
	if err != nil {
		t.Fatalf("ListBundles: %v", err)
	}
	if len(bundles) != 1 {
		t.Fatalf("bundles after second run = %d, want 1", len(bundles))
	}
}

func TestAcquireLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := AcquireLock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock error = %v, want ErrLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = second.Release()
}
