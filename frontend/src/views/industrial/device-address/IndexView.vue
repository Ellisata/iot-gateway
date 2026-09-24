<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <!-- 面包屑导航 -->
    <div class="flex items-center gap-2 text-sm text-gray-500 mb-4">
      <el-button text @click="goBack">
        <el-icon><ArrowLeft /></el-icon>
        {{ t('deviceAddress.backToList') }}
      </el-button>
    </div>

    <h1 class="text-2xl font-bold text-gray-800 mb-6">
      {{ t('deviceAddress.title') }} — {{ deviceObjectName }}
    </h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            :placeholder="t('deviceAddress.searchPlaceholder')"
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
          <el-button type="primary" @click="handleAdd">{{ t('deviceAddress.add') }}</el-button>
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
        <el-table-column prop="name" :label="t('deviceAddress.addressName')" min-width="140" />
        <el-table-column prop="label" :label="t('deviceAddress.tag')" min-width="140" show-overflow-tooltip />
        <el-table-column prop="dataType" :label="t('deviceAddress.dataType')" width="130" align="center" />
        <el-table-column prop="commonDataType" :label="t('deviceAddress.commonDataType')" width="140" align="center" />
        <el-table-column prop="rwPermission" :label="t('deviceAddress.rwPermission')" width="110" align="center">
          <template #default="{ row }">
            <el-tag
              :type="row.rwPermission === 'RW' ? 'success' : row.rwPermission === 'R' ? 'primary' : 'warning'"
              size="small"
            >
              {{ { R: t('deviceAddress.readonly'), W: t('deviceAddress.writeonly'), RW: t('deviceAddress.readwrite') }[row.rwPermission] || row.rwPermission }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="scanFrequency" :label="t('deviceAddress.scanFrequency')" width="120" align="center">
          <template #default="{ row }">
            {{ row.scanFrequency }} ms
          </template>
        </el-table-column>
        <el-table-column prop="description" :label="t('common.description')" min-width="180" show-overflow-tooltip />
        <el-table-column prop="status" :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'danger'" size="small">
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" :label="t('common.createdAt')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="100" align="center" fixed="right">
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
          @size-change="fetchList"
          @current-change="fetchList"
        />
      </div>
    </el-card>

    <!-- ========== 新增 / 编辑 对话框 ========== -->
    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? t('deviceAddress.editTitle') : t('deviceAddress.addTitle')"
      width="620px"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="110px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('deviceAddress.addressName')" prop="name">
          <div class="flex items-center gap-2 w-full">
            <el-input
              v-model="formData.name"
              :placeholder="addressPlaceholder"
              maxlength="100"
              show-word-limit
              class="flex-1 min-w-0"
            />
            <!-- 用气泡而不是抽屉：表单本身已经开在对话框里，气泡能让说明和输入框同时可见、
                 边看边改，抽屉会盖住半个表单 -->
            <el-popover v-if="addressSpec" placement="bottom" :width="380" trigger="click">
              <template #reference>
                <el-button :icon="QuestionFilled" circle text class="shrink-0" />
              </template>
              <div class="text-xs leading-5">
                <div class="font-semibold text-gray-700 mb-2">{{ t('deviceAddress.addressHelpTitle') }}</div>

                <div class="text-gray-400 mb-1">{{ t('deviceAddress.addressHelpSyntax') }}</div>
                <div class="font-mono text-gray-800 bg-gray-50 rounded px-2 py-1 mb-3 break-all">
                  {{ t(addressSpec.templateKey) }}
                </div>

                <div class="text-gray-400 mb-1">{{ t('deviceAddress.addressHelpExamples') }}</div>
                <div class="font-mono text-gray-800 mb-3 space-y-0.5">
                  <div v-for="eg in addressSpec.examples" :key="eg">{{ eg }}</div>
                </div>

                <div class="text-gray-400 mb-1">{{ t('deviceAddress.addressHelpPitfalls') }}</div>
                <ul class="list-disc pl-4 text-gray-600 space-y-1.5">
                  <li v-for="key in addressSpec.pitfallKeys" :key="key">{{ t(key) }}</li>
                </ul>
              </div>
            </el-popover>
          </div>
          <!-- 常驻提示：占位符一输入就消失，而地址格式正是最需要一直看见的信息 -->
          <div v-if="addressExample" class="w-full text-xs text-gray-400 leading-5 mt-1">
            {{ t('deviceAddress.addressHint', { protocol: protocolName, example: addressExample }) }}
          </div>
        </el-form-item>

        <el-form-item :label="t('deviceAddress.tag')" prop="label">
          <el-input
            v-model="formData.label"
            :placeholder="t('deviceAddress.tagPlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>

        <el-form-item :label="t('deviceAddress.dataType')" prop="dataType">
          <el-select
            v-model="formData.dataType"
            :placeholder="t('deviceAddress.dataTypePlaceholder')"
            style="width: 100%"
            :loading="dataTypeLoading"
            :loading-text="t('common.loading')"
          >
            <el-option
              v-for="item in dataTypeOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item :label="t('deviceAddress.rwPermission')" prop="rwPermission">
          <el-select v-model="formData.rwPermission" :placeholder="t('deviceAddress.rwPlaceholder')" style="width: 100%">
            <el-option
              v-for="item in rwPermissionOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item :label="t('deviceAddress.scanFrequency')" prop="scanFrequency">
          <el-select v-model="formData.scanFrequency" :placeholder="t('deviceAddress.scanPlaceholder')" style="width: 100%">
            <el-option
              v-for="item in scanFrequencyOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            :placeholder="t('deviceAddress.descPlaceholder')"
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

      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            {{ isEdit ? t('common.save') : t('common.submit') }}
          </el-button>
        </div>
      </template>
    </el-dialog>

    <!-- ========== Excel 导入结果对话框 ========== -->
    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ArrowLeft, CopyDocument, Delete, Download, EditPen, QuestionFilled, Upload } from '@element-plus/icons-vue'
import {
  getDeviceAddressList,
  addDeviceAddress,
  updateDeviceAddress,
  deleteDeviceAddress,
  importDeviceAddresses,
  downloadDeviceAddressTemplate,
} from '@/api/modules/deviceAddress'
import { getDataTypes } from '@/api/modules/protocol'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'
import { getAddressExample, getAddressSpec } from '@/utils/addressSyntax'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()

// ---------- 下拉选项 ----------
// 数据类型通过协议接口获取：/protocol/getDataTypes?protocolName=xxx
const dataTypeOptions = ref([])
const dataTypeLoading = ref(false)

// 接口返回 { protocol, commonTypes, extendedTypes, allTypes }，使用 allTypes 填充下拉选项
function normalizeDataTypes(data) {
  const list = data?.allTypes ?? data
  if (!Array.isArray(list)) return []
  return list
    .map((item) => {
      if (typeof item === 'string') return { value: item, label: item }
      const value = item?.code ?? item?.value ?? item?.dataType
      const label = item?.description ?? item?.name ?? item?.label ?? value
      return value == null ? null : { value: String(value), label: String(label) }
    })
    .filter(Boolean)
}

async function fetchDataTypes() {
  dataTypeLoading.value = true
  try {
    // 取设备对象传入的协议名；取不到就别猜（原来兜底到 Omron.Net.CIP，但那个名字
    // 不是任何驱动注册的协议名，解析不到反而把问题藏起来），让下拉保持为空
    // （局部变量改叫 proto：外层 protocolName 是 computed，同名遮蔽容易看混）
    const proto = route.query.protocolName
    const res = await getDataTypes(proto)
    dataTypeOptions.value = normalizeDataTypes(res)
  } catch {
    dataTypeOptions.value = []
  } finally {
    dataTypeLoading.value = false
  }
}

const rwPermissionOptions = computed(() => {
  const options = [
    { value: 'R', label: t('deviceAddress.readonlyShort') },
    { value: 'W', label: t('deviceAddress.writeonlyShort') },
    { value: 'RW', label: t('deviceAddress.readwriteShort') },
  ]
  return isEdit.value ? options : options.filter(o => o.value !== 'W')
})

const isZh = computed(() => locale.value === 'zh-CN')

// 扫描频率下拉：中文显示秒数备注，英文直接展示 ms 值
const scanFrequencyOptions = computed(() => {
  const withSeconds = (ms) => ({
    value: ms,
    label: isZh.value ? `${ms} ms（${ms / 1000}秒）` : `${ms} ms (${ms / 1000}s)`,
  })
  return [
    { value: 100, label: '100 ms' },
    { value: 200, label: '200 ms' },
    { value: 500, label: '500 ms' },
    withSeconds(1000),
    withSeconds(2000),
    withSeconds(5000),
    withSeconds(10000),
    withSeconds(30000),
    withSeconds(60000),
  ]
})

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()
const deviceObjectId = route.params.deviceObjectId
const deviceObjectName = route.query.deviceObjectName || ''

// ---------- 协议地址示例 ----------
// 地址写法随协议完全不同（"D100" 在三菱指 D 寄存器、在欧姆龙 FINS 指 DM 区），
// 实例化在 utils/addressSyntax.js，这里只负责取。协议未登记时回落到通用文案，
// 宁可不举例也不给一个错的例子。
const protocolName = computed(() => route.query.protocolName || '')
const addressSpec = computed(() => getAddressSpec(protocolName.value))
const addressExample = computed(() => getAddressExample(protocolName.value))
const addressPlaceholder = computed(() =>
  addressExample.value
    ? t('deviceAddress.namePlaceholderExample', { example: addressExample.value })
    : t('deviceAddress.namePlaceholder'),
)

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

// ---------- 复制 ID ----------
async function copyId(id) {
  try {
    await navigator.clipboard.writeText(String(id))
    ElMessage.success(t('deviceAddress.idCopied'))
  } catch {
    ElMessage.error(t('deviceAddress.copyFailed'))
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

// ---------- 获取列表 ----------
async function fetchList() {
  loading.value = true
  try {
    const params = {
      deviceObjectId,
      page: currentPage.value,
      size: pageSize.value,
    }
    if (queryForm.name) params.name = queryForm.name
    const res = await getDeviceAddressList(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
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
  importFn: (file) => importDeviceAddresses(file, deviceObjectId),
  downloadFn: downloadDeviceAddressTemplate,
  templateName: () => t('deviceAddress.importTemplateName'),
  refresh: fetchList,
})

// ---------- 对话框状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const formRef = ref(null)

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
  label: '',
  dataType: '',
  rwPermission: '',
  scanFrequency: null,
  description: '',
  status: 1,
})

// ---------- 表单校验规则 ----------
const formRules = {
  name: [
    { required: true, message: () => t('deviceAddress.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('deviceAddress.nameLength'), trigger: 'blur' },
  ],
  label: [
    { required: true, message: () => t('deviceAddress.tagRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('deviceAddress.tagLength'), trigger: 'blur' },
  ],
  dataType: [{ required: true, message: () => t('deviceAddress.selectDataType'), trigger: 'change' }],
  rwPermission: [{ required: true, message: () => t('deviceAddress.selectRw'), trigger: 'change' }],
  scanFrequency: [{ required: true, message: () => t('deviceAddress.selectScan'), trigger: 'change' }],
  description: [{ max: 200, message: () => t('deviceAddress.descMaxLength'), trigger: 'blur' }],
}

// ---------- 新增 ----------
function handleAdd() {
  isEdit.value = false
  resetFormData()
  dialogVisible.value = true
}

// ---------- 编辑 ----------
function handleEdit(row) {
  isEdit.value = true
  formData.id = row.id
  formData.name = row.name
  formData.label = row.label ?? ''
  formData.dataType = row.commonDataType ?? ''
  formData.rwPermission = row.rwPermission ?? ''
  formData.scanFrequency = row.scanFrequency ?? null
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1
  dialogVisible.value = true
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
    ElMessage.success(t('protocol.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

// ---------- 对话框关闭 ----------
function handleDialogClose() {
  formRef.value?.resetFields()
}

// ---------- 重置表单数据 ----------
function resetFormData() {
  formData.id = ''
  formData.name = ''
  formData.label = ''
  formData.dataType = ''
  formData.rwPermission = ''
  formData.scanFrequency = null
  formData.description = ''
  formData.status = 1
}

// ---------- 提交 ----------
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
      deviceId: deviceObjectId,
      name: formData.name.trim(),
      label: formData.label.trim(),
      commonDataType: formData.dataType,
      rwPermission: formData.rwPermission,
      scanFrequency: formData.scanFrequency,
      description: formData.description.trim() || undefined,
      status: formData.status,
    }

    if (isEdit.value) {
      payload.id = formData.id
      await updateDeviceAddress(payload)
      ElMessage.success(t('protocol.editSuccess'))
    } else {
      await addDeviceAddress(payload)
      ElMessage.success(t('protocol.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {
    
  } finally {
    submitting.value = false
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
  fetchDataTypes()
})
</script>
