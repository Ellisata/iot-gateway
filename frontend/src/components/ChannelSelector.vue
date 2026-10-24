<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-select
    class="selector-field"
    :model-value="modelValue"
    :placeholder="resolvedPlaceholder"
    :disabled="disabled"
    :loading="loading"
    @update:model-value="handleChange"
  >
    <el-option
      v-for="item in options"
      :key="item.type"
      :label="item.label || item.type"
      :value="item.type"
    />
  </el-select>
</template>

<script setup>
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { usePushChannelTypes } from '@/composables/usePushChannelTypes'

defineOptions({ name: 'ChannelSelector' })

const props = defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  placeholder: {
    type: String,
    default: '',
  },
  disabled: {
    type: Boolean,
    default: false,
  },
})

const { t } = useI18n()

const emit = defineEmits(['update:modelValue', 'select'])

const { types, loading, load } = usePushChannelTypes()

// 后端不可达时的兜底，至少不让选择器空白。
const fallback = [
  { type: 'mqtt', label: 'MQTT' },
  { type: 'http', label: 'HTTP / Webhook' },
  { type: 'tdengine-v3', label: 'TDengine v3' },
  { type: 'influxdb-v3', label: 'InfluxDB v3' },
]

const options = computed(() => (types.value.length ? types.value : fallback))

const resolvedPlaceholder = computed(() => props.placeholder || t('selector.selectChannel'))

onMounted(() => {
  load().catch(() => {
    // 失败时保留兜底列表；具体错误由全局拦截器提示。
  })
})

function handleChange(value) {
  emit('update:modelValue', value)
  // 兼容旧事件契约：name 仍是类型；新增 type 字段供通道页使用。
  emit('select', { name: value, type: value, id: '' })
}
</script>

<style scoped>
/* 与 ProtocolSelector 的触发器同宽同高：两者都出现在设备/通道配置表单里 */
.selector-field {
  width: 280px;
  max-width: 100%;
}

.selector-field :deep(.el-select__wrapper) {
  min-height: 34px;
  border-radius: 9px;
}
</style>
