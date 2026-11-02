// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 设备地址报警条件的操作数编解码。
 *
 * 库里存的是 JSON 数组字符串（如 ["10","40"]），前端表单用数组、表格用摘要文本。
 * 地址编辑框（AddressFormDialog）与地址表格（conditionText）都要解析同一份数据，
 * 放在这里是为了避免两处各写一份解析逻辑后悄悄漂移。
 */

// 把库里的 JSON 数组字符串解析成数组。坏数据/空值退回空数组（等价于未配置）。
export function parseConditionValues(raw) {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.map((v) => String(v)) : []
  } catch {
    return []
  }
}

// 把表单条件整理成交付后端的形态：去掉尾部空槽位，并按其算子应有的形状归一。
// 后端要求 range 恰好两个槽位（位置固定：下限、上限），所以那里**保留**空串占位。
export function buildConditionValues(op, values) {
  const list = values ?? []
  if (op === 'range') {
    return [String(list[0] ?? ''), String(list[1] ?? '')]
  }
  if (op === 'in') {
    return list.map((v) => String(v ?? '').trim()).filter((v) => v !== '')
  }
  return list.length ? [String(list[0] ?? '').trim()] : []
}
