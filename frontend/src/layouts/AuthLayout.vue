<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="auth-shell">
    <!-- 背景：柔和光晕 + 细网格 -->
    <div class="auth-atmosphere" aria-hidden="true">
      <div class="auth-glow auth-glow--a" />
      <div class="auth-glow auth-glow--b" />
      <div class="auth-grid" />
    </div>

    <!-- 顶栏：品牌 + 语言切换 -->
    <header class="auth-header">
      <div class="brand">
        <div class="brand-mark">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M12 2.5 2.5 7.5 12 12.5l9.5-5L12 2.5Z" />
            <path d="M2.5 12 12 17l9.5-5" />
            <path d="M2.5 16.5 12 21.5l9.5-5" />
          </svg>
        </div>
        <div class="brand-copy">
          <span class="brand-name">IoT Gateway</span>
          <span class="brand-divider" aria-hidden="true" />
          <span class="brand-eyebrow">{{ t('login.brandEyebrow') }}</span>
        </div>
      </div>

      <div class="locale-pill">
        <LocaleSwitcher />
      </div>
    </header>

    <!-- 内容区 -->
    <main class="auth-main">
      <router-view v-slot="{ Component }">
        <transition name="page-fade" mode="out-in">
          <component :is="Component" />
        </transition>
      </router-view>
    </main>

    <!-- 底栏 -->
    <footer class="auth-footer">
      <span>© 2026 IoT Gateway Platform. All rights reserved.</span>
    </footer>
  </div>
</template>

<script setup>
// 认证布局：浅色、克制的空间感，用于登录/注册页
import { useI18n } from 'vue-i18n'
import LocaleSwitcher from '@/components/LocaleSwitcher.vue'

const { t } = useI18n()
</script>

<style scoped>
/* ========== 外壳 ========== */
.auth-shell {
  position: relative;
  display: flex;
  flex-direction: column;
  min-height: 100vh;
  overflow: hidden;
  background: #f6f8fb;
}

/* ========== 背景 ========== */
.auth-atmosphere {
  position: absolute;
  inset: 0;
  pointer-events: none;
}

.auth-glow {
  position: absolute;
  border-radius: 9999px;
  filter: blur(100px);
}

.auth-glow--a {
  top: -320px;
  left: -220px;
  width: 720px;
  height: 720px;
  background: rgba(37, 99, 235, 0.10);
}

.auth-glow--b {
  right: -240px;
  bottom: -340px;
  width: 660px;
  height: 660px;
  background: rgba(14, 165, 233, 0.08);
}

.auth-grid {
  position: absolute;
  inset: 0;
  opacity: 0.55;
  background-image:
    linear-gradient(to right, rgba(15, 23, 42, 0.05) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(15, 23, 42, 0.05) 1px, transparent 1px);
  background-size: 56px 56px;
  mask-image: radial-gradient(ellipse 78% 70% at 50% 28%, #000 25%, transparent 78%);
  -webkit-mask-image: radial-gradient(ellipse 78% 70% at 50% 28%, #000 25%, transparent 78%);
}

/* ========== 顶栏 ========== */
.auth-header {
  position: relative;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 22px clamp(20px, 4vw, 48px) 10px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.brand-mark {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  flex: none;
  color: #2563eb;
  background: #fff;
  border: 1px solid #e4eaf2;
  border-radius: 10px;
  box-shadow: 0 6px 16px -10px rgba(15, 23, 42, 0.35);
}

.brand-mark svg {
  width: 19px;
  height: 19px;
}

.brand-copy {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.brand-name {
  font-size: 15px;
  font-weight: 650;
  letter-spacing: 0.01em;
  color: #0f172a;
  white-space: nowrap;
}

.brand-divider {
  width: 1px;
  height: 13px;
  background: #d8e0ea;
}

.brand-eyebrow {
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: #8b9aae;
  white-space: nowrap;
}

.locale-pill {
  flex: none;
  padding: 6px 12px;
  color: #475569;
  background: rgba(255, 255, 255, 0.72);
  border: 1px solid #e4eaf2;
  border-radius: 999px;
  transition: border-color 0.2s ease, background 0.2s ease;
}

.locale-pill:hover {
  background: #fff;
  border-color: #cfdae7;
}

/* ========== 内容区 ========== */
.auth-main {
  position: relative;
  z-index: 1;
  display: flex;
  flex: 1;
  align-items: center;
  justify-content: center;
  padding: 12px clamp(20px, 4vw, 48px) 4px;
}

/* ========== 底栏 ========== */
.auth-footer {
  position: relative;
  z-index: 1;
  padding: 14px 20px 18px;
  text-align: center;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: #a1adbd;
  user-select: none;
}

/* ========== 页面切换 ========== */
.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity 0.32s ease, transform 0.32s ease;
}

.page-fade-enter-from {
  opacity: 0;
  transform: translateY(10px);
}

.page-fade-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}

/* 桌面端左侧叙事已展示定位语，头部不再重复 */
@media (min-width: 1024px) {
  .brand-eyebrow,
  .brand-divider {
    display: none;
  }
}

@media (max-width: 640px) {
  .brand-eyebrow,
  .brand-divider {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .page-fade-enter-active,
  .page-fade-leave-active {
    transition: none;
  }
}
</style>
