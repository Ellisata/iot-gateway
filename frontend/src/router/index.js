import { createRouter, createWebHistory } from 'vue-router'
import { useUserStore } from '@/store'

const routes = [
  {
    path: '/login',
    component: () => import('@/layouts/AuthLayout.vue'),
    children: [
      {
        path: '',
        name: 'Login',
        component: () => import('@/views/login/LoginView.vue'),
        meta: { title: '登录' },
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
        meta: { title: '首页', icon: 'Odometer' },
      },
      // ---- 工业物联网路由 ----
      {
        path: 'industrial/device-object/:gatewayId?',
        name: 'GatewayDeviceObject',
        component: () => import('@/views/industrial/device-object/IndexView.vue'),
        meta: { title: '设备对象管理', icon: 'Cpu' },
      },
      {
        path: 'industrial/device-address/:deviceObjectId',
        name: 'GatewayDeviceAddress',
        component: () => import('@/views/industrial/device-address/IndexView.vue'),
        meta: { title: '设备地址标签', hidden: true },
      },

      {
        path: 'industrial/protocol',
        name: 'IndustrialProtocol',
        component: () => import('@/views/industrial/protocol/IndexView.vue'),
        meta: { title: '协议管理', icon: 'List' },
      },
      {
        path: 'industrial/protocol-form',
        name: 'IndustrialProtocolForm',
        component: () => import('@/views/industrial/protocol-form/IndexView.vue'),
        meta: { title: '协议表单配置', icon: 'Document', roles: ['admin'] },
      },
      // ---- 数据推送路由 ----
      {
        path: 'data-push/channel',
        name: 'DataPushChannel',
        component: () => import('@/views/data-push/channel/IndexView.vue'),
        meta: { title: '通道管理', icon: 'Connection' },
      },
      {
        path: 'data-push/channel-form',
        name: 'DataPushChannelForm',
        component: () => import('@/views/data-push/channel-form/IndexView.vue'),
        meta: { title: '通道表单管理', icon: 'Document', roles: ['admin'] },
      },
      // ---- 报警管理路由 ----
      {
        path: 'alarm/device',
        name: 'AlarmDevice',
        component: () => import('@/views/alarm/IndexView.vue'),
        props: { targetType: 'device' },
        meta: { title: '设备报警', icon: 'Warning' },
      },
      {
        path: 'alarm/channel',
        name: 'AlarmChannel',
        component: () => import('@/views/alarm/IndexView.vue'),
        props: { targetType: 'channel' },
        meta: { title: '通道报警', icon: 'Warning' },
      },
      // ---- 日志管理路由 ----
      {
        path: 'log/file',
        name: 'LogFile',
        component: () => import('@/views/log/IndexView.vue'),
        meta: { title: '日志文件查看', icon: 'Tickets', roles: ['admin'] },
      },
      // ---- 密钥管理路由 ----
      {
        path: 'secret/api-key',
        name: 'SecretApiKey',
        component: () => import('@/views/secret/api-key/IndexView.vue'),
        meta: { title: 'API Key', icon: 'Key', roles: ['admin'] },
      },
    ],
  },
  // 开放接口文档：独立于主布局的全页阅读页，从侧边栏菜单新窗口打开
  {
    path: '/open-api/doc',
    name: 'OpenApiDoc',
    component: () => import('@/views/open-api/doc/IndexView.vue'),
    meta: { title: '开放接口文档' },
  },
]

const router = createRouter({
  history: createWebHistory('/admin'),
  routes,
})

// 路由守卫：登录校验 + 页面级权限
router.beforeEach((to, from, next) => {
  document.title = `${to.meta.title || 'IoT Admin'} - IoT Admin`

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
