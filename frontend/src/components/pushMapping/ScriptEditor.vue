<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="script-editor">
    <!-- 标题与说明独占一行；运行限制移进代码面板顶栏，和它约束的编辑器待在一起 -->
    <div class="script-head">
      <span class="script-head-mark" aria-hidden="true">
        <el-icon><IconCode /></el-icon>
      </span>
      <div class="script-head-text">
        <p class="script-title">{{ t('channel.scriptEditor.title') }}</p>
        <p class="script-hint">{{ t('channel.scriptEditor.hint') }}</p>
      </div>
    </div>

    <!-- 代码面板：行号 / 高亮层 / 透明输入层共用同一份字体度量，保证逐行对齐 -->
    <CodeEditor
      ref="codeEditorRef"
      :model-value="scriptText"
      filename="encode.js"
      :meta="codeMeta"
      :disabled="disabled"
      :placeholder="placeholder"
      :aria-label="t('channel.scriptEditor.title')"
      @update:model-value="onScriptInput"
    >
      <template #file-extra>
        <el-tooltip
          v-if="isSampleScript"
          :content="t('channel.scriptEditor.sampleHint')"
          placement="top"
        >
          <span class="iot-sample-badge">{{ t('channel.scriptEditor.sampleBadge') }}</span>
        </el-tooltip>
      </template>
      <template #bar-right>
        <label class="script-limit">
          <span class="script-limit-label">{{ t('channel.scriptEditor.timeout') }}</span>
          <el-input-number
            v-model="state.timeoutMs"
            :min="1"
            :max="500"
            :step="10"
            size="small"
            :disabled="disabled"
            controls-position="right"
            @change="emitChange"
          />
        </label>
        <label class="script-limit">
          <span class="script-limit-label">{{ t('channel.scriptEditor.maxMessages') }}</span>
          <el-input-number
            v-model="state.maxMessages"
            :min="1"
            :max="1000"
            :step="10"
            size="small"
            :disabled="disabled"
            controls-position="right"
            @change="emitChange"
          />
        </label>
      </template>
    </CodeEditor>

    <!-- 字段速查：用一条淡提示替掉原来紧贴输入框的裸文本 -->
    <p class="script-fields-hint">
      <el-icon><IconInfoCircle /></el-icon>
      <span>{{ t('channel.scriptEditor.fieldsHint') }}</span>
    </p>

    <!-- 原始字段：折叠卡片里的字段表，与订阅页「目标字段」同一套顶栏 + 展开说明按钮 -->
    <section class="script-schema">
      <div class="script-schema-bar">
        <div class="script-schema-bar-title">
          <span class="script-schema-title">{{ t('channel.scriptEditor.schema.title') }}</span>
          <span class="script-schema-count">{{ schemaFieldCount }}</span>
        </div>
        <el-button type="primary" link size="small" @click="schemaOpen = !schemaOpen">
          {{ schemaOpen ? t('channel.scriptEditor.schema.hide') : t('channel.scriptEditor.schema.show') }}
        </el-button>
      </div>
      <div v-if="schemaOpen" class="script-schema-body">
        <p class="script-schema-hint">{{ t('channel.scriptEditor.schema.hint') }}</p>
        <div v-for="group in schemaGroups" :key="group.key" class="script-schema-group">
          <p class="script-schema-group-name">{{ group.key }}</p>
          <div class="script-schema-row is-head" aria-hidden="true">
            <span>{{ t('channel.scriptEditor.schema.field') }}</span>
            <span>{{ t('channel.scriptEditor.schema.type') }}</span>
            <span>{{ t('channel.scriptEditor.schema.description') }}</span>
          </div>
          <div v-for="field in group.fields" :key="field.name" class="script-schema-row">
            <code class="script-schema-field">{{ field.name }}</code>
            <code class="script-schema-type">{{ field.type }}</code>
            <span class="script-schema-desc">{{ field.desc }}</span>
          </div>
        </div>
        <ul class="script-schema-notes">
          <li>{{ t('channel.scriptEditor.schema.noteDevice') }}</li>
          <li>{{ t('channel.scriptEditor.schema.notePreview') }}</li>
          <li>{{ t('channel.scriptEditor.schema.noteValue') }}</li>
        </ul>
      </div>
    </section>

    <!-- 预览：状态头 + 可滚动的报文区 -->
    <section class="script-preview">
      <div class="script-preview-head">
        <span class="script-preview-mark" aria-hidden="true">
          <el-icon><IconTerminal2 /></el-icon>
        </span>
        <span class="script-preview-title">{{ t('channel.scriptEditor.previewTitle') }}</span>
        <span v-if="previewCount" class="script-preview-count">
          {{ t('channel.scriptEditor.previewCount', { count: previewCount }, previewCount) }}
        </span>
        <span v-if="autoPreviewing" class="script-preview-auto">
          {{ t('channel.mapEditor.previewAuto') }}
        </span>
        <el-button
          class="script-preview-btn"
          size="small"
          :icon="IconRefresh"
          :loading="previewLoading"
          :disabled="disabled || previewLoading"
          @click="$emit('preview')"
        >
          {{ t('channel.mapEditor.preview') }}
        </el-button>
      </div>
      <pre v-if="previewText" class="script-preview-body">{{ previewText }}</pre>
      <p v-else class="script-preview-empty">{{ t('channel.mapEditor.previewEmpty') }}</p>
      <p class="script-preview-hint">{{ t('channel.mapEditor.previewHint') }}</p>
    </section>
  </div>
</template>

<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  IconCode,
  IconInfoCircle,
  IconRefresh,
  IconTerminal2,
} from '@tabler/icons-vue'
import CodeEditor from '@/components/CodeEditor.vue'
import { MappingValidationError } from '@/utils/pushMapping'
import { DEFAULT_ENCODE_SCRIPT } from '@/utils/pushScripts'

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

const codeEditorRef = ref(null)
const schemaOpen = ref(false)

const placeholder = DEFAULT_ENCODE_SCRIPT

const scriptText = computed(() => String(props.state.script || ''))
// 与默认示例逐字一致时才算「仍是示例」；用户一改动，标识自动消失
const isSampleScript = computed(
  () =>
    scriptText.value.trim() !== '' &&
    scriptText.value.trim() === DEFAULT_ENCODE_SCRIPT.trim(),
)
const codeMeta = computed(() =>
  t('channel.scriptEditor.codeMeta', {
    lines: scriptText.value.split('\n').length,
    chars: scriptText.value.length,
  }),
)

/* ---------------- 数据结构速查 ---------------- */
const schemaGroups = computed(() => {
  const field = (key) => t(`channel.scriptEditor.schema.fields.${key}`)
  return [
    {
      key: 'batch',
      fields: [
        { name: 'deviceId', type: 'string', desc: field('deviceId') },
        { name: 'collectedAt', type: 'string', desc: field('collectedAt') },
        { name: 'records', type: 'record[]', desc: field('records') },
      ],
    },
    {
      key: 'records[]',
      fields: [
        { name: 'deviceId', type: 'string', desc: field('recordDeviceId') },
        { name: 'deviceName', type: 'string', desc: field('recordDeviceName') },
        { name: 'deviceAddressId', type: 'string', desc: field('recordAddressId') },
        { name: 'deviceAddressName', type: 'string', desc: field('recordAddressName') },
        { name: 'deviceAddressLabel', type: 'string', desc: field('recordAddressLabel') },
        { name: 'value', type: 'string', desc: field('recordValue') },
        { name: 'dataType', type: 'string', desc: field('recordDataType') },
        { name: 'kind', type: 'string', desc: field('recordKind') },
        { name: 'protocol', type: 'string', desc: field('recordProtocol') },
        { name: 'quality', type: 'number', desc: field('recordQuality') },
        { name: 'collectedAt', type: 'string', desc: field('recordCollectedAt') },
      ],
    },
  ]
})

const schemaFieldCount = computed(() =>
  schemaGroups.value.reduce((total, group) => total + group.fields.length, 0),
)

/* ---------------- 预览 ---------------- */
const previewText = computed(() => {
  const res = props.previewResult
  if (!res) return ''
  if (res.error) return `${t('channel.mapEditor.previewError')}: ${res.error}`
  if (res.payloads?.length > 1) return res.payloads.join('\n')
  return res.payload || res.payloads?.[0] || ''
})

const previewCount = computed(() => {
  const res = props.previewResult
  if (!res || res.error) return 0
  return res.payloads?.length ?? (res.payload ? 1 : 0)
})

/* ---------------- 编辑器交互 ---------------- */
function emitChange() {
  emit('change')
}

function onScriptInput(value) {
  props.state.script = value
  emitChange()
}

// 父层回填另一份脚本后，滚动位置回到顶部并同步各层
watch(
  () => props.stateId,
  async () => {
    await nextTick()
    codeEditorRef.value?.resetScroll()
  },
)

function validateProblems() {
  const problems = []
  if (!String(props.state.script || '').trim()) {
    problems.push({ level: 'error', code: 'scriptEmpty' })
  }
  const timeout = Number(props.state.timeoutMs)
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > 500) {
    problems.push({ level: 'error', code: 'scriptTimeoutRange' })
  }
  const maxMessages = Number(props.state.maxMessages)
  if (!Number.isInteger(maxMessages) || maxMessages < 1 || maxMessages > 1000) {
    problems.push({ level: 'error', code: 'scriptMaxMessagesRange' })
  }
  return problems
}

/** 保存/预览用：返回 script 格式的 mapping 对象。 */
function buildMapping() {
  const err = validateProblems().find((p) => p.level === 'error')
  if (err) throw new MappingValidationError(err)
  return {
    script: String(props.state.script),
    timeoutMs: Number(props.state.timeoutMs),
    maxMessages: Number(props.state.maxMessages),
  }
}

defineExpose({ validateProblems, buildMapping })
</script>

<style scoped>
/* ---------- 标题与限制参数 ---------- */
.script-head {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 12px;
}

.script-head-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 30px;
  height: 30px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 9px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.08);
}

.script-head-mark .el-icon {
  font-size: 16px;
}

.script-head-text {
  flex: 1;
  min-width: 0;
}

.script-title {
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  color: var(--ink);
}

.script-hint {
  max-width: 720px;
  margin: 3px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.script-limit {
  display: flex;
  align-items: center;
  gap: 6px;
}

.script-limit-label {
  font-size: 11px;
  color: var(--ink-4);
  white-space: nowrap;
}

.script-limit :deep(.el-input-number) {
  width: 92px;
}

/* ---------- 字段速查 ---------- */
.script-fields-hint {
  display: flex;
  gap: 7px;
  margin: 10px 2px 0;
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--ink-4);
}

.script-fields-hint .el-icon {
  flex: none;
  margin-top: 2px;
  font-size: 13px;
}

/* ---------- 原始字段卡片 ---------- */
.script-schema {
  margin-top: 12px;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 14px;
}

.script-schema-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px 8px 14px;
  background: var(--canvas);
}

.script-schema-bar-title {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.script-schema-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2);
}

.script-schema-count {
  padding: 1px 8px;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  color: var(--ink-4);
  background: var(--surface);
  border: 1px solid var(--line-soft);
  border-radius: 999px;
}

.script-schema-body {
  padding: 12px 14px 14px;
  border-top: 1px solid var(--line-soft);
}

.script-schema-hint {
  margin: 0 0 10px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-4);
}

.script-schema-group + .script-schema-group {
  margin-top: 10px;
}

.script-schema-group-name {
  margin: 0 0 4px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  font-weight: 600;
  color: var(--brand);
}

.script-schema-row {
  display: grid;
  grid-template-columns: minmax(160px, 210px) 78px 1fr;
  gap: 10px;
  align-items: baseline;
  padding: 5px 8px;
  border-radius: 6px;
}

.script-schema-row:not(.is-head):hover {
  background: var(--canvas);
}

.script-schema-row.is-head {
  padding-bottom: 3px;
  font-size: 11px;
  color: var(--ink-4);
  border-bottom: 1px solid var(--line-soft);
  border-radius: 0;
}

.script-schema-field {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--ink);
}

.script-schema-type {
  justify-self: start;
  padding: 1px 7px;
  font-family: var(--font-mono);
  font-size: 10.5px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 5px;
}

.script-schema-desc {
  font-size: 12px;
  line-height: 1.55;
  color: var(--ink-3);
}

.script-schema-notes {
  margin: 12px 0 0;
  padding-left: 18px;
  font-size: 11.5px;
  line-height: 1.7;
  color: var(--ink-4);
}

/* ---------- 预览卡片 ---------- */
.script-preview {
  margin-top: 14px;
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 14px;
}

.script-preview-head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  padding: 8px 10px 8px 14px;
  background: var(--canvas);
  border-bottom: 1px solid var(--line-soft);
}

.script-preview-mark {
  display: inline-flex;
  font-size: 14px;
  color: var(--ink-4);
}

.script-preview-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2);
}

.script-preview-count {
  padding: 1px 8px;
  font-size: 11px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 999px;
}

.script-preview-auto {
  font-size: 11.5px;
  color: var(--brand);
}

.script-preview-btn {
  margin-left: auto;
}

.script-preview-body {
  max-height: 240px;
  margin: 0;
  padding: 12px 14px;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.6;
  color: var(--ink-2);
  white-space: pre-wrap;
  word-break: break-all;
  scrollbar-width: thin;
}

.script-preview-empty {
  margin: 0;
  padding: 18px 14px;
  font-size: 12px;
  color: var(--ink-4);
}

.script-preview-hint {
  margin: 0;
  padding: 0 14px 10px;
  font-size: 11px;
  line-height: 1.55;
  color: var(--ink-4);
}

@media (max-width: 900px) {
  .script-head {
    flex-direction: column;
    gap: 10px;
  }

  .script-schema-row {
    grid-template-columns: minmax(140px, 180px) 70px 1fr;
  }
}
</style>
