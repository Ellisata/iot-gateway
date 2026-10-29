// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { formCreateLocaleName } from '@/i18n/dynamicFormText'

/**
 * 按当前语言取一段插件声明的文案（扫描能力的标题/描述等）。
 *
 * 插件在 SDK 字段里给的是可选的 i18n map（键 zh-cn / en），源语言中文由基础字段
 * （title / description）承载。回退顺序：当前语言 → zh-cn → 基础字段 ——
 * 旧插件不带 map、或只填了部分语言时都能拿到非空文案。
 */
export function resolveLocalizedText(base, i18n, locale) {
  if (!i18n) return base
  const current = formCreateLocaleName(locale)
  return i18n[current] || i18n['zh-cn'] || base
}

/**
 * 动态表单的 label 列宽：英文标签明显比中文长（如 "Per-target timeout (ms)"），
 * 沿用中文宽度会把标签折成两行。中文保留表单声明的宽度，英文给一个够放最长
 * 标签的固定宽度；同一页面里外层 el-form 的 labelWidth 要跟着一起变，否则
 * 两条 label 列会对不齐。
 */
export function dynamicFormLabelWidth(baseWidth, locale, enWidth = '200px') {
  return formCreateLocaleName(locale) === 'en' ? enWidth : baseWidth
}

// Element Plus 的 label 默认字号/字体；测量时优先取页面上已渲染 label 的实际样式，
// 免得和主题里改过的字号脱节
const DEFAULT_LABEL_FONT = '14px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif'

let measureCanvas

function labelFont() {
  const el = typeof document !== 'undefined' ? document.querySelector('.el-form-item__label') : null
  if (!el) return DEFAULT_LABEL_FONT
  const cs = getComputedStyle(el)
  return `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`
}

function measureLabelTextWidth(texts) {
  if (!measureCanvas) measureCanvas = document.createElement('canvas')
  const ctx = measureCanvas.getContext('2d')
  ctx.font = labelFont()
  return texts.reduce((max, text) => Math.max(max, ctx.measureText(text).width), 0)
}

/**
 * 动态表单 label 列宽 —— 按当前规则的**实际标题**测算，而不是给一个固定宽度。
 * 固定宽度对短标签的协议（Device port 只要 ~75px）会把整张表单往右推，
 * 对长标签又可能折行；这里实测当前语言下的最长标题，再给必填星号（~10px）
 * 与 label 的右内边距（12px）留余量。中文仍用表单声明的宽度：设计器按中文排版。
 *
 * @param {Array} rules - 已按当前语言本地化的 rule（标题是最终展示文案）
 * @param {string} locale - vue-i18n 当前语言
 * @param {string} baseWidth - 表单声明的 labelWidth（中文沿用）
 * @param {{min?: number, max?: number}} [bounds] - 测算结果的像素边界
 */
export function dynamicFormLabelWidthForRules(rules, locale, baseWidth, bounds = {}) {
  if (formCreateLocaleName(locale) !== 'en') return baseWidth
  const titles = (rules || [])
    .map((rule) => rule?.title)
    .filter((title) => typeof title === 'string' && title)
  if (!titles.length) return baseWidth
  const { min = 96, max = 320 } = bounds
  const width = Math.ceil(measureLabelTextWidth(titles)) + 26
  return `${Math.min(max, Math.max(min, width))}px`
}
