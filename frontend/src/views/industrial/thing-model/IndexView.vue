<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page dw-page tm-page">
    <!-- 页头：沿用设备对象那套字号/留白层级。模型级动作（模板/导入/新增）
         作用于「模型集合」，不属于当前选中的某一个，放在页头右侧常驻。 -->
    <header class="iot-head dw-head">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.industrial') }}
          </p>
        </div>
        <div class="iot-title-row">
          <!-- 标题前的图标底座：与右栏模型身份、弹窗卡头用同一枚图标语言 -->
          <div class="dw-title-main">
            <span class="dw-title-mark" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <h1 class="iot-title">{{ t('menu.thingModel') }}</h1>
          </div>
          <span class="iot-head-sub">{{ t('thingModel.subtitle') }}</span>
        </div>
      </div>
      <div class="dw-head-aside">
        <div class="iot-stat dw-stat-card">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('thingModel.totalUnit') }}</span>
        </div>
        <div class="dw-head-actions">
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
    </header>

    <!-- 主从工作区：左列表选模型，右栏直接维护这个模型的属性。
         整页定高、两栏各自滚动 —— 换模型时左列表不会跟着右栏的表格一起滚走。 -->
    <div
      ref="splitRef"
      class="dw-split"
      :class="{
        'is-single': singleColumn,
        'is-list-view': !selectedModel,
        'is-detail-view': !!selectedModel,
      }"
    >
      <!-- ========== 左：物模型列表 ========== -->
      <aside class="iot-panel dw-list tm-list" :aria-label="t('thingModel.modelListLabel')">
        <div class="iot-toolbar dw-list-toolbar">
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
            <div class="tm-list-filter-actions">
              <el-select
                v-model="queryForm.category"
                class="tm-category-select"
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
          </div>
        </div>

        <div
          ref="listScrollRef"
          class="dw-list-scroll"
          role="listbox"
          :aria-label="t('thingModel.modelListLabel')"
          v-loading="loading && !records.length"
          @keydown.down.prevent="moveFocus(1)"
          @keydown.up.prevent="moveFocus(-1)"
          @keydown.home.prevent="focusCard(0)"
          @keydown.end.prevent="focusCard(records.length - 1)"
        >
          <!-- 已选模型不在当前筛选结果里：保住右栏与属性编辑现场，只说明列表里找不到它 -->
          <p v-if="selectedModel && !selectedOnPage && records.length" class="dw-list-note">
            {{ t('thingModel.selectedNotInResults') }}
          </p>

          <div
            v-for="(row, idx) in records"
            :key="row.id"
            role="option"
            :aria-selected="isSelected(row)"
            :tabindex="idx === activeFocusIndex ? 0 : -1"
            class="dw-card"
            :class="{ 'is-active': isSelected(row) }"
            :aria-label="`${row.name}, ${row.code || ''}, ${statusLabel(row)}`"
            @focus="focusedIndex = idx"
            @click="handleCardClick(row)"
            @keydown.enter.prevent="handleCardClick(row, { focusDetail: true })"
            @keydown.space.prevent="handleCardClick(row, { focusDetail: true })"
          >
            <span
              class="dw-card-dot"
              :class="row.status === 1 ? 'is-enabled' : 'is-disabled'"
              :title="statusLabel(row)"
              aria-hidden="true"
            />
            <div class="dw-card-main">
              <span class="dw-card-name" :title="row.name">{{ row.name }}</span>
              <span class="dw-card-meta">
                <span class="dw-card-code" :title="row.code || ''">{{ row.code || '—' }}</span>
                <span class="dw-card-proto" :title="categoryLabel(row.category)">
                  {{ categoryLabel(row.category) }}
                </span>
              </span>
            </div>
            <span class="tm-card-count">
              {{ row.propertyCount }} {{ t('thingModel.totalPropertiesUnit') }}
            </span>
            <!-- 悬停/选中才露出的进入提示：卡片是跳转入口而非纯展示，留一个方向感 -->
            <span class="dw-card-go" aria-hidden="true">
              <el-icon><ChevronRight /></el-icon>
            </span>
          </div>

          <div v-if="!loading && !records.length" class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ hasQuery ? t('thingModel.emptySearch') : t('thingModel.emptyTitle') }}
            </p>
            <p v-if="!hasQuery" class="iot-empty-hint">{{ t('thingModel.emptyHint') }}</p>
            <el-button v-if="hasQuery" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('thingModel.add') }}
            </el-button>
          </div>
        </div>

        <div v-if="total > pageSize" class="iot-pager dw-list-pager">
          <el-pagination
            v-model:current-page="currentPage"
            :page-size="pageSize"
            :total="total"
            size="small"
            layout="prev, pager, next"
            background
            @current-change="handleCurrentChange"
          />
        </div>
      </aside>

      <!-- ========== 右：选中模型的属性工作台 ========== -->
      <section class="iot-panel dw-detail" v-loading="hydrating">
        <header v-if="selectedModel" class="dw-detail-head">
          <button
            v-if="singleColumn"
            type="button"
            class="iot-back dw-back"
            :title="t('thingModel.backToModels')"
            @click="backToList"
          >
            <el-icon><ArrowLeft /></el-icon>
            <span class="dw-back-text">{{ t('thingModel.backToModels') }}</span>
          </button>

          <div class="dw-detail-identity">
            <span class="dw-identity-mark" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <div class="dw-detail-copy">
              <h2
                ref="detailHeadingRef"
                class="dw-detail-name"
                tabindex="-1"
                :title="selectedModel.name"
              >
                {{ selectedModel.name }}
              </h2>
              <div class="dw-detail-meta">
                <el-tooltip
                  v-if="selectedModel.code"
                  :content="t('common.copy')"
                  placement="top"
                >
                  <button
                    type="button"
                    class="dw-code-btn"
                    :aria-label="t('common.copy')"
                    @click="copyCode(selectedModel.code)"
                  >
                    <span class="dw-code-text">{{ selectedModel.code }}</span>
                    <el-icon><CopyDocument /></el-icon>
                  </button>
                </el-tooltip>
                <span class="dw-proto-chip">{{ categoryLabel(selectedModel.category) }}</span>
                <span
                  v-if="selectedModel.version"
                  class="tm-version-chip"
                  :title="t('thingModel.version')"
                >
                  v{{ selectedModel.version }}
                </span>
                <span
                  class="iot-pill"
                  :class="selectedModel.status === 1 ? 'is-green' : 'is-gray'"
                >
                  <span class="iot-pill-dot" aria-hidden="true" />
                  {{ statusLabel(selectedModel) }}
                </span>
                <span class="dw-points-stat">{{ propertiesTotalText }}</span>
              </div>
            </div>
          </div>

          <div class="dw-detail-actions">
            <el-tooltip :content="t('thingModel.copyModel')" placement="top">
              <el-button
                class="iot-row-btn"
                link
                :icon="CopyPlus"
                :loading="modelCopying"
                :aria-label="t('thingModel.copyModel')"
                @click="handleCopyModel(selectedModel)"
              />
            </el-tooltip>
            <el-tooltip :content="t('common.edit')" placement="top">
              <el-button
                class="iot-row-btn"
                type="primary"
                link
                :icon="EditPen"
                :aria-label="t('common.edit')"
                @click="handleEdit(selectedModel)"
              />
            </el-tooltip>
            <el-tooltip :content="t('common.delete')" placement="top">
              <el-button
                class="iot-row-btn"
                type="danger"
                link
                :icon="Delete"
                :aria-label="t('common.delete')"
                @click="handleDelete(selectedModel)"
              />
            </el-tooltip>
          </div>
        </header>

        <div class="dw-detail-body">
          <!-- 换模型必须重挂载：属性的检索词、分页与点位绑定缓存都在面板 setup 时
               按模型捕获；`v-if` 同时保证切回列表（窄屏）或删除模型后请求立即停止。 -->
          <PropertiesPanel
            v-if="selectedModel"
            :key="String(selectedModel.id)"
            fill
            :model-id="String(selectedModel.id)"
            :model-name="selectedModel.name"
            @total-change="propertyTotal = $event"
            @loading-change="propertyLoading = $event"
          />
          <div v-else class="iot-empty dw-detail-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('thingModel.selectModelHint') }}</p>
            <el-button type="primary" :icon="Plus" @click="handleAdd">
              {{ t('thingModel.add') }}
            </el-button>
          </div>
        </div>
      </section>
    </div>

    <!-- ========== 物模型表单 ==========
         版式与点位表单 / 触发规则表单 / 属性表单同一套：页头三级递进，
         正文按「基础信息」一张卡片分组，页脚左侧常驻一句后果说明。 -->
    <el-dialog
      v-model="modelDialogVisible"
      class="iot-dialog thing-model-dialog"
      :title="isModelEdit ? t('thingModel.editTitle') : t('thingModel.addTitle')"
      width="min(680px, 94vw)"
      append-to-body
      :close-on-click-modal="false"
      @closed="handleModelDialogClosed"
    >
      <template #header>
        <div class="tm-dialog-head">
          <p class="tm-dialog-eyebrow">
            <span class="tm-dialog-eyebrow-dot" aria-hidden="true" />
            {{ isModelEdit ? t('thingModel.modelFormEyebrowEdit') : t('thingModel.modelFormEyebrowAdd') }}
          </p>
          <h2 class="tm-dialog-title">
            {{ isModelEdit ? t('thingModel.editTitle') : t('thingModel.addTitle') }}
          </h2>
          <p class="tm-dialog-sub">{{ t('thingModel.modelFormLead') }}</p>
        </div>
      </template>

      <el-form
        ref="modelFormRef"
        :model="modelForm"
        :rules="modelRules"
        label-width="96px"
        label-position="right"
        status-icon
        class="tm-form"
      >
        <!-- 基础信息：启停开关收进卡片头，不与一组输入项抢同一条视觉线 -->
        <section class="tm-card">
          <header class="tm-card-head">
            <span class="tm-card-icon" aria-hidden="true">
              <el-icon><Box /></el-icon>
            </span>
            <div class="tm-card-copy">
              <h3 class="tm-card-title">{{ t('thingModel.sectionBasic') }}</h3>
              <p class="tm-card-desc">{{ t('thingModel.modelSectionBasicDesc') }}</p>
            </div>
            <div v-if="isModelEdit" class="tm-enable">
              <span class="tm-enable-label">{{ t('common.status') }}</span>
              <el-switch v-model="modelForm.status" :active-value="1" :inactive-value="0" />
            </div>
          </header>
          <div class="tm-card-body">
            <div class="tm-grid">
              <!-- 模型标识与名称是身份信息，各自独占一行：并排时半宽输入框
                   放不下示例文案，也压不住名称的 100 字上限 -->
              <el-form-item :label="t('thingModel.code')" prop="code" class="tm-span-2">
                <el-input
                  v-model="modelForm.code"
                  :placeholder="t('thingModel.codePlaceholder')"
                  maxlength="64"
                />
                <!-- 编辑态允许改名，但改名会打断外部引用：改动后提示切到警示色，
                     打开弹窗时先不吓人一跳 -->
                <div
                  class="tm-field-hint is-block"
                  :class="{ 'is-warning': modelCodeRenamed }"
                >
                  {{
                    isModelEdit
                      ? t('thingModel.codeEditHint')
                      : t('thingModel.codeHint')
                  }}
                </div>
              </el-form-item>
              <el-form-item :label="t('common.name')" prop="name" class="tm-span-2">
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
              <el-form-item :label="t('common.description')" prop="description" class="tm-span-2">
                <el-input
                  v-model="modelForm.description"
                  type="textarea"
                  :rows="3"
                  :placeholder="t('thingModel.descriptionPlaceholder')"
                  maxlength="500"
                  show-word-limit
                />
              </el-form-item>
            </div>
          </div>
        </section>
      </el-form>

      <template #footer>
        <div class="tm-dialog-footer">
          <p class="tm-footer-hint">{{ t('thingModel.modelFormFooterHint') }}</p>
          <div class="tm-footer-actions">
            <el-button @click="modelDialogVisible = false">{{ t('common.cancel') }}</el-button>
            <el-button type="primary" :loading="modelSubmitting" @click="handleSubmitModel">
              {{ t('common.save') }}
            </el-button>
          </div>
        </div>
      </template>
    </el-dialog>

    <ImportResultDialog v-model:visible="modelImportResultVisible" :result="modelImportResult" />
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconArrowLeft as ArrowLeft,
  IconBox as Box,
  IconChevronRight as ChevronRight,
  IconCopy as CopyDocument,
  IconCopyPlus as CopyPlus,
  IconDownload as Download,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  copyThingModel,
  createThingModel,
  deleteThingModel,
  downloadThingModelTemplate,
  getThingModelById,
  getThingModelList,
  importThingModels,
  updateThingModel,
} from '@/api/modules/thingModel'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import PropertiesPanel from './properties/PropertiesPanel.vue'
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
  const custom = [...new Set(records.value.map((r) => r.category).filter((code) => code && !CATEGORY_CODES.includes(code)))]
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

const statusLabel = (row) =>
  row.status === 1 ? t('common.enabled') : t('common.enableDisable.off')

async function copyCode(code) {
  try {
    await navigator.clipboard.writeText(String(code))
    ElMessage.success(t('thingModel.codeCopied'))
  } catch {
    ElMessage.error(t('thingModel.copyFailed'))
  }
}

// ---------- 列表 ----------
const route = useRoute()
const router = useRouter()

// 筛选与分页写回地址栏：刷新页面、分享链接时能回到同一屏结果。
// 选中模型的 id 也写回 URL，刷新/前进后退都能回到同一个模型。
// 从属性深链页回来时 URL 为空，用 sessionStorage 恢复最近一次的筛选。
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
const records = ref([])
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

const hasQuery = computed(() => !!(queryForm.keyword || queryForm.category))
const displayTotal = computed(() =>
  loading.value && records.value.length === 0 ? '—' : total.value,
)

// ---------- 选中模型 ----------
const selectedModel = ref(null)
const propertyTotal = ref(0)
const propertyLoading = ref(true)
const hydrating = ref(false)
const initialized = ref(false)

const selectedOnPage = computed(() => records.value.some((row) => isSelected(row)))
const propertiesTotalText = computed(() =>
  propertyLoading.value && propertyTotal.value === 0
    ? '—'
    : `${propertyTotal.value} ${t('thingModel.totalPropertiesUnit')}`,
)

function isSelected(row) {
  return !!selectedModel.value && String(selectedModel.value.id) === String(row?.id)
}

// keepRouteModelId：列表刷新时 URL 里待恢复的 modelId 还没选中（首屏/深链），
// 不能被一次筛选同步提前抹掉。
function syncQuery({ keepRouteModelId = false } = {}) {
  const query = {}
  if (queryForm.keyword) query.keyword = queryForm.keyword
  if (queryForm.category) query.category = queryForm.category
  if (currentPage.value > 1) query.page = String(currentPage.value)
  if (pageSize.value !== 10) query.size = String(pageSize.value)
  const modelId = selectedModel.value
    ? String(selectedModel.value.id)
    : keepRouteModelId && route.query.modelId
      ? String(route.query.modelId)
      : ''
  if (modelId) query.modelId = modelId
  try {
    sessionStorage.setItem(
      LIST_QUERY_KEY,
      JSON.stringify({
        ...(queryForm.keyword ? { keyword: queryForm.keyword } : {}),
        ...(queryForm.category ? { category: queryForm.category } : {}),
        ...(currentPage.value > 1 ? { page: String(currentPage.value) } : {}),
        ...(pageSize.value !== 10 ? { size: String(pageSize.value) } : {}),
      }),
    )
  } catch {
    // 隐私模式等场景下 sessionStorage 不可用，URL 同步仍然生效
  }
  router.replace({ query })
}

function setSelected(row, { writeUrl = true } = {}) {
  selectedModel.value = row
  propertyTotal.value = 0
  propertyLoading.value = true
  if (writeUrl) syncQuery()
}

// URL 里的模型不在当前页时按 id 补一次详情：列表行已含模型完整字段，
// 足以渲染右侧头部，不必等翻到那一页。
async function hydrateModel(id) {
  if (!id || hydrating.value) return
  hydrating.value = true
  try {
    const detail = await getThingModelById(id)
    if (detail && detail.id) {
      setSelected(detail, { writeUrl: false })
    } else {
      ElMessage.warning(t('thingModel.hydrateFailed'))
      selectedModel.value = null
      syncQuery()
    }
  } catch {
    ElMessage.warning(t('thingModel.hydrateFailed'))
    selectedModel.value = null
    syncQuery()
  } finally {
    hydrating.value = false
  }
}

// 首屏只做一次自动选中：有 ?modelId 优先恢复；否则宽屏选第一个，窄屏留列表。
// 宽度未知（ResizeObserver 还没回调）时先不选，避免窄屏一进来就被详情顶掉列表。
async function maybeAutoSelect() {
  if (initialized.value || loading.value) return
  const id = route.query.modelId ? String(route.query.modelId) : ''
  if (id) {
    const inPage = records.value.find((row) => String(row.id) === id)
    initialized.value = true
    if (inPage) {
      setSelected(inPage, { writeUrl: false })
      return
    }
    await hydrateModel(id)
    if (!selectedModel.value && !singleColumn.value && records.value.length) {
      setSelected(records.value[0])
    }
    return
  }
  if (splitWidth.value === null) return
  initialized.value = true
  if (!singleColumn.value && records.value.length) {
    setSelected(records.value[0])
  }
}

async function fetchList() {
  syncQuery({ keepRouteModelId: true })
  loading.value = true
  try {
    const res = await getThingModelList({
      page: currentPage.value,
      size: pageSize.value,
      keyword: queryForm.keyword,
      category: queryForm.category,
    })
    records.value = res?.records ?? []
    total.value = res?.total ?? 0
    // 选中的那个若仍在结果里，用最新行刷新头部（状态/名称/属性数可能已变）
    if (selectedModel.value) {
      const fresh = records.value.find((row) => isSelected(row))
      if (fresh) selectedModel.value = fresh
    }
  } catch {
    // 错误提示由 axios 拦截器统一给出
    records.value = []
    total.value = 0
  } finally {
    // 必须先落下 loading 再判首次选中：maybeAutoSelect 以「列表已就绪」为前提
    loading.value = false
    maybeAutoSelect()
  }
}

// 换一屏数据就把列表滚回顶部：分页器在列表底部，翻页时人正处在底部。
function scrollListToTop() {
  if (listScrollRef.value) listScrollRef.value.scrollTop = 0
  focusedIndex.value = -1
}

function handleSearch() {
  currentPage.value = 1
  scrollListToTop()
  fetchList()
}

function handleReset() {
  queryForm.keyword = ''
  queryForm.category = ''
  currentPage.value = 1
  scrollListToTop()
  fetchList()
}

function handleCurrentChange(page) {
  currentPage.value = page
  scrollListToTop()
  fetchList()
}

// ---------- 卡片选择 / 键盘 ----------
const focusedIndex = ref(-1)
const listScrollRef = ref(null)
const detailHeadingRef = ref(null)
const activeFocusIndex = computed(() =>
  focusedIndex.value >= 0 && focusedIndex.value < records.value.length ? focusedIndex.value : 0,
)

function handleCardClick(row, { focusDetail = false } = {}) {
  focusedIndex.value = records.value.indexOf(row)
  setSelected(row)
  if (focusDetail && singleColumn.value) {
    nextTick(() => detailHeadingRef.value?.focus())
  }
}

function focusCard(index) {
  if (!records.value.length) return
  focusedIndex.value = Math.min(Math.max(index, 0), records.value.length - 1)
  nextTick(() => {
    const cards = listScrollRef.value?.querySelectorAll('.dw-card')
    const el = cards?.[focusedIndex.value]
    el?.focus()
    el?.scrollIntoView({ block: 'nearest' })
  })
}

function moveFocus(delta) {
  focusCard(focusedIndex.value < 0 ? 0 : focusedIndex.value + delta)
}

function backToList() {
  selectedModel.value = null
  propertyTotal.value = 0
  propertyLoading.value = false
  syncQuery()
  nextTick(() => focusCard(activeFocusIndex.value))
}

// 浏览器前进/后退与手改 URL：与当前选中相同就忽略，避免自触发循环
watch(
  () => route.query.modelId,
  (value) => {
    const id = value ? String(value) : ''
    const current = selectedModel.value ? String(selectedModel.value.id) : ''
    if (id === current) return
    if (!id) {
      selectedModel.value = null
      propertyTotal.value = 0
      propertyLoading.value = false
      return
    }
    const inPage = records.value.find((row) => String(row.id) === id)
    if (inPage) {
      setSelected(inPage, { writeUrl: false })
      return
    }
    hydrateModel(id)
  },
)

// ---------- 物模型表单 ----------
const modelDialogVisible = ref(false)
const modelSubmitting = ref(false)
const modelCopying = ref(false)
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

// 编辑态用来判断模型标识是否真的被改名：只有改动后才把提示切到警示色。
const modelOriginalCode = ref('')
const modelCodeRenamed = computed(
  () => isModelEdit.value && modelForm.code.trim() !== modelOriginalCode.value,
)

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
  modelOriginalCode.value = ''
  resetModelForm()
  modelDialogVisible.value = true
  nextTick(() => modelFormRef.value?.clearValidate())
}

function handleEdit(row) {
  modelOriginalCode.value = row.code ?? ''
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

// 复制只带基础信息（属性不随行，点位绑定是 1:1 的）：生成后直接选中副本，
// 方便接着补属性。副本可能落在别的页/被当前筛选挡掉，按 id 补一次详情兜底。
async function handleCopyModel(row) {
  if (!row || modelCopying.value) return
  modelCopying.value = true
  try {
    const data = await copyThingModel(row.id)
    ElMessage.success(t('thingModel.copyModelSuccess', { name: data?.name || '' }))
    await fetchList()
    const fresh = data?.id
      ? records.value.find((item) => String(item.id) === String(data.id))
      : null
    if (fresh) setSelected(fresh)
    else if (data?.id) hydrateModel(String(data.id))
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    modelCopying.value = false
  }
}

function handleModelDialogClosed() {
  modelOriginalCode.value = ''
  modelFormRef.value?.resetFields()
}

async function handleSubmitModel() {
  const valid = await modelFormRef.value?.validate().catch(() => false)
  if (!valid) return

  const isEdit = isModelEdit.value
  const editedId = isEdit ? String(modelForm.id) : ''
  modelSubmitting.value = true
  try {
    const payload = {
      name: modelForm.name,
      category: modelForm.category,
      version: modelForm.version,
      description: modelForm.description,
    }
    if (isEdit) {
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
    await fetchList()
    // 编辑的是当前选中的模型：按 id 对齐右栏头部，改完立即看到新值
    if (editedId && selectedModel.value && String(selectedModel.value.id) === editedId) {
      const fresh = records.value.find((row) => String(row.id) === editedId)
      if (fresh) selectedModel.value = fresh
      else hydrateModel(editedId)
    }
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    modelSubmitting.value = false
  }
}

// ---------- 删除后的选中接力 ----------
// 删的是当前选中的模型：删完按原位置选下一个；当前页删空且不是第一页则退一页。
async function reloadAfterModelDelete(deletedIds, preferIndex) {
  const deletingSelected = selectedModel.value
    && deletedIds.has(String(selectedModel.value.id))
  const pageEmptied = records.value.length > 0
    && records.value.every((row) => deletedIds.has(String(row.id)))

  if (pageEmptied && currentPage.value > 1) {
    currentPage.value -= 1
    scrollListToTop()
  }
  await fetchList()

  if (!deletingSelected) return
  if (!records.value.length) {
    selectedModel.value = null
    propertyTotal.value = 0
    propertyLoading.value = false
    syncQuery()
    return
  }
  const nextIndex = Math.min(Math.max(preferIndex, 0), records.value.length - 1)
  setSelected(records.value[nextIndex])
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

  const preferIndex = records.value.findIndex((item) => String(item.id) === String(row.id))
  try {
    await deleteThingModel(row.id)
    ElMessage.success(t('thingModel.deleteSuccess'))
    await reloadAfterModelDelete(new Set([String(row.id)]), preferIndex)
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
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
  refresh: () => {
    // 导入可能来自任意页：回到第一页重新取
    currentPage.value = 1
    scrollListToTop()
    fetchList()
  },
})

// ---------- 响应式 ----------
// 判据取**工作区的真实宽度**而不是窗口宽度：侧栏折叠/拖宽后同一窗口会多出上百像素。
// 低于 1080 主从两栏放不下，切成「列表 / 详情」二选一全宽。
const splitRef = ref(null)
const splitWidth = ref(null)
const singleColumn = computed(() => splitWidth.value !== null && splitWidth.value < 1080)
let splitObserver = null

// ---------- 初始化 ----------
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && splitRef.value) {
    splitObserver = new ResizeObserver(([entry]) => {
      splitWidth.value = entry.contentRect.width
      maybeAutoSelect()
    })
    splitObserver.observe(splitRef.value)
  }
  fetchList()
})

onBeforeUnmount(() => {
  splitObserver?.disconnect()
})
</script>

<style scoped>
/* 整页定高：页头之外全部交给主从两栏，各自滚动（同设备对象 / protocol-form 的约定） */
.dw-page {
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--header-height, 60px) - 48px);
}

.dw-head {
  flex: none;
}

/* 标题与图标同一视觉组：图标底座把「物模型」从纯文字标题抬成有身份的页面入口 */
.dw-title-main {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.dw-title-mark {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  font-size: 20px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.05), inset 0 1px 0 rgba(255, 255, 255, 0.9);
}

/* 右侧计数与动作不许被标题挤窄：主区（标题/说明）先让位、说明自行换行，
   三个模型级动作才能始终排在同一行 */
.dw-head-aside {
  display: flex;
  flex: none;
  flex-direction: column;
  align-items: flex-end;
  gap: 12px;
}

/* 计数从裸文字收进一枚浅色小卡：读数与右侧动作在纵向上分成「看」和「做」两层 */
.dw-stat-card {
  padding: 7px 14px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border: 1px solid var(--line);
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), inset 0 1px 0 #fff;
}

.dw-stat-card .iot-stat-num {
  font-size: 20px;
}

.dw-head-actions {
  display: flex;
  flex-wrap: nowrap;
  justify-content: flex-end;
  gap: 8px;
}

/* 页头按钮比工具栏更重一档：9px 圆角与工具栏同源，主按钮带一点下沉阴影 */
.dw-head-actions :deep(.el-button) {
  border-radius: 9px;
  font-weight: 500;
}

.dw-head-actions :deep(.el-button--primary) {
  box-shadow: 0 1px 2px rgba(37, 99, 235, 0.22), 0 8px 18px -10px rgba(37, 99, 235, 0.55);
}

/* ---------- 主从两栏 ---------- */
.dw-split {
  display: flex;
  flex: 1;
  gap: 16px;
  min-height: 0;
  overflow: hidden;
}

/* 两块工作区共用一层「浮起」的观感：贴边仍是发丝线，远处用大偏移低透明度的
   投影把面板从画布上托起，层次靠光而不是靠更深的边框 */
.dw-split > .iot-panel {
  margin-bottom: 0;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 18px 44px -34px rgba(15, 23, 42, 0.32);
}

.dw-list {
  display: flex;
  flex: 0 0 320px;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.dw-detail {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

/* 左栏工具条：搜索独占一行，分类与动作另起一行，320px 里不挤成一团 */
.dw-list-toolbar {
  padding: 12px;
}

.dw-list-toolbar .iot-toolbar-filters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}

.dw-list-toolbar .iot-search {
  width: 100%;
}

.tm-list-filter-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  width: 100%;
}

.tm-category-select {
  flex: 1 1 auto;
  min-width: 0;
}

.dw-list-scroll {
  flex: 1;
  min-height: 0;
  padding: 8px;
  overflow-y: auto;
  overscroll-behavior: contain;
}

.dw-list-pager {
  flex: none;
  justify-content: center;
  padding: 8px 12px;
  background: linear-gradient(180deg, #fbfcfe 0%, #ffffff 100%);
  border-top: 1px solid var(--line-soft);
}

.dw-list-note {
  margin: 2px 4px 8px;
  padding: 8px 10px;
  font-size: 12px;
  line-height: 1.5;
  color: #92400e;
  background: #fffbeb;
  border-radius: 8px;
}

/* ---------- 模型卡片 ----------
   静止时是一张没有边框的纯文字行；悬停/选中才补上描边、浅渐变与投影，
   列表因此保持安静，被指向的那一张才浮起来。 */
.dw-card {
  position: relative;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 12px 11px 14px;
  border: 1px solid transparent;
  border-radius: 12px;
  cursor: pointer;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    box-shadow 0.16s ease;
}

.dw-card + .dw-card {
  margin-top: 4px;
}

.dw-card:hover {
  background: linear-gradient(180deg, #ffffff 0%, #f8fbff 100%);
  border-color: var(--line);
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04), 0 10px 24px -20px rgba(15, 23, 42, 0.35);
}

.dw-card.is-active {
  background: linear-gradient(180deg, #ffffff 0%, var(--brand-soft) 100%);
  border-color: var(--brand-line);
  box-shadow: 0 1px 2px rgba(37, 99, 235, 0.08), 0 10px 24px -22px rgba(37, 99, 235, 0.55);
}

/* 选中态用压在左缘的短竖条，不靠整块深色抢过右栏的读数 */
.dw-card.is-active::before {
  content: '';
  position: absolute;
  top: 10px;
  bottom: 10px;
  left: 0;
  width: 3px;
  background: var(--brand);
  border-radius: 3px;
}

.dw-card:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.5);
  outline-offset: -1px;
}

.dw-card-dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.dw-card-dot.is-enabled {
  background: #10b981;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.14);
}

.dw-card-dot.is-disabled {
  background: #94a3b8;
}

.dw-card-main {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.dw-card-name {
  overflow: hidden;
  font-size: 13.5px;
  font-weight: 600;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-card-meta {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--ink-4);
}

.dw-card-code {
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-card-proto {
  overflow: hidden;
  max-width: 56%;
  padding: 1px 6px;
  font-size: 10.5px;
  line-height: 15px;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
  background: #f1f5f9;
  border-radius: 5px;
}

/* 属性数是挑选模型的直接依据，用等宽数字常驻在卡片右缘 */
.tm-card-count {
  flex: none;
  font-size: 11.5px;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

/* 进入提示：默认让位给文字，指向或被选中时才滑入，不在十张卡片上重复喊「可点」 */
.dw-card-go {
  display: grid;
  flex: none;
  place-items: center;
  width: 18px;
  height: 18px;
  font-size: 14px;
  color: var(--ink-4);
  opacity: 0;
  transform: translateX(-3px);
  transition: opacity 0.16s ease, transform 0.16s ease, color 0.16s ease;
}

.dw-card:hover .dw-card-go,
.dw-card.is-active .dw-card-go,
.dw-card:focus-visible .dw-card-go {
  color: var(--brand);
  opacity: 1;
  transform: none;
}

/* 触屏没有悬停：选中态就是可进入的提示，未选中的卡片不占这 18px */
@media (hover: none) {
  .dw-card-go {
    display: none;
  }
}

/* ---------- 右栏详情头 ---------- */
.dw-detail-head {
  display: flex;
  flex: none;
  align-items: center;
  gap: 12px;
  padding: 14px 18px;
  background: linear-gradient(180deg, #ffffff 0%, #fbfcfe 100%);
  border-bottom: 1px solid var(--line-soft);
}

.dw-back {
  flex: none;
}

.dw-detail-identity {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

/* 与页头同一种图标底座，只是小一档：进到详情仍能认出「这是同一个模型」 */
.dw-identity-mark {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  font-size: 20px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 12px;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.9);
}

.dw-detail-copy {
  min-width: 0;
}

.dw-detail-name {
  margin: 0;
  overflow: hidden;
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-detail-name:focus {
  outline: none;
}

.dw-detail-name:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.5);
  outline-offset: 2px;
  border-radius: 4px;
}

.dw-detail-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
  margin-top: 5px;
  font-size: 12px;
  color: var(--ink-4);
}

.dw-code-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
  padding: 0;
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: var(--ink-3);
  background: none;
  border: 0;
  cursor: pointer;
}

/* 编码是一长串等宽字符：宁可截断并悬停看全名，也不要按短横线折成三行 */
.dw-code-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dw-code-btn:hover {
  color: var(--brand);
}

.dw-code-btn .el-icon {
  font-size: 12px;
  opacity: 0.55;
}

.dw-code-btn:hover .el-icon {
  opacity: 1;
}

.dw-proto-chip {
  overflow: hidden;
  max-width: 220px;
  padding: 1px 8px;
  font-size: 11.5px;
  line-height: 18px;
  color: var(--ink-2);
  text-overflow: ellipsis;
  white-space: nowrap;
  background: #f1f5f9;
  border: 1px solid #e8edf4;
  border-radius: 6px;
}

.tm-version-chip {
  padding: 1px 8px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 18px;
  color: var(--ink-3);
  background: #f8fafc;
  border: 1px solid var(--line-soft);
  border-radius: 6px;
  white-space: nowrap;
}

.dw-points-stat {
  font-variant-numeric: tabular-nums;
}

/* 图标动作收进一枚带边的按钮座：与其他白面/描边元素同一种质感，
   不再裸浮在渐变头上；按钮仍只在悬停时染色 */
.dw-detail-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 2px;
  padding: 2px;
  background: #ffffff;
  border: 1px solid var(--line);
  border-radius: 10px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.dw-detail-actions :deep(.iot-row-btn.el-button) {
  width: 30px;
  height: 30px;
  margin: 0;
  padding: 0;
  color: var(--ink-4);
  border-radius: 8px;
}

.dw-detail-actions :deep(.iot-row-btn.el-button .el-icon) {
  font-size: 16px;
}

.dw-detail-actions :deep(.iot-row-btn.el-button:hover) {
  color: var(--brand);
  background: var(--brand-soft);
}

.dw-detail-actions :deep(.iot-row-btn.el-button.el-button--danger:hover) {
  color: var(--danger);
  background: #fff1f2;
}

.dw-detail-body {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.dw-detail-body > * {
  flex: 1;
  min-width: 0;
}

.dw-detail-empty {
  justify-content: center;
  height: 100%;
}

/* ---------- 窄屏：列表 / 详情二选一 ---------- */
.dw-split.is-single {
  display: block;
}

.dw-split.is-single > .dw-list,
.dw-split.is-single > .dw-detail {
  height: 100%;
}

.dw-split.is-single.is-list-view > .dw-detail {
  display: none;
}

.dw-split.is-single.is-detail-view > .dw-list {
  display: none;
}

/* 与主从断点同一档（1080）：再窄下去页头右侧会被标题挤成两行按钮，
   不如整块落到标题下方，旁白与动作各占一行 */
@media (max-width: 1080px) {
  .dw-head {
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  /* 叠放后计数与动作并成一排：高度省一行，动作仍从左侧起读 */
  .dw-head-aside {
    flex-direction: row;
    flex-wrap: wrap;
    align-items: center;
  }

  .dw-head-actions {
    justify-content: flex-start;
  }
}

/* 手机级极窄屏：三个动作一行确实放不下时允许换行，宁可多占一行也不要横向溢出 */
@media (max-width: 640px) {
  .dw-head-actions {
    flex-wrap: wrap;
  }
}

/* 详情头按**面板自身宽度**收缩，而不是视口宽度：侧栏展开/折叠、主从分栏，
   真正决定头部空间的都是右栏的宽度。600px 以下让图标底座与返回文字让位，
   把宽度留给模型名与动作。 */
.dw-detail {
  container-type: inline-size;
}

@container (max-width: 600px) {
  .dw-detail-head {
    gap: 10px;
    padding: 12px 14px;
  }

  .dw-back-text,
  .dw-identity-mark {
    display: none;
  }

  .dw-detail-actions :deep(.iot-row-btn.el-button) {
    width: 28px;
    height: 28px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dw-page *,
  .dw-page *::before,
  .dw-page *::after {
    transition: none !important;
  }
}

/* ---------- 物模型弹窗：页头三级递进 ---------- */
.tm-dialog-head {
  padding-right: 28px;
}

.tm-dialog-eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 8px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--ink-3);
}

.tm-dialog-eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.tm-dialog-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.tm-dialog-sub {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

/* ---------- 分区卡片 ---------- */
.tm-form {
  padding-top: 2px;
}

.tm-card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.tm-card-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--line-soft);
}

.tm-card-icon {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  font-size: 16px;
  color: var(--brand);
  background: linear-gradient(180deg, var(--brand-tint) 0%, var(--brand-soft) 100%);
  border: 1px solid var(--brand-line);
  border-radius: 10px;
}

.tm-card-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;
}

.tm-card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.tm-card-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* 启停是整张卡的开关，与标题同排靠右 */
.tm-enable {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.tm-enable-label {
  font-size: 12.5px;
  color: var(--ink-3);
}

.tm-card-body {
  padding: 20px 22px;
}

.tm-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 20px;
  margin-bottom: -16px;
}

.tm-grid :deep(.el-form-item) {
  margin-bottom: 16px;
}

/* 一侧字段带说明文字时，内容区会被网格拉高；两侧都钉到顶部才能齐平 */
.tm-grid :deep(.el-form-item__content) {
  align-items: flex-start;
  align-content: flex-start;
}

.tm-grid .tm-span-2 {
  grid-column: span 2;
}

.tm-field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.tm-field-hint.is-block {
  flex: 0 0 100%;
}

/* 标识被改名时才点亮：打开弹窗先按普通提示显示 */
.tm-field-hint.is-warning {
  color: var(--warning);
}

/* ---------- 页脚：左侧常驻后果说明，右侧动作 ---------- */
.tm-dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.tm-footer-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.tm-footer-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 10px;
}

@media (max-width: 720px) {
  .tm-grid {
    grid-template-columns: 1fr;
  }

  .tm-grid .tm-span-2 {
    grid-column: auto;
  }

  /* 窄屏也让页脚保持一行：说明与动作挤不下时整组换行，而不是把弹窗顶出视口 */
  .tm-dialog-footer {
    flex-wrap: wrap;
    align-items: center;
  }
}
</style>
