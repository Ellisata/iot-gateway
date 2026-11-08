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
	// Prepare 表级预校验钩子：在表头校验之后、事务与逐行写入之前调用一次。
	// 返回错误时整表中止、不写入任何行。用于「整表必须同一协议」这类
	// 无法在单行 Parse 里表达的约束。
	Prepare func(rows [][]string) error
	// Insert 可选的入库回调，默认 tx.Create(record)。
	// 记录里带 gorm default 标签的零值字段（如 device.status=0）会被 GORM 省略，
	// 这类调用方需要用 tx.Select("*").Create(record) 显式写入零值。
	Insert func(tx *gorm.DB, record *T) error
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
	if opt.Prepare != nil {
		if err := opt.Prepare(rows); err != nil {
			return nil, err
		}
	}

	existing := make(map[string]bool, len(opt.ExistingKeys))
	for _, k := range opt.ExistingKeys {
		existing[k] = true
	}
	seen := make(map[string]bool) // 文件内已出现的 key，用于识别文件内重复

	insert := opt.Insert
	if insert == nil {
		insert = func(tx *gorm.DB, record *T) error {
			return tx.Create(record).Error
		}
	}

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
			if err := insert(tx, record); err != nil {
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

// IsBlankRow 判断整行是否全为空（缺失列视为空）。
func IsBlankRow(row []string) bool {
	return isBlank(row, 0)
}

// isBlank 判断前 cols 列是否全为空（缺失列视为空）。
// cols <= 0 表示检查整行 —— 表头列数由动态协议参数决定时使用。
func isBlank(row []string, cols int) bool {
	n := cols
	if n <= 0 || len(row) < n {
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

// TemplateOptions 导入模板生成参数。
type TemplateOptions struct {
	Headers []string
	Example []string
	Widths  []float64
	Lists   []ListValidation

	// SheetName 首个工作表名（空表示保留 excelize 默认名）。
	SheetName string
	// ExampleSheetName 非空时把表头+示例行写到第二个工作表，首个工作表只放表头。
	// 导入框架只读首个工作表，因此示例行不会在用户直接导入模板时被当成数据。
	ExampleSheetName string
	// TextCols 需设置为文本格式的列下标（0 起）：编码、IP、串口等，
	// 避免 Excel 把 000123 这类值吃掉前导零或转成科学计数法。
	TextCols []int
	// DataRows 首表数据行（从第 2 行起）。非空时替代首表示例行 ——
	// 用于「按模板导出已有数据」（如扫描结果导出为导入模板）。
	DataRows [][]string
	// RequiredCols 必填列下标。非 nil 时表头按「必填（深蓝+红下边）/选填（浅蓝）」
	// 分色；nil 表示不区分（附加说明表沿用统一蓝底）。
	RequiredCols []int
	// Legend 示例工作表中的图例行（仅 ExampleSheetName 非空时写入）。
	Legend string
	// ExtraSheets 附加说明页/字典页。每张表只写表头 + 数据行，首行冻结。
	ExtraSheets []TemplateSheet
}

// TemplateSheet 附加工作表（如逐列填写说明）。
type TemplateSheet struct {
	Name    string
	Headers []string
	Rows    [][]string
	Widths  []float64
}

// BuildTemplate 生成带表头 + 示例行 + 冻结首行的导入模板（.xlsx）。
func BuildTemplate(headers, example []string, widths []float64) (*bytes.Buffer, error) {
	return BuildTemplateWithLists(headers, example, widths, nil)
}

// BuildTemplateWithLists 与 BuildTemplate 相同，并给指定列加下拉选择。
func BuildTemplateWithLists(headers, example []string, widths []float64, lists []ListValidation) (*bytes.Buffer, error) {
	return BuildTemplateWithOptions(TemplateOptions{
		Headers: headers,
		Example: example,
		Widths:  widths,
		Lists:   lists,
	})
}

// BuildTemplateWithOptions 生成导入模板。除表头/示例/列宽/下拉外，支持把示例行
// 放进单独工作表，以及把指定列设为文本格式。
func BuildTemplateWithOptions(opt TemplateOptions) (*bytes.Buffer, error) {
	f := excelize.NewFile()
	defer f.Close()

	sheet := f.GetSheetName(0)
	if opt.SheetName != "" {
		if err := f.SetSheetName(sheet, opt.SheetName); err != nil {
			return nil, err
		}
		sheet = opt.SheetName
	}

	// 首表数据行：DataRows 优先；否则示例行放首表（未指定示例表时）。
	var firstRows [][]string
	switch {
	case len(opt.DataRows) > 0:
		firstRows = opt.DataRows
	case opt.ExampleSheetName == "" && len(opt.Example) > 0:
		firstRows = [][]string{opt.Example}
	}
	if err := writeTemplateSheet(f, sheet, opt.Headers, firstRows, opt.Widths, opt.TextCols, opt.RequiredCols); err != nil {
		return nil, err
	}

	if opt.ExampleSheetName != "" {
		if _, err := f.NewSheet(opt.ExampleSheetName); err != nil {
			return nil, err
		}
		var exampleRows [][]string
		if len(opt.Example) > 0 {
			exampleRows = [][]string{opt.Example}
		}
		if err := writeTemplateSheet(f, opt.ExampleSheetName, opt.Headers, exampleRows, opt.Widths, opt.TextCols, opt.RequiredCols); err != nil {
			return nil, err
		}
		if opt.Legend != "" {
			// 示例行固定写在第 2 行（数据只有一行）。
			if err := writeLegend(f, opt.ExampleSheetName, opt.Legend, 1); err != nil {
				return nil, err
			}
		}
	}

	for _, extra := range opt.ExtraSheets {
		if strings.TrimSpace(extra.Name) == "" {
			continue
		}
		if _, err := f.NewSheet(extra.Name); err != nil {
			return nil, err
		}
		if err := writeDataSheet(f, extra.Name, extra.Headers, extra.Rows, extra.Widths); err != nil {
			return nil, err
		}
	}

	// 列下拉选择：用户从固定枚举里选，避免手填出模板不认识的取值。
	for _, lv := range opt.Lists {
		if lv.Col < 0 || lv.Col >= len(opt.Headers) {
			return nil, fmt.Errorf("下拉列下标越界：%d", lv.Col)
		}
		if len(lv.Options) == 0 {
			continue
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

func writeTemplateSheet(f *excelize.File, sheet string, headers []string, rows [][]string, widths []float64, textCols, requiredCols []int) error {
	for r, row := range rows {
		for j, v := range row {
			cell, err := excelize.CoordinatesToCellName(j+1, r+2)
			if err != nil {
				return err
			}
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				return err
			}
		}
	}

	for i, w := range widths {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return err
		}
		if err := f.SetColWidth(sheet, col, col, w); err != nil {
			return err
		}
	}

	// 文本列：整列设文本格式，避免前导零/长数字被 Excel 自动转换。
	if len(textCols) > 0 {
		textStyle, err := f.NewStyle(&excelize.Style{NumFmt: 49}) // 49 = "@"
		if err != nil {
			return err
		}
		for _, idx := range textCols {
			if idx < 0 || idx >= len(headers) {
				return fmt.Errorf("文本列下标越界：%d", idx)
			}
			col, err := excelize.ColumnNumberToName(idx + 1)
			if err != nil {
				return err
			}
			if err := f.SetColStyle(sheet, col, textStyle); err != nil {
				return err
			}
		}
	}

	// 表头最后写：SetColStyle 会覆盖整列已有单元格样式（包括表头），
	// 放在文本列格式之后才能让必填/选填配色保留。
	if err := writeHeaderRow(f, sheet, headers, requiredCols); err != nil {
		return err
	}

	return f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2"})
}

// writeDataSheet 写一张「表头 + 多行数据」的辅助表（如逐列填写说明）。
func writeDataSheet(f *excelize.File, sheet string, headers []string, rows [][]string, widths []float64) error {
	if err := writeHeaderRow(f, sheet, headers, nil); err != nil {
		return err
	}
	for r, row := range rows {
		for c, value := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				return err
			}
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				return err
			}
		}
	}
	for i, w := range widths {
		col, err := excelize.ColumnNumberToName(i + 1)
		if err != nil {
			return err
		}
		if err := f.SetColWidth(sheet, col, col, w); err != nil {
			return err
		}
	}
	return f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2"})
}

// writeHeaderRow 写表头。
//
// requiredCols 为 nil 时沿用统一蓝底（附加说明表）；非 nil 时按必填/选填分色：
// 必填列深蓝底 + 红色下边框，选填列浅蓝底，配合列名末尾的 * 形成双重提示。
func writeHeaderRow(f *excelize.File, sheet string, headers []string, requiredCols []int) error {
	styled := requiredCols != nil
	required := make(map[int]bool, len(requiredCols))
	for _, col := range requiredCols {
		required[col] = true
	}

	var requiredStyle, optionalStyle, plainStyle int
	var err error
	if styled {
		requiredStyle, err = f.NewStyle(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"2F5FD0"}},
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
			Border:    []excelize.Border{{Type: "bottom", Color: "E4572E", Style: 2}},
		})
		if err != nil {
			return err
		}
		optionalStyle, err = f.NewStyle(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Color: "23406B"},
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DCE9FF"}},
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
			Border:    []excelize.Border{{Type: "bottom", Color: "C9D8F2", Style: 1}},
		})
		if err != nil {
			return err
		}
	} else {
		plainStyle, err = f.NewStyle(&excelize.Style{
			Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
			Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"409EFF"}},
			Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		})
		if err != nil {
			return err
		}
	}

	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return err
		}
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			return err
		}
		styleID := plainStyle
		if styled {
			styleID = optionalStyle
			if required[i] {
				styleID = requiredStyle
			}
		}
		if err := f.SetCellStyle(sheet, cell, cell, styleID); err != nil {
			return err
		}
	}
	if styled {
		return f.SetRowHeight(sheet, 1, 28)
	}
	return nil
}

// writeLegend 在示例表数据下方写一行图例，说明 * 与表头配色的含义。
func writeLegend(f *excelize.File, sheet, legend string, exampleRows int) error {
	row := exampleRows + 3 // 表头 1 + 示例行 + 空行
	if row < 3 {
		row = 3
	}
	cell, err := excelize.CoordinatesToCellName(1, row)
	if err != nil {
		return err
	}
	if err := f.SetCellValue(sheet, cell, legend); err != nil {
		return err
	}
	styleID, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Italic: true, Color: "8A94A6", Size: 10},
	})
	if err != nil {
		return err
	}
	return f.SetCellStyle(sheet, cell, cell, styleID)
}
