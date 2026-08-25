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

// generalStatusText 常用 CIP 通用状态码的人类可读描述（排错提示）。
// 完整列表见 CIP spec vol.1 表 3-6.1。真机最常见的是 0x04（标签路径无法解析）。
var generalStatusText = map[byte]string{
	0x01: "connection failure",
	0x02: "resource unavailable",
	0x03: "invalid parameter value",
	0x04: "path segment error (tag not found)",
	0x05: "path destination unknown",
	0x06: "partial transfer",
	0x07: "connection lost",
	0x08: "service not supported",
	0x0A: "attribute list error",
	0x0E: "attribute not settable",
	0x13: "data too large for response packet",
	0x1C: "invalid attribute",
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
