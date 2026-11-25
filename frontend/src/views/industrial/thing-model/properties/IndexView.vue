<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<!--
物模型属性的独立页：属性主编辑界面已并入物模型主从工作台右栏，
这里保留为旧深链与「属性控制下发」返回入口的落点，主体复用 PropertiesPanel。
-->
<template>
  <div class="iot-page">
    <header class="iot-head iot-enter">
      <div class="iot-head-main">
        <div class="iot-head-top">
          <button type="button" class="iot-back" @click="goBack">
            <el-icon><ArrowLeft /></el-icon>
            {{ t('thingModel.backToModels') }}
          </button>
          <p class="iot-eyebrow">
            <span class="iot-eyebrow-dot" aria-hidden="true" />
            {{ t('menu.industrial') }}
          </p>
        </div>
        <div class="iot-title-row">
          <h1 class="iot-title">{{ headerTitle }}</h1>
          <span class="iot-head-sub">{{ t('thingModel.propertiesSubtitle') }}</span>
        </div>
      </div>
      <div class="iot-head-aside">
        <!-- 模型的稳定身份放在页头：它在工具栏里会挤压搜索框，且不属于筛选项 -->
        <span v-if="modelCode" class="iot-mono thing-model-code" :title="modelCode">
          {{ modelCode }}
        </span>
        <el-tag v-if="modelCategory" size="small" effect="plain">
          {{ categoryLabel(modelCategory) }}
        </el-tag>
        <div class="iot-stat">
          <span class="iot-stat-num">{{ displayTotal }}</span>
          <span class="iot-stat-label">{{ t('thingModel.totalPropertiesUnit') }}</span>
        </div>
      </div>
    </header>

    <section class="iot-panel iot-enter" style="animation-delay: 60ms">
      <PropertiesPanel
        :model-id="String(modelId)"
        :model-name="modelName"
        @total-change="propertyTotal = $event"
        @loading-change="propertyLoading = $event"
      />
    </section>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { IconArrowLeft as ArrowLeft } from '@tabler/icons-vue'
import { getThingModelById } from '@/api/modules/thingModel'
import PropertiesPanel from './PropertiesPanel.vue'

const { t, te } = useI18n()
const route = useRoute()
const router = useRouter()

const modelId = route.params.id
const modelName = ref(typeof route.query.name === 'string' ? route.query.name : '')
const modelCode = ref('')
const modelCategory = ref('')

// 分类展示名来自 i18n；开放接口下发的自定义分类没有内置文案，直接展示 code。
const categoryLabel = (code) => {
  if (!code) return t('thingModel.category.none')
  const key = `thingModel.category.${code}`
  return te(key) ? t(key) : code
}

const headerTitle = computed(() =>
  modelName.value
    ? `${t('thingModel.propertiesPageTitle')} — ${modelName.value}`
    : t('thingModel.propertiesPageTitle'),
)

// 属性计数由面板回报：首屏还没拿到数时显示占位符而不是 0
const propertyTotal = ref(0)
const propertyLoading = ref(true)
const displayTotal = computed(() =>
  propertyLoading.value && propertyTotal.value === 0 ? '—' : propertyTotal.value,
)

// 刷新页面时 query 里没有名称，靠 id 回查补齐页头；列表页带过来的名称先顶上，避免闪烁。
async function fetchModel() {
  if (!modelId) return
  try {
    const res = await getThingModelById(modelId)
    if (!res) return
    modelName.value = res.name || modelName.value
    modelCode.value = res.code || ''
    modelCategory.value = res.category || ''
  } catch {
    // 错误提示由 axios 拦截器统一给出
  }
}

function goBack() {
  router.push({ name: 'ThingModel', query: { modelId: String(modelId) } })
}

onMounted(fetchModel)
</script>

<style scoped>
/* 页头右侧的模型标识：弱化为上下文信息，不与标题抢注意力；
   模型标识最长 64 字符，窄屏下要能截断而不是把计数挤出去 */
.thing-model-code {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12.5px;
  color: var(--ink-3);
}
</style>
