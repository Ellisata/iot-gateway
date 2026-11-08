// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/excelutil"
	"iot-gateway/formjson"
	"iot-gateway/i18n"
	"iot-gateway/model/po"
)

// newDeviceTemplateOptions 生成模板的固定六列与通用选项（表头/示例/列宽/下拉/配色）。
func newDeviceTemplateOptions(lang string) excelutil.TemplateOptions {
	base := newDeviceTemplateBase(lang)
	return excelutil.TemplateOptions{
		Headers:          base.headers,
		Example:          base.example,
		Widths:           base.widths,
		Lists:            base.lists,
		SheetName:        i18n.T(lang, "device.import.sheet.name"),
		ExampleSheetName: i18n.T(lang, "device.import.sheet.example"),
		TextCols:         base.textCols,
		RequiredCols:     append([]int(nil), base.requiredCols...),
		Legend:           i18n.T(lang, "device.import.note.legend"),
	}
}

// buildGenericDeviceTemplate 通用模板（基础六列，协议列给全部协议名作参考下拉）。
func buildGenericDeviceTemplate(ctx context.Context, db *gorm.DB, lang string) (excelutil.TemplateOptions, error) {
	spec := newDeviceTemplateOptions(lang)
	var names []string
	if err := db.WithContext(ctx).Model(&po.IotProtocol{}).
		Order("sort asc, name asc").Pluck("name", &names).Error; err != nil {
		return spec, err
	}
	// 协议可能很多/名字很长：放不下内联下拉时跳过，协议列仍允许手输。
	if options, ok := safeValidationOptions(names); ok {
		spec.Lists = append(spec.Lists, excelutil.ListValidation{
			Col: importColProtocol, Options: options, AllowCustom: true,
		})
	}
	return spec, nil
}

// buildProtocolDeviceTemplate 指定协议的完整模板：基础六列 + formJson 顶层标量字段。
//
// 模板生成与扫描结果导出共用这一份，保证两条路径产出的列名、顺序、下拉与
// 必填标记完全一致 —— 导出的文件必须能直接被设备导入读回去。
func buildProtocolDeviceTemplate(
	ctx context.Context,
	db *gorm.DB,
	protocolID, lang string,
) (excelutil.TemplateOptions, *formjson.Document, po.IotProtocol, error) {
	spec := newDeviceTemplateOptions(lang)

	var protocol po.IotProtocol
	if err := db.WithContext(ctx).Where("id = ?", protocolID).First(&protocol).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return spec, nil, po.IotProtocol{}, appError.NewAppErrorCtx(
				enums.ProtocolNotExistsEnum.GetCode(),
				enums.ProtocolNotExistsEnum.GetMessage(),
				enums.ProtocolNotExistsEnum.GetMsgKey(),
			)
		}
		return spec, nil, po.IotProtocol{}, err
	}

	doc, err := formjson.Parse([]byte(protocol.FormJSON), lang)
	if err != nil {
		return spec, nil, protocol, importTableError(fmt.Sprintf("协议「%s」的配置表单无法解析：%v", protocol.Name, err))
	}
	if len(doc.Unsupported) > 0 {
		return spec, nil, protocol, importTableError(fmt.Sprintf(
			"协议「%s」的表单包含无法用 Excel 表达的分组或集合字段，请使用设备编辑页填写",
			protocol.Name,
		))
	}

	spec.Example[importColProtocol] = protocol.Name
	// 指定协议模板：协议列锁死为该协议，从 UI 上强化整表同协议约束。
	spec.Lists = append(spec.Lists, excelutil.ListValidation{Col: importColProtocol, Options: []string{protocol.Name}})

	for _, field := range doc.Fields {
		col := len(spec.Headers)
		spec.Headers = append(spec.Headers, importParamHeader(field))
		spec.Widths = append(spec.Widths, importParamWidth(field.Title))
		if field.Required {
			spec.RequiredCols = append(spec.RequiredCols, col)
		}
		if !field.IsNumber() {
			spec.TextCols = append(spec.TextCols, col)
		}
		if field.IsSelect() {
			labels := make([]string, 0, len(field.Options))
			for _, option := range field.Options {
				labels = append(labels, option.Label)
			}
			if options, ok := safeValidationOptions(labels); ok {
				spec.Lists = append(spec.Lists, excelutil.ListValidation{
					Col:     col,
					Options: options,
					Prompt:  truncateRunes(field.Info, 200),
				})
			}
		}
		spec.Example = append(spec.Example, importExampleValue(field))
	}

	return spec, doc, protocol, nil
}
