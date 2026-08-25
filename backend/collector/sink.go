package collector

// RecordSink 采集数据接收者接口（存储 / 推送）
// 采集引擎每轮采集后将数据交给 RecordSink，由实现方决定如何消费（如推送引擎）。
// 实现方必须保证不阻塞采集（内部应使用非阻塞入队）。
type RecordSink interface {
	PushRecords(records []CollectedRecord)
}

// DeviceStateSink 设备在线状态接收者接口（断联报警等）。
// 采集引擎每轮对单台设备轮询结束后上报成功/失败，由实现方（如 alarm.Engine）
// 维护每设备状态机并落库。实现方必须保证不阻塞采集（内部应轻量快速）。
type DeviceStateSink interface {
	// ReportDevicePoll 上报单台设备本轮采集是否成功。
	// ok=false 包括连接失败、重连失败及退避窗口内跳过轮询。
	ReportDevicePoll(deviceID, deviceName string, ok bool)
}
