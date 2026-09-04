<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="h-screen flex flex-col bg-[#f5f7fa]">
    <!-- 独立阅读页顶栏（不在管理端主布局内，供新窗口打开） -->
    <header class="flex items-center justify-between px-6 border-b bg-white" style="height: 60px">
      <div class="flex items-center gap-3">
        <el-icon class="text-lg text-[#409eff]"><Document /></el-icon>
        <span class="font-bold text-base">{{ t('openApiDoc.title') }}</span>
        <span class="text-xs text-gray-400">{{ t('openApiDoc.subtitle') }}</span>
      </div>
      <el-button text type="primary" @click="router.push('/dashboard')">
        <el-icon class="mr-1"><Back /></el-icon>{{ t('openApiDoc.backToConsole') }}
      </el-button>
    </header>

    <main ref="scrollMain" class="flex-1 overflow-y-auto p-6" @scroll.passive="onScroll">
      <div class="mx-auto max-w-[1240px] flex gap-5 items-start">
        <!-- 左侧目录导航（从渲染后的标题动态生成，支持整体折叠与分组展开收起） -->
        <aside v-if="tocTree.length" class="w-56 shrink-0 sticky top-6">
          <div class="toc-panel">
            <button type="button" class="toc-title" @click="panelCollapsed = !panelCollapsed">
              <span>{{ t('openApiDoc.toc') }}</span>
              <el-icon class="toc-caret" :class="{ 'is-folded': panelCollapsed }"><ArrowDown /></el-icon>
            </button>
            <nav v-show="!panelCollapsed" class="toc-list">
              <template v-for="group in tocTree" :key="group.id">
                <a
                  :href="'#' + group.id"
                  :class="['toc-item', 'is-l2', { 'is-active': isActive(group) }]"
                  @click.prevent="scrollTo(group)"
                >
                  <span class="toc-item-text">{{ group.text }}</span>
                  <el-icon
                    v-if="group.children.length"
                    class="toc-caret is-group"
                    :class="{ 'is-folded': !group.expanded }"
                    @click.stop.prevent="toggleGroup(group)"
                  >
                    <ArrowDown />
                  </el-icon>
                </a>
                <template v-if="group.children.length && group.expanded">
                  <a
                    v-for="child in group.children"
                    :key="child.id"
                    :href="'#' + child.id"
                    :class="['toc-item', 'is-l3', { 'is-active': child.id === activeId }]"
                    @click.prevent="scrollTo(child)"
                  >
                    {{ child.text }}
                  </a>
                </template>
              </template>
            </nav>
          </div>
        </aside>

        <el-card shadow="never" class="flex-1 api-doc-card">
          <!-- 文档由后端 go:embed 内置 markdown 渲染为 HTML，此处仅展示 -->
          <div v-if="loading" v-loading="loading" class="h-48" />
          <div v-show="!loading && docHtml" ref="contentRef" class="api-doc-content" v-html="docHtml" />
          <el-empty v-if="!loading && !docHtml" :description="t('openApiDoc.loadFailed')" />
        </el-card>
      </div>
    </main>
  </div>
</template>

<script setup>
import { ref, nextTick, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getOpenApiDoc } from '@/api/modules/openApiSecret'

const { t } = useI18n()

const router = useRouter()
const docHtml = ref('')
const loading = ref(true)
const scrollMain = ref(null) // 滚动容器（main）
const contentRef = ref(null) // 文档内容节点

// ---------- 目录数据 ----------
// tocFlat：文档顺序的标题列表（滚动高亮定位用）
// tocTree：h2 为组、h3 挂在其 children 下（渲染用）；分组展开状态 group.expanded
// 存在各自节点上，互不影响，不存在跨分组的共享状态
const tocFlat = ref([])
const tocTree = ref([])
const panelCollapsed = ref(false)
const activeId = ref('')
let autoScrollTimer = null // 平滑滚动期间屏蔽 scroll 高亮，避免中途路过章节误联动

// 渲染完成后抽取 h2/h3 生成目录：为每个标题注入顺序 id，供锚点跳转与高亮定位
watch(docHtml, async (html) => {
  if (!html || !contentRef.value) return
  await nextTick()

  const headings = Array.from(contentRef.value.querySelectorAll('h2, h3'))
  tocFlat.value = headings.map((el, i) => {
    const id = `doc-sec-${i}`
    el.id = id
    return { id, text: el.textContent.trim(), level: el.tagName === 'H2' ? 2 : 3 }
  })

  const tree = []
  for (const item of tocFlat.value) {
    if (item.level === 2 || tree.length === 0) {
      // 分组子项（h3）默认收起，点击组名或箭头展开
      tree.push({ ...item, children: [], expanded: false })
    } else {
      tree[tree.length - 1].children.push(item)
    }
  }
  tocTree.value = tree
  // 初始高亮第一个标题
  if (tocFlat.value.length) activeId.value = tocFlat.value[0].id
})

/** 分组展开/收起（状态挂在分组节点自身） */
function toggleGroup(group) {
  group.expanded = !group.expanded
}

/** 目录项是否处于激活态（组本身激活或其子项激活，子项激活时若分组已收起则自动展开） */
function isActive(group) {
  if (group.id === activeId.value) return true
  return group.children.some((c) => c.id === activeId.value)
}

watch(activeId, (id) => {
  const group = tocTree.value.find((g) => g.children.some((c) => c.id === id))
  if (group && !group.expanded) group.expanded = true
})

/** 点击目录：平滑滚动到对应标题，高亮立即归位到点击项 */
function scrollTo(item) {
  clearTimeout(autoScrollTimer)
  activeId.value = item.id // 立即定位，避免依赖滚动结束时机
  document.getElementById(item.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  autoScrollTimer = setTimeout(() => (autoScrollTimer = null), 800)
}

/** 滚动高亮：取视口顶部以上最近的一个标题 */
function onScroll() {
  const container = scrollMain.value
  if (!container || !tocFlat.value.length || autoScrollTimer) return
  const threshold = container.getBoundingClientRect().top + 90
  let current = tocFlat.value[0]?.id ?? ''
  for (const item of tocFlat.value) {
    const el = document.getElementById(item.id)
    if (el && el.getBoundingClientRect().top <= threshold) {
      current = item.id
    } else if (el) {
      break
    }
  }
  activeId.value = current
}

onMounted(async () => {
  try {
    const html = await getOpenApiDoc()
    if (typeof html === 'string' && html) docHtml.value = html
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
/* ---------- 左侧目录 ---------- */
.toc-panel {
  background: #fff;
  border-radius: 6px;
  padding: 12px 0;
}
.toc-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: calc(100% - 24px);
  margin: 0 12px 8px;
  padding: 4px 4px 10px;
  font-size: 13px;
  font-weight: 600;
  color: #303133;
  background: none;
  border: none;
  border-bottom: 1px solid #ebeef5;
  border-radius: 0;
  cursor: pointer;
}
.toc-caret {
  color: #909399;
  transition: transform 0.25s;
}
.toc-caret.is-folded {
  transform: rotate(-90deg);
}
.toc-list {
  display: flex;
  flex-direction: column;
  padding-top: 6px;
  max-height: calc(100vh - 180px);
  overflow-y: auto;
}
.toc-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  font-size: 13px;
  line-height: 1.5;
  padding: 6px 12px;
  color: #606266;
  text-decoration: none;
  border-left: 2px solid transparent;
}
.toc-item.is-l2 {
  font-weight: 500;
}
.toc-item.is-l3 {
  padding-left: 34px;
  font-size: 12px;
  color: #909399;
  white-space: normal;
  word-break: break-all;
}
.toc-item:hover {
  color: #409eff;
}
.toc-item.is-active {
  color: #409eff;
  font-weight: 600;
  border-left-color: #409eff;
  background: #ecf5ff;
}
.toc-item-text {
  white-space: normal;
  word-break: break-all;
}
.toc-caret.is-group {
  flex-shrink: 0;
  margin-right: -2px;
}

.api-doc-card {
  max-width: 920px;
}

.api-doc-content {
  line-height: 1.75;
  color: #303133;
  font-size: 14px;
}
.api-doc-content :deep(h2),
.api-doc-content :deep(h3) {
  /* 锚点跳转预留高度，避免标题被顶栏/容器边缘裁住 */
  scroll-margin-top: 24px;
}
.api-doc-content :deep(h1) {
  font-size: 22px;
  margin: 12px 0 16px;
  border-bottom: 1px solid #ebeef5;
  padding-bottom: 8px;
}
.api-doc-content :deep(h2) {
  font-size: 18px;
  margin: 24px 0 12px;
}
.api-doc-content :deep(h3) {
  font-size: 15px;
  margin: 20px 0 10px;
}
.api-doc-content :deep(p),
.api-doc-content :deep(li) {
  margin: 6px 0;
}
.api-doc-content :deep(ul),
.api-doc-content :deep(ol) {
  padding-left: 24px;
}
.api-doc-content :deep(blockquote) {
  margin: 10px 0;
  padding: 8px 14px;
  border-left: 3px solid #e6a23c;
  background: #fdf6ec;
  color: #b88230;
}
.api-doc-content :deep(code) {
  background: #f5f7fa;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 13px;
  color: #c7254e;
}
.api-doc-content :deep(pre code) {
  display: block;
  padding: 12px;
  border-radius: 6px;
  color: #d4d4d4;
  background: #282c34;
  overflow-x: auto;
  line-height: 1.6;
}
.api-doc-content :deep(table) {
  border-collapse: collapse;
  width: auto;
  min-width: 50%;
  margin: 10px 0;
}
.api-doc-content :deep(th),
.api-doc-content :deep(td) {
  border: 1px solid #ebeef5;
  padding: 7px 12px;
  text-align: left;
}
.api-doc-content :deep(th) {
  background: #f5f7fa;
  font-weight: 600;
}
.api-doc-content :deep(tr:nth-child(even)) {
  background: #fafafa;
}
</style>
