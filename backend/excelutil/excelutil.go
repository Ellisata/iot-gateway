// Package excelutil 提供 Excel(.xlsx) 批量导入的通用框架与模板生成工具。
// 各业务（设备、设备地址等）只需提供行解析回调，解析/表头校验/空行跳过/
// 去重/事务/逐行续入/汇总等样板逻辑统一在此处理。
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package excelutil

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/enums"
	"iot-gateway/model/vo"
)

// RowFunc 解析单行数据。返回 (入库记录, 用于去重的 key/名称, 失败原因)。
// reason 非空表示该行跳过并记录；record 不为 nil 且 reason 为空才入库。
type RowFunc[T any] func(row []string) (*T, string, string)

// Options 导入框架参数
type Options[T any] struct {
	// Headers 表头强校验（前 N 列须逐一匹配；空表示不校验）
	Headers []string
	// ExistingKeys 库内已存在的去重 key（如设备名 / 设备地址名）
	ExistingKeys []string
	// Cols 模板列数；前 Cols 列全空的行视为空行跳过
	Cols int
	// DupMsg 名称重复（库内或文件内）时的失败原因
	DupMsg string
	// Label 失败明细里展示的行标识。为空时退回 Parse 返回的去重 key。
	// 去重 key 需要唯一（如属性标识符），但给人看的通常是名称 —— 两者不是一回事。
	Label func(row []string) string
	// Parse 单行解析回调
	Parse RowFunc[T]
}

// Import 通用 Excel(.xlsx) 批量导入：
// 打开文件 → 读取首个工作表 → 表头校验 → 逐行跳过空行/计数 → Parse 校验映射 →
// 去重 → 事务内逐行入库（单行失败只记错误不回滚）→ 返回汇总。
func Import[T any](ctx context.Context, db *gorm.DB, data []byte, opt Options[T]) (*vo.ExcelImportResultVO, error) {
	rows, err := OpenRows(data)
	if err != nil {
		return nil, err
	}

	result := &vo.ExcelImportResultVO{Errors: []vo.ExcelImportErrorVO{}}

	// 仅表头或无数据：不算错误，返回空汇总
	if len(rows) < 2 {
		return result, nil
	}

	if len(opt.Headers) > 0 {
		if err := CheckHeader(rows, opt.Headers...); err != nil {
			return nil, err
		}
	}

	existing := make(map[string]bool, len(opt.ExistingKeys))
	for _, k := range opt.ExistingKeys {
		existing[k] = true
	}
	seen := make(map[string]bool) // 文件内已出现的 key，用于识别文件内重复

	// 整体事务提交；单行插入失败只记错误，不回滚其余行
	_ = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i := 1; i < len(rows); i++ {
			excelRow := i + 1 // 含表头行号
			row := rows[i]
			// 全空行跳过
			if isBlank(row, opt.Cols) {
				continue
			}
			result.Total++

			record, key, reason := opt.Parse(row)
			name := key
			if opt.Label != nil {
				if label := opt.Label(row); label != "" {
					name = label
				}
			}
			if reason != "" {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{Row: excelRow, Name: name, Reason: reason})
				continue
			}
			if existing[key] || seen[key] {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{Row: excelRow, Name: name, Reason: opt.DupMsg})
				continue
			}
			if err := tx.Create(record).Error; err != nil {
				result.Errors = append(result.Errors, vo.ExcelImportErrorVO{Row: excelRow, Name: name, Reason: "入库失败：" + err.Error()})
				continue
			}
			existing[key] = true
			seen[key] = true
			result.Success++
		}
		return nil
	})

	result.Failed = len(result.Errors)
	return result, nil
}

// OpenRows 打开 Excel 并返回首个工作表的所有行。
func OpenRows(data []byte) ([][]string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), "Excel 解析失败，请使用下载的 .xlsx 模板", enums.ParamValidEnum.GetMsgKey())
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), "Excel 工作表读取失败", enums.ParamValidEnum.GetMsgKey())
	}
	return rows, nil
}

// CheckHeader 校验表头：前 len(expected) 列须与期望值逐一相等（去空白后）。
func CheckHeader(rows [][]string, expected ...string) error {
	if len(rows) == 0 {
		return appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), "Excel 内容为空", enums.ParamValidEnum.GetMsgKey())
	}
	for i, want := range expected {
		if len(rows[0]) <= i || strings.TrimSpace(rows[0][i]) != want {
			return appError.NewAppErrorCtx(enums.ParamValidEnum.GetCode(), fmt.Sprintf("模板表头不正确：第 %d 列须为「%s」", i+1, want), enums.ParamValidEnum.GetMsgKey())
		}
	}
	return nil
}

// Cell 安全读取行中第 idx 列（越界视为空串），并去除首尾空白。
func Cell(row []string, idx int) string {
	if idx < len(row) {
		return strings.TrimSpace(row[idx])
	}
	return ""
}

// isBlank 判断前 cols 列是否全为空（缺失列视为空）。
func isBlank(row []string, cols int) bool {
	n := cols
	if len(row) < n {
		n = len(row)
	}
	for i := 0; i < n; i++ {
		if strings.TrimSpace(row[i]) != "" {
			return false
		}
	}
	return true
}

// ListValidation 为模板某一列附加下拉选择（Excel 数据验证）。
// Col 是 0 起的列下标；校验作用于第 2 行起的 templateValidationRows 行。
type ListValidation struct {
	Col     int
	Options []string
	// Prompt 输入提示（可选）。留空则不显示提示气泡。
	Prompt string
	// AllowCustom 允许手工输入下拉项以外的值。默认为 false（严格）：
	// 取值必须来自下拉项，适合固定枚举。协议扩展类型这类无法穷举的列应置 true。
	AllowCustom bool
}

// templateValidationRows 下拉校验覆盖的数据行数（从第 2 行开始）。
// 与模板的 1000 行预留空间一致，避免用户往下填几行就失去下拉。
const templateValidationRows = 1000

// BuildTemplate 生成带表头 + 示例行 + 冻结首行的导入模板（.xlsx）。
func BuildTemplate(headers, example []string, widths []float64) (*bytes.Buffer, error) {
	return BuildTemplateWithLists(headers, example, widths, nil)
}

// BuildTemplateWithLists 与 BuildTemplate 相同，并给指定列加下拉选择。
func BuildTemplateWithLists(headers, example []string, widths []float64, lists []ListValidation) (*bytes.Buffer, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := f.GetSheetName(0)

	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return nil, err
		}
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			return nil, err
		}
	}
	// 表头加粗 + 蓝底白字
	styleID, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"409EFF"}},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	})
	if err != nil {
		return nil, err
	}
	lastCell, err := excelize.CoordinatesToCellName(len(headers), 1)
	if err != nil {
		return nil, err
	}
	if err := f.SetCellStyle(sheet, "A1", lastCell, styleID); err != nil {
		return nil, err
	}

	// 示例行（可删除或覆盖）
	for j, v := range example {
		cell, err := excelize.CoordinatesToCellName(j+1, 2)
		if err != nil {
			return nil, err
		}
		if err := f.SetCellValue(sheet, cell, v); err != nil {
			return nil, err
		}
	}

	// 列宽
	for i, w := range widths {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return nil, err
		}
		if err := f.SetColWidth(sheet, col, col, w); err != nil {
			return nil, err
		}
	}
	// 冻结首行
	if err := f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2"}); err != nil {
		return nil, err
	}

	// 列下拉选择：用户从固定枚举里选，避免手填出模板不认识的取值。
	for _, lv := range lists {
		if lv.Col < 0 || lv.Col >= len(headers) {
			return nil, fmt.Errorf("下拉列下标越界：%d", lv.Col)
		}
		col, err := excelize.ColumnNumberToName(lv.Col + 1)
		if err != nil {
			return nil, err
		}
		dv := excelize.NewDataValidation(true)
		dv.Sqref = fmt.Sprintf("%s2:%s%d", col, col, templateValidationRows+1)
		if err := dv.SetDropList(lv.Options); err != nil {
			return nil, err
		}
		if !lv.AllowCustom {
			dv.SetError(excelize.DataValidationErrorStyleStop, "取值不合法", "请从下拉列表中选择")
		}
		if lv.Prompt != "" {
			dv.SetInput("请选择", lv.Prompt)
		}
		if err := f.AddDataValidation(sheet, dv); err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return &buf, nil
}
