<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="p-6">
    <h1 class="text-2xl font-bold text-gray-800 mb-2">{{ t('runtimeConfig.title') }}</h1>
    <p class="text-sm text-gray-400 leading-6 mb-6 max-w-4xl">{{ t('runtimeConfig.subtitle') }}</p>

    <!-- 默认值与覆盖值的语义说明。放在最上面，避免用户把「默认值」误当成「当前生效值」 -->
    <el-card shadow="never" class="mb-4">
      <div class="flex items-start gap-3">
        <div class="w-1 self-stretch rounded-full bg-blue-200 shrink-0" />
        <div class="min-w-0">
          <div class="text-sm font-medium text-gray-700 mb-1">{{ t('runtimeConfig.introTitle') }}</div>
          <p class="text-xs text-gray-400 leading-6">{{ t('runtimeConfig.intro') }}</p>
        </div>
      </div>
    </el-card>

    <div v-loading="loading">
      <el-card
        v-for="group in groups"
        :key="group.key"
        shadow="never"
        class="mb-4 last:mb-0"
      >
        <template #header>
          <span class="text-sm font-semibold text-gray-700">{{ t(group.labelKey) }}</span>
        </template>

        <div
          v-for="(field, index) in group.fields"
          :key="field.key"
          class="flex items-start justify-between gap-6 py-4"
          :class="index > 0 ? 'border-t border-gray-100' : ''"
        >
          <div class="min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-sm font-medium text-gray-800">{{ t(field.labelKey) }}</span>
              <el-tag size="small" effect="plain" :type="field.hot ? 'success' : 'info'">
                {{ field.hot ? t('runtimeConfig.hotBadge') : t('runtimeConfig.restartBadge') }}
              </el-tag>
              <el-tag v-if="field.pendingRestart" size="small" type="warning" effect="light">
                {{ t('runtimeConfig.pendingRestart') }}
              </el-tag>
            </div>

            <div class="text-gray-400 text-xs leading-5 mt-1">{{ t(field.descKey) }}</div>

            <div class="text-gray-400 text-xs leading-5 mt-1">
              {{ t('runtimeConfig.defaultValue') }}：{{ boolText(field.defaultValue) }}
              <span class="mx-1 text-gray-300">·</span>
              {{ field.source === 'override' ? t('runtimeConfig.sourceOverride') : t('runtimeConfig.sourceDefault') }}
              <template v-if="field.source === 'override' && (field.updatedBy || field.updatedAt)">
                <span class="mx-1 text-gray-300">·</span>
                <span>{{ field.updatedBy || '—' }} {{ field.updatedAt }}</span>
              </template>
            </div>
          </div>

          <div class="flex items-center gap-3 shrink-0 pt-0.5">
            <el-button
              v-if="field.source === 'override'"
              link
              type="primary"
              :loading="resetting[field.key]"
              @click="handleReset(field)"
            >
              {{ t('runtimeConfig.resetDefault') }}
            </el-button>
            <el-switch
              v-model="field.value"
              :loading="saving[field.key]"
              :before-change="() => beforeToggle(field)"
              @change="(value) => handleToggle(field, value)"
            />
          </div>
        </div>
      </el-card>

      <el-empty v-if="!loading && groups.length === 0" :description="t('runtimeConfig.empty')" />
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from 'vue-i18n'

import { listSystemConfig, updateSystemConfig, resetSystemConfig } from '@/api/modules/systemConfig'

const { t } = useI18n()

const loading = ref(false)
const groups = ref([])
const saving = reactive({})
const resetting = reactive({})

// 关闭这两个开关的影响面比其它项大：一个让管理平台找不到网关，一个让网关
// 重启后不支持任何协议。打开方向不需要确认 —— 恢复能力不该有摩擦。
const CONFIRM_ON_DISABLE = new Set(['discovery.enabled', 'plugin.enabled'])

function boolText(value) {
  return value ? t('common.enabled') : t('common.disabled')
}

function confirmAction(title, message) {
  return ElMessageBox.confirm(message, title, {
    type: 'warning',
    confirmButtonText: t('common.confirm'),
    cancelButtonText: t('common.cancel'),
  })
    .then(() => true)
    .catch(() => false)
}

function beforeToggle(field) {
  if (!field.value || !CONFIRM_ON_DISABLE.has(field.key)) {
    return true
  }
  if (field.key === 'discovery.enabled') {
    return confirmAction(
      t('runtimeConfig.confirm.discoveryTitle'),
      t('runtimeConfig.confirm.discoveryMessage'),
    )
  }
  return confirmAction(
    t('runtimeConfig.confirm.pluginTitle'),
    t('runtimeConfig.confirm.pluginMessage'),
  )
}

async function fetchConfig() {
  loading.value = true
  try {
    const res = await listSystemConfig()
    groups.value = res?.groups ?? []
  } catch (err) {
    console.error('获取运行开关失败', err)
    // 拦截器已经弹过错误提示
  } finally {
    loading.value = false
  }
}

async function handleToggle(field, value) {
  saving[field.key] = true
  try {
    const res = await updateSystemConfig({ key: field.key, value })
    ElMessage.success(
      res?.requiresRestart ? t('runtimeConfig.savedRestart') : t('runtimeConfig.savedHot'),
    )
  } catch (err) {
    // 失败时用服务端真值回填，避免开关停在用户以为已生效的位置
  } finally {
    saving[field.key] = false
    await fetchConfig()
  }
}

async function handleReset(field) {
  const ok = await confirmAction(
    t('runtimeConfig.confirm.resetTitle'),
    t('runtimeConfig.confirm.resetMessage'),
  )
  if (!ok) return

  resetting[field.key] = true
  try {
    await resetSystemConfig({ key: field.key })
    ElMessage.success(t('runtimeConfig.resetDone'))
  } catch (err) {
    // 拦截器已经弹过错误提示
  } finally {
    resetting[field.key] = false
    await fetchConfig()
  }
}

onMounted(() => {
  fetchConfig()
})
</script>
