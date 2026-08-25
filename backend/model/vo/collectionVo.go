package vo

// CollectionStatusVO 采集状态响应（协议无关）
type CollectionStatusVO struct {
	Running         bool   `json:"running"`
	LastPollTime    string `json:"lastPollTime"`
	LastSuccessTime string `json:"lastSuccessTime"`
	ErrorCount      int    `json:"errorCount"`
	DeviceCount     int    `json:"deviceCount"`
	AddressCount    int    `json:"addressCount"`
}
