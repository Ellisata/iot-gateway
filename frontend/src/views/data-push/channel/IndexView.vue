<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ t('menu.channel') }}</h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            :placeholder="t('channel.searchPlaceholder')"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <el-button type="primary" @click="handleAdd">{{ t('channel.add') }}</el-button>
      </div>
    </el-card>

    <!-- 数据表格 -->
    <el-card shadow="never">
      <el-table
        :data="tableData"
        v-loading="loading"
        stripe
        style="width: 100%"
        border
      >
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="name" :label="t('common.name')" min-width="140" />
        <el-table-column prop="description" :label="t('common.description')" min-width="180" show-overflow-tooltip />
        <el-table-column prop="status" :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? t('common.enabled') : t('common.disabled') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" :label="t('common.createdAt')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="130" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
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
      :title="isEdit ? t('channel.editTitle') : t('channel.addTitle')"
      width="580px"
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
        <el-form-item :label="t('common.name')" prop="name">
          <ChannelSelector
            v-model="formData.name"
            :disabled="isEdit"
            @select="handleChannelSelect"
          />
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            :placeholder="t('channel.descPlaceholder')"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('common.status')" prop="status">
          <el-switch
            v-model="formData.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.disabled')"
          />
        </el-form-item>
      </el-form>

      <!-- 动态连接配置表单：根据所选通道名称加载 -->
      <template v-if="formData.name">
        <el-divider content-position="left">{{ t('channel.connectConfig') }}</el-divider>
        <div class="flex items-center justify-between mb-2">
          <span class="text-sm font-medium text-gray-600">{{ t('channel.connectParams') }}</span>
          <el-button
            type="primary"
            link
            :loading="testingConnection"
            @click="handleTestConnectivity"
          >
            <el-icon class="mr-1"><Connection /></el-icon> {{ t('channel.testConnection') }}
          </el-button>
        </div>
        <div v-loading="dynamicFormLoading" class="min-h-[60px] pb-2">
          <div
            v-if="!dynamicFormLoading && !dynamicFormRule.length"
            class="text-sm text-gray-400 py-2"
          >
            {{ t('channel.noConnectConfig') }}
          </div>
          <form-create
            v-else
            :key="dynamicFormKey"
            :rule="dynamicFormRule"
            :option="dynamicFormOptions"
            @mounted="handleDynamicFormMounted"
          />
        </div>
      </template>

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
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Connection, Delete, EditPen } from '@element-plus/icons-vue'
import {
  getChannelList,
  addChannel,
  updateChannel,
  deleteChannel,
  getChannelById,
  testConnectivity,
} from '@/api/modules/pushChannel'
import { getPushChannelFormByName } from '@/api/modules/pushChannelForm'
import ChannelSelector from '@/components/ChannelSelector.vue'
import { useI18n } from 'vue-i18n'

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

// ---------- 获取列表（分页） ----------
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
    const res = await getChannelList(params)
    // 兼容两种响应格式：直接返回数组 / 返回 { records: [], total }
    if (Array.isArray(res)) {
      tableData.value = res
      total.value = res.length
    } else {
      tableData.value = res?.records ?? []
      total.value = res?.total ?? 0
    }
  } catch (err) {
    console.error('获取通道列表失败', err)
    ElMessage.error(t('channel.fetchListFailed'))
  } finally {
    loading.value = false
  }
}

// ---------- 搜索 / 重置 ----------
function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  currentPage.value = 1
  fetchList()
}

// ---------- 分页切换 ----------
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

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
  description: '',
  status: 1,
})

// ---------- 表单校验规则 ----------
const formRules = {
  name: [
    { required: true, message: () => t('channel.nameRequired'), trigger: 'blur' },
    { min: 1, max: 50, message: () => t('channel.nameLength'), trigger: 'blur' },
  ],
  description: [
    { max: 200, message: () => t('channel.descMaxLength'), trigger: 'blur' },
  ],
}

// ---------- 动态连接配置表单（按所选通道名称加载） ----------
const dynamicFormKey = ref(0)
const dynamicFormApi = ref(null)
const dynamicFormRule = ref([])
const dynamicFormOptions = ref({})
const dynamicFormLoading = ref(false)
// 编辑模式回填数据：通道详情接口返回的 configJson
const dynamicFormPrefill = ref(null)
// 请求序号：用于忽略过期请求，避免快速切换名称 / 关闭弹框时旧响应覆盖新结果
const dynamicFormRequestId = ref(0)
// 测试连接中标记
const testingConnection = ref(false)

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

/** 从后端响应中提取表单配置，兼容 { formJson } / { rule } / JSON 字符串等结构 */
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

/**
 * 按通道名称加载连接配置表单
 * @param {string} name - 通道名称
 * @param {Object} [prefillConfig] - 编辑时传入 configJson（通道详情接口），用于回填已有填写值
 */
async function loadDynamicForm(name, prefillConfig) {
  const requestId = ++dynamicFormRequestId.value
  // 先清空旧表单，避免残留上一个通道的字段
  dynamicFormRule.value = []
  dynamicFormOptions.value = {}
  dynamicFormApi.value = null
  dynamicFormPrefill.value = prefillConfig ?? null
  if (!name) return

  dynamicFormLoading.value = true
  try {
    const res = await getPushChannelFormByName(name)
    // 忽略过期请求的响应（用户可能已切换名称或关闭弹框）
    if (requestId !== dynamicFormRequestId.value) return
    const config = normalizeFormConfig(res)
    if (config?.rule?.length) {
      dynamicFormRule.value = config.rule
      dynamicFormOptions.value = {
        ...(config.options || {}),
        submitBtn: false,
        resetBtn: false,
      }
      // 更换通道名称时强制重建表单实例，触发 mounted 事件以回填编辑值
      dynamicFormKey.value += 1
    }
  } catch (err) {
    if (requestId !== dynamicFormRequestId.value) return
    //console.error('加载通道连接配置失败', err)
    //ElMessage.error('加载连接配置失败')
  } finally {
    if (requestId === dynamicFormRequestId.value) {
      dynamicFormLoading.value = false
    }
  }
}

/**
 * form-create 渲染完成后获取 API 实例（v-model 绑的是表单数据，不能用它调 validate/formData）
 * 编辑模式：用通道详情的 configJson 回填动态表单值（与设备对象的 setValue 回填方式一致）
 */
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
  // 只回填当前 rule 中存在的字段，避免脏数据残留
  const validFields = new Set(
    dynamicFormRule.value.map((item) => item.field).filter(Boolean)
  )
  Object.entries(configData).forEach(([field, value]) => {
    if (validFields.has(field)) {
      $f.setValue(field, value)
    }
  })
}

/** 名称选择回调：加载该通道的连接配置表单 */
function handleChannelSelect({ name }) {
  loadDynamicForm(name)
}

// ---------- 测试连接 ----------
async function handleTestConnectivity() {
  if (!formData.name) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  // 校验动态连接配置表单
  if (dynamicFormRule.value.length && dynamicFormApi.value) {
    try {
      await dynamicFormApi.value.validate()
    } catch {
      ElMessage.warning(t('channel.completeConnectConfig'))
      return
    }
  }
  testingConnection.value = true
  try {
    // 收集动态表单填写值作为 configJson 传给后端
    const configJson = dynamicFormApi.value?.formData?.() ?? {}
    const res = await testConnectivity({
      name: formData.name,
      configJson,
    })
    // 兼容后端返回布尔值 / { connected } / { success } 结构；无显式失败标记则视为成功
    const connected =
      typeof res === 'boolean' ? res : res?.connected ?? res?.success ?? true
    if (connected === false || connected === 'false') {
      ElMessage.error(t('channel.connectionFailed'))
    } else {
      ElMessage.success(t('channel.connectionSuccess'))
    }
  } catch (err) {
    // 失败时全局拦截器已弹出后端错误信息，此处静默
  } finally {
    testingConnection.value = false
  }
}

// ---------- 操作 ----------
function handleAdd() {
  isEdit.value = false
  // 清空上次残留的表单值，避免新增弹框缓存通道名称 / 描述
  formData.id = ''
  formData.name = ''
  formData.description = ''
  formData.status = 1
  dialogVisible.value = true
  // 新增时等待用户选择名称后再加载连接配置
  loadDynamicForm('')
}

async function handleEdit(row) {
  isEdit.value = true
  formData.id = row.id
  formData.name = row.name
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1
  dialogVisible.value = true

  // 编辑时需回填连接配置：列表接口不一定返回 configJson，先获取通道详情
  let prefillConfig = null
  try {
    const detail = await getChannelById(row.id)
    if (detail?.configJson != null) {
      prefillConfig = detail.configJson
    }
  } catch (err) {
    console.error('获取通道详情失败', err)
  }
  // 兜底：列表行若自带 configJson 也直接使用
  if (prefillConfig == null && row.configJson != null) {
    prefillConfig = row.configJson
  }
  // 编辑时按名称加载连接配置，并回填已有填写值
  loadDynamicForm(row.name, prefillConfig)
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('channel.deleteConfirm', { name: row.name }), t('channel.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
    await deleteChannel(row.id)
    ElMessage.success(t('channel.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

function handleDialogClose() {
  formRef.value?.resetFields()
  loadDynamicForm('')
}

async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  // 校验动态连接配置表单
  if (dynamicFormRule.value.length && dynamicFormApi.value) {
    try {
      await dynamicFormApi.value.validate()
    } catch {
      ElMessage.warning(t('channel.completeConnectConfig'))
      return
    }
  }
  submitting.value = true
  try {
    // 收集动态表单填写值，作为 configJson 传给后端（与设备对象的 protocolJson 传递方式一致）
    const dynamicValues = dynamicFormApi.value?.formData?.() ?? {}
    const payload = {
      name: formData.name.trim(),
      description: formData.description.trim(),
      status: formData.status,
      configJson: dynamicValues,
    }
    if (isEdit.value) {
      payload.id = formData.id
      await updateChannel(payload)
      ElMessage.success(t('channel.editSuccess'))
    } else {
      await addChannel(payload)
      ElMessage.success(t('channel.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {
    //const msg = isEdit.value ? '编辑失败' : '新增失败'
    //console.error(msg, err)
    //ElMessage.error(err?.response?.data?.msg || err?.message || msg)
  } finally {
    submitting.value = false
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>
