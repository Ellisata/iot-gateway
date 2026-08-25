package vo

import "encoding/json"

// PushChannelFormVO 数据推送通道表单响应
type PushChannelFormVO struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	FormJSON  json.RawMessage `json:"formJson"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}
