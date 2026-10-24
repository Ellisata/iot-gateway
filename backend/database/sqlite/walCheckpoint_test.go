// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// enableWAL 把库切到 WAL 并回读确认 —— 与 InitDB 的调用形式保持一致。
func enableWAL(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	var mode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want \"wal\"", mode)
	}
}

// readPragmaInt 回读只返回一个整数的 PRAGMA。
func readPragmaInt(t *testing.T, db *gorm.DB, stmt string) int {
	t.Helper()

	var got int
	if err := db.Raw(stmt).Scan(&got).Error; err != nil {
		t.Fatalf("read %s: %v", stmt, err)
	}
	return got
}

// DSN 里的 _pragma 必须在**每个连接**上生效，且查询串不能混进文件名。
// 这条同时钉住生产顺序：先由 DSN 设 wal_autocheckpoint，再 Exec 切 WAL，后者不会把它冲掉。
func TestDSNAppliesWALAutocheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iot.db")
	db := openFileDB(t, dsnWithPragmas(path))

	if got := readPragmaInt(t, db, "PRAGMA wal_autocheckpoint"); got != walAutocheckpointPages {
		t.Fatalf("wal_autocheckpoint = %d, want %d", got, walAutocheckpointPages)
	}
	// 查询串被剥掉了，库文件就建在 path 上（而不是 "iot.db?_pragma=..."）。
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file %s not created (query leaked into the filename?): %v", path, err)
	}

	enableWAL(t, db)
	if got := readPragmaInt(t, db, "PRAGMA wal_autocheckpoint"); got != walAutocheckpointPages {
		t.Fatalf("wal_autocheckpoint after journal_mode=WAL = %d, want %d", got, walAutocheckpointPages)
	}
}

// WAL 下提交不等 fsync 依赖 synchronous=NORMAL（FULL=2 会每批提交都 fsync）。
// 它同样是连接级 PRAGMA，必须由 DSN 下发，否则连接池重开连接后会悄悄退回 FULL。
func TestDSNAppliesSynchronousNormal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iot.db")
	db := openFileDB(t, dsnWithPragmas(path))

	const normal = 1 // PRAGMA synchronous: OFF=0, NORMAL=1, FULL=2, EXTRA=3
	if got := readPragmaInt(t, db, "PRAGMA synchronous"); got != normal {
		t.Fatalf("synchronous = %d, want %d (NORMAL)", got, normal)
	}

	enableWAL(t, db)
	if got := readPragmaInt(t, db, "PRAGMA synchronous"); got != normal {
		t.Fatalf("synchronous after journal_mode=WAL = %d, want %d (NORMAL)", got, normal)
	}

	// 连接池重开物理连接后仍须生效：关光空闲连接，下一条查询会新建连接。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxIdleConns(0)
	sqlDB.SetConnMaxLifetime(0)
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if got := readPragmaInt(t, db, "PRAGMA synchronous"); got != normal {
		t.Fatalf("synchronous on a fresh connection = %d, want %d (NORMAL)", got, normal)
	}
}

// 没有读者时，checkpoint 必须把帧合并回主库并把 -wal 截断到 0，数据仍然可读。
func TestWALCheckpointerTruncatesIdleWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iot.db")
	db := openWALDB(t, path)

	if err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := db.Exec("INSERT INTO t (v) VALUES (?)", "x").Error; err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	walPath := path + "-wal"
	fi, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat %s: %v", walPath, err)
	}
	if fi.Size() == 0 {
		t.Fatalf("%s is empty before checkpoint; test setup did not produce WAL frames", walPath)
	}

	r, err := NewWALCheckpointer(db).checkpoint(context.Background())
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if r.Busy != 0 {
		t.Fatalf("checkpoint = %+v, want idle (busy=0)", r)
	}
	if !r.Truncated {
		t.Fatalf("checkpoint = %+v, want -wal truncated", r)
	}

	fi, err = os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat %s after TRUNCATE: %v", walPath, err)
	}
	if fi.Size() != 0 {
		t.Fatalf("%s size = %d after TRUNCATE, want 0", walPath, fi.Size())
	}

	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM t").Scan(&n).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if n != 10 {
		t.Fatalf("row count = %d, want 10", n)
	}
}

// 外部读者钉住旧快照时：PASSIVE 返回 busy=0 但 checkpointed < log，
// 此时**不能**升级 TRUNCATE（否则会占住唯一连接等它，最长 busy_timeout）。
// 读者一退出，下一次 checkpoint 就该恢复截断 —— 这证明"没截断"确实是读者造成的。
func TestWALCheckpointerSkipsTruncateWhenReaderPins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iot.db")
	db := openWALDB(t, path)

	if err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := db.Exec("INSERT INTO t (v) VALUES (?)", "x").Error; err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// 第二个连接开一个显式事务并读一次，把快照钉在当前的 WAL 帧上。
	reader := openFileDB(t, dsnWithPragmas(path))
	tx := reader.Begin()
	if tx.Error != nil {
		t.Fatalf("begin reader tx: %v", tx.Error)
	}
	var n int64
	if err := tx.Raw("SELECT COUNT(*) FROM t").Scan(&n).Error; err != nil {
		t.Fatalf("reader snapshot query: %v", err)
	}

	// 读快照之后继续写：这些帧越过读者快照，PASSIVE 推不过去。
	for i := 0; i < 10; i++ {
		if err := db.Exec("INSERT INTO t (v) VALUES (?)", "y").Error; err != nil {
			t.Fatalf("insert after snapshot: %v", err)
		}
	}

	walPath := path + "-wal"
	before, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat %s: %v", walPath, err)
	}

	cp := NewWALCheckpointer(db)
	r, err := cp.checkpoint(context.Background())
	if err != nil {
		t.Fatalf("checkpoint with pinned reader: %v", err)
	}
	if r.Busy != 0 {
		t.Fatalf("checkpoint = %+v, want busy=0 (PASSIVE does not report readers as busy)", r)
	}
	if r.Checkpointed >= r.Log {
		t.Fatalf("checkpoint = %+v, want partial backfill (checkpointed < log) while a reader pins the snapshot", r)
	}
	if r.Truncated {
		t.Fatalf("checkpoint = %+v, must not TRUNCATE while a reader pins the WAL", r)
	}
	after, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("stat %s after passive checkpoint: %v", walPath, err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("WAL size changed %d -> %d during a passive checkpoint; expected no truncation", before.Size(), after.Size())
	}

	if err := tx.Rollback().Error; err != nil {
		t.Fatalf("rollback reader tx: %v", err)
	}

	r, err = cp.checkpoint(context.Background())
	if err != nil {
		t.Fatalf("checkpoint after reader left: %v", err)
	}
	if !r.Truncated {
		t.Fatalf("checkpoint after reader left = %+v, want -wal truncated", r)
	}
	if fi, err := os.Stat(walPath); err != nil {
		t.Fatalf("stat %s after truncate: %v", walPath, err)
	} else if fi.Size() != 0 {
		t.Fatalf("%s size = %d after TRUNCATE, want 0", walPath, fi.Size())
	}
}

// Start/Stop 必须幂等：未启动时 Stop、重复 Start/Stop 都不能 panic 或挂起。
func TestWALCheckpointerStartStopIdempotent(t *testing.T) {
	db := openWALDB(t, filepath.Join(t.TempDir(), "iot.db"))
	cp := NewWALCheckpointer(db)

	cp.Stop() // 未启动
	cp.Start()
	cp.Start()
	cp.Stop()
	cp.Stop()
}

// 后台循环真的会跑：把间隔压到 20ms，轮询等待 -wal 归零。
func TestWALCheckpointerLoopTruncatesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iot.db")
	db := openWALDB(t, path)

	if err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := db.Exec("INSERT INTO t (v) VALUES (?)", "x").Error; err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	cp := NewWALCheckpointer(db)
	cp.interval = 20 * time.Millisecond
	cp.Start()
	defer cp.Stop()

	walPath := path + "-wal"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(walPath); err == nil && fi.Size() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fi, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("periodic checkpointer did not truncate the WAL: stat %s: %v", walPath, err)
	}
	t.Fatalf("periodic checkpointer did not truncate the WAL within 2s (size=%d)", fi.Size())
}

// openWALDB 打开一个 WAL 模式的临时库。生产路径的 PRAGMA 顺序由 InitDB 负责，
// 这里用同样的 DSN + enableWAL 复现，避免测试绕过被测配置。
func openWALDB(t *testing.T, path string) *gorm.DB {
	t.Helper()

	db := openFileDB(t, dsnWithPragmas(path))
	enableWAL(t, db)
	return db
}
