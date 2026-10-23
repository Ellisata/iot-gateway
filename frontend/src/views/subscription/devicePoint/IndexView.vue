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
          <div class="filter-actions">
            <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
            <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
          </div>
        </div>

        <div class="iot-toolbar-actions">
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

      <el-table class="iot-table" :data="rows" v-loading="loading" style="width: 100%">
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
        <el-table-column :label="t('subscriptionDevicePoint.fieldSource')" width="90" align="center">
          <template #default="{ row }">
            <!-- 手工行不会被订阅报文覆盖，来源标签让这件事在列表里可见 -->
            <span v-if="row.source === 'manual'" class="iot-chip is-manual">
              {{ t('subscriptionDevicePoint.sourceManual') }}
            </span>
            <span v-else class="iot-chip">{{ t('subscriptionDevicePoint.sourceAuto') }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('common.action')"
          width="88"
          align="center"
          fixed="right"
        >
          <template #default="{ row }">
            <el-button link type="primary" :icon="EditPen" @click="handleEdit(row)">
              {{ t('common.edit') }}
            </el-button>
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
import { ElMessage } from 'element-plus'
import {
  IconDeviceDesktop as DeviceDesktop,
  IconDownload as Download,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconPoint as Point,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  getSubscriptionDevicePointList,
  createSubscriptionDevicePoint,
  updateSubscriptionDevicePoint,
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
})

// 首屏还没拿到数时显示占位符而不是 0：0 个点位会被当成"还没收到任何订阅报文"
const displayTotal = computed(() =>
  loading.value && rows.value.length === 0 ? '—' : total.value
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
