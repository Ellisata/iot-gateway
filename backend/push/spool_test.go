package push

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/collector"
	"iot-gateway/model/po"
)

// newTestSpoolDB 创建 :memory: 数据库并迁移 push_outbox 表
func newTestSpoolDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&po.PushOutbox{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	return db
}

// sampleBatch 构造一个测试批次
func sampleBatch(deviceID, collectedAt string, values ...string) PushBatch {
	recs := make([]collector.CollectedRecord, 0, len(values))
	for _, v := range values {
		recs = append(recs, collector.CollectedRecord{
			DeviceID: deviceID, DeviceAddressID: "a1", Value: v, Kind: "int", Quality: 192,
		})
	}
	return PushBatch{DeviceID: deviceID, CollectedAt: collectedAt, Records: recs}
}

func TestSqliteSpoolInsertFetchOrder(t *testing.T) {
	s := NewSqliteSpool(newTestSpoolDB(t), "c1")

	if err := s.Insert(sampleBatch("d1", "t1", "v1")); err != nil {
		t.Fatalf("insert #1 failed: %v", err)
	}
	if err := s.Insert(sampleBatch("d1", "t2", "v2", "v3")); err != nil {
		t.Fatalf("insert #2 failed: %v", err)
	}
	if err := s.Insert(sampleBatch("d1", "t3", "v4")); err != nil {
		t.Fatalf("insert #3 failed: %v", err)
	}

	got, err := s.FetchOldest(10)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// 按入队序（id ASC）返回，最旧在前
	for i, wantTs := range []string{"t1", "t2", "t3"} {
		if got[i].CollectedAt != wantTs {
			t.Errorf("batch[%d] collectedAt = %q, want %q", i, got[i].CollectedAt, wantTs)
		}
		if got[i].ID <= 0 {
			t.Errorf("batch[%d] id = %d, want > 0", i, got[i].ID)
		}
	}
	// 记录 JSON 往返还原
	if len(got[1].Records) != 2 || got[1].Records[0].Value != "v2" || got[1].Records[1].Value != "v3" {
		t.Errorf("records roundtrip failed: %+v", got[1].Records)
	}
	// Kind 携带 json tag:断网缓存序列化/反序列化后不丢失(补发时推送通道仍按 Kind 类型化)
	if got[1].Records[0].Kind != "int" {
		t.Errorf("record Kind should survive spool JSON roundtrip, got %q", got[1].Records[0].Kind)
	}
}

func TestSqliteSpoolFetchLimit(t *testing.T) {
	s := NewSqliteSpool(newTestSpoolDB(t), "c1")
	for i, ts := range []string{"t1", "t2", "t3"} {
		if err := s.Insert(sampleBatch("d1", ts)); err != nil {
			t.Fatalf("insert #%d failed: %v", i, err)
		}
	}

	got, err := s.FetchOldest(2)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].CollectedAt != "t1" || got[1].CollectedAt != "t2" {
		t.Errorf("limit fetch order = %q/%q, want t1/t2", got[0].CollectedAt, got[1].CollectedAt)
	}

	// limit ≤ 0 返回空
	if got, err := s.FetchOldest(0); err != nil || len(got) != 0 {
		t.Errorf("FetchOldest(0) = %d rows, err=%v; want 0, nil", len(got), err)
	}
}

func TestSqliteSpoolDeleteAndCount(t *testing.T) {
	s := NewSqliteSpool(newTestSpoolDB(t), "c1")
	if err := s.Insert(sampleBatch("d1", "t1")); err != nil {
		t.Fatalf("insert #1 failed: %v", err)
	}
	if err := s.Insert(sampleBatch("d1", "t2")); err != nil {
		t.Fatalf("insert #2 failed: %v", err)
	}

	if n, err := s.Count(); err != nil || n != 2 {
		t.Fatalf("count = %d, err=%v; want 2, nil", n, err)
	}

	got, err := s.FetchOldest(10)
	if err != nil || len(got) != 2 {
		t.Fatalf("fetch before delete failed: %v", err)
	}
	if err := s.Delete(got[0].ID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if n, _ := s.Count(); n != 1 {
		t.Errorf("count after delete = %d, want 1", n)
	}
	rest, _ := s.FetchOldest(10)
	if len(rest) != 1 || rest[0].CollectedAt != "t2" {
		t.Errorf("after delete first, remaining = %+v, want only t2", rest)
	}
}

func TestSqliteSpoolDeleteBatch(t *testing.T) {
	s := NewSqliteSpool(newTestSpoolDB(t), "c1")
	for _, ts := range []string{"t1", "t2", "t3", "t4"} {
		if err := s.Insert(sampleBatch("d1", ts)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	got, _ := s.FetchOldest(10)
	if err := s.DeleteBatch([]int64{got[0].ID, got[1].ID, got[3].ID}); err != nil {
		t.Fatalf("delete batch failed: %v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatalf("count after delete batch = %d, want 1", n)
	}
	rest, _ := s.FetchOldest(10)
	if len(rest) != 1 || rest[0].CollectedAt != "t3" {
		t.Errorf("after batch delete, remaining = %+v, want only t3", rest)
	}

	// 幂等:重复删除(RowsAffected=0)不影响计数
	if err := s.DeleteBatch([]int64{got[0].ID}); err != nil {
		t.Fatalf("idempotent delete failed: %v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("count after idempotent delete = %d, want 1", n)
	}
	// 空切片为 no-op
	if err := s.DeleteBatch(nil); err != nil {
		t.Fatalf("empty delete batch failed: %v", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Errorf("count after empty delete batch = %d, want 1", n)
	}
}

func TestSqliteSpoolDeleteOldest(t *testing.T) {
	s := NewSqliteSpool(newTestSpoolDB(t), "c1")
	for _, ts := range []string{"t1", "t2", "t3", "t4"} {
		if err := s.Insert(sampleBatch("d1", ts)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	if err := s.DeleteOldest(2); err != nil {
		t.Fatalf("delete oldest failed: %v", err)
	}

	if n, _ := s.Count(); n != 2 {
		t.Fatalf("count after trim = %d, want 2", n)
	}
	rest, _ := s.FetchOldest(10)
	if len(rest) != 2 || rest[0].CollectedAt != "t3" || rest[1].CollectedAt != "t4" {
		t.Errorf("after trim, remaining = %+v, want t3/t4", rest)
	}
}

func TestSqliteSpoolSeedsExistingCount(t *testing.T) {
	db := newTestSpoolDB(t)
	// 预置既有积压(模拟 push_outbox 跨重启持久化的待补发数据)
	s1 := NewSqliteSpool(db, "c1")
	if err := s1.Insert(sampleBatch("d1", "t1")); err != nil {
		t.Fatalf("insert #1 failed: %v", err)
	}
	if err := s1.Insert(sampleBatch("d1", "t2")); err != nil {
		t.Fatalf("insert #2 failed: %v", err)
	}

	// 新实例构造时应一次性预载既有积压计数,裁剪判断不再依赖每批 COUNT 查询
	s2 := NewSqliteSpool(db, "c1")
	if n, err := s2.Count(); err != nil || n != 2 {
		t.Fatalf("seeded count = %d, err=%v; want 2, nil", n, err)
	}

	// 内存计数随本实例操作同步增长
	if err := s2.Insert(sampleBatch("d1", "t3")); err != nil {
		t.Fatalf("insert #3 failed: %v", err)
	}
	if n, _ := s2.Count(); n != 3 {
		t.Errorf("count after insert on seeded instance = %d, want 3", n)
	}
	// 底层确实落库(经库查询验证,内存计数为实例私有,见 SqliteSpool 注释)
	if got, _ := s1.FetchOldest(10); len(got) != 3 {
		t.Errorf("underlying rows = %d, want 3", len(got))
	}
}

func TestSqliteSpoolIsolationByChannel(t *testing.T) {
	db := newTestSpoolDB(t)
	s1 := NewSqliteSpool(db, "c1")
	s2 := NewSqliteSpool(db, "c2")

	if err := s1.Insert(sampleBatch("d1", "t1")); err != nil {
		t.Fatalf("insert c1 failed: %v", err)
	}
	if err := s1.Insert(sampleBatch("d1", "t2")); err != nil {
		t.Fatalf("insert c1 failed: %v", err)
	}
	if err := s2.Insert(sampleBatch("d2", "t1")); err != nil {
		t.Fatalf("insert c2 failed: %v", err)
	}

	n1, _ := s1.Count()
	n2, _ := s2.Count()
	if n1 != 2 || n2 != 1 {
		t.Errorf("counts = %d/%d, want 2/1", n1, n2)
	}

	// 删除互不影响
	got, _ := s2.FetchOldest(10)
	if err := s2.Delete(got[0].ID); err != nil {
		t.Fatalf("delete c2 failed: %v", err)
	}
	n1, _ = s1.Count()
	if n1 != 2 {
		t.Errorf("c1 count after deleting c2 row = %d, want 2", n1)
	}
}
