// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 用户登录
 * @param {Object} params - 登录参数
 * @param {string} params.username - 用户名
 * @param {string} params.password - 密码
 * @returns {Promise<{accessToken: string}>}
 */
export function loginApi(data) {
  return request({
    url: '/user/login',
    method: 'post',
    data,
  })
}

/**
 * 修改当前用户密码
 * @param {Object} data - 密码参数（三个字段均需 RSA 加密后传入）
 * @param {string} data.oldPassword - 旧密码（RSA 密文）
 * @param {string} data.newPassword - 新密码（RSA 密文）
 * @param {string} data.confirmPassword - 确认新密码（RSA 密文）
 * @returns {Promise<null>}
 */
export function changePasswordApi(data) {
  return request({
    url: '/user/password',
    method: 'put',
    data,
  })
}
