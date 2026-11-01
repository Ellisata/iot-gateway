// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors
import request from '@/api'

/**
 * 查询设备是否支持控制下发。
 * @param {string} deviceId
 * @returns {Promise<{deviceId:string, deviceName:string, protocolName:string, enabled:boolean, writable:boolean, reason?:string}>}
 */
export function getControlCapability(deviceId) {
  return request.get('/control/capability', { params: { deviceId } })
}

/**
 * 执行控制下发。
 *
 * 超时单独放大到 30s：网关侧写超时是 15s，且写可能排在一次在途 Read 之后；
 * 用 axios 默认的 15s 会在网关还没返回时先断开，用户看到超时但设备可能已经动作。
 *
 * @param {{deviceId:string, requestId?:string, verify?:boolean,
 *          items:Array<{deviceAddressId:string, value:string}>}} data
 */
export function writeControl(data) {
  return request.post('/control/write', data, { timeout: 30000 })
}

/**
 * 控制下发审计分页查询。
 * @param {{page:number, size:number, deviceId?:string, addressId?:string,
 *          source?:string, operator?:string, requestId?:string, name?:string,
 *          code?:number, verified?:number, startTime?:string, endTime?:string}} params
 */
export function getControlLogPage(params) {
  return request.get('/control/pageControlLog', { params })
}
