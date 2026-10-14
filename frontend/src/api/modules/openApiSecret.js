// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取密钥列表（无需分页）
 * @returns {Promise<Array>} 密钥列表
 */
export function getSecretList() {
  return request({
    url: '/openApiSecret/list',
    method: 'get',
  })
}

/**
 * 新增密钥（密钥内容由服务端随机生成）
 * @param {Object} data - 密钥数据
 * @param {string} data.name - 名称（必填）
 * @returns {Promise}
 */
export function addSecret(data) {
  return request({
    url: '/openApiSecret/create',
    method: 'post',
    data,
  })
}

/**
 * 修改密钥名称
 * @param {Object} data - 请求数据
 * @param {string} data.id - ID（必填）
 * @param {string} data.name - 新名称（必填）
 * @returns {Promise}
 */
export function updateSecretName(data) {
  return request({
    url: '/openApiSecret/updateName',
    method: 'post',
    data,
  })
}

/**
 * 删除密钥
 * @param {string} id - 密钥 ID
 * @returns {Promise}
 */
export function deleteSecret(id) {
  return request({
    url: `/openApiSecret/delete/${id}`,
    method: 'get',
  })
}

/**
 * 获取开放接口说明文档（服务端渲染好的 HTML 字符串）
 * @returns {Promise<string>} 文档 HTML
 */
export function getOpenApiDoc() {
  return request({
    url: '/openApiSecret/doc',
    method: 'get',
  })
}
