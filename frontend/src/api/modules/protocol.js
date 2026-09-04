// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取协议列表（分页）
 * @param {Object} params - 查询参数
 * @param {number} params.pageNum - 当前页码（从 1 开始）
 * @param {number} params.pageSize - 每页条数
 * @param {string} params.name - 按名称搜索（可选）
 * @returns {Promise<{records: Array, total: number}>} 返回分页数据
 */
export function getProtocolList(params) {
  return request({
    url: '/protocol/pageProtocol',
    method: 'get',
    params,
  })
}

/**
 * 新增协议
 * @param {Object} data - 协议数据
 * @param {string} data.name - 名称（必填）
 * @param {string} data.description - 描述
 * @param {string} data.formJson - 表单 JSON
 * @param {number} data.sort - 排序
 * @returns {Promise}
 */
export function addProtocol(data) {
  return request({
    url: '/protocol/createProtocol',
    method: 'post',
    data,
  })
}

/**
 * 修改协议
 * @param {Object} data - 协议数据
 * @param {string} data.id - ID（必填）
 * @param {string} data.name - 名称（必填）
 * @param {string} data.description - 描述
 * @param {string} data.formJson - 表单 JSON
 * @param {number} data.sort - 排序
 * @returns {Promise}
 */
export function updateProtocol(data) {
  return request({
    url: '/protocol/updateProtocol',
    method: 'post',
    data,
  })
}

/**
 * 删除协议
 * @param {string} id - 协议 ID
 * @returns {Promise}
 */
export function deleteProtocol(id) {
  return request({
    url: `/protocol/deleteProtocol/${id}`,
    method: 'get',
  })
}

/**
 * 根据 ID 获取协议详情
 * @param {string|number} id - 协议 ID
 * @returns {Promise<Object>} 协议详情，含 formJson 字段
 */
export function getProtocolById(id) {
  return request({
    url: `/protocol/getProtocolById/${id}`,
    method: 'get',
  })
}

/**
 * 根据协议 ID 更新表单 JSON
 * @param {Object} data - 请求数据
 * @param {string} data.id - 协议 ID
 * @param {Object} data.formConfig - 表单完整配置
 * @param {Array}  data.formConfig.rule - 表单字段规则
 * @param {Object} data.formConfig.options - 表单全局选项
 * @returns {Promise}
 */
export function updateProtocolFormJsonById(data) {
  return request({
    url: '/protocol/updateProtocolFormById',
    method: 'post',
    data,
  })
}

/**
 * 根据协议名称获取该协议支持的数据类型
 * @param {string} protocolName - 协议名称（如 Omron.Net.CIP）
 * @returns {Promise<Array|Object>} 数据类型列表
 */
export function getDataTypes(protocolName) {
  return request({
    url: '/protocol/getDataTypes',
    method: 'get',
    params: { protocolName },
  })
}
