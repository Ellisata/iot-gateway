// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"

	"iot-gateway/database/sqlite"
	"iot-gateway/utils"
)

// restoreConfigTables 把 config 包里的白名单表复制回当前库。
//
// 与 full 包的“整库文件替换”不同，这里保留当前库的运行与历史表。所有
// DELETE + INSERT 在**一个事务**内完成，任何一张表失败都整体回滚，不会留下
// 半套配置。事务前先用 VACUUM INTO 留一份完整库副本，供运维对照或手工回退。
func restoreConfigTables(ctx context.Context, vb *VerifiedBundle, absDB string, opts RestoreOptions,
	ts string, result *RestoreResult) error {
	if _, err := os.Stat(absDB); err != nil {
		return fmt.Errorf("目标数据库 %s 不存在；config 包只能恢复到已有的网关库: %w", absDB, err)
	}

	db, err := sqlite.OpenStandalone(absDB)
	if err != nil {
		return err
	}
	defer sqlite.CloseDB(db)

	// 读/写期间钉住 WAL 是预期行为，避免 WALCheckpointer 误报外部读者。
	doneMaintenance := sqlite.BeginExternalMaintenance()
	defer doneMaintenance()

	// 恢复前的完整副本。命名与 full 恢复的 pre-restore 一致，共用清理逻辑。
	prePath := uniquePath(absDB + ".pre-restore-" + ts)
	if err := db.WithContext(ctx).Exec("VACUUM INTO '" + sqlEscape(prePath) + "'").Error; err != nil {
		return fmt.Errorf("create pre-restore copy %s: %w", prePath, err)
	}
	result.PreRestore = append(result.PreRestore, prePath)

	// 目标必须是一张已迁移的网关库：没有任何配置表时继续执行只会“成功”地
	// 什么也不做，必须报错。
	deviceCols, err := tableColumns(db, "main", "device")
	if err != nil {
		return err
	}
	if len(deviceCols) == 0 {
		return fmt.Errorf("目标数据库 %s 没有 device 表，不是有效的网关库", absDB)
	}

	var newID, newName string
	if opts.Identity == identityClone {
		newID, newName, err = makeCloneIdentity(vb.DBPath)
		if err != nil {
			return err
		}
	}

	attachSQL := "ATTACH DATABASE '" + sqlEscape(vb.DBPath) + "' AS src"
	if err := db.WithContext(ctx).Exec(attachSQL).Error; err != nil {
		return fmt.Errorf("attach bundle database: %w", err)
	}
	attached := true
	defer func() {
		if attached {
			_ = db.Exec("DETACH DATABASE src").Error
		}
	}()

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, table := range ConfigTables {
			ok, err := tableExists(tx, "src", table)
			if err != nil {
				return err
			}
			if !ok {
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
				return fmt.Errorf("clear table %s: %w", table, err)
			}
			insert := fmt.Sprintf("INSERT OR REPLACE INTO main.%s (%s) SELECT %s FROM src.%s AS s",
				quoteIdent(table), quoteColumns(cols), strings.Join(selects, ", "), quoteIdent(table))
			if err := tx.Exec(insert).Error; err != nil {
				return fmt.Errorf("restore table %s: %w", table, err)
			}
			result.TablesRestored = append(result.TablesRestored, table)
		}
		if newID != "" {
			if err := tx.Exec("UPDATE main.iot_gateway SET id = ?, name = ?", newID, newName).Error; err != nil {
				return fmt.Errorf("clone gateway identity: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("恢复配置表失败，数据库已回滚: %w", err)
	}
	if err := db.Exec("DETACH DATABASE src").Error; err != nil {
		return fmt.Errorf("detach bundle database: %w", err)
	}
	attached = false

	result.NewID, result.NewName = newID, newName
	return nil
}

// makeCloneIdentity 读取包内网关身份并生成新的 id / name，不修改包本身。
func makeCloneIdentity(dbPath string) (string, string, error) {
	db, err := sqlite.OpenStandalone(dbPath)
	if err != nil {
		return "", "", err
	}
	defer sqlite.CloseDB(db)

	var row struct {
		ID   string `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	if err := db.Raw("SELECT id, name FROM iot_gateway LIMIT 1").Scan(&row).Error; err != nil {
		return "", "", nil
	}
	if strings.TrimSpace(row.ID) == "" {
		return "", "", nil
	}
	newID := utils.GenerateUUIDV7()
	base := strings.TrimSpace(row.Name)
	if base == "" {
		base = "iot-gateway"
	}
	return newID, fmt.Sprintf("%s-copy-%s", base, newID[:4]), nil
}
