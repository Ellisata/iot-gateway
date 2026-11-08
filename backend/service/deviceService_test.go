// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"testing"

	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
)

// TestBuildDeviceOverview 覆盖 buildDeviceOverview 纯函数的统计口径：
// 总数（启用+禁用）/启用/禁用/已接入采集/在线/离线/未接入采集/在线率。
func TestBuildDeviceOverview(t *testing.T) {
	tests := []struct {
		name          string
		enabled       []string
		disabled      []string
		offline       []string
		collected     []string
		wantTotal     int
		wantEnabled   int
		wantDisabled  int
		wantCollected int
		wantOnline    int
		wantOffline   int
		wantUnCollect int
		wantRate      float64
	}{
		{
			name:          "空集",
			enabled:       nil,
			disabled:      nil,
			offline:       nil,
			collected:     nil,
			wantTotal:     0,
			wantEnabled:   0,
			wantDisabled:  0,
			wantCollected: 0,
			wantOnline:    0,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      0,
		},
		{
			name:          "全部在线无禁用",
			enabled:       []string{"a", "b"},
			disabled:      nil,
			offline:       nil,
			collected:     []string{"a", "b"},
			wantTotal:     2,
			wantEnabled:   2,
			wantDisabled:  0,
			wantCollected: 2,
			wantOnline:    2,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      1,
		},
		{
			name:          "混合含未接入采集和禁用",
			enabled:       []string{"a", "b", "c", "d"},
			disabled:      []string{"e"},
			offline:       []string{"b"},
			collected:     []string{"a", "b", "c"},
			wantTotal:     5,
			wantEnabled:   4,
			wantDisabled:  1,
			wantCollected: 3,
			wantOnline:    2,
			wantOffline:   1,
			wantUnCollect: 1,
			wantRate:      float64(2) / 3,
		},
		{
			name:          "一台接入采集在线一台禁用",
			enabled:       []string{"a", "b"},
			disabled:      []string{"c"},
			offline:       nil,
			collected:     []string{"a"},
			wantTotal:     3,
			wantEnabled:   2,
			wantDisabled:  1,
			wantCollected: 1,
			wantOnline:    1,
			wantOffline:   0,
			wantUnCollect: 1,
			wantRate:      1,
		},
		{
			name:          "全部禁用不参与在线率",
			enabled:       nil,
			disabled:      []string{"a", "b"},
			offline:       nil,
			collected:     nil,
			wantTotal:     2,
			wantEnabled:   0,
			wantDisabled:  2,
			wantCollected: 0,
			wantOnline:    0,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      0,
		},
		{
			name:          "未接入采集的设备不拉低在线率",
			enabled:       []string{"a", "b", "c"},
			disabled:      nil,
			offline:       []string{"a"},
			collected:     []string{"a"},
			wantTotal:     3,
			wantEnabled:   3,
			wantDisabled:  0,
			wantCollected: 1,
			wantOnline:    0,
			wantOffline:   1,
			wantUnCollect: 2,
			wantRate:      0,
		},
		{
			name:          "collected 含未启用设备被防御性剔除",
			enabled:       []string{"a"},
			disabled:      []string{"b"},
			offline:       nil,
			collected:     []string{"a", "x"},
			wantTotal:     2,
			wantEnabled:   1,
			wantDisabled:  1,
			wantCollected: 1,
			wantOnline:    1,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      1,
		},
		{
			name:          "offline 指向未启用/未采集设备不计入",
			enabled:       []string{"a", "b"},
			disabled:      []string{"x"},
			offline:       []string{"x"},
			collected:     []string{"a", "b"},
			wantTotal:     3,
			wantEnabled:   2,
			wantDisabled:  1,
			wantCollected: 2,
			wantOnline:    2,
			wantOffline:   0,
			wantUnCollect: 0,
			wantRate:      1,
		},
		{
			name:          "collected 为 0 时在线率为 0",
			enabled:       []string{"a"},
			disabled:      nil,
			offline:       nil,
			collected:     nil,
			wantTotal:     1,
			wantEnabled:   1,
			wantDisabled:  0,
			wantCollected: 0,
			wantOnline:    0,
			wantOffline:   0,
			wantUnCollect: 1,
			wantRate:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enabledSet := toSet(tt.enabled)
			disabledSet := toSet(tt.disabled)
			offlineSet := toSet(tt.offline)
			collectedSet := toSet(tt.collected)

			got := buildDeviceOverview(enabledSet, disabledSet, offlineSet, collectedSet)

			if got.Total != tt.wantTotal ||
				got.Enabled != tt.wantEnabled ||
				got.Disabled != tt.wantDisabled ||
				got.Collected != tt.wantCollected ||
				got.Online != tt.wantOnline ||
				got.Offline != tt.wantOffline ||
				got.UnCollected != tt.wantUnCollect {
				t.Fatalf("mismatch: got %+v", got)
			}
			// 在线率保留 2 位小数，与实现一致
			if got.OnlineRate != float64(int(tt.wantRate*100+0.5))/100 {
				t.Fatalf("onlineRate mismatch: got %v, want %v", got.OnlineRate, tt.wantRate)
			}
		})
	}
}

// TestPageDeviceSearchesNameOrScanIdentity 设备列表只有一个检索框，关键词要同时
// 匹配名称与扫描身份签名（`协议|主机|端口|站号`）：用户输入的是"192.168.1.10"
// 还是"1号风机"从形态上就能分辨，不该逼他先选检索字段。
// 同时钉住 scanIdentity 会随列表回传 —— 前端把它显示在名称下方，
// 不返回的话这个字段在页面上永远是空的。
func TestPageDeviceSearchesNameOrScanIdentity(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	rows := []po.Device{
		{ID: "dev-scan", Name: "1号风机", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1,
			ScanIdentity: "Modbus.TCP|192.168.1.10|502|1"},
		{ID: "dev-manual", Name: "2号水泵", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed devices: %v", err)
	}

	tests := []struct {
		name    string
		keyword string
		want    []string
	}{
		{name: "按名称", keyword: "水泵", want: []string{"dev-manual"}},
		{name: "按扫描身份的 IP", keyword: "192.168.1.10", want: []string{"dev-scan"}},
		{name: "按扫描身份的协议前缀", keyword: "Modbus.TCP", want: []string{"dev-scan"}},
		{name: "空关键词不过滤", keyword: "", want: []string{"dev-scan", "dev-manual"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := svc.PageDevice(ctx, &dto.PageDeviceDTO{Page: 1, Size: 10, Name: tt.keyword})
			if err != nil {
				t.Fatalf("PageDevice: %v", err)
			}
			if int(page.Total) != len(tt.want) {
				t.Fatalf("total = %d, want %d", page.Total, len(tt.want))
			}
			got := make(map[string]string, len(page.Records))
			for _, r := range page.Records {
				got[r.ID] = r.ScanIdentity
			}
			for _, id := range tt.want {
				if _, ok := got[id]; !ok {
					t.Fatalf("缺少设备 %s，got %v", id, got)
				}
			}
			if v, present := got["dev-scan"]; present && v != "Modbus.TCP|192.168.1.10|502|1" {
				t.Errorf("scanIdentity = %q, want 原样回传", v)
			}
			// 手工建的设备没有扫描身份：回传空串，前端据此不渲染第二行。
			if v, present := got["dev-manual"]; present && v != "" {
				t.Errorf("手工设备的 scanIdentity = %q, want 空串", v)
			}
		})
	}
}

// TestUpdateDeviceRecomputesScanIdentityOnRealConfigChange 扫描身份只在连接
// 配置**真的变了**时才重算；只改名字/描述时必须保留。
// 重算只认 host/port/unitId/dstNode；认不出 host 时写空串，空签名不参与判重。
func TestUpdateDeviceRecomputesScanIdentityOnRealConfigChange(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	for _, protocol := range []po.IotProtocol{
		{ID: "proto-1", Name: "ModBus.TCP"},
		{ID: "proto-2", Name: "OPC.UA"},
		{ID: "proto-3", Name: "ModBus.RTU"},
	} {
		if err := db.Create(&protocol).Error; err != nil {
			t.Fatalf("seed protocol: %v", err)
		}
	}
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	const identity = "ModBus.TCP|192.168.1.10|502|0"
	dev := po.Device{
		ID: "dev-scan", Name: "1号风机", ProtocolID: "proto-1",
		ProtocolJSON: `{"host":"192.168.1.10","port":502}`, Status: 1,
		ScanIdentity: identity,
	}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}

	scanIdentityOfRow := func(id string) string {
		t.Helper()
		var row po.Device
		if err := db.Where("id = ?", id).First(&row).Error; err != nil {
			t.Fatalf("reload device: %v", err)
		}
		return row.ScanIdentity
	}

	// ① 只改名，配置原样回填（键顺序与空白都变了）→ 签名必须保留
	if err := svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID:           dev.ID,
		Name:         "1号风机-改名",
		ProtocolID:   "proto-1",
		ProtocolJSON: json.RawMessage(`{ "port": 502, "host": "192.168.1.10" }`),
	}); err != nil {
		t.Fatalf("UpdateDevice(改名): %v", err)
	}
	if got := scanIdentityOfRow(dev.ID); got != identity {
		t.Fatalf("改名后 scanIdentity = %q, want 保留 %q", got, identity)
	}

	// ② 连接配置真的改了 → 按新地址重算
	if err := svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID:           dev.ID,
		ProtocolID:   "proto-1",
		ProtocolJSON: json.RawMessage(`{"host":"192.168.1.11","port":502}`),
	}); err != nil {
		t.Fatalf("UpdateDevice(改地址): %v", err)
	}
	if got, want := scanIdentityOfRow(dev.ID), "ModBus.TCP|192.168.1.11|502|0"; got != want {
		t.Fatalf("改地址后 scanIdentity = %q, want %q", got, want)
	}

	// ③ 协议换了 → 签名里的协议名跟着换
	if err := svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID:           dev.ID,
		ProtocolID:   "proto-2",
		ProtocolJSON: json.RawMessage(`{"host":"192.168.1.11","port":4840}`),
	}); err != nil {
		t.Fatalf("UpdateDevice(换协议): %v", err)
	}
	if got, want := scanIdentityOfRow(dev.ID), "OPC.UA|192.168.1.11|4840|0"; got != want {
		t.Fatalf("换协议后 scanIdentity = %q, want %q", got, want)
	}

	// ④ 新配置没有 host（串口协议）→ 写空串
	if err := svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID:           dev.ID,
		ProtocolID:   "proto-3",
		ProtocolJSON: json.RawMessage(`{"comPort":"COM1","unitId":1}`),
	}); err != nil {
		t.Fatalf("UpdateDevice(串口): %v", err)
	}
	if got := scanIdentityOfRow(dev.ID); got != "" {
		t.Fatalf("串口配置后 scanIdentity = %q, want 空串", got)
	}

	// ⑤ 手工建设备原本没有签名，首次改连接配置后应补上
	manual := po.Device{
		ID: "dev-manual", Name: "手工设备", ProtocolID: "proto-1",
		ProtocolJSON: `{}`, Status: 1,
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatalf("seed manual device: %v", err)
	}
	if err := svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{
		ID:           manual.ID,
		ProtocolID:   "proto-1",
		ProtocolJSON: json.RawMessage(`{"host":"10.0.0.9","port":502}`),
	}); err != nil {
		t.Fatalf("UpdateDevice(手工设备): %v", err)
	}
	if got, want := scanIdentityOfRow(manual.ID), "ModBus.TCP|10.0.0.9|502|0"; got != want {
		t.Fatalf("手工设备 scanIdentity = %q, want %q", got, want)
	}
}

// TestCreateDeviceDerivesScanIdentity 手工创建设备时也按连接参数生成扫描身份；
// 串口协议没有 host，保持空串。
func TestCreateDeviceDerivesScanIdentity(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	for _, protocol := range []po.IotProtocol{
		{ID: "proto-1", Name: "ModBus.TCP"},
		{ID: "proto-3", Name: "ModBus.RTU"},
	} {
		if err := db.Create(&protocol).Error; err != nil {
			t.Fatalf("seed protocol: %v", err)
		}
	}
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	if err := svc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code:         "tcp-1",
		Name:         "TCP 设备",
		ProtocolID:   "proto-1",
		ProtocolJSON: json.RawMessage(`{"host":"10.0.0.8","port":502,"unitId":2}`),
	}); err != nil {
		t.Fatalf("create tcp device: %v", err)
	}
	var tcp po.Device
	if err := db.Where("name = ?", "TCP 设备").First(&tcp).Error; err != nil {
		t.Fatal(err)
	}
	if want := "ModBus.TCP|10.0.0.8|502|2"; tcp.ScanIdentity != want {
		t.Errorf("TCP scanIdentity = %q, want %q", tcp.ScanIdentity, want)
	}

	if err := svc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code:         "rtu-1",
		Name:         "RTU 设备",
		ProtocolID:   "proto-3",
		ProtocolJSON: json.RawMessage(`{"comPort":"COM1","unitId":1}`),
	}); err != nil {
		t.Fatalf("create rtu device: %v", err)
	}
	var rtu po.Device
	if err := db.Where("name = ?", "RTU 设备").First(&rtu).Error; err != nil {
		t.Fatal(err)
	}
	if rtu.ScanIdentity != "" {
		t.Errorf("RTU scanIdentity = %q, want 空串", rtu.ScanIdentity)
	}
}

// TestCreateDeviceCodeRules 覆盖设备编码：创建必填、全局唯一、格式校验、
// 创建后不可修改，以及列表检索能命中编码。
func TestCreateDeviceCodeRules(t *testing.T) {
	db := newDeviceAddressImportTestDB(t)
	svc := NewDeviceService(db, nil, nil)
	ctx := context.Background()

	if err := svc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code: "plc-line-a", Name: "A线PLC", ProtocolID: "proto-1", ProtocolJSON: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// 编码重复（名称不同也必须拒绝）。
	err := svc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code: "plc-line-a", Name: "另一台PLC", ProtocolID: "proto-1", ProtocolJSON: json.RawMessage(`{}`),
	})
	if got := errCode(t, err); got != enums.DeviceCodeExistsEnum.GetCode() {
		t.Errorf("duplicate code = %s, want %s", got, enums.DeviceCodeExistsEnum.GetCode())
	}

	// 编码格式不合法。
	err = svc.CreateDevice(ctx, &dto.CreateDeviceDTO{
		Code: "1bad", Name: "坏编码PLC", ProtocolID: "proto-1", ProtocolJSON: json.RawMessage(`{}`),
	})
	if got := errCode(t, err); got != enums.DeviceCodeInvalidEnum.GetCode() {
		t.Errorf("invalid code = %s, want %s", got, enums.DeviceCodeInvalidEnum.GetCode())
	}

	var device po.Device
	if err := db.Where("code = ?", "plc-line-a").First(&device).Error; err != nil {
		t.Fatalf("load device: %v", err)
	}

	// 创建后不可修改；原样回传不算修改。
	err = svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{ID: device.ID, Code: "plc-line-b"})
	if got := errCode(t, err); got != enums.DeviceCodeImmutableEnum.GetCode() {
		t.Errorf("immutable code = %s, want %s", got, enums.DeviceCodeImmutableEnum.GetCode())
	}
	if err := svc.UpdateDevice(ctx, &dto.UpdateDeviceDTO{ID: device.ID, Code: "plc-line-a", Name: "A线PLC-改名"}); err != nil {
		t.Fatalf("update with same code: %v", err)
	}

	// 列表检索命中编码。
	page, err := svc.PageDevice(ctx, &dto.PageDeviceDTO{Page: 1, Size: 10, Name: "plc-line-a"})
	if err != nil {
		t.Fatalf("PageDevice: %v", err)
	}
	if page.Total != 1 || page.Records[0].Code != "plc-line-a" {
		t.Fatalf("page = %+v", page)
	}
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
