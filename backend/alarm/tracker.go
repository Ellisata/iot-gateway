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
)

// 报警记录常量（导出供 service 层查询复用）。
const (
	TypeOffline   = "offline" // 断联报警
	TypeRecover   = "recover" // 恢复记录
	StatusActive  = "active"  // 未恢复（当前在线状态反查依据）
	StatusCleared = "cleared" // 已恢复
)

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

// Tracker 断联报警状态机核心，对单个目标（设备/推送通道）的在线状态做边沿触发判定并落库。
//
// Report 由信号源驱动（设备=采集轮询成败；推送通道=连接状态巡检），
// 只在 ONLINE↔OFFLINE 边沿写/清 alarm 表，中间持续失败节流刷新 last_occur_time。
// 同一目标同时最多一条 status='active' 的离线记录。
type Tracker struct {
	db         *gorm.DB
	cfg        Config
	targetType string // TypeDevice / TypeChannel
	label      string // 文案前缀：设备 / 推送通道

	mu     sync.Mutex
	states map[string]*devState // targetID -> 状态机
}

// devState 单台目标的在线状态机。
type devState struct {
	online    bool      // 当前判定为在线
	failCount int       // 连续失败次数
	lastTouch time.Time // 最近一次刷新 last_occur_time 的时间（节流）
}

// NewTracker 创建断联报警状态机（默认连续失败 3 次判离线）。
// targetType 为 TypeDevice/TypeChannel，label 用于报警文案（"设备"/"推送通道"）。
func NewTracker(db *gorm.DB, targetType, label string) *Tracker {
	return &Tracker{
		db:         db,
		cfg:        Config{ConsecutiveFailures: DefaultConsecutiveFailures, Level: defaultLevel},
		targetType: targetType,
		label:      label,
		states:     make(map[string]*devState),
	}
}

// threshold 返回生效的连续失败阈值。
func (t *Tracker) threshold() int {
	if t.cfg.ConsecutiveFailures <= 0 {
		return DefaultConsecutiveFailures
	}
	return t.cfg.ConsecutiveFailures
}

// Report 上报目标本轮信号是否成功（true=在线/可达，false=失败/断联）。
// 由信号源同步调用，内部持锁快速完成状态迁移与落库，不阻塞调用方。
func (t *Tracker) Report(targetID, targetName string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	st := t.states[targetID]
	if st == nil {
		// 新目标（含网关重启/热刷新后）默认视为在线：无失败证据不报警，
		// 需连续失败达到阈值才判离线，避免首轮成功误写恢复记录。
		st = &devState{online: true}
		t.states[targetID] = st
	}

	if ok {
		// 一次成功即恢复（现场恢复是硬事实，无需去抖）。
		st.failCount = 0
		if !st.online {
			st.online = true
			t.recoverLocked(targetID, targetName)
		}
		return
	}

	st.failCount++
	if !st.online {
		// 已判定离线：只刷新最近失败时间，不重复报警。
		t.touchLastOccurLocked(targetID, st)
		return
	}
	if st.failCount >= t.threshold() {
		// ONLINE -> OFFLINE 边沿，落一条 active 离线报警。
		st.online = false
		t.raiseOfflineLocked(targetID, targetName)
	}
}

// Reset 移除目标的状态机（如通道重启/停止、热刷新），下次信号重新累计。
// 不触碰库中已存在的报警记录。
func (t *Tracker) Reset(targetID string) {
	t.mu.Lock()
	delete(t.states, targetID)
	t.mu.Unlock()
}

// ClearActive 重置状态机并清除目标的 active 离线报警（历史保留）。
// 用于目标被运营停用/删除时，或巡检发现目标由运行转停止时。
func (t *Tracker) ClearActive(targetID string) {
	t.Reset(targetID)
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := t.db.Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.targetType, StatusActive).
		Updates(map[string]interface{}{
			"status":     StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error; err != nil {
		logger.Error("alarm: clear active alarm for %s %s failed: %v", t.targetType, targetID, err)
	}
}

// raiseOfflineLocked 落库离线报警（调用方持 t.mu）。
// 目标已有 active 记录时仅刷新时间戳（异常场景防御），否则新建。
func (t *Tracker) raiseOfflineLocked(targetID, targetName string) {
	now := time.Now().Format("2006-01-02 15:04:05")

	var existing po.Alarm
	if err := t.db.Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.targetType, StatusActive).
		Order("created_at DESC").First(&existing).Error; err == nil {
		// 理论上不出现：状态机已 online 但库中存在 active 离线记录
		// （多进程/重启等异常场景）。刷新时间戳即可，不重复造记录。
		if err := t.db.Model(&po.Alarm{}).Where("id = ?", existing.ID).
			Updates(map[string]interface{}{
				"last_occur_time": now,
				"updated_at":      now,
			}).Error; err != nil {
			logger.Error("alarm: refresh offline alarm for %s %s failed: %v", t.targetType, targetName, err)
		}
		return
	}

	a := &po.Alarm{
		TargetID:       targetID,
		TargetName:     targetName,
		TargetType:     t.targetType,
		AlarmType:      TypeOffline,
		Level:          t.cfg.Level,
		Content:        fmt.Sprintf("%s %s 断联", t.label, targetName),
		Status:         StatusActive,
		FirstOccurTime: now,
		LastOccurTime:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := t.db.Create(a).Error; err != nil {
		logger.Error("alarm: create offline alarm for %s %s failed: %v", t.targetType, targetName, err)
	}
}

// recoverLocked 目标恢复：清 active 离线报警并写一条恢复历史（调用方持 t.mu）。
func (t *Tracker) recoverLocked(targetID, targetName string) {
	now := time.Now().Format("2006-01-02 15:04:05")

	if err := t.db.Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.targetType, StatusActive).
		Updates(map[string]interface{}{
			"status":     StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error; err != nil {
		logger.Error("alarm: clear offline alarm for %s %s failed: %v", t.targetType, targetName, err)
	}

	rec := &po.Alarm{
		TargetID:       targetID,
		TargetName:     targetName,
		TargetType:     t.targetType,
		AlarmType:      TypeRecover,
		Level:          t.cfg.Level,
		Content:        fmt.Sprintf("%s %s 恢复通信", t.label, targetName),
		Status:         StatusCleared,
		FirstOccurTime: now,
		LastOccurTime:  now,
		ClearTime:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := t.db.Create(rec).Error; err != nil {
		logger.Error("alarm: create recover record for %s %s failed: %v", t.targetType, targetName, err)
	}
}

// touchLastOccurLocked 已离线目标持续失败时刷新 active 行的 last_occur_time（节流）。
// 调用方持 t.mu。
func (t *Tracker) touchLastOccurLocked(targetID string, st *devState) {
	now := time.Now()
	if !st.lastTouch.IsZero() && now.Sub(st.lastTouch) < touchMinInterval {
		return
	}
	st.lastTouch = now
	if err := t.db.Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, t.targetType, StatusActive).
		Update("last_occur_time", now.Format("2006-01-02 15:04:05")).Error; err != nil {
		logger.Error("alarm: touch last_occur_time for %s %s failed: %v", t.targetType, targetID, err)
	}
}
