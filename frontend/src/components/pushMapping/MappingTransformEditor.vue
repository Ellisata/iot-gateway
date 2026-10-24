<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="transform-editor">
    <div class="transform-head">
      <span class="transform-title">{{ t('channel.mapEditor.transform') }}</span>
      <el-button text size="small" :disabled="disabled" @click="addTransform">
        {{ t('channel.mapEditor.addTransform') }}
      </el-button>
    </div>
    <p v-if="!transforms.length" class="transform-empty">
      {{ t('channel.mapEditor.transformEmpty') }}
    </p>
    <div v-for="(tr, index) in transforms" :key="tr.id || index" class="transform-row">
      <el-select
        :model-value="tr.op"
        size="small"
        :disabled="disabled"
        class="transform-op"
        @change="changeOp(tr, $event)"
      >
        <el-option
          v-for="op in TRANSFORM_OPS"
          :key="op"
          :value="op"
          :label="t(`channel.mapEditor.transformOp.${op}`)"
        />
      </el-select>

      <div class="transform-params">
        <template v-if="tr.op === 'scale'">
          <el-input
            v-model="tr.factor"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.factor')"
            @input="emitChange"
          />
          <el-input
            v-model="tr.offset"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.offset')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'round'">
          <el-input
            v-model="tr.digits"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.digits')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'default'">
          <el-input
            v-model="tr.value"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.value')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'bitfield'">
          <el-input
            v-model="tr.offset"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.offset')"
            @input="emitChange"
          />
          <el-input
            v-model="tr.length"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.length')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'regex'">
          <el-input
            v-model="tr.pattern"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.pattern')"
            @input="emitChange"
          />
          <el-input
            v-model="tr.group"
            size="small"
            :disabled="disabled"
            :placeholder="t('channel.mapEditor.transformParam.group')"
            @input="emitChange"
          />
        </template>
        <span v-else class="transform-none">—</span>
      </div>

      <el-tooltip :content="t('channel.mapEditor.removeTransform')" placement="top">
        <el-button text size="small" :disabled="disabled" @click="removeTransform(index)">
          <el-icon><IconTrash /></el-icon>
        </el-button>
      </el-tooltip>
    </div>
  </div>
</template>

<script setup>
import { useI18n } from 'vue-i18n'
import { IconTrash } from '@tabler/icons-vue'
import {
  TRANSFORM_OPS,
  applyTransformDefaults,
  createTransformRow,
} from '@/utils/pushMapping'

const props = defineProps({
  transforms: {
    type: Array,
    default: () => [],
  },
  disabled: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['change'])
const { t } = useI18n()

function emitChange() {
  emit('change')
}

function addTransform() {
  props.transforms.push(createTransformRow('scale'))
  emitChange()
}

function removeTransform(index) {
  props.transforms.splice(index, 1)
  emitChange()
}

function changeOp(tr, op) {
  tr.op = op
  applyTransformDefaults(tr)
  emitChange()
}
</script>

<style scoped>
.transform-editor {
  margin-top: 8px;
}

.transform-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 6px;
}

.transform-title {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2, #475569);
}

.transform-empty {
  margin: 0;
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}

.transform-row {
  display: grid;
  grid-template-columns: 132px 1fr 32px;
  gap: 8px;
  align-items: center;
  margin-top: 6px;
}

.transform-params {
  display: flex;
  gap: 8px;
  min-width: 0;
}

.transform-params :deep(.el-input) {
  width: 150px;
}

.transform-none {
  font-size: 12px;
  color: var(--ink-4, #94a3b8);
}
</style>
