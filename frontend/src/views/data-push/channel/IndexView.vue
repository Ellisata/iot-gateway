<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与工业物联网各页同一套字号/留白层级；右侧常驻通道数 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.dataPush') }}
        </p>
        <h1 class="iot-title">{{ t('menu.channel') }}</h1>
        <p class="iot-sub">{{ t('channel.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('channel.totalUnit') }}</span>
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
            :placeholder="t('channel.searchPlaceholder')"
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
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('channel.add') }}</el-button>
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column :label="t('channel.type')" width="150">
          <template #default="{ row }">
            <span class="iot-pill is-gray">{{ typeLabel(row.type) }}</span>
          </template>
        </el-table-column>
        <!-- 通道名是这一行的身份：小图标底座让「一个推送出口」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('common.name')" min-width="200">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Broadcast /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('common.description')"
          min-width="240"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('common.status')" width="112" align="center">
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
        <el-table-column :label="t('common.action')" width="110" align="center" fixed="right">
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
              <el-icon><Broadcast /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.name
                ? t('channel.emptySearch', { name: queryForm.name })
                : t('channel.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name" class="iot-empty-hint">{{ t('channel.emptyHint') }}</p>
            <el-button v-if="queryForm.name" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('channel.add') }}
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
      class="iot-dialog channel-dialog"
      :title="isEdit ? t('channel.editTitle') : t('channel.addTitle')"
      width="min(1180px, 96vw)"
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
        <el-form-item :label="t('channel.type')" prop="type">
          <ChannelSelector
            v-model="formData.type"
            :disabled="isEdit"
            @select="handleChannelSelect"
          />
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="formData.name"
            :placeholder="t('channel.namePlaceholder')"
            maxlength="50"
            show-word-limit
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

      <!-- 动态连接配置表单：根据所选通道名称加载。
           用一条发丝线接续，而不是 el-divider —— 分隔线会带着一段居中留白，
           把「基本信息」和「连接参数」切成两张互不相干的表。 -->
      <div v-if="formData.type" class="channel-section">
        <div class="channel-section-head">
          <div class="channel-section-headline">
            <span class="channel-section-mark" aria-hidden="true">
              <el-icon><Connection /></el-icon>
            </span>
            <div>
              <p class="channel-section-name">{{ t('channel.connectConfig') }}</p>
              <p class="channel-section-sub">{{ t('channel.connectParams') }}</p>
            </div>
          </div>
          <el-button
            type="primary"
            link
            :loading="testingConnection"
            :disabled="detailLoading"
            @click="handleTestConnectivity"
          >
            {{ t('channel.testConnection') }}
          </el-button>
        </div>
        <div v-loading="dynamicFormLoading" class="dynamic-form-wrapper">
          <p
            v-if="!dynamicFormLoading && !dynamicFormRule.length"
            class="channel-empty-config"
          >
            {{ t('channel.noConnectConfig') }}
          </p>
          <form-create
            v-else
            :key="dynamicFormKey"
            :rule="dynamicFormRule"
            :option="dynamicFormOptions"
            @mounted="handleDynamicFormMounted"
          />
        </div>
        <p v-if="formData.type === 'http'" class="channel-preview-hint">
          {{ t('channel.httpConnectivityHint') }}
        </p>
      </div>

      <!-- 载荷格式 / 出站映射 / 预览与测试发送：仅消息类通道显示。
           format/mapping 不放进 form-create 动态表单，避免与连接参数互相覆盖。 -->
      <div v-if="selectedTypeMeta?.messageBased" class="channel-section">
        <div class="channel-section-head">
          <div class="channel-section-headline">
            <span class="channel-section-mark" aria-hidden="true">
              <el-icon><Braces /></el-icon>
            </span>
            <div>
              <p class="channel-section-name">{{ t('channel.outputFormat') }}</p>
              <p class="channel-section-sub">{{ t('channel.outputFormatHint') }}</p>
            </div>
          </div>
          <!-- 格式下拉跟标题同排：标题已经写明「输出格式」，不再重复一个表单标签 -->
          <el-select
            v-model="outputFormat"
            class="channel-format-select"
            :disabled="detailLoading"
            @change="handleFormatChange"
          >
            <el-option
              v-for="f in selectedTypeMeta.formats || []"
              :key="f.name"
              :label="f.label || f.name"
              :value="f.name"
            />
          </el-select>
        </div>

        <!-- 用 v-show 而不是 v-if：切到 native-json 再切回来时，
             高级 JSON 模式里尚未保存的文本不会丢。 -->
        <MappingEditor
          v-show="editorKind === 'mapping'"
          ref="mappingEditorRef"
          :state="mappingState"
          :state-id="mappingStateId"
          :disabled="detailLoading || submitting"
          :preview-result="previewResult"
          :preview-loading="previewing"
          :auto-previewing="autoPreviewing"
          @change="onMappingChange"
          @preview="handlePreview"
        />
        <ScriptEditor
          v-show="editorKind === 'script'"
          ref="scriptEditorRef"
          :state="scriptState"
          :state-id="scriptStateId"
          :disabled="detailLoading || submitting"
          :preview-result="previewResult"
          :preview-loading="previewing"
          :auto-previewing="autoPreviewing"
          @change="onScriptChange"
          @preview="handlePreview"
        />

        <div
          v-if="!requiresMapping || selectedTypeMeta.supportsTestSend"
          class="channel-preview-actions"
        >
          <el-button
            v-if="!requiresMapping"
            :loading="previewing"
            :disabled="detailLoading"
            @click="handlePreview"
          >
            {{ t('channel.previewPayload') }}
          </el-button>
          <el-button
            v-if="selectedTypeMeta.supportsTestSend"
            :loading="testSending"
            :disabled="detailLoading"
            @click="handleTestSend"
          >
            {{ t('channel.testSend') }}
          </el-button>
          <span v-if="!requiresMapping" class="channel-preview-hint">
            {{ t('channel.previewHint') }}
          </span>
        </div>
        <pre v-if="!requiresMapping && previewText" class="mapping-preview">{{ previewText }}</pre>
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" :disabled="detailLoading" @click="handleSubmit">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onBeforeUnmount, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconAffiliate as Connection,
  IconBraces as Braces,
  IconBroadcast as Broadcast,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import {
  getChannelList,
  addChannel,
  updateChannel,
  deleteChannel,
  getChannelById,
  testConnectivity,
  previewPushChannel,
  previewPushMapping,
  testSendPushChannel,
} from '@/api/modules/pushChannel'
import { getPushChannelFormByName } from '@/api/modules/pushChannelForm'
import ChannelSelector from '@/components/ChannelSelector.vue'
import MappingEditor from '@/components/pushMapping/MappingEditor.vue'
import ScriptEditor from '@/components/pushMapping/ScriptEditor.vue'
import { usePushChannelTypes } from '@/composables/usePushChannelTypes'
import {
  MappingValidationError,
  createEmptyState,
  defaultMappingState,
  isEmptyMappingState,
  mappingToState,
} from '@/utils/pushMapping'
import { DEFAULT_ENCODE_SCRIPT } from '@/utils/pushScripts'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const { load: loadChannelTypes, metaOf } = usePushChannelTypes()

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

// 首屏还没拿到数时显示占位符而不是 0：0 个通道会被当成"还没配任何推送出口"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

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
  type: '',
  name: '',
  description: '',
  status: 1,
})

// 选中类型的元数据与载荷格式/映射状态
const selectedTypeMeta = computed(() => metaOf(formData.type))
const outputFormat = ref('native-json')
const mappingState = reactive(createEmptyState())
const mappingStateId = ref(0)
const mappingEditorRef = ref(null)
const scriptState = reactive(createEmptyScriptState())
const scriptStateId = ref(0)
const scriptEditorRef = ref(null)
const previewing = ref(false)
const autoPreviewing = ref(false)
const previewResult = ref(null)
const testSending = ref(false)
const detailLoading = ref(false)
const mappingTouched = ref(false)
const scriptTouched = ref(false)
// 无法转换成可视化 state 的历史 mapping（保留原文，切到 JSON 模式展示）
const pendingJson = ref(null)
const previewRequestId = ref(0)
const manualPreviewId = ref(0)
let previewTimer = null

function createEmptyScriptState() {
  return { script: '', timeoutMs: 50, maxMessages: 200 }
}

function defaultScriptState() {
  return {
    script: DEFAULT_ENCODE_SCRIPT,
    timeoutMs: 50,
    maxMessages: 200,
  }
}

function isEmptyScriptState(state) {
  return !String(state?.script || '').trim()
}

const selectedFormatMeta = computed(() =>
  (selectedTypeMeta.value?.formats || []).find((f) => f.name === outputFormat.value),
)

// 编辑器种类由格式元数据决定；老后端没有 editor 字段时按 requiresMapping 回落。
const editorKind = computed(() => {
  if (outputFormat.value === 'mapping-json') return 'mapping'
  if (outputFormat.value === 'script') return 'script'
  const editor = selectedFormatMeta.value?.editor
  if (editor === 'mapping' || editor === 'script') return editor
  return selectedFormatMeta.value?.requiresMapping ? 'mapping' : ''
})

// 沿用旧变量名：表示「当前格式需要一个编辑器」，具体是映射还是脚本看 editorKind。
const requiresMapping = computed(() => editorKind.value !== '')

const previewText = computed(() => {
  const res = previewResult.value
  if (!res) return ''
  if (res.error) return `${t('channel.mapEditor.previewError')}: ${res.error}`
  if (res.payloads?.length > 1) return res.payloads.join('\n')
  return res.payload || (res.payloads?.[0] ?? '')
})

function typeLabel(type) {
  return metaOf(type)?.label || type || '—'
}

function mappingProblemText(problem) {
  if (!problem) return t('channel.mappingJsonInvalid')
  const section = String(problem.code || '').startsWith('script')
    ? 'scriptEditor'
    : 'mapEditor'
  return t(`channel.${section}.problems.${problem.code}`, problem.params || {})
}

function showMappingError(err) {
  if (err instanceof MappingValidationError) {
    ElMessage.warning(mappingProblemText(err.problem))
    return
  }
  ElMessage.warning(err?.message || t('channel.mappingJsonInvalid'))
}

function cancelAutoPreview() {
  if (previewTimer) {
    clearTimeout(previewTimer)
    previewTimer = null
  }
  autoPreviewing.value = false
}

function resetMappingState() {
  Object.assign(mappingState, createEmptyState())
  mappingStateId.value += 1
  mappingTouched.value = false
  Object.assign(scriptState, createEmptyScriptState())
  scriptStateId.value += 1
  scriptTouched.value = false
  pendingJson.value = null
  previewResult.value = null
  previewing.value = false
  autoPreviewing.value = false
  manualPreviewId.value = 0
  cancelAutoPreview()
  previewRequestId.value += 1
}

function activeEditor() {
  if (editorKind.value === 'mapping') return mappingEditorRef.value
  if (editorKind.value === 'script') return scriptEditorRef.value
  return null
}

/** 编辑器改动后延迟自动预览；有结构错误时只清空结果，不发请求。 */
function scheduleAutoPreview() {
  cancelAutoPreview()
  previewRequestId.value += 1
  if (!dialogVisible.value || detailLoading.value || submitting.value) return
  if (!editorKind.value) return

  const editor = activeEditor()
  if (!editor) return
  if (editor.validateProblems().some((p) => p.level === 'error')) {
    previewResult.value = null
    return
  }

  const requestId = previewRequestId.value
  autoPreviewing.value = true
  previewTimer = setTimeout(async () => {
    previewTimer = null
    let mapping
    try {
      mapping = editor.buildMapping()
    } catch {
      if (requestId === previewRequestId.value) autoPreviewing.value = false
      return
    }
    try {
      const res = await previewPushMapping({ format: outputFormat.value, mapping })
      if (requestId === previewRequestId.value) previewResult.value = res
    } catch {
      // 全局拦截器已提示
    } finally {
      if (requestId === previewRequestId.value) autoPreviewing.value = false
    }
  }, 600)
}

function onMappingChange() {
  mappingTouched.value = true
  pendingJson.value = null
  scheduleAutoPreview()
}

function onScriptChange() {
  scriptTouched.value = true
  scheduleAutoPreview()
}

function handleFormatChange(next) {
  previewResult.value = null
  cancelAutoPreview()
  previewRequestId.value += 1

  if (next === 'script') {
    if (!scriptTouched.value && isEmptyScriptState(scriptState)) {
      Object.assign(scriptState, defaultScriptState())
      scriptStateId.value += 1
      scriptTouched.value = true
    }
    nextTick(() => scheduleAutoPreview())
    return
  }
  if (next !== 'mapping-json') return

  if (pendingJson.value) {
    nextTick(() => {
      mappingEditorRef.value?.openJson(pendingJson.value.text, pendingJson.value.problem)
    })
    return
  }
  // JSON 模式里有未转换的文本时，不自动填充，避免覆盖用户正在编辑的内容。
  if (mappingEditorRef.value?.hasJsonDraft?.()) {
    nextTick(() => scheduleAutoPreview())
    return
  }
  if (!mappingTouched.value && isEmptyMappingState(mappingState)) {
    Object.assign(mappingState, defaultMappingState())
    mappingStateId.value += 1
    mappingTouched.value = true
  }
  nextTick(() => scheduleAutoPreview())
}

function resetOutputConfig() {
  outputFormat.value = selectedTypeMeta.value?.defaultFormat || 'native-json'
  resetMappingState()
}

async function applyOutputConfig(config) {
  // 兼容 configJson 被二次编码（字符串里再套 JSON）的历史数据。
  let cfg = config
  for (let i = 0; i < 3 && typeof cfg === 'string'; i += 1) {
    cfg = safeParseJSON(cfg)
  }
  outputFormat.value =
    cfg?.format || selectedTypeMeta.value?.defaultFormat || 'native-json'

  Object.assign(mappingState, createEmptyState())
  mappingStateId.value += 1
  mappingTouched.value = false
  Object.assign(scriptState, createEmptyScriptState())
  scriptStateId.value += 1
  scriptTouched.value = false
  pendingJson.value = null
  previewResult.value = null
  cancelAutoPreview()

  if (editorKind.value === 'script') {
    applyScriptConfig(cfg?.mapping)
    return
  }

  let mapping = cfg?.mapping
  if (typeof mapping === 'string') {
    const trimmed = mapping.trim()
    const parsed = trimmed ? safeParseJSON(mapping) : null
    if (trimmed && parsed == null) {
      pendingJson.value = {
        text: mapping,
        problem: { code: 'jsonSyntax', params: { message: t('channel.mappingJsonInvalid') } },
      }
      mappingTouched.value = true
      await nextTick()
      mappingEditorRef.value?.openJson(pendingJson.value.text, pendingJson.value.problem)
      return
    }
    mapping = parsed
  }

  if (mapping && typeof mapping === 'object') {
    const res = mappingToState(mapping)
    if (res.ok) {
      Object.assign(mappingState, res.state)
    } else {
      pendingJson.value = { text: mapping, problem: { code: res.code, params: res.params } }
      await nextTick()
      mappingEditorRef.value?.openJson(mapping, pendingJson.value.problem)
    }
    mappingTouched.value = true
  }
}

/** 回填 script 格式的 mapping（{script, timeoutMs, maxMessages}）。 */
function applyScriptConfig(mapping) {
  let cfg = mapping
  for (let i = 0; i < 3 && typeof cfg === 'string'; i += 1) {
    const parsed = safeParseJSON(cfg)
    if (parsed == null) break
    cfg = parsed
  }
  if (typeof cfg === 'string') {
    // 兼容直接存脚本原文的配置
    scriptState.script = cfg
  } else if (cfg && typeof cfg === 'object') {
    scriptState.script = typeof cfg.script === 'string' ? cfg.script : ''
    const timeout = Number(cfg.timeoutMs)
    scriptState.timeoutMs =
      Number.isInteger(timeout) && timeout >= 1 && timeout <= 500 ? timeout : 50
    const maxMessages = Number(cfg.maxMessages)
    scriptState.maxMessages =
      Number.isInteger(maxMessages) && maxMessages >= 1 && maxMessages <= 1000
        ? maxMessages
        : 200
  }
  scriptStateId.value += 1
  scriptTouched.value = true
}

function safeParseJSON(text) {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

// ---------- 表单校验规则 ----------
const formRules = {
  type: [
    { required: true, message: () => t('channel.typeRequired'), trigger: 'change' },
  ],
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
 * 按通道类型加载连接配置表单
 * @param {string} type - 通道类型（mqtt/http/tdengine-v3...）
 * @param {Object} [prefillConfig] - 编辑时传入 configJson（通道详情接口），用于回填已有填写值
 */
async function loadDynamicForm(type, prefillConfig) {
  const requestId = ++dynamicFormRequestId.value
  // 先清空旧表单，避免残留上一个通道的字段
  dynamicFormRule.value = []
  dynamicFormOptions.value = {}
  dynamicFormApi.value = null
  dynamicFormPrefill.value = prefillConfig ?? null
  if (!type) return

  dynamicFormLoading.value = true
  try {
    const res = await getPushChannelFormByName(type)
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

/** 类型选择回调：加载该类型的连接配置表单，并重置载荷格式。 */
function handleChannelSelect({ type }) {
  formData.type = type
  resetOutputConfig()
  loadDynamicForm(type)
  if (editorKind.value === 'mapping' && isEmptyMappingState(mappingState)) {
    Object.assign(mappingState, defaultMappingState())
    mappingStateId.value += 1
    mappingTouched.value = true
  } else if (editorKind.value === 'script' && isEmptyScriptState(scriptState)) {
    Object.assign(scriptState, defaultScriptState())
    scriptStateId.value += 1
    scriptTouched.value = true
  }
  nextTick(() => scheduleAutoPreview())
}

// ---------- 表单校验 / configJson 收集 ----------
async function validateForms() {
  if (!formRef.value) return false
  try {
    await formRef.value.validate()
  } catch {
    return false
  }
  if (dynamicFormRule.value.length && dynamicFormApi.value) {
    try {
      await dynamicFormApi.value.validate()
    } catch {
      ElMessage.warning(t('channel.completeConnectConfig'))
      return false
    }
  }
  return true
}

// HTTP/Webhook 的鉴权字段与 authType 一一对应（见迁移 021 的 control 联动）。
// 被联动隐藏的字段仍会出现在 formData 里，这里按当前鉴权方式丢弃其余凭证，
// 避免切换鉴权方式后旧 token / 密码残留进 configJson。
const HTTP_AUTH_CREDENTIAL_FIELDS = {
  bearer: ['bearerToken'],
  basic: ['basicUsername', 'basicPassword'],
  'hmac-sha256': ['hmacSecret', 'hmacHeader', 'hmacPrefix'],
}

function pruneHttpAuthFields(config) {
  if (formData.type !== 'http' || !('authType' in config)) return config
  const keep = new Set(HTTP_AUTH_CREDENTIAL_FIELDS[config.authType] || [])
  Object.values(HTTP_AUTH_CREDENTIAL_FIELDS)
    .flat()
    .forEach((field) => {
      if (!keep.has(field)) delete config[field]
    })
  return config
}

/** 统一构造提交/预览/测试发送的 configJson，保证三处完全一致。 */
function buildConfigJson() {
  const config = { ...(dynamicFormApi.value?.formData?.() ?? {}) }
  const meta = selectedTypeMeta.value
  // 元数据尚未加载时不要把已有的 format/mapping 删掉：按当前格式推断即可。
  const messageBased = meta
    ? meta.messageBased
    : outputFormat.value === 'mapping-json' || outputFormat.value === 'script'
  if (messageBased) {
    config.format = outputFormat.value || meta?.defaultFormat || 'native-json'
    if (editorKind.value) {
      const editor = activeEditor()
      if (!editor) {
        throw new Error(
          t(
            editorKind.value === 'script'
              ? 'channel.scriptRequired'
              : 'channel.mappingRequired',
          ),
        )
      }
      config.mapping = editor.buildMapping()
    } else {
      delete config.mapping
    }
  } else {
    delete config.format
    delete config.mapping
  }
  return pruneHttpAuthFields(config)
}

function buildConfigOrWarn() {
  try {
    return buildConfigJson()
  } catch (err) {
    if (err instanceof MappingValidationError) {
      showMappingError(err)
    } else {
      ElMessage.warning(
        err instanceof SyntaxError
          ? t('channel.mappingJsonInvalid')
          : err?.message || t('channel.mappingJsonInvalid'),
      )
    }
    return null
  }
}

// ---------- 测试连接 ----------
async function handleTestConnectivity() {
  if (!formData.type) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  testingConnection.value = true
  try {
    const res = await testConnectivity({
      type: formData.type,
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

// ---------- 载荷预览 / 测试发送 ----------
async function handlePreview() {
  cancelAutoPreview()
  previewRequestId.value += 1
  const requestId = previewRequestId.value
  manualPreviewId.value = requestId
  // 映射/脚本走编码专用接口：不要求连接配置已经填完整。
  if (editorKind.value) {
    const editor = activeEditor()
    if (!editor) return
    let mapping
    try {
      mapping = editor.buildMapping()
    } catch (err) {
      showMappingError(err)
      return
    }
    previewing.value = true
    try {
      const res = await previewPushMapping({ format: outputFormat.value, mapping })
      if (requestId === previewRequestId.value) previewResult.value = res
    } catch {
      // 全局拦截器已提示
    } finally {
      // 自动预览/编辑会让 previewRequestId 前进，但按钮 loading 必须由
      // 这一次手动请求自己收尾，否则会一直转圈。
      if (manualPreviewId.value === requestId) previewing.value = false
    }
    return
  }

  if (!formData.type) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  previewing.value = true
  try {
    const res = await previewPushChannel({
      type: formData.type,
      configJson,
    })
    if (manualPreviewId.value === requestId) previewResult.value = res
  } catch {
    // 全局拦截器已提示
  } finally {
    if (manualPreviewId.value === requestId) previewing.value = false
  }
}

async function handleTestSend() {
  if (!formData.type) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  try {
    await ElMessageBox.confirm(
      t('channel.testSendConfirm'),
      t('channel.testSendTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return
  }
  testSending.value = true
  try {
    const res = await testSendPushChannel({
      type: formData.type,
      configJson,
    })
    if (res?.success) {
      ElMessage.success(
        t('channel.testSendSuccess', {
          code: res.statusCode ?? '-',
          ms: res.durationMs ?? 0,
        }),
      )
    } else {
      ElMessage.error(res?.error || t('channel.testSendFailed'))
    }
  } catch {
    // 全局拦截器已提示
  } finally {
    testSending.value = false
  }
}

// ---------- 操作 ----------
function handleAdd() {
  isEdit.value = false
  detailLoading.value = false
  // 清空上次残留的表单值，避免新增弹框缓存类型 / 名称 / 描述
  formData.id = ''
  formData.type = ''
  formData.name = ''
  formData.description = ''
  formData.status = 1
  outputFormat.value = 'native-json'
  resetMappingState()
  dialogVisible.value = true
  // 新增时等待用户选择类型后再加载连接配置
  loadDynamicForm('')
}

async function handleEdit(row) {
  isEdit.value = true
  detailLoading.value = true
  formData.id = row.id
  formData.type = row.type || row.name
  formData.name = row.name
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1
  // 先清掉上一通道的格式/映射，详情加载完成前不允许测试/预览/保存。
  outputFormat.value = 'native-json'
  resetMappingState()
  dialogVisible.value = true

  try {
    // 类型元数据与通道详情并行加载；元数据必须先就绪，否则 messageBased
    // 判断会把已有 format/mapping 当成非消息类配置删掉。
    const [detail] = await Promise.all([
      getChannelById(row.id).catch((err) => {
        console.error('获取通道详情失败', err)
        return null
      }),
      loadChannelTypes().catch(() => []),
    ])
    let prefillConfig = detail?.configJson ?? null
    // 兜底：列表行若自带 configJson 也直接使用
    if (prefillConfig == null && row.configJson != null) {
      prefillConfig = row.configJson
    }
    await applyOutputConfig(prefillConfig)
    // 编辑时按类型加载连接配置，并回填已有填写值
    loadDynamicForm(formData.type, prefillConfig)
  } finally {
    detailLoading.value = false
    nextTick(() => scheduleAutoPreview())
  }
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
  detailLoading.value = false
  outputFormat.value = 'native-json'
  resetMappingState()
  loadDynamicForm('')
}

onBeforeUnmount(() => {
  cancelAutoPreview()
  previewRequestId.value += 1
})

async function handleSubmit() {
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  submitting.value = true
  try {
    const payload = {
      type: formData.type,
      name: formData.name.trim(),
      description: formData.description.trim(),
      status: formData.status,
      configJson,
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
    // 全局拦截器已提示
  } finally {
    submitting.value = false
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  loadChannelTypes().catch(() => {})
  fetchList()
})
</script>

<style scoped>
/* 连接配置区：在对话框里用发丝线接续基本信息，不再另起一张卡片 */
.channel-section {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--line-soft);
}

.channel-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.channel-section-headline {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.channel-section-mark {
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

.channel-section-mark .el-icon {
  font-size: 15px;
}

.channel-section-name {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.channel-section-sub {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--ink-4);
}

.channel-format-select {
  flex: none;
  width: 200px;
}

.dynamic-form-wrapper {
  min-height: 60px;
  padding-bottom: 8px;
}

.channel-empty-config {
  margin: 0;
  padding: 10px 0;
  font-size: 12.5px;
  color: var(--ink-4);
}

/* 插件声明的连接参数收成一列，避免通栏输入框压着一个小步进器
   （与设备对象页的协议参数表单同一写法） */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 280px;
}

.channel-preview-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 12px;
}

.channel-preview-hint {
  font-size: 12px;
  color: var(--ink-4);
}

.mapping-preview {
  max-height: 240px;
  margin: 10px 0 0;
  padding: 12px 14px;
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  color: var(--ink-2);
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--surface-2, #f8fafc);
  border: 1px solid var(--line-soft);
  border-radius: 10px;
}
</style>
