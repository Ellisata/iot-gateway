<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <div class="login-layout">
    <!-- ================= 左侧：产品定位 ================= -->
    <section class="story">
      <p class="story-eyebrow">
        <span class="eyebrow-dot" aria-hidden="true" />
        {{ t('login.brandEyebrow') }}
      </p>

      <h1 class="story-title">{{ t('login.brandTitle') }}</h1>

      <p class="story-desc">{{ t('login.brandDesc') }}</p>

      <!-- 设备拓扑示意 -->
      <div class="topology" aria-hidden="true">
        <svg
          class="topology-svg"
          viewBox="0 0 560 360"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <!-- 连接线 -->
          <g class="links">
            <path class="link" d="M108 86 C 170 102, 214 138, 250 166" />
            <path class="link" d="M74 192 C 140 192, 198 189, 246 185" />
            <path class="link" d="M124 282 C 174 262, 214 228, 249 203" />
            <path class="link" d="M310 166 C 348 138, 396 108, 452 87" />
            <path class="link" d="M314 185 C 366 189, 434 192, 486 192" />
            <path class="link" d="M311 203 C 350 230, 398 266, 440 286" />
          </g>

          <!-- 数据流（沿线流动的光点） -->
          <g class="flows">
            <path class="flow" d="M108 86 C 170 102, 214 138, 250 166" />
            <path class="flow flow--d1" d="M74 192 C 140 192, 198 189, 246 185" />
            <path class="flow flow--d2" d="M124 282 C 174 262, 214 228, 249 203" />
            <path class="flow flow--d3" d="M310 166 C 348 138, 396 108, 452 87" />
            <path class="flow flow--d4" d="M314 185 C 366 189, 434 192, 486 192" />
            <path class="flow flow--d5" d="M311 203 C 350 230, 398 266, 440 286" />
          </g>

          <!-- 协议标注 -->
          <g class="protocol-labels">
            <text x="172" y="112">S7 / CIP</text>
            <text x="158" y="181">MODBUS RTU</text>
            <text x="170" y="258">MODBUS TCP</text>
            <text x="384" y="116">OPC UA</text>
            <text x="388" y="181">MQTT</text>
            <text x="378" y="258">HTTP</text>
          </g>

          <!-- 现场侧节点 -->
          <g class="node-group">
            <circle cx="90" cy="76" r="18" fill="#fff" stroke="#dce4ee" />
            <circle cx="90" cy="76" r="4" fill="#94a3b8" />
            <text class="node-label" x="90" y="110" text-anchor="middle">PLC</text>

            <circle cx="56" cy="192" r="18" fill="#fff" stroke="#dce4ee" />
            <circle cx="56" cy="192" r="4" fill="#94a3b8" />
            <text class="node-label" x="56" y="226" text-anchor="middle">METER</text>

            <circle cx="108" cy="296" r="18" fill="#fff" stroke="#dce4ee" />
            <circle cx="108" cy="296" r="4" fill="#94a3b8" />
            <text class="node-label" x="108" y="330" text-anchor="middle">SENSOR</text>
          </g>

          <!-- 北向节点 -->
          <g class="node-group">
            <circle cx="470" cy="76" r="18" fill="#fff" stroke="#d7e3f2" />
            <circle cx="470" cy="76" r="4" fill="#60a5fa" />
            <text class="node-label" x="470" y="110" text-anchor="middle">SCADA</text>

            <circle cx="504" cy="192" r="18" fill="#fff" stroke="#d7e3f2" />
            <circle cx="504" cy="192" r="4" fill="#60a5fa" />
            <text class="node-label" x="504" y="226" text-anchor="middle">CLOUD</text>

            <circle cx="456" cy="296" r="18" fill="#fff" stroke="#d7e3f2" />
            <circle cx="456" cy="296" r="4" fill="#60a5fa" />
            <text class="node-label" x="456" y="330" text-anchor="middle">DATABASE</text>
          </g>

          <!-- 中心网关 -->
          <g class="gateway">
            <circle class="gateway-orbit" cx="280" cy="180" r="52" />
            <circle cx="280" cy="180" r="42" fill="rgba(37, 99, 235, 0.05)" />
            <circle cx="280" cy="180" r="31" fill="#fff" stroke="#dbe4f0" />
            <g
              transform="translate(280, 180)"
              stroke="#2563eb"
              stroke-width="1.5"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <path d="M0 -10 L11 -4 L0 2 L-11 -4 Z" />
              <path d="M-11 2 L0 8 L11 2" />
              <path d="M-11 8 L0 14 L11 8" />
            </g>
            <text class="gateway-label" x="280" y="246" text-anchor="middle">GATEWAY</text>
          </g>
        </svg>
      </div>

      <!-- 能力锚点 -->
      <ul class="story-features">
        <li>
          <span class="feature-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="6" cy="12" r="2.4" />
              <circle cx="18" cy="5.5" r="2.4" />
              <circle cx="18" cy="18.5" r="2.4" />
              <path d="m8.2 10.9 7.6-4.3M8.2 13.1l7.6 4.3" />
            </svg>
          </span>
          {{ t('login.featureProtocols') }}
        </li>
        <li>
          <span class="feature-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
              <rect x="7" y="7" width="10" height="10" rx="2" />
              <rect x="10.5" y="10.5" width="3" height="3" rx="0.6" />
              <path d="M10 3v2M14 3v2M10 19v2M14 19v2M3 10h2M3 14h2M19 10h2M19 14h2" />
            </svg>
          </span>
          {{ t('login.featureEdge') }}
        </li>
        <li>
          <span class="feature-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
              <ellipse cx="12" cy="5.5" rx="7" ry="2.8" />
              <path d="M5 5.5v13c0 1.55 3.13 2.8 7 2.8s7-1.25 7-2.8v-13" />
              <path d="M5 12c0 1.55 3.13 2.8 7 2.8s7-1.25 7-2.8" />
            </svg>
          </span>
          {{ t('login.featureStorage') }}
        </li>
      </ul>
    </section>

    <!-- ================= 右侧：登录卡片 ================= -->
    <section class="panel-side">
      <div class="login-panel">
        <h2 class="panel-title">{{ t('login.title') }}</h2>
        <p class="panel-subtitle">{{ t('login.subtitle') }}</p>

        <el-form
          ref="formRef"
          :model="form"
          :rules="rules"
          size="large"
          label-position="top"
          class="login-form"
          @submit.prevent="handleLogin"
        >
          <el-form-item prop="username" :label="t('login.usernameLabel')">
            <el-input
              ref="usernameRef"
              v-model="form.username"
              :prefix-icon="User"
              :aria-label="t('login.usernameLabel')"
              autocomplete="username"
              class="custom-input"
              clearable
            />
          </el-form-item>

          <el-form-item prop="password" :label="t('login.passwordLabel')">
            <el-input
              ref="passwordRef"
              v-model="form.password"
              type="password"
              :prefix-icon="Lock"
              :aria-label="t('login.passwordLabel')"
              autocomplete="current-password"
              show-password
              class="custom-input"
              @keyup="checkCapslock"
            />
            <transition name="caps-tip">
              <p v-if="capsWarning" class="caps-hint">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                  <path d="M7 11V7a5 5 0 0 1 10 0v4" />
                </svg>
                {{ t('login.capsLock') }}
              </p>
            </transition>
          </el-form-item>

          <el-form-item class="submit-item">
            <el-button
              type="primary"
              native-type="submit"
              class="login-btn"
              :loading="loading"
              :disabled="loading"
            >
              <span v-if="!loading" class="btn-label">
                {{ t('login.loginBtn') }}
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M5 12h13M13 6l6 6-6 6" />
                </svg>
              </span>
            </el-button>
          </el-form-item>
        </el-form>

        <div class="secure-note">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M12 3 5 6v5c0 4.5 3 7.7 7 9 4-1.3 7-4.5 7-9V6l-7-3Z" />
            <path d="m9.2 12 2 2 3.6-3.8" />
          </svg>
          <span>{{ t('login.secureNote') }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { IconLock as Lock, IconUser as User } from '@tabler/icons-vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { useUserStore } from '@/store'
import { loginApi } from '@/api/modules/user'
import { rsaEncrypt } from '@/utils/rsa'

const { t } = useI18n()
const router = useRouter()
const userStore = useUserStore()
const formRef = ref(null)
const usernameRef = ref(null)
const passwordRef = ref(null)
const loading = ref(false)
const capsWarning = ref(false)

const form = reactive({
  username: '',
  password: '',
})

// 校验文案随语言切换实时更新
const rules = computed(() => ({
  username: [
    { required: true, message: t('login.usernameRequired'), trigger: 'blur' },
    { min: 2, max: 32, message: t('login.usernameLength'), trigger: 'blur' },
  ],
  password: [
    { required: true, message: t('login.passwordRequired'), trigger: 'blur' },
    { min: 6, max: 64, message: t('login.passwordMinLength'), trigger: 'blur' },
  ],
}))

onMounted(() => {
  nextTick(() => usernameRef.value?.focus())
})

/** 检测 Caps Lock */
function checkCapslock(e) {
  capsWarning.value = e.getModifierState?.('CapsLock') ?? false
}

/** 登录 */
async function handleLogin() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  loading.value = true

  try {
    // 使用 RSA 加密后再传输
    const encryptedUsername = await rsaEncrypt(form.username)
    const encryptedPassword = await rsaEncrypt(form.password)
    const { accessToken } = await loginApi({
      username: encryptedUsername,
      password: encryptedPassword,
    })

    userStore.setToken(accessToken)
    userStore.setUserInfo({
      name: form.username,
      role: form.username === 'admin' ? 'admin' : 'user',
    })

    ElMessage.success({
      message: t('login.success'),
      duration: 1500,
    })

    router.push('/dashboard')
  } catch (err) {
    const errorMsg = err?.message || String(err) || t('login.failed')
    ElMessage.error(errorMsg)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
/* ========== 整体版式 ========== */
.login-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(360px, 420px);
  align-items: center;
  gap: clamp(40px, 6vw, 96px);
  width: 100%;
  max-width: 1160px;
  padding: 8px 0 24px;
}

/* ========== 左侧：品牌叙事 ========== */
.story {
  max-width: 560px;
  animation: rise-in 0.7s cubic-bezier(0.16, 1, 0.3, 1) both;
}

.story-eyebrow {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 0 0 18px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: #7c8ca3;
}

.eyebrow-dot {
  position: relative;
  width: 6px;
  height: 6px;
  flex: none;
  border-radius: 999px;
  background: #2563eb;
}

.eyebrow-dot::after {
  content: '';
  position: absolute;
  inset: -3px;
  border-radius: inherit;
  background: rgba(37, 99, 235, 0.18);
}

.story-title {
  margin: 0;
  font-size: clamp(30px, 3.2vw, 42px);
  font-weight: 650;
  line-height: 1.28;
  letter-spacing: -0.015em;
  color: #0f172a;
  white-space: pre-line;
}

.story-desc {
  max-width: 460px;
  margin: 18px 0 0;
  font-size: 14px;
  line-height: 1.9;
  color: #64748b;
}

/* ========== 拓扑图 ========== */
.topology {
  width: 100%;
  max-width: 500px;
  margin-top: 20px;
}

.topology-svg {
  display: block;
  width: 100%;
  height: auto;
}

.link {
  stroke: #d9e2ec;
  stroke-width: 1.25;
}

.flow {
  stroke: #3b82f6;
  stroke-width: 2;
  stroke-linecap: round;
  stroke-dasharray: 1 22;
  animation: packet-flow 4.2s linear infinite;
}

.flow--d1 { animation-delay: -0.7s; }
.flow--d2 { animation-delay: -1.4s; }
.flow--d3 { animation-delay: -2.1s; }
.flow--d4 { animation-delay: -2.8s; }
.flow--d5 { animation-delay: -3.5s; }

.protocol-labels text {
  font-size: 8.5px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-anchor: middle;
  fill: #9aa9bc;
  stroke: #f6f8fb;
  stroke-width: 4px;
  paint-order: stroke;
}

.node-label,
.gateway-label {
  font-size: 8.5px;
  font-weight: 600;
  letter-spacing: 0.14em;
  fill: #9aa9bc;
}

.gateway-orbit {
  stroke: #dbe4f0;
  stroke-width: 1;
  stroke-dasharray: 2 7;
  transform-origin: 280px 180px;
  animation: orbit 80s linear infinite;
}

/* ========== 能力锚点 ========== */
.story-features {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 28px;
  margin: 20px 0 0;
  padding: 18px 0 0;
  list-style: none;
  border-top: 1px solid rgba(15, 23, 42, 0.07);
}

.story-features li {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  font-weight: 500;
  letter-spacing: 0.02em;
  color: #475569;
}

.feature-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  flex: none;
  color: #3b82f6;
  background: #eff5ff;
  border-radius: 8px;
}

.feature-icon svg {
  width: 14px;
  height: 14px;
}

/* ========== 登录卡片 ========== */
.panel-side {
  width: 100%;
  animation: rise-in 0.7s cubic-bezier(0.16, 1, 0.3, 1) 0.08s both;
}

.login-panel {
  position: relative;
  width: 100%;
  padding: 36px 34px 28px;
  background: #fff;
  border: 1px solid #e6ebf2;
  border-radius: 22px;
  box-shadow:
    0 1px 2px rgba(15, 23, 42, 0.04),
    0 30px 70px -32px rgba(15, 23, 42, 0.20);
}

.login-panel::before {
  content: '';
  position: absolute;
  top: -1px;
  left: 34px;
  width: 52px;
  height: 2px;
  border-radius: 2px;
  background: linear-gradient(90deg, #2563eb, #60a5fa);
}

.panel-title {
  margin: 0;
  font-size: 23px;
  font-weight: 650;
  letter-spacing: -0.01em;
  color: #0f172a;
}

.panel-subtitle {
  margin: 8px 0 0;
  font-size: 13px;
  line-height: 1.6;
  color: #94a3b8;
}

/* ========== 表单 ========== */
.login-form {
  margin-top: 28px;
}

.login-form :deep(.el-form-item) {
  margin-bottom: 20px;
}

.login-form :deep(.el-form-item__label) {
  width: 100%;
  justify-content: flex-start;
  padding: 0 0 8px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.06em;
  line-height: 1.2;
  color: #64748b;
}

/* 隐藏必填星号，保持版面干净 */
.login-form :deep(.el-form-item.is-required:not(.is-no-asterisk) > .el-form-item__label::before),
.login-form :deep(.el-form-item.is-required:not(.is-no-asterisk) > .el-form-item__label::after) {
  display: none;
}

.login-form :deep(.el-form-item__error) {
  padding-top: 5px;
  font-size: 11.5px;
  color: #ef4444;
}

/* 输入框 */
.custom-input :deep(.el-input__wrapper) {
  padding: 1px 14px;
  background: #fff;
  border-radius: 12px;
  box-shadow: 0 0 0 1px #e2e8f0 inset;
  transition: box-shadow 0.22s ease, background 0.22s ease;
}

.custom-input :deep(.el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px #cbd5e1 inset;
}

.custom-input :deep(.el-input__wrapper.is-focus) {
  background: #fff;
  box-shadow:
    0 0 0 1px #2563eb inset,
    0 0 0 4px rgba(37, 99, 235, 0.10);
}

.custom-input :deep(.el-input__inner) {
  height: 44px;
  font-size: 14px;
  color: #1f2937;
}

.custom-input :deep(.el-input__inner::placeholder) {
  color: #a8b3c2;
}

.custom-input :deep(.el-input__prefix) {
  margin-right: 9px;
}

.custom-input :deep(.el-input__prefix-inner) {
  font-size: 16px;
  color: #9ca3af;
  transition: color 0.22s ease;
}

.custom-input :deep(.el-input__wrapper.is-focus) .el-input__prefix-inner {
  color: #2563eb;
}

.custom-input :deep(.el-input__clear),
.custom-input :deep(.el-input__suffix .el-input__password) {
  color: #9ca3af;
  font-size: 14px;
}

.custom-input :deep(.el-input__clear:hover),
.custom-input :deep(.el-input__suffix .el-input__password:hover) {
  color: #64748b;
}

/* 错误状态 */
.login-form :deep(.el-form-item.is-error .el-input__wrapper) {
  box-shadow: 0 0 0 1px #f87171 inset;
}

.login-form :deep(.el-form-item.is-error .el-input__wrapper.is-focus) {
  box-shadow:
    0 0 0 1px #ef4444 inset,
    0 0 0 4px rgba(239, 68, 68, 0.10);
}

/* ========== Caps Lock 提示 ========== */
.caps-hint {
  display: flex;
  align-items: center;
  gap: 5px;
  margin: 7px 0 0;
  font-size: 12px;
  line-height: 1.4;
  color: #d97706;
}

.caps-hint svg {
  width: 13px;
  height: 13px;
  flex: none;
}

.caps-tip-enter-active,
.caps-tip-leave-active {
  transition: opacity 0.25s ease, transform 0.25s ease;
}

.caps-tip-enter-from,
.caps-tip-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

/* ========== 登录按钮 ========== */
.submit-item {
  margin-top: 26px !important;
  margin-bottom: 0 !important;
}

.login-btn {
  width: 100%;
  height: 48px !important;
  border: none !important;
  border-radius: 12px !important;
  font-size: 14px !important;
  font-weight: 600 !important;
  letter-spacing: 0.08em;
  background: #2563eb !important;
  box-shadow: 0 12px 24px -12px rgba(37, 99, 235, 0.60) !important;
  transition: background 0.2s ease, transform 0.2s ease, box-shadow 0.2s ease !important;
}

.login-btn:hover:not(.is-disabled) {
  background: #1d4ed8 !important;
  transform: translateY(-1px);
  box-shadow: 0 16px 30px -14px rgba(37, 99, 235, 0.65) !important;
}

.login-btn:active:not(.is-disabled) {
  transform: translateY(0);
  box-shadow: 0 8px 18px -12px rgba(37, 99, 235, 0.55) !important;
}

.login-btn.is-loading {
  background: #3b82f6 !important;
}

.login-btn :deep(span) {
  width: 100%;
}

.btn-label {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.btn-label svg {
  width: 16px;
  height: 16px;
  transition: transform 0.2s ease;
}

.login-btn:hover .btn-label svg {
  transform: translateX(3px);
}

/* ========== 安全提示 ========== */
.secure-note {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin-top: 22px;
  font-size: 11.5px;
  letter-spacing: 0.02em;
  color: #a1adbd;
}

.secure-note svg {
  width: 13px;
  height: 13px;
  flex: none;
}

/* ========== 动画 ========== */
@keyframes rise-in {
  from {
    opacity: 0;
    transform: translateY(18px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes packet-flow {
  to {
    stroke-dashoffset: -260;
  }
}

@keyframes orbit {
  to {
    transform: rotate(360deg);
  }
}

/* ========== 响应式 ========== */
@media (max-width: 1023px) {
  .login-layout {
    grid-template-columns: 1fr;
    gap: 0;
    max-width: 440px;
  }

  .story {
    display: none;
  }
}

@media (max-width: 480px) {
  .login-panel {
    padding: 30px 24px 24px;
    border-radius: 18px;
  }

  .login-panel::before {
    left: 24px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .story,
  .panel-side {
    animation: none;
  }

  .flow,
  .gateway-orbit {
    animation: none;
  }
}
</style>
