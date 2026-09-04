// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"fmt"
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

// wantTS 将采集时间按与生产相同的本地时区解析为 epoch 毫秒
func wantTS(s string) int64 {
	t, _ := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	return t.UnixMilli()
}

func TestBuildLine(t *testing.T) {
	collectedAt := "2026-08-18 10:00:00"
	ts := wantTS(collectedAt)

	line, ok := buildLine("collected_data", ts, rec("dev-1", "40001", driver.KindInt, "1280", 192))
	if !ok {
		t.Fatal("buildLine should succeed")
	}
	want := fmt.Sprintf(`collected_data,device_id=dev-1,device_address_id=40001 value_int=1280i,value_kind="int",quality=192i %d`, ts)
	if line != want {
		t.Errorf("line = %q\nwant %q", line, want)
	}
}

func TestFormatFieldValue(t *testing.T) {
	cases := []struct {
		kind  string
		value string
		col   string
		lit   string
		knd   string
		ok    bool
	}{
		// bool:落到 value_bool
		{driver.KindBool, "1", fieldBool, "true", `"bool"`, true},
		{driver.KindBool, "0", fieldBool, "false", `"bool"`, true},
		{driver.KindBool, "abc", fieldStr, `"abc"`, `"string"`, true}, // 脏布尔:回落 value_str
		// string / time:原样字符串落 value_str,value_kind 保留声明类别
		{driver.KindTime, "1700000000", fieldStr, `"1700000000"`, `"time"`, true},
		{driver.KindTime, "3000", fieldStr, `"3000"`, `"time"`, true},
		{driver.KindTime, "12:00:00", fieldStr, `"12:00:00"`, `"time"`, true},
		{driver.KindTime, "200", fieldStr, `"200"`, `"time"`, true},
		{driver.KindTime, "2026-08-19", fieldStr, `"2026-08-19"`, `"time"`, true},
		{driver.KindString, "abc", fieldStr, `"abc"`, `"string"`, true},
		{driver.KindString, "A", fieldStr, `"A"`, `"string"`, true},
		// 无符号整数:统一按有符号 i 写、落 value_int(InfluxDB 3.x 同列仅允许一种类型,
		// 避免同表内 signed/unsigned 点位在 integer/uinteger 间翻转;uint32 全量仍在 int64 内)
		{driver.KindUInt, "255", fieldInt, "255i", `"uint"`, true},
		{driver.KindUInt, "65535", fieldInt, "65535i", `"uint"`, true},
		{driver.KindUInt, "4294967295", fieldInt, "4294967295i", `"uint"`, true},
		{driver.KindUInt, "1234", fieldInt, "1234i", `"uint"`, true},
		{driver.KindUInt, "12345678", fieldInt, "12345678i", `"uint"`, true},
		{driver.KindInt, "1280", fieldInt, "1280i", `"int"`, true},
		{driver.KindInt, "-5", fieldInt, "-5i", `"int"`, true},
		{driver.KindInt, "2147483647", fieldInt, "2147483647i", `"int"`, true},
		{driver.KindFloat, "25.5000", fieldFloat, "25.5", `"float"`, true},
		{driver.KindFloat, "3.14", fieldFloat, "3.14", `"float"`, true},
		// 数值类型脏数据:回落 value_str 文本列保留原值(不再跳过,不翻转数值列类型)
		{driver.KindUInt, "oops", fieldStr, `"oops"`, `"string"`, true},
		{driver.KindInt, "3.14", fieldStr, `"3.14"`, `"string"`, true},
		// 超 int64(理论仅 uint64 全量寄存器):value_int 保持 integer,回落 value_str 文本列
		{driver.KindUInt, "18446744073709551615", fieldStr, `"18446744073709551615"`, `"string"`, true},
		// 浮点 NaN/±Inf:行协议无法表示,跳过而非写成非法字面量
		{driver.KindFloat, "NaN", "", "", "", false},
		{driver.KindFloat, "+Inf", "", "", "", false},
		{driver.KindFloat, "-Inf", "", "", "", false},
		// 空值:跳过
		{driver.KindInt, "", "", "", "", false},
		{driver.KindString, "", "", "", "", false},
		// 空/未知 Kind(历史数据或未注册类型):自动推断 int → float → 字符串
		{"", "42", fieldInt, "42i", `"int"`, true},
		{"", "1.5", fieldFloat, "1.5", `"float"`, true},
		{"", "hello", fieldStr, `"hello"`, `"string"`, true},
		{"unknown", "42", fieldInt, "42i", `"int"`, true},
	}
	for _, c := range cases {
		got, ok := formatFieldValue(c.value, c.kind)
		if ok != c.ok {
			t.Errorf("formatFieldValue(%q, kind=%q) ok=%v, want %v", c.value, c.kind, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.col != c.col || got.lit != c.lit || got.kind != c.knd {
			t.Errorf("formatFieldValue(%q, kind=%q) = {%s %s %s}, want {%s %s %s}",
				c.value, c.kind, got.col, got.lit, got.kind, c.col, c.lit, c.knd)
		}
	}
}

func TestBuildLineEscaping(t *testing.T) {
	// tag 值含逗号/等号/空格/反斜杠:均须转义
	r := rec("dev,1", "a=b c", driver.KindString, `say "hi"\n`, 0)
	line, ok := buildLine("collected_data", wantTS("2026-08-18 10:00:00"), r)
	if !ok {
		t.Fatal("buildLine should succeed")
	}
	for _, want := range []string{
		`device_id=dev\,1`,
		`device_address_id=a\=b\ c`,
		`value_str="say \"hi\"\\n"`, // 字符串 field:转义引号与反斜杠
	} {
		if !strings.Contains(line, want) {
			t.Errorf("line missing %q\nline: %s", want, line)
		}
	}
}

func TestBuildLineNamesNotPersisted(t *testing.T) {
	line, ok := buildLine("collected_data", wantTS("2026-08-18 10:00:00"),
		rec("dev-1", "40001", driver.KindInt, "1", 192))
	if !ok {
		t.Fatal("buildLine should succeed")
	}
	// 名称字段可变,不作 tag 入库(与 tdengine 通道设计一致)
	if strings.Contains(line, "设备A") || strings.Contains(line, "点位") {
		t.Errorf("device/address names should not be persisted\nline: %s", line)
	}
}

func TestBuildLineSkipInvalid(t *testing.T) {
	// 数值类型脏数据:不再跳过,回落 value_str 文本列保留原值
	if _, ok := buildLine("m", 0, rec("d", "a", driver.KindInt, "oops", 192)); !ok {
		t.Error("dirty numeric should fall back to value_str, not be skipped")
	}
	// 空值:跳过
	if _, ok := buildLine("m", 0, rec("d", "a", driver.KindString, "", 192)); ok {
		t.Error("empty value should be skipped")
	}
	// 浮点 NaN:行协议无法表示,跳过
	if _, ok := buildLine("m", 0, rec("d", "a", driver.KindFloat, "NaN", 192)); ok {
		t.Error("NaN value should be skipped")
	}
}

func TestLineBuilderAppendsAndDedup(t *testing.T) {
	lb := newLineBuilder("collected_data")
	ts := wantTS("2026-08-18 10:00:00")
	body := lb.String()
	if body != "" || !lb.empty() {
		t.Fatal("fresh builder should be empty")
	}

	records := []collector.CollectedRecord{
		rec("dev-1", "40001", driver.KindInt, "1280", 192),
		rec("dev-1", "40001", driver.KindInt, "999", 192), // 批内同(设备,地址):保留首条
		rec("dev-1", "40002", driver.KindBool, "1", 192),
	}
	lb.appendBatch("2026-08-18 10:00:00", records)
	if lb.empty() {
		t.Fatal("builder should not be empty after append")
	}
	body = lb.String()
	if strings.Contains(body, "999i") {
		t.Errorf("within-batch duplicate series should be dropped\nbody: %s", body)
	}
	want := fmt.Sprintf(`collected_data,device_id=dev-1,device_address_id=40001 value_int=1280i,value_kind="int",quality=192i %d`, ts)
	if !strings.Contains(body, want) {
		t.Errorf("body missing first record\nbody: %s", body)
	}
	// 同批混排不同 Kind:int 与 bool 各落 value_int/value_bool,不再触发同表 field 类型锁
	wantBool := fmt.Sprintf(`collected_data,device_id=dev-1,device_address_id=40002 value_bool=true,value_kind="bool",quality=192i %d`, ts)
	if !strings.Contains(body, wantBool) {
		t.Errorf("body missing bool record\nbody: %s", body)
	}
	// 每行一个 point,新行分隔
	if strings.Count(body, "\n") != 1 {
		t.Errorf("expected 2 points separated by newline, got body %q", body)
	}
}

func TestLineBuilderAggregatesBatches(t *testing.T) {
	lb := newLineBuilder("m")
	lb.appendBatch("2026-08-18 10:00:00", []collector.CollectedRecord{rec("dev-1", "a", driver.KindInt, "1", 192)})
	lb.appendBatch("2026-08-18 10:00:01", []collector.CollectedRecord{rec("dev-2", "a", driver.KindBool, "1", 192)})
	if lb.points != 2 {
		t.Fatalf("points = %d, want 2", lb.points)
	}
	// 不同采集时间即不同 point,聚合后同一 body 内两条行;int/bool 各落 value_int/value_bool,
	// 不同 Kind 共存不触发 InfluxDB 的同表 field 类型锁
	if strings.Count(lb.String(), "\n") != 1 {
		t.Errorf("two batches should join into one body with two lines\nbody: %s", lb.String())
	}
}

func TestCollectedAtMillis(t *testing.T) {
	// 毫秒精度:亚秒级轮次时间戳唯一
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

func TestEscapeTag(t *testing.T) {
	cases := map[string]string{
		"plain":          "plain",
		"a,b":            `a\,b`,
		"a=b":            `a\=b`,
		"a b":            `a\ b`,
		`a\b`:            `a\\b`,
		`a\,b`:           `a\\\,b`, // 原始反斜杠+逗号 → 反斜杠转义后再转义逗号
		"collected_data": "collected_data",
	}
	for in, want := range cases {
		if got := escapeTag(in); got != want {
			t.Errorf("escapeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteFieldString(t *testing.T) {
	if got := quoteFieldString(`say "hi"`); got != `"say \"hi\""` {
		t.Errorf("quoteFieldString = %q", got)
	}
	if got := quoteFieldString(`a\b`); got != `"a\\b"` {
		t.Errorf("quoteFieldString backslash = %q", got)
	}
	// 原始换行/回车必须转为可见转义序列,否则截断行协议行导致整窗 400
	if got := quoteFieldString("a\nb"); got != `"a\\nb"` {
		t.Errorf("quoteFieldString newline = %q, want %q", got, `"a\\nb"`)
	}
	if got := quoteFieldString("a\r\nb"); got != `"a\\r\\nb"` {
		t.Errorf("quoteFieldString CRLF = %q, want %q", got, `"a\\r\\nb"`)
	}
}

func TestBuildLineNewlineInStringValue(t *testing.T) {
	// PLC String 值含换行:渲染为可见转义序列,且生成的行不含真实换行符(单行 point)
	r := rec("dev-1", "a1", driver.KindString, "line1\nline2", 192)
	line, ok := buildLine("m", 123, r)
	if !ok {
		t.Fatal("buildLine should succeed for newline-containing string")
	}
	if strings.Count(line, "\n") != 0 {
		t.Errorf("line must not contain raw newline, got %q", line)
	}
	if !strings.Contains(line, `value_str="line1\\nline2"`) {
		t.Errorf("line should contain escaped newline, got %q", line)
	}
}

func TestFloatFallbackScientific(t *testing.T) {
	// 浮点超大值回落科学计数,避免过长 body
	if got := formatFloat(1e300); got != "1e+300" {
		t.Errorf("formatFloat(1e300) = %q, want 1e+300", got)
	}
	if got := formatFloat(25.0); got != "25" {
		t.Errorf("formatFloat(25.0) = %q, want 25 (float 无后缀)", got)
	}
}
