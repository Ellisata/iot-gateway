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
        <h1 class="iot-title">{{ t('runtimeConfig.title') }}</h1>
        <p class="iot-sub">{{ t('runtimeConfig.subtitle') }}</p>
      </div>
      <div class="iot-head-aside">
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayOverride }}</span>
          <span class="iot-stat-label">{{ t('runtimeConfig.overrideUnit') }}</span>
        </div>
      </div>
    </header>

    <!-- 默认值与覆盖值的语义说明。放在最上面，避免用户把「默认值」误当成「当前生效值」 -->
    <div class="iot-notice is-info iot-enter" style="animation-delay: 40ms">
      <span class="iot-notice-mark" aria-hidden="true">
        <el-icon><InfoCircle /></el-icon>
      </span>
      <div class="runtime-intro">
        <p class="runtime-intro-title">{{ t('runtimeConfig.introTitle') }}</p>
        <p class="iot-notice-text">{{ t('runtimeConfig.intro') }}</p>
      </div>
    </div>

    <div v-loading="loading">
      <section
        v-for="(group, index) in groups"
        :key="group.key"
        class="iot-panel iot-enter"
        :style="{ animationDelay: `${80 + index * 50}ms` }"
      >
        <div class="iot-toolbar">
          <div class="iot-toolbar-lead">
            <span class="iot-mark" aria-hidden="true">
              <el-icon><Settings /></el-icon>
            </span>
            <div class="iot-toolbar-copy">
              <p class="iot-toolbar-title">{{ t(group.labelKey) }}</p>
            </div>
          </div>
        </div>

        <div class="runtime-list">
          <div
            v-for="field in group.fields"
            :key="field.key"
            class="runtime-row"
          >
            <div class="runtime-main">
              <div class="runtime-head">
                <span class="runtime-name">{{ t(field.labelKey) }}</span>
                <span class="iot-pill" :class="field.hot ? 'is-green' : 'is-gray'">
                  {{ field.hot ? t('runtimeConfig.hotBadge') : t('runtimeConfig.restartBadge') }}
                </span>
                <span v-if="field.pendingRestart" class="iot-pill is-amber">
                  {{ t('runtimeConfig.pendingRestart') }}
                </span>
              </div>

              <p class="runtime-desc">{{ t(field.descKey) }}</p>

              <p class="runtime-meta">
                {{ t('runtimeConfig.defaultValue') }}：{{ boolText(field.defaultValue) }}
                <span class="meta-dot" aria-hidden="true">·</span>
                {{ field.source === 'override' ? t('runtimeConfig.sourceOverride') : t('runtimeConfig.sourceDefault') }}
                <template v-if="field.source === 'override' && (field.updatedBy || field.updatedAt)">
                  <span class="meta-dot" aria-hidden="true">·</span>
                  <span>{{ field.updatedBy || '—' }} {{ field.updatedAt }}</span>
                </template>
              </p>
            </div>

            <div class="runtime-actions">
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
        </div>
      </section>

      <div v-if="!loading && groups.length === 0" class="iot-empty">
        <span class="iot-empty-mark" aria-hidden="true">
          <el-icon><Settings /></el-icon>
        </span>
        <p class="iot-empty-title">{{ t('runtimeConfig.empty') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  IconInfoCircle as InfoCircle,
  IconSettings as Settings,
} from '@tabler/icons-vue'
import { useI18n } from 'vue-i18n'

import { listSystemConfig, updateSystemConfig, resetSystemConfig } from '@/api/modules/systemConfig'

const { t } = useI18n()

const loading = ref(false)
const groups = ref([])
const saving = reactive({})
const resetting = reactive({})

// 页头读数：有多少项被页面覆盖（关闭后回到 default.yaml 的项不计入）
const overrideCount = computed(() =>
  groups.value.reduce(
    (n, group) => n + (group.fields || []).filter((f) => f.source === 'override').length,
    0,
  )
)

// 首屏还没拿到配置时用占位符，避免 0 被读成"所有开关都在用默认值"
const displayOverride = computed(() => (loading.value ? '—' : overrideCount.value))

// 关闭这两个开关的影响面比其它项大：一个让管理平台找不到网关，一个让网关
// 重启后不支持任何协议。打开方向不需要确认 —— 恢复能力不该有摩擦。
const CONFIRM_ON_DISABLE = new Set(['discovery.enabled', 'plugin.enabled'])

// /agentApi 的风险方向相反：危险的是「打开」—— 打开后整组免鉴权且含
// 删除设备、上传插件等写操作。关闭方向反而是在收紧，不需要摩擦。
const CONFIRM_ON_ENABLE = new Set(['agentApi.enabled'])

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
  // field.value 是切换前的值：关闭方向（true -> false）与开启方向各管各的
  if (!field.value) {
    if (!CONFIRM_ON_ENABLE.has(field.key)) {
      return true
    }
    return confirmAction(
      t('runtimeConfig.confirm.agentApiTitle'),
      t('runtimeConfig.confirm.agentApiMessage'),
    )
  }
  if (!CONFIRM_ON_DISABLE.has(field.key)) {
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

<style scoped>
/* 说明条里的标题比正文重一档，正文交给 .iot-notice-text 的换行规则 */
.runtime-intro {
  min-width: 0;
}

.runtime-intro-title {
  margin: 0 0 3px;
  font-size: 12.5px;
  font-weight: 600;
}

.runtime-list {
  padding: 2px 20px;
}

/* 每个开关一行：左侧说明、右侧动作，行间用发丝线分隔 */
.runtime-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  padding: 16px 0;
  border-bottom: 1px solid var(--line-soft);
}

.runtime-row:last-child {
  border-bottom: 0;
}

.runtime-main {
  min-width: 0;
}

.runtime-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.runtime-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--ink);
}

.runtime-desc {
  margin: 5px 0 0;
  font-size: 12px;
  line-height: 1.65;
  color: var(--ink-4);
}

.runtime-meta {
  margin: 5px 0 0;
  font-size: 11.5px;
  line-height: 1.6;
  color: var(--ink-4);
  font-variant-numeric: tabular-nums;
}

.meta-dot {
  margin: 0 6px;
  color: #cbd5e1;
}

.runtime-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: 12px;
  padding-top: 1px;
}

@media (max-width: 640px) {
  .runtime-row {
    flex-direction: column;
    gap: 10px;
  }

  .runtime-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
