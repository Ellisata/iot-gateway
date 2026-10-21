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
          {{ t('menu.systemSettings') }}
        </p>
        <h1 class="iot-title">{{ t('menu.gatewaySettings') }}</h1>
        <p class="iot-sub">{{ t('systemSettings.subtitle') }}</p>
      </div>
      <!-- 版本是这一页最常被核对的一项，放到页头右侧常驻 -->
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num version-num">{{ formData.firmwareVersion || '—' }}</span>
          <span class="iot-stat-label">{{ t('systemSettings.version') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-lead">
          <span class="iot-mark" aria-hidden="true">
            <el-icon><Settings /></el-icon>
          </span>
          <div class="iot-toolbar-copy">
            <p class="iot-toolbar-title">{{ t('systemSettings.identityTitle') }}</p>
            <p class="iot-toolbar-sub">{{ t('systemSettings.identityHint') }}</p>
          </div>
        </div>
      </div>

      <div v-loading="loading" class="settings-body">
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
            <div class="settings-readonly-row">
              <el-input
                :model-value="formData.id || '—'"
                readonly
                class="font-mono settings-readonly"
              />
              <el-tooltip :content="t('systemSettings.copyId')" placement="top">
                <el-button
                  :icon="CopyDocument"
                  :disabled="!formData.id"
                  :aria-label="t('systemSettings.copyId')"
                  @click="handleCopyId"
                />
              </el-tooltip>
            </div>
            <div class="iot-form-hint is-block">{{ t('systemSettings.idHint') }}</div>
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
            <el-input
              :model-value="formData.firmwareVersion || '—'"
              readonly
              class="font-mono settings-readonly"
              style="width: 100%"
            />
            <div class="iot-form-hint is-block">{{ t('systemSettings.versionHint') }}</div>
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
        </el-form>

        <div class="settings-actions">
          <el-button :disabled="submitting" @click="fetchGateway">{{ t('common.reset') }}</el-button>
          <el-button type="primary" :loading="submitting" @click="handleSubmit">
            {{ t('common.save') }}
          </el-button>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import {
  IconCopy as CopyDocument,
  IconSettings as Settings,
} from '@tabler/icons-vue'
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

<style scoped>
/* 版本号是标识而非金额，用等宽字体但收小字号，避免撑破页头 */
.version-num {
  font-family: var(--font-mono);
  font-size: 16px;
  letter-spacing: 0;
  color: var(--ink-2);
}

.settings-body {
  padding: 20px 20px 4px;
}

.settings-body :deep(.el-form-item) {
  margin-bottom: 18px;
}

.settings-body :deep(.el-form-item__label) {
  font-size: 13px;
  color: var(--ink-2);
}

.settings-body :deep(.el-input__wrapper) {
  min-height: 34px;
  border-radius: 9px;
  background: #fff;
  box-shadow: 0 0 0 1px var(--line) inset;
  transition: box-shadow 0.18s ease;
}

.settings-body :deep(.el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px #cbd8e6 inset;
}

.settings-body :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 1px var(--brand) inset, 0 0 0 3px rgba(37, 99, 235, 0.1);
}

.settings-body :deep(.el-textarea__inner) {
  border-radius: 9px;
  box-shadow: 0 0 0 1px var(--line) inset;
  font-size: 13px;
}

.settings-body :deep(.el-textarea__inner:focus) {
  box-shadow: 0 0 0 1px var(--brand) inset, 0 0 0 3px rgba(37, 99, 235, 0.1);
}

.settings-body :deep(.el-button) {
  height: 34px;
  padding: 0 14px;
  border-radius: 9px;
  font-weight: 500;
}

.settings-readonly-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.settings-readonly :deep(.el-input__inner) {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--ink-3);
}

/* 保存动作与表单之间用发丝线收尾，按钮固定在右下角 */
.settings-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 6px;
  padding: 14px 0 16px;
  border-top: 1px solid var(--line-soft);
}
</style>
