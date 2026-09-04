// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 根据通道名称获取通道表单配置
 * @param {string} name - 通道名称（如 MQTT / NETTY / OPCUA）
 * @returns {Promise<Object>} 通道表单数据
 */
export function getPushChannelFormByName(name) {
  return request({
    url: '/pushChannelForm/getPushChannelFormByName',
    method: 'get',
    params: { name },
  })
}

/**
 * 新增 / 保存通道表单配置
 * @param {Object} data - 表单数据
 * @param {string} data.name - 通道名称（必填）
 * @param {Object} data.formJson - 表单完整配置
 * @param {Array}  data.formJson.rule - 表单字段规则
 * @param {Object} data.formJson.options - 表单全局选项
 * @returns {Promise}
 */
export function createPushChannelForm(data) {
  return request({
    url: '/pushChannelForm/createPushChannelForm',
    method: 'post',
    data,
  })
}
