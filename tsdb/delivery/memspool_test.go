// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package delivery

import (
	"sync"

	"github.com/Ellisata/iot-gateway/tsdb"
)

// memSpool 内存版 SpoolStore，供 tsdb 包内单测使用。
//
// 不 import tsdb/spool/sqlite：包内测试导入一个反过来依赖 tsdb 的子包会形成
// import cycle。真实 SQLite 实现由 tsdb/spool/sqlite 自己的测试覆盖。
type memSpool struct {
	mu sync.Mutex

	rows    []tsdb.StoredBatch
	nextID  int64
	insert  int
	batches [][]tsdb.Batch

	insertManyErr error
	insertErr     error
}

func (m *memSpool) Insert(b tsdb.Batch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insert++
	if m.insertErr != nil {
		return m.insertErr
	}
	m.nextID++
	m.rows = append(m.rows, storedFromBatch(m.nextID, b))
	return nil
}

func (m *memSpool) InsertMany(batches []tsdb.Batch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches = append(m.batches, append([]tsdb.Batch(nil), batches...))
	if m.insertManyErr != nil {
		return m.insertManyErr
	}
	for _, b := range batches {
		m.nextID++
		m.rows = append(m.rows, storedFromBatch(m.nextID, b))
	}
	return nil
}

func (m *memSpool) FetchOldest(limit int) ([]tsdb.StoredBatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || len(m.rows) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(m.rows) {
		n = len(m.rows)
	}
	out := make([]tsdb.StoredBatch, n)
	copy(out, m.rows[:n])
	return out, nil
}

func (m *memSpool) Delete(id int64) error { return m.DeleteBatch([]int64{id}) }
func (m *memSpool) DeleteBatch(ids []int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	drop := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		drop[id] = struct{}{}
	}
	kept := m.rows[:0]
	for _, r := range m.rows {
		if _, ok := drop[r.ID]; !ok {
			kept = append(kept, r)
		}
	}
	m.rows = kept
	return nil
}

func (m *memSpool) DeleteOldest(n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= 0 {
		return nil
	}
	if n > len(m.rows) {
		n = len(m.rows)
	}
	m.rows = append([]tsdb.StoredBatch(nil), m.rows[n:]...)
	return nil
}

func (m *memSpool) Count() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.rows)), nil
}

func storedFromBatch(id int64, b tsdb.Batch) tsdb.StoredBatch {
	return tsdb.StoredBatch{
		ID:              id,
		DeviceID:        b.DeviceID,
		DeviceCode:      b.DeviceCode,
		ThingModelCode:  b.ThingModelCode,
		GroupID:         b.GroupID,
		GroupCode:       b.GroupCode,
		GroupName:       b.GroupName,
		GroupIntervalMs: b.GroupIntervalMs,
		CollectedAt:     b.CollectedAt,
		Records:         b.Records,
	}
}
