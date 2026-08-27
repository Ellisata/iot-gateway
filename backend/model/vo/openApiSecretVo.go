package vo

// OpenApiSecretVO 开放接口密钥响应
type OpenApiSecretVO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
