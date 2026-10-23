<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page log-page">
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.log') }}
        </p>
        <h1 class="iot-title">{{ t('menu.logFile') }}</h1>
        <p class="iot-sub">{{ t('log.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('log.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：文件列表、分页与刷新共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-lead">
          <span class="iot-mark" aria-hidden="true">
            <el-icon><FileText /></el-icon>
          </span>
          <div class="iot-toolbar-copy">
            <p class="iot-toolbar-title">{{ t('log.filesTitle') }}</p>
            <p class="iot-toolbar-sub">{{ t('log.filesHint') }}</p>
          </div>
        </div>
        <div class="iot-toolbar-actions">
          <el-button type="primary" :icon="Refresh" :loading="loading" @click="fetchList">
            {{ t('common.refresh') }}
          </el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />
        <el-table-column :label="t('log.fileName')" min-width="280" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><FileText /></el-icon>
              </span>
              <span class="iot-name-text log-name" :title="row.fileName">{{ row.fileName }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('log.size')" width="120" align="right" class-name="iot-col-num">
          <template #default="{ row }">{{ formatSize(row.size) }}</template>
        </el-table-column>
        <el-table-column
          prop="modifyTime"
          :label="t('log.modifyTime')"
          width="170"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="120" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('log.viewLog')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Eye"
                  :aria-label="t('log.viewLog')"
                  @click="handleView(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('log.downloadLog')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Download"
                  :loading="downloadingFile === row.fileName"
                  :aria-label="t('log.downloadLog')"
                  @click="handleDownload(row)"
                />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><FileText /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('log.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('log.emptyHint') }}</p>
            <el-button :icon="Refresh" :loading="loading" @click="fetchList">
              {{ t('common.refresh') }}
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

    <!-- 日志内容抽屉 -->
    <el-drawer
      v-model="drawerVisible"
      :title="drawerTitle"
      size="70%"
      destroy-on-close
    >
      <div class="log-toolbar">
        <span class="log-control-label">{{ t('log.showTail') }}</span>
        <el-select
          v-model="tailLines"
          class="log-lines"
          @change="fetchLogContent"
        >
          <el-option :label="t('log.lines', { n: 200 })" :value="200" />
          <el-option :label="t('log.lines', { n: 500 })" :value="500" />
          <el-option :label="t('log.lines', { n: 1000 })" :value="1000" />
          <el-option :label="t('log.lines', { n: 2000 })" :value="2000" />
        </el-select>
        <span class="log-total">{{ t('log.totalLines', { total: totalLines }) }}</span>
        <div class="log-toolbar-actions">
          <el-button
            :icon="Download"
            :loading="downloadingFile === currentFile"
            @click="handleDownload({ fileName: currentFile })"
          >
            {{ t('log.downloadLog') }}
          </el-button>
          <el-button :icon="Bottom" :loading="contentLoading" @click="scrollToBottom">
            {{ t('log.scrollToBottom') }}
          </el-button>
          <el-button
            type="primary"
            :icon="Refresh"
            :loading="contentLoading"
            @click="fetchLogContent"
          >
            {{ t('common.refresh') }}
          </el-button>
        </div>
      </div>

      <div
        ref="contentRef"
        v-loading="contentLoading"
        element-loading-background="rgba(11, 18, 32, 0.65)"
        class="log-output"
      >
        <template v-if="logLines.length">{{ joinedLines }}</template>
        <div v-else class="log-empty">{{ t('log.noContent') }}</div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { downloadLogFile, getLogFilePage, readLogFile } from '@/api/modules/logFile'
import {
  IconArrowBarToDown as Bottom,
  IconDownload as Download,
  IconEye as Eye,
  IconFileText as FileText,
  IconRefresh as Refresh,
} from '@tabler/icons-vue'

const { t } = useI18n()

// ---------- 文件列表（分页） ----------
const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 首屏还没拿到数时显示占位符而不是 0：0 个文件会被当成"日志目录是空的"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value
)

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

// ---------- 下载 ----------
// 记录正在下载的文件名，行内按钮与抽屉按钮共用（同一文件时都会转圈）
const downloadingFile = ref('')

async function handleDownload(row) {
  const fileName = row?.fileName
  if (!fileName || downloadingFile.value) return
  downloadingFile.value = fileName
  try {
    const blob = await downloadLogFile({ fileName })
    // 文件刚被轮转删除等业务错误仍走 JSON 信封（HTTP 200），blob 类型可据此识别；
    // 不拦的话用户会下到一个内容是 JSON 的 .log 文件
    if (blob.type?.includes('application/json')) {
      let msg = ''
      try {
        msg = JSON.parse(await blob.text())?.msg
      } catch {
        // 解析失败走兜底文案
      }
      ElMessage.error(msg || t('log.downloadFailed'))
      return
    }
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = fileName
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    ElMessage.success(t('log.downloadSuccess'))
  } catch (err) {
    // 网络/HTTP 层的错误提示已由 axios 拦截器统一处理
    console.error('下载日志失败', err)
  } finally {
    downloadingFile.value = ''
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
/* 日志文件名是技术标识，等宽字体让大小写与扩展名对齐 */
.log-name {
  font-family: var(--font-mono);
  font-size: 12.5px;
}

/* el-drawer 的根元素由组件内部渲染，拿不到本组件的 scope id，
   必须挂在页面根节点上用 :deep() 才能命中。
   去掉内容区自带的 20px 内边距并改成纵向弹性布局，
   让工具条（白底）和日志区（深色）各自撑满，日志区不留白边。 */
.log-page :deep(.el-drawer) {
  border-radius: 18px 0 0 18px;
}

.log-page :deep(.el-drawer__header) {
  margin-bottom: 0;
  padding: 16px 20px;
  border-bottom: 1px solid var(--line-soft);
}

.log-page :deep(.el-drawer__title) {
  font-size: 15px;
  font-weight: 650;
  color: var(--ink);
}

.log-page :deep(.el-drawer__body) {
  padding: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.log-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 12px;
  flex-shrink: 0;
  padding: 12px 20px;
  background: #fff;
  border-bottom: 1px solid var(--line-soft);
}

.log-control-label,
.log-total {
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}

.log-total {
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

.log-lines {
  width: 130px;
}

.log-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-left: auto;
}

.log-toolbar :deep(.el-select__wrapper) {
  min-height: 34px;
  border-radius: 9px;
  box-shadow: 0 0 0 1px var(--line) inset;
}

.log-toolbar :deep(.el-button) {
  height: 34px;
  border-radius: 9px;
  font-weight: 500;
}

/* 日志正文保持深色终端观感：它是唯一需要逐行读的长文本区域 */
.log-output {
  flex: 1;
  min-height: 0;
  padding: 18px 20px;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.75;
  color: #dbe4f0;
  white-space: pre;
  background: #0b1220;
}

.log-output::-webkit-scrollbar-thumb {
  background-color: #334155;
}

.log-empty {
  color: #7b8aa3;
}

@media (max-width: 640px) {
  .log-toolbar-actions {
    width: 100%;
    margin-left: 0;
    justify-content: flex-end;
  }
}
</style>
