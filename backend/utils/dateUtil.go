// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import "time"

// GetWeekRange 获取本周的起止日期
func GetWeekRange() (time.Time, time.Time) {
	now := time.Now()
	weekday := now.Weekday()
	if weekday == time.Sunday {
		weekday = 7
	}
	startOfWeek := now.AddDate(0, 0, -int(weekday-time.Monday))
	endOfWeek := startOfWeek.AddDate(0, 0, 6)
	return startOfWeek, endOfWeek
}

// GetLastWeekRange 获取上周的起止日期
func GetLastWeekRange() (time.Time, time.Time) {
	now := time.Now()
	weekday := now.Weekday()
	if weekday == time.Sunday {
		weekday = 7
	}
	thisMonday := now.AddDate(0, 0, -int(weekday-time.Monday))
	lastMonday := thisMonday.AddDate(0, 0, -7)
	lastSunday := thisMonday.AddDate(0, 0, -1)
	return lastMonday, lastSunday
}

// GetMonthRange 获取本月的起止日期
func GetMonthRange() (time.Time, time.Time) {
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	endOfMonth := startOfMonth.AddDate(0, 1, -1)
	return startOfMonth, endOfMonth
}

// GetDateRange 获取指定日期的起止时间
func GetDateRange(date time.Time) (time.Time, time.Time) {
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	end := start.AddDate(0, 0, 1).Add(-time.Nanosecond)
	return start, end
}
