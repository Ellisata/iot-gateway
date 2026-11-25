<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page dw-page">
    <!-- 页头：沿用首页那套字号/留白层级，深浅两色由 token 控制，不另起一套视觉语言。
         设备级动作（模板/导入/新增）放页头右侧：它们作用于「设备集合」，
         不属于当前选中的某一台，窄屏切到单列时也仍然常驻。 -->
    <header class="iot-head dw-head">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.industrial') }}
          </p>
        </div>
        <div class="iot-title-row">
          <!-- 标题前的图标底座：与对话框卡片头、右栏设备身份用同一枚图标语言，
               三处「设备」标识因此共享一种质感，而不是各自为政的小图标 -->
          <div class="dw-title-main">
            <span class="dw-title-mark" aria-hidden="true">
              <el-icon><DeviceChip /></el-icon>
            </span>
            <h1 class="iot-title">{{ t('menu.deviceObject') }}</h1>
          </div>
          <span class="iot-head-sub">{{ t('deviceObject.subtitle') }}</span>
        </div>
      </div>
      <div class="dw-head-aside">
        <div class="iot-stat dw-stat-card">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('deviceObject.totalUnit') }}</span>
        </div>
        <div class="dw-head-actions">
          <el-button :icon="Download" @click="openTemplateDialog">{{ t('common.downloadTemplate') }}</el-button>
          <el-button :icon="Upload" :loading="importing" @click="handleImport">{{ t('common.import') }}</el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('deviceObject.add') }}</el-button>
          <input ref="fileInputRef" type="file" accept=".xlsx" class="hidden" @change="handleFileChange" />
        </div>
      </div>
    </header>

    <!-- 主从工作区：左列表选设备，右栏直接管理这台设备的点位。
         整页定高、两栏各自滚动 —— 换设备时左列表不会跟着右栏的表格一起滚走。 -->
    <div
      ref="splitRef"
      class="dw-split"
      :class="{
        'is-single': singleColumn,
        'is-list-view': !selectedDevice,
        'is-detail-view': !!selectedDevice,
      }"
    >
      <!-- ========== 左：设备列表 ========== -->
      <aside class="iot-panel dw-list" :aria-label="t('deviceObject.deviceListLabel')">
        <div class="iot-toolbar dw-list-toolbar">
          <div class="iot-toolbar-filters">
            <el-input
              v-model="queryForm.name"
              class="iot-search"
              :placeholder="t('deviceObject.searchPlaceholder')"
              clearable
              @keyup.enter="handleSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
            <div class="dw-list-filter-actions">
              <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
              <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
              <!-- 勾选会跨搜索/翻页丢失：批量删除宁可让人重勾，也不留下看不见的选中项。
                   批量选择是模式开关而非查询动作，靠右站，与「搜/重」分开一类 -->
              <el-button
                class="dw-batch-toggle"
                :type="selectionMode ? 'primary' : 'default'"
                :plain="selectionMode"
                @click="toggleSelectionMode"
              >
                {{ selectionMode ? t('deviceObject.batchSelectDone') : t('deviceObject.batchSelect') }}
              </el-button>
            </div>
          </div>
        </div>

        <div v-if="selectionMode" class="dw-select-row">
          <el-checkbox
            :model-value="allPageChecked"
            :indeterminate="somePageChecked"
            @change="toggleSelectPage"
          >
            {{ t('deviceObject.selectAllPage') }}
          </el-checkbox>
          <el-button
            type="danger"
            plain
            size="small"
            :icon="Delete"
            :disabled="checkedIds.size === 0"
            @click="handleBatchDelete"
          >
            {{ batchDeleteLabel }}
          </el-button>
        </div>

        <div
          ref="listScrollRef"
          class="dw-list-scroll"
          role="listbox"
          :aria-multiselectable="selectionMode"
          :aria-label="t('deviceObject.deviceListLabel')"
          v-loading="listLoading && !records.length"
          @keydown.down.prevent="moveFocus(1)"
          @keydown.up.prevent="moveFocus(-1)"
          @keydown.home.prevent="focusCard(0)"
          @keydown.end.prevent="focusCard(records.length - 1)"
        >
          <!-- 已选设备不在当前筛选结果里：保住右栏与编辑现场，只说明列表里找不到它 -->
          <p v-if="selectedDevice && !selectedOnPage && records.length" class="dw-list-note">
            {{ t('deviceObject.selectedNotInResults') }}
          </p>

          <div
            v-for="(row, idx) in records"
            :key="row.id"
            role="option"
            :aria-selected="selectionMode ? checkedIds.has(String(row.id)) : isSelected(row)"
            :tabindex="idx === activeFocusIndex ? 0 : -1"
            class="dw-card"
            :class="{
              'is-active': !selectionMode && isSelected(row),
              'is-checked': selectionMode && checkedIds.has(String(row.id)),
            }"
            :aria-label="`${row.name}, ${row.code || ''}, ${onlineStateLabel(row)}`"
            @focus="focusedIndex = idx"
            @click="handleCardClick(row)"
            @keydown.enter.prevent="handleCardClick(row, { focusDetail: true })"
            @keydown.space.prevent="handleCardClick(row, { focusDetail: true })"
          >
            <span
              class="dw-card-dot"
              :class="`is-${onlineState(row)}`"
              :title="onlineStateTip(row)"
              aria-hidden="true"
            />
            <div class="dw-card-main">
              <span class="dw-card-name" :title="row.name">{{ row.name }}</span>
              <span class="dw-card-meta">
                <span class="dw-card-code" :title="row.code || ''">{{ row.code || '—' }}</span>
                <span v-if="row.protocolName" class="dw-card-proto" :title="row.protocolName">
                  {{ row.protocolName }}
                </span>
              </span>
            </div>
            <span v-if="selectionMode" class="dw-card-check" aria-hidden="true">
              <el-icon v-if="checkedIds.has(String(row.id))"><Check /></el-icon>
            </span>
            <!-- 悬停/选中才露出的进入提示：卡片是跳转入口而非纯展示，留一个方向感 -->
            <span v-else class="dw-card-go" aria-hidden="true">
              <el-icon><ChevronRight /></el-icon>
            </span>
          </div>

          <div v-if="!listLoading && !records.length" class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><DeviceChip /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ appliedName
                ? t('deviceObject.emptySearch', { name: appliedName })
                : t('deviceObject.emptyTitle') }}
            </p>
            <p v-if="!appliedName" class="iot-empty-hint">{{ t('deviceObject.emptyHint') }}</p>
            <el-button v-if="appliedName" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('deviceObject.add') }}
            </el-button>
          </div>
        </div>

        <div v-if="total > pageSize" class="iot-pager dw-list-pager">
          <el-pagination
            v-model:current-page="currentPage"
            :page-size="pageSize"
            :total="total"
            size="small"
            layout="prev, pager, next"
            background
            @current-change="handlePageChange"
          />
        </div>
      </aside>

      <!-- ========== 右：选中设备的点位工作台 ========== -->
      <section class="iot-panel dw-detail" v-loading="hydrating">
        <header v-if="selectedDevice" class="dw-detail-head">
          <button
            v-if="singleColumn"
            type="button"
            class="iot-back dw-back"
            :title="t('deviceObject.backToDeviceList')"
            @click="backToList"
          >
            <el-icon><ArrowLeft /></el-icon>
            <span class="dw-back-text">{{ t('deviceObject.backToDeviceList') }}</span>
          </button>

          <div class="dw-detail-identity">
            <span class="dw-identity-mark" aria-hidden="true">
              <el-icon><DeviceChip /></el-icon>
            </span>
            <div class="dw-detail-copy">
              <h2 ref="detailHeadingRef" class="dw-detail-name" tabindex="-1" :title="selectedDevice.name">
                {{ selectedDevice.name }}
              </h2>
              <div class="dw-detail-meta">
                <el-tooltip
                  v-if="selectedDevice.code"
                  :content="t('common.copy')"
                  placement="top"
                >
                  <button
                    type="button"
                    class="dw-code-btn"
                    :aria-label="t('common.copy')"
                    @click="copyCode(selectedDevice.code)"
                  >
                    <span class="dw-code-text">{{ selectedDevice.code }}</span>
                    <el-icon><CopyDocument /></el-icon>
                  </button>
                </el-tooltip>
                <span v-if="selectedDevice.protocolName" class="dw-proto-chip" :title="selectedDevice.protocolName">
                  {{ selectedDevice.protocolName }}
                </span>
                <el-tooltip :content="onlineStateTip(selectedDevice)" placement="top" :persistent="false">
                  <span class="iot-pill" :class="onlinePillClass(selectedDevice)">
                    <span class="iot-pill-dot" aria-hidden="true" />
                    {{ onlineStateLabel(selectedDevice) }}
                  </span>
                </el-tooltip>
                <span class="dw-points-stat">{{ pointsTotalText }}</span>
              </div>
            </div>
          </div>

          <div class="dw-detail-actions">
            <el-tooltip :content="t('deviceWorkspace.tabs.groups')" placement="top">
              <el-button
                class="iot-row-btn"
                type="primary"
                link
                :icon="LayersLinked"
                :aria-label="t('deviceWorkspace.tabs.groups')"
                @click="handleViewGroups(selectedDevice)"
              />
            </el-tooltip>
            <el-tooltip :content="t('realtime.title')" placement="top">
              <el-button
                class="iot-row-btn"
                type="primary"
                link
                :icon="Monitor"
                :aria-label="t('realtime.title')"
                @click="handleViewRealtime(selectedDevice)"
              />
            </el-tooltip>
            <el-tooltip :content="t('common.edit')" placement="top">
              <el-button
                class="iot-row-btn"
                type="primary"
                link
                :icon="EditPen"
                :aria-label="t('common.edit')"
                @click="handleEdit(selectedDevice)"
              />
            </el-tooltip>
            <el-tooltip :content="t('common.delete')" placement="top">
              <el-button
                class="iot-row-btn"
                type="danger"
                link
                :icon="Delete"
                :aria-label="t('common.delete')"
                @click="handleDeleteDevice(selectedDevice)"
              />
            </el-tooltip>
          </div>
        </header>

        <div class="dw-detail-body">
          <!-- 换设备/换协议必须重挂载：实时值轮询与点位发现能力都在面板 setup 时按设备捕获；
               `v-if` 同时保证切回列表（窄屏）或删除设备后轮询立即停止。 -->
          <DevicePointsPanel
            v-if="selectedDevice"
            :key="`${selectedDevice.id}|${selectedDevice.protocolName || ''}`"
            fill
            :device-id="String(selectedDevice.id)"
            :device-name="selectedDevice.name"
            :protocol-name="selectedDevice.protocolName || ''"
            @total-change="pointsTotal = $event"
            @loading-change="pointsLoading = $event"
          />
          <div v-else class="iot-empty dw-detail-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><DeviceChip /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('deviceObject.selectDeviceHint') }}</p>
            <el-button type="primary" :icon="Plus" @click="handleAdd">
              {{ t('deviceObject.add') }}
            </el-button>
          </div>
        </div>
      </section>
    </div>

    <!-- ========== 新增 / 编辑对话框：基本信息与协议配置同屏 ==========
         版式与点位表单（device-address/AddressFormDialog）同一套：页头三级递进
         （eyebrow → 标题 → 说明），正文两张分区卡，卡头承担图标 + 说明 + 卡级动作，
         启停开关也收进卡头，不再在表单里独占一行。 -->
    <el-dialog
      v-model="dialogVisible"
      class="device-dialog"
      :title="isEdit ? t('deviceObject.editTitle') : t('deviceObject.addTitle')"
      width="min(720px, 94vw)"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <template #header>
        <div class="dev-dialog-head">
          <p class="dev-dialog-eyebrow">
            <span class="dev-dialog-eyebrow-dot" aria-hidden="true" />
            {{ isEdit ? t('deviceObject.formEyebrowEdit') : t('deviceObject.formEyebrowAdd') }}
          </p>
          <h2 class="dev-dialog-title">
            {{ isEdit ? t('deviceObject.editTitle') : t('deviceObject.addTitle') }}
          </h2>
          <p class="dev-dialog-sub">{{ t('deviceObject.formLead') }}</p>
        </div>
      </template>

      <div class="device-form">
        <!-- 基本信息：编码/名称/描述各占一行；启停是整台设备的开关，与卡题同排靠右 -->
        <section class="dev-card">
          <header class="dev-card-head">
            <span class="dev-card-icon" aria-hidden="true">
              <el-icon><DeviceChip /></el-icon>
            </span>
            <div class="dev-card-copy">
              <h3 class="dev-card-title">{{ t('deviceObject.basicInfoSection') }}</h3>
              <p class="dev-card-desc">{{ t('deviceObject.sectionBasicDesc') }}</p>
            </div>
            <div class="dev-enable">
              <span class="dev-enable-label">{{ t('common.status') }}</span>
              <el-switch
                v-model="formData.status"
                :active-value="1"
                :inactive-value="0"
                :aria-label="t('common.status')"
              />
            </div>
          </header>
          <div class="dev-card-body">
            <el-form
              ref="formRef"
              :model="formData"
              :rules="formRules"
              label-width="100px"
              label-position="right"
              status-icon
            >
              <el-form-item :label="t('deviceObject.code')" prop="code">
                <el-input
                  v-model="formData.code"
                  :placeholder="t('deviceObject.codePlaceholder')"
                  :disabled="isEdit && !!formData.code"
                  maxlength="64"
                  show-word-limit
                />
                <div class="dev-field-hint is-block">
                  {{ isEdit && formData.code ? t('deviceObject.codeImmutableHint') : t('deviceObject.codeHint') }}
                </div>
              </el-form-item>
              <el-form-item :label="t('common.name')" prop="name">
                <el-input
                  v-model="formData.name"
                  :placeholder="t('deviceObject.namePlaceholder')"
                  maxlength="100"
                  show-word-limit
                />
              </el-form-item>
              <el-form-item :label="t('common.description')" prop="description">
                <el-input
                  v-model="formData.description"
                  :placeholder="t('deviceObject.descPlaceholder')"
                  type="textarea"
                  :rows="3"
                  maxlength="200"
                  show-word-limit
                />
              </el-form-item>
            </el-form>
          </div>
        </section>

        <!-- 协议配置：选择器与动态参数同处一张卡；卡片正文只有一层内边距，
             协议选择器与动态表单的 label 列共用同一条左边缘，不会出现双缩进 -->
        <section class="dev-card">
          <header class="dev-card-head">
            <span class="dev-card-icon" aria-hidden="true">
              <el-icon><Connection /></el-icon>
            </span>
            <div class="dev-card-copy">
              <h3 class="dev-card-title">{{ t('deviceObject.protocolSection') }}</h3>
              <p class="dev-card-desc">{{ t('deviceObject.sectionProtocolDesc') }}</p>
            </div>
            <el-button
              v-if="protocolSelected"
              class="dev-test-btn"
              :loading="testingConnection"
              @click="handleTestConnection"
            >
              <el-icon class="mr-1"><Connection /></el-icon> {{ t('deviceObject.testConnection') }}
            </el-button>
          </header>
          <div class="dev-card-body">
            <el-form :label-width="protocolFormLabelWidth" label-position="right">
              <!-- 协议选择（使用公共组件） -->
              <el-form-item
                :label="t('deviceObject.protocol')"
                required
                :validate-status="protocolSelected ? 'success' : undefined"
              >
                <ProtocolSelector
                  v-model="formData.protocol"
                  :button-text="t('deviceObject.selectProtocol')"
                  @select="handleProtocolSelect"
                />
              </el-form-item>

              <!-- 协议参数紧跟选择器，不再套一层底色/卡片：动态表单要与上面的 label 列
                   严格对齐，多一层缩进就会让同一屏出现两条不同的左边缘。
                   form-create 动态渲染（:key 绑定协议名，切换时强制重建）。 -->
              <div v-if="protocolSelected" class="dynamic-form-wrapper">
                <form-create
                  :key="`${formData.protocol || 'empty'}-${fcLocale}`"
                  ref="fcRef"
                  :rule="localizedFcRule"
                  :option="localizedFcOption"
                  :locale="fcLocale"
                  @mounted="handleFcReady"
                />
              </div>
            </el-form>
          </div>
        </section>
      </div>

      <template #footer>
        <div class="dev-dialog-footer">
          <p class="dev-footer-hint">{{ t('deviceObject.formFooterHint') }}</p>
          <div class="dev-footer-actions">
            <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
            <el-button type="primary" :loading="submitting" @click="handleSubmit">
              {{ isEdit ? t('common.save') : t('common.submit') }}
            </el-button>
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- ========== 下载导入模板：通用 / 按协议 ========== -->
    <el-dialog
      v-model="templateDialogVisible"
      class="iot-dialog"
      :title="t('deviceObject.templateDialogTitle')"
      width="520px"
      :close-on-click-modal="false"
    >
      <el-radio-group v-model="templateMode" class="template-mode">
        <el-radio value="universal">{{ t('deviceObject.templateModeUniversal') }}</el-radio>
        <el-radio value="protocol">{{ t('deviceObject.templateModeProtocol') }}</el-radio>
      </el-radio-group>

      <p class="iot-dialog-hint">
        {{ templateMode === 'protocol'
          ? t('deviceObject.templateProtocolHint')
          : t('deviceObject.templateUniversalHint') }}
      </p>

      <el-form v-if="templateMode === 'protocol'" label-position="top">
        <el-form-item :label="t('deviceObject.templateProtocolLabel')" required>
          <el-select
            v-model="templateProtocolId"
            class="template-protocol-select"
            filterable
            remote
            clearable
            :remote-method="fetchTemplateProtocols"
            :loading="templateProtocolLoading"
            :placeholder="t('deviceObject.templateProtocolPlaceholder')"
          >
            <el-option
              v-for="protocol in templateProtocolOptions"
              :key="protocol.id"
              :label="protocol.name"
              :value="protocol.id"
            />
          </el-select>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="templateDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" @click="confirmDownloadTemplate">
          {{ t('common.downloadTemplate') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- ========== Excel 导入结果对话框 ========== -->
    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />
  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconAffiliate as Connection,
  IconArrowLeft as ArrowLeft,
  IconCheck as Check,
  IconChevronRight as ChevronRight,
  IconCopy as CopyDocument,
  IconCpu as DeviceChip,
  IconDeviceDesktop as Monitor,
  IconDownload as Download,
  IconLayersLinked as LayersLinked,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  getDeviceObjectList,
  getDeviceObjectById,
  addDeviceObject,
  updateDeviceObject,
  deleteDeviceObject,
  batchDeleteDeviceObject,
  testDeviceConnection,
  importDeviceObjects,
  downloadDeviceTemplate,
} from '@/api/modules/deviceObject'
import { getProtocolById, getProtocolList } from '@/api/modules/protocol'
import ProtocolSelector from '@/components/ProtocolSelector.vue'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import DevicePointsPanel from '@/views/industrial/device-address/DevicePointsPanel.vue'
import { useExcelImport } from '@/composables/useExcelImport'
import { formCreateLocaleName, localizeDynamicFormRules } from '@/i18n/dynamicFormText'
import { dynamicFormLabelWidthForRules } from '@/i18n/localizedText'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()

// ---------- 复制编码 ----------
async function copyCode(code) {
  try {
    await navigator.clipboard.writeText(String(code))
    ElMessage.success(t('deviceObject.codeCopied'))
  } catch {
    ElMessage.error(t('deviceObject.copyFailed'))
  }
}

// ---------- 在线状态 ----------
// 四态与后端总览同一口径：禁用 → 未接入 → 离线/在线。
// 只看 online 会把「启用但没有点位/驱动」的设备误标成在线。
const ONLINE_STATE_KEYS = {
  online: { label: 'deviceObject.onlineStateOnline', tip: 'deviceObject.onlineStateOnlineTip' },
  offline: { label: 'deviceObject.onlineStateOffline', tip: 'deviceObject.onlineStateOfflineTip' },
  uncollected: { label: 'deviceObject.onlineStateUncollected', tip: 'deviceObject.onlineStateUncollectedTip' },
  disabled: { label: 'deviceObject.onlineStateDisabled', tip: 'deviceObject.onlineStateDisabledTip' },
}

function onlineState(row) {
  if (row.status !== 1) return 'disabled'
  if (row.collected === false) return 'uncollected'
  return row.online ? 'online' : 'offline'
}

function onlineStateLabel(row) {
  return t(ONLINE_STATE_KEYS[onlineState(row)].label)
}

function onlineStateTip(row) {
  return t(ONLINE_STATE_KEYS[onlineState(row)].tip)
}

// 详情头沿用 iot-pill 的三色语义，与列表圆点同一状态来源
function onlinePillClass(row) {
  return {
    online: 'is-green',
    offline: 'is-rose',
    uncollected: 'is-amber',
    disabled: 'is-gray',
  }[onlineState(row)]
}

// ---------- 设备列表 ----------
const records = ref([])
const listLoading = ref(false)
const queryForm = reactive({ name: '' })
const appliedName = ref('')
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 首屏还没拿到数时显示占位符而不是 0：「0 台设备」是一个会被信以为真的假事实
const displayTotal = computed(() =>
  listLoading.value && records.value.length === 0 ? '—' : total.value,
)

// ---------- 选中设备 ----------
const selectedDevice = ref(null)
const pointsTotal = ref(0)
const pointsLoading = ref(true)
const hydrating = ref(false)
const initialized = ref(false)

const selectedOnPage = computed(() =>
  records.value.some((row) => isSelected(row)),
)

const pointsTotalText = computed(() =>
  pointsLoading.value && pointsTotal.value === 0
    ? '—'
    : t('deviceObject.pointsTotal', { n: pointsTotal.value }),
)

function isSelected(row) {
  return !!selectedDevice.value
    && String(selectedDevice.value.id) === String(row?.id)
}

// 选中设备写回 URL：刷新/分享/浏览器前进后退都能回到同一台。
// 旧深链带过来的 deviceObjectName/protocolName 不再需要，顺手清掉。
function writeDeviceQuery(id) {
  const current = route.query.deviceId ? String(route.query.deviceId) : ''
  const next = id ? String(id) : ''
  if (current === next && !route.query.deviceObjectName && !route.query.protocolName) return
  const query = { ...route.query }
  if (next) query.deviceId = next
  else delete query.deviceId
  delete query.deviceObjectName
  delete query.protocolName
  router.replace({ query })
}

function setSelected(row, { writeUrl = true } = {}) {
  selectedDevice.value = row
  pointsTotal.value = 0
  pointsLoading.value = true
  if (writeUrl) writeDeviceQuery(row.id)
}

// URL 里的设备不在当前页时按 id 补一次详情：列表行已含 DeviceVO 的完整字段，
// 足以渲染右侧头部，不必等翻到那一页。
async function hydrateDevice(id) {
  if (!id || hydrating.value) return
  hydrating.value = true
  try {
    const detail = await getDeviceObjectById(id)
    if (detail && detail.id) {
      setSelected(detail, { writeUrl: false })
    } else {
      ElMessage.warning(t('deviceObject.hydrateFailed'))
      writeDeviceQuery('')
    }
  } catch {
    ElMessage.warning(t('deviceObject.hydrateFailed'))
    writeDeviceQuery('')
  } finally {
    hydrating.value = false
  }
}

// 首屏只做一次自动选中：有 ?deviceId 优先恢复；否则宽屏选第一台，窄屏留列表。
// 宽度未知（ResizeObserver 还没回调）时先不选，避免窄屏一进来就被详情顶掉列表。
async function maybeAutoSelect() {
  if (initialized.value || listLoading.value) return
  const id = route.query.deviceId ? String(route.query.deviceId) : ''
  if (id) {
    const inPage = records.value.find((row) => String(row.id) === id)
    if (inPage) {
      initialized.value = true
      setSelected(inPage, { writeUrl: false })
      return
    }
    initialized.value = true
    await hydrateDevice(id)
    if (!selectedDevice.value && !singleColumn.value && records.value.length) {
      setSelected(records.value[0])
    }
    return
  }
  if (splitWidth.value === null) return
  initialized.value = true
  if (!singleColumn.value && records.value.length) {
    setSelected(records.value[0])
  }
}

async function fetchDevices() {
  listLoading.value = true
  try {
    const params = {
      page: currentPage.value,
      size: pageSize.value,
    }
    if (appliedName.value) params.name = appliedName.value
    const res = await getDeviceObjectList(params)
    records.value = res?.records ?? []
    total.value = res?.total ?? 0
    checkedIds.clear()
    // 选中的那台若仍在结果里，用最新行刷新头部（在线状态/名称可能已变）
    if (selectedDevice.value) {
      const fresh = records.value.find((row) => isSelected(row))
      if (fresh) selectedDevice.value = fresh
    }
    // 必须先落下 loading 再判首次选中：maybeAutoSelect 以「列表已就绪」为前提
    listLoading.value = false
    maybeAutoSelect()
  } catch (err) {
    console.log(err.message)
  } finally {
    listLoading.value = false
  }
}

// ---------- 搜索 / 重置 / 分页 ----------
// 换一屏数据就把列表滚回顶部：分页器在列表底部，翻页时人正处在底部，
// 不重置的话新页会停在中间、看不到前几张卡片。顺带丢掉旧的焦点序号。
function scrollListToTop() {
  if (listScrollRef.value) listScrollRef.value.scrollTop = 0
  focusedIndex.value = -1
}

function handleSearch() {
  appliedName.value = queryForm.name.trim()
  currentPage.value = 1
  scrollListToTop()
  fetchDevices()
}

function handleReset() {
  queryForm.name = ''
  appliedName.value = ''
  currentPage.value = 1
  scrollListToTop()
  fetchDevices()
}

function handlePageChange() {
  scrollListToTop()
  fetchDevices()
}

// ---------- 卡片选择 / 键盘 ----------
const focusedIndex = ref(-1)
const listScrollRef = ref(null)
const detailHeadingRef = ref(null)
const activeFocusIndex = computed(() =>
  focusedIndex.value >= 0 && focusedIndex.value < records.value.length ? focusedIndex.value : 0,
)

function handleCardClick(row, { focusDetail = false } = {}) {
  if (selectionMode.value) {
    toggleChecked(row)
    return
  }
  focusedIndex.value = records.value.indexOf(row)
  setSelected(row)
  if (focusDetail && singleColumn.value) {
    nextTick(() => detailHeadingRef.value?.focus())
  }
}

function focusCard(index) {
  if (!records.value.length) return
  focusedIndex.value = Math.min(Math.max(index, 0), records.value.length - 1)
  nextTick(() => {
    const cards = listScrollRef.value?.querySelectorAll('.dw-card')
    const el = cards?.[focusedIndex.value]
    el?.focus()
    el?.scrollIntoView({ block: 'nearest' })
  })
}

function moveFocus(delta) {
  focusCard(focusedIndex.value < 0 ? 0 : focusedIndex.value + delta)
}

function backToList() {
  selectedDevice.value = null
  pointsTotal.value = 0
  pointsLoading.value = false
  writeDeviceQuery('')
  nextTick(() => focusCard(activeFocusIndex.value))
}

// 浏览器前进/后退与手改 URL：与当前选中相同就忽略，避免自触发循环
watch(
  () => route.query.deviceId,
  (value) => {
    const id = value ? String(value) : ''
    const current = selectedDevice.value ? String(selectedDevice.value.id) : ''
    if (id === current) return
    if (!id) {
      selectedDevice.value = null
      pointsTotal.value = 0
      pointsLoading.value = false
      return
    }
    const inPage = records.value.find((row) => String(row.id) === id)
    if (inPage) {
      setSelected(inPage, { writeUrl: false })
      return
    }
    hydrateDevice(id)
  },
)

// ---------- 批量选择 ----------
const selectionMode = ref(false)
// reactive Set：模板里 checkedIds.has() / size 都能跟着增删更新
const checkedIds = reactive(new Set())

const allPageChecked = computed(() =>
  records.value.length > 0 && records.value.every((row) => checkedIds.has(String(row.id))),
)
const somePageChecked = computed(() =>
  !allPageChecked.value && records.value.some((row) => checkedIds.has(String(row.id))),
)
const batchDeleteLabel = computed(() =>
  checkedIds.size
    ? t('deviceObject.batchDeleteCount', { n: checkedIds.size })
    : t('deviceObject.batchDelete'),
)

function toggleSelectionMode() {
  selectionMode.value = !selectionMode.value
  if (!selectionMode.value) checkedIds.clear()
}

function toggleChecked(row) {
  const id = String(row.id)
  if (checkedIds.has(id)) checkedIds.delete(id)
  else checkedIds.add(id)
}

function toggleSelectPage(checked) {
  for (const row of records.value) {
    const id = String(row.id)
    if (checked) checkedIds.add(id)
    else checkedIds.delete(id)
  }
}

// ---------- 跳转深链 ----------
function handleViewRealtime(row) {
  router.push({
    name: 'GatewayRealtime',
    params: { deviceObjectId: String(row.id) },
    query: { deviceObjectName: row.name, protocolName: row.protocolName || '' },
  })
}

function handleViewGroups(row) {
  router.push({
    name: 'GatewayCollectorGroup',
    params: { deviceObjectId: String(row.id) },
    query: { deviceObjectName: row.name, protocolName: row.protocolName || '' },
  })
}

// ---------- Excel 导入 / 模板下载 ----------
const {
  fileInputRef,
  importing,
  importResultVisible,
  importResult,
  handleImport,
  handleFileChange,
  handleDownloadTemplate: downloadTemplate,
} = useExcelImport({
  importFn: (file) => importDeviceObjects(file),
  downloadFn: downloadDeviceTemplate,
  templateName: () => t('deviceObject.importTemplateName'),
  refresh: () => {
    // 导入可能来自任意页：回到第一页，并用当前搜索词重新取
    currentPage.value = 1
    scrollListToTop()
    fetchDevices()
  },
})

// ---------- 模板下载：通用 / 按协议 ----------
const templateDialogVisible = ref(false)
const templateMode = ref('universal')
const templateProtocolId = ref('')
const templateProtocolOptions = ref([])
const templateProtocolLoading = ref(false)

function openTemplateDialog() {
  templateDialogVisible.value = true
  templateMode.value = 'universal'
  templateProtocolId.value = ''
  if (!templateProtocolOptions.value.length) fetchTemplateProtocols('')
}

async function fetchTemplateProtocols(keyword) {
  templateProtocolLoading.value = true
  try {
    const params = { page: 1, size: 50 }
    if (keyword) params.name = keyword
    const res = await getProtocolList(params)
    templateProtocolOptions.value = res?.records ?? []
  } catch {
    ElMessage.error(t('deviceObject.templateProtocolLoadFailed'))
  } finally {
    templateProtocolLoading.value = false
  }
}

/** 文件名里不能出现 \ / : * ? " < > |，协议名可能带点号以外的字符。 */
function safeTemplateFilePart(text) {
  return String(text || '').replace(/[\\/:*?"<>|]/g, '_')
}

async function confirmDownloadTemplate() {
  if (templateMode.value === 'protocol' && !templateProtocolId.value) {
    ElMessage.warning(t('deviceObject.templateProtocolRequired'))
    return
  }
  const protocol = templateProtocolOptions.value.find((item) => item.id === templateProtocolId.value)
  const params = templateMode.value === 'protocol' ? { protocolId: templateProtocolId.value } : {}
  const filename = templateMode.value === 'protocol'
    ? t('deviceObject.importTemplateNameProtocol', {
        name: safeTemplateFilePart(protocol?.name || templateProtocolId.value),
      })
    : t('deviceObject.importTemplateName')
  const ok = await downloadTemplate(params, filename)
  if (ok) templateDialogVisible.value = false
}

// ---------- 删除 / 批量删除后的选中接力 ----------
// deletedIds 里若含当前选中设备：删完按原位置选下一台；当前页删空且不是第一页则退一页。
async function reloadAfterDeviceDelete(deletedIds, preferIndex) {
  const deletingSelected = selectedDevice.value
    && deletedIds.has(String(selectedDevice.value.id))
  const pageEmptied = records.value.length > 0
    && records.value.every((row) => deletedIds.has(String(row.id)))

  if (pageEmptied && currentPage.value > 1) {
    currentPage.value -= 1
    scrollListToTop()
  }
  await fetchDevices()

  if (!deletingSelected) return
  if (!records.value.length) {
    selectedDevice.value = null
    pointsTotal.value = 0
    pointsLoading.value = false
    writeDeviceQuery('')
    return
  }
  const nextIndex = Math.min(Math.max(preferIndex, 0), records.value.length - 1)
  setSelected(records.value[nextIndex])
}

// ---------- 删除单台 ----------
async function handleDeleteDevice(row) {
  try {
    await ElMessageBox.confirm(
      t('deviceObject.deleteConfirm', { name: row.name }),
      t('deviceObject.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return
  }

  const preferIndex = records.value.findIndex((item) => String(item.id) === String(row.id))
  try {
    await deleteDeviceObject(row.id)
    ElMessage.success(t('deviceObject.deleteSuccess'))
    await reloadAfterDeviceDelete(new Set([String(row.id)]), preferIndex)
  } catch {
    // 失败原因由响应拦截器统一提示
  }
}

// ---------- 批量删除 ----------
async function handleBatchDelete() {
  const ids = [...checkedIds]
  if (!ids.length) return

  try {
    await ElMessageBox.confirm(
      t('deviceObject.batchDeleteConfirm', { n: ids.length }),
      t('deviceObject.batchDeleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return // 用户取消
  }

  const preferIndex = selectedDevice.value
    ? records.value.findIndex((row) => isSelected(row))
    : -1
  try {
    const res = await batchDeleteDeviceObject({ ids })
    // deleted 由服务端按**实际删除行数**给出，可能小于勾选数：勾选后又被人删掉的
    // 设备不在删除集里。少删了要说出来，不能一律报"成功 N 台"。
    const deleted = res?.deleted ?? 0
    const addresses = res?.addresses ?? 0
    const missing = ids.length - deleted
    if (missing > 0) {
      ElMessage.warning(t('deviceObject.batchDeletePartial', { n: deleted, missing, addresses }))
    } else {
      ElMessage.success(t('deviceObject.batchDeleteSuccess', { n: deleted, addresses }))
    }
    selectionMode.value = false
    checkedIds.clear()
    await reloadAfterDeviceDelete(new Set(ids), preferIndex)
  } catch {
    // 失败原因由响应拦截器统一提示
  }
}

// ---------- 对话框状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const testingConnection = ref(false)
const formRef = ref(null)

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  code: '',
  name: '',
  protocol: '',
  protocolId: '',
  description: '',
  status: 1,
  protocolJson: '',
})

// 协议参数区是否渲染：编辑回填与手动选择都只认 protocol 是否有值
const protocolSelected = computed(() => !!formData.protocol)

// ---------- 表单校验规则 ----------
const formRules = {
  code: [
    { required: true, message: () => t('deviceObject.codeRequired'), trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_-]{0,63}$/,
      message: () => t('deviceObject.codeInvalid'),
      trigger: 'blur',
    },
  ],
  name: [
    { required: true, message: () => t('deviceObject.searchNameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('deviceObject.nameLength'), trigger: 'blur' },
  ],
  description: [{ max: 200, message: () => t('protocol.descMaxLength'), trigger: 'blur' }],
}

// ---------- form-create 动态表单 ----------
const fcRef = ref(null)
const fcApi = ref(null)
const fcRule = ref([])
const fcOption = ref({
  form: {
    labelWidth: '100px',
    labelPosition: 'right',
  },
  // 提交/重置由对话框底部唯一负责：插件声明的 formJson 若自带按钮，
  // 会在参数区里多出一个和页脚重复的「提交」，两处提交语义还不同
  submitBtn: false,
  resetBtn: false,
})

// 插件声明的 formJson 若带了 options.language（`{{$t.key}}` 形式的文案）按当前语言解析；
// 写死中文的旧插件表单再由存量词典兜底。
const localizedFcRule = computed(() =>
  localizeDynamicFormRules(fcRule.value, locale.value, fcOption.value.language),
)
const fcLocale = computed(() => formCreateLocaleName(locale.value))
// 英文标签比中文长，沿用插件的 125px 会折行；但给固定 230px 又会让短标签的协议
// （Device port 只要 ~75px）整张表单往右推。这里按当前协议的实际标题实测宽度，
// 第二步外层 el-form 的 label 列跟着同一个宽度走，否则动态表单与协议选择器错列。
const protocolLabelWidth = computed(() =>
  dynamicFormLabelWidthForRules(
    localizedFcRule.value,
    locale.value,
    fcOption.value?.form?.labelWidth || '100px',
  ),
)
const localizedFcOption = computed(() => ({
  ...fcOption.value,
  form: {
    ...(fcOption.value.form || {}),
    labelWidth: protocolLabelWidth.value,
  },
}))
const protocolFormLabelWidth = computed(() => protocolLabelWidth.value)

/** form-create 渲染完成后获取 API 实例 */
function handleFcReady($f) {
  fcApi.value = $f
}

/** 协议选中回调：加载 formJson 并渲染动态表单 */
function handleProtocolSelect(row) {
  // 用户主动换协议时丢弃上次编辑带出的旧配置，避免回填到新协议的同名字段
  if (row.id !== formData.protocolId) formData.protocolJson = ''
  formData.protocol = row.name
  formData.protocolId = row.id
  handleProtocolChange(row.id)
}

// ---------- 协议变更：加载 formJson 并渲染动态表单 ----------
async function handleProtocolChange(protocolId) {
  // 清空旧的动态表单
  fcRule.value = []

  if (!protocolId) return

  try {
    const res = await getProtocolById(protocolId)
    if (!res) {
      ElMessage.warning(t('deviceObject.protocolDetailNotFound'))
      return
    }
    // 回填协议名称（编辑模式时覆盖列表回传的 ID）
    if (res.name) formData.protocol = res.name
    // 解析 formJson
    if (res.formJson) {
      let parsed
      try {
        parsed = typeof res.formJson === 'string' ? JSON.parse(res.formJson) : res.formJson
      } catch {
        ElMessage.warning(t('deviceObject.protocolFormParseFailed'))
        return
      }

      // 后端可能将 rule/options 序列化为字符串，需要二次解析
      if (parsed?.rule && typeof parsed.rule === 'string') {
        try {
          parsed.rule = JSON.parse(parsed.rule)
        } catch {
          ElMessage.warning(t('deviceObject.protocolRuleParseFailed'))
          return
        }
      }
      if (parsed?.options && typeof parsed.options === 'string') {
        try {
          parsed.options = JSON.parse(parsed.options)
        } catch {
          // 忽略，options 非必需
        }
      }

      // formJson 结构：{ rule: [...], option/options: {...} }
      let rules = null
      if (parsed?.rule && Array.isArray(parsed.rule)) {
        rules = parsed.rule
      } else if (Array.isArray(parsed)) {
        // 兼容纯数组格式
        rules = parsed
      } else {
        ElMessage.warning(t('deviceObject.protocolFormInvalid'))
        return
      }

      // 编辑模式：把已保存的协议配置直接写进 rule.value，随规则一起装载。
      // 不能用 fcApi.setValue 回填——form-create 的规则是异步装载的，
      // 字段注册前调用会静默落空（首次从第一步进入第二步时必然如此）。
      if (isEdit.value && formData.protocolJson) {
        try {
          const configData =
            typeof formData.protocolJson === 'string'
              ? JSON.parse(formData.protocolJson)
              : formData.protocolJson
          if (typeof configData === 'object' && configData !== null) {
            // 只回填当前 rule 中存在的字段，避免脏数据带进表单
            rules = rules.map((item) =>
              item?.field && Object.prototype.hasOwnProperty.call(configData, item.field)
                ? { ...item, value: configData[item.field] }
                : item,
            )
          }
        } catch {
          // 回填失败不阻塞，按协议默认值渲染
        }
        // 回显数据只消费一次，切换协议时不应复用
        formData.protocolJson = ''
      }

      fcRule.value = rules

      // 合并全局选项（兼容 option 和 options 两种字段名）
      const opts = parsed?.options || parsed?.option
      if (opts) {
        fcOption.value = {
          ...fcOption.value,
          ...opts,
          // language 必须跟着当前协议走，不能沿用上一个协议的多语言表
          language: opts.language || {},
          // 合并插件选项后仍强制关掉内置按钮（见 fcOption 的声明）
          submitBtn: false,
          resetBtn: false,
          form: {
            ...fcOption.value.form,
            ...(opts.form || {}),
          },
        }
      }
    }
  } catch (err) {
    console.error('获取协议详情失败', err)
    ElMessage.error(t('deviceObject.fetchProtocolConfigFailed'))
  }
}

// ---------- 新增 ----------
function handleAdd() {
  isEdit.value = false

  formData.id = ''
  formData.code = ''
  formData.name = ''
  formData.protocol = ''
  formData.protocolId = ''
  formData.description = ''
  formData.status = 1
  formData.protocolJson = ''

  // 重置动态表单
  fcRule.value = []

  dialogVisible.value = true
}

// ---------- 编辑 ----------
async function handleEdit(row) {
  isEdit.value = true

  formData.id = row.id
  formData.code = row.code ?? ''
  formData.name = row.name
  // 列表/详情接口回传的协议字段是 protocolName，没有 protocol；
  // 取错字段会让 protocolSelected 为 false，协议参数区整块不渲染
  formData.protocol = row.protocolName ?? ''
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1

  // 先获取完整详情（含 protocolConfig），保证在对话框打开前数据就绪
  try {
    const detail = await getDeviceObjectById(row.id)
    formData.code = detail?.code ?? formData.code
    formData.protocolJson = detail?.protocolJson ?? ''
    formData.protocolId = detail?.protocolId ?? ''
    if (detail?.protocolName) formData.protocol = detail.protocolName
  } catch (err) {
    formData.protocolJson = ''
    formData.protocolId = ''
    console.error('获取设备对象详情失败', err)
    // 不阻塞编辑流程，回填失败不影响基本信息编辑
  }

  // 协议参数与基本信息同屏：打开前就把动态表单装好，编辑者不必再点「下一步」
  fcRule.value = []
  if (formData.protocolId) {
    await handleProtocolChange(formData.protocolId)
  }

  dialogVisible.value = true
}

// ---------- 对话框关闭 ----------
function handleDialogClose() {
  formRef.value?.resetFields()
  fcRule.value = []
}

/** 收集 form-create 动态表单数据（按当前 rule 的 field 过滤，剔除脏数据） */
function collectProtocolConfig() {
  if (!fcApi.value) return null
  const data = fcApi.value.formData() || {}
  const validFields = new Set(fcRule.value.map(item => item.field).filter(Boolean))
  if (validFields.size === 0) return data
  const cleaned = {}
  Object.keys(data).forEach(key => {
    if (validFields.has(key)) cleaned[key] = data[key]
  })
  return cleaned
}

// ---------- 测试连接 ----------
async function handleTestConnection() {
  if (!formData.protocol) {
    ElMessage.warning(t('deviceObject.selectProtocolFirst'))
    return
  }
  // 校验 form-create 动态表单
  if (fcApi.value) {
    try {
      await fcApi.value.validate()
    } catch {
      ElMessage.warning(t('deviceObject.completeProtocolConfig'))
      return
    }
  }

  testingConnection.value = true
  try {
    await testDeviceConnection({
      protocolName: formData.protocol,
      protocolJson: collectProtocolConfig() ?? {},
    })
    ElMessage.success(t('deviceObject.connectionSuccess'))
  } catch (err) {
    // 失败原因由响应拦截器统一提示
  } finally {
    testingConnection.value = false
  }
}

// ---------- 提交 ----------
async function handleSubmit() {
  // 校验基本信息表单
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    ElMessage.warning(t('deviceObject.basicInfoIncomplete'))
    return
  }

  // 校验协议是否已选
  if (!formData.protocol) {
    ElMessage.warning(t('deviceObject.selectProtocolFirst'))
    return
  }

  // 校验 form-create 动态表单
  if (fcApi.value) {
    try {
      await fcApi.value.validate()
    } catch {
      ElMessage.warning(t('deviceObject.completeProtocolConfig'))
      return
    }
  }

  submitting.value = true
  try {
    // 收集动态表单数据
    const protocolConfig = collectProtocolConfig()
    const payload = {
      code: formData.code.trim(),
      name: formData.name.trim(),
      protocolId: formData.protocolId,
      description: formData.description.trim() || undefined,
      status: formData.status,
      protocolJson: protocolConfig,
    }

    if (isEdit.value) {
      payload.id = formData.id
      await updateDeviceObject(payload)
      ElMessage.success(t('deviceObject.editSuccess'))
    } else {
      await addDeviceObject(payload)
      ElMessage.success(t('deviceObject.addSuccess'))
    }
    dialogVisible.value = false
    const editedId = isEdit.value ? String(formData.id) : ''
    await fetchDevices()
    // 编辑后选中项可能已不在当前筛选结果里：单独按 id 刷新右栏，保证改完立即看到新值
    if (editedId && selectedDevice.value && String(selectedDevice.value.id) === editedId) {
      try {
        const detail = await getDeviceObjectById(editedId)
        if (detail?.id) selectedDevice.value = detail
      } catch {
        // 刷新失败不阻塞；下次列表刷新会再对齐
      }
    }
  } catch (err) {
    const msg = isEdit.value ? t('deviceObject.editFailed') : t('deviceObject.addFailed')
    console.error(msg, err)
    ElMessage.error(err?.response?.data?.msg || err?.message || msg)
  } finally {
    submitting.value = false
  }
}

// ---------- 响应式 ----------
// 判据取**工作区的真实宽度**而不是窗口宽度：侧栏折叠/拖宽后同一窗口会多出上百像素。
// 低于 1080 主从两栏放不下，切成「列表 / 详情」二选一全宽。
const splitRef = ref(null)
const splitWidth = ref(null)
const singleColumn = computed(() => splitWidth.value !== null && splitWidth.value < 1080)
let splitObserver = null

// ---------- 初始化 ----------
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && splitRef.value) {
    splitObserver = new ResizeObserver(([entry]) => {
      splitWidth.value = entry.contentRect.width
      maybeAutoSelect()
    })
    splitObserver.observe(splitRef.value)
  }
  fetchDevices()
})

onBeforeUnmount(() => {
  splitObserver?.disconnect()
})
</script>

<style scoped>
/* 整页定高：页头之外全部交给主从两栏，各自滚动（同 protocol-form / channel-form 的约定） */
.dw-page {
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--header-height, 60px) - 48px);
}

.dw-head {
  flex: none;
}

/* 标题与图标同一视觉组：图标底座把「设备对象」从纯文字标题抬成有身份的页面入口 */
.dw-title-main {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.dw-title-mark {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  font-size: 20px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.05), inset 0 1px 0 rgba(255, 255, 255, 0.9);
}

.dw-head-aside {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 12px;
}

/* 计数从裸文字收进一枚浅色小卡：读数与右侧动作在纵向上分成「看」和「做」两层 */
.dw-stat-card {
  padding: 7px 14px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border: 1px solid var(--line);
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), inset 0 1px 0 #fff;
}

.dw-stat-card .iot-stat-num {
  font-size: 20px;
}

.dw-head-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

/* 页头按钮比工具栏更重一档：9px 圆角与工具栏同源，主按钮带一点下沉阴影 */
.dw-head-actions :deep(.el-button) {
  border-radius: 9px;
  font-weight: 500;
}

.dw-head-actions :deep(.el-button--primary) {
  box-shadow: 0 1px 2px rgba(37, 99, 235, 0.22), 0 8px 18px -10px rgba(37, 99, 235, 0.55);
}

/* ---------- 主从两栏 ---------- */
.dw-split {
  display: flex;
  flex: 1;
  gap: 16px;
  min-height: 0;
  overflow: hidden;
}

/* 两块工作区共用一层「浮起」的观感：贴边仍是发丝线，远处用大偏移低透明度的
   投影把面板从画布上托起，层次靠光而不是靠更深的边框 */
.dw-split > .iot-panel {
  margin-bottom: 0;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 18px 44px -34px rgba(15, 23, 42, 0.32);
}

.dw-list {
  display: flex;
  flex: 0 0 320px;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.dw-detail {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

/* 左栏工具条：搜索独占一行，动作另起一行，320px 里不挤成一团 */
.dw-list-toolbar {
  padding: 12px;
}

.dw-list-toolbar .iot-toolbar-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}

.dw-list-toolbar .iot-search {
  width: 100%;
}

.dw-list-filter-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}

/* 「搜/重」是一组查询动作，批量选择是模式开关：靠右站，与查询拉开一档 */
.dw-batch-toggle {
  margin-left: auto;
}

.dw-select-row {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  background: linear-gradient(180deg, var(--brand-tint) 0%, #ffffff 100%);
  border-bottom: 1px solid var(--brand-line);
}

.dw-list-scroll {
  flex: 1;
  min-height: 0;
  padding: 8px;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.dw-list-pager {
  flex: none;
  justify-content: center;
  padding: 8px 12px;
  background: linear-gradient(180deg, #fbfcfe 0%, #ffffff 100%);
  border-top: 1px solid var(--line-soft);
}

.dw-list-note {
  margin: 2px 4px 8px;
  padding: 8px 10px;
  font-size: 12px;
  line-height: 1.5;
  color: #92400e;
  background: #fffbeb;
  border-radius: 8px;
}

/* ---------- 设备卡片 ----------
   静止时是一张没有边框的纯文字行；悬停/选中才补上描边、浅渐变与投影，
   列表因此保持安静，被指向的那一张才浮起来。 */
.dw-card {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 12px 11px 14px;
  border: 1px solid transparent;
  border-radius: 12px;
  cursor: pointer;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}

.dw-card + .dw-card {
  margin-top: 4px;
}

.dw-card:hover {
  background: linear-gradient(180deg, #ffffff 0%, #f8fbff 100%);
  border-color: var(--line);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 10px 24px -20px rgba(15, 23, 42, 0.35);
}

.dw-card.is-active {
  background: linear-gradient(180deg, #ffffff 0%, var(--brand-soft) 100%);
  border-color: var(--brand-line);
  box-shadow: 0 1px 2px rgba(37, 99, 235, 0.08), 0 10px 24px -22px rgba(37, 99, 235, 0.55);
}

.dw-card.is-checked {
  background: var(--brand-tint);
  border-color: var(--brand-line);
}

/* 选中态用压在左缘的短竖条，不靠整块深色抢过右栏的读数 */
.dw-card.is-active::before {
  content: '';
  position: absolute;
  top: 10px;
  bottom: 10px;
  left: 0;
  width: 3px;
  background: var(--brand);
  border-radius: 3px;
}

.dw-card:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.5);
  outline-offset: -1px;
}

.dw-card-dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.dw-card-dot.is-online {
  background: #10b981;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.14);
}

.dw-card-dot.is-offline {
  background: #f43f5e;
  box-shadow: 0 0 0 3px rgba(244, 63, 94, 0.12);
}

.dw-card-dot.is-uncollected {
  background: #f59e0b;
  box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.14);
}

.dw-card-dot.is-disabled {
  background: #94a3b8;
}

.dw-card-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.dw-card-name {
  overflow: hidden;
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-card-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--ink-4);
}

.dw-card-code {
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-card-proto {
  overflow: hidden;
  max-width: 56%;
  padding: 1px 6px;
  font-size: 10.5px;
  line-height: 15px;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
  background: #f1f5f9;
  border-radius: 5px;
}

.dw-card-check {
  display: grid;
  flex: none;
  place-items: center;
  width: 18px;
  height: 18px;
  color: #fff;
  border: 1.5px solid #cbd5e1;
  border-radius: 50%;
}

.dw-card-check .el-icon {
  font-size: 12px;
}

.dw-card.is-checked .dw-card-check {
  background: var(--brand);
  border-color: var(--brand);
}

/* 进入提示：默认让位给文字，指向或被选中时才滑入，不在十张卡片上重复喊「可点」 */
.dw-card-go {
  display: grid;
  flex: none;
  place-items: center;
  width: 18px;
  height: 18px;
  font-size: 14px;
  color: var(--ink-4);
  opacity: 0;
  transform: translateX(-3px);
  transition: opacity 0.16s ease, transform 0.16s ease, color 0.16s ease;
}

.dw-card:hover .dw-card-go,
.dw-card.is-active .dw-card-go,
.dw-card:focus-visible .dw-card-go {
  color: var(--brand);
  opacity: 1;
  transform: none;
}

/* 触屏没有悬停：选中态就是可进入的提示，未选中的卡片不占这 18px */
@media (hover: none) {
  .dw-card-go {
    display: none;
  }
}

/* ---------- 右栏详情头 ---------- */
.dw-detail-head {
  display: flex;
  flex: none;
  align-items: center;
  gap: 12px;
  padding: 14px 18px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border-bottom: 1px solid var(--line-soft);
}

.dw-back {
  flex: none;
}

.dw-detail-identity {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

/* 与页头同一种图标底座，只是小一档：进到详情仍能认出「这是同一台设备」 */
.dw-identity-mark {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  font-size: 20px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 12px;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.9);
}

.dw-detail-copy {
  min-width: 0;
}

.dw-detail-name {
  margin: 0;
  overflow: hidden;
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-detail-name:focus {
  outline: none;
}

.dw-detail-name:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.5);
  outline-offset: 2px;
  border-radius: 4px;
}

.dw-detail-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  margin-top: 5px;
  font-size: 12px;
  color: var(--ink-4);
}

.dw-code-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
  padding: 0;
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--ink-3);
  background: none;
  border: 0;
  cursor: pointer;
}

/* 编码是一长串等宽字符：宁可截断并悬停看全名，也不要按短横线折成三行 */
.dw-code-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-code-btn:hover {
  color: var(--brand);
}

.dw-code-btn .el-icon {
  font-size: 12px;
  opacity: 0.55;
}

.dw-code-btn:hover .el-icon {
  opacity: 1;
}

.dw-proto-chip {
  overflow: hidden;
  max-width: 220px;
  padding: 1px 8px;
  font-size: 11.5px;
  line-height: 18px;
  color: var(--ink-2);
  text-overflow: ellipsis;
  white-space: nowrap;
  background: #f1f5f9;
  border: 1px solid #e8edf4;
  border-radius: 6px;
}

.dw-points-stat {
  font-variant-numeric: tabular-nums;
}

/* 四个图标动作收进一枚带边的按钮座：与其他白面/描边元素同一种质感，
   不再裸浮在渐变头上；按钮仍只省在悬停时染色 */
.dw-detail-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 2px;
  padding: 2px;
  background: #ffffff;
  border: 1px solid var(--line);
  border-radius: 10px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.dw-detail-actions :deep(.iot-row-btn.el-button) {
  width: 30px;
  height: 30px;
  margin: 0;
  padding: 0;
  color: var(--ink-4);
  border-radius: 8px;
}

.dw-detail-actions :deep(.iot-row-btn.el-button .el-icon) {
  font-size: 16px;
}

.dw-detail-actions :deep(.iot-row-btn.el-button:hover) {
  color: var(--brand);
  background: var(--brand-soft);
}

.dw-detail-actions :deep(.iot-row-btn.el-button.el-button--danger:hover) {
  color: var(--danger);
  background: #fff1f2;
}

.dw-detail-body {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.dw-detail-body > * {
  flex: 1;
  min-width: 0;
}

.dw-detail-empty {
  justify-content: center;
  height: 100%;
}

/* ---------- 窄屏：列表 / 详情二选一 ---------- */
.dw-split.is-single {
  display: block;
}

.dw-split.is-single > .dw-list,
.dw-split.is-single > .dw-detail {
  height: 100%;
}

.dw-split.is-single.is-list-view > .dw-detail {
  display: none;
}

.dw-split.is-single.is-detail-view > .dw-list {
  display: none;
}

/* 与主从断点同一档（1080）：再窄下去页头右侧会被标题挤成两行按钮，
   不如整块落到标题下方，旁白与动作各占一行 */
@media (max-width: 1080px) {
  .dw-head {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  /* 叠放后计数与动作并成一排：高度省一行，动作仍从左侧起读 */
  .dw-head-aside {
    flex-direction: row;
    flex-wrap: wrap;
    align-items: center;
  }

  .dw-head-actions {
    justify-content: flex-start;
  }
}

/* ---------- 对话框 ----------
   el-dialog 会被 teleport 到 body，scoped 的 :deep 够不到，用唯一类名做全局作用域。
   版式与点位表单 / 触发规则同一套：页头渐变、正文白面、卡片分区。 */
/* 纵向 flex：页头/页脚固定，正文吃掉剩余高度并自滚 —— 中英文案、不同视口
   都不会把页脚顶出屏幕，也不需要再按内容猜一个 max-height 魔法数 */
:global(.device-dialog) {
  display: flex;
  flex-direction: column;
  max-height: calc(92vh - 24px);
  margin-top: 8vh;
  border-radius: 18px;
  box-shadow: 0 24px 60px -24px rgba(15, 23, 42, 0.35);
}

:global(.device-dialog .el-dialog__header),
:global(.device-dialog .el-dialog__footer) {
  flex: none;
}

:global(.device-dialog .el-dialog__header) {
  margin: 0;
  padding: 22px 26px 18px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border-bottom: 1px solid var(--line-soft);
}

:global(.device-dialog .el-dialog__headerbtn) {
  top: 14px;
  right: 16px;
  width: 30px;
  height: 30px;
  border-radius: 8px;
}

:global(.device-dialog .el-dialog__headerbtn:hover) {
  background: #f4f6fa;
}

/* 内容短时按自然高度，内容长时让出高度给滚动条；只滚主体，页脚按钮始终可点 */
:global(.device-dialog .el-dialog__body) {
  flex: 1;
  min-height: 0;
  padding: 20px 24px 24px;
  overflow-y: auto;
  overscroll-behavior: contain;
  background: var(--surface);
}

:global(.device-dialog .el-dialog__footer) {
  padding: 14px 24px;
  background: var(--surface);
  border-top: 1px solid var(--line-soft);
}

:global(.device-dialog .el-form-item__label) {
  font-size: 13px;
  color: var(--ink-2);
}

/* ---------- 弹窗头：eyebrow → 标题 → 说明，三级递进 ---------- */
.dev-dialog-head {
  padding-right: 28px;
}

.dev-dialog-eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--ink-3);
}

.dev-dialog-eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.dev-dialog-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.dev-dialog-sub {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

.device-form {
  padding-top: 2px;
}

/* ---------- 分区卡片：白面浮在浅色画布上，用发丝线收边 ---------- */
.dev-card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.dev-card + .dev-card {
  margin-top: 16px;
}

.dev-card-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--line-soft);
}

.dev-card-icon {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  font-size: 16px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 10px;
}

.dev-card-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;
}

.dev-card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.dev-card-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* 启停是整台设备的开关，与卡片标题同排靠右，不再在表单里独占一行 */
.dev-enable {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.dev-enable-label {
  font-size: 12.5px;
  color: var(--ink-3);
}

.dev-test-btn {
  flex: none;
  height: 32px;
  margin-top: 1px;
  padding: 0 12px;
  font-size: 12.5px;
  border-radius: 9px;
}

.dev-card-body {
  padding: 20px 22px;
}

/* 卡片正文最后一条表单项不再留尾巴，卡底留白只由 body 的内边距给出 */
.dev-card-body :deep(.el-form > .el-form-item:last-child) {
  margin-bottom: 0;
}

.dev-field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.dev-field-hint.is-block {
  flex: 0 0 100%;
}

/* ---------- 页脚：说明在左、动作在右 ---------- */
.dev-dialog-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px 16px;
}

.dev-footer-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* 说明过长时整块换行，按钮始终保持右对齐（左对齐的「提交」在窄窗里没有着落点） */
.dev-footer-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 10px;
  margin-left: auto;
}

/* 插件声明的参数控件收成一列，避免通栏输入框压着一个小步进器（同 device-scan 页） */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 280px;
}

.template-mode {
  display: flex;
  gap: 20px;
}

.template-protocol-select {
  width: 100%;
}

/* 详情头按**面板自身宽度**收缩，而不是视口宽度：侧栏展开/折叠、主从分栏，
   真正决定头部空间的都是右栏的宽度。600px 以下让图标底座与返回文字让位，
   把宽度留给设备名与四个动作。 */
.dw-detail {
  container-type: inline-size;
}

@container (max-width: 600px) {
  .dw-detail-head {
    gap: 10px;
    padding: 12px 14px;
  }

  .dw-back-text,
  .dw-identity-mark {
    display: none;
  }

  .dw-detail-actions :deep(.iot-row-btn.el-button) {
    width: 28px;
    height: 28px;
  }
}

/* 卡头在窄视口允许换行：标题说明占满一行，卡级动作落到第二行 */
@media (max-width: 720px) {
  .dev-card-head {
    flex-wrap: wrap;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dw-page *,
  .dw-page *::before,
  .dw-page *::after {
    transition: none !important;
  }
}
</style>
