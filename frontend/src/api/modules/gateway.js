// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 查询网关自身身份（单行，无需分页）
 * @returns {Promise<Object>} 身份对象，含 id / name / port / firmwareVersion / description / createdAt / updatedAt
 */
export function getGateway() {
  return request({
    url: '/gateway/get',
    method: 'get',
  })
}

/**
 * 编辑网关名称与描述
 * @param {Object} data - 请求体
 * @param {string} data.name - 网关名称（必填，1-100 字符）
 * @param {string} [data.description] - 描述（最长 500 字符）
 * @returns {Promise<void>}
 */
export function updateGateway(data) {
  return request({
    url: '/gateway/update',
    method: 'post',
    data,
  })
}
