// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

// RecordSink 采集数据接收者接口（存储 / 推送）
// 采集引擎每轮采集后将数据交给 RecordSink，由实现方决定如何消费（如推送引擎）。
// 实现方必须保证不阻塞采集（内部应使用非阻塞入队）。
type RecordSink interface {
	PushRecords(records []CollectedRecord)
}

// MultiSink 把同一批采集记录扇出给多个接收者，按声明顺序依次转发。
//
// 用于除推送引擎之外还需要一个本地消费方（如实时值快照）的场景：
// 采集引擎只接受单个 RecordSink，扇出由本类型承担，各接收者不必互相感知。
//
// 顺序有意义：排在前面的先拿到数据。纯内存的接收者应排在前，
// 避免慢的下游（MQTT 入队、落盘）让它滞后。
type MultiSink []RecordSink

// PushRecords 实现 RecordSink：依次转发给每个接收者。
func (m MultiSink) PushRecords(records []CollectedRecord) {
	for _, s := range m {
		s.PushRecords(records)
	}
}

// DeviceStateSink 设备在线状态接收者接口（断联报警等）。
// 采集引擎每轮对单台设备轮询结束后上报成功/失败，由实现方（如 alarm.Engine）
// 维护每设备状态机并落库。实现方必须保证不阻塞采集（内部应轻量快速）。
type DeviceStateSink interface {
	// ReportDevicePoll 上报单台设备本轮采集是否成功。
	// ok=false 包括连接失败、重连失败及退避窗口内跳过轮询。
	ReportDevicePoll(deviceID, deviceName string, ok bool)
}

// PointStateSink 点位采集质量接收者接口（点位质量报警）。
// 采集引擎把每轮**成功读到**的每个点位交给实现方（见 gatewayTask.reportPointQuality）。
//
// 与 DeviceStateSink 的分工：设备只关心"这一轮读通了没有"，点位关心"读回来的值
// 可不可信、为什么不可信"。两者互不替代 —— 整段被设备以可恢复状态拒绝时设备那头
// 仍是成功的（Read 没返错），只有点位质量这一层看得见。
//
// 实现方必须保证不阻塞采集（内部应轻量快速）：本方法在采集协程里按点位逐个调用，
// 海量点位下即热点路径。
type PointStateSink interface {
	// ReportPointQuality 上报单个点位本轮的采集质量。
	//
	// quality 直接取自 driver.ReadResult.Quality（语义与取值见 driver.QualityXxx：
	// 类别在高 2 位、成因在低 6 位）。**是否算作故障由实现方判定** ——
	// 采集引擎只报事实，不做报警策略；具体的取舍见 alarm.PointEngine 的说明。
	//
	// pointName 为可读的点位名（地址 name 或其中文说明），deviceID/deviceName
	// 为该点位所属设备。
	ReportPointQuality(deviceID, deviceName, pointID, pointName string, quality int)
}

// PointThreshold 点位报警条件（源自 device_address 的 threshold_* 三列）。
//
// 随每个点位一起交给 PointValueSink：采集侧持有的是本轮任务快照里的 po.DeviceAddress，
// 而配置变更会经 10s 热加载重建任务，故传下去的配置总是最新的 ——
// 阈值不需要第二份配置缓存，也就不会与采集侧看到的那份漂移。
type PointThreshold struct {
	Enabled bool   // 报警开关
	Op      string // 比较符（driver.OpXxx），按数据类型的 Kind 取
	// Values 操作数。range 为 [min, max]（任一为空串 = 该侧不检查）；
	// 其余算子为 1 个；in 为集合。由采集侧从 device_address.threshold_value
	// 的 JSON 数组解析而来（解析失败时为空切片 → Inert 为真，不上报）。
	Values []string
}

// Inert 配置不构成任何信号（未启用，或没有可用条件）。
//
// 采集侧据此跳过上报 —— 这不是报警策略，而是"没有信息可发"：条件关掉之后
// 没有任何状态需要维护。代价是「关掉配置」不会再有任何信号去恢复已存在的活跃
// 报警，因此运维改配置那条路必须显式清掉它（见 DeviceAddressService.UpdateDeviceAddress）。
func (t PointThreshold) Inert() bool {
	return !t.Enabled || t.Op == "" || len(t.Values) == 0
}

// PointValueSink 点位采集值接收者接口（点位阈值报警）。
//
// 与 PointStateSink 的分工：质量关心"读回来的值可不可信"，本接口关心
// "读回来的值是不是超出了配置的范围"。两者可同时有效（质量 128 = 不确定但值可用）。
//
// 实现方必须保证不阻塞采集（内部应轻量快速）：本方法在采集协程里按点位逐个调用，
// 海量点位下即热点路径。
type PointValueSink interface {
	// ReportPointValue 上报单个点位本轮读到的原始值及其阈值配置。
	//
	// value 为驱动格式化好的字符串（未重新格式化，报警文案据此与实时页一致）；
	// quality 取自 driver.ReadResult.Quality，**是否可用由实现方判定** ——
	// 用 driver.QualityUsable 判，别写 `quality != 0`：读失败的点位不再恒为 0
	// （见 driver.QualityXxx 的子码），value 是占位串；
	// kind 为 driver.KindXxx，实现方据它决定该值能否参与比较。
	// 采集引擎只报事实，不做"算不算越限"的判定。
	ReportPointValue(deviceID, deviceName, pointID, pointName, value, kind string, quality int, threshold PointThreshold)
}
