// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import { createRouter, createWebHistory } from 'vue-router'
import { useUserStore } from '@/store'
import { translate } from '@/i18n'

const routes = [
  {
    path: '/login',
    component: () => import('@/layouts/AuthLayout.vue'),
    children: [
      {
        path: '',
        name: 'Login',
        component: () => import('@/views/login/LoginView.vue'),
        meta: { titleKey: 'menu.login' },
      },
    ],
  },
  {
    path: '/',
    component: () => import('@/layouts/MainLayout.vue'),
    redirect: '/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'Dashboard',
        component: () => import('@/views/dashboard/IndexView.vue'),
        meta: { titleKey: 'menu.dashboard', icon: 'Odometer' },
      },
      // ---- 工业物联网路由 ----
      {
        path: 'industrial/device-object/:gatewayId?',
        name: 'GatewayDeviceObject',
        component: () => import('@/views/industrial/device-object/IndexView.vue'),
        meta: { titleKey: 'menu.deviceObject', icon: 'Cpu' },
      },
      {
        path: 'industrial/device-address/:deviceObjectId',
        name: 'GatewayDeviceAddress',
        component: () => import('@/views/industrial/device-address/IndexView.vue'),
        meta: { titleKey: 'menu.deviceAddress', hidden: true },
      },

      {
        path: 'industrial/protocol',
        name: 'IndustrialProtocol',
        component: () => import('@/views/industrial/protocol/IndexView.vue'),
        meta: { titleKey: 'menu.protocol', icon: 'List' },
      },
      {
        path: 'industrial/protocol-form',
        name: 'IndustrialProtocolForm',
        component: () => import('@/views/industrial/protocol-form/IndexView.vue'),
        meta: { titleKey: 'menu.protocolForm', icon: 'Document', roles: ['admin'] },
      },
      // ---- 数据推送路由 ----
      {
        path: 'data-push/channel',
        name: 'DataPushChannel',
        component: () => import('@/views/data-push/channel/IndexView.vue'),
        meta: { titleKey: 'menu.channel', icon: 'Connection' },
      },
      {
        path: 'data-push/channel-form',
        name: 'DataPushChannelForm',
        component: () => import('@/views/data-push/channel-form/IndexView.vue'),
        meta: { titleKey: 'menu.channelForm', icon: 'Document', roles: ['admin'] },
      },
      // ---- 报警管理路由 ----
      {
        path: 'alarm/device',
        name: 'AlarmDevice',
        component: () => import('@/views/alarm/IndexView.vue'),
        props: { targetType: 'device' },
        meta: { titleKey: 'menu.alarmDevice', icon: 'Warning' },
      },
      {
        path: 'alarm/channel',
        name: 'AlarmChannel',
        component: () => import('@/views/alarm/IndexView.vue'),
        props: { targetType: 'channel' },
        meta: { titleKey: 'menu.alarmChannel', icon: 'Warning' },
      },
      // ---- 日志管理路由 ----
      {
        path: 'log/file',
        name: 'LogFile',
        component: () => import('@/views/log/IndexView.vue'),
        meta: { titleKey: 'menu.logFile', icon: 'Tickets', roles: ['admin'] },
      },
      // ---- 密钥管理路由 ----
      {
        path: 'secret/api-key',
        name: 'SecretApiKey',
        component: () => import('@/views/secret/api-key/IndexView.vue'),
        meta: { titleKey: 'menu.secret', icon: 'Key', roles: ['admin'] },
      },
    ],
  },
  // 开放接口文档：独立于主布局的全页阅读页，从侧边栏菜单新窗口打开
  {
    path: '/open-api/doc',
    name: 'OpenApiDoc',
    component: () => import('@/views/open-api/doc/IndexView.vue'),
    meta: { titleKey: 'menu.openApiDoc' },
  },
]

const router = createRouter({
  history: createWebHistory('/admin'),
  routes,
})

// 路由守卫：登录校验 + 页面级权限
router.beforeEach((to, from, next) => {
  document.title = `${to.meta.titleKey ? translate(to.meta.titleKey) : 'IoT Admin'} - IoT Admin`

  const userStore = useUserStore()

  // 未登录跳转登录页
  if (to.name !== 'Login' && !userStore.isLoggedIn) {
    next({ name: 'Login' })
    return
  }

  // 已登录访问登录页，跳回首页
  if (to.name === 'Login' && userStore.isLoggedIn) {
    next('/dashboard')
    return
  }

  // 页面级权限：meta.roles 限制了角色时，非 admin 禁止进入
  if (to.meta.roles && !userStore.isAdmin) {
    next('/dashboard')
    return
  }

  next()
})

export default router
