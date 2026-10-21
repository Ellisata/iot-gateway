<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="trend-chart">
    <div class="chart-legend">
      <span class="legend-item">
        <i class="legend-dot is-online" aria-hidden="true" />
        {{ t('dashboard.trendOnlineRate') }}
      </span>
      <span class="legend-item">
        <i class="legend-dot is-throughput" aria-hidden="true" />
        {{ t('dashboard.trendThroughput') }}
        <span class="legend-unit">{{ t('dashboard.trendPerMinute') }}</span>
      </span>
      <span v-if="totalBad" class="legend-item is-bad">
        <i class="legend-dot is-bad" aria-hidden="true" />
        {{ t('dashboard.trendBadPoints', { n: totalBad }) }}
      </span>
    </div>

    <div class="chart-body">
      <!-- viewBox 固定、宽度自适应：坐标在脚本里算一次，模板不做几何运算。
           与环形图同一套做法，避免为一个趋势图引入整套图表库。 -->
      <svg
        class="chart-svg"
        :viewBox="`0 0 ${W} ${H}`"
        role="img"
        :aria-label="ariaLabel"
        @mousemove="handleMove"
        @mouseleave="hover = null"
      >
        <!-- 在线率网格 -->
        <g class="grid">
          <line
            v-for="tick in onlineTicks"
            :key="`og${tick}`"
            :x1="PAD.left"
            :x2="W - PAD.right"
            :y1="onlineY(tick)"
            :y2="onlineY(tick)"
          />
          <text
            v-for="tick in onlineTicks"
            :key="`ot${tick}`"
            class="axis"
            :x="PAD.left - 8"
            :y="onlineY(tick) + 3"
            text-anchor="end"
          >
            {{ tick }}%
          </text>
        </g>

        <text
          v-if="!hasRate"
          class="empty-label"
          :x="PAD.left + innerW / 2"
          :y="ONLINE_TOP + ONLINE_H / 2"
          text-anchor="middle"
        >
          {{ t('dashboard.trendNoOverview') }}
        </text>

        <path
          v-for="(seg, i) in areaSegments"
          :key="`a${i}`"
          class="area"
          :class="rateClass"
          :d="seg"
        />
        <path
          v-for="(seg, i) in lineSegments"
          :key="`l${i}`"
          class="line"
          :class="rateClass"
          :d="seg"
        />
        <circle
          v-if="lastRate != null"
          class="last-dot"
          :class="rateClass"
          :cx="x(lastTs)"
          :cy="onlineY(lastRate)"
          r="2.6"
        />

        <!-- 采集吞吐网格 -->
        <g class="grid">
          <line
            :x1="PAD.left"
            :x2="W - PAD.right"
            :y1="THROUGH_BOTTOM"
            :y2="THROUGH_BOTTOM"
          />
          <line
            class="grid-soft"
            :x1="PAD.left"
            :x2="W - PAD.right"
            :y1="THROUGH_TOP"
            :y2="THROUGH_TOP"
          />
          <text class="axis" :x="PAD.left - 8" :y="THROUGH_TOP + 3" text-anchor="end">
            {{ pointsMax }}
          </text>
          <text class="axis" :x="PAD.left - 8" :y="THROUGH_BOTTOM + 3" text-anchor="end">
            0
          </text>
        </g>

        <g class="bars">
          <rect
            v-for="bar in bars"
            :key="`b${bar.ts}`"
            class="bar"
            :x="bar.x"
            :y="bar.y"
            :width="barW"
            :height="bar.h"
            rx="1"
          />
          <rect
            v-for="bar in badBars"
            :key="`bb${bar.ts}`"
            class="bar-bad"
            :x="bar.x"
            :y="bar.y"
            :width="barW"
            :height="bar.badH"
            rx="1"
          />
        </g>

        <!-- 时间轴 -->
        <text
          v-for="tick in xTicks"
          :key="`x${tick.label}`"
          class="axis"
          :x="tick.x"
          :y="H - 8"
          :text-anchor="tick.anchor"
        >
          {{ tick.label }}
        </text>

        <!-- 悬停十字线 -->
        <g v-if="hover">
          <line
            class="hover-line"
            :x1="hover.x"
            :x2="hover.x"
            :y1="ONLINE_TOP - 4"
            :y2="THROUGH_BOTTOM"
          />
          <circle
            v-if="hover.rate != null"
            class="hover-dot"
            :cx="hover.x"
            :cy="onlineY(hover.rate)"
            r="3"
          />
          <rect
            v-if="hover.barH > 0"
            class="hover-bar"
            :x="hover.x - barW / 2"
            :y="hover.barY"
            :width="barW"
            :height="hover.barH"
          />
        </g>
      </svg>

      <div v-if="hover" class="chart-tip" :style="{ left: `${hover.leftPct}%` }">
        <div class="tip-time">{{ hover.timeLabel }}</div>
        <div class="tip-row">
          <i class="tip-dot is-online" aria-hidden="true" />
          {{ t('dashboard.trendOnlineRate') }}
          <b>
            {{ hover.collected ? `${hover.online}/${hover.collected}` : '—' }}
            · {{ hover.rate == null ? '—' : `${hover.rate}%` }}
          </b>
        </div>
        <div class="tip-row">
          <i class="tip-dot is-throughput" aria-hidden="true" />
          {{ t('dashboard.trendThroughput') }}
          <b>{{ hover.points }}</b>
          <span v-if="hover.badPoints" class="tip-warn">
            · {{ t('dashboard.trendBadPoints', { n: hover.badPoints }) }}
          </span>
        </div>
        <div v-if="hover.queueDepth || hover.spoolDepth" class="tip-row is-muted">
          {{ t('dashboard.outletQueue', { n: hover.queueDepth }) }} ·
          {{ t('dashboard.outletSpool', { n: hover.spoolDepth }) }}
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { parseBackendTime } from '@/utils/datetime'

const props = defineProps({
  samples: {
    type: Array,
    required: true,
  },
  // 后端采样间隔（秒）。用于判断两点之间是正常间隔还是断档。
  intervalSec: {
    type: Number,
    default: 60,
  },
})

const { t } = useI18n()

// ---------- 几何常量 ----------
const W = 720
const H = 236
const PAD = { left: 44, right: 12 }
const ONLINE_TOP = 14
const ONLINE_H = 86
const THROUGH_TOP = 142
const THROUGH_H = 62
const THROUGH_BOTTOM = THROUGH_TOP + THROUGH_H
const innerW = W - PAD.left - PAD.right

const onlineTicks = [100, 50, 0]

function x(ts) {
  const span = endTs.value - startTs.value
  if (span <= 0) return PAD.left
  return PAD.left + ((ts - startTs.value) / span) * innerW
}

function onlineY(rate) {
  return ONLINE_TOP + (1 - rate / 100) * ONLINE_H
}

// ---------- 数据 ----------
// 缺少 overview 的采样点 rate 为 null：曲线在这里断开，而不是画成 0%。
const parsed = computed(() =>
  props.samples
    .map((s) => {
      const date = parseBackendTime(s.time)
      if (!date) return null
      // 与首页环形图同一口径：有离线设备时压到 99%，不让曲线在
      // 「离线 N 台」的同一屏上画出一条 100% 的线
      const pct =
        s.hasOverview && s.collected > 0
          ? Math.min(100, Math.max(0, Math.round((s.online / s.collected) * 100)))
          : null
      const rate = pct !== null && s.offline > 0 ? Math.min(99, pct) : pct
      return { ...s, ts: date.getTime(), date, rate }
    })
    .filter(Boolean)
)

const startTs = computed(() => parsed.value[0]?.ts ?? 0)
const endTs = computed(() => {
  const list = parsed.value
  if (!list.length) return 0
  const last = list[list.length - 1].ts
  // 只有一个点时给出一分钟以上的横轴，避免除零，也让点落在图内而不是边缘
  return Math.max(last, list[0].ts + props.intervalSec * 1000)
})
const lastTs = computed(() => parsed.value[parsed.value.length - 1]?.ts ?? 0)

const lastRate = computed(() => {
  for (let i = parsed.value.length - 1; i >= 0; i--) {
    if (parsed.value[i].rate != null) return parsed.value[i].rate
  }
  return null
})

const hasRate = computed(() => parsed.value.some((p) => p.rate != null))

// 颜色表达当前健康等级，与首页环形图同一套阈值
const rateClass = computed(() => {
  if (lastRate.value == null) return 'is-unknown'
  if (lastRate.value >= 90) return 'is-ok'
  if (lastRate.value >= 70) return 'is-warn'
  return 'is-bad'
})

const gapMs = computed(() => props.intervalSec * 1000 * 1.6)

function buildSegments(valueOf) {
  const segments = []
  let current = []
  let prevTs = null
  const flush = () => {
    if (current.length) {
      segments.push(current)
      current = []
    }
  }
  for (const point of parsed.value) {
    const value = valueOf(point)
    if (value == null) {
      flush()
      prevTs = null
      continue
    }
    if (prevTs != null && point.ts - prevTs > gapMs.value) flush()
    current.push(point)
    prevTs = point.ts
  }
  flush()
  return segments
}

const lineSegments = computed(() =>
  buildSegments((p) => p.rate).map(
    (seg) => `M ${seg.map((p) => `${x(p.ts)} ${onlineY(p.rate)}`).join(' L ')}`
  )
)

const areaSegments = computed(() =>
  buildSegments((p) => p.rate).map((seg) => {
    const baseline = ONLINE_TOP + ONLINE_H
    const points = seg.map((p) => `${x(p.ts)} ${onlineY(p.rate)}`).join(' L ')
    const first = seg[0]
    const last = seg[seg.length - 1]
    return `M ${x(first.ts)} ${baseline} L ${points} L ${x(last.ts)} ${baseline} Z`
  })
)

// 纵轴取一个整齐的上限，避免出现 137 这种刻度
function niceMax(value) {
  if (!Number.isFinite(value) || value <= 0) return 10
  const mag = 10 ** Math.floor(Math.log10(value))
  const n = value / mag
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10
  return step * mag
}

const pointsMax = computed(() => niceMax(Math.max(0, ...parsed.value.map((p) => p.points))))

const barW = computed(() =>
  Math.max(0.8, Math.min(7, (innerW / Math.max(1, parsed.value.length)) * 0.62))
)

const bars = computed(() =>
  parsed.value.map((p) => {
    const h = p.points > 0 ? Math.max(1, (p.points / pointsMax.value) * THROUGH_H) : 0
    return {
      ts: p.ts,
      x: x(p.ts) - barW.value / 2,
      y: THROUGH_BOTTOM - h,
      h,
      badH:
        p.badPoints > 0
          ? Math.max(1, Math.min(h, (p.badPoints / pointsMax.value) * THROUGH_H))
          : 0,
    }
  })
)

const badBars = computed(() => bars.value.filter((b) => b.badH > 0))

const totalBad = computed(() =>
  parsed.value.reduce((sum, point) => sum + (point.badPoints || 0), 0)
)

// ---------- 时间轴 ----------
const spanMs = computed(() => endTs.value - startTs.value)
const withDate = computed(() => spanMs.value > 12 * 3600 * 1000)

function axisLabel(date) {
  const hh = String(date.getHours()).padStart(2, '0')
  const mm = String(date.getMinutes()).padStart(2, '0')
  if (!withDate.value) return `${hh}:${mm}`
  return `${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${hh}:${mm}`
}

const xTicks = computed(() => {
  const ticks = []
  const seen = new Set()
  for (let i = 0; i <= 4; i++) {
    const fraction = i / 4
    const date = new Date(startTs.value + spanMs.value * fraction)
    const label = axisLabel(date)
    if (seen.has(label)) continue
    seen.add(label)
    ticks.push({
      x: PAD.left + innerW * fraction,
      label,
      anchor: i === 0 ? 'start' : i === 4 ? 'end' : 'middle',
    })
  }
  return ticks
})

const ariaLabel = computed(
  () => `${t('dashboard.trendOnlineRate')} / ${t('dashboard.trendThroughput')}`
)

// ---------- 悬停 ----------
const hover = ref(null)

function handleMove(event) {
  if (!parsed.value.length) return
  const rect = event.currentTarget.getBoundingClientRect()
  if (!rect.width) return
  const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width))
  const targetTs = startTs.value + spanMs.value * ratio

  let nearest = parsed.value[0]
  for (const point of parsed.value) {
    if (Math.abs(point.ts - targetTs) < Math.abs(nearest.ts - targetTs)) nearest = point
  }

  const xx = x(nearest.ts)
  const barH =
    nearest.points > 0 ? Math.max(1, (nearest.points / pointsMax.value) * THROUGH_H) : 0
  const hh = String(nearest.date.getHours()).padStart(2, '0')
  const mm = String(nearest.date.getMinutes()).padStart(2, '0')

  hover.value = {
    x: xx,
    rate: nearest.rate,
    online: nearest.online,
    collected: nearest.collected,
    points: nearest.points,
    badPoints: nearest.badPoints,
    queueDepth: nearest.queueDepth,
    spoolDepth: nearest.spoolDepth,
    barY: THROUGH_BOTTOM - barH,
    barH,
    leftPct: Math.min(88, Math.max(12, (xx / W) * 100)),
    timeLabel: withDate.value
      ? `${pad(nearest.date.getMonth() + 1)}-${pad(nearest.date.getDate())} ${hh}:${mm}`
      : `${hh}:${mm}`,
  }
}

function pad(n) {
  return String(n).padStart(2, '0')
}
</script>

<style scoped>
.trend-chart {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.chart-legend {
  display: flex;
  gap: 16px;
  margin-bottom: 4px;
  font-size: 12px;
  color: var(--ink-3);
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.legend-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
}

.legend-dot.is-online {
  background: var(--success);
}

.legend-dot.is-throughput {
  background: var(--brand);
}

.legend-dot.is-bad {
  background: var(--danger);
}

.legend-item.is-bad {
  color: var(--danger);
}

.legend-unit {
  color: var(--ink-4);
}

.chart-body {
  position: relative;
  min-height: 0;
}

.chart-svg {
  display: block;
  width: 100%;
  height: auto;
  overflow: visible;
}

/* 网格与坐标：发丝线 + 小字号，不与数据抢注意力 */
.grid line {
  stroke: var(--line-soft);
  stroke-width: 1;
}

.grid line.grid-soft {
  stroke-dasharray: 3 4;
  stroke: var(--line);
}

.axis {
  font-size: 10.5px;
  fill: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

.empty-label {
  font-size: 12px;
  fill: var(--ink-4);
}

/* 在线率：面积表达连续性，线表达当前值 */
.area {
  fill: rgba(5, 150, 105, 0.08);
}

.area.is-warn {
  fill: rgba(217, 119, 6, 0.08);
}

.area.is-bad {
  fill: rgba(225, 29, 72, 0.08);
}

.area.is-unknown {
  fill: rgba(148, 163, 184, 0.08);
}

.line {
  fill: none;
  stroke: var(--success);
  stroke-width: 1.8;
  stroke-linejoin: round;
  stroke-linecap: round;
}

.line.is-warn {
  stroke: var(--warning);
}

.line.is-bad {
  stroke: var(--danger);
}

.line.is-unknown {
  stroke: var(--ink-4);
}

.last-dot {
  fill: var(--success);
}

.last-dot.is-warn {
  fill: var(--warning);
}

.last-dot.is-bad {
  fill: var(--danger);
}

.last-dot.is-unknown {
  fill: var(--ink-4);
}

.bar {
  fill: var(--brand);
  opacity: 0.72;
}

.bar-bad {
  fill: var(--danger);
  opacity: 0.85;
}

.hover-line {
  stroke: var(--ink-4);
  stroke-width: 1;
  stroke-dasharray: 3 3;
}

.hover-dot {
  fill: #fff;
  stroke: var(--success);
  stroke-width: 1.8;
}

.hover-bar {
  fill: none;
  stroke: var(--brand);
  stroke-width: 1;
  opacity: 0.6;
}

/* 工具提示是 HTML，方便换行与文案；坐标由脚本换算成百分比 */
.chart-tip {
  position: absolute;
  top: 2px;
  z-index: 3;
  min-width: 150px;
  padding: 9px 11px;
  color: var(--ink-2);
  font-size: 12px;
  line-height: 1.55;
  pointer-events: none;
  background: rgba(255, 255, 255, 0.97);
  border: 1px solid var(--line);
  border-radius: 10px;
  box-shadow: 0 10px 26px -14px rgba(15, 23, 42, 0.35);
  transform: translateX(-50%);
}

.tip-time {
  margin-bottom: 4px;
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.tip-row {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}

.tip-row b {
  margin-left: auto;
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.tip-row.is-muted {
  color: var(--ink-4);
}

.tip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.tip-dot.is-online {
  background: var(--success);
}

.tip-dot.is-throughput {
  background: var(--brand);
}

.tip-warn {
  color: var(--danger);
}
</style>
