<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <button type="button" class="iot-back" @click="goBack">
          <el-icon><ArrowLeft /></el-icon>
          {{ t('deviceAddress.backToList') }}
        </button>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.deviceAddress') }}
        </p>
        <h1 class="iot-title">{{ t('deviceAddress.title') }} — {{ deviceObjectName }}</h1>
        <p class="iot-sub">{{ t('deviceAddress.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('deviceAddress.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：筛选、批量动作、表格、分页共用一块表面 -->
    <section ref="panelRef" class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('deviceAddress.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <!-- 批量删除放在这一组的最左边，且未勾选时置灰不移除：它是**破坏性**动作，
               和右边的下载/导入/新增不是一类；常驻在固定位置能让人形成固定预期，
               按勾选数变长的文案则让「删几条」在按下确认之前就可见。 -->
          <el-button
            type="danger"
            plain
            :icon="Delete"
            :disabled="selectedRows.length === 0"
            @click="handleBatchDelete"
          >
            {{ batchDeleteLabel }}
          </el-button>
          <span class="iot-sep" aria-hidden="true" />
          <el-button :icon="Download" @click="handleDownloadTemplate">{{ t('common.downloadTemplate') }}</el-button>
          <el-button :icon="Upload" :loading="importing" @click="handleImport">{{ t('common.import') }}</el-button>
          <!-- 「自动发现点位」。
               协议不支持时**置灰 + 提示，不隐藏** —— 隐藏会让人以为这个功能没做，
               而置灰加一句理由能让人知道"换条路"或"换个协议插件"。
               能力是异步拿到的，未就绪时先按不可用渲染（避免点开才发现不行）。 -->
          <el-tooltip
            :disabled="canDiscoverPoints"
            placement="top"
            :content="pointScanDisabledReason"
          >
            <span>
              <el-button
                :icon="Search"
                :disabled="!canDiscoverPoints"
                @click="pointDialogVisible = true"
              >
                {{ t('deviceAddress.pointDiscovery.entry') }}
              </el-button>
            </span>
          </el-tooltip>
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('deviceAddress.add') }}</el-button>
          <input ref="fileInputRef" type="file" accept=".xlsx" class="hidden" @change="handleFileChange" />
        </div>
      </div>

      <el-table
        ref="tableRef"
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        row-key="id"
        style="width: 100%"
        @selection-change="handleSelectionChange"
      >
        <!-- 项目里没用 reserve-selection：跨页保留勾选会让「已勾选 N 条」与实际
             能删到的集合不一致（翻页后看不见的行还在里面），批量删除宁可让人重勾。 -->
        <el-table-column type="selection" width="44" align="center" />
        <!-- 窄屏藏起序号与描述：十来列挤进 1000px 会把创建时间推到固定操作列底下，
             反而让人以为数据丢了。两者都在宽屏回归。 -->
        <el-table-column v-if="!compact" type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column label="ID" width="150">
          <template #default="{ row }">
            <div class="iot-id-cell">
              <el-tooltip :content="String(row.id)" placement="top">
                <span class="iot-id-text">{{ row.id }}</span>
              </el-tooltip>
              <el-tooltip :content="t('common.copy')" placement="top">
                <button
                  type="button"
                  class="iot-copy-btn"
                  :aria-label="t('common.copy')"
                  @click="copyId(row.id)"
                >
                  <el-icon><CopyDocument /></el-icon>
                </button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <!-- 地址是这一行的身份：小图标底座让「一个点位」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('deviceAddress.addressName')" min-width="200">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><MapPin /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="label"
          :label="t('deviceAddress.tag')"
          min-width="150"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('deviceAddress.dataType')" width="130" align="center">
          <template #default="{ row }">
            <span v-if="row.dataType" class="iot-chip">{{ row.dataType }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.commonDataType')" width="140" align="center">
          <template #default="{ row }">
            <span v-if="row.commonDataType" class="iot-chip">{{ row.commonDataType }}</span>
            <span v-else class="iot-cell-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.rwPermission')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="rwClass(row.rwPermission)">
              {{ rwLabel(row.rwPermission) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          :label="t('deviceAddress.scanFrequency')"
          width="116"
          align="center"
          class-name="iot-col-num"
        >
          <template #default="{ row }">
            {{ row.scanFrequency }} ms
          </template>
        </el-table-column>
        <el-table-column :label="t('deviceAddress.thresholdAlarm')" width="180" align="center">
          <template #default="{ row }">
            <span v-if="row.thresholdEnabled !== 1" class="iot-pill is-gray">
              {{ t('deviceAddress.thresholdOff') }}
            </span>
            <span v-else class="iot-cond">{{ conditionText(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column
          v-if="!compact"
          prop="description"
          :label="t('common.description')"
          min-width="160"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('common.status')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-rose'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.enableDisable.off') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="t('common.createdAt')"
          width="160"
          class-name="iot-col-time"
        />
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
              <el-icon><MapPin /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.name
                ? t('deviceAddress.emptySearch', { name: queryForm.name })
                : t('deviceAddress.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name" class="iot-empty-hint">{{ t('deviceAddress.emptyHint') }}</p>
            <el-button v-if="queryForm.name" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('deviceAddress.add') }}
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
          @size-change="fetchList"
          @current-change="fetchList"
        />
      </div>
    </section>

    <!-- ========== 新增 / 编辑 对话框 ========== -->
    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog"
      :title="isEdit ? t('deviceAddress.editTitle') : t('deviceAddress.addTitle')"
      width="620px"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="110px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('deviceAddress.addressName')" prop="name">
          <div class="flex items-center gap-2 w-full">
            <el-input
              v-model="formData.name"
              :placeholder="addressPlaceholder"
              maxlength="100"
              show-word-limit
              class="flex-1 min-w-0"
            />
            <!-- 用气泡而不是抽屉：表单本身已经开在对话框里，气泡能让说明和输入框同时可见、
                 边看边改，抽屉会盖住半个表单 -->
            <el-popover v-if="addressSpec" placement="bottom" :width="380" trigger="click">
              <template #reference>
                <el-button :icon="QuestionFilled" circle text class="shrink-0" />
              </template>
              <div class="text-xs leading-5">
                <div class="font-semibold text-gray-700 mb-2">{{ t('deviceAddress.addressHelpTitle') }}</div>

                <div class="text-gray-400 mb-1">{{ t('deviceAddress.addressHelpSyntax') }}</div>
                <div class="font-mono text-gray-800 bg-gray-50 rounded px-2 py-1 mb-3 break-all">
                  {{ t(addressSpec.templateKey) }}
                </div>

                <div class="text-gray-400 mb-1">{{ t('deviceAddress.addressHelpExamples') }}</div>
                <div class="font-mono text-gray-800 mb-3 space-y-0.5">
                  <div v-for="eg in addressSpec.examples" :key="eg">{{ eg }}</div>
                </div>

                <div class="text-gray-400 mb-1">{{ t('deviceAddress.addressHelpPitfalls') }}</div>
                <ul class="list-disc pl-4 text-gray-600 space-y-1.5">
                  <li v-for="key in addressSpec.pitfallKeys" :key="key">{{ t(key) }}</li>
                </ul>
              </div>
            </el-popover>
          </div>
          <!-- 常驻提示：占位符一输入就消失，而地址格式正是最需要一直看见的信息 -->
          <div v-if="addressExample" class="iot-form-hint is-block">
            {{ t('deviceAddress.addressHint', { protocol: protocolName, example: addressExample }) }}
          </div>
        </el-form-item>

        <el-form-item :label="t('deviceAddress.tag')" prop="label">
          <el-input
            v-model="formData.label"
            :placeholder="t('deviceAddress.tagPlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>

        <el-form-item :label="t('deviceAddress.dataType')" prop="dataType">
          <el-select
            v-model="formData.dataType"
            :placeholder="t('deviceAddress.dataTypePlaceholder')"
            style="width: 100%"
            :loading="dataTypeLoading"
            :loading-text="t('common.loading')"
            @change="onDataTypeChange"
          >
            <el-option
              v-for="item in dataTypeOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item :label="t('deviceAddress.rwPermission')" prop="rwPermission">
          <el-select v-model="formData.rwPermission" :placeholder="t('deviceAddress.rwPlaceholder')" style="width: 100%">
            <el-option
              v-for="item in rwPermissionOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item :label="t('deviceAddress.scanFrequency')" prop="scanFrequency">
          <el-select v-model="formData.scanFrequency" :placeholder="t('deviceAddress.scanPlaceholder')" style="width: 100%">
            <el-option
              v-for="item in scanFrequencyOptions"
              :key="item.value"
              :label="item.label"
              :value="item.value"
            />
          </el-select>
        </el-form-item>

        <!-- 报警条件：开关 + 比较符 + 操作数。比较符与操作数控件都按数据类型的类别切换 -->
        <el-form-item :label="t('deviceAddress.thresholdAlarm')" prop="thresholdEnabled">
          <el-switch
            v-model="formData.thresholdEnabled"
            :active-value="1"
            :inactive-value="0"
            :disabled="conditionUnsupported"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
          <div class="iot-form-hint is-block">
            {{ conditionUnsupported ? t('deviceAddress.opUnsupportedKind') : t('deviceAddress.thresholdHint') }}
          </div>
        </el-form-item>

        <template v-if="formData.thresholdEnabled === 1 && !conditionUnsupported">
          <el-form-item :label="t('deviceAddress.thresholdOp')" prop="thresholdOp">
            <el-select v-model="formData.thresholdOp" :placeholder="t('deviceAddress.opPlaceholder')" style="width: 100%">
              <el-option v-for="op in availableOperators" :key="op" :label="operatorLabel(op)" :value="op" />
            </el-select>
          </el-form-item>

          <!-- 区间：两个数值槽位（下限、上限），位置固定，不检查的那侧留空 -->
          <template v-if="formData.thresholdOp === 'range'">
            <el-form-item :label="t('deviceAddress.opRangeLow')" prop="thresholdValues">
              <el-input-number
                v-model="formData.thresholdValues[0]"
                :controls="false"
                :placeholder="t('deviceAddress.boundUnchecked')"
                style="width: 100%"
              />
            </el-form-item>
            <el-form-item :label="t('deviceAddress.opRangeHigh')">
              <el-input-number
                v-model="formData.thresholdValues[1]"
                :controls="false"
                :placeholder="t('deviceAddress.boundUnchecked')"
                style="width: 100%"
              />
            </el-form-item>
          </template>

          <!-- 布尔：值就是真/假，用下拉而不是输入框，避免运维手敲 "on" 这类认不出的字面量 -->
          <el-form-item v-else-if="currentKind === 'bool'" :label="t('deviceAddress.boolValue')">
            <el-select v-model="formData.thresholdValues[0]" style="width: 100%">
              <el-option :label="t('deviceAddress.boolTrue')" value="1" />
              <el-option :label="t('deviceAddress.boolFalse')" value="0" />
            </el-select>
          </el-form-item>

          <!-- 文本集合：多值输入 -->
          <el-form-item v-else-if="formData.thresholdOp === 'in'" :label="t('deviceAddress.textSet')">
            <el-select
              v-model="formData.thresholdValues"
              multiple
              filterable
              allow-create
              default-first-option
              :reserve-keyword="false"
              :placeholder="t('deviceAddress.textSetPlaceholder')"
              style="width: 100%"
            />
          </el-form-item>

          <!-- 数值单比较 / 文本等于不等于：一个操作数，控件随类别切换 -->
          <el-form-item v-else :label="t('deviceAddress.thresholdValue')">
            <el-input-number
              v-if="currentKind !== 'string'"
              v-model="formData.thresholdValues[0]"
              :controls="false"
              style="width: 100%"
            />
            <el-input v-else v-model="formData.thresholdValues[0]" :placeholder="t('deviceAddress.textValuePlaceholder')" />
          </el-form-item>
        </template>

        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            :placeholder="t('deviceAddress.descPlaceholder')"
            type="textarea"
            :rows="3"
            maxlength="200"
            show-word-limit
          />
        </el-form-item>

        <el-form-item :label="t('common.status')" prop="status">
          <el-switch
            v-model="formData.status"
            :active-value="1"
            :inactive-value="0"
            :active-text="t('common.enabled')"
            :inactive-text="t('common.enableDisable.off')"
          />
        </el-form-item>
      </el-form>

      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            {{ isEdit ? t('common.save') : t('common.submit') }}
          </el-button>
        </div>
      </template>
    </el-dialog>

    <!-- ========== Excel 导入结果对话框 ========== -->
    <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />

    <!-- ========== 点位范围扫描（自动发现点位） ========== -->
    <PointDiscoveryDialog
      v-model:visible="pointDialogVisible"
      :device-id="deviceObjectId"
      :protocol-name="protocolName"
      @created="fetchList"
    />
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconArrowLeft as ArrowLeft,
  IconCopy as CopyDocument,
  IconDownload as Download,
  IconHelpCircleFilled as QuestionFilled,
  IconMapPin as MapPin,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconSearch as Search,
  IconTrash as Delete,
  IconUpload as Upload,
} from '@tabler/icons-vue'
import {
  getDeviceAddressList,
  addDeviceAddress,
  updateDeviceAddress,
  deleteDeviceAddress,
  batchDeleteDeviceAddress,
  importDeviceAddresses,
  downloadDeviceAddressTemplate,
} from '@/api/modules/deviceAddress'
import { getDataTypes } from '@/api/modules/protocol'
import { getPointCapability } from '@/api/modules/deviceScan'
import ImportResultDialog from '@/components/ImportResultDialog.vue'
import PointDiscoveryDialog from './PointDiscoveryDialog.vue'
import { useExcelImport } from '@/composables/useExcelImport'
import { useScanFrequencyOptions } from '@/composables/useScanFrequencyOptions'
import { getAddressExample, getAddressSpec } from '@/utils/addressSyntax'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// ---------- 下拉选项 ----------
// 数据类型通过协议接口获取：/protocol/getDataTypes?protocolName=xxx
const dataTypeOptions = ref([])
const dataTypeLoading = ref(false)
// 通用类型名 -> 数据类型类别（driver.KindXxx）。报警条件的比较符下拉靠它决定 ——
// 光看类型名字分不出 "Boolean" 与 "Float" 的类别。见 vo.DataTypeOptionVO.KindByType。
const kindByType = ref({})

// 接口返回 { protocol, commonTypes, extendedTypes, allTypes, kindByType }，使用 allTypes 填充下拉选项
function normalizeDataTypes(data) {
  kindByType.value = data?.kindByType ?? {}
  const list = data?.allTypes ?? data
  if (!Array.isArray(list)) return []
  return list
    .map((item) => {
      if (typeof item === 'string') return { value: item, label: item }
      const value = item?.code ?? item?.value ?? item?.dataType
      const label = item?.description ?? item?.name ?? item?.label ?? value
      return value == null ? null : { value: String(value), label: String(label) }
    })
    .filter(Boolean)
}

async function fetchDataTypes() {
  dataTypeLoading.value = true
  try {
    // 取设备对象传入的协议名；取不到就别猜（原来兜底到 Omron.Net.CIP，但那个名字
    // 不是任何驱动注册的协议名，解析不到反而把问题藏起来），让下拉保持为空
    // （局部变量改叫 proto：外层 protocolName 是 computed，同名遮蔽容易看混）
    const proto = route.query.protocolName
    const res = await getDataTypes(proto)
    dataTypeOptions.value = normalizeDataTypes(res)
  } catch {
    dataTypeOptions.value = []
  } finally {
    dataTypeLoading.value = false
  }
}

const rwPermissionOptions = computed(() => {
  const options = [
    { value: 'R', label: t('deviceAddress.readonlyShort') },
    { value: 'W', label: t('deviceAddress.writeonlyShort') },
    { value: 'RW', label: t('deviceAddress.readwriteShort') },
  ]
  return isEdit.value ? options : options.filter(o => o.value !== 'W')
})

// 扫描频率下拉与「自动发现点位」弹窗共用一份（composables/useScanFrequencyOptions.js）：
// 两处写的是同一个 device_address.scan_frequency，选项必须一致
const scanFrequencyOptions = useScanFrequencyOptions()

// ---------- 路由 ----------
const route = useRoute()
const router = useRouter()
const deviceObjectId = route.params.deviceObjectId
const deviceObjectName = route.query.deviceObjectName || ''

// ---------- 协议地址示例 ----------
// 地址写法随协议完全不同（"D100" 在三菱指 D 寄存器、在欧姆龙 FINS 指 DM 区），
// 实例化在 utils/addressSyntax.js，这里只负责取。协议未登记时回落到通用文案，
// 宁可不举例也不给一个错的例子。
const protocolName = computed(() => route.query.protocolName || '')
const addressSpec = computed(() => getAddressSpec(protocolName.value))
const addressExample = computed(() => getAddressExample(protocolName.value))
const addressPlaceholder = computed(() =>
  addressExample.value
    ? t('deviceAddress.namePlaceholderExample', { example: addressExample.value })
    : t('deviceAddress.namePlaceholder'),
)

function goBack() {
  router.push({ name: 'GatewayDeviceObject' })
}

// ---------- 自动发现点位 ----------
// 按钮的可用性由**服务端**现算的能力决定，不在前端按协议名硬编码一张表：
// 能力声明在插件里（Meta.Protocols[].PointDiscovery），热升级随时可能改变。
const pointDialogVisible = ref(false)
const pointCapability = ref(null)

const canDiscoverPoints = computed(
  () => !!pointCapability.value?.enabled && !!pointCapability.value?.supported,
)
const pointScanDisabledReason = computed(() => {
  if (!pointCapability.value) return t('deviceAddress.pointDiscovery.checking')
  if (!pointCapability.value.enabled) return t('deviceAddress.pointDiscovery.disabled')
  return t('deviceAddress.pointDiscovery.unsupported')
})

async function fetchPointCapability() {
  // 协议名取不到时（协议行缺失）能力必然是 false，不必问服务端。
  if (!protocolName.value) {
    pointCapability.value = { enabled: true, supported: false }
    return
  }
  try {
    pointCapability.value = await getPointCapability(protocolName.value)
  } catch {
    // 能力查询失败按"不可用"渲染：按钮点开也只会得到一个错误，
    // 不如让用户看到一句"暂不可用"。
    pointCapability.value = { enabled: true, supported: false }
  }
}

// ---------- 复制 ID ----------
async function copyId(id) {
  try {
    await navigator.clipboard.writeText(String(id))
    ElMessage.success(t('deviceAddress.idCopied'))
  } catch {
    ElMessage.error(t('deviceAddress.copyFailed'))
  }
}

// ---------- 查询表单 ----------
const queryForm = reactive({
  name: '',
})

// ---------- 分页 ----------
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// 首屏还没拿到数时显示占位符而不是 0：「0 个点位」是一个会被信以为真的假事实
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

// ---------- 窄屏列裁剪 ----------
// 判据取**工作区的真实宽度**而不是窗口宽度：侧栏折叠后同一窗口会多出 176px，
// 按 window.innerWidth 判断会在折叠后仍少显示两列。
const panelRef = ref(null)
const compact = ref(false)
let panelObserver = null

// ---------- 勾选（批量删除） ----------
const tableRef = ref(null)
const selectedRows = ref([])

function handleSelectionChange(rows) {
  selectedRows.value = rows
}

// 按钮文案带上勾选数：按确认之前就能看到"要删几条"
const batchDeleteLabel = computed(() =>
  selectedRows.value.length
    ? t('deviceAddress.batchDeleteCount', { n: selectedRows.value.length })
    : t('deviceAddress.batchDelete'),
)

// ---------- 获取列表 ----------
async function fetchList() {
  loading.value = true
  try {
    const params = {
      deviceObjectId,
      page: currentPage.value,
      size: pageSize.value,
    }
    if (queryForm.name) params.name = queryForm.name
    const res = await getDeviceAddressList(params)
    tableData.value = res?.records ?? []
    total.value = res?.total ?? 0
  } catch (err) {
  } finally {
    loading.value = false
  }
}

// ---------- 搜索 / 重置 ----------
function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  currentPage.value = 1
  fetchList()
}

// ---------- Excel 导入 / 模板下载 ----------
const {
  fileInputRef,
  importing,
  importResultVisible,
  importResult,
  handleImport,
  handleFileChange,
  handleDownloadTemplate,
} = useExcelImport({
  importFn: (file) => importDeviceAddresses(file, deviceObjectId),
  downloadFn: downloadDeviceAddressTemplate,
  templateName: () => t('deviceAddress.importTemplateName'),
  refresh: fetchList,
})

// ---------- 对话框状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const formRef = ref(null)

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
  label: '',
  dataType: '',
  rwPermission: '',
  scanFrequency: null,
  description: '',
  status: 1,
  // 报警条件：开关 + 比较符 + 操作数。提交时打包成嵌套的 threshold 对象，
  // 因为后端用「整个对象省不省略」区分「不动配置」与「整份覆盖」——见 deviceDto.go。
  // values 用定长槽位：range 是 [下限, 上限]（空串占位），其余算子只用 [0]；
  // in 用整个数组（多值）。
  thresholdEnabled: 0,
  thresholdOp: '',
  thresholdValues: [],
})

// ---------- 表单校验规则 ----------
const formRules = {
  name: [
    { required: true, message: () => t('deviceAddress.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('deviceAddress.nameLength'), trigger: 'blur' },
  ],
  label: [
    { required: true, message: () => t('deviceAddress.tagRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('deviceAddress.tagLength'), trigger: 'blur' },
  ],
  dataType: [{ required: true, message: () => t('deviceAddress.selectDataType'), trigger: 'change' }],
  rwPermission: [{ required: true, message: () => t('deviceAddress.selectRw'), trigger: 'change' }],
  scanFrequency: [{ required: true, message: () => t('deviceAddress.selectScan'), trigger: 'change' }],
  description: [{ max: 200, message: () => t('deviceAddress.descMaxLength'), trigger: 'blur' }],
}

// ---------- 新增 ----------
function handleAdd() {
  isEdit.value = false
  resetFormData()
  dialogVisible.value = true
}

// ---------- 编辑 ----------
function handleEdit(row) {
  isEdit.value = true
  formData.id = row.id
  formData.name = row.name
  formData.label = row.label ?? ''
  formData.dataType = row.commonDataType ?? ''
  formData.rwPermission = row.rwPermission ?? ''
  formData.scanFrequency = row.scanFrequency ?? null
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1
  formData.thresholdEnabled = row.thresholdEnabled ?? 0
  formData.thresholdOp = row.thresholdOp ?? ''
  formData.thresholdValues = parseConditionValues(row.thresholdValue)
  dialogVisible.value = true
}

// ---------- 删除 ----------
async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('deviceAddress.deleteConfirm', { name: row.name }), t('deviceAddress.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
    await deleteDeviceAddress(row.id)
    ElMessage.success(t('deviceAddress.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

// ---------- 批量删除 ----------
async function handleBatchDelete() {
  const rows = selectedRows.value
  if (!rows.length) return

  try {
    await ElMessageBox.confirm(
      t('deviceAddress.batchDeleteConfirm', { n: rows.length }),
      t('deviceAddress.batchDeleteTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return // 用户取消
  }

  try {
    const res = await batchDeleteDeviceAddress({
      deviceId: deviceObjectId,
      ids: rows.map((row) => row.id),
    })
    // deleted 由服务端按**实际删除行数**给出，可能小于勾选数：勾选后又被人删掉的
    // 地址不在删除集里。少删了要说出来，不能一律报"成功 N 条"。
    const deleted = res?.deleted ?? 0
    const missing = rows.length - deleted
    if (missing > 0) {
      ElMessage.warning(t('deviceAddress.batchDeletePartial', { n: deleted, missing }))
    } else {
      ElMessage.success(t('deviceAddress.batchDeleteSuccess', { n: deleted }))
    }
    tableRef.value?.clearSelection()
    fetchList()
  } catch {
    // 失败原因由响应拦截器统一提示
  }
}

// ---------- 对话框关闭 ----------
function handleDialogClose() {
  formRef.value?.resetFields()
}

// ---------- 重置表单数据 ----------
function resetFormData() {
  formData.id = ''
  formData.name = ''
  formData.label = ''
  formData.dataType = ''
  formData.rwPermission = ''
  formData.scanFrequency = null
  formData.description = ''
  formData.status = 1
  formData.thresholdEnabled = 0
  formData.thresholdOp = ''
  formData.thresholdValues = []
}

// ---------- 报警条件（按数据类型的类别切换控件） ----------

// 每个数据类型的类别可用哪些比较符 —— 与后端 driver.OperatorsForKind 同一张矩阵。
// 前端复制一份是刻意的：靠 /protocol/getDataTypes 的 kindByType 拿到类别后，
// 下拉选项必须立刻能算出来，不该为每个类型再发一次请求。漂移风险由后端的
// 校验兜住（算子不在可用集时保存会被拒并说明原因）。
const OPERATORS_BY_KIND = {
  int: ['range', 'gt', 'ge', 'lt', 'le', 'eq', 'ne'],
  uint: ['range', 'gt', 'ge', 'lt', 'le', 'eq', 'ne'],
  float: ['range', 'gt', 'ge', 'lt', 'le', 'eq', 'ne'],
  bool: ['eq', 'ne'],
  string: ['eq', 'ne', 'in'],
}

const OPERATOR_LABEL_KEYS = {
  range: 'deviceAddress.opRange',
  gt: 'deviceAddress.opGt',
  ge: 'deviceAddress.opGe',
  lt: 'deviceAddress.opLt',
  le: 'deviceAddress.opLe',
  eq: 'deviceAddress.opEq',
  ne: 'deviceAddress.opNe',
  in: 'deviceAddress.opIn',
}

// 当前选中数据类型的类别（查不到就是空串 = 不支持条件报警）
const currentKind = computed(() => kindByType.value[formData.dataType] ?? '')

// 时间类型与未知类别不支持条件报警 —— 整个条件区禁用并给出原因
const conditionUnsupported = computed(() => !currentKind.value || currentKind.value === 'time')

const availableOperators = computed(() => OPERATORS_BY_KIND[currentKind.value] ?? [])

function operatorLabel(op) {
  return t(OPERATOR_LABEL_KEYS[op] ?? op)
}

// 把库里的 JSON 数组字符串解析成表单用的数组。坏数据/空值退回空数组（等价于未配置）。
function parseConditionValues(raw) {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.map((v) => String(v)) : []
  } catch {
    return []
  }
}

// 把表单条件整理成交付后端的形态：去掉尾部空槽位，并按其算子应有的形状归一。
// 后端要求 range 恰好两个槽位（位置固定：下限、上限），所以那里**保留**空串占位。
function buildConditionValues() {
  const values = formData.thresholdValues ?? []
  if (formData.thresholdOp === 'range') {
    return [String(values[0] ?? ''), String(values[1] ?? '')]
  }
  if (formData.thresholdOp === 'in') {
    return values.map((v) => String(v ?? '').trim()).filter((v) => v !== '')
  }
  return values.length ? [String(values[0] ?? '').trim()] : []
}

// 用户切换数据类型时重置条件：类型一变，可用算子与操作数的**形状**都可能变
// （数值的 range 对布尔非法，布尔的 1/0 对文本无意义），留着上一次的残留只会得到
// 一个后端不认的组合。
//
// 刻意用 el-select 的 @change 而不是 watch(currentKind)：currentKind 也会因为
// **程序化加载**而变化（handleEdit 载入另一行、kindByType 异步到达），
// 用 watch 会在打开编辑框的那一刻把刚载入的操作数清空 —— 表现为"条件莫名其妙变了"。
function onDataTypeChange() {
  formData.thresholdOp = ''
  formData.thresholdValues = []
}

// 换算子时按新算子应有的形状整理操作数。这里是**保值**的：
// 三种形状都从 values[0]（和 [1]）取值，编辑时载入的条件不会被破坏。
watch(
  () => formData.thresholdOp,
  (op) => {
    if (op === 'range') {
      formData.thresholdValues = [formData.thresholdValues[0] ?? '', formData.thresholdValues[1] ?? '']
    } else if (op === 'in') {
      formData.thresholdValues = [...formData.thresholdValues.filter((v) => String(v ?? '').trim() !== '')]
    } else {
      formData.thresholdValues = [formData.thresholdValues[0] ?? '']
    }
  },
)

// 跨字段规则没法用 Element Plus 的声明式 rules 表达，在提交前统一校验一次。
// 文案与后端 validateAddressThreshold 保持一致，避免两边说法分叉。
function validateThreshold() {
  if (formData.thresholdEnabled !== 1) return true
  if (conditionUnsupported.value) {
    ElMessage.warning(t('deviceAddress.opUnsupportedKind'))
    return false
  }
  if (!formData.thresholdOp) {
    ElMessage.warning(t('deviceAddress.opRequired'))
    return false
  }
  const values = buildConditionValues()
  if (formData.thresholdOp === 'range') {
    const [low, high] = values
    if (!low && !high) {
      ElMessage.warning(t('deviceAddress.rangeRequired'))
      return false
    }
    if (low && high && Number(low) >= Number(high)) {
      ElMessage.warning(t('deviceAddress.rangeOrder'))
      return false
    }
    return true
  }
  if (values.length === 0 || values.every((v) => !v)) {
    ElMessage.warning(t('deviceAddress.valueRequired'))
    return false
  }
  return true
}

// 表格里展示条件摘要（如 `∈ [10, 40]`、`= FAULT`）。
function conditionText(row) {
  const op = row.thresholdOp
  const values = parseConditionValues(row.thresholdValue)
  switch (op) {
    case 'range':
      return t('deviceAddress.condRange', { low: values[0] || '−∞', high: values[1] || '+∞' })
    case 'gt':
      return `> ${values[0] ?? ''}`
    case 'ge':
      return `≥ ${values[0] ?? ''}`
    case 'lt':
      return `< ${values[0] ?? ''}`
    case 'le':
      return `≤ ${values[0] ?? ''}`
    case 'eq':
      return `= ${values[0] === '1' || values[0] === '0' ? boolLabel(values[0]) : values[0] ?? ''}`
    case 'ne':
      return `≠ ${values[0] ?? ''}`
    case 'in':
      return `∈ {${values.join(', ')}}`
  }
  return ''
}

function boolLabel(v) {
  return v === '1' ? t('deviceAddress.boolTrue') : t('deviceAddress.boolFalse')
}

// 读写权限：胶囊只区分三种语义，不把 R/W/RW 直接当颜色码
function rwClass(permission) {
  if (permission === 'RW') return 'is-green'
  if (permission === 'R') return 'is-blue'
  return 'is-amber'
}

function rwLabel(permission) {
  return {
    R: t('deviceAddress.readonly'),
    W: t('deviceAddress.writeonly'),
    RW: t('deviceAddress.readwrite'),
  }[permission] || permission || '—'
}

// ---------- 提交 ----------
async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  if (!validateThreshold()) return

  submitting.value = true
  try {
    const payload = {
      deviceId: deviceObjectId,
      name: formData.name.trim(),
      label: formData.label.trim(),
      commonDataType: formData.dataType,
      rwPermission: formData.rwPermission,
      scanFrequency: formData.scanFrequency,
      description: formData.description.trim() || undefined,
      status: formData.status,
      // 始终整份提交：编辑时后端靠「对象是否省略」区分不动/覆盖，这里既然用户看得见
      // 条件字段，传过去的就是他的当前意图（关掉开关也要如实传 enabled=0）。
      threshold: {
        enabled: formData.thresholdEnabled,
        op: formData.thresholdOp,
        values: buildConditionValues(),
      },
    }

    if (isEdit.value) {
      payload.id = formData.id
      await updateDeviceAddress(payload)
      ElMessage.success(t('deviceAddress.editSuccess'))
    } else {
      await addDeviceAddress(payload)
      ElMessage.success(t('deviceAddress.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {

  } finally {
    submitting.value = false
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  // 十来列的固定宽度合计远超 1400px，低于 1550 就切到紧凑列 —— 差几个像素时表格
  // 会横向滚动，固定操作列又会盖住创建时间，不如少两列让关键字段完整可见
  if (typeof ResizeObserver !== 'undefined' && panelRef.value) {
    panelObserver = new ResizeObserver(([entry]) => {
      compact.value = entry.contentRect.width < 1550
    })
    panelObserver.observe(panelRef.value)
  }
  fetchList()
  fetchDataTypes()
  fetchPointCapability()
})

onBeforeUnmount(() => {
  panelObserver?.disconnect()
})
</script>

<style scoped>
/* 报警条件摘要：与地址/类型同属技术标识，用弱化的等宽字体 */
.iot-cond {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink-2);
  font-variant-numeric: tabular-nums;
}
</style>
