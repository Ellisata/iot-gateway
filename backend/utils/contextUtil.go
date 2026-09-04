// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

import (
	"context"
)

type contextKey string

const (
	keyUserId contextKey = "userId"
	keyArea   contextKey = "area"
)

// SetUserIdToCtx 将 userId 注入到 context
func SetUserIdToCtx(ctx context.Context, userId string) context.Context {
	return context.WithValue(ctx, keyUserId, userId)
}

// GetUserIdFromCtx 从 context 提取 userId
func GetUserIdFromCtx(ctx context.Context) string {
	userId, ok := ctx.Value(keyUserId).(string)
	if !ok {
		return ""
	}
	return userId
}

// SetAreaToCtx 将区域信息注入到 context
func SetAreaToCtx(ctx context.Context, area string) context.Context {
	return context.WithValue(ctx, keyArea, area)
}

// GetAreaFromCtx 从 context 提取区域信息
func GetAreaFromCtx(ctx context.Context) string {
	area, ok := ctx.Value(keyArea).(string)
	if !ok {
		return "zh"
	}
	return area
}
