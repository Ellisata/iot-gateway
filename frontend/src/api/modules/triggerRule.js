// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 事件触发规则分页
 * @param {{page:number,size:number,name?:string,status?:number}} params
 */
export function pageTriggerRules(params = {}) {
  return request({
    url: '/triggerRule/pageTriggerRule',
    method: 'get',
    params,
  })
}

/** 规则详情（含引用点位与 Kind 回显） */
export function getTriggerRuleById(id) {
  return request({
    url: `/triggerRule/getTriggerRuleById/${id}`,
    method: 'get',
  })
}

/** 创建规则 */
export function createTriggerRule(data) {
  return request({
    url: '/triggerRule/createTriggerRule',
    method: 'post',
    data,
  })
}

/** 更新规则（整份覆盖） */
export function updateTriggerRule(data) {
  return request({
    url: '/triggerRule/updateTriggerRule',
    method: 'post',
    data,
  })
}

/** 删除规则（历史记录保留） */
export function deleteTriggerRule(id) {
  return request({
    url: `/triggerRule/deleteTriggerRule/${id}`,
    method: 'get',
  })
}

/**
 * 最近触发（仅内存，新→旧，最多 100 条）
 * @param {{ruleId?:string,limit?:number}} params
 */
export function getRecentTriggers(params = {}) {
  return request({
    url: '/triggerRule/recentTriggers',
    method: 'get',
    params,
  })
}

/**
 * 触发历史分页
 * @param {{page:number,size:number,ruleId?:string,ruleName?:string,status?:string,startTime?:string,endTime?:string}} params
 */
export function pageTriggerRuleRecords(params = {}) {
  return request({
    url: '/triggerRule/pageTriggerRuleRecord',
    method: 'get',
    params,
  })
}
