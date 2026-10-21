// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 获取大盘遥测趋势（在线率与采集吞吐）。
 *
 * 后端每分钟落一个内存采样点，最长保留 24 小时；网关重启后从空开始，
 * 因此 samples 的数量本身就是「本次运行积累了多少历史」，不要补零。
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
