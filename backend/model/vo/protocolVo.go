package vo

import "encoding/json"

// ProtocolVO 协议响应
type ProtocolVO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	FormJSON    json.RawMessage `json:"formJson"`
	Sort        int             `json:"sort"`
	Status      int             `json:"status"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}
