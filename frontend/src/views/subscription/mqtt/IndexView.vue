<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与工业物联网 / 数据推送各页同一套层级；右侧是自动刷新提示与订阅数 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.subscription') }}
        </p>
        <h1 class="iot-title">{{ t('menu.mqttSubscription') }}</h1>
        <p class="iot-sub">{{ t('subscription.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <span class="refresh-note">
          <el-icon class="refresh-note-icon"><Refresh /></el-icon>
          {{ t('subscription.statusAutoRefresh') }}
        </span>
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('subscription.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：筛选、状态、表格、分页共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('subscription.searchPlaceholder')"
            clearable
            @keyup.enter="handleSearch"
          >
            <template #prefix>
              <el-icon><Search /></el-icon>
            </template>
          </el-input>
          <el-select v-model="queryForm.status" class="status-filter">
            <el-option :label="t('subscription.statusAll')" :value="''" />
            <el-option :label="t('common.enabled')" :value="1" />
            <el-option :label="t('common.disabled')" :value="0" />
          </el-select>
          <el-button type="primary" @click="handleSearch">{{ t('common.search') }}</el-button>
          <el-button @click="handleReset">{{ t('common.reset') }}</el-button>
        </div>
        <div class="iot-toolbar-actions">
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('subscription.add') }}</el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <!-- 名称是这一行的身份；描述作为第二行，避免再占一列 -->
        <el-table-column :label="t('common.name')" min-width="210">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Message /></el-icon>
              </span>
              <div class="sub-name-copy">
                <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
                <span v-if="row.description" class="sub-name-desc" :title="row.description">
                  {{ row.description }}
                </span>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.broker')" min-width="170" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="iot-mono">{{ row.broker }}{{ row.port ? ':' + row.port : '' }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.topic')" min-width="190" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="iot-mono">{{ row.topic }}</span>
          </template>
        </el-table-column>
        <el-table-column label="QoS" width="96" align="center">
          <template #default="{ row }">
            <span class="iot-chip">QoS {{ row.qos }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('subscription.runtime')" width="180">
          <template #default="{ row }">
            <!-- 停用的订阅不参与运行：显示占位符，避免误读成「连接失败」 -->
            <span v-if="row.status !== 1" class="iot-cell-muted">—</span>
            <div v-else class="runtime-cell">
              <span class="iot-pill" :class="runtimeClass(row)">
                <span class="iot-pill-dot" aria-hidden="true" />
                {{ runtimeText(row) }}
              </span>
              <span v-if="statusOf(row)" class="runtime-counts">
                {{ t('subscription.counts', { messages: statusOf(row).messages, records: statusOf(row).records }) }}
              </span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="110" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-gray'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.disabled') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="updatedAt"
          :label="t('common.updatedAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="110" align="center" fixed="right">
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
              <el-icon><Message /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.name || queryForm.status !== ''
                ? t('subscription.emptySearch')
                : t('subscription.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name && queryForm.status === ''" class="iot-empty-hint">
              {{ t('subscription.emptyHint') }}
            </p>
            <el-button v-if="queryForm.name || queryForm.status !== ''" @click="handleReset">
              {{ t('common.reset') }}
            </el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('subscription.add') }}
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

    <!-- 新增 / 编辑 对话框 -->
    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog subscription-dialog"
      :title="isEdit ? t('subscription.editTitle') : t('subscription.addTitle')"
      width="1000px"
      top="4vh"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <div class="subscription-dialog-body">
        <el-form
          ref="formRef"
          :model="formData"
          :rules="formRules"
          label-width="110px"
          label-position="right"
          status-icon
        >
          <!-- ========== 基本信息 ========== -->
          <section class="sub-section is-first">
            <p class="sub-section-title">{{ t('subscription.basicInfo') }}</p>
            <div class="grid grid-cols-2 gap-x-6">
              <el-form-item :label="t('common.name')" prop="name">
                <el-input v-model="formData.name" :placeholder="t('subscription.namePlaceholder')" maxlength="100" />
              </el-form-item>
              <el-form-item :label="t('subscription.broker')" prop="broker">
                <el-input v-model="formData.broker" :placeholder="t('subscription.brokerPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('subscription.port')">
                <el-input v-model="formData.port" :placeholder="t('subscription.portPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('subscription.clientId')">
                <el-input v-model="formData.clientId" :placeholder="t('subscription.clientIdPlaceholder')" />
              </el-form-item>
              <el-form-item :label="t('subscription.username')">
                <el-input v-model="formData.username" :placeholder="t('subscription.optional')" />
              </el-form-item>
              <el-form-item :label="t('subscription.password')">
                <el-input
                  v-model="formData.password"
                  type="password"
                  show-password
                  :placeholder="t('subscription.optional')"
                />
              </el-form-item>
              <el-form-item :label="t('subscription.topic')" prop="topic" class="col-span-2">
                <el-input v-model="formData.topic" :placeholder="t('subscription.topicPlaceholder')">
                  <template #append>
                    <span class="text-xs">{{ t('subscription.topicWildcardHint') }}</span>
                  </template>
                </el-input>
              </el-form-item>
              <el-form-item :label="t('subscription.qos')">
                <el-select v-model="formData.qos" style="width: 100%">
                  <el-option :value="0" label="0 — at most once" />
                  <el-option :value="1" label="1 — at least once" />
                  <el-option :value="2" label="2 — exactly once" />
                </el-select>
              </el-form-item>
              <el-form-item :label="t('common.status')">
                <el-switch
                  v-model="formData.status"
                  :active-value="1"
                  :inactive-value="0"
                  :active-text="t('common.enabled')"
                  :inactive-text="t('common.disabled')"
                />
              </el-form-item>
              <el-form-item :label="t('common.description')" class="col-span-2">
                <el-input
                  v-model="formData.description"
                  type="textarea"
                  :rows="2"
                  maxlength="200"
                  show-word-limit
                  :placeholder="t('subscription.descPlaceholder')"
                />
              </el-form-item>
              <el-form-item :label="t('subscription.sort')">
                <el-input-number v-model="formData.sort" :min="0" :max="9999" style="width: 100%" />
              </el-form-item>
              <div class="sub-test-row">
                <el-button type="primary" link :loading="testing" @click="handleTestConnectivity">
                  <el-icon class="mr-1"><Connection /></el-icon>{{ t('subscription.testConnection') }}
                </el-button>
              </div>
            </div>
          </section>

          <!-- ========== 映射规则 ========== -->
          <section class="sub-section">
            <p class="sub-section-title">{{ t('subscription.mapping') }}</p>
            <p class="sub-section-hint">{{ t('subscription.mappingHint') }}</p>

            <el-form-item :label="t('subscription.root')">
              <div class="sub-root-row">
                <el-input v-model="rootPath" style="width: 220px" placeholder="$" />
                <span class="sub-inline-hint">{{ t('subscription.rootHint') }}</span>
              </div>
            </el-form-item>

            <div class="sub-inline-row">
              <span class="sub-inline-hint">{{ t('subscription.primaryFieldsHint') }}</span>
              <div class="sub-inline-actions">
                <span v-if="advancedConfiguredCount" class="iot-pill is-amber">
                  {{ t('subscription.advancedConfigured', { count: advancedConfiguredCount }) }}
                </span>
                <el-button
                  type="primary"
                  link
                  size="small"
                  @click="showAdvancedFields = !showAdvancedFields"
                >
                  {{ showAdvancedFields ? t('subscription.hideAdvancedFields') : t('subscription.showAdvancedFields') }}
                </el-button>
              </div>
            </div>

            <div class="mapping-table">
              <div class="mapping-head">
                <span>{{ t('subscription.targetField') }}</span>
                <span>{{ t('subscription.source') }}</span>
                <span>{{ t('subscription.value') }}</span>
              </div>
              <template v-for="def in visibleFieldDefs" :key="def.key">
                <div class="mapping-row">
                  <div class="mapping-label">
                    <span class="text-gray-700">{{ t(def.labelKey) }}</span>
                    <span v-if="def.required" class="text-red-500 ml-1">*</span>
                    <span class="font-mono text-[11px] text-gray-400 ml-2">{{ def.key }}</span>
                  </div>
                  <el-select
                    v-model="fieldMap[def.key].source"
                    size="small"
                    style="width: 118px"
                    @change="handleSourceChange(def.key)"
                  >
                    <el-option :label="t('subscription.sourcePath')" value="path" />
                    <el-option :label="t('subscription.sourceConst')" value="const" />
                    <el-option :label="t('subscription.sourceTemplate')" value="template" />
                    <el-option
                      v-if="def.key === 'kind' || def.key === 'quality'"
                      :label="t('subscription.sourceMap')"
                      value="map"
                    />
                  </el-select>
                  <!-- kind 是受控枚举：固定值时直接给下拉，避免操作人员手填出 Float/Int 等无效值 -->
                  <el-select
                    v-if="def.key === 'kind' && fieldMap[def.key].source === 'const'"
                    v-model="fieldMap[def.key].value"
                    size="small"
                    clearable
                    :placeholder="t('subscription.kindAuto')"
                  >
                    <el-option :label="t('subscription.kindAuto')" value="" />
                    <el-option
                      v-for="opt in KIND_OPTIONS"
                      :key="opt.value"
                      :label="t(opt.labelKey)"
                      :value="opt.value"
                    />
                  </el-select>
                  <el-input
                    v-else-if="fieldMap[def.key].source === 'map'"
                    v-model="fieldMap[def.key].path"
                    size="small"
                    :placeholder="
                      def.key === 'quality'
                        ? t('subscription.qualityMapPathPlaceholder')
                        : t('subscription.kindMapPathPlaceholder')
                    "
                    clearable
                  />
                  <el-input
                    v-else
                    v-model="fieldMap[def.key].value"
                    size="small"
                    :placeholder="valuePlaceholder(def)"
                    clearable
                  />
                </div>

                <!-- 外部系统自有定义 → 本系统 kind / quality 的逐项翻译 -->
                <div
                  v-if="(def.key === 'kind' || def.key === 'quality') && fieldMap[def.key].source === 'map'"
                  class="mapping-map-row"
                >
                  <div class="flex items-center justify-between mb-2">
                    <span class="text-xs text-gray-500">
                      {{ def.key === 'kind' ? t('subscription.kindMapHint') : t('subscription.qualityMapHint') }}
                    </span>
                    <el-button type="primary" link size="small" @click="addMapRow(def.key)">
                      <el-icon class="mr-1"><Plus /></el-icon>{{ t('subscription.addKindMap') }}
                    </el-button>
                  </div>
                  <div
                    v-if="fieldMap[def.key].mappingRows.length === 0"
                    class="text-xs text-gray-400 mb-2"
                  >
                    {{ t('subscription.kindMapEmpty') }}
                  </div>
                  <div
                    v-for="(pair, idx) in fieldMap[def.key].mappingRows"
                    :key="idx"
                    class="kind-map-row"
                  >
                    <el-input
                      v-model="pair.from"
                      size="small"
                      :placeholder="t('subscription.kindMapFromPlaceholder')"
                    />
                    <el-icon class="text-gray-400"><Right /></el-icon>
                    <el-select
                      v-model="pair.to"
                      size="small"
                      clearable
                      :placeholder="
                        def.key === 'kind'
                          ? t('subscription.kindMapToPlaceholder')
                          : t('subscription.qualityMapToPlaceholder')
                      "
                    >
                      <el-option
                        v-for="opt in targetOptions(def.key)"
                        :key="opt.value"
                        :label="t(opt.labelKey)"
                        :value="opt.value"
                      />
                    </el-select>
                    <el-button
                      type="danger"
                      link
                      size="small"
                      :icon="Delete"
                      @click="fieldMap[def.key].mappingRows.splice(idx, 1)"
                    />
                  </div>
                  <div class="flex items-center gap-2 mt-2">
                    <span class="text-xs text-gray-500">{{ t('subscription.kindMapDefault') }}</span>
                    <el-select
                      v-model="fieldMap[def.key].default"
                      size="small"
                      :clearable="def.key === 'kind'"
                      style="width: 260px"
                      :placeholder="
                        def.key === 'kind'
                          ? t('subscription.kindKeepRaw')
                          : t('subscription.qualityMapDefaultPlaceholder')
                      "
                    >
                      <el-option
                        v-if="def.key === 'kind'"
                        :label="t('subscription.kindKeepRaw')"
                        value=""
                      />
                      <el-option
                        v-for="opt in targetOptions(def.key)"
                        :key="opt.value"
                        :label="t(opt.labelKey)"
                        :value="opt.value"
                      />
                    </el-select>
                  </div>
                </div>
              </template>
            </div>
            <p class="sub-hint">{{ t('subscription.kindHint') }}</p>

            <div class="sub-inline-row is-filter-head">
              <div>
                <p class="sub-group-title">{{ t('subscription.filter') }}</p>
                <p class="sub-section-hint is-tight">{{ t('subscription.filterHint') }}</p>
              </div>
              <el-button type="primary" link size="small" @click="addFilter">
                <el-icon class="mr-1"><Plus /></el-icon>{{ t('subscription.addFilter') }}
              </el-button>
            </div>
            <p v-if="filterRows.length === 0" class="sub-empty">{{ t('subscription.noFilter') }}</p>
            <div v-for="(f, idx) in filterRows" :key="idx" class="filter-row">
              <el-input v-model="f.path" size="small" :placeholder="t('subscription.filterPath')" />
              <el-select v-model="f.op" size="small" style="width: 128px">
                <el-option v-for="op in FILTER_OPS" :key="op" :label="op" :value="op" />
              </el-select>
              <el-input
                v-if="f.op === 'in' || f.op === 'notin'"
                v-model="f.valuesText"
                size="small"
                :placeholder="t('subscription.filterValues')"
              />
              <el-input
                v-else-if="f.op !== 'exists' && f.op !== 'notexists'"
                v-model="f.value"
                size="small"
                :placeholder="t('subscription.filterValue')"
              />
              <span v-else class="text-xs text-gray-400">{{ t('subscription.filterNoValue') }}</span>
              <el-button type="danger" link size="small" :icon="Delete" @click="filterRows.splice(idx, 1)" />
            </div>
          </section>

          <!-- ========== 预览 ========== -->
          <section class="sub-section">
            <p class="sub-section-title">{{ t('subscription.preview') }}</p>
            <div class="sub-preview">
              <div class="sub-preview-main">
                <div class="sub-sample-presets">
                  <span class="sub-inline-hint">{{ t('subscription.samplePreset') }}</span>
                  <el-button type="primary" link size="small" @click="useSample('minimal')">
                    {{ t('subscription.sampleMinimal') }}
                  </el-button>
                  <el-button type="primary" link size="small" @click="useSample('advanced')">
                    {{ t('subscription.sampleAdvanced') }}
                  </el-button>
                </div>
                <el-input
                  v-model="sampleText"
                  type="textarea"
                  :rows="9"
                  spellcheck="false"
                  :placeholder="t('subscription.samplePlaceholder')"
                />
              </div>
              <div class="sub-preview-side">
                <el-button type="primary" plain size="small" :loading="previewing" @click="handlePreview">
                  {{ t('subscription.runPreview') }}
                </el-button>
              </div>
            </div>

            <!-- 预览结果整行展示：主字段固定 8 列，dataType / protocol 按需追加。
                 结果区独立成块并带标题，点预览后自动滚到这里，避免表格在折叠线以下被忽略。 -->
            <div v-if="previewResult" ref="previewResultRef" class="preview-result">
              <div class="preview-head">
                <span class="sub-group-title">{{ t('subscription.previewResult') }}</span>
                <span v-if="!previewResult.error" class="iot-pill is-green">
                  {{ t('subscription.previewSummary', { count: previewResult.count, skipped: previewResult.skipped }) }}
                </span>
              </div>
              <div v-if="previewResult.error" class="iot-notice is-warning is-inline preview-error">
                <span class="iot-notice-mark" aria-hidden="true">
                  <el-icon><AlertTriangle /></el-icon>
                </span>
                <p class="iot-notice-text">{{ previewResult.error }}</p>
              </div>
              <el-table
                v-else-if="previewResult.records && previewResult.records.length"
                class="iot-mini-table preview-table"
                :data="previewRows"
                size="small"
                max-height="240"
              >
                <el-table-column prop="deviceId" label="deviceId" min-width="90" show-overflow-tooltip />
                <el-table-column prop="deviceName" label="deviceName" min-width="100" show-overflow-tooltip />
                <el-table-column prop="deviceAddressId" label="deviceAddressId" min-width="105" show-overflow-tooltip />
                <el-table-column prop="deviceAddressLabel" label="deviceAddressLabel" min-width="115" show-overflow-tooltip />
                <el-table-column prop="value" label="value" min-width="80" show-overflow-tooltip />
                <el-table-column
                  v-if="previewShowsCollectedAt"
                  prop="collectedAt"
                  label="collectedAt"
                  min-width="155"
                  show-overflow-tooltip
                />
                <el-table-column prop="kind" label="kind" width="72" />
                <el-table-column prop="quality" label="quality" width="78" align="center" />
                <template v-if="previewShowsAdvanced">
                  <el-table-column prop="deviceAddressName" label="deviceAddressName" min-width="115" show-overflow-tooltip />
                  <el-table-column prop="dataType" label="dataType" width="90" show-overflow-tooltip />
                  <el-table-column prop="protocol" label="protocol" width="90" show-overflow-tooltip />
                </template>
              </el-table>
              <p v-else class="sub-empty">{{ t('subscription.previewEmpty') }}</p>
            </div>
          </section>
        </el-form>
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconAffiliate as Connection,
  IconAlertTriangle as AlertTriangle,
  IconChevronRight as Right,
  IconMessage as Message,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconRefresh as Refresh,
  IconSearch as Search,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'
import {
  getSubscriptionList,
  getSubscriptionById,
  addSubscription,
  updateSubscription,
  deleteSubscription,
  testSubscriptionConnectivity,
  previewSubscriptionMapping,
  getSubscriptionStatus,
} from '@/api/modules/mqttSubscription'

const { t } = useI18n()

// 映射目标字段 = collector.CollectedRecord 的字段（顺序即展示顺序）
const FIELD_DEFS = [
  { key: 'deviceId', labelKey: 'subscription.fieldDeviceId', required: true, placeholder: 'deviceId' },
  { key: 'deviceName', labelKey: 'subscription.fieldDeviceName', required: true, placeholder: 'deviceName' },
  { key: 'deviceAddressId', labelKey: 'subscription.fieldDeviceAddressId', required: true, placeholder: 'tagId' },
  { key: 'deviceAddressName', labelKey: 'subscription.fieldDeviceAddressName', placeholder: 'tagName' },
  { key: 'deviceAddressLabel', labelKey: 'subscription.fieldDeviceAddressLabel', required: true, placeholder: 'tagLabel' },
  { key: 'value', labelKey: 'subscription.fieldValue', required: true, placeholder: 'value' },
  { key: 'collectedAt', labelKey: 'subscription.fieldCollectedAt', placeholder: 'ts / timestamp（可选）' },
  { key: 'dataType', labelKey: 'subscription.fieldDataType', placeholder: 'dataType' },
  { key: 'kind', labelKey: 'subscription.fieldKind', required: true, placeholder: 'kind / const' },
  { key: 'protocol', labelKey: 'subscription.fieldProtocol', placeholder: 'protocol / const' },
  { key: 'quality', labelKey: 'subscription.fieldQuality', required: true, placeholder: 'quality / const 192' },
]

// 历史 mapping_json 里的目标字段名：编辑旧订阅时仍要能读回已配置的取值路径，
// 保存时会按新字段名重写。
const LEGACY_FIELD_KEYS = {
  deviceId: 'deviceID',
  deviceAddressId: 'deviceAddressID',
}

// 主字段 = 默认展示的字段：身份、名称、说明、取值、采集时间（可选）、类型、质量。
// deviceAddressName / dataType / protocol 为透传字段，默认收起。
const PRIMARY_FIELD_KEYS = [
  'deviceId',
  'deviceName',
  'deviceAddressId',
  'deviceAddressLabel',
  'value',
  'collectedAt',
  'kind',
  'quality',
]
const showAdvancedFields = ref(false)
const visibleFieldDefs = computed(() =>
  showAdvancedFields.value
    ? FIELD_DEFS
    : FIELD_DEFS.filter((def) => PRIMARY_FIELD_KEYS.includes(def.key))
)
// 已配置的透传字段数量：默认收起时用角标提示“下面还藏着东西”，
// 避免编辑旧配置时误以为这些字段已丢失。
const advancedConfiguredCount = computed(
  () =>
    FIELD_DEFS.filter(
      (def) =>
        !PRIMARY_FIELD_KEYS.includes(def.key) &&
        String(fieldMap[def.key]?.value ?? '').trim() !== ''
    ).length
)

const FILTER_OPS = ['eq', 'neq', 'gt', 'gte', 'lt', 'lte', 'contains', 'in', 'notin', 'exists', 'notexists']

// kind 的合法取值（driver.KindXxx）。uint 与 int 共用 value_int 列，
// time 与 string 共用 value_str 列，标签里直接写明落列，操作人员不必记文档。
const KIND_OPTIONS = [
  { value: 'bool', labelKey: 'subscription.kindBool' },
  { value: 'int', labelKey: 'subscription.kindInt' },
  { value: 'uint', labelKey: 'subscription.kindUint' },
  { value: 'float', labelKey: 'subscription.kindFloat' },
  { value: 'string', labelKey: 'subscription.kindString' },
  { value: 'time', labelKey: 'subscription.kindTime' },
]

// 网关的质量码（driver.QualityXxx）：类别在高 2 位、成因在低 6 位。
// 外部质量体系与本系统不一致时，用「值映射」翻译到这几个值。
const QUALITY_OPTIONS = [
  { value: '192', labelKey: 'subscription.qualityGood' },
  { value: '128', labelKey: 'subscription.qualityUncertain' },
  { value: '0', labelKey: 'subscription.qualityBad' },
  { value: '1', labelKey: 'subscription.qualityBadAddress' },
  { value: '2', labelKey: 'subscription.qualityBadComms' },
  { value: '3', labelKey: 'subscription.qualityBadParse' },
]

function targetOptions(fieldKey) {
  return fieldKey === 'quality' ? QUALITY_OPTIONS : KIND_OPTIONS
}

// 主字段报文：必填字段 + 可选的采集时间，便于直接看出每行映射的取值来源。
const MINIMAL_SAMPLE = JSON.stringify(
  {
    deviceId: 'meter-01',
    deviceName: '1号电表',
    tagId: 'current',
    tagLabel: '电流',
    value: 10.2,
    ts: '2026-10-19 17:05:30.123',
    kind: 'float',
    quality: 192,
  },
  null,
  2
)

// 含透传字段的报文：主字段 + 透传字段一一对应。
const ADVANCED_SAMPLE = JSON.stringify(
  {
    deviceId: 'meter-01',
    deviceName: '1号电表',
    tagId: 'current',
    tagName: 'A 相电流',
    tagLabel: '电流',
    dataType: 'Float',
    protocol: 'MQTT',
    value: 10.2,
    ts: '2026-10-19 17:05:30.123',
    kind: 'float',
    quality: 192,
  },
  null,
  2
)

// 预览记录来自后端 CollectedRecord（JSON tag 仍是 deviceID / deviceAddressID），
// 表格按目标字段名展示，避免界面上出现两套名字。
const previewRows = computed(() =>
  (previewResult.value?.records || []).map((r) => ({
    ...r,
    deviceId: r.deviceID,
    deviceAddressId: r.deviceAddressID,
  }))
)

// 采集时间只在确实映射出来时展示，避免给未配置时间的订阅多出一列空值。
const previewShowsCollectedAt = computed(
  () => (previewResult.value?.records || []).some((r) => String(r.collectedAt ?? '') !== '')
)

// 预览是否展示透传列：展开透传字段，或结果里确实带出了 dataType / protocol。
const previewShowsAdvanced = computed(
  () =>
    showAdvancedFields.value ||
    (previewResult.value?.records || []).some((r) =>
      ['deviceAddressName', 'dataType', 'protocol'].some((key) => String(r[key] ?? '') !== '')
    )
)

/**
 * 填入示例报文。
 * - minimal：收起透传字段，只演示 5 个主字段。
 * - advanced：展开透传字段，并把仍为空的透传映射补成示例路径。
 */
// 示例报文带 ts 字段：仅在采集时间映射仍为空时补上，避免覆盖用户已配置的路径。
function fillCollectedAtPath() {
  const m = fieldMap.collectedAt
  if (!m || m.source === 'map') return
  if (String(m.value ?? '').trim() === '') {
    m.source = 'path'
    m.value = 'ts'
  }
}

function useSample(kind) {
  previewResult.value = null
  fillCollectedAtPath()
  if (kind === 'advanced') {
    sampleText.value = ADVANCED_SAMPLE
    showAdvancedFields.value = true
    const paths = {
      deviceAddressName: 'tagName',
      dataType: 'dataType',
      protocol: 'protocol',
    }
    for (const [key, path] of Object.entries(paths)) {
      const m = fieldMap[key]
      if (!m || m.source === 'map') continue
      if (String(m.value ?? '').trim() === '') {
        m.source = 'path'
        m.value = path
      }
    }
    return
  }
  sampleText.value = MINIMAL_SAMPLE
  showAdvancedFields.value = false
}

// ---------- 列表 ----------
const loading = ref(false)
const tableData = ref([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const queryForm = reactive({ name: '', status: '' })
const statusMap = ref({})

// 首屏还没拿到数时显示占位符而不是 0：0 条订阅会被当成"还没配任何订阅"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value
)

function statusOf(row) {
  return statusMap.value[row.id]
}

// 运行态：连接成功 / 重连中 / 未连接。重连中带呼吸点，表示状态还在变化。
function runtimeClass(row) {
  const s = statusOf(row)
  if (s?.connected) return 'is-green'
  if (s?.running) return 'is-amber is-live'
  return 'is-gray'
}

function runtimeText(row) {
  const s = statusOf(row)
  if (s?.connected) return t('subscription.connected')
  if (s?.running) return t('subscription.reconnecting')
  return t('subscription.disconnected')
}

async function fetchList() {
  loading.value = true
  try {
    const params = { page: currentPage.value, size: pageSize.value }
    if (queryForm.name) params.name = queryForm.name
    if (queryForm.status !== '' && queryForm.status !== null) params.status = queryForm.status
    const res = await getSubscriptionList(params)
    if (Array.isArray(res)) {
      tableData.value = res
      total.value = res.length
    } else {
      tableData.value = res?.records ?? []
      total.value = res?.total ?? 0
    }
  } catch (err) {
    console.error('获取订阅列表失败', err)
    ElMessage.error(t('subscription.fetchListFailed'))
  } finally {
    loading.value = false
  }
}

async function fetchStatus() {
  try {
    const list = await getSubscriptionStatus()
    const map = {}
    ;(list || []).forEach((s) => {
      map[s.id] = s
    })
    statusMap.value = map
  } catch {
    // 状态查询失败不打扰用户（列表本身仍然可用）
  }
}

function handleSearch() {
  currentPage.value = 1
  fetchList()
}

function handleReset() {
  queryForm.name = ''
  queryForm.status = ''
  currentPage.value = 1
  fetchList()
}

function handleSizeChange(val) {
  pageSize.value = val
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange(val) {
  currentPage.value = val
  fetchList()
}

// ---------- 表单 / 对话框 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const testing = ref(false)
const previewing = ref(false)
const formRef = ref(null)
const rootPath = ref('$')
// 样例报文只是预览辅助，不预填：避免把演示数据保存进配置、也避免
// 操作人员把演示结果误当成自己的真实报文验证通过。
const sampleText = ref('')
const previewResult = ref(null)
const previewResultRef = ref(null)
const filterRows = ref([])

const formData = reactive({
  id: '',
  name: '',
  description: '',
  broker: '',
  port: '',
  clientId: '',
  username: '',
  password: '',
  topic: '',
  qos: 1,
  sort: 0,
  status: 1,
})

const fieldMap = reactive({})

const formRules = {
  name: [{ required: true, message: () => t('subscription.nameRequired'), trigger: 'blur' }],
  broker: [{ required: true, message: () => t('subscription.brokerRequired'), trigger: 'blur' }],
  topic: [{ required: true, message: () => t('subscription.topicRequired'), trigger: 'blur' }],
}

function resetFieldMap() {
  Object.keys(fieldMap).forEach((k) => delete fieldMap[k])
  const defaults = {
    // 主字段：身份/取值/类型/质量 + 后续统计分析需要的名称与说明。
    // kind / quality 报文中缺失时自动推断 / 回落 192。
    deviceId: { source: 'path', value: 'deviceId' },
    deviceName: { source: 'path', value: 'deviceName' },
    deviceAddressId: { source: 'path', value: 'tagId' },
    deviceAddressLabel: { source: 'path', value: 'tagLabel' },
    value: { source: 'path', value: 'value' },
    // 采集时间可选：留空时使用网关收到报文的时间。
    collectedAt: { source: 'path', value: '' },
    kind: { source: 'path', value: 'kind' },
    quality: { source: 'path', value: 'quality' },
    // 透传字段：默认留空，展开后才会写入映射
    deviceAddressName: { source: 'path', value: '' },
    dataType: { source: 'path', value: '' },
    protocol: { source: 'const', value: '' },
  }
  FIELD_DEFS.forEach((def) => {
    const entry = { mappingRows: [], default: '', path: '', ...defaults[def.key] }
    // 记录切换前的取值方式，用于把已填的路径/取值带到新方式上。
    entry._lastSource = entry.source
    fieldMap[def.key] = entry
  })
}

function setFieldMapFromRule(rule) {
  resetFieldMap()
  const fields = rule?.fields || {}
  FIELD_DEFS.forEach((def) => {
    // 兼容早期保存的 mapping_json：目标字段曾叫 deviceID / deviceAddressID。
    const src = fields[def.key] || fields[LEGACY_FIELD_KEYS[def.key]]
    if (src) {
      fieldMap[def.key] = {
        source: src.source || 'path',
        value: src.source === 'map' ? '' : src.value ?? '',
        path: src.source === 'map' ? src.value ?? '' : '',
        mappingRows:
          src.source === 'map'
            ? Object.entries(src.mapping || {}).map(([from, to]) => ({ from, to }))
            : [],
        default: src.default ?? '',
        _lastSource: src.source || 'path',
      }
    }
  })
  rootPath.value = rule?.root || '$'
  // 透传字段始终默认收起；已配置的数量由 advancedConfiguredCount 角标提示。
  showAdvancedFields.value = false
  filterRows.value = (rule?.filter || []).map((f) => ({
    path: f.path || '',
    op: f.op || 'eq',
    value: f.value ?? '',
    valuesText: (f.values || []).join(','),
  }))
}

function resetForm() {
  Object.assign(formData, {
    id: '',
    name: '',
    description: '',
    broker: '',
    port: '',
    clientId: '',
    username: '',
    password: '',
    topic: '',
    qos: 1,
    sort: 0,
    status: 1,
  })
  rootPath.value = '$'
  sampleText.value = ''
  previewResult.value = null
  filterRows.value = []
  showAdvancedFields.value = false
  resetFieldMap()
  nextTick(() => formRef.value?.clearValidate())
}

function handleAdd() {
  isEdit.value = false
  resetForm()
  dialogVisible.value = true
}

async function handleEdit(row) {
  isEdit.value = true
  resetForm()
  try {
    const data = await getSubscriptionById(row.id)
    Object.assign(formData, {
      id: data.id,
      name: data.name,
      description: data.description || '',
      broker: data.broker,
      port: data.port || '',
      clientId: data.clientId || '',
      username: data.username || '',
      password: data.password || '',
      topic: data.topic,
      qos: data.qos ?? 1,
      sort: data.sort ?? 0,
      status: data.status ?? 1,
    })
    const rule = typeof data.mappingJson === 'string' ? safeParse(data.mappingJson) : data.mappingJson
    setFieldMapFromRule(rule)
    if (data.sampleJson) {
      sampleText.value =
        typeof data.sampleJson === 'string' ? data.sampleJson : JSON.stringify(data.sampleJson, null, 2)
    }
    dialogVisible.value = true
  } catch (err) {
    console.error('获取订阅详情失败', err)
    ElMessage.error(t('subscription.fetchDetailFailed'))
  }
}

function handleDialogClose() {
  previewResult.value = null
}

function safeParse(text) {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

function addFilter() {
  filterRows.value.push({ path: '', op: 'eq', value: '', valuesText: '' })
}

// 非 map 取值方式的占位符；map 时填的是“外部字段的路径”，在模板里单独渲染。
function valuePlaceholder(def) {
  if (def.key === 'kind') return 'bool / int / uint / float / string / time'
  return def.placeholder
}

function addMapRow(fieldKey) {
  fieldMap[fieldKey].mappingRows.push({ from: '', to: '' })
}

// 切换取值方式时保留已填的值：kind / quality 的「值映射」通常仍从原来那个字段
// 读取类型/质量，若让操作人员重新填一遍路径，很容易漏填后被校验拦住。
// 仅在同属于路径语义的 JSON 路径 ↔ 值映射之间搬运（固定值/模板的含义不同）；
// 固定值没有路径可沿用，用字段名兜底（kind / quality 的常见取值路径）。
function handleSourceChange(fieldKey) {
  const m = fieldMap[fieldKey]
  if (!m) return
  const prev = m._lastSource || 'path'
  if (prev !== 'map' && m.source === 'map' && !String(m.path ?? '').trim()) {
    m.path = (prev === 'path' && String(m.value ?? '').trim()) || fieldKey
  } else if (prev === 'map' && m.source === 'path' && !String(m.value ?? '').trim()) {
    m.value = String(m.path ?? '').trim()
  }
  // kind / quality 的固定值只能从受控枚举里选；从路径/映射切过来时原值不是
  // 合法枚举（如 kind / quality 本身），清空避免把无效值存进配置。
  if (
    m.source === 'const' &&
    (fieldKey === 'kind' || fieldKey === 'quality') &&
    String(m.value ?? '').trim() !== '' &&
    !targetOptions(fieldKey).some((opt) => String(opt.value) === String(m.value))
  ) {
    m.value = ''
  }
  m._lastSource = m.source
}

// ---------- 映射规则组装 ----------
function buildMapping() {
  const fields = {}
  FIELD_DEFS.forEach((def) => {
    const m = fieldMap[def.key]
    if (m?.source === 'map') {
      const path = m.path == null ? '' : String(m.path).trim()
      const mapping = {}
      for (const pair of m.mappingRows || []) {
        const from = String(pair.from ?? '').trim()
        const to = String(pair.to ?? '').trim()
        if (from === '' || to === '') continue
        mapping[from] = to
      }
      if (path && Object.keys(mapping).length) {
        fields[def.key] = { source: 'map', value: path, mapping }
        if (m.default) fields[def.key].default = m.default
      }
      return
    }
    const value = m?.value == null ? '' : String(m.value).trim()
    if (value === '') return
    fields[def.key] = { source: m.source || 'path', value }
  })

  const filter = filterRows.value
    .filter((f) => String(f.path || '').trim() !== '')
    .map((f) => {
      const item = { path: f.path.trim(), op: f.op || 'eq' }
      if (f.op === 'in' || f.op === 'notin') {
        item.values = String(f.valuesText || '')
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean)
      } else if (f.op !== 'exists' && f.op !== 'notexists') {
        item.value = f.value == null ? '' : String(f.value)
      }
      return item
    })

  return {
    version: 1,
    root: rootPath.value.trim() || '$',
    fields,
    filter,
  }
}

function validateMapping() {
  // kind / quality 走值映射时，路径与映射表都要有；在保存前先拦住，
  // 避免“配置看着没问题、类型或质量却全部走了兜底”。
  for (const fieldKey of ['kind', 'quality']) {
    const m = fieldMap[fieldKey]
    if (m?.source !== 'map') continue

    if (!String(m.path ?? '').trim()) {
      return fieldKey === 'kind'
        ? t('subscription.kindMapPathRequired')
        : t('subscription.qualityMapPathRequired')
    }
    const pairs = (m.mappingRows || []).filter(
      (p) => String(p.from ?? '').trim() && String(p.to ?? '').trim()
    )
    if (pairs.length === 0) {
      return fieldKey === 'kind'
        ? t('subscription.kindMapRequired')
        : t('subscription.qualityMapRequired')
    }
    const seen = new Set()
    for (const p of pairs) {
      const key = String(p.from).trim().toLowerCase()
      if (seen.has(key)) {
        return t('subscription.kindMapDuplicate', { value: String(p.from).trim() })
      }
      seen.add(key)
    }
    // quality 的未命中默认值必须是明确的网关质量码，不能像 kind 那样保留原值。
    if (fieldKey === 'quality' && !String(m.default ?? '').trim()) {
      return t('subscription.qualityMapDefaultRequired')
    }
  }

  const missing = FIELD_DEFS.filter((def) => {
    if (!def.required) return false
    const m = fieldMap[def.key]
    // 值映射的“路径”存在 path 字段里，不能只看 value。
    const v = m?.source === 'map' ? m.path : m?.value
    return !String(v ?? '').trim()
  })
  if (missing.length) {
    return t('subscription.mappingRequired', {
      fields: missing.map((d) => t(d.labelKey)).join('、'),
    })
  }
  for (const f of filterRows.value) {
    if (!String(f.path || '').trim()) continue
    if ((f.op === 'in' || f.op === 'notin') && !String(f.valuesText || '').trim()) {
      return t('subscription.filterValuesRequired')
    }
  }
  return ''
}

function parseSampleJSON() {
  const text = String(sampleText.value || '').trim()
  if (!text) return { error: t('subscription.sampleRequired') }
  try {
    return { value: JSON.parse(text) }
  } catch {
    return { error: t('subscription.sampleInvalid') }
  }
}

// ---------- 测试连接 ----------
async function handleTestConnectivity() {
  testing.value = true
  try {
    await testSubscriptionConnectivity({
      broker: formData.broker,
      port: formData.port,
      clientId: formData.clientId,
      username: formData.username,
      password: formData.password,
      topic: formData.topic,
    })
    ElMessage.success(t('subscription.connectionSuccess'))
  } catch {
    // 错误提示已由 axios 拦截器统一展示
  } finally {
    testing.value = false
  }
}

// ---------- 预览 ----------
async function handlePreview() {
  const mappingErr = validateMapping()
  if (mappingErr) {
    ElMessage.warning(mappingErr)
    return
  }
  const sample = parseSampleJSON()
  if (sample.error) {
    ElMessage.warning(sample.error)
    return
  }
  previewing.value = true
  try {
    previewResult.value = await previewSubscriptionMapping({
      mappingJson: buildMapping(),
      sampleJson: sample.value,
    })
    // 结果表格生成在对话框下方，可能落在可视区外；主动滚过去让操作人员看到产出。
    // 等 el-table 完成首次渲染再算位置，否则滚动目标会因表格高度后到而偏差。
    await nextTick()
    setTimeout(() => {
      const el = previewResultRef.value
      const body = el?.closest('.subscription-dialog-body')
      if (!el || !body) return
      const top = el.getBoundingClientRect().top - body.getBoundingClientRect().top + body.scrollTop
      body.scrollTo({ top: top - 12, behavior: 'smooth' })
    }, 80)
  } catch {
    // 错误提示已由 axios 拦截器统一展示
  } finally {
    previewing.value = false
  }
}

// ---------- 保存 / 删除 ----------
async function handleSubmit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  const mappingErr = validateMapping()
  if (mappingErr) {
    ElMessage.warning(mappingErr)
    return
  }
  // 样例报文可选：填了就必须是合法 JSON；留空不影响保存。
  let sampleJson = null
  if (String(sampleText.value || '').trim()) {
    const sample = parseSampleJSON()
    if (sample.error) {
      ElMessage.warning(sample.error)
      return
    }
    sampleJson = sample.value
  }

  submitting.value = true
  try {
    const payload = {
      name: formData.name,
      description: formData.description,
      broker: formData.broker,
      port: formData.port,
      clientId: formData.clientId,
      username: formData.username,
      password: formData.password,
      topic: formData.topic,
      qos: formData.qos,
      mappingJson: buildMapping(),
      sort: formData.sort,
      status: formData.status,
    }
    if (sampleJson !== null) {
      payload.sampleJson = sampleJson
    }
    if (isEdit.value) {
      await updateSubscription({ id: formData.id, ...payload })
      ElMessage.success(t('subscription.editSuccess'))
    } else {
      await addSubscription(payload)
      ElMessage.success(t('subscription.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
    fetchStatus()
  } catch {
    // 错误提示已由 axios 拦截器统一展示
  } finally {
    submitting.value = false
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('subscription.deleteConfirm', { name: row.name }),
      t('subscription.deleteTitle'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
    await deleteSubscription(row.id)
    ElMessage.success(t('subscription.deleteSuccess'))
    fetchList()
    fetchStatus()
  } catch {
    // 用户取消或删除失败
  }
}

// ---------- 初始化 ----------
let statusTimer = null

onMounted(() => {
  fetchList()
  fetchStatus()
  statusTimer = setInterval(fetchStatus, 5000)
})

onBeforeUnmount(() => {
  if (statusTimer) clearInterval(statusTimer)
})
</script>

<style scoped>
/* ---------- 列表 ---------- */
.status-filter {
  width: 130px;
}

.refresh-note {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--ink-4);
  white-space: nowrap;
}

.refresh-note-icon {
  font-size: 14px;
  color: var(--brand);
}

/* 名称两行：主名称 + 描述，描述不抢主名称的视觉重量 */
.sub-name-copy {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.45;
}

.sub-name-desc {
  margin-top: 2px;
  overflow: hidden;
  font-size: 11.5px;
  color: var(--ink-4);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}

.runtime-counts {
  font-size: 11.5px;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

/* ---------- 对话框 ---------- */
.subscription-dialog-body {
  max-height: 70vh;
  overflow-y: auto;
  padding-right: 8px;
}

/* 三段（基本信息 / 映射规则 / 预览）用发丝线接续，不再用 el-divider 的居中留白 */
.sub-section {
  margin-top: 20px;
  padding-top: 18px;
  border-top: 1px solid var(--line-soft);
}

.sub-section.is-first {
  margin-top: 0;
  padding-top: 0;
  border-top: 0;
}

.sub-section-title {
  margin: 0 0 12px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.06em;
  color: var(--ink-3);
}

.sub-section-hint {
  margin: -4px 0 12px;
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
}

.sub-section-hint.is-tight {
  margin: 2px 0 0;
}

.sub-group-title {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.sub-hint {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
}

.sub-inline-hint {
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
}

.sub-root-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.sub-inline-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px 12px;
  margin: 8px 0;
}

.sub-inline-row.is-filter-head {
  margin: 18px 0 10px;
}

.sub-inline-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sub-empty {
  margin: 0;
  padding: 8px 0;
  font-size: 12px;
  color: var(--ink-4);
}

.sub-test-row {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding-top: 6px;
}

/* 映射表：与列表同源的表头底色与发丝线，去掉灰色外框的厚重感 */
.mapping-table {
  overflow: hidden;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 12px;
}

.mapping-head,
.mapping-row {
  display: grid;
  grid-template-columns: 260px 130px 1fr;
  align-items: center;
  gap: 10px;
  padding: 7px 12px;
}

.mapping-head {
  background: #f8fafc;
  color: var(--ink-3);
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.mapping-row {
  border-top: 1px solid var(--line-soft);
  transition: background-color 0.15s ease;
}

.mapping-row:hover {
  background: #f8fbff;
}

.mapping-label {
  display: flex;
  align-items: center;
  font-size: 13px;
}

/* kind / quality 值映射编辑器：紧跟在对应行下方，占满整行宽度 */
.mapping-map-row {
  padding: 10px 12px 12px;
  background: #fbfcfe;
  border-top: 1px dashed var(--line);
}

.kind-map-row {
  display: grid;
  grid-template-columns: 1fr 20px 240px 36px;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

/* 过滤条件：一行一条，底部用发丝线分隔 */
.filter-row {
  display: grid;
  grid-template-columns: 1fr 128px 1fr 36px;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  border-bottom: 1px solid var(--line-soft);
}

.filter-row:last-child {
  border-bottom: none;
}

/* ---------- 预览 ---------- */
.sub-preview {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.sub-preview-main {
  flex: 1;
  min-width: 0;
}

.sub-preview-side {
  width: 132px;
  padding-top: 24px;
}

.sub-sample-presets {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.preview-result {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--line-soft);
}

.preview-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.iot-notice.preview-error {
  margin: 0;
}

.preview-table {
  overflow: hidden;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

/* 映射表/过滤行里的输入控件：与页面的 9px 圆角保持同一家族 */
.mapping-table :deep(.el-input__wrapper),
.mapping-table :deep(.el-select__wrapper),
.filter-row :deep(.el-input__wrapper),
.filter-row :deep(.el-select__wrapper) {
  border-radius: 7px;
  box-shadow: 0 0 0 1px var(--line) inset;
}

@media (max-width: 860px) {
  .mapping-head,
  .mapping-row {
    grid-template-columns: 1fr;
  }

  .sub-preview {
    flex-direction: column;
  }

  .sub-preview-side {
    width: 100%;
    padding-top: 0;
  }
}
</style>
