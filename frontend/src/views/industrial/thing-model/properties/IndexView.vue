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
      <div>
        <button type="button" class="iot-back" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          {{ t('thingModel.backToModels') }}
        </button>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ headerTitle }}</h1>
        <p class="iot-sub">{{ t('thingModel.propertiesSubtitle') }}</p>
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
        <el-table-column :label="t('thingModel.identifier')" min-width="200">
          <template #default="{ row }">
            <span class="iot-mono" :title="row.identifier">{{ row.identifier }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.name')" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">{{ row.name }}</template>
        </el-table-column>
        <el-table-column :label="t('thingModel.kind.label')" width="120" align="center">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">{{ kindLabel(row.kind) }}</el-tag>
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
        <el-table-column :label="t('thingModel.pointCount')" width="96" align="center">
          <template #default="{ row }">
            <el-button
              v-if="row.pointCount > 0"
              link
              type="primary"
              @click="openPropertyPoints(row)"
            >
              {{ row.pointCount }}
            </el-button>
            <span v-else class="iot-cell-muted">0</span>
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

    <!-- ========== 属性表单 ========== -->
    <el-dialog
      v-model="propertyDialogVisible"
      class="iot-dialog"
      :title="isPropertyEdit ? t('thingModel.editPropertyTitle') : t('thingModel.addPropertyTitle')"
      width="640px"
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
        <el-form-item :label="t('thingModel.bindPoints')" prop="addressIds">
          <el-select
            v-model="propertyForm.addressIds"
            multiple
            filterable
            collapse-tags
            collapse-tags-tooltip
            :loading="propertyPointOptionsLoading"
            :placeholder="t('thingModel.bindPointsPlaceholder')"
            :no-data-text="t('thingModel.bindPointsEmpty')"
            style="width: 100%"
          >
            <el-option-group
              v-for="g in propertyPointGroups"
              :key="g.deviceId"
              :label="g.deviceLabel"
            >
              <el-option
                v-for="p in g.points"
                :key="p.addressId"
                :label="pointOptionLabel(p)"
                :value="p.addressId"
                :disabled="pointOptionDisabled(p)"
              />
            </el-option-group>
          </el-select>
          <div class="iot-form-hint is-block">{{ t('thingModel.bindPointsHint') }}</div>
          <div
            v-if="!propertyPointOptionsLoading && !propertyPointOptionsReady"
            class="iot-form-hint is-block thing-hint-warning"
          >
            {{ t('thingModel.pointBindingLoadFailed') }}
          </div>
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

    <ImportResultDialog
      v-model:visible="propertyImportResultVisible"
      :result="propertyImportResult"
    />
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconArrowLeft as ArrowLeft,
  IconDownload as Download,
  IconPencil as EditPen,
  IconListDetails as ListDetails,
  IconPlus as Plus,
  IconSearch as Search,
  IconSend as Send,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  createThingModelProperty,
  deleteThingModelProperty,
  downloadThingModelPropertyTemplate,
  getThingModelById,
  importThingModelProperties,
  listThingModelPointOptions,
  listThingModelPropertyPoints,
  pageThingModelProperties,
  updateThingModelProperty,
} from '@/api/modules/thingModel'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'

const { t, te } = useI18n()
const route = useRoute()
const router = useRouter()

const modelId = route.params.id
const modelName = ref(typeof route.query.name === 'string' ? route.query.name : '')
const modelCode = ref('')
const modelCategory = ref('')

const CATEGORY_CODES = ['sensor', 'meter', 'actuator', 'robot', 'plc', 'other']
const KIND_CODES = ['bool', 'int', 'uint', 'float', 'string', 'time']
const ACCESS_CODES = ['R', 'W', 'RW']

// 分类展示名来自 i18n；开放接口下发的自定义分类没有内置文案，直接展示 code。
const categoryLabel = (code) => {
  if (!code) return t('thingModel.category.none')
  const key = `thingModel.category.${code}`
  return te(key) ? t(key) : code
}
const kindLabel = (kind) => t(`thingModel.kind.${kind}`)
const accessLabel = (mode) => t(`thingModel.access.${mode}`)

const kindOptions = computed(() =>
  KIND_CODES.map((code) => ({ value: code, label: kindLabel(code) })),
)
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
  kind: 'float',
  accessMode: 'R',
  unit: '',
  description: '',
  sortOrder: 10,
  status: 1,
  addressIds: [],
})
const isPropertyEdit = computed(() => !!propertyForm.id)

// ---------- 点位绑定候选 ----------
// 属性可以直接绑定任意设备的点位；选项加载失败时不发送 addressIds，
// 避免把一次失败的请求变成「全部解绑」。
const propertyPointOptions = ref([])
const propertyPointOptionsLoading = ref(false)
const propertyPointOptionsReady = ref(false)
const originalPropertyAddressIDs = ref([])
let pointOptionsToken = 0

async function loadPropertyPointOptions(propertyId) {
  const token = ++pointOptionsToken
  propertyPointOptionsLoading.value = true
  propertyPointOptionsReady.value = false
  try {
    const rows = (await listThingModelPointOptions()) || []
    if (token !== pointOptionsToken) return
    propertyPointOptions.value = rows
    propertyPointOptionsReady.value = true
    const bound = propertyId
      ? rows.filter((p) => p.propertyId === propertyId).map((p) => p.addressId)
      : []
    originalPropertyAddressIDs.value = bound
    propertyForm.addressIds = [...bound]
  } catch {
    if (token === pointOptionsToken) {
      propertyPointOptions.value = []
      propertyPointOptionsReady.value = false
    }
  } finally {
    if (token === pointOptionsToken) propertyPointOptionsLoading.value = false
  }
}

// 仅用于末尾「已绑定其他属性」分组的 key，不会与真实设备 ID 撞名。
const BOUND_ELSEWHERE_GROUP = '__bound_elsewhere__'

// 被别的属性占用的点位。当前属性自己已绑定的不算：
// 它们要留在设备分组里，用户改类型/权限后还能取消勾选。
function isPointBoundElsewhere(p) {
  return !!p.propertyId && p.propertyId !== propertyForm.id
}

// 点位所属设备名：末尾分组的条目要靠它区分设备。
function pointDeviceLabel(p) {
  return p.deviceName || p.deviceCode || p.deviceId
}

// 按设备分组展示，避免多台设备的同名地址混在一起分不清。
// 已绑定其他属性的点位一律不可选，收进末尾单独一组：
// 既保留“点位在哪、被谁占用”的信息，又不让它们冲淡真正可选项。
const propertyPointGroups = computed(() => {
  const groups = []
  const byDevice = new Map()
  const boundElsewhere = []
  for (const p of propertyPointOptions.value) {
    if (isPointBoundElsewhere(p)) {
      boundElsewhere.push(p)
      continue
    }
    let g = byDevice.get(p.deviceId)
    if (!g) {
      g = {
        deviceId: p.deviceId,
        deviceLabel: pointDeviceLabel(p),
        points: [],
      }
      byDevice.set(p.deviceId, g)
      groups.push(g)
    }
    g.points.push(p)
  }
  if (boundElsewhere.length > 0) {
    groups.push({
      deviceId: BOUND_ELSEWHERE_GROUP,
      deviceLabel: t('thingModel.pointBoundElsewhereGroup', { count: boundElsewhere.length }),
      points: boundElsewhere,
    })
  }
  return groups
})

// 与后端 accessCovers 同一规则（R 只覆盖 R，W 只覆盖 W，RW 全覆盖）。
function accessCovers(addressRW, propertyAccess) {
  switch (addressRW) {
    case 'RW':
      return true
    case 'R':
      return propertyAccess === 'R'
    case 'W':
      return propertyAccess === 'W'
    default:
      return false
  }
}

function pointOptionDisabled(p) {
  // 当前属性已绑定的点位始终可选：改 kind/access 后用户仍要能取消勾选。
  if (p.propertyId === propertyForm.id && p.propertyId) return false
  if (isPointBoundElsewhere(p)) return true
  if (!p.kind) return true
  if (p.kind !== propertyForm.kind) return true
  return !accessCovers(p.rwPermission, propertyForm.accessMode)
}

// 禁用原因要指明方向：只说「权限不足」会让人以为把点位改成 RW 就能绑，
// 实际是「属性只读 vs 点位只写」这类方向性不匹配。文案按属性读写模式取，
// 与后端 accessCovers 的三种失败组合一一对应。
function pointAccessMismatchLabel(p) {
  const key = `thingModel.pointAccessMismatch.${propertyForm.accessMode}`
  return t(key, { point: accessLabel(p.rwPermission) })
}

function pointOptionLabel(p) {
  const pointLabel = `${p.label || p.name} (${p.name})`
  if (isPointBoundElsewhere(p)) {
    // 末尾分组混着多台设备的点位，分组名看不出设备，条目上补设备名；
    // 属性标识只在模型内唯一，同名属性可能来自不同模型，所以带上模型名。
    const propertyName = p.propertyName || p.propertyIdentifier || p.propertyId
    const boundLabel = p.modelName ? `${p.modelName} / ${propertyName}` : propertyName
    const boundHint = t('thingModel.pointBoundElsewhereShort', { name: boundLabel })
    return `${pointDeviceLabel(p)} / ${pointLabel} · ${boundHint}`
  }
  const parts = [pointLabel]
  if (!p.kind) {
    parts.push(t('thingModel.pointKindUnknown'))
  } else if (p.kind !== propertyForm.kind) {
    parts.push(t('thingModel.pointKindMismatch'))
  } else if (!accessCovers(p.rwPermission, propertyForm.accessMode)) {
    parts.push(pointAccessMismatchLabel(p))
  }
  return parts.length > 1 ? `${parts[0]} · ${parts.slice(1).join(' / ')}` : parts[0]
}

// 同一设备对同一属性只能绑一个点位（后端返回 41021，迁移里有部分唯一索引）。
// 多选在设备维度上退化为单选：以“最后选中的”为准，静默替换同设备已选点，
// 避免用户选完提交才看到错误。
watch(
  () => propertyForm.addressIds,
  (next, prev) => {
    if (!Array.isArray(next) || next.length < 2) return
    const prevSet = new Set(prev || [])
    const added = next.filter((id) => !prevSet.has(id))
    if (added.length === 0) return

    const winners = new Map()
    for (const id of added) {
      const p = propertyPointOptions.value.find((x) => x.addressId === id)
      if (p) winners.set(p.deviceId, id)
    }
    if (winners.size === 0) return
    const current = next.filter((id) => {
      const p = propertyPointOptions.value.find((x) => x.addressId === id)
      if (!p || !winners.has(p.deviceId)) return true
      return winners.get(p.deviceId) === id
    })
    if (current.length !== next.length) {
      propertyForm.addressIds = current
    }
  },
)

// kind / accessMode 变化后，已选但不再兼容的“新增”点位自动移除；
// 原本就绑在本属性上的点位保留，交由后端 41018 阻止改类型。
watch([() => propertyForm.kind, () => propertyForm.accessMode], () => {
  if (!propertyDialogVisible.value || propertyForm.addressIds.length === 0) return
  const originals = new Set(originalPropertyAddressIDs.value)
  const next = propertyForm.addressIds.filter((id) => {
    if (originals.has(id)) return true
    const p = propertyPointOptions.value.find((x) => x.addressId === id)
    return p ? !pointOptionDisabled(p) : false
  })
  if (next.length !== propertyForm.addressIds.length) {
    propertyForm.addressIds = next
    ElMessage.warning(t('thingModel.bindPointsPruned'))
  }
})

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
    addressIds: [],
  })
  originalPropertyAddressIDs.value = []
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
  loadPropertyPointOptions('')
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
    addressIds: [],
  })
  originalPropertyAddressIDs.value = []
  propertyDialogVisible.value = true
  nextTick(() => propertyFormRef.value?.clearValidate())
  loadPropertyPointOptions(row.id)
}

function handlePropertyDialogClosed() {
  pointOptionsToken += 1
  propertyPointOptions.value = []
  propertyPointOptionsLoading.value = false
  propertyPointOptionsReady.value = false
  originalPropertyAddressIDs.value = []
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
      // 选项没加载成功就不碰绑定：发送 [] 会把原有绑定全部清掉。
      if (propertyPointOptionsReady.value) {
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

.thing-hint-warning {
  color: var(--warning);
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
