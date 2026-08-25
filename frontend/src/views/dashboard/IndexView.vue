<template>
  <div class="p-6">
    <!-- 设备在线情况统计 -->
    <el-card
      v-for="group in deviceCards"
      :key="group.title"
      class="collect-card mb-4 cursor-pointer"
      shadow="never"
      v-loading="loading"
      element-loading-text="统计加载中..."
      @click="goDeviceList"
    >
      <template #header>
        <div class="flex items-center justify-between">
          <span class="font-bold text-gray-700">{{ group.title }}</span>
        </div>
      </template>
      <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div
          v-for="item in group.items"
          :key="item.label"
          class="collect-item"
        >
          <div class="collect-label">{{ item.label }}</div>
          <div
            class="collect-value"
            :style="item.color ? { color: item.color } : {}"
          >
            {{ item.value }}
          </div>
        </div>
      </div>
    </el-card>

    <!-- 数据采集状态 -->
    <el-card
      class="collect-card mb-4"
      shadow="never"
      v-loading="colLoading"
      element-loading-text="采集状态加载中..."
    >
      <template #header>
        <div class="flex items-center justify-between">
          <span class="font-bold text-gray-700">数据采集状态</span>
          <el-tag :type="collection?.running ? 'success' : 'danger'" size="small" effect="dark">
            {{ collection?.running ? '运行中' : '已停止' }}
          </el-tag>
        </div>
      </template>
      <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div class="collect-item">
          <div class="collect-label">最近轮询</div>
          <div class="collect-value">{{ collection?.lastPollTime || '—' }}</div>
        </div>
        <div class="collect-item">
          <div class="collect-label">最近成功采集</div>
          <div class="collect-value">{{ collection?.lastSuccessTime || '—' }}</div>
        </div>
        <div class="collect-item">
          <div class="collect-label">采集地址数</div>
          <div class="collect-value">{{ collection?.addressCount ?? 0 }}</div>
        </div>
        <div class="collect-item">
          <div class="collect-label">累计错误</div>
          <div
            class="collect-value"
            :class="{ 'text-red-500': (collection?.errorCount ?? 0) > 0 }"
          >
            {{ collection?.errorCount ?? 0 }}
          </div>
        </div>
      </div>
    </el-card>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { getDeviceOverview } from '@/api/modules/deviceObject'
import { getCollectionStatus } from '@/api/modules/collection'

const router = useRouter()

const loading = ref(false)
const overview = ref(null)

const colLoading = ref(false)
const collection = ref(null)

let timer = null

// ---------- 设备信息（按采集状态卡片布局分组） ----------
const deviceCards = computed(() => {
  const o = overview.value || {}
  return [
    {
      title: '设备在线情况',
      items: [
        { label: '全部设备', value: o.total ?? 0, color: '#606266' },
        { label: '在线', value: o.online ?? 0, color: '#67c23a' },
        { label: '离线', value: o.offline ?? 0, color: '#f56c6c' },
        { label: '在线率', value: `${Math.round((o.onlineRate || 0) * 100)}%`, color: '#409eff' },
      ],
    },
    {
      title: '设备明细',
      items: [
        { label: '启用', value: o.enabled ?? 0 },
        { label: '禁用', value: o.disabled ?? 0 },
        { label: '已接入采集', value: o.collected ?? 0 },
        { label: '未接入采集', value: o.unCollected ?? 0 },
      ],
    },
  ]
})

// ---------- 获取统计 ----------
async function fetchOverview() {
  loading.value = true
  try {
    overview.value = await getDeviceOverview()
  } catch (err) {
    console.error('获取设备在线统计失败', err)
  } finally {
    loading.value = false
  }
}

// ---------- 获取采集状态 ----------
async function fetchCollection() {
  colLoading.value = true
  try {
    collection.value = await getCollectionStatus()
  } catch (err) {
    console.error('获取采集状态失败', err)
  } finally {
    colLoading.value = false
  }
}

// ---------- 跳转设备列表 ----------
function goDeviceList() {
  router.push('/industrial/device-object')
}

onMounted(() => {
  fetchOverview()
  fetchCollection()
  // 在线情况/采集状态随采集变化，定时刷新
  timer = setInterval(() => {
    fetchOverview()
    fetchCollection()
  }, 30000)
})

onUnmounted(() => {
  if (timer) {
    clearInterval(timer)
  }
})
</script>

<style scoped>
.collect-card {
  --el-card-padding: 20px;
  border-radius: 12px;
}

.collect-item {
  padding: 12px 16px;
  background: #f5f7fa;
  border-radius: 8px;
}

.collect-label {
  font-size: 13px;
  color: #909399;
  margin-bottom: 4px;
}

.collect-value {
  font-size: 15px;
  font-weight: 500;
  color: #303133;
  word-break: break-all;
}
</style>
