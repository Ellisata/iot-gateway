package collector

// CollectedRecord 采集记录
type CollectedRecord struct {
	DeviceID           string `json:"deviceID"`
	DeviceName         string `json:"deviceName"`
	DeviceAddressID    string `json:"deviceAddressID"`
	DeviceAddressName  string `json:"deviceAddressName"`
	DeviceAddressLabel string `json:"deviceAddressLabel"` // 地址 name 的中文说明（如温度、电流）
	Value              string `json:"value"`
	DataType           string `json:"dataType"`
	// Kind 数据类型类别（driver.KindXxx），由驱动从已解析类型填充。
	// 携带 json tag 以保证断网缓存（push/spool）序列化回放后不丢失。
	// MQTT 通道自行构建 pushPoint，不受本字段影响。
	Kind string `json:"kind"`
	// Protocol 协议名称（iot_protocol.name，如 "ModBus.TCP"），由 collector 填充，
	// 供下游区分协议命名空间（dataType 为协议内部类型名）。
	Protocol string `json:"protocol"`
	Quality  int    `json:"quality"`
}
