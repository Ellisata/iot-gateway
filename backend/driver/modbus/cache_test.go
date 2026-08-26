package modbus

import (
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// TestModbusPlanCache 批次区间规划缓存命中/新增行为（无需客户端，直接测 planFor）。
func TestModbusPlanCache(t *testing.T) {
	d := &modbusDriver{}
	cfg := DefaultModbusTcpConfig()

	addrs := []po.DeviceAddress{
		{ID: "1", Name: "40001", DataType: "int16"},
		{ID: "2", Name: "40003", DataType: "int16"},
	}
	p1, err := d.plans.planFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("first planFor = %v", err)
	}
	if n := len(d.plans.plans); n != 1 {
		t.Fatalf("plan cache entries after first call = %d, want 1", n)
	}

	p2, err := d.plans.planFor(driver.RangeSig(addrs), addrs, cfg)
	if err != nil {
		t.Fatalf("second planFor = %v", err)
	}
	if n := len(d.plans.plans); n != 1 {
		t.Errorf("plan cache entries after same-batch second call = %d, want 1 (cache miss)", n)
	}
	if p1 != p2 {
		t.Errorf("cache hit should return the same plan")
	}

	// 不同批次 → 新增条目
	other := []po.DeviceAddress{{ID: "3", Name: "00001", DataType: "bool"}}
	if _, err := d.plans.planFor(driver.RangeSig(other), other, cfg); err != nil {
		t.Fatalf("other planFor = %v", err)
	}
	if n := len(d.plans.plans); n != 2 {
		t.Errorf("plan cache entries after different batch = %d, want 2", n)
	}
}
