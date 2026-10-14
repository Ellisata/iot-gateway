<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="dash p-6">
    <!-- 区1 概览条：整块可点，点击进设备列表 -->
    <section class="enter">
      <div
        class="overview"
        v-loading="loading"
        :element-loading-text="t('dashboard.deviceStatusLoading')"
        @click="goDeviceList"
      >
        <!-- 在线率环形：本页的视觉锚点，弧长由在线率驱动 -->
        <el-tooltip :content="t('dashboard.onlineRateTooltip')" placement="top">
          <div class="ring-wrap">
            <svg viewBox="0 0 120 120" width="120" height="120" aria-hidden="true">
              <circle class="ring-track" cx="60" cy="60" r="52" fill="none" stroke-width="10" />
              <circle
                class="ring-value"
                cx="60"
                cy="60"
                r="52"
                fill="none"
                stroke-width="10"
                :stroke="rateColor"
                :stroke-dasharray="RING_C"
                :stroke-dashoffset="ringOffset"
                :stroke-linecap="ratePct > 0 ? 'round' : 'butt'"
                transform="rotate(-90 60 60)"
              />
            </svg>
            <div class="ring-center">
              <span class="ring-num" :style="{ color: rateColor }">{{ rateText }}</span>
              <span class="ring-unit">{{ t('dashboard.onlineRate') }}</span>
            </div>
          </div>
        </el-tooltip>

        <div class="overview-main">
          <div class="overview-title">{{ t('dashboard.deviceOnline') }}</div>
          <div class="overview-sub">{{ onlineOfTotalText }}</div>
        </div>

        <div class="overview-status">
          <el-tag
            class="status-tag"
            :type="collection?.running ? 'success' : 'danger'"
            size="small"
            effect="light"
            round
          >
            <span class="tag-dot" :class="{ 'tag-dot--live': collection?.running }" />
            {{ collection?.running ? t('dashboard.running') : t('dashboard.stopped') }}
          </el-tag>
          <div class="overview-last">
            {{ t('dashboard.lastSuccess') }}
            <span :class="stalenessClass(lastSuccessSeconds)">{{ lastSuccessText }}</span>
          </div>
        </div>
      </div>
    </section>

    <!-- 区2 四个指标磁贴 -->
    <section class="enter grid grid-cols-2 md:grid-cols-4 gap-4 mt-4" style="animation-delay: 80ms">
      <div
        v-for="tile in kpiTiles"
        :key="tile.key"
        class="kpi"
        :class="[`kpi--${tile.tone}`, { 'kpi--clickable': tile.to }]"
        @click="tile.to && router.push(tile.to)"
      >
        <div class="kpi-icon">
          <el-icon><component :is="tile.icon" /></el-icon>
        </div>
        <div class="kpi-body">
          <div class="kpi-label">{{ tile.label }}</div>
          <div class="kpi-value" :class="{ 'kpi-value--empty': tile.value === '—' }">
            <!-- :key 绑值使节点重挂载以重放闪烁；只有报警磁贴带 tile-flash -->
            <span :key="tile.value" :class="{ 'tile-flash': tile.flash }">{{ tile.value }}</span>
          </div>
        </div>
      </div>
    </section>

    <!-- 区3 设备构成 + 采集健康 -->
    <section class="enter grid grid-cols-1 lg:grid-cols-2 gap-4 mt-4" style="animation-delay: 160ms">
      <el-card
        class="panel-card"
        shadow="never"
        v-loading="loading"
        :element-loading-text="t('dashboard.deviceStatusLoading')"
      >
        <template #header>
          <span class="font-bold text-gray-700">{{ t('dashboard.deviceDetail') }}</span>
        </template>
        <div
          v-for="row in composition"
          :key="row.key"
          class="comp-row"
        >
          <div class="comp-label">{{ row.label }}</div>
          <div class="comp-bar">
            <div class="comp-fill" :class="`comp-fill--${row.tone}`" :style="{ width: `${row.width}%` }" />
          </div>
          <div class="comp-value">{{ row.value }}</div>
        </div>
      </el-card>

      <el-card
        class="panel-card"
        shadow="never"
        v-loading="colLoading"
        :element-loading-text="t('dashboard.collectionLoading')"
      >
        <template #header>
          <div class="flex items-center justify-between">
            <span class="font-bold text-gray-700">{{ t('dashboard.collectionStatus') }}</span>
          </div>
        </template>
        <div v-for="row in healthRows" :key="row.key" class="health-row">
          <div class="health-label">{{ row.label }}</div>
          <div class="health-value" :class="row.tone ? `is-${row.tone}` : ''">{{ row.value }}</div>
        </div>
      </el-card>
    </section>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Monitor, CircleCheckFilled, CircleCloseFilled, BellFilled } from '@element-plus/icons-vue'
import { getDeviceOverview } from '@/api/modules/deviceObject'
import { getCollectionStatus } from '@/api/modules/collection'
import { getActiveAlarmCount } from '@/api/modules/alarm'
import { secondsSince, formatRelativeTime } from '@/utils/datetime'

const { t } = useI18n()
const router = useRouter()

const loading = ref(false)
const overview = ref(null)

const colLoading = ref(false)
const collection = ref(null)

// null = 未知（首屏未回 / 请求失败）。绝不与 0 混用：
// 把「取数失败」画成「没有报警」是最危险的一种撒谎
const activeAlarms = ref(null)

let pollTimer = null
let clockTimer = null

// ---------- 在线率环形 ----------
// 几何常量在脚本里算一次，模板不做算术
const RING_R = 52
const RING_C = 2 * Math.PI * RING_R

// null = 数据不可用，0 = 确实 0% 在线。两者必须走不同分支：
// 旧实现用 o.onlineRate || 0 把「请求失败」和「0% 在线」渲染成了同一个样子
const ratePct = computed(() => {
  const o = overview.value
  if (!o || !o.collected) return null
  const rate = Number(o.onlineRate)
  if (!Number.isFinite(rate)) return null
  const pct = Math.round(Math.min(1, Math.max(0, rate)) * 100)
  // 后端口径是 online/collected，且有离线设备时因四舍五入仍可能返回 1.0
  // （如 249/250 → 99.6 → 100）。此时压到 99%，避免与「离线 N 台」自相矛盾。
  // 判据必须是 offline 而非 online<total —— total 含禁用与未接入采集的设备，
  // 用 total 判定会让任何有禁用设备的网关永远显示不出 100%
  if ((o.offline ?? 0) > 0) return Math.min(99, pct)
  return pct
})

const ringOffset = computed(() =>
  ratePct.value === null ? RING_C : RING_C * (1 - ratePct.value / 100)
)

const rateColor = computed(() => {
  const pct = ratePct.value
  if (pct === null) return 'var(--el-text-color-placeholder)'
  if (pct >= 90) return 'var(--el-color-success)'
  if (pct >= 70) return 'var(--el-color-warning)'
  return 'var(--el-color-danger)'
})

const rateText = computed(() => (ratePct.value === null ? '—' : `${ratePct.value}%`))

// 分母必须与环形一致用 collected：后端的在线率就是 online/collected。
// 用 total 会让「99%」和「234 / 268 台在线」在同一屏上互相矛盾
const onlineOfTotalText = computed(() => {
  const o = overview.value
  if (!o) return '—'
  return t('dashboard.onlineOfCollected', { online: o.online ?? 0, collected: o.collected ?? 0 })
})

// ---------- 四个指标磁贴 ----------
const hasOverview = computed(() => overview.value !== null)

const kpiTiles = computed(() => {
  const o = overview.value
  const alarms = activeAlarms.value
  return [
    {
      key: 'all',
      label: t('dashboard.allDevices'),
      icon: Monitor,
      tone: hasOverview.value ? 'neutral' : 'unknown',
      value: hasOverview.value ? (o.total ?? 0) : '—',
      to: '/industrial/device-object',
    },
    {
      key: 'online',
      label: t('dashboard.online'),
      icon: CircleCheckFilled,
      tone: hasOverview.value ? 'online' : 'unknown',
      value: hasOverview.value ? (o.online ?? 0) : '—',
      to: '/industrial/device-object',
    },
    {
      key: 'offline',
      label: t('dashboard.offline'),
      icon: CircleCloseFilled,
      // 离线数为 0 是好事，不该继续用红色喊叫
      tone: !hasOverview.value ? 'unknown' : (o.offline > 0 ? 'offline' : 'quiet'),
      value: hasOverview.value ? (o.offline ?? 0) : '—',
      to: '/industrial/device-object',
    },
    {
      key: 'alarm',
      label: t('dashboard.activeAlarms'),
      icon: BellFilled,
      tone: alarms === null ? 'unknown' : (alarms > 0 ? 'alarm' : 'quiet'),
      value: alarms === null ? '—' : alarms,
      to: '/alarm/device',
      // 全页唯一「变了就代表有新事件」的数字，只有它闪
      flash: alarms !== null,
    },
  ]
})

// ---------- 设备构成（每组按占全部设备的比例画条） ----------
const composition = computed(() => {
  const o = overview.value
  const total = o?.total ?? 0
  const share = (n) => (total > 0 ? Math.round((n / total) * 100) : 0)
  const rows = [
    { key: 'enabled', label: t('dashboard.detailEnabled'), raw: o?.enabled, tone: 'on' },
    { key: 'disabled', label: t('dashboard.detailDisabled'), raw: o?.disabled, tone: 'off' },
    { key: 'collected', label: t('dashboard.collected'), raw: o?.collected, tone: 'on' },
    { key: 'unCollected', label: t('dashboard.unCollected'), raw: o?.unCollected, tone: 'off' },
  ]
  return rows.map((row) => ({
    ...row,
    value: row.raw ?? '—',
    width: row.raw ? share(row.raw) : 0,
  }))
})

// ---------- 采集健康 ----------
// 相对时间要自己走字：computed 里直接读 Date.now() 不是响应式依赖，
// 只会在 30 秒轮询时重算，文案会在同一个数字上冻结半分钟，看起来像坏了
const now = ref(Date.now())

const lastPollSeconds = computed(() => secondsSince(collection.value?.lastPollTime, now.value))
const lastSuccessSeconds = computed(() => secondsSince(collection.value?.lastSuccessTime, now.value))

/** 解析不了就原样显示，不吞掉后端给的数据 */
function relativeText(raw) {
  if (!raw) return '—'
  return formatRelativeTime(raw, t, now.value) ?? raw
}

const lastSuccessText = computed(() => relativeText(collection.value?.lastSuccessTime))

/**
 * 采集新鲜度：超过 1 分钟开始示警，超过 5 分钟视为已经停摆。
 * 这是真正有用的信号 —— 光看绝对时间戳，用户得自己做减法才知道采集卡住了。
 */
function stalenessClass(seconds) {
  if (seconds === null || seconds === undefined) return ''
  if (seconds > 300) return 'is-danger'
  if (seconds > 60) return 'is-warn'
  return ''
}

function formatCount(n) {
  return typeof n === 'number' && Number.isFinite(n) ? n.toLocaleString() : '—'
}

const healthRows = computed(() => {
  const c = collection.value
  return [
    {
      key: 'lastPoll',
      label: t('dashboard.lastPoll'),
      value: relativeText(c?.lastPollTime),
      tone: stalenessClass(lastPollSeconds.value).replace('is-', ''),
    },
    {
      key: 'lastSuccess',
      label: t('dashboard.lastSuccess'),
      value: lastSuccessText.value,
      tone: stalenessClass(lastSuccessSeconds.value).replace('is-', ''),
    },
    {
      key: 'addressCount',
      label: t('dashboard.addressCount'),
      value: formatCount(c?.addressCount),
      tone: '',
    },
    {
      key: 'totalErrors',
      label: t('dashboard.totalErrors'),
      value: formatCount(c?.errorCount),
      tone: (c?.errorCount ?? 0) > 0 ? 'danger' : '',
    },
  ]
})

// ---------- 获取统计 ----------
async function fetchOverview() {
  // 只在首次加载时开遮罩：每 30 秒轮询都盖一层会糊住环形
  if (!overview.value) loading.value = true
  try {
    overview.value = await getDeviceOverview()
  } catch (err) {
    console.error('获取设备在线统计失败', err)
  } finally {
    loading.value = false
  }
}

async function fetchCollection() {
  if (!collection.value) colLoading.value = true
  try {
    collection.value = await getCollectionStatus()
  } catch (err) {
    console.error('获取采集状态失败', err)
  } finally {
    colLoading.value = false
  }
}

async function fetchActiveAlarms() {
  try {
    activeAlarms.value = await getActiveAlarmCount()
  } catch (err) {
    // 静默：保留上一轮数值，不回落成 0
    console.error('获取活动报警数失败', err)
  }
}

// ---------- 跳转 ----------
function goDeviceList() {
  router.push('/industrial/device-object')
}

onMounted(() => {
  fetchOverview()
  fetchCollection()
  fetchActiveAlarms()
  // 在线情况/采集状态随采集变化，定时刷新
  pollTimer = setInterval(() => {
    fetchOverview()
    fetchCollection()
    fetchActiveAlarms()
  }, 30000)
  // 只驱动相对时间文案走字，不发请求
  clockTimer = setInterval(() => {
    now.value = Date.now()
  }, 5000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  if (clockTimer) clearInterval(clockTimer)
})
</script>

<style scoped>
.dash {
  /* 语义色集中在页面作用域，避免十六进制字面量散落；
     用 Element Plus 主题变量是为了和旁边的 el-tag 同色系，不出现两种「绿」 */
  --panel-bg: #fff;
  --panel-border: var(--el-border-color-lighter);
  --panel-radius: 12px;
}

/* 三块内容自上而下错峰落位，把视线先引到概览条。
   必须挂在区级包裹层而不是磁贴上：animation-fill-mode: both 结束后
   to 关键帧的 transform: none 仍然生效，而动画声明在层叠中优先级高于普通声明，
   挂磁贴上会把 hover 的 translateY 永久压掉 */
.enter {
  animation: dashEnter 0.6s cubic-bezier(0.16, 1, 0.3, 1) both;
}

@keyframes dashEnter {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

/* ---------- 区1 概览条 ---------- */
.overview {
  display: flex;
  align-items: center;
  gap: 28px;
  padding: 22px 28px;
  cursor: pointer;
  border: 1px solid var(--panel-border);
  border-radius: var(--panel-radius);
  background: linear-gradient(135deg, #f2f8ff 0%, #ffffff 55%);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

.overview:hover {
  border-color: var(--el-color-primary-light-5);
  box-shadow: 0 6px 18px rgba(64, 158, 255, 0.12);
  transform: translateY(-2px);
}

.ring-wrap {
  position: relative;
  flex: none;
  width: 120px;
  height: 120px;
}

.ring-track {
  stroke: var(--el-fill-color);
}

/* 弧长变化时平滑生长：在线率是这页的核心结论，硬跳会让人怀疑是重排错位。
   0.8s + 与登录页入场动画同一条 cubic-bezier，保持全站同一手感 */
.ring-value {
  transition: stroke-dashoffset 0.8s cubic-bezier(0.16, 1, 0.3, 1), stroke 0.4s ease;
}

.ring-center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

.ring-num {
  font-size: 30px;
  font-weight: 700;
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.ring-unit {
  margin-top: 4px;
  font-size: 11px;
  color: var(--el-text-color-secondary);
}

.overview-main {
  flex: 1;
  min-width: 0;
}

.overview-title {
  margin-bottom: 6px;
  font-size: 14px;
  color: var(--el-text-color-secondary);
}

.overview-sub {
  font-size: 22px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  font-variant-numeric: tabular-nums;
}

.overview-status {
  display: flex;
  flex: none;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
}

.status-tag {
  display: inline-flex;
  align-items: center;
}

/* 运行中的呼吸点：静态圆点无法表达「引擎正在跳动」。
   2 秒的缓慢呼吸够被余光捕捉，低于 1 秒会显得焦躁 */
.tag-dot {
  display: inline-block;
  width: 7px;
  height: 7px;
  margin-right: 5px;
  border-radius: 50%;
  background: currentColor;
}

.tag-dot--live {
  animation: tagPulse 2s ease-in-out infinite;
}

@keyframes tagPulse {
  0%,
  100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.4;
    transform: scale(0.8);
  }
}

.overview-last {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.is-warn {
  color: var(--el-color-warning);
  font-weight: 600;
}

.is-danger {
  color: var(--el-color-danger);
  font-weight: 600;
}

/* ---------- 区2 指标磁贴 ---------- */
.kpi {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px 20px;
  border: 1px solid var(--panel-border);
  border-radius: var(--panel-radius);
  background: var(--panel-bg);
  transition: transform 0.2s ease, border-color 0.2s ease, box-shadow 0.2s ease;

  --tone-fg: var(--el-text-color-regular);
  --tone-bg: var(--el-fill-color-light);
}

.kpi--clickable {
  cursor: pointer;
}

.kpi--clickable:hover {
  transform: translateY(-2px);
  border-color: var(--el-color-primary-light-5);
  box-shadow: 0 6px 16px rgba(64, 158, 255, 0.12);
}

/* 色值走 Element Plus 主题变量而非 Tailwind 类：
   bg-${tone}-50 这类拼接不出现在源码文本里，Tailwind v4 的扫描器扫不到，
   CSS 不会被生成、类名静默失效；且 EP 的绿/红/橙不在 Tailwind 默认色板里，
   混用会让同一页出现两种「绿」 */
.kpi--neutral {
  --tone-fg: var(--el-text-color-regular);
  --tone-bg: var(--el-fill-color-light);
}

.kpi--online {
  --tone-fg: var(--el-color-success);
  --tone-bg: var(--el-color-success-light-9);
}

.kpi--offline {
  --tone-fg: var(--el-color-danger);
  --tone-bg: var(--el-color-danger-light-9);
}

.kpi--alarm {
  --tone-fg: var(--el-color-warning);
  --tone-bg: var(--el-color-warning-light-9);
}

.kpi--quiet {
  --tone-fg: var(--el-text-color-secondary);
  --tone-bg: var(--el-fill-color-light);
}

.kpi--unknown {
  --tone-fg: var(--el-text-color-placeholder);
  --tone-bg: var(--el-fill-color-light);
}

.kpi-icon {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  font-size: 22px;
  color: var(--tone-fg);
  background: var(--tone-bg);
  border-radius: 10px;
}

.kpi-body {
  min-width: 0;
}

.kpi-label {
  margin-bottom: 2px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.kpi-value {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.15;
  color: var(--tone-fg);
  font-variant-numeric: tabular-nums;
}

/* 取数失败时用占位灰，避免「—」以语义色出现、被误读成一个真实数值 */
.kpi-value--empty {
  color: var(--el-text-color-placeholder);
}

/* 报警数变化时闪一次：全页只有它「变了就代表有新事件」。
   负 margin 抵消 padding，闪烁不会推动布局 */
.tile-flash {
  display: inline-block;
  padding: 0 6px;
  margin: 0 -6px;
  border-radius: 4px;
  animation: tileFlash 0.9s ease-out;
}

@keyframes tileFlash {
  from {
    background-color: var(--el-color-warning-light-7);
  }
  to {
    background-color: transparent;
  }
}

/* ---------- 区3 两列卡片 ---------- */
.panel-card {
  --el-card-padding: 20px;
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
}

.comp-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 9px 0;
}

.comp-row + .comp-row {
  border-top: 1px solid var(--el-border-color-extra-light);
}

.comp-label {
  flex: none;
  width: 120px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.comp-bar {
  flex: 1;
  height: 8px;
  overflow: hidden;
  background: var(--el-fill-color);
  border-radius: 4px;
}

.comp-fill {
  height: 100%;
  border-radius: 4px;
  transition: width 0.8s cubic-bezier(0.16, 1, 0.3, 1);
}

.comp-fill--on {
  background: var(--el-color-primary);
}

.comp-fill--off {
  background: var(--el-color-info-light-5);
}

.comp-value {
  flex: none;
  width: 56px;
  font-size: 15px;
  font-weight: 600;
  text-align: right;
  color: var(--el-text-color-primary);
  font-variant-numeric: tabular-nums;
}

.health-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 0;
}

.health-row + .health-row {
  border-top: 1px solid var(--el-border-color-extra-light);
}

.health-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.health-value {
  font-size: 15px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  font-variant-numeric: tabular-nums;
}

/* 窄屏：概览条竖排，环居中 */
@media (max-width: 768px) {
  .overview {
    flex-direction: column;
    align-items: flex-start;
    gap: 16px;
  }

  .overview-status {
    align-items: flex-start;
  }
}
</style>
