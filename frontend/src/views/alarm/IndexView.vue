<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ pageTitle }}</h1>

    <!-- 筛选工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex flex-wrap items-center gap-3">
        <el-input
          v-model="queryForm.targetName"
          placeholder="请输入目标名称搜索"
          clearable
          style="width: 240px"
          @keyup.enter="handleSearch"
        />
        <el-select
          v-model="queryForm.alarmType"
          placeholder="报警类型"
          clearable
          style="width: 150px"
        >
          <el-option label="断联报警" value="offline" />
          <el-option label="恢复记录" value="recover" />
        </el-select>
        <el-select
          v-model="queryForm.status"
          placeholder="报警状态"
          clearable
          style="width: 150px"
        >
          <el-option label="未恢复" value="active" />
          <el-option label="已恢复" value="cleared" />
        </el-select>
        <el-button type="primary" @click="handleSearch">查询</el-button>
        <el-button @click="handleReset">重置</el-button>
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
        <el-table-column type="index" label="序号" width="70" align="center" />
        <el-table-column prop="targetName" label="目标名称" min-width="140" show-overflow-tooltip />
        <el-table-column prop="targetType" label="目标类型" width="100" align="center">
          <template #default="{ row }">
            {{ row.targetType === 'device' ? '设备' : '通道' }}
          </template>
        </el-table-column>
        <el-table-column prop="alarmTypeName" label="报警类型" width="110" align="center">
          <template #default="{ row }">
            <el-tag :type="row.alarmType === 'offline' ? 'danger' : 'success'" size="small">
              {{ row.alarmTypeName }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="level" label="级别" width="90" align="center">
          <template #default="{ row }">
            <el-tag :type="levelTagType(row.level)" size="small">{{ levelName(row.level) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="content" label="报警内容" min-width="220" show-overflow-tooltip />
        <el-table-column prop="statusName" label="状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'danger' : 'success'" size="small">
              {{ row.statusName }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="firstOccurTime" label="首次发生时间" width="170" align="center" />
        <el-table-column prop="lastOccurTime" label="最近发生时间" width="170" align="center" />
        <el-table-column prop="clearTime" label="恢复时间" width="170" align="center" />
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
import { ref, reactive, computed, onMounted, watch } from 'vue'
import { getAlarmPage } from '@/api/modules/alarm'

// 通过路由 props 注入目标类型：device | channel
const props = defineProps({
  targetType: {
    type: String,
    default: '',
  },
})

const pageTitle = computed(() =>
  props.targetType === 'device' ? '设备报警' : '通道报警'
)

// ---------- 查询表单 ----------
const queryForm = reactive({
  targetName: '',
  alarmType: '',
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
    }
    if (props.targetType) params.targetType = props.targetType
    if (queryForm.targetName) params.targetName = queryForm.targetName
    if (queryForm.alarmType) params.alarmType = queryForm.alarmType
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
  queryForm.alarmType = ''
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
    critical: '严重',
    error: '错误',
    warning: '警告',
    info: '信息',
  }
  return map[level] || level || '—'
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

// 设备报警 / 通道报警复用同一组件，切换路由时 props.targetType 变化，需重新拉取对应类型的数据
watch(
  () => props.targetType,
  () => {
    currentPage.value = 1
    fetchList()
  }
)
</script>
