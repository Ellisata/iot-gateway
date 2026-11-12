// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package constants

import (
	"regexp"
	"strings"
)

// 物模型分类 code。
//
// 网关内置分类是固定枚举，用于网关本地管理端与导入模板：thing_model.category
// 只存 code，展示名由前端 i18n 提供（thingModel.category.<code>），这样英文界面
// 不会出现中文分类名，也不会出现“传感器 / 传感器类 / sensor”这类自由文本漂移。
//
// 开放接口（管理平台下发）允许使用平台自定义分类：平台是分类词表的权威源，
// 网关只校验格式（见 IsValidOpenApiThingModelCategory），不再把分类限制在
// 内置枚举内。这样新增分类只需平台维护，不需要网关跟着发版。
//
// 新增内置分类仍需同时修改：本文件、两份 i18n 文案、以及属性/模型的 DTO 校验。
// 刻意不在 SQL 里加 CHECK 约束 —— SQLite 修改 CHECK 需要重建表，分类扩展
// 的成本不该落在这里，校验统一放服务层。
const (
	ThingModelCategoryUnset    = ""         // 未分类
	ThingModelCategorySensor   = "sensor"   // 传感器
	ThingModelCategoryMeter    = "meter"    // 仪表
	ThingModelCategoryActuator = "actuator" // 执行器
	ThingModelCategoryRobot    = "robot"    // 机器人
	ThingModelCategoryPLC      = "plc"      // PLC / 控制器
	ThingModelCategoryOther    = "other"    // 其他
)

// ThingModelCategoryCodes 全部分类 code（按展示顺序，不含“未分类”）。
var ThingModelCategoryCodes = []string{
	ThingModelCategorySensor,
	ThingModelCategoryMeter,
	ThingModelCategoryActuator,
	ThingModelCategoryRobot,
	ThingModelCategoryPLC,
	ThingModelCategoryOther,
}

// IsValidThingModelCategory 报告 code 是否为网关内置分类。
// 空串表示“未分类”，同样合法。
func IsValidThingModelCategory(code string) bool {
	if code == ThingModelCategoryUnset {
		return true
	}
	return containsCode(ThingModelCategoryCodes, code)
}

// OpenApiThingModelCategoryPattern 开放接口下发的分类 code 格式。
// 小写字母开头，可含小写字母、数字、下划线、中划线，最长 32 位。
var OpenApiThingModelCategoryPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// IsValidThingModelCategoryCode 校验任意分类 code 的格式（内置或平台自定义）。
//
// 空串 = 未分类；只约束格式，不校验是否在内置枚举内。用于开放接口写入和
// 列表筛选这类“接受平台词表”的场景。
func IsValidThingModelCategoryCode(code string) bool {
	code = strings.TrimSpace(code)
	return code == "" || OpenApiThingModelCategoryPattern.MatchString(code)
}

// IsValidOpenApiThingModelCategory 校验开放接口下发的分类。
//
// 分类词表由平台维护，网关不再做白名单校验 —— 否则平台每加一个分类都要升级网关。
func IsValidOpenApiThingModelCategory(code string) bool {
	return IsValidThingModelCategoryCode(code)
}

// ThingModelCategoryDisplayNames 分类 code → 中文展示名，仅供 Excel 导入模板使用。
//
// 页面上的分类名仍由前端 i18n（thingModel.category.<code>）提供；这里是模板里
// 唯一的可读名来源 —— 模板由后端生成，拿不到前端文案。**改一处须同步另一处**。
var ThingModelCategoryDisplayNames = map[string]string{
	ThingModelCategorySensor:   "传感器",
	ThingModelCategoryMeter:    "仪表",
	ThingModelCategoryActuator: "执行器",
	ThingModelCategoryRobot:    "机器人",
	ThingModelCategoryPLC:      "PLC / 控制器",
	ThingModelCategoryOther:    "其他",
}

// ThingModelCategoryTemplateOptions 导入模板的下拉项，形如「传感器（sensor）」。
func ThingModelCategoryTemplateOptions() []string {
	return BuildEnumOptions(ThingModelCategoryCodes, ThingModelCategoryDisplayNames)
}

// ParseThingModelCategory 解析导入文件里的分类取值，返回归一后的 code。
// 接受：空串（未分类）、code（忽略大小写）、中文展示名、「展示名（code）」组合。
// ok 为 false 表示无法识别。
func ParseThingModelCategory(raw string) (code string, ok bool) {
	if strings.TrimSpace(raw) == "" {
		return ThingModelCategoryUnset, true
	}
	return ParseEnumValue(raw, ThingModelCategoryCodes, ThingModelCategoryDisplayNames)
}
