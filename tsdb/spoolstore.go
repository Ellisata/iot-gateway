// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tsdb

// StoredBatch 从断网本地缓存取回的一批待补发数据。
// Records 为反序列化后的采集记录，由调用方重建 Batch 走既有写入路径。
type StoredBatch struct {
	ID              int64
	DeviceID        string
	DeviceCode      string
	ThingModelCode  string
	GroupID         string
	GroupCode       string
	GroupName       string
	GroupIntervalMs int
	CollectedAt     string
	Records         []Record
}

// ToBatch 把缓存批次重建为待写入批次。
//
// 旧行（升级前写入）组列为空，但 records_json 里的记录可能已经带了组字段
// （新版本写入的 records 才有；更早的连记录也没有）—— 因此组列为空时从首条
// 记录回退推导，仍然取不到就按 legacy（空组）处理。
func (b StoredBatch) ToBatch() Batch {
	deviceCode, thingModelCode := b.DeviceCode, b.ThingModelCode
	groupID, groupCode, groupName, groupInterval := b.GroupID, b.GroupCode, b.GroupName, b.GroupIntervalMs
	if len(b.Records) > 0 {
		// 旧行没有编码列；新写入的记录本身带编码，可从这里回退。
		if deviceCode == "" {
			deviceCode = b.Records[0].DeviceCode
		}
		if thingModelCode == "" {
			thingModelCode = b.Records[0].ThingModelCode
		}
		if groupCode == "" {
			groupCode = b.Records[0].GroupCode
		}
		if groupID == "" {
			groupID = b.Records[0].GroupID
			groupName = b.Records[0].GroupName
			groupInterval = b.Records[0].GroupIntervalMs
		}
	}
	return Batch{
		DeviceID:        b.DeviceID,
		DeviceCode:      deviceCode,
		ThingModelCode:  thingModelCode,
		GroupID:         groupID,
		GroupCode:       groupCode,
		GroupName:       groupName,
		GroupIntervalMs: groupInterval,
		CollectedAt:     b.CollectedAt,
		Records:         b.Records,
	}
}

// ToPushBatch 保留旧名称，等价于 ToBatch。
func (b StoredBatch) ToPushBatch() Batch { return b.ToBatch() }

// SpoolStore 断网本地缓存（持久化 outbox）接口。
//
// 通道断连/内存队列满时写入，重连后按 id（入队序，最旧在前）取回补发，
// 写入成功删除。实现方必须串行化底层存储访问（如 SQLite 单连接）。
//
// 组件自带 tsdb/spool/sqlite、tsdb/spool/postgres 实现；其它存储（MySQL、Redis 等）
// 只需实现本接口。
type SpoolStore interface {
	// Insert 持久化一个批次（records 序列化为 JSON）。
	Insert(batch Batch) error
	// InsertMany 在一个事务里持久化多个批次（一次多行 INSERT）。
	// 断网期把排队中的批次合并提交，避免逐批 fsync 的写放大；空切片为 no-op。
	InsertMany(batches []Batch) error
	// FetchOldest 按入队序取回最旧的至多 limit 个批次。
	FetchOldest(limit int) ([]StoredBatch, error)
	// Delete 删除单个已成功写入的批次。
	Delete(id int64) error
	// DeleteBatch 批量删除多个已成功写入的批次（单条 DELETE，回放路径合并提交）。
	DeleteBatch(ids []int64) error
	// DeleteOldest 删除最旧的 n 个批次（磁盘上限裁剪）。
	DeleteOldest(n int) error
	// Count 返回当前待补发批次数量。
	Count() (int64, error)
}
