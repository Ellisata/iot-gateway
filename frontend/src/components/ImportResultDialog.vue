<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-dialog
    v-model="visible"
    class="iot-dialog"
    :title="t('excelImport.resultTitle')"
    width="640px"
    :close-on-click-modal="false"
  >
    <!-- 三个读数并列：总数是背景，成功/失败才是用户要看的结论 -->
    <div class="import-summary">
      <div class="summary-item">
        <span class="summary-num">{{ result.total }}</span>
        <span class="summary-label">{{ t('excelImport.totalLabel') }}</span>
      </div>
      <div class="summary-item is-success">
        <span class="summary-num">{{ result.success }}</span>
        <span class="summary-label">{{ t('excelImport.successLabel') }}</span>
      </div>
      <div class="summary-item" :class="result.failed > 0 ? 'is-danger' : 'is-muted'">
        <span class="summary-num">{{ result.failed }}</span>
        <span class="summary-label">{{ t('excelImport.failedLabel') }}</span>
      </div>
    </div>

    <template v-if="result.failed > 0">
      <p class="import-lead">{{ t('excelImport.errorLead') }}</p>
      <div class="import-errors">
        <el-table
          :data="result.errors"
          class="iot-table is-compact"
          max-height="360"
        >
          <el-table-column prop="row" :label="t('excelImport.row')" width="90" align="center" class-name="iot-col-num" />
          <el-table-column prop="name" :label="t('excelImport.name')" min-width="140" show-overflow-tooltip />
          <el-table-column prop="reason" :label="t('excelImport.reason')" min-width="220" show-overflow-tooltip class-name="iot-col-muted" />
        </el-table>
      </div>
    </template>

    <!-- 全部成功时不再给一张空表，用一条成功通知收尾 -->
    <div v-else class="iot-notice is-success is-inline import-ok">
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><Check /></el-icon>
      </span>
      <p class="iot-notice-text">{{ t('excelImport.allSuccess') }}</p>
    </div>

    <template #footer>
      <el-button type="primary" @click="visible = false">{{ t('common.confirm') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { useI18n } from 'vue-i18n'
import { IconCheck as Check } from '@tabler/icons-vue'

const { t } = useI18n()

const visible = defineModel('visible', { type: Boolean })
defineProps({
  result: { type: Object, required: true },
})
</script>

<style scoped>
/* 结果读数条：三格发丝线，数字承担结论，标签压低存在感 */
.import-summary {
  display: flex;
  gap: 1px;
  margin-bottom: 18px;
  overflow: hidden;
  background: var(--line-soft);
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

.summary-item {
  flex: 1;
  min-width: 0;
  padding: 12px 16px;
  background: #fff;
}

.summary-num {
  display: block;
  font-size: 20px;
  font-weight: 650;
  line-height: 1.2;
  letter-spacing: -0.02em;
  color: var(--ink);
  font-variant-numeric: tabular-nums;
}

.summary-label {
  display: block;
  margin-top: 2px;
  font-size: 12px;
  color: var(--ink-4);
}

.summary-item.is-success .summary-num {
  color: #047857;
}

.summary-item.is-danger .summary-num {
  color: #be123c;
}

.summary-item.is-muted .summary-num {
  color: var(--ink-4);
}

.import-lead {
  margin: 0 0 10px;
  font-size: 12.5px;
  line-height: 1.65;
  color: var(--ink-3);
}

.import-errors {
  overflow: hidden;
  border: 1px solid var(--line-soft);
  border-radius: 12px;
}

.iot-notice.import-ok {
  margin: 0;
}
</style>
