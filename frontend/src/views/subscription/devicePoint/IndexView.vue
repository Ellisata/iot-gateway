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
          {{ t('menu.subscription') }}
        </p>
        <h1 class="iot-title">{{ t('menu.subscriptionDevicePoint') }}</h1>
        <p class="iot-sub">{{ t('subscriptionDevicePoint.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('subscriptionDevicePoint.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：五个筛选条件、表格与分页共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters point-filters">
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceId') }}</span>
            <el-input
              v-model="query.deviceId"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceName') }}</span>
            <el-input
              v-model="query.deviceName"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceAddressId') }}</span>
            <el-input
              v-model="query.deviceAddressId"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field">
            <span class="filter-label">{{ t('subscription.fieldDeviceAddressName') }}</span>
            <el-input
              v-model="query.deviceAddressName"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-field is-short">
            <span class="filter-label">{{ t('subscription.fieldProtocol') }}</span>
            <el-input
              v-model="query.protocol"
              clearable
              class="filter-input"
              @keyup.enter="handleSearch"
            />
          </div>
          <div class="filter-actions">
            <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
            <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
          </div>
        </div>
      </div>

      <el-table class="iot-table" :data="rows" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column
          :label="t('subscription.fieldDeviceId')"
          min-width="130"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="iot-mono">{{ row.deviceId }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('subscription.fieldDeviceName')"
          min-width="170"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><DeviceDesktop /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.deviceName">{{ row.deviceName || '—' }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('subscription.fieldDeviceAddressId')"
          min-width="130"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="iot-mono">{{ row.deviceAddressId }}</span>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('subscription.fieldDeviceAddressName')"
          min-width="150"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('subscription.fieldDeviceAddressLabel')" min-width="130" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.deviceAddressLabel" class="iot-chip">{{ row.deviceAddressLabel }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.fieldDataType')" width="120" align="center">
          <template #default="{ row }">
            <span v-if="row.dataType" class="iot-chip">{{ row.dataType }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.fieldKind')" width="100" align="center">
          <template #default="{ row }">
            <span v-if="row.kind" class="iot-chip">{{ row.kind }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.fieldProtocol')" min-width="130" show-overflow-tooltip>
          <template #default="{ row }">
            <span v-if="row.protocol" class="iot-chip">{{ row.protocol }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="firstSeenAt"
          :label="t('subscriptionDevicePoint.firstSeenAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column
          prop="lastSeenAt"
          :label="t('subscriptionDevicePoint.lastSeenAt')"
          width="160"
          class-name="iot-col-time"
        />

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Point /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('subscriptionDevicePoint.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('subscriptionDevicePoint.emptyHint') }}</p>
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
import { reactive, ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  IconDeviceDesktop as DeviceDesktop,
  IconPoint as Point,
} from '@tabler/icons-vue'
import { getSubscriptionDevicePointList } from '@/api/modules/mqttSubscription'

const { t } = useI18n()

const loading = ref(false)
const rows = ref([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const query = reactive({
  deviceId: '',
  deviceName: '',
  deviceAddressId: '',
  deviceAddressName: '',
  protocol: '',
})

// 首屏还没拿到数时显示占位符而不是 0：0 个点位会被当成"还没收到任何订阅报文"
const displayTotal = computed(() =>
  loading.value && rows.value.length === 0 ? '—' : total.value
)

async function fetchData() {
  loading.value = true
  try {
    const params = { page: currentPage.value, size: pageSize.value }
    for (const key of Object.keys(query)) {
      const value = String(query[key] ?? '').trim()
      if (value) params[key] = value
    }
    const res = await getSubscriptionDevicePointList(params)
    rows.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
    console.error('获取订阅设备与点位失败', err)
    ElMessage.error(t('subscriptionDevicePoint.fetchFailed'))
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchData()
}

function handleReset() {
  Object.keys(query).forEach((key) => {
    query[key] = ''
  })
  handleSearch()
}

function handleSizeChange() {
  currentPage.value = 1
  fetchData()
}

function handleCurrentChange() {
  fetchData()
}

onMounted(fetchData)
</script>

<style scoped>
/* 五个筛选条件：标签在上、控件在下，工具栏自动换行；按钮组贴底对齐 */
.point-filters {
  align-items: flex-end;
  width: 100%;
}

.filter-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.filter-label {
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  color: var(--ink-4);
  white-space: nowrap;
}

.filter-input {
  width: 164px;
}

.filter-field.is-short .filter-input {
  width: 134px;
}

.filter-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

@media (max-width: 900px) {
  .filter-input,
  .filter-field.is-short .filter-input {
    width: 100%;
  }

  .filter-field {
    flex: 1 1 160px;
  }
}
</style>
