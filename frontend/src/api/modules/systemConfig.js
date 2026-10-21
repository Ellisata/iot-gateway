// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 查询全部运行期开关及当前状态
 * @returns {Promise<Object>} { groups: [{ key, labelKey, fields: [...] }] }
 */
export function listSystemConfig() {
  return request({
    url: '/systemConfig/list',
    method: 'get',
  })
}

/**
 * 修改一个运行期开关
 * @param {Object} data - 请求体
 * @param {string} data.key - 开关标识（取自列表）
 * @param {boolean} data.value - 新值
 * @returns {Promise<Object>} { key, value, requiresRestart }
 */
export function updateSystemConfig(data) {
  return request({
    url: '/systemConfig/update',
    method: 'post',
    data,
  })
}

/**
 * 删除覆盖值，恢复 default.yaml 的默认值
 * @param {Object} data - 请求体
 * @param {string} data.key - 开关标识（取自列表）
 * @returns {Promise<Object>} { key, value, requiresRestart }
 */
export function resetSystemConfig(data) {
  return request({
    url: '/systemConfig/reset',
    method: 'post',
    data,
  })
}
