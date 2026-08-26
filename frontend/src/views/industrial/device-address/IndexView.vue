<template>
  <div class="p-6">
    <!-- 面包屑导航 -->
    <div class="flex items-center gap-2 text-sm text-gray-500 mb-4">
      <el-button text @click="goBack">
        <el-icon><ArrowLeft /></el-icon>
        返回设备对象列表
      </el-button>
    </div>

    <h1 class="text-2xl font-bold text-gray-800 mb-6">
      设备地址标签 — {{ deviceObjectName }}
    </h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            placeholder="请输入地址名称搜索"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">查询</el-button>
          <el-button @click="handleReset">重置</el-button>
        </div>
        <div class="flex items-center gap-3">
          <el-button :icon="Download" @click="handleDownloadTemplate">下载模板</el-button>
          <el-button type="primary" :icon="Upload" :loading="importing" @click="handleImport">导入</el-button>
          <el-button type="primary" @click="handleAdd">新增地址</el-button>
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
        <el-table-column type="index" label="序号" width="70" align="center" />
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
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column prop="label" label="标签" min-width="140" show-overflow-tooltip />
        <el-table-column prop="dataType" label="数据类型" width="130" align="center" />
        <el-table-column prop="commonDataType" label="通用数据类型" width="140" align="center" />
        <el-table-column prop="rwPermission" label="读写权限" width="110" align="center">
          <template #default="{ row }">
            <el-tag
              :type="row.rwPermission === 'RW' ? 'success' : row.rwPermission === 'R' ? 'primary' : 'warning'"
              size="small"
            >
              {{ { R: '只读', W: '只写', RW: '读写' }[row.rwPermission] || row.rwPermission }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="scanFrequency" label="扫描频率" width="120" align="center">
          <template #default="{ row }">
            {{ row.scanFrequency }} ms
          </template>
        </el-table-column>
        <el-table-column prop="description" label="描述" min-width="180" show-overflow-tooltip />
        <el-table-column prop="status" label="状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'danger'" size="small">
              {{ row.status === 1 ? '启用' : '禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" label="创建时间" width="180" align="center" />
        <el-table-column label="操作" width="100" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
              <el-tooltip content="编辑" placement="top">
                <el-button type="primary" link size="small" :icon="EditPen" @click="handleEdit(row)" />
              </el-tooltip>
              <el-tooltip content="删除" placement="top">
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
      :title="isEdit ? '编辑设备地址' : '新增设备地址'"
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
        <el-form-item label="名称" prop="name">
          <el-input
            v-model="formData.name"
            placeholder="请输入地址名称（1-100 个字符）"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>

        <el-form-item label="标签" prop="label">
          <el-input
            v-model="formData.label"
            placeholder="请输入地址名称的中文说明（如温度、电流）"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>

        <el-form-item label="数据类型" prop="dataType">
          <el-select
            v-model="formData.dataType"
            placeholder="请选择数据类型"
            style="width: 100%"
            :loading="dataTypeLoading"
            loading-text="加载中..."
          >
            <el-option
              v-for="item in dataTypeOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item label="读写权限" prop="rwPermission">
          <el-select v-model="formData.rwPermission" placeholder="请选择读写权限" style="width: 100%">
            <el-option
              v-for="item in rwPermissionOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item label="扫描频率" prop="scanFrequency">
          <el-select v-model="formData.scanFrequency" placeholder="请选择扫描频率" style="width: 100%">
            <el-option
              v-for="item in scanFrequencyOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item label="描述" prop="description">
          <el-input
            v-model="formData.description"
            placeholder="请输入描述（可选）"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>

        <el-form-item label="状态" prop="status">
          <el-switch
            v-model="formData.status"
            :active-value="1"
            :inactive-value="0"
            active-text="启用"
            inactive-text="禁用"
          />
        </el-form-item>
      </el-form>

      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            {{ isEdit ? '保存' : '提交' }}
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
import { ArrowLeft, CopyDocument, Delete, Download, EditPen, Upload } from '@element-plus/icons-vue'
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
    // 优先取设备对象传入的协议名，缺省按 Omron.Net.CIP 处理
    const protocolName = route.query.protocolName || 'Omron.Net.CIP'
    const res = await getDataTypes(protocolName)
    dataTypeOptions.value = normalizeDataTypes(res)
  } catch {
    dataTypeOptions.value = []
  } finally {
    dataTypeLoading.value = false
  }
}

const rwPermissionOptions = computed(() => {
  const options = [
    { value: 'R', label: 'R（只读）' },
    { value: 'W', label: 'W（只写）' },
    { value: 'RW', label: 'RW（读写）' },
  ]
  return isEdit.value ? options : options.filter(o => o.value !== 'W')
})

const scanFrequencyOptions = [
  { value: 100, label: '100 ms' },
  { value: 200, label: '200 ms' },
  { value: 500, label: '500 ms' },
  { value: 1000, label: '1000 ms（1秒）' },
  { value: 2000, label: '2000 ms（2秒）' },
  { value: 5000, label: '5000 ms（5秒）' },
  { value: 10000, label: '10000 ms（10秒）' },
  { value: 30000, label: '30000 ms（30秒）' },
  { value: 60000, label: '60000 ms（60秒）' },
]

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()
const deviceObjectId = route.params.deviceObjectId
const deviceObjectName = route.query.deviceObjectName || ''

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

// ---------- 复制 ID ----------
async function copyId(id) {
  try {
    await navigator.clipboard.writeText(String(id))
    ElMessage.success('ID 已复制')
  } catch {
    ElMessage.error('复制失败')
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
  templateName: '设备地址导入模板.xlsx',
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
    { required: true, message: '请输入地址名称', trigger: 'blur' },
    { min: 1, max: 100, message: '名称长度在 1 到 100 个字符', trigger: 'blur' },
  ],
  label: [
    { required: true, message: '请输入标签（中文说明）', trigger: 'blur' },
    { min: 1, max: 100, message: '标签长度在 1 到 100 个字符', trigger: 'blur' },
  ],
  dataType: [{ required: true, message: '请选择数据类型', trigger: 'change' }],
  rwPermission: [{ required: true, message: '请选择读写权限', trigger: 'change' }],
  scanFrequency: [{ required: true, message: '请选择扫描频率', trigger: 'change' }],
  description: [{ max: 200, message: '描述不能超过 200 个字符', trigger: 'blur' }],
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
    await ElMessageBox.confirm(`确定要删除设备地址「${row.name}」吗？`, '删除确认', {
      type: 'warning',
      confirmButtonText: '确定',
      cancelButtonText: '取消',
    })
    await deleteDeviceAddress(row.id)
    ElMessage.success('删除成功')
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
      ElMessage.success('编辑成功')
    } else {
      await addDeviceAddress(payload)
      ElMessage.success('新增成功')
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
