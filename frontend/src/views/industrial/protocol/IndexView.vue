<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">协议管理</h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            placeholder="请输入协议名称搜索"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">查询</el-button>
          <el-button @click="handleReset">重置</el-button>
        </div>
        <el-button v-if="userStore.isAdmin" type="primary" @click="handleAdd">新增协议</el-button>
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
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column prop="description" label="描述" min-width="180" show-overflow-tooltip />
        <el-table-column prop="sort" label="排序" width="80" align="center" />
        <el-table-column prop="status" label="状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 1 ? 'success' : 'info'" size="small">
              {{ row.status === 1 ? '启用' : '停用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" label="创建时间" width="180" align="center" />
        <el-table-column label="操作" width="130" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
              <el-tooltip content="查看表单" placement="top">
                <el-button type="primary" link size="small" :icon="View" @click="handleViewForm(row)" />
              </el-tooltip>
              <el-tooltip v-if="userStore.isAdmin" content="编辑" placement="top">
                <el-button type="primary" link size="small" :icon="EditPen" @click="handleEdit(row)" />
              </el-tooltip>
              <el-tooltip v-if="userStore.isAdmin" content="删除" placement="top">
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
      :title="isEdit ? '编辑协议' : '新增协议'"
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
        <el-form-item label="名称" prop="name">
          <el-input
            v-model="formData.name"
            placeholder="请输入协议名称（1-50 个字符）"
            maxlength="50"
            show-word-limit
          />
        </el-form-item>
        <el-form-item label="描述" prop="description">
          <el-input
            v-model="formData.description"
            placeholder="请输入协议描述"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>
        <el-form-item label="排序" prop="sort">
          <el-input-number
            v-model="formData.sort"
            :min="0"
            :max="9999"
            placeholder="排序值"
          />
        </el-form-item>
        <el-form-item label="状态" prop="status">
          <el-switch
            v-model="formData.status"
            :active-value="1"
            :inactive-value="0"
            active-text="启用"
            inactive-text="停用"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          确定
        </el-button>
      </template>
    </el-dialog>

    <!-- 查看表单对话框 -->
    <el-dialog
      v-model="viewDialogVisible"
      :title="`查看表单 —— ${viewFormName}`"
      width="min(800px, 92vw)"
      :close-on-click-modal="false"
      @close="handleViewDialogClose"
    >
      <div v-loading="viewFormLoading" class="min-h-[200px]">
        <el-alert
          v-if="viewFormError"
          :title="viewFormError"
          type="warning"
          show-icon
          :closable="false"
        />
        <pre
          v-if="viewFormRawJson && !viewFormLoading"
          class="bg-gray-50 border border-gray-200 rounded p-4 text-sm leading-relaxed overflow-auto max-h-[min(500px,60vh)] whitespace-pre-wrap break-all"
        >{{ viewFormRawJson }}</pre>
      </div>
      <template #footer>
        <el-button @click="handleCopyJson" :disabled="!viewFormRawJson" v-if="viewFormRawJson">
          复制 JSON
        </el-button>
        <el-button @click="viewDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Delete, EditPen, View } from '@element-plus/icons-vue'
import { useUserStore } from '@/store'
import {
  getProtocolList,
  addProtocol,
  updateProtocol,
  deleteProtocol,
} from '@/api/modules/protocol'

const userStore = useUserStore()

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
    const res = await getProtocolList(params)
    // 兼容两种响应格式：直接返回数组 / 返回 { records: [], total }
    if (Array.isArray(res)) {
      tableData.value = res
      total.value = res.length
    } else {
      tableData.value = res?.records ?? []
      total.value = res?.total ?? 0
    }
  } catch (err) {
    console.error('获取协议列表失败', err)
    ElMessage.error('获取列表失败')
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
  sort: undefined,
  status: 1,
})

// ---------- 表单校验规则 ----------
const formRules = {
  name: [
    { required: true, message: '请输入协议名称', trigger: 'blur' },
    { min: 1, max: 50, message: '名称长度在 1 到 50 个字符', trigger: 'blur' },
  ],
  description: [
    { max: 200, message: '描述不能超过 200 个字符', trigger: 'blur' },
  ],
}

// ---------- 操作 ----------
function handleAdd() {
  isEdit.value = false
  formData.status = 1
  dialogVisible.value = true
}

function handleEdit(row) {
  isEdit.value = true
  formData.id = row.id
  formData.name = row.name
  formData.description = row.description ?? ''
  formData.sort = row.sort ?? undefined
  formData.status = row.status ?? 1
  dialogVisible.value = true
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(`确定要删除协议「${row.name}」吗？`, '删除确认', {
      type: 'warning',
      confirmButtonText: '确定',
      cancelButtonText: '取消',
    })
    await deleteProtocol(row.id)
    ElMessage.success('删除成功')
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

function handleDialogClose() {
  formRef.value?.resetFields()
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
      name: formData.name.trim(),
      description: formData.description.trim(),
      sort: formData.sort,
      status: formData.status,
    }
    if (isEdit.value) {
      payload.id = formData.id
      await updateProtocol(payload)
      ElMessage.success('编辑成功')
    } else {
      await addProtocol(payload)
      ElMessage.success('新增成功')
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {
    const msg = isEdit.value ? '编辑失败' : '新增失败'
    console.error(msg, err)
    ElMessage.error(err?.response?.data?.msg || err?.message || msg)
  } finally {
    submitting.value = false
  }
}

// ---------- 查看表单 ----------
const viewDialogVisible = ref(false)
const viewFormName = ref('')
const viewFormRawJson = ref('')
const viewFormLoading = ref(false)
const viewFormError = ref('')

/** 尝试深度解析可能被二次编码的 JSON 字段 */
function tryDeepParse(value) {
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value)
      return tryDeepParse(parsed)
    } catch {
      return value
    }
  }
  if (Array.isArray(value)) {
    return value.map(tryDeepParse)
  }
  if (value && typeof value === 'object') {
    const obj = {}
    for (const key of Object.keys(value)) {
      obj[key] = tryDeepParse(value[key])
    }
    return obj
  }
  return value
}

function handleViewForm(row) {
  viewFormName.value = row.name
  viewFormLoading.value = true
  viewFormError.value = ''
  viewFormRawJson.value = ''
  viewDialogVisible.value = true

  try {
    const formJson = row.formJson
    if (!formJson) {
      viewFormError.value = '暂无表单配置'
      return
    }

    // 兼容后端返回字符串或对象，统一格式化为美观 JSON 展示
    const obj = typeof formJson === 'string' ? JSON.parse(formJson) : formJson
    // 二次解析：rule / options 等字段可能仍是 JSON 字符串，递归解开
    const deep = tryDeepParse(obj)
    viewFormRawJson.value = JSON.stringify(deep, null, 2)
  } catch (e) {
    viewFormError.value = '表单配置解析失败，原始内容：' + String(row.formJson)
    console.error('解析 formJson 失败', e)
  } finally {
    viewFormLoading.value = false
  }
}

function handleViewDialogClose() {
  viewFormRawJson.value = ''
  viewFormError.value = ''
}

function handleCopyJson() {
  if (!viewFormRawJson.value) return
  try {
    navigator.clipboard.writeText(viewFormRawJson.value)
    ElMessage.success('JSON 已复制到剪贴板')
  } catch {
    // 降级方案：创建临时 textarea 复制
    const textarea = document.createElement('textarea')
    textarea.value = viewFormRawJson.value
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    ElMessage.success('JSON 已复制到剪贴板')
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>
