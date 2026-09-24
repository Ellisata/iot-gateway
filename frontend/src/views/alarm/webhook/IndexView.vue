<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ t('menu.alarmWebhook') }}</h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            :placeholder="t('alarmWebhook.searchPlaceholder')"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="flex items-center gap-3">
          <el-button @click="handleShowStatus">
            <el-icon class="mr-1"><DataLine /></el-icon>
            {{ t('alarmWebhook.statusHealth') }}
          </el-button>
          <el-button type="primary" @click="handleAdd">{{ t('alarmWebhook.add') }}</el-button>
        </div>
      </div>
    </el-card>

    <!-- 数据表格 -->
    <el-card shadow="never">
      <el-table :data="tableData" v-loading="loading" stripe style="width: 100%" border>
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="name" :label="t('common.name')" min-width="140" show-overflow-tooltip />
        <el-table-column :label="t('alarmWebhook.type')" width="170">
          <template #default="{ row }">
            {{ webhookTypeLabel(row.type, row.typeName, t) }}
          </template>
        </el-table-column>
        <el-table-column prop="url" :label="t('alarmWebhook.url')" min-width="240" show-overflow-tooltip />
        <el-table-column :label="t('alarmWebhook.secret')" width="120" align="center">
          <template #default="{ row }">
            <el-tag :type="row.hasSecret ? 'success' : 'info'" size="small">
              {{ row.hasSecret ? t('alarmWebhook.hasSecret') : t('alarmWebhook.noSecret') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? t('common.enabled') : t('common.disabled') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" :label="t('common.createdAt')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="170" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
              <el-tooltip :content="t('alarmWebhook.testSend')" placement="top">
                <el-button type="success" link size="small" :icon="Promotion" @click="handleTestSendRow(row)" />
              </el-tooltip>
              <el-tooltip :content="t('common.edit')" placement="top">
                <el-button type="primary" link size="small" :icon="EditPen" @click="handleEdit(row)" />
              </el-tooltip>
              <el-tooltip :content="t('common.delete')" placement="top">
                <el-button type="danger" link size="small" :icon="Delete" @click="handleDelete(row)" />
              </el-tooltip>
            </div>
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
          @current-change="handleCurrentChange"
        />
      </div>
    </el-card>

    <!-- 新增 / 编辑 对话框 -->
    <el-dialog
      v-model="dialogVisible"
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

      <!-- 动态通知配置表单：按所选类型加载 -->
      <template v-if="formData.type">
        <el-divider content-position="left">{{ t('alarmWebhook.notifyConfig') }}</el-divider>
        <div class="flex items-center justify-between mb-2">
          <span class="text-sm font-medium text-gray-600">{{ t('alarmWebhook.notifyParams') }}</span>
          <div class="flex items-center gap-2">
            <el-select v-model="testAlarmType" size="small" style="width: 120px">
              <el-option :label="t('alarmWebhook.testOffline')" value="offline" />
              <el-option :label="t('alarmWebhook.testRecover')" value="recover" />
            </el-select>
            <el-button
              type="primary"
              link
              :loading="testing"
              @click="handleTestSendForm"
            >
              <el-icon class="mr-1"><Promotion /></el-icon> {{ t('alarmWebhook.testSend') }}
            </el-button>
          </div>
        </div>
        <div v-loading="dynamicFormLoading" class="min-h-[60px] pb-2">
          <div
            v-if="!dynamicFormLoading && !dynamicFormRule.length"
            class="text-sm text-gray-400 py-2"
          >
            {{ t('alarmWebhook.noNotifyConfig') }}
          </div>
          <template v-else>
            <!-- 密钥不回显，编辑时留空即保持原值；这条提示否则会被误解为「已清空」 -->
            <div v-if="isEdit && formData.hasSecret" class="text-xs text-gray-400 mb-2">
              {{ t('alarmWebhook.secretKeepHint') }}
            </div>
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
              class="mt-1"
            >
              {{ t('alarmWebhook.clearSecret') }}
            </el-checkbox>
          </template>
        </div>
      </template>

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
      :title="t('alarmWebhook.healthTitle')"
      width="900px"
    >
      <div v-loading="statusLoading">
        <el-descriptions :column="3" border size="small" class="mb-4">
          <el-descriptions-item :label="t('alarmWebhook.notifier')">
            <el-tag :type="status.running ? 'success' : 'info'" size="small">
              {{ status.running ? t('alarmWebhook.running') : t('alarmWebhook.stopped') }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item :label="t('alarmWebhook.ingressDepth')">
            {{ status.ingressDepth }}
          </el-descriptions-item>
          <el-descriptions-item :label="t('alarmWebhook.ingressDropped')">
            {{ status.ingressDropped }}
          </el-descriptions-item>
        </el-descriptions>

        <el-table :data="status.webhooks" stripe border style="width: 100%">
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
          <el-table-column prop="lastSuccessTime" :label="t('alarmWebhook.lastSuccessTime')" width="170" align="center" />
          <el-table-column prop="error" :label="t('alarmWebhook.lastError')" min-width="180" show-overflow-tooltip />
        </el-table>

        <div v-if="!statusLoading && !status.webhooks.length" class="text-sm text-gray-400 py-3 text-center">
          {{ t('alarmWebhook.noWebhook') }}
        </div>
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
import { DataLine, Delete, EditPen, Promotion } from '@element-plus/icons-vue'
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
