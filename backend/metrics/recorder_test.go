// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package metrics

import (
	"context"
	"errors"
	"fmt"
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

func TestTrendClampsHoursAndKeepsNewest(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < 120; i++ {
		r.append(Sample{Time: fmt.Sprintf("t%03d", i)})
	}

	if got := r.Trend(1); len(got) != 60 {
		t.Errorf("Trend(1) len = %d, want 60", len(got))
	} else if got[0].Time != "t060" || got[len(got)-1].Time != "t119" {
		t.Errorf("Trend(1) window = [%s..%s], want [t060..t119]", got[0].Time, got[len(got)-1].Time)
	}

	if got := r.Trend(0); len(got) != 60 {
		t.Errorf("Trend(0) should clamp to 1h, len = %d", len(got))
	}
	if got := r.Trend(99); len(got) != 120 {
		t.Errorf("Trend(99) should clamp to available samples, len = %d", len(got))
	}
}

func TestAppendTrimsToMaxSamples(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < MaxSamples+5; i++ {
		r.append(Sample{Time: fmt.Sprintf("t%04d", i)})
	}

	got := r.Trend(24)
	if len(got) != MaxSamples {
		t.Fatalf("len = %d, want %d", len(got), MaxSamples)
	}
	if got[0].Time != "t0005" {
		t.Errorf("oldest sample = %s, want t0005", got[0].Time)
	}
	if got[len(got)-1].Time != "t1444" {
		t.Errorf("newest sample = %s, want t1444", got[len(got)-1].Time)
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

	sampler.collectAt(time.Date(2026, 10, 21, 22, 0, 0, 0, time.Local))

	got := rec.Trend(1)
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

	sampler.collectAt(time.Now())

	got := rec.Trend(1)
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
