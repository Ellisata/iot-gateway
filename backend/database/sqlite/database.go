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

// walAutocheckpointPages WAL 自动 checkpoint 的页数阈值（4KB 页下 512 页约 2MB）。
//
// SQLite 默认 1000 页（约 4MB）。这个阈值只决定「写提交时最多攒多少帧才合并回主库」，
// 不决定文件能不能缩小 —— 缩文件由 WALCheckpointer 定期 TRUNCATE 负责。
const walAutocheckpointPages = 512

// dsnWithPragmas 在数据文件路径后追加**每连接**生效的 PRAGMA。
//
// 为什么必须走 DSN、不能打开后再 Exec：PRAGMA 是连接级设置，而 database/sql 会在
// ConnMaxLifetime 到期后关掉物理连接、下次查询再开一条新的。新连接不会继承上一条
// 连接上的 wal_autocheckpoint / synchronous，WAL 就会悄悄退回默认值（busy_timeout
// 同理，不过 go-sqlite 自己会在每条新连接上设 BUSY_TIMEOUT(5000)）。
//
// synchronous=NORMAL：WAL 下每次提交只追加 -wal、不 fsync，checkpoint 回主库时才
// fsync。掉电最多丢失上一次 checkpoint 之后的提交（默认约 wal_autocheckpoint 个页），
// 库本身不会损坏 —— 这是 SQLite 官方对 WAL + NORMAL 的保证。默认的 FULL 会让**每一批**
// 断网缓存落盘都等一次 fsync：实测单行 INSERT 1.86ms（约 540 次/秒）vs NORMAL 0.095ms
// （约 10500 次/秒），是断网期写放大的主因，在 eMMC/SD 卡上差距还会更大。
//
// glebarez/go-sqlite 的 newConn 会把**不以 file: 开头**的 DSN 里 "?" 之后的部分从
// 文件名剥掉，并在 applyQueryParams 里按顺序执行每个 _pragma，所以相对路径与 Windows
// 路径都可以直接拼接；文件系统操作（MkdirAll / Abs / 日志）仍然只认原始 path。
func dsnWithPragmas(path string) string {
	return fmt.Sprintf("%s?_pragma=wal_autocheckpoint(%d)&_pragma=synchronous(NORMAL)",
		path, walAutocheckpointPages)
}

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

	conn, err := gorm.Open(sqlite.Open(dsnWithPragmas(path)), gormConfig)
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
	// 这两条会让物理连接在 1 小时后被回收重开，**连接级 PRAGMA 因此不能只在打开后
	// Exec 一次**（新连接不继承），wal_autocheckpoint / synchronous 因此改由
	// dsnWithPragmas 下发。
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
