<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <!-- 返回入口放在标题块里，与设备对象页的层级一致，不再单独占一条面包屑 -->
        <button type="button" class="iot-back" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          {{ t('realtime.backToList') }}
        </button>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.realtime') }}
        </p>
        <h1 class="iot-title">
          {{ t('realtime.title') }} — {{ snapshot?.deviceName || deviceObjectName || '—' }}
        </h1>
        <p class="iot-sub">{{ t('realtime.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ total }}</span>
          <span class="iot-stat-label">{{ t('realtime.pointUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 设备与采集状态：六项读数排成一张发丝线格栅，不再是一堆灰色小块 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div v-loading="loading" class="status-strip">
        <div class="status-item">
          <p class="status-label">{{ t('realtime.protocol') }}</p>
          <p class="status-value">{{ snapshot?.protocolName || '—' }}</p>
        </div>
        <div class="status-item">
          <p class="status-label">{{ t('realtime.online') }}</p>
          <div class="status-value">
            <span class="iot-pill" :class="snapshot?.online ? 'is-green' : 'is-rose'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ snapshot?.online ? t('realtime.onlineYes') : t('realtime.onlineNo') }}
            </span>
          </div>
        </div>
        <div class="status-item">
          <p class="status-label">{{ t('realtime.collectorRunning') }}</p>
          <div class="status-value">
            <span class="iot-pill" :class="snapshot?.collectorRunning ? 'is-green' : 'is-gray'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ snapshot?.collectorRunning ? t('realtime.collectorRunningYes') : t('realtime.collectorRunningNo') }}
            </span>
          </div>
        </div>
        <div class="status-item">
          <p class="status-label">{{ t('realtime.collecting') }}</p>
          <div class="status-value">
            <span class="iot-pill" :class="snapshot?.collecting ? 'is-green' : 'is-amber'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ snapshot?.collecting ? t('realtime.collectingYes') : t('realtime.collectingNo') }}
            </span>
          </div>
        </div>
        <div class="status-item">
          <p class="status-label">{{ t('realtime.lastSuccess') }}</p>
          <p class="status-value is-time">{{ snapshot?.lastSuccessTime || '—' }}</p>
        </div>
        <div class="status-item">
          <p class="status-label">{{ t('realtime.serverTime') }}</p>
          <p class="status-value is-time">{{ snapshot?.serverTime || '—' }}</p>
        </div>
      </div>
    </section>

    <!-- 诊断提示：说明「为什么没数据」，正常时不显示 -->
    <div v-if="diagKey" class="iot-notice iot-enter" :class="diagClass">
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon>
          <InfoCircle v-if="diagClass === 'is-info'" />
          <AlertTriangle v-else />
        </el-icon>
      </span>
      <p class="iot-notice-text">{{ t(diagKey) }}</p>
    </div>

    <!-- 工作区：筛选、刷新控制、表格与分页共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 120ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('realtime.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
            @clear="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <span class="refresh-label">{{ t('realtime.refreshInterval') }}</span>
          <el-select v-model="interval" class="refresh-interval">
            <el-option
              v-for="opt in intervalOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
          <el-button :type="paused ? 'success' : 'default'" @click="togglePause">
            {{ paused ? t('realtime.resume') : t('realtime.pause') }}
          </el-button>
          <el-button :icon="Refresh" @click="fetchSnapshot(true)">
            {{ t('realtime.refreshNow') }}
          </el-button>
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <!-- 点位名是这一行的身份：与设备对象页一样给一个小图标底座 -->
        <el-table-column :label="t('realtime.pointName')" min-width="200">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Point /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="label"
          :label="t('realtime.pointLabel')"
          min-width="150"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('realtime.value')" min-width="160" align="right">
          <template #default="{ row }">
            <!-- :key 带值：值一变即换新节点，令高亮动画重新播放，
                 无需额外的定时器或脏值集合来跟踪「刚才变过」 -->
            <span
              v-if="row.collected"
              :key="`${row.addressId}|${row.value}`"
              class="value-flash"
            >{{ row.value }}</span>
            <span v-else class="iot-cell-muted">{{ t('realtime.emptyValue') }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('realtime.dataType')"
          width="130"
          align="center"
        >
          <template #default="{ row }">
            <span v-if="row.dataType" class="iot-chip">{{ row.dataType }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('realtime.quality')" width="120" align="center">
          <template #default="{ row }">
            <span v-if="!row.collected" class="iot-cell-muted">—</span>
            <span v-else class="iot-pill" :class="qualityClass(row)">
              {{ qualityText(row) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('realtime.collectedAt')" width="160" class-name="iot-col-time">
          <template #default="{ row }">
            {{ row.updatedAt || '—' }}
          </template>
        </el-table-column>
        <el-table-column :label="t('realtime.pointStatus')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="pointStatusClass(row)">
              {{ pointStatusText(row) }}
            </span>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><ActivityHeartbeat /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('realtime.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('realtime.emptyHint') }}</p>
          </div>
        </template>
      </el-table>

      <div class="iot-pager">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @size-change="handleSizeChange"
          @current-change="handlePageChange"
        />
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  IconActivityHeartbeat as ActivityHeartbeat,
  IconAlertTriangle as AlertTriangle,
  IconArrowLeft as ArrowLeft,
  IconInfoCircle as InfoCircle,
  IconPoint as Point,
  IconRefresh as Refresh,
  IconSearch as Search,
} from '@tabler/icons-vue'
import { getRealtimeSnapshot } from '@/api/modules/realtime'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()
const deviceObjectId = route.params.deviceObjectId
// 从列表页带过来，用于首屏请求返回前先显示设备名（刷新时为空，由快照补上）
const deviceObjectName = route.query.deviceObjectName || ''

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

// ---------- 查询表单 ----------
const queryForm = reactive({
  name: '',
})

// ---------- 分页 ----------
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)

// ---------- 数据 ----------
const loading = ref(false)
const tableData = ref([])
// 快照头（设备状态 + 分页信息），随每次轮询整体替换
const snapshot = ref(null)

// ---------- 轮询控制 ----------
const paused = ref(false)
const interval = ref(2000)

const intervalOptions = computed(() => [
  { value: 1000, label: t('common.seconds', { n: 1 }) },
  { value: 2000, label: t('common.seconds', { n: 2 }) },
  { value: 5000, label: t('common.seconds', { n: 5 }) },
  { value: 10000, label: t('common.seconds', { n: 10 }) },
])

let timer = null
// 上一轮请求尚未返回时跳过本次触发，避免慢响应下请求堆积
let inFlight = false

function clearTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

// 起停定时器只需覆盖三种「不该轮询」的情形：用户主动暂停、标签页被切到后台、
// 页面已卸载（由 onUnmounted 的 clearTimer 兜底）。
function restartTimer() {
  clearTimer()
  if (paused.value || document.hidden) return
  timer = setInterval(() => fetchSnapshot(false), interval.value)
}

// 切换刷新间隔立即生效，不必等到下一次触发
watch(interval, restartTimer)

function togglePause() {
  paused.value = !paused.value
  restartTimer()
  // 从暂停恢复时立刻补一次，不必干等一个间隔
  if (!paused.value) fetchSnapshot(false)
}

// 标签页不可见时停轮询：用户看不见页面，但网关与 PLC 仍会被白白压着。
// 恢复可见时立刻拉一次，避免看到切走前的旧值。
function handleVisibilityChange() {
  if (document.hidden) {
    clearTimer()
  } else {
    restartTimer()
    if (!paused.value) fetchSnapshot(false)
  }
}

// ---------- 拉取快照 ----------
// showLoading 仅用于首屏、翻页与手动刷新；周期轮询不带加载态，
// 否则表格每秒闪一次 loading 遮罩，反而看不清数据变化。
async function fetchSnapshot(showLoading = false) {
  if (inFlight) return
  inFlight = true
  if (showLoading) loading.value = true
  try {
    const res = await getRealtimeSnapshot({
      deviceObjectId,
      page: currentPage.value,
      size: pageSize.value,
      name: queryForm.name || undefined,
    })
    snapshot.value = res
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 静默失败：保留上一轮数据，状态条上的「最近成功采集」会体现数据已陈旧。
    // 设备掉线时清空表格只会让页面变成空白，不如留着最后一次的值。
  } finally {
    inFlight = false
    if (showLoading) loading.value = false
  }
}

// ---------- 搜索 / 翻页 ----------
function handleSearch() {
  currentPage.value = 1
  fetchSnapshot(true)
}

function handleReset() {
  queryForm.name = ''
  currentPage.value = 1
  fetchSnapshot(true)
}

function handleSizeChange() {
  currentPage.value = 1
  fetchSnapshot(true)
}

function handlePageChange() {
  fetchSnapshot(true)
}

// ---------- 状态呈现 ----------
// 四类「为什么没数据」按指向性从强到弱排序：能给出确定原因的排在前面，
// 泛泛的「请稍候」放最后。
const diagKey = computed(() => {
  const s = snapshot.value
  if (!s) return null
  if (!s.collectorRunning) return 'realtime.diagCollectorStopped'
  if (s.total === 0) return 'realtime.diagNoAddress'
  if (!s.collecting) return 'realtime.diagNotCollecting'
  if (!s.online) return 'realtime.diagOffline'
  if (tableData.value.length > 0 && tableData.value.every((r) => !r.collected)) {
    return 'realtime.diagWaiting'
  }
  return null
})

const diagClass = computed(() => {
  const key = diagKey.value
  if (key === 'realtime.diagWaiting') return 'is-info'
  if (key === 'realtime.diagOffline' || key === 'realtime.diagNotCollecting') return 'is-warning'
  return 'is-error'
})

function pointStatusClass(row) {
  if (!row.collected) return 'is-gray'
  return row.stale ? 'is-amber' : 'is-green'
}

function pointStatusText(row) {
  if (!row.collected) return t('realtime.statusPending')
  return row.stale ? t('realtime.statusStale') : t('realtime.statusNormal')
}

// 质量码 → 胶囊：192 正常、低 6 位 1/2/3 是坏点位的三种成因；
// 其余（含 128「不确定但可用」与未归因的 0）按中性码显示，不伪装成正常。
function qualityClass(row) {
  if (row.quality === 192) return 'is-green'
  if (row.quality <= 3) return 'is-rose'
  return 'is-gray'
}

function qualityText(row) {
  if (row.quality === 192) return t('realtime.qualityGood')
  if (row.quality <= 3) return qualityBadText(row)
  return row.quality
}

// 坏子码 → i18n key。取值只有后端 driver.QualityBadXxx 这三个（低 6 位的子码），
// 故用显式映射而不是拼 key —— 拼错了 vue-i18n 会把 key 原样显示出来，
// 而"realtime.qualityBad.undefined"这种东西出现在运维页面上比不显示更糟。
const QUALITY_BAD_KEYS = {
  1: 'realtime.qualityBadAddress',
  2: 'realtime.qualityBadComms',
  3: 'realtime.qualityBadParse',
}

// qualityBadText 返回坏点位的文案；未归因（0）时退回原始质量码 ——
// 不把"未知"伪装成某个具体原因。
function qualityBadText(row) {
  const key = QUALITY_BAD_KEYS[row.quality]
  return key ? t(key) : String(row.quality)
}

// ---------- 生命周期 ----------
onMounted(() => {
  fetchSnapshot(true)
  restartTimer()
  document.addEventListener('visibilitychange', handleVisibilityChange)
})

onUnmounted(() => {
  // 必须清定时器：否则离开页面后仍在后台拉取，网关与 PLC 被持续压着
  clearTimer()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
/* 状态格栅：1px 的 gap 让每个读数成为独立的一格，靠底色当分隔线 */
.status-strip {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1px;
  background: var(--line-soft);
}

.status-item {
  min-width: 0;
  padding: 16px 20px;
  background: #fff;
}

.status-label {
  margin: 0;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--ink-4);
}

.status-value {
  margin: 6px 0 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.5;
  color: var(--ink);
  word-break: break-all;
}

.status-value.is-time {
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

.refresh-label {
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}

.refresh-interval {
  width: 118px;
}

/* 值变化时闪烁一次：值是数据的核心，不闪的话用户看不出「数据在动」 */
.value-flash {
  display: inline-block;
  padding: 0 6px;
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
  border-radius: 4px;
  animation: valueFlash 0.9s ease-out;
}

@keyframes valueFlash {
  from {
    background-color: var(--el-color-primary-light-7);
  }
  to {
    background-color: transparent;
  }
}

@media (max-width: 900px) {
  .status-strip {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (max-width: 560px) {
  .status-strip {
    grid-template-columns: 1fr;
  }
}
</style>
