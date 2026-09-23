// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 后端时间解析与相对时间格式化。
 *
 * 统一收口的理由：后端返回的是 'YYYY-MM-DD HH:mm:ss' 这类**空格分隔**字符串，
 * 而 new Date('2026-09-23 12:00:00') 在 Safari / 旧 WebKit 上得到 Invalid Date
 * —— V8 出于兼容接受了这种非标准写法，所以本地开发永远发现不了。
 * 页面不要各自 new Date。
 */

/**
 * 时间戳按秒还是按毫秒：秒级时间戳当前是 10 位（约 1.7e9），毫秒级是 13 位（约 1.7e12），
 * 以 1e11 分界 —— 若把 1e11 当毫秒只到 1973 年，当秒则是 5138 年，不存在歧义区间。
 */
const SECOND_TIMESTAMP_MAX = 1e11

function fromTimestamp(n) {
  if (!Number.isFinite(n)) return null
  const parsed = new Date(Math.abs(n) < SECOND_TIMESTAMP_MAX ? n * 1000 : n)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

/**
 * 解析后端时间字符串 / 时间戳，无法解析返回 null。
 *
 * 支持：Date 对象、秒级或毫秒级时间戳（数字或纯数字串）、
 *      'YYYY-MM-DD HH:mm:ss'、'YYYY/MM/DD HH:mm:ss'、带时区的 ISO 串、RFC 2822。
 *
 * @param {string|number|Date|null|undefined} value
 * @returns {Date|null} 解析失败返回 null（**不是** new Date() 兜底，兜底会把未知显示成"刚刚"）
 */
export function parseBackendTime(value) {
  if (value instanceof Date) {
    return Number.isNaN(value.getTime()) ? null : value
  }
  if (typeof value === 'number') {
    return fromTimestamp(value)
  }
  if (value === null || value === undefined) {
    return null
  }

  const raw = String(value).trim()
  if (!raw || raw === '-' || raw === '—' || raw === 'null' || raw === 'undefined') {
    return null
  }

  // 纯数字串按时间戳处理，与上面的数字分支同一套秒/毫秒判断。
  // 不能直接 new Date(raw)：那会把 10 位秒级时间戳当成毫秒，解析到 1970 年
  if (/^\d+$/.test(raw)) {
    return fromTimestamp(Number(raw))
  }

  // 关键一步：'YYYY-MM-DD HH:mm:ss' → 'YYYY-MM-DDTHH:mm:ss'。
  // 按 ES 规范，不带时区的 'T' 形式一律按**本地时间**解析，各浏览器一致；
  // 带空格的写法属「实现自定义」格式，Safari 直接判 Invalid Date。
  const normalized = raw.replace(/\//g, '-').replace(' ', 'T')
  const parsed = new Date(normalized)
  if (!Number.isNaN(parsed.getTime())) {
    return parsed
  }

  // 最后兜底：原始串再试一次（RFC 2822 之类）
  const fallback = new Date(raw)
  return Number.isNaN(fallback.getTime()) ? null : fallback
}

/**
 * 距现在多少秒，无法解析返回 null。
 * 未来时间（客户端与服务端时钟偏差）返回 0 而非负数。
 *
 * @param {string|number|Date|null|undefined} value
 * @param {number} [now] 当前时间戳，便于测试注入
 * @returns {number|null}
 */
export function secondsSince(value, now = Date.now()) {
  const parsed = parseBackendTime(value)
  if (!parsed) return null
  return Math.max(0, Math.round((now - parsed.getTime()) / 1000))
}

/**
 * 相对时间文案（如「12 秒前」），无法解析返回 null。
 *
 * t 由调用方注入而非在此 import i18n：util 保持零依赖，且调用方在 computed
 * 内传 useI18n() 的 t 时，切换语言能自动重算。
 *
 * @param {string|number|Date|null|undefined} value
 * @param {Function} t vue-i18n 的翻译函数
 * @param {number} [now] 当前时间戳，便于测试注入
 * @returns {string|null} 解析失败返回 null，由调用方决定回退成原始串还是 '—'
 */
export function formatRelativeTime(value, t, now = Date.now()) {
  const seconds = secondsSince(value, now)
  if (seconds === null) return null
  if (seconds < 10) return t('common.justNow')
  if (seconds < 60) return t('common.secondsAgo', { n: seconds })
  if (seconds < 3600) return t('common.minutesAgo', { n: Math.floor(seconds / 60) })
  if (seconds < 86400) return t('common.hoursAgo', { n: Math.floor(seconds / 3600) })
  return t('common.daysAgo', { n: Math.floor(seconds / 86400) })
}
