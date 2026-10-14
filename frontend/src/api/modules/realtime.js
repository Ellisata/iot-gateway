// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取设备实时数据快照（分页）
 *
 * 一次请求同时返回设备状态（在线/接入采集/采集引擎运行中/最后成功采集时间）
 * 与本页点位的最新值，供页面按秒级轮询 —— 页面按秒轮询，拆成两个请求会成倍放大开销。
 *
 * @param {Object} params - 查询参数
 * @param {string} params.deviceObjectId - 设备对象 ID
 * @param {number} params.page - 页码（从 1 开始）
 * @param {number} params.size - 每页条数
 * @param {string} [params.name] - 按点位名或说明模糊搜索（可选）
 * @returns {Promise<{deviceId:string, deviceName:string, protocolName:string,
 *   online:boolean, collecting:boolean, collectorRunning:boolean,
 *   lastSuccessTime:string, serverTime:string,
 *   page:number, size:number, total:number, records:Array}>}
 */
export function getRealtimeSnapshot(params = {}) {
  return request({
    url: '/realtime/snapshot',
    method: 'get',
    params: {
      deviceId: params.deviceObjectId,
      page: params.page,
      size: params.size,
      name: params.name || undefined,
    },
    // 轮询失败（设备掉线、采集未启动）由页面自身用状态条与提示语呈现。
    // 不静默的话，每次轮询失败都会弹一个全局错误提示，秒级刷新下会刷满屏幕。
    silent: true,
  }).then((res) => {
    // 必须在这里拆信封：响应拦截器对 silent 请求**不拆**，直接返回 {code,msg,isSuccess,data}，
    // 且业务错误也不 reject、只 resolve 一个 code!=0 的信封（见 api/index.js）。
    // 不处理的话调用方会把信封当数据用 —— 字段全是 undefined，页面据此误报「采集引擎未运行」。
    // 在这里统一还原成与其它模块一致的契约：成功给 data，失败走 reject。
    if (res?.code !== '0') {
      throw new Error(res?.msg || 'failed to load realtime snapshot')
    }
    return res.data
  })
}
