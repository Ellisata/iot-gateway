<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <!-- 「点位配置 / 实时监控」是同一台设备的两个视图，不是两个模块：
       所以页签放在设备页头部，而不是菜单里。切换用 replace —— 浏览器后退
       应该回到设备列表，而不是在页签之间来回横跳。 -->
  <nav class="workspace-tabs iot-enter" role="tablist" :aria-label="t('deviceWorkspace.ariaLabel')">
    <button
      v-for="tab in tabs"
      :key="tab.name"
      type="button"
      role="tab"
      class="workspace-tab"
      :class="{ 'is-active': tab.name === activeName }"
      :aria-selected="tab.name === activeName"
      @click="go(tab.name)"
    >
      <el-icon class="workspace-tab-icon" aria-hidden="true">
        <component :is="tab.icon" />
      </el-icon>
      <span>{{ t(tab.labelKey) }}</span>
    </button>
  </nav>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  IconActivityHeartbeat as ActivityHeartbeat,
  IconMapPin as MapPin,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  deviceObjectId: { type: String, required: true },
  deviceObjectName: { type: String, default: '' },
  protocolName: { type: String, default: '' },
})

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const tabs = [
  { name: 'GatewayDeviceAddress', labelKey: 'deviceWorkspace.tabs.address', icon: MapPin },
  { name: 'GatewayRealtime', labelKey: 'deviceWorkspace.tabs.realtime', icon: ActivityHeartbeat },
]

const activeName = computed(() => route.name)

function go(name) {
  if (name === activeName.value) return
  // 保留当前页的筛选与分页：在实时页筛出某个点位、切去改配置，回来还是那一屏。
  // name/page/size 三个键在两个页面上语义一致（都是点位名、页码、每页条数）。
  router.replace({
    name,
    params: { deviceObjectId: props.deviceObjectId },
    query: {
      ...route.query,
      ...(props.deviceObjectName ? { deviceObjectName: props.deviceObjectName } : {}),
      ...(props.protocolName ? { protocolName: props.protocolName } : {}),
    },
  })
}
</script>

<style scoped>
.workspace-tabs {
  display: flex;
  gap: 4px;
  margin: -4px 0 16px;
  border-bottom: 1px solid var(--line-soft);
}

.workspace-tab {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 10px 14px 12px;
  font-size: 13px;
  font-weight: 500;
  color: var(--ink-3);
  background: none;
  border: 0;
  cursor: pointer;
  transition: color 0.15s ease;
}

.workspace-tab:hover {
  color: var(--ink);
}

.workspace-tab:focus-visible {
  outline: 2px solid var(--el-color-primary-light-5);
  outline-offset: -2px;
  border-radius: 6px;
}

.workspace-tab.is-active {
  font-weight: 600;
  color: var(--brand);
}

/* 选中态用一条压在发丝线上的短下划线，不额外占高度 */
.workspace-tab.is-active::after {
  content: '';
  position: absolute;
  right: 12px;
  bottom: -1px;
  left: 12px;
  height: 2px;
  background: var(--brand);
  border-radius: 2px 2px 0 0;
}

.workspace-tab-icon {
  font-size: 15px;
}
</style>
