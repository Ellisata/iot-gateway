// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package subscribe

import (
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

const (
	// devicePointTimeLayout 与项目内其它表的时间文本格式保持一致。
	devicePointTimeLayout = "2006-01-02 15:04:05"
	// devicePointQueueSize 待落库条目缓冲。满时丢弃本次入队，下一条报文会重试。
	devicePointQueueSize = 4096
	// devicePointBatchSize 单条 INSERT 的行数上限。
	devicePointBatchSize = 100
	// devicePointFlushInterval 攒批刷盘间隔。
	devicePointFlushInterval = time.Second
)

// devicePointSink 订阅记录中设备/点位元信息的出口。
type devicePointSink interface {
	Record(records []collector.CollectedRecord)
}

type devicePointKey struct {
	deviceID  string
	addressID string
}

// DevicePointKey 订阅设备/点位的自然键，用于从外部作废落库器的元信息指纹。
//
// 与 devicePointKey 是同一个键，导出这一份是为了让 service 层能指名作废
// （恢复自动收录 / 删除手工行后），而不必把内部 map 的形状暴露出去。
type DevicePointKey struct {
	DeviceID        string
	DeviceAddressID string
}

// devicePointStore 把订阅报文中的设备/点位元信息异步 upsert 到 SQLite。
//
// 收包协程只做内存查重与非阻塞入队；真正的写库由一个独立协程攒批完成。
// SQLite 连接池只有一条写连接（见 database/sqlite.InitDB），因此绝不能
// 在 paho 的收包协程里直接写库。
type devicePointStore struct {
	db *gorm.DB

	mu      sync.Mutex
	entries map[devicePointKey]string
	queue   chan po.MqttSubscriptionDevicePoint
	running bool
	quit    chan struct{}
	done    chan struct{}

	flushInterval time.Duration
	batchSize     int
}

func newDevicePointStore(db *gorm.DB) *devicePointStore {
	return &devicePointStore{
		db:            db,
		entries:       make(map[devicePointKey]string),
		flushInterval: devicePointFlushInterval,
		batchSize:     devicePointBatchSize,
	}
}

// Start 启动落库协程。
func (s *devicePointStore) Start() {
	if s == nil || s.db == nil {
		return
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.queue = make(chan po.MqttSubscriptionDevicePoint, devicePointQueueSize)
	s.quit = make(chan struct{})
	s.done = make(chan struct{})
	queue, quit, done := s.queue, s.quit, s.done
	s.mu.Unlock()

	go s.run(queue, quit, done)
}

// Stop 停止落库协程，并排空队列后刷出剩余条目（保证停机前的元信息不丢）。
func (s *devicePointStore) Stop() {
	if s == nil || s.db == nil {
		return
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	quit, done := s.quit, s.done
	s.quit, s.done = nil, nil
	s.mu.Unlock()

	close(quit)
	<-done
}

// Record 记录一批转换成功的订阅记录（非阻塞）。
//
// 首次出现的点位、元信息发生变化的点位才会入队；同一批里重复的点位只保留一次。
func (s *devicePointStore) Record(records []collector.CollectedRecord) {
	if s == nil || s.db == nil || len(records) == 0 {
		return
	}

	nowStr := time.Now().Format(devicePointTimeLayout)
	dropped := 0

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	for i := range records {
		r := &records[i]
		if r.DeviceID == "" || r.DeviceAddressID == "" {
			// 映射层已要求这两个字段，这里只是防御：没有身份就无法建索引。
			continue
		}

		key := devicePointKey{deviceID: r.DeviceID, addressID: r.DeviceAddressID}
		sig := devicePointSignature(r)
		if old, ok := s.entries[key]; ok && old == sig {
			continue
		}

		row := po.MqttSubscriptionDevicePoint{
			DeviceID:           r.DeviceID,
			DeviceAddressID:    r.DeviceAddressID,
			DeviceName:         r.DeviceName,
			DeviceAddressLabel: r.DeviceAddressLabel,
			Kind:               r.Kind,
			Source:             po.MqttSubscriptionDevicePointSourceAuto,
			CreatedAt:          nowStr,
			UpdatedAt:          nowStr,
		}

		select {
		case s.queue <- row:
			s.entries[key] = sig
		default:
			// 队列满：不更新缓存，下一条报文会再次尝试。
			dropped++
		}
	}
	s.mu.Unlock()

	if dropped > 0 {
		logger.Warn("subscribe: device point queue full, dropped %d entries", dropped)
	}
}

// Forget 作废这些点位的元信息指纹。
//
// 下一次 Record 会把它们当作首次出现重新入队，即使报文元信息没有变化。
// 恢复自动收录 / 删除手工行之后必须调用：否则 Record 比对指纹后一直跳过，
// 要等报文元信息变化或网关重启才会重新写库 —— 表现为"点了恢复，值却没变"。
//
// 只操作内存 map，不写库；未 Start 的 store 调用它也是安全的。
func (s *devicePointStore) Forget(keys []DevicePointKey) {
	if s == nil || len(keys) == 0 {
		return
	}

	s.mu.Lock()
	for _, k := range keys {
		delete(s.entries, devicePointKey{deviceID: k.DeviceID, addressID: k.DeviceAddressID})
	}
	s.mu.Unlock()
}

// run 消费队列并攒批写库。
func (s *devicePointStore) run(queue <-chan po.MqttSubscriptionDevicePoint,
	quit <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	batch := make([]po.MqttSubscriptionDevicePoint, 0, s.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.writeBatch(batch)
		batch = batch[:0]
	}

	for {
		select {
		case row := <-queue:
			batch = append(batch, row)
			if len(batch) >= s.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-quit:
			// 退出前排空队列：Stop 之后已不会再有人写入队列。
			for {
				select {
				case row := <-queue:
					batch = append(batch, row)
					if len(batch) >= s.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// writeBatch 以 (device_id, device_address_id) 为冲突键 upsert。
//
// 具体更新语义见 UpsertDevicePoints：空值不覆盖非空值，手工维护的行整体跳过。
func (s *devicePointStore) writeBatch(rows []po.MqttSubscriptionDevicePoint) {
	rows = dedupeDevicePoints(rows)
	if len(rows) == 0 {
		return
	}

	if err := UpsertDevicePoints(s.db, rows); err != nil {
		logger.Error("subscribe: upsert device points failed: %v", err)
	}
}

// dedupeDevicePoints 同一批次里同一自然键只保留一条，
// 避免单条 INSERT 内重复命中冲突键。
//
// 合并规则与 upsert 保持一致：后一条的非空元信息覆盖前一条，
// 后一条为空的字段保留前一条的值（否则同一批次内的空值会把名称丢掉）。
func dedupeDevicePoints(rows []po.MqttSubscriptionDevicePoint) []po.MqttSubscriptionDevicePoint {
	seen := make(map[devicePointKey]int, len(rows))
	out := make([]po.MqttSubscriptionDevicePoint, 0, len(rows))
	for _, r := range rows {
		key := devicePointKey{deviceID: r.DeviceID, addressID: r.DeviceAddressID}
		if idx, ok := seen[key]; ok {
			out[idx] = mergeDevicePoint(out[idx], r)
			continue
		}
		seen[key] = len(out)
		out = append(out, r)
	}
	return out
}

// mergeDevicePoint 把后到的行合并到先到的行上：非空覆盖、空值保留旧值。
func mergeDevicePoint(prev, next po.MqttSubscriptionDevicePoint) po.MqttSubscriptionDevicePoint {
	merged := next
	merged.DeviceName = firstNonEmpty(next.DeviceName, prev.DeviceName)
	merged.DeviceAddressLabel = firstNonEmpty(next.DeviceAddressLabel, prev.DeviceAddressLabel)
	merged.Kind = firstNonEmpty(next.Kind, prev.Kind)
	merged.CreatedAt = prev.CreatedAt
	return merged
}

func firstNonEmpty(next, prev string) string {
	if next != "" {
		return next
	}
	return prev
}

// devicePointSignature 元信息指纹：只有这些字段变化才需要重新写库。
func devicePointSignature(r *collector.CollectedRecord) string {
	return strings.Join([]string{
		r.DeviceName,
		r.DeviceAddressLabel,
		r.Kind,
	}, "\x00")
}
