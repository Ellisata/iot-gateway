<template>
  <div class="relative min-h-screen flex items-center justify-center overflow-hidden bg-gradient-to-br from-slate-900 via-blue-950 to-indigo-950">
    <!-- 背景装饰光晕 -->
    <div class="absolute inset-0 overflow-hidden pointer-events-none">
      <!-- 右上角大光圈 -->
      <div
        class="absolute -top-40 -right-40 w-[500px] h-[500px] rounded-full blur-[120px]"
        style="background: radial-gradient(circle, rgba(59,130,246,0.12) 0%, transparent 70%)"
      />
      <!-- 左下角大光圈 -->
      <div
        class="absolute -bottom-40 -left-40 w-[600px] h-[600px] rounded-full blur-[120px]"
        style="background: radial-gradient(circle, rgba(99,102,241,0.10) 0%, transparent 70%)"
      />
      <!-- 中间柔和光晕 -->
      <div
        class="absolute top-1/3 left-1/2 -translate-x-1/2 w-[400px] h-[400px] rounded-full blur-[100px]"
        style="background: radial-gradient(circle, rgba(168,85,247,0.06) 0%, transparent 70%)"
      />
    </div>

    <!-- 细微点阵纹理 -->
    <div class="absolute inset-0 opacity-[0.03] pointer-events-none"
      style="background-image: radial-gradient(circle, #fff 1px, transparent 1px); background-size: 32px 32px"
    />

    <!-- 顶部渐变线 -->
    <div class="absolute top-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-blue-400/20 to-transparent" />

    <!-- 内容区 -->
    <router-view v-slot="{ Component }">
      <transition name="page-fade" mode="out-in">
        <component :is="Component" />
      </transition>
    </router-view>

    <!-- 底部信息 -->
    <div class="absolute bottom-8 text-center w-full pointer-events-none">
      <p class="text-white/15 text-xs tracking-[0.2em] select-none">
        © 2026 IoT Admin Platform. All rights reserved.
      </p>
    </div>
  </div>
</template>

<script setup>
// 认证布局：优雅深色渐变背景，用于登录/注册页
</script>

<style scoped>
.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity 0.35s ease, transform 0.35s ease;
}

.page-fade-enter-from {
  opacity: 0;
  transform: translateY(12px);
}

.page-fade-leave-to {
  opacity: 0;
  transform: translateY(-12px);
}
</style>
