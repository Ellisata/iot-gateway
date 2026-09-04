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
