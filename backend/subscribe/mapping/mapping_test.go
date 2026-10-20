// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mapping

import (
	"strings"
	"testing"

	"iot-gateway/driver"
)

func mustParse(t *testing.T, raw string) *Rule {
	t.Helper()
	r, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return r
}

func TestConvertSingleObject(t *testing.T) {
	rule := mustParse(t, `{
		"version": 1,
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceName":      {"source": "template", "value": "gw-{deviceId}"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"deviceAddressName": {"source": "path", "value": "tagName"},
			"value":           {"source": "path", "value": "value"},
			"dataType":        {"source": "path", "value": "dataType"},
			"kind":            {"source": "path", "value": "kind"},
			"quality":         {"source": "const", "value": "192"}
		}
	}`)

	res, err := rule.Convert([]byte(`{"deviceId":"d1","tagId":"a1","tagName":"温度","value":12.5,"dataType":"Float","kind":"float"}`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(res.Records))
	}
	rec := res.Records[0]
	if rec.DeviceID != "d1" || rec.DeviceAddressID != "a1" || rec.DeviceAddressName != "温度" {
		t.Errorf("unexpected record: %+v", rec)
	}
	if rec.DeviceName != "gw-d1" {
		t.Errorf("deviceName = %q, want gw-d1", rec.DeviceName)
	}
	if rec.Value != "12.5" || rec.DataType != "Float" || rec.Kind != "float" {
		t.Errorf("value/dataType/kind = %q/%q/%q", rec.Value, rec.DataType, rec.Kind)
	}
	if rec.Quality != driver.QualityGood {
		t.Errorf("quality = %d, want %d", rec.Quality, driver.QualityGood)
	}
}

func TestConvertArrayRootAndFilter(t *testing.T) {
	rule := mustParse(t, `{
		"root": "$.data[*]",
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"},
			"quality":         {"source": "path", "value": "quality"}
		},
		"filter": [{"path": "quality", "op": "eq", "value": "192"}]
	}`)

	res, err := rule.Convert([]byte(`{"data":[
		{"deviceId":"d1","tagId":"a1","value":1,"quality":192},
		{"deviceId":"d1","tagId":"a2","value":2,"quality":0},
		{"deviceId":"d1","tagId":"a3","value":"3.0","quality":"192"}
	]}`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2 (filter must drop quality=0)", len(res.Records))
	}
	if res.Records[0].DeviceAddressID != "a1" || res.Records[1].DeviceAddressID != "a3" {
		t.Errorf("unexpected addresses: %s, %s", res.Records[0].DeviceAddressID, res.Records[1].DeviceAddressID)
	}
	if res.Records[1].Quality != driver.QualityGood {
		t.Errorf("string quality parsed = %d, want %d", res.Records[1].Quality, driver.QualityGood)
	}
}

// TestConvertTopLevelArrayWithDefaultRoot 默认 root（$）下，整篇报文是数组时
// 自动逐元素拆条，不需要用户再补写 $[*]。
func TestConvertTopLevelArrayWithDefaultRoot(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	res, err := rule.Convert([]byte(`[
		{"deviceId":"d1","tagId":"a1","value":1},
		{"deviceId":"d1","tagId":"a2","value":2}
	]`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.Records))
	}
	if res.Records[0].DeviceAddressID != "a1" || res.Records[1].DeviceAddressID != "a2" {
		t.Errorf("unexpected records: %+v", res.Records)
	}
}

func TestConvertSkipsBadRecords(t *testing.T) {
	rule := mustParse(t, `{
		"root": "$[*]",
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	// 第二条缺 value、第三条 value 为空串，均只跳过自身。
	res, err := rule.Convert([]byte(`[
		{"deviceId":"d1","tagId":"a1","value":1},
		{"deviceId":"d1","tagId":"a2"},
		{"deviceId":"d1","tagId":"a3","value":""}
	]`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 1 || res.Skipped != 2 {
		t.Fatalf("records=%d skipped=%d, want 1/2", len(res.Records), res.Skipped)
	}
	if res.FirstError == nil {
		t.Error("FirstError should describe the first skipped record")
	}
}

func TestConvertAllFailedReturnsError(t *testing.T) {
	rule := mustParse(t, `{
		"root": "$[*]",
		"fields": {
			"deviceId":        {"source": "path", "value": "missing"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)
	if _, err := rule.Convert([]byte(`[{"tagId":"a1","value":1}]`)); err == nil {
		t.Fatal("expected error when every record fails")
	}
}

func TestConvertKeepsLargeIntegerPrecision(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)
	res, err := rule.Convert([]byte(`{"deviceId":"d1","tagId":"a1","value":510637000000000001}`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if got := res.Records[0].Value; got != "510637000000000001" {
		t.Errorf("value = %q, want exact literal 510637000000000001", got)
	}
	if got := res.Records[0].Kind; got != driver.KindInt {
		t.Errorf("kind = %q, want %q", got, driver.KindInt)
	}
}

func TestKindInference(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "const", "value": "d1"},
			"deviceAddressId": {"source": "const", "value": "a1"},
			"value":           {"source": "path", "value": "v"}
		}
	}`)

	cases := []struct {
		payload string
		want    string
	}{
		{`{"v":true}`, driver.KindBool},
		{`{"v":12}`, driver.KindInt},
		{`{"v":12.5}`, driver.KindFloat},
		{`{"v":"abc"}`, ""}, // 字符串留给时序通道自动推断
	}
	for _, tc := range cases {
		res, err := rule.Convert([]byte(tc.payload))
		if err != nil {
			t.Fatalf("Convert(%s) error = %v", tc.payload, err)
		}
		if got := res.Records[0].Kind; got != tc.want {
			t.Errorf("Convert(%s) kind = %q, want %q", tc.payload, got, tc.want)
		}
	}
}

func TestParseStrictRequiresPrimaryFields(t *testing.T) {
	raw := []byte(`{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	// 加载路径保持宽松：历史配置不能因为新增必填项被整体判死。
	if _, err := Parse(raw); err != nil {
		t.Fatalf("Parse() should accept legacy config, got %v", err)
	}
	// 保存路径要求全部主字段。
	if _, err := ParseStrict(raw); err == nil || !strings.Contains(err.Error(), "deviceName") {
		t.Fatalf("ParseStrict() error = %v, want it to mention deviceName", err)
	}
}

func TestQualityValueMapping(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"},
			"quality": {
				"source": "map",
				"value": "q",
				"mapping": {"OK": "192", "BAD": "0", "UNCERTAIN": "128"},
				"default": "192"
			}
		}
	}`)

	cases := map[string]int{
		"OK":        driver.QualityGood,
		"bad":       driver.QualityBad,
		"uncertain": driver.QualityUncertain,
		"UNKNOWN":   driver.QualityGood, // 未命中回落默认值
	}
	for raw, want := range cases {
		payload := `{"deviceId":"d","tagId":"a","value":1,"q":"` + raw + `"}`
		res, err := rule.Convert([]byte(payload))
		if err != nil {
			t.Fatalf("%s: Convert() error = %v", raw, err)
		}
		if got := res.Records[0].Quality; got != want {
			t.Errorf("%s: quality = %d, want %d", raw, got, want)
		}
	}
}

func TestQualityMappingValidation(t *testing.T) {
	base := `"deviceId":{"value":"d"},"deviceAddressId":{"value":"a"},"value":{"value":"v"}`

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "missing default",
			raw:  `{"fields":{` + base + `,"quality":{"source":"map","value":"q","mapping":{"OK":"192"}}}}`,
			want: "未命中默认值",
		},
		{
			name: "non-numeric target",
			raw:  `{"fields":{` + base + `,"quality":{"source":"map","value":"q","mapping":{"OK":"GOOD"},"default":"192"}}}`,
			want: "整数质量码",
		},
		{
			name: "non-numeric default",
			raw:  `{"fields":{` + base + `,"quality":{"source":"map","value":"q","mapping":{"OK":"192"},"default":"OK"}}}`,
			want: "整数质量码",
		},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(tc.raw))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func TestKindValueMapping(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"},
			"kind": {
				"source": "map",
				"value": "dataType",
				"mapping": {"1": "bool", "2": "int", "3": "float", "TEMP": "float", "DINT": "int"},
				"default": "string"
			}
		}
	}`)

	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"numeric code", `{"deviceId":"d","tagId":"a","value":1,"dataType":3}`, driver.KindFloat},
		{"numeric string vs number", `{"deviceId":"d","tagId":"a","value":1,"dataType":1.0}`, driver.KindBool},
		{"case-insensitive key", `{"deviceId":"d","tagId":"a","value":1,"dataType":"temp"}`, driver.KindFloat},
		{"vendor code", `{"deviceId":"d","tagId":"a","value":1,"dataType":"DINT"}`, driver.KindInt},
		{"unmatched uses default", `{"deviceId":"d","tagId":"a","value":1,"dataType":99}`, driver.KindString},
	}
	for _, tc := range cases {
		res, err := rule.Convert([]byte(tc.payload))
		if err != nil {
			t.Fatalf("%s: Convert() error = %v", tc.name, err)
		}
		if got := res.Records[0].Kind; got != tc.want {
			t.Errorf("%s: kind = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMapSourceValidation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "missing path",
			raw:  `{"fields":{"deviceId":{"value":"d"},"deviceAddressId":{"value":"a"},"value":{"value":"v"},"kind":{"source":"map","mapping":{"1":"int"}}}}`,
			want: "取值路径",
		},
		{
			name: "empty mapping",
			raw:  `{"fields":{"deviceId":{"value":"d"},"deviceAddressId":{"value":"a"},"value":{"value":"v"},"kind":{"source":"map","value":"t"}}}`,
			want: "至少需要一条映射",
		},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(tc.raw))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func TestKindNormalization(t *testing.T) {
	cases := map[string]string{
		"Float":    driver.KindFloat,
		"DOUBLE":   driver.KindFloat,
		"WORD":     driver.KindUInt,
		"Boolean":  driver.KindBool,
		"DateTime": driver.KindTime,
		"custom":   "custom",
	}
	for input, want := range cases {
		if got := normalizeKind(input); got != want {
			t.Errorf("normalizeKind(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNestedPathsAndTemplate(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceId":        {"source": "template", "value": "{gateway.id}-{deviceId}"},
			"deviceAddressId": {"source": "path", "value": "tags[0].id"},
			"value":           {"source": "path", "value": "samples.0.value"}
		}
	}`)
	res, err := rule.Convert([]byte(`{
		"gateway": {"id": "gw1"},
		"deviceId": "d1",
		"tags": [{"id": "t1"}],
		"samples": [{"value": 7}]
	}`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	rec := res.Records[0]
	if rec.DeviceID != "gw1-d1" || rec.DeviceAddressID != "t1" || rec.Value != "7" {
		t.Errorf("unexpected record: %+v", rec)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "missing required field",
			raw:  `{"fields":{"deviceId":{"source":"path","value":"d"}}}`,
			want: "deviceAddressId",
		},
		{
			name: "unknown target field",
			raw:  `{"fields":{"deviceId":{"value":"d"},"deviceAddressId":{"value":"a"},"value":{"value":"v"},"unknown":{"value":"x"}}}`,
			want: "未知的目标字段",
		},
		{
			name: "unknown source",
			raw:  `{"fields":{"deviceId":{"source":"expr","value":"d"},"deviceAddressId":{"value":"a"},"value":{"value":"v"}}}`,
			want: "未知的取值方式",
		},
		{
			name: "in filter without values",
			raw: `{"fields":{"deviceId":{"value":"d"},"deviceAddressId":{"value":"a"},"value":{"value":"v"}},
			       "filter":[{"path":"q","op":"in"}]}`,
			want: "values",
		},
	}
	for _, tc := range cases {
		_, err := Parse([]byte(tc.raw))
		if err == nil {
			t.Errorf("%s: expected error", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

// TestLegacyFieldNames 历史配置里的 deviceID / deviceAddressID 仍能加载并转换，
// 解析后统一归一成 deviceId / deviceAddressId。
func TestLegacyFieldNames(t *testing.T) {
	rule := mustParse(t, `{
		"fields": {
			"deviceID":        {"source": "path", "value": "deviceId"},
			"deviceAddressID": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		}
	}`)

	if _, ok := rule.Fields["deviceID"]; ok {
		t.Error("legacy key deviceID should be normalized away")
	}
	if _, ok := rule.Fields[FieldDeviceID]; !ok {
		t.Errorf("normalized fields missing %q", FieldDeviceID)
	}

	res, err := rule.Convert([]byte(`{"deviceId":"d1","tagId":"a1","value":1}`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 1 || res.Records[0].DeviceID != "d1" || res.Records[0].DeviceAddressID != "a1" {
		t.Fatalf("unexpected records: %+v", res.Records)
	}
}

func TestFilterNumericTolerance(t *testing.T) {
	rule := mustParse(t, `{
		"root": "$[*]",
		"fields": {
			"deviceId":        {"source": "path", "value": "deviceId"},
			"deviceAddressId": {"source": "path", "value": "tagId"},
			"value":           {"source": "path", "value": "value"}
		},
		"filter": [{"path": "seq", "op": "gte", "value": "10"}]
	}`)
	res, err := rule.Convert([]byte(`[
		{"deviceId":"d1","tagId":"a1","value":1,"seq":"9.5"},
		{"deviceId":"d1","tagId":"a2","value":2,"seq":"10.0"}
	]`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if len(res.Records) != 1 || res.Records[0].DeviceAddressID != "a2" {
		t.Fatalf("filter result = %+v", res.Records)
	}
}
