// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"iot-gateway/alarm"
	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

const statsTimeLayout = "2006-01-02 15:04:05"

// statsWindow 造一个**整体位于过去**的 5 天窗口，返回日期串与基准零点。
//
// 窗口整个落在"现在"之前是刻意的：未恢复的段一律被窗口终点截断，断言因此与运行
// 时刻无关 —— 否则跑在窗口中间时 downSeconds 会随 now 漂移，测试变成偶发失败。
func statsWindow() (from, to string, base time.Time) {
	n := time.Now()
	base = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -10)
	return base.Format("2006-01-02"), base.AddDate(0, 0, 4).Format("2006-01-02"), base
}

// seedOffline 插一条设备断联行。clear 传空串 = 未恢复。
//
// 空串而非 NULL 是与生产一致的：po.Alarm.ClearTime 是 Go string，未恢复时 GORM
// 写进去的是空串而不是 NULL（见 service 层 SQL 里的 NULLIF(clear_time, ...) 判定）。
func seedOffline(db *gorm.DB, deviceID, name, first, clear, status string) {
	a := &po.Alarm{
		TargetID:       deviceID,
		TargetName:     name,
		TargetType:     alarm.TypeDevice,
		AlarmType:      alarm.TypeOffline,
		Level:          "warning",
		Content:        "设备 " + name + " 断联",
		Status:         status,
		FirstOccurTime: first,
		LastOccurTime:  first,
		ClearTime:      clear,
		CreatedAt:      first,
		UpdatedAt:      first,
	}
	if err := db.Create(a).Error; err != nil {
		panic(err)
	}
}

// seedPointAbn 插一条点位报警行（质量或超限，target_type 按类型自动选）。
func seedPointAbn(db *gorm.DB, deviceID, pointID, pointName, alarmType, first, clear, status string) {
	targetType := alarm.TypePoint
	if alarmType == alarm.TypeThreshold {
		targetType = alarm.TypePointThreshold
	}
	a := &po.Alarm{
		TargetID:       pointID,
		TargetName:     pointName,
		TargetType:     targetType,
		AlarmType:      alarmType,
		DeviceID:       deviceID,
		Level:          "warning",
		Content:        "test",
		Status:         status,
		FirstOccurTime: first,
		LastOccurTime:  first,
		ClearTime:      clear,
		CreatedAt:      first,
		UpdatedAt:      first,
	}
	if err := db.Create(a).Error; err != nil {
		panic(err)
	}
}

// approx 浮点秒数比较（julianday 差值有微小误差）。
func approx(got, want float64) bool {
	d := got - want
	return d < 0.01 && d > -0.01
}

func TestDeviceReliabilityAggregatesAndRanks(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	// dev-A 停了 1 小时；dev-B 停了 30 分钟；dev-C 窗口内无故障（不出现在结果里）
	seedOffline(db, "dev-A", "1#炉",
		base.AddDate(0, 0, 1).Format(statsTimeLayout),
		base.AddDate(0, 0, 1).Add(time.Hour).Format(statsTimeLayout),
		alarm.StatusCleared)
	seedOffline(db, "dev-B", "2#炉",
		base.AddDate(0, 0, 2).Format(statsTimeLayout),
		base.AddDate(0, 0, 2).Add(30*time.Minute).Format(statsTimeLayout),
		alarm.StatusCleared)

	report, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	got := report.Records
	// 窗口内无故障的设备**不出现**（等价于可用率 100%，但不在榜上）
	if len(got) != 2 {
		t.Fatalf("expected 2 devices with outages, got %d: %+v", len(got), got)
	}
	// 最差在前
	if got[0].DeviceID != "dev-A" || got[0].Ranking != 1 || got[1].DeviceID != "dev-B" || got[1].Ranking != 2 {
		t.Fatalf("unexpected order/ranking: %+v", got)
	}
	if got[0].DeviceName != "1#炉" {
		t.Fatalf("expected name snapshot 1#炉, got %q", got[0].DeviceName)
	}
	if got[0].Episodes != 1 || got[1].Episodes != 1 {
		t.Fatalf("unexpected episodes: %+v", got)
	}
	if !approx(got[0].DownSeconds, 3600) || !approx(got[1].DownSeconds, 1800) {
		t.Fatalf("unexpected downSeconds: %v / %v", got[0].DownSeconds, got[1].DownSeconds)
	}
	if !approx(got[0].MttrSeconds, 3600) || !approx(got[1].MttrSeconds, 1800) {
		t.Fatalf("unexpected mttr: %v / %v", got[0].MttrSeconds, got[1].MttrSeconds)
	}
	// 窗口 = 5 天（含两端）
	if !approx(got[0].WindowSeconds, 5*86400) {
		t.Fatalf("windowSeconds: want %v, got %v", 5*86400, got[0].WindowSeconds)
	}
	// 可用率是**百分数**、四舍五入到两位小数：(1 - 3600/432000) * 100 = 99.1666… -> 99.17
	if want := 99.17; got[0].Availability != want {
		t.Fatalf("availability: want %v, got %v", want, got[0].Availability)
	}
}

// TestDeviceReliabilityClipsOngoingSegmentAtWindowEnd 未恢复的段算到**窗口终点**
// （不是算到 now）—— 窗口整体在过去，两者恰好不同，能咬住这个区别。
func TestDeviceReliabilityClipsOngoingSegmentAtWindowEnd(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	start := base.AddDate(0, 0, 1)
	seedOffline(db, "dev-A", "1#炉", start.Format(statsTimeLayout), "", alarm.StatusActive)

	report, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	got := report.Records
	if len(got) != 1 {
		t.Fatalf("expected 1 device, got %d", len(got))
	}
	// 窗口右开边界 = to 次日 00:00:00（左闭右开口径，见 parseReliabilityWindow）
	wEnd := base.AddDate(0, 0, 5)
	if want := wEnd.Sub(start).Seconds(); !approx(got[0].DownSeconds, want) {
		t.Fatalf("ongoing segment: want downSeconds %v, got %v", want, got[0].DownSeconds)
	}
	// 没有已完成的段 → MTTR 为 -1（不是 0，0 会与"秒级故障"混淆）
	if got[0].MttrSeconds != -1 {
		t.Fatalf("expected mttr -1 when nothing recovered, got %v", got[0].MttrSeconds)
	}
}

// TestDeviceReliabilityClipsSegmentSpanningWindowStart 起点在窗口之前的段只计
// **重叠部分**；且它**不进 MTTR**（起点不可见，算进去会把修复时长虚增）。
func TestDeviceReliabilityClipsSegmentSpanningWindowStart(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	seedOffline(db, "dev-A", "1#炉",
		base.AddDate(0, 0, -2).Format(statsTimeLayout), // 窗口起点前两天就断了
		base.Add(time.Hour).Format(statsTimeLayout),    // 窗口内 1 小时后恢复
		alarm.StatusCleared)

	report, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	got := report.Records
	if len(got) != 1 || got[0].Episodes != 1 {
		t.Fatalf("跨越窗口起点的段必须计入: %+v", got)
	}
	if !approx(got[0].DownSeconds, 3600) {
		t.Fatalf("应只计窗口内重叠的 3600s，得到 %v", got[0].DownSeconds)
	}
	if got[0].MttrSeconds != -1 {
		t.Fatalf("跨窗口起点的段不应进 MTTR，得到 %v", got[0].MttrSeconds)
	}
}

// TestDeviceReliabilityIgnoresRecoverRowsAndPointAlarms recover 历史行与点位报警
// 都不能混进设备维度（它们 target_type / alarm_type 不同，或根本不是故障）。
func TestDeviceReliabilityIgnoresRecoverRowsAndPointAlarms(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	first := base.AddDate(0, 0, 1).Format(statsTimeLayout)
	clear := base.AddDate(0, 0, 1).Add(time.Hour).Format(statsTimeLayout)
	seedOffline(db, "dev-A", "1#炉", first, clear, alarm.StatusCleared)

	// 恢复历史行：alarm_type=recover，不能算作一次故障
	rec := &po.Alarm{
		TargetID: "dev-A", TargetName: "1#炉", TargetType: alarm.TypeDevice,
		AlarmType: alarm.TypeRecover, Level: "warning", Content: "设备 1#炉 恢复通信",
		Status: alarm.StatusCleared, FirstOccurTime: clear, LastOccurTime: clear,
		ClearTime: clear, CreatedAt: clear, UpdatedAt: clear,
	}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("seed recover: %v", err)
	}
	// 同设备下的点位质量报警：不是设备维度的数据
	seedPointAbn(db, "dev-A", "pt-1", "1#炉.温度", alarm.TypeQuality, first, clear, alarm.StatusCleared)

	report, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	got := report.Records
	if len(got) != 1 {
		t.Fatalf("expected 1 device, got %d: %+v", len(got), got)
	}
	if got[0].Episodes != 1 {
		t.Fatalf("recover 行或点位报警混入了设备维度：episodes=%d", got[0].Episodes)
	}
	if !approx(got[0].DownSeconds, 3600) {
		t.Fatalf("downSeconds 被 recovery 行污染: %v", got[0].DownSeconds)
	}
}

func TestReliabilityWindowValidation(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	_, _, base := statsWindow()

	wantParamErr := func(t *testing.T, err error) {
		t.Helper()
		var appErr *appError.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected *appError.AppError, got %v", err)
		}
		if appErr.Code != enums.ParamValidEnum.GetCode() {
			t.Fatalf("expected code %s, got %s (%s)", enums.ParamValidEnum.GetCode(), appErr.Code, appErr.Message)
		}
	}

	bad := []struct {
		name, from, to string
	}{
		{"from 格式非法", "2026-8-1", "2026-08-05"},
		{"to 格式非法", "2026-08-01", "2026/08/05"},
		{"to 早于 from", "2026-08-05", "2026-08-01"},
		{"空窗口参数", "", ""},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: c.from, To: c.to})
			wantParamErr(t, err)
		})
	}

	// 边界：正好 90 天通过，91 天拒绝
	t.Run("正好 90 天通过", func(t *testing.T) {
		from := base.Format("2006-01-02")
		to := base.AddDate(0, 0, 89).Format("2006-01-02") // 含当天 = 90 天
		if _, err := svc.DeviceReliability(context.Background(),
			&dto.DeviceReliabilityDTO{From: from, To: to}); err != nil {
			t.Fatalf("90 天窗口应当通过，得到 %v", err)
		}
	})
	t.Run("91 天拒绝", func(t *testing.T) {
		from := base.Format("2006-01-02")
		to := base.AddDate(0, 0, 90).Format("2006-01-02") // 含当天 = 91 天
		_, err := svc.DeviceReliability(context.Background(),
			&dto.DeviceReliabilityDTO{From: from, To: to})
		wantParamErr(t, err)
	})
}

func TestPointAbnormalSeparatesQualityAndThreshold(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	first := base.AddDate(0, 0, 1).Format(statsTimeLayout)
	clear := base.AddDate(0, 0, 1).Add(time.Hour).Format(statsTimeLayout)

	// pt-1（dev-A）：2 次质量异常 + 1 次超限
	for i := 0; i < 2; i++ {
		seedPointAbn(db, "dev-A", "pt-1", "1#炉.温度", alarm.TypeQuality, first, clear, alarm.StatusCleared)
	}
	seedPointAbn(db, "dev-A", "pt-1", "1#炉.温度", alarm.TypeThreshold, first, clear, alarm.StatusCleared)
	// pt-2（dev-B）：1 次超限
	seedPointAbn(db, "dev-B", "pt-2", "2#炉.压力", alarm.TypeThreshold, first, clear, alarm.StatusCleared)

	report, err := svc.PointAbnormal(context.Background(), &dto.PointAbnormalDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("PointAbnormal: %v", err)
	}
	got := report.Records
	if len(got) != 2 {
		t.Fatalf("expected 2 points, got %d: %+v", len(got), got)
	}
	// 按异常段数降序
	if got[0].TargetID != "pt-1" || got[0].Ranking != 1 || got[1].Ranking != 2 {
		t.Fatalf("unexpected order: %+v", got)
	}
	// 两类**分开计数**，不合并
	if got[0].QualityEpisodes != 2 || got[0].ThresholdEpisodes != 1 || got[0].TotalEpisodes != 3 {
		t.Fatalf("pt-1 counts wrong: %+v", got[0])
	}
	if got[1].QualityEpisodes != 0 || got[1].ThresholdEpisodes != 1 || got[1].TotalEpisodes != 1 {
		t.Fatalf("pt-2 counts wrong: %+v", got[1])
	}
	// 展示串与所属设备都带出来（分组用 deviceId，展示用 targetName）
	if got[0].TargetName != "1#炉.温度" || got[0].DeviceID != "dev-A" {
		t.Fatalf("pt-1 meta wrong: %+v", got[0])
	}
	if got[0].LastOccurTime == "" {
		t.Fatalf("expected lastOccurTime populated: %+v", got[0])
	}

	t.Run("按 deviceId 过滤", func(t *testing.T) {
		rep, err := svc.PointAbnormal(context.Background(),
			&dto.PointAbnormalDTO{DeviceID: "dev-B", From: from, To: to})
		if err != nil {
			t.Fatalf("PointAbnormal: %v", err)
		}
		one := rep.Records
		if len(one) != 1 || one[0].TargetID != "pt-2" {
			t.Fatalf("deviceId 过滤失效: %+v", one)
		}
	})

	t.Run("limit 截断", func(t *testing.T) {
		rep, err := svc.PointAbnormal(context.Background(),
			&dto.PointAbnormalDTO{From: from, To: to, Limit: 1})
		if err != nil {
			t.Fatalf("PointAbnormal: %v", err)
		}
		one := rep.Records
		if len(one) != 1 || one[0].TargetID != "pt-1" {
			t.Fatalf("limit 截断失效: %+v", one)
		}
	})
}

// TestDeviceReliabilityLimit 设备维度的截断语义与其他端点相反：
// 省略 limit = **全量不截断**（设备是有限维度，截断后的列表容易被误读成完整清单）。
func TestDeviceReliabilityLimit(t *testing.T) {
	db := newAlarmTestDB(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	// 三台设备，停机时长递减 —— 可用率升序即 dev-0 / dev-1 / dev-2
	for i, dur := range []time.Duration{3 * time.Hour, 2 * time.Hour, time.Hour} {
		id := fmt.Sprintf("dev-%d", i)
		seedOffline(db, id, id,
			base.AddDate(0, 0, 1).Format(statsTimeLayout),
			base.AddDate(0, 0, 1).Add(dur).Format(statsTimeLayout),
			alarm.StatusCleared)
	}

	allRep, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	all := allRep.Records
	if len(all) != 3 {
		t.Fatalf("省略 limit 应返回全部 3 台，得到 %d", len(all))
	}

	topRep, err := svc.DeviceReliability(context.Background(),
		&dto.DeviceReliabilityDTO{From: from, To: to, Limit: 2})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	top := topRep.Records
	if len(top) != 2 {
		t.Fatalf("limit=2 应返回 2 台，得到 %d", len(top))
	}
	// 名次是**全局名次**（截断发生在排序之后、编号之前），不从 1 之外重数
	if top[0].DeviceID != "dev-0" || top[0].Ranking != 1 ||
		top[1].DeviceID != "dev-1" || top[1].Ranking != 2 {
		t.Fatalf("截断后名次错误: %+v", top)
	}
}

// newStatsDBWithGateway 建一个**同时有 alarm 与 iot_gateway 两表**的测试库。
//
// 复用 newGatewayTestDB（它已建 iot_gateway）再补 alarm，而不是反过来：身份行的
// 播种辅助 seedGatewayRow 在那里，且它断言的是那份固定的身份值。
func newStatsDBWithGateway(t *testing.T) *gorm.DB {
	t.Helper()
	db := newGatewayTestDB(t)
	if err := db.AutoMigrate(&po.Alarm{}); err != nil {
		t.Fatalf("migrate alarm: %v", err)
	}
	return db
}

// TestStatisticsReportCarriesGatewayIdentity 两个统计端点的响应都要带上网关自身身份
// —— 外部系统把这些榜单跨网关汇总时，靠的就是它。**空榜单同样要带**：否则"这段时间
// 没有故障"与"这份报表没对上网关"在调用方那边长得一模一样，而后者会让它把别人的
// 数据挂到错误的网关上。
func TestStatisticsReportCarriesGatewayIdentity(t *testing.T) {
	db := newStatsDBWithGateway(t)
	gw := seedGatewayRow(t, db)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	first := base.AddDate(0, 0, 1).Format(statsTimeLayout)
	clear := base.AddDate(0, 0, 1).Add(time.Hour).Format(statsTimeLayout)
	seedOffline(db, "dev-A", "1#炉", first, clear, alarm.StatusCleared)
	seedPointAbn(db, "dev-A", "pt-1", "1#炉.温度", alarm.TypeQuality, first, clear, alarm.StatusCleared)

	rel, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("DeviceReliability: %v", err)
	}
	if rel.GatewayID != gw.ID || rel.GatewayName != gw.Name {
		t.Fatalf("设备可靠性报表未带网关身份: want %s/%s, got %+v", gw.ID, gw.Name, rel)
	}
	if len(rel.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rel.Records))
	}

	pt, err := svc.PointAbnormal(context.Background(), &dto.PointAbnormalDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("PointAbnormal: %v", err)
	}
	if pt.GatewayID != gw.ID || pt.GatewayName != gw.Name {
		t.Fatalf("点位异常报表未带网关身份: want %s/%s, got %+v", gw.ID, gw.Name, pt)
	}

	// 空榜单：身份仍在
	empty, err := svc.PointAbnormal(context.Background(),
		&dto.PointAbnormalDTO{DeviceID: "dev-none", From: from, To: to})
	if err != nil {
		t.Fatalf("PointAbnormal: %v", err)
	}
	if len(empty.Records) != 0 {
		t.Fatalf("expected empty records, got %+v", empty.Records)
	}
	if empty.GatewayID != gw.ID || empty.GatewayName != gw.Name {
		t.Fatalf("空榜单丢了网关身份: %+v", empty)
	}
}

// TestStatisticsReportWithoutGatewayRow 身份行缺失（InitDB 里播种失败是刻意非致命的）
// 不得让统计端点失败：报表照出，只是没有网关标注（身份字段为空串）。
func TestStatisticsReportWithoutGatewayRow(t *testing.T) {
	// 建表但不写行 —— 这正是"播种失败"在现场的形态（表在、行不在）。
	db := newStatsDBWithGateway(t)
	svc := NewAlarmService(db)
	from, to, base := statsWindow()

	seedOffline(db, "dev-A", "1#炉",
		base.AddDate(0, 0, 1).Format(statsTimeLayout),
		base.AddDate(0, 0, 1).Add(time.Hour).Format(statsTimeLayout),
		alarm.StatusCleared)

	rel, err := svc.DeviceReliability(context.Background(), &dto.DeviceReliabilityDTO{From: from, To: to})
	if err != nil {
		t.Fatalf("缺身份行不应让统计端点报错: %v", err)
	}
	if len(rel.Records) != 1 {
		t.Fatalf("缺身份行时榜单丢了: %+v", rel)
	}
	if rel.GatewayID != "" || rel.GatewayName != "" {
		t.Fatalf("缺身份行时身份字段应为空串: %+v", rel)
	}
}
