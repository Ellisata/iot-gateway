// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"testing"

	"iot-gateway/model/po"
)

// TestDriverReadRangeCached 同批点位第二次 Read 命中区间指纹缓存，不再重复计算。
// 缓存只跳过区间计算，不跳过传输层 I/O（每轮仍按区间读取）。
func TestDriverReadRangeCached(t *testing.T) {
	cfg := DefaultFINSConfig()
	cfg.MergeWindow = 10
	readCalls := 0
	mt := &mockTransport{fn: func(area finsArea, word, count uint16) ([]byte, error) {
		readCalls++
		return make([]byte, int(count)*2), nil
	}}
	d := &finsDriver{config: cfg, client: mt}

	addrs := []po.DeviceAddress{
		mkAddr("1", "D100", "int16"),
		mkAddr("2", "D102", "int16"),
	}
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("first Read = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Fatalf("range cache entries after first read = %d, want 1", n)
	}

	// 同批内容：应命中缓存，缓存条数不增
	if _, err := d.Read(addrs); err != nil {
		t.Fatalf("second Read = %v", err)
	}
	if n := len(d.rangeCache); n != 1 {
		t.Errorf("range cache entries after same-batch second read = %d, want 1 (cache miss)", n)
	}

	// 不同批次：新增条目
	other := []po.DeviceAddress{mkAddr("3", "D110", "int16")}
	if _, err := d.Read(other); err != nil {
		t.Fatalf("other Read = %v", err)
	}
	if n := len(d.rangeCache); n != 2 {
		t.Errorf("range cache entries after different batch = %d, want 2", n)
	}

	// 传输层读取仍按轮执行（缓存只跳过区间计算，不跳过 I/O）
	if readCalls != 3 {
		t.Errorf("transport read calls = %d, want 3", readCalls)
	}
}
