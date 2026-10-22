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
          v-for="(seg, i) in bandSegments"
          :key="`r${i}`"
          class="band"
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
            :key="`b${bar.order}`"
            class="bar"
            :x="bar.x"
            :y="bar.y"
            :width="barW"
            :height="bar.h"
            rx="1"
          />
          <rect
            v-for="bar in badBars"
            :key="`bb${bar.order}`"
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
          :y="AXIS_Y"
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
          <!-- 合并过的桶显示平均率与桶内范围；单点仍显示原始的 在线/采集 -->
          <b v-if="hover.samples > 1">
            {{ hover.rate == null ? '—' : `${hover.rate}%` }}
            <span v-if="hover.rateMin !== hover.rateMax" class="tip-range">
              ({{ hover.rateMin }}–{{ hover.rateMax }}%)
            </span>
          </b>
          <b v-else>
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
const PAD = { left: 44, right: 28 }
const ONLINE_TOP = 14
const ONLINE_H = 86
const THROUGH_TOP = 142
const THROUGH_H = 62
const THROUGH_BOTTOM = THROUGH_TOP + THROUGH_H
// 时间刻度紧贴吞吐柱图下方：它属于柱图的坐标轴，不该飘在底部空白里
const AXIS_Y = THROUGH_BOTTOM + 14
const H = AXIS_Y + 8
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
const spanMs = computed(() => endTs.value - startTs.value)
const withDate = computed(() => spanMs.value > 12 * 3600 * 1000)

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

// ---------- 降采样 ----------
// 24 小时窗口是 1440 个分钟点，摊在 ~660px 宽的图里每根柱子不到半个像素，
// 叠在一起只剩一片噪点；6 小时的 360 个点也同样密。按时间合并成 ~100 个桶：
// 曲线取桶内平均值（上下沿画桶内最小/最大值），柱子取桶内平均吞吐
// （仍是图例里的「点/分」口径），每个桶的时间范围与明细在悬停里给出。
const MAX_BUCKETS = 96
// 桶宽取整分钟，边界落在整分钟上：6h → 5 分钟，24h → 15 分钟
const BUCKET_STEPS = [1, 2, 3, 5, 10, 15, 30, 60]

const bucketMs = computed(() => {
  const intervalMs = Math.max(1, props.intervalSec) * 1000
  const span = Math.max(spanMs.value, intervalMs)
  const neededMs = Math.ceil(span / MAX_BUCKETS)
  const step = BUCKET_STEPS.find((m) => m * 60000 >= neededMs) ?? 60
  return Math.max(intervalMs, step * 60000)
})

const buckets = computed(() => {
  const intervalMs = Math.max(1, props.intervalSec) * 1000
  const gapMs = intervalMs * 1.6
  const width = bucketMs.value
  const list = []

  let current = null
  let prevTs = null
  for (const point of parsed.value) {
    // 以 epoch 对齐桶边界，桶的范围在悬停里读起来是整分钟
    const index = Math.floor(point.ts / width)
    // 跨桶，或与前一个采样点之间出现断档（网关重启 / 采集停止）就另起一桶：
    // 曲线在 gapBefore 处断开，而不是拉一条直线跨过空窗
    const gap = prevTs != null && point.ts - prevTs > gapMs
    if (!current || index !== current.index || gap) {
      current = {
        index,
        gapBefore: gap,
        tsSum: 0,
        samples: 0,
        firstTs: point.ts,
        lastTs: point.ts,
        rateSum: 0,
        rateCount: 0,
        rateMin: null,
        rateMax: null,
        pointsSum: 0,
        badSum: 0,
        last: point,
      }
      list.push(current)
    }
    current.samples += 1
    current.tsSum += point.ts
    current.lastTs = point.ts
    current.last = point
    current.pointsSum += point.points || 0
    current.badSum += point.badPoints || 0
    if (point.rate != null) {
      current.rateSum += point.rate
      current.rateCount += 1
      current.rateMin =
        current.rateMin == null ? point.rate : Math.min(current.rateMin, point.rate)
      current.rateMax =
        current.rateMax == null ? point.rate : Math.max(current.rateMax, point.rate)
    }
    prevTs = point.ts
  }

  return list.map((b, order) => ({
    order,
    gapBefore: b.gapBefore,
    ts: b.tsSum / b.samples,
    fromTs: b.firstTs,
    toTs: b.lastTs + intervalMs,
    samples: b.samples,
    rate: b.rateCount ? Math.round(b.rateSum / b.rateCount) : null,
    rateMin: b.rateMin,
    rateMax: b.rateMax,
    // 平均每分钟点数：桶宽变了，纵轴口径仍是「点/分」
    points: b.pointsSum / b.samples,
    badPoints: b.badSum / b.samples,
    badTotal: b.badSum,
    // 悬停展示用桶内最后一个点的瞬时状态（队列/积压本来就是仪表值）
    online: b.last.online,
    collected: b.last.collected,
    queueDepth: b.last.queueDepth,
    spoolDepth: b.last.spoolDepth,
  }))
})

const rateSegments = computed(() => {
  const segments = []
  let current = []
  const flush = () => {
    if (current.length) {
      segments.push(current)
      current = []
    }
  }
  for (const bucket of buckets.value) {
    if (bucket.rate == null) {
      flush()
      continue
    }
    if (bucket.gapBefore) flush()
    current.push(bucket)
  }
  flush()
  return segments
})

const lineSegments = computed(() =>
  rateSegments.value.map(
    (seg) => `M ${seg.map((b) => `${x(b.ts)} ${onlineY(b.rate)}`).join(' L ')}`
  )
)

const areaSegments = computed(() =>
  rateSegments.value.map((seg) => {
    const baseline = ONLINE_TOP + ONLINE_H
    const points = seg.map((b) => `${x(b.ts)} ${onlineY(b.rate)}`).join(' L ')
    const first = seg[0]
    const last = seg[seg.length - 1]
    return `M ${x(first.ts)} ${baseline} L ${points} L ${x(last.ts)} ${baseline} Z`
  })
)

// 平均线会抹平短时掉线（24h 下 15 分钟的桶里掉 1 分钟，
// 平均只降 1 个点出头），所以再画一条桶内最小/最大值的包络
const bandSegments = computed(() =>
  rateSegments.value
    .filter((seg) => seg.some((b) => b.rateMin !== b.rateMax))
    .map((seg) => {
      const top = seg.map((b) => `${x(b.ts)} ${onlineY(b.rateMax)}`).join(' L ')
      const bottom = [...seg]
        .reverse()
        .map((b) => `${x(b.ts)} ${onlineY(b.rateMin)}`)
        .join(' L ')
      return `M ${top} L ${bottom} Z`
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

const pointsMax = computed(() => niceMax(Math.max(0, ...buckets.value.map((b) => b.points))))

const barW = computed(() =>
  Math.max(0.8, Math.min(7, (innerW / Math.max(1, buckets.value.length)) * 0.62))
)

const bars = computed(() =>
  buckets.value.map((b) => {
    const h = b.points > 0 ? Math.max(1, (b.points / pointsMax.value) * THROUGH_H) : 0
    return {
      order: b.order,
      x: x(b.ts) - barW.value / 2,
      y: THROUGH_BOTTOM - h,
      h,
      badH:
        b.badPoints > 0
          ? Math.max(1, Math.min(h, (b.badPoints / pointsMax.value) * THROUGH_H))
          : 0,
    }
  })
)

const badBars = computed(() => bars.value.filter((b) => b.badH > 0))

const totalBad = computed(() =>
  parsed.value.reduce((sum, point) => sum + (point.badPoints || 0), 0)
)

// ---------- 时间轴 ----------
function axisLabel(date) {
  const hh = String(date.getHours()).padStart(2, '0')
  const mm = String(date.getMinutes()).padStart(2, '0')
  if (!withDate.value) return `${hh}:${mm}`
  return `${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${hh}:${mm}`
}

// 桶的起止时间：同一天时后半段只留时分，避免「10-22 04:00–10-22 04:15」的重复
function rangeLabel(fromTs, toTs) {
  const from = new Date(fromTs)
  const to = new Date(toTs)
  const sameDay =
    from.getFullYear() === to.getFullYear() &&
    from.getMonth() === to.getMonth() &&
    from.getDate() === to.getDate()
  const tail =
    withDate.value && sameDay
      ? `${String(to.getHours()).padStart(2, '0')}:${String(to.getMinutes()).padStart(2, '0')}`
      : axisLabel(to)
  return `${axisLabel(from)}–${tail}`
}

// 刻度步长（秒）：一屏最多 5 段，24h 不会只剩一根线，几分钟的窗口也不会挤满文字
const TICK_STEPS = [60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]

// 按本地时钟对齐刻度：标签写的时间必须正好落在它画的位置上。
// 旧实现按比例取点再截断到分钟，数据只有几分钟时「10:01」会画在 10:01:30 处。
function alignedTick(ts, stepMs) {
  const offset = new Date(ts).getTimezoneOffset() * 60000
  return Math.ceil((ts - offset) / stepMs) * stepMs + offset
}

const xTicks = computed(() => {
  const start = startTs.value
  const end = endTs.value
  const span = end - start
  if (span <= 0) return []

  const stepSec =
    TICK_STEPS.find((s) => span / (s * 1000) <= 5) ?? TICK_STEPS[TICK_STEPS.length - 1]
  const stepMs = stepSec * 1000
  const pxPerMs = innerW / span

  const times = []
  for (let t = alignedTick(start, stepMs); t <= end; t += stepMs) times.push(t)

  // 端点刻度只在窗口足够长时补：标签只到分钟，短窗口里「15:03」会被画在
  // 15:03:37 的位置上。宁可少标两端，也不让标签和它所在的位置对不上。
  if (span >= 2 * 3600 * 1000) {
    const minGap = (withDate.value ? 96 : 52) / pxPerMs
    const inner = times.filter((t) => t - start >= minGap && end - t >= minGap)
    times.length = 0
    times.push(start, ...inner, end)
  }

  // 标签精度到分钟：相邻刻度可能同名，保留靠前（更准确）的那个
  const ticks = []
  const seen = new Set()
  const edgePx = 26
  for (const t of times) {
    const label = axisLabel(new Date(t))
    if (seen.has(label)) continue
    seen.add(label)
    ticks.push({
      x: x(t),
      label,
      anchor:
        (t - start) * pxPerMs < edgePx
          ? 'start'
          : (end - t) * pxPerMs < edgePx
            ? 'end'
            : 'middle',
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
  if (!buckets.value.length) return
  const rect = event.currentTarget.getBoundingClientRect()
  if (!rect.width) return
  const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width))
  const targetTs = startTs.value + spanMs.value * ratio

  let nearest = buckets.value[0]
  for (const bucket of buckets.value) {
    if (Math.abs(bucket.ts - targetTs) < Math.abs(nearest.ts - targetTs)) nearest = bucket
  }

  const xx = x(nearest.ts)
  const barH =
    nearest.points > 0 ? Math.max(1, (nearest.points / pointsMax.value) * THROUGH_H) : 0
  // 合并过的桶显示时间段，单点仍显示到分钟
  const timeLabel =
    nearest.samples > 1
      ? rangeLabel(nearest.fromTs, nearest.toTs)
      : axisLabel(new Date(nearest.fromTs))

  hover.value = {
    x: xx,
    samples: nearest.samples,
    rate: nearest.rate,
    rateMin: nearest.rateMin,
    rateMax: nearest.rateMax,
    online: nearest.online,
    collected: nearest.collected,
    points: Math.round(nearest.points),
    badPoints: Math.round(nearest.badTotal),
    queueDepth: nearest.queueDepth,
    spoolDepth: nearest.spoolDepth,
    barY: THROUGH_BOTTOM - barH,
    barH,
    leftPct: Math.min(88, Math.max(12, (xx / W) * 100)),
    timeLabel,
  }
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

/* 桶内最小/最大值包络：平均线抹平的短时掉线在这里仍然看得见 */
.band {
  fill: rgba(5, 150, 105, 0.16);
}

.band.is-warn {
  fill: rgba(217, 119, 6, 0.16);
}

.band.is-bad {
  fill: rgba(225, 29, 72, 0.16);
}

.band.is-unknown {
  fill: rgba(148, 163, 184, 0.16);
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

.tip-range {
  font-weight: 400;
  color: var(--ink-4);
}
</style>
