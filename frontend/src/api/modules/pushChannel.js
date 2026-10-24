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

/**
 * 已注册的通道类型（是否消息类、可选载荷格式、预览/测试发送能力）。
 * @returns {Promise<Array>} ChannelTypeMeta 数组
 */
export function getPushChannelTypeList() {
  return request({
    url: '/pushChannel/listTypes',
    method: 'get',
  })
}

/**
 * 预览出站载荷（不发送、不落库）。
 * @param {Object} data - { type, configJson }
 * @returns {Promise<{payload?: string, payloads?: string[], count: number, error?: string}>}
 */
export function previewPushChannel(data) {
  return request({
    url: '/pushChannel/preview',
    method: 'post',
    data,
  })
}

/**
 * 只预览出站编码：不依赖通道类型与连接配置，也不发送任何请求。
 * @param {Object} data - { format, mapping }；format 省略时按 mapping-json
 * @returns {Promise<{payload?: string, payloads?: string[], count: number, error?: string}>}
 */
export function previewPushMapping(data) {
  return request({
    url: '/pushChannel/previewMapping',
    method: 'post',
    data,
  })
}

/**
 * 测试发送：会真的向配置的下游发送一条样例数据。
 * @param {Object} data - { type, configJson }
 * @returns {Promise<{success: boolean, statusCode?: number, messages: number, durationMs: number, error?: string}>}
 */
export function testSendPushChannel(data) {
  return request({
    url: '/pushChannel/testSend',
    method: 'post',
    data,
  })
}

/**
 * 获取推送通道运行状态。
 *
 * 与通道配置列表是两件事：这里返回的是运行中的通道实例（连接状态、
 * 队列深度、断网缓存积压、发布/丢弃计数），用于大盘「数据出口」面板。
 *
 * @returns {Promise<Array>} ChannelStatusVO 数组
 */
export function getPushStatus() {
  return request({
    url: '/push/status',
    method: 'get',
  })
}
