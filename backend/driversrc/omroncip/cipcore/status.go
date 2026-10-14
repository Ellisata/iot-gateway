// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"errors"
	"fmt"
)

// GeneralStatusError CIP 通用状态错误（设备已响应但拒绝该请求，
// 如变量不存在、路径错误、类型不符）。
// 状态非 0 说明连接是通的，不应据此判定连接断开、也不应中断整台设备读取。
type GeneralStatusError struct {
	status byte
}

// 明确表示"这个路径/标签我这儿没有"的通用状态码。它们与"设备暂时拒绝"
// （资源不可用、部分传输、连接丢失…）性质完全相反：前者是配置错，
// 设备状态怎么恢复都不会变；后者下一轮原样重试就可能拿到值。
//
// 判据与 rockwellcip 的 isAddressStatusError 逐字一致 —— 两个插件的 cipcore
// 是各自的副本（见 CLAUDE.md「插件之间不得互相 import」），这里必须自己持一份。
const (
	GeneralStatusPathSegmentError       byte = 0x04 // 路径段错误：标签路径解析不了
	GeneralStatusPathDestinationUnknown byte = 0x05 // 路径目标未知：路径指向的标签不存在
	GeneralStatusObjectDoesNotExist     byte = 0x16 // 对象不存在
)

// generalStatusText 常用 CIP 通用状态码的人类可读描述（排错提示）。
// 完整列表见 CIP spec vol.1 表 3-6.1。真机最常见的是 0x04（标签路径无法解析）。
var generalStatusText = map[byte]string{
	GeneralStatusPathSegmentError:       "path segment error (tag not found)",
	GeneralStatusPathDestinationUnknown: "path destination unknown",
	GeneralStatusObjectDoesNotExist:     "object does not exist",
	0x01:                                "connection failure",
	0x02:                                "resource unavailable",
	0x03:                                "invalid parameter value",
	0x06:                                "partial transfer",
	0x07:                                "connection lost",
	0x08:                                "service not supported",
	0x0A:                                "attribute list error",
	0x0E:                                "attribute not settable",
	0x13:                                "data too large for response packet",
	0x1C:                                "invalid attribute",
}

func (e *GeneralStatusError) Error() string {
	msg, ok := generalStatusText[e.status]
	if !ok {
		msg = "unknown"
	}
	return fmt.Sprintf("cip: general status 0x%02X (%s)", e.status, msg)
}

// NewGeneralStatusError 构造通用状态错误
func NewGeneralStatusError(status byte) error {
	return &GeneralStatusError{status: status}
}

// IsGeneralStatusError 判断错误是否为 CIP 通用状态错误（设备已响应但拒绝请求）。
func IsGeneralStatusError(err error) bool {
	var e *GeneralStatusError
	return errors.As(err, &e)
}

// IsAddressStatusError 这条通用状态是否等于设备在说"这个路径/标签我这儿没有"。
//
// 与 IsGeneralStatusError 的关系：后者是"设备响应但拒绝"（决定要不要把整台设备
// 判离线），这里是"拒绝的原因是关于地址的"（决定这个点位该归因到配置错还是
// 通信抖动）。两个问题，两个判据。
//
// 只认三个明确表示"不存在"的状态码，其余一律**不是**地址问题：0x02 资源不可用、
// 0x06 部分传输、0x13 数据过长等都是**可恢复的设备状态**，拿它们去"确认"某个
// 标签不存在，会把一整组点位误摘掉 —— 而摘掉的点位在界面上与"这个点位本来
// 就没有值"完全一样，没有任何线索。
func IsAddressStatusError(err error) bool {
	var e *GeneralStatusError
	if !errors.As(err, &e) {
		return false
	}
	switch e.status {
	case GeneralStatusPathSegmentError, GeneralStatusPathDestinationUnknown, GeneralStatusObjectDoesNotExist:
		return true
	default:
		return false
	}
}
