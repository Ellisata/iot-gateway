<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="doc-page">
    <!-- 独立阅读页顶栏（不在管理端主布局内，供新窗口打开）：与控制台同一套字号/配色 -->
    <header class="doc-head">
      <div class="doc-brand">
        <span class="doc-brand-mark" aria-hidden="true">
          <el-icon><Document /></el-icon>
        </span>
        <div class="doc-brand-copy">
          <p class="doc-brand-title">{{ t('openApiDoc.title') }}</p>
          <p class="doc-brand-sub">{{ t('openApiDoc.subtitle') }}</p>
        </div>
      </div>
      <el-button text type="primary" @click="router.push('/dashboard')">
        <el-icon class="mr-1"><Back /></el-icon>{{ t('openApiDoc.backToConsole') }}
      </el-button>
    </header>

    <main ref="scrollMain" class="doc-main" @scroll.passive="onScroll">
      <div class="doc-layout">
        <!-- 左侧目录导航（从渲染后的标题动态生成，支持整体折叠与分组展开收起） -->
        <aside v-if="tocTree.length" class="doc-aside">
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

        <section class="doc-surface">
          <!-- 文档由后端 go:embed 内置 markdown 渲染为 HTML，此处仅展示 -->
          <div v-if="loading" v-loading="loading" class="doc-loading" />
          <div v-show="!loading && docHtml" ref="contentRef" class="api-doc-content" v-html="docHtml" />
          <div v-if="!loading && !docHtml" class="iot-empty">
            <span class="iot-empty-mark" aria-hidden="true">
              <el-icon><Document /></el-icon>
            </span>
            <p class="iot-empty-title">{{ t('openApiDoc.loadFailed') }}</p>
          </div>
        </section>
      </div>
    </main>
  </div>
</template>

<script setup>
import { ref, nextTick, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { getOpenApiDoc } from '@/api/modules/openApiSecret'
import {
  IconArrowLeft as Back,
  IconChevronDown as ArrowDown,
  IconFileText as Document,
} from '@tabler/icons-vue'

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
/* 阅读页在独立窗口打开：token 挂在 :root 上，独立渲染也能继承 */
.doc-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  color: var(--ink-2);
  background: #f6f8fb;
}

/* ---------- 顶栏 ---------- */
.doc-head {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  height: 60px;
  padding: 0 24px;
  background: #fff;
  border-bottom: 1px solid var(--line);
}

.doc-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.doc-brand-mark {
  display: grid;
  flex: none;
  place-items: center;
  width: 30px;
  height: 30px;
  color: var(--brand);
  background: var(--brand-soft);
  border-radius: 9px;
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.08);
}

.doc-brand-mark .el-icon {
  font-size: 17px;
}

.doc-brand-copy {
  min-width: 0;
}

.doc-brand-title {
  margin: 0;
  font-size: 14px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: var(--ink);
}

.doc-brand-sub {
  margin: 2px 0 0;
  overflow: hidden;
  font-size: 12px;
  line-height: 1.5;
  color: var(--ink-4);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---------- 布局 ---------- */
.doc-main {
  flex: 1;
  padding: 24px;
  overflow-y: auto;
}

.doc-layout {
  display: flex;
  align-items: flex-start;
  gap: 20px;
  max-width: 1240px;
  margin: 0 auto;
}

.doc-aside {
  position: sticky;
  top: 0;
  flex: none;
  width: 224px;
}

.doc-surface {
  flex: 1;
  min-width: 0;
  max-width: 920px;
  padding: 24px 28px;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 18px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.doc-loading {
  height: 192px;
}

/* ---------- 左侧目录 ---------- */
.toc-panel {
  padding: 10px 0;
  overflow: hidden;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 14px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.toc-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 6px 16px 10px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--ink-2);
  background: none;
  border: 0;
  border-bottom: 1px solid var(--line-soft);
  cursor: pointer;
}

.toc-caret {
  color: var(--ink-4);
  transition: transform 0.25s;
}

.toc-caret.is-folded {
  transform: rotate(-90deg);
}

.toc-list {
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 200px);
  padding-top: 6px;
  overflow-y: auto;
}

.toc-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  padding: 6px 16px;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--ink-3);
  text-decoration: none;
  border-left: 2px solid transparent;
  transition: color 0.15s ease, background-color 0.15s ease;
}

.toc-item.is-l2 {
  font-weight: 500;
}

.toc-item.is-l3 {
  padding-left: 34px;
  font-size: 12px;
  color: var(--ink-4);
  white-space: normal;
  word-break: break-all;
}

.toc-item:hover {
  color: var(--brand);
  background: #f8fafc;
}

.toc-item.is-active {
  font-weight: 600;
  color: var(--brand);
  background: var(--brand-soft);
  border-left-color: var(--brand);
}

.toc-item-text {
  white-space: normal;
  word-break: break-all;
}

.toc-caret.is-group {
  flex-shrink: 0;
  margin-right: -2px;
}

/* ---------- 文档正文 ---------- */
.api-doc-content {
  font-size: 14px;
  line-height: 1.75;
  color: var(--ink-2);
}

.api-doc-content :deep(h1),
.api-doc-content :deep(h2),
.api-doc-content :deep(h3) {
  color: var(--ink);
}

.api-doc-content :deep(h2),
.api-doc-content :deep(h3) {
  /* 锚点跳转预留高度，避免标题被顶栏/容器边缘裁住 */
  scroll-margin-top: 24px;
}

.api-doc-content :deep(h1) {
  padding-bottom: 8px;
  margin: 12px 0 16px;
  font-size: 22px;
  border-bottom: 1px solid var(--line-soft);
}

.api-doc-content :deep(h2) {
  margin: 24px 0 12px;
  font-size: 18px;
}

.api-doc-content :deep(h3) {
  margin: 20px 0 10px;
  font-size: 15px;
}

.api-doc-content :deep(p),
.api-doc-content :deep(li) {
  margin: 6px 0;
}

.api-doc-content :deep(ul),
.api-doc-content :deep(ol) {
  padding-left: 24px;
}

.api-doc-content :deep(a) {
  color: var(--brand);
  text-decoration: none;
}

.api-doc-content :deep(a:hover) {
  text-decoration: underline;
}

.api-doc-content :deep(blockquote) {
  margin: 10px 0;
  padding: 8px 14px;
  color: #92400e;
  background: #fffbeb;
  border-left: 3px solid #f59e0b;
  border-radius: 0 8px 8px 0;
}

.api-doc-content :deep(code) {
  padding: 1px 6px;
  font-size: 13px;
  color: #be123c;
  background: #f1f5f9;
  border-radius: 5px;
}

.api-doc-content :deep(pre code) {
  display: block;
  padding: 14px 16px;
  overflow-x: auto;
  line-height: 1.6;
  color: #d4d4d4;
  background: #282c34;
  border-radius: 12px;
}

.api-doc-content :deep(table) {
  width: auto;
  min-width: 50%;
  margin: 10px 0;
  border-collapse: collapse;
}

.api-doc-content :deep(th),
.api-doc-content :deep(td) {
  padding: 7px 12px;
  text-align: left;
  border: 1px solid var(--line-soft);
}

.api-doc-content :deep(th) {
  font-weight: 600;
  background: #f8fafc;
}

.api-doc-content :deep(tr:nth-child(even)) {
  background: #fbfcfe;
}

@media (max-width: 920px) {
  .doc-head {
    padding: 0 16px;
  }

  .doc-main {
    padding: 16px;
  }

  .doc-layout {
    flex-direction: column;
  }

  .doc-aside {
    position: static;
    width: 100%;
  }

  .toc-list {
    max-height: 240px;
  }

  .doc-surface {
    width: 100%;
    max-width: none;
    padding: 20px;
  }
}
</style>
