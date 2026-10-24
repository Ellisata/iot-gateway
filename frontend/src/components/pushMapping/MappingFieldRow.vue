<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="map-field" :class="{ 'is-expanded': row.expanded }">
    <div class="map-field-main">
      <el-input
        v-model="row.key"
        size="small"
        :disabled="disabled"
        :placeholder="KEY_PLACEHOLDER"
        class="map-field-key"
        @input="emitChange"
      />

      <el-select
        v-model="row.source"
        size="small"
        :disabled="disabled"
        class="map-field-source"
        @change="onSourceChange"
      >
        <el-option
          v-for="source in SOURCES"
          :key="source"
          :value="source"
          :label="t(`channel.mapEditor.source.${source}`)"
        />
      </el-select>

      <div class="map-field-value">
        <el-select
          v-if="row.source === 'path' || row.source === 'map'"
          v-model="row.value"
          size="small"
          filterable
          allow-create
          default-first-option
          :disabled="disabled"
          :placeholder="t('channel.mapEditor.pathPlaceholder')"
          @change="emitChange"
        >
          <el-option v-for="key in allowedKeys" :key="key" :value="key" :label="key" />
        </el-select>

        <el-input
          v-else-if="row.source === 'const'"
          v-model="row.value"
          size="small"
          :disabled="disabled"
          :placeholder="t('channel.mapEditor.constPlaceholder')"
          @input="emitChange"
        />

        <div v-else class="template-value">
          <el-input
            ref="templateInput"
            v-model="row.value"
            size="small"
            :disabled="disabled"
            :placeholder="TEMPLATE_PLACEHOLDER"
            @input="emitChange"
          />
          <el-dropdown :disabled="disabled" trigger="click" @command="insertToken">
            <el-button text size="small" :disabled="disabled">
              {{ t('channel.mapEditor.insertField') }}
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item v-for="key in allowedKeys" :key="key" :command="key">
                  {{ key }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>

      <el-select
        v-model="row.type"
        size="small"
        :disabled="disabled"
        class="map-field-type"
        @change="emitChange"
      >
        <el-option
          v-for="type in FIELD_TYPES"
          :key="type"
          :value="type"
          :label="t(`channel.mapEditor.type.${type}`)"
        />
      </el-select>

      <div class="map-field-actions">
        <el-tooltip
          :content="row.expanded ? t('channel.mapEditor.collapse') : t('channel.mapEditor.advanced')"
          placement="top"
        >
          <el-button text size="small" :disabled="disabled" @click="toggleAdvanced">
            <el-icon><IconChevronDown v-if="!row.expanded" /><IconChevronUp v-else /></el-icon>
          </el-button>
        </el-tooltip>
        <el-tooltip :content="t('channel.mapEditor.duplicate')" placement="top">
          <el-button text size="small" :disabled="disabled" @click="$emit('duplicate')">
            <el-icon><IconCopy /></el-icon>
          </el-button>
        </el-tooltip>
        <el-tooltip :content="t('channel.mapEditor.remove')" placement="top">
          <el-button text size="small" :disabled="disabled" @click="$emit('remove')">
            <el-icon><IconTrash /></el-icon>
          </el-button>
        </el-tooltip>
      </div>
    </div>

    <div v-if="row.expanded" class="map-field-advanced">
      <div class="advanced-line">
        <span class="advanced-label">{{ t('channel.mapEditor.defaultValue') }}</span>
        <el-input
          v-model="row.default"
          size="small"
          :disabled="disabled"
          :placeholder="t('channel.mapEditor.defaultValueHint')"
          class="advanced-input"
          @input="emitChange"
        />
      </div>

      <div v-if="row.source === 'map'" class="map-pairs">
        <div class="map-pairs-head">
          <span>{{ t('channel.mapEditor.mapPairs') }}</span>
          <el-button text size="small" :disabled="disabled" @click="addPair">
            {{ t('channel.mapEditor.addMapPair') }}
          </el-button>
        </div>
        <p v-if="!row.mappingRows.length" class="map-pairs-empty">
          {{ t('channel.mapEditor.mapPairsEmpty') }}
        </p>
        <div v-for="(pair, index) in row.mappingRows" :key="index" class="map-pair-row">
          <el-input
            v-model="pair.from"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.mapFrom')"
            @input="emitChange"
          />
          <span class="map-pair-arrow">→</span>
          <el-input
            v-model="pair.to"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.mapTo')"
            @input="emitChange"
          />
          <el-button text size="small" :disabled="disabled" @click="removePair(index)">
            <el-icon><IconTrash /></el-icon>
          </el-button>
        </div>
      </div>

      <MappingTransformEditor
        :transforms="row.transform"
        :disabled="disabled"
        @change="emitChange"
      />
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  IconChevronDown,
  IconChevronUp,
  IconCopy,
  IconTrash,
} from '@tabler/icons-vue'
import MappingTransformEditor from './MappingTransformEditor.vue'
import {
  BATCH_DOC_KEYS,
  FIELD_TYPES,
  KEY_PLACEHOLDER,
  RECORD_DOC_KEYS,
  SOURCES,
  TEMPLATE_PLACEHOLDER,
  insertTemplateToken,
} from '@/utils/pushMapping'

const props = defineProps({
  row: {
    type: Object,
    required: true,
  },
  section: {
    type: String,
    default: 'fields',
  },
  disabled: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['change', 'remove', 'duplicate'])
const { t } = useI18n()

const templateInput = ref(null)

const allowedKeys = computed(() =>
  props.section === 'batchFields' ? BATCH_DOC_KEYS : RECORD_DOC_KEYS,
)

function emitChange() {
  emit('change')
}

function toggleAdvanced() {
  props.row.expanded = !props.row.expanded
}

function onSourceChange(source) {
  if (source === 'map' && props.row.mappingRows.length === 0) {
    props.row.mappingRows.push({ from: '', to: '' })
  }
  emitChange()
}

function addPair() {
  props.row.mappingRows.push({ from: '', to: '' })
  emitChange()
}

function removePair(index) {
  props.row.mappingRows.splice(index, 1)
  emitChange()
}

function insertToken(path) {
  const input = templateInput.value?.input || templateInput.value?.textarea
  const caret = input?.selectionStart ?? String(props.row.value || '').length
  const result = insertTemplateToken(props.row.value, caret, path)
  props.row.value = result.text
  emitChange()
  nextTick(() => {
    input?.focus()
    input?.setSelectionRange(result.caret, result.caret)
  })
}
</script>

<style scoped>
.map-field {
  padding: 8px 0;
  border-bottom: 1px dashed var(--line-soft, #e2e8f0);
}

.map-field:last-child {
  border-bottom: 0;
}

.map-field-main {
  display: grid;
  grid-template-columns: minmax(150px, 1.1fr) 132px minmax(200px, 1.8fr) 108px 96px;
  gap: 8px;
  align-items: center;
}

.map-field-value,
.template-value {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.map-field-value :deep(.el-select),
.map-field-value :deep(.el-input) {
  width: 100%;
}

.map-field-actions {
  display: flex;
  justify-content: flex-end;
  gap: 2px;
}

.map-field-actions :deep(.el-button) {
  margin-left: 0;
  padding: 4px;
}

.map-field-advanced {
  margin: 8px 0 2px;
  padding: 10px 12px;
  background: var(--surface-2, #f8fafc);
  border-radius: 10px;
}

.advanced-line {
  display: flex;
  align-items: center;
  gap: 10px;
}

.advanced-label {
  flex: none;
  width: 72px;
  font-size: 12.5px;
  color: var(--ink-3, #64748b);
}

.advanced-input {
  max-width: 280px;
}

.map-pairs {
  margin-top: 10px;
}

.map-pairs-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2, #475569);
}

.map-pairs-empty {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.map-pair-row {
  display: grid;
  grid-template-columns: minmax(140px, 1fr) 20px minmax(140px, 1fr) 32px;
  gap: 8px;
  align-items: center;
  margin-top: 6px;
}

.map-pair-arrow {
  text-align: center;
  color: var(--ink-4, #94a3b8);
}
</style>
