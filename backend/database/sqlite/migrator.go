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
