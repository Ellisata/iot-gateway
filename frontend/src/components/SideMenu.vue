<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-aside
    :width="collapsed ? '64px' : '240px'"
    class="flex flex-col transition-all duration-300"
    style="background-color: #304156"
  >
    <!-- Logo 区域 -->
    <div class="flex items-center justify-center h-[60px] text-white text-lg font-bold">
      <span v-if="!collapsed" class="tracking-wider">IoT Admin</span>
      <span v-else class="text-2xl">IoT</span>
    </div>

    <!-- 导航菜单 -->
    <el-menu
      :default-active="route.path"
      :collapse="collapsed"
      :collapse-transition="false"
      background-color="#304156"
      text-color="#bfcbd9"
      active-text-color="#409eff"
      class="flex-1 border-r-0"
      @select="handleMenuSelect"
    >
      <el-menu-item index="/dashboard">
        <el-icon><Odometer /></el-icon>
        <template #title>{{ t('menu.dashboard') }}</template>
      </el-menu-item>

      <!-- 工业物联网 -->
      <el-sub-menu index="/industrial">
        <template #title>
          <el-icon><Monitor /></el-icon>
          <span>{{ t('menu.industrial') }}</span>
        </template>
        <el-menu-item index="/industrial/device-object">
          <el-icon><Cpu /></el-icon>
          <template #title>{{ t('menu.deviceObject') }}</template>
        </el-menu-item>
        <el-menu-item index="/industrial/protocol">
          <el-icon><List /></el-icon>
          <template #title>{{ t('menu.protocol') }}</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/industrial/protocol-form">
          <el-icon><Document /></el-icon>
          <template #title>{{ t('menu.protocolForm') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 数据推送 -->
      <el-sub-menu index="/data-push">
        <template #title>
          <el-icon><Promotion /></el-icon>
          <span>{{ t('menu.dataPush') }}</span>
        </template>
        <el-menu-item index="/data-push/channel">
          <el-icon><Connection /></el-icon>
          <template #title>{{ t('menu.channel') }}</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/data-push/channel-form">
          <el-icon><Document /></el-icon>
          <template #title>{{ t('menu.channelForm') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 报警管理 -->
      <el-sub-menu index="/alarm">
        <template #title>
          <el-icon><Bell /></el-icon>
          <span>{{ t('menu.alarm') }}</span>
        </template>
        <el-menu-item index="/alarm/device">
          <el-icon><Monitor /></el-icon>
          <template #title>{{ t('menu.alarmDevice') }}</template>
        </el-menu-item>
        <el-menu-item index="/alarm/channel">
          <el-icon><Connection /></el-icon>
          <template #title>{{ t('menu.alarmChannel') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 日志管理 -->
      <el-sub-menu v-if="userStore.isAdmin" index="/log">
        <template #title>
          <el-icon><Memo /></el-icon>
          <span>{{ t('menu.log') }}</span>
        </template>
        <el-menu-item index="/log/file">
          <el-icon><Tickets /></el-icon>
          <template #title>{{ t('menu.logFile') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 密钥管理 -->
      <el-sub-menu v-if="userStore.isAdmin" index="/secret">
        <template #title>
          <el-icon><Key /></el-icon>
          <span>{{ t('menu.secret') }}</span>
        </template>
        <el-menu-item index="/secret/api-key">
          <el-icon><Key /></el-icon>
          <template #title>API Key</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 开放接口文档（独立顶级入口，全员可见，新窗口打开） -->
      <el-menu-item index="/open-api/doc">
        <el-icon><Document /></el-icon>
        <template #title>{{ t('menu.openApiDoc') }}</template>
      </el-menu-item>

    </el-menu>
  </el-aside>
</template>

<script setup>
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/store'

// 新窗口打开的页面（全页阅读型内容，不在管理端布局内展示）
const NEW_WINDOW_ROUTES = ['/open-api/doc']

defineProps({
  collapsed: {
    type: Boolean,
    default: false,
  },
})

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const { t } = useI18n()

/**
 * 菜单点击处理：常规项当前窗口跳转，阅读型文档项新开窗口。
 * 由 el-menu 的 @select 统一接管（替代 router 模式），便于按 index 分流。
 */
function handleMenuSelect(index) {
  if (NEW_WINDOW_ROUTES.includes(index)) {
    const { href } = router.resolve(index)
    window.open(href, '_blank')
    return
  }
  router.push(index)
}
</script>
