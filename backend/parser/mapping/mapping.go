// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package mapping 把任意 JSON 报文转换为 collector.CollectedRecord。
//
// 设计参考 iot-data-persistence 的可视化数据映射：
//   - 规则由前端页面产出、存于 mqtt_subscription.mapping_json；
//   - root 支持整篇 / 对象 / 数组逐元素拆条；
//   - 每个目标字段用 path / const / template 三种取值方式；
//   - filter 为隐式 AND 的过滤条件，不满足即丢弃该条记录。
//
// 与参考实现的差异：目标结构固定为 CollectedRecord，不接受任意 measurement/tags/fields，
// 因此字段名是白名单（见 FieldNames），未知字段在保存时直接拒绝，避免拼错字段名后
// 「配置看着没问题、数据一条也没有」。
package mapping

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
	"iot-gateway/mappingdsl"
	"iot-gateway/parser"
)

// CollectedRecord 的可映射字段（目标字段名）。
//
// 真源在 parser 契约包：脚本层与映射层必须共用同一份白名单。
const (
	FieldDeviceID           = parser.FieldDeviceID
	FieldDeviceName         = parser.FieldDeviceName
	FieldDeviceAddressID    = parser.FieldDeviceAddressID
	FieldDeviceAddressName  = parser.FieldDeviceAddressName
	FieldDeviceAddressLabel = parser.FieldDeviceAddressLabel
	FieldValue              = parser.FieldValue
	FieldCollectedAt        = parser.FieldCollectedAt
	FieldDataType           = parser.FieldDataType
	FieldKind               = parser.FieldKind
	FieldProtocol           = parser.FieldProtocol
	FieldQuality            = parser.FieldQuality
)

// legacyFieldAliases 历史配置里的目标字段名 → 当前字段名。
//
// deviceID / deviceAddressID 是早期版本的写法；保存的 mapping_json 里可能还留着它们。
// 解析时统一归一，既不让老订阅失效，也保证 Validate/Apply 只认一套名字。
var legacyFieldAliases = map[string]string{
	"deviceID":        FieldDeviceID,
	"deviceAddressID": FieldDeviceAddressID,
}

// FieldNames 全部合法的目标字段，顺序即前端映射表格的展示顺序。
var FieldNames = parser.FieldNames

// requiredFields 没有映射就无法产出有意义记录、也无法落时序库子表的字段。
// deviceId / deviceAddressId 是 TDengine 子表名与 tag 的来源，value 是数据本身。
// 这是「加载已有配置」的底线：老配置即便缺名称/类型字段也要能继续运行。
var requiredFields = []string{FieldDeviceID, FieldDeviceAddressID, FieldValue}

// strictRequiredFields 保存新配置时必须配置的全部主字段（界面上的必填项）。
// 加载已有配置时不用这份清单，避免历史订阅因新增必填项被整体跳过。
var strictRequiredFields = []string{
	FieldDeviceID,
	FieldDeviceName,
	FieldDeviceAddressID,
	FieldDeviceAddressLabel,
	FieldValue,
	FieldKind,
	FieldQuality,
}

// 取值方式。
const (
	SourcePath     = mappingdsl.SourcePath
	SourceConst    = mappingdsl.SourceConst
	SourceTemplate = mappingdsl.SourceTemplate
	// SourceMap 读取报文中一个值，再按 Mapping 翻译成本系统的值。
	// 典型用途：外部系统自有的类型定义（1/2/3、TEMP/SWITCH、DINT/REAL…）
	// 与本系统的 kind 类别不一致时逐项翻译。
	SourceMap = mappingdsl.SourceMap
	// SourceTopic 从 topicPattern 的命名段取值。MQTT 遥测最常见的身份来源是主题，
	// 只允许从 payload 取值会迫使这类报文写脚本。
	SourceTopic = mappingdsl.SourceTopic
)

// Rule JSON 报文 → CollectedRecord 的映射规则（DSL v1）。
//
//	示例：
//	  {
//	    "version": 1,
//	    "root": "$[*]",
//	    "fields": {
//	      "deviceId":        { "source": "path",     "value": "deviceId" },
//	      "deviceName":      { "source": "template", "value": "{gateway}-{deviceId}" },
//	      "deviceAddressId": { "source": "path",     "value": "tagId" },
//	      "value":           { "source": "path",     "value": "value" },
//	      "quality":         { "source": "const",    "value": "192" }
//	    },
//	    "filter": [ { "path": "quality", "op": "eq", "value": "1" } ]
//	  }
type Rule struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	// TopicPattern 主题模板，如 "factory/{deviceId}/telemetry"。
	// {name} 匹配一个 topic 段，供 source=topic 的字段取值。
	TopicPattern string                 `json:"topicPattern,omitempty"`
	Fields       map[string]ValueSource `json:"fields"`
	// Fanout 多测点扇出：基础 fields 先求值，每项在基础字段之上覆盖，
	// 产出一条记录。用于 {"temp":1,"hum":2} 这类一条报文多个点位的形态。
	Fanout []map[string]ValueSource `json:"fanout,omitempty"`
	Filter []FilterCondition        `json:"filter"`

	// topicSegs 由 Validate 编译，避免每条报文重复解析模板。
	topicSegs []topicSeg `json:"-"`
	// topicUsed 配置是否真的引用了命名段。没引用时 topicPattern 不参与解析，
	// 也不做主题匹配校验（见 usesTopicVars）。
	topicUsed bool `json:"-"`
}

// ValueSource 单个目标字段的取值方式。
//
// 实现已下沉到 mappingdsl（入站与出站共用）；保留别名是为了让既有 JSON
// 配置、前端映射编辑器与测试无需迁移。
type ValueSource = mappingdsl.ValueSource

// Parse 解析并校验映射规则（加载路径）。
// 只要求结构性字段，保证历史配置在新增必填项后仍能继续运行。
func Parse(raw []byte) (*Rule, error) {
	return parseWith(raw, false)
}

// ParseStrict 解析并校验映射规则（保存路径）：额外要求全部主字段都已配置。
func ParseStrict(raw []byte) (*Rule, error) {
	return parseWith(raw, true)
}

func parseWith(raw []byte, strict bool) (*Rule, error) {
	var rule Rule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return nil, fmt.Errorf("映射规则 JSON 解析失败: %w", err)
	}
	rule.normalizeFieldNames()
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	if strict {
		if err := rule.validateStrictRequired(); err != nil {
			return nil, err
		}
	}
	return &rule, nil
}

// normalizeFieldNames 把历史目标字段名统一成当前写法。
// 新旧名字同时出现时以新名字为准，旧键直接丢弃。
func (r *Rule) normalizeFieldNames() {
	if r.Fields == nil {
		return
	}
	for legacy, current := range legacyFieldAliases {
		src, ok := r.Fields[legacy]
		if !ok {
			continue
		}
		if _, exists := r.Fields[current]; !exists {
			r.Fields[current] = src
		}
		delete(r.Fields, legacy)
	}
}

// validateStrictRequired 校验保存时必须配置的主字段。
// 配置了 fanout 时按「基础字段 + 每项覆盖」合并后检查。
func (r *Rule) validateStrictRequired() error {
	if len(r.Fanout) == 0 {
		return validateRequiredFields(r.Fields, strictRequiredFields, "")
	}
	for i, ov := range r.Fanout {
		merged := mergeFields(r.Fields, ov)
		if err := validateRequiredFields(merged, strictRequiredFields, fmt.Sprintf("fanout 第 %d 项: ", i+1)); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredFields(fields map[string]ValueSource, names []string, prefix string) error {
	for _, name := range names {
		src, ok := fields[name]
		if !ok || strings.TrimSpace(src.Value) == "" {
			return fmt.Errorf("%s必须配置 %s 的取值", prefix, name)
		}
	}
	return nil
}

// Validate 校验规则是否自洽。
//
// 只检查配置本身（必填字段、未知字段、运算符、取值方式），不检查路径是否存在于
// 具体报文中 —— 那只能通过预览验证。
func (r *Rule) Validate() error {
	if len(r.Fields) == 0 {
		return errors.New("至少需要配置一个字段映射")
	}
	if err := r.validateFieldSet(r.Fields, ""); err != nil {
		return err
	}
	if err := r.compileTopicPattern(); err != nil {
		return err
	}
	r.topicUsed = r.usesTopicVars()
	if err := r.validateTopicSources(r.Fields, ""); err != nil {
		return err
	}

	if len(r.Fanout) == 0 {
		if err := validateRequiredFields(r.Fields, requiredFields, ""); err != nil {
			return err
		}
		qualitySrc, hasQuality := r.Fields[FieldQuality]
		if err := validateQualityMapping(qualitySrc, hasQuality); err != nil {
			return err
		}
	} else {
		for i, ov := range r.Fanout {
			prefix := fmt.Sprintf("fanout 第 %d 项: ", i+1)
			if len(ov) == 0 {
				return fmt.Errorf("fanout 第 %d 项: 不能为空", i+1)
			}
			if err := r.validateFieldSet(ov, prefix); err != nil {
				return err
			}
			if err := r.validateTopicSources(ov, prefix); err != nil {
				return err
			}
			merged := mergeFields(r.Fields, ov)
			if err := validateRequiredFields(merged, requiredFields, prefix); err != nil {
				return err
			}
			qualitySrc, hasQuality := merged[FieldQuality]
			if err := validateQualityMapping(qualitySrc, hasQuality); err != nil {
				return fmt.Errorf("%s%w", prefix, err)
			}
		}
	}

	for i := range r.Filter {
		if err := r.Filter[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateFieldSet 校验一组字段名与取值方式。
func (r *Rule) validateFieldSet(fields map[string]ValueSource, prefix string) error {
	for name := range fields {
		if !validField(name) {
			return fmt.Errorf("%s未知的目标字段 %q", prefix, name)
		}
		if err := fields[name].Validate(); err != nil {
			return fmt.Errorf("%s字段 %q: %w", prefix, name, err)
		}
	}
	return nil
}

// validateTopicSources 校验 source=topic 的字段都有对应的模板命名段。
func (r *Rule) validateTopicSources(fields map[string]ValueSource, prefix string) error {
	for name, src := range fields {
		if !src.IsTopic() {
			continue
		}
		if r.TopicPattern == "" {
			return fmt.Errorf("%s字段 %q 使用主题取值，但未配置 topicPattern", prefix, name)
		}
		if !r.hasTopicVar(src.Value) {
			return fmt.Errorf("%s字段 %q 的主题变量 %q 不在 topicPattern 中", prefix, name, src.Value)
		}
	}
	return nil
}

// mergeFields 返回基础字段 + 覆盖层的新 map（不修改入参）。
func mergeFields(base, overlay map[string]ValueSource) map[string]ValueSource {
	merged := make(map[string]ValueSource, len(base)+len(overlay))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overlay {
		merged[k] = v
	}
	return merged
}

// validateQualityMapping 校验 quality 的值映射。
//
// quality 与 kind 不同：kind 未命中时可以安全地退化为自动推断，而 quality 的
// 数值语义是网关自定义的（0 / 1 / 2 / 3 / 128 / 192），把外部质量码原样带进来
// 很可能含义相反（例如外部 0=正常，网关 1=地址无效）。因此：
//   - 必须配置未命中默认值；
//   - 默认值与每条映射的目标值都必须是整数质量码。
func validateQualityMapping(src ValueSource, ok bool) error {
	if !ok || !src.IsMap() {
		return nil
	}
	if strings.TrimSpace(src.Default) == "" {
		return errors.New("quality 值映射必须配置未命中默认值")
	}
	if _, ok := toInt(src.Default); !ok {
		return fmt.Errorf("quality 未命中默认值 %q 不是整数质量码", src.Default)
	}
	for raw, dst := range src.Mapping {
		if _, ok := toInt(strings.TrimSpace(dst)); !ok {
			return fmt.Errorf("quality 映射 %q 的目标值 %q 不是整数质量码", raw, dst)
		}
	}
	return nil
}

func validField(name string) bool {
	for _, f := range FieldNames {
		if name == f {
			return true
		}
	}
	return false
}

// ValueSource 的取值、变换与映射查找实现已下沉到 mappingdsl。

// Result 一次报文转换的结果。
type Result struct {
	Records []collector.CollectedRecord
	// Skipped 因缺字段/为空而跳过的记录数
	Skipped int
	// FirstError 首个失败原因（部分失败时用于日志与预览）
	FirstError error
}

// Convert 用规则转换一整条 MQTT payload（无主题；保留给旧调用与测试）。
func (r *Rule) Convert(payload []byte) (Result, error) {
	return r.ConvertWithTopic("", payload)
}

// ConvertWithTopic 用规则转换一整条 MQTT payload。
//
// 返回 error 只表示「整条报文不可用」（JSON 非法、root 不存在、主题不匹配、
// 全部记录都转换失败）；部分记录失败通过 Result.Skipped / FirstError 反映，
// 不整批丢弃 —— 数组报文里一条坏记录不该让其余几百条一起丢。
func (r *Rule) ConvertWithTopic(topic string, payload []byte) (Result, error) {
	raw, err := decodeJSON(payload)
	if err != nil {
		return Result{}, err
	}

	docs, err := r.Expand(raw)
	if err != nil {
		return Result{}, err
	}

	topicVars, err := r.topicVars(topic)
	if err != nil {
		return Result{}, err
	}

	var result Result
	for i, doc := range docs {
		if !r.Match(doc) {
			continue
		}
		records, skipped, applyErr := r.applyDoc(doc, topicVars)
		result.Skipped += skipped
		if applyErr != nil && result.FirstError == nil {
			result.FirstError = fmt.Errorf("第 %d 条记录: %w", i+1, applyErr)
		}
		result.Records = append(result.Records, records...)
	}

	if len(result.Records) == 0 && result.Skipped > 0 {
		return result, fmt.Errorf("全部 %d 条记录都转换失败（%w）", result.Skipped, result.FirstError)
	}
	return result, nil
}

// applyDoc 把单条文档转换为一条或多条记录（配置了 fanout 时为多条）。
func (r *Rule) applyDoc(doc any, topicVars map[string]string) ([]collector.CollectedRecord, int, error) {
	if len(r.Fanout) == 0 {
		rec, err := r.applyFields(doc, r.Fields, topicVars)
		if err != nil {
			return nil, 1, err
		}
		return []collector.CollectedRecord{rec}, 0, nil
	}

	records := make([]collector.CollectedRecord, 0, len(r.Fanout))
	skipped := 0
	var firstErr error
	for i, overlay := range r.Fanout {
		rec, err := r.applyFields(doc, mergeFields(r.Fields, overlay), topicVars)
		if err != nil {
			skipped++
			if firstErr == nil {
				firstErr = fmt.Errorf("扇出第 %d 项: %w", i+1, err)
			}
			continue
		}
		records = append(records, rec)
	}
	if len(records) == 0 {
		return nil, skipped, firstErr
	}
	return records, skipped, firstErr
}

// Apply 将单条文档转换为一条 CollectedRecord（fanout 时取第一条）。
func (r *Rule) Apply(doc any) (collector.CollectedRecord, error) {
	records, _, err := r.applyDoc(doc, nil)
	if len(records) == 0 {
		if err != nil {
			return collector.CollectedRecord{}, err
		}
		return collector.CollectedRecord{}, errors.New("没有可用的记录")
	}
	return records[0], nil
}

// applyFields 用一组字段配置把单条文档转换为一条记录。
func (r *Rule) applyFields(doc any, fields map[string]ValueSource, topicVars map[string]string) (collector.CollectedRecord, error) {
	var rec collector.CollectedRecord

	deviceID, err := requiredField(fields, doc, topicVars, FieldDeviceID)
	if err != nil {
		return rec, err
	}
	addressID, err := requiredField(fields, doc, topicVars, FieldDeviceAddressID)
	if err != nil {
		return rec, err
	}

	valueSrc, ok := fields[FieldValue]
	if !ok {
		return rec, errors.New("value 未配置取值")
	}
	valueRaw, err := valueSrc.Resolve(doc, topicVars)
	if err != nil {
		return rec, fmt.Errorf("value 取值失败: %w", err)
	}
	value := toString(valueRaw)
	if value == "" {
		return rec, errors.New("value 为空")
	}

	rec.DeviceID = deviceID
	rec.DeviceAddressID = addressID
	rec.DeviceName = optionalField(fields, doc, topicVars, FieldDeviceName)
	rec.DeviceAddressName = optionalField(fields, doc, topicVars, FieldDeviceAddressName)
	rec.DeviceAddressLabel = optionalField(fields, doc, topicVars, FieldDeviceAddressLabel)
	rec.Value = value
	// 采集时间可选：识别常见时间写法/epoch 后归一成毫秒精度本地时间；
	// 未配置或无法识别时留空，时序通道回落到批次落地时间。
	rec.CollectedAt = normalizeCollectedAt(optionalField(fields, doc, topicVars, FieldCollectedAt))
	rec.DataType = optionalField(fields, doc, topicVars, FieldDataType)
	rec.Kind = normalizeKind(optionalField(fields, doc, topicVars, FieldKind))
	rec.Protocol = optionalField(fields, doc, topicVars, FieldProtocol)
	rec.Quality = qualityOf(fields, doc, topicVars)

	if rec.Kind == "" {
		// 未映射 kind 时只在 JSON 类型足以判断时补上；字符串留给时序通道
		// 按「整型 → 浮点 → 文本」自动推断（见 push/tdengine、push/influxdb）。
		rec.Kind = inferKind(valueRaw)
	}
	return rec, nil
}

// requiredField 取必填字段并拒绝空值。
func requiredField(fields map[string]ValueSource, doc any, topicVars map[string]string, field string) (string, error) {
	src, ok := fields[field]
	if !ok {
		return "", fmt.Errorf("%s 未配置取值", field)
	}
	v, err := src.Resolve(doc, topicVars)
	if err != nil {
		return "", fmt.Errorf("%s 取值失败: %w", field, err)
	}
	s := strings.TrimSpace(toString(v))
	if s == "" {
		return "", fmt.Errorf("%s 为空", field)
	}
	return s, nil
}

// optionalField 取可选字段；未配置或取值失败都返回空串。
func optionalField(fields map[string]ValueSource, doc any, topicVars map[string]string, field string) string {
	src, ok := fields[field]
	if !ok {
		return ""
	}
	v, err := src.Resolve(doc, topicVars)
	if err != nil {
		return ""
	}
	return toString(v)
}

// qualityOf 取质量码。未配置时默认 QualityGood（外部数据视为正常）；
// 配置了但解析失败也回落 QualityGood —— 不因为脏质量码丢掉整条数据。
func qualityOf(fields map[string]ValueSource, doc any, topicVars map[string]string) int {
	src, ok := fields[FieldQuality]
	if !ok {
		return driver.QualityGood
	}
	v, err := src.Resolve(doc, topicVars)
	if err != nil {
		return driver.QualityGood
	}
	if n, ok := toInt(toString(v)); ok {
		return int(n)
	}
	return driver.QualityGood
}

// Expand 按 root 把整篇文档展开为待转换的单条文档列表。
//
//	"$" / ""     整篇文档作为一条记录；若整篇文档本身是数组，则逐元素拆条
//	"data"       data 作为一条记录（保持单条语义，逐条请写 "data[*]"）
//	"$[*]"       文档本身是数组，逐元素拆条
//	"$.data[*]"  data 是数组，逐元素拆条
//
// 顶层数组自动拆条是刻意的：MQTT 遥测报文最常见的形态就是 [{...},{...}]，
// 默认 root 为 "$" 时再让用户补写 [*]，只会得到「字段在报文中不存在」这种
// 与真实原因无关的报错。显式写了路径的 root 仍保留原有语义。
func (r *Rule) Expand(raw any) ([]any, error) {
	base, array := parseRoot(r.Root)

	doc, ok := parser.LookupPath(raw, base)
	if !ok {
		return nil, fmt.Errorf("root %q 在报文中不存在", r.Root)
	}
	if !array {
		if base == "" {
			if arr, ok := doc.([]any); ok {
				return arr, nil
			}
		}
		return []any{doc}, nil
	}

	arr, ok := doc.([]any)
	if !ok {
		return nil, fmt.Errorf("root %q 期望数组，实际为 %s", r.Root, jsonTypeName(doc))
	}
	return arr, nil
}

func parseRoot(root string) (base string, array bool) {
	root = strings.TrimSpace(root)
	root = strings.TrimPrefix(root, "$")
	root = strings.TrimPrefix(root, ".")

	if strings.HasSuffix(root, "[*]") {
		return strings.TrimSuffix(strings.TrimSuffix(root, "[*]"), "."), true
	}
	return root, false
}

// Match 判断单条文档是否通过全部过滤条件（隐式 AND）。
func (r *Rule) Match(doc any) bool {
	for i := range r.Filter {
		if !r.Filter[i].match(doc) {
			return false
		}
	}
	return true
}

// decodeJSON 解析 JSON，数字统一以 json.Number 承载。
// 默认的 float64 解码会把设备 ID、毫秒时间戳等大整数变成近似值，落库即失真。
func decodeJSON(payload []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()

	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	return raw, nil
}

func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "布尔值"
	case string:
		return "字符串"
	case map[string]any:
		return "对象"
	case []any:
		return "数组"
	default:
		return "数字"
	}
}

// normalizeKind 把常见的类型写法归一为 driver.KindXxx。
//
// 外部系统的报文里可能是 "Float"、"WORD"、"Double" 这类写法；时序通道只认
// 全小写的六个类别。这里统一做大小写与常见别名归一，避免「配置了 kind 却因为
// 大小写不匹配退化成自动推断」这种静默偏差。认不出的值原样小写返回，由时序
// 通道按「空/未知 Kind」的自动推断处理。
func normalizeKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bool", "boolean":
		return driver.KindBool
	case "int", "integer", "short", "long", "int8", "int16", "int32", "int64":
		return driver.KindInt
	case "uint", "unsigned", "byte", "word", "dword", "uint8", "uint16", "uint32", "uint64":
		return driver.KindUInt
	case "float", "float32", "float64", "double", "number":
		return driver.KindFloat
	case "string", "text", "char":
		return driver.KindString
	case "time", "date", "datetime", "timestamp":
		return driver.KindTime
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}

func inferKind(v any) string {
	switch t := v.(type) {
	case bool:
		return driver.KindBool
	case json.Number:
		if _, err := t.Int64(); err == nil {
			return driver.KindInt
		}
		return driver.KindFloat
	case float64:
		return driver.KindFloat
	case int64, int:
		return driver.KindInt
	default:
		// 字符串/空值：留空，交由时序通道按值自动推断
		return ""
	}
}
