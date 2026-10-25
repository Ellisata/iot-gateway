// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package realtime

import (
	"sync"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"iot-gateway/collector"
)

func rec(deviceID, addrID, value string) collector.CollectedRecord {
	return collector.CollectedRecord{
		DeviceID:           deviceID,
		DeviceName:         "dev-" + deviceID,
		DeviceAddressID:    addrID,
		DeviceAddressName:  "a-" + addrID,
		DeviceAddressLabel: "标签-" + addrID,
		Value:              value,
		DataType:           "int16",
		Kind:               "int",
		Quality:            0,
	}
}

// 质量码必须随值一起落到快照里：实时页要显示"为什么坏"，只带一个裸的坏值
// 对操作工没有意义。也顺带钉住"恢复后坏码不能残留"。
func TestStoreCarriesQuality(t *testing.T) {
	s := NewStore()
	bad := rec("d1", "a1", "")
	bad.Quality = driver.QualityBadAddress
	s.PushRecords([]collector.CollectedRecord{bad})

	if got := s.Lookup("d1", []string{"a1"})["a1"]; got.Quality != driver.QualityBadAddress {
		t.Errorf("Quality = %d, want %d", got.Quality, driver.QualityBadAddress)
	}

	// 同一个点位下一轮读通了：值与质量码一起被覆盖，不能残留上一轮的坏码。
	good := rec("d1", "a1", "42")
	good.Quality = driver.QualityGood
	s.PushRecords([]collector.CollectedRecord{good})

	got := s.Lookup("d1", []string{"a1"})["a1"]
	if got.Quality != driver.QualityGood {
		t.Errorf("恢复后 = %+v, want quality=QualityGood", got)
	}
}

// 同一点位重复采集时后写覆盖先写，且元信息与值一起更新。
func TestStorePushOverwrites(t *testing.T) {
	s := NewStore()
	s.PushRecords([]collector.CollectedRecord{rec("d1", "a1", "1")})
	s.PushRecords([]collector.CollectedRecord{rec("d1", "a1", "2")})

	got := s.Lookup("d1", []string{"a1"})
	if len(got) != 1 {
		t.Fatalf("want 1 point, got %d", len(got))
	}
	if got["a1"].Value != "2" {
		t.Errorf("value = %q, want %q", got["a1"].Value, "2")
	}
	if got["a1"].Label != "标签-a1" {
		t.Errorf("label = %q, want %q", got["a1"].Label, "标签-a1")
	}
	if got["a1"].UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set")
	}
}

// Lookup 只返回被点名的点位：未采集的点位与其它设备的同名点位都不应出现。
func TestStoreLookupSelective(t *testing.T) {
	s := NewStore()
	s.PushRecords([]collector.CollectedRecord{
		rec("d1", "a1", "1"),
		rec("d1", "a2", "2"),
		rec("d2", "a1", "other"),
	})

	got := s.Lookup("d1", []string{"a1", "a2", "a3"})
	if len(got) != 2 {
		t.Fatalf("want 2 points, got %d: %v", len(got), got)
	}
	if got["a1"].Value != "1" {
		t.Errorf("d1/a1 = %q, want %q", got["a1"].Value, "1")
	}
	if _, ok := got["a3"]; ok {
		t.Error("未采集的点位不应出现在结果里")
	}

	// 另一台设备的同名点位不得串台
	if other := s.Lookup("d2", []string{"a1"}); other["a1"].Value != "other" {
		t.Errorf("d2/a1 = %q, want %q", other["a1"].Value, "other")
	}
}

// 未知设备与空点位列表都应安全返回。
func TestStoreLookupEmpty(t *testing.T) {
	s := NewStore()
	if got := s.Lookup("nope", []string{"a1"}); got != nil {
		t.Errorf("unknown device: want nil, got %v", got)
	}
	if got := s.Lookup("nope", nil); got != nil {
		t.Errorf("no address ids: want nil, got %v", got)
	}
	if got := s.Lookup("nope", []string{}); got != nil {
		t.Errorf("empty address ids: want nil, got %v", got)
	}
}

// 空批次不应建出空设备条目（否则 Store 会为无数据的设备留下垃圾键）。
func TestStorePushEmptyIsNoop(t *testing.T) {
	s := NewStore()
	s.PushRecords(nil)
	s.PushRecords([]collector.CollectedRecord{})

	s.mu.RLock()
	n := len(s.data)
	s.mu.RUnlock()
	if n != 0 {
		t.Errorf("want 0 devices, got %d", n)
	}
}

// 采集协程写与 HTTP 协程读并发时不得发生数据竞争（配合 -race 运行）。
func TestStoreConcurrentAccess(t *testing.T) {
	s := NewStore()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.PushRecords([]collector.CollectedRecord{rec("d1", "a1", "v")})
			}
		}()
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.Lookup("d1", []string{"a1", "a2"})
			}
		}()
	}
	wg.Wait()
}
