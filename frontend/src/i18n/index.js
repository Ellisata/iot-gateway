import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN'
import en from './locales/en'

export const LOCALE_STORAGE_KEY = 'locale'

/** 支持的语言列表 */
export const SUPPORTED_LOCALES = [
  { value: 'zh-CN', label: '中文' },
  { value: 'en', label: 'English' },
]

/**
 * 解析初始语言：localStorage 手动选择 > 浏览器语言 > 默认 zh-CN。
 * 浏览器语言前缀匹配：zh* 视为中文，en* 视为英文。
 */
function resolveInitialLocale() {
  const stored = localStorage.getItem(LOCALE_STORAGE_KEY)
  if (stored && SUPPORTED_LOCALES.some((l) => l.value === stored)) {
    return stored
  }

  const languages = navigator.languages?.length
    ? navigator.languages
    : [navigator.language || '']
  for (const lang of languages) {
    const code = (lang || '').toLowerCase()
    if (code.startsWith('zh')) return 'zh-CN'
    if (code.startsWith('en')) return 'en'
  }

  // 默认中文
  return 'zh-CN'
}

const i18n = createI18n({
  legacy: false,
  locale: resolveInitialLocale(),
  fallbackLocale: 'zh-CN',
  messages: {
    'zh-CN': zhCN,
    en,
  },
})

/** 切换语言并持久化到 localStorage */
export function setLocale(locale) {
  i18n.global.locale.value = locale
  localStorage.setItem(LOCALE_STORAGE_KEY, locale)
  // Element Plus 组件库文案跟随切换
  document.documentElement.setAttribute('lang', locale)
}

/** 供非组件环境（如 axios 拦截器）取文案 */
export function translate(key, params) {
  return i18n.global.t(key, params ?? {})
}

export default i18n
