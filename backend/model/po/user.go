// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package po

// User 用户持久化对象（映射 sqlite sys_user 表）
type User struct {
	ID        string `gorm:"primaryKey;column:id" json:"id"`
	Username  string `gorm:"column:username;not null" json:"name"`
	Email     string `gorm:"column:email;uniqueIndex" json:"email"`
	Password  string `gorm:"column:password;not null" json:"-"`
	Phone     string `gorm:"column:phone" json:"phone"`
	Avatar    string `gorm:"column:avatar" json:"avatar"`
	Status    int    `gorm:"column:status;default:1" json:"status"`
	CreatedAt string `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt string `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 表名
func (User) TableName() string {
	return "sys_user"
}
