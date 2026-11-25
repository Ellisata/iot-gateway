<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <!-- 点位管理主体：工具栏 + 表格 + 分页 + 全部对话框。
       独立页（点位配置）与设备对象主从工作台共用这一份，差异只在 fill：
       fill=true 时表格填满父容器、自己纵向滚动；false 时沿用「随页面滚动」。 -->
  <div ref="panelRef" class="dpp" :class="{ 'is-fill': fill }">
    <div class="iot-toolbar dpp-toolbar">
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
        <el-button
          :icon="LayersLinked"
          :disabled="selectedRows.length === 0"
          @click="openAssignDialog"
        >
          {{ t('deviceAddress.batchAssignGroup') }}
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
      class="iot-table dpp-table"
      :data="tableData"
      :height="fill ? '100%' : undefined"
      v-loading="loading"
      row-key="id"
      style="width: 100%"
      @selection-change="handleSelectionChange"
    >
      <!-- 项目里没用 reserve-selection：跨页保留勾选会让「已勾选 N 条」与实际
           能删到的集合不一致（翻页后看不见的行还在里面），批量删除宁可让人重勾。 -->
      <el-table-column type="selection" width="44" align="center" />
      <!-- 窄屏藏起序号与描述：十来列挤进 1000px 会把创建时间推到固定操作列底下，
           反而让人以为数据丢了。两者都在宽屏回归。
           再窄一档（嵌在主从右栏时）继续藏次要列，只留「名称/采集组/实时值/读写/频率/状态」。 -->
      <el-table-column v-if="!compact" type="index" :label="t('common.index')" width="68" align="center" />

      <el-table-column v-if="!narrow" label="ID" width="150">
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
      <el-table-column :label="t('deviceAddress.addressName')" :min-width="narrow ? 180 : 200">
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
        v-if="!narrow"
        prop="label"
        :label="t('deviceAddress.tag')"
        min-width="150"
        show-overflow-tooltip
        class-name="iot-col-muted"
      />
      <el-table-column v-if="!ultra" :label="t('deviceAddress.collectorGroup')" width="170" show-overflow-tooltip>
        <template #default="{ row }">
          <span v-if="row.groupId && row.groupName">
            <span class="iot-mono">{{ row.groupName }}</span>
            <span class="iot-cell-muted"> · {{ row.groupIntervalMs }} ms</span>
            <el-tooltip v-if="row.groupStatus !== 1" :content="t('collectorGroup.disabledHint')" placement="top">
              <span class="iot-pill is-rose iot-hint" tabindex="0">
                {{ t('collectorGroup.disabledShort') }}
              </span>
            </el-tooltip>
          </span>
          <span v-else class="iot-cell-muted">{{ t('deviceAddress.ungrouped') }}</span>
        </template>
      </el-table-column>
      <!-- 物模型属性：identifier 只在模型内唯一，点位又可绑定任意模型的属性，
           所以带上模型名（与新增/编辑下拉的 `模型名 · identifier · 属性名` 同一口径）。
           不单开一列：两列是一对一关系，这个表格已经十几列宽。 -->
      <el-table-column
        v-if="!narrow"
        :label="t('deviceAddress.property')"
        width="220"
        show-overflow-tooltip
      >
        <template #default="{ row }">
          <span v-if="row.propertyIdentifier">
            <span v-if="row.propertyModelName" class="iot-cell-muted">{{ row.propertyModelName }} · </span>
            <span class="iot-mono">{{ row.propertyIdentifier }}</span>
            <span class="iot-cell-muted"> · {{ row.propertyName }}</span>
          </span>
          <span v-else class="iot-cell-muted">{{ t('deviceAddress.unboundProperty') }}</span>
        </template>
      </el-table-column>
      <!-- 工程量变换：采集侧对原始值执行的流水线。列表里只给可读摘要（如
           「缩放 ×0.1 → 四舍五入 2 位」），完整表达式悬停可见 —— 单元格窄，
           箭头链一截断就只剩半条规则，比不显示更容易误判。 -->
      <el-table-column v-if="!narrow" :label="t('deviceAddress.engineeringTransform')" width="200">
        <template #default="{ row }">
          <el-tooltip
            v-if="transformSummaries.get(row.id)"
            :content="transformSummaries.get(row.id)"
            placement="top"
          >
            <span class="iot-transform">{{ transformSummaries.get(row.id) }}</span>
          </el-tooltip>
          <span v-else class="iot-cell-muted">{{ t('deviceAddress.transformNone') }}</span>
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
          <!-- 只写点位 / 只写属性不参与采集：别显示成「等待采集」—— 那是可读点位还没轮询到的状态 -->
          <div v-else-if="isReadSuppressed(row)" class="live-pending">
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
            <!-- 长值单行省略、质量码不被挤走；完整值悬停可见 -->
            <el-tooltip
              placement="top"
              :disabled="String(liveOf(row).value ?? '').length <= 10"
            >
              <template #content>
                <div class="iot-value-tooltip-content">{{ liveOf(row).value }}</div>
              </template>
              <span
                :key="`${row.id}|${liveOf(row).value}`"
                class="iot-value-flash live-value iot-value-clip"
              >
                {{ liveOf(row).value === '' ? '—' : liveOf(row).value }}
              </span>
            </el-tooltip>
            <span class="iot-pill" :class="livePillClass(liveOf(row))">
              {{ livePillText(liveOf(row)) }}
            </span>
          </div>
        </template>
      </el-table-column>
      <el-table-column v-if="!narrow" :label="t('deviceAddress.commonDataType')" width="140" align="center">
        <template #default="{ row }">
          <span v-if="row.commonDataType" class="iot-chip">{{ row.commonDataType }}</span>
          <span v-else class="iot-cell-muted">—</span>
        </template>
      </el-table-column>
      <el-table-column v-if="!micro" :label="t('deviceAddress.rwPermission')" width="110" align="center">
        <template #default="{ row }">
          <!-- 只写点位 / 只写属性不参与采集，值永远为空：胶囊加虚线下划线，悬停说明这是预期而非故障 -->
          <el-tooltip
            :disabled="!isReadSuppressed(row)"
            :content="t('deviceAddress.writeonlyHint')"
            placement="top"
          >
            <span
              class="iot-pill"
              :class="[rwClass(row.rwPermission), isReadSuppressed(row) ? 'iot-hint' : '']"
              :tabindex="isReadSuppressed(row) ? 0 : undefined"
            >
              {{ rwLabel(row.rwPermission) }}
            </span>
          </el-tooltip>
        </template>
      </el-table-column>
      <!-- 采集周期：绑定组时与上一列重复，窄档先让位给实时值 -->
      <el-table-column
        v-if="!narrow"
        :label="t('deviceAddress.scanFrequency')"
        width="116"
        align="center"
        class-name="iot-col-num"
      >
        <template #default="{ row }">
          <!-- 绑定组后展示生效周期（组周期），扫描频率不再生效 -->
          {{ row.groupId ? row.groupIntervalMs : row.scanFrequency }} ms
        </template>
      </el-table-column>
      <el-table-column v-if="!narrow" :label="t('deviceAddress.thresholdAlarm')" width="180" align="center">
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
        v-if="!narrow"
        prop="createdAt"
        :label="t('common.createdAt')"
        width="160"
        class-name="iot-col-time"
      />
      <el-table-column :label="t('common.action')" width="100" align="center" fixed="right">
        <template #default="{ row }">
          <div class="iot-row-actions">
            <el-tooltip :content="t('deviceAddress.editAction')" placement="top">
              <el-button
                class="iot-row-btn"
                type="primary"
                link
                size="small"
                :icon="EditPen"
                :aria-label="t('deviceAddress.editAction')"
                @click="handleEdit(row)"
              />
            </el-tooltip>
            <el-tooltip :content="t('deviceAddress.deleteAction')" placement="top">
              <el-button
                class="iot-row-btn"
                type="danger"
                link
                size="small"
                :icon="Delete"
                :aria-label="t('deviceAddress.deleteAction')"
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

    <div class="iot-pager dpp-pager">
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

    <!-- ========== 新增 / 编辑 对话框 ========== -->
    <AddressFormDialog
      v-model="formDialogVisible"
      :device-id="deviceId"
      :device-name="deviceName"
      :protocol-name="protocolName"
      :address-id="editingAddressId"
      @saved="handleDialogSaved"
    />

    <!-- ========== 批量分配采集组 ========== -->
    <el-dialog
      v-model="assignDialogVisible"
      class="iot-dialog"
      :title="t('deviceAddress.batchAssignTitle')"
      width="480px"
      :close-on-click-modal="false"
    >
      <p class="iot-sub" style="margin-top: 0">
        {{ t('deviceAddress.batchAssignHint', { n: selectedRows.length }) }}
      </p>
      <el-select
        v-model="assignGroupId"
        :placeholder="t('deviceAddress.collectorGroupPlaceholder')"
        clearable
        :loading="groupLoading"
        style="width: 100%"
      >
        <el-option :label="t('deviceAddress.ungroup')" value="" />
        <el-option
          v-for="g in groupOptions"
          :key="g.id"
          :label="g.status === 1 ? g.name : `${g.name} (${t('collectorGroup.disabledShort')})`"
          :value="g.id"
        />
      </el-select>
      <p class="iot-form-hint is-block">{{ t('deviceAddress.batchAssignGroupHint') }}</p>
      <template #footer>
        <el-button @click="assignDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="assignSubmitting" @click="handleAssignSubmit">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- ========== Excel 导入结果对话框 ========== -->
    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />

    <!-- ========== 点位范围扫描（自动发现点位） ========== -->
    <PointDiscoveryDialog
      v-model:visible="pointDialogVisible"
      :device-id="deviceId"
      :protocol-name="protocolName"
      @created="handleDiscovered"
    />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconCopy as CopyDocument,
  IconDownload as Download,
  IconLayersLinked as LayersLinked,
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
import { listCollectorGroups, batchAssignCollectorGroup } from '@/api/modules/collectorGroup'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import AddressFormDialog from './AddressFormDialog.vue'
import PointDiscoveryDialog from './PointDiscoveryDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'
import { useRealtimeValues } from '@/composables/useRealtimeValues'
import { parseConditionValues } from '@/utils/addressThreshold'
import { addressTransformText } from '@/utils/addressTransform'
import { qualityBadText } from '@/utils/pointQuality'
import { useI18n } from 'vue-i18n'

defineOptions({ name: 'DevicePointsPanel' })

const props = defineProps({
  deviceId: { type: String, required: true },
  deviceName: { type: String, default: '' },
  protocolName: { type: String, default: '' },
  // 独立页从 URL 种下筛选与分页；嵌入主从工作台时不传，保持本地状态
  initialName: { type: String, default: '' },
  initialPage: { type: Number, default: 1 },
  initialSize: { type: Number, default: 10 },
  // true = 表格填满父容器并自己做纵向滚动（嵌入右栏）
  fill: { type: Boolean, default: false },
})

const emit = defineEmits(['total-change', 'loading-change', 'query-change', 'saved'])

const { t } = useI18n()

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
  if (!props.protocolName) {
    pointCapability.value = { enabled: true, supported: false }
    return
  }
  try {
    pointCapability.value = await getPointCapability(props.protocolName)
  } catch {
    // 能力查询失败按"不可用"渲染：按钮点开也只会得到一个错误，
    // 不如让用户看到一句"暂不可用"。
    pointCapability.value = { enabled: true, supported: false }
  }
}

// 设备协议被编辑后能力可能变化（右栏与设备行同屏），重新问一次
watch(() => props.protocolName, fetchPointCapability)

// ---------- 复制 ID ----------
async function copyId(id) {
  try {
    await navigator.clipboard.writeText(String(id))
    ElMessage.success(t('deviceAddress.idCopied'))
  } catch {
    ElMessage.error(t('deviceAddress.copyFailed'))
  }
}

// ---------- 查询表单 / 分页 ----------
const PAGE_SIZES = [10, 20, 50, 100]
const queryForm = reactive({
  name: props.initialName,
})
// 已生效的搜索词（输入框里敲了但没点查询的不算）：翻页、空态文案都沿用它
const appliedName = ref(props.initialName)

const currentPage = ref(props.initialPage > 0 ? props.initialPage : 1)
const pageSize = ref(PAGE_SIZES.includes(props.initialSize) ? props.initialSize : 10)
const total = ref(0)

// 把已生效的筛选与分页交给外层：独立页写回 URL，嵌入时忽略。
// 默认值不写进 query，避免地址里堆一串 ?page=1&size=10。
function notifyQueryChange() {
  emit('query-change', {
    name: appliedName.value,
    page: currentPage.value,
    size: pageSize.value,
  })
}

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// 工程量变换摘要按行算一次，避免模板里每行重复解析 JSON（完整表达式同时用于 tooltip）。
// 依赖 t：切换语言时摘要随 locale 一起重算。
const transformSummaries = computed(() => {
  const summaries = new Map()
  for (const row of tableData.value) {
    summaries.set(row.id, addressTransformText(t, row.transformJson))
  }
  return summaries
})

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
} = useRealtimeValues(props.deviceId, pageAddressIds)

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

// ---------- 宽度分档 ----------
// 判据取**工作区的真实宽度**而不是窗口宽度：侧栏折叠后同一窗口会多出 176px。
// narrow / ultra / micro 是嵌入主从右栏才触发的第二、三、四档，独立页（≤1400 版心）只会走到 compact。
// 主从右栏约 984 时走 narrow（藏 ID/标签/属性/变换/类型/阈值/时间/周期），
// 约 744 时再走 ultra（连采集组一起让位），单列窄屏约 620 时走 micro（读写权限让位给状态）。
// 逐档收列的目标是：任何宽度下都不出现永久横向滚动，状态与实时值始终可见。
const panelRef = ref(null)
const compact = ref(false)
const narrow = ref(false)
const ultra = ref(false)
const micro = ref(false)
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
  emit('loading-change', true)
  try {
    const params = {
      deviceObjectId: props.deviceId,
      page: currentPage.value,
      size: pageSize.value,
    }
    if (appliedName.value) params.name = appliedName.value
    const res = await getDeviceAddressList(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
    emit('total-change', total.value)
  } catch (err) {
    // 失败原因由响应拦截器统一提示；旧数据保留，避免表格瞬间变空
  } finally {
    loading.value = false
    emit('loading-change', false)
  }
}

// ---------- 搜索 / 重置 ----------
function handleSearch() {
  appliedName.value = queryForm.name.trim()
  currentPage.value = 1
  notifyQueryChange()
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  appliedName.value = ''
  currentPage.value = 1
  notifyQueryChange()
  fetchList()
}

function handlePageChange() {
  notifyQueryChange()
  fetchList()
}

function handleSizeChange() {
  currentPage.value = 1
  notifyQueryChange()
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
  importFn: (file) => importDeviceAddresses(file, props.deviceId),
  downloadFn: () => downloadDeviceAddressTemplate(props.deviceId),
  templateName: () => t('deviceAddress.importTemplateName'),
  refresh: () => {
    fetchList()
    emit('saved', { action: 'import' })
  },
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
  emit('saved', { action: 'save' })
}

function handleDiscovered() {
  fetchList()
  emit('saved', { action: 'discover' })
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
    emit('saved', { action: 'delete' })
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

// ---------- 批量分配采集组 ----------
const assignDialogVisible = ref(false)
const assignGroupId = ref('')
const assignSubmitting = ref(false)
const groupOptions = ref([])
const groupLoading = ref(false)

async function loadGroupOptions() {
  groupLoading.value = true
  try {
    groupOptions.value = (await listCollectorGroups(props.deviceId)) || []
  } catch {
    groupOptions.value = []
  } finally {
    groupLoading.value = false
  }
}

function openAssignDialog() {
  if (!selectedRows.value.length) return
  assignGroupId.value = ''
  assignDialogVisible.value = true
  loadGroupOptions()
}

async function handleAssignSubmit() {
  const rows = selectedRows.value
  if (!rows.length) return
  assignSubmitting.value = true
  try {
    const res = await batchAssignCollectorGroup({
      deviceId: props.deviceId,
      ids: rows.map((row) => row.id),
      groupId: assignGroupId.value || '',
    })
    const assigned = res?.assigned ?? 0
    const missing = rows.length - assigned
    if (missing > 0) {
      ElMessage.warning(t('deviceAddress.batchAssignPartial', { n: assigned, missing }))
    } else {
      ElMessage.success(t('deviceAddress.batchAssignSuccess', { n: assigned }))
    }
    assignDialogVisible.value = false
    tableRef.value?.clearSelection()
    fetchList()
    emit('saved', { action: 'assign' })
  } catch {
    // 失败原因由响应拦截器统一提示
  } finally {
    assignSubmitting.value = false
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
      deviceId: props.deviceId,
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
    emit('saved', { action: 'batchDelete' })
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

// 有效读能力 = 点位含 R 且绑定属性为空或含 R；任一不满足即「永不采集」。
// 「RW 点位 + W 属性」是合法绑定，但业务语义只写，实时值同样永远为空。
function isReadSuppressed(row) {
  if (!String(row?.rwPermission || '').includes('R')) return true
  const mode = String(row?.propertyAccessMode || '')
  return mode !== '' && !mode.includes('R')
}

// ---------- 初始化 ----------
onMounted(() => {
  // 十来列的固定宽度合计远超 1400px，低于 1550 就切到紧凑列；
  // 嵌在主从右栏（约 900-1050）再切窄档，否则整张表永久横向滚动
  if (typeof ResizeObserver !== 'undefined' && panelRef.value) {
    panelObserver = new ResizeObserver(([entry]) => {
      const width = entry.contentRect.width
      compact.value = width < 1550
      narrow.value = width < 1150
      ultra.value = width < 900
      micro.value = width < 720
    })
    panelObserver.observe(panelRef.value)
  }
  fetchList()
  fetchPointCapability()
})

onBeforeUnmount(() => {
  panelObserver?.disconnect()
})

defineExpose({ refresh: fetchList })
</script>

<style scoped>
/* fill 模式：表格占满父容器，工具条与分页固定，只有表体滚动 */
.dpp {
  min-width: 0;
}

.dpp.is-fill {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.dpp-toolbar,
.dpp-pager {
  flex: none;
}

.dpp-table {
  flex: 1;
  min-height: 0;
}

/* 嵌入右栏时表体高度由窗口决定：小本/手机上表体可能只有 200 来像素，
   独立页那套大留白空态（44+48 内边距、52px 图标）会把图标顶到表头底下，
   被 body-wrapper 裁掉。这里收紧留白与图标，让「图标+标题+说明+按钮」完整放得下；
   极矮窗口仍放不下时，让空态块从顶部开始并自己滚动，而不是居中溢出后上半截不可见。 */
.dpp.is-fill :deep(.el-table__empty-block) {
  align-items: safe center;
  overflow-y: auto;
}

/* Element 给空态文本设了 50% 宽和 60px 行高；行高会被标题/说明继承，
   把整块空态撑到 300px 以上。撑满宽度并恢复正常行高，内容才真正紧凑。 */
.dpp.is-fill :deep(.el-table__empty-text) {
  width: 100%;
  line-height: normal;
}

.dpp.is-fill .iot-empty {
  padding: 18px 16px 20px;
}

.dpp.is-fill .iot-empty-mark {
  width: 42px;
  height: 42px;
  margin-bottom: 10px;
  border-radius: 12px;
}

.dpp.is-fill .iot-empty-mark .el-icon {
  font-size: 21px;
}

.dpp.is-fill .iot-empty-hint {
  margin-top: 4px;
}

.dpp.is-fill .iot-empty .el-button {
  margin-top: 12px;
}

/* 报警条件摘要：与地址/类型同属技术标识，用弱化的等宽字体 */
.iot-cond {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

/* 变换流水线摘要：与报警条件同属技术表达式，窄单元格里截断，完整内容在 tooltip */
.iot-transform {
  display: block;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink-2);
  text-overflow: ellipsis;
  white-space: nowrap;
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
  flex: 1 1 auto;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

/* 质量码不参与收缩：值再长也只挤省略号，不把胶囊挤出单元格 */
.live-cell .iot-pill {
  flex: none;
}
</style>
