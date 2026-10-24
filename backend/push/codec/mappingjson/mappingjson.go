// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package mappingjson 实现出站自定义 JSON 映射。
//
// 配置示例：
//
//	{
//	  "version": 1,
//	  "emit": "batch",
//	  "envelope": "data",
//	  "batchFields": {"site": {"source": "const", "value": "plant-a"}},
//	  "fields": {
//	    "device.id":  {"source": "path", "value": "deviceId"},
//	    "value":      {"source": "path", "value": "value", "type": "number"},
//	    "ts":         {"source": "path", "value": "collectedAt"}
//	  }
//	}
//
// emit=batch 输出一条消息（数组或 {envelope: [...]}）；emit=record 每条记录
// 输出一条消息。输入文档的键固定为 CollectedRecord 的逻辑字段名。
package mappingjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"iot-gateway/collector"
	"iot-gateway/mappingdsl"
	"iot-gateway/push"
)

// emit 取值。
const (
	EmitBatch  = "batch"
	EmitRecord = "record"
)

// 输出值类型。
const (
	TypeAuto    = "auto"
	TypeString  = "string"
	TypeNumber  = "number"
	TypeBoolean = "boolean"
)

// Field 一个输出字段：取值方式 + 可选的输出类型。
type Field struct {
	mappingdsl.ValueSource
	Type string `json:"type,omitempty"`
}

// Mapping 出站映射配置。
type Mapping struct {
	Version     int              `json:"version"`
	Emit        string           `json:"emit,omitempty"`
	Envelope    string           `json:"envelope,omitempty"`
	BatchFields map[string]Field `json:"batchFields,omitempty"`
	Fields      map[string]Field `json:"fields"`
}

type compiledField struct {
	key   string
	parts []string
	src   mappingdsl.ValueSource
	typ   string
}

// Encoder 出站映射编码器。
type Encoder struct {
	fields      []compiledField
	batchFields []compiledField
	emitRecord  bool
	envelope    string
}

// New 解析并校验一份出站映射配置。
func New(raw json.RawMessage) (*Encoder, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("mapping-json 需要配置 mapping")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Mapping
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("映射配置 JSON 解析失败: %w", err)
	}
	return compile(&m)
}

// Validate 只校验配置，不保留编码器。
func Validate(raw json.RawMessage) error {
	_, err := New(raw)
	return err
}

func compile(m *Mapping) (*Encoder, error) {
	if m.Version != 0 && m.Version != 1 {
		return nil, fmt.Errorf("不支持的映射版本 %d（仅支持 0 或 1）", m.Version)
	}
	if len(m.Fields) == 0 {
		return nil, errors.New("至少需要配置一个字段映射")
	}

	emit := strings.ToLower(strings.TrimSpace(m.Emit))
	if emit == "" {
		emit = EmitBatch
	}
	if emit != EmitBatch && emit != EmitRecord {
		return nil, fmt.Errorf("emit 只能是 %q 或 %q", EmitBatch, EmitRecord)
	}

	envelope := strings.TrimSpace(m.Envelope)
	if emit == EmitRecord {
		if envelope != "" {
			return nil, errors.New("emit=record 时不能配置 envelope")
		}
		if len(m.BatchFields) > 0 {
			return nil, errors.New("emit=record 时不能配置 batchFields")
		}
	}
	if envelope != "" && strings.ContainsAny(envelope, ". \t\r\n") {
		return nil, fmt.Errorf("envelope %q 不能包含点号或空白", envelope)
	}

	fields, err := compileFieldSet(m.Fields, "fields")
	if err != nil {
		return nil, err
	}
	batchFields, err := compileFieldSet(m.BatchFields, "batchFields")
	if err != nil {
		return nil, err
	}
	if err := validateFieldConflicts(fields); err != nil {
		return nil, err
	}
	if err := validateFieldConflicts(batchFields); err != nil {
		return nil, err
	}
	// 无 envelope 但有 batchFields 时，记录数组落在保留键 records 下；
	// 先把有效 envelope 算出来，才能挡住 batchFields 里同名的键。
	if emit == EmitBatch && envelope == "" && len(batchFields) > 0 {
		envelope = "records"
	}
	if envelope != "" {
		for _, f := range batchFields {
			if f.parts[0] == envelope {
				return nil, fmt.Errorf("envelope %q 与 batchFields 的 %q 冲突", envelope, f.key)
			}
		}
	}

	return &Encoder{
		fields:      fields,
		batchFields: batchFields,
		emitRecord:  emit == EmitRecord,
		envelope:    envelope,
	}, nil
}

func compileFieldSet(in map[string]Field, section string) ([]compiledField, error) {
	out := make([]compiledField, 0, len(in))
	for rawKey, f := range in {
		key := strings.TrimSpace(rawKey)
		parts, err := splitOutputKey(key)
		if err != nil {
			return nil, fmt.Errorf("%s 的键 %q: %w", section, rawKey, err)
		}
		if err := f.ValueSource.Validate(); err != nil {
			return nil, fmt.Errorf("%s 的字段 %q: %w", section, rawKey, err)
		}
		if f.ValueSource.IsTopic() {
			return nil, fmt.Errorf("%s 的字段 %q: 出站映射不支持 source=topic", section, rawKey)
		}
		if !f.ValueSource.IsConst() && strings.TrimSpace(f.ValueSource.Value) == "" {
			return nil, fmt.Errorf("%s 的字段 %q: source=%s 需要填写取值路径", section, rawKey, sourceName(f.ValueSource.Source))
		}
		typ, err := normalizeType(f.Type)
		if err != nil {
			return nil, fmt.Errorf("%s 的字段 %q: %w", section, rawKey, err)
		}
		out = append(out, compiledField{key: key, parts: parts, src: f.ValueSource, typ: typ})
	}
	return out, nil
}

func sourceName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return mappingdsl.SourcePath
	}
	return s
}

func normalizeType(t string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", TypeAuto:
		return TypeAuto, nil
	case TypeString:
		return TypeString, nil
	case TypeNumber:
		return TypeNumber, nil
	case TypeBoolean:
		return TypeBoolean, nil
	default:
		return "", fmt.Errorf("未知的输出类型 %q（可选 auto/string/number/boolean）", t)
	}
}

func splitOutputKey(key string) ([]string, error) {
	if key == "" {
		return nil, errors.New("输出键不能为空")
	}
	if strings.HasPrefix(key, "$") {
		return nil, errors.New("输出键不要使用 $ 前缀")
	}
	parts := strings.Split(key, ".")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, errors.New("输出键不能有空段")
		}
	}
	return parts, nil
}

// validateFieldConflicts 拒绝「a」与「a.b」同时出现：前者是标量，后者要往下建对象。
func validateFieldConflicts(fields []compiledField) error {
	for i := range fields {
		for j := range fields {
			if i == j {
				continue
			}
			if strings.HasPrefix(fields[j].key, fields[i].key+".") {
				return fmt.Errorf("输出键 %q 与 %q 冲突（一个是另一个的前缀）", fields[i].key, fields[j].key)
			}
		}
	}
	return nil
}

// Encode 把一批记录编码为一条或多条消息。
func (e *Encoder) Encode(batch push.PushBatch) ([]push.EncodedMessage, error) {
	if len(batch.Records) == 0 {
		return nil, nil
	}

	records := make([]map[string]any, 0, len(batch.Records))
	for _, r := range batch.Records {
		doc := recordDoc(r, batch.CollectedAt)
		obj, err := applyFields(e.fields, doc, "记录 "+r.DeviceID+"/"+r.DeviceAddressID)
		if err != nil {
			return nil, err
		}
		records = append(records, obj)
	}

	if e.emitRecord {
		msgs := make([]push.EncodedMessage, 0, len(records))
		for _, obj := range records {
			payload, err := json.Marshal(obj)
			if err != nil {
				return nil, fmt.Errorf("序列化记录失败: %w", err)
			}
			msgs = append(msgs, push.EncodedMessage{Payload: payload})
		}
		return msgs, nil
	}

	var out any = records
	if len(e.batchFields) > 0 || e.envelope != "" {
		root := make(map[string]any, len(e.batchFields)+1)
		if len(e.batchFields) > 0 {
			batchDoc := batchDocument(batch)
			for _, f := range e.batchFields {
				v, err := resolveField(f, batchDoc, "batchFields")
				if err != nil {
					return nil, err
				}
				if err := setPath(root, f.parts, v); err != nil {
					return nil, fmt.Errorf("batchFields 的字段 %q: %w", f.key, err)
				}
			}
		}
		env := e.envelope
		if env == "" {
			env = "records"
		}
		root[env] = records
		out = root
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("序列化映射载荷失败: %w", err)
	}
	return []push.EncodedMessage{{Payload: payload}}, nil
}

func recordDoc(r collector.CollectedRecord, batchAt string) map[string]any {
	at := r.CollectedAt
	if at == "" {
		at = batchAt
	}
	return map[string]any{
		"deviceId":           r.DeviceID,
		"deviceName":         r.DeviceName,
		"deviceAddressId":    r.DeviceAddressID,
		"deviceAddressName":  r.DeviceAddressName,
		"deviceAddressLabel": r.DeviceAddressLabel,
		"value":              r.Value,
		"dataType":           r.DataType,
		"kind":               r.Kind,
		"protocol":           r.Protocol,
		"quality":            r.Quality,
		"collectedAt":        at,
	}
}

func batchDocument(batch push.PushBatch) map[string]any {
	at := batch.CollectedAt
	if len(batch.Records) > 0 && at == "" {
		at = batch.Records[0].CollectedAt
	}
	return map[string]any{
		"deviceId":    batch.DeviceID,
		"collectedAt": at,
		"count":       len(batch.Records),
	}
}

func applyFields(fields []compiledField, doc map[string]any, where string) (map[string]any, error) {
	obj := make(map[string]any, len(fields))
	for _, f := range fields {
		v, err := resolveField(f, doc, where)
		if err != nil {
			return nil, err
		}
		if err := setPath(obj, f.parts, v); err != nil {
			return nil, fmt.Errorf("%s 的字段 %q: %w", where, f.key, err)
		}
	}
	return obj, nil
}

func resolveField(f compiledField, doc map[string]any, where string) (any, error) {
	raw, err := f.src.Resolve(doc, nil)
	if err != nil {
		return nil, fmt.Errorf("%s 的字段 %q 取值失败: %w", where, f.key, err)
	}
	v, err := coerce(raw, f.typ, f.src, doc)
	if err != nil {
		return nil, fmt.Errorf("%s 的字段 %q: %w", where, f.key, err)
	}
	return v, nil
}

func setPath(root map[string]any, parts []string, v any) error {
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			if _, exists := cur[p]; exists {
				return fmt.Errorf("输出键 %q 重复", strings.Join(parts, "."))
			}
			cur[p] = v
			return nil
		}
		next, exists := cur[p]
		if !exists {
			m := make(map[string]any)
			cur[p] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("输出键 %q 与已写入的标量冲突", strings.Join(parts[:i+1], "."))
		}
		cur = m
	}
	return nil
}

func coerce(v any, typ string, src mappingdsl.ValueSource, doc map[string]any) (any, error) {
	if typ == TypeAuto {
		return coerceAuto(v, src, doc)
	}
	switch typ {
	case TypeString:
		return mappingdsl.ToString(v), nil
	case TypeNumber:
		return toJSONNumber(v)
	case TypeBoolean:
		return toBool(v)
	default:
		return nil, fmt.Errorf("未知的输出类型 %q", typ)
	}
}

// coerceAuto 对最常见的 value 字段按记录的 kind 输出原生 JSON 类型；
// 其他字段保持解析出来的值（路径取值通常是字符串）。
func coerceAuto(v any, src mappingdsl.ValueSource, doc map[string]any) (any, error) {
	path := strings.TrimSpace(src.Value)
	if src.IsConst() || src.IsTemplate() {
		return v, nil
	}
	switch path {
	case "value":
		kind, _ := doc["kind"].(string)
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "bool":
			return toBool(v)
		case "int", "uint", "float":
			return toJSONNumber(v)
		case "string", "time":
			return mappingdsl.ToString(v), nil
		default:
			return v, nil
		}
	case "quality":
		return toJSONNumber(v)
	default:
		return v, nil
	}
}

var jsonNumberRE = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func toJSONNumber(v any) (json.Number, error) {
	s := strings.TrimSpace(mappingdsl.ToString(v))
	if s == "" {
		return "", errors.New("空值不能转换为数字")
	}
	if !jsonNumberRE.MatchString(s) {
		return "", fmt.Errorf("%q 不是合法数字", s)
	}
	return json.Number(s), nil
}

func toBool(v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
	case json.Number:
		return numberToBool(string(t))
	case float64:
		return numberToBool(strconv.FormatFloat(t, 'f', -1, 64))
	case float32:
		return numberToBool(strconv.FormatFloat(float64(t), 'f', -1, 32))
	case int:
		return numberToBool(strconv.Itoa(t))
	case int64:
		return numberToBool(strconv.FormatInt(t, 10))
	case uint64:
		return numberToBool(strconv.FormatUint(t, 10))
	}
	return false, fmt.Errorf("%q 不是布尔值（可用 true/false/1/0）", mappingdsl.ToString(v))
}

func numberToBool(s string) (bool, error) {
	switch strings.TrimSpace(s) {
	case "1":
		return true, nil
	case "0":
		return false, nil
	default:
		return false, fmt.Errorf("%q 不是布尔值（可用 true/false/1/0）", s)
	}
}
