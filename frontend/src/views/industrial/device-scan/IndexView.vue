<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="scan-page">
    <!-- 页头：与设备对象页同一套字号/留白层级；右侧常驻「本次发现」计数，
         扫到一台就跳一下，不用视线下移去某张卡片里找数字。 -->
    <header class="page-head enter">
      <div>
        <p class="eyebrow">
          <span class="eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="page-title">{{ t('menu.deviceScan') }}</h1>
        <p class="page-sub">{{ t('deviceScan.intro') }}</p>
      </div>
      <div class="page-stat">
        <span class="page-stat-num">{{ displayFound }}</span>
        <span class="page-stat-label">{{ t('deviceScan.foundUnit') }}</span>
      </div>
    </header>

    <!-- 功能未启用 / 没有可用协议或网段：直接把原因说清楚，而不是给一个点了没反应的按钮。
         用页面自己的通知条而不是 el-alert：后者的实心柱状图标在浅色画布上过于抢眼，
         而这里先要读的是"为什么不能扫"，不是"注意"。 -->
    <div v-if="caps && !caps.enabled" class="notice is-warning enter">
      <span class="notice-mark" aria-hidden="true">
        <el-icon><AlertTriangle /></el-icon>
      </span>
      <p class="notice-text">{{ t('deviceScan.disabled') }}</p>
    </div>
    <template v-else-if="caps">
      <div v-if="protocols.length === 0" class="notice is-info enter">
        <span class="notice-mark" aria-hidden="true">
          <el-icon><InfoCircle /></el-icon>
        </span>
        <p class="notice-text">{{ t('deviceScan.noProtocol') }}</p>
      </div>
      <div v-if="subnets.length === 0" class="notice is-info enter">
        <span class="notice-mark" aria-hidden="true">
          <el-icon><InfoCircle /></el-icon>
        </span>
        <p class="notice-text">{{ t('deviceScan.noSubnet') }}</p>
      </div>
    </template>

    <!-- 工作区：扫描配置与「开始扫描」共用一块表面，按钮就在配置的右上角，
         调完参数不必再往下找动作。 -->
    <section class="panel enter" style="animation-delay: 60ms">
      <div class="toolbar">
        <div class="toolbar-lead">
          <span class="toolbar-mark" aria-hidden="true">
            <el-icon><Radar /></el-icon>
          </span>
          <div class="toolbar-copy">
            <p class="toolbar-title">{{ t('deviceScan.configTitle') }}</p>
            <p class="toolbar-sub">
              {{ currentProtocol?.title || currentProtocol?.name || t('deviceScan.protocolPlaceholder') }}
            </p>
          </div>
        </div>
        <div class="toolbar-actions">
          <span v-if="job && scanning" class="estimate">
            {{ t('deviceScan.estimated', { s: Math.ceil((job.estimateMaxMs || 0) / 1000) }) }}
          </span>
          <el-button v-if="scanning" :loading="canceling" @click="handleCancel">
            {{ t('deviceScan.cancel') }}
          </el-button>
          <el-button
            type="primary"
            :icon="Radar"
            :loading="starting"
            :disabled="!canStart"
            @click="handleStart"
          >
            {{ scanning ? t('deviceScan.rescan') : t('deviceScan.start') }}
          </el-button>
        </div>
      </div>

      <div class="config-body">
        <el-form :label-width="formLabelWidth" label-position="right">
          <el-form-item :label="t('deviceScan.protocol')" required>
            <el-select
              v-model="form.protocol"
              class="field-select"
              :placeholder="t('deviceScan.protocolPlaceholder')"
              :disabled="scanning"
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
            <span v-if="currentProtocol && !currentProtocol.protocolId" class="field-warn">
              {{ t('deviceScan.protocolRowMissing') }}
            </span>
            <!-- 协议说明是**协议级**的（"这个协议的扫描是怎么做的"），不是扫描参数
                 的一部分，所以挂在这一行、与网段/端口的提示同款；放在参数块里会
                 变成一堵横跨整张卡片的通栏文字，粘在最后一个字段下面。 -->
            <div v-if="currentProtocol?.description" class="field-desc">
              {{ currentProtocol.description }}
            </div>
          </el-form-item>

          <el-form-item :label="t('deviceScan.subnet')">
            <el-checkbox-group
              v-model="form.subnets"
              class="subnet-group"
              :disabled="scanning"
            >
              <el-checkbox v-for="s in subnets" :key="s.cidr" :value="s.cidr" class="subnet-chip">
                {{ s.cidr }}
                <span class="subnet-meta">
                  {{ s.iface }} · {{ t('deviceScan.hostCount', { n: s.hostCount }) }}
                </span>
              </el-checkbox>
            </el-checkbox-group>
            <!-- 占满一行：el-form-item__content 是 flex 容器，不整行铺开这句提示会并排挤在网段文字右边 -->
            <div class="field-hint is-block">{{ t('deviceScan.subnetHint') }}</div>
          </el-form-item>

          <el-form-item :label="t('deviceScan.port')">
            <el-input-number
              v-model="form.port"
              class="field-port"
              :min="1"
              :max="65535"
              :disabled="scanning"
              controls-position="right"
            />
            <span class="field-hint">
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
      </div>
    </section>

    <!-- ========== 进度 ==========
         状态胶囊 + 三个读数收在一条工具条里，下面是唯一的进度条；
         原来的 el-tag 与四段灰字平铺，扫到一半时看不出哪一段是结论。 -->
    <section v-if="job" class="panel progress-panel enter" style="animation-delay: 120ms">
      <div class="toolbar">
        <div class="toolbar-lead">
          <span class="state-pill" :class="stateClass">
            <span class="state-dot" aria-hidden="true" />
            {{ stateText }}
          </span>
          <p class="toolbar-title">{{ t('deviceScan.progressTitle') }}</p>
        </div>
        <div class="progress-meta">
          <span class="meta-item">{{ t('deviceScan.scanned', { done: job.scanned, total: job.total }) }}</span>
          <span class="meta-sep" aria-hidden="true" />
          <span class="meta-item">{{ t('deviceScan.foundCount', { n: job.found }) }}</span>
          <span class="meta-sep" aria-hidden="true" />
          <span class="meta-item is-muted">{{ t('deviceScan.elapsed', { s: (job.elapsedMs / 1000).toFixed(1) }) }}</span>
        </div>
      </div>

      <div class="progress-body">
        <el-progress
          :percentage="progressPercent"
          :status="job.state === 'running' ? undefined : progressStatus"
          :stroke-width="10"
        />

        <p v-if="job.reason" class="progress-reason">{{ job.reason }}</p>

        <!-- 截断必须说出来：否则用户会从"没扫到设备"得出"这个网段没有设备"的错误结论 -->
        <div v-if="job.truncated" class="notice is-warning is-inline">
          <span class="notice-mark" aria-hidden="true">
            <el-icon><AlertTriangle /></el-icon>
          </span>
          <p class="notice-text">
            {{ t('deviceScan.truncated', { requested: job.requestedTargets, planned: job.plannedTargets }) }}
          </p>
        </div>

        <!-- 探测本身失败的目标：与"没扫到"是两回事，需要单独列出来。
             上限是**目标数**（默认 1024，可配到 65536），所以这张表也要分页 ——
             它虽然只有两列，但一次全挂上去就是几千个单元格。 -->
        <el-collapse v-if="scanErrors.length" class="errors-collapse">
          <el-collapse-item
            :title="t('deviceScan.errorCount', { n: scanErrors.length })"
            name="errors"
          >
            <p class="collapse-hint">{{ t('deviceScan.errorHint') }}</p>
            <el-table :data="pagedErrors" class="mini-table" size="small" max-height="240">
              <el-table-column prop="host" :label="t('deviceScan.column.host')" width="160">
                <template #default="{ row }">
                  <span class="host-text">{{ row.host }}</span>
                </template>
              </el-table-column>
              <el-table-column prop="reason" :label="t('deviceScan.column.reason')" min-width="260" show-overflow-tooltip />
            </el-table>
            <div v-if="errorTotal > errorPageSize" class="dialog-pager">
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
      </div>
    </section>

    <!-- ========== 扫描结果 ========== -->
    <section v-if="job" class="panel results-panel enter" style="animation-delay: 180ms">
      <div class="toolbar">
        <div class="toolbar-lead">
          <span class="toolbar-mark" aria-hidden="true">
            <el-icon><ScanRadar /></el-icon>
          </span>
          <div class="toolbar-copy">
            <p class="toolbar-title">{{ t('deviceScan.resultTitle') }}</p>
            <p class="toolbar-sub">{{ t('deviceScan.resultHint') }}</p>
          </div>
        </div>
        <div class="toolbar-actions">
          <el-button
            type="primary"
            :icon="Plus"
            :disabled="selectedRows.length === 0"
            @click="handleOpenAddDialog"
          >
            {{ t('deviceScan.addDevices', { n: selectedRows.length }) }}
          </el-button>
        </div>
      </div>

      <!-- **只把当前页交给 el-table**：一次扫出几百上千台时整表挂载是几万个组件
           实例，主线程会被 patch 占满几十秒、浏览器表现为页面假死；行数还会把
           页面拉成几千行高。分页组件与点扫弹窗共用一个组合式函数
           （composables/usePagedRows.js），默认 20 行。 -->
      <el-table
        ref="tableRef"
        class="scan-table"
        :data="pagedDevices"
        row-key="index"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <!-- reserve-selection：轮询在有新设备时会换掉 data 实例，没有它 el-table
             会直接 clearSelection()（还会 emit selection-change []），用户勾到一半
             的选中态就被抹掉；勾选态按 row-key 重算，翻页回来还是勾着的。
             row-key 用 index —— 它是结果在快照数组里的下标，只增不变。 -->
        <el-table-column type="selection" width="44" align="center" reserve-selection :selectable="() => true" />

        <el-table-column :label="t('deviceScan.column.host')" width="156">
          <template #default="{ row }">
            <span class="host-text">{{ row.host }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="port" :label="t('deviceScan.column.port')" width="84" align="center" class-name="col-num" />
        <el-table-column :label="t('deviceScan.column.unitId')" width="80" align="center" class-name="col-num">
          <template #default="{ row }">
            <span :class="row.unitId ? '' : 'cell-muted'">{{ row.unitId || '—' }}</span>
          </template>
        </el-table-column>

        <!-- 名称是这一行的身份：与设备对象页一样给一个小图标底座，滚动中可被一眼拾取 -->
        <el-table-column :label="t('common.name')" min-width="200">
          <template #default="{ row }">
            <div v-if="row.name" class="name-cell">
              <span class="name-mark" aria-hidden="true">
                <el-icon><DeviceChip /></el-icon>
              </span>
              <span class="name-text" :title="row.name">{{ row.name }}</span>
            </div>
            <span v-else class="cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column prop="vendor" :label="t('deviceScan.column.vendor')" min-width="130" show-overflow-tooltip class-name="col-muted" />
        <el-table-column prop="model" :label="t('deviceScan.column.model')" min-width="130" show-overflow-tooltip class-name="col-muted" />
        <el-table-column prop="version" :label="t('deviceScan.column.version')" width="104" class-name="col-muted" />
        <el-table-column :label="t('deviceScan.column.extra')" min-width="190">
          <template #default="{ row }">
            <div v-if="row.extra?.length" class="extra-tags">
              <span v-for="a in row.extra" :key="a.name" class="proto-chip">
                {{ a.name }}：{{ a.value }}
              </span>
            </div>
            <span v-else class="cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceScan.column.protocolJson')" width="104" align="center">
          <template #default="{ row }">
            <!-- persistent=false：默认的 true 会让每一行挂载时就把 popper 子树建出来
                 （还要 teleport 到 body），而这里每行都会有一个 tooltip。非持久不影响
                 行为 —— 内容每次展开时重建，而它本来就只读 row.protocolJson。 -->
            <el-tooltip
              v-if="row.protocolJson"
              :content="t('deviceScan.copyJson')"
              placement="top"
              :persistent="false"
            >
              <el-button
                class="row-btn"
                type="primary"
                link
                size="small"
                :icon="Document"
                :aria-label="t('deviceScan.copyJson')"
                @click="copyJson(row.protocolJson)"
              />
            </el-tooltip>
            <span v-else class="cell-muted">—</span>
          </template>
        </el-table-column>

        <!-- 空表：扫描中的"还没出现"与扫完的"没有结果"是两回事，用文案区分 -->
        <template #empty>
          <div class="empty">
            <span class="empty-mark" aria-hidden="true">
              <el-icon><ScanRadar /></el-icon>
            </span>
            <p class="empty-title">{{ t('deviceScan.noResult') }}</p>
            <p class="empty-hint">
              {{ scanning ? t('deviceScan.scanningHint') : t('deviceScan.emptyHint') }}
            </p>
          </div>
        </template>
      </el-table>

      <div v-if="deviceTotal > 0" class="pager">
        <el-pagination
          v-model:current-page="devicePage"
          v-model:page-size="devicePageSize"
          :page-sizes="[20, 50, 100]"
          :total="deviceTotal"
          layout="total, sizes, prev, pager, next, jumper"
          background
        />
      </div>
    </section>

    <!-- ========== 添加为设备 ========== -->
    <el-dialog
      v-model="addDialogVisible"
      class="scan-dialog"
      :title="t('deviceScan.addDialogTitle')"
      width="760px"
      :close-on-click-modal="false"
    >
      <p class="dialog-hint">{{ t('deviceScan.addDialogHint') }}</p>
      <!-- 这里也必须分页：勾了 1000 行就是 1000 × 2 个 el-input 一起挂上来，
           输入框的挂载成本远高于普通单元格。分页不丢编辑 —— 输入框绑的是
           addRows 里的真实行对象，翻页只是换显示哪一段。 -->
      <el-table :data="pagedAddRows" class="mini-table" size="small" max-height="420">
        <el-table-column :label="t('deviceScan.column.host')" width="150">
          <template #default="{ row }">
            <span class="host-text">{{ row.host }}</span>
            <span v-if="row.unitId" class="unit-suffix">#{{ row.unitId }}</span>
          </template>
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
      <div v-if="addTotal > 0" class="dialog-pager">
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
      class="scan-dialog"
      :title="t('deviceScan.duplicateTitle')"
      width="620px"
      :close-on-click-modal="false"
    >
      <p class="dialog-lead">{{ t('deviceScan.duplicateConfirm', { n: pendingDuplicateErrors.length }) }}</p>
      <el-table :data="pendingDuplicateErrors" class="mini-table" size="small" max-height="280">
        <el-table-column prop="name" :label="t('common.name')" width="150" show-overflow-tooltip />
        <el-table-column prop="reason" :label="t('deviceScan.column.reason')" min-width="260" show-overflow-tooltip />
      </el-table>
      <p class="dialog-hint is-quiet">{{ t('deviceScan.duplicateHint') }}</p>
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
import {
  IconAlertTriangle as AlertTriangle,
  IconCpu as DeviceChip,
  IconFileText as Document,
  IconInfoCircle as InfoCircle,
  IconPlus as Plus,
  IconRadar as ScanRadar,
  IconRadar2 as Radar,
} from '@tabler/icons-vue'

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
// 页头计数：还没有任务时显示占位符 —— "0 台"会被误读成"这个网段没有设备"
const displayFound = computed(() =>
  job.value ? job.value.found ?? 0 : '—',
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
const stateClass = computed(() => {
  switch (job.value?.state) {
    case 'running': return 'is-running'
    case 'done': return 'is-done'
    case 'canceled': return 'is-canceled'
    default: return 'is-failed'
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
/* 色板与字体来自 styles/tokens.css，页面只管版式 */
.scan-page {
  max-width: 1400px;
  margin: 0 auto;
}

/* 页头与各面板错峰落位；动画挂在区级包裹层，不挂表格行，避免影响 hover 重绘 */
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

/* ---------- 通知条 ---------- */
.notice {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 16px;
  padding: 11px 14px;
  font-size: 12.5px;
  line-height: 1.65;
  border: 1px solid transparent;
  border-radius: 12px;
}

.notice.is-info {
  color: #1e40af;
  background: #f5f9ff;
  border-color: #dbe8fb;
}

.notice.is-warning {
  color: #92400e;
  background: #fffbeb;
  border-color: #fce7b4;
}

.notice-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 20px;
  height: 20px;
  margin-top: 1px;
}

.notice-mark .el-icon {
  font-size: 16px;
}

.notice-text {
  margin: 0;
}

.notice.is-inline {
  margin: 12px 0 0;
}

/* ---------- 面板 / 工具条 ---------- */
.panel {
  margin-bottom: 16px;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 18px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.panel:last-of-type {
  margin-bottom: 0;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px 16px;
  padding: 14px 20px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border-bottom: 1px solid var(--line-soft);
}

.toolbar-lead {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.toolbar-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 32px;
  height: 32px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 9px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.08);
}

.toolbar-mark .el-icon {
  font-size: 17px;
}

.toolbar-copy {
  min-width: 0;
}

.toolbar-title {
  margin: 0;
  font-size: 13.5px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.toolbar-sub {
  margin: 2px 0 0;
  overflow: hidden;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-4);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.toolbar-actions :deep(.el-button) {
  height: 34px;
  padding: 0 14px;
  border-radius: 9px;
  font-weight: 500;
}

.estimate {
  font-size: 12px;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

/* ---------- 扫描配置 ---------- */
.config-body {
  padding: 20px 20px 4px;
}

.config-body :deep(.el-form-item__label) {
  font-size: 13px;
  color: var(--ink-2);
}

.config-body :deep(.el-form-item) {
  margin-bottom: 18px;
}

/* 输入类控件统一 34px 高、9px 圆角，与设备对象页工具栏同一条基线 */
.config-body :deep(.el-input__wrapper),
.config-body :deep(.el-select__wrapper) {
  min-height: 34px;
  border-radius: 9px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--line) inset;
  transition: box-shadow 0.18s ease;
}

.config-body :deep(.el-input__wrapper:hover),
.config-body :deep(.el-select__wrapper:hover) {
  box-shadow: 0 0 0 1px #cbd8e6 inset;
}

.config-body :deep(.el-input__wrapper.is-focus),
.config-body :deep(.el-select__wrapper.is-focused) {
  box-shadow: 0 0 0 1px var(--brand) inset, 0 0 0 3px rgba(37, 99, 235, 0.1);
}

.config-body :deep(.el-input-number .el-input__inner) {
  text-align: left;
}

.field-select {
  width: 320px;
}

.field-port {
  width: 160px;
}

.field-warn {
  margin-left: 12px;
  font-size: 12px;
  line-height: 1.6;
  color: #c2410c;
}

.field-desc {
  flex: 0 0 100%;
  margin-top: 6px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--ink-4);
}

.field-hint {
  margin-left: 12px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.field-hint.is-block {
  flex: 0 0 100%;
  margin: 6px 0 0;
}

/* 网段多选：每个网段是一枚可点的卡片，勾中时整枚染蓝；
   原来一排系统复选框 + 灰字说明，扫之前还得逐行读 iface 才能确认选的是哪块网卡 */
.subnet-group {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}

.subnet-group :deep(.el-checkbox) {
  height: auto;
  margin-right: 0;
  padding: 7px 11px;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 9px;
  transition: border-color 0.16s ease, background-color 0.16s ease, box-shadow 0.16s ease;
}

.subnet-group :deep(.el-checkbox:hover) {
  border-color: #cbd8e6;
  background: #f8fafc;
}

.subnet-group :deep(.el-checkbox.is-checked) {
  background: var(--brand-soft);
  border-color: rgba(37, 99, 235, 0.35);
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.06);
}

.subnet-group :deep(.el-checkbox__label) {
  padding-left: 8px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ink-2);
}

.subnet-group :deep(.el-checkbox.is-checked .el-checkbox__label) {
  color: var(--ink);
}

.subnet-meta {
  margin-left: 4px;
  font-size: 11.5px;
  font-weight: 400;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

.subnet-group :deep(.el-checkbox.is-disabled) {
  opacity: 0.6;
}

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

/* ---------- 进度 ---------- */
.progress-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  font-size: 12.5px;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

.meta-item.is-muted {
  color: var(--ink-4);
}

.meta-sep {
  width: 1px;
  height: 14px;
  background: var(--line);
}

.state-pill {
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

.state-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.state-pill.is-running {
  color: #1d4ed8;
  background: var(--brand-soft);
}

.state-pill.is-running .state-dot {
  animation: statusPulse 1.4s ease-in-out infinite;
}

.state-pill.is-done {
  color: #047857;
  background: #ecfdf5;
}

.state-pill.is-canceled {
  color: #b45309;
  background: #fffbeb;
}

.state-pill.is-failed {
  color: #be123c;
  background: #fff1f2;
}

@keyframes statusPulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}

.progress-body {
  padding: 16px 20px 18px;
}

.progress-body :deep(.el-progress-bar__outer) {
  background: #eef2f7;
  border-radius: 999px;
}

.progress-body :deep(.el-progress-bar__inner) {
  border-radius: 999px;
}

.progress-reason {
  margin: 10px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-3);
}

.errors-collapse {
  margin-top: 12px;
  overflow: hidden;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

.errors-collapse :deep(.el-collapse) {
  border: none;
}

.errors-collapse :deep(.el-collapse-item__header) {
  height: 42px;
  padding: 0 14px;
  font-size: 12.5px;
  font-weight: 500;
  color: var(--ink-2);
  background: #fbfcfe;
  border-bottom-color: var(--line-soft);
}

.errors-collapse :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}

.errors-collapse :deep(.el-collapse-item__content) {
  padding: 12px 14px 14px;
}

.collapse-hint {
  margin: 0 0 10px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* ---------- 表格 ---------- */
.scan-table {
  --el-table-border-color: transparent;
  --el-table-bg-color: #fff;
  --el-table-tr-bg-color: #fff;
  --el-table-header-bg-color: #f8fafc;
  --el-table-row-hover-bg-color: #f8fbff;
  font-size: 13px;
  color: var(--ink-2);
}

.scan-table :deep(td.el-table__cell) {
  background-color: #fff;
}

.scan-table :deep(.el-table__body tr:hover > td.el-table__cell) {
  background-color: #f8fbff;
}

/* Element 默认在表体底部画一条贯穿线，与卡片圆角相撞时会在两角露头 */
.scan-table :deep(.el-table__inner-wrapper::before) {
  display: none;
}

.scan-table :deep(th.el-table__cell) {
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
.scan-table :deep(th .cell) {
  white-space: nowrap;
}

.scan-table :deep(tr.el-table__row td.el-table__cell) {
  padding: 11px 0;
  border-bottom: 1px solid var(--line-soft);
}

.scan-table :deep(.el-table__body tr:last-child td.el-table__cell) {
  border-bottom: none;
}

.scan-table :deep(.cell) {
  padding: 0 16px;
}

.scan-table :deep(.el-checkbox__inner) {
  border-radius: 4px;
}

.scan-table :deep(.col-num .cell) {
  font-size: 12.5px;
  color: var(--ink-3);
  font-variant-numeric: tabular-nums;
}

.scan-table :deep(.col-muted .cell) {
  color: var(--ink-3);
}

.cell-muted {
  color: var(--ink-4);
}

.host-text {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}

.unit-suffix {
  margin-left: 2px;
  font-size: 11.5px;
  color: var(--ink-4);
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

/* 附加信息/协议名是技术标识，用中性 chip 而不是彩色标签，避免和状态色抢语义 */
.extra-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

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

/* 行内操作：默认中性灰，悬停才染蓝 —— 与设备对象页的行内按钮同一套 */
.scan-table :deep(.row-btn.el-button) {
  width: 28px;
  height: 28px;
  margin: 0;
  padding: 0;
  color: var(--ink-4);
  border-radius: 8px;
}

.scan-table :deep(.row-btn.el-button .el-icon) {
  font-size: 16px;
}

.scan-table :deep(.row-btn.el-button:hover) {
  color: var(--brand);
  background: var(--brand-soft);
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

/* ---------- 分页 ---------- */
.pager {
  display: flex;
  justify-content: flex-end;
  padding: 14px 20px;
  border-top: 1px solid var(--line-soft);
}

.pager :deep(.el-pagination),
.dialog-pager :deep(.el-pagination) {
  --el-pagination-button-bg-color: #f4f6fa;
  --el-pagination-hover-color: var(--brand);
}

.pager :deep(.el-pager li),
.pager :deep(.btn-prev),
.pager :deep(.btn-next),
.dialog-pager :deep(.el-pager li),
.dialog-pager :deep(.btn-prev),
.dialog-pager :deep(.btn-next) {
  border-radius: 8px;
  font-weight: 500;
}

.dialog-pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}

/* ---------- 小表格（探测失败 / 弹窗内） ---------- */
.mini-table {
  --el-table-border-color: transparent;
  --el-table-header-bg-color: #f8fafc;
  --el-table-row-hover-bg-color: #f8fbff;
  font-size: 12.5px;
  color: var(--ink-2);
}

.mini-table :deep(.el-table__inner-wrapper::before) {
  display: none;
}

.mini-table :deep(th.el-table__cell) {
  height: 36px;
  padding: 0;
  background: #f8fafc;
  color: var(--ink-3);
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  border-bottom: 1px solid var(--line);
}

.mini-table :deep(tr.el-table__row td.el-table__cell) {
  padding: 7px 0;
  border-bottom: 1px solid var(--line-soft);
}

.mini-table :deep(.el-table__body tr:last-child td.el-table__cell) {
  border-bottom: none;
}

.mini-table :deep(.cell) {
  padding: 0 12px;
}

/* ---------- 对话框 ----------
   el-dialog 会被 teleport 到 body，scoped 的 :deep 够不到，用唯一类名做全局作用域 */
:global(.scan-dialog) {
  border-radius: 18px;
  box-shadow: 0 24px 60px -24px rgba(15, 23, 42, 0.35);
}

:global(.scan-dialog .el-dialog__header) {
  margin: 0;
  padding: 20px 24px 16px;
  border-bottom: 1px solid #eef2f7;
}

:global(.scan-dialog .el-dialog__title) {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: #0f172a;
}

:global(.scan-dialog .el-dialog__headerbtn) {
  top: 12px;
  right: 12px;
  width: 30px;
  height: 30px;
  border-radius: 8px;
}

:global(.scan-dialog .el-dialog__headerbtn:hover) {
  background: #f4f6fa;
}

:global(.scan-dialog .el-dialog__body) {
  padding: 20px 24px 8px;
}

:global(.scan-dialog .el-dialog__footer) {
  padding: 14px 24px 18px;
  border-top: 1px solid #eef2f7;
}

.dialog-hint {
  margin: 0 0 14px;
  padding: 10px 12px;
  font-size: 12.5px;
  line-height: 1.65;
  color: var(--ink-3);
  background: #f8fafc;
  border: 1px solid var(--line-soft);
  border-radius: 10px;
}

.dialog-hint.is-quiet {
  margin: 12px 0 0;
  color: var(--ink-4);
  background: transparent;
  border: none;
  padding: 0;
}

.dialog-lead {
  margin: 0 0 14px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--ink-2);
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

  .progress-meta {
    width: 100%;
  }
}

@media (max-width: 640px) {
  .toolbar,
  .toolbar-actions {
    width: 100%;
  }

  .toolbar-actions {
    justify-content: flex-end;
  }

  .field-select {
    width: 100%;
  }

  .field-warn,
  .field-hint {
    margin-left: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .enter,
  .state-pill.is-running .state-dot {
    animation: none;
  }

  .scan-page *,
  .scan-page *::before,
  .scan-page *::after {
    transition: none !important;
  }
}
</style>
