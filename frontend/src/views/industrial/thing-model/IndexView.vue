<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与设备对象/协议页同一套字号与留白层级 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ t('menu.thingModel') }}</h1>
        <p class="iot-sub">{{ t('thingModel.subtitle') }}</p>
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
          <el-button type="primary" :icon="Plus" @click="handleAdd">
            {{ t('thingModel.add') }}
          </el-button>
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
                  :icon="ListDetails"
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
      width="580px"
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

    <!-- ========== 属性管理抽屉 ========== -->
    <el-drawer
      v-model="propertyDrawerVisible"
      class="thing-property-drawer"
      size="min(920px, 96vw)"
      :title="propertyDrawerTitle"
      @closed="handleDrawerClosed"
    >
      <div class="thing-drawer-body">
        <div class="thing-drawer-head">
          <p class="thing-drawer-sub">{{ t('thingModel.propertiesSubtitle') }}</p>
          <el-button type="primary" :icon="Plus" @click="handleAddProperty">
            {{ t('thingModel.addProperty') }}
          </el-button>
        </div>

        <el-table
          class="iot-table is-compact"
          :data="properties"
          v-loading="propertyLoading"
          row-key="id"
        >
          <el-table-column
            :label="t('thingModel.sortOrder')"
            width="72"
            align="center"
            class-name="iot-col-num"
          >
            <template #default="{ row }">{{ row.sortOrder }}</template>
          </el-table-column>
          <el-table-column :label="t('thingModel.identifier')" min-width="170">
            <template #default="{ row }">
              <span class="iot-mono" :title="row.identifier">{{ row.identifier }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.name')" min-width="140" show-overflow-tooltip>
            <template #default="{ row }">{{ row.name }}</template>
          </el-table-column>
          <el-table-column :label="t('thingModel.kind.label')" width="116" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain">{{ kindLabel(row.kind) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.access.label')" width="104" align="center">
            <template #default="{ row }">
              <el-tag
                size="small"
                effect="plain"
                :type="row.accessMode === 'R' ? 'info' : 'warning'"
              >
                {{ accessLabel(row.accessMode) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.unit')" width="86" align="center">
            <template #default="{ row }">
              <span :class="row.unit ? '' : 'iot-cell-muted'">{{ row.unit || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.status')" width="90" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
                {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.action')" width="100" align="center" fixed="right">
            <template #default="{ row }">
              <div class="iot-row-actions">
                <el-tooltip :content="t('common.edit')" placement="top">
                  <el-button
                    class="iot-row-btn"
                    type="primary"
                    link
                    size="small"
                    :icon="EditPen"
                    :aria-label="t('common.edit')"
                    @click="handleEditProperty(row)"
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
                    @click="handleDeleteProperty(row)"
                  />
                </el-tooltip>
              </div>
            </template>
          </el-table-column>

          <template #empty>
            <div class="iot-empty">
              <span class="iot-empty-mark" aria-hidden="true">
                <el-icon><ListDetails /></el-icon>
              </span>
              <p class="iot-empty-title">{{ t('thingModel.propertyEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.propertyEmptyHint') }}</p>
              <el-button type="primary" :icon="Plus" @click="handleAddProperty">
                {{ t('thingModel.addProperty') }}
              </el-button>
            </div>
          </template>
        </el-table>

        <div v-if="propertyTotal > 0" class="iot-pager">
          <el-pagination
            v-model:current-page="propertyPage"
            v-model:page-size="propertyPageSize"
            :page-sizes="[10, 20, 50, 100]"
            :total="propertyTotal"
            layout="total, sizes, prev, pager, next"
            background
            small
            @size-change="handlePropertySizeChange"
            @current-change="handlePropertyCurrentChange"
          />
        </div>
      </div>
    </el-drawer>

    <!-- ========== 属性表单 ========== -->
    <el-dialog
      v-model="propertyDialogVisible"
      class="iot-dialog"
      :title="isPropertyEdit ? t('thingModel.editPropertyTitle') : t('thingModel.addPropertyTitle')"
      width="560px"
      append-to-body
      :close-on-click-modal="false"
      @closed="handlePropertyDialogClosed"
    >
      <el-form
        ref="propertyFormRef"
        :model="propertyForm"
        :rules="propertyRules"
        label-width="108px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('thingModel.identifier')" prop="identifier">
          <el-input
            v-model="propertyForm.identifier"
            :disabled="isPropertyEdit"
            :placeholder="t('thingModel.identifierPlaceholder')"
            maxlength="64"
          />
          <div class="iot-form-hint is-block">
            {{
              isPropertyEdit
                ? t('thingModel.identifierImmutableHint')
                : t('thingModel.identifierHint')
            }}
          </div>
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="propertyForm.name"
            :placeholder="t('thingModel.propertyNamePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.kind.label')" prop="kind">
          <el-select v-model="propertyForm.kind" style="width: 100%">
            <el-option
              v-for="item in kindOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('thingModel.access.label')" prop="accessMode">
          <el-select v-model="propertyForm.accessMode" style="width: 100%">
            <el-option
              v-for="item in accessOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('thingModel.unit')" prop="unit">
          <el-input
            v-model="propertyForm.unit"
            :placeholder="t('thingModel.unitPlaceholder')"
            maxlength="32"
          />
        </el-form-item>
        <el-form-item :label="t('thingModel.sortOrder')" prop="sortOrder">
          <el-input-number v-model="propertyForm.sortOrder" :min="0" :max="99999" />
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="propertyForm.description"
            type="textarea"
            :rows="2"
            :placeholder="t('thingModel.descriptionPlaceholder')"
            maxlength="500"
            show-word-limit
          />
        </el-form-item>
        <el-form-item v-if="isPropertyEdit" :label="t('common.status')" prop="status">
          <el-switch
            v-model="propertyForm.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="propertyDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="propertySubmitting" @click="handleSubmitProperty">
          {{ t('common.save') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconBox as Box,
  IconEdit as EditPen,
  IconListDetails as ListDetails,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import {
  createThingModel,
  createThingModelProperty,
  deleteThingModel,
  deleteThingModelProperty,
  getThingModelList,
  pageThingModelProperties,
  updateThingModel,
  updateThingModelProperty,
} from '@/api/modules/thingModel'

const { t } = useI18n()

// 分类是固定枚举 code，展示名来自 i18n；空串表示未分类，且不落库中文。
const CATEGORY_CODES = ['sensor', 'meter', 'actuator', 'robot', 'plc', 'other']
const KIND_CODES = ['bool', 'int', 'uint', 'float', 'string', 'time']
const ACCESS_CODES = ['R', 'W', 'RW']

const categoryLabel = (code) =>
  code ? t(`thingModel.category.${code}`) : t('thingModel.category.none')
const kindLabel = (kind) => t(`thingModel.kind.${kind}`)
const accessLabel = (mode) => t(`thingModel.access.${mode}`)

const filterCategoryOptions = computed(() =>
  CATEGORY_CODES.map((code) => ({ value: code, label: categoryLabel(code) })),
)
// 表单里的“未分类”是空串，和筛选框的“全部”不同：筛选空串 = 不过滤。
const formCategoryOptions = computed(() => [
  { value: '', label: t('thingModel.category.none') },
  ...filterCategoryOptions.value,
])
const kindOptions = computed(() =>
  KIND_CODES.map((code) => ({ value: code, label: kindLabel(code) })),
)
const accessOptions = computed(() =>
  ACCESS_CODES.map((code) => ({ value: code, label: accessLabel(code) })),
)

// ---------- 列表 ----------
const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const queryForm = reactive({ keyword: '', category: '' })

const hasQuery = computed(() => !!(queryForm.keyword || queryForm.category))
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

async function fetchList() {
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
      await createThingModel({ ...payload, code: modelForm.code })
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

// ---------- 属性抽屉 ----------
const propertyDrawerVisible = ref(false)
const propertyLoading = ref(false)
const properties = ref([])
const propertyPage = ref(1)
const propertyPageSize = ref(10)
const propertyTotal = ref(0)
// 模型内最大排序值：分页后前端只有当前页，默认排序必须由服务端给出
const propertyMaxSortOrder = ref(0)
const currentModel = ref(null)

const propertyDrawerTitle = computed(() =>
  currentModel.value
    ? t('thingModel.propertiesTitle', { name: currentModel.value.name })
    : t('thingModel.propertiesTitle', { name: '' }),
)

async function openProperties(row) {
  currentModel.value = row
  propertyPage.value = 1
  propertyDrawerVisible.value = true
  await fetchProperties()
}

async function fetchProperties() {
  if (!currentModel.value) return
  propertyLoading.value = true
  try {
    const res = await pageThingModelProperties(currentModel.value.id, {
      page: propertyPage.value,
      size: propertyPageSize.value,
    })
    properties.value = res?.records ?? []
    propertyTotal.value = res?.total ?? 0
    propertyMaxSortOrder.value = res?.maxSortOrder ?? 0
  } catch {
    properties.value = []
    propertyTotal.value = 0
    propertyMaxSortOrder.value = 0
  } finally {
    propertyLoading.value = false
  }
}

function handlePropertySizeChange(size) {
  propertyPageSize.value = size
  propertyPage.value = 1
  fetchProperties()
}

function handlePropertyCurrentChange(page) {
  propertyPage.value = page
  fetchProperties()
}

function handleDrawerClosed() {
  currentModel.value = null
  properties.value = []
  propertyPage.value = 1
  propertyTotal.value = 0
  propertyMaxSortOrder.value = 0
}

// ---------- 属性表单 ----------
const propertyDialogVisible = ref(false)
const propertySubmitting = ref(false)
const propertyFormRef = ref(null)
const propertyForm = reactive({
  id: '',
  identifier: '',
  name: '',
  kind: 'float',
  accessMode: 'R',
  unit: '',
  description: '',
  sortOrder: 10,
  status: 1,
})
const isPropertyEdit = computed(() => !!propertyForm.id)

const propertyRules = computed(() => ({
  identifier: [
    { required: true, message: t('thingModel.identifierRequired'), trigger: 'blur' },
    {
      pattern: /^[A-Za-z][A-Za-z0-9_]{0,63}$/,
      message: t('thingModel.identifierFormat'),
      trigger: 'blur',
    },
  ],
  name: [
    { required: true, message: t('thingModel.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: t('thingModel.nameLength'), trigger: 'blur' },
  ],
  kind: [{ required: true, message: t('thingModel.kindRequired'), trigger: 'change' }],
  accessMode: [{ required: true, message: t('thingModel.accessRequired'), trigger: 'change' }],
  unit: [{ max: 32, message: t('thingModel.unitLength'), trigger: 'blur' }],
  description: [{ max: 500, message: t('thingModel.descriptionLength'), trigger: 'blur' }],
}))

function nextSortOrder() {
  return (Number(propertyMaxSortOrder.value) || 0) + 10
}

function handleAddProperty() {
  Object.assign(propertyForm, {
    id: '',
    identifier: '',
    name: '',
    kind: 'float',
    accessMode: 'R',
    unit: '',
    description: '',
    sortOrder: nextSortOrder(),
    status: 1,
  })
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
}

function handleEditProperty(row) {
  Object.assign(propertyForm, {
    id: row.id,
    identifier: row.identifier,
    name: row.name,
    kind: row.kind,
    accessMode: row.accessMode,
    unit: row.unit ?? '',
    description: row.description ?? '',
    sortOrder: row.sortOrder ?? 0,
    status: row.status,
  })
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
}

function handlePropertyDialogClosed() {
  propertyFormRef.value?.resetFields()
}

async function handleSubmitProperty() {
  if (!currentModel.value) return
  const valid = await propertyFormRef.value?.validate().catch(() => false)
  if (!valid) return

  // 编辑态要在关闭弹窗前取：关闭后 resetFields 会清掉 propertyForm.id
  const isEdit = isPropertyEdit.value
  propertySubmitting.value = true
  try {
    const payload = {
      name: propertyForm.name,
      kind: propertyForm.kind,
      accessMode: propertyForm.accessMode,
      unit: propertyForm.unit,
      description: propertyForm.description,
      sortOrder: propertyForm.sortOrder,
    }
    if (isEdit) {
      await updateThingModelProperty({
        ...payload,
        id: propertyForm.id,
        identifier: propertyForm.identifier,
        status: propertyForm.status,
      })
      ElMessage.success(t('thingModel.propertyUpdateSuccess'))
    } else {
      await createThingModelProperty({
        ...payload,
        modelId: currentModel.value.id,
        identifier: propertyForm.identifier,
      })
      ElMessage.success(t('thingModel.propertyCreateSuccess'))
    }
    propertyDialogVisible.value = false
    // 新增属性排在末尾（sortOrder = 全量最大值 + 10），翻到末页才看得见
    if (!isEdit) {
      propertyPage.value = Math.max(
        1,
        Math.ceil((propertyTotal.value + 1) / propertyPageSize.value),
      )
    }
    await fetchProperties()
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    propertySubmitting.value = false
  }
}

async function handleDeleteProperty(row) {
  if (!currentModel.value) return
  try {
    await ElMessageBox.confirm(
      t('thingModel.deletePropertyConfirm', { name: row.name }),
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
    await deleteThingModelProperty(row.id)
    ElMessage.success(t('thingModel.propertyDeleteSuccess'))
    if (properties.value.length === 1 && propertyPage.value > 1) {
      propertyPage.value -= 1
    }
    await fetchProperties()
    fetchList()
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

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

.thing-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.thing-drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.thing-drawer-sub {
  margin: 0;
  color: var(--ink-3);
  font-size: 13px;
  line-height: 1.6;
}
</style>
