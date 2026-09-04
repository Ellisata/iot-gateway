<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-dialog v-model="visible" :title="t('excelImport.resultTitle')" width="640px" :close-on-click-modal="false">
    <div class="flex items-center gap-6 mb-4 text-sm">
      <span>{{ t('excelImport.total', { total: result.total }) }}</span>
      <span class="text-green-600">{{ t('excelImport.success', { n: result.success }) }}</span>
      <span class="text-red-600">{{ t('excelImport.failed', { n: result.failed }) }}</span>
    </div>
    <el-table
      v-if="result.failed > 0"
      :data="result.errors"
      max-height="360"
      border
      stripe
    >
      <el-table-column prop="row" :label="t('excelImport.row')" width="90" align="center" />
      <el-table-column prop="name" :label="t('excelImport.name')" min-width="140" show-overflow-tooltip />
      <el-table-column prop="reason" :label="t('excelImport.reason')" min-width="220" show-overflow-tooltip />
    </el-table>
    <el-empty v-else :description="t('excelImport.allSuccess')" />
    <template #footer>
      <el-button type="primary" @click="visible = false">{{ t('common.confirm') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const visible = defineModel('visible', { type: Boolean })
defineProps({
  result: { type: Object, required: true },
})
</script>
