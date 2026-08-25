package vo

import "encoding/json"

// PushChannelVO 数据推送通道响应
type PushChannelVO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ConfigJSON  json.RawMessage `json:"configJson"`
	Status      int             `json:"status"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}
