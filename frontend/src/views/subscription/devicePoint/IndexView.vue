<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <div class="mb-6">
      <h1 class="text-2xl font-bold text-gray-800">{{ t('menu.subscriptionDevicePoint') }}</h1>
      <p class="text-sm text-gray-500 mt-1">{{ t('subscriptionDevicePoint.subtitle') }}</p>
    </div>

    <!-- 查询条件 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex flex-wrap items-end gap-3">
        <div class="flex flex-col gap-1">
          <span class="text-xs text-gray-500">{{ t('subscription.fieldDeviceId') }}</span>
          <el-input
            v-model="query.deviceId"
            clearable
            style="width: 170px"
            @keyup.enter="handleSearch"
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-gray-500">{{ t('subscription.fieldDeviceName') }}</span>
          <el-input
            v-model="query.deviceName"
            clearable
            style="width: 170px"
            @keyup.enter="handleSearch"
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-gray-500">{{ t('subscription.fieldDeviceAddressId') }}</span>
          <el-input
            v-model="query.deviceAddressId"
            clearable
            style="width: 170px"
            @keyup.enter="handleSearch"
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-gray-500">{{ t('subscription.fieldDeviceAddressName') }}</span>
          <el-input
            v-model="query.deviceAddressName"
            clearable
            style="width: 170px"
            @keyup.enter="handleSearch"
          />
        </div>
        <div class="flex flex-col gap-1">
          <span class="text-xs text-gray-500">{{ t('subscription.fieldProtocol') }}</span>
          <el-input
            v-model="query.protocol"
            clearable
            style="width: 150px"
            @keyup.enter="handleSearch"
          />
        </div>
        <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
        <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
      </div>
    </el-card>

    <!-- 数据表格 -->
    <el-card shadow="never">
      <el-table :data="rows" v-loading="loading" stripe border style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="65" align="center" />
        <el-table-column
          prop="deviceId"
          :label="t('subscription.fieldDeviceId')"
          min-width="120"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="font-mono text-xs">{{ row.deviceId }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="deviceName"
          :label="t('subscription.fieldDeviceName')"
          min-width="130"
          show-overflow-tooltip
        />
        <el-table-column
          prop="deviceAddressId"
          :label="t('subscription.fieldDeviceAddressId')"
          min-width="130"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            <span class="font-mono text-xs">{{ row.deviceAddressId }}</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="deviceAddressName"
          :label="t('subscription.fieldDeviceAddressName')"
          min-width="130"
          show-overflow-tooltip
        />
        <el-table-column
          prop="deviceAddressLabel"
          :label="t('subscription.fieldDeviceAddressLabel')"
          min-width="120"
          show-overflow-tooltip
        />
        <el-table-column prop="dataType" :label="t('subscription.fieldDataType')" width="110" show-overflow-tooltip />
        <el-table-column prop="kind" :label="t('subscription.fieldKind')" width="90" />
        <el-table-column prop="protocol" :label="t('subscription.fieldProtocol')" min-width="120" show-overflow-tooltip />
        <el-table-column
          prop="firstSeenAt"
          :label="t('subscriptionDevicePoint.firstSeenAt')"
          width="165"
          align="center"
        />
        <el-table-column
          prop="lastSeenAt"
          :label="t('subscriptionDevicePoint.lastSeenAt')"
          width="165"
          align="center"
        />
      </el-table>

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
import { reactive, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
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
