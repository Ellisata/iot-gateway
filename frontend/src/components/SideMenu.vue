<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-aside
    :width="collapsed ? '64px' : width + 'px'"
    class="side-rail flex flex-col"
    :class="{ 'is-resizing': resizing }"
  >
    <!-- 品牌区：与顶栏同高，保证两侧首行对齐 -->
    <div class="brand" :class="{ 'is-collapsed': collapsed }">
      <span class="brand-mark" aria-hidden="true">
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M12 3 3 8l9 5 9-5-9-5Z" />
          <path d="M3 12l9 5 9-5" />
          <path d="M3 16l9 5 9-5" />
        </svg>
      </span>
      <span v-if="!collapsed" class="brand-name">IoT Gateway</span>
    </div>

    <!-- 导航菜单 -->
    <el-menu
      :default-active="route.path"
      :collapse="collapsed"
      :collapse-transition="false"
      popper-class="side-menu-popper"
      :popper-offset="8"
      class="side-menu flex-1"
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

      <!-- 系统设置：网关身份与运行开关同为管理员项，归入一个一级菜单 -->
      <el-sub-menu v-if="userStore.isAdmin" index="/system">
        <template #title>
          <el-icon><MenuIcon name="settings" /></el-icon>
          <span>{{ t('menu.systemSettings') }}</span>
        </template>
        <el-menu-item index="/settings">
          <el-icon><MenuIcon name="gateway" /></el-icon>
          <template #title>{{ t('menu.gatewaySettings') }}</template>
        </el-menu-item>
        <!-- default.yaml 作默认值，可在页面覆盖 -->
        <el-menu-item index="/runtime-config">
          <el-icon><MenuIcon name="runtimeSwitch" /></el-icon>
          <template #title>{{ t('menu.runtimeConfig') }}</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 开放接口文档（独立顶级入口，全员可见，新窗口打开） -->
      <el-menu-item index="/open-api/doc">
        <el-icon><MenuIcon name="apiDoc" /></el-icon>
        <template #title>
          {{ t('menu.openApiDoc') }}
          <!-- 新窗口打开，给一个轻量的外链提示，避免点完才发现跳走 -->
          <svg
            v-if="!collapsed"
            class="ext-mark"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.8"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M7 17 17 7M8 7h9v9" />
          </svg>
        </template>
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
  // 展开态宽度由布局层维护（可拖动），折叠态固定 64px
  width: {
    type: Number,
    default: 240,
  },
  // 拖动中关闭宽度过渡，避免指针与边线之间出现拖尾
  resizing: {
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
/*
  侧栏材质：深石板底色（slate-900 家族），而不是旧的 #304156 中灰蓝。
  深色只负责“压住”导航，层次依旧交给字号、留白和一根发丝线；
  强调色收敛到一种蓝，靠亮度而不是饱和度在暗底上发声。
*/
.side-rail {
  --rail-line: rgba(255, 255, 255, 0.08);

  flex: none;
  transition: width 0.28s cubic-bezier(0.4, 0, 0.2, 1);
  --rail-line-soft: rgba(255, 255, 255, 0.06);
  --rail-ink: #cbd5e1;
  --rail-ink-strong: #f8fafc;
  --rail-muted: #8fa2ba;
  --rail-sub: #94a3b8;
  --rail-sub-icon: #7c8fa9;
  --rail-brand: #93c5fd;
  --rail-brand-rail: #60a5fa;
  --rail-hover: rgba(255, 255, 255, 0.05);

  background:
    radial-gradient(120% 42% at 0% 0%, rgba(59, 130, 246, 0.1), transparent 60%),
    linear-gradient(180deg, #111c30 0%, #0d1626 100%);
  border-right: 1px solid rgba(255, 255, 255, 0.06);
  -webkit-font-smoothing: antialiased;
}

/* 拖动过程中宽度跟手变化，不能再走 280ms 的过渡 */
.side-rail.is-resizing {
  transition: none;
}

/* ---------- 品牌区 ---------- */
.brand {
  display: flex;
  flex: none;
  align-items: center;
  gap: 10px;
  height: 60px;
  padding: 0 16px;
  border-bottom: 1px solid var(--rail-line);
}

.brand.is-collapsed {
  justify-content: center;
  padding: 0;
}

.brand-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 30px;
  height: 30px;
  color: #fff;
  background: linear-gradient(135deg, #60a5fa 0%, #2563eb 55%, #1d4ed8 100%);
  border-radius: 9px;
  box-shadow:
    0 7px 16px -8px rgba(37, 99, 235, 0.7),
    inset 0 1px 0 rgba(255, 255, 255, 0.3);
}

.brand-mark svg {
  width: 16px;
  height: 16px;
}

.brand-name {
  font-size: 14px;
  font-weight: 650;
  letter-spacing: 0.02em;
  color: #f8fafc;
  white-space: nowrap;
}

/* ---------- 菜单容器 ---------- */
.side-menu {
  /* 覆盖 Element 的默认深色/大行高变量，让展开与折叠共用一套节奏 */
  --el-menu-bg-color: transparent;
  --el-menu-text-color: var(--rail-ink);
  --el-menu-hover-bg-color: var(--rail-hover);
  --el-menu-hover-text-color: var(--rail-ink-strong);
  --el-menu-active-color: var(--rail-brand);
  --el-menu-item-height: 40px;
  --el-menu-sub-item-height: 36px;
  --el-menu-item-font-size: 13.5px;
  --el-menu-base-level-padding: 12px;
  --el-menu-level-padding: 0px;
  --el-menu-icon-width: 18px;

  flex: 1;
  min-height: 0;
  padding: 12px 10px 18px;
  overflow-x: hidden;
  overflow-y: auto;
  background: transparent;
  border-right: 0;
  scrollbar-width: thin;
  scrollbar-color: rgba(255, 255, 255, 0.14) transparent;
}

.side-menu::-webkit-scrollbar {
  width: 4px;
}

.side-menu::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.14);
  border-radius: 999px;
}

.side-menu::-webkit-scrollbar-track {
  background: transparent;
}

/* ---------- 一级菜单 ---------- */
.side-menu :deep(.el-menu-item),
.side-menu :deep(.el-sub-menu__title) {
  height: 40px;
  margin: 2px 0;
  padding: 0 12px !important;
  font-size: 13.5px;
  font-weight: 500;
  line-height: 40px;
  letter-spacing: 0.01em;
  color: var(--rail-ink);
  border-radius: 10px;
  transition: background-color 0.18s ease, color 0.18s ease;
}

.side-menu :deep(.el-menu-item .el-icon),
.side-menu :deep(.el-sub-menu__title .el-icon) {
  width: 18px;
  margin-right: 10px;
  font-size: 18px;
  color: var(--rail-muted);
  opacity: 1;
  transition: color 0.18s ease;
}

.side-menu :deep(.el-menu-item:not(.is-disabled):hover),
.side-menu :deep(.el-sub-menu__title:hover) {
  color: var(--rail-ink-strong);
  background-color: var(--rail-hover);
}

.side-menu :deep(.el-menu-item:not(.is-disabled):hover .el-icon),
.side-menu :deep(.el-sub-menu__title:hover .el-icon) {
  color: #cbd5e1;
}

/* 当前项：左侧短轨 + 极浅蓝底，只加一个信号，不刷整块高饱和色 */
.side-menu :deep(.el-menu-item.is-active) {
  font-weight: 600;
  color: var(--rail-brand);
  background: linear-gradient(90deg, rgba(59, 130, 246, 0.2), rgba(59, 130, 246, 0.055));
}

.side-menu :deep(.el-menu-item.is-active .el-icon) {
  color: var(--rail-brand);
}

.side-menu :deep(.el-menu-item.is-active)::before {
  content: '';
  position: absolute;
  top: 50%;
  left: -10px;
  width: 3px;
  height: 18px;
  background: var(--rail-brand-rail);
  border-radius: 0 3px 3px 0;
  transform: translateY(-50%);
}

/* 分组内有当前页时，父级只染字色，不抢子项的选中底色 */
.side-menu :deep(.el-sub-menu.is-active > .el-sub-menu__title) {
  color: var(--rail-brand);
}

.side-menu :deep(.el-sub-menu.is-active > .el-sub-menu__title .el-icon) {
  color: var(--rail-brand);
}

/* 展开箭头放轻：它只是操作提示，不该和图标争视觉重量 */
.side-menu :deep(.el-sub-menu__icon-arrow) {
  right: 12px;
  font-size: 12px;
  color: #64748b;
}

.side-menu :deep(.el-sub-menu__title:hover .el-sub-menu__icon-arrow) {
  color: #94a3b8;
}

/* ---------- 二级菜单 ---------- */
.side-menu :deep(.el-menu--inline) {
  position: relative;
  padding: 0;
  margin: 2px 0 6px;
  background: transparent;
}

/* 一条浅色导引线把子项串在父级图标下方，层级靠结构而不是靠缩进猜 */
.side-menu :deep(.el-menu--inline)::before {
  content: '';
  position: absolute;
  top: 5px;
  bottom: 5px;
  left: 32px;
  width: 1px;
  background: var(--rail-line-soft);
}

.side-menu :deep(.el-menu--inline .el-menu-item) {
  height: 36px;
  margin: 1px 0;
  padding: 0 12px 0 42px !important;
  font-size: 13px;
  line-height: 36px;
  color: var(--rail-sub);
  border-radius: 9px;
  background: transparent;
}

.side-menu :deep(.el-menu--inline .el-menu-item .el-icon) {
  width: 16px;
  margin-right: 9px;
  font-size: 16px;
  color: var(--rail-sub-icon);
}

.side-menu :deep(.el-menu--inline .el-menu-item:hover) {
  color: var(--rail-ink-strong);
  background-color: var(--rail-hover);
}

.side-menu :deep(.el-menu--inline .el-menu-item.is-active) {
  color: #bfdbfe;
  background: linear-gradient(90deg, rgba(59, 130, 246, 0.18), rgba(59, 130, 246, 0.045));
}

.side-menu :deep(.el-menu--inline .el-menu-item.is-active .el-icon) {
  color: var(--rail-brand);
}

/* ---------- 外链提示 ---------- */
.ext-mark {
  width: 13px;
  height: 13px;
  margin-left: auto;
  color: #64748b;
  transition: color 0.18s ease;
}

.side-menu :deep(.el-menu-item:hover) .ext-mark {
  color: #cbd5e1;
}

/* ---------- 折叠态 ---------- */
.side-menu.el-menu--collapse {
  width: 100%;
  padding: 12px 8px 18px;
}

.side-menu.el-menu--collapse :deep(.el-menu-item),
.side-menu.el-menu--collapse :deep(.el-sub-menu__title) {
  justify-content: center;
  padding: 0 !important;
}

.side-menu.el-menu--collapse :deep(.el-menu-tooltip__trigger) {
  justify-content: center;
  padding: 0;
}

.side-menu.el-menu--collapse :deep(.el-menu-item .el-icon),
.side-menu.el-menu--collapse :deep(.el-sub-menu__title .el-icon) {
  width: 18px;
  margin-right: 0;
}

.side-menu.el-menu--collapse :deep(.el-sub-menu__icon-arrow) {
  display: none;
}

.side-menu.el-menu--collapse :deep(.el-menu-item.is-active)::before {
  left: -8px;
}

.side-menu.el-menu--collapse :deep(.el-sub-menu.is-active > .el-sub-menu__title) {
  color: #bfdbfe;
  background: linear-gradient(135deg, rgba(59, 130, 246, 0.22), rgba(59, 130, 246, 0.06));
}

@media (prefers-reduced-motion: reduce) {
  .side-menu :deep(.el-menu-item),
  .side-menu :deep(.el-sub-menu__title),
  .side-menu :deep(.el-icon),
  .ext-mark {
    transition: none;
  }
}
</style>

<!-- 折叠态的子菜单通过 teleport 挂到 body，scoped 管不到，单独给 popper 一套深色样式 -->
<style>
.side-menu-popper {
  --el-menu-bg-color: #152135;
  --el-menu-text-color: #cbd5e1;
  --el-menu-active-color: #bfdbfe;
  --el-menu-hover-bg-color: rgba(255, 255, 255, 0.06);
  --el-menu-base-level-padding: 10px;
  --el-menu-item-height: 36px;
  --el-menu-icon-width: 16px;
}

/* popper-class 也会被折叠态的文字 tooltip 继承，用 :has 只给真正含菜单的浮层加卡片边 */
.side-menu-popper.el-popper:has(.el-menu--popup) {
  overflow: hidden;
  background: #152135;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 12px;
  box-shadow:
    0 20px 44px -20px rgba(2, 6, 23, 0.7),
    0 4px 14px rgba(2, 6, 23, 0.35);
}

/* 折叠态的图标 tooltip：比侧栏亮一档，浮在浅色内容上也清晰 */
.side-menu-popper.el-popper.is-dark {
  padding: 6px 10px;
  font-size: 12px;
  color: #f8fafc;
  background: #1e293b;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  box-shadow: 0 10px 24px -12px rgba(2, 6, 23, 0.7);
}

.side-menu-popper .el-menu--popup {
  min-width: 190px;
  padding: 8px;
  background: #152135;
  border-radius: 12px;
  box-shadow: none;
}

.side-menu-popper .el-menu-item {
  height: 36px;
  margin: 1px 0;
  padding: 0 10px;
  font-size: 13px;
  line-height: 36px;
  color: #cbd5e1;
  border-radius: 8px;
}

.side-menu-popper .el-menu-item .el-icon {
  width: 16px;
  margin-right: 9px;
  font-size: 16px;
  color: #7c8fa9;
}

.side-menu-popper .el-menu-item:hover {
  color: #f8fafc;
  background: rgba(255, 255, 255, 0.06);
}

.side-menu-popper .el-menu-item.is-active {
  font-weight: 600;
  color: #bfdbfe;
  background: rgba(59, 130, 246, 0.18);
}

.side-menu-popper .el-menu-item.is-active .el-icon {
  color: #93c5fd;
}
</style>
