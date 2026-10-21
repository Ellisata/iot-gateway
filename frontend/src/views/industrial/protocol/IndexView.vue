<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ t('menu.protocol') }}</h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-3">
          <el-input
            v-model="queryForm.name"
            :placeholder="t('protocol.searchPlaceholder')"
            clearable
            style="width: 240px"
            @keyup.enter="handleSearch"
          />
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <!-- 只有「新增插件」这一个入口：协议行全部由插件供给，手工建的行没有驱动与之对应 -->
        <div class="flex items-center gap-3">
          <el-button v-if="userStore.isAdmin" type="primary" @click="handleOpenPluginDialog">
            {{ t('protocol.addPlugin') }}
          </el-button>
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
        <el-table-column prop="name" :label="t('common.name')" min-width="140" />
        <el-table-column prop="description" :label="t('common.description')" min-width="180" show-overflow-tooltip />
        <el-table-column prop="sort" :label="t('protocol.sort')" width="80" align="center" />
        <el-table-column prop="createdAt" :label="t('common.createdAt')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="130" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
              <el-tooltip :content="t('protocol.viewForm')" placement="top">
                <el-button type="primary" link size="small" :icon="View" @click="handleViewForm(row)" />
              </el-tooltip>
              <el-tooltip v-if="userStore.isAdmin" :content="t('common.edit')" placement="top">
                <el-button type="primary" link size="small" :icon="EditPen" @click="handleEdit(row)" />
              </el-tooltip>
              <el-tooltip v-if="userStore.isAdmin" :content="t('protocol.resetForm')" placement="top">
                <el-button type="warning" link size="small" :icon="RefreshLeft" @click="handleResetForm(row)" />
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

    <!-- 编辑协议对话框（无新增模式：协议只能由插件供给） -->
    <el-dialog
      v-model="dialogVisible"
      :title="t('protocol.editTitle')"
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
        <!-- 协议名只读：它同时是设备的解析目标与插件驱动的注册名，
             改名会让引用它的设备静默停采（后端也会拒绝） -->
        <el-form-item :label="t('common.name')" prop="name">
          <el-input v-model="formData.name" disabled />
          <div class="text-xs text-gray-500 mt-1 leading-relaxed">
            {{ t('protocol.nameImmutableHint') }}
          </div>
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            :placeholder="t('protocol.descPlaceholder')"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('protocol.sort')" prop="sort">
          <el-input-number
            v-model="formData.sort"
            :min="0"
            :max="9999"
            :placeholder="t('protocol.sortPlaceholder')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 查看表单对话框 -->
    <el-dialog
      v-model="viewDialogVisible"
      :title="t('protocol.viewFormTitle', { name: viewFormName })"
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
          {{ t('protocol.copyJson') }}
        </el-button>
        <el-button @click="viewDialogVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- 新增插件（批量上传）对话框 -->
    <el-dialog
      v-model="pluginDialogVisible"
      :title="t('protocol.pluginDialogTitle')"
      width="min(720px, 94vw)"
      :close-on-click-modal="false"
      @close="handlePluginDialogClose"
    >
      <div v-loading="pluginInfoLoading" class="min-h-[60px]">
        <!-- 目标环境：先说清「该传什么样的文件」，再让用户选文件 -->
        <el-descriptions v-if="pluginInfo" :column="1" size="small" border class="mb-4">
          <el-descriptions-item :label="t('protocol.pluginTarget')">
            {{ pluginInfo.os }}/{{ pluginInfo.arch }}
          </el-descriptions-item>
          <el-descriptions-item :label="t('protocol.pluginDir')">
            <span class="break-all">{{ pluginInfo.dir }}</span>
          </el-descriptions-item>
          <el-descriptions-item :label="t('protocol.pluginRule')">
            {{ pluginInfo.glob }}{{ pluginInfo.fileExt }}
          </el-descriptions-item>
        </el-descriptions>

        <el-alert
          v-if="pluginInfo && !pluginInfo.enabled"
          :title="t('protocol.pluginDisabled')"
          type="error"
          show-icon
          :closable="false"
          class="mb-4"
        />

        <el-upload
          ref="pluginUploadRef"
          v-model:file-list="pluginFileList"
          drag
          multiple
          :auto-upload="false"
          :accept="pluginAccept"
          :limit="pluginBatchLimit"
          :disabled="pluginSubmitting || (pluginInfo && !pluginInfo.enabled)"
          :on-exceed="handlePluginExceed"
        >
          <el-icon class="el-icon--upload"><upload-filled /></el-icon>
          <div class="el-upload__text">{{ t('protocol.pluginDropHint') }}</div>
          <template #tip>
            <div class="el-upload__tip">
              {{ t('protocol.pluginDropTip', { limit: pluginBatchLimit }) }}
            </div>
          </template>
        </el-upload>

        <!-- 逐文件结果：失败项直接给出后端的原因（含平台不符的实际格式） -->
        <div v-if="pluginResult" class="mt-4">
          <el-alert
            :title="pluginResultTitle"
            :type="pluginResult.failed === 0 ? 'success' : 'warning'"
            show-icon
            :closable="false"
          />
          <div v-if="pluginResult.hint" class="text-xs text-gray-500 mt-2 leading-relaxed">
            {{ pluginResult.hint }}
          </div>
          <div
            v-if="pluginRefreshing"
            class="flex items-center gap-1 text-xs text-blue-500 mt-2"
          >
            <el-icon class="is-loading"><loading /></el-icon>
            {{ t('protocol.pluginListRefreshing') }}
          </div>

          <el-table
            v-if="pluginResult.errors?.length"
            :data="pluginResult.errors"
            size="small"
            border
            class="mt-3"
          >
            <el-table-column
              prop="name"
              :label="t('protocol.pluginFileName')"
              min-width="180"
              show-overflow-tooltip
            />
            <el-table-column
              prop="reason"
              :label="t('protocol.pluginFailReason')"
              min-width="240"
              show-overflow-tooltip
            />
          </el-table>

          <div
            v-for="(warn, i) in pluginResult.warnings || []"
            :key="i"
            class="text-xs text-gray-500 mt-2"
          >
            {{ warn }}
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="pluginDialogVisible = false">{{ t('common.close') }}</el-button>
        <el-button
          type="primary"
          :loading="pluginSubmitting"
          :disabled="!pluginFileList.length || (pluginInfo && !pluginInfo.enabled)"
          @click="handlePluginSubmit"
        >
          {{ t('protocol.pluginUpload') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconCloudUpload as UploadFilled,
  IconEye as View,
  IconLoader2 as Loading,
  IconPencil as EditPen,
  IconRotate as RefreshLeft,
} from '@tabler/icons-vue'
import { useUserStore } from '@/store'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
import {
  getProtocolList,
  updateProtocol,
  resetProtocolForm,
} from '@/api/modules/protocol'
import { getPluginInfo, importPlugins } from '@/api/modules/plugin'

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
//
// silent=true 供后台轮询使用：不翻表格的 loading，失败也只写控制台。
// 轮询期间网络抖一下不该在屏幕上弹提示（axios 拦截器已兜过一层）。
async function fetchList({ silent = false } = {}) {
  if (!silent) loading.value = true
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
    if (!silent) ElMessage.error(t('protocol.fetchListFailed'))
  } finally {
    if (!silent) loading.value = false
  }
}

/**
 * 列表身份指纹：总行数 + 当前页协议行 id。
 *
 * 带上总行数是因为新协议按 sort=100 排在末尾，大概率不落在用户当前翻到的那一页上；
 * 只看当前页的 id 会漏判，白等到超时。
 */
function listSignature() {
  return `${total.value}|${tableData.value.map((row) => row.id).join(',')}`
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
// 只有「编辑协议」一个弹窗：协议只能由插件供给（新增入口即上传插件），
// 故不再有新增模式。
const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref(null)

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
  description: '',
  sort: undefined,
})

// ---------- 表单校验规则 ----------
const formRules = {
  name: [
    { required: true, message: () => t('protocol.nameRequired'), trigger: 'blur' },
    { min: 1, max: 50, message: () => t('protocol.nameLength'), trigger: 'blur' },
  ],
  description: [
    { max: 200, message: () => t('protocol.descMaxLength'), trigger: 'blur' },
  ],
}

// ---------- 操作 ----------
function handleEdit(row) {
  formData.id = row.id
  formData.name = row.name
  formData.description = row.description ?? ''
  formData.sort = row.sort ?? undefined
  dialogVisible.value = true
}

async function handleResetForm(row) {
  try {
    await ElMessageBox.confirm(
      t('protocol.resetFormConfirm', { name: row.name }),
      t('protocol.resetFormTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
    await resetProtocolForm(row.id)
    ElMessage.success(t('protocol.resetFormSuccess'))
    fetchList()
  } catch {
    // 用户取消；失败原因由 axios 响应拦截器统一提示
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
    await updateProtocol({
      id: formData.id,
      // 名字只读，原样回传供后端校验它没被改动（后端会拒绝任何改名）
      name: formData.name.trim(),
      description: formData.description.trim(),
      sort: formData.sort,
    })
    ElMessage.success(t('protocol.editSuccess'))
    dialogVisible.value = false
    fetchList()
  } catch {
    // 失败原因由 axios 响应拦截器统一提示
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
      viewFormError.value = t('protocol.noFormConfig')
      return
    }

    // 兼容后端返回字符串或对象，统一格式化为美观 JSON 展示
    const obj = typeof formJson === 'string' ? JSON.parse(formJson) : formJson
    // 二次解析：rule / options 等字段可能仍是 JSON 字符串，递归解开
    const deep = tryDeepParse(obj)
    viewFormRawJson.value = JSON.stringify(deep, null, 2)
  } catch (e) {
    viewFormError.value = t('protocol.formParseFailed', { raw: String(row.formJson) })
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
    ElMessage.success(t('protocol.jsonCopied'))
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
    ElMessage.success(t('protocol.jsonCopied'))
  }
}

// ---------- 新增插件（批量上传） ----------

/** 单次上传的文件数上限，与后端 service.MaxPluginBatch 保持一致 */
const pluginBatchLimit = 50

const pluginDialogVisible = ref(false)
const pluginInfoLoading = ref(false)
const pluginSubmitting = ref(false)
const pluginInfo = ref(null)
const pluginFileList = ref([])
const pluginResult = ref(null)
const pluginUploadRef = ref(null)

/**
 * 文件选择器的扩展名过滤。
 *
 * Windows 上插件必须是 .exe，限定后用户就选不到注定被拒的文件；
 * 其余平台插件没有扩展名，无从过滤，返回 undefined 表示不过滤。
 * 注意 accept 只影响选择器，挡不住拖拽——真正的判定始终在后端。
 */
const pluginAccept = computed(() => pluginInfo.value?.fileExt || undefined)

const pluginResultTitle = computed(() => {
  if (!pluginResult.value) return ''
  const { total, success, failed } = pluginResult.value
  return failed === 0
    ? t('protocol.pluginUploadSuccess', { success, total })
    : t('protocol.pluginUploadPartial', { success, failed, total })
})

/** 打开对话框：每次都重新拉取环境信息（网关可能被换了平台或改了配置） */
async function handleOpenPluginDialog() {
  pluginDialogVisible.value = true
  pluginFileList.value = []
  pluginResult.value = null
  pluginInfo.value = null
  pluginInfoLoading.value = true
  try {
    pluginInfo.value = await getPluginInfo()
  } catch {
    // 拦截器已提示；此时对话框里没有环境信息，提交按钮不可用
  } finally {
    pluginInfoLoading.value = false
  }
}

function handlePluginExceed() {
  ElMessage.warning(t('protocol.pluginExceedLimit', { limit: pluginBatchLimit }))
}

// 上传成功后等待插件生效的轮询参数。
//
// 插件文件落盘 ≠ 生效：要等后台目录扫描（默认 10s 一轮）发现它、拉起进程、
// 握手成功，才会供给出 iot_protocol 行。上传接口返回时这些行都还不存在，
// 所以刷一次必然看不到 —— 按固定间隔重试，列表一变就停，最多等到超时。
const pluginPollInterval = 3000
const pluginPollTimeout = 30000

const pluginRefreshing = ref(false)
// 轮询令牌：连着传两批插件时，后一批让前一批的轮询立刻自弃，
// 避免两条轮询同时刷同一张表。
let pluginPollToken = 0

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/**
 * 轮询协议列表，直到新插件供给的行出现（或超时）。
 *
 * baseline 是上传前的列表指纹。热升级（协议行集合不变）的情形不会命中，
 * 会一直等到超时 —— 那个情形本就没有新行可等，多刷几次也无害。
 */
async function pollListAfterPluginUpload(baseline) {
  const token = ++pluginPollToken
  pluginRefreshing.value = true
  const deadline = Date.now() + pluginPollTimeout
  try {
    while (token === pluginPollToken && Date.now() < deadline) {
      await sleep(pluginPollInterval)
      if (token !== pluginPollToken) return
      await fetchList({ silent: true })
      if (listSignature() !== baseline) return
    }
  } finally {
    // 令牌已被后来者接管时不动刷新态，那是新轮询的。
    if (token === pluginPollToken) pluginRefreshing.value = false
  }
}

async function handlePluginSubmit() {
  const files = pluginFileList.value.map((f) => f.raw).filter(Boolean)
  if (!files.length) {
    ElMessage.warning(t('protocol.pluginNoFile'))
    return
  }

  // 上传前的指纹要在请求发出前取：文件落盘后一轮扫描很快，
  // 若等响应回来再取，可能已经把新行算进 baseline，轮询就永远等不到变化。
  const baseline = listSignature()

  pluginSubmitting.value = true
  pluginResult.value = null
  try {
    const res = await importPlugins(files)
    pluginResult.value = res
    if (res.failed === 0) {
      ElMessage.success(t('protocol.pluginUploadSuccess', { success: res.success, total: res.total }))
    } else {
      ElMessage.warning(
        t('protocol.pluginUploadPartial', { success: res.success, failed: res.failed, total: res.total }),
      )
    }
    if (res.success > 0) {
      // 插件要等下一轮目录扫描（约 10s）才会被加载并供给 iot_protocol 行，
      // 此刻刷新多半还是旧列表 —— 改为轮询，新协议一出现就自动刷新并停下。
      pollListAfterPluginUpload(baseline)
    }
    // 清空待传队列：成功的已落盘，失败的已在下方的结果表里逐条给出原因，
    // 用户据此换文件重传比对着残留列表猜更直接。
    pluginFileList.value = []
  } catch {
    // 拦截器已提示（含 plugin.enabled=false、重名、数量超限等整单失败）
  } finally {
    pluginSubmitting.value = false
  }
}

function handlePluginDialogClose() {
  pluginUploadRef.value?.clearFiles()
  pluginFileList.value = []
  pluginResult.value = null
  pluginInfo.value = null
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})

onUnmounted(() => {
  // 换代即作废：组件没了还继续轮询，只是对着已销毁的 ref 空刷。
  pluginPollToken++
})
</script>
