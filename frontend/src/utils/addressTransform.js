// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 设备地址工程量变换的表格摘要。
 *
 * 库里存的是有序算子流水线的 JSON 字符串（如 `[{"op":"scale","factor":0.1}]`），
 * 地址编辑框（AddressFormDialog）逐行编辑，点位配置列表只需要一行可读摘要。
 * 算子白名单与编辑框共用 ADDRESS_TRANSFORM_OPS，避免两处各自维护后悄悄漂移。
 */

import { ADDRESS_TRANSFORM_OPS } from './pushMapping'

// parseAddressTransformPipeline 把库里的 JSON 解析成算子列表。
// 坏数据/空值退回空数组（等价于未配置），白名单之外的算子同样丢弃。
export function parseAddressTransformPipeline(raw) {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item) => item && ADDRESS_TRANSFORM_OPS.includes(item.op))
  } catch {
    return []
  }
}

// 偏移量按数学写法带符号；0 是 scale 的默认值，显示出来只是噪音。
function signedOffset(raw) {
  if (raw == null) return ''
  const value = String(raw).trim()
  if (value === '' || Number(value) === 0) return ''
  return Number(value) > 0 ? `+${value}` : value
}

function describeTransform(t, item) {
  switch (item.op) {
    case 'scale': {
      const factor = item.factor == null ? '1' : String(item.factor)
      const offset = signedOffset(item.offset)
      return offset
        ? t('deviceAddress.transformSummary.scaleOffset', { factor, offset })
        : t('deviceAddress.transformSummary.scale', { factor })
    }
    case 'round':
      return t('deviceAddress.transformSummary.round', {
        digits: item.digits == null ? '0' : String(item.digits),
      })
    case 'trim':
      return t('deviceAddress.transformSummary.trim')
    case 'default':
      return t('deviceAddress.transformSummary.default', { value: String(item.value ?? '') })
    default:
      return item.op
  }
}

// addressTransformText 返回流水线的可读摘要（如「缩放 ×0.1 → 四舍五入 2 位」）。
// 未配置或无法解析时返回空串，由调用方决定空态文案。
export function addressTransformText(t, raw) {
  const items = parseAddressTransformPipeline(raw)
  if (!items.length) return ''
  return items.map((item) => describeTransform(t, item)).join(' → ')
}
