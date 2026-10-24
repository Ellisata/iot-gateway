<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="mapping-editor">
    <div class="mapping-editor-head">
      <div>
        <p class="mapping-editor-title">{{ t('channel.mapEditor.title') }}</p>
        <p class="mapping-editor-hint">{{ t('channel.mapEditor.hint') }}</p>
      </div>
      <el-radio-group
        :model-value="mode"
        size="small"
        :disabled="disabled"
        @change="switchMode"
      >
        <el-radio-button value="visual">{{ t('channel.mapEditor.modeVisual') }}</el-radio-button>
        <el-radio-button value="json">{{ t('channel.mapEditor.modeJson') }}</el-radio-button>
      </el-radio-group>
    </div>

    <template v-if="mode === 'visual'">
      <div class="mapping-shape">
        <span class="mapping-shape-label">{{ t('channel.mapEditor.shape') }}</span>
        <el-radio-group
          :model-value="state.shape"
          size="small"
          :disabled="disabled"
          @change="setShape"
        >
          <el-radio-button value="array">{{ t('channel.mapEditor.shapeArray') }}</el-radio-button>
          <el-radio-button value="object">{{ t('channel.mapEditor.shapeObject') }}</el-radio-button>
          <el-radio-button value="record">{{ t('channel.mapEditor.shapeRecord') }}</el-radio-button>
        </el-radio-group>
        <div class="mapping-presets">
          <span>{{ t('channel.mapEditor.preset') }}</span>
          <el-button text size="small" :disabled="disabled" @click="usePreset('flat')">
            {{ t('channel.mapEditor.presetFlat') }}
          </el-button>
          <el-button text size="small" :disabled="disabled" @click="usePreset('envelope')">
            {{ t('channel.mapEditor.presetEnvelope') }}
          </el-button>
          <el-button text size="small" :disabled="disabled" @click="usePreset('record')">
            {{ t('channel.mapEditor.presetRecord') }}
          </el-button>
        </div>
      </div>

      <div v-if="state.shape === 'object'" class="mapping-envelope">
        <span class="mapping-shape-label">{{ t('channel.mapEditor.envelope') }}</span>
        <el-input
          v-model="state.envelope"
          size="small"
          :disabled="disabled"
          :placeholder="t('channel.mapEditor.envelopePlaceholder')"
          class="mapping-envelope-input"
          @input="emitChange"
        />
        <span v-if="effectiveEnvelope" class="mapping-envelope-effective">
          {{ t('channel.mapEditor.effectiveEnvelope', { key: effectiveEnvelope }) }}
        </span>
      </div>

      <div class="mapping-section">
        <div class="mapping-section-head">
          <span class="mapping-section-title">{{ t('channel.mapEditor.fields') }}</span>
          <div class="mapping-section-actions">
            <el-button text size="small" :disabled="disabled" @click="fillCommon">
              {{ t('channel.mapEditor.fillCommon') }}
            </el-button>
            <el-button text size="small" :disabled="disabled" @click="addField">
              {{ t('channel.mapEditor.addField') }}
            </el-button>
          </div>
        </div>
        <div class="mapping-table">
          <div class="mapping-table-head">
            <span>{{ t('channel.mapEditor.key') }}</span>
            <span>{{ t('channel.mapEditor.sourceLabel') }}</span>
            <span>{{ t('channel.mapEditor.value') }}</span>
            <span>{{ t('channel.mapEditor.typeLabel') }}</span>
            <span />
          </div>
          <p v-if="!state.fields.length" class="mapping-table-empty">
            {{ t('channel.mapEditor.noFields') }}
          </p>
          <MappingFieldRow
            v-for="(row, index) in state.fields"
            :key="row.id"
            :row="row"
            section="fields"
            :disabled="disabled"
            @change="emitChange"
            @duplicate="duplicateRow(state.fields, index)"
            @remove="removeRow(state.fields, index)"
          />
        </div>
      </div>

      <div v-if="state.shape === 'object'" class="mapping-section">
        <div class="mapping-section-head">
          <div>
            <span class="mapping-section-title">{{ t('channel.mapEditor.batchFields') }}</span>
            <p class="mapping-section-hint">{{ t('channel.mapEditor.batchFieldsHint') }}</p>
          </div>
          <el-button text size="small" :disabled="disabled" @click="addBatchField">
            {{ t('channel.mapEditor.addBatchField') }}
          </el-button>
        </div>
        <div class="mapping-table">
          <div class="mapping-table-head">
            <span>{{ t('channel.mapEditor.key') }}</span>
            <span>{{ t('channel.mapEditor.sourceLabel') }}</span>
            <span>{{ t('channel.mapEditor.value') }}</span>
            <span>{{ t('channel.mapEditor.typeLabel') }}</span>
            <span />
          </div>
          <p v-if="!state.batchFields.length" class="mapping-table-empty">
            {{ t('channel.mapEditor.noBatchFields') }}
          </p>
          <MappingFieldRow
            v-for="(row, index) in state.batchFields"
            :key="row.id"
            :row="row"
            section="batchFields"
            :disabled="disabled"
            @change="emitChange"
            @duplicate="duplicateRow(state.batchFields, index)"
            @remove="removeRow(state.batchFields, index)"
          />
        </div>
      </div>

      <div class="mapping-validation">
        <p class="mapping-validation-title">{{ t('channel.mapEditor.validationTitle') }}</p>
        <p v-if="!problems.length" class="mapping-validation-ok">
          {{ t('channel.mapEditor.validationOk') }}
        </p>
        <ul v-else class="mapping-validation-list">
          <li
            v-for="(problem, index) in problems"
            :key="`${problem.code}-${index}`"
            :class="`is-${problem.level}`"
          >
            {{ problemText(problem) }}
          </li>
        </ul>
      </div>
    </template>

    <template v-else>
      <p class="mapping-json-hint">{{ t('channel.mapEditor.jsonModeHint') }}</p>
      <el-input
        v-model="jsonText"
        type="textarea"
        :rows="16"
        class="mapping-json-editor"
        :disabled="disabled"
        @blur="validateJsonText"
      />
      <el-alert
        v-if="jsonError"
        :title="jsonError"
        type="error"
        :closable="false"
        show-icon
        class="mapping-json-error"
      />
    </template>

    <div class="mapping-preview">
      <div class="mapping-preview-actions">
        <el-button
          size="small"
          :loading="previewLoading"
          :disabled="disabled || previewLoading"
          @click="$emit('preview')"
        >
          {{ t('channel.mapEditor.preview') }}
        </el-button>
        <span v-if="autoPreviewing" class="mapping-preview-auto">
          {{ t('channel.mapEditor.previewAuto') }}
        </span>
        <span class="mapping-preview-hint">{{ t('channel.mapEditor.previewHint') }}</span>
      </div>
      <pre v-if="previewText" class="mapping-preview-text">{{ previewText }}</pre>
      <p v-else class="mapping-preview-empty">{{ t('channel.mapEditor.previewEmpty') }}</p>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import MappingFieldRow from './MappingFieldRow.vue'
import {
  MappingValidationError,
  applyPreset,
  createRow,
  fillCommonFields,
  firstError,
  mappingToState,
  stateToMapping,
  validateState,
} from '@/utils/pushMapping'

const props = defineProps({
  state: {
    type: Object,
    required: true,
  },
  stateId: {
    type: Number,
    default: 0,
  },
  disabled: {
    type: Boolean,
    default: false,
  },
  previewResult: {
    type: Object,
    default: null,
  },
  previewLoading: {
    type: Boolean,
    default: false,
  },
  autoPreviewing: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['change', 'preview'])
const { t } = useI18n()

const mode = ref('visual')
const jsonText = ref('')
const jsonError = ref('')

const problems = computed(() => (mode.value === 'visual' ? validateState(props.state) : []))

const hasBatchRows = computed(() =>
  props.state.batchFields.some((row) => String(row.key || '').trim() !== ''),
)

const effectiveEnvelope = computed(() => {
  if (props.state.shape !== 'object') return ''
  const envelope = String(props.state.envelope || '').trim()
  if (envelope) return envelope
  return hasBatchRows.value ? 'records' : ''
})

const previewText = computed(() => {
  const res = props.previewResult
  if (!res) return ''
  if (res.error) return `${t('channel.mapEditor.previewError')}: ${res.error}`
  if (res.payloads?.length > 1) return res.payloads.join('\n')
  return res.payload || res.payloads?.[0] || ''
})

watch(
  () => props.stateId,
  () => {
    mode.value = 'visual'
    jsonText.value = ''
    jsonError.value = ''
  },
)

function problemText(problem) {
  if (!problem) return ''
  return t(`channel.mapEditor.problems.${problem.code}`, problem.params || {})
}

function emitChange() {
  emit('change')
}

function setShape(shape) {
  props.state.shape = shape
  emitChange()
}

function usePreset(preset) {
  applyPreset(props.state, preset)
  emitChange()
}

function addField() {
  props.state.fields.push(createRow())
  emitChange()
}

function addBatchField() {
  props.state.batchFields.push(createRow())
  emitChange()
}

function fillCommon() {
  fillCommonFields(props.state)
  emitChange()
}

function duplicateRow(rows, index) {
  const source = rows[index]
  const copy = createRow({
    key: source.key,
    source: source.source,
    value: source.value,
    type: source.type,
    default: source.default,
    mappingRows: (source.mappingRows || []).map((p) => ({ ...p })),
    transform: (source.transform || []).map((tr) => ({
      ...tr,
      id: `copy-${tr.id}-${Date.now()}`,
    })),
    expanded: source.expanded,
  })
  rows.splice(index + 1, 0, copy)
  emitChange()
}

function removeRow(rows, index) {
  rows.splice(index, 1)
  emitChange()
}

function parseJsonState() {
  const text = jsonText.value.trim()
  if (!text) {
    return { problem: { code: 'jsonSyntax', params: { message: t('channel.mappingRequired') } } }
  }
  let parsed
  try {
    parsed = JSON.parse(text)
  } catch (err) {
    return { problem: { code: 'jsonSyntax', params: { message: err.message } } }
  }
  const res = mappingToState(parsed)
  if (!res.ok) {
    return { problem: { code: res.code, params: res.params } }
  }
  return { state: res.state }
}

function applyState(next) {
  props.state.version = next.version
  props.state.shape = next.shape
  props.state.envelope = next.envelope
  props.state.fields.splice(0, props.state.fields.length, ...next.fields)
  props.state.batchFields.splice(0, props.state.batchFields.length, ...next.batchFields)
}

function validateJsonText() {
  if (!jsonText.value.trim()) {
    jsonError.value = ''
    return
  }
  const { problem } = parseJsonState()
  jsonError.value = problem ? problemText(problem) : ''
}

function switchMode(next) {
  if (next === mode.value) return
  if (next === 'json') {
    jsonText.value = JSON.stringify(stateToMapping(props.state), null, 2)
    jsonError.value = ''
    mode.value = 'json'
    return
  }
  const { problem, state } = parseJsonState()
  if (problem) {
    jsonError.value = problemText(problem)
    return
  }
  applyState(state)
  jsonError.value = ''
  mode.value = 'visual'
  emitChange()
}

function openJson(text, problem) {
  mode.value = 'json'
  jsonText.value =
    typeof text === 'string' ? text : JSON.stringify(text ?? {}, null, 2)
  jsonError.value = problem ? problemText(problem) : ''
}

function validateProblems() {
  if (mode.value === 'visual') return validateState(props.state)
  const { problem } = parseJsonState()
  return problem ? [{ level: 'error', ...problem }] : []
}

/** JSON 模式里是否有尚未转成 state 的文本（父层据此避免自动填充覆盖）。 */
function hasJsonDraft() {
  return mode.value === 'json' && jsonText.value.trim() !== ''
}

/** 保存/预览用：JSON 模式先解析再校验，失败抛 MappingValidationError。 */
function buildMapping() {
  if (mode.value === 'json') {
    const { problem, state } = parseJsonState()
    if (problem) {
      jsonError.value = problemText(problem)
      throw new MappingValidationError(problem)
    }
    applyState(state)
  }
  const err = firstError(validateState(props.state))
  if (err) {
    if (mode.value === 'json') jsonError.value = problemText(err)
    throw new MappingValidationError(err)
  }
  return stateToMapping(props.state)
}

defineExpose({ buildMapping, validateProblems, hasJsonDraft, openJson, problemText })
</script>

<style scoped>
.mapping-editor {
  width: 100%;
}

.mapping-editor-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}

.mapping-editor-title {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink, #0f172a);
}

.mapping-editor-hint {
  margin: 3px 0 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-shape {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin-bottom: 10px;
}

.mapping-shape-label {
  flex: none;
  font-size: 12.5px;
  color: var(--ink-3, #64748b);
}

.mapping-presets {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-left: auto;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-envelope {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.mapping-envelope-input {
  width: 220px;
}

.mapping-envelope-effective {
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-section {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--line-soft, #e2e8f0);
}

.mapping-section-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.mapping-section-title {
  font-size: 12.5px;
  font-weight: 650;
  color: var(--ink-2, #475569);
}

.mapping-section-hint {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-section-actions {
  display: flex;
  gap: 2px;
}

.mapping-table {
  overflow-x: auto;
}

.mapping-table-head {
  display: grid;
  grid-template-columns: minmax(150px, 1.1fr) 132px minmax(200px, 1.8fr) 108px 96px;
  gap: 8px;
  padding: 6px 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
  border-bottom: 1px solid var(--line-soft, #e2e8f0);
}

.mapping-table-empty {
  margin: 8px 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-validation {
  margin-top: 12px;
  padding: 10px 12px;
  background: var(--surface-2, #f8fafc);
  border-radius: 10px;
}

.mapping-validation-title {
  margin: 0 0 4px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2, #475569);
}

.mapping-validation-ok {
  margin: 0;
  font-size: 12px;
  color: #15803d;
}

.mapping-validation-list {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  line-height: 1.7;
}

.mapping-validation-list .is-error {
  color: #b91c1c;
}

.mapping-validation-list .is-warning {
  color: #b45309;
}

.mapping-json-hint {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-json-editor :deep(.el-textarea__inner) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
}

.mapping-json-error {
  margin-top: 8px;
}

.mapping-preview {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--line-soft, #e2e8f0);
}

.mapping-preview-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
}

.mapping-preview-auto {
  font-size: 12px;
  color: var(--brand, #2563eb);
}

.mapping-preview-hint {
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.mapping-preview-text {
  max-height: 240px;
  margin: 10px 0 0;
  padding: 12px 14px;
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.55;
  color: var(--ink-2, #475569);
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--surface-2, #f8fafc);
  border: 1px solid var(--line-soft, #e2e8f0);
  border-radius: 10px;
}

.mapping-preview-empty {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}
</style>
