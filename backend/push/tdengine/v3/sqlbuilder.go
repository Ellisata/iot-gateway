package v3

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"iot-gateway/collector"
	"iot-gateway/driver"
)

// 超级表值族列宽:写入侧截断与建表 DDL 必须保持一致(见 ensureSchemaSQL),改动两处同步。
const (
	// maxValueStr value_str NCHAR 宽度(按字符计,超长按 rune 截断兜底)
	maxValueStr = 255
	// maxKindLen value_kind NCHAR 宽度(声明类别,恒远小于此值,截断仅防御)
	maxKindLen = 16
	// maxTagLen tag(device_id / device_address_id)NCHAR 宽度
	maxTagLen = 64
)

// insertBuilder 将多个采集批次聚合为单条多子表批量 INSERT 语句。
//
// TDengine 原生支持一条 INSERT 内并列多个 `sub USING stable TAGS(...) VALUES(...)`
// 子句(自动建表),聚合可把「每设备每轮采集一次往返」合并为「攒批后一次往返」,
// 显著降低设备量大时的固定开销(见 batchRows / batchIntervalMs 配置)。
//
// 子表名 = deviceId_deviceAddressId(自动建表),tag 仅 deviceId / deviceAddressId
// 两个稳定标识(名称可变不作 tag)。同一条语句内子表名必须唯一:
// appendBatch 检测到与已追加子表冲突(跨采集轮次同一设备+点位、不同时间戳)时
// 返回 false 且不改动状态,调用方应先 flush 再重试,避免跨轮数据被批内去重丢弃。
type insertBuilder struct {
	db     string
	prefix string
	sb     strings.Builder
	seen   map[string]struct{} // 本语句已含子表,用于冲突检测与批内去重
	rows   int
}

func newInsertBuilder(db, prefix string) *insertBuilder {
	ib := &insertBuilder{db: db, prefix: prefix, seen: make(map[string]struct{})}
	ib.sb.Grow(256)
	ib.sb.WriteString("INSERT INTO ")
	return ib
}

// appendBatch 将一批采集记录追加进语句。批内重复子表只保留首条(与单批构建一致)。
// 返回 false 表示本批与已追加子表冲突,状态未改动,调用方应 flush 后重试。
func (ib *insertBuilder) appendBatch(collectedAt string, records []collector.CollectedRecord) bool {
	// 预检:任一记录的子表已在本语句中 → 整体拒绝,不做部分追加
	for _, r := range records {
		if _, dup := ib.seen[subtableName(r.DeviceID, r.DeviceAddressID)]; dup {
			return false
		}
	}

	ts := collectedAtMillis(collectedAt)
	ib.sb.Grow(len(records) * 24)
	for _, r := range records {
		sub := subtableName(r.DeviceID, r.DeviceAddressID)
		if _, dup := ib.seen[sub]; dup {
			continue // 批内重复子表:保留首条
		}
		ib.seen[sub] = struct{}{}

		if ib.rows > 0 {
			ib.sb.WriteByte(' ')
		}

		// value 列:按 Kind 落到超级表值族对应列(value_bool/int/float/str),保留原生类型入库;
		// value_kind 列每行记录声明类别,查询可据此判定读取哪列(见 valueParts)
		vb, vi, vf, vs, vk := valueParts(r.Value, r.Kind)

		fmt.Fprintf(&ib.sb, "%s.%s USING %s.%s TAGS (%s) VALUES (%d, %s, %s, %s, %s, %s, %d)",
			ib.db, sub,
			ib.db, ib.prefix,
			joinTags(r),
			ts, vb, vi, vf, vs, vk, r.Quality)
		ib.rows++
	}
	return true
}

// empty 语句当前是否无行可写
func (ib *insertBuilder) empty() bool { return ib.rows == 0 }

// String 返回完整 INSERT 语句;无行时返回空串,调用方应跳过写入。
func (ib *insertBuilder) String() string {
	if ib.rows == 0 {
		return ""
	}
	return ib.sb.String()
}

// buildInsertSQL 将单个批次的记录构建为单条多子表批量 INSERT 语句(insertBuilder 的单批封装)。
// 子表名 = deviceId_deviceAddressId,value 按 Kind 落到超级表值族对应列(见 valueParts),
// 保留原生类型而非统一字符串:
//
//	INSERT INTO {db}.{sub} USING {db}.{stable} TAGS (…) VALUES (…) {db}.{sub} USING … …
func buildInsertSQL(db, prefix, collectedAt string, records []collector.CollectedRecord) string {
	ib := newInsertBuilder(db, prefix)
	ib.appendBatch(collectedAt, records)
	return ib.String()
}

// valueParts 按 PLC 数据类型类别 Kind 将采集值渲染为超级表值族各列的 SQL 字面量
// (列顺序与 ensureSchemaSQL 建表一致),保留原生类型入库而非统一字符串:
//
//	  KindBool       → value_bool  BOOL
//	  KindUInt / Int → value_int   BIGINT
//	  KindFloat      → value_float DOUBLE
//	  KindString / Time → value_str NCHAR
//
// 返回值 vb/vi/vf/vs 恰一个为实际值、其余为 "NULL"(TDengine 非时间戳列允许 NULL,
// 列式存储下空列几乎零成本;每行恰一值列非空)。vk 为 value_kind 列字面量,
// 作普通列而非 tag:Kind 可能随地址重配变化,tag 一经创建不可改会陈旧。
// 空值回落值列全 NULL;数值类别解析失败回落 value_str + kind=string(不阻断整批,
// 与旧 NCHAR 策略同语义);空/未知 Kind 自动数值推断(int → float → string 降级)。
func valueParts(v, kind string) (vb, vi, vf, vs, vk string) {
	const nul = "NULL"
	vb, vi, vf, vs = nul, nul, nul, nul

	// 空值:值列全 NULL,value_kind 保留声明类别(空/未知按 string)
	if v == "" {
		if kind == "" {
			kind = driver.KindString
		}
		return nul, nul, nul, nul, quoteKind(kind)
	}

	switch kind {
	case driver.KindBool:
		if b, err := strconv.ParseBool(v); err == nil {
			return strconv.FormatBool(b), nul, nul, nul, quoteKind(driver.KindBool)
		}
		return nul, nul, nul, quoteString(truncateRunes(v, maxValueStr)), quoteKind(driver.KindString)

	case driver.KindUInt:
		if u, err := strconv.ParseUint(v, 10, 64); err == nil {
			return nul, strconv.FormatUint(u, 10), nul, nul, quoteKind(driver.KindUInt)
		}
		// 无符号解析失败(负号/浮点写法):落浮点尝试,再回落文本

	case driver.KindInt:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return nul, strconv.FormatInt(i, 10), nul, nul, quoteKind(driver.KindInt)
		}
		// 整数解析失败(如 "3.14" 声明为 int):落浮点尝试,再回落文本

	case driver.KindFloat:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return nul, nul, formatFloat(f), nul, quoteKind(driver.KindFloat)
		}

	case driver.KindString, driver.KindTime:
		return nul, nul, nul, quoteString(truncateRunes(v, maxValueStr)), quoteKind(kind)

	default:
		// 空/未知 Kind:自动数值推断(int → float → string 降级)
		if !strings.Contains(v, ".") {
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				return nul, strconv.FormatInt(i, 10), nul, nul, quoteKind(driver.KindInt)
			}
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return nul, nul, formatFloat(f), nul, quoteKind(driver.KindFloat)
		}
	}

	// 数值类别解析失败:兼容浮点写法后再回落文本列(与旧 parseValueByType 降级链一致)
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return nul, nul, formatFloat(f), nul, quoteKind(driver.KindFloat)
	}
	return nul, nul, nul, quoteString(truncateRunes(v, maxValueStr)), quoteKind(driver.KindString)
}

// formatFloat 将 float64 渲染为最短可往返表示的十进制字面量(必要时科学计数)。
// 供 value_float DOUBLE 列使用:DOUBLE 无宽度限制,不涉及旧 NCHAR 超长截断问题。
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// quoteKind 渲染 value_kind 列字面量(NCHAR 宽度 maxKindLen,超长按 rune 截断防御)。
func quoteKind(kind string) string {
	return quoteString(truncateRunes(kind, maxKindLen))
}

// collectedAtMillis 将采集时间(毫秒精度 "2006-01-02 15:04:05.000",兼容旧秒级串)按本地
// 时区转换为 epoch 毫秒,供 TDengine TIMESTAMP 列使用。解析失败时回落当前时间,避免整批写入失败。
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

// subtableName 生成子表名:sanitizeIdent 后的 deviceId_deviceAddressId 拼接 + fnv64 哈希后缀。
//
// 哈希后缀必须恒定追加:消毒会折叠不同原始串(如 "dev-1" 与 "dev_1" 均 → "dev_1"),
// 若仅靠消毒串做表名,两个不同设备会落到同一子表,而 TAGS(原始串)不同 → TDengine
// 对已存在子表校验 TAGS 不一致而报错,写入永久失败并阻塞补发队列。
// 带哈希后缀保证原始 (deviceId, addressId) 与子表名一一映射
// (截断到 160 + 16 位 hex,恒 ≤ 177,不超 TDengine 表名上限 192)。
func subtableName(deviceID, addressID string) string {
	base := sanitizeIdent(deviceID) + "_" + sanitizeIdent(addressID)
	if len(base) > 160 {
		base = base[:160]
	}
	return base + "_" + subtableHash(deviceID, addressID)
}

// subtableHash 基于原始 deviceId/addressId 计算 fnv64 十六进制后缀(与 sanitizeIdent 无关,
// 保证消毒碰撞的两组原始串也映射到不同子表)。
func subtableHash(deviceID, addressID string) string {
	h := fnv.New64a()
	h.Write([]byte(deviceID))
	h.Write([]byte("|"))
	h.Write([]byte(addressID))
	return strconv.FormatUint(h.Sum64(), 16)
}

// joinTags 将采集记录的标签组合为 TAGS(...) 内的值列表(与建表 tag 顺序一致)。
// 仅保留稳定标识 deviceId / deviceAddressId:deviceName / deviceAddressName 可变,
// 作 tag 会因子表 tag 值不可变更而陈旧,故不入库(名称查询/展示走设备配置关联)。
func joinTags(r collector.CollectedRecord) string {
	return quoteString(truncateRunes(r.DeviceID, maxTagLen)) + ", " +
		quoteString(truncateRunes(r.DeviceAddressID, maxTagLen))
}

// truncateRunes 将字符串截断为至多 max 个 rune(字符)。
// NCHAR(n) 按字符数计宽,超长写入会报错,故按字符截断兜底。
func truncateRunes(s string, max int) string {
	if len(s) <= max { // 字节数 ≤ max 时字符数必然 ≤ max,快速路径
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// quoteString 将字符串包装为单引号字面量并转义(TDengine 字符串转义为反斜杠风格)。
func quoteString(s string) string {
	return "'" + escapeSQL(s) + "'"
}

// escapeSQL 转义单引号与反斜杠(TDengine 字符串字面量内嵌 ' 与 \ 的转义)。
func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}
