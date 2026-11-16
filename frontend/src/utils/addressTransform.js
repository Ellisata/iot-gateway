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

// applyAddressTransformPipeline 用采集侧同一套算子把原始值换算成工程值。
//
// 只用于控制下发弹窗的只读预览：下发链路不做逆变换，输入框始终是原始值，
// 这里只是让操作工在写 PLC 前看到「这个原始值会对应什么工程值」。语义与
// 后端 mappingdsl.Transform.apply 一致（scale 默认 ×1+0、round 定长小数、
// trim 去空白、default 只在空值时回落）。换算失败返回 { ok:false }。
export function applyAddressTransformPipeline(raw, input) {
  const items = parseAddressTransformPipeline(raw)
  if (!items.length) return { ok: false, value: '' }
  let value = input == null ? '' : String(input)
  for (const item of items) {
    switch (item.op) {
      case 'default':
        if (value.trim() === '') value = String(item.value ?? '')
        break
      case 'trim':
        value = value.trim()
        break
      case 'scale': {
        const n = toFiniteNumber(value)
        if (n === null) return { ok: false, value: '' }
        const factor = item.factor == null ? 1 : Number(item.factor)
        const offset = item.offset == null ? 0 : Number(item.offset)
        value = formatDecimal(n * factor + offset)
        break
      }
      case 'round': {
        const n = toFiniteNumber(value)
        if (n === null) return { ok: false, value: '' }
        const digits = Math.min(12, Math.max(0, Number(item.digits) || 0))
        value = n.toFixed(digits)
        break
      }
      default:
        return { ok: false, value: '' }
    }
  }
  return { ok: true, value }
}

function toFiniteNumber(raw) {
  const text = String(raw).trim()
  if (text === '') return null
  const n = Number(text)
  return Number.isFinite(n) ? n : null
}

// 与 Go strconv.FormatFloat(v, 'f', -1, 64) 对齐：不用指数写法。
function formatDecimal(n) {
  if (!Number.isFinite(n)) return ''
  if (Number.isInteger(n)) return String(n)
  const text = String(n)
  if (!text.includes('e') && !text.includes('E')) return text
  return n.toFixed(20).replace(/0+$/, '').replace(/\.$/, '')
}
