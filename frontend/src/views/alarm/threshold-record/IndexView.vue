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
          {{ t('menu.alarm') }}
        </p>
        <h1 class="iot-title">{{ t('thresholdRecord.title') }}</h1>
        <!-- 常驻说明：本表与「超限报警」不是一回事，用户很容易以为报警页漏了记录 -->
        <p class="iot-sub">{{ t('thresholdRecord.hint') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('thresholdRecord.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('thresholdRecord.searchPlaceholder')"
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
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column :label="t('thresholdRecord.device')" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><DeviceDesktop /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.deviceName">{{ row.deviceName || '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('thresholdRecord.point')" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="iot-mono">{{ row.addressName }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thresholdRecord.value')" width="120" align="right">
          <template #default="{ row }">
            <span class="record-value">{{ row.value }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thresholdRecord.bound')" width="120" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.bound === 'high' ? 'is-rose' : 'is-amber'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.boundName }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thresholdRecord.threshold')" width="190" align="center">
          <template #default="{ row }">
            <span class="record-cond">{{ conditionText(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="occurredAt"
          :label="t('thresholdRecord.occurredAt')"
          width="170"
          class-name="iot-col-time"
        />

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Clock /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('thresholdRecord.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('thresholdRecord.emptyHint') }}</p>
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
  IconClock as Clock,
  IconDeviceDesktop as DeviceDesktop,
  IconSearch as Search,
} from '@tabler/icons-vue'
import { getThresholdRecordPage } from '@/api/modules/alarm'

const { t } = useI18n()

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

// 首屏还没拿到数时显示占位符而不是 0
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value
)

// ---------- 获取列表（分页） ----------
async function fetchList() {
  loading.value = true
  try {
    const res = await getThresholdRecordPage({
      page: currentPage.value,
      size: pageSize.value,
      name: queryForm.name || undefined,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.error('获取越限记录失败', err)
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
  handleSearch()
}

function handleSizeChange() {
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange() {
  fetchList()
}

// 条件快照：显示的是**写下这条记录时**生效的条件 —— 运维之后改了配置，
// 历史记录仍按当时的口径自证，这正是把条件存成快照而不是外键的原因。
function conditionText(row) {
  const values = parseValues(row.thresholdValue)
  switch (row.thresholdOp) {
    case 'range':
      return `∈ [${values[0] || '−∞'}, ${values[1] || '+∞'}]`
    case 'gt':
      return `> ${values[0] ?? ''}`
    case 'ge':
      return `≥ ${values[0] ?? ''}`
    case 'lt':
      return `< ${values[0] ?? ''}`
    case 'le':
      return `≤ ${values[0] ?? ''}`
    case 'eq':
      return `= ${values[0] ?? ''}`
    case 'ne':
      return `≠ ${values[0] ?? ''}`
    case 'in':
      return `∈ {${values.join(', ')}}`
  }
  return ''
}

function parseValues(raw) {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.map((v) => String(v)) : []
  } catch {
    return []
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>

<style scoped>
.record-value {
  font-family: var(--font-mono);
  font-size: 13px;
  font-weight: 600;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

/* 条件快照是技术口径，用等宽字体与地址列保持同一家族 */
.record-cond {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}
</style>
