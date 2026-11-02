// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

import request from '@/api'

/**
 * 物模型分页列表
 * @param {Object} params
 * @param {number} params.page
 * @param {number} params.size
 * @param {string} [params.keyword] 同时模糊匹配 code 与 name
 * @param {string} [params.category] sensor/meter/actuator/robot/plc/other
 * @returns {Promise<{page:number,size:number,total:number,records:Array}>}
 */
export function getThingModelList(params = {}) {
  return request({
    url: '/thingModel/pageThingModel',
    method: 'get',
    params: {
      page: params.page,
      size: params.size,
      keyword: params.keyword || undefined,
      category: params.category || undefined,
    },
  })
}

/**
 * 全部物模型（设备绑定下拉用）
 * @returns {Promise<Array<{id:string,code:string,name:string,status:number}>>}
 */
export function getThingModels() {
  return request({
    url: '/thingModel/listThingModels',
    method: 'get',
  })
}

/**
 * 新增物模型
 * @param {Object} data
 * @param {string} data.code 模型标识（字母开头，创建后不可改）
 * @param {string} data.name 显示名
 * @param {string} [data.category] 分类 code
 * @param {string} [data.version] 版本号，默认 1.0.0
 * @param {string} [data.description]
 */
export function createThingModel(data) {
  return request({
    url: '/thingModel/createThingModel',
    method: 'post',
    data,
  })
}

/**
 * 修改物模型（code 创建后不可改）
 * @param {Object} data
 * @param {string} data.id
 * @param {string} [data.code] 省略或与原值一致
 * @param {string} data.name
 * @param {string} [data.category]
 * @param {string} [data.version]
 * @param {string} [data.description]
 * @param {number} [data.status] 0/1，省略 = 不改
 */
export function updateThingModel(data) {
  return request({
    url: '/thingModel/updateThingModel',
    method: 'post',
    data,
  })
}

/**
 * 删除物模型（被设备绑定或属性被点位绑定时会被拒绝）
 * @param {string} id
 */
export function deleteThingModel(id) {
  return request({
    url: `/thingModel/deleteThingModel/${id}`,
    method: 'get',
  })
}

/**
 * 根据 ID 查询物模型
 * @param {string} id
 */
export function getThingModelById(id) {
  return request({
    url: `/thingModel/getThingModelById/${id}`,
    method: 'get',
  })
}

/**
 * 列出某模型下的全部属性
 * @param {string} modelId
 * @returns {Promise<Array>}
 */
export function listThingModelProperties(modelId) {
  return request({
    url: '/thingModel/listThingModelProperties',
    method: 'get',
    params: { modelId },
  })
}

/**
 * 某模型下的属性分页
 * @param {string} modelId
 * @param {Object} params
 * @param {number} params.page
 * @param {number} params.size
 * @returns {Promise<{page:number,size:number,total:number,records:Array,maxSortOrder:number}>}
 */
export function pageThingModelProperties(modelId, params = {}) {
  return request({
    url: '/thingModel/pageThingModelProperties',
    method: 'get',
    params: {
      modelId,
      page: params.page,
      size: params.size,
    },
  })
}

/**
 * 新增物模型属性
 * @param {Object} data
 * @param {string} data.modelId
 * @param {string} data.identifier 属性标识符（字母开头，模型内唯一，创建后不可改）
 * @param {string} data.name
 * @param {string} data.kind bool/int/uint/float/string/time
 * @param {string} data.accessMode R/W/RW
 * @param {string} [data.unit]
 * @param {string} [data.description]
 * @param {number} [data.sortOrder]
 */
export function createThingModelProperty(data) {
  return request({
    url: '/thingModel/createThingModelProperty',
    method: 'post',
    data,
  })
}

/**
 * 修改物模型属性（identifier 创建后不可改）
 * @param {Object} data
 * @param {string} data.id
 * @param {string} [data.identifier] 省略或与原值一致
 * @param {string} data.name
 * @param {string} data.kind
 * @param {string} data.accessMode
 * @param {string} [data.unit]
 * @param {string} [data.description]
 * @param {number} [data.sortOrder]
 * @param {number} [data.status] 0/1，省略 = 不改
 */
export function updateThingModelProperty(data) {
  return request({
    url: '/thingModel/updateThingModelProperty',
    method: 'post',
    data,
  })
}

/**
 * 删除物模型属性（被点位绑定时会被拒绝）
 * @param {string} id
 */
export function deleteThingModelProperty(id) {
  return request({
    url: `/thingModel/deleteThingModelProperty/${id}`,
    method: 'get',
  })
}
