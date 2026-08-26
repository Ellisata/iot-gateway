package vo

// ExcelImportErrorVO 单行导入失败明细（Excel 批量导入共用）
type ExcelImportErrorVO struct {
	Row    int    `json:"row"` // Excel 行号（含表头，从 1 计）
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ExcelImportResultVO 批量导入结果汇总（Excel 批量导入共用）
type ExcelImportResultVO struct {
	Total   int                  `json:"total"`
	Success int                  `json:"success"`
	Failed  int                  `json:"failed"`
	Errors  []ExcelImportErrorVO `json:"errors"`
}
