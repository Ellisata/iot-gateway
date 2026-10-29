// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 分页查询 MQTT 订阅
 * @param {Object} params - { page, size, name?, status? }
 */
export function getSubscriptionList(params) {
  return request({
    url: '/mqttSubscription/pageMqttSubscription',
    method: 'get',
    params,
  })
}

/**
 * 分页查询订阅报文里出现过的设备与点位信息
 * @param {Object} params - { page, size, deviceId?, deviceName?, deviceAddressId? }
 */
export function getSubscriptionDevicePointList(params) {
  return request({
    url: '/mqttSubscription/pageMqttSubscriptionDevicePoint',
    method: 'get',
    params,
  })
}

/**
 * 手工新增订阅设备/点位
 * @param {Object} data - { deviceId, deviceAddressId, deviceName?, deviceAddressLabel?, kind? }
 */
export function createSubscriptionDevicePoint(data) {
  return request({
    url: '/mqttSubscription/createMqttSubscriptionDevicePoint',
    method: 'post',
    data,
  })
}

/**
 * 编辑订阅设备/点位元信息（自然键只用于定位）
 * @param {Object} data - { deviceId, deviceAddressId, deviceName?, deviceAddressLabel?, kind? }
 */
export function updateSubscriptionDevicePoint(data) {
  return request({
    url: '/mqttSubscription/updateMqttSubscriptionDevicePoint',
    method: 'post',
    data,
  })
}

/**
 * 删除手工维护的订阅设备/点位（自然键走查询参数）
 * @param {string} deviceId
 * @param {string} deviceAddressId
 */
export function deleteSubscriptionDevicePoint(deviceId, deviceAddressId) {
  return request({
    url: '/mqttSubscription/deleteMqttSubscriptionDevicePoint',
    method: 'get',
    params: { deviceId, deviceAddressId },
  })
}

/**
 * 把手工艺维护的订阅设备/点位交还给自动收录（元信息保留，待报文变化后同步）
 * @param {string} deviceId
 * @param {string} deviceAddressId
 */
export function restoreSubscriptionDevicePointAuto(deviceId, deviceAddressId) {
  return request({
    url: '/mqttSubscription/restoreMqttSubscriptionDevicePointAuto',
    method: 'post',
    data: { deviceId, deviceAddressId },
  })
}

/**
 * 批量删除手工维护的订阅设备/点位
 * @param {Array<{deviceId:string, deviceAddressId:string}>} items
 * @returns {Promise<{requested:number, deleted:number}>}
 */
export function batchDeleteSubscriptionDevicePoints(items) {
  return request({
    url: '/mqttSubscription/batchDeleteMqttSubscriptionDevicePoint',
    method: 'post',
    data: { items },
  })
}

/**
 * 批量导入订阅设备/点位（Excel .xlsx，按设备ID+点位ID upsert）
 * @param {File} file
 * @returns {Promise<{total:number, success:number, failed:number, errors:Array<{row:number,name:string,reason:string}>}>}
 */
export function importSubscriptionDevicePoints(file) {
  const formData = new FormData()
  formData.append('file', file)
  return request({
    url: '/mqttSubscription/importMqttSubscriptionDevicePoint',
    method: 'post',
    data: formData,
  })
}

/**
 * 下载订阅设备/点位导入模板（.xlsx）
 * @returns {Promise<Blob>}
 */
export function downloadSubscriptionDevicePointTemplate() {
  return request({
    url: '/mqttSubscription/importMqttSubscriptionDevicePointTemplate',
    method: 'get',
    responseType: 'blob',
  })
}

/**
 * 根据 ID 获取订阅详情
 * @param {string} id
 */
export function getSubscriptionById(id) {
  return request({
    url: `/mqttSubscription/getMqttSubscriptionById/${id}`,
    method: 'get',
  })
}

/**
 * 新增订阅
 * @param {Object} data
 */
export function addSubscription(data) {
  return request({
    url: '/mqttSubscription/createMqttSubscription',
    method: 'post',
    data,
  })
}

/**
 * 编辑订阅
 * @param {Object} data
 */
export function updateSubscription(data) {
  return request({
    url: '/mqttSubscription/updateMqttSubscription',
    method: 'post',
    data,
  })
}

/**
 * 删除订阅
 * @param {string} id
 */
export function deleteSubscription(id) {
  return request({
    url: `/mqttSubscription/deleteMqttSubscription/${id}`,
    method: 'get',
  })
}

/**
 * 测试 broker 连通性（不保存配置）
 * @param {Object} data - { broker, port, clientId, username, password, topic }
 */
export function testSubscriptionConnectivity(data) {
  return request({
    url: '/mqttSubscription/testConnectivity',
    method: 'post',
    data,
  })
}

/**
 * 用未保存的映射规则预览样例报文
 * @param {Object} data - { mappingJson, sampleJson }
 */
export function previewSubscriptionMapping(data) {
  return request({
    url: '/mqttSubscription/preview',
    method: 'post',
    data,
  })
}

/**
 * 查询所有活跃订阅的运行状态
 */
export function getSubscriptionStatus() {
  return request({
    url: '/mqttSubscription/status',
    method: 'get',
    // 5 秒轮询：失败不该反复弹全局错误提示。
    silent: true,
  }).then((res) => {
    // 响应拦截器对 silent 请求**不拆信封**，直接返回 {code,msg,isSuccess,data}，
    // 且业务错误也只 resolve、不 reject（见 api/index.js）。必须在这里还原契约，
    // 否则调用方拿到的是信封对象，把它当数组遍历会抛错并被页面的 catch 吞掉 ——
    // 表现为状态一直为空、列表永远显示「未连接」。
    if (res?.code !== '0') {
      throw new Error(res?.msg || 'failed to load mqtt subscription status')
    }
    return res.data
  })
}

/**
 * 查询某个订阅最近收到的报文（新→旧，仅内存）
 * @param {{ id: string, limit?: number }} params
 */
export function getSubscriptionRecentMessages(params) {
  return request({
    url: '/mqttSubscription/recentMessages',
    method: 'get',
    params,
    // 抽屉打开时 2 秒轮询：失败不弹全局错误，保留上一轮数据。
    silent: true,
  }).then((res) => {
    // 与状态接口同一套 silent 信封还原规则（见上）。
    if (res?.code !== '0') {
      throw new Error(res?.msg || 'failed to load recent mqtt subscription messages')
    }
    return res.data
  })
}
