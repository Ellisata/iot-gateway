// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package iotdb

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/apache/iotdb-client-go/v2/client"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway/tsdb/model"
)

// 测量(measurement)列名。设备路径已承载 deviceID/deviceAddressID,
// 值按 Kind 落到值族对应测量列,保留原生类型。
const (
	measurementValueBool  = "value_bool"
	measurementValueInt   = "value_int"
	measurementValueFloat = "value_float"
	measurementValueStr   = "value_str"
	measurementQuality    = "quality"
	// 树模型没有 TDengine 的 tag,物模型/属性编码作为 TEXT 测量逐行写入,
	// 未绑定时不写该列(不产生空串序列)。
	measurementThingModelCode = "thing_model_code"
	measurementPropertyCode   = "property_code"
)

// payloadRow 是一次 insertRecords 请求中的一行(一个设备路径在一个时间戳上的若干测量值)。
//
// Values 直接保存原生 Go 值(bool/int32/int64/float64/string),整条负载只经一次
// json.Marshal。解码侧用 json.Decoder.UseNumber 把数字保留为 json.Number 再按
// Types 还原 —— 既不丢 int64 精度,也避免逐值 json.Unmarshal 的开销。
type payloadRow struct {
	Device       string   `json:"d"`
	Timestamp    int64    `json:"t"`
	Measurements []string `json:"m"`
	Types        []string `json:"ty"`
	Values       []any    `json:"v"`
}

// rowBuilder 将采集批次聚合为一条 insertRecords 请求的 JSON 负载。
//
// 与 TDengine 的 insertBuilder 不同,IoTDB 的 insertRecords 以
// (设备路径, 时间戳) 为行,同一设备可出现在多行,不存在语句级子表唯一约束,
// 因此 Append 永不受批间冲突限制;重复时间戳由服务端按后写覆盖。
type rowBuilder struct {
	db   string
	rows []payloadRow
}

func newRowBuilder(db string) *rowBuilder {
	return &rowBuilder{db: db}
}

// appendBatch 将一批采集记录追加为若干 payloadRow。
// 批内未携带 CollectedAt 的记录共用该批次的落地时间(与 tdengine/influxdb 一致)。
func (b *rowBuilder) appendBatch(collectedAt string, records []model.Record) {
	batchTS := collectedAtMillis(collectedAt)
	for _, r := range records {
		b.rows = append(b.rows, buildRow(b.db, r, batchTS))
	}
}

// empty 当前负载是否无行可写。
func (b *rowBuilder) empty() bool { return len(b.rows) == 0 }

// String 返回完整 JSON 数组;无行时返回空串,调用方应跳过写入。
func (b *rowBuilder) String() string {
	if len(b.rows) == 0 {
		return ""
	}
	buf, err := json.Marshal(b.rows)
	if err != nil {
		return "" // 值均为可编码类型,理论上不可达
	}
	return string(buf)
}

// buildRow 把一条采集记录映射为一行:
//   - value 按 Kind 落到 value_bool/value_int/value_float/value_str(空值不写值列);
//   - quality 恒写 INT32;
//   - thing_model_code / property_code 非空时作为 TEXT 测量写入。
func buildRow(db string, r model.Record, batchTS int64) payloadRow {
	ts := batchTS
	// 外部订阅记录可携带逐条采集时间(映射字段 collectedAt);
	// 本地点位留空,回落到批次落地时间。
	if r.CollectedAt != "" {
		ts = collectedAtMillis(r.CollectedAt)
	}

	row := payloadRow{
		Device:    db + "." + deviceNode(r.DeviceID, r.DeviceAddressID),
		Timestamp: ts,
	}
	if meas, typ, val, ok := valueParts(r.Value, r.Kind); ok {
		row.Measurements = append(row.Measurements, meas)
		row.Types = append(row.Types, typ)
		row.Values = append(row.Values, val)
	}
	row.Measurements = append(row.Measurements, measurementQuality)
	row.Types = append(row.Types, "INT32")
	row.Values = append(row.Values, int32(r.Quality))
	if r.ThingModelCode != "" {
		row.Measurements = append(row.Measurements, measurementThingModelCode)
		row.Types = append(row.Types, "TEXT")
		row.Values = append(row.Values, r.ThingModelCode)
	}
	if r.PropertyCode != "" {
		row.Measurements = append(row.Measurements, measurementPropertyCode)
		row.Types = append(row.Types, "TEXT")
		row.Values = append(row.Values, r.PropertyCode)
	}
	return row
}

// valueParts 按 PLC 数据类型类别 Kind 返回测量名、IoTDB 类型与原生值。
// 空值返回 ok=false(不写值列,只保留 quality);数值解析失败与 NaN/Inf 回落
// value_str(TEXT),与 tdengine 的降级链保持一致,不阻断整批写入。
func valueParts(v, kind string) (measurement, typ string, val any, ok bool) {
	if v == "" {
		return "", "", nil, false
	}

	switch kind {
	case driver.KindBool:
		if b, err := strconv.ParseBool(v); err == nil {
			return measurementValueBool, "BOOLEAN", b, true
		}

	case driver.KindUInt:
		if u, err := strconv.ParseUint(v, 10, 64); err == nil {
			if u <= math.MaxInt64 {
				return measurementValueInt, "INT64", int64(u), true
			}
			// 超出 INT64 的无符号值用文本保留精确值,避免浮点精度损失。
			return measurementValueStr, "TEXT", v, true
		}

	case driver.KindInt:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return measurementValueInt, "INT64", i, true
		}

	case driver.KindFloat:
		if f, err := strconv.ParseFloat(v, 64); err == nil && isFinite(f) {
			return measurementValueFloat, "DOUBLE", f, true
		}

	case driver.KindString, driver.KindTime:
		return measurementValueStr, "TEXT", v, true

	default:
		// 空/未知 Kind:自动数值推断(int → float → text 降级)
		if !strings.Contains(v, ".") {
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return measurementValueInt, "INT64", i, true
			}
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil && isFinite(f) {
			return measurementValueFloat, "DOUBLE", f, true
		}
	}

	// 数值类别解析失败(浮点写法/NaN/Inf):再试浮点,仍失败回落文本
	if kind != driver.KindString && kind != driver.KindTime {
		if f, err := strconv.ParseFloat(v, 64); err == nil && isFinite(f) {
			return measurementValueFloat, "DOUBLE", f, true
		}
	}
	return measurementValueStr, "TEXT", v, true
}

// isFinite 报告 float 是否为可 JSON 编码的有限值(NaN/±Inf 不能编码为 JSON 数字)。
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// decodePayload 把 rowBuilder 产出的 JSON 还原为 client.Session.InsertRecords 的入参。
//
// 用 json.Decoder.UseNumber 一次性解码:数字保留为 json.Number 文本,再按 Types
// 用 strconv 还原,既保证 int64 全精度,又避免逐值 json.Unmarshal 的反射开销。
// 调用前校验每个行的测量/类型/值数组等长且非空:原生客户端的 valuesToBytes
// 按 dataTypes 下标取值,长度不一致会越界 panic,必须在这里拦住。
func decodePayload(payload string) (deviceIDs []string, measurements [][]string, dataTypes [][]client.TSDataType, values [][]any, timestamps []int64, err error) {
	var rows []payloadRow
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&rows); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("iotdb: decode payload failed: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil, nil, nil, nil, nil
	}

	deviceIDs = make([]string, len(rows))
	measurements = make([][]string, len(rows))
	dataTypes = make([][]client.TSDataType, len(rows))
	values = make([][]any, len(rows))
	timestamps = make([]int64, len(rows))

	for i, row := range rows {
		if row.Device == "" {
			return nil, nil, nil, nil, nil, fmt.Errorf("iotdb: payload row %d has empty device", i)
		}
		if len(row.Measurements) == 0 || len(row.Measurements) != len(row.Types) || len(row.Types) != len(row.Values) {
			return nil, nil, nil, nil, nil, fmt.Errorf(
				"iotdb: payload row %d has inconsistent measurements/types/values (%d/%d/%d)",
				i, len(row.Measurements), len(row.Types), len(row.Values))
		}

		ms := make([]string, len(row.Measurements))
		copy(ms, row.Measurements)
		dts := make([]client.TSDataType, len(row.Types))
		vals := make([]any, len(row.Values))
		for j := range row.Types {
			dt, derr := client.GetDataTypeByStr(strings.ToUpper(row.Types[j]))
			if derr != nil {
				return nil, nil, nil, nil, nil, fmt.Errorf("iotdb: payload row %d type %q invalid: %w", i, row.Types[j], derr)
			}
			v, verr := decodeValue(dt, row.Values[j])
			if verr != nil {
				return nil, nil, nil, nil, nil, fmt.Errorf("iotdb: payload row %d value %d invalid: %w", i, j, verr)
			}
			dts[j] = dt
			vals[j] = v
		}
		deviceIDs[i] = row.Device
		measurements[i] = ms
		dataTypes[i] = dts
		values[i] = vals
		timestamps[i] = row.Timestamp
	}
	return deviceIDs, measurements, dataTypes, values, timestamps, nil
}

// decodeValue 按声明类型还原一个 JSON 值。
//
// 数字来自 json.Decoder.UseNumber(类型为 json.Number,即原始数字文本),
// 用 strconv 解析保证 int64 全精度;类型不匹配(如 DOUBLE 收到字符串)直接报错,
// 不静默丢数据。
func decodeValue(dt client.TSDataType, v any) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("null value")
	}
	switch dt {
	case client.BOOLEAN:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("want boolean, got %T", v)
		}
		return b, nil
	case client.INT32:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("want number, got %T", v)
		}
		i, err := strconv.ParseInt(n.String(), 10, 32)
		if err != nil {
			return nil, err
		}
		return int32(i), nil
	case client.INT64, client.TIMESTAMP:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("want number, got %T", v)
		}
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			return nil, err
		}
		return i, nil
	case client.DOUBLE:
		n, ok := v.(json.Number)
		if !ok {
			return nil, fmt.Errorf("want number, got %T", v)
		}
		f, err := strconv.ParseFloat(n.String(), 64)
		if err != nil {
			return nil, err
		}
		if !isFinite(f) {
			return nil, fmt.Errorf("non-finite double")
		}
		return f, nil
	case client.TEXT, client.STRING:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want string, got %T", v)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("unsupported type %d", dt)
	}
}

// deviceNode 生成设备路径的最后一个节点:
// sanitize(deviceID)_sanitize(addressID)_fnv64。
//
// 哈希后缀必须恒存在:消毒会折叠不同原始串(如 "dev-1" 与 "dev_1"),
// 仅靠消毒串会让两个不同设备落到同一设备路径,时间戳重叠时互相覆盖。
// 截断 base 到 160 + 16 位 hex,控制路径与请求帧大小。
func deviceNode(deviceID, addressID string) string {
	base := sanitizeIdent(deviceID) + "_" + sanitizeIdent(addressID)
	if len(base) > 160 {
		base = base[:160]
	}
	h := fnv.New64a()
	h.Write([]byte(deviceID))
	h.Write([]byte("|"))
	h.Write([]byte(addressID))
	return base + "_" + strconv.FormatUint(h.Sum64(), 16)
}

// collectedAtMillis 将采集时间(毫秒精度 "2006-01-02 15:04:05.000",兼容旧秒级串)按本地
// 时区转换为 epoch 毫秒。解析失败时回落当前时间,避免整批写入失败。
func collectedAtMillis(s string) int64 {
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.000", s, time.Local); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t.UnixMilli()
	}
	return time.Now().UnixMilli()
}
