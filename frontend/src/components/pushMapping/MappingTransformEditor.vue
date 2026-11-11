<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="transform-editor">
    <div class="transform-head" :class="{ 'is-titleless': !showTitle }">
      <span v-if="showTitle" class="transform-title">{{ L('transform') }}</span>
      <el-button text size="small" :disabled="disabled" @click="addTransform">
        {{ L('addTransform') }}
      </el-button>
    </div>
    <p v-if="!transforms.length" class="transform-empty">
      {{ L('transformEmpty') }}
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
          v-for="op in ops"
          :key="op"
          :value="op"
          :label="L(`transformOp.${op}`)"
        />
      </el-select>

      <div class="transform-params">
        <template v-if="tr.op === 'scale'">
          <el-input
            v-model="tr.factor"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.factor')"
            @input="emitChange"
          />
          <el-input
            v-model="tr.offset"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.offset')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'round'">
          <el-input
            v-model="tr.digits"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.digits')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'default'">
          <el-input
            v-model="tr.value"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.value')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'bitfield'">
          <el-input
            v-model="tr.offset"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.offset')"
            @input="emitChange"
          />
          <el-input
            v-model="tr.length"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.length')"
            @input="emitChange"
          />
        </template>
        <template v-else-if="tr.op === 'regex'">
          <el-input
            v-model="tr.pattern"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.pattern')"
            @input="emitChange"
          />
          <el-input
            v-model="tr.group"
            size="small"
            :disabled="disabled"
            :placeholder="L('transformParam.group')"
            @input="emitChange"
          />
        </template>
        <span v-else class="transform-none">—</span>
      </div>

      <el-tooltip :content="L('removeTransform')" placement="top">
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
  // 可选算子；点位工程量变换只传归一小集合，push mapping 用默认全量。
  ops: {
    type: Array,
    default: () => TRANSFORM_OPS,
  },
  // i18n 命名空间；默认沿用 push mapping，点位页可指向自己的文案。
  labelPrefix: {
    type: String,
    default: 'channel.mapEditor',
  },
  // 点位页外层已有 form-item 标签，这里隐藏内部的「变换」标题。
  showTitle: {
    type: Boolean,
    default: true,
  },
})

const emit = defineEmits(['change'])
const { t } = useI18n()

function L(key) {
  return t(`${props.labelPrefix}.${key}`)
}

function emitChange() {
  emit('change')
}

function addTransform() {
  props.transforms.push(createTransformRow(props.ops[0] || 'scale'))
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

.transform-head.is-titleless {
  justify-content: flex-end;
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
