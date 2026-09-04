// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package v3

import (
	"math"
	"strconv"
	"strings"
	"time"

	"iot-gateway/collector"
	"iot-gateway/driver"
)

// lineBuilder 将多个采集批次聚合为单个 HTTP POST 的 Line Protocol body
// (每行一个 point,新行分隔)。
//
// Line Protocol 单行格式:
//
//	{measurement},{tagK}={tagV},{tagK}={tagV} {fieldK}={fieldV},{fieldK}={fieldV} {timestamp}
//
// 与 tdengine 通道的差异:InfluxDB 以 (series, timestamp) 为行标识,不同批次的
// 采集时间不同即为不同 point,因此**无「批间冲突」概念**,appendBatch 永不拒绝
// (仅批内同设备+点位、同时间戳去重,保留首条)。
type lineBuilder struct {
	measurement string

	// sb 汇总多批次的完整 body;scratch 为单行渲染复用缓冲(避免逐点新建 Builder)。
	sb      strings.Builder
	scratch strings.Builder
	points  int

	// 批内去重:批次按设备拆分(engine.splitBatches),同批 device_id 恒定,
	// 以 device_address_id 为 key;设备切换时重建常量前缀并重置去重。
	// dedup map 跨批复用(clear 保容量),避免每批新建去重表。
	dedup     map[string]struct{}
	curDevice string
	prefix    string // 缓存的行首常量前缀(measurement + device_id 预转义),设备切换时重建
}

func newLineBuilder(measurement string) *lineBuilder {
	lb := &lineBuilder{
		measurement: measurement,
		dedup:       make(map[string]struct{}),
	}
	lb.sb.Grow(256)
	lb.scratch.Grow(128)
	return lb
}

// appendBatch 将一批采集记录追加进 body。
// 批内重复 (deviceID, addressID)(同时间戳)只保留首条;批按设备拆分,
// 常量前缀(measurement + device_id)只构建一次,逐点只渲染变化部分;
// 空值/浮点 NaN/±Inf 无法表示时跳过该 point(见 formatFieldValue),不阻断整批。
func (lb *lineBuilder) appendBatch(collectedAt string, records []collector.CollectedRecord) {
	ts := collectedAtMillis(collectedAt)
	clear(lb.dedup)
	for _, r := range records {
		if r.DeviceID != lb.curDevice {
			// 设备切换(批间,或防御性批内):重建常量前缀,去重按 (设备,地址) 重新计数
			lb.curDevice = r.DeviceID
			lb.prefix = linePrefix(lb.measurement, r.DeviceID)
			clear(lb.dedup)
		}
		if _, dup := lb.dedup[r.DeviceAddressID]; dup {
			continue // 批内重复 series:保留首条
		}
		lb.dedup[r.DeviceAddressID] = struct{}{}

		if !appendLine(&lb.scratch, lb.prefix, ts, r) {
			continue // 无法类型化:跳过该点
		}
		if lb.points > 0 {
			lb.sb.WriteByte('\n')
		}
		lb.sb.WriteString(lb.scratch.String())
		lb.points++
	}
}

// empty body 当前是否无 point 可写
func (lb *lineBuilder) empty() bool { return lb.points == 0 }

// String 返回完整 Line Protocol body;无 point 时返回空串,调用方应跳过写入。
func (lb *lineBuilder) String() string {
	if lb.points == 0 {
		return ""
	}
	return lb.sb.String()
}

// linePrefix 构建一行 Line Protocol 的常量前缀(measurement + device_id tag)。
// 批内 device_id 恒定,前缀只构建一次;device_address_id 变化部分由 appendLine 逐点追加,
// 避免每行重复转义常量 tag。
func linePrefix(measurement, deviceID string) string {
	return escapeMeasurement(measurement) + ",device_id=" + escapeTag(deviceID) + ",device_address_id="
}

// appendLine 将单条采集记录渲染进 sb(调用方提供复用缓冲),返回 ok=false 表示无法表示。
// tag 仅稳定标识 device_id / device_address_id(名称可变不作 tag,与 tdengine 通道一致);
// value 按 Kind 落到值族对应列(value_bool/value_int/value_float/value_str),每点只写
// 命中的一列(其余列缺省即 null),value_kind 记录声明类别;quality 为整数。
func appendLine(sb *strings.Builder, prefix string, ts int64, r collector.CollectedRecord) bool {
	vv, ok := formatFieldValue(r.Value, r.Kind)
	if !ok {
		return false // 空值/浮点 NaN/±Inf:调用方跳过
	}

	sb.Reset()
	sb.WriteString(prefix)
	sb.WriteString(escapeTag(r.DeviceAddressID))
	sb.WriteByte(' ')

	sb.WriteString(vv.col)
	sb.WriteByte('=')
	sb.WriteString(vv.lit)
	sb.WriteString(",value_kind=")
	sb.WriteString(vv.kind)
	sb.WriteString(",quality=")
	sb.WriteString(strconv.FormatInt(int64(r.Quality), 10))
	sb.WriteByte('i')

	sb.WriteByte(' ')
	sb.WriteString(strconv.FormatInt(ts, 10))
	return true
}

// buildLine 渲染单条记录为一行 Line Protocol(单条独立渲染用;热路径由 appendBatch
// 经 appendLine 复用缓冲,不新建 Builder)。
func buildLine(measurement string, ts int64, r collector.CollectedRecord) (string, bool) {
	var sb strings.Builder
	if !appendLine(&sb, linePrefix(measurement, r.DeviceID), ts, r) {
		return "", false
	}
	return sb.String(), true
}

// 值族字段名(iox 类型固定):InfluxDB 3.x 同 measurement 内每个字段名只允许一种
// iox 类型,故 value 按类型拆列,每列类型恒定,规避同表 field 类型锁(见 formatFieldValue)。
const (
	fieldBool  = "value_bool"  // boolean
	fieldInt   = "value_int"   // integer(统一按有符号 i 写)
	fieldFloat = "value_float" // float
	fieldStr   = "value_str"   // string
)

// valueField 单点值的渲染结果:命中的值列名 + 该列字面量 + value_kind 列字面量(已带引号)。
type valueField struct {
	col  string
	lit  string
	kind string
}

// formatFieldValue 按 PLC 数据类型类别 Kind 将字符串值渲染为值族对应列的字面量。
//
// InfluxDB 3.x 的 field 列类型按表(measurement)锁定:同表内同名 field 只允许一种
// iox 类型(integer / uinteger / float / boolean / string),跨 series(不同 tag 集)
// 同样生效,后续写入不同类型整行 400。同一通道写一个 measurement,表内不同点位可能
// 声明为不同 Kind,故不能共用单一 value 字段,必须按 iox 类型拆成值族四列,每列类型恒定:
//
//	KindBool        → value_bool(boolean)
//	KindUInt        → value_int,统一按有符号 i 写(uint8/16/32、word/dword/BCD/LBCD
//	                  的真实取值 ≤ uint32 均在 int64 范围内,同表内 signed/unsigned
//	                  点位共列,可避免 value_int 在 integer/uinteger 间翻转;仅超
//	                  int64 的 uint64 全量回落 value_str 文本列保留原值)
//	KindInt         → value_int(解析失败回落 value_str)
//	KindFloat       → value_float;NaN/±Inf 无法由行协议表示(写 API 返回 400 拒绝
//	                  整窗),返回 ok=false 由调用方跳过,避免脏读(PLC 浮点寄存器
//	                  未初始化/除零)毒化整个 series
//	KindString/Time → value_str(原样字符串)
//	空/未知 Kind(历史数据或未注册类型):自动数值推断 int → float → string 降级
//
// 数值类别解析失败(脏数据/浮点写法声明为整数)回落 value_str 文本列而非跳过,与
// tdengine 通道 valueParts 的降级链一致(脏读通常为瞬时,下一轮恢复正常数值列)。
// 空值返回 ok=false 跳过该点(不写全空行)。
func formatFieldValue(v, kind string) (valueField, bool) {
	if v == "" {
		return valueField{}, false
	}

	switch kind {
	case driver.KindBool:
		if b, err := strconv.ParseBool(v); err == nil {
			return valueField{col: fieldBool, lit: strconv.FormatBool(b), kind: quoteFieldString(driver.KindBool)}, true
		}

	case driver.KindUInt:
		if u, err := strconv.ParseUint(v, 10, 64); err == nil && u <= math.MaxInt64 {
			return valueField{col: fieldInt, lit: strconv.FormatInt(int64(u), 10) + "i", kind: quoteFieldString(driver.KindUInt)}, true
		}

	case driver.KindInt:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return valueField{col: fieldInt, lit: strconv.FormatInt(i, 10) + "i", kind: quoteFieldString(driver.KindInt)}, true
		}

	case driver.KindFloat:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			break // 解析失败:回落 value_str
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return valueField{}, false
		}
		return valueField{col: fieldFloat, lit: formatFloat(f), kind: quoteFieldString(driver.KindFloat)}, true

	case driver.KindString, driver.KindTime:
		return valueField{col: fieldStr, lit: quoteFieldString(v), kind: quoteFieldString(kind)}, true

	default:
		// 空/未知 Kind:自动数值推断(int → float → string 降级)
		if !strings.Contains(v, ".") {
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return valueField{col: fieldInt, lit: strconv.FormatInt(i, 10) + "i", kind: quoteFieldString(driver.KindInt)}, true
			}
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return valueField{}, false
			}
			return valueField{col: fieldFloat, lit: formatFloat(f), kind: quoteFieldString(driver.KindFloat)}, true
		}
		return valueField{col: fieldStr, lit: quoteFieldString(v), kind: quoteFieldString(driver.KindString)}, true
	}

	// 数值类别解析失败(含超 int64 的 uint64 全量):回落 value_str 文本列保留原值,
	// value_kind 标注 string(实际落列与类别一致,查询可直接读)。
	return valueField{col: fieldStr, lit: quoteFieldString(v), kind: quoteFieldString(driver.KindString)}, true
}

// formatFloat 渲染浮点 field 值(无后缀 = float)。
// 浮点超大值(长十进制串)回落科学计数,避免过长 body。
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if len(s) > 250 {
		s = strconv.FormatFloat(f, 'g', -1, 64)
	}
	return s
}

// collectedAtMillis 将采集时间(毫秒精度 "2006-01-02 15:04:05.000",兼容旧秒级串)
// 按本地时区转换为 epoch 毫秒,供 Line Protocol timestamp 使用(precision=millisecond)。
// 解析失败时回落当前时间,避免整批写入失败。
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

// escapeTag 转义 tag 键值中的特殊字符(Line Protocol 规则):
// 反斜杠、逗号、空格、等号均须加反斜杠前缀。反斜杠先替换,避免二次转义。
func escapeTag(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `,`, `\,`)
	s = strings.ReplaceAll(s, ` `, `\ `)
	s = strings.ReplaceAll(s, `=`, `\=`)
	return s
}

// escapeMeasurement 转义 measurement 名(Line Protocol 规则):measurement 只需
// 转义逗号与空格(与 tag 不同,`=` 与 `\` 是合法 measurement 字符,转义反而冗余)。
func escapeMeasurement(s string) string {
	s = strings.ReplaceAll(s, `,`, `\,`)
	s = strings.ReplaceAll(s, ` `, `\ `)
	return s
}

// quoteFieldString 渲染字符串 field 值:双引号包裹,转义内部反斜杠与双引号。
// 原始换行/回车会截断或错位 Line Protocol 行(InfluxDB 不支持在字符串值内表示换行),
// 转为可见转义序列 `\\n` / `\\r`(入库后为字面反斜杠+n/r),避免整窗写入被 400 拒绝。
func quoteFieldString(s string) string {
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
