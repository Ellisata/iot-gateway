<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  采集组是设备工作区的一个视图：组持有采集周期，绑定到组的点位按组周期采集，
  推送侧按 (设备, 组) 分帧。未分组点位继续按自身 scan_frequency 采集。
-->
<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <button type="button" class="iot-back" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          {{ t('collectorGroup.backToList') }}
        </button>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.collectorGroup') }}
        </p>
        <h1 class="iot-title">{{ t('collectorGroup.title') }} — {{ deviceObjectName }}</h1>
        <p class="iot-sub">{{ t('collectorGroup.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ total }}</span>
          <span class="iot-stat-label">{{ t('collectorGroup.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <DeviceWorkspaceTabs
      :device-object-id="deviceObjectId"
      :device-object-name="deviceObjectName"
      :protocol-name="protocolName"
    />

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('collectorGroup.searchPlaceholder')"
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
          <el-button type="primary" :icon="Plus" @click="handleAdd">
            {{ t('collectorGroup.add') }}
          </el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" row-key="id" style="width: 100%">
        <el-table-column :label="t('collectorGroup.name')" min-width="180">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><LayersLinked /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('collectorGroup.code')" width="160">
          <template #default="{ row }">
            <span class="iot-code-text" :title="row.code">{{ row.code || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('collectorGroup.interval')" width="150" align="center">
          <template #default="{ row }">{{ row.intervalMs }} ms</template>
        </el-table-column>
        <el-table-column :label="t('collectorGroup.pointCount')" width="120" align="center">
          <template #default="{ row }">{{ row.pointCount ?? 0 }}</template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-rose'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('common.description')"
          min-width="180"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column
          prop="updatedAt"
          :label="t('common.updatedAt')"
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
              <el-icon><LayersLinked /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('collectorGroup.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('collectorGroup.emptyHint') }}</p>
            <el-button type="primary" :icon="Plus" @click="handleAdd">
              {{ t('collectorGroup.add') }}
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

    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog"
      :title="isEdit ? t('collectorGroup.editTitle') : t('collectorGroup.addTitle')"
      width="560px"
      :close-on-click-modal="false"
      @close="resetForm"
    >
      <el-form ref="formRef" :model="formData" :rules="formRules" label-width="110px" status-icon>
        <el-form-item :label="t('collectorGroup.code')" prop="code">
          <el-input
            v-model="formData.code"
            :placeholder="t('collectorGroup.codePlaceholder')"
            :disabled="isEdit && !!formData.code"
            maxlength="64"
            show-word-limit
          />
          <div class="iot-form-hint is-block">
            {{ isEdit && formData.code ? t('collectorGroup.codeImmutableHint') : t('collectorGroup.codeHint') }}
          </div>
        </el-form-item>
        <el-form-item :label="t('collectorGroup.name')" prop="name">
          <el-input
            v-model="formData.name"
            :placeholder="t('collectorGroup.namePlaceholder')"
            maxlength="64"
            show-word-limit
          />
          <div class="iot-form-hint is-block">{{ t('collectorGroup.nameHint') }}</div>
        </el-form-item>
        <el-form-item :label="t('collectorGroup.interval')" prop="intervalMs">
          <el-select v-model="formData.intervalMs" style="width: 100%">
            <el-option v-for="item in intervalOptions" :key="item.value" :label="item.label" :value="item.value" />
          </el-select>
          <div class="iot-form-hint is-block">{{ t('collectorGroup.intervalHint') }}</div>
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            type="textarea"
            :rows="3"
            maxlength="500"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('common.status')" prop="status">
          <el-switch
            v-model="formData.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
          <div class="iot-form-hint is-block">{{ t('collectorGroup.statusHint') }}</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconArrowLeft as ArrowLeft,
  IconLayersLinked as LayersLinked,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import {
  pageCollectorGroups,
  createCollectorGroup,
  updateCollectorGroup,
  deleteCollectorGroup,
} from '@/api/modules/collectorGroup'
import { useScanFrequencyOptions } from '@/composables/useScanFrequencyOptions'
import DeviceWorkspaceTabs from '@/components/DeviceWorkspaceTabs.vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const deviceObjectId = route.params.deviceObjectId
const deviceObjectName = route.query.deviceObjectName || ''
const protocolName = computed(() => route.query.protocolName || '')

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

const intervalOptions = useScanFrequencyOptions()

// ---------- 列表 ----------
const loading = ref(false)
const tableData = ref([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)
const queryForm = reactive({ name: '' })
const appliedName = ref('')

async function fetchList() {
  loading.value = true
  try {
    const res = await pageCollectorGroups({
      deviceId: deviceObjectId,
      page: currentPage.value,
      size: pageSize.value,
      name: appliedName.value || undefined,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 错误提示由响应拦截器统一给出
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  appliedName.value = queryForm.name.trim()
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  appliedName.value = ''
  currentPage.value = 1
  fetchList()
}

function handlePageChange() {
  fetchList()
}

function handleSizeChange() {
  currentPage.value = 1
  fetchList()
}

// ---------- 新增 / 编辑 ----------
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)
const isEdit = computed(() => !!formData.id)

const formData = reactive({
  id: '',
  code: '',
  name: '',
  intervalMs: 1000,
  description: '',
  status: 1,
})

const formRules = {
  code: [
    { required: true, message: () => t('collectorGroup.codeRequired'), trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_-]{0,63}$/,
      message: () => t('collectorGroup.codeInvalid'),
      trigger: 'blur',
    },
    {
      validator: (_rule, value, callback) => {
        if (String(value || '').trim().toLowerCase() === 'legacy') {
          return callback(new Error(t('collectorGroup.codeInvalid')))
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
  name: [
    { required: true, message: () => t('collectorGroup.nameRequired'), trigger: 'blur' },
    { min: 1, max: 64, message: () => t('collectorGroup.nameLength'), trigger: 'blur' },
    {
      validator: (_rule, value, callback) => {
        const name = String(value || '').trim()
        if (!name) return callback()
        if (name.toLowerCase() === 'legacy' || /[/+#]/.test(name)) {
          return callback(new Error(t('collectorGroup.nameInvalid')))
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
  intervalMs: [{ required: true, message: () => t('collectorGroup.intervalRequired'), trigger: 'change' }],
}

function handleAdd() {
  resetForm()
  dialogVisible.value = true
}

function handleEdit(row) {
  formData.id = row.id
  formData.code = row.code || ''
  formData.name = row.name
  formData.intervalMs = row.intervalMs
  formData.description = row.description || ''
  formData.status = row.status
  dialogVisible.value = true
}

function resetForm() {
  formData.id = ''
  formData.code = ''
  formData.name = ''
  formData.intervalMs = 1000
  formData.description = ''
  formData.status = 1
  formRef.value?.clearValidate()
}

async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    const payload = {
      code: formData.code.trim(),
      name: formData.name.trim(),
      intervalMs: formData.intervalMs,
      description: formData.description.trim(),
    }
    if (isEdit.value) {
      payload.id = formData.id
      payload.status = formData.status
      await updateCollectorGroup(payload)
      ElMessage.success(t('collectorGroup.editSuccess'))
    } else {
      payload.deviceId = deviceObjectId
      await createCollectorGroup(payload)
      ElMessage.success(t('collectorGroup.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch {
    // 失败原因由响应拦截器统一提示；保留弹窗让用户修正
  } finally {
    submitting.value = false
  }
}

// ---------- 删除 ----------
async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('collectorGroup.deleteConfirm', { name: row.name }),
      t('collectorGroup.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
    await deleteCollectorGroup(row.id)
    ElMessage.success(t('collectorGroup.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消，或服务端拒绝删除（仍有绑定点位）—— 原因由拦截器提示
  }
}

fetchList()
</script>

<style scoped>
/* 编码是主题里的稳定段：等宽小字，避免与组名的视觉权重打架 */
.iot-code-text {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.4;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}
</style>
