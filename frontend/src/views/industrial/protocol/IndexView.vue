<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与设备对象页同一套字号/留白层级；右侧常驻协议数 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ t('menu.protocol') }}</h1>
        <p class="iot-sub">{{ t('protocol.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('protocol.totalUnit') }}</span>
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
            :placeholder="t('protocol.searchPlaceholder')"
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
        <!-- 只有「新增插件」这一个入口：协议行全部由插件供给，手工建的行没有驱动与之对应 -->
        <div class="iot-toolbar-actions">
          <el-button
            v-if="userStore.isAdmin"
            type="primary"
            :icon="Plus"
            @click="handleOpenPluginDialog"
          >
            {{ t('protocol.addPlugin') }}
          </el-button>
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <!-- 协议名是这一行的身份：小图标底座让「一个协议」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('common.name')" min-width="200">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><PlugConnected /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('common.description')"
          min-width="220"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('protocol.sort')" width="88" align="center" class-name="iot-col-num">
          <template #default="{ row }">
            <span :class="row.sort == null ? 'iot-cell-muted' : ''">{{ row.sort ?? '—' }}</span>
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
              <el-tooltip :content="t('protocol.viewForm')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="View"
                  :aria-label="t('protocol.viewForm')"
                  @click="handleViewForm(row)"
                />
              </el-tooltip>
              <el-tooltip v-if="userStore.isAdmin" :content="t('common.edit')" placement="top">
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
              <!-- 恢复默认表单会覆盖当前改动，用琥珀色提示它与编辑不是同一类动作 -->
              <el-tooltip v-if="userStore.isAdmin" :content="t('protocol.resetForm')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="warning"
                  link
                  size="small"
                  :icon="RefreshLeft"
                  :aria-label="t('protocol.resetForm')"
                  @click="handleResetForm(row)"
                />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><PlugConnected /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.name
                ? t('protocol.emptySearch', { name: queryForm.name })
                : t('protocol.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name" class="iot-empty-hint">{{ t('protocol.emptyHint') }}</p>
            <el-button v-if="queryForm.name" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button
              v-else-if="userStore.isAdmin"
              type="primary"
              :icon="Plus"
              @click="handleOpenPluginDialog"
            >
              {{ t('protocol.addPlugin') }}
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

    <!-- 编辑协议对话框（无新增模式：协议只能由插件供给） -->
    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog"
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
          <div class="iot-form-hint is-block">
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
      class="iot-dialog"
      :title="t('protocol.viewFormTitle', { name: viewFormName })"
      width="min(800px, 92vw)"
      :close-on-click-modal="false"
      @close="handleViewDialogClose"
    >
      <div v-loading="viewFormLoading" class="view-body">
        <div v-if="viewFormError" class="iot-notice is-warning">
          <span class="iot-notice-mark" aria-hidden="true">
            <el-icon><AlertTriangle /></el-icon>
          </span>
          <p class="iot-notice-text">{{ viewFormError }}</p>
        </div>
        <pre
          v-if="viewFormRawJson && !viewFormLoading"
          class="iot-code"
        >{{ viewFormRawJson }}</pre>
      </div>
      <template #footer>
        <el-button v-if="viewFormRawJson" :disabled="!viewFormRawJson" @click="handleCopyJson">
          {{ t('protocol.copyJson') }}
        </el-button>
        <el-button @click="viewDialogVisible = false">{{ t('common.close') }}</el-button>
      </template>
    </el-dialog>

    <!-- 新增插件（批量上传）对话框 -->
    <el-dialog
      v-model="pluginDialogVisible"
      class="iot-dialog"
      :title="t('protocol.pluginDialogTitle')"
      width="min(720px, 94vw)"
      :close-on-click-modal="false"
      @close="handlePluginDialogClose"
    >
      <div v-loading="pluginInfoLoading" class="plugin-body">
        <!-- 目标环境：先说清「该传什么样的文件」，再让用户选文件 -->
        <el-descriptions
          v-if="pluginInfo"
          :column="1"
          size="small"
          border
          class="plugin-meta"
        >
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

        <div v-if="pluginInfo && !pluginInfo.enabled" class="iot-notice is-error">
          <span class="iot-notice-mark" aria-hidden="true">
            <el-icon><AlertTriangle /></el-icon>
          </span>
          <p class="iot-notice-text">{{ t('protocol.pluginDisabled') }}</p>
        </div>

        <el-upload
          ref="pluginUploadRef"
          v-model:file-list="pluginFileList"
          class="plugin-upload"
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
        <div v-if="pluginResult" class="plugin-result">
          <div
            class="iot-notice"
            :class="pluginResult.failed === 0 ? 'is-success' : 'is-warning'"
          >
            <span class="iot-notice-mark" aria-hidden="true">
              <el-icon>
                <Check v-if="pluginResult.failed === 0" />
                <AlertTriangle v-else />
              </el-icon>
            </span>
            <p class="iot-notice-text">{{ pluginResultTitle }}</p>
          </div>
          <p v-if="pluginResult.hint" class="plugin-note">{{ pluginResult.hint }}</p>
          <div v-if="pluginRefreshing" class="plugin-refresh">
            <el-icon class="is-loading"><loading /></el-icon>
            {{ t('protocol.pluginListRefreshing') }}
          </div>

          <el-table
            v-if="pluginResult.errors?.length"
            :data="pluginResult.errors"
            class="iot-mini-table plugin-errors"
            size="small"
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

          <p
            v-for="(warn, i) in pluginResult.warnings || []"
            :key="i"
            class="plugin-note"
          >
            {{ warn }}
          </p>
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
  IconAlertTriangle as AlertTriangle,
  IconCheck as Check,
  IconCloudUpload as UploadFilled,
  IconEye as View,
  IconLoader2 as Loading,
  IconPencil as EditPen,
  IconPlugConnected as PlugConnected,
  IconPlus as Plus,
  IconRotate as RefreshLeft,
  IconSearch as Search,
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

// 首屏还没拿到数时显示占位符而不是 0：0 个协议会被当成"一个插件都没装"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

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

<style scoped>
.view-body {
  min-height: 200px;
}

.plugin-body {
  min-height: 60px;
}

/* 插件环境说明表：去掉通用边框的灰，让「平台 / 目录 / 命名规则」读起来是一段说明 */
.plugin-meta {
  margin-bottom: 16px;
  overflow: hidden;
  border-radius: 12px;
}

.plugin-meta :deep(.el-descriptions__body) {
  background: #fbfcfe;
}

.plugin-meta :deep(.el-descriptions__label) {
  width: 96px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ink-4);
}

.plugin-meta :deep(.el-descriptions__content) {
  font-size: 12.5px;
  color: var(--ink-2);
}

.plugin-meta :deep(.el-descriptions__cell) {
  border-color: var(--line-soft);
}

/* 上传区：虚线框同色系，悬停才染蓝，避免整块区域过重 */
.plugin-upload {
  display: block;
}

.plugin-upload :deep(.el-upload) {
  width: 100%;
}

.plugin-upload :deep(.el-upload-dragger) {
  padding: 26px 16px;
  background: #fbfcfe;
  border: 1px dashed #d7e0ea;
  border-radius: 14px;
  transition: border-color 0.18s ease, background-color 0.18s ease;
}

.plugin-upload :deep(.el-upload-dragger:hover) {
  background: var(--brand-soft);
  border-color: var(--brand);
}

.plugin-upload :deep(.el-icon--upload) {
  margin-bottom: 6px;
  font-size: 38px;
  color: var(--brand);
}

.plugin-upload :deep(.el-upload__text) {
  font-size: 13px;
  color: var(--ink-3);
}

.plugin-upload :deep(.el-upload__text em) {
  font-style: normal;
  font-weight: 600;
  color: var(--brand);
}

.plugin-upload :deep(.el-upload__tip) {
  margin-top: 8px;
  font-size: 12px;
  color: var(--ink-4);
}

.plugin-result {
  margin-top: 16px;
}

.plugin-note {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
}

.plugin-refresh {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--brand);
}

.plugin-errors {
  margin-top: 12px;
  overflow: hidden;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}
</style>
