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
        path: 'industrial/realtime/:deviceObjectId',
        name: 'GatewayRealtime',
        component: () => import('@/views/industrial/realtime/IndexView.vue'),
        meta: { titleKey: 'menu.realtime', hidden: true },
      },
      {
        path: 'industrial/device-scan',
        name: 'GatewayDeviceScan',
        component: () => import('@/views/industrial/device-scan/IndexView.vue'),
        meta: { titleKey: 'menu.deviceScan', icon: 'Search' },
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
      // ---- 订阅管理路由 ----
      // 外部 MQTT 报文 → 映射为采集记录 → 写入时序数据库（只投时序库通道）
      {
        path: 'subscription/mqtt',
        name: 'MqttSubscription',
        component: () => import('@/views/subscription/mqtt/IndexView.vue'),
        meta: { titleKey: 'menu.mqttSubscription', icon: 'Message' },
      },
      {
        path: 'subscription/device-point',
        name: 'MqttSubscriptionDevicePoint',
        component: () => import('@/views/subscription/devicePoint/IndexView.vue'),
        meta: { titleKey: 'menu.subscriptionDevicePoint', icon: 'DataLine' },
      },
      // ---- 报警管理路由 ----
      // 以下四个报警页（设备 / 通道 / 点位 / 超限）是同一个页面，表格实现只有一份
      // (views/alarm/AlarmTable.vue)，各自的 IndexView.vue 只是写死 targetType 的
      // 薄壳 —— 每页对应的 target_type 现在写在页面文件里，不再经路由 props 注入。
      {
        path: 'alarm/device',
        name: 'AlarmDevice',
        component: () => import('@/views/alarm/device/IndexView.vue'),
        meta: { titleKey: 'menu.alarmDevice', icon: 'Warning' },
      },
      {
        path: 'alarm/channel',
        name: 'AlarmChannel',
        component: () => import('@/views/alarm/channel/IndexView.vue'),
        meta: { titleKey: 'menu.alarmChannel', icon: 'Warning' },
      },
      {
        // 点位质量报警：target_type='point'
        path: 'alarm/point',
        name: 'AlarmPoint',
        component: () => import('@/views/alarm/point/IndexView.vue'),
        meta: { titleKey: 'menu.alarmPoint', icon: 'Warning' },
      },
      {
        // 点位超限报警：后端是独立 target_type（point_threshold），页面与点位报警复用
        path: 'alarm/threshold',
        name: 'AlarmThreshold',
        component: () => import('@/views/alarm/threshold/IndexView.vue'),
        meta: { titleKey: 'menu.alarmThreshold', icon: 'Warning' },
      },
      {
        // 越限记录：与上面的报警不是一回事（原始越限逐条留证，见页面内的说明）
        path: 'alarm/threshold-record',
        name: 'AlarmThresholdRecord',
        component: () => import('@/views/alarm/threshold-record/IndexView.vue'),
        meta: { titleKey: 'menu.alarmThresholdRecord', icon: 'DataLine' },
      },
      {
        // 可靠性统计：与上面各页不同，它按**时间窗**给出聚合排行（设备可用率 /
        // 点位异常次数），不是事件列表。数据只读 alarm 表，故仍归在报警管理下。
        path: 'alarm/reliability',
        name: 'AlarmReliability',
        component: () => import('@/views/alarm/reliability/IndexView.vue'),
        meta: { titleKey: 'menu.alarmReliability', icon: 'Histogram' },
      },
      {
        // 通知配置含群机器人地址与加签密钥（地址本身即凭证），限管理员
        path: 'alarm/webhook',
        name: 'AlarmWebhook',
        component: () => import('@/views/alarm/webhook/IndexView.vue'),
        meta: { titleKey: 'menu.alarmWebhook', icon: 'Bell', roles: ['admin'] },
      },
      // ---- 日志管理路由 ----
      {
        path: 'log/file',
        name: 'LogFile',
        component: () => import('@/views/logs/IndexView.vue'),
        meta: { titleKey: 'menu.logFile', icon: 'Tickets', roles: ['admin'] },
      },
      // ---- 密钥管理路由 ----
      {
        path: 'secret/api-key',
        name: 'SecretApiKey',
        component: () => import('@/views/secret/api-key/IndexView.vue'),
        meta: { titleKey: 'menu.secret', icon: 'Key', roles: ['admin'] },
      },
      // ---- 系统设置路由 ----
      // 网关身份（settings）与运行开关同属「系统设置」一级菜单。
      {
        path: 'settings',
        name: 'SystemSettings',
        component: () => import('@/views/settings/gateway/IndexView.vue'),
        meta: { titleKey: 'menu.gatewaySettings', icon: 'Setting', roles: ['admin'] },
      },
      // default.yaml 作默认值、system_config 表作覆盖；与网关身份（settings）分开，
      // 因为这里的修改可能立即启停局域网套接字 / 改变采集推送行为。
      {
        path: 'runtime-config',
        name: 'RuntimeConfig',
        component: () => import('@/views/settings/runtime-config/IndexView.vue'),
        meta: { titleKey: 'menu.runtimeConfig', icon: 'Setting', roles: ['admin'] },
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
  document.title = `${to.meta.titleKey ? translate(to.meta.titleKey) : 'IoT Gateway'} - IoT Gateway`

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
