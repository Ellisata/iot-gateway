// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package vo

// ExcelImportErrCodeAddressExists 单行失败的**结构化**原因码：该设备的扫描地址
// 在设备表中已被另一台设备占用（判重规则见 service.scanIdentityOf）。
//
// 为什么除了给人看的 reason 之外还要一个码：reason 随文案与 i18n 调整而变，
// 客户端拿它做判断就是拿一个会变的东西当契约 —— 改一次措辞就静默失效。
// 前端正是靠这个码把"地址重复"的行挑出来请用户二次确认，再带上
// dto.CreateDevicesFromScanDTO.ConfirmDuplicate 重提一次。
const ExcelImportErrCodeAddressExists = "addressExists"

// ExcelImportErrorVO 单行导入失败明细（Excel 批量导入共用）
type ExcelImportErrorVO struct {
	Row    int    `json:"row"` // Excel 行号（含表头，从 1 计）
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Code 失败原因的稳定标识，可为空（绝大多数失败没有码，只有需要客户端
	// 特殊处理的那几种才有）。**只用于分辨，展示一律走 Reason** ——
	// 在界面上枚举 Code 等于把两种职责混成一种。
	Code string `json:"code,omitempty"`
}

// ExcelImportResultVO 批量导入结果汇总（Excel 批量导入共用）
type ExcelImportResultVO struct {
	Total   int                  `json:"total"`
	Success int                  `json:"success"`
	Failed  int                  `json:"failed"`
	Errors  []ExcelImportErrorVO `json:"errors"`
}
