// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/appError"
	"iot-gateway/configFile"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/model/vo"
	"iot-gateway/runtimecfg"
)

// fakeControlRunner 记录调用并回放预设结果。
type fakeControlRunner struct {
	mu          sync.Mutex
	writes      []driver.WriteRequest
	writeCalls  int
	writeErr    error
	writeResult []driver.WriteResult

	reads      []po.DeviceAddress
	readCalls  int
	readErr    error
	readResult []driver.ReadResult

	// writeEntered/writeGate 用于并发 requestId 收敛用例：Write 先关闭 entered
	// 再阻塞在 gate 上，让测试能确定性地构造"第一个请求仍在执行"的时刻。
	writeEntered chan struct{}
	writeGate    chan struct{}
	enterOnce    sync.Once
}

func (f *fakeControlRunner) Write(_ context.Context, _ string, reqs []driver.WriteRequest) ([]driver.WriteResult, error) {
	f.mu.Lock()
	f.writeCalls++
	f.writes = append(f.writes, reqs...)
	err, result := f.writeErr, f.writeResult
	entered, gate := f.writeEntered, f.writeGate
	f.mu.Unlock()

	if entered != nil {
		f.enterOnce.Do(func() { close(entered) })
	}
	if gate != nil {
		<-gate
	}
	return result, err
}

func (f *fakeControlRunner) ReadPoints(_ context.Context, _ string, addrs []po.DeviceAddress) ([]driver.ReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readCalls++
	f.reads = addrs
	return f.readResult, f.readErr
}

func (f *fakeControlRunner) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writeCalls
}

type fakeWritableSource struct{ writable map[string]bool }

func (f *fakeWritableSource) ProtocolWritable(protocol string) bool { return f.writable[protocol] }

func newControlTestService(t *testing.T, enabled bool) (*ControlService, *fakeControlRunner, *fakeWritableSource, *gorm.DB) {
	t.Helper()
	return newControlTestServiceWithPolicy(t, enabled, nil)
}

// newControlTestServiceWithPolicy 同 newControlTestService，但可注入运行开关来源
// （nil = 回退启动期配置）。
func newControlTestServiceWithPolicy(t *testing.T, enabled bool, policy runtimecfg.Reader) (*ControlService, *fakeControlRunner, *fakeWritableSource, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&po.Device{}, &po.DeviceAddress{}, &po.IotProtocol{}, &po.ControlLog{},
		&po.ThingModelProperty{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	runner := &fakeControlRunner{}
	writable := &fakeWritableSource{writable: map[string]bool{"ModBus.TCP": true}}
	cfg := &configFile.Config{}
	cfg.Control.Enabled = enabled
	return NewControlService(db, runner, writable, cfg, policy), runner, writable, db
}

// fakeControlPolicy 返回固定的 control.enabled 值，用来验证运行开关优先于
// 启动期配置、且下一个请求立即生效。
type fakeControlPolicy struct{ enabled bool }

func (f fakeControlPolicy) Bool(runtimecfg.Key) bool { return f.enabled }

func seedControlDevice(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&po.IotProtocol{ID: "p1", Name: "ModBus.TCP"}).Error; err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if err := db.Create(&po.Device{ID: "d1", Name: "PLC-1", ProtocolID: "p1", Status: 1}).Error; err != nil {
		t.Fatalf("seed device: %v", err)
	}
	rows := []po.DeviceAddress{
		{ID: "a-rw", DeviceID: "d1", Name: "40001", Label: "温度", CommonDataType: "Short", DataType: "int16", RwPermission: "RW", Status: 1},
		{ID: "a-r", DeviceID: "d1", Name: "40002", Label: "只读", CommonDataType: "Short", DataType: "int16", RwPermission: "R", Status: 1},
		{ID: "a-w", DeviceID: "d1", Name: "40003", Label: "只写", CommonDataType: "Short", DataType: "int16", RwPermission: "W", Status: 1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed addresses: %v", err)
	}
}

func TestControlDisabled(t *testing.T) {
	svc, _, _, db := newControlTestService(t, false)
	seedControlDevice(t, db)

	_, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
	}, "test", "tester")
	assertControlCode(t, err, enums.ControlDisabledEnum.GetCode())
}

// TestControlRuntimeSwitchOverridesBootConfig 运行开关优先于启动期配置，
// 且修改后下一个请求立即生效 —— 这是把 control.enabled 提升为 runtimecfg
// 热读项的全部意义。
func TestControlRuntimeSwitchOverridesBootConfig(t *testing.T) {
	// 启动期关闭，但运行开关打开。
	svc, runner, _, db := newControlTestServiceWithPolicy(t, false, fakeControlPolicy{enabled: true})
	seedControlDevice(t, db)
	runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: driver.WriteCodeOK, Value: "1"}}

	if _, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
	}, "test", "tester"); err != nil {
		t.Fatalf("运行开关打开时应允许下发: %v", err)
	}

	// 运行期关闭：同一进程内的下一个请求立即被拒绝，不需要重启。
	svc.policy = fakeControlPolicy{enabled: false}
	_, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
	}, "test", "tester")
	assertControlCode(t, err, enums.ControlDisabledEnum.GetCode())

	cap, err := svc.Capability(context.Background(), "d1")
	if err != nil {
		t.Fatalf("Capability: %v", err)
	}
	if cap.Enabled || cap.Writable || cap.Reason != "disabled" {
		t.Errorf("运行开关关闭后能力 = %+v，期望 disabled", cap)
	}
}

func TestControlProtocolNotWritable(t *testing.T) {
	svc, runner, writable, db := newControlTestService(t, true)
	seedControlDevice(t, db)
	writable.writable["ModBus.TCP"] = false

	_, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
	}, "test", "tester")
	assertControlCode(t, err, enums.ControlProtocolNotWritableEnum.GetCode())
	if runner.writeCalls != 0 {
		t.Error("协议不可写时不应调用驱动")
	}
}

func TestControlRejectsReadOnlyPoint(t *testing.T) {
	svc, runner, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)

	_, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-r", Value: "1"}},
	}, "test", "tester")
	assertControlCode(t, err, enums.ControlAddressNotWritableEnum.GetCode())
	if runner.writeCalls != 0 {
		t.Error("只读点位不应调用驱动")
	}
}

// TestControlPropertyAccessIsUpperBound 物模型属性是写入能力的硬上界：
// 「RW 点位 + R 属性」是合法绑定（点位覆盖属性），但业务语义只读，写入必须拒绝。
// 未绑定属性、属性为 W/RW 时仍按点位 rw_permission 放行。
func TestControlPropertyAccessIsUpperBound(t *testing.T) {
	cases := []struct {
		name       string
		mode       string // 空串 = 不插入属性行，模拟悬空 property_id
		wantReject bool
	}{
		{name: "只读属性拒绝写入", mode: "R", wantReject: true},
		{name: "只写属性允许写入", mode: "W"},
		{name: "读写属性允许写入", mode: "RW"},
		{name: "属性悬空保守拒绝", mode: "", wantReject: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, runner, _, db := newControlTestService(t, true)
			seedControlDevice(t, db)

			if tc.mode != "" {
				if err := db.Create(&po.ThingModelProperty{
					ID: "prop-1", ModelID: "m1", Identifier: "temperature", Name: "温度",
					Kind: "int", AccessMode: tc.mode, Status: 1,
				}).Error; err != nil {
					t.Fatalf("seed property: %v", err)
				}
			}
			if err := db.Model(&po.DeviceAddress{}).Where("id = ?", "a-rw").
				Update("property_id", "prop-1").Error; err != nil {
				t.Fatalf("bind property: %v", err)
			}
			runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: driver.WriteCodeOK, Value: "1"}}

			_, err := svc.Write(context.Background(), &dto.ControlDTO{
				DeviceID: "d1",
				Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
			}, "test", "tester")

			if tc.wantReject {
				assertControlCode(t, err, enums.ControlPropertyNotWritableEnum.GetCode())
				if runner.calls() != 0 {
					t.Error("属性不允许写时不应调用驱动")
				}
				return
			}
			if err != nil {
				t.Fatalf("属性 %s 应允许写入: %v", tc.mode, err)
			}
			if runner.calls() != 1 {
				t.Errorf("驱动写入次数 = %d，期望 1", runner.calls())
			}
		})
	}
}

func TestControlWriteAndAudit(t *testing.T) {
	svc, runner, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)
	runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: 0, Value: "42"}}
	runner.readResult = []driver.ReadResult{{DeviceAddressID: "a-rw", Value: "42", Quality: driver.QualityGood}}

	out, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID:  "d1",
		RequestID: "req-1",
		Verify:    true,
		Items:     []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "42"}},
	}, "web", "alice")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(out.Results) != 1 || !out.Results[0].OK || !out.Results[0].Verified {
		t.Fatalf("结果 = %+v，期望成功且回读一致", out.Results)
	}
	if runner.readCalls != 1 || len(runner.reads) != 1 || runner.reads[0].ID != "a-rw" {
		t.Errorf("回读调用不正确: calls=%d addrs=%+v", runner.readCalls, runner.reads)
	}

	var logs []po.ControlLog
	if err := db.Find(&logs).Error; err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("审计行数 = %d，期望 1", len(logs))
	}
	l := logs[0]
	if l.RequestID != "req-1" || l.Code != 0 || l.Verified != 1 || l.RequestValue != "42" ||
		l.ReadbackValue != "42" || l.Operator != "alice" || l.Source != "web" {
		t.Errorf("审计内容不正确: %+v", l)
	}

	// 同一 requestId 再次调用必须返回历史结果，不再下发。
	runner.writeResult = nil
	dup, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID:  "d1",
		RequestID: "req-1",
		Items:     []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "42"}},
	}, "web", "alice")
	if err != nil {
		t.Fatalf("重复请求: %v", err)
	}
	if !dup.Duplicate || len(dup.Results) != 1 || !dup.Results[0].OK {
		t.Fatalf("重复请求结果 = %+v，期望 Duplicate 且成功", dup)
	}
	if runner.writeCalls != 1 {
		t.Errorf("重复请求触发了 %d 次驱动写入，期望仍为 1", runner.writeCalls)
	}
}

func TestControlTransportErrorAudited(t *testing.T) {
	svc, runner, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)
	runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: driver.WriteCodeTransport, Message: "timeout"}}
	runner.writeErr = errors.New("i/o timeout")

	_, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
	}, "web", "alice")
	if err == nil {
		t.Fatal("期望链路错误")
	}
	var logs []po.ControlLog
	if err := db.Find(&logs).Error; err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if len(logs) != 1 || logs[0].Code != driver.WriteCodeTransport {
		t.Errorf("链路错误也必须落审计: %+v", logs)
	}
}

func assertControlCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("期望业务错误，得到 nil")
	}
	var ae *appError.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("错误类型 = %T，期望 *appError.AppError", err)
	}
	if ae.Code != want {
		t.Fatalf("错误码 = %s，期望 %s（%v）", ae.Code, want, err)
	}
}

func TestControlCapability(t *testing.T) {
	svc, _, writable, db := newControlTestService(t, true)
	seedControlDevice(t, db)

	out, err := svc.Capability(context.Background(), "d1")
	if err != nil {
		t.Fatalf("Capability: %v", err)
	}
	if !out.Enabled || !out.Writable || out.Reason != "" {
		t.Errorf("能力 = %+v，期望 enabled+writable", out)
	}

	writable.writable["ModBus.TCP"] = false
	out, err = svc.Capability(context.Background(), "d1")
	if err != nil {
		t.Fatalf("Capability: %v", err)
	}
	if out.Writable || out.Reason != "protocol_not_writable" {
		t.Errorf("能力 = %+v，期望 protocol_not_writable", out)
	}

	// 总开关关闭优先级最高：即使协议可写也只报 disabled。
	svc.cfg.Control.Enabled = false
	writable.writable["ModBus.TCP"] = true
	out, err = svc.Capability(context.Background(), "d1")
	if err != nil {
		t.Fatalf("Capability: %v", err)
	}
	if out.Enabled || out.Writable || out.Reason != "disabled" {
		t.Errorf("能力 = %+v，期望 disabled", out)
	}
}

func TestControlRateLimitDevice(t *testing.T) {
	svc, runner, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)
	runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: 0, Value: "1"}}
	svc.limiter = newControlRateLimiter(2, 10)

	req := func() *vo.ControlResultVO {
		out, err := svc.Write(context.Background(), &dto.ControlDTO{
			DeviceID: "d1",
			Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
		}, "web", "alice")
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		return out
	}

	req()
	req()
	if runner.calls() != 2 {
		t.Fatalf("前两次应下发，实际 %d 次", runner.calls())
	}

	_, err := svc.Write(context.Background(), &dto.ControlDTO{
		DeviceID: "d1",
		Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
	}, "web", "alice")
	assertControlCode(t, err, enums.ControlRateLimitedEnum.GetCode())
	if runner.calls() != 2 {
		t.Errorf("被限流后不应再调用驱动，实际 %d 次", runner.calls())
	}
}

func TestControlRateLimitPoint(t *testing.T) {
	svc, runner, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)
	runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: 0, Value: "1"}}
	svc.limiter = newControlRateLimiter(10, 1)

	req := func() (*vo.ControlResultVO, error) {
		return svc.Write(context.Background(), &dto.ControlDTO{
			DeviceID: "d1",
			Items:    []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "1"}},
		}, "web", "alice")
	}

	if _, err := req(); err != nil {
		t.Fatalf("第一次 Write: %v", err)
	}
	_, err := req()
	assertControlCode(t, err, enums.ControlRateLimitedEnum.GetCode())
	if runner.calls() != 1 {
		t.Errorf("点位限流后不应再调用驱动，实际 %d 次", runner.calls())
	}
}

// TestControlConcurrentRequestID 两个并发请求携带同一 requestId 时，
// 只能有一次真实下发；后到的请求等待并回放首次结果。
func TestControlConcurrentRequestID(t *testing.T) {
	svc, runner, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)
	runner.writeResult = []driver.WriteResult{{DeviceAddressID: "a-rw", Code: 0, Value: "42"}}
	runner.writeEntered = make(chan struct{})
	runner.writeGate = make(chan struct{})
	svc.limiter = newControlRateLimiter(0, 0)

	req := &dto.ControlDTO{
		DeviceID:  "d1",
		RequestID: "req-concurrent",
		Items:     []dto.ControlItemDTO{{DeviceAddressID: "a-rw", Value: "42"}},
	}

	var wg sync.WaitGroup
	var out1, out2 *vo.ControlResultVO
	var err1, err2 error
	wg.Add(1)
	go func() {
		defer wg.Done()
		out1, err1 = svc.Write(context.Background(), req, "web", "alice")
	}()

	select {
	case <-runner.writeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("第一个请求未进入驱动写入")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		out2, err2 = svc.Write(context.Background(), req, "web", "alice")
	}()

	// 等第二个请求真正登记到 inflight，确保两者重叠。
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.inflightMu.Lock()
		_, waiting := svc.inflight["req-concurrent"]
		svc.inflightMu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("第二个请求未登记到 inflight")
		}
		time.Sleep(time.Millisecond)
	}

	close(runner.writeGate)
	wg.Wait()

	if err1 != nil || out1 == nil || out1.Duplicate {
		t.Fatalf("第一个请求 = %+v, err=%v，期望真实执行", out1, err1)
	}
	if err2 != nil || out2 == nil || !out2.Duplicate {
		t.Fatalf("第二个请求 = %+v, err=%v，期望回放 Duplicate", out2, err2)
	}
	if runner.calls() != 1 {
		t.Errorf("驱动写入次数 = %d，期望 1", runner.calls())
	}
}

func TestControlPageLogs(t *testing.T) {
	svc, _, _, db := newControlTestService(t, true)
	seedControlDevice(t, db)

	rows := []po.ControlLog{
		{RequestID: "r1", Source: "web", Operator: "alice", DeviceID: "d1", DeviceName: "PLC-1", ProtocolName: "ModBus.TCP",
			AddressID: "a-rw", AddressName: "40001", AddressLabel: "温度", DataType: "int16", RwPermission: "RW",
			RequestValue: "1", ResultValue: "1", ReadbackValue: "1", Verified: 1, Code: 0, CreatedAt: "2026-10-31 10:00:00"},
		{RequestID: "r2", Source: "mqtt", Operator: "mqtt:sub", DeviceID: "d1", DeviceName: "PLC-1", ProtocolName: "ModBus.TCP",
			AddressID: "a-rw", AddressName: "40001", AddressLabel: "温度", DataType: "int16", RwPermission: "RW",
			RequestValue: "2", ResultValue: "2", Code: driver.WriteCodeRejected, Message: "device rejected", CreatedAt: "2026-10-31 10:01:00"},
		{RequestID: "r3", Source: "web", Operator: "bob", DeviceID: "d1", DeviceName: "PLC-1", ProtocolName: "ModBus.TCP",
			AddressID: "a-w", AddressName: "40003", AddressLabel: "只写", DataType: "int16", RwPermission: "W",
			RequestValue: "3", ResultValue: "3", Code: 0, CreatedAt: "2026-10-31 10:02:00"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed logs: %v", err)
	}

	out, err := svc.PageLogs(context.Background(), &dto.PageControlLogDTO{Page: 1, Size: 2, DeviceID: "d1"})
	if err != nil {
		t.Fatalf("PageLogs: %v", err)
	}
	if out.Total != 3 || len(out.Records) != 2 {
		t.Fatalf("分页 = total %d records %d，期望 3/2", out.Total, len(out.Records))
	}
	if out.Records[0].RequestID != "r3" || out.Records[1].RequestID != "r2" {
		t.Errorf("排序 = %s,%s，期望按 created_at DESC", out.Records[0].RequestID, out.Records[1].RequestID)
	}

	// 来源过滤
	out, err = svc.PageLogs(context.Background(), &dto.PageControlLogDTO{Page: 1, Size: 10, Source: "mqtt"})
	if err != nil {
		t.Fatalf("PageLogs source: %v", err)
	}
	if out.Total != 1 || out.Records[0].RequestID != "r2" {
		t.Errorf("source 过滤结果 = %+v", out.Records)
	}

	// 结果码 + 模糊名称
	code := driver.WriteCodeRejected
	out, err = svc.PageLogs(context.Background(), &dto.PageControlLogDTO{
		Page: 1, Size: 10, Name: "温度", Code: &code,
	})
	if err != nil {
		t.Fatalf("PageLogs filter: %v", err)
	}
	if out.Total != 1 || out.Records[0].RequestID != "r2" {
		t.Errorf("组合过滤结果 = %+v，期望只有 r2", out.Records)
	}

	// 回读确认过滤
	verified := 1
	out, err = svc.PageLogs(context.Background(), &dto.PageControlLogDTO{
		Page: 1, Size: 10, Verified: &verified,
	})
	if err != nil {
		t.Fatalf("PageLogs verified: %v", err)
	}
	if out.Total != 1 || out.Records[0].RequestID != "r1" {
		t.Errorf("verified 过滤结果 = %+v，期望只有 r1", out.Records)
	}

	// 时间闭区间
	out, err = svc.PageLogs(context.Background(), &dto.PageControlLogDTO{
		Page: 1, Size: 10, StartTime: "2026-10-31 10:01:00", EndTime: "2026-10-31 10:02:00",
	})
	if err != nil {
		t.Fatalf("PageLogs time: %v", err)
	}
	if out.Total != 2 {
		t.Errorf("时间过滤 total = %d，期望 2", out.Total)
	}

	// 非法时间
	if _, err := svc.PageLogs(context.Background(), &dto.PageControlLogDTO{Page: 1, Size: 10, StartTime: "2026/10/31"}); err == nil {
		t.Fatal("非法时间格式应报错")
	}
}
