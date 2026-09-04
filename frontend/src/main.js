// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import zhCn from 'element-plus/dist/locale/zh-cn.mjs'

import App from './App.vue'
import router from './router'
import i18n from './i18n'
import './styles/tailwind.css'
import './styles/index.scss'

import FcDesigner from '@form-create/designer'

const app = createApp(App)

// 注册所有 Element Plus 图标
for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

// 将 axios 挂载到 window 上（VForm3 依赖）
import axios from 'axios'
window.axios = axios

app.use(createPinia())
app.use(router)
app.use(i18n)
app.use(ElementPlus)
app.use(FcDesigner)
app.use(FcDesigner.formCreate) // 注册表单渲染器

app.mount('#app')
