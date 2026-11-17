// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/database/sqlite"
	"iot-gateway/logger"
)

// CreateConfigSnapshot 生成“仅配置”的一致性快照。
//
// 做法是按 ConfigTables 白名单把源库的配置表复制进一张按当前二进制建好
// schema 的空库，而不是先整库 VACUUM INTO 再删运行表：离线期 push_outbox
// 可能有数百 MB，整库快照会让配置级备份被运行数据拖慢。
//
// 复制在**一个事务**内完成：SQLite 在事务的第一个读操作确定快照点，后续
// 所有表看到的是同一份已提交数据。源库通过 ATTACH 访问，同样是一致读。
//
// outPath 必须不存在（调用方负责清理临时文件）。
func CreateConfigSnapshot(ctx context.Context, dbPath, outPath string) (*SnapshotInfo, error) {
	absDB, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve database path %s: %w", dbPath, err)
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return nil, fmt.Errorf("resolve snapshot path %s: %w", outPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(absOut), 0700); err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	if _, err := os.Stat(absDB); err != nil {
		return nil, fmt.Errorf("stat database %s: %w", absDB, err)
	}
	if err := os.Remove(absOut); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale snapshot %s: %w", absOut, err)
	}

	// 打开一个全新的空库，按当前二进制建出完整 schema。连接随文件创建。
	db, err := sqlite.OpenStandalone(absOut)
	if err != nil {
		return nil, err
	}
	defer sqlite.CloseDB(db)

	if err := sqlite.Migrate(db); err != nil {
		return nil, fmt.Errorf("create config snapshot schema: %w", err)
	}

	// 读源库期间 WAL 会被钉住，告知 WALCheckpointer 这是预期现象。
	doneMaintenance := sqlite.BeginExternalMaintenance()
	defer doneMaintenance()

	attachSQL := "ATTACH DATABASE '" + sqlEscape(absDB) + "' AS src"
	if err := db.WithContext(ctx).Exec(attachSQL).Error; err != nil {
		return nil, fmt.Errorf("attach source database %s: %w", absDB, err)
	}
	attached := true
	defer func() {
		if attached {
			_ = db.Exec("DETACH DATABASE src").Error
		}
	}()

	var copied []string
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range ConfigTables {
			ok, err := tableExists(tx, "src", table)
			if err != nil {
				return err
			}
			if !ok {
				// 源库比当前 schema 旧（升级后还没迁移过）时允许缺表。
				logger.Debug("backup: config snapshot skips missing table %s", table)
				continue
			}
			cols, selects, err := copyProjection(tx, table)
			if err != nil {
				return err
			}
			if len(cols) == 0 {
				continue
			}
			if err := tx.Exec("DELETE FROM main." + quoteIdent(table)).Error; err != nil {
				return fmt.Errorf("clear snapshot table %s: %w", table, err)
			}
			insert := fmt.Sprintf("INSERT OR REPLACE INTO main.%s (%s) SELECT %s FROM src.%s AS s",
				quoteIdent(table), quoteColumns(cols), strings.Join(selects, ", "), quoteIdent(table))
			if err := tx.Exec(insert).Error; err != nil {
				return fmt.Errorf("copy config table %s: %w", table, err)
			}
			copied = append(copied, table)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := db.Exec("DETACH DATABASE src").Error; err != nil {
		return nil, fmt.Errorf("detach source database: %w", err)
	}
	attached = false

	// 收缩：建表与复制会留下空闲页，配置包越小越快传输。
	if err := db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		return nil, fmt.Errorf("vacuum config snapshot: %w", err)
	}

	// 必须先关闭连接再读文件：库处于 WAL 模式，VACUUM 的结果可能还在 -wal 里，
	// 只复制主库文件会得到一个缺数据的快照。先显式 checkpoint，再关闭最后一个
	// 连接（SQLite 也会在关闭时 checkpoint 并删除伴生文件）。defer 里的 CloseDB
	// 对已关闭的连接是 no-op。
	_ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error
	sqlite.CloseDB(db)

	gw, schema, err := inspectSnapshot(absOut)
	if err != nil {
		return nil, err
	}
	sum, size, err := fileSHA256(absOut)
	if err != nil {
		return nil, fmt.Errorf("hash snapshot: %w", err)
	}
	logger.Info("backup: config snapshot copied %d table(s)", len(copied))
	return &SnapshotInfo{
		Path:          absOut,
		SHA256:        sum,
		Size:          size,
		SchemaVersion: schema,
		Gateway:       gw,
	}, nil
}

// tableExists 检查指定 schema 下是否存在该表。
func tableExists(db *gorm.DB, schema, table string) (bool, error) {
	var n int64
	q := fmt.Sprintf("SELECT COUNT(*) FROM %s.sqlite_master WHERE type = 'table' AND name = ?", schema)
	if err := db.Raw(q, table).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("check table %s.%s: %w", schema, table, err)
	}
	return n > 0, nil
}

type pragmaColumn struct {
	Name      string  `gorm:"column:name"`
	Type      string  `gorm:"column:type"`
	NotNull   int     `gorm:"column:notnull"`
	DfltValue *string `gorm:"column:dflt_value"`
}

// tableColumns 返回 schema.table 的列定义（按表定义顺序）。
func tableColumns(db *gorm.DB, schema, table string) ([]pragmaColumn, error) {
	var cols []pragmaColumn
	q := fmt.Sprintf("PRAGMA %s.table_info(%s)", schema, quoteIdent(table))
	if err := db.Raw(q).Scan(&cols).Error; err != nil {
		return nil, fmt.Errorf("read columns of %s.%s: %w", schema, table, err)
	}
	out := cols[:0]
	for _, c := range cols {
		if c.Name != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

// copyProjection 构造“把 attached 库同名表复制进 main”的列与 SELECT 表达式。
// 返回的 SELECT 表达式统一用别名 s 引用源表（调用方 FROM 时必须 `AS s`）。
//
// 为什么必须加别名：本仓库使用的 modernc SQLite 在 `SELECT src.col FROM src."t"`
// 这种“库名.列”写法上会报 no such column: src.col（`t.col` 别名形式正常）。
// 加表别名既绕开该问题，也让语句更短。
//
// 源库可能是升级前的旧 schema。共有的列直接取 src；源库缺的列：
//   - 有默认值或可为 NULL → 不写，交给建表默认值；
//   - NOT NULL 且无默认值 → 显式写类型零值，否则 INSERT 会失败。
//
// 直接 `SELECT *` 会因列数不匹配而失败；只写共有列又会撞上 NOT NULL。
func copyProjection(db *gorm.DB, table string) ([]string, []string, error) {
	mainCols, err := tableColumns(db, "main", table)
	if err != nil {
		return nil, nil, err
	}
	srcCols, err := tableColumns(db, "src", table)
	if err != nil {
		return nil, nil, err
	}
	srcSet := make(map[string]struct{}, len(srcCols))
	for _, c := range srcCols {
		srcSet[c.Name] = struct{}{}
	}
	var cols, selects []string
	for _, c := range mainCols {
		if _, ok := srcSet[c.Name]; ok {
			cols = append(cols, c.Name)
			selects = append(selects, "s."+quoteIdent(c.Name))
			continue
		}
		if c.NotNull == 1 && c.DfltValue == nil {
			cols = append(cols, c.Name)
			selects = append(selects, zeroLiteral(c.Type))
		}
	}
	return cols, selects, nil
}

func zeroLiteral(columnType string) string {
	t := strings.ToUpper(columnType)
	switch {
	case strings.Contains(t, "INT"):
		return "0"
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return "0.0"
	case strings.Contains(t, "BLOB"):
		return "X''"
	default:
		return "''"
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteColumns(cols []string) string {
	quoted := make([]string, 0, len(cols))
	for _, c := range cols {
		quoted = append(quoted, quoteIdent(c))
	}
	return strings.Join(quoted, ", ")
}

func sqlEscape(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
