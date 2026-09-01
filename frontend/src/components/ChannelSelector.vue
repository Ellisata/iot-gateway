<template>
  <el-select
    :model-value="modelValue"
    :placeholder="resolvedPlaceholder"
    :disabled="disabled"
    style="width: 280px"
    @update:model-value="handleChange"
  >
    <el-option
      v-for="channel in channelOptions"
      :key="channel"
      :label="channel"
      :value="channel"
    />
  </el-select>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

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

// 未传入 placeholder 时使用当前语言的默认文案
const resolvedPlaceholder = computed(() => props.placeholder || t('selector.selectChannel'))

// 可选通道（固定协议）
const channelOptions = ['mqtt', 'tdengine-v3', 'influxdb-v3']

function handleChange(value) {
  emit('update:modelValue', value)
  // 兼容旧事件契约：返回 { name, id }，协议通道无后端 id
  emit('select', { name: value, id: '' })
}
</script>
