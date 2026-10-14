<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-select
    :model-value="modelValue"
    :placeholder="resolvedPlaceholder"
    :disabled="disabled"
    :loading="loading"
    style="width: 280px"
    @update:model-value="handleChange"
  >
    <el-option
      v-for="item in typeOptions"
      :key="item.type"
      :label="labelOf(item)"
      :value="item.type"
    >
      <span>{{ labelOf(item) }}</span>
      <span class="text-xs text-gray-400 ml-2">{{ item.type }}</span>
    </el-option>
  </el-select>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getWebhookTypeList } from '@/api/modules/alarmWebhook'
import { webhookTypeLabel } from '@/utils/alarmWebhookType'

defineOptions({ name: 'AlarmWebhookTypeSelector' })

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

const resolvedPlaceholder = computed(
  () => props.placeholder || t('selector.selectAlarmWebhookType')
)

// 类型列表来自后端注册表而非硬编码：后端新增一种通知类型，
// 这里自动出现，不需要改前端（ChannelSelector 的硬编码就是反面教材）。
const typeOptions = ref([])
const loading = ref(false)

// 模块级缓存：弹框反复开关时不重复请求
let cachedTypes = null

async function loadTypes() {
  if (cachedTypes) {
    typeOptions.value = cachedTypes
    return
  }
  loading.value = true
  try {
    const res = await getWebhookTypeList()
    cachedTypes = Array.isArray(res) ? res : []
    typeOptions.value = cachedTypes
  } catch {
    // 失败时全局拦截器已提示，这里保持空列表
  } finally {
    loading.value = false
  }
}

/** 展示名走后端注册表 + 前端 i18n，缺翻译时回落后端 label */
function labelOf(item) {
  return webhookTypeLabel(item.type, item.label, t)
}

function handleChange(value) {
  emit('update:modelValue', value)
  // 同时抛出该类型的元信息，父组件据此决定提示文案等
  emit('select', typeOptions.value.find((item) => item.type === value) || null)
}

onMounted(loadTypes)
</script>
