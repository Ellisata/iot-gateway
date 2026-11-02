// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import "github.com/Ellisata/iot-gateway-sdk/driver"

// 标准数据类型类别（driver.KindXxx）的 code 与模板展示名。
//
// 物模型属性导入与订阅设备/点位导入共用这一份：两处都要给 Excel 模板生成
// 可读下拉，各写一份枚举会漂移成「下拉能选、导入却报不合法」。
// 中文名与前端 zh-CN 的 thingModel.kind.* / subscriptionDevicePoint.kind* 文案
// 保持一致；模板由后端生成，拿不到前端 i18n，**改一处须同步另一处**。
var (
	deviceKindCodes = []string{
		driver.KindBool, driver.KindInt, driver.KindUInt,
		driver.KindFloat, driver.KindString, driver.KindTime,
	}
	deviceKindDisplayNames = map[string]string{
		driver.KindBool:   "布尔",
		driver.KindInt:    "有符号整数",
		driver.KindUInt:   "无符号整数",
		driver.KindFloat:  "浮点",
		driver.KindString: "文本",
		driver.KindTime:   "时间",
	}
	deviceKinds = stringSet(deviceKindCodes)
)

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		out[v] = struct{}{}
	}
	return out
}
