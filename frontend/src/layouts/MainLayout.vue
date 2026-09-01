<template>
  <div class="h-screen flex">
    <!-- 侧边栏导航 -->
    <SideMenu :collapsed="isCollapsed" />

    <!-- 右侧主区域 -->
    <el-container class="flex-1 flex flex-col">
      <!-- 顶部导航栏 -->
      <el-header class="flex items-center justify-between px-6 border-b bg-white" style="height: 60px">
        <div class="flex items-center gap-4">
          <el-icon
            class="cursor-pointer text-lg hover:text-[#409eff] transition-colors"
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
            <span class="flex items-center gap-2 cursor-pointer hover:text-[#409eff]">
              <el-avatar :size="32" icon="UserFilled" />
              <span class="text-sm">{{ userStore.userName || t('layout.admin') }}</span>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="profile">{{ t('layout.profile') }}</el-dropdown-item>
                <el-dropdown-item divided command="logout">{{ t('layout.logout') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <!-- 主体内容 -->
      <el-main class="bg-[#f0f2f5] p-6 overflow-auto">
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
