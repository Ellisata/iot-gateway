<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.secret') }}
        </p>
        <h1 class="iot-title">API Key</h1>
        <p class="iot-sub">{{ t('apiKey.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('apiKey.totalUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-lead">
          <span class="iot-mark" aria-hidden="true">
            <el-icon><Key /></el-icon>
          </span>
          <div class="iot-toolbar-copy">
            <p class="iot-toolbar-title">{{ t('apiKey.listTitle') }}</p>
            <p class="iot-toolbar-sub">{{ t('apiKey.hint') }}</p>
          </div>
        </div>
        <div class="iot-toolbar-actions">
          <el-button type="primary" :icon="Plus" @click="handleAdd">{{ t('apiKey.add') }}</el-button>
        </div>
      </div>

      <el-table class="iot-table" :data="tableData" v-loading="loading" style="width: 100%">
        <el-table-column type="index" :label="t('common.index')" width="68" align="center" />

        <el-table-column :label="t('common.name')" min-width="190">
          <template #default="{ row }">
            <div class="iot-name-cell">
              <span class="iot-name-mark" aria-hidden="true">
                <el-icon><Key /></el-icon>
              </span>
              <span class="iot-name-text" :title="row.name">{{ row.name }}</span>
              <!-- 默认密钥由启动播种创建、随局域网广播发布，不可删除或改名 -->
              <span v-if="row.isDefault" class="iot-pill is-blue">{{ t('apiKey.defaultTag') }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('apiKey.keyContent')" min-width="280">
          <template #default="{ row }">
            <div class="apikey-cell">
              <span class="apikey-text">{{ maskKey(row.key) }}</span>
              <el-tooltip :content="t('apiKey.copyKey')" placement="top">
                <button
                  type="button"
                  class="iot-copy-btn"
                  :aria-label="t('apiKey.copyKey')"
                  @click="handleCopyKey(row)"
                >
                  <el-icon><CopyDocument /></el-icon>
                </button>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="createdAt"
          :label="t('common.createdAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column
          prop="updatedAt"
          :label="t('common.updatedAt')"
          width="160"
          class-name="iot-col-time"
        />
        <el-table-column :label="t('common.action')" width="110" align="center" fixed="right">
          <template #default="{ row }">
            <div class="iot-row-actions">
              <!-- 禁用的按钮不派发鼠标事件，必须套一层 span，否则 tooltip 不弹（也就说不出为什么禁用） -->
              <el-tooltip
                :content="row.isDefault ? t('apiKey.builtinLocked') : t('apiKey.rename')"
                placement="top"
              >
                <span>
                  <el-button
                    class="iot-row-btn"
                    type="primary"
                    link
                    size="small"
                    :icon="EditPen"
                    :disabled="row.isDefault"
                    :aria-label="t('apiKey.rename')"
                    @click="handleEdit(row)"
                  />
                </span>
              </el-tooltip>
              <el-tooltip
                :content="row.isDefault ? t('apiKey.builtinLocked') : t('common.delete')"
                placement="top"
              >
                <span>
                  <el-button
                    class="iot-row-btn"
                    type="danger"
                    link
                    size="small"
                    :icon="Delete"
                    :disabled="row.isDefault"
                    :aria-label="t('common.delete')"
                    @click="handleDelete(row)"
                  />
                </span>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <template #empty>
          <div class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Key /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('apiKey.emptyTitle') }}</p>
            <p class="iot-empty-hint">{{ t('apiKey.emptyHint') }}</p>
            <el-button type="primary" :icon="Plus" @click="handleAdd">
              {{ t('apiKey.add') }}
            </el-button>
          </div>
        </template>
      </el-table>
    </section>

    <!-- 新增 / 修改名称 对话框 -->
    <el-dialog
      v-model="dialogVisible"
      class="iot-dialog"
      :title="isEdit ? t('apiKey.editTitle') : t('apiKey.addTitle')"
      width="520px"
      :close-on-click-modal="false"
      @close="handleDialogClose"
    >
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="90px"
        label-position="right"
        status-icon
      >
        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="formData.name"
            :placeholder="t('apiKey.namePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" @click="handleSubmit">{{ t('common.confirm') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconCopy as CopyDocument,
  IconKey as Key,
  IconPencil as EditPen,
  IconPlus as Plus,
  IconTrash as Delete,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
import {
  getSecretList,
  addSecret,
  updateSecretName,
  deleteSecret,
} from '@/api/modules/openApiSecret'

// ---------- 表格数据 ----------
const loading = ref(false)
const tableData = ref([])

// 首屏还没拿到数时显示占位符而不是 0：0 个密钥会被当成"开放接口还没配访问凭据"
const displayTotal = computed(() =>
  loading.value && tableData.value.length === 0 ? '—' : tableData.value.length
)

/** 密钥脱敏展示：保留前 6 位与后 4 位 */
function maskKey(key) {
  if (!key) return ''
  if (key.length <= 10) return key
  return `${key.slice(0, 6)}••••••${key.slice(-4)}`
}

// ---------- 获取列表 ----------
async function fetchList() {
  loading.value = true
  try {
    const res = await getSecretList()
    tableData.value = Array.isArray(res) ? res : res?.records ?? []
  } catch (err) {
    console.error('获取密钥列表失败', err)
    ElMessage.error(t('apiKey.fetchListFailed'))
  } finally {
    loading.value = false
  }
}

// ---------- 对话框状态 ----------
const dialogVisible = ref(false)
const isEdit = ref(false)
const submitting = ref(false)
const formRef = ref(null)

// ---------- 表单数据 ----------
const formData = reactive({
  id: '',
  name: '',
})

// ---------- 表单校验规则 ----------
const formRules = {
  name: [
    { required: true, message: () => t('apiKey.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('apiKey.nameLength'), trigger: 'blur' },
  ],
}

// ---------- 操作 ----------
function handleAdd() {
  isEdit.value = false
  formData.id = ''
  formData.name = ''
  dialogVisible.value = true
}

function handleEdit(row) {
  isEdit.value = true
  formData.id = row.id
  formData.name = row.name
  dialogVisible.value = true
}

async function handleDelete(row) {
  try {
    await ElMessageBox.confirm(t('apiKey.deleteConfirm', { name: row.name }), t('apiKey.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
    await deleteSecret(row.id)
    ElMessage.success(t('apiKey.deleteSuccess'))
    fetchList()
  } catch {
    // 用户取消或删除失败，不做处理
  }
}

function handleDialogClose() {
  formRef.value?.resetFields()
}

async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }
  submitting.value = true
  try {
    const payload = {
      name: formData.name.trim(),
    }
    if (isEdit.value) {
      payload.id = formData.id
      await updateSecretName(payload)
      ElMessage.success(t('apiKey.editSuccess'))
    } else {
      await addSecret(payload)
      ElMessage.success(t('apiKey.addSuccess'))
    }
    dialogVisible.value = false
    fetchList()
  } catch (err) {

  } finally {
    submitting.value = false
  }
}

// ---------- 复制密钥（完整明文） ----------
function handleCopyKey(row) {
  if (!row.key) return
  copyToClipboard(row.key).then(() => {
    ElMessage.success(t('apiKey.keyCopied'))
  }).catch(() => {
    ElMessage.error(t('apiKey.copyFailed'))
  })
}

/**
 * 复制文本到剪贴板
 * 现场为纯 HTTP + IP 部署时 navigator.clipboard 不可用（仅安全上下文），降级 execCommand
 * @param {string} text - 待复制文本
 * @returns {Promise<void>}
 */
function copyToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text)
  }
  return new Promise((resolve, reject) => {
    try {
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
      resolve()
    } catch (e) {
      reject(e)
    }
  })
}

// ---------- 初始化 ----------
onMounted(() => {
  fetchList()
})
</script>

<style scoped>
.apikey-cell {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.apikey-text {
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--ink-3);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
