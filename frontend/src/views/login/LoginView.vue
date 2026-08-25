<template>
  <div class="w-full max-w-[420px] px-4 animate-card-enter">
    <div class="login-card-wrapper">
      <!-- 顶部渐变色装饰条 -->
      <div class="absolute top-0 left-6 right-6 h-[2px] bg-gradient-to-r from-sky-400 via-blue-500 to-indigo-500 rounded-full" />

      <!-- Logo & 标题 -->
      <div class="flex flex-col items-center mb-9 mt-3">
        <div
          class="w-14 h-14 bg-gradient-to-br from-blue-500 to-indigo-600 rounded-2xl flex items-center justify-center
                 shadow-lg shadow-blue-500/20 mb-4 ring-1 ring-white/10"
        >
          <!-- IoT 三层网络图标 -->
          <svg
            class="w-7 h-7 text-white"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M12 2L2 7l10 5 10-5-10-5z" />
            <path d="M2 17l10 5 10-5" />
            <path d="M2 12l10 5 10-5" />
          </svg>
        </div>
        <h1 class="text-2xl font-bold text-gray-900 tracking-tight">IoT Admin</h1>
        <p class="text-sm text-gray-400 mt-1.5">设备管理后台 · 请登录您的账号</p>
      </div>

      <!-- 登录表单 -->
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        size="large"
        class="login-form"
        @submit.prevent="handleLogin"
        @keydown="handleKeydown"
      >
        <el-form-item prop="username">
          <el-input
            v-model="form.username"
            placeholder="请输入用户名"
            :prefix-icon="User"
            class="custom-input"
            clearable
          />
        </el-form-item>

        <el-form-item prop="password">
          <el-input
            ref="passwordRef"
            v-model="form.password"
            type="password"
            placeholder="请输入密码"
            :prefix-icon="Lock"
            show-password
            class="custom-input"
            @keyup="checkCapslock"
          />
          <!-- Caps Lock 提示 -->
          <transition name="caps-tip">
            <p v-if="capsWarning" class="caps-hint">
              <svg class="w-3.5 h-3.5 inline-block mr-1 -mt-0.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
              大写锁定已开启 — 请注意密码大小写
            </p>
          </transition>
        </el-form-item>

        <!-- 登录按钮 -->
        <el-form-item>
          <el-button
            type="primary"
            native-type="submit"
            class="login-btn"
            :loading="loading"
            :disabled="loading"
          >
            <span v-if="!loading">登 录</span>
          </el-button>
        </el-form-item>
      </el-form>

    </div>
  </div>
</template>

<script setup>
import { ref, reactive, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { User, Lock } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useUserStore } from '@/store'
import { loginApi } from '@/api/modules/user'
import { rsaEncrypt } from '@/utils/rsa'

const router = useRouter()
const userStore = useUserStore()
const formRef = ref(null)
const passwordRef = ref(null)
const loading = ref(false)
const capsWarning = ref(false)

const form = reactive({
  username: '',
  password: '',
})

const rules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { min: 2, max: 32, message: '用户名长度为 2~32 个字符', trigger: 'blur' },
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    { min: 6, max: 64, message: '密码长度不少于 6 位', trigger: 'blur' },
  ],
}

/** 检测 Caps Lock */
function checkCapslock(e) {
  capsWarning.value = e.getModifierState?.('CapsLock') ?? false
}

/** 全局键盘事件中处理 Caps Lock */
function handleKeydown(e) {
  if (e.key === 'CapsLock') {
    // 交由 password 输入框的 keyup 事件处理即可
  }
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
      message: '登录成功',
      duration: 1500,
    })

    router.push('/dashboard')
  } catch (err) {
    const errorMsg = err?.message || String(err) || '登录失败，请重试'
    ElMessage.error(errorMsg)
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
/* ========== 卡片主体 ========== */
.login-card-wrapper {
  position: relative;
  background: #fff;
  border-radius: 20px;
  padding: 40px 36px 32px;
  box-shadow:
    0 20px 60px rgba(0, 0, 0, 0.08),
    0 8px 24px rgba(0, 0, 0, 0.04),
    0 0 0 1px rgba(255, 255, 255, 0.05) inset;
  transition: box-shadow 0.3s ease;
}

.login-card-wrapper:hover {
  box-shadow:
    0 24px 80px rgba(0, 0, 0, 0.10),
    0 8px 28px rgba(0, 0, 0, 0.05);
}

/* ========== 入场动画 ========== */
.animate-card-enter {
  animation: cardFadeSlide 0.6s cubic-bezier(0.16, 1, 0.3, 1) both;
}

@keyframes cardFadeSlide {
  from {
    opacity: 0;
    transform: translateY(24px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

/* ========== Caps Lock 提示 ========== */
.caps-hint {
  display: flex;
  align-items: center;
  margin-top: 6px;
  font-size: 12px;
  color: #e6a23c;
  line-height: 1.4;
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

/* ========== Element Plus 样式覆写 ========== */
/* 表单项间距 */
.login-form :deep(.el-form-item) {
  margin-bottom: 22px;
}

.login-form :deep(.el-form-item:last-child) {
  margin-bottom: 0;
}

/* 输入框 */
.custom-input :deep(.el-input__wrapper) {
  background: #f7f8fa;
  border: 1.5px solid transparent;
  border-radius: 12px;
  padding: 4px 12px;
  box-shadow: none;
  transition: all 0.25s ease;
}

.custom-input :deep(.el-input__wrapper:hover) {
  background: #f0f2f5;
  border-color: #d9dde4;
}

.custom-input :deep(.el-input__wrapper.is-focus) {
  background: #fff;
  border-color: #3b82f6;
  box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.10);
}

.custom-input :deep(.el-input__inner) {
  font-size: 14px;
  color: #1f2937;
  height: 42px;
}

.custom-input :deep(.el-input__inner::placeholder) {
  color: #9ca3af;
}

.custom-input :deep(.el-input__prefix) {
  margin-right: 8px;
}

.custom-input :deep(.el-input__prefix-inner) {
  color: #9ca3af;
  font-size: 16px;
}

.custom-input :deep(.el-input__wrapper.is-focus) .el-input__prefix-inner {
  color: #3b82f6;
}

/* 清除按钮 */
.custom-input :deep(.el-input__clear) {
  color: #9ca3af;
  font-size: 14px;
}

.custom-input :deep(.el-input__clear:hover) {
  color: #6b7280;
}

/* 密码可见切换 */
.custom-input :deep(.el-input__suffix .el-input__password) {
  color: #9ca3af;
}

/* 错误状态 */
.custom-input.is-error :deep(.el-input__wrapper) {
  background: #fef2f2;
  border-color: #ef4444;
}

.custom-input.is-error :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 3px rgba(239, 68, 68, 0.10);
}

/* 错误提示文字 */
.login-form :deep(.el-form-item__error) {
  padding-top: 4px;
  font-size: 12px;
  color: #ef4444;
}

/* ========== 登录按钮 ========== */
.login-btn {
  width: 100%;
  height: 46px !important;
  font-size: 15px !important;
  font-weight: 600 !important;
  letter-spacing: 0.05em;
  border: none !important;
  border-radius: 12px !important;
  background: linear-gradient(135deg, #3b82f6 0%, #2563eb 50%, #1d4ed8 100%) !important;
  transition: all 0.3s ease !important;
  position: relative;
  overflow: hidden;
}

.login-btn::before {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(135deg, #2563eb 0%, #1d4ed8 50%, #1e40af 100%);
  opacity: 0;
  transition: opacity 0.3s ease;
  border-radius: inherit;
}

.login-btn:hover:not(:disabled) {
  transform: translateY(-1px);
  box-shadow: 0 8px 24px rgba(59, 130, 246, 0.35) !important;
}

.login-btn:hover:not(:disabled)::before {
  opacity: 1;
}

.login-btn:active:not(:disabled) {
  transform: translateY(0);
  box-shadow: 0 4px 12px rgba(59, 130, 246, 0.25) !important;
}

.login-btn:disabled {
  opacity: 0.7;
  cursor: not-allowed;
}

.login-btn :deep(span) {
  position: relative;
  z-index: 1;
}

/* ========== 自定义复选框 ========== */
.custom-checkbox :deep(.el-checkbox__input) {
  --el-checkbox-checked-bg-color: #3b82f6;
  --el-checkbox-checked-input-border-color: #3b82f6;
  --el-checkbox-input-border-color-hover: #3b82f6;
}

.custom-checkbox :deep(.el-checkbox__inner) {
  width: 16px;
  height: 16px;
  border-radius: 4px;
  border-color: #d1d5db;
  transition: all 0.2s ease;
}

.custom-checkbox :deep(.el-checkbox__inner:hover) {
  border-color: #3b82f6;
}

.custom-checkbox :deep(.el-checkbox__input.is-checked .el-checkbox__inner) {
  background-color: #3b82f6;
  border-color: #3b82f6;
}

.custom-checkbox :deep(.el-checkbox__label) {
  padding-left: 6px;
}

/* ========== 响应式 ========== */
@media (max-width: 480px) {
  .login-card-wrapper {
    padding: 32px 24px 28px;
    border-radius: 16px;
  }
}
</style>
