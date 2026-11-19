// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package tree

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/apache/iotdb-client-go/client"
	"iot-gateway/collector"
	"iot-gateway/push"
)

const maxNodePrefix = 120

type iotdbRow struct {
	Path           string `json:"path"`
	Timestamp      int64  `json:"timestamp"`
	Value          string `json:"value"`
	Kind           string `json:"kind"`
	Quality        int    `json:"quality"`
	Protocol       string `json:"protocol"`
	ThingModelCode string `json:"thingModelCode"`
	PropertyCode   string `json:"propertyCode"`
}

type insertBuilder struct {
	database string
	rows     []iotdbRow
	seen     map[string]struct{}
}

func newInsertBuilder(database string) *insertBuilder {
	return &insertBuilder{database: database, seen: make(map[string]struct{})}
}

func (b *insertBuilder) Append(batch push.PushBatch) bool {
	b.appendBatch(batch)
	return true
}

func (b *insertBuilder) appendBatch(batch push.PushBatch) {
	batchTime := collectedAtMillis(batch.CollectedAt, time.Now())
	for _, record := range batch.Records {
		deviceID := record.DeviceID
		if deviceID == "" {
			deviceID = batch.DeviceID
		}
		addressID := record.DeviceAddressID
		path := seriesPath(b.database, deviceID, addressID)
		ts := batchTime
		if record.CollectedAt != "" {
			ts = collectedAtMillis(record.CollectedAt, time.UnixMilli(batchTime))
		}
		key := fmt.Sprintf("%s/%d", path, ts)
		if _, duplicate := b.seen[key]; duplicate {
			continue
		}
		b.seen[key] = struct{}{}
		b.rows = append(b.rows, iotdbRow{
			Path:           path,
			Timestamp:      ts,
			Value:          record.Value,
			Kind:           record.Kind,
			Quality:        record.Quality,
			Protocol:       record.Protocol,
			ThingModelCode: record.ThingModelCode,
			PropertyCode:   record.PropertyCode,
		})
	}
}

func (b *insertBuilder) Rows() int { return len(b.rows) }

func (b *insertBuilder) Empty() bool { return len(b.rows) == 0 }

func (b *insertBuilder) Payload() string { return b.String() }

func (b *insertBuilder) String() string {
	if len(b.rows) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(b.rows)
	return string(encoded)
}

func buildRows(database, collectedAt string, records []collector.CollectedRecord) []iotdbRow {
	b := newInsertBuilder(database)
	b.appendBatch(push.PushBatch{CollectedAt: collectedAt, Records: records})
	return b.rows
}

func seriesPath(database, deviceID, addressID string) string {
	return database + ".device_" + nodeWithHash(deviceID) + ".point_" + nodeWithHash(addressID)
}

func nodeWithHash(value string) string {
	prefix := sanitizeNode(value)
	if len(prefix) > maxNodePrefix {
		prefix = prefix[:maxNodePrefix]
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return prefix + "_" + strconv.FormatUint(hash.Sum64(), 16)
}

func collectedAtMillis(value string, fallback time.Time) int64 {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
	} {
		var parsed time.Time
		var err error
		if layout == time.RFC3339Nano {
			parsed, err = time.Parse(layout, value)
		} else {
			parsed, err = time.ParseInLocation(layout, value, time.Local)
		}
		if err == nil {
			return parsed.UnixMilli()
		}
	}
	return fallback.UnixMilli()
}

type measurementValue struct {
	name  string
	type_ string
	value any
}

func rowMeasurements(row iotdbRow) []measurementValue {
	out := make([]measurementValue, 0, 6)
	if value, kind, ok := typedValue(row.Value, row.Kind); ok {
		out = append(out, value)
		row.Kind = kind
	} else if row.Kind == "" {
		row.Kind = driver.KindString
	}
	out = append(out,
		measurementValue{name: "value_kind", type_: "TEXT", value: normalizeKind(row.Kind)},
		measurementValue{name: "quality", type_: "INT32", value: int32(row.Quality)},
		measurementValue{name: "protocol", type_: "TEXT", value: row.Protocol},
		measurementValue{name: "thing_model_code", type_: "TEXT", value: row.ThingModelCode},
		measurementValue{name: "property_code", type_: "TEXT", value: row.PropertyCode},
	)
	return out
}

func typedValue(value, kind string) (measurementValue, string, bool) {
	if value == "" {
		return measurementValue{}, normalizeKind(kind), false
	}
	switch kind {
	case driver.KindBool:
		if parsed, err := strconv.ParseBool(value); err == nil {
			return measurementValue{name: "value_bool", type_: "BOOLEAN", value: parsed}, driver.KindBool, true
		}
	case driver.KindUInt:
		if parsed, err := strconv.ParseUint(value, 10, 64); err == nil && parsed <= math.MaxInt64 {
			return measurementValue{name: "value_int", type_: "INT64", value: int64(parsed)}, driver.KindUInt, true
		}
	case driver.KindInt:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return measurementValue{name: "value_int", type_: "INT64", value: parsed}, driver.KindInt, true
		}
	case driver.KindFloat:
		if parsed, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			return measurementValue{name: "value_float", type_: "DOUBLE", value: parsed}, driver.KindFloat, true
		}
	case driver.KindString, driver.KindTime:
		return measurementValue{name: "value_str", type_: "TEXT", value: value}, kind, true
	default:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return measurementValue{name: "value_int", type_: "INT64", value: parsed}, driver.KindInt, true
		}
		if parsed, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
			return measurementValue{name: "value_float", type_: "DOUBLE", value: parsed}, driver.KindFloat, true
		}
		return measurementValue{name: "value_str", type_: "TEXT", value: value}, driver.KindString, true
	}
	// Malformed values fall back to text so one dirty PLC value cannot poison a batch.
	return measurementValue{name: "value_str", type_: "TEXT", value: value}, driver.KindString, true
}

func normalizeKind(kind string) string {
	if kind == "" {
		return driver.KindString
	}
	return kind
}

func splitInsertRows(rows []iotdbRow) ([]string, [][]string, [][]client.TSDataType, [][]any, []int64) {
	devices := make([]string, 0, len(rows))
	measurements := make([][]string, 0, len(rows))
	types := make([][]client.TSDataType, 0, len(rows))
	values := make([][]any, 0, len(rows))
	timestamps := make([]int64, 0, len(rows))
	for _, row := range rows {
		fields := rowMeasurements(row)
		rowMeasurements := make([]string, len(fields))
		rowTypes := make([]client.TSDataType, len(fields))
		rowValues := make([]any, len(fields))
		for i, field := range fields {
			rowMeasurements[i] = field.name
			dataType, _ := client.GetDataTypeByStr(field.type_)
			rowTypes[i] = dataType
			rowValues[i] = field.value
		}
		devices = append(devices, row.Path)
		measurements = append(measurements, rowMeasurements)
		types = append(types, rowTypes)
		values = append(values, rowValues)
		timestamps = append(timestamps, row.Timestamp)
	}
	return devices, measurements, types, values, timestamps
}
