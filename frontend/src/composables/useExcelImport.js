// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

/**
 * Excel 批量导入组合式函数：封装文件选择、.xlsx 校验、上传、结果弹窗状态与模板下载。
 * @param {Object} opts
 * @param {(file: File) => Promise<{total:number, success:number, failed:number, errors:Array}>} opts.importFn - 导入请求（返回导入结果）
 * @param {(params?: Object) => Promise<Blob>} opts.downloadFn - 模板下载请求（返回 Blob）
 * @param {string|((params?: Object) => string)} opts.templateName - 模板下载的文件名（如 "设备导入模板.xlsx"）
 * @param {() => void} [opts.refresh] - 导入成功（至少 1 条）后刷新列表
 */
export function useExcelImport({ importFn, downloadFn, templateName, refresh }) {
  const { t } = useI18n()
  const fileInputRef = ref(null)
  const importing = ref(false)
  const importResultVisible = ref(false)
  const importResult = ref({ total: 0, success: 0, failed: 0, errors: [] })

  function handleImport() {
    fileInputRef.value?.click()
  }

  async function handleFileChange(e) {
    const file = e.target.files?.[0]
    e.target.value = '' // 允许重复选择同一文件
    if (!file) return
    if (!file.name.toLowerCase().endsWith('.xlsx')) {
      ElMessage.warning(t('excelImport.onlyXlsx'))
      return
    }
    importing.value = true
    try {
      const res = await importFn(file)
      importResult.value = res ?? { total: 0, success: 0, failed: 0, errors: [] }
      importResultVisible.value = true
      if (importResult.value.success > 0) refresh?.()
    } catch {
      // 错误提示已由 axios 拦截器统一处理
    } finally {
      importing.value = false
    }
  }

  /**
   * 下载模板。
   * @param {Object} [params] - 透传给 downloadFn 的参数（如 { protocolId }）
   * @param {string} [filename] - 覆盖 templateName 的文件名
   * @returns {Promise<boolean>} 是否下载成功
   */
  async function handleDownloadTemplate(params, filename) {
    try {
      const blob = await downloadFn(params)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = filename || (typeof templateName === 'function' ? templateName(params) : templateName)
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
      ElMessage.success(t('excelImport.templateDownloaded'))
      return true
    } catch {
      ElMessage.error(t('excelImport.templateDownloadFailed'))
      return false
    }
  }

  return {
    fileInputRef,
    importing,
    importResultVisible,
    importResult,
    handleImport,
    handleFileChange,
    handleDownloadTemplate,
  }
}
