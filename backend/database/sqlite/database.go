// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"os"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"iot-gateway/configFile"
	appLogger "iot-gateway/logger"
)

// InitDB 初始化 SQLite 数据库连接
func InitDB(cfg *configFile.Config) *gorm.DB {
	path := cfg.SQLite.Path
	if path == "" {
		path = "data/iot-gateway.db"
	}

	// 确保数据目录存在
	dir := path
	if os.IsPathSeparator(dir[len(dir)-1]) == false {
		// 取目录部分
		for i := len(dir) - 1; i >= 0; i-- {
			if os.IsPathSeparator(dir[i]) {
				dir = dir[:i]
				break
			}
		}
	}
	if dir != path {
		if err := os.MkdirAll(dir, 0755); err != nil {
			appLogger.Warn("failed to create sqlite data directory: %v", err)
		}
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
		appLogger.Fatal("failed to connect sqlite database: %v", err)
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

	// 启用 WAL 模式提升并发读性能（写仍串行，WAL 让读与写不互斥）
	conn.Exec("PRAGMA journal_mode=WAL")
	// 启用外键约束
	//conn.Exec("PRAGMA foreign_keys=ON")
	// 设置 busy timeout
	conn.Exec("PRAGMA busy_timeout=5000")

	// 自动建表（按版本执行迁移）
	if err := runMigrations(conn); err != nil {
		appLogger.Fatal("database migration failed: %v", err)
		return nil
	}

	appLogger.Info("sqlite database connected successfully, path: %s", path)
	return conn
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
