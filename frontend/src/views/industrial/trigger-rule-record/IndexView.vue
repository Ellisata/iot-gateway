<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  事件触发历史。每条记录是规则的一次上升沿：
  success 只表示已进入通道 outbox 队列；offline/read_failed/push_failed/dropped
  分别对应设备离线、读取失败、推送失败与队列满丢弃。
  条件与结果都是触发时刻的快照，规则改名/删除后仍可自证。
-->
<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <!-- hidden 路由没有菜单项：这条链接是回到规则列表的主入口 -->
        <button type="button" class="iot-back" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          {{ t('triggerRuleRecord.backToList') }}
        </button>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ t('triggerRuleRecord.title') }}</h1>
        <p class="iot-sub">{{ t('triggerRuleRecord.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ total }}</span>
          <span class="iot-stat-label">{{ t('triggerRuleRecord.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input v-model="queryForm.ruleName" class="iot-search"
            :placeholder="t('triggerRuleRecord.searchPlaceholder')" clearable
            @keyup.enter="handleSearch">
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-select v-model="queryForm.status" style="width: 150px" clearable
            :placeholder="t('triggerRuleRecord.status')">
            <el-option v-for="s in STATUSES" :key="s" :label="statusLabel(s)" :value="s" />
          </el-select>
          <el-date-picker
            v-model="queryForm.timeRange"
            type="datetimerange"
            value-format="YYYY-MM-DD HH:mm:ss"
            :range-separator="t('triggerRuleRecord.to')"
            :start-placeholder="t('triggerRuleRecord.startTime')"
            :end-placeholder="t('triggerRuleRecord.endTime')"
            style="width: 360px"
          />
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" row-key="id" style="width: 100%">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="snapshot-wrap">
              <div class="snapshot-block">
                <div class="snapshot-title">{{ t('triggerRuleRecord.conditionSnapshot') }}</div>
                <pre class="snapshot-json">{{ pretty(row.conditionJson) }}</pre>
              </div>
              <div class="snapshot-block">
                <div class="snapshot-title">{{ t('triggerRuleRecord.actionSnapshot') }}</div>
                <pre class="snapshot-json">{{ pretty(row.actionResultJson) }}</pre>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="firedAt" :label="t('triggerRuleRecord.firedAt')" width="170"
          class-name="iot-col-time" />
        <el-table-column prop="ruleName" :label="t('triggerRuleRecord.ruleName')" min-width="160" />
        <el-table-column prop="ruleCode" :label="t('triggerRuleRecord.ruleCode')" width="150"
          class-name="iot-code-text" />
        <el-table-column :label="t('triggerRuleRecord.status')" width="130" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="statusClass(row.status)">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ statusLabel(row.status) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="durationMs" :label="t('triggerRuleRecord.duration')" width="120"
          align="center">
          <template #default="{ row }">{{ row.durationMs ?? 0 }} ms</template>
        </el-table-column>
        <el-table-column prop="error" :label="t('triggerRuleRecord.error')" min-width="200"
          show-overflow-tooltip class-name="iot-col-muted" />
        <el-table-column prop="triggerId" :label="t('triggerRuleRecord.triggerId')" width="180"
          class-name="iot-code-text" />

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><History /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('triggerRuleRecord.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('triggerRuleRecord.emptyHint') }}</p>
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
          @current-change="handlePageChange"
        />
      </div>
    </section>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  IconArrowLeft as ArrowLeft,
  IconHistory as History,
  IconSearch as Search,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'
import { pageTriggerRuleRecords } from '@/api/modules/triggerRule'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const STATUSES = ['success', 'offline', 'timeout', 'read_failed', 'push_failed', 'dropped']

const loading = ref(false)
const tableData = ref([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const queryForm = reactive({
  ruleName: route.query.ruleName || '',
  status: '',
  timeRange: [],
})
const applied = reactive({
  ruleId: route.query.ruleId || '',
  ruleName: route.query.ruleName || '',
  status: '',
  startTime: '',
  endTime: '',
})

async function fetchList() {
  loading.value = true
  try {
    const res = await pageTriggerRuleRecords({
      page: currentPage.value,
      size: pageSize.value,
      ruleId: applied.ruleId || undefined,
      ruleName: applied.ruleName || undefined,
      status: applied.status || undefined,
      startTime: applied.startTime || undefined,
      endTime: applied.endTime || undefined,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 错误提示由响应拦截器统一给出
  } finally {
    loading.value = false
  }
}

// 有历史栈就原路返回，列表页 URL 已带上页码/筛选，回去能恢复现场；
// 直接打开或刷新本页（无上一页）时兜底跳到规则列表。
function goBack() {
  if (window.history.state?.back) router.back()
  else router.push({ name: 'GatewayTriggerRule' })
}

function handleSearch() {
  applied.ruleName = queryForm.ruleName.trim()
  applied.status = queryForm.status || ''
  applied.startTime = queryForm.timeRange?.[0] || ''
  applied.endTime = queryForm.timeRange?.[1] || ''
  // 一旦按规则名/状态/时间搜索，就不再限定打开页面时的 ruleId，
  // 否则用户会以为"搜不到"——实际是被路由参数钉死了。
  applied.ruleId = ''
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.ruleName = ''
  queryForm.status = ''
  queryForm.timeRange = []
  applied.ruleName = ''
  applied.status = ''
  applied.startTime = ''
  applied.endTime = ''
  applied.ruleId = ''
  currentPage.value = 1
  fetchList()
}

function handlePageChange() {
  fetchList()
}

function handleSizeChange() {
  currentPage.value = 1
  fetchList()
}

function statusLabel(s) {
  const key = {
    success: 'triggerRuleRecord.statusSuccess',
    offline: 'triggerRuleRecord.statusOffline',
    timeout: 'triggerRuleRecord.statusTimeout',
    read_failed: 'triggerRuleRecord.statusReadFailed',
    push_failed: 'triggerRuleRecord.statusPushFailed',
    dropped: 'triggerRuleRecord.statusDropped',
  }[s]
  return key ? t(key) : s
}

function statusClass(s) {
  if (s === 'success') return 'is-green'
  if (s === 'offline' || s === 'timeout') return 'is-amber'
  return 'is-rose'
}

function pretty(raw) {
  if (!raw) return '—'
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

fetchList()
</script>

<style scoped>
.snapshot-wrap {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
  padding: 8px 16px 16px;
}
.snapshot-title {
  margin-bottom: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-3);
}
.snapshot-json {
  margin: 0;
  max-height: 280px;
  overflow: auto;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--surface-2);
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
