// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/devicescan"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/pluginhost"
	"iot-gateway/testutil/drivertest"
)

// ==================== 替身 ====================

// fakePointSource 假的点位扫描来源（生产实现是 *pluginhost.Manager）。
//
// 它记下自己收到了什么 —— "服务端从 device 行取 protocolJson"这条不变量
// 只有这样才验得到：客户端根本传不了这个值（DTO 里没有这个字段），
// 所以断言的对象只能是"管理器收到的那一份"。
type fakePointSource struct {
	specs []driver.ProtocolPointDiscovery

	gotProtocol     string
	gotProtocolJSON string
	gotOptions      json.RawMessage

	result *driver.PointScanResult
	err    error
}

func (f *fakePointSource) PointDiscoverySpecs() []driver.ProtocolPointDiscovery { return f.specs }

func (f *fakePointSource) DiscoverPoints(_ context.Context, protocol, protocolJSON string,
	options json.RawMessage) (*driver.PointScanResult, error) {

	f.gotProtocol = protocol
	f.gotProtocolJSON = protocolJSON
	f.gotOptions = options
	// err 与 result **可以同时非 nil**，这不是替身的宽容而是生产的实情：
	// 契约允许"带着已完成的部分结果返回 + error"（见 driversdk.FindPointsFunc
	// 第 3 条），宿主 pluginhost.DiscoverPoints 也照这条实现了。替身不能同时
	// 给出两者的话，"部分结果不许被丢掉"这条规则在服务层就永远测不到。
	if f.err != nil {
		return f.result, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &driver.PointScanResult{Points: []driver.DiscoveredPoint{}}, nil
}

// ==================== 夹具 ====================

const pointTestDeviceJSON = `{"host":"10.0.0.7","port":502,"unitId":3}`

// newPointScanDB 一个含设备/协议/地址三张表的临时库。
func newPointScanDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newImportTestDB(t)
	if err := db.AutoMigrate(&po.DeviceAddress{}); err != nil {
		t.Fatalf("migrate DeviceAddress: %v", err)
	}
	return db
}

// pointScanFixture 组装一个点位扫描服务：合成协议（类型可解析）+ 假点扫来源 + 临时库。
func pointScanFixture(t *testing.T, enabled bool, src *fakePointSource) (*DeviceScanService, *gorm.DB, *po.Device) {
	t.Helper()

	// 用 drivertest 的合成协议：这里验的是服务层的校验/去重/映射，
	// 不是某个真实协议的类型语义。
	drivertest.EnsureSyntheticProtocol()
	db := newPointScanDB(t)
	if err := db.Create(&po.IotProtocol{
		ID: "proto-1", Name: drivertest.TestProtocol,
	}).Error; err != nil {
		t.Fatalf("建协议行: %v", err)
	}
	dev := &po.Device{
		Name:         "测试设备",
		ProtocolID:   "proto-1",
		ProtocolJSON: pointTestDeviceJSON,
		Status:       1,
	}
	if err := db.Create(dev).Error; err != nil {
		t.Fatalf("建设备: %v", err)
	}

	engine := devicescan.NewEngine(&configFile.Config{
		DeviceScan: configFile.DeviceScanConfig{Enabled: enabled},
	}, &fakeScanSpecs{}, &scriptedProber{}).WithSubnetSource(testScanSubnets)
	t.Cleanup(engine.Stop)

	if src == nil {
		src = &fakePointSource{}
	}
	return NewDeviceScanService(db, engine, src, nil), db, dev
}

// pointSpecs 造一份该协议的点扫声明。
func pointSpecs(protocol string) []driver.ProtocolPointDiscovery {
	return []driver.ProtocolPointDiscovery{{
		Protocol: protocol,
		Spec: driver.PointDiscoverySpec{
			Title:                 "扫描寄存器范围",
			Description:           "列出读得到的地址",
			DefaultCommonDataType: "Short",
			MaxBudgetMs:           2000,
			MaxPoints:             100,
		},
	}}
}

// addrItem 造一条待创建的点位。
func addrItem(name string) dto.ScanAddressItemDTO {
	return dto.ScanAddressItemDTO{
		Name:           name,
		Label:          "标签-" + name,
		CommonDataType: "Short",
		RwPermission:   "R",
		ScanFrequency:  1000,
	}
}

// ==================== PointCapability ====================

// TestPointCapabilityReportsSupported 支持的协议要返回它的规格。
func TestPointCapabilityReportsSupported(t *testing.T) {
	src := &fakePointSource{specs: pointSpecs(drivertest.TestProtocol)}
	svc, _, _ := pointScanFixture(t, true, src)

	cap, err := svc.PointCapability(context.Background(), drivertest.TestProtocol)
	if err != nil {
		t.Fatalf("PointCapability: %v", err)
	}
	if !cap.Enabled || !cap.Supported {
		t.Fatalf("能力 = %+v，期望都可用", cap)
	}
	if cap.Spec == nil || cap.Spec.Title != "扫描寄存器范围" {
		t.Fatalf("规格 = %+v", cap.Spec)
	}
	if cap.Spec.DefaultCommonDataType != "Short" || cap.Spec.MaxBudgetMs != 2000 {
		t.Errorf("规格内容不对: %+v", cap.Spec)
	}
}

// TestPointCapabilityExposesEffectiveBudget 前端显示"大约要等多久"必须用
// **实际生效**的预算，不是插件声明的那个。
//
// 两个值可以差三倍（Modbus 声明 20000、真正生效 7000）。显示声明值时用户会按
// 20 秒预估，实际 7 秒就停了，而他会去怀疑设备与链路 —— 根因却在网关这条
// 同步 HTTP 的互锁上。声明值本身也要照样返回：它是插件的原始意图，排查时要看。
func TestPointCapabilityExposesEffectiveBudget(t *testing.T) {
	newSrc := func(declared int) *fakePointSource {
		specs := pointSpecs(drivertest.TestProtocol)
		specs[0].Spec.MaxBudgetMs = declared
		return &fakePointSource{specs: specs}
	}

	// 声明值在网关上限之内：原样生效。
	svc, _, _ := pointScanFixture(t, true, newSrc(2000))
	cap, err := svc.PointCapability(context.Background(), drivertest.TestProtocol)
	if err != nil {
		t.Fatalf("PointCapability: %v", err)
	}
	if cap.Spec.MaxBudgetMs != 2000 || cap.Spec.EffectiveBudgetMs != 2000 {
		t.Errorf("未触顶时两个值应相等: 声明 %d 生效 %d",
			cap.Spec.MaxBudgetMs, cap.Spec.EffectiveBudgetMs)
	}

	// 声明值超过上限：生效值被钳住，声明值仍是插件给的那一个。
	const declared = 30000
	svc, _, _ = pointScanFixture(t, true, newSrc(declared))
	cap, err = svc.PointCapability(context.Background(), drivertest.TestProtocol)
	if err != nil {
		t.Fatalf("PointCapability: %v", err)
	}
	if cap.Spec.MaxBudgetMs != declared {
		t.Errorf("声明值被改了: %d", cap.Spec.MaxBudgetMs)
	}
	if cap.Spec.EffectiveBudgetMs != pluginhost.BudgetWithinGatewayCapMs(declared) {
		t.Errorf("生效预算 = %d，期望 %d（网关上限）",
			cap.Spec.EffectiveBudgetMs, pluginhost.BudgetWithinGatewayCapMs(declared))
	}
	if cap.Spec.EffectiveBudgetMs >= declared {
		t.Errorf("生效预算 %d 不该等于或超过声明值 %d —— 钳制没生效",
			cap.Spec.EffectiveBudgetMs, declared)
	}
}

// TestPointCapabilityUnsupportedIsNotAnError 不支持的协议回 200 + supported=false。
//
// **不能报错**：这是进页面就会调的一次查询，"这个协议不支持"是正常答案。
// 报错会让前端把"功能不可用"显示成一个网络错误。
func TestPointCapabilityUnsupportedIsNotAnError(t *testing.T) {
	src := &fakePointSource{specs: pointSpecs("Other.Protocol")}
	svc, _, _ := pointScanFixture(t, true, src)

	cap, err := svc.PointCapability(context.Background(), drivertest.TestProtocol)
	if err != nil {
		t.Fatalf("不支持的协议不该是错误: %v", err)
	}
	if cap.Supported {
		t.Error("Supported 应为 false")
	}
	if !cap.Enabled {
		t.Error("Enabled 反映的是总开关，与协议能力无关")
	}
}

// TestPointCapabilityReflectsSwitch 总开关关掉时 Enabled 为 false。
func TestPointCapabilityReflectsSwitch(t *testing.T) {
	svc, _, _ := pointScanFixture(t, false, &fakePointSource{specs: pointSpecs(drivertest.TestProtocol)})

	cap, err := svc.PointCapability(context.Background(), drivertest.TestProtocol)
	if err != nil {
		t.Fatalf("PointCapability: %v", err)
	}
	if cap.Enabled {
		t.Error("deviceScan.enabled=false 时 Enabled 应为 false")
	}
}

// ==================== DiscoverPoints ====================

// TestDiscoverPointsUsesServerSideProtocolJSON 连接配置必须由**服务端**从 device
// 行读取后透传，不采信客户端。
//
// 它是跨信任边界的数据（决定网关去连哪台设备）。DTO 里刻意没有这个字段，
// 因此这条断言的实质是"管理器收到的就是库里那一份"。
func TestDiscoverPointsUsesServerSideProtocolJSON(t *testing.T) {
	src := &fakePointSource{
		specs: pointSpecs(drivertest.TestProtocol),
		result: &driver.PointScanResult{
			AssumedCommonDataType: "Word",
			Scanned:               2, Readable: 2,
			Points: []driver.DiscoveredPoint{
				{Name: "%MW100", Value: "7"},
				{Name: "%MW101", Note: "当前值为 0"},
			},
		},
	}
	svc, _, dev := pointScanFixture(t, true, src)

	options := json.RawMessage(`{"functionCode":3,"startAddress":100,"count":2}`)
	out, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{
		DeviceID: dev.ID, Options: options,
	})
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}

	if src.gotProtocolJSON != pointTestDeviceJSON {
		t.Fatalf("插件收到的连接配置 = %q，期望服务端从 device 行取的那一份 %q",
			src.gotProtocolJSON, pointTestDeviceJSON)
	}
	if src.gotProtocol != drivertest.TestProtocol {
		t.Errorf("协议名 = %q", src.gotProtocol)
	}
	if string(src.gotOptions) != string(options) {
		t.Errorf("扫描选项 = %q，期望原样透传", src.gotOptions)
	}

	// VO 的投影：三个计数量、两个标志、点位字段都要对上。
	if out.Protocol != drivertest.TestProtocol {
		t.Errorf("结果里的协议名 = %q", out.Protocol)
	}
	if len(out.Points) != 2 || out.Points[0].Name != "%MW100" || out.Points[0].Value != "7" {
		t.Fatalf("点位 = %+v", out.Points)
	}
	if out.Points[1].Note != "当前值为 0" {
		t.Errorf("零值的说明没有透出来：%+v", out.Points[1])
	}
	if out.Scanned != 2 || out.Readable != 2 || out.Unreadable != 0 {
		t.Errorf("统计 = %+v", out)
	}
}

// TestDiscoverPointsCarriesPerPointType 原生枚举协议的**逐点类型**要透到 VO。
//
// 这一层是前端的唯一数据来源：Rockwell.CIP 枚举出来的标签各有各的类型
// （DINT/REAL/BOOL 混在一张表里），结果级的 AssumedCommonDataType 表达不了。
// 服务层漏搬这个字段不会报任何错 —— 症状是前端把每一行都初始化成同一个默认类型，
// 用户看着"类型"那一列填好了就提交，建出的点位采出来的值是错的。
//
// 顺带钉住那条**跨端约定**：本协议不读采样值，所以结果级的 AssumedCommonDataType
// 必须原样留空 —— 前端据它为空隐藏「显示 0 值」开关。
func TestDiscoverPointsCarriesPerPointType(t *testing.T) {
	src := &fakePointSource{
		specs: pointSpecs(drivertest.TestProtocol),
		result: &driver.PointScanResult{
			// 刻意**不设** AssumedCommonDataType：原生枚举没有统一的假设类型。
			Scanned: 2, Readable: 2,
			Points: []driver.DiscoveredPoint{
				{Name: "MotorSpeed", CommonDataType: "Long"},
				{Name: "Temperature", CommonDataType: "Float"},
			},
		},
	}
	svc, _, dev := pointScanFixture(t, true, src)

	out, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: dev.ID})
	if err != nil {
		t.Fatalf("DiscoverPoints: %v", err)
	}

	if out.AssumedCommonDataType != "" {
		t.Errorf("AssumedCommonDataType = %q，期望空 —— 前端据它为空隐藏「显示 0 值」开关",
			out.AssumedCommonDataType)
	}
	want := []string{"Long", "Float"}
	if len(out.Points) != len(want) {
		t.Fatalf("点位 %d 条，期望 %d", len(out.Points), len(want))
	}
	for i, w := range want {
		if out.Points[i].CommonDataType != w {
			t.Errorf("点位 %q 的类型 = %q，期望 %q —— 它在服务层被漏搬了",
				out.Points[i].Name, out.Points[i].CommonDataType, w)
		}
	}
}

// TestDiscoverPointsUnsupportedProtocol 协议没声明点扫能力时要用**专门的**错误码。
//
// 与 40024（不支持局域网扫描）分开：用户的下一步动作完全不同 ——
// 那个要换插件版本，这个要去看该协议的 PointDiscovery 声明。
func TestDiscoverPointsUnsupportedProtocol(t *testing.T) {
	src := &fakePointSource{specs: pointSpecs("Other.Protocol")}
	svc, _, dev := pointScanFixture(t, true, src)

	_, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: dev.ID})
	assertBusinessCode(t, err, enums.DevicePointScanUnsupportedEnum.GetCode())
}

// TestDiscoverPointsDisabled 复用 deviceScan.enabled，**不开新开关**。
func TestDiscoverPointsDisabled(t *testing.T) {
	src := &fakePointSource{specs: pointSpecs(drivertest.TestProtocol)}
	svc, _, dev := pointScanFixture(t, false, src)

	_, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: dev.ID})
	assertBusinessCode(t, err, enums.DeviceScanDisabledEnum.GetCode())
}

// TestDiscoverPointsUnknownDevice 设备不存在时按设备域的错误码报，而不是 500。
func TestDiscoverPointsUnknownDevice(t *testing.T) {
	src := &fakePointSource{specs: pointSpecs(drivertest.TestProtocol)}
	svc, _, _ := pointScanFixture(t, true, src)

	_, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: "no-such-device"})
	assertBusinessCode(t, err, enums.DeviceNotExistsEnum.GetCode())
}

// TestDiscoverPointsFailureCarriesRootCause 执行期的失败（设备正忙、串口被占、
// 配置模式）必须把插件的原文透出来。
//
// 这些原因全是动态的，回一句固定的译文等于把最有用的信息丢掉 ——
// 用户需要的是"先停用那台设备"这样的可执行动作。
func TestDiscoverPointsFailureCarriesRootCause(t *testing.T) {
	src := &fakePointSource{
		specs: pointSpecs(drivertest.TestProtocol),
		err:   errors.New("设备正忙：采集正在读取这台设备，请先停用该设备再扫描"),
	}
	svc, _, dev := pointScanFixture(t, true, src)

	_, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: dev.ID})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !strings.Contains(err.Error(), "设备正忙") {
		t.Errorf("错误里应保留插件的根因，实际 %v", err)
	}
}

// TestDiscoverPointsKeepsPartialResultOnFailure 扫描中途断链时，**已经读到的
// 点位必须跟着返回**，而不是被一句"扫描失败"盖掉。
//
// 这条链路上三方都刻意保了这份结果：插件（ScanPoints 在连接类错误上
// `return sc.finish(), chunkErr`）、宿主（pluginhost.DiscoverPoints 判了
// "reply 非空就一并返回"）、契约（driversdk.FindPointsFunc 第 3 条）。
// 服务层是最后一环 —— 它丢掉就等于前面三处的刻意全部作废，而代价是用户
// 重跑一次十几秒的串口扫描。
func TestDiscoverPointsKeepsPartialResultOnFailure(t *testing.T) {
	src := &fakePointSource{
		specs: pointSpecs(drivertest.TestProtocol),
		result: &driver.PointScanResult{
			AssumedCommonDataType: "Word",
			Scanned:               3, Readable: 2, Unreadable: 1,
			Points: []driver.DiscoveredPoint{
				{Name: "%MW100", Value: "7"},
				{Name: "%MW101", Note: "当前值为 0"},
			},
		},
		err: errors.New("扫描 [102, 104) 时连接中断: EOF"),
	}
	svc, _, dev := pointScanFixture(t, true, src)

	out, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{
		DeviceID: dev.ID,
		Options:  json.RawMessage(`{"functionCode":3,"startAddress":100,"count":4}`),
	})
	if err != nil {
		t.Fatalf("带着部分结果时不该再作为错误返回: %v", err)
	}
	if out == nil || len(out.Points) != 2 || out.Points[0].Name != "%MW100" {
		t.Fatalf("部分点位被丢了: %+v", out)
	}
	if out.Scanned != 3 || out.Readable != 2 || out.Unreadable != 1 {
		t.Errorf("统计没透出来: %+v", out)
	}
	// 中断必然是"没扫完"：插件侧的 Incomplete 只在预算耗尽时置位，链路中断
	// 不经过那条分支，必须由服务层补上 —— 否则用户会把一份截断的清单当成全量。
	if !out.Incomplete {
		t.Error("中断的扫描必须置 Incomplete，否则用户会以为范围已经扫完")
	}
	if !strings.Contains(out.Warning, "连接中断") {
		t.Errorf("提示里应保留插件给的根因，实际 %q", out.Warning)
	}
	if !strings.Contains(out.Warning, "建为点位") {
		t.Errorf("提示要说清这份清单该怎么用，实际 %q", out.Warning)
	}
}

// TestDiscoverPointsNoPartialResultStillFails 一个地址都没读到时**不进**部分
// 结果那条路：那时没有任何东西可给，回业务错误码才是对的。
//
// 少了这条，"部分结果"很容易被写成"只要有 error 就返回空 VO"，
// 于是"设备正忙"这类根本没开始的失败会被显示成一次成功的空扫描。
func TestDiscoverPointsNoPartialResultStillFails(t *testing.T) {
	src := &fakePointSource{
		specs:  pointSpecs(drivertest.TestProtocol),
		result: &driver.PointScanResult{},
		err:    errors.New("设备正忙：采集正在读取这台设备，请先停用该设备再扫描"),
	}
	svc, _, dev := pointScanFixture(t, true, src)

	out, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: dev.ID})
	if err == nil {
		t.Fatalf("空结果 + 错误应作为错误返回，实际拿到 %+v", out)
	}
	if !strings.Contains(err.Error(), "设备正忙") {
		t.Errorf("错误里应保留根因，实际 %v", err)
	}
}

// TestDiscoverPointsUnsupportedFromManager 管理器自己报"不支持"时，
// 也要映射成那个专门的错误码。
//
// 能力表与调用出口可能不同源（能力表是现算的、调用出口还会查一次），
// 因此两条路都要能归到同一个码上 —— 否则用户会在"协议明明支持"的提示下
// 收到一条泛泛的"扫描参数不合法"。
func TestDiscoverPointsUnsupportedFromManager(t *testing.T) {
	src := &fakePointSource{
		specs: pointSpecs(drivertest.TestProtocol),
		err:   pluginhost.ErrPointDiscoveryUnsupported,
	}
	svc, _, dev := pointScanFixture(t, true, src)

	_, err := svc.DiscoverPoints(context.Background(), &dto.DiscoverPointsDTO{DeviceID: dev.ID})
	assertBusinessCode(t, err, enums.DevicePointScanUnsupportedEnum.GetCode())
}

// ==================== CreateAddresses ====================

// TestCreateAddressesBulk 正常路径：勾选两条建成两行，通用类型映射成内部类型。
func TestCreateAddressesBulk(t *testing.T) {
	svc, db, dev := pointScanFixture(t, true, nil)

	res, err := svc.CreateAddresses(context.Background(), &dto.CreateAddressesFromScanDTO{
		DeviceID: dev.ID,
		Items:    []dto.ScanAddressItemDTO{addrItem("%MW100"), addrItem("%MW101")},
	})
	if err != nil {
		t.Fatalf("CreateAddresses: %v", err)
	}
	if res.Total != 2 || res.Success != 2 || res.Failed != 0 {
		t.Fatalf("结果 = %+v，期望 2/2/0", res)
	}

	var rows []po.DeviceAddress
	if err := db.Order("name").Find(&rows).Error; err != nil {
		t.Fatalf("查地址: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("库里地址数 = %d，期望 2", len(rows))
	}
	// 通用类型 → 协议内部类型：这一步错了的表现是"点位存进去了但采不到值"。
	if rows[0].CommonDataType != "Short" || rows[0].DataType != "int16" {
		t.Errorf("类型映射 = %q → %q，期望 Short → int16", rows[0].CommonDataType, rows[0].DataType)
	}
	if rows[0].DeviceID != dev.ID {
		t.Errorf("DeviceID = %q", rows[0].DeviceID)
	}
	if rows[0].Status != 1 {
		t.Errorf("新建点位应为启用，实际 status=%d", rows[0].Status)
	}
}

// TestCreateAddressesFallsBackLabel 标签为空时兜底为名称。
//
// 这是**有意**的：扫描结果没有标签来源（设备不会告诉你"这个寄存器是温度"），
// 要求用户逐行为几十个点位想标签等于逼他用 Excel 以外的路走回头路。
// name 至少是唯一的、能定位的。
func TestCreateAddressesFallsBackLabel(t *testing.T) {
	svc, db, dev := pointScanFixture(t, true, nil)
	item := addrItem("%MW100")
	item.Label = ""

	if _, err := svc.CreateAddresses(context.Background(), &dto.CreateAddressesFromScanDTO{
		DeviceID: dev.ID, Items: []dto.ScanAddressItemDTO{item},
	}); err != nil {
		t.Fatalf("CreateAddresses: %v", err)
	}

	var row po.DeviceAddress
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("查地址: %v", err)
	}
	if row.Label != "%MW100" {
		t.Errorf("Label = %q，期望兜底为名称", row.Label)
	}
}

// TestCreateAddressesDedupesBothWays 库内与批内**双重去重**。
//
// (device_id, name) 是复合唯一，而同一次扫描里同一个地址被勾两次完全可能 ——
// 少了批内那层，第二条会撞 SQLite 约束，用户看到一条数据库错误。
func TestCreateAddressesDedupesBothWays(t *testing.T) {
	svc, db, dev := pointScanFixture(t, true, nil)
	// 库里已有一条。
	if err := db.Create(&po.DeviceAddress{
		DeviceID: dev.ID, Name: "%MW100", Label: "旧的",
		CommonDataType: "Short", DataType: "int16",
		RwPermission: "R", ScanFrequency: 1000, Status: 1,
	}).Error; err != nil {
		t.Fatalf("预置地址: %v", err)
	}

	res, err := svc.CreateAddresses(context.Background(), &dto.CreateAddressesFromScanDTO{
		DeviceID: dev.ID,
		Items: []dto.ScanAddressItemDTO{
			addrItem("%MW100"), // 与库内撞名
			addrItem("%MW101"), // 批内第一次出现
			addrItem("%MW101"), // 批内重复
		},
	})
	if err != nil {
		t.Fatalf("CreateAddresses: %v", err)
	}
	if res.Total != 3 || res.Success != 1 || res.Failed != 2 {
		t.Fatalf("结果 = %+v，期望 3/1/2", res)
	}
	for _, e := range res.Errors {
		if e.Reason != enums.DeviceAddressExistsEnum.GetMessage() {
			t.Errorf("失败原因 = %q，期望重名文案", e.Reason)
		}
	}
}

// TestCreateAddressesPartialFailure 逐条失败不中断整批（与 Excel 导入同源）。
func TestCreateAddressesPartialFailure(t *testing.T) {
	svc, db, dev := pointScanFixture(t, true, nil)

	badType := addrItem("%MW102")
	badType.CommonDataType = "NoSuchType"
	badRw := addrItem("%MW103")
	badRw.RwPermission = "X"
	noName := addrItem("")
	noName.Name = ""

	res, err := svc.CreateAddresses(context.Background(), &dto.CreateAddressesFromScanDTO{
		DeviceID: dev.ID,
		Items: []dto.ScanAddressItemDTO{
			addrItem("%MW100"), // 好
			badType,            // 协议不支持该类型
			badRw,              // 读写权限非法
			noName,             // 名称为空
			addrItem("%MW104"), // 好
		},
	})
	if err != nil {
		// 部分成功是预期结果，不该是请求级错误（HTTP 始终 200）。
		t.Fatalf("部分失败不该是请求级错误: %v", err)
	}
	if res.Total != 5 || res.Success != 2 || res.Failed != 3 {
		t.Fatalf("结果 = %+v，期望 5/2/3", res)
	}
	if len(res.Errors) != 3 {
		t.Fatalf("失败明细数 = %d，期望 3", len(res.Errors))
	}
	for _, e := range res.Errors {
		if e.Reason == "" {
			t.Error("每一条失败都必须给出原因")
		}
		if e.Row <= 0 {
			t.Error("失败明细要带行号，前端靠它定位")
		}
	}
	// 坏行不该入库，好行要在。
	var rows []po.DeviceAddress
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("查地址: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("库里地址数 = %d，期望 2（只该收下合法的两条）", len(rows))
	}
}

// TestCreateAddressesRejectsBadRwPermission 读写权限必须落在 R/W/RW 之内 ——
// 与单条创建、Excel 导入走的是同一个纯函数，三处的判定必须一致。
func TestCreateAddressesRejectsBadRwPermission(t *testing.T) {
	svc, _, dev := pointScanFixture(t, true, nil)

	item := addrItem("%MW100")
	item.RwPermission = "read"
	res, err := svc.CreateAddresses(context.Background(), &dto.CreateAddressesFromScanDTO{
		DeviceID: dev.ID, Items: []dto.ScanAddressItemDTO{item},
	})
	if err != nil {
		t.Fatalf("CreateAddresses: %v", err)
	}
	if res.Success != 0 || res.Failed != 1 {
		t.Fatalf("结果 = %+v", res)
	}
	if !strings.Contains(res.Errors[0].Reason, "读写权限") {
		t.Errorf("失败原因 = %q，应点明读写权限", res.Errors[0].Reason)
	}
}

// TestCreateAddressesUnknownDevice 设备不存在时整个请求就失败（拿不到协议，
// 类型映射无从谈起）。
func TestCreateAddressesUnknownDevice(t *testing.T) {
	svc, _, _ := pointScanFixture(t, true, nil)

	_, err := svc.CreateAddresses(context.Background(), &dto.CreateAddressesFromScanDTO{
		DeviceID: "no-such-device",
		Items:    []dto.ScanAddressItemDTO{addrItem("%MW100")},
	})
	assertBusinessCode(t, err, enums.DeviceNotExistsEnum.GetCode())
}
