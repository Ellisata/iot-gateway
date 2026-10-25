// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// 越限记录的写入与保留参数。
//
// 全部是硬编码常量而非配置项：它们只影响"留证多少"，不影响报警正确性，
// 与 Tracker.touchMinInterval 属同一取舍（见 specs/alarm.md）。
const (
	// thresholdRecordMinInterval 同一连续越限段内，越限记录的最小写入间隔。
	//
	// 段的**首条样本总是立即写入**（见 persist 的注释）：否则一个只持续几秒的短促越限
	// 在历史里会完全消失，而它恰恰是运维最想回看的东西。之后按本间隔节流。
	//
	// 生产库 MaxOpenConns=1（database/sqlite/database.go），每次同步写都要与 HTTP 请求
	// 抢那唯一的连接，所以节流不只是省磁盘。
	thresholdRecordMinInterval = time.Minute
	// thresholdRecordRetention 越限记录的保留时长。
	thresholdRecordRetention = 7 * 24 * time.Hour
	// thresholdRecordMaxRows 越限记录的硬行数上限（比按时长更硬的一道界）。
	thresholdRecordMaxRows = 20000
	// thresholdRecordCleanupInterval 惰性清理的最小间隔。
	//
	// **刻意不起清理协程**：清理挂在写入路径上按间隔顺带做，与 devicescan 的惰性 TTL
	// （过期判定放在 Submit/Status 里）同一取舍 —— 高频率并不需要"准时"清理。
	thresholdRecordCleanupInterval = time.Hour
)

// 越限记录里的 bound 取值。range 命中的是哪一侧（high/low），其余算子就是算子名本身
// （gt/ge/lt/le/eq/ne/in，直接复用 driver 的常量）。BoundName 据此给中文。
const (
	boundHigh = "high"
	boundLow  = "low"
)

// ThresholdEngine 点位阈值报警引擎。
//
// 实现 collector.PointValueSink：采集引擎每轮把**配了阈值**的点位值连同阈值配置一起
// 交给 ReportPointValue，本引擎负责判定与落库，再由共享 Tracker（ThresholdSpec）完成
// 状态机判定、报警落库与通知（见 tracker.go）。
//
// # 判定策略（本引擎唯一的判断，其余都交给状态机）
//
// 只有「值可信」且「确实满足配置的条件」才算命中。以下情形一律**中性**
// （不调用状态机 = 既不报命中也不报正常，状态机原地冻结），沿用 alarm.PointEngine
// 的同一约定：
//
//	配置为空    未启用，或没有算子/操作数。采集侧已先行过滤（collector.PointThreshold.Inert），
//	            这里再挡一次是防御。
//	quality=0   读失败，此时 value 是占位串而不是测量结果。
//	            192（正常）与 128（ABB 的不确定，值可用）都照常参与比较。
//	类型不支持  时间类型（及未知 Kind）。**不猜** —— 见 driver/OperatorsForKind。
//	值/操作数认不出来  数值解析失败、布尔值不是 0/1/true/false。
//	            布尔尤其要小心：它的 "0"/"1" 是各插件 formatBool 的**实现约定、不是契约**，
//	            所以这里同时认 true/false，认不出的照样中性。
//
// 中性还有一个免费的好处：设备离线时 drv.Read 直接返 error、一条记录都不产生，
// 于是压根不会有信号上报 —— 状态机原地冻结。「与设备离线去重」不需要任何额外代码。
//
// # 记录与报警的刻意不对称
//
// 越限记录按**原始命中**写（同一段内节流），报警按**去抖后**报（连续 N 轮）。因此
// 1~2 轮的尖峰会出现在越限记录里但不产生报警 —— 数据是事实，报警是去抖后的策略。
//
// # 精度
//
// 数值经 float64 比较。uint64 超过 2^53 时会丢精度（PLC 值远低于此，接受）。
type ThresholdEngine struct {
	tr *Tracker // 共享状态机（ThresholdSpec）
	db *gorm.DB

	mu          sync.Mutex
	lastWrite   map[string]time.Time // pointID -> 本段最近一次记录写入时刻（节流）
	lastCleanup time.Time            // 最近一次惰性清理时刻

	// 可测试注入：0 表示用上面的默认常量。
	minWriteInterval time.Duration
	cleanupInterval  time.Duration
}

// NewThresholdEngine 创建点位阈值报警引擎（默认连续越限 DefaultConsecutiveFailures 轮判超限）。
// sink 为报警通知出口，传 nil 表示只落库不通知（单测/裁剪部署）。
func NewThresholdEngine(db *gorm.DB, sink NotifySink) *ThresholdEngine {
	return &ThresholdEngine{
		tr:               NewTracker(db, ThresholdSpec, sink),
		db:               db,
		lastWrite:        make(map[string]time.Time),
		minWriteInterval: thresholdRecordMinInterval,
		cleanupInterval:  thresholdRecordCleanupInterval,
	}
}

// ReportPointValue 实现 collector.PointValueSink。
// 由采集协程同步调用（每点位每轮一次，海量点位下即热点路径），内部持锁快速完成。
//
// pointID 为 device_address.id（状态机的键，也是报警行的 target_id）；
// pointName 为可读名（调用方已给的「点位名」）；deviceID 记进报警行供清理时反查。
func (e *ThresholdEngine) ReportPointValue(deviceID, deviceName, pointID, pointName, value, kind string,
	quality int, th collector.PointThreshold) {

	if th.Inert() {
		return // 防御：采集侧已过滤（见 collector.gatewayTask.reportPointValues）
	}
	if !driver.QualityUsable(quality) {
		return // 读失败：value 是占位串，不构成证据
	}

	breach, detail, bound, ok := evaluateCondition(value, kind, th.Op, th.Values)
	if !ok {
		return // 类型/值/操作数认不出来：不猜，保持中性
	}

	if breach {
		e.persist(deviceID, deviceName, pointID, pointName, value, kind, bound, th)
	} else {
		e.resetGate(pointID) // 本段结束：下一段的首次命中重新立即写记录
	}
	// 报警始终走共享状态机：连续 N 轮命中才 raise，一轮不命中即 recover（同断联/质量）。
	e.tr.ReportThreshold(pointID, deviceName+"."+pointName, detail, deviceID, !breach)
}

// evaluateCondition 判定采集值是否满足报警条件。
// 返回 (是否命中, 报警文案里的 detail, 命中的那一部分, 是否判定成功)。
// 第四个返回值为 false 表示**中性**：类型/值/操作数认不出来，调用方不得改动状态机。
//
// 按 Kind 分派 —— 三种类型要的是三种不同的比较，不能靠"能不能 ParseFloat"来区分
// （布尔值的 "1"、文本的 "9" 都能解析成功，那样一句文本就能触发报警）：
//
//	数值  range 是区间原语（v<min || v>max，任一侧空则不检查），其余是单次比较
//	布尔  eq/ne，两侧都归一化成真值再比
//	文本  eq/ne 精确比较（trim 后、大小写敏感），in 判集合成员
//
// detail 里的值用**驱动给的原始字符串**而不是重新格式化的浮点 —— 报警文案与实时页、
// 采集记录因此逐字节一致（避免把 45.2 渲染成 45.199999）。操作数没有对应的原始串，
// 用 FormatFloat 的最短往返表示（数值）或原样（文本）。
func evaluateCondition(value, kind, op string, values []string) (bool, string, string, bool) {
	switch {
	case driver.IsNumericKind(kind):
		return evaluateNumeric(value, op, values)
	case kind == driver.KindBool:
		return evaluateBool(value, op, values)
	case kind == driver.KindString:
		return evaluateString(value, op, values)
	default:
		// 时间类型与未知 Kind：不猜（见 driver/operatorsByKind 的说明）
		return false, "", "", false
	}
}

// evaluateNumeric 数值判定。操作数必须是可解析的数值，否则中性。
func evaluateNumeric(value, op string, values []string) (bool, string, string, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return false, "", "", false // 报的 kind 是数值但解析不出来：不猜
	}

	if op == driver.OpRange {
		// [min, max]；空串表示该侧不检查。缺操作数（len<2）时该侧同样视为不检查。
		var minS, maxS string
		if len(values) > 0 {
			minS = strings.TrimSpace(values[0])
		}
		if len(values) > 1 {
			maxS = strings.TrimSpace(values[1])
		}
		minV, minOK := parseOptionalFloat(minS)
		maxV, maxOK := parseOptionalFloat(maxS)
		if !minOK || !maxOK {
			return false, "", "", false // 操作数不可解析（校验本应拦住）
		}
		// 先判上限：两侧同时可命中时不可能（校验保证 min<max），顺序只为确定性
		if maxV != nil && v > *maxV {
			return true, fmt.Sprintf("%s > 上限 %s", value, formatNum(*maxV)), boundHigh, true
		}
		if minV != nil && v < *minV {
			return true, fmt.Sprintf("%s < 下限 %s", value, formatNum(*minV)), boundLow, true
		}
		return false, "", "", true
	}

	// 单操作数算子
	operand, ok := parseOptionalFloat(firstValue(values))
	if !ok || operand == nil {
		return false, "", "", false
	}
	x := *operand
	switch op {
	case driver.OpGt:
		return v > x, fmt.Sprintf("%s > %s", value, formatNum(x)), driver.OpGt, true
	case driver.OpGe:
		return v >= x, fmt.Sprintf("%s >= %s", value, formatNum(x)), driver.OpGe, true
	case driver.OpLt:
		return v < x, fmt.Sprintf("%s < %s", value, formatNum(x)), driver.OpLt, true
	case driver.OpLe:
		return v <= x, fmt.Sprintf("%s <= %s", value, formatNum(x)), driver.OpLe, true
	case driver.OpEq:
		return v == x, fmt.Sprintf("%s = %s", value, formatNum(x)), driver.OpEq, true
	case driver.OpNe:
		return v != x, fmt.Sprintf("%s != %s", value, formatNum(x)), driver.OpNe, true
	}
	return false, "", "", false // 算子与类型不匹配（校验本应拦住）
}

// evaluateBool 布尔判定。值与操作数都归一化成真值再比；任一侧认不出来即中性。
//
// "0"/"1" 是各插件 formatBool 的实现约定，不是跨插件契约，所以同时认 true/false
// （大小写不敏感）—— 见 ThresholdEngine 的说明。
func evaluateBool(value, op string, values []string) (bool, string, string, bool) {
	v, ok := parseBool(value)
	if !ok {
		return false, "", "", false
	}
	w, ok := parseBool(firstValue(values))
	if !ok {
		return false, "", "", false
	}
	shown := boolText(w)
	switch op {
	case driver.OpEq:
		return v == w, "值 = " + shown, driver.OpEq, true
	case driver.OpNe:
		return v != w, "值 != " + shown, driver.OpNe, true
	}
	return false, "", "", false
}

// evaluateString 文本判定。trim 后**大小写敏感**地比较 —— 故障码/状态字大小写有别。
func evaluateString(value, op string, values []string) (bool, string, string, bool) {
	v := strings.TrimSpace(value)
	switch op {
	case driver.OpEq, driver.OpNe:
		w := strings.TrimSpace(firstValue(values))
		if w == "" {
			return false, "", "", false // 空操作数认不出来（校验本应拦住）
		}
		if op == driver.OpEq {
			return v == w, "值 = " + w, driver.OpEq, true
		}
		return v != w, "值 != " + w, driver.OpNe, true
	case driver.OpIn:
		set := make([]string, 0, len(values))
		for _, raw := range values {
			if s := strings.TrimSpace(raw); s != "" {
				set = append(set, s)
			}
		}
		if len(set) == 0 {
			return false, "", "", false
		}
		hit := false
		for _, s := range set {
			if v == s {
				hit = true
				break
			}
		}
		return hit, "值 属于 {" + strings.Join(set, ", ") + "}", driver.OpIn, true
	}
	return false, "", "", false
}

// parseOptionalFloat 解析可空数值（空串 → nil, true；解析失败 → nil, false）。
func parseOptionalFloat(s string) (*float64, bool) {
	if s == "" {
		return nil, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, false
	}
	return &v, true
}

// parseBool 归一化布尔值：接受 1/0/true/false（大小写不敏感）。
// 认不出来返回 false —— 调用方据此作中性处理，绝不猜。
func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	}
	return false, false
}

// boolText 布尔值在报警文案里的呈现（与 parseBool 接受的字面量一致）。
func boolText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// firstValue 取单操作数算子的操作数；缺失返回空串。
func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// formatNum 把数值操作数格式化成最短往返表示（如 40 而不是 40.000000）。
func formatNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// persist 落一条越限记录（同一连续越限段内按 minWriteInterval 节流）。
//
// **段的判定靠"上一轮是否越限"**：每轮正常值都会调 resetGate 清掉节流时刻，
// 因此下一段的首次越限必然立即写入 —— lastWrite 只在一段之内起作用。
func (e *ThresholdEngine) persist(deviceID, deviceName, pointID, pointName, value, kind, bound string,
	th collector.PointThreshold) {

	now := time.Now()

	// 节流判定与清理时钟都在锁内完成，DB 操作放到锁外 ——
	// 在锁内等唯一的 SQLite 连接会把采集协程串行化（见 thresholdRecordMinInterval 的注释）。
	e.mu.Lock()
	interval := e.minWriteInterval
	if interval <= 0 {
		interval = thresholdRecordMinInterval
	}
	if last, ok := e.lastWrite[pointID]; ok && now.Sub(last) < interval {
		e.mu.Unlock()
		return
	}
	e.lastWrite[pointID] = now

	cleanupEvery := e.cleanupInterval
	if cleanupEvery <= 0 {
		cleanupEvery = thresholdRecordCleanupInterval
	}
	needCleanup := e.lastCleanup.IsZero() || now.Sub(e.lastCleanup) >= cleanupEvery
	if needCleanup {
		e.lastCleanup = now
	}
	e.mu.Unlock()

	// 操作数快照重新序列化回 JSON：存的是**当时生效**的条件，
	// 运维改了配置之后历史记录仍可自证（这也是把它做成快照而不是外键的原因）。
	snapshot, err := json.Marshal(th.Values)
	if err != nil {
		// 理论上不会发生（操作数是 []string）。真发生了也不该让采集失败：记空快照。
		logger.Warn("alarm: marshal threshold values for point %s failed: %v", pointID, err)
		snapshot = []byte("[]")
	}

	rec := &po.PointThresholdRecord{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		AddressID:  pointID,
		// 只存点位自身名，不带设备名前缀 —— device_name 是独立一列，
		// 前缀只会在列表页显示成「1#炉 1#炉.温度」。（报警行的 target_name 另有约定，
		// 那处用 deviceName.pointName 拼。）
		AddressName:    pointName,
		Value:          value,
		Kind:           kind,
		Bound:          bound,
		ThresholdOp:    th.Op,
		ThresholdValue: string(snapshot),
		OccurredAt:     now.Format("2006-01-02 15:04:05"),
		CreatedAt:      now.Format("2006-01-02 15:04:05"),
	}
	if err := e.db.Create(rec).Error; err != nil {
		// 记录没落不影响报警正确性，采集更不能因此失败 —— 只记日志。
		logger.Error("alarm: create threshold record for point %s failed: %v", pointID, err)
	} else if needCleanup {
		e.cleanup()
	}
}

// resetGate 结束一个连续越限段：清掉该点位的节流时刻，使下一段的首次越限立即写记录。
func (e *ThresholdEngine) resetGate(pointID string) {
	e.mu.Lock()
	delete(e.lastWrite, pointID)
	e.mu.Unlock()
}

// cleanup 按保留时长与行数上限清理越限记录（惰性，由 persist 按间隔触发）。
// 失败只记日志：清理是写入的收尾，不该让它影响已经完成的报警。
func (e *ThresholdEngine) cleanup() {
	if err := e.db.Where("occurred_at < ?", time.Now().Add(-thresholdRecordRetention).Format("2006-01-02 15:04:05")).
		Delete(&po.PointThresholdRecord{}).Error; err != nil {
		logger.Warn("alarm: cleanup threshold records by age failed: %v", err)
		return
	}

	var total int64
	if err := e.db.Model(&po.PointThresholdRecord{}).Count(&total).Error; err != nil {
		logger.Warn("alarm: count threshold records failed: %v", err)
		return
	}
	if excess := total - thresholdRecordMaxRows; excess > 0 {
		if err := e.db.Where("id IN (?)",
			e.db.Model(&po.PointThresholdRecord{}).Select("id").
				Order("occurred_at ASC").Limit(int(excess))).
			Delete(&po.PointThresholdRecord{}).Error; err != nil {
			logger.Warn("alarm: cleanup threshold records by row cap failed: %v", err)
		}
	}
}
