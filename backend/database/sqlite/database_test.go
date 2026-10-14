// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openFileDB 打开（必要时创建）一个落在磁盘上的库。
func openFileDB(t *testing.T, path string) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() }) // sql.DB.Close 幂等，用例里提前关过也无妨
	return db
}

// seedDB 在 path 上建一个库、写一张表、正常关闭（关闭时 checkpoint 并删掉 -wal），
// 让随后的用例只改动自己想验证的那一个变量。
func seedDB(t *testing.T, path string) {
	t.Helper()

	db := openFileDB(t, path)
	if err := db.Exec("CREATE TABLE t (id INTEGER)").Error; err != nil {
		t.Fatalf("seed table: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
}

func hasProbeTable(t *testing.T, db *gorm.DB) bool {
	t.Helper()

	var n int64
	if err := db.Raw(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, probeTable,
	).Scan(&n).Error; err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return n > 0
}

// 正常库：探针通过，且不留下探针表。
func TestVerifyReadWritePassesOnWritableDB(t *testing.T) {
	db := openFileDB(t, filepath.Join(t.TempDir(), "iot.db"))

	if err := verifyReadWrite(db); err != nil {
		t.Fatalf("verifyReadWrite on writable db: %v", err)
	}
	if hasProbeTable(t, db) {
		t.Fatalf("probe table %q left behind", probeTable)
	}

	// 每次进程启动都会探一次，必须可重复执行
	if err := verifyReadWrite(db); err != nil {
		t.Fatalf("verifyReadWrite on second run: %v", err)
	}
	if hasProbeTable(t, db) {
		t.Fatalf("probe table %q left behind after second run", probeTable)
	}
}

// 写失败必须变成 error（调用方据此 Fatal），而不是 panic 或静默通过。
func TestVerifyReadWriteReportsWriteFailure(t *testing.T) {
	db := openFileDB(t, filepath.Join(t.TempDir(), "iot.db"))

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	_ = sqlDB.Close() // 关掉底层连接，模拟一切写入都失败的库

	if err := verifyReadWrite(db); err == nil {
		t.Fatal("verifyReadWrite on closed db: want error, got nil")
	}
}

// InitDB 靠回读 journal_mode 判断 WAL 是否真的生效，这里把它依赖的调用形式钉住：
// PRAGMA 会返回一行结果，Scan 到 string 必须拿到模式名（拿不到就等于诊断失效，
// 只会静默地永远不发那条 WARN）。
func TestJournalModeReadBack(t *testing.T) {
	db := openFileDB(t, filepath.Join(t.TempDir(), "iot.db"))

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

// 「库能打开、就是写不进去」的典型形状，且与平台无关：
// 连接建得起来（读得好好的），一旦落写就报 readonly。探针必须抓住它 ——
// 这正是它存在的理由，只靠 gorm.Open 成不成功判断可写性会整个漏掉这一类。
func TestVerifyReadWriteRejectsReadOnlyConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "iot.db")
	seedDB(t, path)

	// mode=ro 是 SQLite 的 URI 参数，glebarez/go-sqlite 始终带 SQLITE_OPEN_URI 打开，
	// 因此「连接只读」可以构造出来，而 gorm.Open 本身会成功。
	ro, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=ro", path)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open read-only sqlite: %v", err)
	}
	sqlRO, err := ro.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	defer func() { _ = sqlRO.Close() }()

	if err := verifyReadWrite(ro); err == nil {
		t.Fatal("verifyReadWrite on read-only connection: want error, got nil")
	}
}

// 目录只读：库文件本身是普通可写文件，但 -wal / 回滚日志建不出来。
// 这是生产上最常见的「库能打开、就是写不进去」—— Windows 服务换成普通账号、
// 容器里数据目录以只读方式挂载都长这样。
func TestVerifyReadWriteRejectsReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows 的目录访问由 ACL 决定，模式位不参与判定，这个构造方式不成立。
		t.Skip("windows 下 chmod 目录不改变访问判定")
	}
	if os.Geteuid() == 0 {
		// root 有 CAP_DAC_OVERRIDE，会无视目录权限照写不误。
		t.Skip("root 绕过目录权限检查")
	}

	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "iot.db")
	seedDB(t, path)

	// 库文件本身仍是普通可写文件，唯一的差别只剩目录不可写。
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// t.Cleanup 是后进先出，这条会赶在 t.TempDir 的 RemoveAll 之前把权限还回去。
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	// 打开阶段就可能失败（WAL 库要建 -wal/-shm）；若打开成功，则必须由写探针拦下。
	// 两种结果都算通过，但绝不能出现「打开了、探针也通过」。
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Logf("open on read-only dir already failed, probe not reached: %v", err)
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := verifyReadWrite(db); err == nil {
		t.Fatal("verifyReadWrite on read-only dir: want error, got nil")
	}
}
