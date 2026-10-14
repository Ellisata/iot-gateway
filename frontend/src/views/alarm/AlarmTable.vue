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
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ pageTitle }}</h1>

    <!-- 筛选工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex flex-wrap items-center gap-3">
        <el-input
          v-model="queryForm.targetName"
          :placeholder="t('alarm.targetNamePlaceholder')"
          clearable
          style="width: 240px"
          @keyup.enter="handleSearch"
        />
        <el-select
          v-model="queryForm.status"
          :placeholder="t('alarm.alarmStatus')"
          clearable
          style="width: 150px"
        >
          <el-option :label="t('alarm.statusActive')" value="active" />
          <el-option :label="t('alarm.statusCleared')" value="cleared" />
        </el-select>
        <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
        <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
      </div>
    </el-card>

    <!-- 报警数据表格 -->
    <el-card shadow="never">
      <el-table
        :data="tableData"
        v-loading="loading"
        stripe
        border
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="targetName" :label="t('alarm.targetName')" min-width="140" show-overflow-tooltip />
        <el-table-column prop="targetType" :label="t('alarm.targetType')" width="100" align="center">
          <template #default="{ row }">
            {{ targetTypeName(row.targetType) }}
          </template>
        </el-table-column>
        <el-table-column prop="alarmTypeName" :label="t('alarm.alarmTypeCol')" width="110" align="center">
          <template #default="{ row }">
            <el-tag :type="row.alarmType === 'recover' ? 'success' : 'danger'" size="small">
              {{ row.alarmTypeName }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="level" :label="t('alarm.level')" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="levelTagType(row.level)" size="small">{{ levelName(row.level) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="content" :label="t('alarm.content')" min-width="220" show-overflow-tooltip />
        <el-table-column prop="statusName" :label="t('alarm.statusCol')" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'danger' : 'success'" size="small">
              {{ row.statusName }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="firstOccurTime" :label="t('alarm.firstOccurTime')" width="170" align="center" />
        <el-table-column prop="lastOccurTime" :label="t('alarm.lastOccurTime')" width="170" align="center" />
        <el-table-column prop="clearTime" :label="t('alarm.clearTime')" width="170" align="center" />
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
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
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

function levelTagType(level) {
  const map = {
    critical: 'danger',
    error: 'danger',
    warning: 'warning',
    info: 'info',
  }
  return map[level] || 'info'
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>
