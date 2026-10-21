<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-2">
      {{ t('menu.deviceScan') }}
    </h1>
    <p class="text-sm text-gray-500 mb-6">{{ t('deviceScan.intro') }}</p>

    <!-- 功能未启用 / 没有可用协议或网段：直接把原因说清楚，而不是给一个点了没反应的按钮 -->
    <el-alert
      v-if="caps && !caps.enabled"
      type="warning"
      :closable="false"
      class="mb-4"
      :title="t('deviceScan.disabled')"
    />
    <template v-else-if="caps">
      <el-alert
        v-if="caps.protocols.length === 0"
        type="info"
        :closable="false"
        class="mb-4"
        :title="t('deviceScan.noProtocol')"
      />
      <el-alert
        v-if="caps.subnets.length === 0"
        type="info"
        :closable="false"
        class="mb-4"
        :title="t('deviceScan.noSubnet')"
      />
    </template>

    <!-- ========== 扫描配置 ========== -->
    <el-card shadow="never" class="mb-4">
      <el-form :label-width="formLabelWidth" label-position="right">
        <el-form-item :label="t('deviceScan.protocol')" required>
          <el-select
            v-model="form.protocol"
            :placeholder="t('deviceScan.protocolPlaceholder')"
            :disabled="scanning"
            style="width: 320px"
            @change="handleProtocolChange"
          >
            <el-option
              v-for="p in protocols"
              :key="p.name"
              :label="p.title || p.name"
              :value="p.name"
              :disabled="!p.protocolId"
            />
          </el-select>
          <!-- 协议行缺失时后端给不出 protocolId，建出来的设备会没有协议 -->
          <span
            v-if="currentProtocol && !currentProtocol.protocolId"
            class="ml-3 text-xs text-orange-500"
          >
            {{ t('deviceScan.protocolRowMissing') }}
          </span>
          <!-- 协议说明是**协议级**的（"这个协议的扫描是怎么做的"），不是扫描参数
               的一部分，所以挂在这一行、与网段/端口的提示同款；放在参数块里会
               变成一堵横跨整张卡片的通栏文字，粘在最后一个字段下面。 -->
          <div
            v-if="currentProtocol?.description"
            class="w-full mt-1 text-xs text-gray-400 leading-5"
          >
            {{ currentProtocol.description }}
          </div>
        </el-form-item>

        <el-form-item :label="t('deviceScan.subnet')">
          <el-checkbox-group v-model="form.subnets" :disabled="scanning">
            <el-checkbox v-for="s in subnets" :key="s.cidr" :value="s.cidr">
              {{ s.cidr }}
              <span class="text-gray-400 text-xs ml-1">
                {{ s.iface }} · {{ t('deviceScan.hostCount', { n: s.hostCount }) }}
              </span>
            </el-checkbox>
          </el-checkbox-group>
          <!-- 占满一行：el-form-item__content 是 flex 容器，不给 w-full 这句提示会并排挤在网段文字右边 -->
          <div class="w-full text-xs text-gray-400 leading-5 mt-1">
            {{ t('deviceScan.subnetHint') }}
          </div>
        </el-form-item>

        <el-form-item :label="t('deviceScan.port')">
          <el-input-number
            v-model="form.port"
            :min="1"
            :max="65535"
            :disabled="scanning"
            controls-position="right"
            style="width: 160px"
          />
          <span class="ml-3 text-xs text-gray-400">
            {{ t('deviceScan.portHint', { n: currentProtocol?.defaultPort ?? '—' }) }}
          </span>
        </el-form-item>

        <!-- 扫描参数：由插件声明的 scanFormJson 动态渲染（站号、超时…），就接在
             端口下面，**不加分组标题** —— 插件的字段名自己就是标签，多一行"扫描
             参数"只会让这段看起来是另一张表单。

             **也不套 el-form-item**：动态表单自带一条 label 列（宽度由插件声明，
             见 fcOption），套进 el-form-item 就是在已经缩进 label-width 的内容区里
             再缩进一次 —— 子 label 落在"别的行的控件"处、子控件再右移一整列，
             同一屏出现三条左边缘。这里直接放在 el-form 下、与上面的协议/网段/端口
             共用同一条 label 列，宽度靠 formLabelWidth 对齐。
             设备页的协议参数表单（device-object/IndexView.vue）是同一个写法。 -->
        <div v-if="fcRule.length" class="dynamic-form-wrapper">
          <form-create
            :key="form.protocol || 'empty'"
            ref="fcRef"
            :rule="fcRule"
            :option="fcOption"
            @mounted="handleFcReady"
          />
        </div>
      </el-form>

      <div class="flex items-center justify-end gap-3">
        <span v-if="job && scanning" class="text-sm text-gray-500">
          {{ t('deviceScan.estimated', { s: Math.ceil((job.estimateMaxMs || 0) / 1000) }) }}
        </span>
        <el-button v-if="scanning" @click="handleCancel" :loading="canceling">
          {{ t('deviceScan.cancel') }}
        </el-button>
        <el-button
          type="primary"
          :icon="Search"
          :loading="starting"
          :disabled="!canStart"
          @click="handleStart"
        >
          {{ scanning ? t('deviceScan.rescan') : t('deviceScan.start') }}
        </el-button>
      </div>
    </el-card>

    <!-- ========== 进度 ========== -->
    <el-card v-if="job" shadow="never" class="mb-4">
      <div class="flex items-center justify-between mb-2">
        <div class="flex items-center gap-3">
          <el-tag :type="stateTagType" size="small">{{ stateText }}</el-tag>
          <span class="text-sm text-gray-600">
            {{ t('deviceScan.scanned', { done: job.scanned, total: job.total }) }}
          </span>
          <span class="text-sm text-gray-600">
            {{ t('deviceScan.foundCount', { n: job.found }) }}
          </span>
          <span class="text-sm text-gray-400">
            {{ t('deviceScan.elapsed', { s: (job.elapsedMs / 1000).toFixed(1) }) }}
          </span>
        </div>
        <span v-if="job.reason" class="text-sm text-gray-500">{{ job.reason }}</span>
      </div>

      <el-progress
        :percentage="progressPercent"
        :status="job.state === 'running' ? undefined : progressStatus"
        :stroke-width="12"
      />

      <!-- 截断必须说出来：否则用户会从"没扫到设备"得出"这个网段没有设备"的错误结论 -->
      <el-alert
        v-if="job.truncated"
        type="warning"
        :closable="false"
        class="mt-3"
        :title="t('deviceScan.truncated', { requested: job.requestedTargets, planned: job.plannedTargets })"
      />

      <!-- 探测本身失败的目标：与"没扫到"是两回事，需要单独列出来。
           上限是**目标数**（默认 1024，可配到 65536），所以这张表也要分页 ——
           它虽然只有两列，但一次全挂上去就是几千个单元格。 -->
      <el-collapse v-if="scanErrors.length" class="mt-3">
        <el-collapse-item
          :title="t('deviceScan.errorCount', { n: scanErrors.length })"
          name="errors"
        >
          <div class="text-xs text-gray-500 mb-2">{{ t('deviceScan.errorHint') }}</div>
          <el-table :data="pagedErrors" size="small" border max-height="240">
            <el-table-column prop="host" :label="t('deviceScan.column.host')" width="160" />
            <el-table-column prop="reason" :label="t('deviceScan.column.reason')" min-width="260" show-overflow-tooltip />
          </el-table>
          <div v-if="errorTotal > errorPageSize" class="flex justify-end mt-2">
            <el-pagination
              v-model:current-page="errorPage"
              v-model:page-size="errorPageSize"
              :page-sizes="[50, 100, 200]"
              :total="errorTotal"
              layout="total, sizes, prev, pager, next"
              size="small"
              background
            />
          </div>
        </el-collapse-item>
      </el-collapse>
    </el-card>

    <!-- ========== 扫描结果 ========== -->
    <el-card v-if="job" shadow="never">
      <div class="flex items-center justify-between mb-4">
        <span class="text-base font-medium text-gray-700">{{ t('deviceScan.resultTitle') }}</span>
        <el-button
          type="primary"
          :disabled="selectedRows.length === 0"
          @click="handleOpenAddDialog"
        >
          {{ t('deviceScan.addDevices', { n: selectedRows.length }) }}
        </el-button>
      </div>

      <!-- **只把当前页交给 el-table**：一次扫出几百上千台时整表挂载是几万个组件
           实例，主线程会被 patch 占满几十秒、浏览器表现为页面假死；行数还会把
           页面拉成几千行高。分页组件与点扫弹窗共用一个组合式函数
           （composables/usePagedRows.js），默认 20 行。 -->
      <el-table
        ref="tableRef"
        :data="pagedDevices"
        stripe
        border
        style="width: 100%"
        row-key="index"
        @selection-change="handleSelectionChange"
      >
        <!-- reserve-selection：轮询在有新设备时会换掉 data 实例，没有它 el-table
             会直接 clearSelection()（还会 emit selection-change []），用户勾到一半
             的选中态就被抹掉；勾选态按 row-key 重算，翻页回来还是勾着的。
             row-key 用 index —— 它是结果在快照数组里的下标，只增不变。 -->
        <el-table-column type="selection" width="48" reserve-selection :selectable="() => true" />
        <el-table-column prop="host" :label="t('deviceScan.column.host')" width="150" />
        <el-table-column prop="port" :label="t('deviceScan.column.port')" width="90" align="center" />
        <el-table-column prop="unitId" :label="t('deviceScan.column.unitId')" width="90" align="center">
          <template #default="{ row }">{{ row.unitId || '—' }}</template>
        </el-table-column>
        <el-table-column prop="name" :label="t('common.name')" min-width="180" show-overflow-tooltip />
        <el-table-column prop="vendor" :label="t('deviceScan.column.vendor')" min-width="130" show-overflow-tooltip />
        <el-table-column prop="model" :label="t('deviceScan.column.model')" min-width="130" show-overflow-tooltip />
        <el-table-column prop="version" :label="t('deviceScan.column.version')" width="110" />
        <el-table-column :label="t('deviceScan.column.extra')" min-width="200">
          <template #default="{ row }">
            <el-tag
              v-for="a in row.extra"
              :key="a.name"
              size="small"
              type="info"
              class="mr-1 mb-1"
            >
              {{ a.name }}：{{ a.value }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceScan.column.protocolJson')" width="120" align="center">
          <template #default="{ row }">
            <!-- persistent=false 与点扫弹窗里那三个行内下拉同一条理由：默认的
                 true 会让每一行挂载时就把 popper 子树建出来（还要 teleport 到
                 body），而这里每行都会有一个 tooltip。非持久不影响行为 ——
                 内容每次展开时重建，而它本来就只读 row.protocolJson。 -->
            <el-tooltip v-if="row.protocolJson" placement="left" :persistent="false">
              <template #content>
                <div class="font-mono text-xs break-all max-w-[320px]">{{ row.protocolJson }}</div>
              </template>
              <el-button type="primary" link size="small" :icon="Document" @click="copyJson(row.protocolJson)">
                {{ t('common.view') }}
              </el-button>
            </el-tooltip>
            <span v-else class="text-gray-400">—</span>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="deviceTotal > 0" class="flex justify-end mt-3">
        <el-pagination
          v-model:current-page="devicePage"
          v-model:page-size="devicePageSize"
          :page-sizes="[20, 50, 100]"
          :total="deviceTotal"
          layout="total, sizes, prev, pager, next, jumper"
          background
        />
      </div>

      <el-empty v-if="devices.length === 0" :description="scanning ? t('deviceScan.scanningHint') : t('deviceScan.noResult')" />
    </el-card>

    <!-- ========== 添加为设备 ========== -->
    <el-dialog
      v-model="addDialogVisible"
      :title="t('deviceScan.addDialogTitle')"
      width="760px"
      :close-on-click-modal="false"
    >
      <div class="text-sm text-gray-500 mb-3">{{ t('deviceScan.addDialogHint') }}</div>
      <!-- 这里也必须分页：勾了 1000 行就是 1000 × 2 个 el-input 一起挂上来，
           输入框的挂载成本远高于普通单元格。分页不丢编辑 —— 输入框绑的是
           addRows 里的真实行对象，翻页只是换显示哪一段。 -->
      <el-table :data="pagedAddRows" border size="small" max-height="420">
        <el-table-column :label="t('deviceScan.column.host')" width="140">
          <template #default="{ row }">{{ row.host }}<span v-if="row.unitId">#{{ row.unitId }}</span></template>
        </el-table-column>
        <el-table-column :label="t('common.name')" min-width="220">
          <template #default="{ row }">
            <el-input v-model="row.name" :placeholder="row.suggestedName" />
          </template>
        </el-table-column>
        <el-table-column :label="t('common.description')" min-width="200">
          <template #default="{ row }">
            <el-input v-model="row.description" />
          </template>
        </el-table-column>
      </el-table>
      <div v-if="addTotal > 0" class="flex justify-end mt-3">
        <el-pagination
          v-model:current-page="addPage"
          v-model:page-size="addPageSize"
          :page-sizes="[20, 50, 100]"
          :total="addTotal"
          layout="total, sizes, prev, pager, next"
          background
        />
      </div>
      <template #footer>
        <el-button @click="addDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="creating" @click="handleCreateDevices">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- ========== 地址重复确认 ==========
         两段式提交的第二段：后端默认**不建**"扫描地址与库内某台设备相同"的那些行，
         只把它们作为明细返回，由这里问一句。同一地址上多台设备是合法的，所以既不能
         默默建、也不能拦死 —— 理由见 handleCreateDevices 的注释。 -->
    <el-dialog
      v-model="dupDialogVisible"
      :title="t('deviceScan.duplicateTitle')"
      width="620px"
      :close-on-click-modal="false"
    >
      <div class="text-sm mb-3">
        {{ t('deviceScan.duplicateConfirm', { n: pendingDuplicateErrors.length }) }}
      </div>
      <el-table :data="pendingDuplicateErrors" border size="small" max-height="280">
        <el-table-column prop="name" :label="t('common.name')" width="150" show-overflow-tooltip />
        <el-table-column prop="reason" :label="t('deviceScan.column.reason')" min-width="260" show-overflow-tooltip />
      </el-table>
      <div class="text-xs text-gray-500 mt-3">{{ t('deviceScan.duplicateHint') }}</div>
      <template #footer>
        <el-button @click="handleSkipDuplicate">{{ t('deviceScan.duplicateSkip') }}</el-button>
        <el-button type="primary" :loading="creating" @click="handleConfirmDuplicate">
          {{ t('deviceScan.duplicateOk') }}
        </el-button>
      </template>
    </el-dialog>

    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { IconFileText as Document, IconSearch as Search } from '@tabler/icons-vue'

import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { usePagedRows } from '@/composables/usePagedRows'
import {
  cancelScan,
  createDevicesFromScan,
  getScanCapabilities,
  getScanStatus,
  startScan,
} from '@/api/modules/deviceScan'

const { t } = useI18n()

// 轮询间隔与上限。上限是防御性的：任务若因某种原因停在 running（后端 bug），
// 无限轮询会一直占着这个页面。
const POLL_INTERVAL_MS = 800
const POLL_MAX_MS = 30 * 60 * 1000

// ---------- 状态 ----------
const caps = ref(null)
const job = ref(null)
const starting = ref(false)
const canceling = ref(false)
const creating = ref(false)
const jobExpired = ref(false)

const form = reactive({
  protocol: '',
  subnets: [],
  port: null,
})

const fcRef = ref(null)
const fcApi = ref(null)
const fcRule = ref([])
const fcOption = ref({
  form: { labelWidth: '110px', labelPosition: 'right' },
})

const tableRef = ref(null)
const selectedRows = ref([])

const addDialogVisible = ref(false)
const addRows = ref([])

const dupDialogVisible = ref(false)
// pendingDuplicateErrors 第一轮里被判"地址重复"的明细；pendingResult 是那一轮的
// 完整结果 —— 用户选择"不建这些"时照原样展示它，重复行就留在失败明细里。
const pendingDuplicateErrors = ref([])
const pendingResult = ref(null)

const importResultVisible = ref(false)
const importResult = ref({ total: 0, success: 0, failed: 0, errors: [] })

let pollTimer = null
let pollStartedAt = 0

// ---------- 派生 ----------
const protocols = computed(() => caps.value?.protocols ?? [])
const subnets = computed(() => caps.value?.subnets ?? [])
const devices = computed(() => job.value?.devices ?? [])
const scanErrors = computed(() => job.value?.errors ?? [])
const currentProtocol = computed(
  () => protocols.value.find((p) => p.name === form.protocol) ?? null,
)
// 页面的 label 列宽必须与插件为动态表单声明的 labelWidth 一致：动态表单不套
// el-form-item、与协议/网段/端口 共用同一条 label 列（见模板里那段注释），
// 取两个不同的宽度就会让扫描参数的控件错开一列。插件没声明时用 el-form 常规值。
const formLabelWidth = computed(() => fcOption.value?.form?.labelWidth || '110px')
const scanning = computed(() => job.value?.state === 'running')
const canStart = computed(
  () => !!caps.value?.enabled
    && !!form.protocol
    && form.subnets.length > 0
    && !starting.value,
)
const progressPercent = computed(() => {
  if (!job.value?.total) return 0
  return Math.min(100, Math.round((job.value.scanned / job.value.total) * 100))
})
const progressStatus = computed(() => {
  if (job.value?.state === 'failed') return 'exception'
  if (job.value?.state === 'canceled') return 'warning'
  return 'success'
})
const stateText = computed(() => {
  switch (job.value?.state) {
    case 'running': return t('deviceScan.state.running')
    case 'done': return t('deviceScan.state.done')
    case 'canceled': return t('deviceScan.state.canceled')
    default: return t('deviceScan.state.failed')
  }
})
const stateTagType = computed(() => {
  switch (job.value?.state) {
    case 'running': return 'primary'
    case 'done': return 'success'
    case 'canceled': return 'warning'
    default: return 'danger'
  }
})

// ---------- 分页 ----------
// 三张表都要分页（结果 / 探测失败明细 / 添加弹窗），理由见模板里的注释与
// composables/usePagedRows.js。每页条数按各自的挂载成本定：结果表一行有标签与
// 连接配置按钮，弹窗一行两个输入框，失败明细只有两列纯文本。
const {
  currentPage: devicePage,
  pageSize: devicePageSize,
  pagedRows: pagedDevices,
  totalRows: deviceTotal,
} = usePagedRows(devices)
const {
  currentPage: errorPage,
  pageSize: errorPageSize,
  pagedRows: pagedErrors,
  totalRows: errorTotal,
} = usePagedRows(scanErrors, { pageSize: 50 })
const {
  currentPage: addPage,
  pageSize: addPageSize,
  pagedRows: pagedAddRows,
  totalRows: addTotal,
} = usePagedRows(addRows)

// ---------- 初始化 ----------
onMounted(fetchCapabilities)
onBeforeUnmount(stopPolling)

async function fetchCapabilities() {
  try {
    caps.value = await getScanCapabilities()
  } catch {
    // 拦截器已提示
    return
  }
  if (!caps.value?.enabled) return

  // 默认：选第一个可用的协议、全选本机网段 —— 进页面即可直接点"开始扫描"。
  const first = protocols.value.find((p) => p.protocolId) ?? protocols.value[0]
  if (first) {
    form.protocol = first.name
    applyProtocolDefaults(first)
  }
  form.subnets = subnets.value.map((s) => s.cidr)
  // 表单必须在这里也渲染一次。上面的赋值不会触发 el-select 的 @change
  //（那是用户交互事件），而 renderScanForm 只挂在 handleProtocolChange 上 ——
  // 少了这一句，fcRule 停在空数组、`v-if="fcRule.length"` 为假、form-create
  // 从不挂载，症状是**进页面看不到"站号/拨号超时/单帧超时"三个选项**，
  // 必须把协议切走再切回来才出现（Modbus 的默认值与表单声明值恰好一致，
  // 所以扫描还能跑，但用户想扫站号 1,2,5 时找不到入口）。
  await renderScanForm()
}

/** 协议切换：重置端口为插件声明的默认值，并渲染该协议的扫描参数表单。 */
async function handleProtocolChange() {
  stopPolling()
  job.value = null
  jobExpired.value = false
  selectedRows.value = []
  applyProtocolDefaults(currentProtocol.value)
  await renderScanForm()
}

function applyProtocolDefaults(p) {
  form.port = p?.defaultPort || null
}

/** 渲染插件声明的扫描参数表单。与设备页的 form-create 用法同构。 */
async function renderScanForm() {
  fcRule.value = []
  fcApi.value = null

  const raw = currentProtocol.value?.scanFormJson
  if (!raw) return

  let parsed = raw
  try {
    if (typeof parsed === 'string') parsed = JSON.parse(parsed)
    // 后端可能把 rule/options 二次序列化成字符串
    if (parsed?.rule && typeof parsed.rule === 'string') parsed.rule = JSON.parse(parsed.rule)
    if (parsed?.options && typeof parsed.options === 'string') parsed.options = JSON.parse(parsed.options)
  } catch {
    ElMessage.warning(t('deviceScan.scanFormInvalid'))
    return
  }

  if (Array.isArray(parsed?.rule)) {
    fcRule.value = parsed.rule
  } else if (Array.isArray(parsed)) {
    fcRule.value = parsed
  } else {
    ElMessage.warning(t('deviceScan.scanFormInvalid'))
    return
  }

  const opts = parsed?.options || parsed?.option
  if (opts) {
    fcOption.value = {
      ...fcOption.value,
      ...opts,
      form: { ...fcOption.value.form, ...(opts.form || {}) },
    }
  }
}

function handleFcReady($f) {
  fcApi.value = $f
}

/** 收集扫描参数（按当前 rule 的 field 过滤，剔除脏数据）。 */
function collectOptions() {
  if (!fcApi.value) return undefined
  const data = fcApi.value.formData() || {}
  const fields = new Set(fcRule.value.map((item) => item.field).filter(Boolean))
  if (fields.size === 0) return data
  const cleaned = {}
  Object.keys(data).forEach((k) => {
    if (fields.has(k)) cleaned[k] = data[k]
  })
  return cleaned
}

// ---------- 扫描控制 ----------
async function handleStart() {
  if (!canStart.value) return
  starting.value = true
  jobExpired.value = false
  selectedRows.value = []
  // 分页回第一页、清掉表里的勾选标记：新一轮是另一份清单，而下面那份 devices
  // 若恰好与上一轮**长度相同**，applySnapshot 会保留数组实例、el-table 也不换
  // data，勾选标记就会跨轮残留（勾着一批已经不属于本次结果的序号）。
  devicePage.value = 1
  tableRef.value?.clearSelection()
  try {
    const snap = await startScan({
      protocol: form.protocol,
      options: collectOptions(),
      subnets: form.subnets,
      port: form.port || 0,
    })
    job.value = snap
    startPolling(snap.jobId)
  } catch {
    // 拦截器已提示（无网段、协议不支持、已有任务等都由后端给出可执行的原因）
  } finally {
    starting.value = false
  }
}

async function handleCancel() {
  if (!job.value) return
  canceling.value = true
  try {
    await cancelScan(job.value.jobId)
    // 不在这里改状态：以服务端返回的快照为准（它才知道有多少在飞探测已收尾）。
    await pollOnce()
  } catch {
    // 拦截器已提示
  } finally {
    canceling.value = false
  }
}

function startPolling(jobId) {
  stopPolling()
  pollStartedAt = Date.now()
  pollTimer = setInterval(async () => {
    if (Date.now() - pollStartedAt > POLL_MAX_MS) {
      stopPolling()
      ElMessage.warning(t('deviceScan.pollTimeout'))
      return
    }
    await pollOnce(jobId)
  }, POLL_INTERVAL_MS)
  // 立即拉一次，避免等满一个间隔才看到第一屏进度。
  pollOnce(jobId)
}

function stopPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function pollOnce(jobId = job.value?.jobId) {
  if (!jobId) return
  try {
    // 先拿**轻量快照**（withDevices=false）：绝大多数拍子并没有新设备，而一份
    // 几百台的清单是每拍几百 KB，后端还要为它锁下拷一次切片。发现数没变就用它
    // —— 那份快照里的 devices 是 null，applySnapshot 会保留手上那份。
    //
    // 这个请求是 silent 的（轮询失败不该每 800ms 弹一次提示），拦截器因此返回的
    // 是**响应信封**而不是解包后的 data —— 这里自己解。
    const light = await getScanStatus(jobId, { withDevices: false })
    if (!light || light.code !== '0' || !light.data) {
      stopPolling()
      jobExpired.value = true
      ElMessage.warning(t('deviceScan.jobExpired'))
      return
    }

    if (light.data.found === devices.value.length) {
      applySnapshot(light.data)
    } else {
      // 计数对不上就说明漏了结果：新设备落进来了，或上一次补请求失败了。
      // 补一次带清单的请求；它若也失败，下一拍还会再来一次（轻量快照里的计数
      // 不会因为失败而回退），因此这里不需要重试机制。
      const full = await getScanStatus(jobId, { withDevices: true })
      applySnapshot(full?.code === '0' && full.data ? full.data : light.data)
    }

    if (job.value?.state !== 'running') {
      stopPolling()
    }
  } catch {
    // 网络/TCP 层错误会 reject（silent 只吞业务错误的提示，见 api/index.js）。
    // 跳过这一拍即可：轮询不能因为一次抖动就停，但也不能不接 —— 不 catch 就是
    // setInterval 里的 unhandled rejection。
  }
}

/**
 * 把一份快照应用上去。两份契约都在这里落地，缺一条都会退化成"结果表乱跳"：
 *
 * ① **`devices`/`errors` 为 null 表示这一份没带清单**（轻量快照），保留手上那份；
 *    是数组（哪怕空）才是清单本身。把 null 当空数组处理，结果表会在每个拍子上
 *    被清空一次 —— 症状是"边扫边看"完全失效，扫完才一次性冒出来。
 * ② **长度没变就不换数组实例**：清单在服务端是只增的（index 就是数组下标），
 *    长度相等即内容相等。换实例会让 el-table 走 setData → 整表重新 patch、
 *    重算列宽；保持同一个数组则连它的 watcher 都不触发。
 */
function applySnapshot(data) {
  const prev = job.value

  let nextDevices = Array.isArray(data.devices) ? data.devices : prev?.devices ?? []
  if (prev?.devices && nextDevices.length === prev.devices.length) {
    nextDevices = prev.devices
  }
  const nextErrors = Array.isArray(data.errors) ? data.errors : prev?.errors ?? []

  job.value = { ...data, devices: nextDevices, errors: nextErrors }
}

// ---------- 结果 -> 设备 ----------
function handleSelectionChange(rows) {
  selectedRows.value = rows
}

function handleOpenAddDialog() {
  if (selectedRows.value.length === 0) {
    ElMessage.warning(t('deviceScan.noSelection'))
    return
  }
  addPage.value = 1
  addRows.value = selectedRows.value.map((row) => ({
    index: row.index,
    host: row.host,
    unitId: row.unitId,
    // 留空时代后端会按地址生成；这里把那个默认值当占位符显示出来，
    // 用户一眼能看出不填会发生什么。
    suggestedName: row.unitId > 1 ? `${row.host}-${row.unitId}` : row.host,
    name: '',
    description: '',
  }))
  addDialogVisible.value = true
}

// itemsOf 把待建行投影成提交载荷（不回传 protocolJson，见 api 模块的说明）。
function itemsOf(rows) {
  return rows.map((r) => ({ index: r.index, name: r.name, description: r.description }))
}

// submitCreate 提交一批待建行。confirmDuplicate 为 true 表示"地址重复也已经
// 确认过了"—— 两段式提交的第二段，见 handleCreateDevices。
function submitCreate(items, confirmDuplicate) {
  return createDevicesFromScan({ jobId: job.value.jobId, items, confirmDuplicate })
}

// handleCreateDevices 第一段：提交勾选的设备。
//
// 后端对"扫描地址与库内某台设备相同"的行**默认不建**，只回一条带
// code=addressExists 的明细 —— 这里据它弹一次确认，用户点"仍要新建"再走
// handleConfirmDuplicate 重提。判据必须是 code 而不是 reason 文案：文案随
// i18n 改，code 是契约。
async function handleCreateDevices() {
  creating.value = true
  try {
    const res = await submitCreate(itemsOf(addRows.value), false)
    const dups = (res.errors ?? []).filter((e) => e.code === 'addressExists')
    if (dups.length > 0) {
      pendingResult.value = res
      pendingDuplicateErrors.value = dups
      dupDialogVisible.value = true
      return
    }
    finishCreate(res)
  } catch {
    // 拦截器已提示
  } finally {
    creating.value = false
  }
}

// handleConfirmDuplicate 第二段：用户确认"地址重复也要建"。
//
// **只重提被判重复的那些行**，不是整批重来 —— 第一轮已经把其余行建好了，
// 整批重来会全部撞名。代价是第二轮的 row 是它在重提数组里的下标，必须映射回
// 第一轮的行号，否则 finishCreate 会把失败的行留错。
async function handleConfirmDuplicate() {
  const first = pendingResult.value
  const dupRows = pendingDuplicateErrors.value.map((e) => e.row)
  const offset = dupRows.map((r) => r - 1)
  creating.value = true
  try {
    const res = await submitCreate(
      itemsOf(offset.map((i) => addRows.value[i])),
      true,
    )
    // 重复行在第二轮里被重新判定（可能建成，也可能撞名），所以先把它们从第一轮
    // 的失败明细里摘掉，再并进第二轮的明细。
    const dupSet = new Set(dupRows)
    const retried = (res.errors ?? []).map((e) => ({ ...e, row: offset[e.row - 1] + 1 }))
    const keptErrors = first.errors.filter((e) => !dupSet.has(e.row))
    finishCreate({
      total: first.total,
      success: first.success + res.success,
      failed: keptErrors.length + retried.length,
      errors: [...keptErrors, ...retried],
    })
  } catch {
    // 拦截器已提示。第一轮的结果照样展示，这些行按失败留在弹窗里，可以再试。
    finishCreate(first)
  } finally {
    creating.value = false
    dupDialogVisible.value = false
  }
}

// handleSkipDuplicate 用户决定不建这些重复行：照原样展示第一轮的结果。
//
// 它们与撞名的失败行一视同仁地留在勾选里 —— 反悔的方式是再提交一次、这回点
// "仍要新建"（地址来自扫描结果，改名字解决不了重复，所以不给"改一改再试"的暗示）。
function handleSkipDuplicate() {
  dupDialogVisible.value = false
  finishCreate(pendingResult.value)
}

// finishCreate 收尾一次提交：展示明细，并把**没建成**的行留在弹窗里。
//
// **只摘掉真正入库的那些行。** 失败的行（撞名、协议行缺失、地址重复未确认）
// 如果一起被摘掉，用户连改个名字重试的入口都没有 —— 只能重新扫一遍网段
//（十几秒到几十秒）。errors[].row 是提交数组里的 1-based 下标（见后端
// CreateDevices 的 row 注释）。
function finishCreate(res) {
  importResult.value = res
  importResultVisible.value = true
  addDialogVisible.value = false

  const failedItems = new Set((res.errors ?? []).map((e) => e.row - 1))
  const keptRows = addRows.value.filter((_, i) => failedItems.has(i))
  if (keptRows.length < addRows.value.length) {
    // 打开弹窗时勾选的那批（addRows 是它的投影，index 一一对应），
    // 其中失败的那些留在表里。
    const chosen = new Set(selectedRows.value.map((s) => s.index))
    const keptIndexes = new Set(keptRows.map((r) => r.index))
    const nextDevices = devices.value.filter(
      (d) => !chosen.has(d.index) || keptIndexes.has(d.index),
    )
    job.value = { ...job.value, devices: nextDevices }
    // 勾选重建：失败的那些仍然勾着，改个名字就能直接再提交一次。
    selectedRows.value = []
    tableRef.value?.clearSelection()
    keptRows.forEach((r) => {
      const row = nextDevices.find((d) => d.index === r.index)
      if (row) tableRef.value?.toggleRowSelection(row, true)
    })
  }
}

// ---------- 工具 ----------
async function copyJson(text) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(t('deviceScan.copied'))
  } catch {
    ElMessage.warning(t('deviceScan.copyFailed'))
  }
}
</script>

<style scoped>
/* 插件声明的扫描参数表单：把各控件收成等宽的一列。
   默认宽度下 el-input 是 width:100%（撑满整条内容区），而 el-input-number
   在 flex 容器里会被挤到 ~150px —— 两个字段并排读起来就是一个通栏长条压着
   一个小步进器。扫描参数全是短文本/数字/下拉，定宽不会截断。
   类名与设备页的动态表单一致，指的是同一个东西。 */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 260px;
}
</style>
