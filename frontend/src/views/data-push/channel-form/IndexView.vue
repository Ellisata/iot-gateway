<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page cf-page">
    <!-- 页头：与工业物联网、数据推送其余页面同一套层级；设计器占满剩余高度 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.dataPush') }}
        </p>
        <h1 class="iot-title">{{ t('menu.channelForm') }}</h1>
        <p class="iot-sub">{{ t('channelForm.subtitle') }}</p>
      </div>
    </header>

    <section class="iot-panel designer-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <span class="cf-label">{{ t('channelForm.selectChannel') }}</span>
          <ChannelSelector
            v-model="selectedChannel"
            :button-text="t('channelForm.selectChannelBtn')"
            @select="handleChannelSelect"
          />
        </div>
        <div class="iot-toolbar-actions">
          <el-tooltip :content="t('formI18n.hint')" placement="bottom">
            <el-button :icon="Language" @click="handleMigrateI18n">
              {{ t('formI18n.migrate') }}
            </el-button>
          </el-tooltip>
          <el-button
            type="primary"
            :icon="Check"
            :loading="saving"
            @click="handleSave"
          >
            {{ t('common.save') }}
          </el-button>
        </div>
      </div>

      <!-- 表单设计器 -->
      <div class="designer-wrapper">
        <fc-designer
          ref="designerRef"
          height="100%"
          :config="designerConfig"
          :locale="designerLocale"
        />
        <!-- 加载遮罩 -->
        <div v-if="formLoading" class="loading-mask">
          <el-icon class="loading-icon" :size="32"><Loading /></el-icon>
          <span>{{ t('common.loading') }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import {
  IconCheck as Check,
  IconLanguage as Language,
  IconLoader2 as Loading,
} from '@tabler/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getPushChannelFormByName,
  createPushChannelForm,
} from '@/api/modules/pushChannelForm'
import ChannelSelector from '@/components/ChannelSelector.vue'
import { useDesignerLocale } from '@/composables/useDesignerLocale'
import { migrateDynamicFormRulesToLanguage } from '@/utils/dynamicFormMigration'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const { designerLocale, designerConfig } = useDesignerLocale()

defineOptions({ name: 'DataPushChannelForm' })

const designerRef = ref(null)
const selectedChannel = ref('')
const formLoading = ref(false)
const saving = ref(false)

defineExpose({ designerRef })

/** 页面加载 */
onMounted(() => {
  // 无需预加载，组件内部自管理
})

/** 通道选中回调：按名称加载表单配置 */
function handleChannelSelect(row) {
  selectedChannel.value = row.name
  handleChannelChange(row.name)
}

/** 递归解析可能被二次编码的 JSON 字段 */
function deepParse(value) {
  if (typeof value === 'string') {
    try {
      return deepParse(JSON.parse(value))
    } catch {
      return value
    }
  }
  if (Array.isArray(value)) {
    return value.map(deepParse)
  }
  if (value && typeof value === 'object') {
    const obj = {}
    for (const key of Object.keys(value)) {
      obj[key] = deepParse(value[key])
    }
    return obj
  }
  return value
}

/** 从后端响应中提取表单配置，兼容多种返回结构 */
function normalizeFormConfig(res) {
  if (!res) return null
  // 兼容返回 { formJson: ... } 包装结构
  const formJson = res.formJson != null ? res.formJson : res
  const obj = deepParse(formJson)
  if (!obj || typeof obj !== 'object') return null
  return {
    rule: obj.rule,
    options: obj.options || {},
  }
}

/** 切换通道时加载已有表单配置并回填 */
async function handleChannelChange(name) {
  if (!name) return

  formLoading.value = true
  try {
    const res = await getPushChannelFormByName(name)
    const config = normalizeFormConfig(res)

    if (config?.rule && Array.isArray(config.rule)) {
      designerRef.value?.setRule(config.rule)
      designerRef.value?.setOptions(config.options)
    } else {
      // 无配置时清空设计器
      designerRef.value?.setRule([])
      designerRef.value?.setOptions({})
    }
  } catch {
    //ElMessage.error('加载通道表单配置失败')
    designerRef.value?.setRule([])
    designerRef.value?.setOptions({})
  } finally {
    formLoading.value = false
  }
}

/**
 * 把当前表单里的中文字面量抽成多语言 Key（不自动保存）。
 * 存量表单是早期迁移写死的中文；不转换的话，运维改一个字英文界面就退回中文。
 */
async function handleMigrateI18n() {
  if (!designerRef.value) return
  let options = {}
  try {
    options = JSON.parse(designerRef.value.getOptionsJson() || '{}')
  } catch {
    // 选项解析失败时按空对象处理，规则仍可迁移
  }
  const { rules, language } = migrateDynamicFormRulesToLanguage(
    designerRef.value.getRule(),
    options.language,
  )
  const count = Object.keys(language['zh-cn'] || {}).length - Object.keys(options.language?.['zh-cn'] || {}).length
  if (count <= 0) {
    ElMessage.info(t('formI18n.nothing'))
    return
  }
  try {
    await ElMessageBox.confirm(t('formI18n.confirm'), t('formI18n.title'), {
      type: 'info',
      confirmButtonText: t('common.confirm'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return
  }
  designerRef.value.setRule(rules)
  designerRef.value.setOptions({ ...options, language })
  ElMessage.success(t('formI18n.done'))
}

/** 保存表单 */
async function handleSave() {
  if (!designerRef.value) return

  const name = selectedChannel.value
  if (!name) {
    ElMessage.warning(t('channelForm.selectFirst'))
    return
  }

  const rule = JSON.parse(designerRef.value.getJson() || '[]')
  const options = JSON.parse(designerRef.value.getOptionsJson() || '{}')
  if (!rule || !rule.length) {
    ElMessage.warning(t('channelForm.emptyForm'))
    return
  }

  saving.value = true
  try {
    await createPushChannelForm({
      name,
      formJson: { rule, options }
    })
    ElMessage.success(t('channelForm.saveSuccess'))
  } catch (e) {
    ElMessage.error(e?.msg || t('channelForm.saveFailed'))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
/* 设计器页占满内容区剩余高度：减去顶栏 60px 与 el-main 的上下内边距 48px */
.cf-page {
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--header-height, 60px) - 48px);
}

.cf-page .iot-head {
  flex: none;
}

.designer-panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  margin-bottom: 0;
}

.cf-label {
  font-size: 12.5px;
  color: var(--ink-3);
  white-space: nowrap;
}

.designer-wrapper {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
  background: #f8fafc;
}

/* 让 fc-designer 填满容器 */
.designer-wrapper :deep(.fc-designer) {
  height: 100%;
}

.loading-mask {
  position: absolute;
  inset: 0;
  z-index: 20;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  font-size: 13px;
  color: var(--brand);
  background: rgba(255, 255, 255, 0.78);
  backdrop-filter: blur(2px);
}

.loading-icon {
  animation: rotating 1s linear infinite;
}

@keyframes rotating {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
