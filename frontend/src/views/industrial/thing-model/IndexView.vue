<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与设备对象/协议页同一套字号与留白层级 -->
    <header class="iot-head iot-enter">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.industrial') }}
          </p>
        </div>
        <div class="iot-title-row">
          <h1 class="iot-title">{{ t('menu.thingModel') }}</h1>
          <span class="iot-head-sub">{{ t('thingModel.subtitle') }}</span>
        </div>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('thingModel.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.keyword"
            class="iot-search"
            :placeholder="t('thingModel.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-select
            v-model="queryForm.category"
            class="thing-category-select"
            :placeholder="t('thingModel.categoryFilter')"
            clearable
            @change="handleSearch"
          >
            <el-option
              v-for="opt in filterCategoryOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <el-button :icon="Download" @click="handleModelTemplateDownload">
            {{ t('common.downloadTemplate') }}
          </el-button>
          <el-button :icon="Upload" :loading="modelImporting" @click="handleModelImport">
            {{ t('common.import') }}
          </el-button>
          <el-button type="primary" :icon="Plus" @click="handleAdd">
            {{ t('thingModel.add') }}
          </el-button>
          <input
            ref="modelFileInputRef"
            type="file"
            accept=".xlsx"
            class="hidden"
            @change="handleModelFileChange"
          />
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        row-key="id"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <!-- code 是模型的稳定身份，用等宽字体；名称才是给人看的 -->
        <el-table-column :label="t('thingModel.code')" min-width="180">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Box /></el-icon>
              </span>
              <span class="iot-mono" :title="row.code">{{ row.code }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.name')" min-width="180">
          <template #default="{ row }">
            <span class="thing-name" :title="row.name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thingModel.categoryLabel')" width="128" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.category ? 'primary' : 'info'">
              {{ categoryLabel(row.category) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('thingModel.version')"
          width="96"
          align="center"
          class-name="iot-col-num"
        >
          <template #default="{ row }">{{ row.version || '—' }}</template>
        </el-table-column>
        <el-table-column :label="t('thingModel.propertyCount')" width="96" align="center">
          <template #default="{ row }">
            <el-button link type="primary" @click="openProperties(row)">
              {{ row.propertyCount }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="updatedAt"
          :label="t('common.updatedAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="168" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('thingModel.manageProperties')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Settings"
                  :aria-label="t('thingModel.manageProperties')"
                  @click="openProperties(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('common.edit')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="EditPen"
                  :aria-label="t('common.edit')"
                  @click="handleEdit(row)"
                />
              </el-tooltip>
              <el-tooltip :content="t('common.delete')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="danger"
                  link
                  size="small"
                  :icon="Delete"
                  :aria-label="t('common.delete')"
                  @click="handleDelete(row)"
                />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ hasQuery
                ? t('thingModel.emptySearch')
                : t('thingModel.emptyTitle') }}
            </p>
            <p v-if="!hasQuery" class="iot-empty-hint">{{ t('thingModel.emptyHint') }}</p>
            <el-button v-if="hasQuery" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('thingModel.add') }}
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

    <!-- ========== 物模型表单 ========== -->
    <el-dialog
      v-model="modelDialogVisible"
      class="iot-dialog"
      :title="isModelEdit ? t('thingModel.editTitle') : t('thingModel.addTitle')"
      width="640px"
      :close-on-click-modal="false"
      @closed="handleModelDialogClosed"
    >
      <el-form
        ref="modelFormRef"
        :model="modelForm"
        :rules="modelRules"
        label-width="108px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('thingModel.code')" prop="code">
          <el-input
            v-model="modelForm.code"
            :disabled="isModelEdit"
            :placeholder="t('thingModel.codePlaceholder')"
            maxlength="64"
          />
          <div class="iot-form-hint is-block">
            {{ isModelEdit ? t('thingModel.codeImmutableHint') : t('thingModel.codeHint') }}
          </div>
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="modelForm.name"
            :placeholder="t('thingModel.namePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.categoryLabel')" prop="category">
          <el-select v-model="modelForm.category" style="width: 100%">
            <el-option
              v-for="opt in formCategoryOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('thingModel.version')" prop="version">
          <el-input
            v-model="modelForm.version"
            :placeholder="t('thingModel.versionPlaceholder')"
            maxlength="32"
          />
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="modelForm.description"
            type="textarea"
            :rows="3"
            :placeholder="t('thingModel.descriptionPlaceholder')"
            maxlength="500"
            show-word-limit
          />
        </el-form-item>
        <el-form-item v-if="isModelEdit" :label="t('common.status')" prop="status">
          <el-switch
            v-model="modelForm.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="modelDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="modelSubmitting" @click="handleSubmitModel">
          {{ t('common.save') }}
        </el-button>
      </template>
    </el-dialog>

    <ImportResultDialog v-model:visible="modelImportResultVisible" :result="modelImportResult" />
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconBox as Box,
  IconDownload as Download,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconSettings as Settings,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  createThingModel,
  deleteThingModel,
  downloadThingModelTemplate,
  getThingModelList,
  importThingModels,
  updateThingModel,
} from '@/api/modules/thingModel'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'

const { t, te } = useI18n()

// 内置分类的展示名来自 i18n；开放接口下发的平台自定义分类没有内置文案，
// 直接展示 code（平台是分类词表的权威源，网关不跟随发版）。
const CATEGORY_CODES = ['sensor', 'meter', 'actuator', 'robot', 'plc', 'other']

const categoryLabel = (code) => {
  if (!code) return t('thingModel.category.none')
  const key = `thingModel.category.${code}`
  return te(key) ? t(key) : code
}

const filterCategoryOptions = computed(() => {
  const custom = [...new Set(tableData.value.map((r) => r.category).filter((code) => code && !CATEGORY_CODES.includes(code)))]
  return [...CATEGORY_CODES, ...custom].map((code) => ({ value: code, label: categoryLabel(code) }))
})
// 表单里的“未分类”是空串，和筛选框的“全部”不同：筛选空串 = 不过滤。
const formCategoryOptions = computed(() => {
  // 本地管理端只允许新建内置分类；编辑时保留开放接口已下发的自定义分类。
  const options = [
    { value: '', label: t('thingModel.category.none') },
    ...CATEGORY_CODES.map((code) => ({ value: code, label: categoryLabel(code) })),
  ]
  const current = modelForm.category
  if (current && !options.some((o) => o.value === current)) {
    options.push({ value: current, label: categoryLabel(current) })
  }
  return options
})
// ---------- 列表 ----------
const route = useRoute()
const router = useRouter()

// 筛选与分页写回地址栏：刷新页面、分享链接时能回到同一屏结果。
// 属性页的返回/面包屑走的是不带 query 的列表路由，所以同时把最近一次的
// 筛选存进 sessionStorage，回列表时 URL 为空就恢复它。
// 只写非默认值，默认进页面时 URL 保持干净。
const PAGE_SIZES = [10, 20, 50, 100]
const LIST_QUERY_KEY = 'iotGateway:thingModelListQuery'
const LIST_QUERY_FIELDS = ['keyword', 'category', 'page', 'size']

function readStoredQuery() {
  try {
    const raw = sessionStorage.getItem(LIST_QUERY_KEY)
    return raw ? JSON.parse(raw) : {}
  } catch {
    return {}
  }
}

const routeHasListQuery = LIST_QUERY_FIELDS.some((key) => key in route.query)
const initialQuery = routeHasListQuery ? route.query : readStoredQuery()
const loading = ref(false)
const tableData = ref([])
const currentPage = ref(
  Number.isInteger(Number(initialQuery.page)) && Number(initialQuery.page) > 0
    ? Number(initialQuery.page)
    : 1,
)
const pageSize = ref(
  PAGE_SIZES.includes(Number(initialQuery.size)) ? Number(initialQuery.size) : 10,
)
const total = ref(0)
const queryForm = reactive({
  keyword: typeof initialQuery.keyword === 'string' ? initialQuery.keyword : '',
  category: typeof initialQuery.category === 'string' ? initialQuery.category : '',
})

function syncQuery() {
  const query = {}
  if (queryForm.keyword) query.keyword = queryForm.keyword
  if (queryForm.category) query.category = queryForm.category
  if (currentPage.value > 1) query.page = String(currentPage.value)
  if (pageSize.value !== 10) query.size = String(pageSize.value)
  try {
    sessionStorage.setItem(LIST_QUERY_KEY, JSON.stringify(query))
  } catch {
    // 隐私模式等场景下 sessionStorage 不可用，URL 同步仍然生效
  }
  router.replace({ query })
}

const hasQuery = computed(() => !!(queryForm.keyword || queryForm.category))
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

async function fetchList() {
  syncQuery()
  loading.value = true
  try {
    const res = await getThingModelList({
      page: currentPage.value,
      size: pageSize.value,
      keyword: queryForm.keyword,
      category: queryForm.category,
    })
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch {
    // 错误提示由 axios 拦截器统一给出
    tableData.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.keyword = ''
  queryForm.category = ''
  currentPage.value = 1
  fetchList()
}

function handleSizeChange(size) {
  pageSize.value = size
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange(page) {
  currentPage.value = page
  fetchList()
}

// ---------- 物模型表单 ----------
const modelDialogVisible = ref(false)
const modelSubmitting = ref(false)
const modelFormRef = ref(null)
const modelForm = reactive({
  id: '',
  code: '',
  name: '',
  category: '',
  version: '1.0.0',
  description: '',
  status: 1,
})

const isModelEdit = computed(() => !!modelForm.id)

const modelRules = computed(() => ({
  code: [
    { required: true, message: t('thingModel.codeRequired'), trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_-]{0,63}$/,
      message: t('thingModel.codeFormat'),
      trigger: 'blur',
    },
  ],
  name: [
    { required: true, message: t('thingModel.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: t('thingModel.nameLength'), trigger: 'blur' },
  ],
  version: [{ max: 32, message: t('thingModel.versionLength'), trigger: 'blur' }],
  description: [{ max: 500, message: t('thingModel.descriptionLength'), trigger: 'blur' }],
}))

function resetModelForm() {
  Object.assign(modelForm, {
    id: '',
    code: '',
    name: '',
    category: '',
    version: '1.0.0',
    description: '',
    status: 1,
  })
}

function handleAdd() {
  resetModelForm()
  modelDialogVisible.value = true
  nextTick(() => modelFormRef.value?.clearValidate())
}

function handleEdit(row) {
  Object.assign(modelForm, {
    id: row.id,
    code: row.code,
    name: row.name,
    category: row.category ?? '',
    version: row.version || '1.0.0',
    description: row.description ?? '',
    status: row.status,
  })
  modelDialogVisible.value = true
  nextTick(() => modelFormRef.value?.clearValidate())
}

function handleModelDialogClosed() {
  modelFormRef.value?.resetFields()
}

async function handleSubmitModel() {
  const valid = await modelFormRef.value?.validate().catch(() => false)
  if (!valid) return

  modelSubmitting.value = true
  try {
    const payload = {
      name: modelForm.name,
      category: modelForm.category,
      version: modelForm.version,
      description: modelForm.description,
    }
    if (isModelEdit.value) {
      await updateThingModel({
        ...payload,
        id: modelForm.id,
        code: modelForm.code,
        status: modelForm.status,
      })
      ElMessage.success(t('thingModel.updateSuccess'))
    } else {
      await createThingModel({
        ...payload,
        code: modelForm.code,
      })
      ElMessage.success(t('thingModel.createSuccess'))
    }
    modelDialogVisible.value = false
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    modelSubmitting.value = false
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('thingModel.deleteConfirm', { name: row.name }),
      t('thingModel.deleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return
  }
  try {
    await deleteThingModel(row.id)
    ElMessage.success(t('thingModel.deleteSuccess'))
    if (tableData.value.length === 1 && currentPage.value > 1) {
      currentPage.value -= 1
    }
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

// 属性在独立页面编辑：钻取路由把模型 id 带过去，名称只用于页头先显示，
// 属性页仍会用 id 回查一次（刷新页面时 query 不可信）。
function openProperties(row) {
  router.push({
    name: 'ThingModelProperties',
    params: { id: row.id },
    query: { name: row.name },
  })
}

// ---------- Excel 导入 / 模板下载 ----------
const {
  fileInputRef: modelFileInputRef,
  importing: modelImporting,
  importResultVisible: modelImportResultVisible,
  importResult: modelImportResult,
  handleImport: handleModelImport,
  handleFileChange: handleModelFileChange,
  handleDownloadTemplate: handleModelTemplateDownload,
} = useExcelImport({
  importFn: (file) => importThingModels(file),
  downloadFn: downloadThingModelTemplate,
  templateName: () => t('thingModel.importModelTemplateName'),
  refresh: fetchList,
})


onMounted(fetchList)
</script>

<style scoped>
.thing-category-select {
  width: 150px;
}

.thing-name {
  color: var(--ink-2);
  font-weight: 500;
}
</style>
