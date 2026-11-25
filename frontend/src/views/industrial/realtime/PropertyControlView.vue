<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
属性级控制下发：属性与设备点位是 1 对 1，下发目标只有一条点位。

控制下发接口是 device-scoped 的（一条请求只写一台设备），因此这里仍按设备
分行、单独确认、单独下发、单独看结果 —— 与实时监控同一套展示，也让将来若
放开「一属性多点」时页面不必重写。属性页的「控制下发」操作列只负责跳转。
-->
<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <button type="button" class="iot-back" @click="goBack">
            <el-icon><ArrowLeft /></el-icon>
            {{ t('thingModel.propertyControlBack') }}
          </button>
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.industrial') }}
          </p>
        </div>
        <div class="iot-title-row">
          <h1 class="iot-title">{{ headerTitle }}</h1>
          <span class="iot-head-sub">{{ t('thingModel.propertyControlSubtitle') }}</span>
        </div>
      </div>
      <div class="iot-head-aside">
        <span v-if="property?.identifier" class="iot-mono property-control-code" :title="property.identifier">
          {{ property.identifier }}
        </span>
        <el-tag v-if="property?.unit" size="small" effect="plain">{{ property.unit }}</el-tag>
        <el-tag v-if="property" size="small" effect="plain" type="info">
          {{ accessLabel(property.accessMode) }}
        </el-tag>
        <div class="iot-stat">
          <span class="iot-stat-num">{{ rawRows.length }}</span>
          <span class="iot-stat-label">{{ t('realtime.pointUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 属性本身不具备写能力时先把原因说清楚，避免用户逐行看到一排禁用的按钮 -->
    <div v-if="property && !propertyAllowsWrite" class="iot-notice is-warning iot-enter">
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><AlertTriangle /></el-icon>
      </span>
      <p class="iot-notice-text">{{ t('thingModel.propertyControlReadOnlyNotice') }}</p>
    </div>
    <div
      v-else-if="property && property.accessMode === 'W'"
      class="iot-notice is-info iot-enter"
    >
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><InfoCircle /></el-icon>
      </span>
      <p class="iot-notice-text">{{ t('thingModel.propertyControlWriteOnlyNotice') }}</p>
    </div>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="keywordInput"
            class="iot-search"
            :placeholder="t('thingModel.propertyControlSearchPlaceholder')"
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
          <el-button :icon="Refresh" @click="refreshNow">
            {{ t('realtime.refreshNow') }}
          </el-button>
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="pagedRows"
        v-loading="loadingPoints || loading"
        row-key="addressId"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column :label="t('thingModel.pointDevice')" min-width="190">
          <template #default="{ row }">
            <div class="property-control-device">
              <span class="property-control-device-name" :title="row.deviceName">
                {{ row.deviceName }}
              </span>
              <span class="property-control-device-sub">
                <span class="iot-mono" :title="row.deviceCode">{{ row.deviceCode }}</span>
                <span class="iot-pill" :class="deviceOnlineClass(row)">
                  {{ deviceOnlineText(row) }}
                </span>
              </span>
            </div>
          </template>
        </el-table-column>

        <el-table-column :label="t('deviceAddress.tag')" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">{{ row.label || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.addressName')" min-width="150">
          <template #default="{ row }">
            <span class="iot-mono" :title="row.name">{{ row.name }}</span>
          </template>
        </el-table-column>

        <el-table-column :label="t('realtime.value')" min-width="150" align="right">
          <template #default="{ row }">
            <span v-if="row.collected" class="iot-value-clip">{{ row.value }}</span>
            <el-tooltip
              v-else-if="isReadSuppressed(row)"
              :content="t('deviceAddress.writeonlyHint')"
              placement="top"
            >
              <span class="iot-cell-muted iot-hint" tabindex="0">{{ t('realtime.writeonlyNoValue') }}</span>
            </el-tooltip>
            <span v-else class="iot-cell-muted">{{ t('realtime.emptyValue') }}</span>
          </template>
        </el-table-column>

        <el-table-column :label="t('realtime.quality')" width="110" align="center">
          <template #default="{ row }">
            <span v-if="!row.collected" class="iot-cell-muted">—</span>
            <span v-else class="iot-pill" :class="qualityClass(row)">
              {{ qualityText(row) }}
            </span>
          </template>
        </el-table-column>

        <el-table-column :label="t('realtime.collectedAt')" width="160" class-name="iot-col-time">
          <template #default="{ row }">{{ row.updatedAt || '—' }}</template>
        </el-table-column>

        <el-table-column :label="t('thingModel.access.label')" width="96" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.rwPermission === 'R' ? 'info' : 'warning'">
              {{ accessLabel(row.rwPermission) }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column :label="t('common.action')" width="88" align="center" fixed="right">
          <template #default="{ row }">
            <el-tooltip
              :content="canWrite(row) ? t('control.writeTitle') : writeDisabledReason(row)"
              placement="top"
            >
              <span>
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Send"
                  :disabled="!canWrite(row)"
                  :aria-label="t('control.write')"
                  @click="openWrite(row)"
                />
              </span>
            </el-tooltip>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><ListDetails /></el-icon>
            </span>
            <template v-if="loadingPoints || loading">
              <p class="iot-empty-title">{{ t('common.loading') }}</p>
            </template>
            <template v-else-if="notFound">
              <p class="iot-empty-title">{{ t('thingModel.propertyControlNotFound') }}</p>
              <el-button @click="goBack">{{ t('thingModel.propertyControlBack') }}</el-button>
            </template>
            <template v-else-if="loadError">
              <p class="iot-empty-title">{{ t('thingModel.propertyControlLoadFailed') }}</p>
              <el-button @click="loadAll(true)">{{ t('common.refresh') }}</el-button>
            </template>
            <template v-else-if="rawRows.length === 0">
              <p class="iot-empty-title">{{ t('thingModel.propertyControlEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.propertyControlEmptyHint') }}</p>
              <el-button @click="goBack">{{ t('thingModel.propertyControlBack') }}</el-button>
            </template>
            <template v-else>
              <p class="iot-empty-title">
                {{ t('thingModel.propertyControlEmptySearch', { keyword: appliedKeyword.trim() }) }}
              </p>
              <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
            </template>
          </div>
        </template>
      </el-table>

      <div v-if="total > 0" class="iot-pager">
        <el-pagination
          v-model:current-page="page"
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

    <ControlWriteDialog
      v-model="writeDialogVisible"
      :device-id="writeTarget?.deviceId || ''"
      :target="writeTarget"
      @written="handleWritten"
    />
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  IconAlertTriangle as AlertTriangle,
  IconArrowLeft as ArrowLeft,
  IconInfoCircle as InfoCircle,
  IconListDetails as ListDetails,
  IconRefresh as Refresh,
  IconSearch as Search,
  IconSend as Send,
} from '@tabler/icons-vue'
import { getRealtimeSnapshot } from '@/api/modules/realtime'
import { getControlCapability } from '@/api/modules/control'
import {
  listAllThingModelProperties,
  listThingModelPropertyPoints,
} from '@/api/modules/thingModel'
import ControlWriteDialog from '@/components/ControlWriteDialog.vue'
import { qualityBadText } from '@/utils/pointQuality'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const propertyId = route.params.propertyId

// ---------- 属性与绑定 ----------
const property = ref(null)
const rawRows = ref([])
const notFound = ref(false)
const loadError = ref(false)
const loadingPoints = ref(false)

const headerTitle = computed(() =>
  property.value
    ? `${t('thingModel.propertyControlTitle')} — ${property.value.name}`
    : t('thingModel.propertyControlTitle'),
)

const accessLabel = (mode) => (mode ? t(`thingModel.access.${mode}`) : '—')

function goBack() {
  if (property.value?.modelId) {
    router.push({ name: 'ThingModelProperties', params: { id: property.value.modelId } })
    return
  }
  router.push({ name: 'ThingModel' })
}

// 属性元信息只有「按 id 找」这一条路：列表页的属性和这里读的是同一份数据，
// 直接用跨模型的属性下拉接口回查，刷新页面也能自己恢复。
async function loadProperty() {
  // 先确认属性是否存在：属性被删后点位接口会直接报错，提前返回可让空态
  // 说「不存在」而不是弹一个接口错误。
  const props = await listAllThingModelProperties()
  property.value = (props || []).find((p) => p.id === propertyId) || null
  notFound.value = !property.value
  if (!property.value) {
    rawRows.value = []
    return
  }
  const points = await listThingModelPropertyPoints(propertyId)
  rawRows.value = (points || []).map((p) => ({
    ...p,
    value: '',
    quality: 0,
    collected: false,
    stale: false,
    updatedAt: '',
    transformJson: '',
    deviceOnline: null,
  }))
}

async function loadAll(showLoading = true) {
  loadingPoints.value = true
  loadError.value = false
  try {
    await loadProperty()
  } catch {
    loadError.value = true
    rawRows.value = []
  } finally {
    loadingPoints.value = false
  }
  resetCapabilities()
  await fetchSnapshots(showLoading)
}

// ---------- 搜索 / 分页 ----------
const keywordInput = ref('')
const appliedKeyword = ref('')
const page = ref(1)
const pageSize = ref(20)

const filteredRows = computed(() => {
  const kw = appliedKeyword.value.trim().toLowerCase()
  if (!kw) return rawRows.value
  return rawRows.value.filter((r) =>
    [r.deviceName, r.deviceCode, r.name, r.label].some((v) =>
      String(v || '').toLowerCase().includes(kw),
    ),
  )
})

const total = computed(() => filteredRows.value.length)

const pagedRows = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return filteredRows.value.slice(start, start + pageSize.value)
})

function handleSearch() {
  appliedKeyword.value = keywordInput.value.trim()
  page.value = 1
  fetchSnapshots(true)
}

function handleReset() {
  keywordInput.value = ''
  appliedKeyword.value = ''
  page.value = 1
  fetchSnapshots(true)
}

function handleSizeChange() {
  page.value = 1
  fetchSnapshots(true)
}

function handlePageChange() {
  fetchSnapshots(true)
}

// ---------- 控制能力（按设备缓存） ----------
// 能力查询是 device-scoped 的，没有批量接口。只对当前页的设备查询一次并缓存；
// 手动「立即刷新」时清空，让总开关/协议状态的变化能重新读出来。
const capabilities = ref({})

function resetCapabilities() {
  capabilities.value = {}
}

async function ensureCapability(deviceId) {
  if (capabilities.value[deviceId]) return
  try {
    capabilities.value[deviceId] = await getControlCapability(deviceId)
  } catch {
    // 设备被删/网关未就绪时保守禁用，与实时页同一策略
    capabilities.value[deviceId] = { enabled: false, writable: false, reason: 'unavailable' }
  }
}

const propertyAllowsWrite = computed(() => String(property.value?.accessMode || '').includes('W'))

function capabilityOf(row) {
  return capabilities.value[row.deviceId] || null
}

function canWrite(row) {
  if (!propertyAllowsWrite.value) return false
  if (row.status !== 1) return false
  if (!String(row.rwPermission || '').includes('W')) return false
  return capabilityOf(row)?.writable === true
}

function writeDisabledReason(row) {
  if (canWrite(row)) return ''
  if (!propertyAllowsWrite.value) return t('control.propertyNotWritable')
  const cap = capabilityOf(row)
  if (!cap) return t('control.checking')
  if (!cap.enabled) return t('control.disabled')
  if (!cap.writable) {
    return t(`control.reason.${cap.reason || 'protocol_not_writable'}`)
  }
  if (row.status !== 1) return t('control.pointDisabled')
  return t('control.pointNotWritable')
}

// ---------- 实时值 ----------
const loading = ref(false)
let inFlight = false

// 只取当前页的设备：属性可能绑到很多台设备，全量轮询会把网关的 SQLite
// 和浏览器连接数一起压满。翻页/筛选时再取新的这一页。
async function fetchSnapshots(showLoading = false) {
  if (inFlight) return
  inFlight = true
  if (showLoading) loading.value = true
  const rows = pagedRows.value.slice()
  const devices = [...new Set(rows.map((r) => r.deviceId))]
  try {
    await Promise.allSettled([
      ...rows.map((r) => fetchRow(r)),
      ...devices.map((id) => ensureCapability(id)),
    ])
  } finally {
    inFlight = false
    if (showLoading) loading.value = false
  }
}

async function fetchRow(row) {
  try {
    // addressIds 精确模式：只取这一个点位的实时值与元信息，不受设备点位数影响。
    // page/size 在 addressIds 模式下会被后端忽略，这里填合法值只为过参数校验。
    const res = await getRealtimeSnapshot({
      deviceObjectId: row.deviceId,
      page: 1,
      size: 1,
      addressIds: [row.addressId],
    })
    row.deviceOnline = res?.online ?? null
    const rec = (res?.records || []).find((x) => x.addressId === row.addressId)
    if (!rec) {
      row.value = ''
      row.quality = 0
      row.collected = false
      row.stale = false
      row.updatedAt = ''
      return
    }
    row.value = rec.value ?? ''
    row.quality = rec.quality ?? 0
    row.collected = !!rec.collected
    row.stale = !!rec.stale
    row.updatedAt = rec.updatedAt || ''
    row.transformJson = rec.transformJson || ''
    row.dataType = rec.dataType || row.dataType
    row.commonDataType = rec.commonDataType || row.commonDataType
  } catch {
    // 单台设备查询失败不影响其它行；设备在线状态标为未知
    row.deviceOnline = null
  }
}

// ---------- 轮询 ----------
const paused = ref(false)
const interval = ref(5000)
let timer = null

const intervalOptions = computed(() => [
  { value: 2000, label: t('common.seconds', { n: 2 }) },
  { value: 5000, label: t('common.seconds', { n: 5 }) },
  { value: 10000, label: t('common.seconds', { n: 10 }) },
])

function clearTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

function restartTimer() {
  clearTimer()
  if (paused.value || document.hidden) return
  timer = setInterval(() => fetchSnapshots(false), interval.value)
}

watch(interval, restartTimer)

function togglePause() {
  paused.value = !paused.value
  restartTimer()
  if (!paused.value) fetchSnapshots(false)
}

function refreshNow() {
  // 清掉能力缓存，让总开关/协议变更立即反映到按钮上
  resetCapabilities()
  fetchSnapshots(true)
}

function handleVisibilityChange() {
  if (document.hidden) {
    clearTimer()
  } else {
    restartTimer()
    if (!paused.value) fetchSnapshots(false)
  }
}

// ---------- 控制下发 ----------
const writeDialogVisible = ref(false)
const writeTarget = ref(null)

function openWrite(row) {
  writeTarget.value = row
  writeDialogVisible.value = true
}

async function handleWritten() {
  if (writeTarget.value) await fetchRow(writeTarget.value)
}

// ---------- 展示 ----------
// 有效读能力 = 点位含 R 且属性含 R；任一不满足即「永不采集」。
function isReadSuppressed(row) {
  if (!String(row?.rwPermission || '').includes('R')) return true
  const mode = String(property.value?.accessMode || '')
  return mode !== '' && !mode.includes('R')
}

function deviceOnlineClass(row) {
  if (row.deviceOnline === true) return 'is-green'
  if (row.deviceOnline === false) return 'is-rose'
  return 'is-gray'
}

function deviceOnlineText(row) {
  if (row.deviceOnline === true) return t('realtime.onlineYes')
  if (row.deviceOnline === false) return t('realtime.onlineNo')
  return '—'
}

function qualityClass(row) {
  if (row.quality === 192) return 'is-green'
  if (row.quality <= 3) return 'is-rose'
  return 'is-gray'
}

function qualityText(row) {
  if (row.quality === 192) return t('realtime.qualityGood')
  if (row.quality <= 3) return qualityBadText(t, row.quality)
  return row.quality
}

// ---------- 生命周期 ----------
onMounted(async () => {
  await loadAll(true)
  restartTimer()
  document.addEventListener('visibilitychange', handleVisibilityChange)
})

onUnmounted(() => {
  clearTimer()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
.property-control-code {
  max-width: 220px;
  overflow: hidden;
  font-size: 12.5px;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 设备单元格：名称在上，编码与在线状态并排在下 */
.property-control-device {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.property-control-device-name {
  overflow: hidden;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.property-control-device-sub {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 11px;
  color: var(--ink-4);
}

.property-control-device-sub .iot-mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.refresh-label {
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}

.refresh-interval {
  width: 118px;
}
</style>
