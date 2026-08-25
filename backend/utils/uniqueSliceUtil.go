package utils

// UniqueSlice 泛型切片去重
func UniqueSlice[T comparable](items []T) []T {
	if len(items) == 0 {
		return items
	}
	seen := make(map[T]struct{}, len(items))
	result := make([]T, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}
