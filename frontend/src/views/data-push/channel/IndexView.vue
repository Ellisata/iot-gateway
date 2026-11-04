<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <!-- 页头：与工业物联网各页同一套字号/留白层级；右侧常驻通道数 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.dataPush') }}
        </p>
        <h1 class="iot-title">{{ t('menu.channel') }}</h1>
        <p class="iot-sub">{{ t('channel.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('channel.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 工作区：筛选、表格、分页共用一块表面 -->
    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <el-input
            v-model="queryForm.name"
            class="iot-search"
            :placeholder="t('channel.searchPlaceholder')"
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
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('channel.add') }}</el-button>
        </div>
      </div>

      <el-table
        class="iot-table"
        :data="tableData"
        v-loading="loading"
        style="width: 100%"
      >
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column :label="t('channel.type')" width="150">
          <template #default="{ row }">
            <span class="iot-pill is-gray">{{ typeLabel(row.type) }}</span>
          </template>
        </el-table-column>
        <!-- 通道名是这一行的身份：小图标底座让「一个推送出口」在滚动中可被一眼拾取 -->
        <el-table-column :label="t('common.name')" min-width="200">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Broadcast /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="description"
          :label="t('common.description')"
          min-width="240"
          show-overflow-tooltip
          class-name="iot-col-muted"
        />
        <el-table-column :label="t('common.status')" width="112" align="center">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.status === 1 ? 'is-green' : 'is-gray'">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ row.status === 1 ? t('common.enabled') : t('common.disabled') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="t('common.createdAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="150" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <el-tooltip :content="t('channel.monitor.action')" placement="top">
                <el-button
                  class="iot-row-btn"
                  type="primary"
                  link
                  size="small"
                  :icon="Monitor"
                  :aria-label="t('channel.monitor.action')"
                  @click="handleMonitor(row)"
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
              <el-icon><Broadcast /></el-icon>
            </span>
            <p class="iot-empty-title">
              {{ queryForm.name
                ? t('channel.emptySearch', { name: queryForm.name })
                : t('channel.emptyTitle') }}
            </p>
            <p v-if="!queryForm.name" class="iot-empty-hint">{{ t('channel.emptyHint') }}</p>
            <el-button v-if="queryForm.name" @click="handleReset">{{ t('common.reset') }}</el-button>
            <el-button v-else type="primary" :icon="Plus" @click="handleAdd">
              {{ t('channel.add') }}
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
      class="iot-dialog channel-dialog"
      :title="isEdit ? t('channel.editTitle') : t('channel.addTitle')"
      width="min(1180px, 96vw)"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="100px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('channel.type')" prop="type">
          <ChannelSelector
            v-model="formData.type"
            :disabled="isEdit"
            @select="handleChannelSelect"
          />
        </el-form-item>
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="formData.name"
            :placeholder="t('channel.namePlaceholder')"
            maxlength="50"
            show-word-limit
          />
        </el-form-item>
        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            :placeholder="t('channel.descPlaceholder')"
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
            :inactive-text="t('common.disabled')"
          />
        </el-form-item>
      </el-form>

      <!-- 动态连接配置表单：根据所选通道名称加载。
           用一条发丝线接续，而不是 el-divider —— 分隔线会带着一段居中留白，
           把「基本信息」和「连接参数」切成两张互不相干的表。 -->
      <div v-if="formData.type" class="channel-section">
        <div class="channel-section-head">
          <div class="channel-section-headline">
            <span class="channel-section-mark" aria-hidden="true">
              <el-icon><Connection /></el-icon>
            </span>
            <div>
              <p class="channel-section-name">{{ t('channel.connectConfig') }}</p>
              <p class="channel-section-sub">{{ t('channel.connectParams') }}</p>
            </div>
          </div>
          <el-button
            type="primary"
            link
            :loading="testingConnection"
            :disabled="detailLoading"
            @click="handleTestConnectivity"
          >
            {{ t('channel.testConnection') }}
          </el-button>
        </div>
        <div v-loading="dynamicFormLoading" class="dynamic-form-wrapper">
          <p
            v-if="!dynamicFormLoading && !dynamicFormRule.length"
            class="channel-empty-config"
          >
            {{ t('channel.noConnectConfig') }}
          </p>
          <!-- 只渲染表单本身（v-else-if 而不是 v-else）：加载中不要先挂一个
               空规则的表单实例，否则它会触发一次假的 mounted 并清掉回填数据。
               key 带上 locale：表单文案在创建实例时就固定了，切语言必须重建；
               v-model 保住重建前已填的值 -->
          <form-create
            v-else-if="dynamicFormRule.length"
            v-model="dynamicFormValues"
            :key="`${dynamicFormKey}-${locale}`"
            :rule="localizedDynamicFormRule"
            :option="localizedDynamicFormOptions"
            :locale="fcLocale"
            @mounted="handleDynamicFormMounted"
          />
        </div>
        <p v-if="formData.type === 'http'" class="channel-preview-hint">
          {{ t('channel.httpConnectivityHint') }}
        </p>
        <p v-if="formData.type === 'mqtt'" class="channel-preview-hint">
          {{ t('channel.mqttTopicHintPrefix') }}<code>{{ mqttTopicTokens }}</code>{{ t('channel.mqttTopicHintSuffix') }}
        </p>
      </div>

      <!-- 采集组订阅：空 = 收全量（含未分组）；非空 = 只收所选组。
           它是“收哪些数据”的运行期路由，排在连接配置之后、输出格式之前：
           先确定数据范围，再决定怎么编码。 -->
      <div v-if="selectedTypeMeta?.messageBased" class="channel-section">
        <div class="channel-section-head">
          <div class="channel-section-headline">
            <span class="channel-section-mark" aria-hidden="true">
              <el-icon><LayersLinked /></el-icon>
            </span>
            <div>
              <p class="channel-section-name">{{ t('channel.groupSubscription.title') }}</p>
              <p class="channel-section-sub">{{ t('channel.groupSubscription.subtitle') }}</p>
            </div>
          </div>
        </div>
        <el-select
          v-model="subscribedGroupIds"
          multiple
          filterable
          clearable
          :loading="groupOptionsLoading"
          :placeholder="t('channel.groupSubscription.placeholder')"
          style="width: 100%"
        >
          <el-option-group
            v-for="dev in groupOptionGroups"
            :key="dev.deviceId"
            :label="dev.deviceName || dev.deviceId"
          >
            <el-option
              v-for="g in dev.groups"
              :key="g.id"
              :label="g.status === 1 ? g.name : `${g.name} (${t('collectorGroup.disabledShort')})`"
              :value="g.id"
            />
          </el-option-group>
        </el-select>
        <p class="iot-form-hint is-block">{{ t('channel.groupSubscription.hint') }}</p>
      </div>

      <!-- 载荷格式 / 出站映射 / 预览与测试发送：仅消息类通道显示。
           format/mapping 不放进 form-create 动态表单，避免与连接参数互相覆盖。 -->
      <div v-if="selectedTypeMeta?.messageBased" class="channel-section">
        <div class="channel-section-head">
          <div class="channel-section-headline">
            <span class="channel-section-mark" aria-hidden="true">
              <el-icon><Braces /></el-icon>
            </span>
            <div>
              <p class="channel-section-name">{{ t('channel.outputFormat') }}</p>
              <p class="channel-section-sub">{{ t('channel.outputFormatHint') }}</p>
            </div>
          </div>
          <!-- 格式下拉跟标题同排：标题已经写明「输出格式」，不再重复一个表单标签 -->
          <el-select
            v-model="outputFormat"
            class="channel-format-select"
            :disabled="detailLoading"
            @change="handleFormatChange"
          >
            <el-option
              v-for="f in selectedTypeMeta.formats || []"
              :key="f.name"
              :label="f.label || f.name"
              :value="f.name"
            />
          </el-select>
        </div>

        <!-- 用 v-show 而不是 v-if：切到 native-json 再切回来时，
             高级 JSON 模式里尚未保存的文本不会丢。 -->
        <MappingEditor
          v-show="editorKind === 'mapping'"
          ref="mappingEditorRef"
          :state="mappingState"
          :state-id="mappingStateId"
          :disabled="detailLoading || submitting"
          :preview-result="previewResult"
          :preview-loading="previewing"
          :auto-previewing="autoPreviewing"
          @change="onMappingChange"
          @preview="handlePreview"
        />
        <ScriptEditor
          v-show="editorKind === 'script'"
          ref="scriptEditorRef"
          :state="scriptState"
          :state-id="scriptStateId"
          :disabled="detailLoading || submitting"
          :preview-result="previewResult"
          :preview-loading="previewing"
          :auto-previewing="autoPreviewing"
          @change="onScriptChange"
          @preview="handlePreview"
        />

        <div
          v-if="!requiresMapping || selectedTypeMeta.supportsTestSend"
          class="channel-preview-actions"
        >
          <el-button
            v-if="!requiresMapping"
            :loading="previewing"
            :disabled="detailLoading"
            @click="handlePreview"
          >
            {{ t('channel.previewPayload') }}
          </el-button>
          <el-button
            v-if="selectedTypeMeta.supportsTestSend"
            :loading="testSending"
            :disabled="detailLoading"
            @click="handleTestSend"
          >
            {{ t('channel.testSend') }}
          </el-button>
          <span v-if="!requiresMapping" class="channel-preview-hint">
            {{ t('channel.previewHint') }}
          </span>
        </div>
        <pre
          v-if="!requiresMapping && previewText"
          ref="payloadPreviewRef"
          class="mapping-preview"
        >{{ previewText }}</pre>
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" :disabled="detailLoading" @click="handleSubmit">
          {{ t('common.confirm') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- 通道监控：运行状态 + 最近推送报文。数据只来自内存，重启即清空；
         抽屉打开期间每 3 秒刷新一次，关闭即停止轮询。 -->
    <el-drawer
      v-model="monitorVisible"
      class="channel-monitor-drawer"
      size="min(980px, 96vw)"
      :with-header="false"
      @open="handleMonitorOpen"
      @closed="handleMonitorClosed"
    >
      <div v-loading="monitorLoading" class="channel-monitor">
        <header class="channel-monitor-head">
          <div>
            <p class="iot-eyebrow">
              <span class="iot-eyebrow-dot" aria-hidden="true" />
              {{ t('channel.monitor.title') }}
            </p>
            <h2 class="channel-monitor-name">{{ monitorRow?.name || '—' }}</h2>
            <p class="channel-monitor-sub">{{ monitorSubtitle }}</p>
          </div>
          <el-button :icon="Refresh" :loading="monitorLoading" @click="refreshMonitor(true)">
            {{ t('common.refresh') }}
          </el-button>
        </header>

        <div class="channel-monitor-stats">
          <div class="monitor-stat">
            <span class="monitor-stat-label">{{ t('channel.monitor.connection') }}</span>
            <span class="iot-pill" :class="monitorConnectionClass">
              <span class="iot-pill-dot" aria-hidden="true" />
              {{ monitorConnectionText }}
            </span>
          </div>
          <div v-for="item in monitorStats" :key="item.key" class="monitor-stat">
            <span class="monitor-stat-label">{{ item.label }}</span>
            <span class="monitor-stat-value" :class="{ 'monitor-stat-error': item.isError }">
              {{ item.value }}
            </span>
          </div>
        </div>

        <!-- OPC UA 是服务端通道：没有「下游连接」，真正要盯的是地址空间与点位
             数据新鲜度。这些字段来自后端 ChannelStatusVO.Extra。 -->
        <div v-if="monitorOpcua && monitorOpcuaDetails.length" class="channel-monitor-section">
          <div class="channel-monitor-section-head">
            <div>
              <p class="channel-monitor-section-title">{{ t('channel.monitor.addressSpace') }}</p>
              <p class="channel-monitor-section-sub">{{ t('channel.monitor.addressSpaceHint') }}</p>
            </div>
          </div>
          <div class="channel-monitor-stats is-details">
            <div v-for="item in monitorOpcuaDetails" :key="item.key" class="monitor-stat">
              <span class="monitor-stat-label">{{ item.label }}</span>
              <span class="monitor-stat-value" :class="{ 'monitor-stat-error': item.isError }">
                {{ item.value }}
              </span>
            </div>
          </div>
          <div v-if="monitorEndpoints.length" class="channel-monitor-endpoints">
            <span class="monitor-stat-label">{{ t('channel.monitor.endpoints') }}</span>
            <code v-for="ep in monitorEndpoints" :key="ep" class="channel-monitor-endpoint">{{ ep }}</code>
          </div>
        </div>

        <p v-if="!monitorActive" class="channel-monitor-hint">
          {{ t('channel.monitor.inactiveHint') }}
        </p>

        <div class="channel-monitor-section">
          <div class="channel-monitor-section-head">
            <div>
              <p class="channel-monitor-section-title">{{ monitorRecentTitle }}</p>
              <p class="channel-monitor-section-sub">{{ monitorRecentSubtitle }}</p>
            </div>
            <span
              class="channel-monitor-refresh-hint"
              :class="{ 'is-paused': hasExpandedRow }"
            >
              {{ hasExpandedRow
                ? t('channel.monitor.autoRefreshPaused')
                : t('channel.monitor.autoRefresh') }}
            </span>
          </div>

          <!-- 轮询会整体替换 messages 数组，Element Plus 默认按行对象引用记展开状态，
               换对象就收起。用 expand-row-keys 按 seq 显式保持展开，否则每 3 秒
               载荷预览都会被折叠掉。 -->
          <el-table
            :data="monitorMessages"
            row-key="seq"
            :expand-row-keys="expandedSeqKeys"
            class="iot-table monitor-table"
            size="small"
            max-height="460"
            @expand-change="handleExpandChange"
          >
            <el-table-column type="expand" width="36">
              <template #default="{ row }">
                <pre class="mapping-preview monitor-payload">{{ row.payload || t('channel.monitor.payloadEmpty') }}</pre>
                <p v-if="row.truncated" class="channel-monitor-hint">
                  {{ t('channel.monitor.truncated', { size: formatBytes(row.size) }) }}
                </p>
              </template>
            </el-table-column>
            <el-table-column prop="time" :label="t('channel.monitor.colTime')" width="176" />
            <el-table-column :label="t('channel.monitor.colTarget')" min-width="150" show-overflow-tooltip>
              <template #default="{ row }">
                <!-- 补发标记放在目标前面：列宽不够时被截断的是目标文本，
                     而不是说明这条来源的标记。 -->
                <span v-if="row.fromDrain" class="iot-pill is-gray monitor-drain-pill">
                  {{ t('channel.monitor.fromDrain') }}
                </span>
                <span class="monitor-target">{{ row.target || '—' }}</span>
              </template>
            </el-table-column>
            <!-- 时序库一次写入可能跨多台设备，不显示「设备 / 组」，改为看行数。 -->
            <el-table-column
              v-if="monitorMessageBased"
              :label="t('channel.monitor.colDevice')"
              min-width="110"
              show-overflow-tooltip
            >
              <template #default="{ row }">{{ deviceGroupText(row) }}</template>
            </el-table-column>
            <el-table-column
              prop="records"
              :label="monitorMessageBased
                ? t('channel.monitor.colRecords')
                : t('channel.monitor.colRows')"
              width="76"
              align="right"
            />
            <el-table-column :label="t('channel.monitor.colStatus')" width="92" align="center">
              <template #default="{ row }">
                <span class="iot-pill" :class="row.error ? 'is-rose' : 'is-green'">
                  <span class="iot-pill-dot" aria-hidden="true" />
                  {{ messageStatusText(row) }}
                </span>
              </template>
            </el-table-column>
            <el-table-column :label="t('channel.monitor.colCost')" width="132" align="right">
              <template #default="{ row }">
                <!-- OPC UA 没有出站载荷，size 只是预览字符串长度，展示毫秒数即可。 -->
                <template v-if="monitorOpcua">{{ row.durationMs }} ms</template>
                <template v-else>{{ row.durationMs }} ms · {{ formatBytes(row.size) }}</template>
              </template>
            </el-table-column>
            <el-table-column :label="t('channel.monitor.colError')" min-width="150" show-overflow-tooltip>
              <template #default="{ row }">
                <span :class="{ 'monitor-error-text': row.error }">{{ row.error || '—' }}</span>
              </template>
            </el-table-column>

            <template #empty>
              <div class="iot-empty">
                <p class="iot-empty-title">{{ monitorEmptyTitle }}</p>
                <p class="iot-empty-hint">{{ monitorEmptyHint }}</p>
              </div>
            </template>
          </el-table>
        </div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, reactive, computed, nextTick, onBeforeUnmount, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconActivity as Monitor,
  IconAffiliate as Connection,
  IconBraces as Braces,
  IconBroadcast as Broadcast,
  IconLayersLinked as LayersLinked,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconRefresh as Refresh,
  IconSearch as Search,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import {
  getChannelList,
  addChannel,
  updateChannel,
  deleteChannel,
  getChannelById,
  getPushRecentMessages,
  getPushStatus,
  testConnectivity,
  previewPushChannel,
  previewPushMapping,
  testSendPushChannel,
} from '@/api/modules/pushChannel'
import { getPushChannelFormByName } from '@/api/modules/pushChannelForm'
import { listCollectorGroups } from '@/api/modules/collectorGroup'
import ChannelSelector from '@/components/ChannelSelector.vue'
import MappingEditor from '@/components/pushMapping/MappingEditor.vue'
import ScriptEditor from '@/components/pushMapping/ScriptEditor.vue'
import { usePushChannelTypes } from '@/composables/usePushChannelTypes'
import { formCreateLocaleName, localizeDynamicFormRules } from '@/i18n/dynamicFormText'
import { dynamicFormLabelWidth } from '@/i18n/localizedText'
import {
  MappingValidationError,
  createEmptyState,
  defaultMappingState,
  isEmptyMappingState,
  mappingToState,
} from '@/utils/pushMapping'
import { DEFAULT_ENCODE_SCRIPT } from '@/utils/pushScripts'
import { useI18n } from 'vue-i18n'

const { t, locale } = useI18n()
const { load: loadChannelTypes, metaOf } = usePushChannelTypes()

// 占位符放在普通 JS 字符串里，不写进 i18n 文案：vue-i18n 会把 {deviceCode}
// 当成命名插值，${...} 会渲染成空串。模板里用 code 元素拼接展示。
const mqttTopicTokens = '${deviceCode} ${thingModelCode} ${groupCode}'

// ---------- 查询表单 ----------
const queryForm = reactive({
  name: '',
})

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// ---------- 分页 ----------
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 首屏还没拿到数时显示占位符而不是 0：0 个通道会被当成"还没配任何推送出口"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : total.value,
)

// ---------- 获取列表（分页） ----------
async function fetchList() {
  loading.value = true
  try {
    const params = {
      page: currentPage.value,
      size: pageSize.value,
    }
    if (queryForm.name) {
      params.name = queryForm.name
    }
    const res = await getChannelList(params)
    // 兼容两种响应格式：直接返回数组 / 返回 { records: [], total }
    if (Array.isArray(res)) {
      tableData.value = res
      total.value = res.length
    } else {
      tableData.value = res?.records ?? []
      total.value = res?.total ?? 0
    }
  } catch (err) {
    console.error('获取通道列表失败', err)
    ElMessage.error(t('channel.fetchListFailed'))
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

// ---------- 分页切换 ----------
function handleSizeChange(val) {
  pageSize.value = val
  currentPage.value = 1
  fetchList()
}

function handleCurrentChange(val) {
  currentPage.value = val
  fetchList()
}

// ---------- 对话框状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const formRef = ref(null)

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  type: '',
  name: '',
  description: '',
  status: 1,
})

// 选中类型的元数据与载荷格式/映射状态
const selectedTypeMeta = computed(() => metaOf(formData.type))
const outputFormat = ref('native-json')

// ---------- 采集组订阅 ----------
// 空 = 收全部（含未分组）；非空 = 只收所选组。组选项跨设备，按设备分组展示。
const subscribedGroupIds = ref([])
const groupOptions = ref([])
const groupOptionsLoading = ref(false)

const groupOptionGroups = computed(() => {
  const byDevice = new Map()
  for (const g of groupOptions.value) {
    const key = g.deviceId || ''
    if (!byDevice.has(key)) {
      byDevice.set(key, { deviceId: key, deviceName: g.deviceName || key, groups: [] })
    }
    byDevice.get(key).groups.push(g)
  }
  // 配置里引用了已删除的组：保留选中项并单独列出来，不能静默变成「收全量」。
  const known = new Set(groupOptions.value.map((g) => g.id))
  const missing = (subscribedGroupIds.value || []).filter((id) => !known.has(id))
  if (missing.length) {
    byDevice.set('__missing__', {
      deviceId: '__missing__',
      deviceName: t('channel.groupSubscription.missingGroup'),
      groups: missing.map((id) => ({ id, name: id, status: 1 })),
    })
  }
  return [...byDevice.values()]
})

async function loadGroupOptions() {
  groupOptionsLoading.value = true
  try {
    groupOptions.value = (await listCollectorGroups()) || []
  } catch {
    groupOptions.value = []
  } finally {
    groupOptionsLoading.value = false
  }
}
const mappingState = reactive(createEmptyState())
const mappingStateId = ref(0)
const mappingEditorRef = ref(null)
const scriptState = reactive(createEmptyScriptState())
const scriptStateId = ref(0)
const scriptEditorRef = ref(null)
const previewing = ref(false)
const autoPreviewing = ref(false)
const previewResult = ref(null)
const payloadPreviewRef = ref(null)
const testSending = ref(false)
const detailLoading = ref(false)
const mappingTouched = ref(false)
const scriptTouched = ref(false)
// 无法转换成可视化 state 的历史 mapping（保留原文，切到 JSON 模式展示）
const pendingJson = ref(null)
const previewRequestId = ref(0)
const manualPreviewId = ref(0)
let previewTimer = null

function createEmptyScriptState() {
  return { script: '', timeoutMs: 50, maxMessages: 200 }
}

function defaultScriptState() {
  return {
    script: DEFAULT_ENCODE_SCRIPT,
    timeoutMs: 50,
    maxMessages: 200,
  }
}

function isEmptyScriptState(state) {
  return !String(state?.script || '').trim()
}

const selectedFormatMeta = computed(() =>
  (selectedTypeMeta.value?.formats || []).find((f) => f.name === outputFormat.value),
)

// 编辑器种类由格式元数据决定；老后端没有 editor 字段时按 requiresMapping 回落。
const editorKind = computed(() => {
  if (outputFormat.value === 'mapping-json') return 'mapping'
  if (outputFormat.value === 'script') return 'script'
  const editor = selectedFormatMeta.value?.editor
  if (editor === 'mapping' || editor === 'script') return editor
  return selectedFormatMeta.value?.requiresMapping ? 'mapping' : ''
})

// 沿用旧变量名：表示「当前格式需要一个编辑器」，具体是映射还是脚本看 editorKind。
const requiresMapping = computed(() => editorKind.value !== '')

const previewText = computed(() => {
  const res = previewResult.value
  if (!res) return ''
  if (res.error) return `${t('channel.mapEditor.previewError')}: ${res.error}`
  if (res.payloads?.length > 1) return res.payloads.join('\n')
  return res.payload || (res.payloads?.[0] ?? '')
})

function typeLabel(type) {
  return metaOf(type)?.label || type || '—'
}

function mappingProblemText(problem) {
  if (!problem) return t('channel.mappingJsonInvalid')
  const section = String(problem.code || '').startsWith('script')
    ? 'scriptEditor'
    : 'mapEditor'
  return t(`channel.${section}.problems.${problem.code}`, problem.params || {})
}

function showMappingError(err) {
  if (err instanceof MappingValidationError) {
    ElMessage.warning(mappingProblemText(err.problem))
    return
  }
  ElMessage.warning(err?.message || t('channel.mappingJsonInvalid'))
}

function cancelAutoPreview() {
  if (previewTimer) {
    clearTimeout(previewTimer)
    previewTimer = null
  }
  autoPreviewing.value = false
}

function resetMappingState() {
  Object.assign(mappingState, createEmptyState())
  mappingStateId.value += 1
  mappingTouched.value = false
  Object.assign(scriptState, createEmptyScriptState())
  scriptStateId.value += 1
  scriptTouched.value = false
  pendingJson.value = null
  previewResult.value = null
  previewing.value = false
  autoPreviewing.value = false
  manualPreviewId.value = 0
  cancelAutoPreview()
  previewRequestId.value += 1
}

function activeEditor() {
  if (editorKind.value === 'mapping') return mappingEditorRef.value
  if (editorKind.value === 'script') return scriptEditorRef.value
  return null
}

/** 编辑器改动后延迟自动预览；有结构错误时只清空结果，不发请求。 */
function scheduleAutoPreview() {
  cancelAutoPreview()
  previewRequestId.value += 1
  if (!dialogVisible.value || detailLoading.value || submitting.value) return
  if (!editorKind.value) return

  const editor = activeEditor()
  if (!editor) return
  if (editor.validateProblems().some((p) => p.level === 'error')) {
    previewResult.value = null
    return
  }

  const requestId = previewRequestId.value
  autoPreviewing.value = true
  previewTimer = setTimeout(async () => {
    previewTimer = null
    let mapping
    try {
      mapping = editor.buildMapping()
    } catch {
      if (requestId === previewRequestId.value) autoPreviewing.value = false
      return
    }
    try {
      const res = await previewPushMapping({ format: outputFormat.value, mapping })
      if (requestId === previewRequestId.value) previewResult.value = res
    } catch {
      // 全局拦截器已提示
    } finally {
      if (requestId === previewRequestId.value) autoPreviewing.value = false
    }
  }, 600)
}

function onMappingChange() {
  mappingTouched.value = true
  pendingJson.value = null
  scheduleAutoPreview()
}

function onScriptChange() {
  scriptTouched.value = true
  scheduleAutoPreview()
}

function handleFormatChange(next) {
  previewResult.value = null
  cancelAutoPreview()
  previewRequestId.value += 1

  if (next === 'script') {
    if (!scriptTouched.value && isEmptyScriptState(scriptState)) {
      Object.assign(scriptState, defaultScriptState())
      scriptStateId.value += 1
      scriptTouched.value = true
    }
    nextTick(() => scheduleAutoPreview())
    return
  }
  if (next !== 'mapping-json') return

  if (pendingJson.value) {
    nextTick(() => {
      mappingEditorRef.value?.openJson(pendingJson.value.text, pendingJson.value.problem)
    })
    return
  }
  // JSON 模式里有未转换的文本时，不自动填充，避免覆盖用户正在编辑的内容。
  if (mappingEditorRef.value?.hasJsonDraft?.()) {
    nextTick(() => scheduleAutoPreview())
    return
  }
  if (!mappingTouched.value && isEmptyMappingState(mappingState)) {
    Object.assign(mappingState, defaultMappingState())
    mappingStateId.value += 1
    mappingTouched.value = true
  }
  nextTick(() => scheduleAutoPreview())
}

function resetOutputConfig() {
  outputFormat.value = selectedTypeMeta.value?.defaultFormat || 'native-json'
  resetMappingState()
}

async function applyOutputConfig(config) {
  // 兼容 configJson 被二次编码（字符串里再套 JSON）的历史数据。
  let cfg = config
  for (let i = 0; i < 3 && typeof cfg === 'string'; i += 1) {
    cfg = safeParseJSON(cfg)
  }
  outputFormat.value =
    cfg?.format || selectedTypeMeta.value?.defaultFormat || 'native-json'

  Object.assign(mappingState, createEmptyState())
  mappingStateId.value += 1
  mappingTouched.value = false
  Object.assign(scriptState, createEmptyScriptState())
  scriptStateId.value += 1
  scriptTouched.value = false
  pendingJson.value = null
  previewResult.value = null
  cancelAutoPreview()

  if (editorKind.value === 'script') {
    applyScriptConfig(cfg?.mapping)
    return
  }

  let mapping = cfg?.mapping
  if (typeof mapping === 'string') {
    const trimmed = mapping.trim()
    const parsed = trimmed ? safeParseJSON(mapping) : null
    if (trimmed && parsed == null) {
      pendingJson.value = {
        text: mapping,
        problem: { code: 'jsonSyntax', params: { message: t('channel.mappingJsonInvalid') } },
      }
      mappingTouched.value = true
      await nextTick()
      mappingEditorRef.value?.openJson(pendingJson.value.text, pendingJson.value.problem)
      return
    }
    mapping = parsed
  }

  if (mapping && typeof mapping === 'object') {
    const res = mappingToState(mapping)
    if (res.ok) {
      Object.assign(mappingState, res.state)
    } else {
      pendingJson.value = { text: mapping, problem: { code: res.code, params: res.params } }
      await nextTick()
      mappingEditorRef.value?.openJson(mapping, pendingJson.value.problem)
    }
    mappingTouched.value = true
  }
}

/** 回填 script 格式的 mapping（{script, timeoutMs, maxMessages}）。 */
function applyScriptConfig(mapping) {
  let cfg = mapping
  for (let i = 0; i < 3 && typeof cfg === 'string'; i += 1) {
    const parsed = safeParseJSON(cfg)
    if (parsed == null) break
    cfg = parsed
  }
  if (typeof cfg === 'string') {
    // 兼容直接存脚本原文的配置
    scriptState.script = cfg
  } else if (cfg && typeof cfg === 'object') {
    scriptState.script = typeof cfg.script === 'string' ? cfg.script : ''
    const timeout = Number(cfg.timeoutMs)
    scriptState.timeoutMs =
      Number.isInteger(timeout) && timeout >= 1 && timeout <= 500 ? timeout : 50
    const maxMessages = Number(cfg.maxMessages)
    scriptState.maxMessages =
      Number.isInteger(maxMessages) && maxMessages >= 1 && maxMessages <= 1000
        ? maxMessages
        : 200
  }
  scriptStateId.value += 1
  scriptTouched.value = true
}

function safeParseJSON(text) {
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

// ---------- 表单校验规则 ----------
const formRules = {
  type: [
    { required: true, message: () => t('channel.typeRequired'), trigger: 'change' },
  ],
  name: [
    { required: true, message: () => t('channel.nameRequired'), trigger: 'blur' },
    { min: 1, max: 50, message: () => t('channel.nameLength'), trigger: 'blur' },
  ],
  description: [
    { max: 200, message: () => t('channel.descMaxLength'), trigger: 'blur' },
  ],
}

// ---------- 动态连接配置表单（按所选通道名称加载） ----------
const dynamicFormKey = ref(0)
const dynamicFormApi = ref(null)
const dynamicFormRule = ref([])
// form-create 的双向绑定值：切语言重建实例时用它保住已填内容
const dynamicFormValues = ref({})
const dynamicFormOptions = ref({})
// 表单自带的 options.language（设计器「多语言」面板产出）优先；
// 早期写死中文的表单再由存量词典兜底。原始 rule 保持不变，回填/收集字段仍以它为准。
const localizedDynamicFormRule = computed(() =>
  localizeDynamicFormRules(
    dynamicFormRule.value,
    locale.value,
    dynamicFormOptions.value.language,
  ),
)
// form-create 按字符串精确匹配 options.language 的键（zh-cn / en）
const fcLocale = computed(() => formCreateLocaleName(locale.value))
// 英文标签更长（"Additional advertised hosts"），沿用表单声明的 125px 会折行
const localizedDynamicFormOptions = computed(() => {
  const base = dynamicFormOptions.value?.form?.labelWidth || '125px'
  return {
    ...dynamicFormOptions.value,
    form: {
      ...(dynamicFormOptions.value.form || {}),
      labelWidth: dynamicFormLabelWidth(base, locale.value, '220px'),
    },
  }
})
const dynamicFormLoading = ref(false)
// 请求序号：用于忽略过期请求，避免快速切换名称 / 关闭弹框时旧响应覆盖新结果
const dynamicFormRequestId = ref(0)
// 测试连接中标记
const testingConnection = ref(false)

/** 递归解析可能被二次编码的 JSON 字段 */
function deepParse(value) {
  if (typeof value === 'string') {
    try {
      return deepParse(JSON.parse(value))
    } catch {
      return value
    }
  }
  if (Array.isArray(value)) {
    return value.map(deepParse)
  }
  if (value && typeof value === 'object') {
    const obj = {}
    for (const key of Object.keys(value)) {
      obj[key] = deepParse(value[key])
    }
    return obj
  }
  return value
}

/** 从后端响应中提取表单配置，兼容 { formJson } / { rule } / JSON 字符串等结构 */
function normalizeFormConfig(res) {
  if (!res) return null
  const formJson = res.formJson != null ? res.formJson : res
  const obj = deepParse(formJson)
  if (!obj || typeof obj !== 'object') return null
  return {
    rule: Array.isArray(obj.rule) ? obj.rule : [],
    options: obj.options || {},
  }
}

/**
 * 从 configJson 中挑出当前表单声明了的字段。
 * 编辑回显只取这些字段，避免 format / mapping 这类非表单键混进 v-model
 * （form-create 会把多余的 modelValue 键一并收进 formData）。
 */
function pickPrefillValues(prefill, rule) {
  let data = prefill
  for (let i = 0; i < 3 && typeof data === 'string'; i += 1) {
    data = safeParseJSON(data)
  }
  if (!data || typeof data !== 'object') return {}
  const fields = new Set(rule.map((item) => item.field).filter(Boolean))
  const out = {}
  Object.entries(data).forEach(([field, value]) => {
    if (fields.has(field)) out[field] = value
  })
  return out
}

/**
 * 按通道类型加载连接配置表单
 * @param {string} type - 通道类型（mqtt/http/tdengine-v3...）
 * @param {Object} [prefillConfig] - 编辑时传入 configJson（通道详情接口），用于回填已有填写值
 */
async function loadDynamicForm(type, prefillConfig) {
  const requestId = ++dynamicFormRequestId.value
  // 先清空旧表单，避免残留上一个通道的字段
  dynamicFormRule.value = []
  dynamicFormOptions.value = {}
  dynamicFormApi.value = null
  // 换通道类型时清掉旧值，避免同名字段把上一条通道的内容带进新表单
  dynamicFormValues.value = {}
  if (!type) return

  dynamicFormLoading.value = true
  try {
    const res = await getPushChannelFormByName(type)
    // 忽略过期请求的响应（用户可能已切换名称或关闭弹框）
    if (requestId !== dynamicFormRequestId.value) return
    const config = normalizeFormConfig(res)
    if (config?.rule?.length) {
      // 编辑回显必须在表单实例创建前把值交给 v-model：form-create 建实例时
      // 会把 modelValue 当作字段初值。mounted 之后再 setValue 是行不通的 ——
      // 多语言会把 rule 复制成普通对象，form-create 对非响应式规则上的
      // setValue 只改内部 formData，不会更新已经渲染出来的控件。
      dynamicFormValues.value = pickPrefillValues(prefillConfig, config.rule)
      dynamicFormRule.value = config.rule
      dynamicFormOptions.value = {
        ...(config.options || {}),
        submitBtn: false,
        resetBtn: false,
      }
      // 更换通道名称时强制重建表单实例
      dynamicFormKey.value += 1
    }
  } catch (err) {
    if (requestId !== dynamicFormRequestId.value) return
    //console.error('加载通道连接配置失败', err)
    //ElMessage.error('加载连接配置失败')
  } finally {
    if (requestId === dynamicFormRequestId.value) {
      dynamicFormLoading.value = false
    }
  }
}

/** form-create 渲染完成后获取 API 实例（v-model 绑的是表单数据，不能用它调 validate/formData） */
function handleDynamicFormMounted($f) {
  dynamicFormApi.value = $f
}

/** 类型选择回调：加载该类型的连接配置表单，并重置载荷格式。 */
function handleChannelSelect({ type }) {
  formData.type = type
  resetOutputConfig()
  loadDynamicForm(type)
  if (editorKind.value === 'mapping' && isEmptyMappingState(mappingState)) {
    Object.assign(mappingState, defaultMappingState())
    mappingStateId.value += 1
    mappingTouched.value = true
  } else if (editorKind.value === 'script' && isEmptyScriptState(scriptState)) {
    Object.assign(scriptState, defaultScriptState())
    scriptStateId.value += 1
    scriptTouched.value = true
  }
  nextTick(() => scheduleAutoPreview())
}

// ---------- 表单校验 / configJson 收集 ----------
async function validateForms() {
  if (!formRef.value) return false
  try {
    await formRef.value.validate()
  } catch {
    return false
  }
  if (dynamicFormRule.value.length && dynamicFormApi.value) {
    try {
      await dynamicFormApi.value.validate()
    } catch {
      ElMessage.warning(t('channel.completeConnectConfig'))
      return false
    }
  }
  return true
}

// HTTP/Webhook 的鉴权字段与 authType 一一对应（见迁移 021 的 control 联动）。
// 被联动隐藏的字段仍会出现在 formData 里，这里按当前鉴权方式丢弃其余凭证，
// 避免切换鉴权方式后旧 token / 密码残留进 configJson。
const HTTP_AUTH_CREDENTIAL_FIELDS = {
  bearer: ['bearerToken'],
  basic: ['basicUsername', 'basicPassword'],
  'hmac-sha256': ['hmacSecret', 'hmacHeader', 'hmacPrefix'],
}

function pruneHttpAuthFields(config) {
  if (formData.type !== 'http' || !('authType' in config)) return config
  const keep = new Set(HTTP_AUTH_CREDENTIAL_FIELDS[config.authType] || [])
  Object.values(HTTP_AUTH_CREDENTIAL_FIELDS)
    .flat()
    .forEach((field) => {
      if (!keep.has(field)) delete config[field]
    })
  return config
}

/** 统一构造提交/预览/测试发送的 configJson，保证三处完全一致。 */
function buildConfigJson() {
  const config = { ...(dynamicFormApi.value?.formData?.() ?? {}) }
  const meta = selectedTypeMeta.value
  // 元数据尚未加载时不要把已有的 format/mapping 删掉：按当前格式推断即可。
  const messageBased = meta
    ? meta.messageBased
    : outputFormat.value === 'mapping-json' || outputFormat.value === 'script'
  if (messageBased) {
    // 组订阅列表：空数组 = 收全量（含未分组），非空 = 只收所选组。
    // 用 delete 而不是写空数组，保持旧配置的字节形态不变。
    if (subscribedGroupIds.value.length) {
      config.groupIds = [...subscribedGroupIds.value]
    } else {
      delete config.groupIds
    }
    config.format = outputFormat.value || meta?.defaultFormat || 'native-json'
    if (editorKind.value) {
      const editor = activeEditor()
      if (!editor) {
        throw new Error(
          t(
            editorKind.value === 'script'
              ? 'channel.scriptRequired'
              : 'channel.mappingRequired',
          ),
        )
      }
      config.mapping = editor.buildMapping()
    } else {
      delete config.mapping
    }
  } else {
    delete config.format
    delete config.mapping
    delete config.groupIds
  }
  return pruneHttpAuthFields(config)
}

function buildConfigOrWarn() {
  try {
    return buildConfigJson()
  } catch (err) {
    if (err instanceof MappingValidationError) {
      showMappingError(err)
    } else {
      ElMessage.warning(
        err instanceof SyntaxError
          ? t('channel.mappingJsonInvalid')
          : err?.message || t('channel.mappingJsonInvalid'),
      )
    }
    return null
  }
}

// ---------- 测试连接 ----------
async function handleTestConnectivity() {
  if (!formData.type) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  testingConnection.value = true
  try {
    const res = await testConnectivity({
      type: formData.type,
      name: formData.name,
      configJson,
    })
    // 兼容后端返回布尔值 / { connected } / { success } 结构；无显式失败标记则视为成功
    const connected =
      typeof res === 'boolean' ? res : res?.connected ?? res?.success ?? true
    if (connected === false || connected === 'false') {
      ElMessage.error(t('channel.connectionFailed'))
    } else {
      ElMessage.success(t('channel.connectionSuccess'))
    }
  } catch (err) {
    // 失败时全局拦截器已弹出后端错误信息，此处静默
  } finally {
    testingConnection.value = false
  }
}

// ---------- 载荷预览 / 测试发送 ----------
/** 无编辑器的格式（native-json 等）预览结果直接追加在按钮下方：
 *  对话框 body 已经滚到底时新内容仍在可视区外，看起来像「点了没反应」，
 *  结果渲染出来后把它滚进视野。 */
function revealPayloadPreview() {
  nextTick(() => {
    payloadPreviewRef.value?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  })
}

async function handlePreview() {
  cancelAutoPreview()
  previewRequestId.value += 1
  const requestId = previewRequestId.value
  manualPreviewId.value = requestId
  // 映射/脚本走编码专用接口：不要求连接配置已经填完整。
  if (editorKind.value) {
    const editor = activeEditor()
    if (!editor) return
    let mapping
    try {
      mapping = editor.buildMapping()
    } catch (err) {
      showMappingError(err)
      return
    }
    previewing.value = true
    try {
      const res = await previewPushMapping({ format: outputFormat.value, mapping })
      if (requestId === previewRequestId.value) previewResult.value = res
    } catch {
      // 全局拦截器已提示
    } finally {
      // 自动预览/编辑会让 previewRequestId 前进，但按钮 loading 必须由
      // 这一次手动请求自己收尾，否则会一直转圈。
      if (manualPreviewId.value === requestId) previewing.value = false
    }
    return
  }

  if (!formData.type) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  previewing.value = true
  try {
    const res = await previewPushChannel({
      type: formData.type,
      configJson,
    })
    if (manualPreviewId.value === requestId) {
      previewResult.value = res
      revealPayloadPreview()
    }
  } catch {
    // 全局拦截器已提示
  } finally {
    if (manualPreviewId.value === requestId) previewing.value = false
  }
}

async function handleTestSend() {
  if (!formData.type) {
    ElMessage.warning(t('channel.selectChannelFirst'))
    return
  }
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  try {
    await ElMessageBox.confirm(
      t('channel.testSendConfirm'),
      t('channel.testSendTitle'),
      {
        type: 'warning',
        confirmButtonText: t('common.confirm'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return
  }
  testSending.value = true
  try {
    const res = await testSendPushChannel({
      type: formData.type,
      configJson,
    })
    if (res?.success) {
      ElMessage.success(
        t('channel.testSendSuccess', {
          code: res.statusCode ?? '-',
          ms: res.durationMs ?? 0,
        }),
      )
    } else {
      ElMessage.error(res?.error || t('channel.testSendFailed'))
    }
  } catch {
    // 全局拦截器已提示
  } finally {
    testSending.value = false
  }
}

// ---------- 操作 ----------
function handleAdd() {
  isEdit.value = false
  detailLoading.value = false
  // 清空上次残留的表单值，避免新增弹框缓存类型 / 名称 / 描述
  formData.id = ''
  formData.type = ''
  formData.name = ''
  formData.description = ''
  formData.status = 1
  outputFormat.value = 'native-json'
  subscribedGroupIds.value = []
  groupOptions.value = []
  resetMappingState()
  dialogVisible.value = true
  loadGroupOptions()
  // 新增时等待用户选择类型后再加载连接配置
  loadDynamicForm('')
}

async function handleEdit(row) {
  isEdit.value = true
  detailLoading.value = true
  formData.id = row.id
  formData.type = row.type || row.name
  formData.name = row.name
  formData.description = row.description ?? ''
  formData.status = row.status ?? 1
  // 先清掉上一通道的格式/映射，详情加载完成前不允许测试/预览/保存。
  outputFormat.value = 'native-json'
  resetMappingState()
  dialogVisible.value = true

  try {
    // 类型元数据与通道详情并行加载；元数据必须先就绪，否则 messageBased
    // 判断会把已有 format/mapping 当成非消息类配置删掉。
    const [detail] = await Promise.all([
      getChannelById(row.id).catch((err) => {
        console.error('获取通道详情失败', err)
        return null
      }),
      loadChannelTypes().catch(() => []),
    ])
    let prefillConfig = detail?.configJson ?? null
    // 兜底：列表行若自带 configJson 也直接使用
    if (prefillConfig == null && row.configJson != null) {
      prefillConfig = row.configJson
    }
    await applyOutputConfig(prefillConfig)
    // 组订阅不在 form-create 规则里，必须显式回填；兼容二次编码的历史 configJson。
    let cfg = prefillConfig
    for (let i = 0; i < 3 && typeof cfg === 'string'; i += 1) {
      cfg = safeParseJSON(cfg)
    }
    subscribedGroupIds.value = Array.isArray(cfg?.groupIds)
      ? cfg.groupIds.filter(Boolean)
      : []
    loadGroupOptions()
    // 编辑时按类型加载连接配置，并回填已有填写值
    loadDynamicForm(formData.type, prefillConfig)
  } finally {
    detailLoading.value = false
    nextTick(() => scheduleAutoPreview())
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('channel.deleteConfirm', { name: row.name }), t('channel.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
    await deleteChannel(row.id)
    ElMessage.success(t('channel.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

function handleDialogClose() {
  formRef.value?.resetFields()
  detailLoading.value = false
  outputFormat.value = 'native-json'
  resetMappingState()
  loadDynamicForm('')
}

// ---------- 通道监控 ----------
// 运行状态来自 /push/status（同一份仪表盘数据），最近报文来自
// /push/recentMessages（每通道内存环形缓冲，最近 100 条）。抽屉打开期间轮询。
const monitorVisible = ref(false)
const monitorRow = ref(null)
const monitorStatus = ref(null)
const monitorRecent = ref({ active: false, messages: [] })
const monitorLoading = ref(false)
// 已展开的报文行（按 seq）：轮询替换数组后仍保持展开。
const expandedSeqKeys = ref([])
let monitorTimer = null

const monitorActive = computed(() => monitorRecent.value?.active ?? false)
const monitorMessages = computed(() => monitorRecent.value?.messages ?? [])
// 有展开行时暂停拉取报文：新报文从顶部插入会把正在查看的载荷不断往下顶。
const hasExpandedRow = computed(() => expandedSeqKeys.value.length > 0)

const monitorType = computed(() => monitorRow.value?.type || monitorRow.value?.name || '')
// OPC UA 是「服务端」通道：没有下游连接，也不做断网缓存。
// 它的运行状态、地址空间与客户端订阅走单独一套展示。
const monitorOpcua = computed(() => monitorType.value === 'opcua')
const monitorExtra = computed(() => monitorStatus.value?.extra || {})
const monitorSupportsSpool = computed(() => !monitorOpcua.value)

// 消息类通道（mqtt/http）：一次下发即一条报文；时序库（tdengine/influxdb）：
// 一次下发是一个聚合写入批次。元数据未加载时按消息类展示（保持旧行为）。
const monitorMessageBased = computed(() => {
  const meta = metaOf(monitorRow.value?.type)
  return meta ? meta.messageBased !== false : true
})

// 消息通道说「报文」，时序库说「写入批次」——一次写入是聚合窗，不是单条报文。
// OPC UA 的记录是「一批采集数据写入地址空间」，措辞再单独一套。
const monitorSubtitle = computed(() => {
  if (monitorOpcua.value) return t('channel.monitor.subtitleOpcua')
  return monitorMessageBased.value
    ? t('channel.monitor.subtitle')
    : t('channel.monitor.subtitleBatch')
})
const monitorRecentTitle = computed(() => {
  if (monitorOpcua.value) return t('channel.monitor.recentTitleOpcua')
  return monitorMessageBased.value
    ? t('channel.monitor.recentTitle')
    : t('channel.monitor.recentTitleBatch')
})
const monitorRecentSubtitle = computed(() => {
  if (monitorOpcua.value) return t('channel.monitor.recentSubtitleOpcua')
  return monitorMessageBased.value
    ? t('channel.monitor.recentSubtitle')
    : t('channel.monitor.recentSubtitleBatch')
})
const monitorEmptyTitle = computed(() => {
  if (monitorOpcua.value) return t('channel.monitor.noMessagesOpcua')
  return monitorMessageBased.value
    ? t('channel.monitor.noMessages')
    : t('channel.monitor.noMessagesBatch')
})
const monitorEmptyHint = computed(() => {
  if (monitorOpcua.value) return t('channel.monitor.noMessagesHintOpcua')
  return monitorMessageBased.value
    ? t('channel.monitor.noMessagesHint')
    : t('channel.monitor.noMessagesHintBatch')
})

const monitorConnectionText = computed(() => {
  const s = monitorStatus.value
  if (!s?.running) return t('channel.monitor.stopped')
  if (monitorOpcua.value) {
    return s.connected
      ? t('channel.monitor.listening')
      : t('channel.monitor.notListening')
  }
  return s.connected ? t('channel.monitor.connected') : t('channel.monitor.disconnected')
})

const monitorConnectionClass = computed(() =>
  monitorStatus.value?.running && monitorStatus.value?.connected ? 'is-green' : 'is-gray',
)

const monitorStats = computed(() => {
  const s = monitorStatus.value || {}
  const extra = monitorExtra.value
  const value = (v) => (v == null || v === '' ? '—' : String(v))
  const stats = [
    { key: 'queue', label: t('channel.monitor.queueDepth'), value: value(s.queueDepth) },
  ]
  // OPC UA 没有断网缓存，spoolDepth 恒 0，展示它只会误导。
  if (monitorSupportsSpool.value) {
    stats.push({ key: 'spool', label: t('channel.monitor.spoolDepth'), value: value(s.spoolDepth) })
  }
  if (monitorOpcua.value) {
    // publishCount/rowsCount 在后端已对齐为「写入批次 / 更新点位」。
    stats.push(
      { key: 'publish', label: t('channel.monitor.updateBatches'), value: value(s.publishCount) },
      { key: 'rows', label: t('channel.monitor.updatePoints'), value: value(s.rowsCount) },
      { key: 'subscriptions', label: t('channel.monitor.subscriptions'), value: value(extra.subscriptions) },
      { key: 'monitoredItems', label: t('channel.monitor.monitoredItems'), value: value(extra.monitoredItems) },
    )
  } else if (monitorMessageBased.value) {
    stats.push({
      key: 'publish',
      label: t('channel.monitor.publishCount'),
      value: value(s.publishCount),
    })
  } else {
    // 时序库：publishCount 是刷盘批次次数，行数单独统计。
    stats.push({
      key: 'publish',
      label: t('channel.monitor.writeBatches'),
      value: value(s.publishCount),
    })
    stats.push({
      key: 'rows',
      label: t('channel.monitor.rowCount'),
      value: value(s.rowsCount),
    })
  }
  stats.push(
    { key: 'dropped', label: t('channel.monitor.droppedCount'), value: value(s.droppedCount) },
    { key: 'lastSuccess', label: t('channel.monitor.lastSuccess'), value: value(s.lastSuccessTime) },
    {
      key: 'lastError',
      label: t('channel.monitor.lastError'),
      value: value(s.error),
      isError: !!s.error,
    },
  )
  return stats
})

// OPC UA 地址空间与点位新鲜度（后端 statusExtra）。
const monitorOpcuaDetails = computed(() => {
  const extra = monitorExtra.value
  // 通道未运行时后端不返回 Extra，整块地址空间信息一起隐藏，避免一屏 '—'。
  if (!monitorOpcua.value || !Object.keys(extra).length) return []
  const value = (v) => (v == null || v === '' ? '—' : String(v))
  const badQuality = Number(extra.pointsBadQuality) || 0
  return [
    { key: 'devices', label: t('channel.monitor.addressSpaceDevices'), value: value(extra.addressSpaceDevices) },
    { key: 'points', label: t('channel.monitor.points'), value: value(extra.points) },
    { key: 'withData', label: t('channel.monitor.pointsWithData'), value: value(extra.pointsWithData) },
    {
      key: 'badQuality',
      label: t('channel.monitor.pointsBadQuality'),
      value: value(extra.pointsBadQuality),
      isError: badQuality > 0,
    },
    {
      key: 'stale',
      label: t('channel.monitor.pointsStale'),
      value: value(extra.pointsStale),
      isError: (Number(extra.pointsStale) || 0) > 0,
    },
    {
      key: 'oldest',
      label: t('channel.monitor.oldestValueAge'),
      value: extra.oldestValueAgeSec == null ? '—' : `${extra.oldestValueAgeSec} s`,
    },
    { key: 'rebuilds', label: t('channel.monitor.rebuildCount'), value: value(extra.rebuildCount) },
    { key: 'lastRebuild', label: t('channel.monitor.lastRebuildTime'), value: value(extra.lastRebuildTime) },
  ]
})

const monitorEndpoints = computed(() =>
  Array.isArray(monitorExtra.value.endpoints) ? monitorExtra.value.endpoints : [],
)

// showLoading 只用于首次加载与手动刷新：每 3 秒的自动轮询不应反复盖遮罩，
// 否则画面会周期性闪一下。
// skipMessages 用于「有展开行」的自动轮询：只刷新状态卡，不动报文表，
// 避免新报文把正在查看的载荷往下顶。手动刷新始终拉取报文。
async function refreshMonitor(showLoading = false, { skipMessages = false } = {}) {
  if (!monitorRow.value) return
  if (showLoading) monitorLoading.value = true
  try {
    const [statusRes, recentRes] = await Promise.all([
      getPushStatus(),
      skipMessages
        ? Promise.resolve(null)
        : getPushRecentMessages({ channelId: monitorRow.value.id, limit: 100 }),
    ])
    const list = Array.isArray(statusRes) ? statusRes : (statusRes?.records ?? [])
    monitorStatus.value = list.find((s) => s.id === monitorRow.value.id) ?? null
    if (skipMessages || !recentRes) return

    monitorRecent.value = {
      active: !!recentRes?.active,
      messages: recentRes?.messages ?? [],
    }
    // 展开行滑出 100 条窗口后清掉对应 key，避免集合无限增长。
    const seqs = new Set(monitorRecent.value.messages.map((m) => m.seq))
    expandedSeqKeys.value = expandedSeqKeys.value.filter((key) => seqs.has(key))
  } catch {
    // 全局拦截器已提示；保留上一次结果，避免轮询失败时清空画面
  } finally {
    if (showLoading) monitorLoading.value = false
  }
}

function handleMonitor(row) {
  monitorRow.value = row
  monitorStatus.value = null
  monitorRecent.value = { active: false, messages: [] }
  expandedSeqKeys.value = []
  monitorVisible.value = true
}

/** 同步展开行：expand-change 第二个参数是当前仍展开的行集合。 */
function handleExpandChange(row, expandedRows) {
  const key = row?.seq
  if (key == null) return
  const keys = new Set(expandedSeqKeys.value)
  if ((expandedRows || []).some((r) => r.seq === key)) keys.add(key)
  else keys.delete(key)
  expandedSeqKeys.value = [...keys]
}

function handleMonitorOpen() {
  refreshMonitor(true)
  stopMonitorTimer()
  monitorTimer = setInterval(
    () => refreshMonitor(false, { skipMessages: hasExpandedRow.value }),
    3000,
  )
}

function handleMonitorClosed() {
  stopMonitorTimer()
}

function stopMonitorTimer() {
  if (monitorTimer) {
    clearInterval(monitorTimer)
    monitorTimer = null
  }
}

function deviceGroupText(row) {
  return [row.deviceCode, row.groupCode].filter(Boolean).join(' / ') || '—'
}

function messageStatusText(row) {
  if (row.statusCode) return String(row.statusCode)
  return row.error ? t('channel.monitor.failed') : t('channel.monitor.ok')
}

function formatBytes(n) {
  const size = Number(n) || 0
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(1)} MB`
}

onBeforeUnmount(() => {
  cancelAutoPreview()
  previewRequestId.value += 1
  stopMonitorTimer()
})

async function handleSubmit() {
  if (!(await validateForms())) return
  const configJson = buildConfigOrWarn()
  if (!configJson) return
  submitting.value = true
  try {
    const payload = {
      type: formData.type,
      name: formData.name.trim(),
      description: formData.description.trim(),
      status: formData.status,
      configJson,
    }
    if (isEdit.value) {
      payload.id = formData.id
      await updateChannel(payload)
      ElMessage.success(t('channel.editSuccess'))
    } else {
      await addChannel(payload)
      ElMessage.success(t('channel.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {
    // 全局拦截器已提示
  } finally {
    submitting.value = false
  }
}

// ---------- 初始化 ----------
onMounted(() => {
  loadChannelTypes().catch(() => {})
  fetchList()
})
</script>

<style scoped>
/* 连接配置区：在对话框里用发丝线接续基本信息，不再另起一张卡片 */
.channel-section {
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--line-soft);
}

.channel-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}

.channel-section-headline {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.channel-section-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 28px;
  height: 28px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 8px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.08);
}

.channel-section-mark .el-icon {
  font-size: 15px;
}

.channel-section-name {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.channel-section-sub {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--ink-4);
}

.channel-format-select {
  flex: none;
  width: 200px;
}

.dynamic-form-wrapper {
  min-height: 60px;
  padding-bottom: 8px;
}

.channel-empty-config {
  margin: 0;
  padding: 10px 0;
  font-size: 12.5px;
  color: var(--ink-4);
}

/* 插件声明的连接参数收成一列，避免通栏输入框压着一个小步进器
   （与设备对象页的协议参数表单同一写法） */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 280px;
}

.channel-preview-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 12px;
}

.channel-preview-hint {
  font-size: 12px;
  color: var(--ink-4);
}

.channel-preview-hint code {
  font-family: var(--font-mono);
  color: var(--ink-3);
}

.mapping-preview {
  max-height: 240px;
  margin: 10px 0 0;
  padding: 12px 14px;
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  color: var(--ink-2);
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--surface-2, #f8fafc);
  border: 1px solid var(--line-soft);
  border-radius: 10px;
}

/* ---- 通道监控抽屉 ---- */
.channel-monitor {
  padding: 4px 4px 12px;
}

.channel-monitor-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.channel-monitor-name {
  margin: 2px 0 0;
  font-size: 20px;
  font-weight: 650;
  color: var(--ink);
}

.channel-monitor-sub {
  margin: 4px 0 0;
  font-size: 12.5px;
  color: var(--ink-4);
}

.channel-monitor-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
  margin-top: 18px;
}

.monitor-stat {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  padding: 12px 14px;
  background: var(--surface-2, #f8fafc);
  border: 1px solid var(--line-soft);
  border-radius: 10px;
}

/* 地址空间指标块：与顶部状态卡同一张卡面，只是换了一组字段 */
.channel-monitor-stats.is-details {
  margin-top: 12px;
}

.channel-monitor-endpoints {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
}

.channel-monitor-endpoint {
  padding: 3px 8px;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink-2);
  word-break: break-all;
  background: var(--surface-2, #f8fafc);
  border: 1px solid var(--line-soft);
  border-radius: 6px;
}

.monitor-stat-label {
  font-size: 11.5px;
  color: var(--ink-4);
}

.monitor-stat-value {
  overflow: hidden;
  font-size: 14px;
  font-weight: 600;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.monitor-stat-error {
  color: #be123c;
}

.channel-monitor-hint {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--ink-4);
}

.channel-monitor-section {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--line-soft);
}

.channel-monitor-section-head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.channel-monitor-section-title {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.channel-monitor-section-sub {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--ink-4);
}

.channel-monitor-refresh-hint {
  font-size: 11.5px;
  color: var(--ink-4);
  white-space: nowrap;
}

.channel-monitor-refresh-hint.is-paused {
  color: var(--warning, #b45309);
}

.monitor-payload {
  max-height: 220px;
  margin: 4px 12px 8px !important;
}

.monitor-target {
  margin-left: 6px;
}

.monitor-drain-pill {
  transform: scale(0.92);
  transform-origin: left center;
}

.monitor-error-text {
  color: #be123c;
}

@media (max-width: 720px) {
  .channel-monitor-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
