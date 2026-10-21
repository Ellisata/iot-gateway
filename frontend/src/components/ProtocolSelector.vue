<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="protocol-selector">
    <!-- 只读输入框 + 选择按钮：输入框本身也可点开弹窗，比"只有按钮能点"少一次瞄准 -->
    <el-input
      class="selector-input"
      :model-value="modelValue"
      :placeholder="resolvedPlaceholder"
      readonly
      @click="handleOpen"
    >
      <template #prefix>
        <el-icon><PlugConnected /></el-icon>
      </template>
    </el-input>
    <el-button type="primary" @click="handleOpen">
      {{ resolvedButtonText }}
    </el-button>

    <!-- 协议选择对话框 -->
    <el-dialog
      v-model="visible"
      class="iot-dialog"
      :title="t('selector.selectProtocolBtn')"
      width="620px"
      :close-on-click-modal="false"
      append-to-body
      @close="handleClose"
    >
      <p class="iot-dialog-hint">{{ t('selector.rowHint') }}</p>

      <div class="selector-toolbar">
        <el-input
          v-model="query.keyword"
          class="selector-search"
          :placeholder="t('selector.searchPlaceholder')"
          clearable
          @keyup.enter="handleSearch"
        >
          <template #prefix>
            <el-icon><Search /></el-icon>
          </template>
        </el-input>
        <el-button type="primary" @click="handleSearch">{{ t('selector.search') }}</el-button>
      </div>

      <el-table
        class="iot-table is-compact selector-table"
        :data="list"
        v-loading="loading"
        row-key="id"
        style="width: 100%"
        highlight-current-row
        @row-click="handleRowClick"
      >
        <el-table-column type="index" :label="t('selector.index')" width="56" align="center" />

        <!-- 协议名是选中这一行的依据：给一个小图标底座，和列表页保持同一套身份样式 -->
        <el-table-column :label="t('selector.name')" min-width="160">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><PlugConnected /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('selector.description')"
          min-width="220"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><PlugConnected /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ query.keyword
                ? t('selector.emptySearch', { keyword: query.keyword })
                : t('selector.emptyTitle') }}
            </p>
          </div>
        </template>
      </el-table>

      <div class="iot-pager is-flush">
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
        <el-button @click="visible = false">{{ t('selector.cancel') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, ref, reactive } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  IconPlugConnected as PlugConnected,
  IconSearch as Search,
} from '@tabler/icons-vue'
import { getProtocolList } from '@/api/modules/protocol'

defineOptions({ name: 'ProtocolSelector' })

const { t } = useI18n()

const props = defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  placeholder: {
    type: String,
    default: '',
  },
  buttonText: {
    type: String,
    default: '',
  },
})

const emit = defineEmits(['update:modelValue', 'select'])

// 未传入时使用当前语言的默认文案
const resolvedPlaceholder = computed(() => props.placeholder || t('selector.selectProtocol'))
const resolvedButtonText = computed(() => props.buttonText || t('selector.selectProtocolBtn'))

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

<style scoped>
/* 触发器：输入框与按钮统一 34px 高、9px 圆角，放进设备对象表单或协议表单工具条
   都不会比旁边的控件高出一截 */
.protocol-selector {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.selector-input {
  width: 280px;
  max-width: 100%;
}

.selector-input :deep(.el-input__wrapper) {
  min-height: 34px;
  background: #fbfcfe;
  border-radius: 9px;
  box-shadow: 0 0 0 1px var(--line, #e6ebf2) inset;
  transition: box-shadow 0.18s ease, background-color 0.18s ease;
}

.selector-input :deep(.el-input__wrapper:hover) {
  background: #fff;
  box-shadow: 0 0 0 1px #cbd8e6 inset;
}

.selector-input :deep(.el-input__inner) {
  cursor: pointer;
}

.selector-input :deep(.el-input__prefix) {
  color: var(--brand, #2563eb);
}

.protocol-selector > .el-button {
  height: 34px;
  padding: 0 14px;
  border-radius: 9px;
  font-weight: 500;
  flex: none;
}

/* 弹窗内的筛选行与表格：与页面工具条/表格同一套尺度 */
.selector-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}

.selector-search {
  width: 280px;
}

.selector-table {
  overflow: hidden;
  border: 1px solid var(--line-soft, #eef2f7);
  border-radius: 12px;
}

.selector-table :deep(.el-table__row) {
  cursor: pointer;
}

/* highlight-current-row 的默认底色会被 iot-table 的白色单元格覆盖，这里补回来 */
.selector-table :deep(.el-table__body tr.current-row > td.el-table__cell) {
  background-color: var(--brand-soft, #eff5ff);
}

.selector-table :deep(.el-table__body tr.current-row .iot-name-text) {
  color: var(--brand, #2563eb);
}

@media (max-width: 640px) {
  .protocol-selector {
    flex-wrap: wrap;
  }

  .selector-input,
  .selector-search {
    width: 100%;
  }
}
</style>
