<template>
  <div class="protocol-form-page">
    <!-- 顶部工具栏 -->
    <div class="toolbar">
      <div class="toolbar-left">
        <span class="toolbar-label">{{ t('protocolForm.selectProtocol') }}</span>
        <ProtocolSelector
          v-model="selectedProtocol"
          :button-text="t('protocolForm.selectProtocolBtn')"
          @select="handleProtocolSelect"
        />
      </div>
      <div class="toolbar-right">
        <el-button type="primary" :loading="saving" @click="handleSave">
          <el-icon><Check /></el-icon>
          {{ t('common.save') }}
        </el-button>
      </div>
    </div>

    <!-- 表单设计器 -->
    <div class="designer-wrapper">
      <fc-designer
        ref="designerRef"
        height="100%"
        :config="{ fieldReadonly: false }"
      />
      <!-- 加载遮罩 -->
      <div v-if="formLoading" class="loading-mask">
        <el-icon class="loading-icon" :size="32"><Loading /></el-icon>
        <span>{{ t('common.loading') }}</span>
      </div>
    </div>

  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Check, Loading } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { getProtocolById, updateProtocolFormJsonById } from '@/api/modules/protocol'
import ProtocolSelector from '@/components/ProtocolSelector.vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

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
.protocol-form-page {
  display: flex;
  flex-direction: column;
  height: calc(100vh - var(--header-height, 60px) - 32px);
  overflow: hidden;
  background: #f5f7fa;
}

.toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: #fff;
  border-bottom: 1px solid #ebeef5;
  flex-shrink: 0;
  z-index: 10;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.toolbar-label {
  font-size: 14px;
  color: #606266;
  white-space: nowrap;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.designer-wrapper {
  flex: 1;
  overflow: hidden;
  position: relative;
}

/* 让 fc-designer 填满容器 */
.designer-wrapper :deep(.fc-designer) {
  height: 100%;
}

.loading-mask {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  background: rgba(255, 255, 255, 0.75);
  z-index: 20;
  color: #409eff;
  font-size: 14px;
}

.loading-icon {
  animation: rotating 1s linear infinite;
}

@keyframes rotating {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
