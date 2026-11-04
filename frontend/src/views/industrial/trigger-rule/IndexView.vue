<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  事件触发采集规则列表。

  规则在网关侧判断多条件（组间 OR、组内 AND），条件上升沿时异步读取动作点位，
  再推送到一个指定通道。这里只做配置与历史入口；判定/执行在 gateway 的
  triggerrule 引擎里。
-->
<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ t('triggerRule.title') }}</h1>
        <p class="iot-sub">{{ t('triggerRule.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ total }}</span>
          <span class="iot-stat-label">{{ t('triggerRule.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('triggerRule.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-input v-model="queryForm.code" class="iot-search" style="width: 180px"
            :placeholder="t('triggerRule.searchCodePlaceholder')" clearable
            @keyup.enter="handleSearch" />
          <el-select v-model="queryForm.status" style="width: 140px" clearable
            :placeholder="t('triggerRule.statusFilter')" @change="handleSearch">
            <el-option :label="t('common.enabled')" :value="1" />
            <el-option :label="t('common.enableDisable.off')" :value="0" />
          </el-select>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <el-button :icon="Eye" @click="liveVisible = true">{{ t('triggerRule.live') }}</el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('triggerRule.add') }}</el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" row-key="id" style="width: 100%">
        <el-table-column :label="t('triggerRule.name')" min-width="180">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Bolt /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="code" :label="t('triggerRule.code')" width="150"
          class-name="iot-code-text" />
        <el-table-column :label="t('triggerRule.actionDevice')" min-width="140">
          <template #default="{ row }">{{ row.actionDeviceName || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.conditionCount')" width="110" align="center">
          <template #default="{ row }">{{ row.conditionCount ?? 0 }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.actionAddresses')" width="110" align="center">
          <template #default="{ row }">{{ row.actionAddresses?.length ?? 0 }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.targetChannel')" min-width="150">
          <template #default="{ row }">{{ row.targetChannelName || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.cooldown')" width="120" align="center">
          <template #default="{ row }">{{ row.cooldownMs ?? 0 }} ms</template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-rose'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="updatedAt" :label="t('common.updatedAt')" width="160"
          class-name="iot-col-time" />
        <el-table-column :label="t('common.action')" width="150" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('triggerRule.records')" placement="top">
                <el-button class="iot-row-btn" type="primary" link size="small" :icon="History"
                  :aria-label="t('triggerRule.records')" @click="goRecords(row)" />
              </el-tooltip>
              <el-tooltip :content="t('common.edit')" placement="top">
                <el-button class="iot-row-btn" type="primary" link size="small" :icon="EditPen"
                  :aria-label="t('common.edit')" @click="handleEdit(row)" />
              </el-tooltip>
              <el-tooltip :content="t('common.delete')" placement="top">
                <el-button class="iot-row-btn" type="danger" link size="small" :icon="Delete"
                  :aria-label="t('common.delete')" @click="handleDelete(row)" />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Bolt /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('triggerRule.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('triggerRule.emptyHint') }}</p>
            <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('triggerRule.add') }}</el-button>
          </div>
        </template>
      </el-table>

      <div class="iot-pager">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="PAGE_SIZES"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @size-change="handleSizeChange"
          @current-change="handlePageChange"
        />
      </div>
    </section>

    <!--
      实时监控：只显示规则引擎内存里的最近 100 次触发（重启即清空）。
      与「触发历史」互补 —— 那里是落库的分页快照，这里是"刚刚发生了什么"。
    -->
    <el-drawer
      v-model="liveVisible"
      :title="t('triggerRule.liveTitle')"
      size="min(960px, 94vw)"
      class="trigger-live-drawer"
      @open="handleLiveOpen"
      @closed="handleLiveClosed"
    >
      <div class="live-head">
        <div>
          <p class="live-sub">{{ t('triggerRule.liveSubtitle', { n: 100 }) }}</p>
          <p class="live-hint">{{ t('triggerRule.liveMemoryHint') }}</p>
        </div>
        <div class="live-actions">
          <el-select v-model="liveRuleID" clearable style="width: 200px"
            :placeholder="t('triggerRule.liveRuleFilter')" @change="fetchLive(true)">
            <el-option v-for="r in liveRules" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
          <el-button :icon="Refresh" :loading="liveLoading" @click="fetchLive(true)">
            {{ t('common.refresh') }}
          </el-button>
        </div>
      </div>

      <el-table class="iot-table live-table" :data="liveEvents" v-loading="liveLoading"
        row-key="seq" style="width: 100%">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="live-records">
              <div class="live-records-title">{{ t('triggerRule.liveRecordsTitle') }}</div>
              <el-table v-if="row.records?.length" :data="row.records" size="small"
                class="live-records-table">
                <el-table-column :label="t('triggerRule.livePoint')" min-width="160">
                  <template #default="{ row: rec }">
                    {{ rec.addressName || rec.addressId || '—' }}
                  </template>
                </el-table-column>
                <el-table-column prop="value" :label="t('triggerRule.liveValue')"
                  min-width="140" class-name="iot-code-text" />
                <el-table-column prop="kind" :label="t('triggerRule.liveKind')" width="120"
                  class-name="iot-col-muted" />
                <el-table-column prop="quality" :label="t('triggerRule.liveQuality')"
                  width="100" align="center" class-name="iot-col-muted" />
              </el-table>
              <p v-else class="live-empty">{{ t('triggerRule.liveRecordsEmpty') }}</p>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="firedAt" :label="t('triggerRuleRecord.firedAt')" width="176"
          class-name="iot-col-time" />
        <el-table-column :label="t('triggerRuleRecord.ruleName')" min-width="160">
          <template #default="{ row }">
            <div class="live-rule-cell">
              <span class="live-rule-name">{{ row.ruleName || '—' }}</span>
              <span v-if="row.ruleCode" class="live-rule-code">{{ row.ruleCode }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('triggerRuleRecord.status')" width="120" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="statusClass(row.status)">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ statusLabel(row.status) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.targetChannel')" min-width="140">
          <template #default="{ row }">{{ row.channelName || row.channelId || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.actionDevice')" min-width="120">
          <template #default="{ row }">{{ row.actionDeviceName || row.actionDeviceId || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRule.liveRecords')" width="80" align="center">
          <template #default="{ row }">{{ row.records?.length ?? 0 }}</template>
        </el-table-column>
        <el-table-column :label="t('triggerRuleRecord.duration')" width="100" align="right">
          <template #default="{ row }">{{ row.durationMs ?? 0 }} ms</template>
        </el-table-column>
        <el-table-column prop="error" :label="t('triggerRuleRecord.error')" min-width="160"
          show-overflow-tooltip class-name="iot-col-muted" />

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Bolt /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('triggerRule.liveEmptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('triggerRule.liveEmptyHint') }}</p>
          </div>
        </template>
      </el-table>
    </el-drawer>

    <TriggerRuleFormDialog v-model="dialogVisible" :rule-id="editingId" @saved="fetchList" />
  </div>
</template>

<script setup>
import { onBeforeUnmount, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconBolt as Bolt,
  IconEye as Eye,
  IconHistory as History,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconRefresh as Refresh,
  IconSearch as Search,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'
import { deleteTriggerRule, getRecentTriggers, pageTriggerRules } from '@/api/modules/triggerRule'
import TriggerRuleFormDialog from './TriggerRuleFormDialog.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

// 列表状态（页码/每页条数/已生效筛选）同步到 URL query：行内进入「触发历史」
// 再返回时，浏览器回退能恢复现场，而不是重置回第 1 页、丢掉筛选。
const PAGE_SIZES = [10, 20, 50, 100]
const DEFAULT_PAGE_SIZE = 20

const initialPage = Number(route.query.page)
const initialSize = Number(route.query.size)
const initialStatus = route.query.status
const initialAppliedStatus =
  initialStatus === '0' || initialStatus === '1' ? Number(initialStatus) : ''

const loading = ref(false)
const tableData = ref([])
const total = ref(0)
const currentPage = ref(Number.isInteger(initialPage) && initialPage > 0 ? initialPage : 1)
const pageSize = ref(PAGE_SIZES.includes(initialSize) ? initialSize : DEFAULT_PAGE_SIZE)
const queryForm = reactive({
  code: String(route.query.code || ''),
  name: String(route.query.name || ''),
  status: initialAppliedStatus,
})
const appliedCode = ref(String(route.query.code || ''))
const appliedName = ref(String(route.query.name || ''))
const appliedStatus = ref(initialAppliedStatus)

const dialogVisible = ref(false)
const editingId = ref('')

// ---------- 实时监控（/triggerRule/recentTriggers，仅内存，3 秒轮询） ----------
const liveVisible = ref(false)
const liveLoading = ref(false)
const liveEvents = ref([])
const liveRules = ref([])
const liveRuleID = ref('')
let liveTimer = null

const STATUS_KEYS = {
  success: 'triggerRuleRecord.statusSuccess',
  offline: 'triggerRuleRecord.statusOffline',
  timeout: 'triggerRuleRecord.statusTimeout',
  read_failed: 'triggerRuleRecord.statusReadFailed',
  push_failed: 'triggerRuleRecord.statusPushFailed',
  dropped: 'triggerRuleRecord.statusDropped',
}

function statusLabel(status) {
  const key = STATUS_KEYS[status]
  return key ? t(key) : status
}

function statusClass(status) {
  if (status === 'success') return 'is-green'
  if (status === 'offline' || status === 'timeout') return 'is-amber'
  return 'is-rose'
}

async function fetchLive(showLoading = false) {
  if (showLoading) liveLoading.value = true
  try {
    const res = await getRecentTriggers({
      limit: 100,
      ruleId: liveRuleID.value || undefined,
    })
    liveEvents.value = Array.isArray(res) ? res : []
  } catch {
    // 错误提示由响应拦截器统一给出；保留上一帧，避免轮询失败时清空画面
  } finally {
    if (showLoading) liveLoading.value = false
  }
}

function handleLiveOpen() {
  liveRuleID.value = ''
  fetchLive(true)
  // 过滤下拉独立于列表分页/筛选：打开监控时取前 100 条规则。
  pageTriggerRules({ page: 1, size: 100 })
    .then((res) => { liveRules.value = res?.records ?? [] })
    .catch(() => {})
  stopLiveTimer()
  liveTimer = setInterval(() => fetchLive(false), 3000)
}

function handleLiveClosed() {
  stopLiveTimer()
}

function stopLiveTimer() {
  if (liveTimer) {
    clearInterval(liveTimer)
    liveTimer = null
  }
}

onBeforeUnmount(stopLiveTimer)

// 把当前列表状态写回 URL（仅在变化时 replace，避免无意义的历史项）
function syncQuery() {
  const query = {}
  if (currentPage.value > 1) query.page = String(currentPage.value)
  if (pageSize.value !== DEFAULT_PAGE_SIZE) query.size = String(pageSize.value)
  if (appliedCode.value) query.code = appliedCode.value
  if (appliedName.value) query.name = appliedName.value
  if (appliedStatus.value !== '') query.status = String(appliedStatus.value)

  const sameKeys = Object.keys(query).length === Object.keys(route.query).length
  const sameValues = Object.keys(query).every((key) => route.query[key] === query[key])
  if (!sameKeys || !sameValues) router.replace({ query })
}

async function fetchList() {
  syncQuery()
  loading.value = true
  try {
    const res = await pageTriggerRules({
      page: currentPage.value,
      size: pageSize.value,
      code: appliedCode.value || undefined,
      name: appliedName.value || undefined,
      status: appliedStatus.value === '' ? undefined : appliedStatus.value,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 错误提示由响应拦截器统一给出
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  appliedCode.value = queryForm.code.trim()
  appliedName.value = queryForm.name.trim()
  appliedStatus.value = queryForm.status === '' || queryForm.status === null ? '' : queryForm.status
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.code = ''
  queryForm.name = ''
  queryForm.status = ''
  appliedCode.value = ''
  appliedName.value = ''
  appliedStatus.value = ''
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

function handleAdd() {
  editingId.value = ''
  dialogVisible.value = true
}

function handleEdit(row) {
  editingId.value = row.id
  dialogVisible.value = true
}

function goRecords(row) {
  router.push({
    name: 'GatewayTriggerRuleRecord',
    query: { ruleId: row.id, ruleName: row.name },
  })
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('triggerRule.deleteConfirm', { name: row.name }),
      t('triggerRule.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
    await deleteTriggerRule(row.id)
    ElMessage.success(t('triggerRule.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消，或服务端拒绝 —— 原因由拦截器提示
  }
}

fetchList()
</script>

<style scoped>
.live-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}
.live-sub {
  margin: 0 0 4px;
  font-size: 13px;
  color: var(--ink-2);
}
.live-hint {
  margin: 0;
  font-size: 12px;
  color: var(--ink-3);
}
.live-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.live-rule-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.live-rule-name {
  color: var(--ink-1);
}
.live-rule-code {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--ink-3);
}
.live-records {
  padding: 8px 16px 16px;
}
.live-records-title {
  margin-bottom: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--ink-3);
}
.live-records-table {
  width: 100%;
}
.live-empty {
  margin: 0;
  font-size: 12px;
  color: var(--ink-3);
}
</style>
