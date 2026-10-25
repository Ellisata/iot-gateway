// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"testing"
	"time"

	"github.com/gopcua/opcua/id"
	"github.com/gopcua/opcua/ua"

	"github.com/Ellisata/iot-gateway-sdk/driver"
)

func TestCodecForCommonDataType(t *testing.T) {
	cases := []struct {
		common string
		kind   string
		typeID uint32
		input  string
		want   any
	}{
		{"Boolean", "bool", id.Boolean, "1", true},
		{"Byte", "uint", id.Byte, "200", uint8(200)},
		{"Short", "int", id.Int16, "-12", int16(-12)},
		{"Word", "uint", id.UInt16, "65530", uint16(65530)},
		{"Long", "int", id.Int32, "-100000", int32(-100000)},
		{"DWord", "uint", id.UInt32, "4000000000", uint32(4000000000)},
		{"Float", "float", id.Float, "1.5", float32(1.5)},
		{"Double", "float", id.Double, "-2.25", float64(-2.25)},
		{"String", "string", id.String, "SN-1", "SN-1"},
		{"Char", "string", id.String, "A", "A"},
		// BCD 必须保留原始数字串：强行转整数会丢掉前导 0/小数位语义。
		{"BCD", "string", id.String, "0012", "0012"},
		// 协议内部类型名（导入/旧数据）也能识别。
		{"int16", "", id.Int16, "3", int16(3)},
		{"float64", "", id.Double, "3.5", float64(3.5)},
		{"uint32", "", id.UInt32, "4000000000", uint32(4000000000)},
		// 不认识通用类型时按 Kind 兜底。
		{"", "int", id.Int64, "7", int64(7)},
		{"", "uint", id.UInt64, "7", uint64(7)},
		{"", "float", id.Double, "7.5", float64(7.5)},
		{"Unknown", "string", id.String, "x", "x"},
	}
	for _, c := range cases {
		codec := codecFor(c.common, c.kind)
		if codec.typeID != c.typeID {
			t.Errorf("codecFor(%q,%q).typeID = %d, want %d", c.common, c.kind, codec.typeID, c.typeID)
			continue
		}
		got, err := codec.parse(c.input)
		if err != nil {
			t.Errorf("codecFor(%q,%q).parse(%q): %v", c.common, c.kind, c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("codecFor(%q,%q).parse(%q) = %#v, want %#v", c.common, c.kind, c.input, got, c.want)
		}
	}
}

func TestDateCodec(t *testing.T) {
	codec := codecFor("Date", "time")
	got, err := codec.parse("2026-10-24 12:34:56.789")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := time.Date(2026, 10, 24, 12, 34, 56, 789000000, time.Local)
	if !got.(time.Time).Equal(want) {
		t.Errorf("parsed = %v, want %v", got, want)
	}
}

func TestQualityStatus(t *testing.T) {
	cases := map[int]ua.StatusCode{
		driver.QualityGood:       ua.StatusGood,
		driver.QualityUncertain:  ua.StatusUncertain,
		driver.QualityBadAddress: ua.StatusBadConfigurationError,
		driver.QualityBadComms:   ua.StatusBadCommunicationError,
		driver.QualityBadParse:   ua.StatusBadDataEncodingInvalid,
		driver.QualityBad:        ua.StatusBadInternalError,
		// 未知子码按高 2 位归类。
		driver.QualityGood | 3:      ua.StatusGood,
		driver.QualityUncertain | 3: ua.StatusUncertain,
		5:                           ua.StatusBadInternalError,
	}
	for q, want := range cases {
		if got := qualityStatus(q); got != want {
			t.Errorf("qualityStatus(%d) = %v, want %v", q, got, want)
		}
	}
}

func TestBuildDataValue(t *testing.T) {
	codec := codecFor("Double", "float")
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local)

	dv := buildDataValue(codec, "21.5", nil, driver.QualityGood, at)
	if dv.Status != ua.StatusGood {
		t.Fatalf("status = %v, want Good", dv.Status)
	}
	if got := dv.Value.Value(); got != float64(21.5) {
		t.Errorf("value = %#v, want 21.5", got)
	}
	if !dv.SourceTimestamp.Equal(at) {
		t.Errorf("source timestamp = %v, want %v", dv.SourceTimestamp, at)
	}

	// 解析失败且没有上一个值：退化为该类型的零值，但不能把字符串塞进 Double 节点。
	dv = buildDataValue(codec, "not-a-number", nil, driver.QualityGood, at)
	if dv.Status != ua.StatusBadDataEncodingInvalid {
		t.Errorf("status = %v, want BadDataEncodingInvalid", dv.Status)
	}
	if got := dv.Value.Value(); got != float64(0) {
		t.Errorf("value = %#v, want zero", got)
	}

	// 解析失败但有上一个好值：保留上一个值，状态置坏。
	prev := buildDataValue(codec, "10", nil, driver.QualityGood, at)
	dv = buildDataValue(codec, "", prev, driver.QualityBadComms, at)
	if dv.Status != ua.StatusBadCommunicationError {
		t.Errorf("status = %v, want BadCommunicationError", dv.Status)
	}
	if got := dv.Value.Value(); got != float64(10) {
		t.Errorf("value = %#v, want previous 10", got)
	}
}

func TestSameDataValueIgnoresTimestamps(t *testing.T) {
	codec := codecFor("Short", "int")
	a := buildDataValue(codec, "1", nil, driver.QualityGood, time.Now())
	b := buildDataValue(codec, "1", nil, driver.QualityGood, time.Now().Add(time.Second))
	if !sameDataValue(a, b) {
		t.Error("same value+status should be equal")
	}
	c := buildDataValue(codec, "2", nil, driver.QualityGood, time.Now())
	if sameDataValue(a, c) {
		t.Error("different values should not be equal")
	}
	d := buildDataValue(codec, "1", nil, driver.QualityBadComms, time.Now())
	if sameDataValue(a, d) {
		t.Error("different status should not be equal")
	}
}

func TestParseCollectedAt(t *testing.T) {
	if got := parseCollectedAt(""); time.Since(got) > time.Minute {
		t.Errorf("empty collectedAt should fall back to now, got %v", got)
	}
	got := parseCollectedAt("2026-10-24 12:00:00.500")
	want := time.Date(2026, 10, 24, 12, 0, 0, 500000000, time.Local)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
