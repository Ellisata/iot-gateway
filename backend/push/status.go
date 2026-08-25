package push

import "time"

// ChannelStatusVO 推送通道运行状态响应
type ChannelStatusVO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Running      bool   `json:"running"`
	Connected    bool   `json:"connected"`
	Broker       string `json:"broker"`
	Topic        string `json:"topic"`
	QueueDepth   uint64 `json:"queueDepth"`
	SpoolDepth   uint64 `json:"spoolDepth"` // 断网本地缓存待补发批次
	PublishCount uint64 `json:"publishCount"`
	DroppedCount uint64 `json:"droppedCount"`
	LastPublish  string `json:"lastPublishTime"`
	LastSuccess  string `json:"lastSuccessTime"`
	LastErr      string `json:"error"`
}

// FormatTime 格式化时间为字符串，供各通道子包在 Snapshot 中复用
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
