<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="h-screen flex">
    <!-- 侧边栏导航 -->
    <SideMenu :collapsed="isCollapsed" :width="sidebarWidth" :resizing="resizing" />

    <!-- 左右分割滑块：拖动调整菜单宽度，双击恢复默认；键盘左右键也可调整。
         用 -3px 的负外边距压在侧栏边线上，命中区域 6px 但不挤占内容宽度。 -->
    <div
      v-if="!isCollapsed"
      class="sidebar-resizer"
      :class="{ 'is-active': resizing }"
      role="separator"
      aria-orientation="vertical"
      :aria-label="t('layout.resizeSidebar')"
      :aria-valuemin="MIN_SIDEBAR_WIDTH"
      :aria-valuemax="MAX_SIDEBAR_WIDTH"
      :aria-valuenow="sidebarWidth"
      :title="t('layout.resizeSidebarHint')"
      tabindex="0"
      @pointerdown="startResize"
      @dblclick="resetSidebarWidth"
      @keydown="handleResizeKeydown"
    >
      <span class="sidebar-resizer-grip" aria-hidden="true" />
    </div>

    <!-- 右侧主区域 -->
    <el-container class="flex-1 flex flex-col">
      <!-- 顶部导航栏 -->
      <el-header class="header flex items-center justify-between px-6 bg-white" style="height: 60px">
        <div class="flex items-center gap-4">
          <el-icon
            class="cursor-pointer text-lg text-[#64748b] hover:text-[#2563eb] transition-colors"
            @click="toggleSidebar"
          >
            <Fold v-if="!isCollapsed" />
            <Expand v-else />
          </el-icon>
          <el-breadcrumb separator="/">
            <el-breadcrumb-item v-if="pageTitle">{{ pageTitle }}</el-breadcrumb-item>
          </el-breadcrumb>
        </div>

        <div class="flex items-center gap-4">
          <!-- 语言切换 -->
          <LocaleSwitcher />

          <el-dropdown trigger="click" @command="handleCommand">
            <span class="flex items-center gap-2 cursor-pointer text-[#334155] hover:text-[#2563eb] transition-colors">
              <el-avatar :size="32" class="user-avatar">
                <el-icon><IconUserFilled /></el-icon>
              </el-avatar>
              <span class="text-sm font-medium">{{ userStore.userName || t('layout.admin') }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="logout">{{ t('layout.logout') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <!-- 主体内容 -->
      <el-main class="bg-[#f6f8fb] p-6 overflow-auto">
        <router-view />
      </el-main>
    </el-container>
  </div>
</template>

<script setup>
import { computed, ref, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/store'
import SideMenu from '@/components/SideMenu.vue'
import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
import {
  IconChevronDown as ArrowDown,
  IconLayoutSidebarLeftCollapse as Fold,
  IconLayoutSidebarLeftExpand as Expand,
  IconUserFilled,
} from '@tabler/icons-vue'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()
const { t } = useI18n()

const isCollapsed = ref(false)

// ---------- 侧栏宽度（拖动分割滑块） ----------
const DEFAULT_SIDEBAR_WIDTH = 240
const MIN_SIDEBAR_WIDTH = 200
const MAX_SIDEBAR_WIDTH = 380
const SIDEBAR_WIDTH_STORAGE_KEY = 'iot-gateway.sidebar-width'

function clampSidebarWidth(value) {
  if (value === null || value === undefined || value === '') return DEFAULT_SIDEBAR_WIDTH
  const n = Number(value)
  if (!Number.isFinite(n)) return DEFAULT_SIDEBAR_WIDTH
  return Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, Math.round(n)))
}

// 记住上一次的宽度：现场屏幕尺寸差异很大，每次都从 240px 重新拖一遍很烦。
// 隐私模式/存储被禁用时静默回落到默认值，不影响本次调整。
function readSidebarWidth() {
  try {
    return clampSidebarWidth(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY))
  } catch {
    return DEFAULT_SIDEBAR_WIDTH
  }
}

const sidebarWidth = ref(readSidebarWidth())
const resizing = ref(false)

let resizeStartX = 0
let resizeStartWidth = DEFAULT_SIDEBAR_WIDTH

function persistSidebarWidth() {
  try {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(sidebarWidth.value))
  } catch {
    // 仅影响下次打开时的记忆
  }
}

function startResize(event) {
  if (isCollapsed.value || event.button !== 0) return
  resizing.value = true
  resizeStartX = event.clientX
  resizeStartWidth = sidebarWidth.value
  // 不 preventDefault：双击恢复默认依赖原生 dblclick；拖拽时的文本选中
  // 由 body 上的 user-select:none 关掉。捕获指针，移出滑块也继续收到移动事件。
  event.currentTarget.setPointerCapture?.(event.pointerId)
  document.body.classList.add('is-resizing-sidebar')
  window.addEventListener('pointermove', handleResizeMove)
  window.addEventListener('pointerup', stopResize)
  window.addEventListener('pointercancel', stopResize)
  // 指针在窗口外松开时收不到 pointerup，失焦也要收尾
  window.addEventListener('blur', stopResize)
}

function handleResizeMove(event) {
  if (!resizing.value) return
  sidebarWidth.value = clampSidebarWidth(resizeStartWidth + event.clientX - resizeStartX)
}

function stopResize() {
  if (!resizing.value) return
  resizing.value = false
  document.body.classList.remove('is-resizing-sidebar')
  window.removeEventListener('pointermove', handleResizeMove)
  window.removeEventListener('pointerup', stopResize)
  window.removeEventListener('pointercancel', stopResize)
  window.removeEventListener('blur', stopResize)
  persistSidebarWidth()
}

function resetSidebarWidth() {
  sidebarWidth.value = DEFAULT_SIDEBAR_WIDTH
  persistSidebarWidth()
}

// 键盘可达：焦点落在滑块上时，左右键微调（Shift 加速），Home/End 到两端
function handleResizeKeydown(event) {
  if (isCollapsed.value) return
  const step = event.shiftKey ? 32 : 12
  let next = null
  if (event.key === 'ArrowLeft') next = sidebarWidth.value - step
  else if (event.key === 'ArrowRight') next = sidebarWidth.value + step
  else if (event.key === 'Home') next = MIN_SIDEBAR_WIDTH
  else if (event.key === 'End') next = MAX_SIDEBAR_WIDTH
  else return
  event.preventDefault()
  sidebarWidth.value = clampSidebarWidth(next)
  persistSidebarWidth()
}

onBeforeUnmount(() => {
  stopResize()
})

// 面包屑标题：取路由 titleKey 实时翻译，切换语言时跟随更新
const pageTitle = computed(() =>
  route.meta.titleKey ? t(route.meta.titleKey) : ''
)

function toggleSidebar() {
  // 拖动途中点了折叠按钮：先收尾，别把 document 的拖拽态留下
  if (resizing.value) stopResize()
  isCollapsed.value = !isCollapsed.value
}

function handleCommand(command) {
  if (command === 'logout') {
    userStore.logout()
    router.push('/login')
  }
}
</script>

<style scoped>
/* 顶栏用一条与内容区同色的细线收边，避免和浅灰画布糊在一起 */
.header {
  border-bottom: 1px solid #e6ebf2;
}

.header :deep(.el-breadcrumb__inner) {
  font-size: 13px;
  font-weight: 500;
  color: #64748b;
}

.user-avatar {
  color: #2563eb;
  background: #eff5ff;
  box-shadow: 0 0 0 3px #f4f7fc;
  transition: box-shadow 0.2s ease;
}

.user-avatar:hover {
  box-shadow: 0 0 0 3px #e7effc;
}

/* ---------- 左右分割滑块 ----------
   6px 命中区用负外边距压在侧栏边线上，不改变原本 240px 的布局宽度；
   平时只留一条极淡的边线，悬停/聚焦/拖动时才亮成品牌蓝并露出短握把。 */
.sidebar-resizer {
  position: relative;
  z-index: 20;
  flex: none;
  width: 6px;
  margin: 0 -3px;
  cursor: col-resize;
  touch-action: none;
  outline: none;
}

.sidebar-resizer::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: 50%;
  width: 1px;
  background: rgba(148, 163, 184, 0.18);
  transform: translateX(-50%);
  transition: width 0.16s ease, background-color 0.16s ease, box-shadow 0.16s ease;
}

.sidebar-resizer-grip {
  position: absolute;
  top: 50%;
  left: 50%;
  width: 3px;
  height: 30px;
  background: rgba(147, 197, 253, 0.55);
  border-radius: 999px;
  opacity: 0;
  transform: translate(-50%, -50%);
  transition: opacity 0.16s ease;
}

.sidebar-resizer:hover::before,
.sidebar-resizer:focus-visible::before,
.sidebar-resizer.is-active::before {
  width: 2px;
  background: #60a5fa;
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.12);
}

.sidebar-resizer:hover .sidebar-resizer-grip,
.sidebar-resizer:focus-visible .sidebar-resizer-grip,
.sidebar-resizer.is-active .sidebar-resizer-grip {
  opacity: 1;
}

.sidebar-resizer:focus-visible {
  border-radius: 4px;
  box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.45);
}

@media (prefers-reduced-motion: reduce) {
  .sidebar-resizer::before,
  .sidebar-resizer-grip {
    transition: none;
  }
}
</style>
