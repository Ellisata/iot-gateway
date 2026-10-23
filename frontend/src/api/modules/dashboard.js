// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取大盘遥测趋势（在线率与采集吞吐）。
 *
 * 后端每分钟落一个内存采样点，最长保留 24 小时；网关重启后从空开始，
 * 因此 samples 的数量就是「这段窗口积累了多少历史」，不要补零。
 * 窗口按墙钟裁（hours 小时），休眠断开留下的空档不会被旧点填满；
 * 本次运行一共积累了多少分钟看 startedAt（环形窗口最早的采样点）。
 *
 * @param {number} hours - 回看小时数，1..24，默认 6
 * @returns {Promise<{intervalSec: number, startedAt: string, samples: Array}>}
 */
export function getDashboardTrend(hours = 6) {
  return request({
    url: '/dashboard/trend',
    method: 'get',
    params: { hours },
  })
}
