<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头两排：返回链接与分类标签同排，标题与说明同排；右侧常驻点位数。
         说明文案是长句，与标题同行放不下时整段落到标题下方。 -->
    <header class="iot-head iot-enter">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <button type="button" class="iot-back" @click="goBack">
            <el-icon><ArrowLeft /></el-icon>
            {{ t('deviceAddress.backToList') }}
          </button>
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.deviceAddress') }}
          </p>
        </div>
        <div class="iot-title-row">
          <h1 class="iot-title">{{ t('deviceAddress.title') }} — {{ deviceObjectName }}</h1>
          <span class="iot-head-sub">{{ t('deviceAddress.subtitle') }}</span>
        </div>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('deviceAddress.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 同一台设备的两个视图：点位配置 / 实时监控，一步切换且保留筛选 -->
    <DeviceWorkspaceTabs
      :device-object-id="String(deviceObjectId)"
      :device-object-name="deviceObjectName"
      :protocol-name="protocolName"
    />

    <!-- 工作区：筛选、批量动作、表格、分页共用一块表面。
         点位管理主体已抽到 DevicePointsPanel，与设备对象页的主从右栏共用同一份实现；
         本页只负责页头、页签，以及把筛选/分页写回 URL。 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <DevicePointsPanel
        :key="`${deviceObjectId}|${protocolName}`"
        :device-id="String(deviceObjectId)"
        :device-name="deviceObjectName"
        :protocol-name="protocolName"
        :initial-name="initialName"
        :initial-page="initialPage"
        :initial-size="initialSize"
        @total-change="total = $event"
        @loading-change="panelLoading = $event"
        @query-change="syncQuery"
      />
    </section>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { IconArrowLeft as ArrowLeft } from '@tabler/icons-vue'
import DeviceWorkspaceTabs from '@/components/DeviceWorkspaceTabs.vue'
import DevicePointsPanel from './DevicePointsPanel.vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()
const deviceObjectId = route.params.deviceObjectId
const deviceObjectName = route.query.deviceObjectName || ''

// 协议名由设备对象页带过来：地址写法示例与「自动发现点位」能力都按它取。
const protocolName = computed(() => route.query.protocolName || '')

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

// ---------- 初始筛选 / 分页（来自 URL） ----------
const initialName = String(route.query.name || '')
const initialPage = Number(route.query.page)
const initialSize = Number(route.query.size)

// ---------- 页头点位数 ----------
const total = ref(0)
// 首屏还没拿到数时显示占位符而不是 0：「0 个点位」是一个会被信以为真的假事实
const panelLoading = ref(true)
const displayTotal = computed(() =>
  panelLoading.value && total.value === 0 ? '—' : total.value,
)

// 把已生效的筛选与分页写回 URL。页签切换（DeviceWorkspaceTabs）会带上当前 query，
// 于是「在实时页筛出某个点位 → 切去改配置 → 切回来」仍是同一屏，刷新/分享链接也一致。
// 默认值不写进 query，避免地址里堆一串 ?page=1&size=10。
function syncQuery({ name, page, size }) {
  const query = { ...route.query }
  if (name) query.name = name
  else delete query.name
  if (page > 1) query.page = String(page)
  else delete query.page
  if (size !== 10) query.size = String(size)
  else delete query.size
  router.replace({ query })
}
</script>
