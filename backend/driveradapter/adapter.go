// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// Package driveradapter 是宿主侧数据库模型与 SDK 驱动契约之间的转换层。
//
// 驱动的 Read 契约使用 SDK 的 driver.DeviceAddress（只含过线字段），而采集
// 引擎持有的是 SQLite 模型 po.DeviceAddress。两者刻意不互相依赖：SDK 以
// Apache-2.0 独立发布，不能 import AGPL 的核心；核心在调用驱动前做这一次转换。
package driveradapter

import (
	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/po"
)

// FromPO 把一个数据库点位转换成驱动契约里的地址。
// 只有 ID / Name / DataType 会过进程边界；CommonDataType 仅供宿主侧类型回退使用。
func FromPO(a po.DeviceAddress) driver.DeviceAddress {
	return driver.DeviceAddress{
		ID:             a.ID,
		Name:           a.Name,
		DataType:       a.DataType,
		CommonDataType: a.CommonDataType,
	}
}

// FromPOs 批量转换；nil 输入返回 nil，避免把空切片和非空切片混为一谈。
func FromPOs(addrs []po.DeviceAddress) []driver.DeviceAddress {
	if addrs == nil {
		return nil
	}
	out := make([]driver.DeviceAddress, len(addrs))
	for i := range addrs {
		out[i] = FromPO(addrs[i])
	}
	return out
}
