// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { SUPPORTED_LOCALES } from '@/i18n'
import { formCreateLocaleName } from '@/i18n/dynamicFormText'
import fcDesignerZhCn from '@form-create/designer/locale/zh-cn.es.js'
import fcDesignerEn from '@form-create/designer/locale/en.es.js'

const designerMessages = {
  'zh-cn': fcDesignerZhCn,
  en: fcDesignerEn,
}

/**
 * 表单设计器的界面语言与「多语言」列配置。
 *
 * FcDesigner 的 locale 既翻译设计器自身的界面，也把 name 传给画布/预览里的
 * form-create —— 预览因此能和运行态一样按 {{$t.key}} 解析 options.language。
 * localeOptions 必须是 `zh-cn` / `en`，与 options.language 的键、form-create
 * 的语言名保持一致。
 */
export function useDesignerLocale() {
  const { locale } = useI18n()

  const fcLocale = computed(() => formCreateLocaleName(locale.value))

  const designerLocale = computed(() => designerMessages[fcLocale.value] || fcDesignerZhCn)

  const designerConfig = computed(() => ({
    fieldReadonly: false,
    showLanguage: true,
    localeOptions: SUPPORTED_LOCALES.map((item) => ({
      value: formCreateLocaleName(item.value),
      label: item.label,
    })),
  }))

  return { designerLocale, designerConfig }
}
