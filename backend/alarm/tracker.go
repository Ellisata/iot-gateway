// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package alarm

import (
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
)

// 目标类型（导出供 service 层查询复用）。
const (
	TypeDevice  = "device"  // 设备
	TypeChannel = "channel" // 推送通道
	TypePoint   = "point"   // 采集点位（点位质量报警）
	// TypePointThreshold 点位阈值报警。**与 TypePoint 刻意分开**：Tracker 的落库键是
	// (target_id, target_type, status)，不含 alarm_type（见 raise/recover/touch 三处查询），
	// 而质量报警与阈值报警的 target_id 都是 device_address.id —— 共用 point 会让两者
	// 互相覆盖：一方报警时另一方只会刷新时间戳（漏报），任一方恢复时把对方的活跃
	// 报警一并清掉（误报恢复）。分成两个 target_type 后这条不变量原样成立。
	TypePointThreshold = "point_threshold"
)

// 报警记录常量（导出供 service 层查询复用）。
const (
	TypeOffline   = "offline"   // 断联报警
	TypeQuality   = "quality"   // 采集质量报警（点位专有）
	TypeThreshold = "threshold" // 超限报警（点位专有）
	TypeRecover   = "recover"   // 恢复记录
	StatusActive  = "active"    // 未恢复（当前在线状态反查依据）
	StatusCleared = "cleared"   // 已恢复
)

// Spec 一类报警目标的描述：落库类型 + 文案措辞。
//
// 状态机本身（边沿判定 / 去抖 / 节流 / 落库 / 通知）对全部目标一视同仁，
// 目标之间的差异**只有**本结构这几项。新增一类目标（point 就是这么加的）
// 只需多写一份 Spec，不必复制 raise/recover/touch 三段落库逻辑。
type Spec struct {
	TargetType  string // 落库的 target_type：device / channel / point / point_threshold
	AlarmType   string // 报警行的 alarm_type：offline / quality / threshold
	Label       string // 文案主语：设备 / 推送通道 / 点位
	RaiseVerb   string // 报警文案谓语：断联 / 采集质量异常 / 超限
	RecoverVerb string // 恢复文案谓语：恢复通信 / 恢复采集 / 恢复
}

// 四类目标的 Spec。导出是为了让 notify 的「测试发送」预览复用同一套措辞 ——
// 那条路走的是构造出来的假事件，文案必须与真实报警一字不差。
var (
	DeviceSpec  = Spec{TypeDevice, TypeOffline, "设备", "断联", "恢复通信"}
	ChannelSpec = Spec{TypeChannel, TypeOffline, "推送通道", "断联", "恢复通信"}
	PointSpec   = Spec{TypePoint, TypeQuality, "点位", "采集质量异常", "恢复采集"}
	// ThresholdSpec 点位报警条件命中。target_type 用 TypePointThreshold 而非 TypePoint 的
	// 理由见 TypePointThreshold 的说明 —— 简单说：Tracker 的落库键不含 alarm_type，
	// 共用 point 会让质量报警与条件报警互相覆盖。
	//
	// 谓语用通用的「告警」而不是「超限」：条件现在覆盖数值区间、布尔状态与文本集合三类，
	// 「点位 X 超限（值 = FAULT）」对文本条件是**事实错误**，会把值班人员引向错误的方向。
	// 具体是什么条件由括号里的 detail 说清。
	ThresholdSpec = Spec{TypePointThreshold, TypeThreshold, "点位", "告警", "恢复"}
)

// Content 生成报警文案，如「设备 dev1 断联」「点位 1#炉.温度 采集质量异常（地址无效）」。
// detail 为空时不加括号（断联报警恒为空，它没有比"连不上"更细的原因）。
func (s Spec) Content(targetName, detail string) string {
	c := fmt.Sprintf("%s %s %s", s.Label, targetName, s.RaiseVerb)
	if detail != "" {
		c += "（" + detail + "）"
	}
	return c
}

// RecoverContent 生成恢复文案，如「设备 dev1 恢复通信」。
func (s Spec) RecoverContent(targetName string) string {
	return fmt.Sprintf("%s %s %s", s.Label, targetName, s.RecoverVerb)
}

const (
	// DefaultConsecutiveFailures 连续失败 N 次判定离线（去抖，过滤单次脉冲）。
	// 默认 3，可通过 Config 调整（推送通道巡检用 2）。
	DefaultConsecutiveFailures = 3
	// defaultLevel 默认报警级别。
	defaultLevel = "warning"
	// touchMinInterval 已离线目标持续失败时，last_occur_time 刷新最小间隔。
	// 避免每轮信号都写库（高频信号源放大 SQLite 写入）。
	touchMinInterval = 5 * time.Second
)

// Config 断联报警判定配置。
type Config struct {
	// ConsecutiveFailures 连续失败次数阈值，达到即判离线；<=0 时用默认值。
	ConsecutiveFailures int
	// Level 报警级别。
	Level string
}

// Tracker 报警状态机核心，对单个目标做边沿触发判定并落库。
//
// Report 由信号源驱动（设备=采集轮询成败；推送通道=连接状态巡检；
// 点位=每轮采集到的质量与成因；点位阈值=每轮采集值与配置边界），
// 只在正常↔异常的边沿写/清 alarm 表，
// 中间持续失败节流刷新 last_occur_time。同一目标同时最多一条 status='active' 的记录。
type Tracker struct {
	db   *gorm.DB
	cfg  Config
	spec Spec // 目标类型与文案（见 Spec）

	sink NotifySink // 报警通知出口，可为 nil（不通知）

	mu     sync.Mutex
	states map[string]*devState // targetID -> 状态机
}

// devState 单个目标的状态机。
type devState struct {
	normal    bool      // 当前判定为正常
	failCount int       // 连续失败次数
	lastTouch time.Time // 最近一次刷新 last_occur_time 的时间（节流）
	// reconciled 本状态机是否已经接管了库中可能残留的 active 行（见 report）。
	//
	// 进程重启后状态机是新造的（normal 初值 true），而库里可能残留上一进程写下的
	// active 报警：故障若恰好在重启窗口内结束，那次恢复边沿已经丢失，那行再没有
	// 任何信号来清它，目标会被永久判为离线。只有重启后的**首个成功信号**需要核对，
	// 之后本字段为 true，采集热点路径零额外查询。
	reconciled bool
}

// NewTracker 创建报警状态机（默认连续失败 DefaultConsecutiveFailures 次判异常）。
// spec 描述目标类型与文案（DeviceSpec / ChannelSpec / PointSpec）；
// sink 为报警通知出口，传 nil 表示只落库不通知（单测/裁剪部署）。
func NewTracker(db *gorm.DB, spec Spec, sink NotifySink) *Tracker {
	return &Tracker{
		db:     db,
		cfg:    Config{ConsecutiveFailures: DefaultConsecutiveFailures, Level: defaultLevel},
		spec:   spec,
		sink:   sink,
		states: make(map[string]*devState),
	}
}

// threshold 返回生效的连续失败阈值。
func (t *Tracker) threshold() int {
	if t.cfg.ConsecutiveFailures <= 0 {
		return DefaultConsecutiveFailures
	}
	return t.cfg.ConsecutiveFailures
}

// Report 上报目标本轮信号是否正常（true=正常，false=失败）。
// 由信号源同步调用，内部持锁快速完成状态迁移与落库，不阻塞调用方。
// 设备/推送通道用它；点位质量报警用 ReportQuality（多两项点位专有的信息）。
func (t *Tracker) Report(targetID, targetName string, ok bool) {
	t.report(targetID, targetName, "", "", ok)
}

// ReportQuality 上报一个点位本轮的采集质量（点位质量报警专用）。
//
// 与 Report 的区别只有两处：报警文案附带失败原因（detail），报警行记下点位
// 所属设备（deviceID）。后者是为「删除/停用设备时连同清掉其点位的活跃报警」
// 反查用的 —— 点位的 target_id 是点位自身，看不回设备。
//
// 调用方必须**已经定性**：只对 address / parse 这类「设备是好的、点位本身
// 有问题」的成因报 false。成因不明（comms / 空串）时不要调用本方法 ——
// 不调用就是"中性"，状态机保持原状。见 PointEngine 的策略说明。
func (t *Tracker) ReportQuality(pointID, pointName, detail, deviceID string, ok bool) {
	t.report(pointID, pointName, detail, deviceID, ok)
}

// ReportThreshold 上报一个点位本轮是否越限（点位阈值报警专用）。
//
// 与 ReportQuality 的形状完全一致，差别只在语义：detail 是越限值与越过的边界
// （如「45.2 > 上限 40」），deviceID 同样记进报警行供删除/停用反查。
//
// 调用方必须**已经定性**：只对确实越限/确实在范围内的点位上调用。配置为空、
// 质量不可用、值解析不出来等情形都**不要调用**本方法 —— 不调用就是"中性"，
// 状态机保持原状（见 ThresholdEngine 的判定顺序）。
func (t *Tracker) ReportThreshold(pointID, pointName, detail, deviceID string, ok bool) {
	t.report(pointID, pointName, detail, deviceID, ok)
}

// report 状态机主体。detail 只进报警文案，deviceID 只进报警行的 device_id 列。
func (t *Tracker) report(targetID, targetName, detail, deviceID string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.states[targetID]
	if st == nil {
		// 新目标（含网关重启/热刷新后）默认视为正常：无失败证据不报警，
		// 需连续失败达到阈值才判异常，避免首轮成功误写恢复记录。
		// 重启后库中可能残留的 active 行由下面的 reconciled 分支兜底。
		st = &devState{normal: true}
		t.states[targetID] = st
	}

	if ok {
		// 一次成功即恢复（现场恢复是硬事实，无需去抖）。
		st.failCount = 0
		switch {
		case !st.normal:
			st.normal = true
			t.recoverLocked(targetID, targetName, deviceID)
			st.reconciled = true // 该行已归本状态机管理
		case !st.reconciled:
			// 重启后的首个成功信号：状态机没有故障记忆（normal 初值为 true），
			// 但库里可能残留上一进程写下的 active 行 —— 故障若在重启窗口内结束，
			// 那次恢复边沿已经丢失，那行再没有任何信号来清它，目标就会永久挂在
			// "未恢复"上（AlarmService.IsDeviceOnline 与设备列表都读这张表）。
			//
			// 采集已成功 = 事实上已恢复，清掉即可。**刻意不写 recover 行、不通知**：
			// 真实恢复时刻不可知（可能发生在网关停机期间），伪造一条"现在恢复"
			// 的事件会污染历史与值班判断 —— 与 ClearActive 是同一条理由。
			//
			// 设备若其实**仍在故障**，首个信号是失败，走下面的失败分支：raiseLocked
			// 找到既有 active 行只刷新时间戳，所以故障段的时长原样保留、不会被截断。
			t.clearActiveRow(targetID)
			st.reconciled = true
		}
		return
	}

	st.failCount++
	if !st.normal {
		// 已判定异常：只刷新最近失败时间，不重复报警。
		t.touchLastOccurLocked(targetID, st)
		return
	}
	if st.failCount >= t.threshold() {
		// 正常 -> 异常 边沿，落一条 active 报警。
		st.normal = false
		st.reconciled = true // 该行已归本状态机管理，恢复时由 recoverLocked 清除
		t.raiseLocked(targetID, targetName, detail, deviceID)
	}
}

// Reset 移除目标的状态机（如通道重启/停止、热刷新），下次信号重新累计。
// 不触碰库中已存在的报警记录。
func (t *Tracker) Reset(targetID string) {
	t.mu.Lock()
	delete(t.states, targetID)
	t.mu.Unlock()
}

// ClearActive 重置状态机并清除目标的 active 报警（历史保留）。
// 用于目标被运营停用/删除时，或巡检发现目标由运行转停止时。
//
// 这里刻意不投递通知：目标被停用/删除是运营操作，属于行政取消，不是「恢复」，
// 往群里发恢复通知会误导值班人员。
func (t *Tracker) ClearActive(targetID string) {
	t.Reset(targetID)
	t.clearActiveRow(targetID)
}

// clearActiveRow 把目标的 active 报警置为 cleared（历史保留），不写 recover 行、不通知。
// 名称不带 Locked 后缀：它不要求调用方持 t.mu（ClearActive 与 report 两条路径共用）。
func (t *Tracker) clearActiveRow(targetID string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := t.db.Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.spec.TargetType, StatusActive).
		Updates(map[string]interface{}{
			"status":     StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error; err != nil {
		logger.Error("alarm: clear active alarm for %s %s failed: %v", t.spec.TargetType, targetID, err)
	}
}

// raiseLocked 落库报警（调用方持 t.mu）。
// 目标已有 active 记录时仅刷新时间戳（异常场景防御），否则新建。
func (t *Tracker) raiseLocked(targetID, targetName, detail, deviceID string) {
	now := time.Now().Format("2006-01-02 15:04:05")

	var existing po.Alarm
	if err := t.db.Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.spec.TargetType, StatusActive).
		Order("created_at DESC").First(&existing).Error; err == nil {
		// 理论上不出现：状态机已 normal 但库中存在 active 记录
		// （多进程/重启等异常场景）。刷新时间戳即可，不重复造记录。
		if err := t.db.Model(&po.Alarm{}).Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"last_occur_time": now,
				"updated_at":      now,
			}).Error; err != nil {
			logger.Error("alarm: refresh %s alarm for %s %s failed: %v", t.spec.AlarmType, t.spec.TargetType, targetName, err)
		}
		return
	}

	a := &po.Alarm{
		TargetID:       targetID,
		TargetName:     targetName,
		TargetType:     t.spec.TargetType,
		AlarmType:      t.spec.AlarmType,
		DeviceID:       deviceID,
		Level:          t.cfg.Level,
		Content:        t.spec.Content(targetName, detail),
		Status:         StatusActive,
		FirstOccurTime: now,
		LastOccurTime:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := t.db.Create(a).Error; err != nil {
		logger.Error("alarm: create %s alarm for %s %s failed: %v", t.spec.AlarmType, t.spec.TargetType, targetName, err)
		return // 落库失败不通知：保持「通知 ⇔ 报警行已写入」这一不变式
	}
	t.notifyLocked(a)
}

// recoverLocked 目标恢复：清 active 报警并写一条恢复历史（调用方持 t.mu）。
// deviceID 与报警行同源（点位目标才有值），恢复行也记一份 —— 否则只有恢复行
// 丢了设备关联，按 device_id 分组的历史查询会把它甩到空桶里。
func (t *Tracker) recoverLocked(targetID, targetName, deviceID string) {
	now := time.Now().Format("2006-01-02 15:04:05")

	if err := t.db.Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.spec.TargetType, StatusActive).
		Updates(map[string]interface{}{
			"status":     StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error; err != nil {
		logger.Error("alarm: clear active alarm for %s %s failed: %v", t.spec.TargetType, targetName, err)
	}

	rec := &po.Alarm{
		TargetID:       targetID,
		TargetName:     targetName,
		TargetType:     t.spec.TargetType,
		AlarmType:      TypeRecover,
		DeviceID:       deviceID,
		Level:          t.cfg.Level,
		Content:        t.spec.RecoverContent(targetName),
		Status:         StatusCleared,
		FirstOccurTime: now,
		LastOccurTime:  now,
		ClearTime:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := t.db.Create(rec).Error; err != nil {
		logger.Error("alarm: create recover record for %s %s failed: %v", t.spec.TargetType, targetName, err)
		return // 同上：没写进库就不通知
	}
	t.notifyLocked(rec)
}

// touchLastOccurLocked 已判定异常的目标持续失败时刷新 active 行的 last_occur_time（节流）。
// 调用方持 t.mu。
//
// 这里刻意不投递通知：它只是时间戳刷新，不是状态边沿，通知会按节流周期刷屏。
// 通知只在正常↔异常的边沿（raiseLocked / recoverLocked）发出。
func (t *Tracker) touchLastOccurLocked(targetID string, st *devState) {
	now := time.Now()
	if !st.lastTouch.IsZero() && now.Sub(st.lastTouch) < touchMinInterval {
		return
	}
	st.lastTouch = now
	if err := t.db.Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.spec.TargetType, StatusActive).
		Update("last_occur_time", now.Format("2006-01-02 15:04:05")).Error; err != nil {
		logger.Error("alarm: touch last_occur_time for %s %s failed: %v", t.spec.TargetType, targetID, err)
	}
}
