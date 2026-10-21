<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

// 四个报警页（设备 / 通道 / 点位 / 超限）共用的表格实现。它们只有 target_type 不同，
// 所以实现只留一份，各页的 IndexView.vue 只是把 targetType 传进来的薄壳 —— 目录结构
// 与菜单一一对应，加一列却只要改这一处。
//
// 因此本组件**不** watch props.targetType：切菜单时路由换的是薄壳组件，整棵子树会
// 重挂载，onMounted 照常重新拉数据。（四个路由曾复用同一个组件实例，才必须靠那个
// watch 补一次拉取 —— 拆成四页后它既是多余的，又是很容易漏写的一环。）
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.alarm') }}
        </p>
        <h1 class="iot-title">{{ pageTitle }}</h1>
        <p class="iot-sub">{{ t('alarm.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('alarm.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：筛选、表格、分页共用一块表面 -->
    <section ref="panelRef" class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.targetName"
            class="iot-search"
            :placeholder="t('alarm.targetNamePlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-select
            v-model="queryForm.status"
            class="status-filter"
            :placeholder="t('alarm.alarmStatus')"
            clearable
          >
            <el-option :label="t('alarm.statusActive')" value="active" />
            <el-option :label="t('alarm.statusCleared')" value="cleared" />
          </el-select>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <!-- 窄屏藏起序号与目标类型：报警页已经按类型分页展示，同一页里每行的
             目标类型都相同，这一列是重复信息；序号只是行号。两者都在宽屏回归。 -->
        <el-table-column
          v-if="!compact"
          type="index"
          :label="t('common.index')"
          width="60"
          align="center"
        />

        <!-- 目标名称是这一行的身份：小图标底座让报警对象在滚动中可被一眼拾取 -->
        <el-table-column :label="t('alarm.targetName')" :min-width="compact ? 155 : 170">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Bell /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.targetName">{{ row.targetName || '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column v-if="!compact" :label="t('alarm.targetType')" width="96" align="center">
          <template #default="{ row }">
            <span class="iot-chip">{{ targetTypeName(row.targetType) }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('alarm.alarmTypeCol')" width="110" align="center">
          <template #default="{ row }">
            <span
              class="iot-pill"
              :class="row.alarmType === 'recover' ? 'is-green' : 'is-rose'"
            >
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ alarmTypeLabel(row) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('alarm.level')" width="88" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="levelPillClass(row.level)">{{ levelName(row.level) }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="content"
          :label="t('alarm.content')"
          :min-width="compact ? 150 : 180"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('alarm.statusCol')" width="104" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 'active' ? 'is-rose' : 'is-green'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ statusLabel(row) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="firstOccurTime"
          :label="t('alarm.firstOccurTime')"
          width="150"
          class-name="iot-col-time"
        />
        <el-table-column
          prop="lastOccurTime"
          :label="t('alarm.lastOccurTime')"
          width="150"
          class-name="iot-col-time"
        />
        <el-table-column
          prop="clearTime"
          :label="t('alarm.clearTime')"
          width="150"
          class-name="iot-col-time"
        />

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Bell /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.targetName || queryForm.status
                ? t('alarm.emptySearch')
                : t('alarm.emptyTitle') }}
            </p>
            <p v-if="!queryForm.targetName && !queryForm.status" class="iot-empty-hint">
              {{ t('alarm.emptyHint') }}
            </p>
            <el-button v-if="queryForm.targetName || queryForm.status" @click="handleReset">
              {{ t('common.reset') }}
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
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  IconBell as Bell,
  IconSearch as Search,
} from '@tabler/icons-vue'
import { ALARM_TARGET_TYPES, getAlarmPage } from '@/api/modules/alarm'

const { t } = useI18n()

// 页面标题映射。
const TITLE_KEYS = {
  device: 'alarm.deviceTitle',
  channel: 'alarm.channelTitle',
  point: 'alarm.pointTitle',
  // 超限报警是独立 target_type（后端刻意与 point 分开，见 alarm.TypePointThreshold），
  // 但主体确实是点位，所以「目标类型」标签复用 typePoint，只有页面标题另起一个。
  point_threshold: 'alarm.thresholdTitle',
}

// 「目标类型」列的文案映射。与上面一样用表而不是三元表达式：三元在加第三类（点位）时
// 会把点位的值静默算成通道。
const TARGET_TYPE_KEYS = {
  device: 'alarm.typeDevice',
  channel: 'alarm.typeChannel',
  point: 'alarm.typePoint',
  point_threshold: 'alarm.typePoint',
}

// 由各页薄壳注入，写死在薄壳里（device/IndexView.vue 等）。用 required + validator
// 而不是给个默认值：字面量拼错不会报错，只会静默查到一份空列表 —— 那是"看起来像最近
// 没有报警"的假象，开发期就该吼一声。
const props = defineProps({
  targetType: {
    type: String,
    required: true,
    validator: (value) => ALARM_TARGET_TYPES.includes(value),
  },
})

const pageTitle = computed(() => t(TITLE_KEYS[props.targetType] || 'alarm.deviceTitle'))

function targetTypeName(targetType) {
  return TARGET_TYPE_KEYS[targetType] ? t(TARGET_TYPE_KEYS[targetType]) : targetType
}

// ---------- 查询表单 ----------
const queryForm = reactive({
  targetName: '',
  status: '',
})

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// ---------- 分页 ----------
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 首屏还没拿到数时显示占位符而不是 0：「0 条报警」会被当成"最近没有任何报警"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value
)

// ---------- 获取列表（分页） ----------
async function fetchList() {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      size: pageSize.value,
      targetType: props.targetType,
    }
    if (queryForm.targetName) params.targetName = queryForm.targetName
    if (queryForm.status) params.status = queryForm.status
    const res = await getAlarmPage(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.error('获取报警列表失败', err)
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
  queryForm.targetName = ''
  queryForm.status = ''
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

// ---------- 类型 / 状态展示 ----------
// 后端的 alarmTypeName / statusName 是中文硬编码，英文界面下也会返回中文。
// 按枚举值在前端翻译，未知值回退到后端文案，避免新类型出现时整列空白。
const ALARM_TYPE_KEYS = {
  offline: 'alarm.alarmTypeOffline',
  quality: 'alarm.alarmTypeQuality',
  threshold: 'alarm.alarmTypeThreshold',
  recover: 'alarm.alarmTypeRecover',
}

function alarmTypeLabel(row) {
  const key = ALARM_TYPE_KEYS[row.alarmType]
  return key ? t(key) : row.alarmTypeName
}

function statusLabel(row) {
  return row.status === 'active' ? t('alarm.statusActive') : t('alarm.statusCleared')
}

// ---------- 级别展示 ----------
function levelName(level) {
  const map = {
    critical: 'alarm.levelCritical',
    error: 'alarm.levelError',
    warning: 'alarm.levelWarning',
    info: 'alarm.levelInfo',
  }
  return map[level] ? t(map[level]) : level || '—'
}

// 级别用软底胶囊：critical / error 都落在红系，warning 琥珀、info 中性；
// 不再使用实心 el-tag，避免整列都在喊。
function levelPillClass(level) {
  const map = {
    critical: 'is-rose',
    error: 'is-rose',
    warning: 'is-amber',
    info: 'is-gray',
  }
  return map[level] || 'is-gray'
}

// ---------- 紧凑列 ----------
// 宽屏十列合计 1258px，1600 分辨率下正好放得下；再窄就会横向滚动。
// 阈值按面板实际宽度算而不是 window.innerWidth：侧栏可拖动、可折叠，
// 用视口宽度判断会在侧栏折叠后仍少显示两列。
const panelRef = ref(null)
const compact = ref(false)
let panelObserver = null

// ---------- 初始化 ----------
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && panelRef.value) {
    panelObserver = new ResizeObserver(([entry]) => {
      compact.value = entry.contentRect.width < 1300
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
.status-filter {
  width: 150px;
}
</style>
