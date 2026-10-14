// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package response

import "time"

// Response 统一响应结构体
type Response[T any] struct {
	Code       string `json:"code"`
	Message    string `json:"msg"`
	IsSuccess  bool   `json:"isSuccess"`
	Data       T      `json:"data"`
	ServerTime string `json:"serverTime"`
}

// PageVo 通用分页视图对象
type PageVo[T any] struct {
	Page    int   `json:"page"`
	Size    int   `json:"size"`
	Total   int64 `json:"total"`
	Records []T   `json:"records"`
}

// CursorVo 通用游标分页视图对象（keyset 翻页，与 PageVo 的 OFFSET 翻页并列）。
//
// 用于「全量遍历」场景：Offset 翻页在遍历途中发生增删会让未读区间整体位移，
// 造成漏读或重读；游标翻页按有序主键续读，不受影响。
// 因此这里**刻意没有 total** —— 数据集在移动，每页花一次 COUNT 得出的数字没有意义。
//
// NextAfter 是下一页的游标（本页最后一条记录的 id）。约定：HasMore 为 false 时
// NextAfter 为空串，调用方的循环形如
// `after=""` → 拉取 → `HasMore ? after = NextAfter : 停`。
type CursorVo[T any] struct {
	Size      int    `json:"size"`
	HasMore   bool   `json:"hasMore"`
	NextAfter string `json:"nextAfter"`
	Records   []T    `json:"records"`
}

// Success 成功响应
func Success(data interface{}) Response[interface{}] {
	return Response[interface{}]{
		Code:       "0",
		Message:    "success",
		IsSuccess:  true,
		Data:       data,
		ServerTime: time.Now().Format("2006-01-02 15:04:05"),
	}
}

// Fail 失败响应
func Fail(code, msg string) Response[interface{}] {
	return Response[interface{}]{
		Code:       code,
		Message:    msg,
		IsSuccess:  false,
		Data:       nil,
		ServerTime: time.Now().Format("2006-01-02 15:04:05"),
	}
}
