<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
物模型属性的主编辑界面（从物模型列表钻取进入）。
表格宽、停留久，所以独立成页；绑定点位仍是页内抽屉，属性表单仍是弹窗，
这样从「列表 → 属性页 → 点位抽屉 / 属性弹窗」最多两层浮层。
-->
<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <button type="button" class="iot-back" @click="goBack">
            <el-icon><ArrowLeft /></el-icon>
            {{ t('thingModel.backToModels') }}
          </button>
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.industrial') }}
          </p>
        </div>
        <div class="iot-title-row">
          <h1 class="iot-title">{{ headerTitle }}</h1>
          <span class="iot-head-sub">{{ t('thingModel.propertiesSubtitle') }}</span>
        </div>
      </div>
      <div class="iot-head-aside">
        <!-- 模型的稳定身份放在页头：它在工具栏里会挤压搜索框，且不属于筛选项 -->
        <span v-if="modelCode" class="iot-mono thing-model-code" :title="modelCode">
          {{ modelCode }}
        </span>
        <el-tag v-if="modelCategory" size="small" effect="plain">
          {{ categoryLabel(modelCategory) }}
        </el-tag>
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('thingModel.totalPropertiesUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="propertyKeyword"
            class="iot-search"
            :placeholder="t('thingModel.propertySearchPlaceholder')"
            clearable
            @keyup.enter="handlePropertySearch"
            @clear="handlePropertySearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-button type="primary" @click="handlePropertySearch">{{ t('common.search') }}</el-button>
          <el-button @click="handlePropertyReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <el-button :icon="Download" @click="handlePropertyTemplateDownload">
            {{ t('common.downloadTemplate') }}
          </el-button>
          <el-button :icon="Upload" :loading="propertyImporting" @click="handlePropertyImport">
            {{ t('common.import') }}
          </el-button>
          <el-button type="primary" :icon="Plus" @click="handleAddProperty">
            {{ t('thingModel.addProperty') }}
          </el-button>
          <input
            ref="propertyFileInputRef"
            type="file"
            accept=".xlsx"
            class="hidden"
            @change="handlePropertyFileChange"
          />
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="properties"
        v-loading="propertyLoading"
        row-key="id"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />
        <!-- 标识符与物模型编码同一个身份锚点：小图标底座让这一列在滚动中可被一眼拾取 -->
        <el-table-column :label="t('thingModel.identifier')" min-width="220">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Variable /></el-icon>
              </span>
              <span class="iot-mono" :title="row.identifier">{{ row.identifier }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.name')" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="tp-name" :title="row.name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thingModel.access.label')" width="110" align="center">
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
        <!-- 属性与点位是 1 对 1，点位数恒为 0/1，没有信息量；直接展示绑定的
             点位是谁（设备名称.点位名称），点击仍打开点位明细抽屉。
             存量多绑数据保留 +N 提示，保存后即收敛为一条。 -->
        <el-table-column :label="t('thingModel.boundPoint')" min-width="240">
          <template #default="{ row }">
            <div v-if="row.pointCount > 0" class="tp-bound-point-cell">
              <button
                type="button"
                class="tp-bound-point"
                :title="boundPointLabel(row)"
                @click="openPropertyPoints(row)"
              >
                {{ boundPointLabel(row) }}
              </button>
              <span
                v-if="row.pointCount > 1"
                class="tp-bound-point-extra"
                :title="t('thingModel.bindPointsLegacyPruned', { count: row.pointCount })"
              >
                +{{ row.pointCount - 1 }}
              </span>
            </div>
            <span v-else class="iot-cell-muted">{{ t('thingModel.pointUnbound') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('thingModel.unit')" width="96" align="center">
          <template #default="{ row }">
            <span :class="row.unit ? '' : 'iot-cell-muted'">{{ row.unit || '—' }}</span>
          </template>
        </el-table-column>
        <!-- 排序值是内部配置项，放在单位之后、状态之前：既不抢行首的身份信息，
             也仍在编辑/删除前的同一视觉区里 -->
        <el-table-column
          :label="t('thingModel.sortOrder')"
          width="88"
          align="center"
          class-name="iot-col-num"
        >
          <template #default="{ row }">
            <span :class="row.sortOrder == null ? 'iot-cell-muted' : ''">
              {{ row.sortOrder ?? '—' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.action')" width="120" align="center" fixed="right">
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
              <!-- 属性级的「控制下发」入口：配置与现场动作分离，点击后进入
                   按设备分行的控制页，每台设备单独确认、单独写入。 -->
              <el-tooltip
                :content="
                  canControlProperty(row)
                    ? t('thingModel.propertyControlEntry')
                    : propertyControlDisabledReason(row)
                "
                placement="top"
              >
                <span>
                  <el-button
                    class="iot-row-btn"
                    type="primary"
                    link
                    size="small"
                    :icon="Send"
                    :disabled="!canControlProperty(row)"
                    :aria-label="t('thingModel.propertyControlEntry')"
                    @click="openPropertyControl(row)"
                  />
                </span>
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
            <template v-if="hasPropertyQuery">
              <p class="iot-empty-title">
                {{ t('thingModel.propertyEmptySearch', { keyword: propertyKeyword.trim() }) }}
              </p>
              <el-button @click="handlePropertyReset">{{ t('common.reset') }}</el-button>
            </template>
            <template v-else>
              <p class="iot-empty-title">{{ t('thingModel.propertyEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.propertyEmptyHint') }}</p>
              <el-button type="primary" :icon="Plus" @click="handleAddProperty">
                {{ t('thingModel.addProperty') }}
              </el-button>
            </template>
          </div>
        </template>
      </el-table>

      <div v-if="propertyTotal > 0" class="iot-pager">
        <el-pagination
          v-model:current-page="propertyPage"
          v-model:page-size="propertyPageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="propertyTotal"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @size-change="handlePropertySizeChange"
          @current-change="handlePropertyCurrentChange"
        />
      </div>
    </section>

    <!-- ========== 属性绑定点位抽屉 ========== -->
    <el-drawer
      v-model="propertyPointsDrawerVisible"
      class="thing-property-points-drawer"
      size="min(820px, 96vw)"
      :title="propertyPointsTitle"
      @closed="handlePropertyPointsClosed"
    >
      <div class="thing-drawer-body">
        <div class="thing-drawer-head">
          <p class="thing-drawer-sub">{{ t('thingModel.propertyPointsSubtitle') }}</p>
        </div>
        <el-table
          class="iot-table is-compact"
          :data="propertyPoints"
          v-loading="propertyPointsLoading"
          row-key="addressId"
          max-height="60vh"
        >
          <el-table-column :label="t('thingModel.pointDevice')" min-width="180">
            <template #default="{ row }">
              <div class="thing-point-device">
                <span class="thing-point-device-name" :title="row.deviceName">
                  {{ row.deviceName }}
                </span>
                <span class="thing-point-device-code" :title="row.deviceCode">
                  {{ row.deviceCode }}
                </span>
              </div>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.pointAddress')" min-width="130">
            <template #default="{ row }">
              <span class="iot-mono" :title="row.name">{{ row.name }}</span>
            </template>
          </el-table-column>
          <el-table-column
            :label="t('thingModel.pointLabel')"
            min-width="140"
            show-overflow-tooltip
          >
            <template #default="{ row }">{{ row.label || '—' }}</template>
          </el-table-column>
          <el-table-column :label="t('thingModel.pointDataType')" width="120" align="center">
            <template #default="{ row }">
              <span :title="row.dataType">{{ row.commonDataType || row.dataType }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('thingModel.access.label')" width="96" align="center">
            <template #default="{ row }">
              <el-tag
                size="small"
                effect="plain"
                :type="row.rwPermission === 'R' ? 'info' : 'warning'"
              >
                {{ accessLabel(row.rwPermission) }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.status')" width="90" align="center">
            <template #default="{ row }">
              <el-tag size="small" effect="plain" :type="row.status === 1 ? 'success' : 'info'">
                {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
              </el-tag>
            </template>
          </el-table-column>
          <template #empty>
            <div class="iot-empty">
              <span class="iot-empty-mark" aria-hidden="true">
                <el-icon><ListDetails /></el-icon>
              </span>
              <p class="iot-empty-title">{{ t('thingModel.propertyPointsEmptyTitle') }}</p>
              <p class="iot-empty-hint">{{ t('thingModel.propertyPointsEmptyHint') }}</p>
            </div>
          </template>
        </el-table>
      </div>
    </el-drawer>

    <!-- ========== 属性表单 ==========
         版式与点位表单 / 触发规则表单同一套：页头三级递进，正文按
         「基础信息 / 点位绑定」两张卡片分组，页脚左侧常驻一句后果说明。 -->
    <el-dialog
      v-model="propertyDialogVisible"
      class="iot-dialog thing-property-dialog"
      :title="isPropertyEdit ? t('thingModel.editPropertyTitle') : t('thingModel.addPropertyTitle')"
      width="min(840px, 94vw)"
      append-to-body
      :close-on-click-modal="false"
      @closed="handlePropertyDialogClosed"
    >
      <template #header>
        <div class="tp-dialog-head">
          <p class="tp-dialog-eyebrow">
            <span class="tp-dialog-eyebrow-dot" aria-hidden="true" />
            {{ isPropertyEdit ? t('thingModel.formEyebrowEdit') : t('thingModel.formEyebrowAdd') }}
          </p>
          <div class="tp-dialog-title-row">
            <h2 class="tp-dialog-title">
              {{ isPropertyEdit ? t('thingModel.editPropertyTitle') : t('thingModel.addPropertyTitle') }}
            </h2>
            <span v-if="modelName" class="iot-chip tp-dialog-model" :title="modelName">
              {{ modelName }}
            </span>
          </div>
          <p class="tp-dialog-sub">{{ t('thingModel.formLead') }}</p>
        </div>
      </template>

      <el-form
        ref="propertyFormRef"
        :model="propertyForm"
        :rules="propertyRules"
        label-width="96px"
        label-position="right"
        status-icon
        class="tp-form"
      >
        <!-- 基础信息：启停开关收进卡片头，不与一组输入项抢同一条视觉线 -->
        <section class="tp-card">
          <header class="tp-card-head">
            <span class="tp-card-icon" aria-hidden="true">
              <el-icon><IdBadge /></el-icon>
            </span>
            <div class="tp-card-copy">
              <h3 class="tp-card-title">{{ t('thingModel.sectionBasic') }}</h3>
              <p class="tp-card-desc">{{ t('thingModel.sectionBasicDesc') }}</p>
            </div>
            <div v-if="isPropertyEdit" class="tp-enable">
              <span class="tp-enable-label">{{ t('common.status') }}</span>
              <el-switch
                v-model="propertyForm.status"
                :active-value="1"
                :inactive-value="0"
              />
            </div>
          </header>
          <div class="tp-card-body">
            <div class="tp-grid">
              <!-- 标识符是下游映射的键，独占一行；编辑态可改名，改动时用警示色
                   提醒下游引用要同步更新 -->
              <el-form-item
                :label="t('thingModel.identifier')"
                prop="identifier"
                class="tp-span-2"
              >
                <el-input
                  v-model="propertyForm.identifier"
                  :placeholder="t('thingModel.identifierPlaceholder')"
                  maxlength="64"
                />
                <div
                  class="tp-field-hint is-block"
                  :class="{ 'is-warning': identifierRenamed }"
                >
                  {{
                    isPropertyEdit
                      ? t('thingModel.identifierEditHint')
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
                <el-input-number
                  v-model="propertyForm.sortOrder"
                  class="tp-sort-input"
                  :min="0"
                  :max="99999"
                  controls-position="right"
                />
                <div class="tp-field-hint is-block">{{ t('thingModel.sortOrderHint') }}</div>
              </el-form-item>
              <el-form-item :label="t('common.description')" prop="description" class="tp-span-2">
                <el-input
                  v-model="propertyForm.description"
                  type="textarea"
                  :rows="2"
                  :placeholder="t('thingModel.descriptionPlaceholder')"
                  maxlength="500"
                  show-word-limit
                />
              </el-form-item>
            </div>
          </div>
        </section>

        <!-- 点位绑定：属性的读写对象；卡头保留当前读写模式，点开选择器时同一条约束也在 -->
        <section class="tp-card">
          <header class="tp-card-head">
            <span class="tp-card-icon" aria-hidden="true">
              <el-icon><PlugConnected /></el-icon>
            </span>
            <div class="tp-card-copy">
              <h3 class="tp-card-title">{{ t('thingModel.sectionBinding') }}</h3>
              <p class="tp-card-desc">{{ t('thingModel.sectionBindingDesc') }}</p>
            </div>
            <el-tag
              v-if="propertyForm.accessMode"
              size="small"
              effect="plain"
              :type="propertyForm.accessMode === 'R' ? 'info' : 'warning'"
            >
              {{ accessLabel(propertyForm.accessMode) }}
            </el-tag>
          </header>
          <div class="tp-card-body">
            <PointBindingPicker
              v-model="propertyForm.addressIds"
              :property-id="propertyForm.id"
              :access-mode="propertyForm.accessMode"
              :bound-points="propertyBindingSeed"
              :ready="propertyBindingReady"
              :active="propertyDialogVisible"
            />
            <p class="tp-field-hint">{{ t('thingModel.bindPointsHint') }}</p>
            <p v-if="!propertyBindingReady" class="tp-field-hint is-warning">
              {{ t('thingModel.pointBindingLoadFailed') }}
            </p>
          </div>
        </section>
      </el-form>

      <template #footer>
        <div class="tp-dialog-footer">
          <p class="tp-footer-hint">{{ t('thingModel.formFooterHint') }}</p>
          <div class="tp-footer-actions">
            <el-button @click="propertyDialogVisible = false">{{ t('common.cancel') }}</el-button>
            <el-button type="primary" :loading="propertySubmitting" @click="handleSubmitProperty">
              {{ t('common.save') }}
            </el-button>
          </div>
        </div>
      </template>
    </el-dialog>

    <ImportResultDialog
      v-model:visible="propertyImportResultVisible"
      :result="propertyImportResult"
    />
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconArrowLeft as ArrowLeft,
  IconDownload as Download,
  IconIdBadge2 as IdBadge,
  IconPencil as EditPen,
  IconListDetails as ListDetails,
  IconPlugConnected as PlugConnected,
  IconPlus as Plus,
  IconSearch as Search,
  IconSend as Send,
  IconTrash as Delete,
  IconUpload as Upload,
  IconVariable as Variable,
} from '@tabler/icons-vue'
import {
  createThingModelProperty,
  deleteThingModelProperty,
  downloadThingModelPropertyTemplate,
  getThingModelById,
  importThingModelProperties,
  listThingModelPropertyPoints,
  pageThingModelProperties,
  updateThingModelProperty,
} from '@/api/modules/thingModel'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import PointBindingPicker from './PointBindingPicker.vue'
import { useExcelImport } from '@/composables/useExcelImport'

const { t, te } = useI18n()
const route = useRoute()
const router = useRouter()

const modelId = route.params.id
const modelName = ref(typeof route.query.name === 'string' ? route.query.name : '')
const modelCode = ref('')
const modelCategory = ref('')

const CATEGORY_CODES = ['sensor', 'meter', 'actuator', 'robot', 'plc', 'other']
const ACCESS_CODES = ['R', 'W', 'RW']

// 分类展示名来自 i18n；开放接口下发的自定义分类没有内置文案，直接展示 code。
const categoryLabel = (code) => {
  if (!code) return t('thingModel.category.none')
  const key = `thingModel.category.${code}`
  return te(key) ? t(key) : code
}
const accessLabel = (mode) => t(`thingModel.access.${mode}`)

// 列表页「绑定点位」列的展示名：设备名称.点位名称。设备行缺失（理论上不会）
// 时退回点位名，避免出现以点开头的半截名字。
function boundPointLabel(row) {
  if (!row.pointName) return ''
  return row.pointDeviceName ? `${row.pointDeviceName}.${row.pointName}` : row.pointName
}

const accessOptions = computed(() =>
  ACCESS_CODES.map((code) => ({ value: code, label: accessLabel(code) })),
)

const headerTitle = computed(() =>
  modelName.value
    ? `${t('thingModel.propertiesPageTitle')} — ${modelName.value}`
    : t('thingModel.propertiesPageTitle'),
)

// 刷新页面时 query 里没有名称，靠 id 回查补齐页头；列表页带过来的名称先顶上，避免闪烁。
async function fetchModel() {
  if (!modelId) return
  try {
    const res = await getThingModelById(modelId)
    if (!res) return
    modelName.value = res.name || modelName.value
    modelCode.value = res.code || ''
    modelCategory.value = res.category || ''
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

function goBack() {
  router.push({ name: 'ThingModel' })
}

// ---------- 控制下发入口 ----------
// 只开放给「有绑定点位且属性含写权限」的行：没有点位没得写，只读属性后端也会
// 拒绝。真正的可写性（总开关、协议能力、逐点权限）留到控制页按设备判断，
// 避免在属性列表页为每个属性各发一轮能力查询。
function canControlProperty(row) {
  return row.pointCount > 0 && String(row.accessMode || '').includes('W')
}

function propertyControlDisabledReason(row) {
  if (row.pointCount <= 0) return t('thingModel.propertyControlNoPoints')
  return t('thingModel.propertyControlReadOnly')
}

function openPropertyControl(row) {
  router.push({ name: 'GatewayPropertyControl', params: { propertyId: row.id } })
}

// ---------- 属性列表 ----------
const propertyLoading = ref(false)
const properties = ref([])
const propertyPage = ref(1)
const propertyPageSize = ref(10)
const propertyTotal = ref(0)
// 标识符或名称的模糊检索；服务端按 keyword 同时匹配两列
const propertyKeyword = ref('')
// 模型内最大排序值：分页后前端只有当前页，默认排序必须由服务端给出
const propertyMaxSortOrder = ref(0)

const hasPropertyQuery = computed(() => propertyKeyword.value.trim() !== '')
const displayTotal = computed(() =>
  propertyLoading.value && properties.value.length === 0 ? '—' : propertyTotal.value,
)

async function fetchProperties() {
  if (!modelId) return
  propertyLoading.value = true
  try {
    const res = await pageThingModelProperties(modelId, {
      page: propertyPage.value,
      size: propertyPageSize.value,
      keyword: propertyKeyword.value.trim(),
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

function handlePropertySearch() {
  propertyPage.value = 1
  fetchProperties()
}

function handlePropertyReset() {
  propertyKeyword.value = ''
  propertyPage.value = 1
  fetchProperties()
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

// ---------- 属性绑定点位抽屉 ----------
const propertyPointsDrawerVisible = ref(false)
const propertyPointsLoading = ref(false)
const propertyPoints = ref([])
const propertyPointsProperty = ref(null)
let propertyPointsToken = 0

const propertyPointsTitle = computed(() =>
  t('thingModel.propertyPointsTitle', {
    identifier: propertyPointsProperty.value?.identifier ?? '',
    name: propertyPointsProperty.value?.name ?? '',
  }),
)

async function openPropertyPoints(row) {
  propertyPointsProperty.value = row
  propertyPoints.value = []
  propertyPointsDrawerVisible.value = true
  await fetchPropertyPoints()
}

async function fetchPropertyPoints() {
  if (!propertyPointsProperty.value) return
  const token = ++propertyPointsToken
  propertyPointsLoading.value = true
  try {
    const rows = (await listThingModelPropertyPoints(propertyPointsProperty.value.id)) || []
    if (token !== propertyPointsToken) return
    propertyPoints.value = rows
  } catch {
    if (token === propertyPointsToken) propertyPoints.value = []
  } finally {
    if (token === propertyPointsToken) propertyPointsLoading.value = false
  }
}

function handlePropertyPointsClosed() {
  propertyPointsToken += 1
  propertyPointsProperty.value = null
  propertyPoints.value = []
}

// ---------- 属性表单 ----------
const propertyDialogVisible = ref(false)
const propertySubmitting = ref(false)
const propertyFormRef = ref(null)
const propertyForm = reactive({
  id: '',
  identifier: '',
  name: '',
  // kind 不在页面展示（只作说明）；保留在表单里是为了编辑旧属性时把原值
  // 原样回传，避免一次保存把它清空。
  kind: '',
  accessMode: 'R',
  unit: '',
  description: '',
  sortOrder: 10,
  status: 1,
  addressIds: [],
})
const isPropertyEdit = computed(() => !!propertyForm.id)

// 编辑态用来判断标识符是否真的被改名：只有改动后才把提示切到警示色，
// 打开弹窗时不必先吓人一跳。
const propertyOriginalIdentifier = ref('')
const identifierRenamed = computed(
  () =>
    isPropertyEdit.value &&
    propertyForm.identifier.trim() !== propertyOriginalIdentifier.value,
)

// ---------- 点位绑定（1 对 1） ----------
// 编辑态需要先拉回该属性当前绑定的点位用于展示与回填；加载失败时保存不发送
// addressIds，避免把一次失败的请求变成「解绑」。
const propertyBindingSeed = ref([])
const propertyBindingReady = ref(true)
let propertyBindingToken = 0

async function loadPropertyBinding(propertyId) {
  const token = ++propertyBindingToken
  propertyBindingReady.value = false
  propertyBindingSeed.value = []
  propertyForm.addressIds = []
  try {
    const rows = (await listThingModelPropertyPoints(propertyId)) || []
    if (token !== propertyBindingToken) return
    propertyBindingSeed.value = rows
    // 历史数据可能绑了多个点位；属性已改为 1 对 1，展示并保留第一条，
    // 其余在本次保存时解除 —— 明确提示，不做静默丢弃。
    if (rows.length > 1) {
      ElMessage.warning(t('thingModel.bindPointsLegacyPruned', { count: rows.length }))
    }
    propertyForm.addressIds = rows.slice(0, 1).map((p) => p.addressId)
    propertyBindingReady.value = true
  } catch {
    if (token === propertyBindingToken) {
      propertyBindingSeed.value = []
      propertyForm.addressIds = []
      propertyBindingReady.value = false
    }
  }
}

function resetPropertyBinding() {
  propertyBindingToken += 1
  propertyBindingSeed.value = []
  propertyBindingReady.value = true
  propertyForm.addressIds = []
}

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
  accessMode: [{ required: true, message: t('thingModel.accessRequired'), trigger: 'change' }],
  unit: [{ max: 32, message: t('thingModel.unitLength'), trigger: 'blur' }],
  description: [{ max: 500, message: t('thingModel.descriptionLength'), trigger: 'blur' }],
}))

function nextSortOrder() {
  return (Number(propertyMaxSortOrder.value) || 0) + 10
}

function handleAddProperty() {
  propertyOriginalIdentifier.value = ''
  Object.assign(propertyForm, {
    id: '',
    identifier: '',
    name: '',
    kind: '',
    accessMode: 'R',
    unit: '',
    description: '',
    sortOrder: nextSortOrder(),
    status: 1,
    addressIds: [],
  })
  resetPropertyBinding()
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
}

function handleEditProperty(row) {
  propertyOriginalIdentifier.value = row.identifier ?? ''
  Object.assign(propertyForm, {
    id: row.id,
    identifier: row.identifier,
    name: row.name,
    kind: row.kind ?? '',
    accessMode: row.accessMode,
    unit: row.unit ?? '',
    description: row.description ?? '',
    sortOrder: row.sortOrder ?? 0,
    status: row.status,
    addressIds: [],
  })
  resetPropertyBinding()
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
  loadPropertyBinding(row.id)
}

function handlePropertyDialogClosed() {
  propertyOriginalIdentifier.value = ''
  resetPropertyBinding()
  propertyFormRef.value?.resetFields()
}

async function handleSubmitProperty() {
  if (!modelId) return
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
      // 绑定没加载成功就不碰：发送 [] 会把原有绑定清掉。
      if (propertyBindingReady.value) {
        payload.addressIds = propertyForm.addressIds
      }
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
        modelId,
        identifier: propertyForm.identifier,
        addressIds: propertyForm.addressIds,
      })
      ElMessage.success(t('thingModel.propertyCreateSuccess'))
    }
    propertyDialogVisible.value = false
    if (!isEdit) {
      // 新增属性排在末尾（sortOrder = 全量最大值 + 10），要翻到末页才看得见。
      // 检索条件会把这个新行过滤掉，所以先清空检索，再用**不过滤的** total
      // 算末页 —— 当前页的 total 是过滤后的，直接拿它算会少算。
      propertyKeyword.value = ''
      propertyPage.value = 1
      await fetchProperties()
      const lastPage = Math.max(1, Math.ceil(propertyTotal.value / propertyPageSize.value))
      if (lastPage !== propertyPage.value) {
        propertyPage.value = lastPage
        await fetchProperties()
      }
    } else {
      await fetchProperties()
    }
  } catch {
    // 错误提示由 axios 拦截器统一给出
  } finally {
    propertySubmitting.value = false
  }
}

async function handleDeleteProperty(row) {
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
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

// ---------- Excel 导入 / 模板下载 ----------
const {
  fileInputRef: propertyFileInputRef,
  importing: propertyImporting,
  importResultVisible: propertyImportResultVisible,
  importResult: propertyImportResult,
  handleImport: handlePropertyImport,
  handleFileChange: handlePropertyFileChange,
  handleDownloadTemplate: handlePropertyTemplateDownload,
} = useExcelImport({
  importFn: (file) => importThingModelProperties(file, modelId),
  downloadFn: downloadThingModelPropertyTemplate,
  templateName: () => t('thingModel.importPropertyTemplateName'),
  refresh: fetchProperties,
})

onMounted(() => {
  fetchModel()
  fetchProperties()
})
</script>

<style scoped>
/* 页头右侧的模型标识：弱化为上下文信息，不与标题抢注意力；
   模型标识最长 64 字符，窄屏下要能截断而不是把计数挤出去 */
.thing-model-code {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
  color: var(--ink-3);
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
  flex-wrap: wrap;
  gap: 12px 16px;
}

.thing-drawer-sub {
  margin: 0;
  color: var(--ink-3);
  font-size: 13px;
  line-height: 1.6;
}

/* 名称列：标识符负责识别，名称负责阅读，字重上略高于普通单元格 */
.tp-name {
  font-weight: 500;
  color: var(--ink);
}

/* 「绑定点位」列：设备名称.点位名称，过长时截断；点击打开点位明细抽屉 */
.tp-bound-point-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.tp-bound-point {
  min-width: 0;
  overflow: hidden;
  padding: 0;
  border: 0;
  background: none;
  font: inherit;
  color: var(--brand);
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}

.tp-bound-point:hover {
  text-decoration: underline;
}

.tp-bound-point:focus-visible {
  outline: 2px solid var(--brand);
  outline-offset: 2px;
  border-radius: 3px;
}

/* 存量多绑数据（1 对 1 之前的历史行）的剩余条数提示 */
.tp-bound-point-extra {
  flex: none;
  font-size: 11.5px;
  color: var(--ink-4);
}

/* ---------- 属性弹窗：页头三级递进 ---------- */
.tp-dialog-head {
  padding-right: 28px;
}

.tp-dialog-eyebrow {
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

.tp-dialog-eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.tp-dialog-title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.tp-dialog-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

/* 目标模型：告诉用户这次在给哪个模型加属性，长名称截断而不是挤掉标题 */
.tp-dialog-model {
  max-width: 260px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tp-dialog-sub {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

/* ---------- 分区卡片 ---------- */
.tp-form {
  padding-top: 2px;
}

.tp-card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.tp-card + .tp-card {
  margin-top: 16px;
}

.tp-card-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--line-soft);
}

.tp-card-icon {
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

.tp-card-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;
}

.tp-card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.tp-card-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* 启停是整张卡的开关，与标题同排靠右 */
.tp-enable {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.tp-enable-label {
  font-size: 12.5px;
  color: var(--ink-3);
}

.tp-card-body {
  padding: 20px 22px;
}

.tp-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 20px;
  margin-bottom: -16px;
}

.tp-grid :deep(.el-form-item) {
  margin-bottom: 16px;
}

/* 一侧字段带说明文字时，内容区会被网格拉高；两侧都钉到顶部才能齐平 */
.tp-grid :deep(.el-form-item__content) {
  align-items: flex-start;
  align-content: flex-start;
}

.tp-grid .tp-span-2 {
  grid-column: span 2;
}

.tp-sort-input {
  width: 100%;
}

.tp-field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.tp-field-hint.is-block {
  flex: 0 0 100%;
}

.tp-field-hint.is-warning {
  color: var(--warning);
}

/* ---------- 页脚：左侧常驻后果说明，右侧动作 ---------- */
.tp-dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.tp-footer-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.tp-footer-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 10px;
}

@media (max-width: 720px) {
  .tp-grid {
    grid-template-columns: 1fr;
  }

  .tp-grid .tp-span-2 {
    grid-column: auto;
  }

  /* 窄屏也让页脚保持一行：说明与动作挤不下时整组换行，
     而不是把页脚叠高、把弹窗顶出视口（页脚变高会吃掉正文的高度预算）。 */
  .tp-dialog-footer {
    flex-wrap: wrap;
    align-items: center;
  }
}

/* 属性点位抽屉里的设备单元格：名称在上、编码在下，避免与地址列混淆 */
.thing-point-device {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.thing-point-device-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ink);
}

.thing-point-device-code {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--ink-4);
}
</style>
