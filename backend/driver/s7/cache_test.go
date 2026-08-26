package s7

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// TestS7DriverRangeCache 区间指纹缓存命中/新增行为（无需客户端，直接测 rangesFor）。
func TestS7DriverRangeCache(t *testing.T) {
	d := &s7Driver{}
	cfg := DefaultS7Config()

	addrs := []po.DeviceAddress{
		{ID: "1", Name: "DB1.DBW0", DataType: "int"},
		{ID: "2", Name: "DB1.DBW2", DataType: "int"},
	}
	r1, err := d.rangesFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("first rangesFor = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Fatalf("range cache entries after first call = %d, want 1", n)
	}

	r2, err := d.rangesFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("second rangesFor = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Errorf("range cache entries after same-batch second call = %d, want 1 (cache miss)", n)
	}
	if &r1[0] != &r2[0] {
		t.Errorf("cache hit should return the same underlying slice")
	}

	// 不同批次 → 新增条目
	other := []po.DeviceAddress{{ID: "3", Name: "M0.0", DataType: "bool"}}
	if _, err := d.rangesFor(driver.RangeSig(other), other, cfg); err != nil {
		t.Fatalf("other rangesFor = %v", err)
	}
	if n := len(d.rangeCache); n != 2 {
		t.Errorf("range cache entries after different batch = %d, want 2", n)
	}
}
