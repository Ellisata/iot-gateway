<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">
      {{ t('menu.deviceObject') }}
    </h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            :placeholder="t('deviceObject.searchPlaceholder')"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="flex items-center gap-3">
          <el-button :icon="Download" @click="handleDownloadTemplate">{{ t('common.downloadTemplate') }}</el-button>
          <el-button type="primary" :icon="Upload" :loading="importing" @click="handleImport">{{ t('common.import') }}</el-button>
          <el-button type="primary" @click="handleAdd">{{ t('deviceObject.add') }}</el-button>
          <input ref="fileInputRef" type="file" accept=".xlsx" class="hidden" @change="handleFileChange" />
        </div>
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
        <el-table-column label="ID" width="190" align="center">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-1">
              <el-tooltip :content="String(row.id)" placement="top">
                <span class="text-gray-600 truncate max-w-[120px] inline-block align-middle">
                  {{ row.id }}
                </span>
              </el-tooltip>
              <el-button
                type="primary"
                link
                size="small"
                :icon="CopyDocument"
                @click="copyId(row.id)"
              />
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="name" :label="t('common.name')" min-width="140" />
        <el-table-column prop="protocolName" :label="t('deviceObject.protocol')" width="120" />
        <el-table-column prop="description" :label="t('common.description')" min-width="180" show-overflow-tooltip />
        <el-table-column prop="status" :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'danger'" size="small">
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" :label="t('common.createdAt')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="130" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
              <el-tooltip :content="t('deviceObject.addressManage')" placement="top">
                <el-button type="primary" link size="small" :icon="Position" @click="handleViewAddresses(row)" />
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
          @size-change="fetchList"
          @current-change="fetchList"
        />
      </div>
    </el-card>

    <!-- ========== 新增 / 编辑 两步向导对话框 ========== -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? t('deviceObject.editTitle') : t('deviceObject.addTitle')"
      width="620px"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <!-- 步骤指示器 -->
      <div class="flex items-center justify-center mb-8 mt-2">
        <div class="flex items-center">
          <div
            class="flex items-center justify-center w-8 h-8 rounded-full text-sm font-bold"
            :class="currentStep === 1
              ? 'bg-blue-600 text-white ring-4 ring-blue-100'
              : stepPassed(1)
                ? 'bg-green-500 text-white'
                : 'bg-gray-200 text-gray-500'"
          >
            <el-icon v-if="stepPassed(1)" size="16"><Check /></el-icon>
            <span v-else>1</span>
          </div>
          <span
            class="ml-2 text-sm whitespace-nowrap"
            :class="currentStep === 1 ? 'text-blue-600 font-medium' : 'text-gray-500'"
          >
            {{ t('deviceObject.stepBasicInfo') }}
          </span>
        </div>
        <div
          class="w-20 h-px mx-3"
          :class="stepPassed(1) ? 'bg-green-500' : 'bg-gray-200'"
        />
        <div class="flex items-center">
          <div
            class="flex items-center justify-center w-8 h-8 rounded-full text-sm font-bold"
            :class="currentStep === 2
              ? 'bg-blue-600 text-white ring-4 ring-blue-100'
              : 'bg-gray-200 text-gray-500'"
          >
            2
          </div>
          <span
            class="ml-2 text-sm whitespace-nowrap"
            :class="currentStep === 2 ? 'text-blue-600 font-medium' : 'text-gray-500'"
          >
            {{ t('deviceObject.stepProtocolConfig') }}
          </span>
        </div>
      </div>

      <!-- ======== 第一步：基本信息 ======== -->
      <div v-show="currentStep === 1">
        <el-form
          ref="formRef"
          :model="formData"
          :rules="formRules"
          label-width="100px"
          label-position="right"
          status-icon
        >
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
          <el-form-item :label="t('common.status')" prop="status">
            <el-switch
              v-model="formData.status"
              :active-value="1"
              :inactive-value="0"
              :active-text="t('common.enabled')"
              :inactive-text="t('common.enableDisable.off')"
            />
          </el-form-item>
        </el-form>
      </div>

      <!-- ======== 第二步：协议配置 ======== -->
      <div v-show="currentStep === 2">
        <el-form label-width="100px" label-position="right">
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

          <!-- 分隔线 & 动态表单标题 -->
          <template v-if="protocolSelected">
            <div class="flex items-center justify-between mb-4">
              <span class="text-sm font-medium text-gray-600">{{ t('deviceObject.protocolParams') }}</span>
              <el-button
                type="primary"
                link
                :loading="testingConnection"
                @click="handleTestConnection"
              >
                <el-icon class="mr-1"><Connection /></el-icon> {{ t('deviceObject.testConnection') }}
              </el-button>
            </div>

            <!-- form-create 动态渲染（:key 绑定协议名，切换时强制重建） -->
            <div class="dynamic-form-wrapper">
              <form-create
                :key="formData.protocol || 'empty'"
                ref="fcRef"
                :rule="fcRule"
                :option="fcOption"
                @mounted="handleFcReady"
              />
            </div>
          </template>
        </el-form>
      </div>

      <!-- 底部按钮 -->
      <template #footer>
        <div class="flex items-center justify-between">
          <!-- 左侧：上一步 -->
          <div>
            <el-button v-if="currentStep === 2" @click="prevStep">
              <el-icon><ArrowLeft /></el-icon> {{ t('common.prevStep') }}
            </el-button>
          </div>
          <!-- 右侧：下一步 / 提交 -->
          <div class="flex items-center gap-2">
            <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
            <el-button
              v-if="currentStep === 1"
              type="primary"
              @click="nextStep"
            >
              {{ t('common.nextStep') }}
              <el-icon class="ml-1"><ArrowRight /></el-icon>
            </el-button>
            <el-button
              v-if="currentStep === 2"
              type="primary"
              :loading="submitting"
              @click="handleSubmit"
            >
              {{ isEdit ? t('common.save') : t('common.submit') }}
            </el-button>
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- ========== Excel 导入结果对话框 ========== -->
    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />

	  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft, ArrowRight, Check, Connection, CopyDocument, Delete, Download, EditPen, Position, Upload } from '@element-plus/icons-vue'
import {
  getDeviceObjectList,
  getDeviceObjectById,
  addDeviceObject,
  updateDeviceObject,
  deleteDeviceObject,
  testDeviceConnection,
  importDeviceObjects,
  downloadDeviceTemplate,
} from '@/api/modules/deviceObject'
import { getProtocolById } from '@/api/modules/protocol'
import ProtocolSelector from '@/components/ProtocolSelector.vue'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// ---------- 路由 ----------
const router = useRouter()
// ---------- 查看地址 ----------
function handleViewAddresses(row) {
  router.push({
    name: 'GatewayDeviceAddress',
    params: { deviceObjectId: row.id },
    query: {
      deviceObjectName: row.name,
      protocolName: row.protocolName,
    },
  })
}

// ---------- 复制 ID ----------
async function copyId(id) {
  try {
    await navigator.clipboard.writeText(String(id))
    ElMessage.success(t('deviceObject.idCopied'))
  } catch {
    ElMessage.error(t('deviceObject.copyFailed'))
  }
}

// ---------- 查询表单 ----------
const queryForm = reactive({
  name: '',
})

// ---------- 分页 ----------
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// ---------- 协议相关 ----------
const protocolSelected = computed(() => !!formData.protocol)

// ---------- 获取列表 ----------
async function fetchList() {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      size: pageSize.value,
    }
    if (queryForm.name) params.name = queryForm.name
    const res = await getDeviceObjectList(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.log(err.message)
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
  importFn: (file) => importDeviceObjects(file),
  downloadFn: downloadDeviceTemplate,
  templateName: () => t('deviceObject.importTemplateName'),
  refresh: fetchList,
})

// ---------- 对话框 / 向导状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const testingConnection = ref(false)
const formRef = ref(null)
const currentStep = ref(1)

/** 步骤 1 是否已完成（用于步骤指示器样式） */
const step1Passed = ref(false)

function stepPassed(step) {
  return step === 1 && step1Passed.value
}

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
  protocol: '',
  protocolId: '',
  description: '',
  status: 1,
  protocolJson: '',
})

// ---------- 表单校验规则 ----------
const formRules = {
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
})

/** form-create 渲染完成后获取 API 实例 */
function handleFcReady($f) {
  fcApi.value = $f
}

/** 协议选中回调：加载 formJson 并渲染动态表单 */
function handleProtocolSelect(row) {
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
      if (parsed?.rule && Array.isArray(parsed.rule)) {
        fcRule.value = parsed.rule
      } else if (Array.isArray(parsed)) {
        // 兼容纯数组格式
        fcRule.value = parsed
      } else {
        ElMessage.warning(t('deviceObject.protocolFormInvalid'))
        return
      }

      // 合并全局选项（兼容 option 和 options 两种字段名）
      const opts = parsed?.options || parsed?.option
      if (opts) {
        fcOption.value = {
          ...fcOption.value,
          ...opts,
          form: {
            ...fcOption.value.form,
            ...(opts.form || {}),
          },
        }
      }

      // 编辑模式：如果有已有的协议配置数据，回填到 form-create（仅首次加载）
      if (isEdit.value && formData.protocolJson) {
        await nextTick()
        if (fcApi.value) {
          try {
            const configData =
              typeof formData.protocolJson === 'string'
                ? JSON.parse(formData.protocolJson)
                : formData.protocolJson
            if (typeof configData === 'object' && configData !== null) {
              // 只回填当前 rule 中存在的字段，避免脏数据残留
              const validFields = new Set(
                fcRule.value.map(item => item.field).filter(Boolean)
              )
              Object.entries(configData).forEach(([field, value]) => {
                if (validFields.has(field)) {
                  fcApi.value.setValue(field, value)
                }
              })
            }
          } catch {
            // 回填失败不阻塞
          }
        }
        // 回填完成后清除，避免切换协议时错误回填旧数据
        formData.protocolJson = ''
      }
    }
  } catch (err) {
    console.error('获取协议详情失败', err)
    ElMessage.error(t('deviceObject.fetchProtocolConfigFailed'))
  }
}

// ---------- 步骤导航 ----------
async function nextStep() {
  // 校验第一步表单
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  step1Passed.value = true
  currentStep.value = 2

  // 编辑模式：如果已有协议，自动加载动态表单
  if (isEdit.value && formData.protocolId && fcRule.value.length === 0) {
    await nextTick()
    handleProtocolChange(formData.protocolId)
  }
}

function prevStep() {
  currentStep.value = 1
}

// ---------- 新增 ----------
function handleAdd() {
  isEdit.value = false
  currentStep.value = 1
  step1Passed.value = false

  formData.id = ''
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
  currentStep.value = 1
  step1Passed.value = true // 已有数据，第一步已完成

  formData.id = row.id
  formData.name = row.name
  formData.protocol = row.protocol ?? ''
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1

  // 先获取完整详情（含 protocolConfig），保证在对话框打开前数据就绪
  try {
    const detail = await getDeviceObjectById(row.id)
    formData.protocolJson = detail?.protocolJson ?? ''
    formData.protocolId = detail?.protocolId ?? ''
    // 列表接口返回的 protocol 可能是 ID，用详情接口的协议名称覆盖
    if (detail?.protocol) formData.protocol = detail.protocol
  } catch (err) {
    formData.protocolJson = ''
    formData.protocolId = ''
    console.error('获取设备对象详情失败', err)
    // 不阻塞编辑流程，回填失败不影响基本信息编辑
  }

  // 清空动态表单（进入第二步时会自动加载）
  fcRule.value = []

  dialogVisible.value = true
}

// ---------- 删除 ----------
async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('deviceObject.deleteConfirm', { name: row.name }), t('deviceObject.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
    await deleteDeviceObject(row.id)
    ElMessage.success(t('protocol.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

// ---------- 对话框关闭 ----------
function handleDialogClose() {
  formRef.value?.resetFields()
  currentStep.value = 1
  step1Passed.value = false
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
    
  } finally {
    testingConnection.value = false
  }
}

// ---------- 提交 ----------
async function handleSubmit() {
  // 校验第一步表单（防绕过）
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    currentStep.value = 1
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
      name: formData.name.trim(),
      protocolId: formData.protocolId,
      description: formData.description.trim() || undefined,
      status: formData.status,
      protocolJson: protocolConfig,
    }

    if (isEdit.value) {
      payload.id = formData.id
      await updateDeviceObject(payload)
      ElMessage.success(t('protocol.editSuccess'))
    } else {
      console.log(payload);
      await addDeviceObject(payload)
      ElMessage.success(t('protocol.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {
    const msg = isEdit.value ? t('protocol.editFailed') : t('protocol.addFailed')
    console.error(msg, err)
    ElMessage.error(err?.response?.data?.msg || err?.message || msg)
  } finally {
    submitting.value = false
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>
