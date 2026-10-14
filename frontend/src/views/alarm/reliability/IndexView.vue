<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-2">{{ t('reliability.title') }}</h1>
    <!-- 常驻说明：这一页的两个榜单都**只覆盖出过问题的对象**，不写清楚会被读成全量清单 -->
    <p class="text-xs text-gray-400 leading-5 mb-6">
      {{ t('reliability.hint') }}
      <!-- 括号随语言走（中文全角 / 英文半角），不写死在模板里 -->
      <span v-if="gatewayName">{{ t('reliability.gatewaySuffix', { name: gatewayName }) }}</span>
    </p>

    <!-- 筛选工具栏：时间窗是必填的，两个榜单共用同一个窗口 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex flex-wrap items-center gap-3">
        <span class="text-sm text-gray-500">{{ t('reliability.rangeLabel') }}</span>
        <!-- 两个独立选择器，不用 daterange：两个组件的面板都停在所选日期所在月 -->
        <el-date-picker
          v-model="startDate"
          type="date"
          value-format="YYYY-MM-DD"
          :placeholder="t('reliability.rangeStart')"
          :disabled-date="disableFuture"
          :clearable="false"
          style="width: 150px"
          @change="onStartChange"
        />
        <span class="text-sm text-gray-400">{{ t('reliability.rangeSeparator') }}</span>
        <el-date-picker
          v-model="endDate"
          type="date"
          value-format="YYYY-MM-DD"
          :placeholder="t('reliability.rangeEnd')"
          :disabled-date="disableEnd"
          :clearable="false"
          style="width: 150px"
        />
        <el-button type="primary" :loading="loading" @click="handleSearch">
          {{ t('common.search') }}
        </el-button>
        <span v-if="windowDays" class="text-xs text-gray-400">
          {{ t('reliability.windowDays', { n: windowDays }) }}
        </span>
      </div>
    </el-card>

    <!-- 设备可靠性排行 -->
    <el-card shadow="never" class="mb-4">
      <template #header>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <span class="font-bold text-gray-700">{{ t('reliability.deviceCard') }}</span>
            <span class="ml-2 text-xs text-gray-400">{{ t('reliability.deviceNote') }}</span>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs text-gray-400">{{ t('reliability.limit') }}</span>
            <el-select v-model="deviceLimit" style="width: 100px" size="small" @change="fetchDevice">
              <el-option v-for="opt in DEVICE_LIMITS" :key="opt.value" :label="opt.label" :value="opt.value" />
            </el-select>
          </div>
        </div>
      </template>

      <el-table
        :data="deviceRows"
        v-loading="deviceLoading"
        stripe
        border
        style="width: 100%"
        :empty-text="deviceEmptyText"
      >
        <el-table-column prop="ranking" :label="t('reliability.rank')" width="70" align="center" />
        <el-table-column :label="t('reliability.device')" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ row.deviceName || row.deviceId }}</template>
        </el-table-column>
        <el-table-column :label="t('reliability.availability')" width="180">
          <template #default="{ row }">
            <div class="avail">
              <span class="avail-num" :class="toneClass(row.availability)">
                {{ percentText(row.availability) }}
              </span>
              <span class="avail-bar">
                <span
                  class="avail-fill"
                  :class="toneClass(row.availability)"
                  :style="{ width: `${clampPercent(row.availability)}%` }"
                />
              </span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="episodes"
          :label="t('reliability.episodes')"
          width="110"
          align="center"
        />
        <el-table-column :label="t('reliability.downDuration')" width="150" align="center">
          <template #default="{ row }">{{ formatDuration(row.downSeconds) }}</template>
        </el-table-column>
        <el-table-column :label="t('reliability.mttr')" width="150" align="center">
          <!-- 后端用 -1 表示「窗口内没有已完成的故障段」，不是「修复耗时 -1 秒」 -->
          <template #default="{ row }">
            <span v-if="row.mttrSeconds < 0" class="text-gray-400">{{ EMPTY }}</span>
            <span v-else>{{ formatDuration(row.mttrSeconds) }}</span>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 点位异常排行 -->
    <el-card shadow="never">
      <template #header>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <span class="font-bold text-gray-700">{{ t('reliability.pointCard') }}</span>
            <span class="ml-2 text-xs text-gray-400">{{ t('reliability.pointNote') }}</span>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-xs text-gray-400">{{ t('reliability.limit') }}</span>
            <el-select v-model="pointLimit" style="width: 100px" size="small" @change="fetchPoint">
              <el-option v-for="opt in POINT_LIMITS" :key="opt.value" :label="opt.label" :value="opt.value" />
            </el-select>
          </div>
        </div>
      </template>

      <el-table
        :data="pointRows"
        v-loading="pointLoading"
        stripe
        border
        style="width: 100%"
        :empty-text="pointEmptyText"
      >
        <el-table-column prop="ranking" :label="t('reliability.rank')" width="70" align="center" />
        <el-table-column
          prop="targetName"
          :label="t('reliability.point')"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          prop="qualityEpisodes"
          :label="t('reliability.qualityEpisodes')"
          width="130"
          align="center"
        />
        <el-table-column
          prop="thresholdEpisodes"
          :label="t('reliability.thresholdEpisodes')"
          width="130"
          align="center"
        />
        <el-table-column
          prop="totalEpisodes"
          :label="t('reliability.totalEpisodes')"
          width="110"
          align="center"
        />
        <el-table-column :label="t('reliability.lastOccur')" width="180" align="center">
          <template #default="{ row }">{{ row.lastOccurTime || EMPTY }}</template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { getDeviceReliability, getPointAbnormal } from '@/api/modules/alarm'

const { t } = useI18n()

// 与后端 maxReliabilityWindowDays 是同一个数：超了后端会直接拒（护栏是护住那唯一的
// SQLite 连接，alarm 表永不清理）。前端先拦一道，省一次必然失败的往返。
const MAX_WINDOW_DAYS = 90

const EMPTY = '—'

// ---------- 时间窗 ----------
/**
 * 按**本地时区**格式化成 YYYY-MM-DD。
 *
 * 不能用 toISOString().slice(0,10)：那是 UTC。东八区的凌晨（本地 10-08 07:00 =
 * UTC 10-07 23:00）会算成前一天，用户看到的窗口比选的多一天且每次刷新还不一样。
 */
function fmtDate(d) {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/** 默认最近 7 天（含今天），与后端的「两端都含当天」口径对齐。 */
function defaultRange() {
  const today = new Date()
  const start = new Date(today)
  start.setDate(start.getDate() - 6)
  return [fmtDate(start), fmtDate(today)]
}

// 用两个**独立**的日期选择器，而不是 type="daterange"：daterange 打开弹层时右面板会
// 落在「下个月」（起止同月时 sortDates 走 maxDate.add(1,'month')），而未来日期被
// disableFuture 全禁掉 —— 只要窗口落在当前月内（默认那 7 天就是），右面板就整月变灰、
// 点上去 handlePickDate 直接 return；再加上 unlink-panels 让两个面板互不跟随，用户没
// 任何办法把它拨回可用月份。单日期选择器的面板永远停在所选日期所在月，不存在死面板。
const [defaultStart, defaultEnd] = defaultRange()
const startDate = ref(defaultStart)
const endDate = ref(defaultEnd)

/** 两个选择器合起来就是窗口；取数与校验仍按 [from, to] 两端使用。 */
const range = computed(() => [startDate.value, endDate.value])

/** 窗口天数（含两端），与后端 windowSeconds / 86400 同口径。 */
const windowDays = computed(() => {
  const [from, to] = range.value
  if (!from || !to) return 0
  const a = new Date(`${from}T00:00:00`)
  const b = new Date(`${to}T00:00:00`)
  if (Number.isNaN(a.getTime()) || Number.isNaN(b.getTime())) return 0
  return Math.round((b - a) / 86400000) + 1
})

// 未来的日期不可选：统计窗口是「已经发生的事」，选到明天只会得到一段空窗口
function disableFuture(date) {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return date.getTime() > today.getTime()
}

/**
 * 结束日期还要不早于开始日期。
 *
 * 比的是本地 YYYY-MM-DD 字符串：Element 传进来的 date 是当天 00:00（本地），拿它
 * 直接和 '2026-10-02' 这种字符串比会踩坑，先走 fmtDate 归一化。同一天是合法窗口
 * （窗口 1 天），所以用 < 而不是 <=。
 */
function disableEnd(date) {
  if (disableFuture(date)) return true
  return !!startDate.value && fmtDate(date) < startDate.value
}

/**
 * 开始日期晚于结束日期时，把结束日期一起顶过去。
 *
 * 与其灰掉开始日期里「晚于结束」的那一段（想往后挪窗口就得先改结束日期，绕），不如
 * 允许这么选、然后顺手把结束日期推到开始日期 —— 用户想表达的就是「窗口整体后移」。
 */
function onStartChange(val) {
  if (val && endDate.value && endDate.value < val) endDate.value = val
}

// ---------- 条数 ----------
// 显式把 limit 发给后端（而不是省略让它取默认）：两个端点的「省略」语义**刻意相反**
// —— 设备省略 = 不截断，点位省略 = 兜底 20。统一显式传值，界面上就不会出现
// 「同样写着 20，一个真的是 20 条、另一个是全部」这种事。
const DEVICE_LIMITS = computed(() => [
  { value: 10, label: '10' },
  { value: 20, label: '20' },
  { value: 50, label: '50' },
  { value: 100, label: '100' },
  { value: 0, label: t('reliability.limitAll') },
])

const POINT_LIMITS = [
  { value: 10, label: '10' },
  { value: 20, label: '20' },
  { value: 50, label: '50' },
  { value: 100, label: '100' },
]

const deviceLimit = ref(20)
const pointLimit = ref(20)

// ---------- 数据 ----------
const deviceRows = ref([])
const pointRows = ref([])
const deviceLoading = ref(false)
const pointLoading = ref(false)
const loading = ref(false)
const gatewayName = ref('')

// 取数失败与「窗口内确实没有」必须分开：前者清空表格并说明是取数失败，
// 否则「没有设备出过故障」这个结论会在一张因报错而空掉的表上被读成事实。
// 同一处取舍见 dashboard：那边用「—」而不是 0 表达取数失败。
const deviceError = ref(false)
const pointError = ref(false)

const deviceEmptyText = computed(() =>
  deviceError.value ? t('reliability.loadFailed') : t('reliability.emptyDevice')
)
const pointEmptyText = computed(() =>
  pointError.value ? t('reliability.loadFailed') : t('reliability.emptyPoint')
)

// ---------- 展示 ----------
// 后端返回的 availability 已经是**百分数**（0~100，两位小数），这里只做钳位。
// 别再乘 100 —— 那会把 99.17 顶成 100，停过机的设备在榜上显示满勤。
function clampPercent(value) {
  const n = Number(value)
  if (!Number.isFinite(n)) return 0
  return Math.min(100, Math.max(0, n))
}

function percentText(value) {
  if (!Number.isFinite(Number(value))) return EMPTY
  return `${clampPercent(value).toFixed(2)}%`
}

// 阈值沿用运维的直觉：99% 是工业可用性的常规线，95% 以下就该有人看了
function toneClass(value) {
  const pct = clampPercent(value)
  if (pct >= 99) return 'is-good'
  if (pct >= 95) return 'is-warn'
  return 'is-bad'
}

/**
 * 秒 → 最显著的两个单位（「1 天 5 小时」/「2 小时 30 分」/「30 分」/「45 秒」）。
 *
 * 取「前两个非零单位」而不是「天/小时优先」：后者的写法（有小时就不显示分）会把
 * 9000 秒显示成「2 小时」，把 4500 秒的 MTTR 显示成「1 小时」—— 分别少了 30 分与
 * 15 分，在这两列上是实打实的少报。只有秒数本身不足 1 分钟时才轮到「秒」。
 */
function formatDuration(seconds) {
  const total = Math.round(Number(seconds) || 0)
  if (total <= 0) return t('reliability.durationZero')
  const d = Math.floor(total / 86400)
  const h = Math.floor((total % 86400) / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const parts = []
  if (d) parts.push(t('reliability.durationDay', { n: d }))
  if (h) parts.push(t('reliability.durationHour', { n: h }))
  if (m) parts.push(t('reliability.durationMinute', { n: m }))
  if (s) parts.push(t('reliability.durationSecond', { n: s }))
  return parts.slice(0, 2).join(' ')
}

// ---------- 取数 ----------
async function fetchDevice() {
  deviceLoading.value = true
  try {
    const res = await getDeviceReliability({
      from: range.value[0],
      to: range.value[1],
      limit: deviceLimit.value || undefined,
    })
    deviceRows.value = res?.records ?? []
    deviceError.value = false
    // 网关身份随报表一起回来（见 getDeviceReliability 的说明），拿来当标题后缀，
    // 截图/转发出去时这份报表才自证是哪台网关出的
    if (res?.gatewayName) gatewayName.value = res.gatewayName
  } catch (err) {
    console.error('获取设备可靠性统计失败', err)
    deviceRows.value = []
    deviceError.value = true
  } finally {
    deviceLoading.value = false
  }
}

async function fetchPoint() {
  pointLoading.value = true
  try {
    const res = await getPointAbnormal({
      from: range.value[0],
      to: range.value[1],
      limit: pointLimit.value || undefined,
    })
    pointRows.value = res?.records ?? []
    pointError.value = false
    if (res?.gatewayName) gatewayName.value = res.gatewayName
  } catch (err) {
    console.error('获取点位异常统计失败', err)
    pointRows.value = []
    pointError.value = true
  } finally {
    pointLoading.value = false
  }
}

function handleSearch() {
  // 两个选择器都设了 clearable=false，正常走不到这里；留着当护栏（比如以后改了配置）
  if (!startDate.value || !endDate.value) {
    ElMessage.warning(t('reliability.rangeRequired'))
    return
  }
  if (windowDays.value > MAX_WINDOW_DAYS) {
    ElMessage.warning(t('reliability.rangeTooLong', { n: MAX_WINDOW_DAYS }))
    return
  }
  // 两个榜单共用一个窗口、各自独立取数：其中一个失败不该把另一个也清掉
  loading.value = true
  Promise.allSettled([fetchDevice(), fetchPoint()]).finally(() => {
    loading.value = false
  })
}

onMounted(handleSearch)
</script>

<style scoped>
/* 可用率：数字与细条同色，读「多少」和读「好赖」是同一个动作 */
.avail {
  display: flex;
  align-items: center;
  gap: 8px;
}

.avail-num {
  flex: none;
  width: 62px;
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.avail-bar {
  flex: 1;
  height: 6px;
  overflow: hidden;
  background: var(--el-fill-color);
  border-radius: 3px;
}

.avail-fill {
  display: block;
  height: 100%;
  border-radius: 3px;
  background: currentColor;
  transition: width 0.6s cubic-bezier(0.16, 1, 0.3, 1);
}

.is-good {
  color: var(--el-color-success);
}

.is-warn {
  color: var(--el-color-warning);
}

.is-bad {
  color: var(--el-color-danger);
}
</style>
