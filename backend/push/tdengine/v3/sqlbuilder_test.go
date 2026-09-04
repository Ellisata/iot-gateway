// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"testing"
	"time"

	"iot-gateway/collector"
	"iot-gateway/driver"
)

func rec(deviceID, addressID, kind, value string, quality int) collector.CollectedRecord {
	return collector.CollectedRecord{
		DeviceID:          deviceID,
		DeviceName:        "设备A",
		DeviceAddressID:   addressID,
		DeviceAddressName: "点位" + addressID,
		Value:             value,
		DataType:          kind,
		Kind:              kind,
		Quality:           quality,
	}
}

func TestValueParts(t *testing.T) {
	cases := []struct {
		name               string
		kind, value        string
		vb, vi, vf, vs, vk string
	}{
		// KindBool → value_bool
		{"bool true", driver.KindBool, "1", "true", "NULL", "NULL", "NULL", "'bool'"},
		{"bool false", driver.KindBool, "0", "false", "NULL", "NULL", "NULL", "'bool'"},
		{"bool dirty", driver.KindBool, "abc", "NULL", "NULL", "NULL", "'abc'", "'string'"}, // 解析失败回落文本列
		// KindUInt → value_int
		{"uint", driver.KindUInt, "255", "NULL", "255", "NULL", "NULL", "'uint'"},
		{"uint float-write", driver.KindUInt, "1.5", "NULL", "NULL", "1.5", "NULL", "'float'"}, // 无符号解析失败 → 浮点
		{"uint dirty", driver.KindUInt, "oops", "NULL", "NULL", "NULL", "'oops'", "'string'"},
		// KindInt → value_int
		{"int", driver.KindInt, "1280", "NULL", "1280", "NULL", "NULL", "'int'"},
		{"int negative", driver.KindInt, "-5", "NULL", "-5", "NULL", "NULL", "'int'"},
		{"int float-write", driver.KindInt, "3.14", "NULL", "NULL", "3.14", "NULL", "'float'"}, // 整数解析失败 → 浮点
		{"int dirty", driver.KindInt, "oops", "NULL", "NULL", "NULL", "'oops'", "'string'"},
		// KindFloat → value_float(DOUBLE)
		{"float", driver.KindFloat, "25.5000", "NULL", "NULL", "25.5", "NULL", "'float'"},
		{"float dirty", driver.KindFloat, "oops", "NULL", "NULL", "NULL", "'oops'", "'string'"},
		// KindString / KindTime → value_str
		{"string", driver.KindString, "abc", "NULL", "NULL", "NULL", "'abc'", "'string'"},
		{"string quote-escape", driver.KindString, "AB'C", "NULL", "NULL", "NULL", `'AB\'C'`, "'string'"},
		{"time", driver.KindTime, "1700000000", "NULL", "NULL", "NULL", "'1700000000'", "'time'"},
		// 空值:值列全 NULL,kind 保留声明类别(未知按 string)
		{"empty int", driver.KindInt, "", "NULL", "NULL", "NULL", "NULL", "'int'"},
		{"empty unknown", "", "", "NULL", "NULL", "NULL", "NULL", "'string'"},
		// 空/未知 Kind:自动数值推断(int → float → string 降级)
		{"infer int", "", "42", "NULL", "42", "NULL", "NULL", "'int'"},
		{"infer float", "", "1.5", "NULL", "NULL", "1.5", "NULL", "'float'"},
		{"infer string", "unknown", "hello", "NULL", "NULL", "NULL", "'hello'", "'string'"},
	}
	for _, c := range cases {
		vb, vi, vf, vs, vk := valueParts(c.value, c.kind)
		if vb != c.vb || vi != c.vi || vf != c.vf || vs != c.vs || vk != c.vk {
			t.Errorf("%s: valueParts(%q, kind=%q) = (%s, %s, %s, %s, %s), want (%s, %s, %s, %s, %s)",
				c.name, c.value, c.kind, vb, vi, vf, vs, vk, c.vb, c.vi, c.vf, c.vs, c.vk)
		}
	}

	// 超长字符串:value_str 按 maxValueStr 截断
	long := strings.Repeat("长", 300)
	_, _, _, vs, _ := valueParts(long, driver.KindString)
	if strings.Count(vs, "长") != maxValueStr {
		t.Errorf("value_str should truncate to %d runes, got %d", maxValueStr, strings.Count(vs, "长"))
	}
}

// wantTS 将采集时间按与生产相同的本地时区解析为 epoch 毫秒
func wantTS(s string) int64 {
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	return t.UnixMilli()
}

func TestBuildInsertSQL(t *testing.T) {
	collectedAt := "2026-08-17 17:46:34"
	records := []collector.CollectedRecord{
		rec("dev-1", "40001", driver.KindInt, "1280", 192),
		rec("dev-1", "40003", driver.KindBool, "1", 192),
		rec("dev-1", "40005", driver.KindFloat, "25.5000", 192),
		rec("dev-1", "40007", driver.KindString, "AB'C", 0),
	}
	sql := buildInsertSQL("iot_db", "s_hand", collectedAt, records)

	// 注意:地址 "40001" 以数字开头,sanitizeIdent 补下划线前缀 → "_40001";表名带恒定哈希后缀
	// 值族:每行恰一值列非空、其余 NULL,value_kind 记录声明类别(列顺序与建表一致)
	checks := []string{
		"INSERT INTO ",
		"iot_db." + subtableName("dev-1", "40001") + " USING iot_db.s_hand",
		"TAGS ('dev-1', '40001')", // 仅稳定标识作 tag,名称不入库
		fmt.Sprintf("VALUES (%d, NULL, 1280, NULL, NULL, 'int', 192)", wantTS(collectedAt)),
		"iot_db." + subtableName("dev-1", "40003") + " USING iot_db.s_hand",
		fmt.Sprintf("VALUES (%d, true, NULL, NULL, NULL, 'bool', 192)", wantTS(collectedAt)), // Boolean 1 → true
		"iot_db." + subtableName("dev-1", "40005") + " USING iot_db.s_hand",
		fmt.Sprintf("VALUES (%d, NULL, NULL, 25.5, NULL, 'float', 192)", wantTS(collectedAt)), // Float 25.5000 → 25.5
		"iot_db." + subtableName("dev-1", "40007") + " USING iot_db.s_hand",
		"'AB\\'C', 'string', 0)", // 字符串转义 + quality=0
	}
	// 名称字段不应出现在 SQL 中
	if strings.Contains(sql, "设备A") || strings.Contains(sql, "点位") {
		t.Errorf("device/address names should not be persisted\nSQL: %s", sql)
	}
	for _, c := range checks {
		if !strings.Contains(sql, c) {
			t.Errorf("generated SQL missing %q\nSQL: %s", c, sql)
		}
	}
	// 单张超级表:不应出现按类型族拆分的表名
	for _, fam := range []string{"_int", "_float", "_bool", "_str"} {
		if strings.Contains(sql, "s_hand"+fam) {
			t.Errorf("SQL should not use family-split stable %q\nSQL: %s", "s_hand"+fam, sql)
		}
	}
}

func TestBuildInsertSQLTimestamp(t *testing.T) {
	collectedAt := "2026-08-18 10:00:00"
	sql := buildInsertSQL("iot_db", "s_hand", collectedAt,
		[]collector.CollectedRecord{rec("dev-1", "40001", driver.KindInt, "1280", 192)})
	want := fmt.Sprintf("VALUES (%d, NULL, 1280, NULL, NULL, 'int', 192)", wantTS(collectedAt))
	if !strings.Contains(sql, want) {
		t.Errorf("timestamp should be epoch millis, want %q\nSQL: %s", want, sql)
	}
}

func TestBuildInsertSQLDedupSubtable(t *testing.T) {
	records := []collector.CollectedRecord{
		rec("dev-1", "40001", driver.KindInt, "1280", 192),
		rec("dev-1", "40001", driver.KindInt, "999", 192), // 同子表(同设备+点位):应被去重
	}
	sql := buildInsertSQL("iot_db", "s_hand", "2026-08-18 10:00:00", records)
	if strings.Count(sql, subtableName("dev-1", "40001")+" USING") != 1 {
		t.Errorf("duplicate subtable should be deduped to 1\nSQL: %s", sql)
	}
	if strings.Contains(sql, ", 999, NULL") {
		t.Errorf("duplicate row value should be dropped\nSQL: %s", sql)
	}
}

func TestInsertBuilderAggregatesBatches(t *testing.T) {
	ib := newInsertBuilder("iot_db", "s_hand")
	b1 := []collector.CollectedRecord{rec("dev-1", "40001", driver.KindInt, "1280", 192)}
	b2 := []collector.CollectedRecord{rec("dev-2", "40001", driver.KindBool, "1", 192)}
	if !ib.appendBatch("2026-08-18 10:00:00", b1) {
		t.Fatal("first batch should append")
	}
	if !ib.appendBatch("2026-08-18 10:00:00", b2) {
		t.Fatal("distinct subtable second batch should append")
	}
	if ib.empty() {
		t.Fatal("builder should not be empty after appending")
	}
	sql := ib.String()
	// 单条 INSERT 内含两个子表子句(跨设备聚合),而非两条独立语句
	if strings.Count(sql, "INSERT INTO ") != 1 {
		t.Errorf("aggregated statement must contain exactly one INSERT INTO, got %d", strings.Count(sql, "INSERT INTO "))
	}
	if strings.Count(sql, "USING iot_db.s_hand") != 2 {
		t.Errorf("expected 2 subtable clauses in one INSERT, got %d", strings.Count(sql, "USING iot_db.s_hand"))
	}
	if !strings.Contains(sql, subtableName("dev-1", "40001")+" USING") ||
		!strings.Contains(sql, subtableName("dev-2", "40001")+" USING") {
		t.Errorf("both device subtables should be present\nSQL: %s", sql)
	}
}

func TestInsertBuilderCollisionAcrossBatches(t *testing.T) {
	ib := newInsertBuilder("iot_db", "s_hand")
	b1 := []collector.CollectedRecord{rec("dev-1", "40001", driver.KindInt, "1280", 192)}
	// 同设备+点位、不同轮次时间戳:同一语句内必须拒绝,跨轮数据不能被批内去重丢弃
	b2 := []collector.CollectedRecord{rec("dev-1", "40001", driver.KindInt, "999", 192)}

	if !ib.appendBatch("2026-08-18 10:00:00", b1) {
		t.Fatal("first batch should append")
	}
	if ib.appendBatch("2026-08-18 10:00:01", b2) {
		t.Fatal("conflicting subtable across batches must be rejected")
	}
	// 拒绝时状态不得改动:首条仍在,冲突值未混入
	sql := ib.String()
	sub := subtableName("dev-1", "40001") + " USING"
	if strings.Count(sql, sub) != 1 {
		t.Errorf("rejected batch must not mutate builder, USING count=%d", strings.Count(sql, sub))
	}
	if strings.Contains(sql, ", 999, NULL") {
		t.Errorf("rejected batch value must not appear\nSQL: %s", sql)
	}

	// flush(新窗口)后重试:被拒批次进入下一窗口写入,跨轮数据不丢。
	// 同一窗口内同子表始终只能一条(再次追加仍返回 false),跨轮靠切分语句保留。
	ib2 := newInsertBuilder("iot_db", "s_hand")
	if !ib2.appendBatch("2026-08-18 10:00:01", b2) {
		t.Fatal("conflicting batch should append in a fresh window after flush")
	}
	sql2 := ib2.String()
	if strings.Count(sql2, sub) != 1 {
		t.Errorf("fresh window statement should hold the second-cycle row, USING count=%d", strings.Count(sql2, sub))
	}
	if !strings.Contains(sql2, ", 999, NULL") {
		t.Errorf("second-cycle value should be written after flush\nSQL: %s", sql2)
	}
	// 同一窗口内再次追加同子表:仍拒绝(语句内子表唯一)
	if ib2.appendBatch("2026-08-18 10:00:02", b1) {
		t.Error("same-window duplicate subtable must still be rejected")
	}
}

func TestInsertBuilderDedupWithinBatch(t *testing.T) {
	ib := newInsertBuilder("iot_db", "s_hand")
	records := []collector.CollectedRecord{
		rec("dev-1", "40001", driver.KindInt, "1280", 192),
		rec("dev-1", "40001", driver.KindInt, "999", 192), // 批内同子表:保留首条
	}
	if !ib.appendBatch("2026-08-18 10:00:00", records) {
		t.Fatal("batch with internal duplicates should append")
	}
	sql := ib.String()
	sub := subtableName("dev-1", "40001") + " USING"
	if strings.Count(sql, sub) != 1 {
		t.Errorf("within-batch duplicate subtable should be deduped to 1, got %d", strings.Count(sql, sub))
	}
	if strings.Contains(sql, ", 999, NULL") {
		t.Errorf("within-batch duplicate value should be dropped\nSQL: %s", sql)
	}
}

func TestInsertBuilderEmpty(t *testing.T) {
	ib := newInsertBuilder("iot_db", "s_hand")
	if !ib.empty() || ib.String() != "" {
		t.Fatal("fresh builder should be empty and yield empty SQL")
	}
	ib.appendBatch("2026-08-18 10:00:00", []collector.CollectedRecord{rec("dev-1", "40001", driver.KindInt, "1", 192)})
	if ib.empty() {
		t.Fatal("builder should not be empty after append")
	}
	if !strings.HasPrefix(ib.String(), "INSERT INTO ") {
		t.Errorf("statement should start with INSERT INTO\nSQL: %s", ib.String())
	}
}

func TestBuildInsertSQLUnknownTypeAndDirtyValue(t *testing.T) {
	// 空/未知 Kind:自动数值推断 → value_float 原生 DOUBLE
	sql := buildInsertSQL("iot_db", "s_hand", "2026-08-18 10:00:00",
		[]collector.CollectedRecord{rec("dev-1", "A1", "", "3.14", 192)})
	if !strings.Contains(sql, "NULL, 3.14, NULL, 'float', 192") {
		t.Errorf("unknown numeric kind should infer float\nSQL: %s", sql)
	}
	// 数值类型脏数据:回落 value_str 文本列入库(kind=string),不整批失败
	sql = buildInsertSQL("iot_db", "s_hand", "2026-08-18 10:00:00",
		[]collector.CollectedRecord{rec("dev-1", "A1", driver.KindInt, "oops", 192)})
	if !strings.Contains(sql, "'oops', 'string', 192") {
		t.Errorf("dirty int value should fall back to text column\nSQL: %s", sql)
	}
}

func TestTruncateOversizedValue(t *testing.T) {
	long := strings.Repeat("长", 300) // 300 个字符,超出 value_str NCHAR 与 deviceId tag NCHAR 宽度
	r := rec(long, "40001", driver.KindString, long, 192)
	sql := buildInsertSQL("iot_db", "s_hand", "2026-08-18 10:00:00", []collector.CollectedRecord{r})
	// value_str 截断到 maxValueStr,deviceId tag 截断到 maxTagLen(名称字段不入库,不参与截断)
	if got := strings.Count(sql, "长"); got != maxValueStr+maxTagLen {
		t.Errorf("oversized value/tag should truncate (%d value + %d tag), got count=%d", maxValueStr, maxTagLen, got)
	}
}

func TestSubtableName(t *testing.T) {
	// 消毒 + 恒定哈希后缀:非法字符替换为下划线,地址以数字开头时补下划线前缀
	if got := subtableName("dev-1", "40001"); got != "dev_1__40001_"+subtableHash("dev-1", "40001") {
		t.Errorf("subtableName = %q", got)
	}
	// 超长:截断到 160 + 哈希后缀,保证不超 TDengine 192 字符上限
	long := strings.Repeat("a", 120)
	got := subtableName(long, long)
	if len(got) > 191 {
		t.Errorf("subtableName too long: len=%d", len(got))
	}
	if !strings.HasSuffix(got, "_"+fnvHash(long+"|"+long)) {
		t.Errorf("subtableName should carry fnv64 hash suffix, got %q", got)
	}
	// 消毒碰撞的原始串必须映射到不同子表:旧实现(仅消毒串)会共用子表名而 TAGS 不同,
	// 导致 TDengine 写入永久失败
	if a, b := subtableName("dev-1", "40001"), subtableName("dev_1", "40001"); a == b {
		t.Errorf("sanitize-colliding raw IDs must map to distinct subtables, both %q", a)
	}
}

func TestCollectedAtMillis(t *testing.T) {
	// 毫秒精度:亚秒级轮次时间戳唯一,避免 TDengine 同子表同时间戳覆盖
	if got := collectedAtMillis("2026-08-18 10:00:00.123"); got != wantTS("2026-08-18 10:00:00")+123 {
		t.Errorf("ms precision parse = %d, want %d", got, wantTS("2026-08-18 10:00:00")+123)
	}
	// 兼容旧秒级格式(跨重启的 spool 积压数据)
	if got := collectedAtMillis("2026-08-18 10:00:00"); got != wantTS("2026-08-18 10:00:00") {
		t.Errorf("second format parse = %d, want %d", got, wantTS("2026-08-18 10:00:00"))
	}
	// 解析失败回落当前时间(不阻断整批)
	if got := collectedAtMillis("oops"); got <= 0 {
		t.Errorf("fallback should be now millis, got %d", got)
	}
}

func fnvHash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 16)
}
