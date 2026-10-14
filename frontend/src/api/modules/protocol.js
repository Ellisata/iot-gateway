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
 * 修改协议（只能改描述与排序）
 *
 * 协议名不可修改：它同时是设备的解析目标与插件驱动的注册名，
 * 改名会让引用它的设备静默停采，后端会直接拒绝。
 *
 * @param {Object} data - 协议数据
 * @param {string} data.id - ID（必填）
 * @param {string} data.name - 名称（原样回传，用于后端校验它没被改动）
 * @param {string} data.description - 描述
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
 * 把协议表单恢复为插件声明的默认值
 *
 * 协议行由插件供给（见 pluginhost/provision.go），插件不会覆盖运维改过的表单；
 * 这个接口是运维主动采纳插件当前表单的唯一入口。
 *
 * @param {string} id - 协议 ID
 * @returns {Promise}
 */
export function resetProtocolForm(id) {
  return request({
    url: `/protocol/resetProtocolForm/${id}`,
    method: 'post',
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
