package push

import (
	"encoding/json"
	"fmt"
	"sync"

	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// SpoolBatch 从本地缓存取回的一批待补发数据。
// Records 为反序列化后的采集记录,由调用方重建 PushBatch 走既有 publish 路径。
type SpoolBatch struct {
	ID          int64
	DeviceID    string
	CollectedAt string
	Records     []collector.CollectedRecord
}

// Spool 推送断网本地缓存(持久化 outbox)接口。
// 通道断连/内存队列满时写入,重连后按 id(入队序,最旧在前)取回补发,发布成功删除。
// 实现方必须串行化底层存储访问(如 SQLite 单连接)。
type Spool interface {
	// Insert 持久化一个批次(records 序列化为 JSON)。
	Insert(batch PushBatch) error
	// FetchOldest 按入队序取回最旧的至多 limit 个批次。
	FetchOldest(limit int) ([]SpoolBatch, error)
	// Delete 删除单个已成功发布的批次。
	Delete(id int64) error
	// DeleteBatch 批量删除多个已成功发布的批次(单条 DELETE,回放路径合并提交)。
	DeleteBatch(ids []int64) error
	// DeleteOldest 删除最旧的 n 个批次(磁盘上限裁剪)。
	DeleteOldest(n int) error
	// Count 返回当前待补发批次数量。
	Count() (int64, error)
}

// SqliteSpool 基于 SQLite 的本地缓存实现,按 channel_id 隔离各通道的待补发数据。
// 内部互斥锁串行化 SQLite 访问(单连接),供通道写协程与补发协程并发调用。
type SqliteSpool struct {
	db        *gorm.DB
	channelID string
	mu        sync.Mutex
	count     int64 // 内存计数:待补发批次数量,Insert/Delete/DeleteOldest 维护,避免每批 COUNT 查询
}

// NewSqliteSpool 创建指定推送通道的 SQLite 本地缓存。
// 构造时一次性预载既有积压(push_outbox 跨重启持久化),使内存计数与库中一致;
// 仅首个批次插入前的裁剪判断用到,失败仅记录不影响运行。
func NewSqliteSpool(db *gorm.DB, channelID string) *SqliteSpool {
	s := &SqliteSpool{db: db, channelID: channelID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := db.Model(&po.PushOutbox{}).
		Where("channel_id = ?", channelID).
		Count(&s.count).Error; err != nil {
		logger.Warn("spool: seed count failed (channel=%s): %v", channelID, err)
	}
	return s
}

// Insert 持久化一个批次。records 序列化为 JSON 存入 records_json 列。
func (s *SqliteSpool) Insert(batch PushBatch) error {
	recordsJSON, err := json.Marshal(batch.Records)
	if err != nil {
		return fmt.Errorf("spool: marshal records failed: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	row := po.PushOutbox{
		ChannelID:   s.channelID,
		DeviceID:    batch.DeviceID,
		CollectedAt: batch.CollectedAt,
		RecordsJSON: string(recordsJSON),
	}
	if err := s.db.Create(&row).Error; err != nil {
		logger.Error("spool: insert failed (channel=%s): %v", s.channelID, err)
		return fmt.Errorf("spool: insert failed: %w", err)
	}
	s.count++
	return nil
}

// FetchOldest 按入队序(id ASC)取回最旧的至多 limit 个批次。
func (s *SqliteSpool) FetchOldest(limit int) ([]SpoolBatch, error) {
	if limit <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var rows []po.PushOutbox
	if err := s.db.Where("channel_id = ?", s.channelID).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		logger.Error("spool: fetch oldest failed (channel=%s): %v", s.channelID, err)
		return nil, fmt.Errorf("spool: fetch oldest failed: %w", err)
	}

	batches := make([]SpoolBatch, 0, len(rows))
	for i := range rows {
		var records []collector.CollectedRecord
		if err := json.Unmarshal([]byte(rows[i].RecordsJSON), &records); err != nil {
			logger.Error("spool: unmarshal records failed (id=%d): %v", rows[i].ID, err)
			return nil, fmt.Errorf("spool: unmarshal records failed: %w", err)
		}
		batches = append(batches, SpoolBatch{
			ID:          rows[i].ID,
			DeviceID:    rows[i].DeviceID,
			CollectedAt: rows[i].CollectedAt,
			Records:     records,
		})
	}
	return batches, nil
}

// Delete 删除单个已成功发布的批次。
func (s *SqliteSpool) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.db.Where("channel_id = ? AND id = ?", s.channelID, id).
		Delete(&po.PushOutbox{})
	if res.Error != nil {
		logger.Error("spool: delete failed (channel=%s id=%d): %v", s.channelID, id, res.Error)
		return fmt.Errorf("spool: delete failed: %w", res.Error)
	}
	s.count -= res.RowsAffected // 幂等删除(RowsAffected=0)不计入
	return nil
}

// DeleteBatch 批量删除多个已成功发布的批次。回放协程把整组成功批次合并为一次删除,
// 避免逐批 DELETE 的 SQLite 写放大(单连接串行下回放吞吐瓶颈)。
func (s *SqliteSpool) DeleteBatch(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	res := s.db.Where("channel_id = ? AND id IN ?", s.channelID, ids).
		Delete(&po.PushOutbox{})
	if res.Error != nil {
		logger.Error("spool: delete batch failed (channel=%s n=%d): %v", s.channelID, len(ids), res.Error)
		return fmt.Errorf("spool: delete batch failed: %w", res.Error)
	}
	s.count -= res.RowsAffected // 幂等删除(RowsAffected 可能小于 len(ids))按实际扣减
	return nil
}

// DeleteOldest 删除最旧的 n 个批次(磁盘上限裁剪)。
func (s *SqliteSpool) DeleteOldest(n int) error {
	if n <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// SQLite 不支持带 LIMIT 的 DELETE 目标表,先取最旧 n 个 id 再按主键删除
	var ids []int64
	if err := s.db.Model(&po.PushOutbox{}).
		Where("channel_id = ?", s.channelID).
		Order("id ASC").
		Limit(n).
		Pluck("id", &ids).Error; err != nil {
		logger.Error("spool: delete oldest pluck failed (channel=%s): %v", s.channelID, err)
		return fmt.Errorf("spool: delete oldest pluck failed: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	res := s.db.Where("channel_id = ? AND id IN ?", s.channelID, ids).
		Delete(&po.PushOutbox{})
	if res.Error != nil {
		logger.Error("spool: delete oldest failed (channel=%s): %v", s.channelID, res.Error)
		return fmt.Errorf("spool: delete oldest failed: %w", res.Error)
	}
	s.count -= res.RowsAffected
	return nil
}

// Count 返回当前待补发批次数量(内存计数,无数据库查询)。
func (s *SqliteSpool) Count() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count, nil
}
