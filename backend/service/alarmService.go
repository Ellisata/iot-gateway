// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/appError"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/logger"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/response"
)

// AlarmService 断联报警查询与清理服务。
// 报警记录的生成由 alarm.Tracker（采集侧设备引擎 / 巡检侧通道监控）负责，
// 本服务只读查询 + 目标停用/删除时的清理。
type AlarmService struct {
	sqliteDB *gorm.DB
}

// NewAlarmService 创建报警服务
func NewAlarmService(sqliteDB *gorm.DB) *AlarmService {
	return &AlarmService{sqliteDB: sqliteDB}
}

// ListActiveTargets 当前活跃（未恢复）的离线报警，即离线目标列表。
// targetType 传 alarm.TypeDevice/TypeChannel 仅取一类，空串取全部。
// 按首次发生时间倒序返回。
func (s *AlarmService) ListActiveTargets(ctx context.Context, targetType string) ([]vo.AlarmVO, error) {
	q := s.sqliteDB.WithContext(ctx).
		Where("status = ? AND alarm_type = ?", alarm.StatusActive, alarm.TypeOffline)
	if targetType != "" {
		q = q.Where("target_type = ?", targetType)
	}
	var rows []po.Alarm
	if err := q.Order("first_occur_time DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return toAlarmVOs(rows), nil
}

// ListActiveAlarms 当前活跃离线报警（设备 + 通道），= 离线目标列表。
func (s *AlarmService) ListActiveAlarms(ctx context.Context) ([]vo.AlarmVO, error) {
	return s.ListActiveTargets(ctx, "")
}

// PageAlarms 报警历史分页查询（目标名 / 类型 / 状态过滤）。
func (s *AlarmService) PageAlarms(ctx context.Context, req *dto.PageAlarmDTO) (*response.PageVo[vo.AlarmVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.Alarm{})

	if req.TargetName != "" {
		db = db.Where("target_name LIKE ?", "%"+req.TargetName+"%")
	}
	if req.TargetType != "" {
		db = db.Where("target_type = ?", req.TargetType)
	}
	if req.AlarmType != "" {
		db = db.Where("alarm_type = ?", req.AlarmType)
	}
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []po.Alarm
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("first_occur_time DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	return &response.PageVo[vo.AlarmVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: toAlarmVOs(rows),
	}, nil
}

// PageThresholdRecords 越限记录分页查询（设备 / 点位名过滤）。
//
// 数据由 alarm.ThresholdEngine 写入（每个越限样本一条，同一连续越限段内节流），
// 与 alarm 表是两件事：报警是去抖后的状态机，本表是原始越限的留证。
func (s *AlarmService) PageThresholdRecords(ctx context.Context, req *dto.PageThresholdRecordDTO) (*response.PageVo[vo.PointThresholdRecordVO], error) {
	db := s.sqliteDB.WithContext(ctx).Model(&po.PointThresholdRecord{})

	if req.DeviceID != "" {
		db = db.Where("device_id = ?", req.DeviceID)
	}
	if req.Name != "" {
		db = db.Where("address_name LIKE ?", "%"+req.Name+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var rows []po.PointThresholdRecord
	offset := (req.Page - 1) * req.Size
	if err := db.Offset(offset).Limit(req.Size).Order("occurred_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	return &response.PageVo[vo.PointThresholdRecordVO]{
		Page:    req.Page,
		Size:    req.Size,
		Total:   total,
		Records: toThresholdRecordVOs(rows),
	}, nil
}

// IsDeviceOnline 判断设备当前是否在线（无该设备 active 离线报警视为在线）。
// 查询失败保守视为在线，避免误报。
func (s *AlarmService) IsDeviceOnline(ctx context.Context, deviceID string) bool {
	var n int64
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?",
			deviceID, alarm.TypeDevice, alarm.StatusActive).
		Count(&n).Error; err != nil {
		return true
	}
	return n == 0
}

// ClearDeviceAlarm 设备停用/删除时清除其活跃报警（历史记录保留）。
//
// 一条语句清三类：设备自身的断联报警，以及**该设备下全部点位的质量报警与超限报警**。
// 后者必须一起清 —— 采集停了（设备停用/没了驱动/点位没了），再没有任何信号会来
// 恢复它们的活跃报警，不清就会永久挂在"未恢复"上。
//
// 点位报警的 target_id 是点位自身，看不回设备，故靠 device_id 列反查：
// 删除设备时点位行已在同一事务里删掉，事后再按 device_id 找点位是找不到的。
func (s *AlarmService) ClearDeviceAlarm(ctx context.Context, deviceID string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	return s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Where("status = ?", alarm.StatusActive).
		Where(s.sqliteDB.Where("target_id = ? AND target_type = ?", deviceID, alarm.TypeDevice).
			Or("device_id = ? AND target_type = ?", deviceID, alarm.TypePoint).
			Or("device_id = ? AND target_type = ?", deviceID, alarm.TypePointThreshold)).
		Updates(map[string]interface{}{
			"status":     alarm.StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error
}

// ClearChannelAlarm 推送通道停用/删除时清除其活跃报警（历史记录保留）。
func (s *AlarmService) ClearChannelAlarm(ctx context.Context, channelID string) error {
	return s.clearTargetAlarm(ctx, channelID, alarm.TypeChannel)
}

// ClearPointAlarm 点位被删除时清除其活跃报警（历史记录保留），质量与超限两类都清。
// 理由同 ClearDeviceAlarm：点位没了就不会再有信号来恢复它们。
//
// 两类一起清是正确的：点位都没了，它挂着哪一类报警都是永久"未恢复"。
func (s *AlarmService) ClearPointAlarm(ctx context.Context, addressID string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	return s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Where("target_id = ? AND target_type IN ? AND status = ?",
			addressID, []string{alarm.TypePoint, alarm.TypePointThreshold}, alarm.StatusActive).
		Updates(map[string]interface{}{
			"status":     alarm.StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error
}

// ClearPointThresholdAlarm 只清某点位的活跃**超限**报警（历史记录保留）。
//
// 用于运维把点位阈值改成空配置（未启用 / 上限下限都清空）时：采集侧对空配置不再发
// 任何信号（见 collector.PointThreshold.Inert），状态机原地冻结，活跃报警若不显式清掉
// 就永久挂在"未恢复"上。
//
// 刻意**不动**同一点位的质量报警 —— 改阈值不该顺手清掉一个无关的采集质量故障。
func (s *AlarmService) ClearPointThresholdAlarm(ctx context.Context, addressID string) error {
	return s.clearTargetAlarm(ctx, addressID, alarm.TypePointThreshold)
}

// clearTargetAlarm 清除指定类型目标的活跃报警（历史记录保留）。
func (s *AlarmService) clearTargetAlarm(ctx context.Context, targetID, targetType string) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	return s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Where("target_id = ? AND target_type = ? AND status = ?", targetID, targetType, alarm.StatusActive).
		Updates(map[string]interface{}{
			"status":     alarm.StatusCleared,
			"clear_time": now,
			"updated_at": now,
		}).Error
}

// toAlarmVOs 报警 PO -> VO
func toAlarmVOs(rows []po.Alarm) []vo.AlarmVO {
	vos := make([]vo.AlarmVO, len(rows))
	for i, a := range rows {
		vos[i] = vo.AlarmVO{
			ID:             a.ID,
			TargetID:       a.TargetID,
			TargetName:     a.TargetName,
			TargetType:     a.TargetType,
			AlarmType:      a.AlarmType,
			AlarmTypeName:  alarmTypeName(a.AlarmType),
			Level:          a.Level,
			Content:        a.Content,
			Status:         a.Status,
			StatusName:     alarmStatusName(a.Status),
			FirstOccurTime: a.FirstOccurTime,
			LastOccurTime:  a.LastOccurTime,
			ClearTime:      a.ClearTime,
			CreatedAt:      a.CreatedAt,
			UpdatedAt:      a.UpdatedAt,
		}
	}
	return vos
}

// toThresholdRecordVOs 越限记录 PO -> VO
func toThresholdRecordVOs(rows []po.PointThresholdRecord) []vo.PointThresholdRecordVO {
	vos := make([]vo.PointThresholdRecordVO, len(rows))
	for i, r := range rows {
		vos[i] = vo.PointThresholdRecordVO{
			ID:             r.ID,
			DeviceID:       r.DeviceID,
			DeviceName:     r.DeviceName,
			AddressID:      r.AddressID,
			AddressName:    r.AddressName,
			Value:          r.Value,
			Kind:           r.Kind,
			Bound:          r.Bound,
			BoundName:      thresholdBoundName(r.Bound),
			ThresholdOp:    r.ThresholdOp,
			ThresholdValue: r.ThresholdValue,
			OccurredAt:     r.OccurredAt,
		}
	}
	return vos
}

// thresholdBoundName 命中的条件部分的中文说明。
// range 命中哪一侧是 high/low，其余情况下 bound 就是算子名本身。
func thresholdBoundName(b string) string {
	switch b {
	case "high":
		return "超上限"
	case "low":
		return "低于下限"
	case driver.OpGt:
		return "大于"
	case driver.OpGe:
		return "大于等于"
	case driver.OpLt:
		return "小于"
	case driver.OpLe:
		return "小于等于"
	case driver.OpEq:
		return "等于"
	case driver.OpNe:
		return "不等于"
	case driver.OpIn:
		return "属于"
	}
	return b
}

// alarmTypeName 报警类型中文说明。
func alarmTypeName(t string) string {
	switch t {
	case alarm.TypeOffline:
		return "断联报警"
	case alarm.TypeQuality:
		return "采集质量报警"
	case alarm.TypeThreshold:
		return "超限报警"
	case alarm.TypeRecover:
		return "恢复记录"
	}
	return t
}

// alarmStatusName 报警状态中文说明。
func alarmStatusName(s string) string {
	switch s {
	case alarm.StatusActive:
		return "未恢复"
	case alarm.StatusCleared:
		return "已恢复"
	}
	return s
}

// ==================== 可靠性统计 ====================
//
// 两个端点都只读 alarm 表，**不碰 point_threshold_record** —— 后者是节流后的样本表
// （同一连续越限段内每 1 分钟才写一条），且只保留 7 天 / 20000 行，有效窗口随越限
// 速率缩水，供不起跨天统计。口径与取舍见 vo 的说明与 specs/alarm.md §3。

const (
	// maxReliabilityWindowDays 统计窗口天数上限。
	//
	// 存在的唯一理由是护住那唯一的 SQLite 连接（MaxOpenConns=1，见 database/sqlite）：
	// alarm 表永不清理，不设上限的一次全表聚合会把采集协程串住。
	maxReliabilityWindowDays = 90
	// defaultPointAbnormalLimit 点位 top-N 的默认条数（Limit 省略或 <=0 时）。
	defaultPointAbnormalLimit = 20
	// maxPointAbnormalLimit 点位 top-N 的条数上限。
	maxPointAbnormalLimit = 500

	alarmTimeLayout = "2006-01-02 15:04:05"
	alarmDateLayout = "2006-01-02"
)

// parseReliabilityWindow 校验并展开统计窗口，返回起止时间串与窗口秒数。
//
// 窗口按**左闭右开** [from 00:00:00, to 次日 00:00:00) 处理。这样分母（窗口秒数）
// 与分子的停机时长是同一套口径：覆盖整个窗口的段算出的停机秒数恰好等于窗口秒数，
// 可用率正好是 0；若分母用"含两端 +1 秒"，同一段会得出 0.00001 这种看着就错的值。
func parseReliabilityWindow(from, to string) (wStart, wEnd string, windowSeconds float64, err error) {
	badParam := func(msg string) error {
		return appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), msg, enums.ParamValidEnum.GetMsgKey())
	}
	start, perr := time.ParseInLocation(alarmDateLayout, from, time.Local)
	if perr != nil {
		return "", "", 0, badParam("from 必须是 YYYY-MM-DD 格式")
	}
	endDay, perr := time.ParseInLocation(alarmDateLayout, to, time.Local)
	if perr != nil {
		return "", "", 0, badParam("to 必须是 YYYY-MM-DD 格式")
	}
	if endDay.Before(start) {
		return "", "", 0, badParam("to 不能早于 from")
	}
	end := endDay.AddDate(0, 0, 1) // 右开边界：to 当天全部包含在内
	windowSeconds = end.Sub(start).Seconds()
	if windowSeconds > float64(maxReliabilityWindowDays)*86400 {
		return "", "", 0, badParam(fmt.Sprintf("统计窗口不能超过 %d 天", maxReliabilityWindowDays))
	}
	return start.Format(alarmTimeLayout), end.Format(alarmTimeLayout), windowSeconds, nil
}

// alarmMetaRow 名称快照查询的原始行。
type alarmMetaRow struct {
	TargetID   string `gorm:"column:target_id"`
	TargetName string `gorm:"column:target_name"`
	DeviceID   string `gorm:"column:device_id"`
}

// latestAlarmMeta 取每个目标在窗口内**最近一条**报警行的名称快照与所属设备。
//
// 用"最近一条"而非任取一条：设备/点位改过名时各行的 target_name 是不同时期的快照，
// 任取会让报表上的名字在两次请求间随机变化。名称取自 alarm 行而非 join device 表
// —— 后者在设备被删除后取不到，而 target_name 正是为此存在的快照。
//
// 过滤条件与聚合查询**必须一致**（含跨窗口起点的那条重叠判定），否则压在窗口边界上的
// 目标会被聚合选中却取不到名字。
func (s *AlarmService) latestAlarmMeta(ctx context.Context, targetTypes []string, ids []string,
	wStart, wEnd, now string) (map[string]alarmMetaRow, error) {

	var rows []alarmMetaRow
	if err := s.sqliteDB.WithContext(ctx).Model(&po.Alarm{}).
		Select("target_id, target_name, device_id").
		Where("target_type IN ?", targetTypes).
		Where("target_id IN ?", ids).
		Where("first_occur_time < ?", wEnd).
		Where("COALESCE(NULLIF(clear_time, ''), ?) > ?", now, wStart).
		Order("first_occur_time DESC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]alarmMetaRow, len(rows))
	for _, r := range rows {
		if _, ok := out[r.TargetID]; !ok { // 已按时间倒序，首个即最近
			out[r.TargetID] = r
		}
	}
	return out, nil
}

// gatewayIdentity 读网关自身身份（iot_gateway 单行表），供统计报表打标。
//
// **任何失败都退化成空串，不向上返回 error**：身份是标注不是数据，而身份行缺失
// 在 InitDB 里本就是刻意非致命的状态（播种失败时网关照常运行）。为了一个标签让
// 整份排行取不到，是把报表可用性押在一条与统计无关的行上。取舍与
// discovery.Announcer 相同（那边缺身份时只暂停广播，采集与上报不受影响）。
//
// 这里直接读表而不经 GatewayService：AlarmService 已持有同一个 *gorm.DB，而单行表
// 的取值就是一次 First —— 为它把 GatewayService 拉进依赖图，只会多出一条与统计
// 无关的服务间耦合。
func (s *AlarmService) gatewayIdentity(ctx context.Context) (id, name string) {
	var gw po.IotGateway
	if err := s.sqliteDB.WithContext(ctx).First(&gw).Error; err != nil {
		// Debug 而非 Warn：这是**逐请求**的路径，身份缺失时外部系统会按自己的
		// 轮询节奏反复命中，Warn 会变成稳定噪声。真正的库故障会在随后的统计
		// 查询上直接报出来，这里不必替它喊。
		logger.Debug("alarm: gateway identity unavailable, statistics returned without gateway label: %v", err)
		return "", ""
	}
	return gw.ID, gw.Name
}

// reliabilityAggRow 设备维度聚合的原始行。
type reliabilityAggRow struct {
	TargetID    string  `gorm:"column:target_id"`
	Episodes    int     `gorm:"column:episodes"`
	DownSeconds float64 `gorm:"column:down_s"`
	MttrSum     float64 `gorm:"column:mttr_sum"`
	MttrCount   int     `gorm:"column:mttr_n"`
}

// DeviceReliability 设备维度可靠性统计（窗口内）。
//
// ⚠️ **参数绑定顺序**：`?` 按在 SQL 文本里出现的先后绑定，SELECT 子句先于 WHERE 子句。
// 下表与 SQL 中 `?` 的先后严格对应，改 SQL 必须同步改参数序。
func (s *AlarmService) DeviceReliability(ctx context.Context, req *dto.DeviceReliabilityDTO) (*vo.DeviceReliabilityReportVO, error) {
	wStart, wEnd, windowSeconds, err := parseReliabilityWindow(req.From, req.To)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(alarmTimeLayout)
	gwID, gwName := s.gatewayIdentity(ctx)

	var rows []reliabilityAggRow
	if err := s.sqliteDB.WithContext(ctx).Raw(`
		SELECT target_id,
		       COUNT(*) AS episodes,
		       SUM((julianday(MIN(COALESCE(NULLIF(clear_time, ''), ?), ?))
		          - julianday(MAX(first_occur_time, ?))) * 86400.0) AS down_s,
		       SUM(CASE WHEN status = 'cleared' AND clear_time <> '' AND first_occur_time >= ?
		                THEN (julianday(clear_time) - julianday(first_occur_time)) * 86400.0
		                ELSE 0 END) AS mttr_sum,
		       SUM(CASE WHEN status = 'cleared' AND clear_time <> '' AND first_occur_time >= ?
		                THEN 1 ELSE 0 END) AS mttr_n
		FROM alarm
		WHERE target_type = ? AND alarm_type = ?
		  AND first_occur_time < ?
		  AND COALESCE(NULLIF(clear_time, ''), ?) > ?
		GROUP BY target_id`,
		// SELECT：MIN 的两侧（未恢复段算到窗口终点）+ MAX 的下界（段起点不早于窗口起点）
		now, wEnd, wStart,
		// SELECT：两个 MTTR 的 CASE —— 只收**在窗口内开始**的段，跨窗口起点的段起点不可见
		wStart, wStart,
		// WHERE：类型 + 段与窗口有重叠（起点在窗口终点前、终点在窗口起点后）
		alarm.TypeDevice, alarm.TypeOffline,
		wEnd,
		now, wStart,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// 空榜单也带网关身份：外部系统拿到空 records 时仍要能回答"这是哪台网关报的"，
		// 否则"没有故障"和"报表没对上网关"在它那边长得一样。
		return &vo.DeviceReliabilityReportVO{
			GatewayID:   gwID,
			GatewayName: gwName,
			Records:     []vo.DeviceReliabilityVO{},
		}, nil
	}

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.TargetID)
	}
	meta, err := s.latestAlarmMeta(ctx, []string{alarm.TypeDevice}, ids, wStart, wEnd, now)
	if err != nil {
		return nil, err
	}

	out := make([]vo.DeviceReliabilityVO, 0, len(rows))
	for _, r := range rows {
		// 无已完成故障时用 -1 而不是 0：真实段长不可能为 0，0 会与"秒级故障"混淆。
		mttr := -1.0
		if r.MttrCount > 0 {
			mttr = r.MttrSum / float64(r.MttrCount)
		}
		avail := 1.0
		if windowSeconds > 0 {
			avail = 1 - r.DownSeconds/windowSeconds
		}
		if avail < 0 { // 浮点兜底
			avail = 0
		}
		// 这里存的是**原始精度**：排序要用它，取整统一放在最后一步（见下）。
		out = append(out, vo.DeviceReliabilityVO{
			DeviceID:      r.TargetID,
			DeviceName:    meta[r.TargetID].TargetName,
			Episodes:      r.Episodes,
			DownSeconds:   r.DownSeconds,
			MttrSeconds:   mttr,
			Availability:  avail,
			WindowSeconds: windowSeconds,
		})
	}

	// 可用率升序（最差在前）；同值时按设备 ID 稳定排序，避免两次请求之间顺序抖动。
	// 比的是**取整前**的原始可用率：停机只差几秒的两台设备不会因取整并列、名次被设备 ID 决定。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Availability != out[j].Availability {
			return out[i].Availability < out[j].Availability
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	// 截断放在**排序之后、编号之前**：Ranking 因此仍是全局名次（第 1 名就是最差那台），
	// 不会因为是第 1 页而变成从 1 重新数。
	if req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	// 取整只在**输出前**做：排序 / 截断 / 排名全部建立在原始精度上，这里才收敛成报表口径。
	for i := range out {
		out[i].Ranking = i + 1
		// ×10000 再 ÷100 不是笔误：先把 0~1 的小数抬成百分数、同时留下两位小数
		// （先 ×100 变百分数，再 ×100 取整，最后 ÷100 落回两位）。
		out[i].Availability = math.Round(out[i].Availability*10000) / 100
		out[i].DownSeconds = math.Round(out[i].DownSeconds)
		// MTTR 为 -1 时取整仍是 -1（哨兵值原样透出），不需要额外分支。
		out[i].MttrSeconds = math.Round(out[i].MttrSeconds)
	}
	return &vo.DeviceReliabilityReportVO{
		GatewayID:   gwID,
		GatewayName: gwName,
		Records:     out,
	}, nil
}

// pointAbnormalAggRow 点位维度聚合的原始行。
type pointAbnormalAggRow struct {
	TargetID          string `gorm:"column:target_id"`
	QualityEpisodes   int    `gorm:"column:quality_episodes"`
	ThresholdEpisodes int    `gorm:"column:threshold_episodes"`
	TotalEpisodes     int    `gorm:"column:total_episodes"`
	LastOccurTime     string `gorm:"column:last_occur_time"`
}

// PointAbnormal 点位维度异常 top-N（窗口内）。
//
// 质量异常与超限**分开计数**，不合并：前者要找运维（地址/解析问题），后者可能只是
// 工艺波动，混成一个"非正常次数"值班的人没法据此行动。
//
// 条件用链式 Where 而不是一条 Raw：deviceId 是**可选**过滤，在 Raw 里表达它得让两个 `?`
// 绑同一个值（一个只做空串判断、一个才真去比 device_id），读的人必须回参数表数位数才
// 知道第二个 `?` 是谁。链式的每个 `?` 只绑自己那一个值，参数顺序不再是靠注释维持的纪律。
//
// SQL 里的 IN 列表仍写成字面量而非常量插值：它们是**存进库里的契约值**（迁移与
// alarm 包的常量共同定义）。字面量让这份统计与 Go 侧常量解耦 —— 改常量不会静默
// 改变已存数据被算进哪一类。
func (s *AlarmService) PointAbnormal(ctx context.Context, req *dto.PointAbnormalDTO) (*vo.PointAbnormalReportVO, error) {
	wStart, wEnd, _, err := parseReliabilityWindow(req.From, req.To)
	if err != nil {
		return nil, err
	}
	gwID, gwName := s.gatewayIdentity(ctx)
	limit := req.Limit
	if limit <= 0 {
		limit = defaultPointAbnormalLimit
	}
	if limit > maxPointAbnormalLimit {
		limit = maxPointAbnormalLimit
	}
	now := time.Now().Format(alarmTimeLayout)

	q := s.sqliteDB.WithContext(ctx).Table("alarm").
		Select(`target_id,
		        SUM(CASE WHEN alarm_type = 'quality'   THEN 1 ELSE 0 END) AS quality_episodes,
		        SUM(CASE WHEN alarm_type = 'threshold' THEN 1 ELSE 0 END) AS threshold_episodes,
		        COUNT(*) AS total_episodes,
		        MAX(last_occur_time) AS last_occur_time`).
		Where("target_type IN ('point', 'point_threshold')").
		Where("alarm_type IN ('quality', 'threshold')").
		Where("first_occur_time < ?", wEnd).
		Where("COALESCE(NULLIF(clear_time, ''), ?) > ?", now, wStart).
		Group("target_id").
		Order("total_episodes DESC, target_id ASC").
		Limit(limit)
	if req.DeviceID != "" {
		q = q.Where("device_id = ?", req.DeviceID)
	}

	var rows []pointAbnormalAggRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// 空榜单也带身份，理由同 DeviceReliability（空 = 这段时间没有异常点位，
		// 而不是"不知道这是哪台网关"）。
		return &vo.PointAbnormalReportVO{
			GatewayID:   gwID,
			GatewayName: gwName,
			Records:     []vo.PointAbnormalVO{},
		}, nil
	}

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.TargetID)
	}
	// 点位行的 device_id 对质量与超限两类都会填（见 po.Alarm.DeviceID）。
	meta, err := s.latestAlarmMeta(ctx, []string{alarm.TypePoint, alarm.TypePointThreshold},
		ids, wStart, wEnd, now)
	if err != nil {
		return nil, err
	}

	out := make([]vo.PointAbnormalVO, 0, len(rows))
	for i, r := range rows {
		m := meta[r.TargetID]
		out = append(out, vo.PointAbnormalVO{
			TargetID:          r.TargetID,
			TargetName:        m.TargetName,
			DeviceID:          m.DeviceID,
			QualityEpisodes:   r.QualityEpisodes,
			ThresholdEpisodes: r.ThresholdEpisodes,
			TotalEpisodes:     r.TotalEpisodes,
			LastOccurTime:     r.LastOccurTime,
			// 排序已在 SQL 完成（ORDER BY total DESC, target_id ASC），此处只编号。
			Ranking: i + 1,
		})
	}
	return &vo.PointAbnormalReportVO{
		GatewayID:   gwID,
		GatewayName: gwName,
		Records:     out,
	}, nil
}
