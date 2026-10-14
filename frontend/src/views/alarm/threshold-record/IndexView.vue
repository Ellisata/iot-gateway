<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-2">{{ t('thresholdRecord.title') }}</h1>
    <!-- 常驻说明：本表与「超限报警」不是一回事，用户很容易以为报警页漏了记录 -->
    <p class="text-xs text-gray-400 leading-5 mb-6">{{ t('thresholdRecord.hint') }}</p>

    <!-- 筛选工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex flex-wrap items-center gap-3">
        <el-input
          v-model="queryForm.name"
          :placeholder="t('thresholdRecord.searchPlaceholder')"
          clearable
          style="width: 240px"
          @keyup.enter="handleSearch"
        />
        <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
        <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
      </div>
    </el-card>

    <!-- 越限记录表格 -->
    <el-card shadow="never">
      <el-table :data="tableData" v-loading="loading" stripe border style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="deviceName" :label="t('thresholdRecord.device')" width="150" show-overflow-tooltip />
        <el-table-column prop="addressName" :label="t('thresholdRecord.point')" min-width="140" show-overflow-tooltip />
        <el-table-column prop="value" :label="t('thresholdRecord.value')" width="120" align="center" />
        <el-table-column prop="boundName" :label="t('thresholdRecord.bound')" width="110" align="center">
          <template #default="{ row }">
            <el-tag :type="row.bound === 'high' ? 'danger' : 'warning'" size="small">
              {{ row.boundName }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('thresholdRecord.threshold')" width="190" align="center">
          <template #default="{ row }">
            <span class="text-xs">{{ conditionText(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="occurredAt" :label="t('thresholdRecord.occurredAt')" width="170" align="center" />
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
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
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
