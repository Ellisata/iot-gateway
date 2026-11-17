<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  事件触发规则表单。

  条件采用 DNF：组间 OR、组内 AND。每组里每一行是（设备 → 点位 → 算子 → 值）。
  算子按点位 Kind 过滤；Kind 由「协议 + 通用数据类型」经 /protocol/getDataType 的
  kindByType 解析并缓存，不在前端另写一份类型表。

  版式与点位表单（device-address/AddressFormDialog）同一套：页头三级递进，
  正文按「基础信息 / 触发后动作 / 触发条件」三张卡片分组，条件组再嵌一层编号卡，
  OR/AND 用连接条与胶囊直白标出，避免一堆同权重的表单行糊成一片。
-->
<template>
  <el-dialog
    v-model="visible"
    class="iot-dialog trigger-rule-dialog"
    :title="isEdit ? t('triggerRule.editTitle') : t('triggerRule.addTitle')"
    width="min(920px, 94vw)"
    append-to-body
    :close-on-click-modal="false"
    @close="reset"
  >
    <!-- 页头：eyebrow → 标题 → 说明，先交代这条规则做什么、保存后怎样生效 -->
    <template #header>
      <div class="tr-dialog-head">
        <p class="tr-dialog-eyebrow">
          <span class="tr-dialog-eyebrow-dot" aria-hidden="true" />
          {{ isEdit ? t('triggerRule.formEyebrowEdit') : t('triggerRule.formEyebrowAdd') }}
        </p>
        <h2 class="tr-dialog-title">
          {{ isEdit ? t('triggerRule.editTitle') : t('triggerRule.addTitle') }}
        </h2>
        <p class="tr-dialog-sub">{{ t('triggerRule.formLead') }}</p>
      </div>
    </template>

    <el-form ref="formRef" :model="form" :rules="formRules" label-width="110px" status-icon class="tr-form">
      <!-- 基础信息：启停开关收进卡片头，不与一组输入项抢同一条视觉线 -->
      <section class="tr-card">
        <header class="tr-card-head">
          <span class="tr-card-icon" aria-hidden="true">
            <el-icon><InfoCircle /></el-icon>
          </span>
          <div class="tr-card-copy">
            <h3 class="tr-card-title">{{ t('triggerRule.sectionBasic') }}</h3>
            <p class="tr-card-desc">{{ t('triggerRule.sectionBasicDesc') }}</p>
          </div>
          <div class="tr-enable">
            <span class="tr-enable-label">{{ t('common.status') }}</span>
            <el-switch v-model="form.status" :active-value="1" :inactive-value="0" />
          </div>
        </header>
        <div class="tr-card-body">
          <div class="tr-grid">
            <el-form-item :label="t('triggerRule.code')" prop="code">
              <el-input v-model="form.code" maxlength="64" show-word-limit :disabled="isEdit"
                :placeholder="t('triggerRule.codePlaceholder')" />
              <!-- 常驻提示：占位符一输入就消失，而「编码不可改」正是要一直看见的信息 -->
              <div class="tr-field-hint is-block">{{ t('triggerRule.codeHint') }}</div>
            </el-form-item>
            <el-form-item :label="t('triggerRule.name')" prop="name">
              <el-input v-model="form.name" maxlength="64" show-word-limit
                :placeholder="t('triggerRule.namePlaceholder')" />
            </el-form-item>
            <el-form-item :label="t('common.description')" class="tr-span-2">
              <el-input v-model="form.description" type="textarea" :rows="2" maxlength="500" show-word-limit />
            </el-form-item>
          </div>
        </div>
      </section>

      <!-- 触发后动作：读哪些点位、推到哪个通道、冷却与超时 -->
      <section class="tr-card">
        <header class="tr-card-head">
          <span class="tr-card-icon" aria-hidden="true">
            <el-icon><Bolt /></el-icon>
          </span>
          <div class="tr-card-copy">
            <h3 class="tr-card-title">{{ t('triggerRule.actionSection') }}</h3>
            <p class="tr-card-desc">{{ t('triggerRule.actionSectionDesc') }}</p>
          </div>
        </header>
        <div class="tr-card-body">
          <div class="tr-grid">
            <el-form-item :label="t('triggerRule.actionDevice')" required>
              <el-select v-model="form.actionDeviceId" filterable style="width: 100%"
                :placeholder="t('triggerRule.actionDevicePlaceholder')" @change="onActionDeviceChange">
                <el-option v-for="d in devices" :key="d.id" :label="d.name" :value="d.id" />
              </el-select>
            </el-form-item>
            <el-form-item :label="t('triggerRule.targetChannel')" required>
              <el-select v-model="form.targetChannelId" filterable style="width: 100%"
                :placeholder="t('triggerRule.targetChannelPlaceholder')">
                <el-option v-for="c in channels" :key="c.id" :label="channelLabel(c)" :value="c.id" />
              </el-select>
            </el-form-item>
            <el-form-item :label="t('triggerRule.actionAddresses')" required class="tr-span-2">
              <el-select v-model="form.actionAddressIds" multiple filterable style="width: 100%"
                :placeholder="t('triggerRule.actionAddressesPlaceholder')" :disabled="!form.actionDeviceId">
                <el-option v-for="a in actionAddressOptions" :key="a.id" :label="addressLabel(a)" :value="a.id" />
              </el-select>
              <div class="tr-field-hint is-block">{{ t('triggerRule.actionAddressesHint') }}</div>
            </el-form-item>
            <el-form-item :label="t('triggerRule.cooldown')">
              <el-input-number v-model="form.cooldownMs" :min="0" :max="86400000" :step="500"
                controls-position="right" />
              <span class="tr-unit">{{ t('triggerRule.msUnit') }}</span>
            </el-form-item>
            <el-form-item :label="t('triggerRule.timeout')">
              <el-input-number v-model="form.timeoutMs" :min="100" :max="60000" :step="500"
                controls-position="right" />
              <span class="tr-unit">{{ t('triggerRule.msUnit') }}</span>
            </el-form-item>
          </div>
        </div>
      </section>

      <!-- 触发条件：组间 OR、组内 AND，用连接条把它们画出来 -->
      <section class="tr-card">
        <header class="tr-card-head">
          <span class="tr-card-icon" aria-hidden="true">
            <el-icon><Filter /></el-icon>
          </span>
          <div class="tr-card-copy">
            <h3 class="tr-card-title">{{ t('triggerRule.conditionSection') }}</h3>
            <p class="tr-card-desc">{{ t('triggerRule.conditionHint') }}</p>
          </div>
        </header>
        <div class="tr-card-body">
          <div v-for="(group, gi) in form.groups" :key="'g' + gi">
            <div v-if="gi > 0" class="tr-group-join" aria-hidden="true">
              <span class="tr-group-join-line" />
              <span class="tr-group-join-chip">OR</span>
              <span class="tr-group-join-line" />
            </div>

            <div class="tr-group">
              <header class="tr-group-head">
                <span class="tr-group-index" aria-hidden="true">{{ gi + 1 }}</span>
                <span class="tr-group-title">{{ t('triggerRule.groupTitle') }}</span>
                <span class="tr-group-mode">AND</span>
                <el-button class="tr-group-remove" type="danger" link size="small" :icon="Delete"
                  :disabled="form.groups.length <= 1" @click="removeGroup(gi)">
                  {{ t('triggerRule.removeGroup') }}
                </el-button>
              </header>

              <div class="tr-cond-list">
                <div v-for="(cond, ci) in group.conditions" :key="'c' + gi + '-' + ci">
                  <div v-if="ci > 0" class="tr-cond-join" aria-hidden="true">AND</div>
                  <div class="tr-cond-row">
                    <span class="tr-cond-index" aria-hidden="true">{{ ci + 1 }}</span>
                    <el-select v-model="cond.deviceId" filterable size="small" class="w-device"
                      :placeholder="t('triggerRule.conditionDevice')" @change="onConditionDeviceChange(cond)">
                      <el-option v-for="d in devices" :key="d.id" :label="d.name" :value="d.id" />
                    </el-select>
                    <el-select v-model="cond.addressId" filterable size="small" class="w-point"
                      :placeholder="t('triggerRule.conditionPoint')" :disabled="!cond.deviceId"
                      @change="onConditionAddressChange(cond)">
                      <el-option v-for="a in addressOptionsFor(cond.deviceId)" :key="a.id"
                        :label="addressLabel(a)" :value="a.id" />
                    </el-select>
                    <el-select v-model="cond.op" size="small" class="w-op"
                      :placeholder="t('triggerRule.conditionOp')" :disabled="!cond.addressId"
                      @change="onOperatorChange(cond)">
                      <el-option v-for="op in operatorsFor(cond)" :key="op" :label="opLabel(op)" :value="op" />
                    </el-select>

                    <template v-if="cond.op === 'range'">
                      <el-input v-model="cond.values[0]" size="small" class="w-value"
                        :placeholder="t('triggerRule.rangeMin')" />
                      <el-input v-model="cond.values[1]" size="small" class="w-value"
                        :placeholder="t('triggerRule.rangeMax')" />
                    </template>
                    <el-select v-else-if="cond.op === 'in'" v-model="cond.valuesText" multiple filterable
                      allow-create default-first-option size="small" class="w-value-wide"
                      :placeholder="t('triggerRule.inPlaceholder')" />
                    <el-select v-else-if="cond.kind === 'bool'" v-model="cond.values[0]" size="small" class="w-value"
                      :placeholder="t('triggerRule.value')" :disabled="!cond.op">
                      <el-option label="1 / true" value="1" />
                      <el-option label="0 / false" value="0" />
                    </el-select>
                    <el-input v-else v-model="cond.values[0]" size="small" class="w-value"
                      :placeholder="t('triggerRule.value')" :disabled="!cond.op" />

                    <el-button class="tr-cond-remove" type="danger" link size="small" :icon="Delete"
                      :aria-label="t('triggerRule.removeCondition')"
                      :disabled="group.conditions.length <= 1" @click="removeCondition(gi, ci)" />
                  </div>
                </div>
              </div>

              <el-button class="tr-add-row" size="small" :icon="Plus" @click="addCondition(gi)">
                {{ t('triggerRule.addCondition') }}
              </el-button>
            </div>
          </div>

          <el-button class="tr-add-group" :icon="Plus" @click="addGroup">
            {{ t('triggerRule.addGroup') }}
          </el-button>
        </div>
      </section>
    </el-form>

    <template #footer>
      <div class="tr-dialog-footer">
        <p class="tr-footer-hint">{{ t('triggerRule.formFooterHint') }}</p>
        <div class="tr-footer-actions">
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
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import {
  IconBolt as Bolt,
  IconFilter as Filter,
  IconInfoCircle as InfoCircle,
  IconPlus as Plus,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'
import {
  createTriggerRule,
  getTriggerRuleById,
  updateTriggerRule,
} from '@/api/modules/triggerRule'
import { getDeviceObjectList } from '@/api/modules/deviceObject'
import { getDeviceAddressList } from '@/api/modules/deviceAddress'
import { getChannelList } from '@/api/modules/pushChannel'
import { getDataTypes } from '@/api/modules/protocol'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  ruleId: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue', 'saved'])

const { t } = useI18n()

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const OPERATORS_BY_KIND = {
  int: ['range', 'gt', 'ge', 'lt', 'le', 'eq', 'ne'],
  uint: ['range', 'gt', 'ge', 'lt', 'le', 'eq', 'ne'],
  float: ['range', 'gt', 'ge', 'lt', 'le', 'eq', 'ne'],
  bool: ['eq', 'ne'],
  string: ['eq', 'ne', 'in'],
}

const OP_LABEL_KEYS = {
  range: 'triggerRule.opRange',
  gt: 'triggerRule.opGt',
  ge: 'triggerRule.opGe',
  lt: 'triggerRule.opLt',
  le: 'triggerRule.opLe',
  eq: 'triggerRule.opEq',
  ne: 'triggerRule.opNe',
  in: 'triggerRule.opIn',
}

const isEdit = computed(() => !!props.ruleId)

const devices = ref([])
const channels = ref([])
const addressCache = reactive({})
const kindCache = reactive({})
const loadedDevices = reactive({})

const submitting = ref(false)

function newCondition() {
  return { deviceId: '', addressId: '', kind: '', op: '', values: [''], valuesText: [] }
}

const formRef = ref(null)

// 与设备/采集组编码同一口径，输入时即时校验，不等提交。
const entityCodePattern = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/
const formRules = {
  code: [
    { required: true, message: () => t('triggerRule.codeRequired'), trigger: 'blur' },
    { pattern: entityCodePattern, message: () => t('triggerRule.codeInvalid'), trigger: 'blur' },
  ],
  name: [
    { required: true, message: () => t('triggerRule.nameRequired'), trigger: 'blur' },
    { max: 64, message: () => t('triggerRule.nameTooLong'), trigger: 'blur' },
  ],
}

const form = reactive({
  id: '',
  code: '',
  name: '',
  description: '',
  status: 1,
  actionDeviceId: '',
  actionAddressIds: [],
  targetChannelId: '',
  cooldownMs: 0,
  timeoutMs: 5000,
  groups: [{ conditions: [newCondition()] }],
})

const actionAddressOptions = computed(() => addressOptionsFor(form.actionDeviceId))

function addressOptionsFor(deviceId) {
  return deviceId ? addressCache[deviceId] || [] : []
}

function addressLabel(a) {
  const label = a.label || a.name
  return a.name && a.label ? `${a.label}（${a.name}）` : label
}

function channelLabel(c) {
  return c.type ? `${c.name}（${c.type}）` : c.name
}

function opLabel(op) {
  return OP_LABEL_KEYS[op] ? t(OP_LABEL_KEYS[op]) : op
}

function operatorsFor(cond) {
  return OPERATORS_BY_KIND[cond.kind] || []
}

async function loadOptions() {
  const [devRes, chRes] = await Promise.all([
    getDeviceObjectList({ page: 1, size: 100 }),
    getChannelList({ page: 1, size: 100 }),
  ])
  devices.value = (devRes?.records ?? []).filter((d) => d.status === 1)
  channels.value = (chRes?.records ?? []).filter((c) => c.status === 1)
}

async function ensureAddresses(deviceId) {
  if (!deviceId || loadedDevices[deviceId]) return
  loadedDevices[deviceId] = true
  try {
    const res = await getDeviceAddressList({ deviceObjectId: deviceId, page: 1, size: 100 })
    addressCache[deviceId] = (res?.records ?? []).filter(
      (a) => a.status === 1 && /R/i.test(a.rwPermission || ''),
    )
    await ensureKindMap(deviceId)
  } catch {
    delete loadedDevices[deviceId]
  }
}

async function ensureKindMap(deviceId) {
  const dev = devices.value.find((d) => d.id === deviceId)
  const protocolName = dev?.protocolName
  if (!protocolName || kindCache[protocolName]) return
  try {
    const data = await getDataTypes(protocolName)
    kindCache[protocolName] = data?.kindByType ?? {}
  } catch {
    kindCache[protocolName] = {}
  }
}

function kindOf(deviceId, address) {
  const dev = devices.value.find((d) => d.id === deviceId)
  const map = kindCache[dev?.protocolName] || {}
  return map[address?.commonDataType] || address?.kind || ''
}

function ensureDeviceOption(meta) {
  if (!meta?.deviceId || devices.value.some((d) => d.id === meta.deviceId)) return
  devices.value.push({
    id: meta.deviceId,
    name: meta.deviceName || meta.deviceId,
    protocolName: meta.protocolName || '',
    status: 1,
  })
}

function ensureAddressOption(meta) {
  if (!meta?.deviceId || !meta?.id) return
  const list = addressCache[meta.deviceId] || (addressCache[meta.deviceId] = [])
  if (list.some((a) => a.id === meta.id)) return
  list.push({
    id: meta.id,
    label: meta.label,
    name: meta.name,
    commonDataType: meta.dataType,
    kind: meta.kind,
    rwPermission: meta.rwPermission,
  })
  if (!kindCache[meta.protocolName] && meta.kind) {
    kindCache[meta.protocolName] = { [meta.dataType]: meta.kind }
  }
}

async function onActionDeviceChange() {
  form.actionAddressIds = []
  await ensureAddresses(form.actionDeviceId)
}

async function onConditionDeviceChange(cond) {
  cond.addressId = ''
  cond.kind = ''
  cond.op = ''
  cond.values = ['']
  cond.valuesText = []
  await ensureAddresses(cond.deviceId)
}

function onConditionAddressChange(cond) {
  const addr = addressOptionsFor(cond.deviceId).find((a) => a.id === cond.addressId)
  cond.kind = kindOf(cond.deviceId, addr)
  cond.op = ''
  cond.values = ['']
  cond.valuesText = []
}

function onOperatorChange(cond) {
  if (cond.op === 'range') cond.values = ['', '']
  else if (cond.op === 'in') cond.valuesText = []
  else cond.values = ['']
}

function addCondition(gi) {
  form.groups[gi].conditions.push(newCondition())
}

function removeCondition(gi, ci) {
  form.groups[gi].conditions.splice(ci, 1)
}

function addGroup() {
  form.groups.push({ conditions: [newCondition()] })
}

function removeGroup(gi) {
  form.groups.splice(gi, 1)
}

function reset() {
  form.id = ''
  form.code = ''
  form.name = ''
  form.description = ''
  form.status = 1
  form.actionDeviceId = ''
  form.actionAddressIds = []
  form.targetChannelId = ''
  form.cooldownMs = 0
  form.timeoutMs = 5000
  form.groups = [{ conditions: [newCondition()] }]
  formRef.value?.clearValidate()
}

function conditionValues(cond) {
  if (cond.op === 'in') {
    return (cond.valuesText || []).map((v) => String(v).trim()).filter(Boolean)
  }
  return (cond.values || []).map((v) => String(v ?? ''))
}

function validate() {
  if (!form.actionDeviceId) {
    ElMessage.warning(t('triggerRule.actionDeviceRequired'))
    return false
  }
  if (!form.actionAddressIds.length) {
    ElMessage.warning(t('triggerRule.actionAddressesRequired'))
    return false
  }
  if (!form.targetChannelId) {
    ElMessage.warning(t('triggerRule.targetChannelRequired'))
    return false
  }
  if (!form.groups.length) {
    ElMessage.warning(t('triggerRule.conditionRequired'))
    return false
  }
  for (const group of form.groups) {
    if (!group.conditions.length) {
      ElMessage.warning(t('triggerRule.conditionRequired'))
      return false
    }
    for (const cond of group.conditions) {
      if (!cond.addressId || !cond.op) {
        ElMessage.warning(t('triggerRule.conditionIncomplete'))
        return false
      }
      const values = conditionValues(cond)
      if (cond.op === 'range') {
        if (!values[0] && !values[1]) {
          ElMessage.warning(t('triggerRule.rangeRequired'))
          return false
        }
      } else if (cond.op === 'in') {
        if (!values.length) {
          ElMessage.warning(t('triggerRule.inRequired'))
          return false
        }
      } else if (!values[0]) {
        ElMessage.warning(t('triggerRule.valueRequired'))
        return false
      }
    }
  }
  return true
}

function buildPayload() {
  return {
    id: form.id || undefined,
    code: form.code.trim(),
    name: form.name.trim(),
    description: form.description.trim(),
    status: form.status,
    actionDeviceId: form.actionDeviceId,
    actionAddressIds: form.actionAddressIds,
    targetChannelId: form.targetChannelId,
    cooldownMs: form.cooldownMs,
    timeoutMs: form.timeoutMs,
    conditionsJson: {
      groups: form.groups.map((g) => ({
        conditions: g.conditions.map((c) => ({
          addressId: c.addressId,
          op: c.op,
          values: conditionValues(c),
        })),
      })),
    },
  }
}

async function handleSubmit() {
  try {
    await formRef.value?.validate()
  } catch {
    return // 字段错误已由表单项就地显示
  }
  if (!validate()) return
  submitting.value = true
  try {
    const payload = buildPayload()
    if (isEdit.value) {
      await updateTriggerRule(payload)
      ElMessage.success(t('triggerRule.editSuccess'))
    } else {
      delete payload.id
      await createTriggerRule(payload)
      ElMessage.success(t('triggerRule.addSuccess'))
    }
    visible.value = false
    emit('saved')
  } catch {
    // 错误提示由响应拦截器统一给出；保留弹窗让用户修正
  } finally {
    submitting.value = false
  }
}

async function loadRule() {
  const rule = await getTriggerRuleById(props.ruleId)
  form.id = rule.id
  form.code = rule.code || ''
  form.name = rule.name
  form.description = rule.description || ''
  form.status = rule.status
  form.actionDeviceId = rule.actionDeviceId
  form.actionAddressIds = rule.actionAddressIds || []
  form.targetChannelId = rule.targetChannelId
  form.cooldownMs = rule.cooldownMs ?? 0
  form.timeoutMs = rule.timeoutMs ?? 5000

  ;(rule.actionAddresses || []).forEach(ensureDeviceOption)
  ;(rule.conditionAddresses || []).forEach(ensureDeviceOption)
  await ensureAddresses(rule.actionDeviceId)
  for (const a of rule.conditionAddresses || []) {
    await ensureAddresses(a.deviceId)
  }
  ;(rule.actionAddresses || []).forEach(ensureAddressOption)
  ;(rule.conditionAddresses || []).forEach(ensureAddressOption)

  const addrById = {}
  ;[...(rule.actionAddresses || []), ...(rule.conditionAddresses || [])].forEach((a) => {
    addrById[a.id] = a
  })

  const groups = rule.conditionsJson?.groups || []
  form.groups = groups.length
    ? groups.map((g) => ({
        conditions: (g.conditions || []).map((c) => {
          const addr = addrById[c.addressId]
          const isIn = c.op === 'in'
          return {
            deviceId: addr?.deviceId || '',
            addressId: c.addressId,
            kind: addr?.kind || '',
            op: c.op,
            values: c.op === 'range' ? [c.values?.[0] ?? '', c.values?.[1] ?? ''] : [c.values?.[0] ?? ''],
            valuesText: isIn ? [...(c.values || [])] : [],
          }
        }),
      }))
    : [{ conditions: [newCondition()] }]
  formRef.value?.clearValidate()
}

async function init() {
  reset()
  await loadOptions()
  if (isEdit.value) {
    await loadRule()
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) init()
  },
)
</script>

<style scoped>
/* ---------- 弹窗头：eyebrow → 标题 → 说明，三级递进 ---------- */
.tr-dialog-head {
  padding-right: 28px;
}

.tr-dialog-eyebrow {
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

.tr-dialog-eyebrow-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--brand);
  box-shadow: 0 0 0 4px rgba(37, 99, 235, 0.1);
}

.tr-dialog-title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.tr-dialog-sub {
  margin: 8px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--ink-3);
}

.tr-form {
  padding-top: 2px;
}

/* ---------- 分区卡片：白面浮在浅色画布上，用发丝线收边 ---------- */
.tr-card {
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.tr-card + .tr-card {
  margin-top: 16px;
}

.tr-card-head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--line-soft);
}

.tr-card-icon {
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

.tr-card-copy {
  flex: 1;
  min-width: 0;
  padding-top: 1px;
}

.tr-card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.tr-card-desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

/* 开关是整张卡的开关，与标题同排靠右，不再独自占一行 */
.tr-enable {
  display: flex;
  flex: none;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.tr-enable-label {
  font-size: 12.5px;
  color: var(--ink-3);
}

.tr-card-body {
  padding: 20px 22px;
}

/* ---------- 两列字段 ---------- */
.tr-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 20px;
  margin-bottom: -16px;
}

.tr-grid :deep(.el-form-item) {
  margin-bottom: 16px;
}

/* 一侧字段带说明文字时，内容区会被网格拉高；两侧都钉到顶部才能齐平 */
.tr-grid :deep(.el-form-item__content) {
  align-items: flex-start;
  align-content: flex-start;
}

.tr-grid .tr-span-2 {
  grid-column: span 2;
}

.tr-field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.tr-field-hint.is-block {
  flex: 0 0 100%;
}

.tr-unit {
  margin-left: 8px;
  font-size: 12px;
  color: var(--ink-4);
}

/* ---------- 条件组：卡内再嵌一层浅底卡，OR 连接条把组隔开 ---------- */
.tr-group {
  padding: 14px 16px 12px;
  background: #fbfcfe;
  border: 1px solid var(--line);
  border-radius: 14px;
}

.tr-group-join {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 0;
}

.tr-group-join-line {
  flex: 1;
  height: 1px;
  background: var(--line-soft);
}

.tr-group-join-chip {
  padding: 1px 8px;
  font-size: 10.5px;
  font-weight: 650;
  line-height: 16px;
  letter-spacing: 0.08em;
  color: var(--ink-4);
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 999px;
}

.tr-group-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.tr-group-index {
  display: grid;
  flex: none;
  place-items: center;
  width: 22px;
  height: 22px;
  font-size: 11px;
  font-weight: 650;
  color: #1d4ed8;
  background: var(--brand-soft);
  border-radius: 7px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.1);
}

.tr-group-title {
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.tr-group-mode {
  padding: 1px 7px;
  font-size: 10.5px;
  font-weight: 600;
  line-height: 16px;
  letter-spacing: 0.08em;
  color: var(--ink-4);
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 999px;
}

.tr-group-remove {
  margin-left: auto;
}

.tr-cond-list {
  display: flex;
  flex-direction: column;
}

.tr-cond-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

/* 行号与组号同一形状、更弱一档颜色：一眼看出「第几条条件」但不当主角 */
.tr-cond-index {
  display: grid;
  flex: none;
  place-items: center;
  width: 22px;
  height: 22px;
  font-size: 11px;
  font-weight: 600;
  color: var(--ink-4);
  background: #f1f5f9;
  border-radius: 7px;
  font-variant-numeric: tabular-nums;
}

/* 组内条件之间的 AND：左侧一根短竖线接住上下两行 */
.tr-cond-join {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  margin-left: 30px;
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.08em;
  color: var(--ink-4);
}

.tr-cond-join::before {
  content: '';
  width: 1px;
  height: 18px;
  background: var(--line);
}

/* 删除动作默认中性，悬停才染红：两三个红色图标常驻会把条件行读成告警 */
.tr-cond-remove.el-button,
.tr-group-remove.el-button {
  color: var(--ink-4);
}

.tr-cond-remove.el-button:hover,
.tr-group-remove.el-button:hover {
  color: var(--danger);
  background: var(--danger-soft);
}

.w-device {
  width: 150px;
}

.w-point {
  width: 190px;
}

.w-op {
  width: 112px;
}

/* 值输入吃掉行内剩余宽度：否则行尾会留一大段空白，删除按钮像被甩在角落 */
.w-value {
  flex: 1;
  min-width: 120px;
  max-width: 280px;
}

.w-value-wide {
  flex: 1;
  min-width: 180px;
}

/* 虚线「添一行 / 添一组」：比实心按钮安静，明确是往列表里加东西 */
.tr-add-row.el-button {
  width: 100%;
  height: 32px;
  margin-top: 10px;
  color: var(--ink-3);
  background: transparent;
  border: 1px dashed var(--line);
  border-radius: 9px;
}

.tr-add-row.el-button:hover {
  color: var(--brand);
  background: var(--brand-tint);
  border-color: #bcd0f7;
}

.tr-add-group.el-button {
  width: 100%;
  height: 36px;
  margin-top: 4px;
  color: var(--brand);
  background: var(--brand-tint);
  border: 1px dashed #c7d7f2;
  border-radius: 10px;
}

.tr-add-group.el-button:hover {
  color: var(--brand);
  background: var(--brand-soft);
  border-color: var(--brand);
}

/* ---------- 页脚：一句常驻说明 + 动作按钮 ---------- */
.tr-dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.tr-footer-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.tr-footer-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

@media (max-width: 820px) {
  .tr-grid {
    grid-template-columns: 1fr;
  }

  .tr-grid .tr-span-2 {
    grid-column: auto;
  }

  .tr-dialog-footer {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
