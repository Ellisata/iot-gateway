<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="dash">
    <!-- 页头：用字号与留白立层级，不再靠彩色磁贴喊话 -->
    <header class="page-head enter">
      <div>
        <p class="eyebrow">
          <span class="eyebrow-dot" aria-hidden="true" />
          {{ t('dashboard.eyebrow') }}
        </p>
        <h1 class="page-title">{{ t('dashboard.title') }}</h1>
      </div>
      <div class="refresh">
        <span class="refresh-dot" :class="{ 'is-live': collection?.running }" aria-hidden="true" />
        {{ t('dashboard.autoRefresh') }}
      </div>
    </header>

    <!-- 区1 概览条：整块可点，点击进设备列表 -->
    <section class="enter" style="animation-delay: 60ms">
      <div
        class="overview"
        v-loading="loading"
        :element-loading-text="t('dashboard.deviceStatusLoading')"
        role="link"
        tabindex="0"
        @click="goDeviceList"
        @keydown.enter="goDeviceList"
      >
        <!-- 在线率环形：弧长由在线率驱动，颜色只表达健康等级 -->
        <el-tooltip :content="t('dashboard.onlineRateTooltip')" placement="top">
          <div class="ring-wrap">
            <svg viewBox="0 0 120 120" width="108" height="108" aria-hidden="true">
              <circle class="ring-track" cx="60" cy="60" r="52" fill="none" stroke-width="8" />
              <circle
                class="ring-value"
                :class="`is-${rateLevel}`"
                cx="60"
                cy="60"
                r="52"
                fill="none"
                stroke-width="8"
                :stroke-dasharray="RING_C"
                :stroke-dashoffset="ringOffset"
                :stroke-linecap="ratePct > 0 ? 'round' : 'butt'"
                transform="rotate(-90 60 60)"
              />
            </svg>
            <div class="ring-center">
              <span class="ring-num">{{ rateText }}</span>
              <span class="ring-unit">{{ t('dashboard.onlineRate') }}</span>
            </div>
          </div>
        </el-tooltip>

        <div class="overview-main">
          <div class="overview-kicker">{{ t('dashboard.deviceOnline') }}</div>
          <div class="overview-sub">
            <span class="online-count">{{ numbers.online }}</span>
            <span class="online-total">/ {{ numbers.collected }}</span>
            <span class="online-unit">{{ t('dashboard.onlineUnit') }}</span>
          </div>
          <div class="overview-legend">
            <span class="legend">
              <i class="dot" :class="offlineDotClass" aria-hidden="true" />
              {{ t('dashboard.offline') }} <b>{{ numbers.offline }}</b>
            </span>
            <span class="legend">
              <i class="dot dot--idle" aria-hidden="true" />
              {{ t('dashboard.unCollected') }} <b>{{ numbers.unCollected }}</b>
            </span>
          </div>
        </div>

        <div class="overview-side">
          <div class="side-kicker">{{ t('dashboard.lastSuccess') }}</div>
          <div class="side-value" :class="stalenessClass(lastSuccessSeconds)">
            {{ lastSuccessText }}
          </div>
          <div class="side-action">
            {{ t('dashboard.viewDevices') }}
            <svg
              class="side-arrow"
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M5 12h14M13 6l6 6-6 6" />
            </svg>
          </div>
        </div>
      </div>
    </section>

    <!-- 区2 指标条：一条连续的表面，用细分隔线切开，而不是四个彩色盒子 -->
    <section class="metrics enter" style="animation-delay: 120ms">
      <div
        v-for="tile in kpiTiles"
        :key="tile.key"
        class="metric"
        :class="[`metric--${tile.tone}`, { 'metric--clickable': tile.to }]"
        :role="tile.to ? 'link' : undefined"
        :tabindex="tile.to ? 0 : undefined"
        @click="tile.to && router.push(tile.to)"
        @keydown.enter="tile.to && router.push(tile.to)"
      >
        <div class="metric-label">
          <i class="dot" :class="`dot--${tile.tone}`" aria-hidden="true" />
          {{ tile.label }}
        </div>
        <div class="metric-value" :class="{ 'metric-value--empty': tile.value === '—' }">
          <!-- :key 绑值使节点重挂载以重放闪烁；只有报警磁贴带 tile-flash -->
          <span :key="tile.value" :class="{ 'tile-flash': tile.flash }">{{ tile.value }}</span>
        </div>
        <div class="metric-hint">{{ tile.hint }}</div>
      </div>
    </section>

    <!-- 区3 采集遥测 + 最近事件：趋势回答「是否在变坏」，事件回答「刚刚发生了什么」 -->
    <section class="telemetry enter" style="animation-delay: 180ms">
      <div
        class="panel trend-panel"
        v-loading="trendLoading && !trend"
        :element-loading-text="t('dashboard.trendLoading')"
      >
        <div class="panel-head">
          <div class="head-main">
            <h2 class="panel-title">{{ t('dashboard.trendTitle') }}</h2>
            <span v-if="trendSummary" class="panel-meta">{{ trendSummary }}</span>
          </div>
          <div class="range-switch" role="group" :aria-label="t('dashboard.trendRange')">
            <button
              v-for="option in TREND_RANGES"
              :key="option.hours"
              type="button"
              class="range-btn"
              :class="{ 'is-active': trendHours === option.hours }"
              :aria-pressed="trendHours === option.hours"
              @click="setTrendHours(option.hours)"
            >
              {{ t(option.labelKey) }}
            </button>
          </div>
        </div>

        <TrendChart
          v-if="trendSamples.length > 1"
          :samples="trendSamples"
          :interval-sec="trend?.intervalSec || 60"
        />
        <div v-else class="trend-empty">
          <span class="trend-empty-dot" aria-hidden="true" />
          <p class="trend-empty-title">{{ t('dashboard.trendEmpty') }}</p>
          <p class="trend-empty-hint">
            {{ t('dashboard.trendAccumulating', { minutes: trendMinutes }) }}
          </p>
        </div>

        <p v-if="trendSamples.length > 1" class="trend-foot">
          {{ t('dashboard.trendRunningSince', { minutes: trendMinutes }) }}
          <template v-if="trendStopped">
            · <span class="is-warn">{{ t('dashboard.trendStopped') }}</span>
          </template>
        </p>
      </div>

      <div class="panel events-panel">
        <div class="panel-head">
          <div class="head-main">
            <h2 class="panel-title">{{ t('dashboard.recentEvents') }}</h2>
            <span v-if="activeAlarms > 0" class="event-badge">{{ activeAlarms }}</span>
            <span v-else class="panel-meta">{{ t('dashboard.eventNoActive') }}</span>
          </div>
          <!-- 报警按目标类型分页，没有「全部」页；这里不做会漏类型的“查看全部”，
               行本身可点，跳到该事件对应的报警页。 -->
        </div>

        <ul v-if="recentEvents.length" class="event-list">
          <li
            v-for="event in recentEvents"
            :key="event.id"
            class="event-row"
            role="link"
            tabindex="0"
            @click="openEvent(event)"
            @keydown.enter="openEvent(event)"
          >
            <span class="event-time">{{ eventClock(event.firstOccurTime) }}</span>
            <i class="event-dot" :class="eventTone(event)" aria-hidden="true" />
            <span class="event-copy">
              <span class="event-target">{{ event.targetName }}</span>
              <span class="event-desc">
                {{ eventTypeLabel(event) }} · {{ eventStatusLabel(event) }}
              </span>
            </span>
            <span class="event-ago">{{ relativeText(event.firstOccurTime) }}</span>
          </li>
        </ul>
        <div v-else class="events-empty">
          <p class="trend-empty-title">
            {{ eventsLoading ? t('dashboard.eventsLoading') : t('dashboard.eventsEmpty') }}
          </p>
          <p class="trend-empty-hint">{{ t('dashboard.eventsEmptyHint') }}</p>
        </div>
      </div>
    </section>

    <!-- 区4 设备构成 + 采集健康 + 数据出口 -->
    <section class="panels panels--three enter" style="animation-delay: 240ms">
      <div class="panel" v-loading="loading" :element-loading-text="t('dashboard.deviceStatusLoading')">
        <div class="panel-head">
          <h2 class="panel-title">{{ t('dashboard.deviceDetail') }}</h2>
          <span class="panel-meta">
            {{ t('dashboard.totalDevices', { total: numbers.total }) }}
          </span>
        </div>

        <div v-for="group in compositionGroups" :key="group.key" class="comp-group">
          <div class="comp-group-head">
            <span>{{ group.label }}</span>
            <span class="comp-share">{{ group.share }}</span>
          </div>
          <div class="stack">
            <span
              v-for="seg in group.segments"
              v-show="seg.width > 0"
              :key="seg.key"
              class="stack-seg"
              :class="`stack-seg--${seg.tone}`"
              :style="{ width: `${seg.width}%` }"
            />
          </div>
          <div class="comp-legend">
            <span v-for="seg in group.segments" :key="seg.key">
              <i class="dot" :class="`dot--${seg.tone}`" aria-hidden="true" />
              {{ seg.label }} <b>{{ seg.value }}</b>
            </span>
          </div>
        </div>

        <p class="comp-note">{{ t('dashboard.compositionNote') }}</p>
      </div>

      <div class="panel" v-loading="colLoading" :element-loading-text="t('dashboard.collectionLoading')">
        <div class="panel-head">
          <h2 class="panel-title">{{ t('dashboard.collectionStatus') }}</h2>
          <span class="run-pill" :class="collection?.running ? 'is-live' : 'is-down'">
            <span class="run-dot" aria-hidden="true" />
            {{ collection?.running ? t('dashboard.running') : t('dashboard.stopped') }}
          </span>
        </div>

        <div class="health-list">
          <div v-for="row in healthRows" :key="row.key" class="health-row">
            <span class="health-label">{{ row.label }}</span>
            <span class="health-value" :class="row.tone ? `is-${row.tone}` : ''">{{ row.value }}</span>
          </div>
        </div>
      </div>

      <!-- 数据出口：采集到的值最终有没有发出去。队列与断网缓存是网关最该被盯着看的两个数 -->
      <div class="panel outlet-panel" v-loading="pushLoading && !pushChannels.length">
        <div class="panel-head">
          <h2 class="panel-title">{{ t('dashboard.dataOutlet') }}</h2>
          <span v-if="pushChannels.length" class="panel-meta">
            {{ t('dashboard.outletChannels', { n: pushChannels.length }) }}
          </span>
        </div>

        <template v-if="pushChannels.length">
          <div class="outlet-summary">
            <span class="outlet-stat">
              <i
                class="dot"
                :class="outletConnectedCount === pushChannels.length ? 'dot--ok' : 'dot--alarm'"
                aria-hidden="true"
              />
              {{ t('dashboard.outletConnectedCount', {
                connected: outletConnectedCount,
                total: pushChannels.length,
              }) }}
            </span>
            <span v-if="outletQueueDepth" class="outlet-stat">
              {{ t('dashboard.outletQueue', { n: outletQueueDepth }) }}
            </span>
            <span v-if="outletSpoolDepth" class="outlet-stat is-warn">
              {{ t('dashboard.outletSpool', { n: outletSpoolDepth }) }}
            </span>
          </div>

          <ul class="outlet-list">
            <li v-for="channel in visibleChannels" :key="channel.id" class="outlet-row">
              <i
                class="dot"
                :class="channel.running && channel.connected ? 'dot--ok' : channel.running ? 'dot--alarm' : 'dot--idle'"
                aria-hidden="true"
              />
              <div class="outlet-copy">
                <span class="outlet-name">{{ channel.name }}</span>
                <span class="outlet-sub">
                  {{ channel.type }}
                  <template v-if="channel.queueDepth || channel.spoolDepth">
                    · {{ t('dashboard.outletQueue', { n: channel.queueDepth }) }}
                    · {{ t('dashboard.outletSpool', { n: channel.spoolDepth }) }}
                  </template>
                  <template v-else-if="channel.lastSuccessTime">
                    · {{ t('dashboard.outletLastSuccess', {
                      time: relativeText(channel.lastSuccessTime),
                    }) }}
                  </template>
                </span>
              </div>
              <span class="outlet-status" :class="outletStatusTone(channel)">
                {{ outletStatusText(channel) }}
              </span>
            </li>
          </ul>
          <p v-if="pushChannels.length > VISIBLE_CHANNELS" class="outlet-more">
            {{ t('dashboard.outletMore', { n: pushChannels.length - VISIBLE_CHANNELS }) }}
          </p>
        </template>

        <div v-else class="outlet-empty">
          <p class="trend-empty-title">{{ t('dashboard.outletEmpty') }}</p>
          <p class="trend-empty-hint">{{ t('dashboard.outletEmptyHint') }}</p>
          <router-link class="head-link" to="/data-push/channel">
            {{ t('dashboard.outletConfigure') }} →
          </router-link>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getDeviceOverview } from '@/api/modules/deviceObject'
import { getCollectionStatus } from '@/api/modules/collection'
import { getActiveAlarmCount, getAlarmPage } from '@/api/modules/alarm'
import { getPushStatus } from '@/api/modules/pushChannel'
import { getDashboardTrend } from '@/api/modules/dashboard'
import { secondsSince, formatRelativeTime, parseBackendTime } from '@/utils/datetime'
import TrendChart from './TrendChart.vue'

const { t } = useI18n()
const router = useRouter()

const loading = ref(false)
const overview = ref(null)

const colLoading = ref(false)
const collection = ref(null)

// null = 未知（首屏未回 / 请求失败）。绝不与 0 混用：
// 把「取数失败」画成「没有报警」是最危险的一种撒谎
const activeAlarms = ref(null)

// ---------- 遥测 / 事件 / 数据出口 ----------
const trend = ref(null)
const trendHours = ref(6)
const trendLoading = ref(false)
const recentEvents = ref([])
const eventsLoading = ref(false)
const pushChannels = ref([])
const pushLoading = ref(false)

const TREND_RANGES = [
  { hours: 6, labelKey: 'dashboard.trendRange6h' },
  { hours: 24, labelKey: 'dashboard.trendRange24h' },
]
// 出口面板只铺前几个通道，更多时给一行入口；否则多通道会把三栏撑爆
const VISIBLE_CHANNELS = 3

let pollTimer = null
let clockTimer = null
let trendTimer = null

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

// 颜色表达健康等级，具体色值交给样式里的页面级 token，避免模板里散落十六进制
const rateLevel = computed(() => {
  const pct = ratePct.value
  if (pct === null) return 'unknown'
  if (pct >= 90) return 'ok'
  if (pct >= 70) return 'warn'
  return 'bad'
})

const rateText = computed(() => (ratePct.value === null ? '—' : `${ratePct.value}%`))

/** 数字统一走这里：null/undefined 归为「—」，0 仍然是 0 */
function formatCount(n) {
  return typeof n === 'number' && Number.isFinite(n) ? n.toLocaleString() : '—'
}

const numbers = computed(() => {
  const o = overview.value
  return {
    total: formatCount(o?.total),
    online: formatCount(o?.online),
    offline: formatCount(o?.offline),
    collected: formatCount(o?.collected),
    unCollected: formatCount(o?.unCollected),
  }
})

const offlineDotClass = computed(() => {
  const o = overview.value
  if (!o) return 'dot--idle'
  return (o.offline ?? 0) > 0 ? 'dot--offline' : 'dot--ok'
})

// 分母必须与环形一致用 collected：后端的在线率就是 online/collected。
// 用 total 会让「98%」和「234 / 268 台在线」在同一屏上互相矛盾
const ratePercentText = computed(() => (ratePct.value === null ? '—' : `${ratePct.value}%`))

const offlineShareText = computed(() => {
  const o = overview.value
  if (!o || !o.collected) return '—'
  return `${Math.round(((o.offline ?? 0) / o.collected) * 100)}%`
})

// ---------- 四个指标 ----------
const hasOverview = computed(() => overview.value !== null)

const kpiTiles = computed(() => {
  const o = overview.value
  const alarms = activeAlarms.value
  return [
    {
      key: 'all',
      label: t('dashboard.allDevices'),
      tone: hasOverview.value ? 'neutral' : 'unknown',
      value: hasOverview.value ? (o.total ?? 0) : '—',
      hint: hasOverview.value
        ? t('dashboard.fleetTotalHint', { enabled: o.enabled ?? 0, disabled: o.disabled ?? 0 })
        : '—',
      to: '/industrial/device-object',
    },
    {
      key: 'online',
      label: t('dashboard.online'),
      tone: hasOverview.value ? 'online' : 'unknown',
      value: hasOverview.value ? (o.online ?? 0) : '—',
      hint: hasOverview.value
        ? t('dashboard.fleetOnlineHint', { rate: ratePercentText.value })
        : '—',
      to: '/industrial/device-object',
    },
    {
      key: 'offline',
      label: t('dashboard.offline'),
      // 离线数为 0 是好事，不该继续用红色喊叫
      tone: !hasOverview.value ? 'unknown' : (o.offline > 0 ? 'offline' : 'quiet'),
      value: hasOverview.value ? (o.offline ?? 0) : '—',
      hint: hasOverview.value
        ? t('dashboard.fleetOfflineHint', { rate: offlineShareText.value })
        : '—',
      to: '/industrial/device-object',
    },
    {
      key: 'alarm',
      label: t('dashboard.activeAlarms'),
      tone: alarms === null ? 'unknown' : (alarms > 0 ? 'alarm' : 'quiet'),
      value: alarms === null ? '—' : alarms,
      hint:
        alarms === null
          ? '—'
          : alarms > 0
            ? t('dashboard.alarmsHintActive', { count: alarms })
            : t('dashboard.alarmsHintNone'),
      to: '/alarm/device',
      // 全页唯一「变了就代表有新事件」的数字，只有它闪
      flash: alarms !== null,
    },
  ]
})

// ---------- 设备构成（两组各用自己成立的 denominators） ----------
// 启用/禁用按全部设备算；已接入/未接入按启用设备算。
// 旧实现把四行都按 total 画，导致第二组两根条加起来只有 93%，留一段空条，
// 看上去像少了一截数据 —— 其实是分母不对
const compositionGroups = computed(() => {
  const o = overview.value
  const pct = (n, d) => (d > 0 && Number.isFinite(n) ? Math.round((n / d) * 100) : 0)

  const groups = [
    {
      key: 'enabled',
      label: t('dashboard.groupEnabled'),
      denom: o?.total,
      primary: { key: 'enabled', label: t('dashboard.detailEnabled'), raw: o?.enabled, tone: 'on' },
      secondary: { key: 'disabled', label: t('dashboard.detailDisabled'), raw: o?.disabled, tone: 'off' },
    },
    {
      key: 'collected',
      label: t('dashboard.groupCollected'),
      denom: o?.enabled,
      primary: { key: 'collected', label: t('dashboard.collected'), raw: o?.collected, tone: 'on' },
      secondary: { key: 'unCollected', label: t('dashboard.unCollected'), raw: o?.unCollected, tone: 'off' },
    },
  ]

  return groups.map((group) => {
    const denom = Number(group.denom)
    const valid = Number.isFinite(denom) && denom > 0
    const primaryWidth = valid ? pct(Number(group.primary.raw), denom) : 0
    return {
      key: group.key,
      label: group.label,
      share: valid ? `${primaryWidth}%` : '—',
      segments: [
        { ...group.primary, width: primaryWidth, value: formatCount(group.primary.raw) },
        // 余数留给次要项，保证两根条严格拼满，不因各自四舍五入多出 1%
        { ...group.secondary, width: valid ? Math.max(0, 100 - primaryWidth) : 0, value: formatCount(group.secondary.raw) },
      ],
    }
  })
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
      key: 'deviceCount',
      label: t('dashboard.collectDevices'),
      value: formatCount(c?.deviceCount),
      tone: '',
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

// ---------- 采集遥测 ----------
const trendSamples = computed(() => trend.value?.samples ?? [])

const lastTrendSample = computed(
  () => trendSamples.value[trendSamples.value.length - 1] ?? null
)

// 最后一个「有在线数据」的采样点：尾部可能因取数失败而没有 overview，
// 用它算摘要，避免把上一分钟的真实在线率显示成「—」
const lastOverviewSample = computed(() => {
  const list = trendSamples.value
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].hasOverview) return list[i]
  }
  return null
})

// 与首页环形图同一口径：有离线设备时压到 99%，避免四舍五入后
// 「100% 在线」和「离线 1 台」同时出现在一屏上。
const trendRate = computed(() => {
  const sample = lastOverviewSample.value
  if (!sample || !sample.collected) return null
  const pct = Math.round((sample.online / sample.collected) * 100)
  return sample.offline > 0 ? Math.min(99, pct) : pct
})

const trendSummary = computed(() => {
  if (!lastTrendSample.value) return ''
  const sample = lastOverviewSample.value
  return t('dashboard.trendSummary', {
    rate: trendRate.value === null ? '—' : `${trendRate.value}%`,
    online: sample ? sample.online : '—',
    collected: sample ? sample.collected : '—',
    points: formatCount(lastTrendSample.value.points),
  })
})

const trendStopped = computed(
  () => lastTrendSample.value != null && !lastTrendSample.value.collecting
)

// 本次运行已积累多少分钟：起点用后端给的 startedAt（环形窗口最早的点），
// 不能用 samples[0] —— 休眠断开后 6h 窗口里可能只剩十几分钟，那样会把它说小
const trendMinutes = computed(() => {
  const list = trendSamples.value
  if (!list.length) return 0
  const first = parseBackendTime(trend.value?.startedAt)
  const last = parseBackendTime(list[list.length - 1].time)
  if (!first || !last) return list.length
  return Math.max(1, Math.round((last.getTime() - first.getTime()) / 60000) + 1)
})

async function fetchTrend() {
  if (!trend.value) trendLoading.value = true
  try {
    trend.value = await getDashboardTrend(trendHours.value)
  } catch (err) {
    console.error('获取遥测趋势失败', err)
  } finally {
    trendLoading.value = false
  }
}

function setTrendHours(hours) {
  if (trendHours.value === hours) return
  trendHours.value = hours
  fetchTrend()
}

// ---------- 最近事件 ----------
async function fetchRecentEvents() {
  if (!recentEvents.value.length) eventsLoading.value = true
  try {
    const res = await getAlarmPage({ page: 1, size: 6 })
    recentEvents.value = res?.records ?? []
  } catch (err) {
    console.error('获取最近事件失败', err)
  } finally {
    eventsLoading.value = false
  }
}

function eventClock(raw) {
  const date = parseBackendTime(raw)
  if (!date) return '—'
  return `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

const EVENT_TONES = {
  recover: 'is-ok',
  offline: 'is-alarm',
  quality: 'is-warn',
  threshold: 'is-warn',
}

function eventTone(event) {
  return EVENT_TONES[event.alarmType] || ''
}

const EVENT_TYPE_KEYS = {
  offline: 'alarm.alarmTypeOffline',
  quality: 'alarm.alarmTypeQuality',
  threshold: 'alarm.alarmTypeThreshold',
  recover: 'alarm.alarmTypeRecover',
}

// 后端的 alarmTypeName 是中文硬编码（英文界面下也会返回中文），
// 事件流按 alarmType 在前端翻译；未知类型回退到后端给的文案。
function eventTypeLabel(event) {
  const key = EVENT_TYPE_KEYS[event.alarmType]
  return key ? t(key) : event.alarmTypeName
}

function eventStatusLabel(event) {
  return event.status === 'active' ? t('alarm.statusActive') : t('alarm.statusCleared')
}

// 每一页只展示同一类目标，点击后跳到对应页面即可
const ALARM_ROUTES = {
  device: '/alarm/device',
  channel: '/alarm/channel',
  point: '/alarm/point',
  point_threshold: '/alarm/threshold',
}

function openEvent(event) {
  router.push(ALARM_ROUTES[event.targetType] || '/alarm/device')
}

// ---------- 数据出口 ----------
async function fetchPushStatus() {
  if (!pushChannels.value.length) pushLoading.value = true
  try {
    pushChannels.value = (await getPushStatus()) ?? []
  } catch (err) {
    console.error('获取推送通道状态失败', err)
  } finally {
    pushLoading.value = false
  }
}

const outletConnectedCount = computed(
  () => pushChannels.value.filter((ch) => ch.running && ch.connected).length
)
const outletQueueDepth = computed(() =>
  pushChannels.value.reduce((sum, ch) => sum + (ch.queueDepth || 0), 0)
)
const outletSpoolDepth = computed(() =>
  pushChannels.value.reduce((sum, ch) => sum + (ch.spoolDepth || 0), 0)
)
const visibleChannels = computed(() => pushChannels.value.slice(0, VISIBLE_CHANNELS))

function outletStatusText(channel) {
  if (!channel.running) return t('dashboard.outletStopped')
  return channel.connected
    ? t('dashboard.outletConnected')
    : t('dashboard.outletDisconnected')
}

function outletStatusTone(channel) {
  if (!channel.running) return 'is-idle'
  return channel.connected ? 'is-ok' : 'is-bad'
}

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
  fetchRecentEvents()
  fetchPushStatus()
  fetchTrend()
  // 在线情况/采集状态/事件/出口随采集变化，定时刷新
  pollTimer = setInterval(() => {
    fetchOverview()
    fetchCollection()
    fetchActiveAlarms()
    fetchRecentEvents()
    fetchPushStatus()
  }, 30000)
  // 后端每分钟才落一个采样点，趋势单独按分钟拉取，避免 30s 拉一次长数组
  trendTimer = setInterval(fetchTrend, 60000)
  // 只驱动相对时间文案走字，不发请求
  clockTimer = setInterval(() => {
    now.value = Date.now()
  }, 5000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  if (clockTimer) clearInterval(clockTimer)
  if (trendTimer) clearInterval(trendTimer)
})
</script>

<style scoped>
/* 色板与字体来自 styles/tokens.css，页面只管版式 */
.dash {
  max-width: 1400px;
  margin: 0 auto;
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

/* ---------- 页头 ---------- */
.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;
}

.eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: #7c8ca3;
}

.eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.page-title {
  margin: 0;
  font-size: 22px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.refresh {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 4px;
  font-size: 12px;
  color: var(--ink-4);
}

.refresh-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #cbd5e1;
  transition: background-color 0.3s ease, box-shadow 0.3s ease;
}

.refresh-dot.is-live {
  background: var(--success);
  box-shadow: 0 0 0 4px rgba(5, 150, 105, 0.1);
}

/* ---------- 区1 概览条 ---------- */
.overview {
  position: relative;
  display: flex;
  align-items: center;
  gap: 28px;
  padding: 24px 28px;
  overflow: hidden;
  cursor: pointer;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background:
    radial-gradient(120% 180% at 0% 0%, rgba(37, 99, 235, 0.055), transparent 55%),
    var(--surface);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  transition: border-color 0.2s ease, box-shadow 0.2s ease, transform 0.2s ease;
}

/* 顶部一小段品牌线：登录面板同款，给白面一个可识别的起点 */
.overview::before {
  content: '';
  position: absolute;
  top: 0;
  left: 28px;
  width: 56px;
  height: 2px;
  border-radius: 0 0 2px 2px;
  background: linear-gradient(90deg, var(--brand), #93c5fd);
}

.overview:hover {
  border-color: #c7d7ee;
  box-shadow: 0 10px 30px -18px rgba(37, 99, 235, 0.35);
  transform: translateY(-2px);
}

.overview:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.55);
  outline-offset: 2px;
}

.ring-wrap {
  position: relative;
  flex: none;
  width: 108px;
  height: 108px;
}

.ring-track {
  stroke: var(--line-soft);
}

/* 弧长变化时平滑生长：在线率是这页的核心结论，硬跳会让人怀疑是重排错位。
   0.8s + 与登录页入场动画同一条 cubic-bezier，保持全站同一手感 */
.ring-value {
  stroke: var(--success);
  transition: stroke-dashoffset 0.8s cubic-bezier(0.16, 1, 0.3, 1), stroke 0.4s ease;
}

.ring-value.is-warn {
  stroke: var(--warning);
}

.ring-value.is-bad {
  stroke: var(--danger);
}

.ring-value.is-unknown {
  stroke: #d7dee8;
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
  font-size: 24px;
  font-weight: 650;
  line-height: 1;
  letter-spacing: -0.02em;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.ring-unit {
  margin-top: 5px;
  font-size: 11px;
  color: var(--ink-4);
}

.overview-main {
  flex: 1;
  min-width: 0;
}

.overview-kicker {
  margin-bottom: 6px;
  font-size: 12.5px;
  letter-spacing: 0.02em;
  color: var(--ink-3);
}

.overview-sub {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-variant-numeric: tabular-nums;
}

.online-count {
  font-size: 34px;
  font-weight: 650;
  line-height: 1.1;
  letter-spacing: -0.02em;
  color: var(--ink);
}

.online-total {
  font-size: 18px;
  font-weight: 500;
  color: var(--ink-3);
}

.online-unit {
  margin-left: 2px;
  font-size: 13px;
  color: var(--ink-3);
}

.overview-legend {
  display: flex;
  gap: 20px;
  margin-top: 10px;
  font-size: 12.5px;
  color: var(--ink-3);
}

.legend {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}

.legend b {
  font-weight: 600;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

/* 语义色圆点：统一 7px，避免满屏彩色数字 */
.dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #cbd5e1;
}

.dot--neutral {
  background: #94a3b8;
}

.dot--online,
.dot--on,
.dot--ok {
  background: var(--success);
}

.dot--offline {
  background: var(--danger);
}

.dot--off {
  background: #cbd5e1;
}

.dot--alarm {
  background: var(--warning);
}

.dot--quiet,
.dot--idle {
  background: #cbd5e1;
}

.dot--unknown {
  background: #dde3ec;
}

.overview-side {
  display: flex;
  flex: none;
  flex-direction: column;
  align-items: flex-end;
  min-width: 160px;
  padding-left: 28px;
  border-left: 1px solid var(--line-soft);
}

.side-kicker {
  font-size: 12px;
  color: var(--ink-4);
}

.side-value {
  margin-top: 5px;
  font-size: 20px;
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.side-action {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 10px;
  font-size: 12px;
  font-weight: 500;
  color: var(--brand);
  opacity: 0.85;
}

.side-arrow {
  transition: transform 0.2s ease;
}

.overview:hover .side-arrow {
  transform: translateX(2px);
}

.is-warn {
  color: var(--warning) !important;
  font-weight: 600;
}

.is-danger {
  color: var(--danger) !important;
  font-weight: 600;
}

/* ---------- 区2 指标条 ---------- */
.metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin-top: 16px;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.metric {
  padding: 18px 22px;
  transition: background-color 0.2s ease;
}

.metric + .metric {
  border-left: 1px solid var(--line-soft);
}

.metric--clickable {
  cursor: pointer;
}

.metric--clickable:hover {
  background: #f8fafc;
}

.metric--clickable:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.55);
  outline-offset: -2px;
}

.metric-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  color: var(--ink-3);
}

.metric-value {
  margin-top: 10px;
  font-size: 30px;
  font-weight: 650;
  line-height: 1.1;
  letter-spacing: -0.02em;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

/* 只有「需要人处理」的数字才染语义色：离线、活动报警 */
.metric--offline .metric-value {
  color: var(--danger);
}

.metric--alarm .metric-value {
  color: var(--warning);
}

/* 取数失败时用占位灰，避免「—」以语义色出现、被误读成一个真实数值 */
.metric-value--empty {
  color: #cbd5e1 !important;
}

.metric-hint {
  margin-top: 6px;
  font-size: 12px;
  color: var(--ink-3);
  font-variant-numeric: tabular-nums;
}

/* 报警数变化时闪一次：全页只有它「变了就代表有新事件」。
   负 margin 抵消 padding，闪烁不会推动布局 */
.tile-flash {
  display: inline-block;
  padding: 0 6px;
  margin: 0 -6px;
  border-radius: 6px;
  animation: tileFlash 0.9s ease-out;
}

@keyframes tileFlash {
  from {
    background-color: rgba(217, 119, 6, 0.16);
  }
  to {
    background-color: transparent;
  }
}

/* ---------- 区3 遥测 + 事件 ---------- */
.telemetry {
  display: grid;
  grid-template-columns: minmax(0, 1.85fr) minmax(300px, 1fr);
  gap: 16px;
  margin-top: 16px;
}

.head-main {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}

.head-link {
  font-size: 12px;
  font-weight: 500;
  color: var(--brand);
  text-decoration: none;
  white-space: nowrap;
}

.head-link:hover {
  text-decoration: underline;
}

/* 时间范围切换：两段式分段控件，比两个独立按钮更像一个整体 */
.range-switch {
  display: inline-flex;
  flex: none;
  padding: 2px;
  background: #f1f5f9;
  border-radius: 8px;
}

.range-btn {
  min-width: 52px;
  padding: 4px 9px;
  font-size: 11.5px;
  font-weight: 500;
  line-height: 1.4;
  color: var(--ink-3);
  background: transparent;
  border: 0;
  border-radius: 6px;
  cursor: pointer;
  transition: background-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
}

.range-btn:hover {
  color: var(--ink-2);
}

.range-btn.is-active {
  color: var(--brand);
  background: var(--surface);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.08);
}

.range-btn:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.5);
  outline-offset: 1px;
}

.trend-panel,
.events-panel {
  min-height: 340px;
}

/* 空态不画假曲线：网关刚启动时，这里应该明确说「还在积累」 */
.trend-empty,
.events-empty,
.outlet-empty {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  min-height: 160px;
  text-align: center;
}

.trend-empty-dot {
  width: 8px;
  height: 8px;
  margin-bottom: 8px;
  background: var(--brand);
  border-radius: 50%;
  box-shadow: 0 0 0 5px rgba(37, 99, 235, 0.1);
}

.trend-empty-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink-2);
}

.trend-empty-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.trend-foot {
  margin: 10px 0 0;
  padding-top: 10px;
  font-size: 11.5px;
  color: var(--ink-4);
  border-top: 1px solid var(--line-soft);
  font-variant-numeric: tabular-nums;
}

.event-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--danger);
  background: var(--danger-soft);
  border-radius: 999px;
  font-variant-numeric: tabular-nums;
}

.event-list {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  margin: 0;
  padding: 0;
  overflow: auto;
  list-style: none;
}

.event-row {
  display: grid;
  grid-template-columns: 42px 6px minmax(0, 1fr) auto;
  gap: 10px;
  align-items: center;
  padding: 10px 4px;
  border-top: 1px solid var(--line-soft);
  cursor: pointer;
  border-radius: 8px;
  transition: background-color 0.15s ease;
}

.event-row:first-child {
  border-top: none;
}

.event-row:hover,
.event-row:focus-visible {
  background: #f8fafc;
  outline: none;
}

.event-time {
  font-size: 11.5px;
  color: var(--ink-3);
  font-variant-numeric: tabular-nums;
}

.event-dot {
  width: 6px;
  height: 6px;
  background: var(--ink-4);
  border-radius: 50%;
}

.event-dot.is-alarm {
  background: var(--danger);
}

.event-dot.is-warn {
  background: var(--warning);
}

.event-dot.is-ok {
  background: var(--success);
}

.event-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.event-target {
  overflow: hidden;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.event-desc {
  overflow: hidden;
  font-size: 11.5px;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.event-ago {
  font-size: 11.5px;
  color: var(--ink-4);
  white-space: nowrap;
}

/* ---------- 区4 三列面板 ---------- */
.panels {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  margin-top: 16px;
}

.panel {
  display: flex;
  flex-direction: column;
  padding: 22px 24px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: var(--radius);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.panel-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 20px;
}

.panel-title {
  margin: 0;
  font-size: 15px;
  font-weight: 650;
  letter-spacing: -0.005em;
  color: var(--ink);
}

.panel-meta {
  font-size: 12px;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

/* 设备构成：两条堆叠条，每组分母各自成立 */
.comp-group + .comp-group {
  margin-top: 22px;
}

.comp-group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 9px;
  font-size: 12.5px;
  color: var(--ink-3);
}

.comp-share {
  font-weight: 600;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

.stack {
  display: flex;
  height: 10px;
  overflow: hidden;
  background: var(--line-soft);
  border-radius: 999px;
}

.stack-seg {
  height: 100%;
  transition: width 0.8s cubic-bezier(0.16, 1, 0.3, 1);
}

.stack-seg--on {
  background: linear-gradient(90deg, #60a5fa, var(--brand));
}

/* 用左侧一道白缝把两段分开，不额外占宽度 */
.stack-seg--off {
  background: #dbe3ee;
  box-shadow: -2px 0 0 #fff;
}

.comp-legend {
  display: flex;
  gap: 20px;
  margin-top: 10px;
  font-size: 12px;
  color: var(--ink-3);
}

.comp-legend span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.comp-legend b {
  font-weight: 600;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

.comp-note {
  margin: 20px 0 0;
  padding-top: 14px;
  border-top: 1px solid var(--line-soft);
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

/* 采集健康 */
.run-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 4px 10px;
  font-size: 11.5px;
  font-weight: 500;
  color: var(--success);
  background: var(--success-soft);
  border-radius: 999px;
}

.run-pill.is-down {
  color: var(--danger);
  background: var(--danger-soft);
}

/* 运行中的呼吸点：静态圆点无法表达「引擎正在跳动」。
   2 秒的缓慢呼吸够被余光捕捉，低于 1 秒会显得焦躁 */
.run-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.run-pill.is-live .run-dot {
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

/* 行距撑满面板高度，四行与左侧“设备构成”的两组基线对齐 */
.health-list {
  display: flex;
  flex: 1;
  flex-direction: column;
  justify-content: space-between;
}

.health-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 13px 0;
  border-top: 1px solid var(--line-soft);
}

.health-row:first-child {
  padding-top: 0;
  border-top: none;
}

.health-row:last-child {
  padding-bottom: 0;
}

.health-label {
  font-size: 12.5px;
  color: var(--ink-3);
}

.health-value {
  font-size: 15px;
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

/* ---------- 数据出口 ---------- */
.outlet-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  margin-bottom: 8px;
  font-size: 12px;
  color: var(--ink-3);
}

.outlet-stat {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-variant-numeric: tabular-nums;
}

.outlet-stat.is-warn {
  color: var(--warning);
  font-weight: 500;
}

.outlet-list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
}

.outlet-row {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 10px 0;
  border-top: 1px solid var(--line-soft);
}

.outlet-row:first-child {
  border-top: none;
}

.outlet-copy {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.outlet-name {
  overflow: hidden;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.outlet-sub {
  overflow: hidden;
  font-size: 11.5px;
  color: var(--ink-4);
  text-overflow: ellipsis;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.outlet-status {
  flex: none;
  font-size: 11.5px;
  font-weight: 500;
  white-space: nowrap;
}

.outlet-status.is-ok {
  color: var(--success);
}

.outlet-status.is-bad {
  color: var(--danger);
}

.outlet-status.is-idle {
  color: var(--ink-4);
}

.outlet-more {
  margin: 8px 0 0;
  padding-top: 8px;
  font-size: 11.5px;
  color: var(--ink-4);
  border-top: 1px solid var(--line-soft);
}

/* ---------- 窄屏 ---------- */
@media (max-width: 1280px) {
  .panels {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  /* 三栏变两栏时，出口占满下一行，避免它被挤成一条窄边 */
  .outlet-panel {
    grid-column: 1 / -1;
  }
}

@media (max-width: 1100px) {
  .telemetry {
    grid-template-columns: 1fr;
  }

  .trend-panel,
  .events-panel {
    min-height: 0;
  }
}

@media (max-width: 900px) {
  .panels {
    grid-template-columns: 1fr;
  }

  .outlet-panel {
    grid-column: auto;
  }

  .metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .metric:nth-child(3) {
    border-left: none;
  }

  .metric:nth-child(n + 3) {
    border-top: 1px solid var(--line-soft);
  }
}

@media (max-width: 768px) {
  .page-head {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }

  .overview {
    flex-direction: column;
    align-items: flex-start;
    gap: 18px;
  }

  .overview-side {
    width: 100%;
    align-items: flex-start;
    padding-top: 16px;
    padding-left: 0;
    border-top: 1px solid var(--line-soft);
    border-left: none;
  }
}
</style>
