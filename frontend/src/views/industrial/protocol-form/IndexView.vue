<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="iot-page pf-page">
    <!-- 页头：与工业物联网其余页面同一套层级；设计器本身占满剩余高度 -->
    <header class="iot-head iot-enter">
      <div>
        <p class="iot-eyebrow">
          <span class="iot-eyebrow-dot" aria-hidden="true" />
          {{ t('menu.industrial') }}
        </p>
        <h1 class="iot-title">{{ t('menu.protocolForm') }}</h1>
        <p class="iot-sub">{{ t('protocolForm.subtitle') }}</p>
      </div>
    </header>

    <section class="iot-panel designer-panel iot-enter" style="animation-delay: 60ms">
      <div class="iot-toolbar">
        <div class="iot-toolbar-filters">
          <span class="pf-label">{{ t('protocolForm.selectProtocol') }}</span>
          <ProtocolSelector
            v-model="selectedProtocol"
            :button-text="t('protocolForm.selectProtocolBtn')"
            @select="handleProtocolSelect"
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
import { getProtocolById, updateProtocolFormJsonById } from '@/api/modules/protocol'
import ProtocolSelector from '@/components/ProtocolSelector.vue'
import { useDesignerLocale } from '@/composables/useDesignerLocale'
import { migrateDynamicFormRulesToLanguage } from '@/utils/dynamicFormMigration'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const { designerLocale, designerConfig } = useDesignerLocale()

defineOptions({ name: 'SettingsProtocolForm' })

const designerRef = ref(null)
const selectedProtocol = ref('')
const selectedProtocolId = ref('')
const formLoading = ref(false)
const saving = ref(false)

defineExpose({ designerRef })

/** 页面加载 */
onMounted(() => {
  // 无需预加载，组件内部自管理
})

/** 协议选中回调：加载表单配置 */
function handleProtocolSelect(row) {
  selectedProtocol.value = row.name
  selectedProtocolId.value = row.id
  handleProtocolChange(row.id)
}

/** 切换协议时加载已有表单配置并回填 */
async function handleProtocolChange(id) {
  if (!id) return

  formLoading.value = true
  try {
    const detail = await getProtocolById(id)
    let formJson = detail?.formJson

    // 兼容后端可能返回字符串或已解析对象
    if (typeof formJson === 'string') {
      try {
        formJson = JSON.parse(formJson)
      } catch {
        formJson = null
      }
    }

    // 后端可能将 rule/options 序列化为字符串，需要二次解析
    if (formJson?.rule && typeof formJson.rule === 'string') {
      try {
        formJson.rule = JSON.parse(formJson.rule)
      } catch {
        formJson.rule = null
      }
    }
    if (formJson?.options && typeof formJson.options === 'string') {
      try {
        formJson.options = JSON.parse(formJson.options)
      } catch {
        // 忽略，options 非必需
      }
    }

    if (formJson?.rule && Array.isArray(formJson.rule)) {
      designerRef.value?.setRule(formJson.rule)
      designerRef.value?.setOptions(formJson.options || {})
    } else {
      // 无配置时清空设计器
      designerRef.value?.setRule([])
      designerRef.value?.setOptions({})
    }
  } catch {
    ElMessage.error(t('protocolForm.loadFailed'))
    designerRef.value?.setRule([])
    designerRef.value?.setOptions({})
  } finally {
    formLoading.value = false
  }
}

/**
 * 把当前表单里的中文字面量抽成多语言 Key（不自动保存）。
 * 插件自带的 formJson 是写死中文的；不转换的话，在设计器里改一个字英文界面就退回中文。
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
  const count =
    Object.keys(language['zh-cn'] || {}).length -
    Object.keys(options.language?.['zh-cn'] || {}).length
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

  const id = selectedProtocolId.value
  if (!id) {
    ElMessage.warning(t('protocolForm.selectFirst'))
    return
  }

  const rule = JSON.parse(designerRef.value.getJson() || '[]')
  const options = JSON.parse(designerRef.value.getOptionsJson() || '{}')
  if (!rule || !rule.length) {
    ElMessage.warning(t('protocolForm.emptyForm'))
    return
  }

  saving.value = true
  try {
    await updateProtocolFormJsonById({
      id,
      formJson: { rule, options }
    })
    ElMessage.success(t('protocolForm.saveSuccess'))
  } catch (e) {
    ElMessage.error(e?.msg || t('protocolForm.saveFailed'))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
/* 设计器页占满内容区剩余高度：减去顶栏 60px 与 el-main 的上下内边距 48px */
.pf-page {
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--header-height, 60px) - 48px);
}

.pf-page .iot-head {
  flex: none;
}

.designer-panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  margin-bottom: 0;
}

.pf-label {
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
