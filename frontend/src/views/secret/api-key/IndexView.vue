<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">API Key</h1>

    <!-- 工具栏 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-center justify-between">
        <span class="text-gray-500 text-sm">{{ t('apiKey.hint') }}</span>
        <el-button type="primary" @click="handleAdd">{{ t('apiKey.add') }}</el-button>
      </div>
    </el-card>

    <!-- 数据表格 -->
    <el-card shadow="never">
      <el-table :data="tableData" v-loading="loading" stripe style="width: 100%" border>
        <el-table-column type="index" :label="t('common.index')" width="70" align="center" />
        <el-table-column prop="name" :label="t('common.name')" min-width="140" />
        <el-table-column :label="t('apiKey.keyContent')" min-width="260">
          <template #default="{ row }">
            <div class="flex items-center gap-2">
              <span class="font-mono text-sm">{{ maskKey(row.key) }}</span>
              <el-tooltip :content="t('apiKey.copyKey')" placement="top">
                <el-button type="primary" link size="small" :icon="CopyDocument" @click="handleCopyKey(row)" />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="createdAt" :label="t('common.createdAt')" width="180" align="center" />
        <el-table-column prop="updatedAt" :label="t('common.updatedAt')" width="180" align="center" />
        <el-table-column :label="t('common.action')" width="130" align="center" fixed="right">
          <template #default="{ row }">
            <div class="flex items-center justify-center gap-2">
              <el-tooltip :content="t('apiKey.rename')" placement="top">
                <el-button type="primary" link size="small" :icon="EditPen" @click="handleEdit(row)" />
              </el-tooltip>
              <el-tooltip :content="t('common.delete')" placement="top">
                <el-button type="danger" link size="small" :icon="Delete" @click="handleDelete(row)" />
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新增 / 修改名称 对话框 -->
    <el-dialog
      v-model="dialogVisible"
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
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, Delete, EditPen } from '@element-plus/icons-vue'
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
    ElMessage.success('删除成功')
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
