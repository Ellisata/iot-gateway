// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package constants

import "strings"

// 物模型分类 code。
//
// 分类是产品内置的固定枚举，**不建字典表**：thing_model.category 只存 code，
// 展示名由前端 i18n 提供（thingModel.category.<code>），这样英文界面不会出现
// 中文分类名，也不会出现“传感器 / 传感器类 / sensor”这类自由文本漂移。
//
// 新增分类需要同时修改：本文件、两份 i18n 文案、以及属性/模型的 DTO 校验。
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

// IsValidThingModelCategory 报告 code 是否为合法分类。
// 空串表示“未分类”，同样合法。
func IsValidThingModelCategory(code string) bool {
	if code == ThingModelCategoryUnset {
		return true
	}
	return containsCode(ThingModelCategoryCodes, code)
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
