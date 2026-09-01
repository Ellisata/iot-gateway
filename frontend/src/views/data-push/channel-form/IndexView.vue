<template>
  <div class="channel-form-page">
    <!-- 顶部工具栏 -->
    <div class="toolbar">
      <div class="toolbar-left">
        <span class="toolbar-label">{{ t('channelForm.selectChannel') }}</span>
        <ChannelSelector
          v-model="selectedChannel"
          :button-text="t('channelForm.selectChannelBtn')"
          @select="handleChannelSelect"
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
import {
  getPushChannelFormByName,
  createPushChannelForm,
} from '@/api/modules/pushChannelForm'
import ChannelSelector from '@/components/ChannelSelector.vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

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
.channel-form-page {
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
