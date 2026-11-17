// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"iot-gateway/database/sqlite"
)

// GatewayInfo 是备份包里记录的网关身份。
type GatewayInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SnapshotInfo 是一致性快照的检查结果。
type SnapshotInfo struct {
	Path          string
	SHA256        string
	Size          int64
	SchemaVersion string
	Gateway       GatewayInfo
}

type gatewayRow struct {
	ID              string `gorm:"column:id"`
	Name            string `gorm:"column:name"`
	FirmwareVersion string `gorm:"column:firmware_version"`
}

// CreateSnapshot 用 VACUUM INTO 生成源库的一致性快照。
//
// 为什么不是复制文件：库处于 WAL 模式，已提交但未 checkpoint 的帧只在 -wal 里；
// 运行中复制主库会丢数据，复制三件套也没有跨文件一致性。VACUUM INTO 在一个
// 读事务里把**已提交**的数据写成一个独立的、无 WAL 的完整库。
//
// 必须使用独立连接：主 GORM 连接是 MaxOpenConns=1，长时间占用会让配置写入与
// outbox 落盘全部排队。VACUUM INTO 完成后立刻关闭源连接，尽量缩短对 WAL 的钉住。
//
// outPath 必须不存在（SQLite 的约束）；调用方负责清理临时文件。
func CreateSnapshot(ctx context.Context, dbPath, outPath string) (*SnapshotInfo, error) {
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
	// VACUUM INTO 的目标必须不存在。临时目录里的同名残留直接清掉。
	if err := os.Remove(absOut); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale snapshot %s: %w", absOut, err)
	}

	db, err := sqlite.OpenStandalone(absDB)
	if err != nil {
		return nil, err
	}

	// 告知进程内的 WALCheckpointer：这段时间 WAL 被备份快照钉住是预期行为，
	// 不要把「外部读者」的 WARN 打出来。
	doneMaintenance := sqlite.BeginExternalMaintenance()
	defer doneMaintenance()

	// 路径做 SQL 单引号转义：VACUUM INTO 的 filename 是字面量表达式，
	// 不同驱动对参数的拼接行为不完全一致，这里不用占位符。
	escaped := strings.ReplaceAll(absOut, "'", "''")
	execErr := db.WithContext(ctx).Exec("VACUUM INTO '" + escaped + "'").Error
	sqlite.CloseDB(db)
	if execErr != nil {
		return nil, fmt.Errorf("snapshot sqlite database %s: %w", absDB, execErr)
	}

	gw, schema, err := inspectSnapshot(absOut)
	if err != nil {
		return nil, err
	}
	sum, size, err := fileSHA256(absOut)
	if err != nil {
		return nil, fmt.Errorf("hash snapshot: %w", err)
	}
	return &SnapshotInfo{
		Path:          absOut,
		SHA256:        sum,
		Size:          size,
		SchemaVersion: schema,
		Gateway:       gw,
	}, nil
}

// InspectSnapshot 校验一个快照/数据库文件并读出 schema 版本与网关身份。
func InspectSnapshot(path string) (GatewayInfo, string, error) {
	return inspectSnapshot(path)
}

func inspectSnapshot(path string) (GatewayInfo, string, error) {
	db, err := sqlite.OpenStandalone(path)
	if err != nil {
		return GatewayInfo{}, "", err
	}
	defer sqlite.CloseDB(db)

	var result string
	if err := db.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil {
		return GatewayInfo{}, "", fmt.Errorf("integrity_check %s: %w", path, err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return GatewayInfo{}, "", fmt.Errorf("integrity_check %s returned %q", path, result)
	}

	// schema_migrations 不存在（极小的手工库）时按空版本处理，恢复后由启动
	// 迁移补齐；这里不把它当成损坏。
	var schema string
	if err := db.Raw("SELECT COALESCE(MAX(version), '') FROM schema_migrations").Scan(&schema).Error; err != nil {
		schema = ""
	}

	var row gatewayRow
	if err := db.Raw("SELECT id, name, firmware_version FROM iot_gateway LIMIT 1").Scan(&row).Error; err != nil {
		row = gatewayRow{}
	}
	return GatewayInfo{ID: row.ID, Name: row.Name, Version: row.FirmwareVersion}, schema, nil
}

// fileSHA256 计算文件摘要与大小。
func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
