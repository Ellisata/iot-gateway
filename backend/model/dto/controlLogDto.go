// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dto

// PageControlLogDTO 控制下发审计分页查询。
//
// 所有过滤条件都可选；时间范围为闭区间，格式 "2006-01-02 15:04:05"
// （与 control_log.created_at 的落库格式一致，字符串比较即可）。
type PageControlLogDTO struct {
	Page int `form:"page" binding:"required,min=1"`
	Size int `form:"size" binding:"required,min=1,max=100"`

	DeviceID  string `form:"deviceId"`
	AddressID string `form:"addressId"`
	Source    string `form:"source"`   // web / openapi / mqtt / agent
	Operator  string `form:"operator"` // 操作者（模糊）
	RequestID string `form:"requestId"`
	// Name 设备名/点位名/点位说明的模糊查询。
	Name string `form:"name"`

	Code     *int `form:"code"`     // driver.WriteCodeXxx；不传 = 全部
	Verified *int `form:"verified"` // 1 已回读确认，0 未确认；不传 = 全部

	StartTime string `form:"startTime"` // 含
	EndTime   string `form:"endTime"`   // 含
}
