// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package utils

// GroupBy 按 key 函数分组
func GroupBy[T any](items []T, keyFn func(T) string) map[string][]T {
	result := make(map[string][]T)
	for _, item := range items {
		key := keyFn(item)
		result[key] = append(result[key], item)
	}
	return result
}

// GroupByList 按 key 函数分组（每组列表）
func GroupByList[T any](items []T, keyFn func(T) string) map[string][]T {
	return GroupBy(items, keyFn)
}
