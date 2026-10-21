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
          {{ t('menu.alarm') }}
        </p>
        <h1 class="iot-title">{{ t('menu.alarmWebhook') }}</h1>
        <p class="iot-sub">{{ t('alarmWebhook.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('alarmWebhook.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：筛选、表格、分页共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('alarmWebhook.searchPlaceholder')"
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
          <el-button :icon="DataLine" @click="handleShowStatus">
            {{ t('alarmWebhook.statusHealth') }}
          </el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('alarmWebhook.add') }}</el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <!-- 名称 + 描述两行；图标底座让「一个通知出口」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('common.name')" min-width="190">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Webhook /></el-icon>
              </span>
              <div class="webhook-name-copy">
                <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
                <span v-if="row.description" class="webhook-name-desc" :title="row.description">
                  {{ row.description }}
                </span>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('alarmWebhook.type')" width="170">
          <template #default="{ row }">
            <span class="iot-chip">{{ webhookTypeLabel(row.type, row.typeName, t) }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('alarmWebhook.url')" min-width="240" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="iot-mono webhook-url">{{ row.url }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('alarmWebhook.secret')" width="120" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.hasSecret ? 'is-green' : 'is-gray'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.hasSecret ? t('alarmWebhook.hasSecret') : t('alarmWebhook.noSecret') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-gray'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.disabled') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="t('common.createdAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="140" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('alarmWebhook.testSend')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="success"
                  link
                  size="small"
                  :icon="Promotion"
                  :aria-label="t('alarmWebhook.testSend')"
                  @click="handleTestSendRow(row)"
                />
              </el-tooltip>
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
              <el-icon><Webhook /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.name
                ? t('alarmWebhook.emptySearch', { name: queryForm.name })
                : t('alarmWebhook.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name" class="iot-empty-hint">{{ t('alarmWebhook.emptyHint') }}</p>
            <el-button v-if="queryForm.name" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('alarmWebhook.add') }}
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
          @current-change="handleCurrentChange"
        />
      </div>
    </section>

    <!-- 新增 / 编辑 对话框 -->
    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog"
      :title="isEdit ? t('alarmWebhook.editTitle') : t('alarmWebhook.addTitle')"
      width="620px"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="100px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('alarmWebhook.type')" prop="type">
          <AlarmWebhookTypeSelector
            v-model="formData.type"
            :disabled="isEdit"
            @select="handleTypeSelect"
          />
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="formData.name"
            :placeholder="t('alarmWebhook.namePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            :placeholder="t('alarmWebhook.descPlaceholder')"
            type="textarea"
            :rows="2"
            maxlength="255"
            show-word-limit
          />
        </el-form-item>
        <el-form-item v-if="isEdit" :label="t('common.status')" prop="status">
          <el-switch
            v-model="formData.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.disabled')"
          />
        </el-form-item>
      </el-form>

      <!-- 动态通知配置表单：按所选类型加载。用发丝线接续基本信息，不再用 el-divider -->
      <div v-if="formData.type" class="webhook-section">
        <div class="webhook-section-head">
          <div class="webhook-section-headline">
            <span class="webhook-section-mark" aria-hidden="true">
              <el-icon><Webhook /></el-icon>
            </span>
            <div>
              <p class="webhook-section-name">{{ t('alarmWebhook.notifyConfig') }}</p>
              <p class="webhook-section-sub">{{ t('alarmWebhook.notifyParams') }}</p>
            </div>
          </div>
          <div class="webhook-test-controls">
            <el-select v-model="testAlarmType" size="small" class="webhook-test-type">
              <el-option :label="t('alarmWebhook.testOffline')" value="offline" />
              <el-option :label="t('alarmWebhook.testRecover')" value="recover" />
            </el-select>
            <el-button
              type="primary"
              link
              :loading="testing"
              @click="handleTestSendForm"
            >
              {{ t('alarmWebhook.testSend') }}
            </el-button>
          </div>
        </div>
        <div v-loading="dynamicFormLoading" class="dynamic-form-wrapper">
          <p
            v-if="!dynamicFormLoading && !dynamicFormRule.length"
            class="webhook-empty-config"
          >
            {{ t('alarmWebhook.noNotifyConfig') }}
          </p>
          <template v-else>
            <!-- 密钥不回显，编辑时留空即保持原值；这条提示否则会被误解为「已清空」 -->
            <p v-if="isEdit && formData.hasSecret" class="webhook-secret-hint">
              {{ t('alarmWebhook.secretKeepHint') }}
            </p>
            <form-create
              :key="dynamicFormKey"
              :rule="dynamicFormRule"
              :option="dynamicFormOptions"
              @mounted="handleDynamicFormMounted"
            />
            <!-- 留空=保持原值，那「清空密钥」就需要一个显式出口，否则密钥一旦设过就再也去不掉 -->
            <el-checkbox
              v-if="isEdit && formData.hasSecret && hasSecretField"
              v-model="clearSecret"
              class="webhook-clear-secret"
            >
              {{ t('alarmWebhook.clearSecret') }}
            </el-checkbox>
          </template>
        </div>
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 运行状态对话框 -->
    <el-dialog
      v-model="statusVisible"
      class="iot-dialog"
      :title="t('alarmWebhook.healthTitle')"
      width="900px"
    >
      <div v-loading="statusLoading">
        <!-- 通知器与入口队列读数：发丝线格栅，与实时数据页的状态条同一套 -->
        <div class="status-strip">
          <div class="status-item">
            <p class="status-label">{{ t('alarmWebhook.notifier') }}</p>
            <div class="status-value">
              <span class="iot-pill" :class="status.running ? 'is-green' : 'is-gray'">
                <span class="iot-pill-dot" aria-hidden="true" />
                {{ status.running ? t('alarmWebhook.running') : t('alarmWebhook.stopped') }}
              </span>
            </div>
          </div>
          <div class="status-item">
            <p class="status-label">{{ t('alarmWebhook.ingressDepth') }}</p>
            <p class="status-value">{{ status.ingressDepth }}</p>
          </div>
          <div class="status-item">
            <p class="status-label">{{ t('alarmWebhook.ingressDropped') }}</p>
            <p class="status-value">{{ status.ingressDropped }}</p>
          </div>
        </div>

        <el-table
          v-if="status.webhooks.length"
          class="iot-mini-table health-table"
          :data="status.webhooks"
          style="width: 100%"
        >
          <el-table-column prop="name" :label="t('common.name')" min-width="120" show-overflow-tooltip />
          <el-table-column :label="t('alarmWebhook.type')" width="150">
            <template #default="{ row }">
              {{ webhookTypeLabel(row.type, row.type, t) }}
            </template>
          </el-table-column>
          <el-table-column prop="queueDepth" :label="t('alarmWebhook.queueDepth')" width="90" align="center" />
          <el-table-column prop="sentCount" :label="t('alarmWebhook.sentCount')" width="90" align="center" />
          <el-table-column prop="failedCount" :label="t('alarmWebhook.failedCount')" width="80" align="center" />
          <el-table-column prop="droppedCount" :label="t('alarmWebhook.droppedCount')" width="80" align="center" />
          <el-table-column prop="lastSuccessTime" :label="t('alarmWebhook.lastSuccessTime')" width="170" class-name="iot-col-time" />
          <el-table-column prop="error" :label="t('alarmWebhook.lastError')" min-width="180" show-overflow-tooltip />
        </el-table>

        <p v-if="!statusLoading && !status.webhooks.length" class="webhook-empty">
          {{ t('alarmWebhook.noWebhook') }}
        </p>
      </div>

      <template #footer>
        <el-button @click="handleRefresh">{{ t('alarmWebhook.refresh') }}</el-button>
        <el-button type="primary" @click="statusVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconChartLine as DataLine,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconSend as Promotion,
  IconTrash as Delete,
  IconWebhook as Webhook,
} from '@tabler/icons-vue'
import {
  getWebhookList,
  getWebhookById,
  addWebhook,
  updateWebhook,
  deleteWebhook,
  testSendWebhook,
  getWebhookStatus,
  refreshWebhook,
  getWebhookFormByName,
} from '@/api/modules/alarmWebhook'
import AlarmWebhookTypeSelector from '@/components/AlarmWebhookTypeSelector.vue'
import { webhookTypeLabel } from '@/utils/alarmWebhookType'
import { useI18n } from 'vue-i18n'

defineOptions({ name: 'AlarmWebhookIndexView' })

const { t } = useI18n()

// ---------- 查询表单 ----------
const queryForm = reactive({
  name: '',
})

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// ---------- 分页 ----------
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 首屏还没拿到数时显示占位符而不是 0：0 条通知会被当成"还没配任何推送"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value
)

async function fetchList() {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      size: pageSize.value,
    }
    if (queryForm.name) {
      params.name = queryForm.name
    }
    const res = await getWebhookList(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 失败时全局拦截器已提示
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  currentPage.value = 1
  fetchList()
}

function handleSizeChange(val) {
  pageSize.value = val
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange(val) {
  currentPage.value = val
  fetchList()
}

// ---------- 对话框状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const formRef = ref(null)

const formData = reactive({
  id: '',
  name: '',
  type: '',
  description: '',
  status: 1,
  hasSecret: false,
})

const formRules = {
  type: [{ required: true, message: () => t('alarmWebhook.typeRequired'), trigger: 'change' }],
  name: [
    { required: true, message: () => t('alarmWebhook.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('alarmWebhook.nameLength'), trigger: 'blur' },
  ],
}

// ---------- 动态通知配置表单 ----------
const dynamicFormKey = ref(0)
const dynamicFormApi = ref(null)
const dynamicFormRule = ref([])
const dynamicFormOptions = ref({})
const dynamicFormLoading = ref(false)
const dynamicFormPrefill = ref(null)
// 请求序号：忽略过期响应，避免快速切换类型时旧表单覆盖新结果
const dynamicFormRequestId = ref(0)
const testing = ref(false)
const testAlarmType = ref('offline')
// 显式清空已保存的密钥（留空只表示「不修改」，无法表达「删掉」）
const clearSecret = ref(false)

/** 递归解析可能被二次编码的 JSON 字段 */
function deepParse(value) {
  if (typeof value === 'string') {
    try {
      return deepParse(JSON.parse(value))
    } catch {
      return value
    }
  }
  if (Array.isArray(value)) {
    return value.map(deepParse)
  }
  if (value && typeof value === 'object') {
    const obj = {}
    for (const key of Object.keys(value)) {
      obj[key] = deepParse(value[key])
    }
    return obj
  }
  return value
}

/** 从响应中提取表单配置，兼容 { formJson } / { rule } / JSON 字符串等结构 */
function normalizeFormConfig(res) {
  if (!res) return null
  const formJson = res.formJson != null ? res.formJson : res
  const obj = deepParse(formJson)
  if (!obj || typeof obj !== 'object') return null
  return {
    rule: Array.isArray(obj.rule) ? obj.rule : [],
    options: obj.options || {},
  }
}

/** 当前表单渲染出来的字段集合 */
const dynamicFields = computed(() =>
  dynamicFormRule.value.map((item) => item.field).filter(Boolean)
)

/** 该类型的表单是否含密钥字段（决定要不要显示「清除密钥」） */
const hasSecretField = computed(() => dynamicFields.value.includes('secret'))

/**
 * 按类型加载通知配置表单
 * @param {string} type - Webhook 类型
 * @param {Object} [prefillConfig] - 编辑时传入已保存的配置，用于回填
 */
async function loadDynamicForm(type, prefillConfig) {
  const requestId = ++dynamicFormRequestId.value
  dynamicFormRule.value = []
  dynamicFormOptions.value = {}
  dynamicFormApi.value = null
  dynamicFormPrefill.value = prefillConfig ?? null
  if (!type) return

  dynamicFormLoading.value = true
  try {
    const res = await getWebhookFormByName(type)
    if (requestId !== dynamicFormRequestId.value) return
    const config = normalizeFormConfig(res)
    if (config?.rule?.length) {
      dynamicFormRule.value = config.rule
      dynamicFormOptions.value = {
        ...(config.options || {}),
        submitBtn: false,
        resetBtn: false,
      }
      // 换类型时强制重建表单实例，触发 mounted 回填
      dynamicFormKey.value += 1
    }
  } catch {
    // 类型无表单配置（后端未预置）时保持空表单
  } finally {
    if (requestId === dynamicFormRequestId.value) {
      dynamicFormLoading.value = false
    }
  }
}

/** form-create 渲染完成后取 API 实例，并回填已保存的配置值 */
function handleDynamicFormMounted($f) {
  dynamicFormApi.value = $f
  const prefill = dynamicFormPrefill.value
  if (!prefill) return
  let configData
  try {
    configData = typeof prefill === 'string' ? JSON.parse(prefill) : prefill
  } catch {
    return
  }
  if (typeof configData !== 'object' || configData === null) return
  const validFields = new Set(dynamicFields.value)
  Object.entries(configData).forEach(([field, value]) => {
    if (validFields.has(field)) {
      $f.setValue(field, value)
    }
  })
}

function handleTypeSelect() {
  // 换类型即重新加载该类型的表单（此时还没有已保存值可回填）
  loadDynamicForm(formData.type)
}

/** 收集动态表单当前值，只取本次渲染存在的字段 */
function collectDynamicValues() {
  const values = dynamicFormApi.value?.formData?.() ?? {}
  const out = {}
  for (const field of dynamicFields.value) {
    if (field in values) {
      out[field] = values[field]
    }
  }
  return out
}

/**
 * 密钥的三态语义（后端用 *string 区分「不传=不修改」与「传空串=清空」）：
 *   勾选「清除密钥」 -> 传空串
 *   留空            -> 不传该字段，保持原密钥
 *   填了值          -> 传新值
 */
function applySecretSemantics(payload) {
  if (!isEdit.value) return payload
  if (clearSecret.value) {
    payload.secret = ''
  } else if (payload.secret === '' || payload.secret == null) {
    delete payload.secret
  }
  return payload
}

/** 校验动态表单，通过返回 true */
async function validateDynamicForm() {
  if (!dynamicFormRule.value.length || !dynamicFormApi.value) return true
  try {
    await dynamicFormApi.value.validate()
    return true
  } catch {
    ElMessage.warning(t('alarmWebhook.completeNotifyConfig'))
    return false
  }
}

// ---------- 打开弹框 ----------
function handleAdd() {
  isEdit.value = false
  formData.id = ''
  formData.type = ''
  formData.name = ''
  formData.description = ''
  formData.status = 1
  formData.hasSecret = false
  testAlarmType.value = 'offline'
  clearSecret.value = false
  dialogVisible.value = true
  loadDynamicForm('')
}

async function handleEdit(row) {
  isEdit.value = true
  formData.id = row.id
  formData.type = row.type
  formData.name = row.name
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1
  formData.hasSecret = !!row.hasSecret
  testAlarmType.value = 'offline'
  clearSecret.value = false
  dialogVisible.value = true

  // 详情接口返回可回填的配置（secret 恒为空，不回显）
  let prefill = null
  try {
    const detail = await getWebhookById(row.id)
    if (detail) {
      prefill = {
        url: detail.url,
        msgType: detail.msgType,
        atAll: detail.atAll,
        atList: detail.atList,
      }
    }
  } catch {
    // 详情失败时退回列表行数据
  }
  if (!prefill) {
    prefill = {
      url: row.url,
      msgType: row.msgType,
      atAll: row.atAll,
      atList: row.atList,
    }
  }
  loadDynamicForm(row.type, prefill)
}

// ---------- 提交 ----------
async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  if (!(await validateDynamicForm())) return

  submitting.value = true
  try {
    const values = collectDynamicValues()
    if (isEdit.value) {
      await updateWebhook(
        applySecretSemantics({
          id: formData.id,
          name: formData.name.trim(),
          description: formData.description.trim(),
          status: formData.status,
          ...values,
        })
      )
      ElMessage.success(t('alarmWebhook.editSuccess'))
    } else {
      await addWebhook({
        name: formData.name.trim(),
        type: formData.type,
        description: formData.description.trim(),
        ...values,
      })
      ElMessage.success(t('alarmWebhook.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch {
    // 失败时全局拦截器已提示
  } finally {
    submitting.value = false
  }
}

function handleDialogClose() {
  formRef.value?.resetFields()
  loadDynamicForm('')
}

// ---------- 删除 ----------
async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('alarmWebhook.deleteConfirm', { name: row.name }),
      t('alarmWebhook.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      }
    )
    await deleteWebhook(row.id)
    ElMessage.success(t('alarmWebhook.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败
  }
}

// ---------- 测试发送 ----------
/** 弹框内测试：以表单当前填写值发送，未保存也能测 */
async function handleTestSendForm() {
  if (!formData.type) {
    ElMessage.warning(t('alarmWebhook.selectTypeFirst'))
    return
  }
  if (!(await validateDynamicForm())) return

  testing.value = true
  try {
    const values = collectDynamicValues()
    // 编辑时带上 id：密钥留空则沿用库里已存的，不必重新输入
    const payload = applySecretSemantics({
      type: formData.type,
      alarmType: testAlarmType.value,
      ...(isEdit.value ? { id: formData.id } : {}),
      ...values,
    })
    await testSendWebhook(payload)
    ElMessage.success(t('alarmWebhook.testSuccess'))
  } catch {
    // 失败时全局拦截器已展示后端返回的具体原因（加签错、关键词不匹配等）
  } finally {
    testing.value = false
  }
}

/** 列表行一键测试：直接用已保存的配置发一条断联通知 */
async function handleTestSendRow(row) {
  try {
    await testSendWebhook({ id: row.id, alarmType: 'offline' })
    ElMessage.success(t('alarmWebhook.testSuccess'))
  } catch {
    // 同上
  }
}

// ---------- 运行状态 ----------
const statusVisible = ref(false)
const statusLoading = ref(false)
const status = reactive({
  running: false,
  ingressDepth: 0,
  ingressDropped: 0,
  webhooks: [],
})

async function loadStatus() {
  statusLoading.value = true
  try {
    const res = await getWebhookStatus()
    status.running = !!res?.running
    status.ingressDepth = res?.ingressDepth ?? 0
    status.ingressDropped = res?.ingressDropped ?? 0
    status.webhooks = res?.webhooks ?? []
  } catch {
    // 失败时全局拦截器已提示
  } finally {
    statusLoading.value = false
  }
}

function handleShowStatus() {
  statusVisible.value = true
  loadStatus()
}

/** 手动热刷新：改完配置立即生效，无需等 10s 巡检 */
async function handleRefresh() {
  try {
    await refreshWebhook()
    ElMessage.success(t('alarmWebhook.refreshSuccess'))
    loadStatus()
  } catch {
    // 同上
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>

<style scoped>
/* 名称两行：主名称 + 描述，描述不抢主名称的视觉重量 */
.webhook-name-copy {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.45;
}

.webhook-name-desc {
  margin-top: 2px;
  overflow: hidden;
  font-size: 11.5px;
  color: var(--ink-4);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.webhook-url {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  color: var(--ink-3);
  text-overflow: ellipsis;
  vertical-align: middle;
  white-space: nowrap;
}

/* 通知配置区：在对话框里用发丝线接续基本信息 */
.webhook-section {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--line-soft);
}

.webhook-section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 12px;
  margin-bottom: 14px;
}

.webhook-section-headline {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.webhook-section-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 28px;
  height: 28px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 8px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.08);
}

.webhook-section-mark .el-icon {
  font-size: 15px;
}

.webhook-section-name {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.webhook-section-sub {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--ink-4);
}

.webhook-test-controls {
  display: flex;
  align-items: center;
  gap: 8px;
}

.webhook-test-type {
  width: 120px;
}

.dynamic-form-wrapper {
  min-height: 60px;
  padding-bottom: 8px;
}

.webhook-empty-config {
  margin: 0;
  padding: 10px 0;
  font-size: 12.5px;
  color: var(--ink-4);
}

.webhook-secret-hint {
  margin: 0 0 10px;
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
}

.webhook-clear-secret {
  margin-top: 4px;
}

/* 插件声明的通知参数收成一列，与设备对象页的协议参数表单同一写法 */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 280px;
}

/* ---------- 运行状态对话框 ---------- */
.status-strip {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1px;
  margin-bottom: 16px;
  overflow: hidden;
  background: var(--line-soft);
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

.status-item {
  min-width: 0;
  padding: 14px 16px;
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
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.health-table {
  overflow: hidden;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

.webhook-empty {
  margin: 0;
  padding: 32px 16px;
  font-size: 12.5px;
  color: var(--ink-4);
  text-align: center;
}

@media (max-width: 640px) {
  .status-strip {
    grid-template-columns: 1fr;
  }
}
</style>
