// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取支持的 Webhook 类型及元信息
 *
 * 类型集合由后端 notify 包的注册表决定（不硬编码在前端），
 * 返回 label / 可选报文格式 / 是否支持加签与 @，供选择器与表单渲染。
 *
 * @returns {Promise<Array<{type: string, label: string, msgTypes: string[],
 *   defaultMsgType: string, supportsSign: boolean, supportsAt: boolean,
 *   urlPlaceholder: string}>>}
 */
export function getWebhookTypeList() {
  return request({
    url: '/alarmWebhook/listTypes',
    method: 'get',
  })
}

/**
 * 分页查询报警 Webhook 配置
 * @param {Object} params - 查询参数
 * @param {number} params.page - 页码（从 1 开始，必填）
 * @param {number} params.size - 每页条数（1-100，必填）
 * @param {string} [params.name] - 名称（模糊查询）
 * @param {string} [params.type] - 类型 dingtalk | wecom | feishu | custom
 * @param {number} [params.status] - 1 启用 0 停用
 * @returns {Promise<{page: number, size: number, total: number, records: Array}>}
 */
export function getWebhookList(params) {
  return request({
    url: '/alarmWebhook/pageAlarmWebhook',
    method: 'get',
    params,
  })
}

/**
 * 根据 ID 查询报警 Webhook 详情
 * 注意：secret 字段恒为空串（后端不回显密钥），是否已配置看 hasSecret
 * @param {string} id - Webhook ID
 * @returns {Promise<Object>}
 */
export function getWebhookById(id) {
  return request({
    url: `/alarmWebhook/getAlarmWebhookById/${id}`,
    method: 'get',
  })
}

/**
 * 新增报警 Webhook
 * @param {Object} data - 配置数据
 * @param {string} data.name - 名称（必填）
 * @param {string} data.type - 类型（必填）
 * @param {string} data.url - Webhook 地址（必填）
 * @param {string} [data.secret] - 加签密钥
 * @param {string} [data.msgType] - 报文格式
 * @param {boolean} [data.atAll] - 是否 @所有人
 * @param {string} [data.atList] - @ 列表（逗号分隔）
 * @param {string} [data.description] - 描述
 * @returns {Promise}
 */
export function addWebhook(data) {
  return request({
    url: '/alarmWebhook/createAlarmWebhook',
    method: 'post',
    data,
  })
}

/**
 * 修改报警 Webhook
 *
 * secret 留空（不传该字段）表示保持原密钥不变；后端用指针区分「不修改」与「清空」。
 *
 * @param {Object} data - 配置数据（含 id）
 * @returns {Promise}
 */
export function updateWebhook(data) {
  return request({
    url: '/alarmWebhook/updateAlarmWebhook',
    method: 'post',
    data,
  })
}

/**
 * 删除报警 Webhook
 * @param {string} id - Webhook ID
 * @returns {Promise}
 */
export function deleteWebhook(id) {
  return request({
    url: `/alarmWebhook/deleteAlarmWebhook/${id}`,
    method: 'get',
  })
}

/**
 * 测试发送一条报警消息（同步单次，失败返回平台原始错误）
 *
 * id 非空时以库中配置为基准、请求字段覆盖之（支持「改完先测再保存」）；
 * 未保存的配置则完全使用传入字段。
 *
 * @param {Object} data - 测试参数
 * @param {string} [data.id] - 已保存配置的 ID
 * @param {string} [data.type] - 类型
 * @param {string} [data.url] - 地址
 * @param {string} [data.secret] - 密钥（留空时沿用库中已存的）
 * @param {string} [data.msgType] - 报文格式
 * @param {boolean} [data.atAll] - 是否 @所有人
 * @param {string} [data.atList] - @ 列表
 * @param {string} [data.alarmType] - offline（默认）| recover，用于预览两种文案
 * @param {string} [data.targetType] - device（默认）| channel
 * @param {string} [data.targetName] - 目标名称，默认「测试设备」
 * @returns {Promise}
 */
export function testSendWebhook(data) {
  return request({
    url: '/alarmWebhook/testSend',
    method: 'post',
    data,
  })
}

/**
 * 查询通知器运行状态（各 Webhook 的队列积压与投递计数）
 * @returns {Promise<{running: boolean, ingressDepth: number, ingressDropped: number,
 *   webhooks: Array}>}
 */
export function getWebhookStatus() {
  return request({
    url: '/alarmWebhook/status',
    method: 'get',
  })
}

/**
 * 手动触发通知器热刷新（改完配置立即生效，无需等 10s 巡检）
 * @returns {Promise}
 */
export function refreshWebhook() {
  return request({
    url: '/alarmWebhook/refresh',
    method: 'post',
  })
}

/**
 * 根据类型名获取该类型的配置表单
 * @param {string} name - 类型名（dingtalk / wecom / feishu / custom）
 * @returns {Promise<Object>} 表单配置，含 formJson
 */
export function getWebhookFormByName(name) {
  return request({
    url: '/alarmWebhookForm/getAlarmWebhookFormByName',
    method: 'get',
    params: { name },
  })
}

/**
 * 新增 / 保存表单配置（表单设计页用）
 * @param {Object} data - 表单数据
 * @param {string} data.name - 类型名（必填）
 * @param {Object} data.formJson - 表单完整配置
 * @returns {Promise}
 */
export function createWebhookForm(data) {
  return request({
    url: '/alarmWebhookForm/createAlarmWebhookForm',
    method: 'post',
    data,
  })
}
