<!--
// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
-->

<template>
  <el-dialog
    v-model="visible"
    :title="t('changePassword.title')"
    width="420px"
    :close-on-click-modal="false"
    @closed="resetForm"
  >
    <el-form
      ref="formRef"
      :model="form"
      :rules="rules"
      label-position="top"
      @submit.prevent="handleSubmit"
    >
      <el-form-item :label="t('changePassword.oldPassword')" prop="oldPassword">
        <el-input
          v-model="form.oldPassword"
          type="password"
          :placeholder="t('changePassword.oldPasswordPlaceholder')"
          :prefix-icon="Lock"
          show-password
          clearable
        />
      </el-form-item>

      <el-form-item :label="t('changePassword.newPassword')" prop="newPassword">
        <el-input
          v-model="form.newPassword"
          type="password"
          :placeholder="t('changePassword.newPasswordPlaceholder')"
          :prefix-icon="Lock"
          show-password
          clearable
        />
      </el-form-item>

      <el-form-item :label="t('changePassword.confirmPassword')" prop="confirmPassword">
        <el-input
          v-model="form.confirmPassword"
          type="password"
          :placeholder="t('changePassword.confirmPasswordPlaceholder')"
          :prefix-icon="Lock"
          show-password
          clearable
          @keyup.enter="handleSubmit"
        />
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="loading" @click="handleSubmit">
        {{ t('common.confirm') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { computed, nextTick, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { Lock } from '@element-plus/icons-vue'
import { changePasswordApi } from '@/api/modules/user'
import { rsaEncrypt } from '@/utils/rsa'

const props = defineProps({
  modelValue: {
    type: Boolean,
    default: false,
  },
})

const emit = defineEmits(['update:modelValue', 'success'])

const { t } = useI18n()
const formRef = ref(null)
const loading = ref(false)

// visible 双向绑定：由父组件控制显隐
const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const form = reactive({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})

// 校验文案随语言切换实时更新
const rules = computed(() => ({
  oldPassword: [
    { required: true, message: t('changePassword.oldRequired'), trigger: 'blur' },
  ],
  newPassword: [
    { required: true, message: t('changePassword.newRequired'), trigger: 'blur' },
    { min: 6, max: 64, message: t('changePassword.newMinLength'), trigger: 'blur' },
  ],
  confirmPassword: [
    { required: true, message: t('changePassword.confirmRequired'), trigger: 'blur' },
    {
      validator: (_, value, callback) => {
        if (value && value !== form.newPassword) {
          callback(new Error(t('changePassword.confirmMismatch')))
        } else {
          callback()
        }
      },
      trigger: 'blur',
    },
  ],
}))

/** 关闭后重置表单与校验状态 */
function resetForm() {
  form.oldPassword = ''
  form.newPassword = ''
  form.confirmPassword = ''
  formRef.value?.clearValidate()
}

/** 提交修改密码 */
async function handleSubmit() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  loading.value = true
  try {
    // 三个密码字段分别 RSA 加密后传输（同登录）
    const [oldPassword, newPassword, confirmPassword] = await Promise.all([
      rsaEncrypt(form.oldPassword),
      rsaEncrypt(form.newPassword),
      rsaEncrypt(form.confirmPassword),
    ])

    await changePasswordApi({ oldPassword, newPassword, confirmPassword })

    ElMessage.success(t('changePassword.success'))
    emit('success')
    visible.value = false
  } catch {
    // 业务错误提示已由 axios 拦截器统一弹出，此处无需重复提示
  } finally {
    loading.value = false
  }
}
</script>
