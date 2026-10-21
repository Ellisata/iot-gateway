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
      <span v-if="!collapsed" class="tracking-wider">IoT Gateway</span>
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
      class="side-menu flex-1 border-r-0"
      @select="handleMenuSelect"
    >
      <el-menu-item index="/dashboard">
        <el-icon><MenuIcon name="dashboard" /></el-icon>
        <template #title>{{ t('menu.dashboard') }}</template>
      </el-menu-item>

      <!-- 工业物联网 -->
      <el-sub-menu index="/industrial">
        <template #title>
          <el-icon><MenuIcon name="industrial" /></el-icon>
          <span>{{ t('menu.industrial') }}</span>
        </template>
        <el-menu-item index="/industrial/device-object">
          <el-icon><MenuIcon name="deviceObject" /></el-icon>
          <template #title>{{ t('menu.deviceObject') }}</template>
        </el-menu-item>
        <el-menu-item index="/industrial/device-scan">
          <el-icon><MenuIcon name="deviceScan" /></el-icon>
          <template #title>{{ t('menu.deviceScan') }}</template>
        </el-menu-item>
        <el-menu-item index="/industrial/protocol">
          <el-icon><MenuIcon name="protocol" /></el-icon>
          <template #title>{{ t('menu.protocol') }}</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/industrial/protocol-form">
          <el-icon><MenuIcon name="protocolForm" /></el-icon>
          <template #title>{{ t('menu.protocolForm') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 数据推送 -->
      <el-sub-menu index="/data-push">
        <template #title>
          <el-icon><MenuIcon name="dataPush" /></el-icon>
          <span>{{ t('menu.dataPush') }}</span>
        </template>
        <el-menu-item index="/data-push/channel">
          <el-icon><MenuIcon name="channel" /></el-icon>
          <template #title>{{ t('menu.channel') }}</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/data-push/channel-form">
          <el-icon><MenuIcon name="channelForm" /></el-icon>
          <template #title>{{ t('menu.channelForm') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 订阅管理 -->
      <el-sub-menu index="/subscription">
        <template #title>
          <el-icon><MenuIcon name="subscription" /></el-icon>
          <span>{{ t('menu.subscription') }}</span>
        </template>
        <el-menu-item index="/subscription/mqtt">
          <el-icon><MenuIcon name="mqttSubscription" /></el-icon>
          <template #title>{{ t('menu.mqttSubscription') }}</template>
        </el-menu-item>
        <el-menu-item index="/subscription/device-point">
          <el-icon><MenuIcon name="subscriptionPoint" /></el-icon>
          <template #title>{{ t('menu.subscriptionDevicePoint') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 报警管理 -->
      <el-sub-menu index="/alarm">
        <template #title>
          <el-icon><MenuIcon name="alarm" /></el-icon>
          <span>{{ t('menu.alarm') }}</span>
        </template>
        <el-menu-item index="/alarm/device">
          <el-icon><MenuIcon name="alarmDevice" /></el-icon>
          <template #title>{{ t('menu.alarmDevice') }}</template>
        </el-menu-item>
        <el-menu-item index="/alarm/channel">
          <el-icon><MenuIcon name="alarmChannel" /></el-icon>
          <template #title>{{ t('menu.alarmChannel') }}</template>
        </el-menu-item>
        <el-menu-item index="/alarm/point">
          <el-icon><MenuIcon name="alarmPoint" /></el-icon>
          <template #title>{{ t('menu.alarmPoint') }}</template>
        </el-menu-item>
        <el-menu-item index="/alarm/threshold">
          <el-icon><MenuIcon name="alarmThreshold" /></el-icon>
          <template #title>{{ t('menu.alarmThreshold') }}</template>
        </el-menu-item>
        <el-menu-item index="/alarm/threshold-record">
          <el-icon><MenuIcon name="alarmRecord" /></el-icon>
          <template #title>{{ t('menu.alarmThresholdRecord') }}</template>
        </el-menu-item>
        <el-menu-item index="/alarm/reliability">
          <el-icon><MenuIcon name="alarmReliability" /></el-icon>
          <template #title>{{ t('menu.alarmReliability') }}</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/alarm/webhook">
          <el-icon><MenuIcon name="notification" /></el-icon>
          <template #title>{{ t('menu.alarmWebhook') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 日志管理 -->
      <el-sub-menu v-if="userStore.isAdmin" index="/log">
        <template #title>
          <el-icon><MenuIcon name="logs" /></el-icon>
          <span>{{ t('menu.log') }}</span>
        </template>
        <el-menu-item index="/log/file">
          <el-icon><MenuIcon name="logFile" /></el-icon>
          <template #title>{{ t('menu.logFile') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 密钥管理 -->
      <el-sub-menu v-if="userStore.isAdmin" index="/secret">
        <template #title>
          <el-icon><MenuIcon name="secret" /></el-icon>
          <span>{{ t('menu.secret') }}</span>
        </template>
        <el-menu-item index="/secret/api-key">
          <el-icon><MenuIcon name="apiKey" /></el-icon>
          <template #title>API Key</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 系统设置 -->
      <el-menu-item v-if="userStore.isAdmin" index="/settings">
        <el-icon><MenuIcon name="settings" /></el-icon>
        <template #title>{{ t('menu.systemSettings') }}</template>
      </el-menu-item>

      <!-- 开放接口文档（独立顶级入口，全员可见，新窗口打开） -->
      <el-menu-item index="/open-api/doc">
        <el-icon><MenuIcon name="apiDoc" /></el-icon>
        <template #title>{{ t('menu.openApiDoc') }}</template>
      </el-menu-item>

    </el-menu>
  </el-aside>
</template>

<script setup>
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/store'
import MenuIcon from '@/components/MenuIcon.vue'

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

<style scoped>
/* 统一图标尺寸与透明度，让不同语义的线条图形保持同一视觉重量 */
.side-menu :deep(.el-menu-item .el-icon),
.side-menu :deep(.el-sub-menu__title .el-icon) {
  font-size: 18px;
  opacity: 0.85;
  transition: opacity 0.2s ease, color 0.2s ease;
}

.side-menu :deep(.el-menu-item.is-active),
.side-menu :deep(.el-sub-menu.is-active > .el-sub-menu__title) {
  color: #409eff;
}

.side-menu :deep(.el-menu-item.is-active .el-icon),
.side-menu :deep(.el-sub-menu.is-active > .el-sub-menu__title .el-icon) {
  opacity: 1;
}

/* 当前项：极轻的蓝色渐变 + 左侧短竖条，避免整块高饱和底色 */
.side-menu :deep(.el-menu-item) {
  position: relative;
}

.side-menu :deep(.el-menu-item.is-active) {
  background: linear-gradient(90deg, rgba(64, 158, 255, 0.16), rgba(64, 158, 255, 0.03));
}

.side-menu :deep(.el-menu-item.is-active)::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  width: 3px;
  height: 20px;
  border-radius: 0 3px 3px 0;
  background: #409eff;
  transform: translateY(-50%);
}
</style>
