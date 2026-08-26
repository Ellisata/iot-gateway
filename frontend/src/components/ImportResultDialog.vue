<template>
  <el-dialog v-model="visible" title="导入结果" width="640px" :close-on-click-modal="false">
    <div class="flex items-center gap-6 mb-4 text-sm">
      <span>共 <span class="font-semibold">{{ result.total }}</span> 条</span>
      <span class="text-green-600">成功 {{ result.success }} 条</span>
      <span class="text-red-600">失败 {{ result.failed }} 条</span>
    </div>
    <el-table
      v-if="result.failed > 0"
      :data="result.errors"
      max-height="360"
      border
      stripe
    >
      <el-table-column prop="row" label="Excel行号" width="90" align="center" />
      <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
      <el-table-column prop="reason" label="失败原因" min-width="220" show-overflow-tooltip />
    </el-table>
    <el-empty v-else description="全部导入成功" />
    <template #footer>
      <el-button type="primary" @click="visible = false">确定</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
const visible = defineModel('visible', { type: Boolean })
defineProps({
  result: { type: Object, required: true },
})
</script>
