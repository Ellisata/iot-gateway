<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  点位范围扫描（设备地址页的「自动发现点位」）。

  **这个功能产出的是一份「候选清单」，不是这台设备的点位表。** 除少数协议
  （OPC UA 的 Browse、ABB 的信号表）之外，设备没有任何标准手段能枚举自己有哪些
  点位。所以界面上的措辞必须一直说清楚：列出来的是「这个范围里读得到的地址」，
  非零不一定有意义、读得到 0 的也可能是有效值。这段说明由插件的 description 提供
  （每个协议的局限不同），它就在参数区上方常驻。
-->
<template>
  <el-dialog
    :model-value="visible"
    class="iot-dialog point-dialog"
    :title="spec?.title || t('deviceAddress.pointDiscovery.title')"
    width="1080px"
    :close-on-click-modal="false"
    @update:model-value="emit('update:visible', $event)"
    @closed="handleClosed"
  >
    <!-- 能力不可用：按钮那边已经会置灰，这里再兜一道（比如开着弹窗时插件被热升级掉） -->
    <div
      v-if="capability && !capability.supported"
      class="iot-notice is-warning is-inline"
    >
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><AlertTriangle /></el-icon>
      </span>
      <p class="iot-notice-text">{{ t('deviceAddress.pointDiscovery.unsupported') }}</p>
    </div>
    <div
      v-else-if="capability && !capability.enabled"
      class="iot-notice is-warning is-inline"
    >
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><AlertTriangle /></el-icon>
      </span>
      <p class="iot-notice-text">{{ t('deviceAddress.pointDiscovery.disabled') }}</p>
    </div>

    <template v-else>
      <!-- ========== 参数 ========== -->
      <div class="iot-notice is-info is-inline pd-notice">
        <span class="iot-notice-mark" aria-hidden="true">
          <el-icon><InfoCircle /></el-icon>
        </span>
        <p class="iot-notice-text">
          {{ spec?.description || t('deviceAddress.pointDiscovery.defaultNotice') }}
        </p>
      </div>

      <!-- 扫描参数：**不加分组标题、也不套 el-form-item** —— 插件的字段名自己就是
           标签；套进 el-form-item 就是在已经缩进 label-width 的内容区里再缩进一次，
           子 label 落在"别的行的控件"处、子控件再右移一整列。
           扫描页（device-scan/IndexView.vue）是同一个写法，改动请两边一起改。 -->
      <div v-if="fcRule.length" class="dynamic-form-wrapper">
        <form-create
          :key="formKey"
          ref="fcRef"
          :rule="fcRule"
          :option="fcOption"
          @mounted="handleFcReady"
        />
      </div>

      <div class="pd-actions">
        <!-- 用**实际生效**的预算（effectiveBudgetMs），不是插件声明的那个：
             网关会把同步 HTTP 这条链路的预算钳到 9s 以内，而插件可以声明 20s，
             显示声明值会让用户按 20 秒预估、实际 7 秒就停了（还会看到"未扫完"）。 -->
        <span class="pd-budget">
          {{ t('deviceAddress.pointDiscovery.budgetHint', { s: Math.round((spec?.effectiveBudgetMs || spec?.maxBudgetMs || 0) / 1000), n: spec?.maxPoints ?? 0 }) }}
        </span>
        <div class="pd-actions-buttons">
          <el-button v-if="result" @click="handleReset">{{ t('deviceAddress.pointDiscovery.rescan') }}</el-button>
          <el-button
            type="primary"
            :icon="Search"
            :loading="scanning"
            :disabled="!capability?.supported || !capability?.enabled"
            @click="handleScan"
          >
            {{ scanning ? t('deviceAddress.pointDiscovery.scanning') : (result ? t('deviceAddress.pointDiscovery.scanAgain') : t('deviceAddress.pointDiscovery.scan')) }}
          </el-button>
        </div>
      </div>

      <!-- ========== 结果 ========== -->
      <template v-if="result">
        <div class="pd-divider" />

        <!-- 中途断链：**下面这些点位是有效的**，但清单残缺，必须说在前面。
             与"扫描失败"（拦截器弹错误、这里什么都没有）是两回事 ——
             文案由服务端拼（根因是动态的），white-space: pre-line 保住里面的换行。 -->
        <div v-if="result.warning" class="iot-notice is-warning is-inline pd-notice">
          <span class="iot-notice-mark" aria-hidden="true">
            <el-icon><AlertTriangle /></el-icon>
          </span>
          <p class="iot-notice-text">{{ result.warning }}</p>
        </div>

        <!-- 统计条固定在结果区顶部：这一屏最容易被误读的就是"扫到多少"。
             sampled 为假（原生枚举协议，不读采样值）时换一套措辞：
             那边没有"读得到/读不到"，只有"列出了/跳过了"，而跳过的原因与地址无关。 -->
        <div class="pd-stats">
          <span class="pd-stat">
            {{ sampled
              ? t('deviceAddress.pointDiscovery.statScanned', { n: result.scanned })
              : t('deviceAddress.pointDiscovery.statScannedNative', { n: result.scanned }) }}
          </span>
          <span class="pd-stat">
            {{ sampled
              ? t('deviceAddress.pointDiscovery.statReadable', { n: result.readable })
              : t('deviceAddress.pointDiscovery.statReadableNative', { n: result.readable }) }}
          </span>
          <span class="pd-stat is-muted">
            {{ sampled
              ? t('deviceAddress.pointDiscovery.statUnreadable', { n: result.unreadable })
              : t('deviceAddress.pointDiscovery.statUnreadableNative', { n: result.unreadable }) }}
          </span>
          <span v-if="sampled" class="pd-stat is-muted">
            {{ t('deviceAddress.pointDiscovery.statZero', { n: zeroCount }) }}
          </span>
          <span class="pd-stat is-muted">
            {{ t('deviceAddress.pointDiscovery.statElapsed', { s: (result.elapsedMs / 1000).toFixed(1) }) }}
          </span>
        </div>

        <!-- 两个标志必须分开显示：截断是"结果条数到了上限"，未扫完是"范围还没扫到"，
             它们与"读不到"是三回事，合并成一句会让用户得出错误的结论 -->
        <div v-if="result.truncated" class="iot-notice is-warning is-inline pd-notice">
          <span class="iot-notice-mark" aria-hidden="true">
            <el-icon><AlertTriangle /></el-icon>
          </span>
          <p class="iot-notice-text">
            {{ sampled
              ? t('deviceAddress.pointDiscovery.truncated', { n: spec?.maxPoints ?? 0 })
              : t('deviceAddress.pointDiscovery.truncatedNative', { n: spec?.maxPoints ?? 0 }) }}
          </p>
        </div>
        <div v-if="result.incomplete" class="iot-notice is-warning is-inline pd-notice">
          <span class="iot-notice-mark" aria-hidden="true">
            <el-icon><AlertTriangle /></el-icon>
          </span>
          <p class="iot-notice-text">{{ t('deviceAddress.pointDiscovery.incomplete') }}</p>
        </div>

        <div class="pd-controls">
          <!-- 「显示 0 值」只在**本次结果真的带采样值**时才有意义。
               原生枚举协议（Rockwell.CIP）只列标签与类型、不读值，每一行的 value
               都是空的：开关开着时一切正常，一关就整张表清空，而"值为 0"的统计
               还会显示成"全部都是 0" —— 比没有这个开关更容易误导。 -->
          <div v-if="sampled" class="pd-zero-toggle">
            <el-switch v-model="showZero" :active-text="t('deviceAddress.pointDiscovery.showZero')" />
            <span class="pd-zero-hint">
              {{ t('deviceAddress.pointDiscovery.valueHint', { type: result.assumedCommonDataType || '—' }) }}
            </span>
          </div>
          <div class="pd-bulk">
            <el-select
              v-model="bulkType"
              :placeholder="t('deviceAddress.pointDiscovery.bulkTypePlaceholder')"
              style="width: 200px"
              :loading="dataTypeLoading"
            >
              <el-option
                v-for="item in dataTypeOptions"
                :key="item.value"
                :label="item.label"
                :value="item.value"
              />
            </el-select>
            <el-button :disabled="!bulkType || selectedRows.length === 0" @click="applyBulkType">
              {{ t('deviceAddress.pointDiscovery.applyBulkType', { n: selectedRows.length }) }}
            </el-button>
          </div>
        </div>

        <el-table
          ref="tableRef"
          class="iot-table is-compact pd-table"
          :data="pagedRows"
          size="small"
          max-height="380"
          row-key="name"
          @selection-change="handleSelectionChange"
        >
          <!-- reserve-selection：切换「显示 0 值」与翻页都会换掉 data，没有它已勾选的行
               会被清空。el-table 在 reserveSelection 为真时不会在换 data 时清理 selection
               （store/index.mjs 的 setData 分支），所以 selectedRows 始终是全量、
               而不是当前页那几条；勾选态按 row-key 重算，翻回来还是勾着的。 -->
          <el-table-column type="selection" width="46" reserve-selection />
          <el-table-column :label="t('deviceAddress.addressName')" width="140">
            <template #default="{ row }">
              <!-- 地址列**只读**：名字非法（该协议解析不出）的后果是这个点位永远没有值
                   （只报「地址无效」，要改配置才恢复），不能让用户顺手改它 -->
              <span class="iot-mono">{{ row.name }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('deviceAddress.pointDiscovery.currentValue')" width="130">
            <template #default="{ row }">
              <span v-if="row.value" class="iot-mono">{{ row.value }}</span>
              <!-- 没有采样值时留 note（如"当前值为 0"），**不能回退成 '0'** ——
                   原生枚举协议压根没读值，写个 0 会被读成"采到的值是 0"。
                   破折号才对：这一格本来就没有内容。 -->
              <span v-else class="iot-cell-muted">
                {{ row.note || (sampled ? '0' : '—') }}
              </span>
            </template>
          </el-table-column>
          <el-table-column :label="t('deviceAddress.tag')" min-width="160">
            <template #default="{ row }">
              <el-input v-model="row.label" size="small" :placeholder="row.name" maxlength="100" />
            </template>
          </el-table-column>
          <!-- 三个行内下拉都必须 :persistent="false"。
               el-select 的 persistent 默认是 true，而 tooltip 的渲染条件是
               `persistent ? true : open` —— 也就是**每一行挂载时就把自己全部 option
               渲染出来**（还要连带一个 teleport 到 body 的 popper + scrollbar）。
               三列加起来：50 行 × (15 + 2 + 5) = 1100 个 el-option + 150 个下拉
               + 150 份被 teleport 出去的 popper 子树，翻一页就建一遍、拆一遍 ——
               这就是翻页"顿一下"的来源。
               persistent=false 后下拉内容只在展开时才渲染（同一时刻最多一份），
               而选中项的 label 不会丢：非持久模式下 el-select 会在关闭后用
               manuallyRenderSlots(slots.default()) 重建选项登记表（见 select.vue
               里那个 watch）。行为完全不变，只是不预先铺开。 -->
          <el-table-column :label="t('deviceAddress.commonDataType')" width="150">
            <template #default="{ row }">
              <el-select
                v-model="row.commonDataType"
                size="small"
                :persistent="false"
                :loading="dataTypeLoading"
              >
                <el-option
                  v-for="item in dataTypeOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column :label="t('deviceAddress.rwPermission')" width="120">
            <template #default="{ row }">
              <el-select v-model="row.rwPermission" size="small" :persistent="false">
                <el-option
                  v-for="item in rwOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column :label="t('deviceAddress.scanFrequency')" width="150">
            <template #default="{ row }">
              <el-select v-model="row.scanFrequency" size="small" :persistent="false">
                <el-option
                  v-for="item in scanFrequencyOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column :label="t('common.description')" min-width="150">
            <template #default="{ row }">
              <el-input v-model="row.description" size="small" maxlength="200" />
            </template>
          </el-table-column>

          <!-- 空态收进表格的 #empty：否则表格自带的「暂无数据」和下面另起一块空态会同时出现 -->
          <template #empty>
            <div class="iot-empty">
              <span class="iot-empty-mark" aria-hidden="true">
                <el-icon><Radar /></el-icon>
              </span>
              <p class="iot-empty-title">
                {{ rows.length === 0
                  ? (sampled
                    ? t('deviceAddress.pointDiscovery.noResult')
                    : t('deviceAddress.pointDiscovery.noResultNative'))
                  : t('deviceAddress.pointDiscovery.allZeroHidden') }}
              </p>
            </div>
          </template>
        </el-table>

        <!-- 分页不是"锦上添花"，是这张表能不能活下来的前提：结果最多 maxPoints 条
             （Modbus 是 2000），而每一行带 3 个下拉 + 2 个输入框，三个下拉展开共
             22 个 option。整表一次性挂载是**五万多个组件实例**，主线程会被 patch
             占满几十秒，浏览器表现为"页面假死"—— 填 2001 只是刚好把结果顶到 2000 条
             的那个人。只渲染当前页，组件数就降到千级。

             每页的默认值也不能随便定（见 pageSize 的注释）：它既是"一页几百个组件"
             这个量级的来源，也直接决定上面开关的迟滞有多久 —— max-height=380 一屏
             约 9 行，默认挂 20 行已经超出两屏，再多就是白挂。 -->
        <div v-if="visibleRows.length > 0" class="iot-pager is-flush">
          <el-pagination
            v-model:current-page="currentPage"
            v-model:page-size="pageSize"
            :page-sizes="[20, 50, 100]"
            :total="visibleRows.length"
            layout="total, sizes, prev, pager, next, jumper"
            background
          />
        </div>

      </template>
    </template>

    <template #footer>
      <div class="flex items-center justify-end gap-2">
        <el-button @click="emit('update:visible', false)">{{ t('common.close') }}</el-button>
        <el-button
          type="primary"
          :loading="creating"
          :disabled="selectedRows.length === 0"
          @click="handleCreate"
        >
          {{ t('deviceAddress.pointDiscovery.createAddresses', { n: selectedRows.length }) }}
        </el-button>
      </div>
    </template>
  </el-dialog>

  <ImportResultDialog v-model:visible="importResultVisible" :result="importResult" />
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import {
  IconAlertTriangle as AlertTriangle,
  IconInfoCircle as InfoCircle,
  IconRadar as Radar,
  IconSearch as Search,
} from '@tabler/icons-vue'

import ImportResultDialog from '@/components/ImportResultDialog.vue'
import { usePagedRows } from '@/composables/usePagedRows'
import { useScanFrequencyOptions } from '@/composables/useScanFrequencyOptions'
import { createAddressesFromScan, discoverPoints, getPointCapability } from '@/api/modules/deviceScan'
import { getDataTypes } from '@/api/modules/protocol'

const props = defineProps({
  visible: { type: Boolean, default: false },
  deviceId: { type: String, default: '' },
  protocolName: { type: String, default: '' },
})

const emit = defineEmits(['update:visible', 'created'])

const { t } = useI18n()

// ---------- 能力 ----------
const capability = ref(null)
const spec = computed(() => capability.value?.spec ?? null)

// ---------- 参数表单 ----------
const fcRef = ref(null)
const fcApi = ref(null)
const fcRule = ref([])
// formKey 每次渲染表单时自增，绑到 <form-create> 的 :key 上强制它重建。
//
// **没有它这个弹窗只在整页加载后的第一次打开能用**：renderPointForm 会先把
// fcRule 清空、把 fcApi 置 null，再把新规则赋回去；三次赋值在同一个 tick 内完成，
// Vue 合并成一次渲染 → fcRule.length 前后都非空 → v-if 不翻转 → 组件既不卸载
// 也不重建 → form-create 不会再 emit 一次 mounted → fcApi 永远停在 null。
// 症状是 collectOptions() 返回 undefined，axios 把 options 键整个丢掉，插件侧报
// "扫描选项为空"。扫描页与设备页用 :key="协议名" 规避了同一个坑（它们是换协议才
// 重渲染），而这个弹窗每次打开都重渲染、协议却可能没变，所以 key 必须按次数自增。
const formKey = ref(0)
const fcOption = ref({ form: { labelWidth: '110px', labelPosition: 'right' } })

// ---------- 结果 ----------
const scanning = ref(false)
const result = ref(null)
const rows = ref([])
const selectedRows = ref([])
const tableRef = ref(null)
// 「显示 0 值」开关。表格用的是滞后一帧的 appliedShowZero，理由见下面那段注释。
const showZero = ref(true)
const appliedShowZero = ref(true)
let showZeroRaf = 0
const bulkType = ref('')

// ---------- 类型下拉 ----------
// 与设备地址页共用同一个接口，这里自己拉一次是为了让组件自包含
// （弹窗可能在任何时候被打开，不该依赖父组件此刻恰好已经加载好了）。
const dataTypeOptions = ref([])
const dataTypeLoading = ref(false)

const rwOptions = computed(() => [
  { value: 'R', label: t('deviceAddress.readonlyShort') },
  { value: 'RW', label: t('deviceAddress.readwriteShort') },
])

// 与设备地址页的新增/编辑地址表单共用一份：这里建出来的点位，之后会在那张表单上
// 被编辑，两边档位不一致就会出现「列表里显示着表单选不到的值」
const scanFrequencyOptions = useScanFrequencyOptions()

const creating = ref(false)
const importResultVisible = ref(false)
const importResult = ref({ total: 0, success: 0, failed: 0, errors: [] })

// ---------- 派生 ----------
// sampled：本次结果**带不带采样值**。
//
// 判据是结果级的 assumedCommonDataType 为空 —— 这是一条**跨端约定**（见
// backend/driver/pointDiscovery.go 与 vo.PointScanResultVO 的注释）：
// 读一段地址的协议（Modbus/S7/FINS）整段共用一个假设类型，它必然非空；
// 而原生枚举协议（Rockwell.CIP）列的是设备自报的标签，每个标签各有各的类型
// （在 Points[].commonDataType 里），没有"统一的假设类型"可言，也不读值，
// 所以它留空。
//
// 它决定三件事，缺一条都会误导：
//   1. 隐藏「显示 0 值」开关 —— 那一列全是空的，关掉开关只会让整张表清空，
//      而"值为 0 的 N 个"会显示成全表条数（每个点位的值都是空）；
//   2. 换一套统计措辞 —— "可读到/读不到"在枚举里是错的，
//      那边只有"列出了"与"被跳过"（跳过与地址是否存在无关）；
//   3. 换一条截断提示 —— "缩小范围重扫"对枚举没有意义（范围是设备定的）。
const sampled = computed(() => !!result.value?.assumedCommonDataType)

// 零值的判据是**值的字段为空**：插件对"原始字节全 0"的点位只给 note、不给 value
// （判据放在插件里用原始字节，是因为按解析后的值判会漏掉 0x00000001 这种
// "按 Float 解出来极小的非零数"）。前端因此只需要看 value 空不空。
const zeroCount = computed(() => rows.value.filter((r) => !r.value).length)
const visibleRows = computed(() =>
  appliedShowZero.value ? rows.value : rows.value.filter((r) => r.value),
)

// 开关的两个方向**代价不对称**，所以这里要让开关先落帧：
//   关 → 开：把之前滤掉的行加回来 —— 要**挂载**它们（每行 3 个 el-select + 2 个
//             el-input，而 el-select 自带 tooltip/popper/options/scrollbar 一整棵树）
//   开 → 关：只是**卸载**，便宜得多
// 再加上 setData 之后 el-table 必然走一次 scheduleLayout → updateColumnsWidth，
// 在刚挂载的这棵子树上强制重排。这些全在开关那一帧同步做完，主线程被占住，
// el-switch 那条 0.3s 的 CSS 过渡就跑不动 —— 表现出来就是"点了开关要等一下才动"。
//
// 把换数据推到下一帧，浏览器就能先把开关的新状态画出来。期间连点只认最后一次。
//
// **必须套两层 rAF，一层不够**：按 HTML 规范，一次「update the rendering」的顺序是
// 先跑 rAF 回调、再做 style/layout/paint。点开关时如果正赶上某个渲染机会马上开始，
// 单层 rAF 的回调就会落在**这一帧**的 rAF 阶段 —— 重活仍然排在这一帧的绘制前面，
// 等于白推。套一层之后，第一帧（只有开关变化）必定已经画完，表格只能等到再下一帧。
// 多花一帧约 16ms，感知不到。
watch(showZero, (val) => {
  if (showZeroRaf) cancelAnimationFrame(showZeroRaf)
  if (val === appliedShowZero.value) {
    showZeroRaf = 0
    return
  }
  showZeroRaf = requestAnimationFrame(() => {
    showZeroRaf = requestAnimationFrame(() => {
      showZeroRaf = 0
      appliedShowZero.value = val
    })
  })
})

onBeforeUnmount(() => {
  if (showZeroRaf) cancelAnimationFrame(showZeroRaf)
})

// ---------- 分页 ----------
// 只把当前页交给 el-table（理由见模板里那段注释）。统计条与 el-empty 仍然按整份
// 结果算 —— 它们说的是"这次扫描一共读到多少"，不是"这一页有几条"。
//
// 默认 20 而不是 50：表格 max-height=380，size=small 一行约 40px，**一屏只看得到
// 9 行左右** —— 挂 50 行等于 5 屏的量，其中 4 屏是白挂的（挂载量直接决定上面
// 那个迟滞）。20 行已是两屏，翻页次数也不会明显变多。
//
// 越界收拢（切换「显示 0 值」、建成点位后摘掉已建的行会让当前页超界）在
// usePagedRows 里，设备扫描页的三张表共用同一个实现。
const { currentPage, pageSize, pagedRows } = usePagedRows(visibleRows, { pageSize: 20 })

// ---------- 生命周期 ----------
watch(
  () => props.visible,
  (open) => {
    if (open) {
      load()
    }
  },
)

async function load() {
  capability.value = null
  result.value = null
  rows.value = []
  selectedRows.value = []
  bulkType.value = ''
  showZero.value = true
  // 与开关一起复位：只改 showZero 的话表格要多等一帧才开始显示 0 值行
  appliedShowZero.value = true
  currentPage.value = 1

  await Promise.all([fetchCapability(), fetchDataTypes()])
  await renderPointForm()
}

async function fetchCapability() {
  if (!props.protocolName) return
  try {
    capability.value = await getPointCapability(props.protocolName)
  } catch {
    // 拦截器已提示；能力拿不到时上面的 alert 会走 unsupported 分支
    capability.value = { enabled: false, supported: false, protocol: props.protocolName }
  }
}

async function fetchDataTypes() {
  dataTypeLoading.value = true
  try {
    const res = await getDataTypes(props.protocolName)
    dataTypeOptions.value = normalizeDataTypes(res)
  } catch {
    dataTypeOptions.value = []
  } finally {
    dataTypeLoading.value = false
  }
}

// 与设备地址页、扫描页同一套归一化：接口返回 { protocol, commonTypes, extendedTypes, allTypes }
function normalizeDataTypes(data) {
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

/**
 * 渲染插件声明的扫描参数表单。
 *
 * 这段容错与 views/industrial/device-scan/IndexView.vue 的 renderScanForm
 * **逐行同构**，改动请两边一起改：后端把 rule/options 二次序列化成字符串是
 * 真实发生过的事，只修一处会让其中一个页面的表单渲染不出来。
 */
async function renderPointForm() {
  fcRule.value = []
  fcApi.value = null

  const raw = spec.value?.pointFormJson
  if (!raw) return

  let parsed = raw
  try {
    if (typeof parsed === 'string') parsed = JSON.parse(parsed)
    // 后端可能把 rule/options 二次序列化成字符串
    if (parsed?.rule && typeof parsed.rule === 'string') parsed.rule = JSON.parse(parsed.rule)
    if (parsed?.options && typeof parsed.options === 'string') parsed.options = JSON.parse(parsed.options)
  } catch {
    ElMessage.warning(t('deviceScan.scanFormInvalid'))
    return
  }

  if (Array.isArray(parsed?.rule)) {
    fcRule.value = parsed.rule
  } else if (Array.isArray(parsed)) {
    fcRule.value = parsed
  } else {
    ElMessage.warning(t('deviceScan.scanFormInvalid'))
    return
  }
  // 与上面两次赋值同一批生效：key 变了才会真的重建（见 formKey 的说明）。
  formKey.value++

  const opts = parsed?.options || parsed?.option
  if (opts) {
    fcOption.value = {
      ...fcOption.value,
      ...opts,
      form: { ...fcOption.value.form, ...(opts.form || {}) },
    }
  }
}

function handleFcReady($f) {
  fcApi.value = $f
}

/** 收集扫描参数（按当前 rule 的 field 过滤，剔除脏数据）。 */
function collectOptions() {
  if (!fcApi.value) return undefined
  const data = fcApi.value.formData() || {}
  const fields = new Set(fcRule.value.map((item) => item.field).filter(Boolean))
  if (fields.size === 0) return data
  const cleaned = {}
  Object.keys(data).forEach((k) => {
    if (fields.has(k)) cleaned[k] = data[k]
  })
  return cleaned
}

// ---------- 扫描 ----------
async function handleScan() {
  if (scanning.value) return
  scanning.value = true
  selectedRows.value = []
  try {
    const res = await discoverPoints({
      deviceId: props.deviceId,
      options: collectOptions(),
    })
    result.value = res
    currentPage.value = 1
    // 默认标签兜底为地址：扫描结果**没有标签来源**（设备不会告诉你"这个寄存器是
    // 温度"），要求用户逐行为几十个点位想标签等于逼他放弃。服务端也会兜，
    // 这里先填上是为了让表格看起来是完整的、且用户能就地改。
    rows.value = (res.points ?? []).map((p) => ({
      name: p.name,
      value: p.value ?? '',
      note: p.note ?? '',
      label: p.label || p.name,
      // 逐点类型优先：原生枚举协议（Rockwell.CIP）每个标签各有各的类型，
      // 结果级的 assumedCommonDataType 表达不了，硬套一个默认值会让 DINT 与 REAL
      // 混在一张表里全部按同一个类型建点 —— 用户看着"类型填好了"就提交，
      // 采出来的值是错的。下面两级回退是给读一段地址的协议准备的（它们不填这一项）。
      commonDataType: p.commonDataType || res.assumedCommonDataType || spec.value?.defaultCommonDataType || '',
      rwPermission: 'R',
      scanFrequency: 1000,
      description: '',
    }))
    if (rows.value.length === 0) {
      // 空结果的原因两套说法完全不同：读一段地址时是"范围里没读到"，
      // 原生枚举时是"设备自报它没有标签"（要去查程序下载与控制器状态）。
      ElMessage.info(sampled.value
        ? t('deviceAddress.pointDiscovery.noResult')
        : t('deviceAddress.pointDiscovery.noResultNative'))
    }
  } catch {
    // 拦截器已提示（设备正忙、串口被占、配置模式等都由后端给出可执行的原因）
  } finally {
    scanning.value = false
  }
}

function handleReset() {
  result.value = null
  rows.value = []
  selectedRows.value = []
  currentPage.value = 1
}

function handleSelectionChange(sel) {
  selectedRows.value = sel
}

function applyBulkType() {
  if (!bulkType.value) return
  selectedRows.value.forEach((row) => {
    row.commonDataType = bulkType.value
  })
  ElMessage.success(t('deviceAddress.pointDiscovery.bulkTypeApplied', { n: selectedRows.value.length }))
}

// ---------- 提交 ----------
async function handleCreate() {
  if (selectedRows.value.length === 0) return
  creating.value = true
  try {
    const res = await createAddressesFromScan({
      deviceId: props.deviceId,
      items: selectedRows.value.map((r) => ({
        name: r.name,
        label: r.label,
        commonDataType: r.commonDataType,
        rwPermission: r.rwPermission,
        scanFrequency: r.scanFrequency,
        description: r.description,
      })),
    })
    importResult.value = res
    importResultVisible.value = true
    if (res.success > 0) emit('created')

    // **只摘掉真正建成的那些行。** 失败的行（撞名、类型不对）如果一起消失，
    // 用户连改个名字重试的入口都没有 —— 他只能把这次扫描重跑一遍，而一次
    // 串口扫描是十几秒的事。errors[].row 是提交数组里的 1-based 下标
    //（见后端 CreateAddresses / CreateDevices 里 row 的用法）。
    const failedItems = new Set((res.errors ?? []).map((e) => e.row - 1))
    const keptRows = selectedRows.value.filter((_, i) => failedItems.has(i))
    if (keptRows.length < selectedRows.value.length) {
      const done = new Set(
        selectedRows.value.filter((_, i) => !failedItems.has(i)).map((r) => r.name),
      )
      rows.value = rows.value.filter((r) => !done.has(r.name))
      // 勾选重建：失败的那些**仍然勾着**，用户改完标签/类型直接再点一次提交即可。
      // 不能只 clearSelection —— 那会把他刚填的东西一起清掉。
      selectedRows.value = []
      tableRef.value?.clearSelection()
      keptRows.forEach((r) => tableRef.value?.toggleRowSelection(r, true))
    }
  } catch {
    // 拦截器已提示
  } finally {
    creating.value = false
  }
}

function handleClosed() {
  // 关掉就清干净：下次打开（可能是另一台设备）不该看到上一次的结果。
  capability.value = null
  result.value = null
  rows.value = []
  selectedRows.value = []
  currentPage.value = 1
}
</script>

<style scoped>
/* 与扫描页同款（改动请两边一起改）：把插件声明的扫描参数控件收成等宽的一列。
   默认宽度下 el-input 是 width:100%（撑满整条内容区），而 el-input-number
   在 flex 容器里会被挤到 ~150px，两个字段并排就是一个通栏长条压着小步进器。
   扫描参数全是短文本/数字/下拉，定宽不会截断。 */
.dynamic-form-wrapper :deep(.el-input),
.dynamic-form-wrapper :deep(.el-input-number),
.dynamic-form-wrapper :deep(.el-select) {
  width: 100%;
  max-width: 260px;
}

/* 说明条与结果区之间的节奏：这里每一条都必须被读到，间距比主页面更紧。
   选择器带上 .iot-notice 是为了稳定压过全局 .iot-notice.is-inline 的 margin。 */
.iot-notice.iot-notice.pd-notice {
  margin: 0 0 14px;
}

.pd-divider {
  height: 1px;
  margin: 18px 0 16px;
  background: var(--line-soft);
}

/* 统计条：把所有读数收进一条浅底信息带，而不是散落在四段灰字里 */
.pd-stats {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  margin-bottom: 12px;
  padding: 10px 14px;
  font-size: 12.5px;
  color: var(--ink-2);
  background: #f8fafc;
  border: 1px solid var(--line-soft);
  border-radius: 10px;
  font-variant-numeric: tabular-nums;
}

.pd-stat.is-muted {
  color: var(--ink-4);
}

.pd-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 4px;
}

.pd-budget {
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.pd-actions-buttons {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pd-actions-buttons :deep(.el-button) {
  height: 34px;
  padding: 0 14px;
  border-radius: 9px;
  font-weight: 500;
}

/* 结果控制条：左边「显示 0 值」，右边批量设置类型 */
.pd-controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.pd-zero-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
}

.pd-zero-hint {
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.pd-bulk {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pd-table {
  overflow: hidden;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

.pd-table :deep(.el-input__wrapper),
.pd-table :deep(.el-select__wrapper) {
  border-radius: 7px;
  box-shadow: 0 0 0 1px var(--line) inset;
}

@media (max-width: 640px) {
  .pd-actions,
  .pd-controls {
    flex-direction: column;
    align-items: flex-start;
  }

  .pd-bulk {
    width: 100%;
  }
}
</style>
