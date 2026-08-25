<template>
  <div class="flex items-center gap-2">
    <el-input
      :model-value="modelValue"
      :placeholder="placeholder"
      readonly
      style="width: 280px"
    />
    <el-button type="primary" @click="handleOpen">
      {{ buttonText }}
    </el-button>

    <!-- 协议选择对话框 -->
    <el-dialog
      v-model="visible"
      title="选择协议"
      width="560px"
      :close-on-click-modal="false"
      append-to-body
      @close="handleClose"
    >
      <div class="flex items-center gap-3 mb-4">
        <el-input
          v-model="query.keyword"
          placeholder="请输入协议名称搜索"
          clearable
          style="width: 240px"
          @keyup.enter="handleSearch"
        />
        <el-button type="primary" @click="handleSearch">查询</el-button>
      </div>
      <el-table
        :data="list"
        v-loading="loading"
        stripe
        style="width: 100%"
        border
        highlight-current-row
        @row-click="handleRowClick"
      >
        <el-table-column type="index" label="序号" width="60" align="center" />
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
      </el-table>
      <div class="flex justify-end mt-4">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :page-sizes="[5, 10, 20, 50]"
          :total="total"
          layout="total, sizes, prev, pager, next, jumper"
          background
          small
          @size-change="fetchList"
          @current-change="fetchList"
        />
      </div>
      <template #footer>
        <el-button @click="visible = false">取消</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive } from 'vue'
import { getProtocolList } from '@/api/modules/protocol'

defineOptions({ name: 'ProtocolSelector' })

const props = defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  placeholder: {
    type: String,
    default: '请选择协议',
  },
  buttonText: {
    type: String,
    default: '选择协议',
  },
})

const emit = defineEmits(['update:modelValue', 'select'])

// ---------- 对话框状态 ----------
const visible = ref(false)
const loading = ref(false)
const list = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(5)
const query = reactive({ keyword: '' })

function handleOpen() {
  visible.value = true
  page.value = 1
  fetchList()
}

async function fetchList() {
  loading.value = true
  try {
    const params = { page: page.value, size: pageSize.value }
    if (query.keyword) params.name = query.keyword
    const res = await getProtocolList(params)
    list.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    list.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  page.value = 1
  fetchList()
}

function handleRowClick(row) {
  emit('update:modelValue', row.name)
  emit('select', row)
  visible.value = false
}

function handleClose() {
  query.keyword = ''
}
</script>
