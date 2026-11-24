<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.systemSettings') }}
          </p>
        </div>
        <div class="iot-title-row">
          <h1 class="iot-title">{{ t('backup.title') }}</h1>
          <span class="iot-head-sub">{{ t('backup.subtitle') }}</span>
        </div>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ bundles.length }}</span>
          <span class="iot-stat-label">{{ t('backup.countUnit') }}</span>
        </div>
      </div>
    </header>

    <div v-if="status && !status.enabled" class="iot-notice is-warning iot-enter">
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><IconAlertTriangle /></el-icon>
      </span>
      <div>
        <p class="iot-notice-text">{{ t('backup.disabled') }}</p>
      </div>
    </div>

    <!-- 待恢复任务：上传/暂存后重启才生效，因此必须常驻显示 -->
    <div v-if="status && status.pending" class="iot-notice is-info iot-enter">
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><IconClock /></el-icon>
      </span>
      <div class="backup-pending">
        <p class="iot-notice-text">
          <strong>{{ t('backup.pendingTitle') }}</strong>
          {{ t('backup.pendingText', {
            scope: status.pending.scope,
            identity: identityLabel(status.pending.identity),
            user: status.pending.uploadedBy || '—',
            time: status.pending.createdAt,
          }) }}
        </p>
        <el-button size="small" type="danger" plain :loading="busy" @click="handleCancelPending">
          {{ t('backup.cancelPending') }}
        </el-button>
      </div>
    </div>

    <section v-if="status" class="iot-panel iot-enter" style="animation-delay: 40ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-lead">
          <span class="iot-mark" aria-hidden="true">
            <el-icon><IconDatabase /></el-icon>
          </span>
          <div class="iot-toolbar-copy">
            <p class="iot-toolbar-title">{{ t('backup.scheduleTitle') }}</p>
            <p class="iot-toolbar-sub">
              {{ t('backup.scheduleText', {
                interval: Math.round(status.intervalSeconds / 60),
                debounce: status.debounceSeconds,
                dir: status.dir,
                keep: status.keep,
                size: status.maxTotalMB,
              }) }}
            </p>
          </div>
        </div>
      </div>
      <p class="backup-security">{{ t('backup.localOnly') }}</p>
    </section>

    <section class="iot-panel iot-enter" style="animation-delay: 80ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-lead">
          <span class="iot-mark" aria-hidden="true">
            <el-icon><IconArchive /></el-icon>
          </span>
          <div class="iot-toolbar-copy">
            <p class="iot-toolbar-title">{{ t('backup.createTitle') }}</p>
            <p class="iot-toolbar-sub">{{ t('backup.createHint') }}</p>
          </div>
        </div>
        <div class="backup-actions">
          <el-select v-model="createScope" size="default" class="backup-scope">
            <el-option :label="t('backup.scopeFull')" value="full" />
            <el-option :label="t('backup.scopeConfig')" value="config" />
          </el-select>
          <el-button type="primary" :icon="IconDatabase" :loading="busy" :disabled="!status?.enabled" @click="handleCreate">
            {{ t('backup.create') }}
          </el-button>
          <el-select v-model="restoreIdentity" size="default" class="backup-identity">
            <el-option :label="t('backup.takeover')" value="takeover" />
            <el-option :label="t('backup.clone')" value="clone" />
          </el-select>
          <el-button :icon="IconUpload" :loading="busy" :disabled="!status?.enabled" @click="pickFile">
            {{ t('backup.upload') }}
          </el-button>
          <el-button :icon="IconRefresh" :loading="loading" @click="loadAll">
            {{ t('backup.refresh') }}
          </el-button>
          <input ref="fileInput" type="file" accept=".igbak" class="backup-file" @change="handleUpload" />
        </div>
      </div>

      <el-table v-loading="loading" :data="bundles" class="backup-table">
        <el-table-column prop="name" :label="t('backup.colName')" min-width="320">
          <template #default="{ row }">
            <span class="font-mono backup-name">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('backup.colScope')" width="110">
          <template #default="{ row }">
            <span class="iot-pill" :class="row.scope === 'full' ? 'is-amber' : 'is-green'">
              {{ row.scope }}
            </span>
          </template>
        </el-table-column>
        <el-table-column :label="t('backup.colSize')" width="110">
          <template #default="{ row }">{{ formatSize(row.size) }}</template>
        </el-table-column>
        <el-table-column prop="modTime" :label="t('backup.colTime')" width="190" />
        <el-table-column :label="t('backup.colActions')" width="260" align="right">
          <template #default="{ row }">
            <el-button link type="primary" :icon="IconDownload" @click="handleDownload(row)">
              {{ t('backup.download') }}
            </el-button>
            <el-button link type="primary" :icon="IconHistory" :disabled="!status?.enabled" @click="handleStage(row)">
              {{ t('backup.stage') }}
            </el-button>
            <el-button link type="danger" :icon="IconTrash" @click="handleDelete(row)">
              {{ t('backup.delete') }}
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <p class="backup-empty">{{ t('backup.empty') }}</p>
        </template>
      </el-table>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconAlertTriangle,
  IconArchive,
  IconClock,
  IconDatabase,
  IconDownload,
  IconHistory,
  IconRefresh,
  IconTrash,
  IconUpload,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'
import {
  cancelPendingBackup,
  createBackup,
  deleteBackup,
  downloadBackup,
  getBackupStatus,
  listBackups,
  stageBackup,
  uploadBackup,
} from '@/api/modules/backup'

const { t } = useI18n()

const loading = ref(false)
const busy = ref(false)
const status = ref(null)
const bundles = ref([])
const createScope = ref('full')
const restoreIdentity = ref('takeover')
const fileInput = ref(null)

function identityLabel(identity) {
  return identity === 'clone' ? t('backup.clone') : t('backup.takeover')
}

function formatSize(size) {
  if (!size) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = size
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

function escapeHtml(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
}

async function loadAll() {
  loading.value = true
  try {
    const [st, list] = await Promise.all([getBackupStatus(), listBackups()])
    status.value = st
    bundles.value = list || []
    if (st?.defaultScope) createScope.value = st.defaultScope
  } catch (err) {
    ElMessage.error(err?.message || t('backup.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function handleCreate() {
  if (busy.value) return
  busy.value = true
  try {
    await createBackup({ scope: createScope.value })
    ElMessage.success(t('backup.createSuccess'))
    await loadAll()
  } finally {
    busy.value = false
  }
}

function pickFile() {
  fileInput.value?.click()
}

async function handleUpload(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return
  busy.value = true
  try {
    const form = new FormData()
    form.append('file', file)
    form.append('identity', restoreIdentity.value)
    await uploadBackup(form)
    ElMessage.success(t('backup.uploadSuccess'))
    await loadAll()
  } finally {
    busy.value = false
  }
}

async function handleDownload(row) {
  try {
    const blob = await downloadBackup(row.name)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = row.name
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    URL.revokeObjectURL(url)
    ElMessage.success(t('backup.downloadSuccess'))
  } catch (err) {
    ElMessage.error(err?.message || t('backup.loadFailed'))
  }
}

async function handleStage(row) {
  const message = t('backup.stageConfirm', {
    name: `<strong>${escapeHtml(row.name)}</strong>`,
    identity: escapeHtml(identityLabel(restoreIdentity.value)),
  })
  try {
    await ElMessageBox.confirm(message, t('backup.stageConfirmTitle'), {
      type: 'warning',
      dangerouslyUseHTMLString: true,
      confirmButtonText: t('backup.stage'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  busy.value = true
  try {
    await stageBackup({ name: row.name, identity: restoreIdentity.value })
    ElMessage.success(t('backup.stageSuccess'))
    await loadAll()
  } finally {
    busy.value = false
  }
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(
      t('backup.deleteConfirm', { name: row.name }),
      t('backup.deleteConfirmTitle'),
      { type: 'warning', confirmButtonText: t('common.delete'), cancelButtonText: t('common.cancel') }
    )
  } catch {
    return
  }
  busy.value = true
  try {
    await deleteBackup(row.name)
    ElMessage.success(t('backup.deleteSuccess'))
    await loadAll()
  } finally {
    busy.value = false
  }
}

async function handleCancelPending() {
  try {
    await ElMessageBox.confirm(t('backup.cancelConfirm'), t('backup.pendingTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  busy.value = true
  try {
    await cancelPendingBackup()
    ElMessage.success(t('backup.cancelSuccess'))
    await loadAll()
  } finally {
    busy.value = false
  }
}

onMounted(loadAll)
</script>

<style scoped>
.backup-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.backup-scope {
  width: 150px;
}

.backup-identity {
  width: 190px;
}

.backup-file {
  display: none;
}

.backup-name {
  word-break: break-all;
}

.backup-security {
  margin: 0;
  padding: 0 20px 18px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.7;
}

.backup-pending {
  display: flex;
  align-items: center;
  gap: 16px;
  flex: 1;
  flex-wrap: wrap;
}

.backup-empty {
  padding: 24px 0;
  color: var(--el-text-color-secondary);
}
</style>
