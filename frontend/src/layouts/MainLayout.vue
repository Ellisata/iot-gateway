<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="h-screen flex">
    <!-- 侧边栏导航 -->
    <SideMenu :collapsed="isCollapsed" />

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
import { computed, ref } from 'vue'
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

// 面包屑标题：取路由 titleKey 实时翻译，切换语言时跟随更新
const pageTitle = computed(() =>
  route.meta.titleKey ? t(route.meta.titleKey) : ''
)

function toggleSidebar() {
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
</style>
