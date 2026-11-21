// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package iotdb

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/model"
)

func TestValueParts(t *testing.T) {
	cases := []struct {
		name, value, kind string
		wantMeas, wantTyp string
		wantVal           any
		wantOK            bool
	}{
		{"bool", "true", driver.KindBool, measurementValueBool, "BOOLEAN", true, true},
		{"bool unparsable", "abc", driver.KindBool, measurementValueStr, "TEXT", "abc", true},
		{"int", "-42", driver.KindInt, measurementValueInt, "INT64", int64(-42), true},
		{"int as float", "3.14", driver.KindInt, measurementValueFloat, "DOUBLE", 3.14, true},
		{"uint max int64", "9223372036854775807", driver.KindUInt, measurementValueInt, "INT64", int64(math.MaxInt64), true},
		{"uint overflow", "9223372036854775808", driver.KindUInt, measurementValueStr, "TEXT", "9223372036854775808", true},
		{"float", "1.5", driver.KindFloat, measurementValueFloat, "DOUBLE", 1.5, true},
		{"float nan", "NaN", driver.KindFloat, measurementValueStr, "TEXT", "NaN", true},
		{"float inf", "+Inf", driver.KindFloat, measurementValueStr, "TEXT", "+Inf", true},
		{"string", "ok", driver.KindString, measurementValueStr, "TEXT", "ok", true},
		{"time", "2026-10-19 17:05:30.123", driver.KindTime, measurementValueStr, "TEXT", "2026-10-19 17:05:30.123", true},
		{"unknown int", "42", "", measurementValueInt, "INT64", int64(42), true},
		{"unknown float", "4.2", "", measurementValueFloat, "DOUBLE", 4.2, true},
		{"unknown text", "abc", "", measurementValueStr, "TEXT", "abc", true},
		{"empty", "", driver.KindFloat, "", "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			meas, typ, val, ok := valueParts(c.value, c.kind)
			if ok != c.wantOK || meas != c.wantMeas || typ != c.wantTyp || val != c.wantVal {
				t.Fatalf("valueParts(%q,%q) = (%q,%q,%v,%v), want (%q,%q,%v,%v)",
					c.value, c.kind, meas, typ, val, ok, c.wantMeas, c.wantTyp, c.wantVal, c.wantOK)
			}
		})
	}
}

func TestBuildRowStructure(t *testing.T) {
	r := model.Record{
		DeviceID:        "dev-1",
		DeviceAddressID: "temp",
		Value:           "23.5",
		Kind:            driver.KindFloat,
		Quality:         3,
		ThingModelCode:  "tm",
		PropertyCode:    "prop",
		CollectedAt:     "2026-10-19 17:05:30.123",
	}
	row := buildRow("root.iot", r, 1)

	wantMeas := []string{measurementValueFloat, measurementQuality, measurementThingModelCode, measurementPropertyCode}
	wantTyp := []string{"DOUBLE", "INT32", "TEXT", "TEXT"}
	if len(row.Measurements) != len(wantMeas) {
		t.Fatalf("measurements = %v, want %v", row.Measurements, wantMeas)
	}
	for i := range wantMeas {
		if row.Measurements[i] != wantMeas[i] || row.Types[i] != wantTyp[i] {
			t.Fatalf("column %d = (%q,%q), want (%q,%q)", i, row.Measurements[i], row.Types[i], wantMeas[i], wantTyp[i])
		}
	}
	if !strings.HasPrefix(row.Device, "root.iot.dev_1_temp_") {
		t.Fatalf("device path = %q", row.Device)
	}
	if row.Timestamp != collectedAtMillis(r.CollectedAt) {
		t.Fatalf("timestamp = %d, want %d", row.Timestamp, collectedAtMillis(r.CollectedAt))
	}
}

func TestBuildRowEmptyValueKeepsQualityOnly(t *testing.T) {
	row := buildRow("root.iot", model.Record{DeviceID: "d", DeviceAddressID: "a", Quality: 192}, 42)
	if len(row.Measurements) != 1 || row.Measurements[0] != measurementQuality {
		t.Fatalf("measurements = %v, want [quality]", row.Measurements)
	}
	if row.Timestamp != 42 {
		t.Fatalf("timestamp = %d, want batch fallback 42", row.Timestamp)
	}
}

func TestPayloadRoundTripPreservesInt64(t *testing.T) {
	rb := newRowBuilder("root.iot")
	b := tsdb.Batch{
		CollectedAt: "2026-10-19 17:05:30.000",
		Records: []model.Record{{
			DeviceID:        "d",
			DeviceAddressID: "a",
			Value:           "9223372036854775807",
			Kind:            driver.KindInt,
		}},
	}
	if !rb.Append(b) {
		t.Fatal("Append should accept batch")
	}
	if rb.Rows() != 1 {
		t.Fatalf("Rows = %d, want 1", rb.Rows())
	}

	payload := rb.Payload()
	deviceIDs, _, _, values, _, err := decodePayload(payload)
	if err != nil {
		t.Fatalf("decodePayload failed: %v", err)
	}
	if len(deviceIDs) != 1 {
		t.Fatalf("rows = %d, want 1", len(deviceIDs))
	}
	got, ok := values[0][0].(int64)
	if !ok || got != math.MaxInt64 {
		t.Fatalf("value = %v (%T), want int64 %d", values[0][0], values[0][0], int64(math.MaxInt64))
	}
}

func TestBatchTimestampFallbackShared(t *testing.T) {
	rb := newRowBuilder("root.iot")
	rb.Append(tsdb.Batch{
		CollectedAt: "2026-10-19 17:05:30.123",
		Records: []model.Record{
			{DeviceID: "d1", DeviceAddressID: "a1", Value: "1"},
			{DeviceID: "d2", DeviceAddressID: "a2", Value: "2"},
		},
	})

	var rows []payloadRow
	if err := json.Unmarshal([]byte(rb.Payload()), &rows); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	want := collectedAtMillis("2026-10-19 17:05:30.123")
	for i, row := range rows {
		if row.Timestamp != want {
			t.Errorf("row %d timestamp = %d, want %d", i, row.Timestamp, want)
		}
	}
}

func TestDeviceNodeHashPreventsSanitizeCollision(t *testing.T) {
	a := deviceNode("dev-1", "addr")
	b := deviceNode("dev_1", "addr")
	if a == b {
		t.Fatalf("device nodes collide: %q", a)
	}
	long := deviceNode(strings.Repeat("x", 500), strings.Repeat("y", 500))
	if len(long) > 177 {
		t.Fatalf("device node too long: %d", len(long))
	}
}

func TestDecodePayloadRejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"invalid json":       `{`,
		"empty device":       `[{"d":"","t":1,"m":["quality"],"ty":["INT32"],"v":[0]}]`,
		"length mismatch":    `[{"d":"root.iot.a","t":1,"m":["value_int"],"ty":[],"v":[1]}]`,
		"null value":         `[{"d":"root.iot.a","t":1,"m":["value_int"],"ty":["INT64"],"v":[null]}]`,
		"unknown type":       `[{"d":"root.iot.a","t":1,"m":["x"],"ty":["NOPE"],"v":[1]}]`,
		"double from string": `[{"d":"root.iot.a","t":1,"m":["value_float"],"ty":["DOUBLE"],"v":["x"]}]`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, _, _, err := decodePayload(payload); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestRowBuilderEmpty(t *testing.T) {
	rb := newRowBuilder("root.iot")
	if !rb.Empty() || rb.Rows() != 0 || rb.Payload() != "" {
		t.Fatalf("new builder should be empty, got empty=%v rows=%d payload=%q", rb.Empty(), rb.Rows(), rb.Payload())
	}
	rb.Append(tsdb.Batch{Records: nil})
	if !rb.Empty() {
		t.Fatal("empty batch should not add rows")
	}
}

// BenchmarkPayloadRoundTrip 度量一次「攒批构建 + 解码」的成本(2000 行),
// 该路径每个刷盘窗口执行一次,直接决定写入侧 CPU 开销。
func BenchmarkPayloadRoundTrip(b *testing.B) {
	kinds := []string{driver.KindFloat, driver.KindInt, driver.KindBool, driver.KindString}
	records := make([]model.Record, 2000)
	for i := range records {
		records[i] = model.Record{
			DeviceID:        "dev-" + strconv.Itoa(i%50),
			DeviceAddressID: "addr-" + strconv.Itoa(i%40),
			Value:           "123.45",
			Kind:            kinds[i%len(kinds)],
			Quality:         0,
		}
	}
	batch := tsdb.Batch{CollectedAt: "2026-10-19 17:05:30.123", Records: records}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rb := newRowBuilder("root.iot")
		rb.Append(batch)
		payload := rb.Payload()
		if _, _, _, _, _, err := decodePayload(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCollectedAtMillis(t *testing.T) {
	ms := time.Date(2026, 10, 19, 17, 5, 30, 123_000_000, time.Local).UnixMilli()
	if got := collectedAtMillis("2026-10-19 17:05:30.123"); got != ms {
		t.Fatalf("millis = %d, want %d", got, ms)
	}
	if got := collectedAtMillis("2026-10-19 17:05:30"); got != ms-123 {
		t.Fatalf("second precision millis = %d, want %d", got, ms-123)
	}
	if got := collectedAtMillis("not-a-time"); got <= 0 {
		t.Fatalf("fallback millis = %d, want current time", got)
	}
}
