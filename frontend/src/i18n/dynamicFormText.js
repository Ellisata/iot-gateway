// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

/**
 * 动态表单文案国际化（运行期）。
 *
 * 动态表单的 rule 是运行时数据（表单设计器产出），所以**译文跟着表单走**：
 * 设计器里的标题/提示/选项可以写成 `{{$t.key}}`，译文存在 form_json 的
 * `options.language[locale][key]` 里（设计器左侧「多语言」面板）。
 * 本文件只做三件事：
 *   1. 把 vue-i18n 的语言名（zh-CN / en）换成 form-create 的语言名（zh-cn / en）；
 *   2. 在渲染前把 `{{$t.key}}` 解析成当前语言的文案 —— 当前语言缺列时回退中文，
 *      避免设计器只填了一列时标签变空白；
 *   3. 生成规则副本，原始 rule 保持不动（回填/收集字段仍以它为准）。
 *
 * 这里**不放任何中文→英文的词典**：那是数据，随表单存在 DB 里。
 * 存量表单（早期迁移写死中文）的迁移工具在 utils/dynamicFormMigration.js。
 */

/**
 * 应用语言名 => form-create / 表单设计器用的语言名。
 * 设计器的 localeOptions 与 options.language 的键是 `zh-cn` / `en`，与 vue-i18n 的
 * `zh-CN` 只差大小写 —— form-create 按字符串精确匹配，`zh-CN` 会取不到译文。
 */
export function formCreateLocaleName(locale) {
  return locale === 'zh-CN' ? 'zh-cn' : locale
}

const LANGUAGE_TOKEN = /^\{\{\s*\$t\.([\w.$-]+)\s*\}\}$/

/** 该文案是否是设计器写入的多语言引用（`{{$t.key}}`）。 */
export function isLanguageToken(text) {
  return typeof text === 'string' && LANGUAGE_TOKEN.test(text)
}

function languageValue(language, locale, key) {
  const value = language?.[locale]?.[key]
  return typeof value === 'string' && value ? value : undefined
}

/**
 * 解析一段表单文案：
 * - `{{$t.key}}`：从表单自带的 options.language 取当前语言；缺失时退回中文（或任一语言）。
 * - 字面量：原样返回（存量表单由设计器「接入多语言」迁移，运行期不做代码侧映射）。
 */
export function resolveDynamicFormText(text, locale, language) {
  if (typeof text !== 'string' || !text) return text

  const token = LANGUAGE_TOKEN.exec(text)
  if (!token) return text

  const key = token[1]
  const current = formCreateLocaleName(locale)
  const hit =
    languageValue(language, current, key) ??
    languageValue(language, 'zh-cn', key) ??
    Object.keys(language || {})
      .map((lang) => languageValue(language, lang, key))
      .find(Boolean)
  // 一个译文都没有时显示 key，比 form-create 解析成空白更容易排查
  return hit ?? key
}

function localizeRule(rule, locale, language) {
  if (!rule || typeof rule !== 'object') return rule
  const next = { ...rule }
  if (typeof next.title === 'string') {
    next.title = resolveDynamicFormText(next.title, locale, language)
  }
  if (typeof next.info === 'string') {
    next.info = resolveDynamicFormText(next.info, locale, language)
  }
  if (next.props && typeof next.props.placeholder === 'string') {
    next.props = {
      ...next.props,
      placeholder: resolveDynamicFormText(next.props.placeholder, locale, language),
    }
  }
  if (Array.isArray(next.options)) {
    next.options = next.options.map((option) =>
      option && typeof option.label === 'string'
        ? { ...option, label: resolveDynamicFormText(option.label, locale, language) }
        : option,
    )
  }
  if (Array.isArray(next.children)) {
    next.children = localizeDynamicFormRules(next.children, locale, language)
  }
  return next
}

/**
 * 生成当前语言下的规则副本，不改动原始 rule。
 * @param {Array} rules - form_json.rule
 * @param {string} locale - vue-i18n 当前语言（zh-CN / en）
 * @param {Object} [language] - form_json.options.language（设计器「多语言」面板的产出）
 */
export function localizeDynamicFormRules(rules, locale, language) {
  if (!Array.isArray(rules)) return rules
  return rules.map((rule) => localizeRule(rule, locale, language))
}
