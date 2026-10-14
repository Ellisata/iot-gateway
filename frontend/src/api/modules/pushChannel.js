// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取通道列表（分页）
 * @param {Object} params - 查询参数
 * @param {number} params.pageNum - 当前页码（从 1 开始）
 * @param {number} params.pageSize - 每页条数
 * @param {string} params.name - 按名称搜索（可选）
 * @returns {Promise<{records: Array, total: number}>} 返回分页数据
 */
export function getChannelList(params) {
  return request({
    url: '/pushChannel/pagePushChannel',
    method: 'get',
    params,
  })
}

/**
 * 新增通道
 * @param {Object} data - 通道数据
 * @param {string} data.name - 名称（必填）
 * @param {string} data.description - 描述
 * @param {string} data.formJson - 表单 JSON
 * @param {number} data.sort - 排序
 * @returns {Promise}
 */
export function addChannel(data) {
  return request({
    url: '/pushChannel/createPushChannel',
    method: 'post',
    data,
  })
}

/**
 * 修改通道
 * @param {Object} data - 通道数据
 * @param {string} data.id - ID（必填）
 * @param {string} data.name - 名称（必填）
 * @param {string} data.description - 描述
 * @param {string} data.formJson - 表单 JSON
 * @param {number} data.sort - 排序
 * @returns {Promise}
 */
export function updateChannel(data) {
  return request({
    url: '/pushChannel/updatePushChannel',
    method: 'post',
    data,
  })
}

/**
 * 删除通道
 * @param {string} id - 通道 ID
 * @returns {Promise}
 */
export function deleteChannel(id) {
  return request({
    url: `/pushChannel/deletePushChannel/${id}`,
    method: 'get',
  })
}

/**
 * 根据 ID 获取通道详情
 * @param {string|number} id - 通道 ID
 * @returns {Promise<Object>} 通道详情，含 formJson 字段
 */
export function getChannelById(id) {
  return request({
    url: `/pushChannel/getPushChannelById/${id}`,
    method: 'get',
  })
}

/**
 * 测试通道连接
 * @param {Object} data - 测试参数
 * @param {string} data.name - 通道名称（如 mqtt）
 * @param {Object} data.configJson - 连接配置 JSON（broker/port/username 等）
 * @returns {Promise<{connected: boolean}>}
 */
export function testConnectivity(data) {
  return request({
    url: '/pushChannel/testConnectivity',
    method: 'post',
    data,
  })
}

/**
 * 根据通道 ID 更新表单 JSON
 * @param {Object} data - 请求数据
 * @param {string} data.id - 通道 ID
 * @param {Object} data.formConfig - 表单完整配置
 * @param {Array}  data.formConfig.rule - 表单字段规则
 * @param {Object} data.formConfig.options - 表单全局选项
 * @returns {Promise}
 */
export function updateChannelFormJsonById(data) {
  return request({
    url: '/channel/updateChannelFormById',
    method: 'post',
    data,
  })
}
