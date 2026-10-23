<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.subscription') }}
        </p>
        <h1 class="iot-title">{{ t('menu.subscriptionDevicePoint') }}</h1>
        <p class="iot-sub">{{ t('subscriptionDevicePoint.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('subscriptionDevicePoint.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：筛选在左、动作在右，表格与分页共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters point-filters">
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceId') }}</span>
            <el-input
              v-model="query.deviceId"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceName') }}</span>
            <el-input
              v-model="query.deviceName"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceAddressId') }}</span>
            <el-input
              v-model="query.deviceAddressId"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field">
            <span class="filter-label">{{ t('subscriptionDevicePoint.fieldMaintenance') }}</span>
            <el-select
              v-model="query.source"
              clearable
              class="filter-input"
              :placeholder="t('subscriptionDevicePoint.sourceAll')"
            >
              <el-option :label="t('subscriptionDevicePoint.sourceManual')" value="manual" />
              <el-option :label="t('subscriptionDevicePoint.sourceAuto')" value="auto" />
            </el-select>
          </div>
          <div class="filter-actions">
            <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
            <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
          </div>
        </div>

        <div class="iot-toolbar-actions">
          <!-- 按钮文案带勾选数：按下确认之前就能看到"要删几条" -->
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
          <el-button :icon="Download" @click="handleDownloadTemplate">
            {{ t('common.downloadTemplate') }}
          </el-button>
          <el-button :icon="Upload" :loading="importing" @click="handleImport">
            {{ t('common.import') }}
          </el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">
            {{ t('subscriptionDevicePoint.add') }}
          </el-button>
          <input ref="fileInputRef" type="file" accept=".xlsx" class="hidden" @change="handleFileChange" />
        </div>
      </div>

      <el-table
        ref="tableRef"
        class="iot-table"
        :data="rows"
        v-loading="loading"
        :row-key="rowKey"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <!-- 项目里没用 reserve-selection：跨页保留勾选会让「已勾选 N 条」与实际
             能删到的集合不一致（翻页后看不见的行还在里面），批量删除宁可让人重勾。
             自动行不可勾选：它由报文推导，删了也会在下一条报文回来。 -->
        <el-table-column
          type="selection"
          width="44"
          align="center"
          :selectable="isRowSelectable"
        />
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column
          :label="t('subscription.fieldDeviceId')"
          min-width="130"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="iot-mono">{{ row.deviceId }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('subscription.fieldDeviceName')"
          min-width="170"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><DeviceDesktop /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.deviceName">{{ row.deviceName || '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('subscription.fieldDeviceAddressId')"
          min-width="130"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="iot-mono">{{ row.deviceAddressId }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.fieldDeviceAddressLabel')" min-width="130" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.deviceAddressLabel" class="iot-chip">{{ row.deviceAddressLabel }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.fieldKind')" width="100" align="center">
          <template #default="{ row }">
            <span v-if="row.kind" class="iot-chip">{{ row.kind }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <!-- source 同时表示维护方式：手工维护的行不会被订阅报文覆盖 -->
        <el-table-column :label="t('subscriptionDevicePoint.fieldMaintenance')" width="100" align="center">
          <template #default="{ row }">
            <span v-if="row.source === 'manual'" class="iot-chip is-manual">
              {{ t('subscriptionDevicePoint.sourceManual') }}
            </span>
            <span v-else class="iot-chip">{{ t('subscriptionDevicePoint.sourceAuto') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.action')" width="140" align="center" fixed="right">
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
              <!-- 手工行可以交还给自动收录；自动行本来就是自动同步，不需要这个入口 -->
              <el-tooltip
                v-if="row.source === 'manual'"
                :content="t('subscriptionDevicePoint.restoreAuto')"
                placement="top"
              >
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Restore"
                  :aria-label="t('subscriptionDevicePoint.restoreAuto')"
                  @click="handleRestoreAuto(row)"
                />
              </el-tooltip>
              <!-- 自动行由报文推导，删了也会在下一条报文回来，故只给手工行删除入口 -->
              <el-tooltip v-if="row.source === 'manual'" :content="t('common.delete')" placement="top">
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
              <el-icon><Point /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('subscriptionDevicePoint.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('subscriptionDevicePoint.emptyHint') }}</p>
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
          @current-change="handleCurrentChange"
        />
      </div>
    </section>

    <!-- 新增 / 编辑：自然键创建后不可改，其余元信息按提交值覆盖 -->
    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog"
      :title="isEdit ? t('subscriptionDevicePoint.editTitle') : t('subscriptionDevicePoint.addTitle')"
      width="560px"
      :close-on-click-modal="false"
    >
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="96px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('subscription.fieldDeviceId')" prop="deviceId">
          <el-input v-model="formData.deviceId" :disabled="isEdit" maxlength="128" />
          <p v-if="isEdit" class="form-hint">{{ t('subscriptionDevicePoint.naturalKeyImmutable') }}</p>
        </el-form-item>
        <el-form-item :label="t('subscription.fieldDeviceAddressId')" prop="deviceAddressId">
          <el-input v-model="formData.deviceAddressId" :disabled="isEdit" maxlength="128" />
        </el-form-item>
        <el-form-item :label="t('subscription.fieldDeviceName')" prop="deviceName">
          <el-input v-model="formData.deviceName" maxlength="100" show-word-limit />
        </el-form-item>
        <el-form-item :label="t('subscription.fieldDeviceAddressLabel')" prop="deviceAddressLabel">
          <el-input v-model="formData.deviceAddressLabel" maxlength="100" show-word-limit />
        </el-form-item>
        <el-form-item :label="t('subscription.fieldKind')" prop="kind">
          <el-select
            v-model="formData.kind"
            class="kind-select"
            clearable
            :placeholder="t('subscriptionDevicePoint.kindPlaceholder')"
          >
            <el-option
              v-for="opt in kindOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <!-- 编辑自动行等于接管维护：先把后果说清楚，别让用户事后才发现元信息不再自动更新 -->
        <el-form-item v-if="isEdit && editingSource === 'auto'">
          <p class="form-hint">{{ t('subscriptionDevicePoint.switchToManualHint') }}</p>
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          {{ t('common.save') }}
        </el-button>
      </template>
    </el-dialog>

    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />
  </div>
</template>

<script setup>
import { reactive, ref, computed, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconDeviceDesktop as DeviceDesktop,
  IconDownload as Download,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconArrowBackUp as Restore,
  IconPoint as Point,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  getSubscriptionDevicePointList,
  createSubscriptionDevicePoint,
  updateSubscriptionDevicePoint,
  deleteSubscriptionDevicePoint,
  restoreSubscriptionDevicePointAuto,
  batchDeleteSubscriptionDevicePoints,
  importSubscriptionDevicePoints,
  downloadSubscriptionDevicePointTemplate,
} from '@/api/modules/mqttSubscription'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'

const { t } = useI18n()

const loading = ref(false)
const rows = ref([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const query = reactive({
  deviceId: '',
  deviceName: '',
  deviceAddressId: '',
  source: '',
})

// 首屏还没拿到数时显示占位符而不是 0：0 个点位会被当成"还没收到任何订阅报文"
const displayTotal = computed(() =>
  loading.value && rows.value.length === 0 ? '—' : total.value
)

// ---------- 勾选与批量删除 ----------
const tableRef = ref(null)
const selectedRows = ref([])

// 自然键是两段字符串，用 JSON 数组序列化拼接，避免分隔符出现在 ID 里造成碰撞。
function rowKey(row) {
  return JSON.stringify([row.deviceId, row.deviceAddressId])
}

// 自动行不可勾选：与单条删除同一口径，只允许删手工行。
function isRowSelectable(row) {
  return row.source === 'manual'
}

function handleSelectionChange(rows) {
  selectedRows.value = rows
}

const batchDeleteLabel = computed(() =>
  selectedRows.value.length
    ? t('subscriptionDevicePoint.batchDeleteCount', { n: selectedRows.value.length })
    : t('subscriptionDevicePoint.batchDelete')
)

async function fetchData() {
  loading.value = true
  try {
    const params = { page: currentPage.value, size: pageSize.value }
    for (const key of Object.keys(query)) {
      const value = String(query[key] ?? '').trim()
      if (value) params[key] = value
    }
    const res = await getSubscriptionDevicePointList(params)
    rows.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.error('获取订阅设备与点位失败', err)
    ElMessage.error(t('subscriptionDevicePoint.fetchFailed'))
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchData()
}

function handleReset() {
  Object.keys(query).forEach((key) => {
    query[key] = ''
  })
  handleSearch()
}

function handleSizeChange() {
  currentPage.value = 1
  fetchData()
}

function handleCurrentChange() {
  fetchData()
}

// ---------- 新增 / 编辑 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
// 正在编辑的行的维护方式：编辑自动行会在弹窗里提示它将转为手工维护。
const editingSource = ref('')
const submitting = ref(false)
const formRef = ref(null)
const formData = reactive({
  deviceId: '',
  deviceAddressId: '',
  deviceName: '',
  deviceAddressLabel: '',
  kind: '',
})

const KIND_LABEL_KEYS = {
  bool: 'kindBool',
  int: 'kindInt',
  uint: 'kindUint',
  float: 'kindFloat',
  string: 'kindString',
  time: 'kindTime',
}

// 自动上报可能带来非标准的类型字符串：编辑时把它作为一项追加进去，
// 避免下拉框里找不到当前值而被静默改成空。
const kindOptions = computed(() => {
  const options = Object.keys(KIND_LABEL_KEYS).map((value) => ({
    value,
    label: t(`subscriptionDevicePoint.${KIND_LABEL_KEYS[value]}`),
  }))
  const current = String(formData.kind || '').trim()
  if (current && !KIND_LABEL_KEYS[current.toLowerCase()]) {
    options.push({ value: formData.kind, label: current })
  }
  return options
})

const formRules = {
  deviceId: [
    { required: true, message: () => t('subscriptionDevicePoint.deviceIdRequired'), trigger: 'blur' },
    { max: 128, message: () => t('subscriptionDevicePoint.idLength'), trigger: 'blur' },
  ],
  deviceAddressId: [
    { required: true, message: () => t('subscriptionDevicePoint.addressIdRequired'), trigger: 'blur' },
    { max: 128, message: () => t('subscriptionDevicePoint.idLength'), trigger: 'blur' },
  ],
  deviceName: [{ max: 100, message: () => t('subscriptionDevicePoint.textLength'), trigger: 'blur' }],
  deviceAddressLabel: [{ max: 100, message: () => t('subscriptionDevicePoint.textLength'), trigger: 'blur' }],
}

function handleAdd() {
  isEdit.value = false
  editingSource.value = ''
  Object.assign(formData, {
    deviceId: '',
    deviceAddressId: '',
    deviceName: '',
    deviceAddressLabel: '',
    kind: '',
  })
  dialogVisible.value = true
  nextTick(() => formRef.value?.clearValidate())
}

function handleEdit(row) {
  isEdit.value = true
  editingSource.value = row.source || ''
  Object.assign(formData, {
    deviceId: row.deviceId,
    deviceAddressId: row.deviceAddressId,
    deviceName: row.deviceName || '',
    deviceAddressLabel: row.deviceAddressLabel || '',
    kind: row.kind || '',
  })
  dialogVisible.value = true
  nextTick(() => formRef.value?.clearValidate())
}

async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

  submitting.value = true
  const payload = {
    deviceId: formData.deviceId.trim(),
    deviceAddressId: formData.deviceAddressId.trim(),
    deviceName: formData.deviceName.trim(),
    deviceAddressLabel: formData.deviceAddressLabel.trim(),
    kind: String(formData.kind || '').trim(),
  }
  try {
    if (isEdit.value) {
      await updateSubscriptionDevicePoint(payload)
      ElMessage.success(t('subscriptionDevicePoint.editSuccess'))
      dialogVisible.value = false
      fetchData()
    } else {
      await createSubscriptionDevicePoint(payload)
      ElMessage.success(t('subscriptionDevicePoint.addSuccess'))
      dialogVisible.value = false
      // 列表按自然键排序，新行不一定落在当前页；回第一页让它可见。
      currentPage.value = 1
      fetchData()
    }
  } catch {
    // 错误提示已由 axios 拦截器统一处理
  } finally {
    submitting.value = false
  }
}

// ---------- 删除（只对手工行开放） ----------
async function handleBatchDelete() {
  const selected = selectedRows.value
  if (!selected.length) return

  try {
    await ElMessageBox.confirm(
      t('subscriptionDevicePoint.batchDeleteConfirm', { n: selected.length }),
      t('subscriptionDevicePoint.batchDeleteTitle'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
  } catch {
    return // 用户取消
  }

  try {
    const res = await batchDeleteSubscriptionDevicePoints(
      selected.map((row) => ({
        deviceId: row.deviceId,
        deviceAddressId: row.deviceAddressId,
      }))
    )
    // deleted 是服务端的实际删除行数，可能小于勾选数：勾选后又被报文重新收录、
    // 或已被他人删掉的条目不在删除集里。少删了要说出来，不能一律报成功。
    const deleted = res?.deleted ?? 0
    const missing = selected.length - deleted
    if (missing > 0) {
      ElMessage.warning(t('subscriptionDevicePoint.batchDeletePartial', { n: deleted, missing }))
    } else {
      ElMessage.success(t('subscriptionDevicePoint.batchDeleteSuccess', { n: deleted }))
    }
    tableRef.value?.clearSelection()
    selectedRows.value = []
    // 勾选覆盖了当前页全部行时回退一页，避免停在空页
    if (selected.length === rows.value.length && currentPage.value > 1) {
      currentPage.value -= 1
    }
    fetchData()
  } catch {
    // 错误提示已由 axios 拦截器统一处理
  }
}

async function handleRestoreAuto(row) {
  const name = `${row.deviceId} / ${row.deviceAddressId}`
  try {
    await ElMessageBox.confirm(
      t('subscriptionDevicePoint.restoreAutoConfirm', { name }),
      t('subscriptionDevicePoint.restoreAutoTitle'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
  } catch {
    return // 用户取消
  }

  try {
    await restoreSubscriptionDevicePointAuto(row.deviceId, row.deviceAddressId)
    ElMessage.success(t('subscriptionDevicePoint.restoreAutoSuccess'))
    tableRef.value?.clearSelection()
    selectedRows.value = []
    fetchData()
  } catch {
    // 错误提示已由 axios 拦截器统一处理
  }
}

async function handleDelete(row) {
  const name = `${row.deviceId} / ${row.deviceAddressId}`
  try {
    await ElMessageBox.confirm(
      t('subscriptionDevicePoint.deleteConfirm', { name }),
      t('subscriptionDevicePoint.deleteTitle'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
  } catch {
    return // 用户取消
  }

  try {
    await deleteSubscriptionDevicePoint(row.deviceId, row.deviceAddressId)
    ElMessage.success(t('subscriptionDevicePoint.deleteSuccess'))
    tableRef.value?.clearSelection()
    selectedRows.value = []
    // 删掉当前页最后一条时回退一页，避免停在空页
    if (rows.value.length === 1 && currentPage.value > 1) {
      currentPage.value -= 1
    }
    fetchData()
  } catch {
    // 错误提示已由 axios 拦截器统一处理
  }
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
  importFn: importSubscriptionDevicePoints,
  downloadFn: downloadSubscriptionDevicePointTemplate,
  templateName: () => t('subscriptionDevicePoint.importTemplateName'),
  refresh: fetchData,
})

onMounted(fetchData)
</script>

<style scoped>
/* 筛选区在左、动作区在右；空间不足时各自换行 */
.point-filters {
  flex: 1 1 auto;
  align-items: flex-end;
}

.filter-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.filter-label {
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--ink-4);
  white-space: nowrap;
}

.filter-input {
  width: 164px;
}

.filter-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.iot-toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

/* 手工维护的行：品牌色轻轻点一下，不抢状态色 */
.iot-chip.is-manual {
  color: var(--brand);
  background: var(--brand-soft);
  border-color: #dbe6ff;
}

.form-hint {
  width: 100%;
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-4);
}

.kind-select {
  width: 100%;
}

@media (max-width: 900px) {
  .filter-input {
    width: 100%;
  }

  .filter-field {
    flex: 1 1 160px;
  }

  .iot-toolbar-actions {
    width: 100%;
  }
}
</style>
