<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  点位新增/编辑表单。

  从 device-address/IndexView.vue 抽出来，是因为「实时监控」页也要能就地改点位：
  在现场看到某个值不对、或质量码报地址错误时，不必退回地址页。两处共用同一份
  表单与校验，避免校验规则在两边悄悄分叉。

  编辑时只接收 addressId，完整数据由本组件按 id 拉取 —— 实时页的表格行里没有
  报警条件、描述、启停状态等字段，拿行数据填表会把这些配置悄悄清空。
-->
<template>
  <el-dialog
    v-model="visible"
    class="iot-dialog address-form-dialog"
    :title="isEdit ? t('deviceAddress.editTitle') : t('deviceAddress.addTitle')"
    width="min(900px, 94vw)"
    append-to-body
    :close-on-click-modal="false"
    @close="handleDialogClose"
  >
    <!-- 页头：eyebrow → 标题 + 协议标签 → 一句说明，先交代"在改什么、改了会怎样" -->
    <template #header>
      <div class="dp-dialog-head">
        <p class="dp-dialog-eyebrow">
          <span class="dp-dialog-eyebrow-dot" aria-hidden="true" />
          {{ isEdit ? t('deviceAddress.formEyebrowEdit') : t('deviceAddress.formEyebrowAdd') }}
        </p>
        <div class="dp-dialog-title-row">
          <h2 class="dp-dialog-title">
            {{ isEdit ? t('deviceAddress.editTitle') : t('deviceAddress.addTitle') }}
          </h2>
          <el-tag v-if="protocolName" class="dp-head-tag" size="small" effect="plain">
            {{ protocolName }}
          </el-tag>
        </div>
        <p class="dp-dialog-sub">
          <template v-if="deviceName">{{ deviceName }} · </template>{{ t('deviceAddress.formLead') }}
        </p>
      </div>
    </template>

    <el-form
      ref="formRef"
      v-loading="loadingAddress"
      :model="formData"
      :rules="formRules"
      label-width="110px"
      class="dp-form"
      status-icon
    >
      <!-- 基础信息：启停开关收进卡片头，不与一组输入项抢同一条视觉线 -->
      <section class="dp-card">
        <header class="dp-card-head">
          <span class="dp-card-icon" aria-hidden="true">
            <el-icon><LayoutGrid /></el-icon>
          </span>
          <div class="dp-card-copy">
            <h3 class="dp-card-title">{{ t('deviceAddress.sectionBasic') }}</h3>
            <p class="dp-card-desc">{{ t('deviceAddress.sectionBasicDesc') }}</p>
          </div>
          <div class="dp-enable">
            <span class="dp-enable-label">{{ t('common.status') }}</span>
            <el-switch v-model="formData.status" :active-value="1" :inactive-value="0" />
          </div>
        </header>
        <div class="dp-card-body">
          <div class="dp-grid">
            <el-form-item prop="name">
              <!-- 帮助气泡收进标签行，和「数据类型」保持同一种交互；
                   输入框因此占满整格，与其它下拉的长度一致 -->
              <template #label>
                <span class="inline-flex items-center gap-1">
                  {{ t('deviceAddress.addressName') }}
                  <el-popover v-if="addressSpec" placement="bottom" :width="380" trigger="click">
                    <template #reference>
                      <el-button
                        :icon="QuestionFilled"
                        circle
                        text
                        size="small"
                        :aria-label="t('deviceAddress.addressHelpTitle')"
                      />
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
                </span>
              </template>
              <el-input
                v-model="formData.name"
                :placeholder="addressPlaceholder"
                maxlength="100"
                show-word-limit
              />
              <!-- 常驻提示：占位符一输入就消失，而地址格式正是最需要一直看见的信息 -->
              <div v-if="addressExample" class="dp-field-hint is-block">
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

            <el-form-item prop="dataType">
              <!-- 类型映射说明：物模型属性过滤与报警比较符都按「类别（Kind）」判定，
                   只给类型名字的话用户看不出 Boolean 与 Float 的区别，遇到"属性不见了"
                   只能猜。气泡放在标签旁，和地址帮助保持同一种交互。 -->
              <template #label>
                <span class="inline-flex items-center gap-1">
                  {{ t('deviceAddress.dataType') }}
                  <el-popover
                    v-if="dataTypeKindRows.length"
                    placement="bottom"
                    :width="320"
                    trigger="click"
                  >
                    <template #reference>
                      <el-button
                        :icon="QuestionFilled"
                        circle
                        text
                        size="small"
                        :aria-label="t('deviceAddress.kindMapTitle')"
                      />
                    </template>
                    <div class="text-xs leading-5">
                      <div class="font-semibold text-gray-700 mb-2">{{ t('deviceAddress.kindMapTitle') }}</div>
                      <div class="max-h-64 overflow-y-auto space-y-0.5">
                        <div
                          v-for="row in dataTypeKindRows"
                          :key="row.value"
                          class="flex items-center justify-between gap-3"
                        >
                          <span class="font-mono text-gray-800 break-all">{{ row.label }}</span>
                          <span class="shrink-0 text-gray-500">{{ row.kindLabel }}</span>
                        </div>
                      </div>
                      <div class="mt-2 text-gray-400">{{ t('deviceAddress.kindMapHint') }}</div>
                    </div>
                  </el-popover>
                </span>
              </template>
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
              <el-select
                v-model="formData.rwPermission"
                :placeholder="t('deviceAddress.rwPlaceholder')"
                style="width: 100%"
              >
                <el-option
                  v-for="item in rwPermissionOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
            </el-form-item>

            <!-- 扫描频率是必填项，与其它必填字段（地址/标签/类型/权限）同区，保证它在
                 打开表单的第一屏就可见。与采集组的互斥关系仍由下方的置灰状态和说明交代：
                 绑组后周期由组决定，这里不生效。
                 必填星号由 rules 的 required 键或本组件的 required 属性驱动；校验是条件式的
                 （绑了组就不填），所以不能给规则加静态 required，否则组内也会显示星号。 -->
            <el-form-item
              :label="t('deviceAddress.scanFrequency')"
              prop="scanFrequency"
              :required="!formData.groupId"
              class="dp-span-2"
            >
              <el-select
                v-model="formData.scanFrequency"
                :placeholder="t('deviceAddress.scanPlaceholder')"
                :disabled="!!formData.groupId"
                style="width: 100%"
              >
                <el-option
                  v-for="item in scanFrequencyOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
              <div class="dp-field-hint is-block">{{ t('deviceAddress.scanFrequencyInlineHint') }}</div>
            </el-form-item>

            <!-- 字节序布局：Modbus / 欧姆龙 FINS（同一设备上不同点位可能来自不同厂商
                 子模块，排列约定不一致）。留空 = 跟随设备的 protocol_json 配置，这是默认。
                 可选集合随数据类型宽度变化：16/32/64 位各一组布局名，位与 8 位类型不显示；
                 FINS 字符串也没有点位级布局。 -->
            <el-form-item
              v-if="byteOrderLayoutOptions.length"
              :label="t('deviceAddress.byteOrderLayout')"
              prop="byteOrderLayout"
              class="dp-span-2"
            >
              <el-select
                v-model="formData.byteOrderLayout"
                :placeholder="t('deviceAddress.byteOrderFollowDevice')"
                clearable
                style="width: 100%"
              >
                <el-option
                  v-for="item in byteOrderLayoutOptions"
                  :key="item.value"
                  :label="item.label"
                  :value="item.value"
                />
              </el-select>
              <div class="dp-field-hint is-block">{{ t('deviceAddress.byteOrderHint') }}</div>
            </el-form-item>

            <el-form-item :label="t('common.description')" prop="description" class="dp-span-2">
              <el-input
                v-model="formData.description"
                :placeholder="t('deviceAddress.descPlaceholder')"
                type="textarea"
                :rows="2"
                maxlength="200"
                show-word-limit
              />
            </el-form-item>
          </div>
        </div>
      </section>

      <!-- 采集与绑定：物模型属性定义用途，采集组/扫描频率定义节奏 -->
      <section class="dp-card">
        <header class="dp-card-head">
          <span class="dp-card-icon" aria-hidden="true">
            <el-icon><Connection /></el-icon>
          </span>
          <div class="dp-card-copy">
            <h3 class="dp-card-title">{{ t('deviceAddress.sectionCollect') }}</h3>
            <p class="dp-card-desc">{{ t('deviceAddress.sectionCollectDesc') }}</p>
          </div>
        </header>
        <div class="dp-card-body">
          <div class="dp-grid">
            <el-form-item :label="t('deviceAddress.property')" prop="propertyId">
              <el-select
                v-model="formData.propertyId"
                :placeholder="t('deviceAddress.propertyPlaceholder')"
                clearable
                filterable
                :loading="propertyLoading"
                style="width: 100%"
              >
                <el-option
                  v-for="p in compatiblePropertyOptions"
                  :key="p.id"
                  :label="propertyOptionLabel(p)"
                  :value="p.id"
                  :disabled="p.status !== 1 && p.id !== formData.propertyId"
                />
              </el-select>
              <div class="dp-field-hint is-block">{{ t('deviceAddress.propertyHint') }}</div>
              <div v-if="selectedPropertyReadSuppressed" class="dp-field-hint is-block is-warning">
                {{ t('deviceAddress.propertyWriteonlyHint') }}
              </div>
            </el-form-item>

            <el-form-item :label="t('deviceAddress.collectorGroup')" prop="groupId">
              <el-select
                v-model="formData.groupId"
                :placeholder="t('deviceAddress.collectorGroupPlaceholder')"
                clearable
                :loading="groupLoading"
                style="width: 100%"
              >
                <el-option
                  v-for="g in groupOptions"
                  :key="g.id"
                  :label="g.status === 1 ? g.name : `${g.name} (${t('collectorGroup.disabledShort')})`"
                  :value="g.id"
                  :disabled="g.status !== 1 && g.id !== formData.groupId"
                />
              </el-select>
              <div class="dp-field-hint is-block">
                {{ formData.groupId ? t('deviceAddress.collectorGroupHint') : t('deviceAddress.collectorGroupEmptyHint') }}
              </div>
            </el-form-item>

          </div>
        </div>
      </section>

      <!-- 报警条件：开关进卡片头，启用时才展开比较符与操作数 -->
      <section class="dp-card">
        <header class="dp-card-head">
          <span class="dp-card-icon" aria-hidden="true">
            <el-icon><Bell /></el-icon>
          </span>
          <div class="dp-card-copy">
            <h3 class="dp-card-title">{{ t('deviceAddress.thresholdAlarm') }}</h3>
            <p class="dp-card-desc">{{ t('deviceAddress.thresholdHint') }}</p>
          </div>
          <div class="dp-enable">
            <span class="dp-enable-label">{{ t('common.enabled') }}</span>
            <el-switch
              v-model="formData.thresholdEnabled"
              :active-value="1"
              :inactive-value="0"
              :disabled="conditionUnsupported"
            />
          </div>
        </header>
        <div class="dp-card-body">
          <p v-if="!currentKind" class="dp-card-empty">
            {{ t('deviceAddress.alarmPickType') }}
          </p>
          <p v-else-if="conditionUnsupported" class="dp-card-empty">
            {{ t('deviceAddress.opUnsupportedKind') }}
          </p>
          <div v-else-if="formData.thresholdEnabled === 1" class="dp-grid">
            <el-form-item :label="t('deviceAddress.thresholdOp')" prop="thresholdOp">
              <el-select
                v-model="formData.thresholdOp"
                :placeholder="t('deviceAddress.opPlaceholder')"
                style="width: 100%"
              >
                <el-option v-for="op in availableOperators" :key="op" :label="operatorLabel(op)" :value="op" />
              </el-select>
            </el-form-item>

            <el-form-item v-if="formData.thresholdOp" :label="t('deviceAddress.thresholdValue')" prop="thresholdValues">
              <div class="dp-operand">
                <!-- 区间：两个固定槽位（下限、上限），不检查的那侧留空 -->
                <template v-if="formData.thresholdOp === 'range'">
                  <el-input-number
                    v-model="formData.thresholdValues[0]"
                    class="dp-operand-input"
                    :controls="false"
                    :placeholder="t('deviceAddress.opRangeLow')"
                  />
                  <span class="dp-range-sep" aria-hidden="true">~</span>
                  <el-input-number
                    v-model="formData.thresholdValues[1]"
                    class="dp-operand-input"
                    :controls="false"
                    :placeholder="t('deviceAddress.opRangeHigh')"
                  />
                </template>

                <!-- 文本集合：多值输入 -->
                <el-select
                  v-else-if="formData.thresholdOp === 'in'"
                  v-model="formData.thresholdValues"
                  class="dp-operand-wide"
                  multiple
                  filterable
                  allow-create
                  default-first-option
                  :reserve-keyword="false"
                  :placeholder="t('deviceAddress.textSetPlaceholder')"
                />

                <!-- 布尔：值就是真/假，用下拉避免手敲 "on" 这类认不出的字面量 -->
                <el-select
                  v-else-if="currentKind === 'bool'"
                  v-model="formData.thresholdValues[0]"
                  class="dp-operand-input"
                >
                  <el-option :label="t('deviceAddress.boolTrue')" value="1" />
                  <el-option :label="t('deviceAddress.boolFalse')" value="0" />
                </el-select>

                <!-- 数值单比较 / 文本等于不等于 -->
                <el-input-number
                  v-else-if="currentKind !== 'string'"
                  v-model="formData.thresholdValues[0]"
                  class="dp-operand-input"
                  :controls="false"
                />
                <el-input
                  v-else
                  v-model="formData.thresholdValues[0]"
                  class="dp-operand-input"
                  :placeholder="t('deviceAddress.textValuePlaceholder')"
                />
              </div>
            </el-form-item>
          </div>
          <p v-else class="dp-card-empty">
            {{ t('deviceAddress.thresholdOffBody') }}
          </p>
        </div>
      </section>

      <!-- 工程量变换：采集侧执行一次，实时值/报警/触发规则/推送共用 -->
      <section class="dp-card">
        <header class="dp-card-head">
          <span class="dp-card-icon" aria-hidden="true">
            <el-icon><Operation /></el-icon>
          </span>
          <div class="dp-card-copy">
            <h3 class="dp-card-title">{{ t('deviceAddress.engineeringTransform') }}</h3>
            <p class="dp-card-desc">{{ t('deviceAddress.sectionTransformDesc') }}</p>
          </div>
        </header>
        <div class="dp-card-body">
          <MappingTransformEditor
            :transforms="formData.transformRows"
            :ops="ADDRESS_TRANSFORM_OPS"
            :show-title="false"
            label-prefix="deviceAddress.transformEditor"
          />
          <p class="dp-field-hint dp-transform-hint">{{ t('deviceAddress.transformHint') }}</p>
        </div>
      </section>
    </el-form>

    <template #footer>
      <div class="dp-dialog-footer">
        <p class="dp-footer-hint">{{ t('deviceAddress.formFooterHint') }}</p>
        <div class="dp-footer-actions">
          <el-button @click="visible = false">{{ t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            {{ isEdit ? t('common.save') : t('common.submit') }}
          </el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, reactive, computed, watch, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import {
  IconBell as Bell,
  IconHelpCircleFilled as QuestionFilled,
  IconLayoutGrid as LayoutGrid,
  IconMathFunction as Operation,
  IconPlugConnected as Connection,
} from '@tabler/icons-vue'
import {
  addDeviceAddress,
  updateDeviceAddress,
  getDeviceAddressById,
} from '@/api/modules/deviceAddress'
import { getDataTypes } from '@/api/modules/protocol'
import { listAllThingModelProperties } from '@/api/modules/thingModel'
import { listCollectorGroups } from '@/api/modules/collectorGroup'
import { useScanFrequencyOptions } from '@/composables/useScanFrequencyOptions'
import { getAddressExample, getAddressSpec } from '@/utils/addressSyntax'
import { buildConditionValues, parseConditionValues } from '@/utils/addressThreshold'
import MappingTransformEditor from '@/components/pushMapping/MappingTransformEditor.vue'
import {
  ADDRESS_TRANSFORM_OPS,
  transformJSONToRows,
  transformRowsToJSON,
  validateTransformRows,
} from '@/utils/pushMapping'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  deviceId: { type: String, required: true },
  // 地址写法、数据类型随协议不同，必须由调用页带进来（实时页取快照里的协议名）
  protocolName: { type: String, default: '' },
  // 页头副标题里显示「设备 名 · 说明」；取不到时只显示说明
  deviceName: { type: String, default: '' },
  // 空串 = 新增；非空 = 编辑该点位
  addressId: { type: String, default: '' },
})

const emit = defineEmits(['update:modelValue', 'saved'])

const { t } = useI18n()

const visible = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value),
})

const isEdit = computed(() => !!props.addressId)

// ---------- 下拉选项 ----------
// 数据类型通过协议接口获取：/protocol/getDataTypes?protocolName=xxx
const dataTypeOptions = ref([])
const dataTypeLoading = ref(false)
// 通用类型名 -> 数据类型类别（driver.KindXxx）。报警条件的比较符下拉靠它决定 ——
// 光看类型名字分不出 "Boolean" 与 "Float" 的类别。见 vo.DataTypeOptionVO.KindByType。
const kindByType = ref({})
// 已经按哪个协议拉过选项：协议不变就不重复请求（实时页每次打开编辑框都复用）
let dataTypesFor = null

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

async function ensureDataTypes() {
  if (dataTypesFor === props.protocolName) return
  dataTypesFor = props.protocolName
  dataTypeLoading.value = true
  try {
    const res = await getDataTypes(props.protocolName || undefined)
    dataTypeOptions.value = normalizeDataTypes(res)
  } catch {
    // 取不到就保持下拉为空，别猜（猜错的类型比空着更难发现）
    dataTypesFor = null
    dataTypeOptions.value = []
  } finally {
    dataTypeLoading.value = false
  }
}

// 数据类型类别（driver.KindXxx）的中文说法：物模型属性过滤与报警比较符都按
// 类别判定，把 Kind 直接摊给用户看（int/bool/string）比不解释更糊涂。
const KIND_LABEL_KEYS = {
  int: 'deviceAddress.kindInt',
  uint: 'deviceAddress.kindUint',
  float: 'deviceAddress.kindFloat',
  bool: 'deviceAddress.kindBool',
  string: 'deviceAddress.kindString',
  time: 'deviceAddress.kindTime',
}

// 当前协议的类型 → 类别映射，供「数据类型」标签旁的气泡展示。
// 未收录的 Kind（将来插件新增类别）原样显示，不静默吞成空。
const dataTypeKindRows = computed(() =>
  dataTypeOptions.value.map((item) => {
    const kind = kindByType.value[item.value] || ''
    const key = KIND_LABEL_KEYS[kind]
    return {
      value: item.value,
      label: item.label,
      kindLabel: kind ? (key ? t(key) : kind) : t('deviceAddress.kindUnknown'),
    }
  }),
)

// 新增与编辑给同一组选项：只写点位虽然采集不到值，但后端/导入/开放 API 都允许 W，
// 新增入口不给会逼人绕去导入，而编辑又必须能原样显示已有的 W 行
const rwPermissionOptions = computed(() => [
  { value: 'R', label: t('deviceAddress.readonlyShort') },
  { value: 'W', label: t('deviceAddress.writeonlyShort') },
  { value: 'RW', label: t('deviceAddress.readwriteShort') },
])

// ---------- 字节序布局（Modbus / 欧姆龙 FINS） ----------
// 同设备不同点位可能来自不同厂商子模块，字节/字排列约定不一致。库与驱动直接
// 存**布局名**（ABCD/CDAB/…），可选集合由当前数据类型的**值宽度**决定：
// 16 位只有字内交换，32 位四种经典排列，64 位用带 8 个字母的名字 —— 后者能表达
// 「字顺序整体反转」（GHEFCDAB）等两轴模型做不到的约定。空串 = 跟随设备配置。
//
// 后端只对 Modbus 与 FINS 接受非空布局；FINS 的 bool/8 位/字符串没有点位级布局。
const supportsPointLayout = computed(() => /^(modbus|omron\.fins)/i.test(props.protocolName || ''))
const isFins = computed(() => /^omron\.fins/i.test(props.protocolName || ''))

// 类型名（小写后）→ 值宽度分类。覆盖 Modbus 与 FINS 会下发的通用名、扩展名
// （Int/Real）与历史数据里可能残留的内部名（float32 等）。
// 未收录的类型回退到「全部布局」，由后端按宽度兜底拒绝。
const LAYOUT_BY_TYPE = {
  boolean: 'bit', bool: 'bit',
  byte: 'small', char: 'small', int8: 'small', uint8: 'small',
  short: '16', word: '16', bcd: '16', int16: '16', uint16: '16',
  long: '32', dword: '32', float: '32', real: '32', int: '32',
  date: '32', lbcd: '32', int32: '32', uint32: '32', float32: '32',
  double: '64', float64: '64',
  string: 'string',
}

const LAYOUTS_BY_WIDTH = {
  bit: [],
  small: [],
  '16': ['ABCD', 'BADC'],
  '32': ['ABCD', 'CDAB', 'BADC', 'DCBA'],
  '64': ['ABCDEFGH', 'CDABGHEF', 'BADCFEHG', 'HGFEDCBA', 'GHEFCDAB', 'DCBAHGFE'],
  string: ['ABCD', 'BADC'],
}

const LAYOUT_LABEL_KEYS = {
  ABCD: 'deviceAddress.layoutABCD',
  CDAB: 'deviceAddress.layoutCDAB',
  BADC: 'deviceAddress.layoutBADC',
  DCBA: 'deviceAddress.layoutDCBA',
  ABCDEFGH: 'deviceAddress.layoutABCDEFGH',
  CDABGHEF: 'deviceAddress.layoutCDABGHEF',
  BADCFEHG: 'deviceAddress.layoutBADCFEHG',
  HGFEDCBA: 'deviceAddress.layoutHGFEDCBA',
  GHEFCDAB: 'deviceAddress.layoutGHEFCDAB',
  DCBAHGFE: 'deviceAddress.layoutDCBAHGFE',
}

const layoutWidth = computed(
  () => LAYOUT_BY_TYPE[String(formData.dataType || '').toLowerCase()] || 'all',
)

const byteOrderLayoutOptions = computed(() => {
  if (!supportsPointLayout.value) return []
  const width = layoutWidth.value
  // FINS 字符串没有点位级布局（后端保存时也会拒绝），不给一个选了必然失败的选项。
  if (isFins.value && width === 'string') return []
  const codes =
    width === 'all'
      ? [...new Set([...LAYOUTS_BY_WIDTH['16'], ...LAYOUTS_BY_WIDTH['32'], ...LAYOUTS_BY_WIDTH['64']])]
      : LAYOUTS_BY_WIDTH[width] || []
  return codes.map((code) => ({ value: code, label: t(LAYOUT_LABEL_KEYS[code]) }))
})

// 清掉当前表单里不再适用的布局（协议/数据类型不匹配）。加载旧配置与切换类型都走
// 这一条：保留一个后端必拒的值，用户只会看到"保存失败"却看不出是哪个字段。
function normalizeByteOrderLayout() {
  if (
    formData.byteOrderLayout &&
    !byteOrderLayoutOptions.value.some((item) => item.value === formData.byteOrderLayout)
  ) {
    formData.byteOrderLayout = ''
  }
}

// 扫描频率下拉与「自动发现点位」弹窗共用一份（composables/useScanFrequencyOptions.js）：
// 两处写的是同一个 device_address.scan_frequency，选项必须一致
const scanFrequencyOptions = useScanFrequencyOptions()

// ---------- 协议地址示例 ----------
// 地址写法随协议完全不同（"D100" 在三菱指 D 寄存器、在欧姆龙 FINS 指 DM 区），
// 实例化在 utils/addressSyntax.js，这里只负责取。协议未登记时回落到通用文案，
// 宁可不举例也不给一个错的例子。
const addressSpec = computed(() => getAddressSpec(props.protocolName))
const addressExample = computed(() => getAddressExample(props.protocolName))
const addressPlaceholder = computed(() =>
  addressExample.value
    ? t('deviceAddress.namePlaceholderExample', { example: addressExample.value })
    : t('deviceAddress.namePlaceholder'),
)

// ---------- 物模型属性 ----------
// 点位可直接绑定任意模型下的属性，与设备是否绑定模型无关。
const propertyOptionsAll = ref([])
const propertyLoading = ref(false)

function propertyOptionLabel(p) {
  const name = `${p.identifier} · ${p.name}`
  return p.modelName ? `${p.modelName} · ${name}` : name
}

async function fetchPropertyOptions() {
  propertyLoading.value = true
  try {
    propertyOptionsAll.value = (await listAllThingModelProperties()) || []
  } catch {
    propertyOptionsAll.value = []
  } finally {
    propertyLoading.value = false
  }
}

// ---------- 采集组 ----------
// 组绑定后采集周期由组决定，scanFrequency 在表单里置灰并给出说明。
const groupOptions = ref([])
const groupLoading = ref(false)

async function fetchGroupOptions() {
  groupLoading.value = true
  try {
    groupOptions.value = (await listCollectorGroups(props.deviceId)) || []
  } catch {
    groupOptions.value = []
  } finally {
    groupLoading.value = false
  }
}

// ---------- 对话框状态 ----------
const loadingAddress = ref(false)
const submitting = ref(false)
const formRef = ref(null)
// 每次打开/关闭都递增：关闭后才返回的详情请求不得再往表单里填数据
let loadToken = 0

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
  label: '',
  dataType: '',
  rwPermission: '',
  // 字节序布局：'' = 跟随设备；非空是布局名（ABCD/CDAB/…，64 位为 8 字母名字）。
  byteOrderLayout: '',
  propertyId: '',
  groupId: '',
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
  // 工程量变换：有序算子流水线（scale/round/trim/default），采集侧执行一次。
  // 提交时始终整份发送，空数组表示清空。
  transformRows: [],
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
  // 绑定采集组后周期由组决定，扫描频率不再是必填项。
  scanFrequency: [
    {
      validator: (_rule, value, callback) => {
        if (formData.groupId) return callback()
        if (value === null || value === undefined || value === '') {
          return callback(new Error(t('deviceAddress.selectScan')))
        }
        callback()
      },
      trigger: 'change',
    },
  ],
  description: [{ max: 200, message: () => t('deviceAddress.descMaxLength'), trigger: 'blur' }],
}

// ---------- 打开 ----------
watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    resetFormData()
    // 关闭时递增过的 token 在这里再递增一次：上一次打开遗留的详情请求作废
    loadToken += 1
    nextTick(() => formRef.value?.clearValidate())
    ensureDataTypes()
    fetchPropertyOptions()
    fetchGroupOptions()
    if (props.addressId) loadAddress(props.addressId)
  },
)

async function loadAddress(id) {
  const token = ++loadToken
  loadingAddress.value = true
  try {
    const row = await getDeviceAddressById(id)
    if (token !== loadToken) return
    formData.id = row.id ?? ''
    formData.name = row.name ?? ''
    formData.label = row.label ?? ''
    formData.dataType = row.commonDataType ?? ''
    formData.rwPermission = row.rwPermission ?? ''
    formData.byteOrderLayout = row.byteOrderLayout ?? ''
    formData.propertyId = row.propertyId ?? ''
    formData.groupId = row.groupId ?? ''
    formData.scanFrequency = row.scanFrequency ?? null
    formData.description = row.description ?? ''
    formData.status = row.status ?? 1
    formData.thresholdEnabled = row.thresholdEnabled ?? 0
    formData.thresholdOp = row.thresholdOp ?? ''
    formData.thresholdValues = parseConditionValues(row.thresholdValue)
    formData.transformRows = parseTransformRows(row.transformJson)
    // 历史配置可能带着协议/类型已不支持的布局（例如旧版 FINS 字符串），加载时清掉。
    normalizeByteOrderLayout()
  } catch {
    // 错误提示由响应拦截器给出；关闭对话框，别让用户对着空表单点保存
    if (token === loadToken) visible.value = false
  } finally {
    if (token === loadToken) loadingAddress.value = false
  }
}

// ---------- 对话框关闭 ----------
function handleDialogClose() {
  loadToken += 1
  loadingAddress.value = false
  formRef.value?.resetFields()
  resetFormData()
}

// ---------- 重置表单数据 ----------
function resetFormData() {
  formData.id = ''
  formData.name = ''
  formData.label = ''
  formData.dataType = ''
  formData.rwPermission = ''
  formData.byteOrderLayout = ''
  formData.propertyId = ''
  formData.groupId = ''
  formData.scanFrequency = null
  formData.description = ''
  formData.status = 1
  formData.thresholdEnabled = 0
  formData.thresholdOp = ''
  formData.thresholdValues = []
  formData.transformRows = []
}

// 后端把流水线存成 JSON 数组字符串（与 thresholdValue 同一口径）。
// 解析失败按"未配置"处理：编辑框应能打开，不能因为一条坏配置把人挡在门外。
function parseTransformRows(raw) {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? transformJSONToRows(parsed, ADDRESS_TRANSFORM_OPS) : []
  } catch {
    return []
  }
}

// ---------- 属性下拉：按当前数据类型与读写模式过滤 ----------
// 与后端 validatePropertyBinding 同一规则；前端过滤是为了让不兼容的选项根本选不到。
// 当前已选中的属性始终保留在列表里，否则类型/权限刚变化时下拉会突然“失值”。
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

const compatiblePropertyOptions = computed(() => {
  const kind = currentKind.value
  return propertyOptionsAll.value.filter((p) => {
    if (p.id === formData.propertyId) return true
    if (kind && p.kind !== kind) return false
    // 新建时读写权限还没选，此时不按权限过滤，否则下拉一开始是空的。
    if (formData.rwPermission && !accessCovers(formData.rwPermission, p.accessMode)) return false
    return true
  })
})

// 选中的属性为只写（access_mode 不含 R）时，即使点位 RW 也不会进入采集。
// 与后端 collection/ControlService 的判定同一口径，提前把后果说清楚。
const selectedPropertyReadSuppressed = computed(() => {
  const p = propertyOptionsAll.value.find((x) => x.id === formData.propertyId)
  const mode = String(p?.accessMode || '')
  return mode !== '' && !mode.includes('R')
})

// 类型/读写权限变化后，已选属性若不再兼容就清空，避免提交时才被后端拒绝。
watch([() => formData.dataType, () => formData.rwPermission], () => {
  if (!props.modelValue || !formData.propertyId) return
  const p = propertyOptionsAll.value.find((x) => x.id === formData.propertyId)
  if (!p) return
  const kind = kindByType.value[formData.dataType] || ''
  const accessMismatch = formData.rwPermission && !accessCovers(formData.rwPermission, p.accessMode)
  if ((kind && p.kind !== kind) || accessMismatch) {
    formData.propertyId = ''
  }
})

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
  // 值宽度变了，旧布局可能不再适用（如 Float → Double，ABCD 在 64 位下不合法；
  // FINS 切到字符串同理）。保留会让保存被后端拒绝，且用户看不出是这个字段的问题。
  normalizeByteOrderLayout()
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
  const values = buildConditionValues(formData.thresholdOp, formData.thresholdValues)
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

// 点位变换的校验规则与后端 ValidateNormalization 对齐（白名单、default 首位、
// scale 参数为数字、round 小数位 0-12）。这里只给一条通用提示，细节由后端兜底。
function validateAddressTransform() {
  const problems = validateTransformRows(formData.transformRows, ADDRESS_TRANSFORM_OPS)
  if (problems.length === 0) return true
  ElMessage.warning(t('deviceAddress.transformInvalid'))
  return false
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
  if (!validateAddressTransform()) return

  submitting.value = true
  try {
    const payload = {
      deviceId: props.deviceId,
      name: formData.name.trim(),
      label: formData.label.trim(),
      commonDataType: formData.dataType,
      // 始终整份提交（空串 = 跟随设备）。编辑接口按「字段是否省略」三态处理，
      // 所以清空布局时必须显式传空串，而不是省略字段。
      byteOrderLayout: formData.byteOrderLayout || '',
      rwPermission: formData.rwPermission,
      propertyId: formData.propertyId || '',
      groupId: formData.groupId || '',
      scanFrequency: formData.scanFrequency,
      description: formData.description.trim() || undefined,
      status: formData.status,
      // 始终整份提交：编辑时后端靠「对象是否省略」区分不动/覆盖，这里既然用户看得见
      // 条件字段，传过去的就是他的当前意图（关掉开关也要如实传 enabled=0）。
      threshold: {
        enabled: formData.thresholdEnabled,
        op: formData.thresholdOp,
        values: buildConditionValues(formData.thresholdOp, formData.thresholdValues),
      },
      // 始终整份提交：编辑时 [] 表示清空流水线（后端按「省不省略」区分三态）。
      transform: transformRowsToJSON(formData.transformRows, ADDRESS_TRANSFORM_OPS),
    }

    if (isEdit.value) {
      payload.id = formData.id
      await updateDeviceAddress(payload)
      ElMessage.success(t('deviceAddress.editSuccess'))
    } else {
      await addDeviceAddress(payload)
      ElMessage.success(t('deviceAddress.addSuccess'))
    }
    emit('saved')
    visible.value = false
  } catch {
    // 失败原因由响应拦截器统一提示；保留弹窗让用户看到输入并修正
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
/* ---------- 弹窗头：eyebrow → 标题 → 说明，三级递进 ---------- */
.dp-dialog-head {
  padding-right: 28px;
}

.dp-dialog-eyebrow {
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

.dp-dialog-eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.dp-dialog-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.dp-dialog-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.dp-head-tag {
  font-weight: 500;
}

.dp-dialog-sub {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

.dp-form {
  padding-top: 2px;
}

/* ---------- 分区卡片：白面浮在浅色画布上，用发丝线收边 ---------- */
.dp-card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.dp-card + .dp-card {
  margin-top: 16px;
}

.dp-card-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--line-soft);
}

.dp-card-icon {
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

.dp-card-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;
}

.dp-card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.dp-card-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* 开关是整张卡的开关，与标题同排靠右，不再独自占一行 */
.dp-enable {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.dp-enable-label {
  font-size: 12.5px;
  color: var(--ink-3);
}

.dp-card-body {
  padding: 20px 22px;
}

.dp-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 20px;
  margin-bottom: -16px;
}

.dp-grid :deep(.el-form-item) {
  margin-bottom: 16px;
}

/* 一侧字段带说明文字时，内容区会被网格拉高；两侧都钉到顶部才能齐平 */
.dp-grid :deep(.el-form-item__content) {
  align-items: flex-start;
  align-content: flex-start;
}

.dp-grid .dp-span-2 {
  grid-column: span 2;
}

.dp-field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.dp-field-hint.is-block {
  flex: 0 0 100%;
}

.dp-field-hint.is-warning {
  color: var(--warning);
}

/* 未配置任何内容时的安静占位，与可编辑区区分开 */
.dp-card-empty {
  margin: 0;
  padding: 12px 14px;
  font-size: 12.5px;
  line-height: 1.65;
  color: var(--ink-4);
  background: #f8fafc;
  border: 1px dashed var(--line);
  border-radius: 10px;
}

/* 阈值操作数：同一行内并排，窄屏自然折行 */
.dp-operand {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.dp-operand-input {
  width: 120px;
}

.dp-operand-wide {
  flex: 1;
  min-width: 0;
}

.dp-range-sep {
  color: var(--ink-4);
}

.dp-transform-hint {
  margin-top: 12px;
}

.dp-dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.dp-footer-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.dp-footer-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

@media (max-width: 820px) {
  .dp-grid {
    grid-template-columns: 1fr;
  }

  .dp-grid .dp-span-2 {
    grid-column: auto;
  }

  .dp-dialog-footer {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
