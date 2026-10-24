<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="code-editor" :class="{ 'is-disabled': disabled }">
    <div class="code-editor-bar">
      <span class="code-editor-file">
        <el-icon><IconFileCode /></el-icon>
        {{ filename }}
        <slot name="file-extra" />
      </span>
      <div class="code-editor-bar-right">
        <slot name="bar-right" />
        <span v-if="meta" class="code-editor-meta">{{ meta }}</span>
      </div>
    </div>
    <div class="code-editor-body" :style="{ height }">
      <div ref="gutterRef" class="code-editor-gutter" aria-hidden="true">
        <div v-for="n in lineCount" :key="n" class="code-editor-gutter-line">{{ n }}</div>
      </div>
      <div class="code-editor-pane">
        <pre ref="highlightRef" class="code-editor-highlight" aria-hidden="true"><code v-html="highlighted" /></pre>
        <textarea
          ref="textareaRef"
          class="code-editor-input"
          :value="modelValue"
          :disabled="disabled"
          :placeholder="placeholder"
          :aria-label="ariaLabel || filename"
          spellcheck="false"
          autocapitalize="off"
          autocomplete="off"
          wrap="off"
          @input="handleInput"
          @scroll="syncScroll"
          @keydown.tab.prevent="insertIndent"
        />
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { IconFileCode } from '@tabler/icons-vue'

const props = defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  filename: {
    type: String,
    default: 'script.js',
  },
  height: {
    type: String,
    default: '340px',
  },
  meta: {
    type: String,
    default: '',
  },
  disabled: {
    type: Boolean,
    default: false,
  },
  placeholder: {
    type: String,
    default: '',
  },
  ariaLabel: {
    type: String,
    default: '',
  },
})

const emit = defineEmits(['update:modelValue'])

const textareaRef = ref(null)
const highlightRef = ref(null)
const gutterRef = ref(null)

/* ---------------- 语法高亮 ----------------
   单次扫描的关键字/字符串/数字着色：所有片段先转义再拼 HTML，
   脚本内容始终以文本参与，不会被当成标记执行。 */
const KEYWORDS = new Set([
  'async', 'await', 'break', 'case', 'catch', 'class', 'const', 'continue',
  'default', 'delete', 'do', 'else', 'extends', 'false', 'finally', 'for',
  'function', 'if', 'in', 'instanceof', 'let', 'new', 'null', 'of', 'return',
  'switch', 'this', 'throw', 'true', 'try', 'typeof', 'undefined', 'var',
  'void', 'while', 'yield',
])

const BUILTINS = new Set([
  'Array', 'Boolean', 'Date', 'JSON', 'Math', 'Number', 'Object', 'RegExp',
  'String', 'encodeURIComponent', 'isNaN', 'parseFloat', 'parseInt',
])

const TOKEN_RE =
  /(\/\/[^\n]*|\/\*[\s\S]*?\*\/)|("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|`(?:\\.|[^`\\])*`)|(0[xX][\da-fA-F]+|\d+(?:\.\d+)?)\b|([A-Za-z_$][\w$]*)/g

const HTML_ESCAPES = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
}

function escapeHtml(text) {
  return text.replace(/[&<>"']/g, (char) => HTML_ESCAPES[char])
}

function highlightScript(source) {
  if (!source) return ''
  let html = ''
  let last = 0
  for (const match of source.matchAll(TOKEN_RE)) {
    const [token, comment, string, number, word] = match
    html += escapeHtml(source.slice(last, match.index))
    if (comment) {
      html += `<span class="tok-comment">${escapeHtml(token)}</span>`
    } else if (string) {
      html += `<span class="tok-string">${escapeHtml(token)}</span>`
    } else if (number) {
      html += `<span class="tok-number">${escapeHtml(token)}</span>`
    } else if (word) {
      let cls = ''
      if (KEYWORDS.has(word)) cls = 'tok-keyword'
      else if (BUILTINS.has(word)) cls = 'tok-builtin'
      else if (/^\s*\(/.test(source.slice(match.index + token.length))) cls = 'tok-fn'
      html += cls ? `<span class="${cls}">${escapeHtml(word)}</span>` : escapeHtml(word)
    }
    last = match.index + token.length
  }
  html += escapeHtml(source.slice(last))
  return html
}

const sourceText = computed(() => String(props.modelValue || ''))
const highlighted = computed(() => highlightScript(sourceText.value))
const lineCount = computed(() => sourceText.value.split('\n').length)

/* ---------------- 交互 ---------------- */
function handleInput(event) {
  emit('update:modelValue', event.target.value)
}

/** 高亮层与行号跟随输入框滚动；三层滚动位置必须一致。 */
function syncScroll(event) {
  const target = event.target
  if (highlightRef.value) {
    highlightRef.value.scrollTop = target.scrollTop
    highlightRef.value.scrollLeft = target.scrollLeft
  }
  if (gutterRef.value) {
    gutterRef.value.scrollTop = target.scrollTop
  }
}

/** Tab 键插入两个空格而不是移出焦点；setRangeText 保留原生撤销栈。 */
function insertIndent(event) {
  if (props.disabled) return
  const target = event.target
  if (typeof target.setRangeText !== 'function') return
  target.setRangeText('  ', target.selectionStart, target.selectionEnd, 'end')
  emit('update:modelValue', target.value)
}

/** 父层替换脚本内容后调用：滚动位置回到顶部并同步各层。 */
function resetScroll() {
  const target = textareaRef.value
  if (!target) return
  target.scrollTop = 0
  target.scrollLeft = 0
  syncScroll({ target })
}

defineExpose({ resetScroll })
</script>

<style scoped>
.code-editor {
  overflow: hidden;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 14px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.03);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
}

.code-editor:focus-within {
  border-color: rgba(37, 99, 235, 0.45);
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.1), 0 1px 2px rgba(15, 23, 42, 0.04);
}

.code-editor.is-disabled {
  opacity: 0.8;
}

.code-editor-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 6px 12px;
  padding: 8px 14px;
  background: var(--canvas);
  border-bottom: 1px solid var(--line-soft);
}

.code-editor-file {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-family: var(--font-mono);
  font-size: 11.5px;
  font-weight: 500;
  color: var(--ink-3);
}

.code-editor-file .el-icon {
  font-size: 13px;
  color: var(--ink-4);
}

.code-editor-bar-right {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 12px;
  min-width: 0;
}

.code-editor-meta {
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  color: var(--ink-4);
}

.code-editor-body {
  display: flex;
}

/* 行号与输入框必须共用字体度量，否则滚动后逐行错位 */
.code-editor-gutter {
  flex: none;
  min-width: 46px;
  padding: 14px 10px 14px 12px;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
  text-align: right;
  background: var(--canvas);
  border-right: 1px solid var(--line-soft);
  user-select: none;
}

.code-editor-gutter-line {
  height: 19.8px;
}

.code-editor-pane {
  position: relative;
  flex: 1;
  min-width: 0;
}

.code-editor-highlight,
.code-editor-input {
  position: absolute;
  inset: 0;
  box-sizing: border-box;
  margin: 0;
  padding: 14px 16px;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 1.65;
  tab-size: 2;
}

.code-editor-highlight {
  overflow: hidden;
  color: var(--ink);
  white-space: pre;
  pointer-events: none;
}

.code-editor-highlight code {
  display: block;
  font: inherit;
}

.code-editor-input {
  width: 100%;
  height: 100%;
  overflow: auto;
  color: transparent;
  caret-color: var(--ink);
  white-space: pre;
  background: transparent;
  border: 0;
  outline: none;
  resize: none;
  scrollbar-width: thin;
}

.code-editor-input::placeholder {
  color: var(--ink-4);
}

.code-editor-input::selection {
  background: rgba(37, 99, 235, 0.18);
}

.code-editor.is-disabled .code-editor-input {
  cursor: not-allowed;
}

/* 语法着色：只借关键字/字符串/数字区分结构，强调色仍是全站唯一的那种蓝 */
.code-editor-highlight :deep(.tok-keyword) {
  color: #7c3aed;
}

.code-editor-highlight :deep(.tok-string) {
  color: #047857;
}

.code-editor-highlight :deep(.tok-number) {
  color: #b45309;
}

.code-editor-highlight :deep(.tok-builtin) {
  color: #0e7490;
}

.code-editor-highlight :deep(.tok-fn) {
  color: var(--brand);
}

.code-editor-highlight :deep(.tok-comment) {
  color: var(--ink-4);
  font-style: italic;
}
</style>
