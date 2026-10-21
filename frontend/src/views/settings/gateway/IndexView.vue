<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-6">{{ t('menu.gatewaySettings') }}</h1>

    <el-card shadow="never" v-loading="loading">
      <el-form
        ref="formRef"
        :model="formData"
        :rules="formRules"
        label-width="100px"
        label-position="right"
        status-icon
      >
        <!-- 网关唯一标识：只读。数据库被克隆时它会一起被克隆，展示出来才有机会被发现 -->
        <el-form-item :label="t('systemSettings.id')" prop="id">
          <div class="flex items-center gap-2 w-full">
            <el-input :model-value="formData.id || '—'" readonly class="font-mono" />
            <el-tooltip :content="t('systemSettings.copyId')" placement="top">
              <el-button :icon="CopyDocument" :disabled="!formData.id" @click="handleCopyId" />
            </el-tooltip>
          </div>
          <div class="text-gray-400 text-xs leading-5 mt-1">{{ t('systemSettings.idHint') }}</div>
        </el-form-item>

        <el-form-item :label="t('common.name')" prop="name">
          <el-input
            v-model="formData.name"
            :placeholder="t('systemSettings.namePlaceholder')"
            maxlength="100"
            show-word-limit
          />
        </el-form-item>

        <!-- 版本只读：它由 default.yaml 的 gateway.version 在首次启动时写入，
             让运维手改会把「版本」变成一句无法核实的话 -->
        <el-form-item :label="t('systemSettings.version')" prop="firmwareVersion">
          <el-input :model-value="formData.firmwareVersion || '—'" readonly class="font-mono" />
          <div class="text-gray-400 text-xs leading-5 mt-1">{{ t('systemSettings.versionHint') }}</div>
        </el-form-item>

        <el-form-item :label="t('common.description')" prop="description">
          <el-input
            v-model="formData.description"
            type="textarea"
            :rows="3"
            :placeholder="t('systemSettings.descriptionPlaceholder')"
            maxlength="500"
            show-word-limit
          />
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            {{ t('common.save') }}
          </el-button>
          <el-button :disabled="submitting" @click="fetchGateway">{{ t('common.reset') }}</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { CopyDocument } from '@element-plus/icons-vue'
import { useI18n } from 'vue-i18n'

import { getGateway, updateGateway } from '@/api/modules/gateway'

const { t } = useI18n()

// ---------- 数据 ----------
const loading = ref(false)
const submitting = ref(false)
const formRef = ref(null)

const formData = reactive({
  id: '',
  name: '',
  firmwareVersion: '',
  description: '',
})

// ---------- 表单校验规则 ----------
// message 写成惰性函数，切换语言时才会重新翻译（同 api-key 页）
const formRules = {
  name: [
    { required: true, message: () => t('systemSettings.nameRequired'), trigger: 'blur' },
    { min: 1, max: 100, message: () => t('systemSettings.nameLength'), trigger: 'blur' },
  ],
  description: [
    { max: 500, message: () => t('systemSettings.descriptionLength'), trigger: 'blur' },
  ],
}

// ---------- 读取 ----------
async function fetchGateway() {
  loading.value = true
  try {
    const res = await getGateway()
    formData.id = res?.id ?? ''
    formData.name = res?.name ?? ''
    formData.firmwareVersion = res?.firmwareVersion ?? ''
    formData.description = res?.description ?? ''
    formRef.value?.clearValidate()
  } catch (err) {
    console.error('获取网关身份失败', err)
    //ElMessage.error(t('systemSettings.fetchFailed'))
  } finally {
    loading.value = false
  }
}

// ---------- 保存 ----------
async function handleSubmit() {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

  submitting.value = true
  try {
    await updateGateway({
      name: formData.name.trim(),
      description: formData.description,
    })
    ElMessage.success(t('systemSettings.saveSuccess'))
    // 重新拉取，用服务端真值回填（id / version 等只读字段以库内为准）
    await fetchGateway()
  } catch (err) {
    // 拦截器已经弹过错误提示，这里不再重复
  } finally {
    submitting.value = false
  }
}

// ---------- 复制网关 ID ----------
function handleCopyId() {
  if (!formData.id) return
  copyToClipboard(formData.id)
    .then(() => ElMessage.success(t('systemSettings.idCopied')))
    .catch(() => ElMessage.error(t('systemSettings.copyFailed')))
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
  fetchGateway()
})
</script>
