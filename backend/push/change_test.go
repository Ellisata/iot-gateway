// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package push

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/configFile"
	"iot-gateway/model/po"
)

func changeRec(deviceID, addressID, value string, quality int) collector.CollectedRecord {
	return collector.CollectedRecord{
		DeviceID:        deviceID,
		DeviceAddressID: addressID,
		Value:           value,
		Quality:         quality,
	}
}

func changeOnlyConfig() *configFile.Config {
	return &configFile.Config{Push: configFile.PushConfig{ChangeOnly: true}}
}

// testNow 是 tracker 测试的固定时钟：变化过滤本身与时间无关，保活测试再显式推进它。
var testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// filterNoKeepalive 走「未开启周期保活」的路径，用于只关心变化语义的用例。
func filterNoKeepalive(tr *changeTracker, records []collector.CollectedRecord) []collector.CollectedRecord {
	return tr.filter(records, testNow, 0)
}

// ==================== changeTracker ====================

func TestChangeTrackerFilter(t *testing.T) {
	tr := newChangeTracker()

	// 首次出现：全部投递
	got := filterNoKeepalive(tr, []collector.CollectedRecord{
		changeRec("d1", "a1", "1", 192),
		changeRec("d1", "a2", "2", 192),
	})
	if len(got) != 2 {
		t.Fatalf("首次投递 %d 条，want 2", len(got))
	}

	// 值与质量码都没变：全部跳过
	if got := filterNoKeepalive(tr, []collector.CollectedRecord{
		changeRec("d1", "a1", "1", 192),
		changeRec("d1", "a2", "2", 192),
	}); len(got) != 0 {
		t.Fatalf("未变化仍投递 %d 条，want 0", len(got))
	}

	// 只有 a2 的值变了：只投 a2
	got = filterNoKeepalive(tr, []collector.CollectedRecord{
		changeRec("d1", "a1", "1", 192),
		changeRec("d1", "a2", "3", 192),
	})
	if len(got) != 1 || got[0].DeviceAddressID != "a2" {
		t.Fatalf("变化过滤结果 = %+v，want 仅 a2", got)
	}

	// 只有 a1 的质量码变了（值相同）：质量变化也算变化
	got = filterNoKeepalive(tr, []collector.CollectedRecord{
		changeRec("d1", "a1", "1", 1),
		changeRec("d1", "a2", "3", 192),
	})
	if len(got) != 1 || got[0].DeviceAddressID != "a1" {
		t.Fatalf("质量变化过滤结果 = %+v，want 仅 a1", got)
	}

	// 不同设备下的同名地址 ID 互不影响
	got = filterNoKeepalive(tr, []collector.CollectedRecord{
		changeRec("d2", "a1", "1", 1),
	})
	if len(got) != 1 {
		t.Fatalf("新设备的同位址 %d 条，want 1", len(got))
	}
}

// 键退化（缺设备 ID 或地址 ID）时一律透传：宁可重复，也不能把不同点位合并成一条。
func TestChangeTrackerPassthroughWithoutIdentity(t *testing.T) {
	tr := newChangeTracker()
	for i := 0; i < 2; i++ {
		got := filterNoKeepalive(tr, []collector.CollectedRecord{
			changeRec("", "a1", "1", 192),
			changeRec("d1", "", "2", 192),
		})
		if len(got) != 2 {
			t.Fatalf("第 %d 轮透传 %d 条，want 2", i+1, len(got))
		}
	}
}

// 物模型/属性编码变化也必须投递：它们是时序库的 tag，改绑定后稳定不变的点位
// 若继续被过滤，新 tag 的 series 会一直缺数（要等到值变化）。
func TestChangeTrackerDeliversOnCodeChange(t *testing.T) {
	tr := newChangeTracker()
	bound := changeRec("d1", "a1", "1", 192)
	bound.ThingModelCode = "th_sensor"
	bound.PropertyCode = "temperature"

	if got := filterNoKeepalive(tr, []collector.CollectedRecord{bound}); len(got) != 1 {
		t.Fatalf("首轮投递 %d 条，want 1", len(got))
	}
	if got := filterNoKeepalive(tr, []collector.CollectedRecord{bound}); len(got) != 0 {
		t.Fatalf("编码未变仍投递 %d 条，want 0", len(got))
	}

	rebound := bound
	rebound.PropertyCode = "humidity"
	if got := filterNoKeepalive(tr, []collector.CollectedRecord{rebound}); len(got) != 1 {
		t.Fatalf("改绑属性后投递 %d 条，want 1", len(got))
	}

	unbound := bound
	unbound.ThingModelCode = ""
	unbound.PropertyCode = ""
	if got := filterNoKeepalive(tr, []collector.CollectedRecord{unbound}); len(got) != 1 {
		t.Fatalf("解绑物模型后投递 %d 条，want 1", len(got))
	}
}

// 未变化但到期时必须再投一次；投递后计时重置，不会每轮重复。
func TestChangeTrackerKeepalive(t *testing.T) {
	tr := newChangeTracker()
	const interval = 5 * time.Minute
	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}

	// 首轮：首次出现，投递并开始计时
	if got := tr.filter(recs, testNow, interval); len(got) != 1 {
		t.Fatalf("首轮投递 %d 条，want 1", len(got))
	}
	// 未到期：跳过
	if got := tr.filter(recs, testNow.Add(4*time.Minute), interval); len(got) != 0 {
		t.Fatalf("未到期投递 %d 条，want 0", len(got))
	}
	// 到期：再投一次
	if got := tr.filter(recs, testNow.Add(5*time.Minute), interval); len(got) != 1 {
		t.Fatalf("到期投递 %d 条，want 1", len(got))
	}
	// 计时已从第二次投递重置：再过 4 分钟仍不投，再过 1 分钟才到期
	if got := tr.filter(recs, testNow.Add(9*time.Minute), interval); len(got) != 0 {
		t.Fatalf("第二轮未到期投递 %d 条，want 0", len(got))
	}
	if got := tr.filter(recs, testNow.Add(10*time.Minute), interval); len(got) != 1 {
		t.Fatalf("第二轮到期投递 %d 条，want 1", len(got))
	}
}

// 值变化立即投递，并把保活计时从"变化那一刻"重新起算：
// 不能沿用旧计时在原来 T+5m 的时刻再补一条重复数据。
func TestChangeTrackerKeepaliveResetsOnChange(t *testing.T) {
	tr := newChangeTracker()
	const interval = 5 * time.Minute

	if got := tr.filter([]collector.CollectedRecord{changeRec("d1", "a1", "1", 192)},
		testNow, interval); len(got) != 1 {
		t.Fatalf("首轮投递 %d 条，want 1", len(got))
	}

	changed := []collector.CollectedRecord{changeRec("d1", "a1", "2", 192)}
	if got := tr.filter(changed, testNow.Add(3*time.Minute), interval); len(got) != 1 {
		t.Fatalf("变化投递 %d 条，want 1", len(got))
	}
	// 原首轮计时本应在 T+5m 到期，但变化已重置计时
	if got := tr.filter(changed, testNow.Add(5*time.Minute), interval); len(got) != 0 {
		t.Fatalf("变化后沿用旧计时投递 %d 条，want 0", len(got))
	}
	// 从变化时刻起满 5 分钟才到期
	if got := tr.filter(changed, testNow.Add(8*time.Minute), interval); len(got) != 1 {
		t.Fatalf("变化后到期投递 %d 条，want 1", len(got))
	}
}

// interval=0 表示关闭：未变化的点位永远不重发。
func TestChangeTrackerKeepaliveDisabled(t *testing.T) {
	tr := newChangeTracker()
	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}

	if got := tr.filter(recs, testNow, 0); len(got) != 1 {
		t.Fatalf("首轮投递 %d 条，want 1", len(got))
	}
	if got := tr.filter(recs, testNow.Add(24*time.Hour), 0); len(got) != 0 {
		t.Fatalf("关闭保活后投递 %d 条，want 0", len(got))
	}
}

// 保活不改变"缺身份字段一律透传"的既有语义。
func TestChangeTrackerKeepalivePassthroughWithoutIdentity(t *testing.T) {
	tr := newChangeTracker()
	const interval = time.Minute
	recs := []collector.CollectedRecord{changeRec("", "a1", "1", 192)}

	for i := 0; i < 2; i++ {
		if got := tr.filter(recs, testNow, interval); len(got) != 1 {
			t.Fatalf("第 %d 轮透传 %d 条，want 1", i+1, len(got))
		}
	}
}

// ==================== Engine ====================

func TestPushRecordsChangeOnly(t *testing.T) {
	ch := &stubChannel{id: "c1"}
	e := NewEngine(nil, changeOnlyConfig())
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	recs := []collector.CollectedRecord{
		changeRec("d1", "a1", "1", 192),
		changeRec("d1", "a2", "2", 192),
	}

	e.PushRecords(recs)
	batches := ch.drain()
	if len(batches) != 1 || len(batches[0].Records) != 2 {
		t.Fatalf("首轮 = %d 批，want 1 批 2 条", len(batches))
	}

	// 原样再来一轮：不下发
	e.PushRecords(recs)
	if got := len(ch.drain()); got != 0 {
		t.Fatalf("未变化仍下发 %d 批，want 0", got)
	}

	// 一个点位变化：只下发那一条
	changed := []collector.CollectedRecord{
		changeRec("d1", "a1", "1", 192),
		changeRec("d1", "a2", "3", 192),
	}
	e.PushRecords(changed)
	batches = ch.drain()
	if len(batches) != 1 || len(batches[0].Records) != 1 || batches[0].Records[0].DeviceAddressID != "a2" {
		t.Fatalf("变化批次 = %+v，want 仅 a2", batches)
	}
}

// 关闭开关（默认零值）时保持旧行为：每轮都下发。
func TestPushRecordsChangeOnlyDisabled(t *testing.T) {
	ch := &stubChannel{id: "c1"}
	e := NewEngine(nil, nil)
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}
	e.PushRecords(recs)
	e.PushRecords(recs)
	if got := len(ch.drain()); got != 2 {
		t.Fatalf("关闭开关后下发 %d 批，want 2", got)
	}
}

// 没有通道时不得把记录记为"已投递"：否则后加的通道永远收不到稳定不变的点位。
func TestPushRecordsChangeOnlyDoesNotMarkWithoutChannels(t *testing.T) {
	e := NewEngine(nil, changeOnlyConfig())
	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}

	e.PushRecords(recs) // 此刻没有任何通道

	ch := &stubChannel{id: "c1"}
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	e.PushRecords(recs)
	if got := len(ch.drain()); got != 1 {
		t.Fatalf("通道就绪后收到 %d 批，want 1（首轮必须补上初值）", got)
	}
}

// MQTT 订阅数据不参与变化过滤：同值记录照样投递。
func TestPushRecordsToTimeSeriesIgnoresChangeOnly(t *testing.T) {
	plain := &stubChannel{id: "mqtt"}
	ts := &tsStub{stubChannel: stubChannel{id: "td"}, timeSeries: true}
	e := NewEngine(nil, changeOnlyConfig())
	e.mu.Lock()
	e.channels["mqtt"] = plain
	e.channels["td"] = ts
	e.mu.Unlock()

	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}
	e.PushRecords(recs) // 本地点位采集：首轮投递
	e.PushRecords(recs) // 未变化：跳过
	if got := len(plain.drain()); got != 1 {
		t.Fatalf("MQTT 通道收到 %d 批，want 1", got)
	}
	if got := len(ts.drain()); got != 1 {
		t.Fatalf("时序通道收到 %d 批，want 1", got)
	}

	// 订阅来源的同值记录：仍然投递
	e.PushRecordsToTimeSeries(recs)
	if got := len(ts.drain()); got != 1 {
		t.Fatalf("订阅数据收到 %d 批，want 1（不参与变化过滤）", got)
	}
}

// 新增通道后必须重置变化状态：新通道没有历史值，稳定不变的点位要先收到一份初值。
func TestRefreshNewChannelResetsChangeState(t *testing.T) {
	Register("stubchange", func(_ *gorm.DB, ch *po.PushChannel) (Channel, error) {
		return &stubChannel{id: ch.ID}, nil
	})
	// push_channel.name 有唯一约束，第二个通道换个类型名（构建器行为相同）
	Register("stubchange2", func(_ *gorm.DB, ch *po.PushChannel) (Channel, error) {
		return &stubChannel{id: ch.ID}, nil
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1) // :memory: 单连接
	if err := db.AutoMigrate(&po.PushChannel{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if err := db.Create(&po.PushChannel{ID: "c1", Name: "stubchange", ConfigJSON: `{}`, Status: 1}).Error; err != nil {
		t.Fatalf("insert c1 failed: %v", err)
	}

	e := NewEngine(db, changeOnlyConfig())
	e.refresh()
	defer e.Stop()

	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}
	e.PushRecords(recs)
	e.PushRecords(recs) // 未变化：跳过

	e.mu.RLock()
	c1 := e.channels["c1"].(*stubChannel)
	e.mu.RUnlock()
	if got := len(c1.drain()); got != 1 {
		t.Fatalf("c1 收到 %d 批，want 1", got)
	}

	// 新增第二个通道 → 重建通道表 → 变化状态清空
	if err := db.Create(&po.PushChannel{ID: "c2", Name: "stubchange2", ConfigJSON: `{}`, Status: 1}).Error; err != nil {
		t.Fatalf("insert c2 failed: %v", err)
	}
	e.refresh()

	e.mu.RLock()
	c2 := e.channels["c2"].(*stubChannel)
	e.mu.RUnlock()

	e.PushRecords(recs)
	if got := len(c2.drain()); got != 1 {
		t.Fatalf("新增通道收到 %d 批，want 1（必须补上初值）", got)
	}
	if got := len(c1.drain()); got != 1 {
		t.Fatalf("既有通道收到 %d 批，want 1（重置后随首轮一起重投）", got)
	}
}

// Engine 级验证：配置的 keepalive 周期确实传到了 tracker，并按采集轮次触发重发。
func TestPushRecordsKeepalive(t *testing.T) {
	cfg := changeOnlyConfig()
	cfg.Push.KeepaliveIntervalSec = 300 // 5 分钟

	ch := &stubChannel{id: "c1"}
	e := NewEngine(nil, cfg)
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	now := testNow
	e.now = func() time.Time { return now }

	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}

	e.PushRecords(recs) // 首轮
	if got := len(ch.drain()); got != 1 {
		t.Fatalf("首轮下发 %d 批，want 1", got)
	}

	e.PushRecords(recs) // 未到期
	if got := len(ch.drain()); got != 0 {
		t.Fatalf("未到期下发 %d 批，want 0", got)
	}

	now = now.Add(5 * time.Minute)
	e.PushRecords(recs) // 到期
	batches := ch.drain()
	if len(batches) != 1 || len(batches[0].Records) != 1 {
		t.Fatalf("到期下发 %+v，want 1 批 1 条", batches)
	}
	if batches[0].Records[0].Value != "1" {
		t.Fatalf("保活重发的值 = %q，want 当前采集值 1", batches[0].Records[0].Value)
	}
}

// changeOnly 关闭时每轮本来就全发，keepalive 配置不产生额外行为。
func TestPushRecordsKeepaliveIgnoredWhenChangeOnlyOff(t *testing.T) {
	cfg := &configFile.Config{Push: configFile.PushConfig{
		ChangeOnly:           false,
		KeepaliveIntervalSec: 60,
	}}
	ch := &stubChannel{id: "c1"}
	e := NewEngine(nil, cfg)
	e.mu.Lock()
	e.channels["c1"] = ch
	e.mu.Unlock()

	recs := []collector.CollectedRecord{changeRec("d1", "a1", "1", 192)}
	e.PushRecords(recs)
	e.PushRecords(recs)
	if got := len(ch.drain()); got != 2 {
		t.Fatalf("changeOnly 关闭时下发 %d 批，want 2", got)
	}
}
