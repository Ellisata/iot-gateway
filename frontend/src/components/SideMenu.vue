<template>
  <el-aside
    :width="collapsed ? '64px' : '240px'"
    class="flex flex-col transition-all duration-300"
    style="background-color: #304156"
  >
    <!-- Logo 区域 -->
    <div class="flex items-center justify-center h-[60px] text-white text-lg font-bold">
      <span v-if="!collapsed" class="tracking-wider">IoT Admin</span>
      <span v-else class="text-2xl">IoT</span>
    </div>

    <!-- 导航菜单 -->
    <el-menu
      :default-active="route.path"
      :collapse="collapsed"
      :collapse-transition="false"
      background-color="#304156"
      text-color="#bfcbd9"
      active-text-color="#409eff"
      router
      class="flex-1 border-r-0"
    >
      <el-menu-item index="/dashboard">
        <el-icon><Odometer /></el-icon>
        <template #title>首页</template>
      </el-menu-item>

      <!-- 工业物联网 -->
      <el-sub-menu index="/industrial">
        <template #title>
          <el-icon><Monitor /></el-icon>
          <span>工业物联网</span>
        </template>
        <el-menu-item index="/industrial/device-object">
          <el-icon><Cpu /></el-icon>
          <template #title>设备对象管理</template>
        </el-menu-item>
        <el-menu-item index="/industrial/protocol">
          <el-icon><List /></el-icon>
          <template #title>协议管理</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/industrial/protocol-form">
          <el-icon><Document /></el-icon>
          <template #title>协议表单配置</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 数据推送 -->
      <el-sub-menu index="/data-push">
        <template #title>
          <el-icon><Promotion /></el-icon>
          <span>数据推送</span>
        </template>
        <el-menu-item index="/data-push/channel">
          <el-icon><Connection /></el-icon>
          <template #title>通道管理</template>
        </el-menu-item>
        <el-menu-item v-if="userStore.isAdmin" index="/data-push/channel-form">
          <el-icon><Document /></el-icon>
          <template #title>通道表单管理</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 报警管理 -->
      <el-sub-menu index="/alarm">
        <template #title>
          <el-icon><Bell /></el-icon>
          <span>报警管理</span>
        </template>
        <el-menu-item index="/alarm/device">
          <el-icon><Monitor /></el-icon>
          <template #title>设备报警</template>
        </el-menu-item>
        <el-menu-item index="/alarm/channel">
          <el-icon><Connection /></el-icon>
          <template #title>通道报警</template>
        </el-menu-item>
      </el-sub-menu>

      <!-- 日志管理 -->
      <el-sub-menu index="/log">
        <template #title>
          <el-icon><Memo /></el-icon>
          <span>日志管理</span>
        </template>
        <el-menu-item index="/log/file">
          <el-icon><Tickets /></el-icon>
          <template #title>日志文件查看</template>
        </el-menu-item>
      </el-sub-menu>

    </el-menu>
  </el-aside>
</template>

<script setup>
import { useRoute } from 'vue-router'
import { useUserStore } from '@/store'

defineProps({
  collapsed: {
    type: Boolean,
    default: false,
  },
})

const route = useRoute()
const userStore = useUserStore()
</script>
