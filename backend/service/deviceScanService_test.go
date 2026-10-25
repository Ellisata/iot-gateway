// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"iot-gateway/appError"
	"iot-gateway/configFile"
	"iot-gateway/devicescan"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
)

// ==================== 替身 ====================

type fakeScanSpecs struct {
	specs []driver.ProtocolDiscovery
}

func (f *fakeScanSpecs) DiscoverySpecs() []driver.ProtocolDiscovery { return f.specs }

// scriptedProber 假的探测实现。
//
//	gate        非 nil 时探测先等它放行，用来把"扫描正在进行中"变成可断言的稳定状态；
//	respondHost 非空时**只**对这个目标产出设备，其余一律返回空。
//	            没有它是没法写断言的：一个网段有 5 个目标，
//	            每个都产出同样的结果会让 Found 变成 5 倍，与被测逻辑无关。
type scriptedProber struct {
	devices     []driver.DiscoveredDevice
	respondHost string
	err         error
	gate        chan struct{}
}

func (p *scriptedProber) ProbeTargets(ctx context.Context, protocol string, options json.RawMessage,
	target driver.ScanTarget) (*driversdk.ProbeReply, error) {

	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.respondHost != "" && target.Host != p.respondHost {
		// 目标上没设备：返回空列表而不是错误（"没扫到"不是错误）。
		return &driversdk.ProbeReply{}, nil
	}
	out := make([]driver.DiscoveredDevice, len(p.devices))
	copy(out, p.devices)
	for i := range out {
		out[i].Host = target.Host
	}
	return &driversdk.ProbeReply{Devices: out}, nil
}

const (
	scanTestProtocol = "Test.Scan.Service"
	scanTestPort     = 502
	// testRespondHost 假探测唯一会产出设备的目标（testScanSubnets 展开后的第二个）。
	testRespondHost = "10.0.0.2"
)

// testScanSubnets 测试用的确定性网段：10.0.0.0/29，本机地址 .1（也在目标里），
// 因此展开后是 .1-.6 共 6 个目标，第一个是 .1。
func testScanSubnets() []devicescan.SubnetInfo {
	return []devicescan.SubnetInfo{{
		Iface: "eth0", CIDR: "10.0.0.0/29", LocalIP: "10.0.0.1",
		Broadcast: "10.0.0.7", HostCount: 6,
	}}
}

// newScanFixture 组装一个扫描服务：假协议能力 + 假探测实现 + 临时库。
func newScanFixture(t *testing.T, devices []driver.DiscoveredDevice, proberErr error) (*DeviceScanService, *gorm.DB) {
	t.Helper()
	db := newImportTestDB(t)

	specs := &fakeScanSpecs{specs: []driver.ProtocolDiscovery{{
		Protocol: scanTestProtocol,
		Spec: driver.DiscoverySpec{
			Title:       "测试扫描",
			DefaultPort: scanTestPort,
			TargetMode:  driver.TargetModeUnicast,
			MaxBudgetMs: 1000,
		},
	}}}
	engine := devicescan.NewEngine(&configFile.Config{
		DeviceScan: configFile.DeviceScanConfig{Enabled: true},
	}, specs, &scriptedProber{
		devices:     devices,
		respondHost: testRespondHost, // 只在这一个目标上产出设备
		err:         proberErr,
	}).
		// 本机网卡因机器而异，跨包测试必须注入确定性网段。
		WithSubnetSource(testScanSubnets)
	t.Cleanup(engine.Stop)

	return NewDeviceScanService(db, engine, nil, nil), db
}

// runScan 发起一次扫描并等它结束。
func runScan(t *testing.T, svc *DeviceScanService) *vo.ScanJobVO {
	t.Helper()
	job, err := svc.Start(context.Background(), &dto.StartDeviceScanDTO{Protocol: scanTestProtocol})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Status(context.Background(), job.JobID, true)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if got.State != "running" {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("扫描没有结束")
	return nil
}

// ==================== Capabilities ====================

// TestScanCapabilitiesJoinsProtocolRow 能力接口要把插件侧的协议名与库里的
// iot_protocol 主键对上 —— 前端建完设备要跳设备列表，导入时也要定位协议行。
func TestScanCapabilitiesJoinsProtocolRow(t *testing.T) {
	svc, db := newScanFixture(t, nil, nil)

	if err := db.Create(&po.IotProtocol{
		ID: "proto-1", Name: scanTestProtocol, Description: "测试",
	}).Error; err != nil {
		t.Fatalf("建协议行: %v", err)
	}

	caps, err := svc.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Enabled {
		t.Error("Enabled 应为 true")
	}
	if len(caps.Protocols) != 1 {
		t.Fatalf("协议数 = %d，期望 1", len(caps.Protocols))
	}
	p := caps.Protocols[0]
	if p.Name != scanTestProtocol || p.ProtocolID != "proto-1" {
		t.Errorf("协议 = %+v，期望 name=%s id=proto-1", p, scanTestProtocol)
	}
	if p.DefaultPort != scanTestPort {
		t.Errorf("默认端口 = %d", p.DefaultPort)
	}
	// 网段来自引擎（测试注入的那一个）。
	if len(caps.Subnets) != 1 || caps.Subnets[0].CIDR != "10.0.0.0/29" {
		t.Errorf("网段 = %+v", caps.Subnets)
	}
}

// TestScanCapabilitiesWithoutProtocolRow 协议行缺失时 ProtocolID 留空而不是报错：
// 前端据此把该项标成不可用 —— 比整个页面打不开好。
func TestScanCapabilitiesWithoutProtocolRow(t *testing.T) {
	svc, _ := newScanFixture(t, nil, nil)

	caps, err := svc.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(caps.Protocols) != 1 {
		t.Fatalf("协议数 = %d，期望 1", len(caps.Protocols))
	}
	if caps.Protocols[0].ProtocolID != "" {
		t.Errorf("协议行不存在时 ProtocolID 应留空，实际 %q", caps.Protocols[0].ProtocolID)
	}
}

// TestScanDisabledReportsBusinessCode 功能关闭要走业务错误码而不是通用错误。
func TestScanDisabledReportsBusinessCode(t *testing.T) {
	db := newImportTestDB(t)
	engine := devicescan.NewEngine(&configFile.Config{
		DeviceScan: configFile.DeviceScanConfig{Enabled: false},
	}, &fakeScanSpecs{}, &scriptedProber{})
	t.Cleanup(engine.Stop)
	svc := NewDeviceScanService(db, engine, nil, nil)

	_, err := svc.Start(context.Background(), &dto.StartDeviceScanDTO{Protocol: scanTestProtocol})
	assertBusinessCode(t, err, enums.DeviceScanDisabledEnum.GetCode())
}

// TestScanUnsupportedProtocolReportsBusinessCode "该协议不支持扫描"必须与
// "参数错误"区分开 —— 用户的下一步动作完全不同（换协议 vs 改网段）。
func TestScanUnsupportedProtocolReportsBusinessCode(t *testing.T) {
	svc, _ := newScanFixture(t, nil, nil)

	_, err := svc.Start(context.Background(), &dto.StartDeviceScanDTO{Protocol: "No.Such.Protocol"})
	assertBusinessCode(t, err, enums.DeviceScanUnsupportedEnum.GetCode())
}

func TestScanStatusUnknownJobReportsBusinessCode(t *testing.T) {
	svc, _ := newScanFixture(t, nil, nil)

	_, err := svc.Status(context.Background(), "scan-404", true)
	assertBusinessCode(t, err, enums.DeviceScanJobNotFoundEnum.GetCode())
}

// TestScanProgressOmitsDevices 轻量快照（withDevices=false）**必须把 Devices 与
// Errors 留成 nil**，而不是空数组。
//
// 这不是洁癖：前端据"是不是数组"决定保留还是清空自己持有的那份清单。服务层把
// nil 归一成空数组（scanJobVO 里一个 make 就会），前端的轮询就会在每个拍子上
// 把已经扫到的设备抹掉，症状是"结果表一直空着，扫完了才一次性冒出来"。
func TestScanProgressOmitsDevices(t *testing.T) {
	svc, _ := newScanFixture(t, []driver.DiscoveredDevice{discovered("10.0.0.2", 1)}, nil)
	job := runScan(t, svc)

	full, err := svc.Status(context.Background(), job.JobID, true)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if full.Devices == nil {
		t.Fatal("完整快照的 Devices 必须是数组（哪怕为空）—— nil 是轻量快照的标记")
	}
	if len(full.Devices) == 0 {
		t.Fatal("这个夹具应当扫到设备，否则下面的计数断言没有意义")
	}

	light, err := svc.Status(context.Background(), job.JobID, false)
	if err != nil {
		t.Fatalf("Status(withDevices=false): %v", err)
	}
	if light.Devices != nil || light.Errors != nil {
		t.Errorf("轻量快照不该带结果清单：devices=%d errors=%d",
			len(light.Devices), len(light.Errors))
	}
	// 计数与状态必须照常给全 —— 前端就是靠 found 判断要不要补一次完整请求。
	if light.Found != full.Found || light.Scanned != full.Scanned || light.State != full.State {
		t.Errorf("轻量快照的计数应与完整快照一致：light=%+v full=%+v", light, full)
	}
}

// ==================== CreateDevices ====================

// discovered 造一条扫描结果。
func discovered(host string, unitID int) driver.DiscoveredDevice {
	return driver.DiscoveredDevice{
		Host:         host,
		Port:         scanTestPort,
		UnitID:       unitID,
		Name:         "假 PLC",
		ProtocolJSON: `{"host":"` + host + `","port":502,"unitId":1}`,
	}
}

// setupProtocolRow 建好协议行，使 validateImportRow 的协议校验能通过。
func setupProtocolRow(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: scanTestProtocol}).Error; err != nil {
		t.Fatalf("建协议行: %v", err)
	}
}

// TestCreateDevicesFromScan 正常路径：勾选两条，建成两行，连接配置来自服务端
// 持有的那份扫描结果。
func TestCreateDevicesFromScan(t *testing.T) {
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{
		discovered("10.0.0.2", 1),
		discovered("10.0.0.3", 2),
	}, nil)
	setupProtocolRow(t, db)

	job := runScan(t, svc)
	if job.Found != 2 {
		t.Fatalf("扫描应发现 2 台，实际 %d 台", job.Found)
	}

	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{
			{Index: 0, Name: "1号机", Description: "车间A"},
			{Index: 1, Name: "2号机"},
		},
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Total != 2 || result.Success != 2 || result.Failed != 0 {
		t.Fatalf("结果 = %+v，期望 2/2/0", result)
	}

	var rows []po.Device
	if err := db.Order("name").Find(&rows).Error; err != nil {
		t.Fatalf("查设备: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("库里设备数 = %d，期望 2", len(rows))
	}
	if rows[0].Name != "1号机" || rows[0].ProtocolID != "proto-1" {
		t.Errorf("第 0 行 = %+v", rows[0])
	}
	if rows[0].Status != 1 {
		t.Errorf("新建设备应为启用，实际 status=%d", rows[0].Status)
	}
	// 连接配置必须是服务端从扫描结果里取的那一份，与插件给的一致。
	var cfg struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal([]byte(rows[0].ProtocolJSON), &cfg); err != nil {
		t.Fatalf("protocolJson 不是合法 JSON: %q", rows[0].ProtocolJSON)
	}
	if cfg.Host != "10.0.0.2" {
		t.Errorf("连接配置里的地址 = %q，期望 10.0.0.2", cfg.Host)
	}
}

// TestCreateDevicesDefaultsName 用户没填名字时按 IP（多站号带站号）兜底，
// 且同一 IP 的两个站号不会互相撞名。
func TestCreateDevicesDefaultsName(t *testing.T) {
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{
		discovered("10.0.0.2", 1),
		discovered("10.0.0.2", 3),
	}, nil)
	setupProtocolRow(t, db)

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{{Index: 0}, {Index: 1}},
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Success != 2 {
		t.Fatalf("应成功 2 条，实际 %+v", result)
	}

	var names []string
	if err := db.Model(&po.Device{}).Pluck("name", &names).Error; err != nil {
		t.Fatalf("查名字: %v", err)
	}
	// device.name 有唯一索引，同名会被判失败 —— 站号必须参与命名。
	want := map[string]bool{"10.0.0.2": true, "10.0.0.2-3": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("意外的默认名字 %q（期望 %v）", n, want)
		}
	}
	if len(names) != 2 {
		t.Errorf("库内设备数 = %d，期望 2", len(names))
	}
}

// TestCreateDevicesRejectsDuplicateNames 库内与批内重名都逐条报错，不中断整批。
func TestCreateDevicesRejectsDuplicateNames(t *testing.T) {
	// 站号必须各不相同：scriptedProber 会把 Host 统一覆写成探测目标的地址，
	// 因此给同一个站号就等于让三条结果**逐字段相同** —— 而引擎会按
	// host:port:unitId 去重（广播与逐主机两条路发现同一台设备时靠它合并），
	// 三条会被合成一条，这个用例考的就不是重名了。真实探测器的形态也是如此：
	// 一个目标产出多台设备时，区分它们的就是站号。
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{
		discovered("10.0.0.2", 1),
		discovered("10.0.0.3", 2),
		discovered("10.0.0.4", 3),
	}, nil)
	setupProtocolRow(t, db)
	// 库里先有一台同名的。
	if err := db.Create(&po.Device{
		Name: "已存在", ProtocolID: "proto-1", ProtocolJSON: "{}", Status: 1,
	}).Error; err != nil {
		t.Fatalf("预置设备: %v", err)
	}

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{
			{Index: 0, Name: "已存在"}, // 与库内撞名
			{Index: 1, Name: "新的"},  // 批内第一次出现
			{Index: 2, Name: "新的"},  // 批内重复
		},
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Total != 3 || result.Success != 1 || result.Failed != 2 {
		t.Fatalf("结果 = %+v，期望 3/1/2", result)
	}
	for _, e := range result.Errors {
		if e.Reason != enums.DeviceExistsEnum.GetMessage() {
			t.Errorf("失败原因 = %q，期望重名文案", e.Reason)
		}
	}
}

// TestCreateDevicesUnknownProtocolIsPerRowFailure 协议行缺失只该让**这一条**
// 失败，而不是整个请求 500 —— 与 Excel 导入的语义一致。
func TestCreateDevicesUnknownProtocolIsPerRowFailure(t *testing.T) {
	svc, _ := newScanFixture(t, []driver.DiscoveredDevice{discovered("10.0.0.2", 1)}, nil)
	// 刻意不建协议行。

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{{Index: 0, Name: "1号机"}},
	})
	if err != nil {
		t.Fatalf("该场景不该是请求级错误: %v", err)
	}
	if result.Success != 0 || result.Failed != 1 {
		t.Fatalf("结果 = %+v，期望 0 成功 1 失败", result)
	}
	if result.Errors[0].Reason == "" {
		t.Error("失败明细必须给出原因")
	}
}

// TestCreateDevicesRejectsBadProtocolJSON 插件给的连接配置不合法时逐条失败 ——
// 一份坏 JSON 写进 device 行之后，症状是"设备采不到值"，完全指不到根因。
func TestCreateDevicesRejectsBadProtocolJSON(t *testing.T) {
	bad := discovered("10.0.0.2", 1)
	bad.ProtocolJSON = "{不是 JSON"
	empty := discovered("10.0.0.3", 1)
	empty.ProtocolJSON = ""

	svc, db := newScanFixture(t, []driver.DiscoveredDevice{bad, empty}, nil)
	setupProtocolRow(t, db)

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{
			{Index: 0, Name: "坏配置"},
			{Index: 1, Name: "空配置"},
		},
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Success != 0 || result.Failed != 2 {
		t.Fatalf("结果 = %+v，期望两条都失败", result)
	}
	var count int64
	if err := db.Model(&po.Device{}).Count(&count).Error; err != nil {
		t.Fatalf("计数: %v", err)
	}
	if count != 0 {
		t.Errorf("坏配置不该入库，实际写入了 %d 行", count)
	}
}

// TestCreateDevicesRejectsOutOfRangeIndex 结果下标越界（如任务已被换过一轮）
// 要逐条报"请重新扫描"，而不是 panic。
func TestCreateDevicesRejectsOutOfRangeIndex(t *testing.T) {
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{discovered("10.0.0.2", 1)}, nil)
	setupProtocolRow(t, db)

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{{Index: 99, Name: "越界"}},
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Failed != 1 || result.Success != 0 {
		t.Fatalf("结果 = %+v", result)
	}
	if result.Errors[0].Reason == "" {
		t.Error("应给出可执行的原因")
	}
}

// TestCreateDevicesWhileRunningIsRejected 扫描还在跑时不许导入：
// 否则用户看到的结果列表还在变，而导入的只是某一瞬间的快照。
func TestCreateDevicesWhileRunningIsRejected(t *testing.T) {
	db := newImportTestDB(t)
	setupProtocolRow(t, db)

	gate := make(chan struct{})
	specs := &fakeScanSpecs{specs: []driver.ProtocolDiscovery{{
		Protocol: scanTestProtocol,
		Spec: driver.DiscoverySpec{
			Title: "测试扫描", DefaultPort: scanTestPort,
			TargetMode: driver.TargetModeUnicast, MaxBudgetMs: 1000,
		},
	}}}
	engine := devicescan.NewEngine(
		&configFile.Config{DeviceScan: configFile.DeviceScanConfig{Enabled: true}},
		specs, &scriptedProber{gate: gate}).WithSubnetSource(testScanSubnets)
	t.Cleanup(func() { close(gate); engine.Stop() })
	svc := NewDeviceScanService(db, engine, nil, nil)

	// 探测全部卡在 gate 上，于是任务必定停在 running。
	job, err := svc.Start(context.Background(), &dto.StartDeviceScanDTO{Protocol: scanTestProtocol})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.Status(context.Background(), job.JobID, true)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if got.State == "running" && got.Scanned == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	_, err = svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{{Index: 0, Name: "抢跑"}},
	})
	assertBusinessCode(t, err, enums.DeviceScanBusyEnum.GetCode())
}

// ==================== 扫描地址判重（软提示） ====================

// TestScanIdentityOfIsTupleNotAddress 判重键是「协议|主机|端口|站号」四元组，
// **不是**主机地址 —— 同一 IP 上多台设备是合法场景，只看 IP 会把它们误报成重复。
func TestScanIdentityOfIsTupleNotAddress(t *testing.T) {
	base := discovered("10.0.0.2", 1)
	base.Port = 502

	tests := []struct {
		label string
		proto string
		dev   driver.DiscoveredDevice
		want  string
	}{
		{"基准", scanTestProtocol, base, scanTestProtocol + "|10.0.0.2|502|1"},
		{
			// 同一个串口服务器后面的两个从站：必须算两台不同的设备。
			"同 IP 不同站号",
			scanTestProtocol,
			driver.DiscoveredDevice{Host: "10.0.0.2", Port: 502, UnitID: 3},
			scanTestProtocol + "|10.0.0.2|502|3",
		},
		{
			// 多通道串口服务器：同一个 IP 的两个端口也是两台设备。
			"同 IP 不同端口",
			scanTestProtocol,
			driver.DiscoveredDevice{Host: "10.0.0.2", Port: 4001, UnitID: 1},
			scanTestProtocol + "|10.0.0.2|4001|1",
		},
		{
			// 同一台 PLC 同时开 S7 与 Modbus：协议不同即不同签名。
			"同 IP 不同协议",
			"Siemens.S7",
			driver.DiscoveredDevice{Host: "10.0.0.2", Port: 502, UnitID: 1},
			"Siemens.S7|10.0.0.2|502|1",
		},
		{
			"主机大小写与空白归一",
			scanTestProtocol,
			driver.DiscoveredDevice{Host: " 10.0.0.2 ", Port: 502, UnitID: 1},
			scanTestProtocol + "|10.0.0.2|502|1",
		},
		{
			// 没有主机的结果「不该出现」，但插件是外部代码。空签名不参与比对 ——
			// 拿一群空串互相撞会把毫不相干的设备报成重复。
			"主机为空则无签名",
			scanTestProtocol,
			driver.DiscoveredDevice{Host: "   ", Port: 502, UnitID: 1},
			"",
		},
	}

	for _, tc := range tests {
		if got := scanIdentityOf(tc.proto, tc.dev); got != tc.want {
			t.Errorf("%s: scanIdentityOf = %q，期望 %q", tc.label, got, tc.want)
		}
	}
}

// TestCreateDevicesWarnsOnSameAddress 库内已有设备占了同一个扫描地址时，
// 默认**不建**这一行，而是给一条带结构化原因码的明细让用户确认。
//
// 断言这个码而不是只看文案：前端就是靠它决定弹不弹确认框的，文案改了码还在，
// 功能才不静默失效。
func TestCreateDevicesWarnsOnSameAddress(t *testing.T) {
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{discovered("10.0.0.2", 1)}, nil)
	setupProtocolRow(t, db)
	seedDeviceOnAddress(t, db, "老设备", discovered("10.0.0.2", 1))

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{{Index: 0, Name: "新设备"}},
	})
	if err != nil {
		t.Fatalf("地址重复是软提示，不该是请求级错误: %v", err)
	}
	if result.Success != 0 || result.Failed != 1 {
		t.Fatalf("结果 = %+v，期望 0 成功 1 失败（等用户确认）", result)
	}
	if got := result.Errors[0].Code; got != vo.ExcelImportErrCodeAddressExists {
		t.Errorf("原因码 = %q，期望 %q", got, vo.ExcelImportErrCodeAddressExists)
	}
	// 提示里要能看出是**哪台**设备占了这个地址，否则用户无从判断该不该继续。
	if !strings.Contains(result.Errors[0].Reason, "老设备") {
		t.Errorf("提示应点名占用该地址的设备，实际 %q", result.Errors[0].Reason)
	}

	var count int64
	if err := db.Model(&po.Device{}).Count(&count).Error; err != nil {
		t.Fatalf("计数: %v", err)
	}
	if count != 1 {
		t.Errorf("未确认时不该入库，实际 %d 行", count)
	}
}

// TestCreateDevicesConfirmDuplicateCreatesRow 用户确认后（ConfirmDuplicate）
// 同一地址照常建成，并且新行带上签名 —— 否则下次扫描又判不出来。
func TestCreateDevicesConfirmDuplicateCreatesRow(t *testing.T) {
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{discovered("10.0.0.2", 1)}, nil)
	setupProtocolRow(t, db)
	seedDeviceOnAddress(t, db, "老设备", discovered("10.0.0.2", 1))

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID:            job.JobID,
		Items:            []dto.ScanDeviceItemDTO{{Index: 0, Name: "新设备"}},
		ConfirmDuplicate: true,
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Success != 1 || result.Failed != 0 {
		t.Fatalf("结果 = %+v，期望 1 成功", result)
	}

	var created po.Device
	if err := db.Where("name = ?", "新设备").First(&created).Error; err != nil {
		t.Fatalf("查新设备: %v", err)
	}
	want := scanIdentityOf(scanTestProtocol, discovered("10.0.0.2", 1))
	if created.ScanIdentity != want {
		t.Errorf("新行签名 = %q，期望 %q", created.ScanIdentity, want)
	}
}

// TestCreateDevicesSameAddressDifferentUnitIsNotDuplicate 同一 IP 上的**不同站号**
// 是两台设备（Modbus TCP 串口服务器后面挂多个从站），不能弹确认。
//
// 这条与上一条是同一个判重键的两面，必须成对存在：只测"能报出来"的话，
// 把判重退化成"只比 IP"也能全绿。
func TestCreateDevicesSameAddressDifferentUnitIsNotDuplicate(t *testing.T) {
	svc, db := newScanFixture(t, []driver.DiscoveredDevice{discovered("10.0.0.2", 3)}, nil)
	setupProtocolRow(t, db)
	// 库里那台是同 IP 的站号 1。
	seedDeviceOnAddress(t, db, "站号1", discovered("10.0.0.2", 1))

	job := runScan(t, svc)
	result, err := svc.CreateDevices(context.Background(), &dto.CreateDevicesFromScanDTO{
		JobID: job.JobID,
		Items: []dto.ScanDeviceItemDTO{{Index: 0, Name: "站号3"}},
	})
	if err != nil {
		t.Fatalf("CreateDevices: %v", err)
	}
	if result.Success != 1 || result.Failed != 0 {
		t.Fatalf("同一 IP 的不同站号不是重复，结果 = %+v", result)
	}
}

// seedDeviceOnAddress 预置一台"已连在某扫描地址上"的设备（带签名的行只可能
// 由扫描建出来，测试里就只能这样造）。
func seedDeviceOnAddress(t *testing.T, db *gorm.DB, name string, dev driver.DiscoveredDevice) {
	t.Helper()
	if err := db.Create(&po.Device{
		Name:         name,
		ProtocolID:   "proto-1",
		ProtocolJSON: dev.ProtocolJSON,
		Status:       1,
		ScanIdentity: scanIdentityOf(scanTestProtocol, dev),
	}).Error; err != nil {
		t.Fatalf("预置设备 %s: %v", name, err)
	}
}

// ==================== 断言助手 ====================

// assertBusinessCode 断言 err 是带指定业务码的 AppError。
func assertBusinessCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("应返回业务错误码 %s，实际 nil", code)
	}
	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("错误类型不是 *AppError: %T (%v)", err, err)
	}
	if appErr.Code != code {
		t.Fatalf("业务码 = %s，期望 %s（错误：%v）", appErr.Code, code, err)
	}
}
