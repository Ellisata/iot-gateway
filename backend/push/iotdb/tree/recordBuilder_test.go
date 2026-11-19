// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tree

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/apache/iotdb-client-go/client"
	"iot-gateway/collector"
	"iot-gateway/push"
)

func testRecord(value, kind string) collector.CollectedRecord {
	return collector.CollectedRecord{
		DeviceID:        "dev-01",
		DeviceAddressID: "40001",
		Value:           value,
		Kind:            kind,
		Protocol:        "ModBus.TCP",
		Quality:         192,
		ThingModelCode:  "th_sensor",
		PropertyCode:    "temperature",
	}
}

func TestSeriesPathIsSafeAndStable(t *testing.T) {
	a := seriesPath("root.iot", "dev-01", "40001/../../x")
	b := seriesPath("root.iot", "dev-01", "40001/../../x")
	if a != b || !strings.HasPrefix(a, "root.iot.device_dev_01_") || strings.Contains(a, "/") || strings.Contains(a, "..") {
		t.Fatalf("unsafe or unstable series path: %q / %q", a, b)
	}
	if a == seriesPath("root.iot", "dev_01", "40001/../../x") {
		t.Fatal("sanitization collision must be separated by the source-id hash")
	}
}

func TestInsertBuilderEncodesTimestampsAndDeduplicates(t *testing.T) {
	builder := newInsertBuilder("root.iot")
	collectedAt := "2026-11-19 10:11:12.345"
	builder.Append(push.PushBatch{
		DeviceID:    "dev-01",
		CollectedAt: collectedAt,
		Records: []collector.CollectedRecord{
			testRecord("19.5", driver.KindFloat),
			testRecord("99", driver.KindInt), // same point and timestamp: first sample wins
		},
	})
	builder.Append(push.PushBatch{
		DeviceID:    "dev-01",
		CollectedAt: "2026-11-19 10:11:13.345",
		Records:     []collector.CollectedRecord{testRecord("20.5", driver.KindFloat)},
	})
	if builder.Rows() != 2 {
		t.Fatalf("builder rows = %d, want 2", builder.Rows())
	}
	var rows []iotdbRow
	if err := json.Unmarshal([]byte(builder.Payload()), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Value != "19.5" || rows[0].Kind != driver.KindFloat {
		t.Fatalf("encoded rows = %+v", rows)
	}
	want := time.Date(2026, time.November, 19, 10, 11, 12, 345000000, time.Local).UnixMilli()
	if rows[0].Timestamp != want {
		t.Fatalf("timestamp = %d, want %d", rows[0].Timestamp, want)
	}
}

func TestRowMeasurementsPreserveValueFamiliesAndMetadata(t *testing.T) {
	cases := []struct {
		name      string
		value     string
		kind      string
		wantField string
		wantType  client.TSDataType
		wantKind  string
	}{
		{name: "boolean", value: "true", kind: driver.KindBool, wantField: "value_bool", wantType: client.BOOLEAN, wantKind: driver.KindBool},
		{name: "integer", value: "-12", kind: driver.KindInt, wantField: "value_int", wantType: client.INT64, wantKind: driver.KindInt},
		{name: "unsigned", value: "42", kind: driver.KindUInt, wantField: "value_int", wantType: client.INT64, wantKind: driver.KindUInt},
		{name: "float", value: "1.25", kind: driver.KindFloat, wantField: "value_float", wantType: client.DOUBLE, wantKind: driver.KindFloat},
		{name: "text", value: "a'b", kind: driver.KindString, wantField: "value_str", wantType: client.TEXT, wantKind: driver.KindString},
		{name: "bad number falls back to text", value: "bad", kind: driver.KindInt, wantField: "value_str", wantType: client.TEXT, wantKind: driver.KindString},
		{name: "infinity falls back to text", value: "Inf", kind: driver.KindFloat, wantField: "value_str", wantType: client.TEXT, wantKind: driver.KindString},
		{name: "infer integer", value: "7", kind: "", wantField: "value_int", wantType: client.INT64, wantKind: driver.KindInt},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := iotdbRow{Path: "root.iot.d.p", Timestamp: 1, Value: test.value, Kind: test.kind, Quality: 192, Protocol: "ModBus.TCP", ThingModelCode: "th", PropertyCode: "temp"}
			fields := rowMeasurements(row)
			if len(fields) != 6 || fields[0].name != test.wantField || fields[0].type_ != dataTypeName(test.wantType) || fields[0].value == nil {
				t.Fatalf("value field = %+v, all fields %+v", fields[0], fields)
			}
			if fields[1].value != test.wantKind || fields[2].type_ != "INT32" || fields[2].value != int32(192) || fields[3].value != "ModBus.TCP" || fields[4].value != "th" || fields[5].value != "temp" {
				t.Fatalf("metadata fields = %+v", fields[1:])
			}
		})
	}

	fields := rowMeasurements(iotdbRow{Kind: driver.KindString})
	if len(fields) != 5 {
		t.Fatalf("empty value should omit the value series, got %d fields", len(fields))
	}
}

func dataTypeName(dataType client.TSDataType) string {
	switch dataType {
	case client.BOOLEAN:
		return "BOOLEAN"
	case client.INT64:
		return "INT64"
	case client.DOUBLE:
		return "DOUBLE"
	case client.TEXT:
		return "TEXT"
	default:
		return "UNKNOWN"
	}
}

func TestSplitInsertRowsHasNoNilValues(t *testing.T) {
	rows := []iotdbRow{
		{Path: "root.iot.d.p", Timestamp: 1, Value: "true", Kind: driver.KindBool, Quality: 192},
		{Path: "root.iot.d.q", Timestamp: 2, Value: "", Kind: driver.KindString, Quality: 1},
	}
	devices, measurements, dataTypes, values, timestamps := splitInsertRows(rows)
	if len(devices) != 2 || len(measurements) != 2 || len(dataTypes) != 2 || len(values) != 2 || len(timestamps) != 2 {
		t.Fatalf("insert dimensions devices=%d measurements=%d types=%d values=%d times=%d", len(devices), len(measurements), len(dataTypes), len(values), len(timestamps))
	}
	for i := range values {
		if len(measurements[i]) != len(dataTypes[i]) || len(dataTypes[i]) != len(values[i]) {
			t.Fatalf("row %d has mismatched dimensions", i)
		}
		for _, value := range values[i] {
			if value == nil {
				t.Fatalf("session client does not allow nil values: row %d", i)
			}
		}
	}
	if got := values[0][0]; got != true {
		t.Fatalf("boolean = %v (%T)", got, got)
	}
}
