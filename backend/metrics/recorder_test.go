// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"iot-gateway/collector"
	"iot-gateway/driver"
	"iot-gateway/model/vo"
	"iot-gateway/push"
)

func TestPushRecordsCountsUsablePoints(t *testing.T) {
	r := NewRecorder()
	r.PushRecords([]collector.CollectedRecord{
		{Quality: driver.QualityGood},
		{Quality: driver.QualityUncertain},
		{Quality: driver.QualityBadAddress},
	})
	r.PushRecords(nil) // 空批次不应影响计数

	points, bad := r.takePoints()
	if points != 3 {
		t.Errorf("points = %d, want 3", points)
	}
	if bad != 1 {
		t.Errorf("bad = %d, want 1", bad)
	}

	// 取走后必须清零，否则下一分钟会重复计入
	points, bad = r.takePoints()
	if points != 0 || bad != 0 {
		t.Errorf("after take = (%d,%d), want (0,0)", points, bad)
	}
}

// appendMinutes 从 from 起每分钟追加一个采样点，便于构造连续 / 带断档的时间序列。
func appendMinutes(r *Recorder, from time.Time, n int) {
	for i := 0; i < n; i++ {
		r.append(Sample{Time: from.Add(time.Duration(i) * time.Minute).Format(SampleTimeLayout)})
	}
}

func TestTrendClampsHoursAndKeepsNewest(t *testing.T) {
	r := NewRecorder()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	// 最近 120 分钟、每分钟一个点（最后一个在 now 的前一分钟）
	appendMinutes(r, now.Add(-120*time.Minute), 120)

	if got := r.trendAt(1, now); len(got) != 60 {
		t.Errorf("Trend(1) len = %d, want 60", len(got))
	} else if got[0].Time != now.Add(-60*time.Minute).Format(SampleTimeLayout) ||
		got[len(got)-1].Time != now.Add(-time.Minute).Format(SampleTimeLayout) {
		t.Errorf("Trend(1) window = [%s..%s]", got[0].Time, got[len(got)-1].Time)
	}

	if got := r.trendAt(0, now); len(got) != 60 {
		t.Errorf("Trend(0) should clamp to 1h, len = %d", len(got))
	}
	if got := r.trendAt(99, now); len(got) != 120 {
		t.Errorf("Trend(99) should clamp to available samples, len = %d", len(got))
	}
}

// 宿主休眠会在采样时间上留下数小时空档。窗口必须按墙钟裁：
// 休眠前那批点不能因为「总共还没到 360 个」就被一起返回。
func TestTrendWindowCutsBeforeSuspendGap(t *testing.T) {
	r := NewRecorder()
	now := time.Date(2026, 10, 23, 9, 37, 0, 0, time.Local)
	// 02:00–02:30 采集，机器休眠到 03:37 只补了一个点，
	// 再休眠到 09:00，恢复后连续采到 09:37。
	appendMinutes(r, time.Date(2026, 10, 23, 2, 0, 0, 0, time.Local), 31)
	appendMinutes(r, time.Date(2026, 10, 23, 3, 37, 0, 0, time.Local), 1)
	appendMinutes(r, time.Date(2026, 10, 23, 9, 0, 0, 0, time.Local), 38)

	got := r.trendAt(6, now)
	if len(got) == 0 {
		t.Fatal("Trend(6) returned no samples")
	}
	// 6 小时前的 03:37 正好落在窗口边界上，必须保留；02:00–02:30 的点必须排除
	if got[0].Time != "2026-10-23 03:37:00" {
		t.Errorf("first = %s, want 2026-10-23 03:37:00", got[0].Time)
	}
	if last := got[len(got)-1].Time; last != "2026-10-23 09:37:00" {
		t.Errorf("last = %s, want 2026-10-23 09:37:00", last)
	}

	// 窗口裁短了，但「本次运行从哪开始」不能被一起裁掉
	if oldest := r.Oldest(); oldest != "2026-10-23 02:00:00" {
		t.Errorf("Oldest() = %s, want 2026-10-23 02:00:00", oldest)
	}
}

func TestOldestEmpty(t *testing.T) {
	if got := NewRecorder().Oldest(); got != "" {
		t.Errorf("Oldest() = %q, want empty", got)
	}
}

func TestAppendTrimsToMaxSamples(t *testing.T) {
	r := NewRecorder()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	appendMinutes(r, now.Add(-time.Duration(MaxSamples+5)*time.Minute), MaxSamples+5)

	got := r.trendAt(24, now)
	if len(got) != MaxSamples {
		t.Fatalf("len = %d, want %d", len(got), MaxSamples)
	}
	if want := now.Add(-MaxSamples * time.Minute).Format(SampleTimeLayout); got[0].Time != want {
		t.Errorf("oldest sample = %s, want %s", got[0].Time, want)
	}
	if want := now.Add(-time.Minute).Format(SampleTimeLayout); got[len(got)-1].Time != want {
		t.Errorf("newest sample = %s, want %s", got[len(got)-1].Time, want)
	}
}

// ---------- 采样器 ----------

type fakeOverview struct {
	overview *vo.DeviceOverviewVO
	err      error
}

func (f fakeOverview) GetDeviceOverview(context.Context) (*vo.DeviceOverviewVO, error) {
	return f.overview, f.err
}

type fakeCollection struct{ status vo.CollectionStatusVO }

func (f fakeCollection) GetCollectionStatus(context.Context) vo.CollectionStatusVO {
	return f.status
}

type fakePush struct{ statuses []push.ChannelStatusVO }

func (f fakePush) GetStatus() []push.ChannelStatusVO { return f.statuses }

func TestSamplerCollectsSnapshot(t *testing.T) {
	rec := NewRecorder()
	rec.PushRecords([]collector.CollectedRecord{
		{Quality: driver.QualityGood},
		{Quality: driver.QualityBadComms},
	})
	sampler := NewSampler(
		rec,
		fakeOverview{overview: &vo.DeviceOverviewVO{
			Total: 5, Enabled: 4, Collected: 3, Online: 2, Offline: 1, UnCollected: 1,
		}},
		fakeCollection{status: vo.CollectionStatusVO{Running: true}},
		fakePush{statuses: []push.ChannelStatusVO{
			{QueueDepth: 2, SpoolDepth: 7},
			{QueueDepth: 3, SpoolDepth: 1},
		}},
	)

	when := time.Date(2026, 10, 21, 22, 0, 0, 0, time.Local)
	sampler.collectAt(when)

	got := rec.trendAt(1, when)
	if len(got) != 1 {
		t.Fatalf("samples = %d, want 1", len(got))
	}
	s := got[0]
	if s.Time != "2026-10-21 22:00:00" {
		t.Errorf("time = %q", s.Time)
	}
	if !s.HasOverview || s.Online != 2 || s.Offline != 1 || s.Collected != 3 {
		t.Errorf("overview = %+v", s)
	}
	if !s.Collecting {
		t.Error("Collecting should be true")
	}
	if s.Points != 2 || s.BadPoints != 1 {
		t.Errorf("points = %d/%d, want 2/1", s.Points, s.BadPoints)
	}
	if s.QueueDepth != 5 || s.SpoolDepth != 8 {
		t.Errorf("depth = %d/%d, want 5/8", s.QueueDepth, s.SpoolDepth)
	}
}

func TestSamplerKeepsPointsWhenOverviewFails(t *testing.T) {
	rec := NewRecorder()
	rec.PushRecords([]collector.CollectedRecord{{Quality: driver.QualityGood}})
	sampler := NewSampler(
		rec,
		fakeOverview{err: errors.New("db down")},
		fakeCollection{},
		fakePush{},
	)

	when := time.Now()
	sampler.collectAt(when)

	got := rec.trendAt(1, when)
	if len(got) != 1 {
		t.Fatalf("samples = %d, want 1", len(got))
	}
	if got[0].HasOverview {
		t.Error("HasOverview should be false on error")
	}
	if got[0].Points != 1 {
		t.Errorf("points = %d, want 1 (throughput is independent of overview)", got[0].Points)
	}
}

// 采样时间戳必须落在整分钟上：前端时间轴按整分钟对齐刻度，
// 点落在 :37 这类任意秒上时，启动初期窗口只有几分钟的柱状图会明显偏离刻度。
func TestSamplerAlignsSampleTimeToInterval(t *testing.T) {
	rec := NewRecorder()
	sampler := NewSampler(rec, fakeOverview{}, fakeCollection{}, fakePush{})

	when := time.Date(2026, 10, 21, 22, 0, 37, 0, time.Local)
	sampler.collectAt(when)

	got := rec.trendAt(1, when)
	if len(got) != 1 {
		t.Fatalf("samples = %d, want 1", len(got))
	}
	if got[0].Time != "2026-10-21 22:00:00" {
		t.Errorf("time = %q, want 2026-10-21 22:00:00", got[0].Time)
	}
}
