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
