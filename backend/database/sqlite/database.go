// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"iot-gateway/configFile"
	appLogger "iot-gateway/logger"
)

// probeTable 启动写探针使用的表名，用完即删（见 verifyReadWrite）。
const probeTable = "iot_gateway_rw_probe"

// InitDB 初始化 SQLite 数据库连接
func InitDB(cfg *configFile.Config) *gorm.DB {
	path := cfg.SQLite.Path
	if path == "" {
		path = "data/iot-gateway.db"
	}

	// 数据目录必须存在。失败直接 Fatal 而不是只记一条 WARN 继续：随后的 gorm.Open
	// 必然也失败，在这里报错才能把路径打进日志。
	//
	// 注意 MkdirAll 对**已存在**的目录不改权限，所以「目录存在」不等于「目录可写」；
	// 目录可写性由下面的写探针验证 —— 它比文件可写更要紧，见 verifyReadWrite 的注释。
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		appLogger.Fatal("failed to create sqlite data directory %s: %v", dir, err)
		return nil
	}

	// 日志与报错一律用绝对路径：Windows 服务由 SCM 拉起、工作目录是 System32，
	// 相对路径报出来对不上运维在磁盘上看到的东西。
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	gormConfig := &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            false, // SQLite 并发场景下关闭预编译缓存
	}

	// 开发环境开启 SQL 日志
	if cfg.Log.Level == "DEBUG" {
		gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Info)
	}

	conn, err := gorm.Open(sqlite.Open(path), gormConfig)
	if err != nil {
		appLogger.Fatal("failed to connect sqlite database %s: %v", absPath, err)
		return nil
	}

	// SQLite 连接池配置（SQLite 不支持高并发写入，限制连接数为 1）
	sqlDB, err := conn.DB()
	if err != nil {
		appLogger.Fatal("failed to get sql.DB: %v", err)
		return nil
	}

	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(30 * time.Minute)

	// 启用 WAL 模式提升并发读性能（写仍串行，WAL 让读与写不互斥）。
	// journal_mode 会持久化进库文件头，所以执行一次即可；但**必须回读确认** ——
	// 库文件只读、目录不可写或数据目录落在网络盘上时，这条 PRAGMA 会失败或静默停在
	// 旧模式，之后单看运行行为是看不出来的。
	if err := conn.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		appLogger.Warn("failed to enable sqlite WAL mode (path: %s): %v", absPath, err)
	}
	var journalMode string
	if err := conn.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		appLogger.Warn("failed to read sqlite journal_mode (path: %s): %v", absPath, err)
	} else if !strings.EqualFold(journalMode, "wal") {
		appLogger.Warn("sqlite journal_mode is %q, expected \"wal\" (path: %s); "+
			"read-only file, read-only directory, or network filesystem?", journalMode, absPath)
	}
	// 启用外键约束
	//conn.Exec("PRAGMA foreign_keys=ON")
	// 设置 busy timeout
	conn.Exec("PRAGMA busy_timeout=5000")

	// 写探针：在迁移之前证明这个库真的可写，见 verifyReadWrite。
	if err := verifyReadWrite(conn); err != nil {
		appLogger.Fatal("sqlite database is not writable (path: %s): %v; "+
			"check that the account running the gateway can write this file AND its directory "+
			"(the -wal / -shm files are created alongside it)", absPath, err)
		return nil
	}

	// 自动建表（按版本执行迁移）
	if err := runMigrations(conn); err != nil {
		appLogger.Fatal("database migration failed: %v", err)
		return nil
	}

	// 播种初始账号（仅在用户表为空时执行）。失败即无账号可登录，与迁移失败同等致命。
	if err := seedDefaultAdmin(conn, cfg); err != nil {
		appLogger.Fatal("seed default admin failed: %v", err)
		return nil
	}

	// 播种网关自身身份行（表为空时插入，否则只同步其中的 firmware_version），
	// 见 seedGateway.go。
	//
	// 与上面刻意不同：失败**不** Fatal。admin 播种失败等于没有任何账号可登录，
	// 网关起来也没用；而身份行只被「系统设置」页与局域网广播读取，版本旧一点
	// 不影响采集与上报，为它让整个网关起不来（Fatal 会 os.Exit(1)，运维只能删库恢复）
	// 是不划算的。
	if err := seedGatewayIdentity(conn, cfg); err != nil {
		appLogger.Error("seed gateway identity failed: %v", err)
	}

	// 播种默认开放接口密钥（表里有名为 default 的行即跳过），见 seedOpenApiSecret.go。
	//
	// 同样不 Fatal：它只影响开放接口有没有现成凭据、以及发现广播里带不带密钥，
	// 缺了照常采集与上报。
	if err := seedDefaultOpenApiSecret(conn); err != nil {
		appLogger.Error("seed default open api secret failed: %v", err)
	}

	appLogger.Info("sqlite database connected successfully, path: %s", absPath)
	return conn
}

// verifyReadWrite 真写一次再读回来，确认这个库确实可读可写。
//
// 为什么非探不可：SQLite 是懒打开的，「可读不可写」的库**能启动成功** —— 迁移在已有库上
// 会全部跳过，admin 播种也只在用户表为空时才写。于是网关照常起来，直到运行期某次写入才报
// "attempt to write a readonly database"，对外表现为登录失败、outbox 落盘失败等一串
// 与根因对不上的错误（Windows 服务账号换成普通用户、容器里卷被只读挂载都是这个形状）。
// 探一次把失败点前移到启动，并且带上路径。
//
// 也不能靠文件模式位判断：「-rw-r--r--」在 Windows 上不约束 ACL、在容器里不约束只读挂载、
// 从别的机器拷过来的库还带着别人的属主。唯一可信的证据是真的写进去再读回来。
//
// 目录可写比文件可写更要紧：WAL 模式下 -wal / -shm 是运行时才创建的伴生文件，
// 目录写不了它们就建不出来，库整体退化为不可写 —— 而这个探针的建表/插入会真正落进
// -wal，因此它同时验证了目录可写。
func verifyReadWrite(db *gorm.DB) error {
	const create = `CREATE TABLE IF NOT EXISTS ` + probeTable + ` (id INTEGER PRIMARY KEY, v TEXT)`
	const insert = `INSERT INTO ` + probeTable + ` (v) VALUES (?)`
	const query = `SELECT COUNT(*) FROM ` + probeTable + ` WHERE v = ?`

	if err := db.Exec(create).Error; err != nil {
		return fmt.Errorf("create probe table: %w", err)
	}
	// 探针表是启动期临时用的，留在运维的库文件里就是一条看不懂的脏表。
	// 写不进去时 DROP 同样会失败，那时返回的错误已经说明了问题，这里的失败忽略即可。
	defer func() { _ = db.Exec(`DROP TABLE IF EXISTS ` + probeTable).Error }()

	if err := db.Exec(insert, "ok").Error; err != nil {
		return fmt.Errorf("insert probe row: %w", err)
	}

	var n int64
	if err := db.Raw(query, "ok").Scan(&n).Error; err != nil {
		return fmt.Errorf("read back probe row: %w", err)
	}
	if n < 1 {
		return fmt.Errorf("read back probe row: got %d rows, want at least 1", n)
	}
	return nil
}

// CloseDB 关闭 SQLite 数据库连接
func CloseDB(db *gorm.DB) {
	if db != nil {
		sqlDB, err := db.DB()
		if err == nil {
			sqlDB.Close()
		}
	}
}
