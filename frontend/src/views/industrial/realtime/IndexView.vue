<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <!-- 面包屑导航 -->
    <div class="flex items-center gap-2 text-sm text-gray-500 mb-4">
      <el-button text @click="goBack">
        <el-icon><ArrowLeft /></el-icon>
        {{ t('realtime.backToList') }}
      </el-button>
    </div>

    <h1 class="text-2xl font-bold text-gray-800 mb-6">
      {{ t('realtime.title') }} — {{ snapshot?.deviceName || deviceObjectName || '—' }}
    </h1>

    <!-- 设备与采集状态 -->
    <el-card class="status-card mb-4" shadow="never" v-loading="loading">
      <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div class="collect-item">
          <div class="collect-label">{{ t('realtime.protocol') }}</div>
          <div class="collect-value">{{ snapshot?.protocolName || '—' }}</div>
        </div>
        <div class="collect-item">
          <div class="collect-label">{{ t('realtime.online') }}</div>
          <div class="collect-value">
            <el-tag :type="snapshot?.online ? 'success' : 'danger'" size="small" effect="dark">
              {{ snapshot?.online ? t('realtime.onlineYes') : t('realtime.onlineNo') }}
            </el-tag>
          </div>
        </div>
        <div class="collect-item">
          <div class="collect-label">{{ t('realtime.collectorRunning') }}</div>
          <div class="collect-value">
            <el-tag :type="snapshot?.collectorRunning ? 'success' : 'info'" size="small">
              {{ snapshot?.collectorRunning ? t('realtime.collectorRunningYes') : t('realtime.collectorRunningNo') }}
            </el-tag>
          </div>
        </div>
        <div class="collect-item">
          <div class="collect-label">{{ t('realtime.collecting') }}</div>
          <div class="collect-value">
            <el-tag :type="snapshot?.collecting ? 'success' : 'warning'" size="small">
              {{ snapshot?.collecting ? t('realtime.collectingYes') : t('realtime.collectingNo') }}
            </el-tag>
          </div>
        </div>
        <div class="collect-item">
          <div class="collect-label">{{ t('realtime.lastSuccess') }}</div>
          <div class="collect-value">{{ snapshot?.lastSuccessTime || '—' }}</div>
        </div>
        <div class="collect-item">
          <div class="collect-label">{{ t('realtime.serverTime') }}</div>
          <div class="collect-value">{{ snapshot?.serverTime || '—' }}</div>
        </div>
      </div>
    </el-card>

    <!-- 诊断提示：说明「为什么没数据」，正常时不显示 -->
    <el-alert
      v-if="diagKey"
      :title="t(diagKey)"
      :type="diagType"
      show-icon
      :closable="false"
      class="mb-4"
    />

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between flex-wrap gap-3">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            :placeholder="t('realtime.searchPlaceholder')"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
            @clear="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="flex items-center gap-3">
          <span class="text-sm text-gray-500">{{ t('realtime.refreshInterval') }}</span>
          <el-select v-model="interval" style="width: 130px">
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
    </el-card>

    <!-- 实时数据表格 -->
    <el-card shadow="never">
      <el-table
        :data="tableData"
        v-loading="loading"
        stripe
        border
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="name" :label="t('realtime.pointName')" min-width="160" show-overflow-tooltip />
        <el-table-column prop="label" :label="t('realtime.pointLabel')" min-width="140" show-overflow-tooltip />
        <el-table-column :label="t('realtime.value')" min-width="160" align="right">
          <template #default="{ row }">
            <!-- :key 带值：值一变即换新节点，令高亮动画重新播放，
                 无需额外的定时器或脏值集合来跟踪「刚才变过」 -->
            <span
              v-if="row.collected"
              :key="`${row.addressId}|${row.value}`"
              class="value-flash"
            >{{ row.value }}</span>
            <span v-else class="text-gray-400">{{ t('realtime.emptyValue') }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="dataType" :label="t('realtime.dataType')" width="130" align="center" show-overflow-tooltip />
        <el-table-column :label="t('realtime.quality')" width="120" align="center">
          <template #default="{ row }">
            <template v-if="!row.collected">
              <span class="text-gray-400">—</span>
            </template>
            <el-tag v-else-if="row.quality === 192" type="success" size="small">
              {{ t('realtime.qualityGood') }}
            </el-tag>
            <!-- 坏点位显示**为什么坏**而不是一个裸的码：地址无效要改配置、通信失败等
                 下一轮就好，两者处置完全不同，只给一个数字等于让操作工去猜。
                 坏的是 quality 的低 6 位（1/2/3），0 是"坏但没归因"（仍显示原码）；
                 128（ABB 的不确定）值可用，不是坏，走最后那个 span。 -->
            <el-tag v-else-if="row.quality <= 3" type="danger" size="small">
              {{ qualityBadText(row) }}
            </el-tag>
            <span v-else class="text-gray-500">{{ row.quality }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('realtime.collectedAt')" width="180" align="center">
          <template #default="{ row }">
            {{ row.updatedAt || '—' }}
          </template>
        </el-table-column>
        <el-table-column :label="t('realtime.pointStatus')" width="110" align="center">
          <template #default="{ row }">
            <el-tag :type="pointStatusType(row)" size="small">
              {{ pointStatusText(row) }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>

      <!-- 分页 -->
      <div class="flex justify-end mt-4">
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
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Refresh } from '@element-plus/icons-vue'
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

const diagType = computed(() => {
  const key = diagKey.value
  if (key === 'realtime.diagWaiting') return 'info'
  if (key === 'realtime.diagOffline' || key === 'realtime.diagNotCollecting') return 'warning'
  return 'error'
})

function pointStatusType(row) {
  if (!row.collected) return 'info'
  return row.stale ? 'warning' : 'success'
}

function pointStatusText(row) {
  if (!row.collected) return t('realtime.statusPending')
  return row.stale ? t('realtime.statusStale') : t('realtime.statusNormal')
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
.status-card {
  --el-card-padding: 20px;
  border-radius: 12px;
}

.collect-item {
  padding: 12px 16px;
  background: #f5f7fa;
  border-radius: 8px;
}

.collect-label {
  font-size: 13px;
  color: #909399;
  margin-bottom: 4px;
}

.collect-value {
  font-size: 15px;
  font-weight: 500;
  color: #303133;
  word-break: break-all;
}

/* 值变化时闪烁一次：值是数据的核心，不闪的话用户看不出「数据在动」 */
.value-flash {
  display: inline-block;
  padding: 0 6px;
  border-radius: 4px;
  font-variant-numeric: tabular-nums;
  animation: value-flash 0.9s ease-out;
}

@keyframes value-flash {
  from {
    background-color: var(--el-color-primary-light-7);
  }
  to {
    background-color: transparent;
  }
}
</style>
