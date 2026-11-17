// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gorm.io/gorm"

	appLogger "iot-gateway/logger"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationNames 返回内嵌迁移文件名（已按执行顺序排序）。
//
// 备份恢复用它判断包内 schema 是否比当前二进制更新：迁移文件名是零填充的
// 顺序编号，字典序即执行序。不要在别处再遍历 migrationsFS 各写一份排序。
func MigrationNames() []string {
	files, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		names = append(names, file.Name())
	}
	sort.Strings(names)
	return names
}

// LatestMigration 返回当前二进制内嵌的最新迁移文件名；空列表返回空串。
//
// 注意它描述的是**二进制**而不是数据库：数据库已应用到哪里要看
// schema_migrations 表（备份 manifest 里的 schemaVersion 就是它的最大值）。
func LatestMigration() string {
	names := MigrationNames()
	if len(names) == 0 {
		return ""
	}
	return names[len(names)-1]
}

// Migrate 在给定的连接上执行全部迁移（含 schema_migrations 引导表）。
//
// 与 InitDB 内部的启动迁移是同一个实现。备份模块的 config 快照需要在一张
// 空临时库上建出与当前二进制一致的 schema，又不能走 InitDB（那会播种 admin、
// 网关身份与开放接口密钥），因此单独导出。
func Migrate(db *gorm.DB) error {
	return runMigrations(db)
}

// runMigrations 按顺序执行所有待处理的迁移。
// 首次调用时自动创建 schema_migrations 跟踪表。
// 任一迁移失败则返回错误，由调用者决定是否 Fatal 退出。
func runMigrations(db *gorm.DB) error {
	if err := bootstrapSchemaMigrations(db); err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	applied, err := getAppliedVersions(db)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}

	files, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}

	// 按文件名排序保证执行顺序
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if _, ok := applied[name]; ok {
			appLogger.Debug("migration already applied, skipping: %s", name)
			continue
		}
		if err := applyMigration(db, name); err != nil {
			return fmt.Errorf("migration %s failed: %w", name, err)
		}
	}

	return nil
}

// bootstrapSchemaMigrations 创建 schema_migrations 跟踪表（如果不存在）。
func bootstrapSchemaMigrations(db *gorm.DB) error {
	sql := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
	)`
	return db.Exec(sql).Error
}

// getAppliedVersions 返回已应用的迁移版本集合。
func getAppliedVersions(db *gorm.DB) (map[string]struct{}, error) {
	type row struct {
		Version string
	}
	var rows []row
	if err := db.Table("schema_migrations").
		Select("version").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	set := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		set[r.Version] = struct{}{}
	}
	return set, nil
}

// applyMigration 读取并执行单个迁移文件，在事务中保证原子性。
func applyMigration(db *gorm.DB, name string) error {
	appLogger.Info("applying migration: %s", name)

	raw, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	statements := splitSQL(string(raw))
	if len(statements) == 0 {
		// 空文件：仅记录版本，不执行 SQL
		return db.Table("schema_migrations").
			Exec("INSERT INTO schema_migrations (version) VALUES (?)", name).Error
	}

	return db.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range statements {
			if err := tx.Exec(stmt).Error; err != nil {
				return fmt.Errorf("execute [%.60s...]: %w", stmt, err)
			}
		}
		// 所有语句执行成功后记录版本
		return tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", name).Error
	})
}

// splitSQL 按分号分割 SQL 文本并过滤空语句。
func splitSQL(text string) []string {
	var stmts []string
	for _, raw := range strings.Split(text, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		stmts = append(stmts, stmt)
	}
	return stmts
}
