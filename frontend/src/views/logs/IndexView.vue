<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6 log-page">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ t('menu.logFile') }}</h1>

    <!-- 日志文件列表 -->
    <el-card shadow="never">
      <div class="flex items-center justify-between mb-4">
        <span class="text-gray-600">{{ t('log.filesHint') }}</span>
        <el-button type="primary" :loading="loading" @click="fetchList">
          <el-icon class="mr-1"><Refresh /></el-icon>{{ t('common.refresh') }}
        </el-button>
      </div>

      <el-table :data="tableData" v-loading="loading" stripe border style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="fileName" :label="t('log.fileName')" min-width="220" show-overflow-tooltip />
        <el-table-column :label="t('log.size')" width="120" align="center">
          <template #default="{ row }">{{ formatSize(row.size) }}</template>
        </el-table-column>
        <el-table-column prop="modifyTime" :label="t('log.modifyTime')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="120" align="center" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link size="small" @click="handleView(row)">
              {{ t('log.viewLog') }}
            </el-button>
          </template>
        </el-table-column>
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

    <!-- 日志内容抽屉 -->
    <el-drawer
      v-model="drawerVisible"
      :title="drawerTitle"
      size="70%"
      destroy-on-close
    >
      <div class="flex shrink-0 items-center gap-3 bg-white px-5 py-4">
        <span class="text-sm text-gray-600">{{ t('log.showTail') }}</span>
        <el-select v-model="tailLines" style="width: 130px" @change="fetchLogContent">
          <el-option :label="t('log.lines', { n: 200 })" :value="200" />
          <el-option :label="t('log.lines', { n: 500 })" :value="500" />
          <el-option :label="t('log.lines', { n: 1000 })" :value="1000" />
          <el-option :label="t('log.lines', { n: 2000 })" :value="2000" />
        </el-select>
        <span class="text-sm text-gray-500">{{ t('log.totalLines', { total: totalLines }) }}</span>
        <el-button :loading="contentLoading" @click="scrollToBottom">
          <el-icon class="mr-1"><Bottom /></el-icon>{{ t('log.scrollToBottom') }}
        </el-button>
        <el-button type="primary" :loading="contentLoading" @click="fetchLogContent">
          <el-icon class="mr-1"><Refresh /></el-icon>{{ t('common.refresh') }}
        </el-button>
      </div>

      <div
        ref="contentRef"
        v-loading="contentLoading"
        class="flex-1 min-h-0 bg-black text-gray-100 p-5 font-mono text-sm leading-6 whitespace-pre overflow-auto"
      >
        <template v-if="logLines.length">{{ joinedLines }}</template>
        <div v-else class="text-gray-400">{{ t('log.noContent') }}</div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getLogFilePage, readLogFile } from '@/api/modules/logFile'

const { t } = useI18n()

// ---------- 文件列表（分页） ----------
const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

async function fetchList() {
  loading.value = true
  try {
    const res = await getLogFilePage({
      page: currentPage.value,
      size: pageSize.value,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.error('获取日志文件列表失败', err)
  } finally {
    loading.value = false
  }
}

function handleSizeChange(val) {
  pageSize.value = val
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange(val) {
  currentPage.value = val
  fetchList()
}

// ---------- 日志内容 ----------
const drawerVisible = ref(false)
const drawerTitle = ref('')
const currentFile = ref('')
const tailLines = ref(500)
const contentLoading = ref(false)
const logLines = ref([])
const totalLines = ref(0)
const joinedLines = computed(() => logLines.value.join('\n'))
const contentRef = ref(null)

// 最新日志在底部，加载后自动滚动到底部
function scrollToBottom() {
  nextTick(() => {
    if (contentRef.value) {
      contentRef.value.scrollTop = contentRef.value.scrollHeight
    }
  })
}

function handleView(row) {
  currentFile.value = row.fileName
  drawerTitle.value = row.fileName
  drawerVisible.value = true
  tailLines.value = 500
  fetchLogContent()
}

async function fetchLogContent() {
  if (!currentFile.value) return
  contentLoading.value = true
  try {
    const res = await readLogFile({
      fileName: currentFile.value,
      tail: tailLines.value,
    })
    logLines.value = res?.lines ?? []
    totalLines.value = res?.totalLines ?? 0
    scrollToBottom()
  } catch (err) {
    console.error('获取日志内容失败', err)
    logLines.value = []
    totalLines.value = 0
  } finally {
    contentLoading.value = false
  }
}

// ---------- 工具 ----------
function formatSize(bytes) {
  if (bytes === undefined || bytes === null) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let val = bytes
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024
    i++
  }
  return `${val.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

onMounted(() => {
  fetchList()
})
</script>

<style scoped>
/* el-drawer 的根元素由组件内部渲染，拿不到本组件的 scope id，
   直接写 .el-drawer__body 这样的类选择器不会生效，
   必须挂在页面根节点上用 :deep() 才能命中。
   去掉内容区自带的 20px 内边距并改成纵向弹性布局，
   让工具条（白底）和日志区（黑底）各自撑满，日志区不留白边。 */
.log-page :deep(.el-drawer__body) {
  padding: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
</style>
