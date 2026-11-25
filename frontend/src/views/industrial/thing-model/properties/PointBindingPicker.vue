<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
属性绑定点位是 1 对 1 的两级选择：先在左侧分页挑选设备，再在右侧分页挑选
该设备的一个点位；选定即替换原绑定，不需要额外「保存」。

点位候选是服务端分页 + 关键词检索的 —— 属性可以绑任意设备的点位，全量
拉回来在设备多时既慢又会把下拉撑爆。禁止项（已绑其他属性 / 读写权限
不匹配）仍逐条给出原因，而不是静默隐藏：用户需要在原地点位里判断到底
该改哪一边。属性的数据类型只作说明，不参与可选性判断。
-->
<template>
  <div class="point-binding-field">
    <div v-if="selectedPoint" class="point-binding-card">
      <span class="point-binding-mark" aria-hidden="true">
        <el-icon><PlugConnected /></el-icon>
      </span>
      <div class="point-binding-current-main">
        <span class="point-binding-device">
          {{ selectedPoint.deviceName || selectedPoint.deviceCode || '—' }}
          <span v-if="selectedPoint.deviceName && selectedPoint.deviceCode" class="point-binding-device-code">
            {{ selectedPoint.deviceCode }}
          </span>
        </span>
        <span class="point-binding-point">
          <span class="iot-mono">{{ selectedPoint.name }}</span>
          <span v-if="selectedPoint.label" class="point-binding-label">{{ selectedPoint.label }}</span>
        </span>
      </div>
      <div class="point-binding-aside">
        <el-tag
          v-if="selectedPoint.rwPermission"
          class="point-binding-access"
          size="small"
          effect="plain"
          :type="selectedPoint.rwPermission === 'R' ? 'info' : 'warning'"
        >
          {{ accessLabel(selectedPoint.rwPermission) }}
        </el-tag>
        <el-button class="point-binding-action" @click="openPicker">
          {{ t('thingModel.bindPointsChange') }}
        </el-button>
      </div>
    </div>
    <div v-else class="point-binding-card is-empty">
      <span class="point-binding-mark is-empty" aria-hidden="true">
        <el-icon><Plus /></el-icon>
      </span>
      <p class="point-binding-empty">{{ t('thingModel.bindPointsEmptyValue') }}</p>
      <el-button
        class="point-binding-action"
        type="primary"
        :disabled="!ready"
        @click="openPicker"
      >
        {{ t('thingModel.bindPointsSelect') }}
      </el-button>
    </div>
  </div>

  <el-dialog
    v-model="pickerVisible"
    class="iot-dialog point-picker-dialog"
    :title="t('thingModel.pointPickerTitle')"
    width="min(1000px, 94vw)"
    append-to-body
    :close-on-click-modal="false"
  >
    <!-- 页头三级递进：eyebrow → 标题 + 目标约束 → 一句规则说明 -->
    <template #header>
      <div class="point-picker-head">
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('thingModel.pointPickerEyebrow') }}
        </p>
        <div class="point-picker-title-row">
          <h2 class="point-picker-title">{{ t('thingModel.pointPickerTitle') }}</h2>
          <el-tag
            v-if="accessMode"
            size="small"
            effect="plain"
            class="point-picker-target-tag"
            :type="accessMode === 'R' ? 'info' : 'warning'"
          >
            {{ accessLabel(accessMode) }}
          </el-tag>
        </div>
        <p class="point-picker-lead">{{ t('thingModel.pointPickerLead') }}</p>
      </div>
    </template>

    <div class="point-picker">
      <!-- 第一级：设备 -->
      <section class="point-picker-pane is-device">
        <header class="point-picker-pane-head">
          <span class="point-picker-step" aria-hidden="true">01</span>
          <h3 class="point-picker-pane-title">{{ t('thingModel.pointPickerStepDevice') }}</h3>
          <span v-if="deviceTotal > 0" class="point-picker-pane-meta">
            {{ deviceTotal }} {{ t('thingModel.pointPickerDeviceUnit') }}
          </span>
        </header>
        <div class="point-picker-search">
          <el-input
            v-model="deviceKeyword"
            :placeholder="t('thingModel.pointPickerDeviceSearch')"
            clearable
            @keyup.enter="handleDeviceSearch"
            @clear="handleDeviceSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-button type="primary" @click="handleDeviceSearch">{{ t('common.search') }}</el-button>
        </div>
        <el-table
          :data="devices"
          v-loading="deviceLoading"
          row-key="id"
          height="380"
          highlight-current-row
          :row-class-name="deviceRowClass"
          @row-click="selectDevice"
        >
          <el-table-column :label="t('thingModel.pointDevice')" min-width="150">
            <template #default="{ row }">
              <div class="point-picker-device">
                <span class="point-picker-device-name" :title="row.name">{{ row.name }}</span>
                <span class="point-picker-device-code" :title="row.code">{{ row.code }}</span>
              </div>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.pointCount')" width="84" align="center">
            <template #default="{ row }">{{ row.pointCount }}</template>
          </el-table-column>
          <template #empty>
            <div class="iot-empty is-compact">
              <p class="iot-empty-title">{{ t('thingModel.pointPickerDeviceEmpty') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.pointPickerDeviceEmptyHint') }}</p>
            </div>
          </template>
        </el-table>
        <el-pagination
          v-if="deviceTotal > 0"
          v-model:current-page="devicePage"
          :page-size="devicePageSize"
          :total="deviceTotal"
          layout="total, prev, pager, next"
          small
          background
          @current-change="handleDeviceCurrentChange"
        />
      </section>

      <!-- 第二级：所选设备的点位 -->
      <section class="point-picker-pane is-point">
        <header class="point-picker-pane-head">
          <span class="point-picker-step" aria-hidden="true">02</span>
          <h3 class="point-picker-pane-title">{{ t('thingModel.pointPickerStepPoint') }}</h3>
          <span v-if="selectedDevice" class="point-picker-device-current" :title="selectedDevice.code">
            <span class="point-picker-device-dot" aria-hidden="true" />
            <span class="point-picker-device-current-name">{{ selectedDevice.name }}</span>
          </span>
        </header>
        <template v-if="selectedDeviceId">
          <div class="point-picker-search">
            <el-input
              v-model="pointKeyword"
              :placeholder="t('thingModel.pointPickerPointSearch')"
              clearable
              @keyup.enter="handlePointSearch"
              @clear="handlePointSearch"
            >
              <template #prefix>
                <el-icon><Search /></el-icon>
              </template>
            </el-input>
            <el-button type="primary" @click="handlePointSearch">{{ t('common.search') }}</el-button>
          </div>
          <el-table
            :data="points"
            v-loading="pointLoading"
            row-key="addressId"
            height="380"
            :row-class-name="pointRowClass"
          >
            <el-table-column :label="t('thingModel.pointAddress')" min-width="110">
              <template #default="{ row }">
                <span class="iot-mono" :title="row.name">{{ row.name }}</span>
              </template>
            </el-table-column>
            <el-table-column :label="t('thingModel.pointLabel')" min-width="100" show-overflow-tooltip>
              <template #default="{ row }">{{ row.label || '—' }}</template>
            </el-table-column>
            <el-table-column :label="t('thingModel.pointDataType')" width="88" align="center">
              <template #default="{ row }">
                <span :title="row.dataType">{{ row.commonDataType || row.dataType }}</span>
              </template>
            </el-table-column>
            <el-table-column :label="t('thingModel.access.label')" width="72" align="center">
              <template #default="{ row }">
                <el-tag
                  size="small"
                  effect="plain"
                  :type="row.rwPermission === 'R' ? 'info' : 'warning'"
                >
                  {{ accessLabel(row.rwPermission) }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column :label="t('common.status')" width="72" align="center">
              <template #default="{ row }">
                <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
                  {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column :label="t('common.action')" width="84" align="center" fixed="right">
              <template #default="{ row }">
                <el-button
                  v-if="isCurrentPoint(row)"
                  link
                  type="danger"
                  @click="unbindPoint"
                >
                  {{ t('thingModel.pointPickerUnbind') }}
                </el-button>
                <el-tooltip
                  v-else-if="disabledReason(row)"
                  :content="disabledReason(row)"
                  placement="top"
                >
                  <span>
                    <el-button link type="primary" disabled>{{ t('thingModel.pointPickerBind') }}</el-button>
                  </span>
                </el-tooltip>
                <el-button v-else link type="primary" @click="choosePoint(row)">
                  {{ t('thingModel.pointPickerBind') }}
                </el-button>
              </template>
            </el-table-column>
            <template #empty>
              <div class="iot-empty is-compact">
                <p class="iot-empty-title">{{ t('thingModel.pointPickerPointEmpty') }}</p>
              </div>
            </template>
          </el-table>
          <el-pagination
            v-if="pointTotal > 0"
            v-model:current-page="pointPage"
            :page-size="pointPageSize"
            :total="pointTotal"
            layout="total, prev, pager, next"
            small
            background
            @current-change="handlePointCurrentChange"
          />
        </template>
        <div v-else class="point-picker-placeholder">
          <span class="point-picker-placeholder-mark" aria-hidden="true">
            <el-icon><ListDetails /></el-icon>
          </span>
          <p class="iot-empty-title">{{ t('thingModel.pointPickerSelectDevice') }}</p>
          <p class="iot-empty-hint">{{ t('thingModel.pointPickerSelectDeviceHint') }}</p>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="point-picker-footer">
        <span v-if="selectedPoint" class="point-picker-current">
          <span class="point-picker-current-dot" aria-hidden="true" />
          {{ t('thingModel.pointPickerSelected') }}
          <span class="iot-mono">{{ selectedPoint.name }}</span>
          <span v-if="selectedPoint.deviceName" class="point-picker-current-device">
            · {{ selectedPoint.deviceName }}
          </span>
        </span>
        <span v-else class="iot-cell-muted">{{ t('thingModel.bindPointsEmptyValue') }}</span>
        <div class="point-picker-footer-actions">
          <el-button v-if="selectedPoint" type="danger" plain @click="unbindPoint">
            {{ t('thingModel.pointPickerUnbind') }}
          </el-button>
          <el-button type="primary" @click="pickerVisible = false">{{ t('common.confirm') }}</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  IconListDetails as ListDetails,
  IconPlugConnected as PlugConnected,
  IconPlus as Plus,
  IconSearch as Search,
} from '@tabler/icons-vue'
import {
  pageThingModelPointDevices,
  pageThingModelPointOptions,
} from '@/api/modules/thingModel'

const props = defineProps({
  /** 当前绑定（0 或 1 个 addressId；数组是为了与表单字段 addressIds 同形） */
  modelValue: { type: Array, default: () => [] },
  /** 当前编辑的属性 id；新建时为空串（用于区分「本属性原有绑定」） */
  propertyId: { type: String, default: '' },
  accessMode: { type: String, default: 'R' },
  /** 编辑态已绑定的点位（listThingModelPropertyPoints 的返回），用于首屏展示 */
  boundPoints: { type: Array, default: () => [] },
  /** 编辑态绑定数据是否已就绪；未就绪时禁止打开选择器，保存也不改绑定 */
  ready: { type: Boolean, default: true },
  /** 外层属性弹窗是否打开；关闭时不要因父表单换行触发的 access 变化做剪枝 */
  active: { type: Boolean, default: true },
})

const emit = defineEmits(['update:modelValue'])

const { t } = useI18n()

const accessLabel = (mode) => (mode ? t(`thingModel.access.${mode}`) : '—')

// ---------- 当前选中 ----------
const selectedPoint = ref(null)
// 打开选择器时要恢复「当前绑定所在设备」的点位页，所以设备 id 与选中状态同层。
const selectedDeviceId = ref('')

watch(
  () => props.boundPoints,
  (rows) => {
    if (rows && rows.length > 0) {
      selectedPoint.value = rows[0]
      selectedDeviceId.value = rows[0].deviceId
      return
    }
    // 表单关闭/重置时父组件清空 seed，这里同步清掉当前绑定展示。
    selectedPoint.value = null
    selectedDeviceId.value = ''
  },
  { immediate: true },
)

// 表单里改 access 时后端也会拒绝不兼容的绑定：已选的新点位直接从选择中
// 移除并提示，避免用户点保存才看到 41017。kind 只作说明，不参与剪枝。
watch(() => props.accessMode, () => {
  if (!props.active) return
  const p = selectedPoint.value
  if (!p || isOriginal(p) || !disabledReason(p)) return
  selectedPoint.value = null
  emit('update:modelValue', [])
  ElMessage.warning(t('thingModel.bindPointsPruned'))
})

function isOriginal(p) {
  return !!props.propertyId && p.propertyId === props.propertyId
}

function isBoundElsewhere(p) {
  return !!p.propertyId && p.propertyId !== props.propertyId
}

// 与后端 accessCovers 同一规则（R 只覆盖 R，W 只覆盖 W，RW 全覆盖）。
function accessCovers(addressRW, propertyAccess) {
  switch (addressRW) {
    case 'RW':
      return true
    case 'R':
      return propertyAccess === 'R'
    case 'W':
      return propertyAccess === 'W'
    default:
      return false
  }
}

function boundElsewhereLabel(p) {
  const propertyName = p.propertyName || p.propertyIdentifier || p.propertyId
  const boundLabel = p.modelName ? `${p.modelName} / ${propertyName}` : propertyName
  return t('thingModel.pointBoundElsewhereShort', { name: boundLabel })
}

// 返回空串表示可选；非空时是禁用原因（用作按钮 tooltip）。
function disabledReason(p) {
  // 当前属性自己的绑定点位始终可选，用户改权限后仍要能解绑。
  if (isOriginal(p)) return ''
  if (isBoundElsewhere(p)) return boundElsewhereLabel(p)
  // 数据类型只是属性的说明，不限制点位选择；点位类型仍在表格里展示供参考。
  if (!accessCovers(p.rwPermission, props.accessMode)) {
    return t(`thingModel.pointAccessMismatch.${props.accessMode}`, {
      point: accessLabel(p.rwPermission),
    })
  }
  return ''
}

function isCurrentPoint(row) {
  return !!selectedPoint.value && selectedPoint.value.addressId === row.addressId
}

function choosePoint(row) {
  if (disabledReason(row)) return
  selectedPoint.value = row
  emit('update:modelValue', [row.addressId])
}

function unbindPoint() {
  selectedPoint.value = null
  emit('update:modelValue', [])
}

// 外部（父表单 reset/剪枝）清空绑定时同步展示。
watch(
  () => props.modelValue,
  (ids) => {
    const id = Array.isArray(ids) && ids.length > 0 ? ids[0] : ''
    if (!id) {
      selectedPoint.value = null
      return
    }
    if (selectedPoint.value && selectedPoint.value.addressId === id) return
    const found =
      points.value.find((p) => p.addressId === id) ||
      props.boundPoints.find((p) => p.addressId === id)
    if (found) selectedPoint.value = found
  },
)

// ---------- 设备分页 ----------
const pickerVisible = ref(false)
const devices = ref([])
const deviceTotal = ref(0)
const devicePage = ref(1)
const devicePageSize = 10
const deviceKeyword = ref('')
const deviceLoading = ref(false)
let deviceToken = 0

const selectedDevice = computed(() =>
  devices.value.find((d) => d.id === selectedDeviceId.value),
)

async function fetchDevices() {
  const token = ++deviceToken
  deviceLoading.value = true
  try {
    const res = await pageThingModelPointDevices({
      page: devicePage.value,
      size: devicePageSize,
      keyword: deviceKeyword.value.trim(),
    })
    if (token !== deviceToken) return
    devices.value = res?.records ?? []
    deviceTotal.value = res?.total ?? 0
  } catch {
    if (token === deviceToken) {
      devices.value = []
      deviceTotal.value = 0
    }
  } finally {
    if (token === deviceToken) deviceLoading.value = false
  }
}

function handleDeviceSearch() {
  devicePage.value = 1
  fetchDevices()
}

function handleDeviceCurrentChange(page) {
  devicePage.value = page
  fetchDevices()
}

function selectDevice(row) {
  if (!row || selectedDeviceId.value === row.id) return
  selectedDeviceId.value = row.id
  pointKeyword.value = ''
  pointPage.value = 1
  fetchPoints()
}

function deviceRowClass({ row }) {
  return row.id === selectedDeviceId.value ? 'is-current' : ''
}

// ---------- 点位分页 ----------
const points = ref([])
const pointTotal = ref(0)
const pointPage = ref(1)
const pointPageSize = 10
const pointKeyword = ref('')
const pointLoading = ref(false)
let pointToken = 0

async function fetchPoints() {
  if (!selectedDeviceId.value) {
    points.value = []
    pointTotal.value = 0
    return
  }
  const token = ++pointToken
  pointLoading.value = true
  try {
    const res = await pageThingModelPointOptions({
      deviceId: selectedDeviceId.value,
      page: pointPage.value,
      size: pointPageSize,
      keyword: pointKeyword.value.trim(),
    })
    if (token !== pointToken) return
    points.value = res?.records ?? []
    pointTotal.value = res?.total ?? 0
  } catch {
    if (token === pointToken) {
      points.value = []
      pointTotal.value = 0
    }
  } finally {
    if (token === pointToken) pointLoading.value = false
  }
}

function handlePointSearch() {
  pointPage.value = 1
  fetchPoints()
}

function handlePointCurrentChange(page) {
  pointPage.value = page
  fetchPoints()
}

function pointRowClass({ row }) {
  if (isCurrentPoint(row)) return 'is-current'
  // 已绑其他属性 / 类型或权限不符的行压暗，让「可绑定」的行自然浮出来
  return disabledReason(row) ? 'is-unavailable' : ''
}

// ---------- 打开选择器 ----------
async function openPicker() {
  if (!props.ready) return
  pickerVisible.value = true
  // 每次打开都刷新一次设备页：设备/点位可能刚在其它页面改过。
  fetchDevices()
  if (selectedPoint.value?.deviceId) {
    if (selectedDeviceId.value !== selectedPoint.value.deviceId) {
      selectedDeviceId.value = selectedPoint.value.deviceId
      pointPage.value = 1
      pointKeyword.value = ''
    }
    fetchPoints()
  } else if (selectedDeviceId.value) {
    fetchPoints()
  }
}
</script>

<style scoped>
/* ---------- 表单内触发区：一张小卡片，绑定态与空态同构 ---------- */
.point-binding-field {
  width: 100%;
}

.point-binding-card {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 9px 12px;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: linear-gradient(180deg, #fff 0%, #fbfcff 100%);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  transition:
    border-color 0.18s ease,
    box-shadow 0.18s ease;
}

.point-binding-card:hover {
  border-color: var(--brand-line);
  box-shadow: 0 6px 18px -12px rgba(37, 99, 235, 0.35);
}

.point-binding-card.is-empty {
  border-style: dashed;
  background: #fbfcfe;
  box-shadow: none;
}

.point-binding-card.is-empty:hover {
  border-color: var(--brand-line);
  background: var(--brand-tint);
}

.point-binding-mark {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  font-size: 17px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 10px;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.9);
}

.point-binding-mark.is-empty {
  color: var(--ink-4);
  background: #f4f6fa;
  border-color: var(--line);
  border-style: dashed;
  box-shadow: none;
}

.point-binding-current-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.point-binding-device {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.point-binding-device-code {
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 400;
  color: var(--ink-4);
}

.point-binding-point {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  font-size: 12.5px;
  color: var(--ink-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.point-binding-label {
  color: var(--ink-3);
}

.point-binding-aside {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
}

.point-binding-access.el-tag {
  border-radius: 999px;
  font-weight: 500;
}

.point-binding-action.el-button {
  margin-left: 0;
}

.point-binding-empty {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 13px;
  color: var(--ink-4);
}

/* ---------- 弹窗页头：eyebrow → 标题 + 目标约束 → 规则说明 ---------- */
.point-picker-head {
  padding-right: 32px;
}

.point-picker-title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.point-picker-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.point-picker-target-tag {
  font-weight: 500;
}

.point-picker-lead {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

/* ---------- 两级选择：两张浮在白卡上的面板 ---------- */
.point-picker {
  display: grid;
  grid-template-columns: minmax(250px, 0.8fr) minmax(0, 1.6fr);
  gap: 16px;
}

.point-picker-pane {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  padding: 14px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow:
    0 1px 2px rgba(15, 23, 42, 0.04),
    0 16px 36px -28px rgba(15, 23, 42, 0.25);
}

.point-picker-pane-head {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 28px;
}

.point-picker-step {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  font-weight: 700;
  letter-spacing: 0.02em;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 9px;
}

.point-picker-pane-title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 13.5px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.point-picker-pane-meta {
  flex: none;
  font-size: 11.5px;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.point-picker-device-current {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: 6px;
  max-width: 58%;
  padding: 3px 10px;
  font-size: 12px;
  color: var(--ink-2);
  background: #f5f8fd;
  border: 1px solid var(--line-soft);
  border-radius: 999px;
}

.point-picker-device-dot {
  flex: none;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.12);
}

.point-picker-device-current-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 搜索行：输入框退成灰底，聚焦时提亮，与表格连成一块而不是并排三个控件 */
.point-picker-search {
  display: flex;
  gap: 8px;
}

.point-picker-search :deep(.el-input) {
  --el-input-border-radius: 9px;
}

.point-picker-search :deep(.el-input__wrapper) {
  background: #f8fafc;
  box-shadow: inset 0 0 0 1px var(--line);
  transition:
    background 0.18s ease,
    box-shadow 0.18s ease;
}

.point-picker-search :deep(.el-input__wrapper:hover) {
  box-shadow: inset 0 0 0 1px var(--brand-line);
}

.point-picker-search :deep(.el-input__wrapper.is-focus) {
  background: #fff;
  box-shadow: inset 0 0 0 1.5px var(--brand);
}

.point-picker-search :deep(.el-input__prefix) {
  color: var(--ink-4);
}

.point-picker-search :deep(.el-button) {
  border-radius: 9px;
}

/* 表格：去竖线、浅灰表头、发丝行线；选中行左侧留一道品牌色书脊 */
.point-picker :deep(.el-table) {
  --el-table-border-color: transparent;
  --el-table-header-bg-color: #f8fafc;
  --el-table-row-hover-bg-color: #f4f8ff;
  --el-table-current-row-bg-color: transparent;
  font-size: 12.5px;
  color: var(--ink-2);
}

.point-picker :deep(.el-table__inner-wrapper::before) {
  display: none;
}

.point-picker :deep(th.el-table__cell) {
  height: 34px;
  padding: 0;
  background: #f8fafc;
  color: var(--ink-3);
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.03em;
  border-bottom: 1px solid var(--line);
}

.point-picker :deep(tr.el-table__row td.el-table__cell) {
  padding: 8px 0;
  border-bottom: 1px solid var(--line-soft);
}

.point-picker :deep(.el-table__body tr:last-child td.el-table__cell) {
  border-bottom: none;
}

.point-picker :deep(.cell) {
  padding: 0 10px;
}

.point-picker-pane.is-device :deep(tr.el-table__row) {
  cursor: pointer;
}

/* 同一行所有单元格共用同一条纵向渐变：横向无断层，滚动时固定列也不露白 */
.point-picker :deep(.el-table__row.is-current > td.el-table__cell) {
  background: linear-gradient(180deg, #f3f7ff 0%, #edf4ff 100%);
}

.point-picker :deep(.el-table__row.is-current > td.el-table__cell:first-child) {
  box-shadow: inset 2px 0 0 var(--brand);
}

.point-picker :deep(.el-table__row.is-unavailable > td.el-table__cell) {
  color: var(--ink-4);
}

.point-picker :deep(.el-table__row.is-unavailable .iot-mono) {
  color: var(--ink-4);
}

.point-picker :deep(.iot-empty) {
  padding: 30px 12px;
}

.point-picker-pane :deep(.el-pagination) {
  justify-content: flex-end;
}

.point-picker-device {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.point-picker-device-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink);
  font-weight: 500;
}

.point-picker-device-code {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--ink-4);
}

.point-picker :deep(.el-table__row.is-current .point-picker-device-name) {
  color: var(--brand);
}

/* 未选设备时的安静占位：柔光 + 一枚渐变图章，不再是光秃秃的虚线框 */
.point-picker-placeholder {
  display: flex;
  flex: 1;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 444px;
  padding: 24px;
  text-align: center;
  background:
    radial-gradient(circle at 50% 34%, rgba(37, 99, 235, 0.07), transparent 62%),
    #fbfcfe;
  border: 1px dashed var(--line);
  border-radius: 12px;
}

.point-picker-placeholder-mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 60px;
  height: 60px;
  margin-bottom: 10px;
  font-size: 26px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 18px;
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.9),
    0 14px 30px -20px rgba(37, 99, 235, 0.55);
}

.point-picker-placeholder .iot-empty-title {
  margin: 0;
  color: var(--ink-2);
}

.point-picker-placeholder .iot-empty-hint {
  margin-top: 6px;
}

/* ---------- 页脚：当前绑定做成胶囊，和页头约束标签同一种语汇 ---------- */
.point-picker-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.point-picker-current {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  max-width: 62%;
  padding: 4px 12px 4px 10px;
  font-size: 12.5px;
  color: var(--ink-3);
  background: var(--brand-tint);
  border: 1px solid var(--brand-line);
  border-radius: 999px;
}

.point-picker-current-dot {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.12);
}

.point-picker-current .iot-mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--ink);
}

.point-picker-current-device {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink-4);
}

.point-picker-footer-actions {
  display: flex;
  flex: none;
  gap: 8px;
}

@media (max-width: 960px) {
  .point-picker {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
