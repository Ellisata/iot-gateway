<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <button type="button" class="iot-back" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          {{ t('deviceAddress.backToList') }}
        </button>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.deviceAddress') }}
        </p>
        <h1 class="iot-title">{{ t('deviceAddress.title') }} — {{ deviceObjectName }}</h1>
        <p class="iot-sub">{{ t('deviceAddress.subtitle') }}</p>
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
      :device-object-id="deviceObjectId"
      :device-object-name="deviceObjectName"
      :protocol-name="protocolName"
    />

    <!-- 工作区：筛选、批量动作、表格、分页共用一块表面 -->
    <section ref="panelRef" class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('deviceAddress.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <!-- 批量删除放在这一组的最左边，且未勾选时置灰不移除：它是**破坏性**动作，
               和右边的下载/导入/新增不是一类；常驻在固定位置能让人形成固定预期，
               按勾选数变长的文案则让「删几条」在按下确认之前就可见。 -->
          <el-button
            type="danger"
            plain
            :icon="Delete"
            :disabled="selectedRows.length === 0"
            @click="handleBatchDelete"
          >
            {{ batchDeleteLabel }}
          </el-button>
          <span class="iot-sep" aria-hidden="true" />
          <el-button :icon="Download" @click="handleDownloadTemplate">{{ t('common.downloadTemplate') }}</el-button>
          <el-button :icon="Upload" :loading="importing" @click="handleImport">{{ t('common.import') }}</el-button>
          <!-- 「自动发现点位」。
               协议不支持时**置灰 + 提示，不隐藏** —— 隐藏会让人以为这个功能没做，
               而置灰加一句理由能让人知道"换条路"或"换个协议插件"。
               能力是异步拿到的，未就绪时先按不可用渲染（避免点开才发现不行）。 -->
          <el-tooltip
            :disabled="canDiscoverPoints"
            placement="top"
            :content="pointScanDisabledReason"
          >
            <span>
              <el-button
                :icon="Search"
                :disabled="!canDiscoverPoints"
                @click="pointDialogVisible = true"
              >
                {{ t('deviceAddress.pointDiscovery.entry') }}
              </el-button>
            </span>
          </el-tooltip>
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('deviceAddress.add') }}</el-button>
          <input ref="fileInputRef" type="file" accept=".xlsx" class="hidden" @change="handleFileChange" />
        </div>
      </div>

      <el-table
        ref="tableRef"
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        row-key="id"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <!-- 项目里没用 reserve-selection：跨页保留勾选会让「已勾选 N 条」与实际
             能删到的集合不一致（翻页后看不见的行还在里面），批量删除宁可让人重勾。 -->
        <el-table-column type="selection" width="44" align="center" />
        <!-- 窄屏藏起序号与描述：十来列挤进 1000px 会把创建时间推到固定操作列底下，
             反而让人以为数据丢了。两者都在宽屏回归。 -->
        <el-table-column v-if="!compact" type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column label="ID" width="150">
          <template #default="{ row }">
            <div class="iot-id-cell">
              <el-tooltip :content="String(row.id)" placement="top">
                <span class="iot-id-text">{{ row.id }}</span>
              </el-tooltip>
              <el-tooltip :content="t('common.copy')" placement="top">
                <button
                  type="button"
                  class="iot-copy-btn"
                  :aria-label="t('common.copy')"
                  @click="copyId(row.id)"
                >
                  <el-icon><CopyDocument /></el-icon>
                </button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <!-- 地址是这一行的身份：小图标底座让「一个点位」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('deviceAddress.addressName')" min-width="200">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><MapPin /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="label"
          :label="t('deviceAddress.tag')"
          min-width="150"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column
          v-if="!compact"
          :label="t('deviceAddress.property')"
          width="180"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span v-if="row.propertyIdentifier">
              <span class="iot-mono">{{ row.propertyIdentifier }}</span>
              <span class="iot-cell-muted"> · {{ row.propertyName }}</span>
            </span>
            <span v-else class="iot-cell-muted">{{ t('deviceAddress.unboundProperty') }}</span>
          </template>
        </el-table-column>
        <!-- 实时值：配置页顺带看一眼「这个地址现在读到什么」。
             只对启用的点位有意义；未启用/未采集都显示占位，不把空值伪装成 0。 -->
        <el-table-column :label="t('deviceAddress.liveValue')" width="176">
          <template #header>
            <div class="live-head">
              <span>{{ t('deviceAddress.liveValue') }}</span>
              <el-tooltip
                :content="livePaused ? t('deviceAddress.liveResume') : t('deviceAddress.livePause')"
                placement="top"
              >
                <button
                  type="button"
                  class="live-toggle"
                  :aria-label="livePaused ? t('deviceAddress.liveResume') : t('deviceAddress.livePause')"
                  :aria-pressed="livePaused"
                  @click="toggleLive"
                >
                  <el-icon>
                    <PlayIcon v-if="livePaused" />
                    <PauseIcon v-else />
                  </el-icon>
                </button>
              </el-tooltip>
            </div>
          </template>
          <template #default="{ row }">
            <span v-if="row.status !== 1" class="iot-cell-muted">—</span>
            <!-- 只写点位不参与采集：别显示成「等待采集」—— 那是 R/RW 还没轮询到的状态 -->
            <div v-else-if="row.rwPermission === 'W'" class="live-pending">
              <el-tooltip :content="t('deviceAddress.writeonlyHint')" placement="top">
                <span class="iot-cell-muted iot-hint" tabindex="0">
                  {{ t('deviceAddress.writeonlyNoValue') }}
                </span>
              </el-tooltip>
            </div>
            <div v-else-if="!liveOf(row).collected" class="live-pending">
              <!-- 暂停时没有缓存值就如实说「已暂停」，别让「等待采集」暗示还在轮询 -->
              <span class="iot-pill" :class="livePaused ? 'is-amber' : 'is-gray'">
                {{ livePaused ? t('deviceAddress.livePaused') : t('deviceAddress.liveWaiting') }}
              </span>
            </div>
            <div v-else class="live-cell">
              <span :key="`${row.id}|${liveOf(row).value}`" class="iot-value-flash live-value">
                {{ liveOf(row).value === '' ? '—' : liveOf(row).value }}
              </span>
              <span class="iot-pill" :class="livePillClass(liveOf(row))">
                {{ livePillText(liveOf(row)) }}
              </span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.dataType')" width="130" align="center">
          <template #default="{ row }">
            <span v-if="row.dataType" class="iot-chip">{{ row.dataType }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.commonDataType')" width="140" align="center">
          <template #default="{ row }">
            <span v-if="row.commonDataType" class="iot-chip">{{ row.commonDataType }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.rwPermission')" width="110" align="center">
          <template #default="{ row }">
            <!-- 只写点位不参与采集，值永远为空：胶囊加虚线下划线，悬停说明这是预期而非故障 -->
            <el-tooltip
              :disabled="row.rwPermission !== 'W'"
              :content="t('deviceAddress.writeonlyHint')"
              placement="top"
            >
              <span
                class="iot-pill"
                :class="[rwClass(row.rwPermission), row.rwPermission === 'W' ? 'iot-hint' : '']"
                :tabindex="row.rwPermission === 'W' ? 0 : undefined"
              >
                {{ rwLabel(row.rwPermission) }}
              </span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('deviceAddress.scanFrequency')"
          width="116"
          align="center"
          class-name="iot-col-num"
        >
          <template #default="{ row }">
            {{ row.scanFrequency }} ms
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.thresholdAlarm')" width="180" align="center">
          <template #default="{ row }">
            <span v-if="row.thresholdEnabled !== 1" class="iot-pill is-gray">
              {{ t('deviceAddress.thresholdOff') }}
            </span>
            <span v-else class="iot-cond">{{ conditionText(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column
          v-if="!compact"
          prop="description"
          :label="t('common.description')"
          min-width="160"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('common.status')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-rose'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="t('common.createdAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="100" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('common.edit')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="EditPen"
                  :aria-label="t('common.edit')"
                  @click="handleEdit(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('common.delete')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="danger"
                  link
                  size="small"
                  :icon="Delete"
                  :aria-label="t('common.delete')"
                  @click="handleDelete(row)"
                />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><MapPin /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ appliedName
                ? t('deviceAddress.emptySearch', { name: appliedName })
                : t('deviceAddress.emptyTitle') }}
            </p>
            <p v-if="!appliedName" class="iot-empty-hint">{{ t('deviceAddress.emptyHint') }}</p>
            <el-button v-if="appliedName" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('deviceAddress.add') }}
            </el-button>
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

    <!-- ========== 新增 / 编辑 对话框 ========== -->
    <AddressFormDialog
      v-model="formDialogVisible"
      :device-id="deviceObjectId"
      :protocol-name="protocolName"
      :address-id="editingAddressId"
      @saved="handleDialogSaved"
    />

    <!-- ========== Excel 导入结果对话框 ========== -->
    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />

    <!-- ========== 点位范围扫描（自动发现点位） ========== -->
    <PointDiscoveryDialog
      v-model:visible="pointDialogVisible"
      :device-id="deviceObjectId"
      :protocol-name="protocolName"
      @created="fetchList"
    />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconArrowLeft as ArrowLeft,
  IconCopy as CopyDocument,
  IconDownload as Download,
  IconMapPin as MapPin,
  IconPencil as EditPen,
  IconPlayerPause as PauseIcon,
  IconPlayerPlay as PlayIcon,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  getDeviceAddressList,
  deleteDeviceAddress,
  batchDeleteDeviceAddress,
  importDeviceAddresses,
  downloadDeviceAddressTemplate,
} from '@/api/modules/deviceAddress'
import { getPointCapability } from '@/api/modules/deviceScan'
import DeviceWorkspaceTabs from '@/components/DeviceWorkspaceTabs.vue'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import AddressFormDialog from './AddressFormDialog.vue'
import PointDiscoveryDialog from './PointDiscoveryDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'
import { useRealtimeValues } from '@/composables/useRealtimeValues'
import { parseConditionValues } from '@/utils/addressThreshold'
import { qualityBadText } from '@/utils/pointQuality'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()
const deviceObjectId = route.params.deviceObjectId
const deviceObjectName = route.query.deviceObjectName || ''

// ---------- 协议 ----------
// 协议名由设备对象页带过来：地址写法示例与「自动发现点位」能力都按它取。
// 新增/编辑表单已抽到 AddressFormDialog，那边同样通过 prop 拿到它。
const protocolName = computed(() => route.query.protocolName || '')

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

// ---------- 自动发现点位 ----------
// 按钮的可用性由**服务端**现算的能力决定，不在前端按协议名硬编码一张表：
// 能力声明在插件里（Meta.Protocols[].PointDiscovery），热升级随时可能改变。
const pointDialogVisible = ref(false)
const pointCapability = ref(null)

const canDiscoverPoints = computed(
  () => !!pointCapability.value?.enabled && !!pointCapability.value?.supported,
)
const pointScanDisabledReason = computed(() => {
  if (!pointCapability.value) return t('deviceAddress.pointDiscovery.checking')
  if (!pointCapability.value.enabled) return t('deviceAddress.pointDiscovery.disabled')
  return t('deviceAddress.pointDiscovery.unsupported')
})

async function fetchPointCapability() {
  // 协议名取不到时（协议行缺失）能力必然是 false，不必问服务端。
  if (!protocolName.value) {
    pointCapability.value = { enabled: true, supported: false }
    return
  }
  try {
    pointCapability.value = await getPointCapability(protocolName.value)
  } catch {
    // 能力查询失败按"不可用"渲染：按钮点开也只会得到一个错误，
    // 不如让用户看到一句"暂不可用"。
    pointCapability.value = { enabled: true, supported: false }
  }
}

// ---------- 复制 ID ----------
async function copyId(id) {
  try {
    await navigator.clipboard.writeText(String(id))
    ElMessage.success(t('deviceAddress.idCopied'))
  } catch {
    ElMessage.error(t('deviceAddress.copyFailed'))
  }
}

// ---------- 查询表单 ----------
const PAGE_SIZES = [10, 20, 50, 100]
const initialPage = Number(route.query.page)
const initialSize = Number(route.query.size)
const queryForm = reactive({
  name: String(route.query.name || ''),
})
// 已生效的搜索词（输入框里敲了但没点查询的不算）：翻页、空态文案与页签切换都沿用它
const appliedName = ref(String(route.query.name || ''))

// ---------- 分页 ----------
const currentPage = ref(initialPage > 0 ? initialPage : 1)
const pageSize = ref(PAGE_SIZES.includes(initialSize) ? initialSize : 10)
const total = ref(0)

// 把已生效的筛选与分页写回 URL。页签切换（DeviceWorkspaceTabs）会带上当前 query，
// 于是「在实时页筛出某个点位 → 切去改配置 → 切回来」仍是同一屏，刷新/分享链接也一致。
// 默认值不写进 query，避免地址里堆一串 ?page=1&size=10。
function syncQuery() {
  const query = { ...route.query }
  if (appliedName.value) query.name = appliedName.value
  else delete query.name
  if (currentPage.value > 1) query.page = String(currentPage.value)
  else delete query.page
  if (pageSize.value !== 10) query.size = String(pageSize.value)
  else delete query.size
  router.replace({ query })
}

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// 首屏还没拿到数时显示占位符而不是 0：「0 个点位」是一个会被信以为真的假事实
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

// ---------- 实时值（点位表内嵌） ----------
// 只看当前页：配置页要的是「这几行现在读到什么」，不是整台设备的实时大盘。
// 轮询节奏、暂停、标签页隐藏时停跑的判据都在 composable 里，与实时监控页同源。
// 只把启用点位送去取值：停用行本来就显示占位，没必要让它们占 IN 子句
const pageAddressIds = computed(() =>
  tableData.value.filter((row) => row.status === 1).map((row) => row.id),
)
const {
  values: liveValues,
  paused: livePaused,
  togglePause: toggleLive,
} = useRealtimeValues(deviceObjectId, pageAddressIds)

function liveOf(row) {
  return liveValues.value[row.id] || {}
}

// 质量优先于新鲜度：地址错误/通讯失败比「值过期」更能解释为什么没有数
function livePillClass(live) {
  if (live.quality <= 3) return 'is-rose'
  if (live.stale) return 'is-amber'
  if (live.quality === 192) return 'is-green'
  return 'is-gray'
}

function livePillText(live) {
  if (live.quality <= 3) return qualityBadText(t, live.quality)
  if (live.stale) return t('deviceAddress.liveStale')
  if (live.quality === 192) return t('deviceAddress.liveNormal')
  return String(live.quality)
}

// ---------- 窄屏列裁剪 ----------
// 判据取**工作区的真实宽度**而不是窗口宽度：侧栏折叠后同一窗口会多出 176px，
// 按 window.innerWidth 判断会在折叠后仍少显示两列。
const panelRef = ref(null)
const compact = ref(false)
let panelObserver = null

// ---------- 勾选（批量删除） ----------
const tableRef = ref(null)
const selectedRows = ref([])

function handleSelectionChange(rows) {
  selectedRows.value = rows
}

// 按钮文案带上勾选数：按确认之前就能看到"要删几条"
const batchDeleteLabel = computed(() =>
  selectedRows.value.length
    ? t('deviceAddress.batchDeleteCount', { n: selectedRows.value.length })
    : t('deviceAddress.batchDelete'),
)

// ---------- 获取列表 ----------
async function fetchList() {
  loading.value = true
  try {
    const params = {
      deviceObjectId,
      page: currentPage.value,
      size: pageSize.value,
    }
    if (appliedName.value) params.name = appliedName.value
    const res = await getDeviceAddressList(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
  } finally {
    loading.value = false
  }
}

// ---------- 搜索 / 重置 ----------
function handleSearch() {
  appliedName.value = queryForm.name.trim()
  currentPage.value = 1
  syncQuery()
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  appliedName.value = ''
  currentPage.value = 1
  syncQuery()
  fetchList()
}

function handlePageChange() {
  syncQuery()
  fetchList()
}

function handleSizeChange() {
  currentPage.value = 1
  syncQuery()
  fetchList()
}

// ---------- Excel 导入 / 模板下载 ----------
const {
  fileInputRef,
  importing,
  importResultVisible,
  importResult,
  handleImport,
  handleFileChange,
  handleDownloadTemplate,
} = useExcelImport({
  importFn: (file) => importDeviceAddresses(file, deviceObjectId),
  downloadFn: downloadDeviceAddressTemplate,
  templateName: () => t('deviceAddress.importTemplateName'),
  refresh: fetchList,
})

// ---------- 新增 / 编辑对话框 ----------
// 空串 = 新增；非空 = 编辑该点位。表单本身在 AddressFormDialog 里，
// 与实时监控页的就地编辑共用同一份校验与提交逻辑。
const formDialogVisible = ref(false)
const editingAddressId = ref('')

function handleAdd() {
  editingAddressId.value = ''
  formDialogVisible.value = true
}

function handleEdit(row) {
  editingAddressId.value = row.id
  formDialogVisible.value = true
}

// 保存后刷新列表：新值/新类型要立刻反映在当前表格里
function handleDialogSaved() {
  fetchList()
}

// ---------- 删除 ----------
async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('deviceAddress.deleteConfirm', { name: row.name }), t('deviceAddress.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
    await deleteDeviceAddress(row.id)
    ElMessage.success(t('deviceAddress.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

// ---------- 批量删除 ----------
async function handleBatchDelete() {
  const rows = selectedRows.value
  if (!rows.length) return

  try {
    await ElMessageBox.confirm(
      t('deviceAddress.batchDeleteConfirm', { n: rows.length }),
      t('deviceAddress.batchDeleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return // 用户取消
  }

  try {
    const res = await batchDeleteDeviceAddress({
      deviceId: deviceObjectId,
      ids: rows.map((row) => row.id),
    })
    // deleted 由服务端按**实际删除行数**给出，可能小于勾选数：勾选后又被人删掉的
    // 地址不在删除集里。少删了要说出来，不能一律报"成功 N 条"。
    const deleted = res?.deleted ?? 0
    const missing = rows.length - deleted
    if (missing > 0) {
      ElMessage.warning(t('deviceAddress.batchDeletePartial', { n: deleted, missing }))
    } else {
      ElMessage.success(t('deviceAddress.batchDeleteSuccess', { n: deleted }))
    }
    tableRef.value?.clearSelection()
    fetchList()
  } catch {
    // 失败原因由响应拦截器统一提示
  }
}

// 表格里展示条件摘要（如 `∈ [10, 40]`、`= FAULT`）。
function conditionText(row) {
  const op = row.thresholdOp
  const values = parseConditionValues(row.thresholdValue)
  switch (op) {
    case 'range':
      return t('deviceAddress.condRange', { low: values[0] || '−∞', high: values[1] || '+∞' })
    case 'gt':
      return `> ${values[0] ?? ''}`
    case 'ge':
      return `≥ ${values[0] ?? ''}`
    case 'lt':
      return `< ${values[0] ?? ''}`
    case 'le':
      return `≤ ${values[0] ?? ''}`
    case 'eq':
      return `= ${values[0] === '1' || values[0] === '0' ? boolLabel(values[0]) : values[0] ?? ''}`
    case 'ne':
      return `≠ ${values[0] ?? ''}`
    case 'in':
      return `∈ {${values.join(', ')}}`
  }
  return ''
}

function boolLabel(v) {
  return v === '1' ? t('deviceAddress.boolTrue') : t('deviceAddress.boolFalse')
}

// 读写权限：胶囊只区分三种语义，不把 R/W/RW 直接当颜色码
function rwClass(permission) {
  if (permission === 'RW') return 'is-green'
  if (permission === 'R') return 'is-blue'
  return 'is-amber'
}

function rwLabel(permission) {
  return {
    R: t('deviceAddress.readonly'),
    W: t('deviceAddress.writeonly'),
    RW: t('deviceAddress.readwrite'),
  }[permission] || permission || '—'
}

// ---------- 初始化 ----------
onMounted(() => {
  // 十来列的固定宽度合计远超 1400px，低于 1550 就切到紧凑列 —— 差几个像素时表格
  // 会横向滚动，固定操作列又会盖住创建时间，不如少两列让关键字段完整可见
  if (typeof ResizeObserver !== 'undefined' && panelRef.value) {
    panelObserver = new ResizeObserver(([entry]) => {
      compact.value = entry.contentRect.width < 1550
    })
    panelObserver.observe(panelRef.value)
  }
  fetchList()
  fetchPointCapability()
})

onBeforeUnmount(() => {
  panelObserver?.disconnect()
})
</script>

<style scoped>
/* 报警条件摘要：与地址/类型同属技术标识，用弱化的等宽字体 */
.iot-cond {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

/* 实时值列：表头带一个暂停按钮，让「等一会看变化」和「别一直刷」都能就地做到 */
.live-head {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.live-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  color: var(--ink-4);
  background: none;
  border: 0;
  border-radius: 4px;
  cursor: pointer;
  transition: color 0.15s ease, background-color 0.15s ease;
}

.live-toggle:hover {
  color: var(--brand);
  background: var(--line-soft);
}

.live-toggle:focus-visible {
  outline: 2px solid var(--el-color-primary-light-5);
  outline-offset: 1px;
}

.live-pending {
  display: inline-flex;
}

.live-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.live-value {
  font-family: var(--font-mono);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
</style>
