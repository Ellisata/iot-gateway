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
	//User        UserVO `json:"user"`
}
