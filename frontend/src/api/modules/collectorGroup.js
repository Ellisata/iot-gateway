// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 采集组分页查询（设备维度）
 * @param {{deviceId:string, page:number, size:number, name?:string}} params
 * @returns {Promise<{records:Array,total:number}>}
 */
export function pageCollectorGroups(params = {}) {
  return request({
    url: '/collectorGroup/pageCollectorGroup',
    method: 'get',
    params,
  })
}

/**
 * 采集组下拉列表。不传 deviceId 时返回全部设备的组，
 * 供推送通道跨设备订阅选择。
 * @param {string} [deviceId]
 * @returns {Promise<Array>}
 */
export function listCollectorGroups(deviceId) {
  return request({
    url: '/collectorGroup/listCollectorGroups',
    method: 'get',
    params: { deviceId: deviceId || undefined },
  })
}

/**
 * 创建采集组
 * @param {{deviceId:string,name:string,intervalMs:number,description?:string}} data
 */
export function createCollectorGroup(data) {
  return request({
    url: '/collectorGroup/createCollectorGroup',
    method: 'post',
    data,
  })
}

/**
 * 更新采集组
 * @param {{id:string,name?:string,intervalMs?:number,description?:string,status?:number}} data
 */
export function updateCollectorGroup(data) {
  return request({
    url: '/collectorGroup/updateCollectorGroup',
    method: 'post',
    data,
  })
}

/**
 * 删除采集组（仍被点位绑定时服务端拒绝）
 * @param {string} id
 */
export function deleteCollectorGroup(id) {
  return request({
    url: `/collectorGroup/deleteCollectorGroup/${id}`,
    method: 'get',
  })
}

/**
 * 批量把点位分配/解绑到采集组
 * @param {{deviceId:string, ids:string[], groupId:string}} data groupId 为空 = 解绑
 * @returns {Promise<{requested:number,assigned:number}>}
 */
export function batchAssignCollectorGroup(data) {
  return request({
    url: '/deviceAddress/batchAssignCollectorGroup',
    method: 'post',
    data,
  })
}
