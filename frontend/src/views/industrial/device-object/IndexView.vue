<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="device-page">
    <!-- 页头：沿用首页那套字号/留白层级，深浅两色由 token 控制，不另起一套视觉语言 -->
    <header class="page-head enter">
      <div>
        <p class="eyebrow">
          <span class="eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="page-title">{{ t('menu.deviceObject') }}</h1>
        <p class="page-sub">{{ t('deviceObject.subtitle') }}</p>
      </div>
      <div class="page-stat">
        <span class="page-stat-num">{{ displayTotal }}</span>
        <span class="page-stat-label">{{ t('deviceObject.totalUnit') }}</span>
      </div>
    </header>

    <!-- 工作区：筛选、表格、分页共用一块表面。
         原来「一条工具栏 + 一张表」是两张独立卡片，之间的 16px 空隙把同一件事切成了两半；
         合成一块后，工具条退化成表头的一部分，视线从筛选直接落到数据上。 -->
    <section ref="panelRef" class="panel enter" style="animation-delay: 60ms">
      <div class="toolbar">
        <div class="toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="filter-search"
            :placeholder="t('deviceObject.searchPlaceholder')"
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
        <div class="toolbar-actions">
          <!-- 批量删除放在这一组的最左边，且未勾选时置灰不移除：它是**破坏性**动作，
               和右边的下载/导入/新增不是一类；常驻在固定位置能让人形成固定预期，
               按勾选数变长的文案则让「删几台」在按下确认之前就可见。 -->
          <el-button
            type="danger"
            plain
            :icon="Delete"
            :disabled="selectedRows.length === 0"
            @click="handleBatchDelete"
          >
            {{ batchDeleteLabel }}
          </el-button>
          <span class="toolbar-sep" aria-hidden="true" />
          <el-button :icon="Download" @click="handleDownloadTemplate">{{ t('common.downloadTemplate') }}</el-button>
          <el-button :icon="Upload" :loading="importing" @click="handleImport">{{ t('common.import') }}</el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('deviceObject.add') }}</el-button>
          <input ref="fileInputRef" type="file" accept=".xlsx" class="hidden" @change="handleFileChange" />
        </div>
      </div>

      <!-- 数据表格 -->
      <el-table
        ref="tableRef"
        class="device-table"
        :data="tableData"
        v-loading="loading"
        row-key="id"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <!-- 项目里没用 reserve-selection：跨页保留勾选会让「已勾选 N 台」与实际
             能删到的集合不一致（翻页后看不见的行还在里面），批量删除宁可让人重勾。 -->
        <el-table-column type="selection" width="44" align="center" />
        <!-- 窄屏（含侧栏展开的 1280/1366）藏起序号与描述：九列挤进 1000px 会把
             创建时间推到固定操作列底下，反而让人以为数据丢了。序号只是行号，
             描述是次要信息；两者都在宽屏回归。 -->
        <el-table-column v-if="!compact" type="index" :label="t('common.index')" width="68" align="center" />

        <!-- ID 是 UUID，用等宽字体压低存在感；复制按钮常驻但低对比，悬停整行才完全显形 -->
        <el-table-column label="ID" width="150">
          <template #default="{ row }">
            <div class="id-cell">
              <el-tooltip :content="String(row.id)" placement="top">
                <span class="id-text">{{ row.id }}</span>
              </el-tooltip>
              <el-tooltip :content="t('common.copy')" placement="top">
                <button
                  type="button"
                  class="copy-btn"
                  :aria-label="t('common.copy')"
                  @click="copyId(row.id)"
                >
                  <el-icon><CopyDocument /></el-icon>
                </button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <!-- 名称是这一行的身份：给一个小图标底座，让「一台设备」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('common.name')" min-width="220">
          <template #default="{ row }">
            <div class="name-cell">
              <span class="name-mark" aria-hidden="true">
                <el-icon><DeviceChip /></el-icon>
              </span>
              <span class="name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceObject.protocol')" width="152">
          <template #default="{ row }">
            <!-- 协议名可以很长（Omron.FINS.HostLinkTCP 22 个字符），放不下时让省略号
                 落在 chip **内部**，再用 tooltip 给出全名；被单元格截在胶囊外面会像渲染故障 -->
            <el-tooltip
              v-if="row.protocolName"
              :content="row.protocolName"
              placement="top"
              :persistent="false"
            >
              <span class="proto-chip">{{ row.protocolName }}</span>
            </el-tooltip>
            <span v-else class="cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column
          v-if="!compact"
          prop="description"
          :label="t('common.description')"
          min-width="98"
          show-overflow-tooltip
          class-name="col-muted"
        />
        <el-table-column :label="t('common.status')" width="112" class-name="col-status">
          <template #default="{ row }">
            <span class="status-pill" :class="row.status === 1 ? 'is-on' : 'is-off'">
              <span class="status-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="t('common.createdAt')"
          width="160"
          class-name="col-time"
        />
        <el-table-column :label="t('common.action')" width="148" align="center" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <el-tooltip :content="t('deviceObject.addressManage')" placement="top">
                <el-button
                  class="row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Position"
                  :aria-label="t('deviceObject.addressManage')"
                  @click="handleViewAddresses(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('realtime.title')" placement="top">
                <el-button
                  class="row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Monitor"
                  :aria-label="t('realtime.title')"
                  @click="handleViewRealtime(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('common.edit')" placement="top">
                <el-button
                  class="row-btn"
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
                  class="row-btn"
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

        <!-- 空表：说清「是还没有数据，还是这次搜索没命中」，并给出下一步动作 -->
        <template #empty>
          <div class="empty">
            <span class="empty-mark" aria-hidden="true">
              <el-icon><DeviceChip /></el-icon>
            </span>
            <p class="empty-title">
              {{ queryForm.name
                ? t('deviceObject.emptySearch', { name: queryForm.name })
                : t('deviceObject.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name" class="empty-hint">{{ t('deviceObject.emptyHint') }}</p>
            <el-button v-if="queryForm.name" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('deviceObject.add') }}
            </el-button>
          </div>
        </template>
      </el-table>

      <!-- 分页：与表格同处一块表面，用一条发丝线收尾 -->
      <div class="pager">
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
    </section>

    <!-- ========== 新增 / 编辑 两步向导对话框 ========== -->
    <el-dialog
      v-model="dialogVisible"
      class="device-dialog"
      :title="isEdit ? t('deviceObject.editTitle') : t('deviceObject.addTitle')"
      width="620px"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <!-- 步骤指示器：细一号的圆点 + 一条会走完的连接线，避免两个大色块抢走表单的注意力 -->
      <div class="stepper">
        <div class="step" :class="{ 'is-active': currentStep === 1, 'is-done': stepPassed(1) }">
          <span class="step-dot">
            <el-icon v-if="stepPassed(1)"><Check /></el-icon>
            <span v-else>1</span>
          </span>
          <span class="step-label">{{ t('deviceObject.stepBasicInfo') }}</span>
        </div>
        <span class="step-line" :class="{ 'is-done': stepPassed(1) }" aria-hidden="true" />
        <div class="step" :class="{ 'is-active': currentStep === 2 }">
          <span class="step-dot">2</span>
          <span class="step-label">{{ t('deviceObject.stepProtocolConfig') }}</span>
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

          <!-- 协议参数与上面的选择器同属第二步，用一条发丝线接续而不是再套一层卡片。
               **不套 el-form-item、也不加左右内边距**：动态表单要与上面的 label 列严格对齐，
               多一层缩进就会让同一屏出现两条不同的左边缘（同 device-scan 页的说明）。 -->
          <div v-if="protocolSelected" class="param-section">
            <div class="param-head">
              <span class="param-title">{{ t('deviceObject.protocolParams') }}</span>
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
          </div>
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
import { ref, reactive, computed, nextTick, onMounted, onBeforeUnmount } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconAffiliate as Connection,
  IconArrowLeft as ArrowLeft,
  IconArrowRight as ArrowRight,
  IconCheck as Check,
  IconCopy as CopyDocument,
  IconCpu as DeviceChip,
  IconDeviceDesktop as Monitor,
  IconDownload as Download,
  IconMapPin as Position,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  getDeviceObjectList,
  getDeviceObjectById,
  addDeviceObject,
  updateDeviceObject,
  deleteDeviceObject,
  batchDeleteDeviceObject,
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

// ---------- 查看实时数据 ----------
function handleViewRealtime(row) {
  router.push({
    name: 'GatewayRealtime',
    params: { deviceObjectId: row.id },
    query: { deviceObjectName: row.name },
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

// 首屏还没拿到数时显示占位符而不是 0：「0 台设备」是一个会被信以为真的假事实
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

// ---------- 窄屏列裁剪 ----------
// 判据取**工作区的真实宽度**而不是窗口宽度：侧栏折叠后同一窗口会多出 176px，
// 按 window.innerWidth 判断会在折叠后仍少显示两列。
const panelRef = ref(null)
const compact = ref(false)
let panelObserver = null

// ---------- 勾选（批量删除） ----------
const tableRef = ref(null)
const selectedRows = ref([])

function handleSelectionChange(rows) {
  selectedRows.value = rows
}

// 按钮文案带上勾选数：按确认之前就能看到"要删几台"
const batchDeleteLabel = computed(() =>
  selectedRows.value.length
    ? t('deviceObject.batchDeleteCount', { n: selectedRows.value.length })
    : t('deviceObject.batchDelete'),
)

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
  // 提交/重置由对话框底部唯一负责：插件声明的 formJson 若自带按钮，
  // 会在参数区里多出一个和页脚重复的「提交」，两处提交语义还不同
  submitBtn: false,
  resetBtn: false,
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
          // 合并插件选项后仍强制关掉内置按钮（见 fcOption 的声明）
          submitBtn: false,
          resetBtn: false,
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

// ---------- 批量删除 ----------
async function handleBatchDelete() {
  const rows = selectedRows.value
  if (!rows.length) return

  try {
    await ElMessageBox.confirm(
      t('deviceObject.batchDeleteConfirm', { n: rows.length }),
      t('deviceObject.batchDeleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return // 用户取消
  }

  try {
    const res = await batchDeleteDeviceObject({ ids: rows.map((row) => row.id) })
    // deleted 由服务端按**实际删除行数**给出，可能小于勾选数：勾选后又被人删掉的
    // 设备不在删除集里。少删了要说出来，不能一律报"成功 N 台"。
    const deleted = res?.deleted ?? 0
    const addresses = res?.addresses ?? 0
    const missing = rows.length - deleted
    if (missing > 0) {
      ElMessage.warning(t('deviceObject.batchDeletePartial', { n: deleted, missing, addresses }))
    } else {
      ElMessage.success(t('deviceObject.batchDeleteSuccess', { n: deleted, addresses }))
    }
    tableRef.value?.clearSelection()
    fetchList()
  } catch {
    // 失败原因由响应拦截器统一提示
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
  // 九列的固定宽度合计 1152，低于这个值就切到紧凑列 —— 差几个像素时表格
  // 会横向滚动，固定操作列又会盖住创建时间，不如少两列让关键字段完整可见
  if (typeof ResizeObserver !== 'undefined' && panelRef.value) {
    panelObserver = new ResizeObserver(([entry]) => {
      compact.value = entry.contentRect.width < 1155
    })
    panelObserver.observe(panelRef.value)
  }
  fetchList()
})

onBeforeUnmount(() => {
  panelObserver?.disconnect()
})
</script>

<style scoped>
/* 页面级 token 与首页/登录页同一套：白面、雾蓝画布、slate 文字，强调色只有一种蓝 */
.device-page {
  --surface: #fff;
  --line: #e6ebf2;
  --line-soft: #eef2f7;
  --ink: #0f172a;
  --ink-2: #475569;
  --ink-3: #64748b;
  --ink-4: #94a3b8;
  --brand: #2563eb;
  --brand-soft: #eff5ff;
  --danger: #e11d48;

  max-width: 1400px;
  margin: 0 auto;
}

/* 页头与工作区错峰落位；动画挂在区级包裹层，不挂表格行，避免影响 hover 重绘 */
.enter {
  animation: pageEnter 0.55s cubic-bezier(0.16, 1, 0.3, 1) both;
}

@keyframes pageEnter {
  from {
    opacity: 0;
    transform: translateY(10px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

/* ---------- 页头 ---------- */
.page-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 20px;
}

.eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: #7c8ca3;
}

.eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.page-title {
  margin: 0;
  font-size: 22px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.page-sub {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

.page-stat {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding-bottom: 4px;
  white-space: nowrap;
}

.page-stat-num {
  font-size: 22px;
  font-weight: 650;
  line-height: 1;
  letter-spacing: -0.02em;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.page-stat-label {
  font-size: 12px;
  color: var(--ink-4);
}

/* ---------- 工作区 ---------- */
.panel {
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 18px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px 16px;
  padding: 16px 20px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border-bottom: 1px solid var(--line-soft);
}

.toolbar-filters,
.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.filter-search {
  width: 272px;
}

/* 输入框与按钮统一 34px 高、9px 圆角：原来两者都是各自的默认高度，并排时总有 1px 的错位 */
.toolbar :deep(.el-input__wrapper) {
  height: 34px;
  border-radius: 9px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--line) inset;
  transition: box-shadow 0.18s ease;
}

.toolbar :deep(.el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px #cbd8e6 inset;
}

.toolbar :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 1px var(--brand) inset, 0 0 0 3px rgba(37, 99, 235, 0.1);
}

.toolbar :deep(.el-input__prefix) {
  color: var(--ink-4);
}

.toolbar :deep(.el-button) {
  height: 34px;
  padding: 0 14px;
  border-radius: 9px;
  font-weight: 500;
}

/* 破坏性动作与其余按钮之间的短分隔：不画横线，只用一根 20px 的竖线分组 */
.toolbar-sep {
  width: 1px;
  height: 20px;
  margin: 0 2px;
  background: var(--line);
}

/* ---------- 表格 ---------- */
.device-table {
  --el-table-border-color: transparent;
  /* 单元格必须是**实底**：固定列靠自己的背景遮住横向滚动时从下面穿过的内容，
     设成 transparent 会让创建时间等文字透到操作列底下（窄屏时尤其明显） */
  --el-table-bg-color: #fff;
  --el-table-tr-bg-color: #fff;
  --el-table-header-bg-color: #f8fafc;
  --el-table-row-hover-bg-color: #f8fbff;
  font-size: 13px;
  color: var(--ink-2);
}

.device-table :deep(td.el-table__cell) {
  background-color: #fff;
}

.device-table :deep(.el-table__body tr:hover > td.el-table__cell) {
  background-color: #f8fbff;
}

/* Element 默认在表体底部画一条贯穿线，与卡片圆角相撞时会在两角露头 */
.device-table :deep(.el-table__inner-wrapper::before) {
  display: none;
}

.device-table :deep(th.el-table__cell) {
  height: 42px;
  padding: 0;
  background: #f8fafc;
  color: var(--ink-3);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
  border-bottom: 1px solid var(--line);
}

/* 表头宁可截断也不折行：两行表头会把行高顶开，整张表的基线随之跳动 */
.device-table :deep(th .cell) {
  white-space: nowrap;
}

.device-table :deep(tr.el-table__row td.el-table__cell) {
  padding: 12px 0;
  border-bottom: 1px solid var(--line-soft);
}

.device-table :deep(.el-table__body tr:last-child td.el-table__cell) {
  border-bottom: none;
}

.device-table :deep(.cell) {
  padding: 0 16px;
}

.device-table :deep(.el-checkbox__inner) {
  border-radius: 4px;
}

.device-table :deep(.col-time .cell) {
  font-size: 12.5px;
  color: var(--ink-3);
  font-variant-numeric: tabular-nums;
}

/* 英文的 Enabled / Disabled 比中文长，收窄内边距给状态胶囊留出整词宽度 */
.device-table :deep(.col-status .cell) {
  padding: 0 12px;
}

.device-table :deep(.col-muted .cell) {
  color: var(--ink-3);
}

.cell-muted {
  color: var(--ink-4);
}

/* ID：等宽字体压低存在感，复制按钮常驻但低对比，悬停整行才完全显形 */
.id-cell {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.id-text {
  overflow: hidden;
  max-width: 96px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  color: var(--ink-4);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.copy-btn {
  display: grid;
  flex: none;
  place-items: center;
  width: 22px;
  height: 22px;
  padding: 0;
  color: var(--ink-4);
  background: transparent;
  border: 0;
  border-radius: 6px;
  cursor: pointer;
  opacity: 0.45;
  transition: opacity 0.15s ease, color 0.15s ease, background-color 0.15s ease;
}

.copy-btn .el-icon {
  font-size: 14px;
}

.device-table :deep(.el-table__row:hover) .copy-btn {
  opacity: 1;
}

.copy-btn:hover {
  color: var(--brand);
  background: var(--brand-soft);
}

.copy-btn:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.5);
  outline-offset: 1px;
  opacity: 1;
}

/* 名称：一行一个身份，小图标底座让它在滚动中可被一眼拾取 */
.name-cell {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.name-mark {
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

.name-mark .el-icon {
  font-size: 15px;
}

.name-text {
  overflow: hidden;
  font-weight: 600;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 协议名是技术标识，用中性 chip 而不是彩色标签，避免和状态色抢语义 */
.proto-chip {
  display: inline-block;
  box-sizing: border-box;
  max-width: 100%;
  padding: 2px 8px;
  overflow: hidden;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
  color: var(--ink-2);
  text-overflow: ellipsis;
  vertical-align: middle;
  white-space: nowrap;
  background: #f1f5f9;
  border: 1px solid #e8edf4;
  border-radius: 6px;
}

/* 状态：软底色 + 圆点，比实心 el-tag 安静，红绿只留给真正需要区分的两态 */
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 9px;
  font-size: 12px;
  font-weight: 500;
  line-height: 16px;
  white-space: nowrap;
  border-radius: 999px;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.status-pill.is-on {
  color: #047857;
  background: #ecfdf5;
}

.status-pill.is-off {
  color: #be123c;
  background: #fff1f2;
}

/* 行内操作：默认中性灰，悬停才染蓝/红，四个图标不再每行都喊一次 */
.row-actions {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 2px;
}

.device-table :deep(.row-btn.el-button) {
  width: 28px;
  height: 28px;
  margin: 0;
  padding: 0;
  color: var(--ink-4);
  border-radius: 8px;
}

.device-table :deep(.row-btn.el-button .el-icon) {
  font-size: 16px;
}

.device-table :deep(.row-btn.el-button:hover) {
  color: var(--brand);
  background: var(--brand-soft);
}

.device-table :deep(.row-btn.el-button.el-button--danger:hover) {
  color: var(--danger);
  background: #fff1f2;
}

/* ---------- 空表 ---------- */
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 44px 16px 48px;
}

.empty-mark {
  display: grid;
  place-items: center;
  width: 52px;
  height: 52px;
  margin-bottom: 14px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 14px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.08);
}

.empty-mark .el-icon {
  font-size: 26px;
}

.empty-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--ink-2);
}

.empty-hint {
  margin: 6px 0 0;
  font-size: 12.5px;
  color: var(--ink-4);
}

.empty .el-button {
  margin-top: 16px;
}

/* ---------- 分页 ---------- */
.pager {
  display: flex;
  justify-content: flex-end;
  padding: 14px 20px;
  border-top: 1px solid var(--line-soft);
}

.pager :deep(.el-pagination) {
  --el-pagination-button-bg-color: #f4f6fa;
  --el-pagination-hover-color: var(--brand);
}

.pager :deep(.el-pager li),
.pager :deep(.btn-prev),
.pager :deep(.btn-next) {
  border-radius: 8px;
  font-weight: 500;
}

/* ---------- 对话框 ----------
   el-dialog 会被 teleport 到 body，scoped 的 :deep 够不到，用唯一类名做全局作用域 */
:global(.device-dialog) {
  border-radius: 18px;
  box-shadow: 0 24px 60px -24px rgba(15, 23, 42, 0.35);
}

:global(.device-dialog .el-dialog__header) {
  margin: 0;
  padding: 20px 24px 16px;
  border-bottom: 1px solid #eef2f7;
}

:global(.device-dialog .el-dialog__title) {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: #0f172a;
}

:global(.device-dialog .el-dialog__headerbtn) {
  top: 12px;
  right: 12px;
  width: 30px;
  height: 30px;
  border-radius: 8px;
}

:global(.device-dialog .el-dialog__headerbtn:hover) {
  background: #f4f6fa;
}

:global(.device-dialog .el-dialog__body) {
  padding: 20px 24px 8px;
}

:global(.device-dialog .el-dialog__footer) {
  padding: 14px 24px 18px;
  border-top: 1px solid #eef2f7;
}

:global(.device-dialog .el-form-item__label) {
  font-size: 13px;
  color: #475569;
}

/* 步骤条：圆点细一号，完成态用品牌蓝而不是绿色 —— 「走过了」是流程信息，不是健康信号 */
.stepper {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  margin: 2px 0 24px;
}

.step {
  display: flex;
  align-items: center;
  gap: 9px;
}

.step-dot {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-4);
  background: #f1f5f9;
  border-radius: 50%;
  box-shadow: inset 0 0 0 1px var(--line);
  transition: color 0.2s ease, background-color 0.2s ease, box-shadow 0.2s ease;
}

.step-dot .el-icon {
  font-size: 14px;
}

.step-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--ink-4);
  white-space: nowrap;
  transition: color 0.2s ease;
}

.step.is-done .step-dot {
  color: var(--brand);
  background: var(--brand-soft);
  box-shadow: inset 0 0 0 1px #c7d9f7;
}

.step.is-done .step-label {
  color: var(--ink-2);
}

.step.is-active .step-dot {
  color: #fff;
  background: linear-gradient(135deg, #3b82f6, var(--brand));
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.step.is-active .step-label {
  font-weight: 600;
  color: var(--ink);
}

.step-line {
  width: 56px;
  height: 1px;
  background: var(--line);
  transition: background-color 0.2s ease;
}

.step-line.is-done {
  background: linear-gradient(90deg, #bfdbfe, var(--brand));
}

/* 协议参数区：一条发丝线接续，不左右缩进，保住与上方 label 列的严格对齐 */
.param-section {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--line-soft);
}

.param-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.param-title {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.06em;
  color: var(--ink-3);
}

/* 插件声明的参数控件收成一列，避免通栏输入框压着一个小步进器（同 device-scan 页） */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 280px;
}

/* ---------- 窄屏 ---------- */
@media (max-width: 900px) {
  .page-head {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  .page-stat {
    padding-bottom: 0;
  }
}

@media (max-width: 640px) {
  .toolbar-filters,
  .toolbar-actions {
    width: 100%;
  }

  .filter-search {
    width: 100%;
  }

  .toolbar-actions {
    justify-content: flex-end;
  }
}

@media (prefers-reduced-motion: reduce) {
  .enter {
    animation: none;
  }

  .device-page *,
  .device-page *::before,
  .device-page *::after {
    transition: none !important;
  }
}
</style>
