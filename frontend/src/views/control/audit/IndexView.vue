<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.control') }}
        </p>
        <h1 class="iot-title">{{ t('controlLog.title') }}</h1>
        <p class="iot-sub">{{ t('controlLog.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('controlLog.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar audit-toolbar">
        <div class="iot-toolbar-filters audit-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('controlLog.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-input
            v-model="queryForm.operator"
            class="audit-input"
            :placeholder="t('controlLog.operatorPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          />
          <el-input
            v-model="queryForm.requestId"
            class="audit-input audit-input-wide"
            :placeholder="t('controlLog.requestIdPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          />
          <el-select
            v-model="queryForm.source"
            class="audit-select"
            :placeholder="t('controlLog.allSources')"
            @change="handleSearch"
          >
            <el-option :label="t('controlLog.allSources')" value="" />
            <el-option
              v-for="s in SOURCES"
              :key="s"
              :label="t(`controlLog.sourceLabel.${s}`)"
              :value="s"
            />
          </el-select>
          <el-select
            v-model="queryForm.result"
            class="audit-select"
            :placeholder="t('controlLog.allResults')"
            @change="handleSearch"
          >
            <el-option :label="t('controlLog.allResults')" value="" />
            <el-option
              v-for="c in CODES"
              :key="c"
              :label="t(`controlLog.codeLabel.${c}`)"
              :value="c"
            />
          </el-select>
          <div class="audit-date">
            <el-date-picker
              v-model="dateRange"
              type="datetimerange"
              format="YYYY-MM-DD HH:mm"
              value-format="YYYY-MM-DD HH:mm:ss"
              :default-time="DEFAULT_TIME"
              :start-placeholder="t('controlLog.timeStart')"
              :end-placeholder="t('controlLog.timeEnd')"
              @change="handleSearch"
            />
          </div>
          <div class="audit-actions">
            <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
            <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
          </div>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="64" align="center" />
        <el-table-column
          prop="createdAt"
          :label="t('controlLog.createdAt')"
          width="168"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('controlLog.device')" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><DeviceDesktop /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.deviceName">{{ row.deviceName || '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.protocol')" width="130">
          <template #default="{ row }">
            <span class="iot-chip">{{ row.protocolName || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.point')" min-width="170" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="audit-point">
              <span class="iot-mono">{{ row.addressName }}</span>
              <span v-if="row.addressLabel" class="audit-point-label">{{ row.addressLabel }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.source')" width="100" align="center">
          <template #default="{ row }">
            <span class="iot-chip">{{ sourceText(row.source) }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="operator"
          :label="t('controlLog.operator')"
          width="120"
          show-overflow-tooltip
        />
        <el-table-column :label="t('controlLog.requestValue')" width="105" align="right">
          <template #default="{ row }">
            <span class="audit-value">{{ row.requestValue || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.resultValue')" width="105" align="right">
          <template #default="{ row }">
            <span class="audit-value">{{ row.resultValue || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.readbackValue')" width="105" align="right">
          <template #default="{ row }">
            <span class="audit-value">{{ row.readbackValue || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.verified')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.verified ? 'is-green' : 'is-gray'">
              {{ row.verified ? t('controlLog.verifiedYes') : t('controlLog.verifiedNo') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('controlLog.result')" width="120" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.code === 0 ? 'is-green' : 'is-rose'">
              {{ codeText(row.code) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="message"
          :label="t('controlLog.message')"
          min-width="200"
          show-overflow-tooltip
        />
        <el-table-column
          prop="requestId"
          :label="t('controlLog.requestId')"
          min-width="170"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="iot-mono audit-req">{{ row.requestId || '—' }}</span>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><History /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('controlLog.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('controlLog.emptyHint') }}</p>
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
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  IconDeviceDesktop as DeviceDesktop,
  IconHistory as History,
  IconSearch as Search,
} from '@tabler/icons-vue'
import { getControlLogPage } from '@/api/modules/control'

const { t } = useI18n()

const SOURCES = ['web', 'openapi', 'mqtt', 'agent']
const CODES = [0, 1, 2, 3, 4, 5, 6]

// 选择器只显示到分。日期面板没动时间时，开始/结束分别按当天 00:00:00 / 23:59:59 回落，
// 避免只选一个结束日期时把当天整天的记录排除掉。
const DEFAULT_TIME = [new Date(2000, 0, 1, 0, 0, 0), new Date(2000, 0, 1, 23, 59, 59)]

const queryForm = reactive({
  name: '',
  operator: '',
  requestId: '',
  source: '',
  result: '',
})
const dateRange = ref([])

const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)

const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value
)

function sourceText(source) {
  if (!source) return '—'
  return t(`controlLog.sourceLabel.${source}`)
}

function codeText(code) {
  const key = `controlLog.codeLabel.${code}`
  const text = t(key)
  return text === key ? String(code) : text
}

async function fetchList() {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      size: pageSize.value,
    }
    if (queryForm.name.trim()) params.name = queryForm.name.trim()
    if (queryForm.operator.trim()) params.operator = queryForm.operator.trim()
    if (queryForm.requestId.trim()) params.requestId = queryForm.requestId.trim()
    if (queryForm.source) params.source = queryForm.source
    if (queryForm.result !== '') params.code = Number(queryForm.result)
    if (Array.isArray(dateRange.value) && dateRange.value.length === 2) {
      // 选择器只到分钟：后端按闭区间比较字符串，上界补到该分钟的第 59 秒
      params.startTime = `${dateRange.value[0].slice(0, 16)}:00`
      params.endTime = `${dateRange.value[1].slice(0, 16)}:59`
    }
    const res = await getControlLogPage(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.error('获取控制审计记录失败', err)
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  queryForm.operator = ''
  queryForm.requestId = ''
  queryForm.source = ''
  queryForm.result = ''
  dateRange.value = []
  currentPage.value = 1
  fetchList()
}

function handleSizeChange() {
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange() {
  fetchList()
}

onMounted(fetchList)
</script>

<style scoped>
.audit-toolbar {
  align-items: flex-start;
}

.audit-filters {
  display: flex;
  flex-wrap: wrap;
  width: 100%;
  gap: 6px;
  align-items: center;
}

/* 这一行要同时放下 6 个筛选项和「查询/重置」，宽度按占位符实测宽度分配：
   搜索 210 / 操作者 160（占位符 112px，输入区需 ≥ 112）/ 请求 ID 190 /
   两个下拉各 120 / 时间区间 260 */
.audit-filters .iot-search {
  width: 210px;
}

.audit-input {
  width: 160px;
}

.audit-input-wide {
  width: 190px;
}

.audit-select {
  width: 120px;
}

/* 动作按钮紧跟日期控件之后，不再推到行尾 */
.audit-actions {
  display: flex;
  flex: 0 0 auto;
  gap: 8px;
}

/* 只显示到分（「YYYY-MM-DD HH:mm」）。13px 字号下每段约 103px，
   把两侧输入从默认 39% 放到 44%，260px 就能完整放下两段值。
   必须套一层自己的 div：Element 的 range 触发器是 tooltip 的子节点，
   页面 scoped 样式（data-v）落不到它身上；而它的 .el-input__wrapper
   又自带 flex-grow:1，直接放进筛选行会被拉满整行。 */
.audit-date {
  width: 260px;
  max-width: 100%;
  flex: 0 0 auto;
}

.audit-date :deep(.el-date-editor) {
  width: 100%;
}

.audit-date :deep(.el-range-input),
.audit-date :deep(.el-range-separator) {
  font-size: 13px;
}

.audit-date :deep(.el-range-input) {
  width: 44%;
}

/* 前缀的时钟图标会再挤掉约 20px，收窄后两段值就放不下了 */
.audit-date :deep(.el-range__icon) {
  display: none;
}

.audit-point {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.audit-point-label {
  font-size: 11.5px;
  color: var(--ink-4);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.audit-value {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.audit-req {
  font-size: 11.5px;
  color: var(--ink-3);
}

</style>
