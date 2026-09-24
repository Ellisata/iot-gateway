// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 报警通知类型的展示名。
 *
 * 分工：**支持哪些类型由后端决定**（/alarmWebhook/listTypes，加一种类型后端加一行即可，
 * 前端自动出现），**怎么显示由前端决定**（i18n）。后端的 label 是中文，
 * 直接用它会让英文界面里混进中文，故这里按 type 取本地化文案、取不到再回落后端 label。
 *
 * 注意不要把类型硬编码成"可选列表"：这里的映射只负责翻译，不负责枚举。
 */

const TYPE_LABEL_KEYS = {
  dingtalk: 'alarmWebhook.typeDingtalk',
  wecom: 'alarmWebhook.typeWecom',
  feishu: 'alarmWebhook.typeFeishu',
  custom: 'alarmWebhook.typeCustom',
}

/**
 * 取类型的本地化展示名
 * @param {string} type - 类型标识（dingtalk / wecom / feishu / custom）
 * @param {string} [backendLabel] - 后端返回的 label，作为翻译缺失时的兜底
 * @param {(key: string) => string} t - vue-i18n 的 t 函数
 * @returns {string} 展示名
 */
export function webhookTypeLabel(type, backendLabel, t) {
  const key = TYPE_LABEL_KEYS[type]
  if (!key || typeof t !== 'function') {
    return backendLabel || type
  }
  const translated = t(key)
  // vue-i18n 未命中时原样返回 key，此时用后端 label 更友好
  return translated === key ? backendLabel || type : translated
}
