// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 报警目标类型的取值，与后端 alarm.Type* 常量一一对应（见 alarm/tracker.go）。
 *
 * 导出而不是散在页面里：每个目标类型对应一个菜单页，四个页面的薄壳各自写死一个字面量，
 * 拼错不会报错、只会静默查到一份空列表 —— 校验用的清单必须是可导入的（组件里
 * defineProps 的实参会被提升到 setup() 之外，引用不到 <script setup> 内的局部 const）。
 */
export const ALARM_TARGET_TYPES = ['device', 'channel', 'point', 'point_threshold']

/**
 * 分页查询报警历史
 * @param {Object} params - 查询参数
 * @param {number} params.page - 页码（从 1 开始，必填）
 * @param {number} params.size - 每页条数（1-100，必填）
 * @param {string} [params.targetName] - 目标名称（模糊查询）
 * @param {string} [params.targetType] - 目标类型 device | channel | point | point_threshold，空查全部
 * @param {string} [params.alarmType] - 报警类型 offline | quality | threshold | recover
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
 * 分页查询点位越限记录
 *
 * 与 getAlarmPage 是两件事：报警是去抖后的状态机（连续 N 轮越限才报），
 * 本表按**原始越限**逐条留证（同一连续越限段内节流）——
 * 因此 1~2 轮的尖峰只在这里看得到。
 *
 * @param {Object} params - 查询参数
 * @param {number} params.page - 页码（从 1 开始，必填）
 * @param {number} params.size - 每页条数（1-100，必填）
 * @param {string} [params.deviceId] - 所属设备，空查全部
 * @param {string} [params.name] - 点位名称（模糊查询）
 * @returns {Promise<{page: number, size: number, total: number, records: Array}>} 分页数据
 */
export function getThresholdRecordPage(params = {}) {
  return request({
    url: '/alarm/pageThresholdRecord',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
      deviceId: params.deviceId || undefined,
      name: params.name || undefined,
    },
  })
}

/**
 * 设备维度可靠性统计（窗口内）
 *
 * 返回的是**包装体** `{ gatewayId, gatewayName, records }`，榜单在 `records` 里。
 * 外层两个字段是本网关自身身份：单网关部署下页面本来就知道自己在哪台网关上，
 * 所以它主要是给外部系统跨网关汇总归位用的 —— 但报表页拿它当标题是有意义的
 * （见 views/alarm/reliability），别当成无用字段删掉。
 *
 * @param {Object} params - 查询参数
 * @param {string} params.from - 起始日期 YYYY-MM-DD，含当天（必填）
 * @param {string} params.to - 结束日期 YYYY-MM-DD，含当天（必填）
 * @param {number} [params.limit] - 只取前 N 名（按可用率升序，1 = 最差那台）。
 *   **不传 = 不截断**，与 pointAbnormal 的默认值语义刻意相反（设备是有限维度）
 * @returns {Promise<{gatewayId: string, gatewayName: string, records: Array}>}
 */
export function getDeviceReliability(params = {}) {
  return request({
    url: '/alarm/deviceReliability',
    method: 'get',
    params: {
      from: params.from,
      to: params.to,
      limit: params.limit || undefined,
    },
  })
}

/**
 * 点位维度异常 top-N（窗口内）
 *
 * 返回 `{ gatewayId, gatewayName, records }`，与 getDeviceReliability 同形。
 * records 里的 qualityEpisodes 与 thresholdEpisodes **分开计数，不要在前端合并**
 * 成一个「非正常次数」—— 前者要找运维（地址/解析问题），后者可能只是工艺波动。
 *
 * @param {Object} params - 查询参数
 * @param {string} [params.deviceId] - 只统计该设备下的点位，空 = 全部设备
 * @param {string} params.from - 起始日期 YYYY-MM-DD，含当天（必填）
 * @param {string} params.to - 结束日期 YYYY-MM-DD，含当天（必填）
 * @param {number} [params.limit] - top-N 条数；不传时服务端兜底 20，上限 500
 * @returns {Promise<{gatewayId: string, gatewayName: string, records: Array}>}
 */
export function getPointAbnormal(params = {}) {
  return request({
    url: '/alarm/pointAbnormal',
    method: 'get',
    params: {
      deviceId: params.deviceId || undefined,
      from: params.from,
      to: params.to,
      limit: params.limit || undefined,
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
