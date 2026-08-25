<template>
  <el-select
    :model-value="modelValue"
    :placeholder="placeholder"
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
defineOptions({ name: 'ChannelSelector' })

defineProps({
  modelValue: {
    type: String,
    default: '',
  },
  placeholder: {
    type: String,
    default: '请选择通道',
  },
  disabled: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['update:modelValue', 'select'])

// 可选通道（固定协议）
const channelOptions = ['mqtt', 'tdengine-v3', 'influxdb-v3']

function handleChange(value) {
  emit('update:modelValue', value)
  // 兼容旧事件契约：返回 { name, id }，协议通道无后端 id
  emit('select', { name: value, id: '' })
}
</script>
