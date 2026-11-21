<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
单点控制下发弹窗：写入前二次确认，写入后展示逐点结果与回读值。

实时页与「属性级控制」页共用同一份交互与文案。调用方负责判断点位是否可写
（总开关、协议能力、点位/属性权限），本组件只处理「已经允许写」之后的值录入、
确认与结果呈现 —— 避免两处各写一套确认文案，把安全提示写漂。
-->
<template>
  <el-dialog
    :model-value="modelValue"
    class="iot-dialog"
    :title="t('control.writeTitle')"
    width="640px"
    :close-on-click-modal="false"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div v-if="target" class="control-write">
      <div class="control-identity">
        <div class="control-identity-head">
          <span class="control-identity-mark" aria-hidden="true">
            <el-icon><Point /></el-icon>
          </span>
          <span class="control-identity-name">{{ target.label || target.name }}</span>
          <span class="iot-pill control-identity-perm" :class="writePermissionClass(target)">
            <span class="iot-pill-dot" aria-hidden="true" />
            {{ writePermissionText(target) }}
            <span class="control-identity-perm-code">{{ target.rwPermission }}</span>
          </span>
        </div>

        <dl class="control-identity-meta">
          <div class="control-identity-field">
            <dt>{{ t('control.pointAddress') }}</dt>
            <dd>
              <button
                type="button"
                class="control-identity-addr"
                :title="t('control.copyAddress')"
                @click="copyPointAddress(target.name)"
              >
                <span class="iot-mono">{{ target.name }}</span>
                <el-icon><Copy /></el-icon>
              </button>
            </dd>
          </div>
          <div class="control-identity-field">
            <dt>{{ t('control.dataType') }}</dt>
            <dd class="control-identity-type">
              <span class="iot-mono">{{ target.dataType || '—' }}</span>
              <span v-if="target.commonDataType" class="control-identity-type-sub">
                {{ target.commonDataType }}
              </span>
            </dd>
          </div>
        </dl>
      </div>

      <el-form label-width="96px" @submit.prevent>
        <el-form-item :label="t('control.currentValue')">
          <span class="iot-mono">{{ target.value || '—' }}</span>
          <span v-if="writeHasTransform" class="control-inline-hint">{{ t('control.engineeringValueHint') }}</span>
          <span v-if="target.stale" class="control-inline-hint">{{ t('control.staleHint') }}</span>
        </el-form-item>
        <el-form-item :label="t('control.targetValue')" required>
          <div class="control-field">
            <el-input
              v-model="writeForm.value"
              :placeholder="writeHasTransform ? t('control.rawValuePlaceholder') : t('control.valuePlaceholder')"
              @keyup.enter="submitWrite"
            />
            <p v-if="writeHasTransform" class="control-field-hint">{{ t('control.rawValueHint') }}</p>
            <p v-if="writePreview !== null" class="control-field-hint control-field-preview">
              {{ t('control.previewText', { value: writePreview }) }}
            </p>
          </div>
        </el-form-item>
        <el-form-item :label="t('control.verify')">
          <el-switch v-model="writeForm.verify" />
          <span class="control-inline-hint">{{ t('control.verifyHint') }}</span>
        </el-form-item>
      </el-form>

      <div class="iot-notice is-warning">
        <span class="iot-notice-mark" aria-hidden="true">
          <el-icon><AlertTriangle /></el-icon>
        </span>
        <p class="iot-notice-text">{{ t('control.confirmHint') }}</p>
      </div>

      <div v-if="writeResult" class="control-result">
        <div class="control-result-head">
          <span>{{ writeResult.duplicate ? t('control.duplicate') : t('control.resultTitle') }}</span>
          <span class="control-result-meta">{{ writeResult.requestId }}</span>
        </div>
        <p v-if="writeHasTransform" class="control-result-hint">{{ t('control.resultRawHint') }}</p>
        <el-table :data="writeResult.results" class="iot-table is-compact" size="small">
          <el-table-column prop="addressName" :label="t('control.point')" min-width="120" />
          <el-table-column prop="value" :label="t('control.requested')" width="100" />
          <el-table-column prop="resultValue" :label="t('control.resultValue')" width="100" />
          <el-table-column prop="readbackValue" :label="t('control.readback')" width="100" />
          <el-table-column :label="t('control.status')" width="120">
            <template #default="{ row }">
              <span class="iot-pill" :class="controlResultClass(row)">
                {{ controlResultText(row) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column
            prop="message"
            :label="t('control.message')"
            min-width="160"
            show-overflow-tooltip
          />
        </el-table>
      </div>
    </div>

    <template #footer>
      <div class="flex items-center justify-end gap-2">
        <el-button @click="emit('update:modelValue', false)">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="writing" @click="submitWrite">
          {{ writeResult ? t('control.writeAgain') : t('control.confirmWrite') }}
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconAlertTriangle as AlertTriangle,
  IconCopy as Copy,
  IconPoint as Point,
} from '@tabler/icons-vue'
import { writeControl } from '@/api/modules/control'
import { applyAddressTransformPipeline, parseAddressTransformPipeline } from '@/utils/addressTransform'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  deviceId: { type: String, required: true },
  // 目标点位：addressId/name/label/dataType/rwPermission/value/transformJson
  target: { type: Object, default: null },
})

const emit = defineEmits(['update:modelValue', 'written'])

const { t } = useI18n()

const writeForm = reactive({ value: '', verify: true })
const writeResult = ref(null)
const writing = ref(false)

// 采样值是采集侧换算后的工程值；配了变换时它在另一个值域，下发却只收原始值。
const writeHasTransform = computed(
  () => parseAddressTransformPipeline(props.target?.transformJson).length > 0,
)

// 正向换算预览：输入原始值 → 用与采集侧相同的流水线得到工程值。
// 未配变换、输入为空或换算失败时不展示。
const writePreview = computed(() => {
  if (!writeHasTransform.value) return null
  if (String(writeForm.value ?? '').trim() === '') return null
  const converted = applyAddressTransformPipeline(props.target?.transformJson, writeForm.value)
  return converted.ok ? converted.value : null
})

// 每次打开都重置：当前采样值是工程值，而下发走原始值且没有逆变换。
// 配了变换时不能拿它预填目标值（scale=0.1 时 25.3 会被当成原始值写进寄存器），
// 留空让操作工按原始值填写，下方会给出对应的工程值预览。
watch(
  () => [props.modelValue, props.target],
  ([visible]) => {
    if (!visible || !props.target) return
    const hasTransform = parseAddressTransformPipeline(props.target.transformJson).length > 0
    writeForm.value = hasTransform ? '' : (props.target.value ?? '')
    writeForm.verify = true
    writeResult.value = null
  },
  { immediate: true },
)

function newRequestId() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

async function submitWrite() {
  const target = props.target
  if (!target) return
  const value = String(writeForm.value ?? '')
  if (value.trim() === '' && target.dataType !== 'string') {
    ElMessage.warning(t('control.valueRequired'))
    return
  }

  try {
    await ElMessageBox.confirm(
      t('control.confirmMessage', {
        name: target.label || target.name,
        value,
      }),
      t('control.confirmTitle'),
      {
        type: 'warning',
        confirmButtonText: t('control.confirmWrite'),
        cancelButtonText: t('common.cancel'),
      },
    )
  } catch {
    return // 用户取消
  }

  writing.value = true
  try {
    const res = await writeControl({
      deviceId: props.deviceId,
      requestId: newRequestId(),
      verify: writeForm.verify,
      items: [{ deviceAddressId: target.addressId, value }],
    })
    writeResult.value = res
    const results = res?.results ?? []
    const failed = results.filter((r) => !r.ok).length
    if (failed > 0) {
      ElMessage.warning(t('control.partialFailed', { failed, total: results.length }))
    } else {
      ElMessage.success(res?.duplicate ? t('control.duplicateSuccess') : t('control.writeSuccess'))
    }
    emit('written', res)
  } catch {
    // axios 拦截器已提示业务错误；保留弹窗让用户看到输入并重试
  } finally {
    writing.value = false
  }
}

function controlResultClass(row) {
  if (!row?.ok) return 'is-rose'
  if (row.verified) return 'is-green'
  return 'is-amber'
}

function controlResultText(row) {
  if (!row?.ok) return t('control.failed')
  if (row.verified) return t('control.verified')
  return t('control.unverified')
}

// 权限胶囊：读写用品牌蓝（可操作但不告警），只写用琥珀，只读用中性灰。
// 颜色只表达「能不能写」，不与在线 / 质量等状态色抢语义。
function writePermissionClass(row) {
  const perm = String(row?.rwPermission || '')
  if (perm.includes('R') && perm.includes('W')) return 'is-blue'
  if (perm.includes('W')) return 'is-amber'
  return 'is-gray'
}

function writePermissionText(row) {
  const perm = String(row?.rwPermission || '')
  if (perm.includes('R') && perm.includes('W')) return t('control.permissionRW')
  if (perm.includes('W')) return t('control.permissionW')
  if (perm.includes('R')) return t('control.permissionR')
  return '—'
}

// 网关常经 http 局域网访问，navigator.clipboard 在非安全上下文不可用，
// 此时退回临时 textarea + execCommand。
async function copyPointAddress(address) {
  const text = String(address || '')
  if (!text) return
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    } else {
      const el = document.createElement('textarea')
      el.value = text
      el.style.position = 'fixed'
      el.style.opacity = '0'
      document.body.appendChild(el)
      el.select()
      document.execCommand('copy')
      document.body.removeChild(el)
    }
    ElMessage.success(t('control.addressCopied'))
  } catch {
    ElMessage.warning(t('control.copyFailed'))
  }
}
</script>

<style scoped>
.control-identity {
  padding: 16px 18px 15px;
  margin-bottom: 18px;
  background:
    radial-gradient(120% 150% at 100% 0%, rgba(37, 99, 235, 0.07), transparent 60%),
    linear-gradient(180deg, #fcfdff 0%, #f8fafd 100%);
  border: 1px solid var(--line-soft);
  border-radius: 14px;
}

.control-identity-head {
  display: flex;
  align-items: center;
  gap: 12px;
}

.control-identity-mark {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  color: var(--brand);
  background: var(--brand-soft);
  border: 1px solid rgba(37, 99, 235, 0.12);
  border-radius: 10px;
}

.control-identity-mark .el-icon {
  font-size: 17px;
}

.control-identity-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.control-identity-perm {
  flex: none;
}

/* 权限码做成胶囊内的嵌入式小标签：本地化文案给操作工，码值给对照文档的人 */
.control-identity-perm-code {
  padding: 0 5px;
  font-family: var(--font-mono);
  font-size: 10px;
  line-height: 15px;
  letter-spacing: 0.05em;
  background: rgba(255, 255, 255, 0.6);
  border-radius: 4px;
}

.control-identity-meta {
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) minmax(0, 1fr);
  gap: 14px 20px;
  padding-top: 14px;
  margin: 14px 0 0;
  border-top: 1px dashed var(--line);
}

.control-identity-field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.control-identity-field dt {
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: var(--ink-4);
}

.control-identity-field dd {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  margin: 0;
}

.control-identity-addr {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
  padding: 5px 9px;
  font: inherit;
  color: var(--ink-2);
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 7px;
  cursor: pointer;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.control-identity-addr:hover {
  border-color: #cfdcf3;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.06);
}

.control-identity-addr:focus-visible {
  outline: 2px solid rgba(37, 99, 235, 0.45);
  outline-offset: 1px;
}

.control-identity-addr .iot-mono {
  overflow: hidden;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.control-identity-addr .el-icon {
  flex: none;
  font-size: 13px;
  color: var(--ink-4);
  transition: color 0.15s ease;
}

.control-identity-addr:hover .el-icon {
  color: var(--brand);
}

.control-identity-type {
  font-size: 12.5px;
  color: var(--ink-2);
}

.control-identity-type-sub {
  padding: 1px 6px;
  font-size: 11px;
  color: var(--ink-3);
  background: #f1f5f9;
  border-radius: 4px;
}

@media (max-width: 640px) {
  .control-identity-meta {
    grid-template-columns: minmax(0, 1fr);
  }
}

.control-inline-hint {
  margin-left: 10px;
  font-size: 12px;
  color: var(--ink-4);
}

/* 目标值一栏是「输入框 + 值域说明 + 换算预览」，纵向排列避免挤在一行 */
.control-field {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  width: 100%;
}

.control-field-hint {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-4);
}

.control-field-preview {
  color: var(--ink-3);
}

.control-result-hint {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--ink-4);
}

.control-result {
  margin-top: 16px;
}

.control-result-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
  font-size: 13px;
  font-weight: 600;
  color: var(--ink);
}

.control-result-meta {
  font-size: 11.5px;
  font-weight: 400;
  color: var(--ink-4);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
