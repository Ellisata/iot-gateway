<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
  事件触发规则表单。

  条件采用 DNF：组间 OR、组内 AND。每组里每一行是（设备 → 点位 → 算子 → 值）。
  算子按点位 Kind 过滤；Kind 由「协议 + 通用数据类型」经 /protocol/getDataTypes 的
  kindByType 解析并缓存，不在前端另写一份类型表。
-->
<template>
  <el-dialog
    v-model="visible"
    class="iot-dialog"
    :title="isEdit ? t('triggerRule.editTitle') : t('triggerRule.addTitle')"
    width="860px"
    :close-on-click-modal="false"
    @close="reset"
  >
    <el-form ref="formRef" :model="form" :rules="formRules" label-width="120px" status-icon>
      <el-form-item :label="t('triggerRule.code')" prop="code" required>
        <el-input v-model="form.code" maxlength="64" show-word-limit :disabled="isEdit"
          :placeholder="t('triggerRule.codePlaceholder')" />
        <div class="iot-form-hint is-block">{{ t('triggerRule.codeHint') }}</div>
      </el-form-item>
      <el-form-item :label="t('triggerRule.name')" prop="name" required>
        <el-input v-model="form.name" maxlength="64" show-word-limit
          :placeholder="t('triggerRule.namePlaceholder')" />
      </el-form-item>
      <el-form-item :label="t('common.description')">
        <el-input v-model="form.description" type="textarea" :rows="2" maxlength="500" show-word-limit />
      </el-form-item>
      <el-form-item :label="t('common.status')">
        <el-switch v-model="form.status" :active-value="1" :inactive-value="0"
          :active-text="t('common.enabled')" :inactive-text="t('common.enableDisable.off')" />
      </el-form-item>

      <el-divider content-position="left">{{ t('triggerRule.actionSection') }}</el-divider>

      <el-form-item :label="t('triggerRule.actionDevice')" required>
        <el-select v-model="form.actionDeviceId" filterable style="width: 100%"
          :placeholder="t('triggerRule.actionDevicePlaceholder')" @change="onActionDeviceChange">
          <el-option v-for="d in devices" :key="d.id" :label="d.name" :value="d.id" />
        </el-select>
      </el-form-item>
      <el-form-item :label="t('triggerRule.actionAddresses')" required>
        <el-select v-model="form.actionAddressIds" multiple filterable style="width: 100%"
          :placeholder="t('triggerRule.actionAddressesPlaceholder')" :disabled="!form.actionDeviceId">
          <el-option v-for="a in actionAddressOptions" :key="a.id" :label="addressLabel(a)" :value="a.id" />
        </el-select>
        <div class="iot-form-hint is-block">{{ t('triggerRule.actionAddressesHint') }}</div>
      </el-form-item>
      <el-form-item :label="t('triggerRule.targetChannel')" required>
        <el-select v-model="form.targetChannelId" filterable style="width: 100%"
          :placeholder="t('triggerRule.targetChannelPlaceholder')">
          <el-option v-for="c in channels" :key="c.id" :label="channelLabel(c)" :value="c.id" />
        </el-select>
      </el-form-item>
      <el-form-item :label="t('triggerRule.cooldown')">
        <el-input-number v-model="form.cooldownMs" :min="0" :max="86400000" :step="500" controls-position="right" />
        <span class="unit-hint">{{ t('triggerRule.msUnit') }}</span>
      </el-form-item>
      <el-form-item :label="t('triggerRule.timeout')">
        <el-input-number v-model="form.timeoutMs" :min="100" :max="60000" :step="500" controls-position="right" />
        <span class="unit-hint">{{ t('triggerRule.msUnit') }}</span>
      </el-form-item>

      <el-divider content-position="left">{{ t('triggerRule.conditionSection') }}</el-divider>
      <div class="iot-form-hint is-block condition-hint">{{ t('triggerRule.conditionHint') }}</div>

      <div v-for="(group, gi) in form.groups" :key="'g' + gi" class="condition-group">
        <div class="condition-group-head">
          <span class="condition-group-title">{{ t('triggerRule.groupLabel', { n: gi + 1 }) }}</span>
          <el-button type="danger" link size="small" :icon="Delete" :disabled="form.groups.length <= 1"
            @click="removeGroup(gi)">
            {{ t('triggerRule.removeGroup') }}
          </el-button>
        </div>

        <div v-for="(cond, ci) in group.conditions" :key="'c' + gi + '-' + ci" class="condition-row">
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

          <el-button type="danger" link size="small" :icon="Delete"
            :disabled="group.conditions.length <= 1" @click="removeCondition(gi, ci)" />
        </div>

        <el-button type="primary" link size="small" :icon="Plus" @click="addCondition(gi)">
          {{ t('triggerRule.addCondition') }}
        </el-button>
      </div>

      <el-button type="primary" plain size="small" :icon="Plus" @click="addGroup">
        {{ t('triggerRule.addGroup') }}
      </el-button>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="submitting" @click="handleSubmit">{{ t('common.confirm') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { IconPlus as Plus, IconTrash as Delete } from '@tabler/icons-vue'
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
.unit-hint {
  margin-left: 8px;
  color: var(--ink-3);
  font-size: 12px;
}
.condition-hint {
  margin-bottom: 10px;
}
.condition-group {
  margin-bottom: 14px;
  padding: 10px 12px;
  border: 1px solid var(--line-1);
  border-radius: 10px;
  background: var(--surface-2);
}
.condition-group-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}
.condition-group-title {
  font-size: 13px;
  font-weight: 600;
}
.condition-row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
  flex-wrap: wrap;
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
.w-value {
  width: 120px;
}
.w-value-wide {
  flex: 1;
  min-width: 180px;
}
</style>
