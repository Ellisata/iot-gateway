// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// UserVO 用户视图对象
type UserVO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Avatar    string `json:"avatar"`
	Status    int    `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// LoginVO 登录响应
type LoginVO struct {
	AccessToken string `json:"accessToken"`
	// ExpiresIn 令牌剩余有效期（秒），前端据此安排续期/重新登录
	ExpiresIn int64 `json:"expiresIn"`
	// ExpiresAt 令牌过期时刻（RFC3339，本地时区）
	ExpiresAt string `json:"expiresAt"`
	//User        UserVO `json:"user"`
}
