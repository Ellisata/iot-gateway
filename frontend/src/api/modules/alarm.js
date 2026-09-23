// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 分页查询报警历史
 * @param {Object} params - 查询参数
 * @param {number} params.page - 页码（从 1 开始，必填）
 * @param {number} params.size - 每页条数（1-100，必填）
 * @param {string} [params.targetName] - 目标名称（模糊查询）
 * @param {string} [params.targetType] - 目标类型 device | channel，空查全部
 * @param {string} [params.alarmType] - 报警类型 offline | recover
 * @param {string} [params.status] - 报警状态 active | cleared
 * @returns {Promise<{page: number, size: number, total: number, records: Array}>} 分页数据
 */
export function getAlarmPage(params = {}) {
  return request({
    url: '/alarm/pageAlarm',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
      targetName: params.targetName || undefined,
      targetType: params.targetType || undefined,
      alarmType: params.alarmType || undefined,
      status: params.status || undefined,
    },
  })
}

/**
 * 查询活动报警（未恢复）总数 —— 首页指标磁贴专用
 *
 * 与 getAlarmPage 打同一端点，但只取 total：分页信封里的 total 是过滤后的全量计数、
 * 与 size 无关，因此 size=1 即可，避免首页每 30 秒白拉一页记录。
 *
 * @returns {Promise<number>} 活动报警数
 */
export function getActiveAlarmCount() {
  return request({
    url: '/alarm/pageAlarm',
    method: 'get',
    params: { page: 1, size: 1, status: 'active' },
    // 首页 30 秒轮询：报警服务抖动时不该每半分钟弹一次全局错误提示刷屏。
    // 与 realtime.js 同一取舍 —— 失败时磁贴保留上一轮数值，不回落成 0。
    silent: true,
  }).then((res) => {
    // silent 请求拦截器既不拆信封也不 reject（见 api/index.js），
    // 必须在这里手动还原契约，否则调用方拿到的是 {code,msg,data} 信封。
    if (res?.code !== '0') {
      throw new Error(res?.msg || 'failed to load active alarm count')
    }
    return res.data?.total ?? 0
  })
}
