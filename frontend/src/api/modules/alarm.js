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
