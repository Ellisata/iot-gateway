// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// AlarmVO 断联报警响应
type AlarmVO struct {
	ID             string `json:"id"`
	TargetID       string `json:"targetId"`
	TargetName     string `json:"targetName"`
	TargetType     string `json:"targetType"`    // device | channel
	AlarmType      string `json:"alarmType"`     // offline | recover
	AlarmTypeName  string `json:"alarmTypeName"` // 断联报警 | 恢复记录
	Level          string `json:"level"`
	Content        string `json:"content"`
	Status         string `json:"status"`     // active | cleared
	StatusName     string `json:"statusName"` // 未恢复 | 已恢复
	FirstOccurTime string `json:"firstOccurTime"`
	LastOccurTime  string `json:"lastOccurTime"`
	ClearTime      string `json:"clearTime"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}
