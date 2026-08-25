package dto

// ==================== 报警 DTO ====================

// PageAlarmDTO 报警历史分页查询（目标名 / 类型 / 状态过滤）
type PageAlarmDTO struct {
	Page       int    `form:"page" binding:"required,min=1"`
	Size       int    `form:"size" binding:"required,min=1,max=100"`
	TargetName string `form:"targetName"` // 目标名称（模糊查询）
	TargetType string `form:"targetType"` // device | channel，空查全部
	AlarmType  string `form:"alarmType"`  // offline | recover
	Status     string `form:"status"`     // active | cleared
}
