// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package postgres 提供基于 Postgres 的断网缓存 SpoolStore 实现。
//
// 与 tsdb/spool/sqlite 的 API 一致：需要断网缓存的调用方可以：
//   - 已有 *gorm.DB（例如宿主自己的 Postgres 业务库）时用 NewSpool 复用连接；
//   - 独立项目用 Open 打开一个专用库，再 New(...).Migrate()。
//
// 表结构与 tsdb/spool/sqlite 的 push_outbox 相同（列名、按 channel_id 隔离、入队序
// 即 id 序），便于 SQLite → Postgres 迁移时直接拷贝积压数据。差异只有主键
// （BIGSERIAL）与建表时间列（TIMESTAMPTZ）。同一 channel_id 只应由一个 Spool
// 实例写入：Count 是实例本地计数（同 SQLite 实现），多写者共享一个 channel
// 时计数会漂移，但取回/删除本身仍是安全的。
package postgres

import (
	"encoding/json"
	"fmt"
	"sync"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway/tsdb"
)

// defaultTable 与 SQLite 实现、网关既有断网缓存表同名，便于积压数据平移。
const defaultTable = "push_outbox"

// Option 构造 Spool 的可选项。
type Option func(*Spool)

// WithTable 指定缓存表名（仅允许字母、数字、下划线，非法值回落默认表名）。
func WithTable(name string) Option {
	return func(s *Spool) {
		if name != "" {
			s.table = sanitizeTable(name)
		}
	}
}

// WithLogger 注入组件日志。
func WithLogger(l tsdb.Logger) Option {
	return func(s *Spool) {
		if l != nil {
			s.log = l
		}
	}
}

// Open 打开一个独立的 Postgres 库（pgx 驱动），用于只做断网缓存的场景。
// 调用方负责关闭底层连接。
func Open(dsn string) (*gorm.DB, error) {
	return gorm.Open(pgdriver.Open(dsn), &gorm.Config{})
}

// Spool 基于 Postgres 的断网缓存实现，按 channel_id 隔离各后端的待补发数据。
// 内部互斥锁串行化同一实例的访问（供写协程与补发协程并发调用）。
type Spool struct {
	db        *gorm.DB
	channelID string
	table     string
	log       tsdb.Logger

	mu    sync.Mutex
	count int64 // 内存计数：待补发批次数量，Insert/Delete/DeleteOldest 维护，避免每批 COUNT 查询
}

// NewSpool 创建指定通道的 Postgres 缓存。
// 构造时一次性预载既有积压（跨重启持久化），使内存计数与库中一致；
// 仅首个批次插入前的裁剪判断用到，失败仅记录不影响运行。
func NewSpool(db *gorm.DB, channelID string, opts ...Option) *Spool {
	s := &Spool{
		db:        db,
		channelID: channelID,
		table:     defaultTable,
		log:       tsdb.NopLogger(),
	}
	for _, opt := range opts {
		opt(s)
	}
	// 表尚未迁移（NewSpool 早于 Migrate，或缺省的首次启动）时跳过预载：
	// 空表计数本来就是 0，直接 COUNT 只会换来一条「关系不存在」错误日志。
	if db != nil && db.Migrator().HasTable(s.table) {
		s.mu.Lock()
		if err := db.Table(s.table).Where("channel_id = ?", channelID).
			Count(&s.count).Error; err != nil {
			s.log.Warnf("spool: seed count failed (channel=%s): %v", channelID, err)
		}
		s.mu.Unlock()
	}
	return s
}

// Migrate 创建断网缓存表与索引（幂等）。宿主已有同名表时可直接跳过。
func (s *Spool) Migrate() error {
	if s.db == nil {
		return fmt.Errorf("spool: nil db")
	}
	table := quoteIdent(s.table)
	// 默认表沿用网关既有索引名，避免在同一个库上再建一条重复索引。
	indexName := quoteIdent("idx_" + s.table + "_channel")
	if s.table == defaultTable {
		indexName = quoteIdent("idx_outbox_channel")
	}
	stmts := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	id               BIGSERIAL PRIMARY KEY,
	channel_id       TEXT NOT NULL,
	device_id        TEXT NOT NULL,
	device_code      TEXT NOT NULL DEFAULT '',
	thing_model_code TEXT NOT NULL DEFAULT '',
	group_id         TEXT NOT NULL DEFAULT '',
	group_code       TEXT NOT NULL DEFAULT '',
	group_name       TEXT NOT NULL DEFAULT '',
	group_interval_ms INTEGER NOT NULL DEFAULT 0,
	collected_at     TEXT NOT NULL,
	records_json     TEXT NOT NULL,
	created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
)`, table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(channel_id, id)`, indexName, table),
	}
	for _, stmt := range stmts {
		if err := s.db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("spool: migrate %s failed: %w", s.table, err)
		}
	}
	return nil
}

// Insert 持久化一个批次。
func (s *Spool) Insert(batch tsdb.Batch) error {
	return s.InsertMany([]tsdb.Batch{batch})
}

// InsertMany 在一个事务里持久化多个批次：一条多行 INSERT、一次提交。
//
// 序列化在加锁前完成（JSON 可能较大），锁只包住数据库操作。合并写失败时整个
// 语句回滚，不留下半批数据；调用方可退回逐条 Insert 隔离坏批次。
func (s *Spool) InsertMany(batches []tsdb.Batch) error {
	if len(batches) == 0 {
		return nil
	}

	rows := make([]row, 0, len(batches))
	for i := range batches {
		recordsJSON, err := json.Marshal(batches[i].Records)
		if err != nil {
			return fmt.Errorf("spool: marshal records failed: %w", err)
		}
		rows = append(rows, row{
			ChannelID:       s.channelID,
			DeviceID:        batches[i].DeviceID,
			DeviceCode:      batches[i].DeviceCode,
			ThingModelCode:  batches[i].ThingModelCode,
			GroupID:         batches[i].GroupID,
			GroupCode:       batches[i].GroupCode,
			GroupName:       batches[i].GroupName,
			GroupIntervalMs: batches[i].GroupIntervalMs,
			CollectedAt:     batches[i].CollectedAt,
			RecordsJSON:     string(recordsJSON),
		})
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Table(s.table).CreateInBatches(&rows, len(rows)).Error; err != nil {
		s.log.Errorf("spool: insert %d batches failed (channel=%s): %v", len(rows), s.channelID, err)
		return fmt.Errorf("spool: insert %d batches failed: %w", len(rows), err)
	}
	s.count += int64(len(rows))
	return nil
}

// FetchOldest 按入队序（id ASC）取回最旧的至多 limit 个批次。
func (s *Spool) FetchOldest(limit int) ([]tsdb.StoredBatch, error) {
	if limit <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var rows []row
	if err := s.db.Table(s.table).Where("channel_id = ?", s.channelID).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		s.log.Errorf("spool: fetch oldest failed (channel=%s): %v", s.channelID, err)
		return nil, fmt.Errorf("spool: fetch oldest failed: %w", err)
	}

	batches := make([]tsdb.StoredBatch, 0, len(rows))
	for i := range rows {
		var records []tsdb.Record
		if err := json.Unmarshal([]byte(rows[i].RecordsJSON), &records); err != nil {
			s.log.Errorf("spool: unmarshal records failed (id=%d): %v", rows[i].ID, err)
			return nil, fmt.Errorf("spool: unmarshal records failed: %w", err)
		}
		groupID, groupName, groupInterval := rows[i].GroupID, rows[i].GroupName, rows[i].GroupIntervalMs
		if groupID == "" && len(records) > 0 {
			groupID = records[0].GroupID
			groupName = records[0].GroupName
			groupInterval = records[0].GroupIntervalMs
		}
		batches = append(batches, tsdb.StoredBatch{
			ID:              rows[i].ID,
			DeviceID:        rows[i].DeviceID,
			DeviceCode:      rows[i].DeviceCode,
			ThingModelCode:  rows[i].ThingModelCode,
			GroupID:         groupID,
			GroupCode:       rows[i].GroupCode,
			GroupName:       groupName,
			GroupIntervalMs: groupInterval,
			CollectedAt:     rows[i].CollectedAt,
			Records:         records,
		})
	}
	return batches, nil
}

// Delete 删除单个已成功写入的批次。
func (s *Spool) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.db.Table(s.table).Where("channel_id = ? AND id = ?", s.channelID, id).
		Delete(&row{})
	if res.Error != nil {
		s.log.Errorf("spool: delete failed (channel=%s id=%d): %v", s.channelID, id, res.Error)
		return fmt.Errorf("spool: delete failed: %w", res.Error)
	}
	s.count -= res.RowsAffected // 幂等删除（RowsAffected=0）不计入
	return nil
}

// DeleteBatch 批量删除多个已成功写入的批次。回放协程把整组成功批次合并为一次删除，
// 避免逐批 DELETE 的往返开销（回放吞吐瓶颈）。
func (s *Spool) DeleteBatch(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.db.Table(s.table).Where("channel_id = ? AND id IN ?", s.channelID, ids).
		Delete(&row{})
	if res.Error != nil {
		s.log.Errorf("spool: delete batch failed (channel=%s n=%d): %v", s.channelID, len(ids), res.Error)
		return fmt.Errorf("spool: delete batch failed: %w", res.Error)
	}
	s.count -= res.RowsAffected // 幂等删除（RowsAffected 可能小于 len(ids)）按实际扣减
	return nil
}

// DeleteOldest 删除最旧的 n 个批次（磁盘上限裁剪）。
func (s *Spool) DeleteOldest(n int) error {
	if n <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 先取最旧 n 个 id 再按主键删除：与 SQLite 实现同形，RowsAffected 即实际
	// 删除数，便于内存计数按实际扣减。
	var ids []int64
	if err := s.db.Table(s.table).
		Where("channel_id = ?", s.channelID).
		Order("id ASC").
		Limit(n).
		Pluck("id", &ids).Error; err != nil {
		s.log.Errorf("spool: delete oldest pluck failed (channel=%s): %v", s.channelID, err)
		return fmt.Errorf("spool: delete oldest pluck failed: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	res := s.db.Table(s.table).Where("channel_id = ? AND id IN ?", s.channelID, ids).
		Delete(&row{})
	if res.Error != nil {
		s.log.Errorf("spool: delete oldest failed (channel=%s): %v", s.channelID, res.Error)
		return fmt.Errorf("spool: delete oldest failed: %w", res.Error)
	}
	s.count -= res.RowsAffected
	return nil
}

// Count 返回当前待补发批次数量（内存计数，无数据库查询）。
func (s *Spool) Count() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count, nil
}

// row 缓存表的 GORM 映射。具体表名由 Spool.table 决定（用 Table() 指定）。
//
// created_at 不在映射里：建表默认 now() 由数据库填充，回放路径也用不到它；
// 映射成 string 会迫使 INSERT 写入空串，而 Postgres 的 TIMESTAMPTZ 拒绝空串。
type row struct {
	ID              int64  `gorm:"primaryKey;column:id;autoIncrement"`
	ChannelID       string `gorm:"column:channel_id;index"`
	DeviceID        string `gorm:"column:device_id"`
	DeviceCode      string `gorm:"column:device_code;not null;default:''"`
	ThingModelCode  string `gorm:"column:thing_model_code;not null;default:''"`
	GroupID         string `gorm:"column:group_id"`
	GroupCode       string `gorm:"column:group_code;not null;default:''"`
	GroupName       string `gorm:"column:group_name"`
	GroupIntervalMs int    `gorm:"column:group_interval_ms"`
	CollectedAt     string `gorm:"column:collected_at"`
	RecordsJSON     string `gorm:"column:records_json"`
}

func (row) TableName() string { return defaultTable }

// sanitizeTable 只保留字母、数字、下划线；空结果回落默认表名。
func sanitizeTable(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return defaultTable
	}
	return string(out)
}

// quoteIdent 给已过白名单的表名/索引名加双引号，避免数字开头或大小写被折叠。
func quoteIdent(name string) string { return `"` + name + `"` }
